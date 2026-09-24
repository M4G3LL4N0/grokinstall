package inspect

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"grokinstall/internal/cache"
	"grokinstall/internal/source"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("testdata", name)
}

func inspectFixture(t *testing.T, name string) *Result {
	t.Helper()
	res, err := Inspect(fixture(t, name), Options{})
	if err != nil {
		t.Fatalf("inspect %s: %v", name, err)
	}
	return res
}

func hasFinding(r *Result, finding string) bool {
	for _, it := range r.Evidence {
		if it.Finding == finding {
			return true
		}
	}
	return false
}

func TestInspectNodeRepo(t *testing.T) {
	res := inspectFixture(t, "node-repo")
	if res.Meta.Name != "node-widget" {
		t.Fatalf("name = %q, want node-widget", res.Meta.Name)
	}
	if res.Meta.Version != "1.2.3" {
		t.Fatalf("version = %q", res.Meta.Version)
	}
	if !hasFinding(res, "cli_entrypoint") {
		t.Fatal("expected cli_entrypoint from package.json bin")
	}
	if !hasFinding(res, "javascript_project") {
		t.Fatal("expected javascript_project finding")
	}
	if !hasFinding(res, "has_tests") {
		t.Fatal("expected has_tests")
	}
	if !hasFinding(res, "has_github_actions") {
		t.Fatal("expected has_github_actions")
	}
	if !hasFinding(res, "license_present") {
		t.Fatal("expected license_present")
	}
	if !hasFinding(res, "env_example_present") {
		t.Fatal("expected env_example_present")
	}
	if !hasFinding(res, "install_script_present") {
		t.Fatal("postinstall script must be surfaced as evidence, never executed")
	}
	if len(res.Meta.Entrypoints) == 0 || res.Meta.Entrypoints[0] != "widget" {
		t.Fatalf("entrypoints = %v", res.Meta.Entrypoints)
	}
}

func TestInspectPythonRepo(t *testing.T) {
	res := inspectFixture(t, "python-repo")
	if res.Meta.Name != "python-widget" {
		t.Fatalf("name = %q", res.Meta.Name)
	}
	if !hasFinding(res, "python_project") {
		t.Fatal("expected python_project")
	}
	if !hasFinding(res, "cli_entrypoint") {
		t.Fatal("project.scripts should be a cli entrypoint")
	}
}

func TestInspectRecordsEntrypointPaths(t *testing.T) {
	res := inspectFixture(t, "node-repo")
	if len(res.Meta.EntrypointPaths) == 0 {
		t.Fatal("inspection must record where a CLI entrypoint actually lives")
	}
	found := false
	for _, p := range res.Meta.EntrypointPaths {
		if strings.Contains(p, "widget") {
			found = true
		}
	}
	if !found {
		t.Fatalf("entrypoint path missing: %v", res.Meta.EntrypointPaths)
	}
}

func TestInspectGoRepo(t *testing.T) {
	res := inspectFixture(t, "go-repo")
	if res.Meta.Name != "go-widget" {
		t.Fatalf("name = %q", res.Meta.Name)
	}
	if !hasFinding(res, "go_project") {
		t.Fatal("expected go_project")
	}
	if res.Meta.Languages[0] != "go" {
		t.Fatalf("languages = %v", res.Meta.Languages)
	}
}

func TestInspectDockerRepo(t *testing.T) {
	res := inspectFixture(t, "docker-repo")
	if !hasFinding(res, "docker_image") {
		t.Fatal("expected docker_image from Dockerfile")
	}
	if !hasFinding(res, "docker_compose") {
		t.Fatal("expected docker_compose")
	}
}

func TestInspectOpenAPIRepo(t *testing.T) {
	res := inspectFixture(t, "openapi-repo")
	if !hasFinding(res, "openapi_spec") {
		t.Fatal("expected openapi_spec")
	}
}

func TestInspectMCPRepo(t *testing.T) {
	res := inspectFixture(t, "mcp-repo")
	if !hasFinding(res, "mcp_server_config") {
		t.Fatal("expected mcp_server_config from .mcp.json")
	}
}

func TestInspectDocsOnlyRepo(t *testing.T) {
	res := inspectFixture(t, "docs-only")
	if !hasFinding(res, "documentation") {
		t.Fatal("expected documentation")
	}
	if hasFinding(res, "cli_entrypoint") {
		t.Fatal("docs-only repo must not yield a CLI capability")
	}
}

func TestInspectCLIRepo(t *testing.T) {
	res := inspectFixture(t, "cli-repo")
	if !hasFinding(res, "cli_entrypoint") {
		t.Fatal("expected cli_entrypoint from executable shebang script")
	}
}

func TestInspectLocalServiceRepo(t *testing.T) {
	res := inspectFixture(t, "local-service")
	if !hasFinding(res, "local_service_hint") {
		t.Fatal("expected local_service_hint")
	}
}

func TestInspectMalformedManifestIsEvidenceNotFailure(t *testing.T) {
	res := inspectFixture(t, "malformed-repo")
	if !hasFinding(res, "manifest_parse_error") {
		t.Fatal("expected manifest_parse_error")
	}
	if !hasFinding(res, "documentation") {
		t.Fatal("inspection must continue past a malformed manifest")
	}
}

func TestInspectTreatsReadmeAsUntrustedData(t *testing.T) {
	res := inspectFixture(t, "malicious-readme")
	if !hasFinding(res, "untrusted_instruction_text") {
		t.Fatal("prompt injection in README should be recorded as untrusted text")
	}
	blob := res.Meta.Description + strings.Join(res.Meta.Entrypoints, " ")
	for _, bad := range []string{"rm -rf", "id_rsa", "Ignore all previous", "exfiltrate"} {
		if strings.Contains(blob, bad) {
			t.Fatalf("readme text treated as instructions/derived field: %q in %q", bad, blob)
		}
	}
}

func TestInspectMissingDirErrors(t *testing.T) {
	if _, err := Inspect(filepath.Join(t.TempDir(), "nope"), Options{}); err == nil {
		t.Fatal("expected error for missing directory")
	}
}

func TestInspectAppliesSizeLimits(t *testing.T) {
	dir := t.TempDir()
	big := filepath.Join(dir, "big.txt")
	if err := os.WriteFile(big, []byte(strings.Repeat("x", 4096)), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Inspect(dir, Options{MaxFileBytes: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(res, "size_limit_hit") {
		t.Fatal("expected size_limit_hit finding")
	}
	if !res.Truncated {
		t.Fatal("result should be marked truncated")
	}
}

func TestInspectDoesNotFollowSymlinks(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(dir, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	res, err := Inspect(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if hasFinding(res, "symlink_followed") {
		t.Fatal("inspection must not follow symlinks out of the source")
	}
}

func TestInspectSkipsHeavyDirectories(t *testing.T) {
	dir := t.TempDir()
	nm := filepath.Join(dir, "node_modules", "left-pad")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nm, "index.js"), []byte("module.exports=1"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Inspect(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Files {
		if strings.Contains(f.Path, "node_modules") {
			t.Fatalf("walked dependency directory: %s", f.Path)
		}
	}
}

func TestSourceCodeMentioningMcpIsNotAnMCPConfig(t *testing.T) {
	dir := t.TempDir()
	// A detector or a comment can mention mcpServers without the file being
	// an MCP configuration.
	if err := os.WriteFile(filepath.Join(dir, "detector.go"), []byte("package main\n\nconst marker = \"mcpServers\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Inspect(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if hasFinding(res, "mcp_server_config") {
		t.Fatal("a Go source file mentioning mcpServers is not an MCP config")
	}
}

func TestConfigFileDeclaringMcpIsDetected(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"mcpServers":{"docs":{"command":"node"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Inspect(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(res, "mcp_server_config") {
		t.Fatal("a JSON config declaring mcpServers must be detected")
	}
}

func TestShellScriptWithSudoIsFlagged(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "setup.sh"), []byte("#!/bin/sh\nsudo rm -rf /var/cache\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	res, err := Inspect(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !hasFinding(res, "privileged_command") {
		t.Fatal("a shell script requesting sudo must be flagged")
	}
}

func TestSourceCodeMentioningSudoIsNotFlagged(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "checker.go"), []byte("package main\n\n// do not use sudo here\nconst hint = \"sudo\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Inspect(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if hasFinding(res, "privileged_command") {
		t.Fatal("a Go file mentioning sudo in a comment is not a privileged command")
	}
}

func TestInspectSkipsTestdataDirectories(t *testing.T) {
	dir := t.TempDir()
	fixtures := filepath.Join(dir, "internal", "pkg", "testdata", "sample")
	if err := os.MkdirAll(fixtures, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixtures, "package.json"), []byte(`{"name":"fixture-only"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# real project\n\nA real project.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Inspect(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Meta.Name == "fixture-only" {
		t.Fatal("test fixtures must not define the project identity")
	}
	for _, f := range res.Files {
		if strings.Contains(f.Path, "testdata") {
			t.Fatalf("test fixture directory was inspected: %s", f.Path)
		}
	}
}

func TestRootManifestDefinesProjectIdentity(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module github.com/acme/realname\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "vendorish", "fixture")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "pyproject.toml"), []byte("[project]\nname = \"nested-fixture\"\nversion = \"9.9.9\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Inspect(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Meta.Name != "realname" {
		t.Fatalf("name = %q, want the root module name", res.Meta.Name)
	}
	if res.Meta.Version != "" && res.Meta.Version == "9.9.9" {
		t.Fatal("a nested fixture version must not become the project version")
	}
}

func TestInspectHonorsRootGitignore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.log\nbuild/\n/secret.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.log"), []byte("noise\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("nope\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buildDir := filepath.Join(dir, "build")
	if err := os.MkdirAll(buildDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, "artifact.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Inspect(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range res.Files {
		switch f.Path {
		case "app.log", "secret.txt":
			t.Fatalf("gitignored file inspected: %s", f.Path)
		}
		if strings.HasPrefix(f.Path, "build/") {
			t.Fatalf("gitignored directory inspected: %s", f.Path)
		}
	}
	if res.Has("secret_like_file") {
		t.Fatal("ignored files must not produce security findings")
	}
}

func TestIdentityStableForUnchangedTree(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	id1, err := Identity(context.Background(), mustLocal(t, dir), Options{})
	if err != nil {
		t.Fatal(err)
	}
	id2, err := Identity(context.Background(), mustLocal(t, dir), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("identity unstable: %s != %s", id1, id2)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	id3, err := Identity(context.Background(), mustLocal(t, dir), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if id3 == id1 {
		t.Fatal("identity must change when content changes")
	}
}

func mustLocal(t *testing.T, dir string) source.Source {
	t.Helper()
	s, err := source.Normalize(dir)
	if err != nil {
		t.Fatal(err)
	}
	return *s
}

func TestAnalyzeCachesUnchangedLocalSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# x\n\nhello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	store, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	src := mustLocal(t, dir)
	first, err := Analyze(context.Background(), src, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if first.CacheHit {
		t.Fatal("first analysis must be a miss")
	}
	second, err := Analyze(context.Background(), src, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !second.CacheHit {
		t.Fatal("second analysis of unchanged source must hit the cache")
	}
}

func TestAnalyzeRejectsUnsupportedSource(t *testing.T) {
	store, _ := cache.New(t.TempDir())
	src := source.Source{Kind: source.KindGitLab, Canonical: "gitlab.com/a/b", URL: "https://gitlab.com/a/b"}
	if _, err := Analyze(context.Background(), src, store, Options{}); err == nil {
		t.Fatal("expected unsupported source error for gitlab in Part 1")
	}
}
