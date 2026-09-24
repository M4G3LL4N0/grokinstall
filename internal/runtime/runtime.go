// Package runtime is the universal capability execution protocol.
//
// Every installed capability, whatever its strategy, is invoked the same way:
//
//	{"query": "foo"}  ->  {"ok": true, "result": {}, "artifacts": [], "warnings": []}
//
// The runtime owns safety: it builds argv directly (never a shell string),
// controls cwd, environment, stdin, timeout and output bounds, and turns every
// failure mode into a structured response instead of a crash or a hang.
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// ExecutionType selects how a capability runs.
type ExecutionType string

// Execution types.
const (
	// TypeSubprocess runs an external command with controlled argv.
	TypeSubprocess ExecutionType = "subprocess"
	// TypeBuiltin runs a handler implemented inside GrokInstall.
	TypeBuiltin ExecutionType = "builtin"
	// TypeNone means the capability is not executable (plan-only, no-install).
	TypeNone ExecutionType = "none"
)

// InputMode selects how caller input reaches a subprocess.
type InputMode string

// Input modes.
const (
	// ModeStdinJSON writes the input object as JSON on stdin.
	ModeStdinJSON InputMode = "stdin_json"
	// ModeArgv converts input fields into command-line arguments.
	ModeArgv InputMode = "argv"
	// ModeNone passes no input.
	ModeNone InputMode = "none"
)

// Error codes returned in Response.Error.Code.
const (
	CodeInvalidInput    = "invalid_input"
	CodeNotExecutable   = "not_executable"
	CodeSpawnFailed     = "spawn_failed"
	CodeTimeout         = "timeout"
	CodeOutputTooLarge  = "output_too_large"
	CodeNonZeroExit     = "nonzero_exit"
	CodeMalformedOutput = "malformed_output"
	CodeHandlerFailed   = "handler_failed"
)

// Defaults for safety bounds.
const (
	DefaultTimeout        = 30 * time.Second
	DefaultMaxOutputBytes = 1 << 20 // 1 MiB
)

// Error is a structured capability failure.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// Response is the universal capability result.
type Response struct {
	OK          bool     `json:"ok"`
	Result      any      `json:"result,omitempty"`
	Artifacts   []string `json:"artifacts,omitempty"`
	Warnings    []string `json:"warnings,omitempty"`
	Error       *Error   `json:"error,omitempty"`
	DurationMs  int64    `json:"duration_ms"`
	InputBytes  int      `json:"input_bytes,omitempty"`
	OutputBytes int      `json:"output_bytes,omitempty"`
}

func failure(code, message string, started time.Time, in int) *Response {
	return &Response{
		OK:         false,
		Error:      &Error{Code: code, Message: message},
		DurationMs: time.Since(started).Milliseconds(),
		InputBytes: in,
	}
}

// Spec describes one capability invocation.
type Spec struct {
	Type           ExecutionType
	Command        string
	Args           []string
	ArgvMap        map[string]string
	InputMode      InputMode
	WorkingDir     string
	Env            map[string]string
	Timeout        time.Duration
	MaxOutputBytes int
	// Handler names a builtin handler for TypeBuiltin.
	Handler string
	// HandlerFunc, when set, is used instead of a registered handler. It lets a
	// caller supply a closure bound to this invocation without sharing state.
	HandlerFunc Handler
	// HandlerInput lets callers pass a pre-built payload to a builtin.
	HandlerInput any
	// ExpectJSON declares that the upstream produces a JSON object. When set,
	// non-JSON output is a malformed-output failure rather than plain text.
	ExpectJSON bool
}

// Handler is a capability implemented inside GrokInstall.
type Handler func(ctx context.Context, input json.RawMessage) (any, error)

// Runtime invokes capabilities.
type Runtime struct {
	handlers map[string]Handler
}

// New returns a runtime with no builtin handlers registered.
func New() *Runtime { return &Runtime{handlers: map[string]Handler{}} }

// Register adds a builtin handler.
func (r *Runtime) Register(name string, h Handler) {
	r.handlers[name] = h
}

func contextTODO() context.Context { return context.Background() }

// Invoke runs a capability and always returns a structured response. The
// error return is reserved for programming errors, never for capability
// failures, which are reported in Response.Error.
func (r *Runtime) Invoke(ctx context.Context, spec Spec, input json.RawMessage) (*Response, error) {
	started := time.Now()

	if len(input) > 0 && !json.Valid(input) {
		return failure(CodeInvalidInput, "input is not valid JSON", started, len(input)), nil
	}

	switch spec.Type {
	case TypeSubprocess:
		return r.invokeSubprocess(ctx, spec, input, started)
	case TypeBuiltin:
		return r.invokeBuiltin(ctx, spec, input, started)
	case TypeNone, "":
		return failure(CodeNotExecutable,
			"this capability is not executable; it is recorded as a plan only", started, len(input)), nil
	default:
		return failure(CodeNotExecutable,
			fmt.Sprintf("unknown execution type %q", spec.Type), started, len(input)), nil
	}
}

func (r *Runtime) invokeSubprocess(ctx context.Context, spec Spec, input json.RawMessage, started time.Time) (*Response, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return failure(CodeNotExecutable, "no command is registered for this capability", started, len(input)), nil
	}

	args, err := r.buildArgs(spec, input)
	if err != nil {
		return failure(CodeInvalidInput, err.Error(), started, len(input)), nil
	}

	timeout := spec.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxOut := spec.MaxOutputBytes
	if maxOut <= 0 {
		maxOut = DefaultMaxOutputBytes
	}

	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Direct argv execution: no shell is involved at any point.
	cmd := exec.CommandContext(runCtx, spec.Command, args...)
	cmd.Dir = spec.WorkingDir
	cmd.Env = buildEnv(spec.Env)
	cmd.WaitDelay = 500 * time.Millisecond

	switch spec.InputMode {
	case ModeStdinJSON:
		if len(input) == 0 {
			input = json.RawMessage(`{}`)
		}
		cmd.Stdin = bytes.NewReader(input)
	}

	var stdout, stderr bytes.Buffer
	stdoutW := &boundedWriter{w: &stdout, limit: maxOut}
	stderrW := &boundedWriter{w: &stderr, limit: maxOut}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	runErr := cmd.Run()
	if stdoutW.exceeded || stderrW.exceeded {
		return failure(CodeOutputTooLarge,
			fmt.Sprintf("capability output exceeded the %d byte bound", maxOut), started, len(input)), nil
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		return failure(CodeTimeout, fmt.Sprintf("capability timed out after %s", timeout), started, len(input)), nil
	}
	if runErr != nil {
		if errors.Is(runCtx.Err(), context.Canceled) {
			return failure(CodeTimeout, "capability was cancelled", started, len(input)), nil
		}
		var exitErr *exec.ExitError
		msg := strings.TrimSpace(stderr.String())
		if errors.As(runErr, &exitErr) {
			detail := fmt.Sprintf("capability exited with status %d", exitErr.ExitCode())
			if msg != "" {
				detail += ": " + truncate(msg, 500)
			}
			return failure(CodeNonZeroExit, detail, started, len(input)), nil
		}
		if msg == "" {
			msg = runErr.Error()
		}
		return failure(CodeSpawnFailed, "could not start capability: "+msg, started, len(input)), nil
	}
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		result, perr := r.parseResult(spec, stdout.Bytes())
		if perr != nil {
			resp := failure(perr.Code, perr.Message, started, len(input))
			resp.Error.Details = perr.Details
			return resp, nil
		}
		resp := success(started, len(input), result)
		resp.Warnings = append(resp.Warnings, "stderr: "+truncate(msg, 500))
		resp.OutputBytes = len(stdout.Bytes())
		return resp, nil
	}
	result, perr := r.parseResult(spec, stdout.Bytes())
	if perr != nil {
		resp := failure(perr.Code, perr.Message, started, len(input))
		resp.Error.Details = perr.Details
		return resp, nil
	}
	resp := success(started, len(input), result)
	resp.OutputBytes = len(stdout.Bytes())
	return resp, nil
}

// parseResult prefers a JSON object from the subprocess and falls back to text,
// so a plain CLI works without an adapter. When the manifest declares JSON
// output, non-JSON is a contract violation and is reported as such.
func (r *Runtime) parseResult(spec Spec, data []byte) (any, *Error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		if spec.ExpectJSON {
			return nil, &Error{Code: CodeMalformedOutput, Message: "capability produced no output but a JSON object was expected"}
		}
		return map[string]any{}, nil
	}
	if trimmed[0] == '{' || trimmed[0] == '[' {
		var v any
		if err := json.Unmarshal(trimmed, &v); err == nil {
			return v, nil
		}
	}
	if spec.ExpectJSON {
		return nil, &Error{
			Code:    CodeMalformedOutput,
			Message: "capability declared JSON output but produced " + describeOutput(trimmed),
			Details: truncate(string(trimmed), 200),
		}
	}
	return map[string]any{"text": string(trimmed)}, nil
}

func describeOutput(b []byte) string {
	if len(b) > 40 {
		return "non-JSON output"
	}
	return fmt.Sprintf("non-JSON output: %q", string(b))
}

func success(started time.Time, in int, result any) *Response {
	return &Response{
		OK:         true,
		Result:     result,
		DurationMs: time.Since(started).Milliseconds(),
		InputBytes: in,
	}
}

func (r *Runtime) buildArgs(spec Spec, input json.RawMessage) ([]string, error) {
	args := append([]string{}, spec.Args...)
	if spec.InputMode != ModeArgv {
		return args, nil
	}
	if len(input) == 0 {
		return args, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(input, &fields); err != nil {
		return nil, fmt.Errorf("argv mode requires a JSON object input: %w", err)
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	// Deterministic argument order keeps invocations reproducible.
	sort.Strings(keys)
	for _, k := range keys {
		flag := spec.ArgvMap[k]
		if flag == "" {
			flag = "--" + k
		}
		value := scalarValue(fields[k])
		if value == "" {
			args = append(args, flag)
			continue
		}
		args = append(args, flag, value)
	}
	return args, nil
}

func scalarValue(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// buildEnv starts from a minimal environment and adds only what is declared,
// so a capability cannot accidentally inherit unrelated secrets.
func buildEnv(extra map[string]string) []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"LANG=C",
	}
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+extra[k])
	}
	return env
}

func (r *Runtime) invokeBuiltin(ctx context.Context, spec Spec, input json.RawMessage, started time.Time) (*Response, error) {
	h := spec.HandlerFunc
	if h == nil {
		var ok bool
		h, ok = r.handlers[spec.Handler]
		if !ok {
			return failure(CodeSpawnFailed,
				fmt.Sprintf("no handler named %q is registered", spec.Handler), started, len(input)), nil
		}
	}
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	result, err := h(ctx, input)
	if err != nil {
		return failure(CodeHandlerFailed, err.Error(), started, len(input)), nil
	}
	encoded, _ := json.Marshal(result)
	resp := success(started, len(input), result)
	resp.OutputBytes = len(encoded)
	return resp, nil
}

// boundedWriter caps accumulation while still draining the pipe, so a chatty
// process is never blocked and can always be terminated.
type boundedWriter struct {
	w        *bytes.Buffer
	limit    int
	exceeded bool
}

func (b *boundedWriter) Write(p []byte) (int, error) {
	if b.w.Len() < b.limit {
		remaining := b.limit - b.w.Len()
		if len(p) <= remaining {
			return b.w.Write(p)
		}
		b.w.Write(p[:remaining])
		b.exceeded = true
		return len(p), nil
	}
	b.exceeded = true
	return len(p), nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
