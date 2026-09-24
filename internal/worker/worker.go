// Package worker defines the external worker protocol. Any external execution
// — OpenCode, Cursor, a model provider, a local script, a CLI — is reached
// through the same JSON request/response contract, so GrokInstall never has to
// know which one it is talking to.
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"grokinstall/internal/contextpack"
)

// Default execution bounds for a worker subprocess.
const (
	DefaultTimeout       = 120 * time.Second
	DefaultMaxOutputSize = 1 << 20 // 1 MiB
)

// Request is what GrokInstall sends to a worker.
type Request struct {
	Task        string            `json:"task"`
	Input       any               `json:"input"`
	ContextPack *contextpack.Pack `json:"context_pack,omitempty"`
	Constraints Constraints       `json:"constraints"`
}

// Constraints bound what a worker may do.
type Constraints struct {
	Timeout       time.Duration     `json:"-"`
	MaxOutputSize int               `json:"max_output_bytes,omitempty"`
	WorkingDir    string            `json:"working_dir,omitempty"`
	Environment   map[string]string `json:"environment,omitempty"`
	AllowedPaths  []string          `json:"allowed_paths,omitempty"`
	ReadOnly      bool              `json:"read_only,omitempty"`
}

// Response is what a worker must return.
type Response struct {
	OK        bool     `json:"ok"`
	Result    any      `json:"result,omitempty"`
	Artifacts []string `json:"artifacts,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

// Provider is one external worker implementation.
type Provider interface {
	Name() string
	Available() (bool, string)
	Execute(ctx context.Context, req Request) (*Response, error)
}

// ErrUnavailable reports that a worker cannot be used right now.
var ErrUnavailable = errors.New("worker unavailable")

// Subprocess runs a local command as a worker.
type Subprocess struct {
	Command        string
	Args           []string
	Timeout        time.Duration
	MaxOutputBytes int
	Env            []string
}

// NewSubprocess builds a subprocess worker for a command.
func NewSubprocess(command string, args ...string) *Subprocess {
	return &Subprocess{
		Command:        command,
		Args:           args,
		Timeout:        DefaultTimeout,
		MaxOutputBytes: DefaultMaxOutputSize,
	}
}

// Name identifies the worker.
func (s *Subprocess) Name() string { return "local_subprocess" }

// Available reports whether the command exists on this machine.
func (s *Subprocess) Available() (bool, string) {
	if s.Command == "" {
		return false, "no command configured"
	}
	if strings.ContainsAny(s.Command, "/\\") {
		if _, err := os.Stat(s.Command); err != nil {
			return false, fmt.Sprintf("worker command %q not found", s.Command)
		}
		return true, ""
	}
	if _, err := exec.LookPath(s.Command); err != nil {
		return false, fmt.Sprintf("worker command %q not found on PATH", s.Command)
	}
	return true, ""
}

// Execute sends the request as JSON on stdin and parses a JSON response from
// stdout, under a timeout and an output bound.
func (s *Subprocess) Execute(ctx context.Context, req Request) (*Response, error) {
	if ok, reason := s.Available(); !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnavailable, reason)
	}
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("encode worker request: %w", err)
	}

	timeout := s.Timeout
	if req.Constraints.Timeout > 0 {
		timeout = req.Constraints.Timeout
	}
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	maxOut := s.MaxOutputBytes
	if req.Constraints.MaxOutputSize > 0 {
		maxOut = req.Constraints.MaxOutputSize
	}
	if maxOut <= 0 {
		maxOut = DefaultMaxOutputSize
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.Command, s.Args...)
	cmd.Dir = req.Constraints.WorkingDir
	cmd.Env = s.baseEnv(req.Constraints)
	// A worker that spawns children must not keep the pipes open after the
	// timeout, so Wait closes them shortly after cancellation.
	cmd.WaitDelay = 200 * time.Millisecond

	var stdout, stderr bytes.Buffer
	stdoutW := &boundedWriter{w: &stdout, limit: maxOut}
	stderrW := &boundedWriter{w: &stderr, limit: maxOut}
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW

	runErr := cmd.Run()
	if stdoutW.exceeded {
		return nil, fmt.Errorf("worker output exceeded the %d byte bound", maxOut)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return nil, fmt.Errorf("worker timed out after %s", timeout)
	}
	if runErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = runErr.Error()
		}
		return nil, fmt.Errorf("worker failed: %s", msg)
	}

	var resp Response
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &resp); err != nil {
		return nil, fmt.Errorf("worker returned malformed JSON: %w", err)
	}
	return &resp, nil
}

func (s *Subprocess) baseEnv(c Constraints) []string {
	env := s.Env
	if env == nil {
		env = os.Environ()
	}
	for k, v := range c.Environment {
		env = append(env, k+"="+v)
	}
	return env
}

// boundedWriter stops accumulating once a limit is reached but keeps draining
// the pipe so the worker is never blocked writing to a full pipe.
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

// Registry holds the known worker providers.
type Registry struct {
	providers map[string]Provider
	order     []string
}

// NewRegistry returns a registry with the optional providers GrokInstall knows
// about. Only the local subprocess worker is executable in Part 1; the rest are
// registered so their absence is reported honestly instead of silently.
func NewRegistry() *Registry {
	r := &Registry{providers: map[string]Provider{}}
	for _, p := range []Provider{
		NewSubprocess(""),
		NewPlaceholder("opencode"),
		NewPlaceholder("cursor"),
		NewPlaceholder("chatgpt"),
		NewPlaceholder("claude"),
		NewPlaceholder("grok"),
		NewPlaceholder("ollama"),
	} {
		r.Register(p)
	}
	return r
}

// Register adds a provider.
func (r *Registry) Register(p Provider) {
	if _, ok := r.providers[p.Name()]; !ok {
		r.order = append(r.order, p.Name())
	}
	r.providers[p.Name()] = p
}

// Lookup returns a provider by name.
func (r *Registry) Lookup(name string) (Provider, error) {
	p, ok := r.providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown worker %q", name)
	}
	return p, nil
}

// Names lists registered workers in registration order.
func (r *Registry) Names() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Placeholder represents a provider that is recognized but not integrated yet.
type Placeholder struct{ id string }

// NewPlaceholder registers a recognized-but-not-integrated provider.
func NewPlaceholder(id string) *Placeholder { return &Placeholder{id: id} }

// Name identifies the provider.
func (p *Placeholder) Name() string { return p.id }

// Available always reports unavailable in Part 1.
func (p *Placeholder) Available() (bool, string) {
	return false, "recognized optional provider; not integrated in Part 1"
}

// Execute refuses to pretend it can run.
func (p *Placeholder) Execute(context.Context, Request) (*Response, error) {
	return nil, fmt.Errorf("%w: %s is not integrated in Part 1", ErrUnavailable, p.Name())
}
