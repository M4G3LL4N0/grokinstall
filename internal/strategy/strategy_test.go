package strategy

import (
	"strings"
	"testing"

	"grokinstall/internal/capability"
	"grokinstall/internal/evidence"
	"grokinstall/internal/toolchain"
)

func caps(kinds ...capability.Kind) []capability.Capability {
	var out []capability.Capability
	for _, k := range kinds {
		out = append(out, capability.Capability{
			Name:   string(k) + ".main",
			Kind:   k,
			Usable: k == capability.KindCLI || k == capability.KindKnowledge,
			Status: "detected",
		})
	}
	return out
}

func fullProfile() *toolchain.Profile {
	return &toolchain.Profile{Tools: map[toolchain.ToolID]toolchain.Tool{
		toolchain.ToolGit:    {ID: toolchain.ToolGit, Present: true, Name: "Git"},
		toolchain.ToolGo:     {ID: toolchain.ToolGo, Present: true, Name: "Go"},
		toolchain.ToolDocker: {ID: toolchain.ToolDocker, Present: true, Name: "Docker"},
	}}
}

func TestAllStrategiesAreCompared(t *testing.T) {
	c := Compare("use this for x", caps(capability.KindCLI), evidence.New(), fullProfile())
	if len(c.Candidates) != len(All) {
		t.Fatalf("candidates = %d, want %d", len(c.Candidates), len(All))
	}
	for _, want := range All {
		if c.Candidate(want) == nil {
			t.Fatalf("strategy %q missing from comparison", want)
		}
	}
}

func TestEveryCandidateCoversEveryDimension(t *testing.T) {
	c := Compare("use this", caps(capability.KindCLI, capability.KindAPI), evidence.New(), fullProfile())
	for _, cand := range c.Candidates {
		if len(cand.Assessments) != len(Dimensions) {
			t.Fatalf("%s has %d assessments, want %d", cand.Strategy, len(cand.Assessments), len(Dimensions))
		}
	}
}

func TestDimensionsAreTheSpecifiedTwelve(t *testing.T) {
	if len(Dimensions) != 12 {
		t.Fatalf("dimensions = %d, want 12", len(Dimensions))
	}
	want := []string{
		"grokbot_footprint", "feature_coverage", "local_execution", "external_cost",
		"latency", "maintenance", "security", "privacy", "cacheability",
		"dependencies", "implementation_effort", "portability",
	}
	got := make([]string, len(Dimensions))
	for i, d := range Dimensions {
		got[i] = string(d)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("dimension %d = %q, want %q", i, got[i], w)
		}
	}
}

func TestCLIBridgeRecommendedForCLI(t *testing.T) {
	c := Compare("let GrokBot run this tool", caps(capability.KindCLI), evidence.New(), fullProfile())
	if c.Recommended != StrategyCLIBridge {
		t.Fatalf("recommended = %q, want cli_bridge", c.Recommended)
	}
}

func TestKnowledgeGoalWinsOverAvailableCLI(t *testing.T) {
	// A project can ship both a CLI and documentation. When the user explicitly
	// asks for documentation retrieval, indexing the docs is the smaller and
	// more faithful capability, even though a CLI is also available.
	c := Compare("Let GrokBot retrieve relevant documentation about this project",
		caps(capability.KindCLI, capability.KindKnowledge), evidence.New(), fullProfile())
	if c.Recommended != StrategyKnowledgeImport {
		t.Fatalf("recommended = %q, want knowledge_import for a documentation goal", c.Recommended)
	}
}

func TestExecutionGoalStillPrefersCLI(t *testing.T) {
	// The goal-awareness rule must not override an explicitly operational goal.
	c := Compare("let GrokBot run this project's CLI", caps(capability.KindCLI, capability.KindKnowledge), evidence.New(), fullProfile())
	if c.Recommended != StrategyCLIBridge {
		t.Fatalf("recommended = %q, want cli_bridge for an execution goal", c.Recommended)
	}
}

func TestNoInstallRecommendedWhenNothingIsUsable(t *testing.T) {
	c := Compare("summarize this documentation", nil, evidence.New(), fullProfile())
	if c.Recommended != StrategyNoInstall {
		t.Fatalf("recommended = %q, want no_install", c.Recommended)
	}
}

func TestKnowledgeImportRecommendedForDocsGoal(t *testing.T) {
	c := Compare("let GrokBot search the documentation", caps(capability.KindKnowledge), evidence.New(), fullProfile())
	if c.Recommended != StrategyKnowledgeImport {
		t.Fatalf("recommended = %q, want knowledge_import", c.Recommended)
	}
}

func TestAPIBridgeMarkedPlannedNotInstalled(t *testing.T) {
	c := Compare("call the api", caps(capability.KindAPI), evidence.New(), fullProfile())
	cand := c.Candidate(StrategyAPIBridge)
	if cand == nil || !cand.Applicable {
		t.Fatal("api_bridge should be applicable when an OpenAPI spec exists")
	}
	if cand.Support != SupportPlanned {
		t.Fatalf("support = %q, want planned (Part 1 does not install it)", cand.Support)
	}
}

func TestDockerBridgeDegradesWithoutDocker(t *testing.T) {
	withoutDocker := &toolchain.Profile{Tools: map[toolchain.ToolID]toolchain.Tool{
		toolchain.ToolGit: {ID: toolchain.ToolGit, Present: true, Name: "Git"},
	}}
	c := Compare("run the service", caps(capability.KindDocker), evidence.New(), withoutDocker)
	cand := c.Candidate(StrategyDockerBridge)
	if cand.Feasibility != RatingPoor {
		t.Fatalf("feasibility = %q, want poor without docker", cand.Feasibility)
	}
	if len(cand.Requires) == 0 {
		t.Fatal("docker_bridge must declare its tool requirement")
	}
}

func TestGeneratedAdapterRequiresCodingWorker(t *testing.T) {
	p := &toolchain.Profile{Tools: map[toolchain.ToolID]toolchain.Tool{
		toolchain.ToolGit: {ID: toolchain.ToolGit, Present: true, Name: "Git"},
	}}
	c := Compare("wrap the local service", caps(capability.KindLocalService), evidence.New(), p)
	cand := c.Candidate(StrategyGeneratedAdapter)
	if cand.Applicable != true {
		t.Fatal("generated_adapter should be considered for a service with no CLI")
	}
	if cand.Feasibility != RatingFair && cand.Feasibility != RatingPoor {
		t.Fatalf("feasibility = %q, want fair/poor without a coding worker", cand.Feasibility)
	}
}

func TestNoInstallIsAlwaysAvailable(t *testing.T) {
	c := Compare("anything", caps(capability.KindCLI, capability.KindAPI, capability.KindDocker), evidence.New(), fullProfile())
	cand := c.Candidate(StrategyNoInstall)
	if cand == nil || !cand.Applicable {
		t.Fatal("no_install must always remain a legitimate option")
	}
}

func TestRecommendationCarriesEvidenceAndReason(t *testing.T) {
	ev := evidence.New()
	ev.Add("cli_entrypoint", evidence.ConfidenceHigh, "package.json bin field: widget")
	c := Compare("use widget", caps(capability.KindCLI), ev, fullProfile())
	if c.RecommendationReason == "" {
		t.Fatal("recommendation must explain itself")
	}
	found := false
	for _, s := range c.RecommendationEvidence {
		if strings.Contains(s, "package.json") {
			found = true
		}
	}
	if !found {
		t.Fatalf("recommendation evidence missing the inspection evidence: %v", c.RecommendationEvidence)
	}
}

func TestGoalMatchIsDeterministic(t *testing.T) {
	g := InterpretGoal("search the documentation for install steps", caps(capability.KindKnowledge, capability.KindCLI))
	if g.Intent != IntentKnowledge {
		t.Fatalf("intent = %q, want knowledge", g.Intent)
	}
}

func TestNoFabricatedNumbers(t *testing.T) {
	c := Compare("use it", caps(capability.KindCLI), evidence.New(), fullProfile())
	for _, cand := range c.Candidates {
		for _, a := range cand.Assessments {
			for _, r := range a.Reason {
				if r >= '0' && r <= '9' && strings.Contains(a.Reason, "%") {
					t.Fatalf("assessment fabricates a percentage: %q", a.Reason)
				}
			}
		}
	}
}
