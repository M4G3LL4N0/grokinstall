package plan

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"grokinstall/internal/cache"
	"grokinstall/internal/inspect"
	"grokinstall/internal/source"
	"grokinstall/internal/toolchain"
)

func newState(t *testing.T) *cache.Store {
	t.Helper()
	store, err := cache.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func localSource(t *testing.T, dir string) source.Source {
	t.Helper()
	s, err := source.Normalize(dir)
	if err != nil {
		t.Fatal(err)
	}
	return *s
}

func copyFixture(t *testing.T, name string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), name)
	if err := copyTree(filepath.Join("..", "inspect", "testdata", name), dst); err != nil {
		t.Fatal(err)
	}
	return dst
}

func copyTree(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		mode := info.Mode()
		if mode&0o111 != 0 {
			mode = 0o755
		} else {
			mode = 0o644
		}
		return os.WriteFile(target, data, mode)
	})
}

func TestPlanFollowsTheWholePipeline(t *testing.T) {
	dir := copyFixture(t, "node-repo")
	p, err := Build(context.Background(), Options{
		Source:  localSource(t, dir),
		Goal:    "Allow GrokBot to run the widget CLI",
		Store:   newState(t),
		Profile: toolchain.Detect(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Understanding) == 0 {
		t.Fatal("plan must state its understanding")
	}
	if len(p.Capabilities) == 0 {
		t.Fatal("plan must discover capabilities")
	}
	if p.Comparison.Recommended == "" {
		t.Fatal("plan must recommend a strategy")
	}
	if p.ContextPack.SizeBytes() == 0 {
		t.Fatal("plan must build a context pack")
	}
	if p.Manifest.Schema != "grokinstall/v1" {
		t.Fatalf("plan must carry a manifest, got schema %q", p.Manifest.Schema)
	}
}

func TestPlanRecommendsNoInstallWhenGoalNeedsLess(t *testing.T) {
	dir := copyFixture(t, "docs-only")
	p, err := Build(context.Background(), Options{
		Source:  localSource(t, dir),
		Goal:    "Answer questions about the documented product",
		Store:   newState(t),
		Profile: toolchain.Detect(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Comparison.Recommended != "knowledge_import" {
		t.Fatalf("recommended = %q, want knowledge_import for a docs goal", p.Comparison.Recommended)
	}
	if p.Manifest.Execution.Supported {
		t.Fatal("a Part 1 manifest must never claim an execution it cannot provide")
	}
}

func TestPlanContextPackStaysSmallForLargeSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# big\n\nA large project.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"big","version":"1.0.0","bin":{"big":"bin/big.js"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// 400 extra files, none of which belong in a context pack.
	for i := 0; i < 400; i++ {
		name := filepath.Join(dir, "src", strings.Repeat("d", i%5+1), "file"+itoa(i)+".go")
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("package main\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p, err := Build(context.Background(), Options{
		Source:  localSource(t, dir),
		Goal:    "use the big CLI",
		Store:   newState(t),
		Profile: toolchain.Detect(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Inspection.FilesScanned < 400 {
		t.Fatalf("expected many scanned files, got %d", p.Inspection.FilesScanned)
	}
	if size := p.ContextPack.SizeBytes(); size > 4096 {
		t.Fatalf("context pack must stay compact, got %d bytes", size)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestPlanRecordsUnresolvedQuestions(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# vague\n\nNothing much.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Build(context.Background(), Options{
		Source:  localSource(t, dir),
		Goal:    "",
		Store:   newState(t),
		Profile: toolchain.Detect(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Unresolved) == 0 {
		t.Fatal("an empty goal must produce unresolved questions, not a guess")
	}
}

func TestPlanToolchainLabelsAreSpecific(t *testing.T) {
	profile := &toolchain.Profile{Tools: map[toolchain.ToolID]toolchain.Tool{
		toolchain.ToolGit:      {ID: toolchain.ToolGit, Name: "Git", Present: true, Role: toolchain.RoleVCS, Why: "version control"},
		toolchain.ToolNpm:      {ID: toolchain.ToolNpm, Name: "npm", Present: true, Role: toolchain.RolePkgManager, Why: "js packages"},
		toolchain.ToolOpenCode: {ID: toolchain.ToolOpenCode, Name: "OpenCode", Present: false, Role: toolchain.RoleCoder, Why: "coding agent"},
	}}
	dir := copyFixture(t, "local-service")
	p, err := Build(context.Background(), Options{
		Source:  localSource(t, dir),
		Goal:    "let GrokBot call the local service",
		Store:   newState(t),
		Profile: profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, n := range p.Toolchain {
		labels[string(n.Tool)] = string(n.Label)
	}
	if labels["opencode"] == "" {
		t.Fatalf("coding worker should be resolved in the plan: %+v", p.Toolchain)
	}
	if labels["opencode"] == "required" {
		t.Fatal("a missing coding worker must not be labeled satisfied")
	}
}

func TestPlanJSONRoundTrip(t *testing.T) {
	dir := copyFixture(t, "node-repo")
	p, err := Build(context.Background(), Options{
		Source:  localSource(t, dir),
		Goal:    "use the widget",
		Store:   newState(t),
		Profile: toolchain.Detect(),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var back Plan
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.Goal != p.Goal || back.Comparison.Recommended != p.Comparison.Recommended {
		t.Fatal("plan JSON round trip lost data")
	}
}

func TestPlanNeverSuggestsInstallingEverything(t *testing.T) {
	profile := &toolchain.Profile{Tools: map[toolchain.ToolID]toolchain.Tool{
		toolchain.ToolGit:      {ID: toolchain.ToolGit, Name: "Git", Present: true, Role: toolchain.RoleVCS},
		toolchain.ToolNode:     {ID: toolchain.ToolNode, Name: "Node", Present: true, Role: toolchain.RoleRuntime},
		toolchain.ToolOpenCode: {ID: toolchain.ToolOpenCode, Name: "OpenCode", Present: false, Role: toolchain.RoleCoder},
		toolchain.ToolDocker:   {ID: toolchain.ToolDocker, Name: "Docker", Present: false, Role: toolchain.RoleContainer},
		toolchain.ToolCursor:   {ID: toolchain.ToolCursor, Name: "Cursor", Present: false, Role: toolchain.RoleCoder},
	}}
	dir := copyFixture(t, "cli-repo")
	p, err := Build(context.Background(), Options{
		Source:  localSource(t, dir),
		Goal:    "run the acme CLI",
		Store:   newState(t),
		Profile: profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	required := 0
	for _, n := range p.Toolchain {
		if n.Label == "required" && n.Required {
			required++
		}
	}
	if required > 1 {
		t.Fatalf("a CLI bridge must not require a pile of tools, got %d required", required)
	}
}

func TestPlanRejectsUnsupportedSource(t *testing.T) {
	_, err := Build(context.Background(), Options{
		Source:  source.Source{Kind: source.KindGitLab, Canonical: "gitlab.com/a/b", URL: "https://gitlab.com/a/b"},
		Goal:    "use it",
		Store:   newState(t),
		Profile: toolchain.Detect(),
	})
	if err == nil {
		t.Fatal("gitlab must be rejected as unsupported in Part 1")
	}
	if !strings.Contains(err.Error(), "Part 1") {
		t.Fatalf("error should explain the limitation: %v", err)
	}
}

func TestPlanFlagsSelfInspection(t *testing.T) {
	// GrokInstall pointed at itself must be recognized rather than planned as
	// a fresh third-party integration.
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(repoRoot, "go.mod")); err != nil {
		t.Skipf("repository root not available: %v", err)
	}
	p, err := Build(context.Background(), Options{
		Source:  localSource(t, repoRoot),
		Goal:    "Allow GrokBot to diagnose GrokInstall",
		Store:   newState(t),
		Profile: toolchain.Detect(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsSelf {
		t.Fatal("the GrokInstall repository must be recognized as self")
	}
}

func TestPlanSecuritySummarySurfacesUntrustedText(t *testing.T) {
	dir := copyFixture(t, "malicious-readme")
	p, err := Build(context.Background(), Options{
		Source:  localSource(t, dir),
		Goal:    "use this project",
		Store:   newState(t),
		Profile: toolchain.Detect(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Security.PromptInjection {
		t.Fatal("plan must surface prompt-injection evidence")
	}
	if p.Comparison.Recommended == "micro_prompt_pack" {
		t.Fatal("untrusted instruction text must never become a prompt pack")
	}
}

func TestInspectionOptionsFlowThrough(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(strings.Repeat("x", 2000)), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := Build(context.Background(), Options{
		Source:  localSource(t, dir),
		Goal:    "use it",
		Store:   newState(t),
		Profile: toolchain.Detect(),
		Inspect: inspect.Options{MaxFileBytes: 100},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !p.Inspection.Truncated {
		t.Fatal("size limits must be honored through the plan pipeline")
	}
}
