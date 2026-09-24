// Package knowledge builds a bounded, deterministic local index over a source's
// documentation and answers queries from it.
//
// It deliberately uses no embeddings and no vector database: a stdlib inverted
// index is inspectable, reproducible, fast enough, and keeps the dependency
// footprint at zero. The point is a small local capability, not AI.
package knowledge

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Schema identifies the index format.
const Schema = "grokinstall/knowledge/v1"

// Output bounds.
const (
	// MaxExcerptBytes bounds a returned excerpt.
	MaxExcerptBytes = 600
	// MaxResults bounds how many matches are ever returned.
	MaxResults = 10
)

// Default index limits.
const (
	DefaultMaxFileBytes  int64 = 64 * 1024
	DefaultMaxFiles            = 500
	DefaultMaxTotalBytes int64 = 4 * 1024 * 1024
)

// textExtensions are the documentation formats indexed.
var textExtensions = map[string]bool{
	".md": true, ".markdown": true, ".rst": true, ".txt": true, ".adoc": true,
	".org": true, ".json": true, ".yaml": true, ".yml": true, ".toml": true,
}

// Limits bound an index build.
type Limits struct {
	MaxFileBytes  int64
	MaxFiles      int
	MaxTotalBytes int64
}

func (l Limits) normalized() Limits {
	if l.MaxFileBytes <= 0 {
		l.MaxFileBytes = DefaultMaxFileBytes
	}
	if l.MaxFiles <= 0 {
		l.MaxFiles = DefaultMaxFiles
	}
	if l.MaxTotalBytes <= 0 {
		l.MaxTotalBytes = DefaultMaxTotalBytes
	}
	return l
}

// Document is one indexed source file, with its text bounded.
type Document struct {
	Path  string         `json:"path"`
	Title string         `json:"title,omitempty"`
	Text  string         `json:"text"`
	Terms map[string]int `json:"terms"`
}

// Index is a bounded, serialized local knowledge index.
type Index struct {
	Schema         string     `json:"schema"`
	Source         string     `json:"source"`
	Documents      []Document `json:"documents"`
	TotalBytes     int64      `json:"total_bytes"`
	Truncated      bool       `json:"truncated"`
	SkippedBinary  int        `json:"skipped_binary,omitempty"`
	TruncatedFiles int        `json:"truncated_files,omitempty"`
	BuiltAt        string     `json:"built_at,omitempty"`
}

// Match is one search result.
type Match struct {
	File    string  `json:"file"`
	Title   string  `json:"title,omitempty"`
	Excerpt string  `json:"excerpt"`
	Score   float64 `json:"score"`
}

// SearchResult is the compact response of a query.
type SearchResult struct {
	Query   string  `json:"query"`
	Matches []Match `json:"matches"`
	Total   int     `json:"total_documents"`
}

// Build indexes documentation under root.
func Build(root string, limits Limits) (*Index, error) {
	limits = limits.normalized()
	idx := &Index{Schema: Schema, Source: root}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if shouldSkipDir(info.Name()) && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		if !textExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		if len(idx.Documents) >= limits.MaxFiles || idx.TotalBytes >= limits.MaxTotalBytes {
			idx.Truncated = true
			return filepath.SkipAll
		}
		// Enforce the total budget per file so a single large document can
		// never overshoot it.
		allow := limits.MaxFileBytes
		if remaining := limits.MaxTotalBytes - idx.TotalBytes; remaining < allow {
			allow = remaining
		}
		if allow <= 0 {
			idx.Truncated = true
			return filepath.SkipAll
		}
		data, readErr := readBounded(path, allow)
		if readErr != nil {
			return nil
		}
		if isBinary(data) {
			idx.SkippedBinary++
			return nil
		}
		text := string(data)
		if int64(len(text)) < info.Size() {
			idx.Truncated = true
			idx.TruncatedFiles++
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		rel = filepath.ToSlash(rel)
		idx.Documents = append(idx.Documents, Document{
			Path:  rel,
			Title: titleFor(rel, text),
			Text:  text,
			Terms: termCounts(text),
		})
		idx.TotalBytes += int64(len(text))
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("index %s: %w", root, err)
	}
	// Stable document order keeps the index and its results reproducible.
	sort.SliceStable(idx.Documents, func(i, j int) bool { return idx.Documents[i].Path < idx.Documents[j].Path })
	return idx, nil
}

func shouldSkipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", ".venv", "venv", "dist", "build", "target",
		"__pycache__", ".next", "coverage", "testdata", ".cache", "bin", "obj":
		return true
	}
	return false
}

// readBounded reads at most limit bytes of a file. Long documents are
// truncated rather than dropped: partial knowledge of a large document is more
// useful than none, and the truncation is recorded on the index.
func readBounded(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	buf := make([]byte, limit)
	n, err := io.ReadFull(f, buf)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return nil, err
	}
	return buf[:n], nil
}

// isBinary reports whether data looks like binary content.
func isBinary(data []byte) bool {
	probe := data
	if len(probe) > 8000 {
		probe = probe[:8000]
	}
	for _, b := range probe {
		if b == 0 {
			return true
		}
	}
	return !utf8.Valid(probe)
}

func titleFor(path, text string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			return strings.TrimSpace(strings.TrimLeft(trimmed, "#"))
		}
		break
	}
	return base
}

// Search returns the documents most relevant to a query, deterministically.
func (idx *Index) Search(query string, limit int) SearchResult {
	res := SearchResult{Query: query, Matches: []Match{}, Total: len(idx.Documents)}
	terms := tokenize(query)
	if len(terms) == 0 {
		return res
	}
	if limit <= 0 || limit > MaxResults {
		limit = MaxResults
	}

	type scored struct {
		doc   Document
		score float64
	}
	var hits []scored
	for _, doc := range idx.Documents {
		score := 0.0
		matched := 0
		for _, term := range terms {
			if n, ok := doc.Terms[term]; ok {
				// Term frequency weighted by term length keeps the score
				// explainable: no opaque model in the ranking path.
				score += float64(n) * (1 + 0.1*float64(len(term)-1))
				matched++
			}
		}
		if matched == 0 {
			continue
		}
		// Reward documents matching more of the query.
		score *= float64(matched) / float64(len(terms))
		hits = append(hits, scored{doc: doc, score: score})
	}

	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].doc.Path < hits[j].doc.Path
	})

	for i, h := range hits {
		if i >= limit {
			break
		}
		res.Matches = append(res.Matches, Match{
			File:    h.doc.Path,
			Title:   h.doc.Title,
			Excerpt: excerptAround(h.doc.Text, terms),
			Score:   round3(h.score),
		})
	}
	return res
}

func round3(f float64) float64 {
	return float64(int(f*1000+0.5)) / 1000
}

// excerptAround returns a bounded window of text around the first matched term.
func excerptAround(text string, terms []string) string {
	lower := strings.ToLower(text)
	pos := -1
	for _, term := range terms {
		if idx := strings.Index(lower, term); idx >= 0 && (pos == -1 || idx < pos) {
			pos = idx
		}
	}
	if pos == -1 {
		pos = 0
	}
	start := pos - MaxExcerptBytes/3
	if start < 0 {
		start = 0
	}
	end := start + MaxExcerptBytes
	if end > len(text) {
		end = len(text)
		start = end - MaxExcerptBytes
		if start < 0 {
			start = 0
		}
	}
	out := strings.TrimSpace(text[start:end])
	out = strings.ReplaceAll(out, "\n", " ")
	if start > 0 {
		out = "…" + out
	}
	if end < len(text) {
		out += "…"
	}
	if len(out) > MaxExcerptBytes+2 {
		out = out[:MaxExcerptBytes]
	}
	return out
}

// tokenize lowercases and splits text into comparable terms.
func tokenize(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := map[string]bool{}
	var out []string
	for _, f := range fields {
		if len(f) < 2 || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// termCounts counts every occurrence of each term.
func termCounts(text string) map[string]int {
	counts := map[string]int{}
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		counts[f]++
	}
	return counts
}

// Save writes the index atomically.
func (idx *Index) Save(path string) error {
	data, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Load reads an index from disk.
func Load(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load knowledge index %s: %w", path, err)
	}
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("parse knowledge index %s: %w", path, err)
	}
	if idx.Schema != Schema {
		return nil, fmt.Errorf("knowledge index %s has unexpected schema %q", path, idx.Schema)
	}
	return &idx, nil
}
