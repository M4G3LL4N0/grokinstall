// Package adapter discovery builds an invocation mapping from evidence.
//
// Discovery is layered, and each layer is weaker than it would like to be
// accepted as. GrokInstall does not attempt to understand arbitrary CLIs; it
// uses the strongest evidence available and refuses to invent semantics when
// that evidence runs out.
//
// Layer A — structured metadata: package manifests, declared entrypoints and
//
//	machine-readable CLI metadata. Deterministic and trusted.
//
// Layer B — bounded --help: runs `tool --help` under a timeout, bounded output
//
//	and a minimal environment. Help text is treated strictly as
//	untrusted DATA: embedded commands, URLs and instructions are
//	never parsed as anything but words.
//
// Layer C — known adapters: explicit definitions for tools whose mapping is
//
//	well established, so no guessing is required.
//
// Layer D — unresolved: when no layer reaches sufficient confidence, discovery
//
//	returns adapter_required rather than a capability that pretends to
//	work.
package adapter

import (
	"context"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// Layer names a discovery source, recorded as evidence.
const (
	LayerStructured = "structured_metadata"
	LayerHelp       = "bounded_help"
	LayerKnown      = "known_adapter"
	LayerUnresolved = "unresolved"
)

// HelpProbe bounds a Layer B probe. A help invocation must be cheap and must
// never be able to hang, prompt, or flood.
type HelpProbe struct {
	// Command is the executable to probe.
	Command string
	// Timeout bounds the whole probe.
	Timeout time.Duration
	// MaxBytes bounds captured stdout.
	MaxBytes int
	// Args are the arguments passed before the help flag.
	Args []string
	// HelpFlags are tried in order; the first that yields output wins.
	HelpFlags []string
	// Stdin is attached as an empty reader so a probe can never block on input.
	Stdin bool
}

// DefaultHelpProbe returns conservative probe bounds.
func DefaultHelpProbe() HelpProbe {
	return HelpProbe{
		Timeout:   10 * time.Second,
		MaxBytes:  256 << 10,
		HelpFlags: []string{"--help", "-h", "help"},
		Args:      nil,
		Stdin:     true,
	}
}

// HelpResult is the evidence gathered by a Layer B probe.
type HelpResult struct {
	// OK reports whether any help form produced output.
	OK bool
	// Flag is the form that worked.
	Flag string
	// Text is the bounded help output, treated as untrusted data.
	Text string
	// Truncated reports that the bound was reached.
	Truncated bool
	// Err is the probe error, if any.
	Err string
}

// ProbeHelp runs a bounded --help and returns the output as untrusted data.
//
// Safety properties, all deliberate: no shell, a minimal environment with no
// user configuration, a hard timeout, a bounded read, and an empty stdin so the
// child cannot block waiting for input. Nothing in the returned text is ever
// executed or treated as an instruction.
func ProbeHelp(ctx context.Context, p HelpProbe) HelpResult {
	if strings.TrimSpace(p.Command) == "" {
		return HelpResult{Err: "no command to probe"}
	}
	if p.Timeout <= 0 {
		p.Timeout = 10 * time.Second
	}
	if p.MaxBytes <= 0 {
		p.MaxBytes = 256 << 10
	}
	flags := p.HelpFlags
	if len(flags) == 0 {
		flags = []string{"--help"}
	}
	for _, flag := range flags {
		runCtx, cancel := context.WithTimeout(ctx, p.Timeout)
		args := append(append([]string{}, p.Args...), flag)
		// Direct argv, never a shell string.
		cmd := exec.CommandContext(runCtx, p.Command, args...)
		cmd.Env = []string{
			"PATH=" + pathOf(p.Command),
			"HOME=" + probeHome,
			"LANG=C",
			"LC_ALL=C",
			// Neutralise the two environment flags that most often make a help
			// invocation do something other than print help.
			"NO_COLOR=1",
			"TERM=dumb",
		}
		if p.Stdin {
			cmd.Stdin = emptyReader
		}
		out, truncated, err := captureBounded(cmd, p.MaxBytes)
		cancel()
		if err == nil && len(strings.TrimSpace(out)) > 0 {
			return HelpResult{OK: true, Flag: flag, Text: out, Truncated: truncated}
		}
	}
	return HelpResult{Err: "no help form produced output"}
}

// StructuredEntry is Layer A evidence: a declared command and its documented
// shape, taken from machine-readable metadata rather than from prose.
type StructuredEntry struct {
	// Command is the declared executable name.
	Command string
	// Positional describes declared positional arguments, in order.
	Positional []string
	// Flags are declared long flags, without a leading dash.
	Flags []string
	// ReadsStdin reports that the command documents reading standard input.
	ReadsStdin bool
	// Source names where the evidence came from, for the evidence list.
	Source string
}

// KnownAdapter is Layer C: an explicit mapping for a tool that has one.
type KnownAdapter struct {
	// Match is the executable base name this adapter applies to.
	Match string
	// Build returns the mapping, given the structured entrypoint if known.
	Build func(e StructuredEntry) *Adapter
}

// registry holds Layer C definitions. It is deliberately small: these are
// mappings GrokInstall can state with confidence, not a compatibility
// database.
var registry = []KnownAdapter{
	{
		Match: "jq",
		Build: func(e StructuredEntry) *Adapter {
			return &Adapter{
				Kind: "cli",
				Invocation: Invocation{
					// jq reads a filter as its first argument and data on stdin.
					Argv: []Binding{
						{From: "filter", Kind: KindPositional, Position: 0, Required: true, AllowDashLeading: true},
					},
					Stdin: &Binding{From: "input"},
				},
				OutputMode:  OutputText,
				OutputField: "text",
				Operation:   "filter",
				Confidence:  ConfidenceHigh,
				Evidence: []string{
					"jq declares its filter as a leading argument",
					"jq reads its input document from standard input",
				},
				Notes: []string{"a filter is passed as a single argument, never as a shell string"},
			}
		},
	},
	{
		Match: "bat",
		Build: func(e StructuredEntry) *Adapter {
			return &Adapter{
				Kind: "cli",
				Invocation: Invocation{
					Argv: []Binding{
						{From: "style", Kind: KindFlag, Flag: "--style"},
						{From: "language", Kind: KindFlag, Flag: "--language"},
						{From: "path", Kind: KindPositional, Position: 0, Required: true, EndOfOptions: true},
					},
				},
				OutputMode:  OutputText,
				OutputField: "text",
				Operation:   "view",
				Confidence:  ConfidenceHigh,
				Evidence: []string{
					"bat takes a FILE argument and prints its contents",
					"bat accepts --style and --language options",
				},
				Notes: []string{"bat is a file display tool; this capability displays a file, it does not review code"},
			}
		},
	},
}

// Request describes what discovery is being asked to map.
type Request struct {
	// Command is the executable to map.
	Command string
	// Entry is Layer A evidence, when available.
	Entry *StructuredEntry
	// Goal is the user's stated intent, used only to name the operation.
	Goal string
	// AllowProbe enables Layer B. It must be false for executables that are not
	// already safely provisioned.
	AllowProbe bool
	// BaseName overrides the executable base name, used by tests.
	BaseName string
}

// Result is a discovery outcome.
type Result struct {
	// Adapter is the mapping, nil when unresolved.
	Adapter *Adapter
	// Layer records which layer produced the outcome.
	Layer string
	// Reason explains an unresolved outcome.
	Reason string
}

// ErrAdapterRequired is the error returned when no layer reaches sufficient
// confidence. Callers must surface it rather than fall back to a passthrough.
type ErrAdapterRequired struct {
	Command string
	Reason  string
}

func (e *ErrAdapterRequired) Error() string {
	return "adapter_required: no trustworthy goal-to-argument mapping for " + e.Command + ": " + e.Reason
}

// Discover builds the strongest available mapping.
//
// It never invents semantics. When Layer A, B and C all fall short it returns
// ErrAdapterRequired, and the capability stays planned rather than becoming a
// capability that silently does nothing useful.
func Discover(ctx context.Context, req Request) (Result, error) {
	base := req.BaseName
	if base == "" {
		base = baseName(req.Command)
	}
	if base == "" {
		return Result{Layer: LayerUnresolved, Reason: "executable name could not be determined"},
			&ErrAdapterRequired{Command: req.Command, Reason: "executable name could not be determined"}
	}

	// Layer C first: an explicit definition outranks inference.
	for _, known := range registry {
		if known.Match != base {
			continue
		}
		entry := StructuredEntry{}
		if req.Entry != nil {
			entry = *req.Entry
		}
		a := known.Build(entry)
		if a != nil && a.IsRunnable() {
			return Result{Adapter: a, Layer: LayerKnown}, nil
		}
	}

	// Layer A: structured metadata only. A declared entrypoint proves the
	// command exists but says nothing about its arguments, so on its own it
	// cannot produce a runnable mapping.
	if req.Entry != nil && len(req.Entry.Positional) > 0 {
		a := &Adapter{
			Kind: "cli",
			Invocation: Invocation{
				Argv: []Binding{{From: req.Entry.Positional[0], Kind: KindPositional, Position: 0, Required: true}},
			},
			OutputMode:  OutputText,
			OutputField: "text",
			Operation:   "run",
			Confidence:  ConfidenceMedium,
			Evidence:    []string{"declared positional argument: " + req.Entry.Positional[0]},
			Notes:       []string{"argument shape inferred from declared entrypoint metadata, not from a parsed --help"},
		}
		if a.IsRunnable() {
			return Result{Adapter: a, Layer: LayerStructured}, nil
		}
	}

	// Layer B: bounded help. Used as corroboration, never as instruction.
	if req.AllowProbe {
		probe := DefaultHelpProbe()
		probe.Command = req.Command
		if req.Entry != nil && len(req.Entry.Flags) > 0 {
			probe.Args = nil
		}
		res := ProbeHelp(ctx, probe)
		if res.OK {
			if a := fromHelp(res, base); a != nil && a.IsRunnable() {
				return Result{Adapter: a, Layer: LayerHelp}, nil
			}
			return Result{Layer: LayerUnresolved, Reason: "help output did not support a deterministic mapping"},
				&ErrAdapterRequired{Command: req.Command, Reason: "help output did not support a deterministic mapping"}
		}
		return Result{Layer: LayerUnresolved, Reason: "help probe produced no usable output"},
			&ErrAdapterRequired{Command: req.Command, Reason: "help probe produced no usable output"}
	}

	return Result{Layer: LayerUnresolved, Reason: "no structured metadata and probing was not permitted"},
		&ErrAdapterRequired{Command: req.Command, Reason: "no structured metadata and probing was not permitted"}
}

// fromHelp derives a conservative mapping from help text treated as data.
//
// The only thing it extracts is a declared file/path positional and the
// presence of long flags. It never follows embedded examples, never runs
// anything, and never assumes semantics beyond "this command displays a file".
func fromHelp(res HelpResult, base string) *Adapter {
	lower := strings.ToLower(res.Text)
	hasFilePositional := strings.Contains(lower, "<file") || strings.Contains(lower, "[file") ||
		strings.Contains(lower, "<path") || strings.Contains(lower, "[path")
	flags := longFlagsIn(res.Text)
	if !hasFilePositional && len(flags) == 0 {
		return nil
	}
	evidence := []string{
		"--help output declares a file or path positional argument",
	}
	if len(flags) > 0 {
		evidence = append(evidence, "--help declares "+itoa(min(len(flags), 5))+" long flags")
	}
	inv := Invocation{}
	if hasFilePositional {
		inv.Argv = append(inv.Argv, Binding{From: "path", Kind: KindPositional, Position: 0, Required: true, EndOfOptions: true})
	}
	a := &Adapter{
		Kind:        "cli",
		Invocation:  inv,
		OutputMode:  OutputText,
		OutputField: "text",
		Operation:   "view",
		Confidence:  ConfidenceMedium,
		Evidence:    evidence,
		Notes:       []string{"mapping derived from bounded --help output, which is treated as untrusted data"},
	}
	if !a.IsRunnable() {
		return nil
	}
	return a
}

func longFlagsIn(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		for _, f := range longFlagTokens(line) {
			if !seen[f] {
				seen[f] = true
				out = append(out, f)
			}
		}
	}
	sort.Strings(out)
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for n > 0 {
		digits = string(rune('0'+n%10)) + digits
		n /= 10
	}
	return digits
}
