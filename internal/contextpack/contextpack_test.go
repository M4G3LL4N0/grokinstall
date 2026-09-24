package contextpack

import (
	"encoding/json"
	"strings"
	"testing"

	"grokinstall/internal/evidence"
	"grokinstall/internal/source"
)

func TestPackContainsOnlyAllowedSections(t *testing.T) {
	pack := &Pack{Goal: "diagnose a repository"}
	data, err := json.Marshal(pack)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"schema": true, "goal": true, "source": true, "source_metadata": true,
		"manifest_evidence": true, "capabilities": true, "unresolved_questions": true, "constraints": true,
	}
	for k := range decoded {
		if !allowed[k] {
			t.Fatalf("context pack leaked section %q", k)
		}
	}
}

func TestNeverCarriesRawSourceFiles(t *testing.T) {
	pack := &Pack{Goal: "x"}
	data, _ := json.Marshal(pack)
	lowered := strings.ToLower(string(data))
	for _, forbidden := range []string{"contents", "body", "filecontents", "rawsource", "readme_text"} {
		if strings.Contains(lowered, forbidden) {
			t.Fatalf("context pack carries raw source material %q", forbidden)
		}
	}
}

func TestSourceMetadataBounded(t *testing.T) {
	pack := &Pack{
		SourceMetadata: map[string]string{
			"name":    "widget",
			"summary": strings.Repeat("a", 5000),
		},
	}
	data, _ := json.Marshal(pack)
	if len(data) > 2000 {
		t.Fatalf("unbounded metadata leaked into pack: %d bytes", len(data))
	}
}

func TestCapabilityEvidenceRetained(t *testing.T) {
	pack := &Pack{
		ManifestEvidence: []evidence.Item{{Finding: "cli_entrypoint", Confidence: evidence.ConfidenceHigh, Evidence: []string{"package.json bin"}}},
	}
	if len(pack.ManifestEvidence) != 1 {
		t.Fatal("evidence must survive into the pack")
	}
}

func TestSizeBytesReflectsSerializedPack(t *testing.T) {
	pack := &Pack{Goal: "do the thing", Source: &source.Source{Kind: source.KindLocalDir, Canonical: "path:/tmp/x"}}
	n := pack.SizeBytes()
	if n <= 0 {
		t.Fatal("size must be measured")
	}
	other := &Pack{Goal: strings.Repeat("x", n*2)}
	if other.SizeBytes() <= n {
		t.Fatal("larger pack should serialize larger")
	}
}
