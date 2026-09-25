package inspect

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/M4G3LL4N0/grokinstall/internal/cache"
	"github.com/M4G3LL4N0/grokinstall/internal/source"
)

// makeGitRepo creates a real local git repository that behaves like a public
// remote, so the clone path is exercised without depending on the network.
func makeGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.com",
			"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.com",
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# remote fixture\n\nA remote CLI.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"remote-widget","version":"9.9.9","bin":{"rwidget":"bin/r.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bin", "r.js"), []byte("#!/usr/bin/env node\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q")
	git("add", ".")
	git("commit", "-q", "-m", "fixture")
	return dir
}

func TestCloneInspectionPath(t *testing.T) {
	remote := makeGitRepo(t)
	store, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// file:// remote exercises the same git clone + ls-remote code path.
	src := source.Source{
		Kind:      source.KindGitHub,
		Ref:       "https://github.com/fixture/remote-widget",
		Owner:     "fixture",
		Repo:      "remote-widget",
		URL:       "file://" + remote,
		Canonical: "github.com/fixture/remote-widget",
	}
	res, err := Analyze(context.Background(), src, store, Options{})
	if err != nil {
		t.Fatalf("analyze remote: %v", err)
	}
	if res.Meta.Name != "remote-widget" {
		t.Fatalf("name = %q", res.Meta.Name)
	}
	if res.CommitSHA == "" {
		t.Fatal("commit identity must be recorded for a cloned repository")
	}
	if !res.Has("git_repository") {
		t.Fatal("cloned repo should record a git repository finding")
	}
	if !res.Has("cli_entrypoint") {
		t.Fatal("cloned repo CLI should be detected")
	}
	// Second run must hit the cache and not clone again.
	res2, err := Analyze(context.Background(), src, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res2.CacheHit {
		t.Fatal("unchanged remote commit must hit the inspection cache")
	}
}

func TestRemoteInspectionWithoutGit(t *testing.T) {
	store, _ := cache.New(t.TempDir())
	src := source.Source{
		Kind:      source.KindGitHub,
		URL:       "https://github.com/fixture/remote-widget",
		Canonical: "github.com/fixture/remote-widget",
	}
	_, err := Analyze(context.Background(), src, store, Options{Git: noGit{}})
	if err == nil {
		t.Fatal("expected a clear error when git is unavailable")
	}
}

type noGit struct{}

func (noGit) Available() bool { return false }
func (noGit) Output(context.Context, string, ...string) (string, error) {
	return "", exec.ErrNotFound
}

func TestLocalGitIdentityUsesCommit(t *testing.T) {
	repo := makeGitRepo(t)
	store, _ := cache.New(t.TempDir())
	src, err := source.Normalize(repo)
	if err != nil {
		t.Fatal(err)
	}
	first, err := Analyze(context.Background(), *src, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if first.CommitSHA == "" {
		t.Fatal("local git repo should record a commit")
	}
	second, err := Analyze(context.Background(), *src, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !second.CacheHit {
		t.Fatal("same commit must hit the cache")
	}
}

// TestPublicGitHubSource is opt-in: set GROKINSTALL_NETWORK_TESTS=1.
func TestPublicGitHubSource(t *testing.T) {
	if os.Getenv("GROKINSTALL_NETWORK_TESTS") != "1" {
		t.Skip("set GROKINSTALL_NETWORK_TESTS=1 to run live public GitHub inspection")
	}
	store, _ := cache.New(t.TempDir())
	s, err := source.Normalize("https://github.com/octocat/Hello-World")
	if err != nil {
		t.Fatal(err)
	}
	res, err := Analyze(context.Background(), *s, store, Options{})
	if err != nil {
		t.Fatalf("live GitHub inspection failed: %v", err)
	}
	if res.CommitSHA == "" {
		t.Fatal("expected a commit SHA from the public repository")
	}
	if !res.Has("readme_present") {
		t.Fatal("expected README detection in Hello-World")
	}
}
