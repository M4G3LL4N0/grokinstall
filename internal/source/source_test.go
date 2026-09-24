package source

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeLocalDir(t *testing.T) {
	ref := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(ref, 0o755); err != nil {
		t.Fatal(err)
	}
	s, err := Normalize(ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Kind != KindLocalDir {
		t.Fatalf("kind = %q, want local_dir", s.Kind)
	}
	if s.Canonical == "" {
		t.Fatal("canonical identity must not be empty")
	}
	if s.Ref != ref {
		t.Fatalf("ref = %q, want %q", s.Ref, ref)
	}
}

func TestNormalizeTildeExpansion(t *testing.T) {
	if s, err := Normalize("~/nonexistent-path-xyz"); err == nil {
		t.Fatalf("expected error for nonexistent path, got %+v", s)
	}
}

func TestNormalizeNonexistentErrors(t *testing.T) {
	if _, err := Normalize(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestNormalizeAnonymousRegularFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "file.txt")
	_, err := Normalize(f)
	if err == nil {
		t.Fatal("expected error for nonexistent file path")
	}
}

func TestNormalizeGitHubURL(t *testing.T) {
	cases := []struct {
		in    string
		owner string
		repo  string
		canon string
	}{
		{"https://github.com/acme/widget", "acme", "widget", "github.com/acme/widget"},
		{"https://github.com/acme/widget.git", "acme", "widget", "github.com/acme/widget"},
		{"https://github.com/acme/widget/tree/main/src", "acme", "widget", "github.com/acme/widget"},
		{"https://github.com/acme/widget/blob/main/README.md", "acme", "widget", "github.com/acme/widget"},
		{"https://github.com/acme/widget/", "acme", "widget", "github.com/acme/widget"},
		{"git@github.com:acme/widget.git", "acme", "widget", "github.com/acme/widget"},
	}
	for _, c := range cases {
		s, err := Normalize(c.in)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.in, err)
		}
		if s.Kind != KindGitHub {
			t.Fatalf("%s: kind = %q, want github", c.in, s.Kind)
		}
		if s.Owner != c.owner || s.Repo != c.repo {
			t.Fatalf("%s: owner/repo = %s/%s, want %s/%s", c.in, s.Owner, s.Repo, c.owner, c.repo)
		}
		if s.Canonical != c.canon {
			t.Fatalf("%s: canonical = %q, want %q", c.in, s.Canonical, c.canon)
		}
	}
}

func TestNormalizeGitHubBareHostRejected(t *testing.T) {
	_, err := Normalize("https://github.com/")
	if err == nil {
		t.Fatal("expected error for bare github host")
	}
}

func TestNormalizeGitHubSingleSegmentRejected(t *testing.T) {
	_, err := Normalize("https://github.com/acme")
	if err == nil {
		t.Fatal("expected error for owner with no repo")
	}
}

func TestNormalizeGitLabRecognizedUnsupported(t *testing.T) {
	s, err := Normalize("https://gitlab.com/acme/widget")
	if err != nil {
		t.Fatalf("expected recognized-but-unsupported source, got error: %v", err)
	}
	if s.Kind != KindGitLab {
		t.Fatalf("kind = %q, want gitlab", s.Kind)
	}
}
