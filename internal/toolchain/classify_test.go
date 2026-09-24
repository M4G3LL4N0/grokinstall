package toolchain

import "testing"

func fakeProfile() *Profile {
	return &Profile{Tools: map[ToolID]Tool{
		ToolGit:      {ID: ToolGit, Present: true, Name: "Git"},
		ToolNpm:      {ID: ToolNpm, Present: true, Name: "npm"},
		ToolPython:   {ID: ToolPython, Present: true, Name: "Python"},
		ToolPnpm:     {ID: ToolPnpm, Present: false, Name: "pnpm", Substitutes: []ToolID{ToolNpm}},
		ToolOpenCode: {ID: ToolOpenCode, Present: false, Name: "OpenCode", Role: RoleCoder, Why: "reasoning/coding agent for generated adapter work"},
		ToolDocker:   {ID: ToolDocker, Present: false, Name: "Docker", Why: "containerization"},
	}}
}

func TestClassifyPresentRequired(t *testing.T) {
	p := fakeProfile()
	needs := p.Classify(NeedSet{Required: []ToolID{ToolGit}})
	if len(needs) != 1 {
		t.Fatalf("needs = %+v", needs)
	}
	if needs[0].Label != LabelRequired {
		t.Fatalf("label = %q, want required", needs[0].Label)
	}
}

func TestClassifyMissingRequiredStaysRequiredWithMissingFlag(t *testing.T) {
	p := fakeProfile()
	needs := p.Classify(NeedSet{Required: []ToolID{ToolDocker}})
	if needs[0].Label != LabelMissing {
		t.Fatalf("label = %q, want missing", needs[0].Label)
	}
	if !needs[0].Required {
		t.Fatal("a missing required tool must still be marked required")
	}
}

func TestClassifySubstituteAvailable(t *testing.T) {
	p := fakeProfile()
	needs := p.Classify(NeedSet{Required: []ToolID{ToolPnpm}})
	if needs[0].Label != LabelSubstituteAvailable {
		t.Fatalf("label = %q, want substitute_available", needs[0].Label)
	}
	if needs[0].Resolved != ToolNpm {
		t.Fatalf("resolved = %q, want npm", needs[0].Resolved)
	}
}

func TestClassifyOptionalMissingIsNotFatal(t *testing.T) {
	p := fakeProfile()
	needs := p.Classify(NeedSet{Optional: []ToolID{ToolOpenCode}})
	if needs[0].Label != LabelMissing {
		t.Fatalf("label = %q, want missing", needs[0].Label)
	}
	if needs[0].Required {
		t.Fatal("optional tool must not be marked required")
	}
	if needs[0].Why == "" {
		t.Fatal("needs should explain what the tool is for")
	}
}

func TestClassifyIrrelevant(t *testing.T) {
	p := fakeProfile()
	needs := p.Classify(NeedSet{Irrelevant: []ToolID{ToolOpenCode}})
	if needs[0].Label != LabelIrrelevant {
		t.Fatalf("label = %q, want irrelevant", needs[0].Label)
	}
}

func TestClassifyRecommendedPresent(t *testing.T) {
	p := fakeProfile()
	needs := p.Classify(NeedSet{Recommended: []ToolID{ToolGit}})
	if needs[0].Label != LabelRecommended {
		t.Fatalf("label = %q, want recommended", needs[0].Label)
	}
}

func TestClassifyDeduplicatesAcrossBuckets(t *testing.T) {
	p := fakeProfile()
	needs := p.Classify(NeedSet{Required: []ToolID{ToolGit}, Optional: []ToolID{ToolGit}})
	if len(needs) != 1 {
		t.Fatalf("expected one entry, got %+v", needs)
	}
}
