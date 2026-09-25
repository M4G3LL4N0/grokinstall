package runtime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/M4G3LL4N0/grokinstall/internal/adapter"
)

// argPrinter writes every argument it receives on its own line and ignores
// stdin entirely, so a test can assert on the exact argv a mapping produced.
func argPrinter(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "printer")
	body := "#!/bin/sh\nfor a in \"$@\"; do printf 'ARGV:%s\\n' \"$a\"; done\n"
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func adaptedSpec(command string, a *Adapter) Spec {
	return Spec{
		Type:           TypeSubprocess,
		Command:        command,
		Adapter:        a,
		OutputMode:     a.OutputMode,
		OutputField:    a.OutputField,
		Capability:     "demo.view",
		Timeout:        15 * time.Second,
		MaxOutputBytes: 1 << 20,
	}
}

func textAdapter() *Adapter {
	return &Adapter{
		Kind:        "cli",
		OutputMode:  "text",
		OutputField: "text",
		Operation:   "view",
		Argv: []Binding{
			{From: "path", Kind: "positional", Position: 0, Required: true},
			{From: "style", Kind: "flag", Flag: "--style"},
		},
	}
}

// --- argv translation -------------------------------------------------------

func TestAdaptedInvocationTranslatesInputToArgv(t *testing.T) {
	cmd := argPrinter(t)
	rt := New()
	resp, err := rt.Invoke(t.Context(), adaptedSpec(cmd, textAdapter()),
		json.RawMessage(`{"path":"/tmp/x","style":"plain"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("invocation failed: %+v", resp.Error)
	}
	encoded, _ := json.Marshal(resp.Result)
	out := string(encoded)
	if !strings.Contains(out, "ARGV:--style") || !strings.Contains(out, "ARGV:plain") {
		t.Fatalf("the flag binding did not produce argv elements: %s", out)
	}
	if !strings.Contains(out, "ARGV:/tmp/x") {
		t.Fatalf("the positional binding did not produce an argv element: %s", out)
	}
	// The result must be the tool's output, not the request.
	if strings.Contains(out, `"path"`) {
		t.Fatalf("result echoed the request instead of the tool's output: %s", out)
	}
}

// --- input validation before any process is spawned -------------------------

func TestAdaptedInvocationRejectsMissingRequiredField(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "spawned")
	script := "#!/bin/sh\ntouch " + marker + "\n"
	tool := filepath.Join(t.TempDir(), "toucher")
	if err := os.WriteFile(tool, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	rt := New()
	resp, err := rt.Invoke(t.Context(), adaptedSpec(tool, textAdapter()), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("a missing required field must be rejected")
	}
	if resp.Error == nil || resp.Error.Code != CodeInvalidInput {
		t.Fatalf("error = %+v, want invalid_input", resp.Error)
	}
	if resp.Error.Component != "input" {
		t.Fatalf("component = %q, want input", resp.Error.Component)
	}
	// The critical property: validation happens before execution.
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the subprocess was spawned despite invalid input")
	}
}

func TestAdaptedInvocationRejectsUnknownField(t *testing.T) {
	rt := New()
	resp, err := rt.Invoke(t.Context(), adaptedSpec(argPrinter(t), textAdapter()),
		json.RawMessage(`{"path":"/tmp/x","surprise":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("an unknown field must be rejected, not ignored")
	}
	if resp.Error == nil || !strings.Contains(resp.Error.Message, "surprise") {
		t.Fatalf("error should name the unknown field: %+v", resp.Error)
	}
}

func TestAdaptedInvocationRejectsNonObjectInput(t *testing.T) {
	rt := New()
	resp, err := rt.Invoke(t.Context(), adaptedSpec(argPrinter(t), textAdapter()),
		json.RawMessage(`"a string"`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK || resp.Error == nil || resp.Error.Code != CodeInvalidInput {
		t.Fatalf("a non-object input must be invalid_input, got %+v", resp.Error)
	}
}

func TestAdaptedInvocationRejectsOversizedValue(t *testing.T) {
	rt := New()
	huge, _ := json.Marshal(strings.Repeat("a", (1<<20)+8))
	resp, err := rt.Invoke(t.Context(), adaptedSpec(argPrinter(t), textAdapter()),
		json.RawMessage(`{"path":`+string(huge)+`}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("an oversized value must be rejected before execution")
	}
}

func TestAdaptedInvocationRejectsInvalidAdapter(t *testing.T) {
	rt := New()
	bad := textAdapter()
	bad.OutputField = "" // a result key can never be guaranteed
	resp, err := rt.Invoke(t.Context(), adaptedSpec(argPrinter(t), bad), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("an invalid mapping must not execute")
	}
	if resp.Error == nil || resp.Error.Code != CodeInvalidAdapter {
		t.Fatalf("error = %+v, want invalid_adapter", resp.Error)
	}
	if resp.Error.RecommendedAction == "" {
		t.Fatal("an invalid mapping should tell the caller what to do")
	}
}

// --- output normalization ---------------------------------------------------

func TestAdaptedTextOutputIsExposedAsText(t *testing.T) {
	tool := filepath.Join(t.TempDir(), "printer")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf 'hello from tool\\n'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rt := New()
	resp, err := rt.Invoke(t.Context(), adaptedSpec(tool, textAdapter()),
		json.RawMessage(`{"path":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("failed: %+v", resp.Error)
	}
	m, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result = %T", resp.Result)
	}
	if m["text"] != "hello from tool\n" {
		t.Fatalf("text = %q, want the tool's output verbatim", m["text"])
	}
}

func TestAdaptedJSONOutputIsPreserved(t *testing.T) {
	tool := filepath.Join(t.TempDir(), "emitter")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nprintf '{\"name\":\"Ada\",\"n\":37}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := textAdapter()
	a.OutputMode = "json"
	a.OutputField = "result"
	rt := New()
	spec := adaptedSpec(tool, a)
	spec.ExpectJSON = true
	resp, err := rt.Invoke(t.Context(), spec, json.RawMessage(`{"path":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("failed: %+v", resp.Error)
	}
	m := resp.Result.(map[string]any)
	if m["name"] != "Ada" {
		t.Fatalf("structured output was not preserved: %+v", m)
	}
}

func TestAdaptedExitStatusOutputToleratesNonZeroExit(t *testing.T) {
	tool := filepath.Join(t.TempDir(), "failer")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 3\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{
		Kind: "cli", OutputMode: adapter.OutputExitStatus, OutputField: "exit_code",
		Argv: []Binding{{From: "path", Kind: "positional", Position: 0, Required: true}},
	}
	rt := New()
	resp, err := rt.Invoke(t.Context(), adaptedSpec(tool, a), json.RawMessage(`{"path":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("an exit-status mapping treats the status as a result, not a failure: %+v", resp.Error)
	}
	if resp.Result.(map[string]any)["exit_code"] != 3 {
		t.Fatalf("result = %+v", resp.Result)
	}
}

func TestAdaptedNonZeroExitIsStillAFailureForTextOutput(t *testing.T) {
	tool := filepath.Join(t.TempDir(), "failer")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\necho bad >&2\nexit 2\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	rt := New()
	resp, err := rt.Invoke(t.Context(), adaptedSpec(tool, textAdapter()), json.RawMessage(`{"path":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("a non-zero exit must be a failure for a text-output mapping")
	}
	if resp.Error == nil || resp.Error.Code != CodeNonZeroExit {
		t.Fatalf("error = %+v, want nonzero_exit", resp.Error)
	}
}

// --- argument safety at the runtime boundary --------------------------------

// A hostile value must arrive as exactly one argv element. There is no shell
// anywhere in this path, so there is nothing for it to escape from.
func TestAdaptedHostileValuesStayData(t *testing.T) {
	canary := filepath.Join(t.TempDir(), "pwned")
	hostile := []string{
		"; touch " + canary,
		"$(touch " + canary + ")",
		"`touch " + canary + "`",
		"&& touch " + canary,
		"| touch " + canary,
		"x > " + canary,
		"' " + canary,
	}
	for _, value := range hostile {
		rt := New()
		resp, err := rt.Invoke(t.Context(), adaptedSpec(argPrinter(t), textAdapter()),
			json.RawMessage(`{"path":`+mustJSON(value)+`}`))
		if err != nil {
			t.Fatal(err)
		}
		// Rejection is acceptable; execution is not.
		if _, statErr := os.Stat(canary); statErr == nil {
			t.Fatalf("value %q was executed: the canary exists", value)
		}
		if resp.OK {
			// Compare the decoded text, not the JSON encoding: the envelope
			// escapes characters like & and <, which is correct and unrelated to
			// whether the value survived intact.
			m, _ := resp.Result.(map[string]any)
			text, _ := m["text"].(string)
			if !strings.Contains(text, value) {
				t.Fatalf("value %q was not passed through verbatim: %q", value, text)
			}
		}
	}
}

func TestAdaptedDashLeadingPositionalIsRejected(t *testing.T) {
	rt := New()
	resp, err := rt.Invoke(t.Context(), adaptedSpec(argPrinter(t), textAdapter()),
		json.RawMessage(`{"path":"--version"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("a dash-leading positional must be rejected so it cannot be read as a flag")
	}
}

func TestAdaptedStdinCarriesStructuredJSON(t *testing.T) {
	tool := filepath.Join(t.TempDir(), "reader")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\ncat\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	a := &Adapter{
		Kind: "cli", OutputMode: "text", OutputField: "text",
		Argv:  []Binding{{From: "filter", Kind: "positional", Position: 0, Required: true, AllowDashLeading: true}},
		Stdin: &Binding{From: "input"},
	}
	rt := New()
	resp, err := rt.Invoke(t.Context(), adaptedSpec(tool, a),
		json.RawMessage(`{"filter":".name","input":{"name":"Ada"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("failed: %+v", resp.Error)
	}
	m := resp.Result.(map[string]any)
	if !strings.Contains(m["text"].(string), `"name":"Ada"`) {
		t.Fatalf("stdin did not carry structured JSON: %q", m["text"])
	}
}

// Bounds that existed before this pass must keep working.
func TestAdaptedTimeoutAndOutputBoundsStillApply(t *testing.T) {
	slow := filepath.Join(t.TempDir(), "slow")
	if err := os.WriteFile(slow, []byte("#!/bin/sh\nsleep 10\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec := adaptedSpec(slow, textAdapter())
	spec.Timeout = 300 * time.Millisecond
	resp, err := New().Invoke(t.Context(), spec, json.RawMessage(`{"path":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp.OK || resp.Error == nil || resp.Error.Code != CodeTimeout {
		t.Fatalf("timeout must still be enforced, got %+v", resp.Error)
	}

	loud := filepath.Join(t.TempDir(), "loud")
	if err := os.WriteFile(loud, []byte("#!/bin/sh\nwhile true; do printf 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA\\n'; done\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	spec2 := adaptedSpec(loud, textAdapter())
	spec2.MaxOutputBytes = 2048
	resp2, err := New().Invoke(t.Context(), spec2, json.RawMessage(`{"path":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if resp2.OK {
		t.Fatal("an output flood must be bounded")
	}
}

func mustJSON(v any) string {
	encoded, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
