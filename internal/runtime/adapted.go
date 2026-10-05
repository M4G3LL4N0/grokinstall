package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/M4G3LL4N0/grokinstall/internal/adapter"
)

// invokeAdapted runs a capability through its adapter: validate, translate,
// execute with direct argv, normalize.
//
// It is a separate path from the legacy stdin-JSON pipe on purpose. The old path
// wrote the caller's JSON to the child's stdin and returned whatever came back,
// which is only correct for a program that speaks JSON on stdin. The adapted
// path is correct for an ordinary CLI.
func (r *Runtime) invokeAdapted(ctx context.Context, spec Spec, input json.RawMessage, started time.Time) (*Response, error) {
	a, err := toAdapterAdapter(spec.Adapter)
	if err != nil {
		resp := failure(CodeInvalidAdapter, err.Error(), started, len(input))
		resp.Error.Capability = spec.Capability
		resp.Error.Component = "adapter"
		resp.Error.RecommendedAction = recommendedAction(spec.Capability)
		return resp, nil
	}

	// Validate input against the manifest before anything is spawned. The
	// upstream binary must not be responsible for GrokInstall's own contract.
	fields, verr := decodeInput(input)
	if verr != nil {
		resp := failure(CodeInvalidInput, verr.Error(), started, len(input))
		resp.Error.Capability = spec.Capability
		resp.Error.Component = "input"
		return resp, nil
	}
	if unknown := unknownFields(fields, a); len(unknown) > 0 {
		resp := failure(CodeInvalidInput,
			"unknown input field(s): "+strings.Join(unknown, ", ")+
				"; this capability accepts "+strings.Join(knownFields(a), ", "),
			started, len(input))
		resp.Error.Capability = spec.Capability
		resp.Error.Component = "input"
		return resp, nil
	}

	plan, perr := a.Plan(fields)
	if perr != nil {
		resp := failure(CodeInvalidInput, perr.Error(), started, len(input))
		resp.Error.Capability = spec.Capability
		resp.Error.Component = "input"
		return resp, nil
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

	// Direct argv. No shell, no command string, no interpolation.
	cmd := exec.CommandContext(runCtx, spec.Command, plan.Argv...)
	workDir := spec.WorkingDir
	if plan.WorkingDir != "" {
		workDir = plan.WorkingDir
	}
	cmd.Dir = workDir
	cmd.Env = buildEnv(spec.Env)
	// Own process group so a timeout kills descendants, not just the
	// direct child. See process_unix.go.
	isolate(cmd)
	cmd.WaitDelay = 500 * time.Millisecond
	if len(plan.Stdin) > 0 {
		cmd.Stdin = bytes.NewReader(plan.Stdin)
	}

	var stdout, stderr bytes.Buffer
	stdoutW := &boundedWriter{w: &stdout, limit: maxOut}
	stderrW := &boundedWriter{w: &stderr, limit: maxOut}
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	runErr := cmd.Run()
	if stdoutW.exceeded || stderrW.exceeded {
		resp := failure(CodeOutputTooLarge,
			fmt.Sprintf("capability output exceeded the %d byte bound", maxOut), started, len(input))
		resp.Error.Capability = spec.Capability
		resp.Error.Component = "runtime"
		return resp, nil
	}
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		resp := failure(CodeTimeout, fmt.Sprintf("capability timed out after %s", timeout), started, len(input))
		resp.Error.Capability = spec.Capability
		resp.Error.Component = "runtime"
		return resp, nil
	}

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
			if spec.OutputMode == adapter.OutputExitStatus {
				// The mapping explicitly asks for the status, so a non-zero exit
				// is a result rather than a failure.
				result, nerr := adapter.Normalize(spec.OutputMode, spec.OutputField, stdout.Bytes(), exitCode, spec.ExpectJSON)
				if nerr != nil {
					return adapterFailure(nerr, spec, started, len(input)), nil
				}
				resp := success(started, len(input), result)
				resp.OutputBytes = len(stdout.Bytes())
				return resp, nil
			}
			detail := fmt.Sprintf("capability exited with status %d", exitCode)
			if msg := strings.TrimSpace(stderr.String()); msg != "" {
				detail += ": " + truncate(msg, 500)
			}
			resp := failure(CodeNonZeroExit, detail, started, len(input))
			resp.Error.Capability = spec.Capability
			resp.Error.Component = "runtime"
			return resp, nil
		}
		if errors.Is(runCtx.Err(), context.Canceled) {
			resp := failure(CodeTimeout, "capability was cancelled", started, len(input))
			resp.Error.Capability = spec.Capability
			resp.Error.Component = "runtime"
			return resp, nil
		}
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = runErr.Error()
		}
		resp := failure(CodeSpawnFailed, "could not start capability: "+msg, started, len(input))
		resp.Error.Capability = spec.Capability
		resp.Error.Component = "runtime"
		return resp, nil
	}

	result, nerr := adapter.Normalize(spec.OutputMode, spec.OutputField, stdout.Bytes(), exitCode, spec.ExpectJSON)
	if nerr != nil {
		return adapterFailure(nerr, spec, started, len(input)), nil
	}
	resp := success(started, len(input), result)
	resp.OutputBytes = len(stdout.Bytes())
	if msg := strings.TrimSpace(stderr.String()); msg != "" {
		resp.Warnings = append(resp.Warnings, "stderr: "+truncate(msg, 500))
	}
	if len(plan.Omitted) > 0 {
		resp.Warnings = append(resp.Warnings,
			"ignored input field(s) with no binding: "+strings.Join(plan.Omitted, ", "))
	}
	return resp, nil
}

func adapterFailure(err error, spec Spec, started time.Time, in int) *Response {
	code := CodeInvalidAdapter
	var aerr *adapter.Error
	if errors.As(err, &aerr) {
		code = aerr.Code
	}
	resp := failure(code, err.Error(), started, in)
	resp.Error.Capability = spec.Capability
	resp.Error.Component = "adapter"
	resp.Error.RecommendedAction = recommendedAction(spec.Capability)
	return resp
}

func recommendedAction(capability string) string {
	if capability == "" {
		return ""
	}
	return "grokinstall diagnose " + capability
}

// toAdapterAdapter converts the runtime's Spec view into the adapter package's
// mapping so translation logic lives in exactly one place.
func toAdapterAdapter(a *Adapter) (*adapter.Adapter, error) {
	if a == nil {
		return nil, errors.New("capability has no invocation mapping")
	}
	out := &adapter.Adapter{
		Kind:        orString(a.Kind, "cli"),
		OutputMode:  a.OutputMode,
		OutputField: a.OutputField,
		Operation:   a.Operation,
		Confidence:  adapter.ConfidenceHigh,
	}
	if out.OutputMode == "" {
		return nil, errors.New("adapter declares no output mode")
	}
	if out.OutputField == "" {
		return nil, errors.New("adapter declares no output field")
	}
	for _, b := range a.Argv {
		out.Invocation.Argv = append(out.Invocation.Argv, adapter.Binding{
			From: b.From, Kind: b.Kind, Flag: b.Flag, Position: b.Position,
			Value: b.Value, Required: b.Required,
			AllowDashLeading: b.AllowDashLeading, EndOfOptions: b.EndOfOptions,
		})
	}
	if a.Stdin != nil {
		out.Invocation.Stdin = &adapter.Binding{
			From: a.Stdin.From, Kind: adapter.KindStdin, Required: a.Stdin.Required,
		}
	}
	if a.WorkingDir != nil {
		out.Invocation.WorkingDir = &adapter.Binding{
			From: a.WorkingDir.From, Kind: adapter.KindWorkingDir, Required: a.WorkingDir.Required,
		}
	}
	if err := out.Validate(); err != nil {
		return nil, err
	}
	return out, nil
}

func orString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// decodeInput turns the caller's JSON into generic fields. A non-object input is
// rejected here, before any subprocess exists.
func decodeInput(input json.RawMessage) (map[string]any, error) {
	if len(bytes.TrimSpace(input)) == 0 {
		return map[string]any{}, nil
	}
	var fields map[string]any
	if err := json.Unmarshal(input, &fields); err != nil {
		return nil, fmt.Errorf("input must be a JSON object: %w", err)
	}
	if fields == nil {
		fields = map[string]any{}
	}
	return fields, nil
}

// knownFields lists the input fields a mapping consumes.
func knownFields(a *adapter.Adapter) []string {
	var out []string
	for _, b := range a.Invocation.Argv {
		if b.From != "" && b.Kind != "literal" {
			out = append(out, b.From)
		}
	}
	if a.Invocation.Stdin != nil {
		out = append(out, a.Invocation.Stdin.From)
	}
	if a.Invocation.WorkingDir != nil {
		out = append(out, a.Invocation.WorkingDir.From)
	}
	sort.Strings(out)
	return out
}

// unknownFields returns input fields that no binding consumes. Rejecting them
// keeps the contract honest: a typo in a field name must not silently do nothing.
func unknownFields(fields map[string]any, a *adapter.Adapter) []string {
	known := map[string]bool{}
	for _, name := range knownFields(a) {
		known[name] = true
	}
	var unknown []string
	for name := range fields {
		if !known[name] {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	return unknown
}
