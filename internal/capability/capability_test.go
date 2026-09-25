package capability

import (
	"testing"

	"github.com/M4G3LL4N0/grokinstall/internal/evidence"
)

func evidenceSet(items ...evidence.Item) *evidence.Set {
	s := evidence.New()
	for _, it := range items {
		s.AddItem(it)
	}
	return s
}

func TestDiscoverCLIFromEntrypoint(t *testing.T) {
	caps := Discover(evidenceSet(evidence.Item{Finding: "cli_entrypoint", Confidence: evidence.ConfidenceHigh, Evidence: []string{"package.json bin field: widget"}}))
	if len(caps) != 1 {
		t.Fatalf("caps = %d, want 1", len(caps))
	}
	if caps[0].Kind != KindCLI {
		t.Fatalf("kind = %q, want cli", caps[0].Kind)
	}
	if !caps[0].Usable {
		t.Fatal("a detected CLI entrypoint is usable in Part 1")
	}
}

func TestDiscoverAPIFromOpenAPI(t *testing.T) {
	caps := Discover(evidenceSet(evidence.Item{Finding: "openapi_spec", Confidence: evidence.ConfidenceHigh, Evidence: []string{"openapi.json: 3 paths"}}))
	if len(caps) != 1 || caps[0].Kind != KindAPI {
		t.Fatalf("caps = %+v, want one api capability", caps)
	}
	if caps[0].Usable {
		t.Fatal("API detection is not runtime-installable in Part 1")
	}
}

func TestDiscoverMultiple(t *testing.T) {
	caps := Discover(evidenceSet(
		evidence.Item{Finding: "cli_entrypoint", Confidence: evidence.ConfidenceHigh},
		evidence.Item{Finding: "docker_image", Confidence: evidence.ConfidenceHigh},
		evidence.Item{Finding: "documentation", Confidence: evidence.ConfidenceMedium},
	))
	if len(caps) != 3 {
		t.Fatalf("caps = %d, want 3", len(caps))
	}
	seen := map[Kind]bool{}
	for _, c := range caps {
		seen[c.Kind] = true
	}
	for _, k := range []Kind{KindCLI, KindDocker, KindKnowledge} {
		if !seen[k] {
			t.Fatalf("missing capability kind %q", k)
		}
	}
}

func TestDiscoverNoEvidenceReturnsEmpty(t *testing.T) {
	if caps := Discover(evidence.New()); len(caps) != 0 {
		t.Fatalf("caps = %+v, want none", caps)
	}
}

func TestFindByKind(t *testing.T) {
	caps := Discover(evidenceSet(
		evidence.Item{Finding: "cli_entrypoint", Confidence: evidence.ConfidenceHigh},
		evidence.Item{Finding: "openapi_spec", Confidence: evidence.ConfidenceHigh},
	))
	if c, ok := Find(caps, KindAPI); !ok || c.Kind != KindAPI {
		t.Fatal("expected to find api capability")
	}
	if _, ok := Find(caps, KindMCP); ok {
		t.Fatal("must not fabricate mcp capability")
	}
}
