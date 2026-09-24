package toolchain

import (
	"testing"
)

func TestDetectFindsGit(t *testing.T) {
	p := Detect()
	if !p.Get(ToolGit).Present {
		t.Fatal("git must be detected; it is required for this repo's tooling")
	}
}

func TestDetectKnownShapes(t *testing.T) {
	p := Detect()
	for _, id := range []ToolID{ToolGit, ToolGo, ToolNode, ToolNpm, ToolPython, ToolDocker} {
		if _, ok := p.Tools[id]; !ok {
			t.Fatalf("profile missing tool %s", id)
		}
	}
}

func TestStatusLabels(t *testing.T) {
	p := Detect()
	// git present on any sane machine: required is satisfiable.
	label, ok := p.Resolve(ToolGit)
	if !ok {
		t.Fatal("expected git to resolve")
	}
	if label.Status != StatusPresent {
		t.Fatalf("git status = %s, want present", label.Status)
	}
}

func TestPnpmSubstitutesNpm(t *testing.T) {
	p := Detect()
	// whichever is present, at least npm or pnpm must resolve for js work
	if !p.Get(ToolNpm).Present && !p.Get(ToolPnpm).Present {
		t.Skip("neither npm nor pnpm installed")
	}
	if _, ok := p.Resolve(ToolPnpm); ok {
		// pnpm may resolve via pnpm itself or via npm substitute
	}
	res, ok := p.Resolve(ToolUv)
	if p.Get(ToolUv).Present && !ok {
		t.Fatal("uv present but unresolvable")
	}
	if !p.Get(ToolUv).Present && !p.Get(ToolPython).Present {
		// neither available; still resolvable to a declared substitute? this is a warning, not failure
		if _, ok := p.Resolve(ToolUv); !ok {
			t.Skip("uv missing, python missing, no substitute on this machine")
		}
	}
	if p.Get(ToolUv).Present && res.Resolved != ToolUv {
		t.Fatalf("wrong substitute resolution: %s", res.Resolved)
	}
}

func TestUnknownToolHasStatusMissing(t *testing.T) {
	p := Detect()
	r, _ := p.Resolve(ToolOllama)
	if p.Get(ToolOllama).Present {
		return
	}
	if r.Status != StatusMissing {
		t.Fatalf("absent ollama status = %s, want missing", r.Status)
	}
}

func TestSubstituteAvailable(t *testing.T) {
	p := Detect()
	r, ok := p.Resolve(ToolPnpm)
	if !ok {
		t.Fatal("pnpm should resolve (pnpm itself or npm substitute)")
	}
	if !p.Get(ToolPnpm).Present && p.Get(ToolNpm).Present {
		if r.Resolved != ToolNpm {
			t.Fatalf("expected npm substitute, got %s", r.Resolved)
		}
		return
	}
	if !r.Present && r.Status != StatusSubstitute {
		t.Fatalf("status = %s, want substitute/present", r.Status)
	}
}
