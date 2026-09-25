// Package adapter turns structured capability input into a real upstream CLI
// invocation, and turns the upstream's output into an honest GrokInstall
// result.
//
// A capability is not "a registered binary". It is a mapping from named input
// fields to concrete argv and stdin, plus a declared way of reading what comes
// back. This package owns that mapping and nothing else: execution, timeouts
// and output bounds stay in the runtime, and trust stays with the integrity
// gate.
//
// Two properties are non-negotiable and enforced by construction:
//
//   - argv is always built as a slice of separate elements. There is no shell,
//     no command string, and no interpolation, so no input value can ever
//     become shell syntax.
//   - a binding may only say where a value goes. It may never contain a format
//     string, a command, or a pipeline.
package adapter

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Binding kinds. Each names a deterministic placement in the invocation.
const (
	// KindPositional places a value as a bare argv element (argv[n]).
	KindPositional = "positional"
	// KindFlag places a value as "--flag value" (two argv elements).
	KindFlag = "flag"
	// KindFlagEquals places a value as "--flag=value" (one argv element).
	KindFlagEquals = "flag_equals"
	// KindBooleanFlag emits "--flag" when the value is true, nothing when false.
	KindBooleanFlag = "boolean_flag"
	// KindRepeatedFlag emits "--flag v" once per element of a list.
	KindRepeatedFlag = "repeated_flag"
	// KindStdin sends a value as raw stdin instead of argv.
	KindStdin = "stdin"
	// KindLiteral emits a constant argv element (a subcommand or mode word).
	KindLiteral = "literal"
	// KindWorkingDir sets the child's working directory.
	KindWorkingDir = "working_dir"
)

// Confidence levels for a generated mapping. A mapping is only marked runnable
// when its confidence clears the policy in IsRunnable.
type Confidence string

// Confidence levels.
const (
	// ConfidenceHigh means every binding is backed by explicit machine-readable
	// evidence (a manifest entrypoint, a known adapter, or declared help flags).
	ConfidenceHigh Confidence = "high"
	// ConfidenceMedium means the mapping is mostly evidence-backed but at least
	// one placement is inferred.
	ConfidenceMedium Confidence = "medium"
	// ConfidenceLow means the mapping is a guess. It is never runnable.
	ConfidenceLow Confidence = "low"
)

// Invocation is how a capability is called.
type Invocation struct {
	// Argv holds the bindings that contribute command-line arguments.
	Argv []Binding `json:"argv,omitempty"`
	// Stdin holds the binding whose value is written to standard input.
	Stdin *Binding `json:"stdin,omitempty"`
	// WorkingDir, when set, sets the child's working directory from input.
	WorkingDir *Binding `json:"working_dir,omitempty"`
}

// Binding maps one input field to one deterministic place in the invocation.
type Binding struct {
	// From is the input field name this binding reads.
	From string `json:"from"`
	// Kind is one of the Kind* constants.
	Kind string `json:"kind"`
	// Flag is the flag spelling for flag-shaped kinds, without a value.
	Flag string `json:"flag,omitempty"`
	// Position is the zero-based argv index for KindPositional.
	Position int `json:"position,omitempty"`
	// Value is the constant emitted by KindLiteral.
	Value string `json:"value,omitempty"`
	// Required marks a binding that must resolve to a value.
	Required bool `json:"required,omitempty"`
	// AllowDashLeading permits a value that begins with "-". It is off by
	// default so that a caller cannot smuggle a flag into a positional slot.
	AllowDashLeading bool `json:"allow_dash_leading,omitempty"`
	// EndOfOptions inserts "--" before positional values when the upstream
	// command supports it, so a value can never be read as a flag.
	EndOfOptions bool `json:"end_of_options,omitempty"`
}

// Adapter is a complete, auditable mapping for one capability.
type Adapter struct {
	// Kind names the adapter family, e.g. "cli".
	Kind string `json:"kind"`
	// Invocation is the deterministic mapping.
	Invocation Invocation `json:"invocation"`
	// OutputMode declares how upstream output is read.
	OutputMode string `json:"output_mode"`
	// OutputField names the result key the normalized value is placed under.
	OutputField string `json:"output_field,omitempty"`
	// Operation is the evidence-backed operation this adapter performs, e.g.
	// "view" or "filter". It must not exceed what the mapping can do.
	Operation string `json:"operation,omitempty"`
	// Confidence records how well-evidenced the mapping is.
	Confidence Confidence `json:"confidence"`
	// Evidence lists the deterministic observations behind the mapping.
	Evidence []string `json:"evidence,omitempty"`
	// Notes carries human-facing caveats, e.g. lexical retrieval.
	Notes []string `json:"notes,omitempty"`
}

// Output modes. The mode decides what a result may honestly contain.
const (
	// OutputJSON preserves structured upstream output as structured JSON.
	OutputJSON = "json"
	// OutputText exposes stdout as a string. This is the honest mode for a CLI
	// that prints text, such as bat.
	OutputText = "text"
	// OutputLines splits stdout into a list of lines.
	OutputLines = "lines"
	// OutputExitStatus returns only the process exit status.
	OutputExitStatus = "exit_status"
	// OutputArtifact writes stdout to a file and returns its path.
	OutputArtifact = "artifact"
)

// ValidOutputMode reports whether m is a known output mode.
func ValidOutputMode(m string) bool {
	switch m {
	case OutputJSON, OutputText, OutputLines, OutputExitStatus, OutputArtifact:
		return true
	}
	return false
}

// RunnableConfidenceThreshold is the documented deterministic policy: a mapping
// must reach at least medium confidence to be marked runnable. Low-confidence
// mappings stay planned.
const RunnableConfidenceThreshold = ConfidenceMedium

// IsRunnable reports whether the adapter may be invoked automatically under the
// documented policy.
func (a *Adapter) IsRunnable() bool {
	if a == nil {
		return false
	}
	switch a.Confidence {
	case ConfidenceHigh, ConfidenceMedium:
		return true
	default:
		return false
	}
}

// Validate checks that an adapter is internally consistent: known binding
// kinds, sensible flags, ordered positional slots, a valid output mode, and
// output fields that match the declared mode.
func (a *Adapter) Validate() error {
	if a == nil {
		return fmt.Errorf("adapter is required for a runnable capability")
	}
	if a.Kind == "" {
		return fmt.Errorf("adapter kind is required")
	}
	if !ValidOutputMode(a.OutputMode) {
		return fmt.Errorf("unsupported output mode %q", a.OutputMode)
	}
	switch a.Confidence {
	case ConfidenceHigh, ConfidenceMedium, ConfidenceLow:
	default:
		return fmt.Errorf("adapter confidence must be high, medium or low, got %q", a.Confidence)
	}
	if a.OutputField == "" {
		return fmt.Errorf("adapter output_field is required so a result key is always present")
	}
	if b := a.Invocation.Stdin; b != nil {
		if b.Kind == "" {
			b.Kind = KindStdin
		}
		if err := validateBinding(b); err != nil {
			return fmt.Errorf("stdin binding: %w", err)
		}
	}
	if b := a.Invocation.WorkingDir; b != nil {
		if b.Kind == "" {
			b.Kind = KindWorkingDir
		}
		if err := validateBinding(b); err != nil {
			return fmt.Errorf("working_dir binding: %w", err)
		}
	}
	seen := map[int]string{}
	for i, b := range a.Invocation.Argv {
		if err := validateBinding(&a.Invocation.Argv[i]); err != nil {
			return fmt.Errorf("argv binding %d: %w", i, err)
		}
		if b.Kind == KindPositional {
			if b.Position < 0 {
				return fmt.Errorf("positional binding %q needs a non-negative position", b.From)
			}
			if prev, dup := seen[b.Position]; dup {
				return fmt.Errorf("two bindings claim positional %d: %q and %q", b.Position, prev, b.From)
			}
			seen[b.Position] = b.From
		}
		if b.From == "" && b.Kind != KindLiteral {
			return fmt.Errorf("argv binding %d must name an input field", i)
		}
	}
	if err := validateOutputField(a); err != nil {
		return err
	}
	return nil
}

func validateOutputField(a *Adapter) error {
	switch a.OutputMode {
	case OutputText:
		if a.OutputField != "text" {
			return fmt.Errorf("output mode %q requires output_field %q, got %q", OutputText, "text", a.OutputField)
		}
	case OutputLines:
		if a.OutputField != "lines" {
			return fmt.Errorf("output mode %q requires output_field %q, got %q", OutputLines, "lines", a.OutputField)
		}
	case OutputJSON:
		if a.OutputField == "text" || a.OutputField == "lines" {
			return fmt.Errorf("output mode %q must not use the %q field", OutputJSON, a.OutputField)
		}
	}
	return nil
}

func validateBinding(b *Binding) error {
	if b == nil {
		return nil
	}
	// A stdin or working_dir binding is identified by the slot it occupies, so
	// an omitted kind is filled in by the caller before validation.
	switch b.Kind {
	case KindPositional, KindStdin, KindWorkingDir, KindLiteral:
		return nil
	case KindFlag, KindFlagEquals, KindBooleanFlag, KindRepeatedFlag:
		if strings.TrimSpace(b.Flag) == "" {
			return fmt.Errorf("binding %q uses kind %q but declares no flag", b.From, b.Kind)
		}
		if !strings.HasPrefix(b.Flag, "-") {
			return fmt.Errorf("binding %q flag %q must start with a dash", b.From, b.Flag)
		}
		return nil
	default:
		return fmt.Errorf("unsupported binding kind %q", b.Kind)
	}
}

// Plan is a fully resolved invocation: literal argv elements plus the stdin
// payload. It contains no shell and no format strings.
type Plan struct {
	// Argv is the complete argument vector, including argv[0]-relative
	// elements only. The command path itself is added by the caller.
	Argv []string
	// Stdin is the raw bytes to write to standard input, if any.
	Stdin []byte
	// WorkingDir is the resolved working directory, if any.
	WorkingDir string
	// Omitted lists input fields that no binding consumed, so the caller can
	// warn or reject unknown fields.
	Omitted []string
}

// Value is a resolved input value, kept as a string plus its original JSON
// shape so a binding can decide how to render it.
type Value struct {
	Raw   string
	Str   string
	Bool  bool
	Num   float64
	Items []string
	IsStr bool
	// IsBool distinguishes a real boolean from the strings "true" and "false",
	// so a boolean flag cannot silently accept a string.
	IsBool bool
	// IsJSONObject reports that the value is a JSON object or array, which
	// matters for tools such as jq that take structured input.
	IsStructured bool
	// Structured holds the raw JSON for a structured value.
	Structured []byte
}

// inputValue converts a decoded JSON value into a Value.
func inputValue(raw any) Value {
	v := Value{}
	switch t := raw.(type) {
	case string:
		v.Str, v.Raw, v.IsStr = t, t, true
	case bool:
		v.Bool, v.Raw, v.IsBool = t, strconv.FormatBool(t), true
	case float64:
		v.Num = t
		v.Raw = strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		v.Raw = "null"
	default:
		encoded, err := marshal(t)
		if err == nil {
			v.Structured = encoded
			v.Raw = string(encoded)
			v.IsStructured = true
		} else {
			v.Raw = ""
		}
	}
	return v
}

// maxValueBytes bounds any single input value so a caller cannot force an
// unbounded argv element or stdin payload.
const maxValueBytes = 1 << 20

// Plan resolves input against the adapter. It validates the mapping shape and
// produces a deterministic argv. It never spawns anything.
func (a *Adapter) Plan(fields map[string]any) (*Plan, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	plan := &Plan{}
	// Positional slots are placed in declared order so output is reproducible.
	positional := make([]*Binding, 0)
	for i := range a.Invocation.Argv {
		b := &a.Invocation.Argv[i]
		if b.Kind == KindPositional {
			positional = append(positional, b)
		}
	}
	sort.SliceStable(positional, func(i, j int) bool { return positional[i].Position < positional[j].Position })

	consumed := map[string]bool{}
	var literalArgs []string
	// Literals and flag bindings keep manifest order; only positionals are
	// index-sorted, so a stable mapping yields a stable command line.
	for i := range a.Invocation.Argv {
		b := &a.Invocation.Argv[i]
		if b.Kind == KindPositional {
			continue
		}
		if b.Kind == KindLiteral {
			literalArgs = append(literalArgs, b.Value)
			continue
		}
		raw, ok := fields[b.From]
		if !ok {
			if b.Required {
				return nil, &ValidationError{Field: b.From, Reason: "required field is missing"}
			}
			consumed[b.From] = true
			continue
		}
		consumed[b.From] = true
		val, err := resolveValue(b, raw)
		if err != nil {
			return nil, err
		}
		literalArgs = append(literalArgs, val...)
	}

	var positionalArgs []string
	endOfOptions := false
	for _, b := range positional {
		raw, ok := fields[b.From]
		if !ok {
			if b.Required {
				return nil, &ValidationError{Field: b.From, Reason: "required field is missing"}
			}
			continue
		}
		consumed[b.From] = true
		val := inputValue(raw)
		if val.Str == "" && !val.IsStr {
			continue
		}
		if len(val.Str) > maxValueBytes {
			return nil, &ValidationError{Field: b.From, Reason: fmt.Sprintf("value exceeds %d bytes", maxValueBytes)}
		}
		if b.EndOfOptions && !endOfOptions {
			positionalArgs = append(positionalArgs, "--")
			endOfOptions = true
		}
		if !b.AllowDashLeading && strings.HasPrefix(val.Str, "-") {
			return nil, &ValidationError{
				Field:  b.From,
				Reason: fmt.Sprintf("value may not begin with %q in this position", "-"),
			}
		}
		positionalArgs = append(positionalArgs, val.Str)
	}

	plan.Argv = append(plan.Argv, literalArgs...)
	plan.Argv = append(plan.Argv, positionalArgs...)

	if b := a.Invocation.Stdin; b != nil {
		if raw, ok := fields[b.From]; ok {
			consumed[b.From] = true
			val := inputValue(raw)
			plan.Stdin = []byte(val.Raw)
		} else if b.Required {
			return nil, &ValidationError{Field: b.From, Reason: "required field is missing"}
		}
	}
	if b := a.Invocation.WorkingDir; b != nil {
		if raw, ok := fields[b.From]; ok {
			consumed[b.From] = true
			plan.WorkingDir = inputValue(raw).Str
		} else if b.Required {
			return nil, &ValidationError{Field: b.From, Reason: "required field is missing"}
		}
	}

	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !consumed[name] {
			plan.Omitted = append(plan.Omitted, name)
		}
	}
	return plan, nil
}

// ValidationError reports input that does not satisfy the manifest contract.
type ValidationError struct {
	Field  string
	Reason string
}

func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Reason
	}
	return fmt.Sprintf("field %q: %s", e.Field, e.Reason)
}

// resolveValue renders one value for a flag-shaped binding.
func resolveValue(b *Binding, raw any) ([]string, error) {
	val := inputValue(raw)
	if len(val.Raw) > maxValueBytes {
		return nil, &ValidationError{Field: b.From, Reason: fmt.Sprintf("value exceeds %d bytes", maxValueBytes)}
	}
	switch b.Kind {
	case KindBooleanFlag:
		if !val.IsBool {
			return nil, &ValidationError{Field: b.From, Reason: "boolean flag requires a true or false value"}
		}
		if val.Bool {
			return []string{b.Flag}, nil
		}
		return nil, nil
	case KindRepeatedFlag:
		if !val.IsStructured {
			return nil, &ValidationError{Field: b.From, Reason: "repeated flag requires an array of values"}
		}
		items, err := decodeArray(val.Structured)
		if err != nil {
			return nil, &ValidationError{Field: b.From, Reason: "repeated flag requires an array of values"}
		}
		var out []string
		for _, item := range items {
			out = append(out, b.Flag, item)
		}
		return out, nil
	case KindFlagEquals:
		if strings.ContainsAny(val.Str, "\n") {
			return nil, &ValidationError{Field: b.From, Reason: "value may not contain a newline"}
		}
		return []string{b.Flag + "=" + val.Str}, nil
	default: // KindFlag
		if strings.ContainsAny(val.Str, "\n") {
			return nil, &ValidationError{Field: b.From, Reason: "value may not contain a newline"}
		}
		return []string{b.Flag, val.Str}, nil
	}
}

// OutputModeOrEmpty reports the adapter's output mode, tolerating a nil
// receiver so callers can record a discovery summary before validating.
func (a *Adapter) OutputModeOrEmpty() string {
	if a == nil {
		return ""
	}
	return a.OutputMode
}
