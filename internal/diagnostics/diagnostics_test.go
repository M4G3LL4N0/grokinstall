package diagnostics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"grokinstall/internal/registry"
	"grokinstall/internal/toolchain"
)

func TestDoctorRunsOnAFreshStateDirectory(t *testing.T) {
	rep := Run(t.TempDir(), toolchain.Detect())
	if len(rep.Checks) == 0 {
		t.Fatal("doctor must report checks")
	}
	if rep.Healthy == false {
		t.Fatalf("a fresh state directory should be healthy; problems: %+v", rep.Problems())
	}
}

func TestDoctorChecksEveryCataloguedTool(t *testing.T) {
	profile := toolchain.Detect()
	rep := Run(t.TempDir(), profile)
	seen := map[string]bool{}
	for _, c := range rep.Checks {
		seen[c.Name] = true
	}
	for _, id := range profile.Order() {
		if !seen["tool:"+string(id)] {
			t.Fatalf("doctor did not check tool %s", id)
		}
	}
}

func TestDoctorFlagsMissingGitWithActionableFix(t *testing.T) {
	profile := &toolchain.Profile{Tools: map[toolchain.ToolID]toolchain.Tool{
		toolchain.ToolGit: {ID: toolchain.ToolGit, Name: "Git", Present: false},
	}}
	rep := Run(t.TempDir(), profile)
	c := rep.Check("tool:git")
	if c == nil {
		t.Fatal("expected a git check")
	}
	if c.Status == StatusOK {
		t.Fatal("missing git must not be reported as ok")
	}
	if c.Action == "" {
		t.Fatalf("missing git must include an actionable fix: %+v", c)
	}
	if !strings.Contains(strings.ToLower(c.Action), "git") {
		t.Fatalf("action should be specific: %q", c.Action)
	}
}

func TestDoctorFlagsMissingOpenCodeAsOptional(t *testing.T) {
	profile := &toolchain.Profile{Tools: map[toolchain.ToolID]toolchain.Tool{
		toolchain.ToolGit:      {ID: toolchain.ToolGit, Name: "Git", Present: true},
		toolchain.ToolOpenCode: {ID: toolchain.ToolOpenCode, Name: "OpenCode", Present: false, Role: toolchain.RoleCoder},
	}}
	rep := Run(t.TempDir(), profile)
	c := rep.Check("tool:opencode")
	if c.Status != StatusWarn {
		t.Fatalf("missing optional tool status = %q, want warn", c.Status)
	}
	if c.Required {
		t.Fatal("OpenCode must never be reported as required")
	}
}

func TestDoctorDetectsUnwritableStateDirectory(t *testing.T) {
	dir := t.TempDir()
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	rep := Run(ro, toolchain.Detect())
	c := rep.Check("state_dir")
	if c.Status != StatusFail {
		t.Fatalf("read-only state dir status = %q, want fail", c.Status)
	}
}

func TestDoctorDetectsCorruptConfig(t *testing.T) {
	dir := t.TempDir()
	reg, err := registry.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reg.ConfigPath(), []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := Run(dir, toolchain.Detect())
	c := rep.Check("config")
	if c.Status != StatusFail {
		t.Fatalf("corrupt config status = %q, want fail", c.Status)
	}
}

func TestDoctorDetectsCorruptRegistry(t *testing.T) {
	dir := t.TempDir()
	reg, _ := registry.New(dir)
	if err := os.WriteFile(reg.RegistryPath(), []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep := Run(dir, toolchain.Detect())
	if c := rep.Check("registry"); c.Status != StatusFail {
		t.Fatalf("corrupt registry status = %q, want fail", c.Status)
	}
}

func TestProblemsListsFailuresFirst(t *testing.T) {
	profile := &toolchain.Profile{Tools: map[toolchain.ToolID]toolchain.Tool{
		toolchain.ToolGit: {ID: toolchain.ToolGit, Name: "Git", Present: false},
	}}
	// A read-only state directory is a genuine failure; missing git is only a
	// warning because local sources work without it.
	ro := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	rep := Run(ro, profile)
	problems := rep.Problems()
	if len(problems) < 2 {
		t.Fatalf("expected several problems, got %+v", problems)
	}
	if problems[0].Status != StatusFail {
		t.Fatalf("first problem = %q, want a fail", problems[0].Status)
	}
	if rep.Healthy {
		t.Fatal("a read-only state directory must not report healthy")
	}
}

func TestMissingGitIsOnlyAWarning(t *testing.T) {
	profile := &toolchain.Profile{Tools: map[toolchain.ToolID]toolchain.Tool{
		toolchain.ToolGit: {ID: toolchain.ToolGit, Name: "Git", Present: false},
	}}
	rep := Run(t.TempDir(), profile)
	if c := rep.Check("tool:git"); c == nil || c.Status != StatusWarn {
		t.Fatalf("missing git should warn, got %+v", c)
	}
	if !rep.Healthy {
		t.Fatal("local-only usage must stay healthy without git")
	}
}

func TestDoctorIsHonestAboutOptionalProviders(t *testing.T) {
	rep := Run(t.TempDir(), &toolchain.Profile{Tools: map[toolchain.ToolID]toolchain.Tool{}})
	for _, id := range []toolchain.ToolID{toolchain.ToolCursor, toolchain.ToolVercel, toolchain.ToolOllama} {
		c := rep.Check("tool:" + string(id))
		if c == nil {
			t.Fatalf("missing check for %s", id)
		}
		if c.Required {
			t.Fatalf("%s must not be reported as required", id)
		}
	}
}
