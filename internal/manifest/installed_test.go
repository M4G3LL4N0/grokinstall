package manifest

import (
	"strings"
	"testing"
)

// installed builds a realistic installed manifest.
func installed() *Manifest {
	return &Manifest{
		Schema:    SchemaID,
		Name:      "widget.search",
		Version:   "1",
		Source:    "path:/tmp/widget",
		Goal:      "search the widget repository",
		Strategy:  "cli_bridge",
		Support:   SupportReady,
		Status:    "ready",
		Execution: Execution{Type: "subprocess", Command: "widget", Supported: true, TimeoutMs: 30000},
		Input:     Schema{Fields: []Field{{Name: "query", Type: "string", Required: true}}},
		Output:    Schema{Fields: []Field{{Name: "matches", Type: "array"}}},
		Grokbot:   Grokbot{UseWhen: "the user wants to search the widget", DoNot: "load the repository first"},
	}
}

func TestInstalledSchemaHasRequiredSections(t *testing.T) {
	data := mustJSON(t, installed())
	for _, key := range []string{"schema", "name", "version", "source", "goal", "strategy",
		"execution", "input", "output", "grokbot", "cache", "security", "provenance"} {
		if !strings.Contains(data, `"`+key+`"`) {
			t.Fatalf("manifest JSON missing %q:\n%s", key, data)
		}
	}
}

func mustJSON(t *testing.T, m *Manifest) string {
	t.Helper()
	data, err := m.JSON()
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestExecutionCarriesRuntimeBounds(t *testing.T) {
	m := installed()
	m.Execution.Args = []string{"--format", "json"}
	m.Execution.WorkingDir = "/tmp"
	m.Execution.Env = map[string]string{"TOKEN": "x"}
	if m.Execution.TimeoutMs != 30000 {
		t.Fatal("timeout must be carried in the manifest")
	}
	data := mustJSON(t, m)
	for _, key := range []string{"type", "command", "args", "cwd", "timeout_ms"} {
		if !strings.Contains(data, `"`+key+`"`) {
			t.Fatalf("execution missing %q:\n%s", key, data)
		}
	}
}

func TestValidateRejectsExecutableClaimWithoutCommand(t *testing.T) {
	m := installed()
	m.Execution.Supported = true
	m.Execution.Command = ""
	if err := m.Validate(); err == nil {
		t.Fatal("a capability cannot claim execution support without a command")
	}
}

func TestValidateRejectsSubprocessWithoutCommand(t *testing.T) {
	m := installed()
	m.Execution.Type = "subprocess"
	m.Execution.Command = ""
	m.Execution.Supported = false
	if err := m.Validate(); err == nil {
		t.Fatal("a subprocess execution without a command is invalid")
	}
}

func TestValidateAcceptsBuiltinWithoutCommand(t *testing.T) {
	m := installed()
	m.Execution.Type = "builtin"
	m.Execution.Handler = "knowledge"
	m.Execution.Command = ""
	m.Execution.Supported = true
	if err := m.Validate(); err != nil {
		t.Fatalf("builtin capabilities need no command: %v", err)
	}
}

func TestValidateAcceptsPlanOnly(t *testing.T) {
	m := installed()
	m.Strategy = "api_bridge"
	m.Support = SupportPlanOnly
	m.Execution.Type = "none"
	m.Execution.Command = ""
	m.Execution.Supported = false
	if err := m.Validate(); err != nil {
		t.Fatalf("plan-only manifests must be valid: %v", err)
	}
}

func TestValidateRejectsUnknownSupport(t *testing.T) {
	m := installed()
	m.Support = "kinda"
	if err := m.Validate(); err == nil {
		t.Fatal("unknown support level must be rejected")
	}
}

func TestValidateRejectsInputFieldWithoutName(t *testing.T) {
	m := installed()
	m.Input.Fields = []Field{{Name: "", Type: "string"}}
	if err := m.Validate(); err == nil {
		t.Fatal("input fields need names")
	}
}

func TestContractUsesRunCommand(t *testing.T) {
	m := installed()
	text := m.ContractText()
	if !strings.Contains(text, "grokinstall run widget.search") {
		t.Fatalf("contract must call grokinstall run:\n%s", text)
	}
	if !strings.Contains(text, "--input") {
		t.Fatalf("contract must show the input mechanism:\n%s", text)
	}
}

func TestContractListsInputAndOutputFields(t *testing.T) {
	text := installed().ContractText()
	if !strings.Contains(text, "INPUT") || !strings.Contains(text, "query") {
		t.Fatalf("contract missing input section:\n%s", text)
	}
	if !strings.Contains(text, "OUTPUT") || !strings.Contains(text, "matches") {
		t.Fatalf("contract missing output section:\n%s", text)
	}
}

func TestContractIsBounded(t *testing.T) {
	m := installed()
	for i := 0; i < 200; i++ {
		m.Input.Fields = append(m.Input.Fields, Field{Name: "f", Type: "string"})
		m.Output.Fields = append(m.Output.Fields, Field{Name: "g", Type: "string"})
	}
	if len(m.ContractText()) > 8192 {
		t.Fatalf("contract must stay small even with many fields: %d bytes", len(m.ContractText()))
	}
}

func TestContractNeverLeaksSourceDetails(t *testing.T) {
	m := installed()
	m.Provenance.Evidence = nil
	m.Source = "path:/tmp/widget"
	text := m.ContractText()
	if strings.Contains(text, "/tmp/widget") {
		t.Fatalf("contract leaked a local path:\n%s", text)
	}
	if strings.Contains(text, "package.json") || strings.Contains(text, "README") {
		t.Fatalf("contract leaked implementation detail:\n%s", text)
	}
}

func TestContractForPlanOnlyIsHonest(t *testing.T) {
	m := installed()
	m.Support = SupportPlanOnly
	m.Execution.Type = "none"
	m.Execution.Command = ""
	m.Execution.Supported = false
	text := m.ContractText()
	if !strings.Contains(text, "not executable") && !strings.Contains(text, "plan") {
		t.Fatalf("plan-only contract must say it cannot run:\n%s", text)
	}
}

func TestContractForBuiltin(t *testing.T) {
	m := installed()
	m.Strategy = "knowledge_import"
	m.Execution.Type = "builtin"
	m.Execution.Handler = "knowledge"
	m.Execution.Command = ""
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	text := m.ContractText()
	if !strings.Contains(text, "grokinstall run") {
		t.Fatalf("builtin capabilities are still invoked through run:\n%s", text)
	}
}

func TestRoundTripThroughJSON(t *testing.T) {
	m := installed()
	m.Execution.Env = map[string]string{"A": "b"}
	data, err := m.JSON()
	if err != nil {
		t.Fatal(err)
	}
	back, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if back.Name != m.Name || back.Strategy != m.Strategy || back.Support != m.Support {
		t.Fatalf("round trip lost data: %+v", back)
	}
	if back.Execution.Env["A"] != "b" {
		t.Fatalf("round trip lost the environment: %+v", back.Execution.Env)
	}
	if back.Input.Fields[0].Name != "query" {
		t.Fatal("round trip lost the input schema")
	}
}

func TestParseRejectsWrongSchema(t *testing.T) {
	if _, err := Parse([]byte(`{"schema":"other/v1","name":"x","source":"y"}`)); err == nil {
		t.Fatal("expected schema rejection")
	}
}
