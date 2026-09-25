package adapter

import (
	"strings"
	"testing"
)

// --- binding kinds ---------------------------------------------------------

func TestPositionalBindingPlacesValueAsBareArgv(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{
			{From: "path", Kind: KindPositional, Position: 0, Required: true},
		}},
	}
	plan, err := a.Plan(map[string]any{"path": "/tmp/example.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Argv) != 1 || plan.Argv[0] != "/tmp/example.txt" {
		t.Fatalf("argv = %q, want the bare path", plan.Argv)
	}
}

func TestPositionalBindingsFollowDeclaredPosition(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{
			{From: "second", Kind: KindPositional, Position: 1, Required: true},
			{From: "first", Kind: KindPositional, Position: 0, Required: true},
		}},
	}
	plan, err := a.Plan(map[string]any{"first": "one", "second": "two"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(plan.Argv, ","); got != "one,two" {
		t.Fatalf("argv = %q, want positions honoured regardless of declaration order", got)
	}
}

func TestFlagBindingEmitsTwoArgvElements(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{{From: "style", Kind: KindFlag, Flag: "--style"}}},
	}
	plan, err := a.Plan(map[string]any{"style": "plain"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Argv) != 2 || plan.Argv[0] != "--style" || plan.Argv[1] != "plain" {
		t.Fatalf("argv = %q, want [\"--style\" \"plain\"]", plan.Argv)
	}
}

func TestFlagEqualsBindingEmitsOneArgvElement(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{{From: "style", Kind: KindFlagEquals, Flag: "--style"}}},
	}
	plan, err := a.Plan(map[string]any{"style": "plain"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Argv) != 1 || plan.Argv[0] != "--style=plain" {
		t.Fatalf("argv = %q, want [\"--style=plain\"]", plan.Argv)
	}
}

func TestBooleanFlagIsOmittedWhenFalse(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{{From: "numbered", Kind: KindBooleanFlag, Flag: "--number"}}},
	}
	off, err := a.Plan(map[string]any{"numbered": false})
	if err != nil {
		t.Fatal(err)
	}
	if len(off.Argv) != 0 {
		t.Fatalf("a false boolean must emit nothing, got %q", off.Argv)
	}
	on, err := a.Plan(map[string]any{"numbered": true})
	if err != nil {
		t.Fatal(err)
	}
	if len(on.Argv) != 1 || on.Argv[0] != "--number" {
		t.Fatalf("a true boolean must emit the flag alone, got %q", on.Argv)
	}
}

func TestRepeatedFlagEmitsOnePairPerItem(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{{From: "include", Kind: KindRepeatedFlag, Flag: "--include"}}},
	}
	plan, err := a.Plan(map[string]any{"include": []string{"*.go", "*.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(plan.Argv, " "); got != "--include *.go --include *.md" {
		t.Fatalf("argv = %q", got)
	}
}

func TestRepeatedFlagRejectsNonArray(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{{From: "include", Kind: KindRepeatedFlag, Flag: "--include"}}},
	}
	if _, err := a.Plan(map[string]any{"include": "*.go"}); err == nil {
		t.Fatal("a repeated flag must reject a scalar rather than silently accept it")
	}
}

func TestStdinBindingSendsValueRaw(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{
			Argv:  []Binding{{From: "filter", Kind: KindPositional, Position: 0, Required: true}},
			Stdin: &Binding{From: "input"},
		},
	}
	plan, err := a.Plan(map[string]any{"filter": ".name", "input": map[string]any{"name": "Ada"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Argv) != 1 || plan.Argv[0] != ".name" {
		t.Fatalf("argv = %q", plan.Argv)
	}
	// jq-style tools need real JSON on stdin, not a stringified object.
	if !strings.Contains(string(plan.Stdin), `"name":"Ada"`) {
		t.Fatalf("stdin = %q, want structured JSON preserved", plan.Stdin)
	}
}

func TestLiteralBindingEmitsConstant(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{
			{Kind: KindLiteral, Value: "subcommand"},
			{From: "path", Kind: KindPositional, Position: 0, Required: true},
		}},
	}
	plan, err := a.Plan(map[string]any{"path": "/tmp/x"})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(plan.Argv, ","); got != "subcommand,/tmp/x" {
		t.Fatalf("argv = %q", got)
	}
}

func TestWorkingDirBindingSetsDirectory(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{
			Argv:       []Binding{{From: "path", Kind: KindPositional, Position: 0, Required: true}},
			WorkingDir: &Binding{From: "dir"},
		},
	}
	plan, err := a.Plan(map[string]any{"path": "x", "dir": "/tmp/work"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.WorkingDir != "/tmp/work" {
		t.Fatalf("working dir = %q", plan.WorkingDir)
	}
}

// --- argument safety --------------------------------------------------------

// A caller must never be able to turn a data value into shell syntax. These
// values are the classic injection payloads; each must arrive as one literal
// argv element.
func TestShellMetacharactersRemainData(t *testing.T) {
	hostile := []string{
		"; rm -rf /",
		"$(touch /tmp/pwn)",
		"`whoami`",
		"a && b",
		"a | b",
		"a > /tmp/out",
		"a\nb",
		"'; DROP TABLE users; --",
	}
	for _, value := range hostile {
		a := &Adapter{
			Kind: "cli", OutputMode: OutputText, OutputField: "text",
			Confidence: ConfidenceHigh,
			Invocation: Invocation{Argv: []Binding{
				{From: "path", Kind: KindPositional, Position: 0, Required: true, AllowDashLeading: true},
			}},
		}
		plan, err := a.Plan(map[string]any{"path": value})
		if err != nil {
			continue // rejecting is also a safe outcome
		}
		if len(plan.Argv) != 1 {
			t.Fatalf("value %q produced %d argv elements, want exactly 1", value, len(plan.Argv))
		}
		if plan.Argv[0] != value {
			t.Fatalf("value %q was transformed into %q; it must be passed through verbatim", value, plan.Argv[0])
		}
	}
}

// A value that looks like a flag must not be silently reinterpreted as one in a
// positional slot. The mapping either refuses it or opts in explicitly.
func TestDashLeadingValueIsRejectedInPositionalByDefault(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{
			{From: "path", Kind: KindPositional, Position: 0, Required: true},
		}},
	}
	if _, err := a.Plan(map[string]any{"path": "--unexpected-flag"}); err == nil {
		t.Fatal("a dash-leading positional value must be rejected unless the mapping opts in")
	}
}

func TestEndOfOptionsDelimiterIsEmittedWhenDeclared(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{
			{From: "path", Kind: KindPositional, Position: 0, Required: true, EndOfOptions: true, AllowDashLeading: true},
		}},
	}
	plan, err := a.Plan(map[string]any{"path": "-weird-name"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Argv) != 2 || plan.Argv[0] != "--" || plan.Argv[1] != "-weird-name" {
		t.Fatalf("argv = %q, want a -- delimiter before the value", plan.Argv)
	}
}

func TestNewlineInFlagValueIsRejected(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{{From: "word", Kind: KindFlag, Flag: "--word"}}},
	}
	if _, err := a.Plan(map[string]any{"word": "a\nb"}); err == nil {
		t.Fatal("a newline in a flag value must be rejected")
	}
}

func TestPathLikeValueIsPassedThroughUnchanged(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{
			{From: "path", Kind: KindPositional, Position: 0, Required: true},
		}},
	}
	// A traversal-looking path is still just data: refusing it would be a
	// correctness bug, not a safety win. Containment is the caller's decision.
	plan, err := a.Plan(map[string]any{"path": "../../etc/passwd"})
	if err != nil {
		t.Fatalf("a path value must not be rejected on shape alone: %v", err)
	}
	if plan.Argv[0] != "../../etc/passwd" {
		t.Fatalf("argv = %q", plan.Argv)
	}
}

// --- validation -------------------------------------------------------------

func TestMissingRequiredFieldIsRejected(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{
			{From: "path", Kind: KindPositional, Position: 0, Required: true},
		}},
	}
	_, err := a.Plan(map[string]any{})
	if err == nil {
		t.Fatal("a missing required field must be rejected")
	}
	var ve *ValidationError
	if !asValidationError(err, &ve) || ve.Field != "path" {
		t.Fatalf("error = %v, want a validation error naming the field", err)
	}
}

func TestOversizedValueIsRejected(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{
			{From: "path", Kind: KindPositional, Position: 0, Required: true},
		}},
	}
	huge := strings.Repeat("a", (1<<20)+1)
	if _, err := a.Plan(map[string]any{"path": huge}); err == nil {
		t.Fatal("an oversized value must be rejected before execution")
	}
}

func TestUnboundFieldsAreReported(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{
			{From: "path", Kind: KindPositional, Position: 0, Required: true},
		}},
	}
	plan, err := a.Plan(map[string]any{"path": "/tmp/x", "typo": "value"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Omitted) != 1 || plan.Omitted[0] != "typo" {
		t.Fatalf("omitted = %q, want the unbound field reported", plan.Omitted)
	}
}

// --- validation of the mapping itself ---------------------------------------

func TestValidateRejectsUnsupportedBindingKind(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{{From: "path", Kind: "shell_template"}}},
	}
	if err := a.Validate(); err == nil {
		t.Fatal("an unsupported binding kind must be rejected")
	}
}

func TestValidateRejectsFlagWithoutFlagName(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{{From: "style", Kind: KindFlag}}},
	}
	if err := a.Validate(); err == nil {
		t.Fatal("a flag binding with no flag must be rejected")
	}
}

func TestValidateRejectsDuplicatePositionalSlots(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{
			{From: "a", Kind: KindPositional, Position: 0},
			{From: "b", Kind: KindPositional, Position: 0},
		}},
	}
	if err := a.Validate(); err == nil {
		t.Fatal("two bindings claiming the same positional must be rejected")
	}
}

func TestValidateRejectsMismatchedOutputField(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "result",
		Confidence: ConfidenceHigh,
		Invocation: Invocation{Argv: []Binding{{From: "path", Kind: KindPositional, Position: 0}}},
	}
	if err := a.Validate(); err == nil {
		t.Fatal("text output must use the field 'text', otherwise the result key lies")
	}
}

// --- confidence policy ------------------------------------------------------

func TestLowConfidenceMappingIsNotRunnable(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceLow,
		Invocation: Invocation{Argv: []Binding{{From: "path", Kind: KindPositional, Position: 0}}},
	}
	if a.IsRunnable() {
		t.Fatal("a low-confidence mapping must never be marked runnable")
	}
	if err := a.Validate(); err != nil {
		t.Fatalf("a low-confidence mapping is still structurally valid: %v", err)
	}
}

func TestMediumConfidenceMappingIsRunnable(t *testing.T) {
	a := &Adapter{
		Kind: "cli", OutputMode: OutputText, OutputField: "text",
		Confidence: ConfidenceMedium,
		Invocation: Invocation{Argv: []Binding{{From: "path", Kind: KindPositional, Position: 0}}},
	}
	if !a.IsRunnable() {
		t.Fatal("medium confidence clears the documented threshold")
	}
}

func asValidationError(err error, target **ValidationError) bool {
	ve, ok := err.(*ValidationError)
	if ok {
		*target = ve
	}
	return ok
}
