package evidence

import (
	"strconv"
	"strings"
	"testing"
)

func TestAddAndList(t *testing.T) {
	s := New()
	s.Add("cli_available", ConfidenceHigh, "package.json bin field", "documented --json option")
	s.Add("openapi_spec", ConfidenceMedium, "openapi.yaml present")
	items := s.List()
	if len(items) != 2 {
		t.Fatalf("len = %d, want 2", len(items))
	}
	if items[0].Finding != "cli_available" {
		t.Fatalf("finding = %q", items[0].Finding)
	}
	if len(items[0].Evidence) != 2 {
		t.Fatalf("evidence len = %d, want 2", len(items[0].Evidence))
	}
	if items[1].Finding != "openapi_spec" {
		t.Fatalf("finding = %q", items[1].Finding)
	}
}

func TestHasAndGet(t *testing.T) {
	s := New()
	s.Add("openapi_spec", ConfidenceMedium)
	if !s.Has("openapi_spec") {
		t.Fatal("expected openapi_spec present")
	}
	if s.Has("mcp_config") {
		t.Fatal("unexpected mcp_config")
	}
}

func TestConfidenceEscalation(t *testing.T) {
	s := New()
	s.Add("docker_available", ConfidenceLow)
	s.Add("docker_available", ConfidenceHigh)
	got := s.Get("docker_available")
	if got.Confidence != ConfidenceHigh {
		t.Fatalf("confidence = %q, want high (max should win)", got.Confidence)
	}
}

func TestEvidenceDeDuplicationByKey(t *testing.T) {
	s := New()
	s.Add("x", ConfidenceHigh, "a", "b")
	s.Add("x", ConfidenceHigh, "b", "a")
	items := s.List()
	if len(items) != 1 {
		t.Fatalf("dedupe failed: %d items", len(items))
	}
	if len(items[0].Evidence) != 2 {
		t.Fatalf("evidence entries duplicated: %v", items[0].Evidence)
	}
}

func TestMerge(t *testing.T) {
	a := New()
	a.Add("one", ConfidenceHigh)
	b := New()
	b.Add("two", ConfidenceMedium)
	a.Merge(b)
	if !a.Has("two") {
		t.Fatal("merged set missing item")
	}
	if !a.Has("one") {
		t.Fatal("merge must not drop existing items")
	}
}

func TestEvidencePerFindingIsBounded(t *testing.T) {
	s := New()
	for i := 0; i < 100; i++ {
		s.Add("documentation", ConfidenceMedium, "docs/file"+itoa(i)+".md")
	}
	item := s.Get("documentation")
	if len(item.Evidence) > MaxEvidencePerFinding+1 {
		t.Fatalf("evidence list is unbounded: %d entries", len(item.Evidence))
	}
	joined := ""
	for _, e := range item.Evidence {
		joined += e
	}
	if !strings.Contains(joined, "more") {
		t.Fatalf("truncated evidence should say how much was omitted: %v", item.Evidence)
	}
}

func itoa(i int) string {
	return strconv.Itoa(i)
}

func TestJSONMarshalRoundTrip(t *testing.T) {
	s := New()
	c := New()
	s.Add("cli_entrypoint", ConfidenceHigh, "package.json bin")
	c.Add("readme_text", ConfidenceLow)
	s.Merge(c)
	data, err := s.JSON()
	if err != nil {
		t.Fatal(err)
	}
	back, err := FromJSON(data)
	if err != nil {
		t.Fatal(err)
	}
	if !back.Has("cli_entrypoint") || !back.Has("readme_text") {
		t.Fatalf("round trip lost items: %+v", back.List())
	}
}
