package manifest

import (
	"encoding/json"
	"strings"
	"testing"
)

func minimal() *Manifest {
	return &Manifest{
		Schema:  SchemaID,
		Name:    "widget",
		Version: "1",
		Source:  "local:/tmp/widget",
	}
}

func TestSchemaIdentifier(t *testing.T) {
	if SchemaID != "grokinstall/v1" {
		t.Fatalf("schema = %q, want grokinstall/v1", SchemaID)
	}
}

func TestRequiredSectionsArePresent(t *testing.T) {
	data, err := json.Marshal(minimal())
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"schema", "name", "version", "source", "goal", "capabilities", "execution",
		"inputs", "outputs", "grokbot", "cache", "security", "provenance",
	} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("manifest JSON missing %q", key)
		}
	}
}

func TestJSONRoundTrip(t *testing.T) {
	m := minimal()
	m.Goal = "diagnose widget"
	m.Capabilities = []string{"widget.diagnose"}
	m.Execution = Execution{Strategy: "cli_bridge", Command: "widget", JSONIO: true}
	m.Grokbot = Grokbot{UseWhen: "a widget diagnosis is requested", DoNot: "load the repository first"}
	m.Provenance = Provenance{Source: "local:/tmp/widget", Identity: "local:/tmp/widget@abc", CommitSHA: "abc123"}

	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var back Manifest
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Goal != m.Goal || back.Execution.Command != "widget" || back.Provenance.CommitSHA != "abc123" {
		t.Fatalf("round trip lost data: %+v", back)
	}
}

func TestValidateRejectsMissingSchema(t *testing.T) {
	m := minimal()
	m.Schema = ""
	if err := m.Validate(); err == nil {
		t.Fatal("expected validation error for missing schema")
	}
}

func TestValidateRejectsEmptyName(t *testing.T) {
	m := minimal()
	m.Name = ""
	if err := m.Validate(); err == nil {
		t.Fatal("expected validation error for empty name")
	}
}

func TestValidateAcceptsMinimal(t *testing.T) {
	if err := minimal().Validate(); err != nil {
		t.Fatalf("minimal manifest should validate: %v", err)
	}
}

func TestContractTextKeepsGrokBotSmall(t *testing.T) {
	m := minimal()
	m.Grokbot = Grokbot{
		UseWhen:   "the user wants widget diagnostics",
		DoNot:     "load the widget repository into GrokBot",
		OnFailure: "run grokinstall doctor",
	}
	text := m.ContractText()
	for _, want := range []string{"USE WHEN", "DO NOT", "ON FAILURE"} {
		if !strings.Contains(text, want) {
			t.Fatalf("contract missing %q:\n%s", want, text)
		}
	}
	if len(text) > 2000 {
		t.Fatalf("contract should stay small, got %d bytes", len(text))
	}
}

func TestValidateRejectsInvalidName(t *testing.T) {
	m := minimal()
	m.Name = "not a valid name!"
	if err := m.Validate(); err == nil {
		t.Fatal("expected validation error for an invalid capability name")
	}
}
