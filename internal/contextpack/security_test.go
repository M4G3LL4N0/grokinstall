package contextpack

import (
	"encoding/json"
	"strings"
	"testing"

	"grokinstall/internal/evidence"
)

func TestContextPackRedactsSecretMetadata(t *testing.T) {
	pack := New().
		WithGoal("configure the tool").
		WithMetadata("description", "Set GITHUB_TOKEN=ghp_abcdefghijklmnopqrstuvwxyz0123 before running")
	data, err := pack.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "ghp_abcdefghijklmnopqrstuvwxyz0123") {
		t.Fatalf("a token leaked into the context pack:\n%s", data)
	}
	if !strings.Contains(string(data), "GITHUB_TOKEN") {
		t.Fatalf("the variable name should survive so the shape is preserved:\n%s", data)
	}
}

func TestContextPackRedactsSecretEvidence(t *testing.T) {
	pack := New().WithEvidence([]evidence.Item{{
		Finding:  "env_example_present",
		Evidence: []string{".env.example: export API_KEY=sk-abcdefghijklmnopqrstuvwxyz123456"},
	}})
	data, _ := pack.JSON()
	if strings.Contains(string(data), "sk-abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatalf("a key leaked through evidence:\n%s", data)
	}
}

func TestContextPackRedactsOnMarshalEvenWhenBuiltDirectly(t *testing.T) {
	// Bypass the builder: the guarantee must hold at serialization.
	pack := &Pack{Schema: Schema, SourceMetadata: map[string]string{
		"token": "ghp_abcdefghijklmnopqrstuvwxyz0123",
	}}
	data, _ := json.Marshal(pack)
	if strings.Contains(string(data), "ghp_") {
		t.Fatalf("a directly-built pack leaked a secret:\n%s", data)
	}
}

func TestContextPackRedactsConstraints(t *testing.T) {
	pack := &Pack{Schema: Schema, Constraints: []string{"do not use PASSWORD=hunter2hunter2 anywhere"}}
	data, _ := json.Marshal(pack)
	if strings.Contains(string(data), "hunter2hunter2") {
		t.Fatalf("a constraint leaked a secret:\n%s", data)
	}
}

func TestContextPackMentionsDocsWithoutCopyingSecrets(t *testing.T) {
	// A pack may say documentation exists without copying its content.
	pack := New().
		WithGoal("configure auth").
		WithMetadata("documentation", "README documents installation and configuration")
	pack.WithUnresolved([]string{"which credential store does the project expect?"})
	data, _ := pack.JSON()
	out := string(data)
	if !strings.Contains(out, "README documents installation") {
		t.Fatalf("the pack should describe the documentation:\n%s", out)
	}
	if strings.Contains(out, "export") {
		t.Fatalf("the pack must not copy documentation commands:\n%s", out)
	}
}

func TestContextPackStaysSmallWithRedaction(t *testing.T) {
	items := make([]evidence.Item, 0, 200)
	for i := 0; i < 200; i++ {
		items = append(items, evidence.Item{
			Finding:  "finding_" + strings.Repeat("x", i%5+1),
			Evidence: []string{"some evidence string that is reasonably long"},
		})
	}
	pack := New().WithEvidence(items)
	if pack.SizeBytes() > 32768 {
		t.Fatalf("a redacted pack should still be bounded: %d bytes", pack.SizeBytes())
	}
}
