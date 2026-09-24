package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeDocs(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestIndexIsBounded(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"a.md": strings.Repeat("alpha content. ", 5000),
		"b.md": strings.Repeat("beta content. ", 5000),
	})
	idx, err := Build(dir, Limits{MaxFileBytes: 4096, MaxFiles: 10, MaxTotalBytes: 6000})
	if err != nil {
		t.Fatal(err)
	}
	if idx.TotalBytes > 6000 {
		t.Fatalf("index exceeded its byte budget: %d", idx.TotalBytes)
	}
	if len(idx.Documents) == 0 {
		t.Fatal("index should retain bounded documents")
	}
	if len(idx.Documents[0].Text) > 4096 {
		t.Fatalf("document not bounded: %d bytes", len(idx.Documents[0].Text))
	}
}

func TestSearchFindsRelevantDocument(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"auth.md":   "Authentication uses a bearer token in the Authorization header.",
		"deploy.md": "Deployment uses containers and a compose file.",
	})
	idx, err := Build(dir, Limits{MaxFileBytes: 4096, MaxFiles: 10, MaxTotalBytes: 100000})
	if err != nil {
		t.Fatal(err)
	}
	res := idx.Search("bearer token", 3)
	if len(res.Matches) == 0 {
		t.Fatal("expected a match for a documented term")
	}
	if res.Matches[0].File != "auth.md" {
		t.Fatalf("top match = %q, want auth.md", res.Matches[0].File)
	}
	if res.Matches[0].Excerpt == "" {
		t.Fatal("match must carry an excerpt")
	}
}

func TestSearchRanksMostRelevantFirst(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"a.md": "The widget is a small tool.",
		"b.md": "The widget widget widget is described at length here in this file.",
	})
	idx, err := Build(dir, Limits{MaxFileBytes: 4096, MaxFiles: 10, MaxTotalBytes: 100000})
	if err != nil {
		t.Fatal(err)
	}
	res := idx.Search("widget widget", 2)
	if len(res.Matches) != 2 {
		t.Fatalf("matches = %d, want 2", len(res.Matches))
	}
	if res.Matches[0].File != "b.md" {
		t.Fatalf("relevance ordering wrong: %s first", res.Matches[0].File)
	}
}

func TestSearchIsDeterministic(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"a.md": "shared term",
		"b.md": "shared term",
		"c.md": "shared term",
	})
	idx, _ := Build(dir, Limits{MaxFileBytes: 4096, MaxFiles: 10, MaxTotalBytes: 100000})
	first := idx.Search("shared", 3)
	for i := 0; i < 5; i++ {
		again := idx.Search("shared", 3)
		for j := range first.Matches {
			if first.Matches[j].File != again.Matches[j].File {
				t.Fatal("search ordering is not deterministic")
			}
		}
	}
}

func TestSearchReturnsNoMatchForUnknownTerm(t *testing.T) {
	dir := writeDocs(t, map[string]string{"a.md": "alpha only"})
	idx, _ := Build(dir, Limits{MaxFileBytes: 4096, MaxFiles: 10, MaxTotalBytes: 100000})
	res := idx.Search("zzzznothing", 3)
	if len(res.Matches) != 0 {
		t.Fatalf("expected no matches, got %d", len(res.Matches))
	}
}

func TestExcerptIsBounded(t *testing.T) {
	dir := writeDocs(t, map[string]string{
		"long.md": "authentication " + strings.Repeat("padding ", 2000),
	})
	idx, _ := Build(dir, Limits{MaxFileBytes: 100000, MaxFiles: 10, MaxTotalBytes: 1000000})
	res := idx.Search("authentication", 1)
	if len(res.Matches) != 1 {
		t.Fatal("expected a match")
	}
	if len(res.Matches[0].Excerpt) > MaxExcerptBytes {
		t.Fatalf("excerpt not bounded: %d bytes", len(res.Matches[0].Excerpt))
	}
}

func TestResultCountIsBounded(t *testing.T) {
	files := map[string]string{}
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		files[n+".md"] = "commonterm here"
	}
	dir := writeDocs(t, files)
	idx, _ := Build(dir, Limits{MaxFileBytes: 4096, MaxFiles: 20, MaxTotalBytes: 100000})
	res := idx.Search("commonterm", 100)
	if len(res.Matches) > MaxResults {
		t.Fatalf("result count not bounded: %d", len(res.Matches))
	}
}

func TestBinaryContentIsRejected(t *testing.T) {
	dir := t.TempDir()
	// Binary payload inside a documentation file must be rejected, not indexed.
	if err := os.WriteFile(filepath.Join(dir, "blob.md"), []byte{0x00, 0x01, 0x02, 0x00, 0xff}, 0o644); err != nil {
		t.Fatal(err)
	}
	// A non-documentation extension is simply not part of the corpus.
	if err := os.WriteFile(filepath.Join(dir, "image.png"), []byte{0x89, 0x50, 0x00, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.md"), []byte("real documentation"), 0o644); err != nil {
		t.Fatal(err)
	}
	idx, err := Build(dir, Limits{MaxFileBytes: 4096, MaxFiles: 10, MaxTotalBytes: 100000})
	if err != nil {
		t.Fatal(err)
	}
	if len(idx.Documents) != 1 || idx.Documents[0].Path != "ok.md" {
		t.Fatalf("binary content should be rejected: %+v", idx.Documents)
	}
	if idx.SkippedBinary == 0 {
		t.Fatal("skipped binary files should be counted")
	}
}

func TestIndexSaveAndLoadRoundTrip(t *testing.T) {
	dir := writeDocs(t, map[string]string{"a.md": "content about widgets"})
	idx, _ := Build(dir, Limits{MaxFileBytes: 4096, MaxFiles: 10, MaxTotalBytes: 100000})
	path := filepath.Join(t.TempDir(), "index.json")
	if err := idx.Save(path); err != nil {
		t.Fatal(err)
	}
	back, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Search("widgets", 1).Matches) != 1 {
		t.Fatal("reloaded index lost its content")
	}
}

func TestLoadMissingIndex(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "nope.json")); err == nil {
		t.Fatal("expected an error for a missing index")
	}
}

func TestQueryIsRequired(t *testing.T) {
	dir := writeDocs(t, map[string]string{"a.md": "content"})
	idx, _ := Build(dir, Limits{})
	res := idx.Search("", 3)
	if len(res.Matches) != 0 {
		t.Fatal("an empty query must not match everything")
	}
}

func TestJSONOutputIsCompact(t *testing.T) {
	dir := writeDocs(t, map[string]string{"a.md": "content about widgets"})
	idx, _ := Build(dir, Limits{MaxFileBytes: 4096, MaxFiles: 10, MaxTotalBytes: 100000})
	data, err := json.Marshal(idx.Search("widgets", 1))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, ok := decoded["matches"]; !ok {
		t.Fatalf("search result shape unexpected: %s", data)
	}
}
