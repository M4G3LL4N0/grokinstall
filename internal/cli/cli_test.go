package cli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func run(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	state := t.TempDir()
	t.Setenv("GROKINSTALL_HOME", state)
	var out, errBuf bytes.Buffer
	root := NewRoot()
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"--state-dir", state}, args...))
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func TestInspectHumanOutput(t *testing.T) {
	out, _, err := run(t, "inspect", "../inspect/testdata/node-repo")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "node-widget") {
		t.Fatalf("output missing project name:\n%s", out)
	}
	if !strings.Contains(out, "cli_entrypoint") {
		t.Fatalf("output missing evidence:\n%s", out)
	}
}

func TestInspectJSONOutput(t *testing.T) {
	out, _, err := run(t, "inspect", "../inspect/testdata/node-repo", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, out)
	}
	if payload["schema"] != "grokinstall/inspection/v1" {
		t.Fatalf("schema = %v", payload["schema"])
	}
}

func TestPlanRequiresGoalOptionIsOptional(t *testing.T) {
	_, _, err := run(t, "plan", "../inspect/testdata/docs-only")
	if err != nil {
		t.Fatalf("plan without a goal should still work and ask questions: %v", err)
	}
}

func TestPlanJSONHasComparisonAndToolchain(t *testing.T) {
	out, _, err := run(t, "plan", "../inspect/testdata/node-repo", "--goal", "run the widget", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var p map[string]any
	if err := json.Unmarshal([]byte(out), &p); err != nil {
		t.Fatalf("plan output is not JSON: %v\n%s", err, out)
	}
	for _, key := range []string{"comparison", "toolchain", "context_pack", "manifest", "capabilities"} {
		if _, ok := p[key]; !ok {
			t.Fatalf("plan JSON missing %q", key)
		}
	}
}

func TestCompareShowsEveryStrategy(t *testing.T) {
	out, _, err := run(t, "compare", "../inspect/testdata/docker-repo", "--goal", "run the service")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"cli_bridge", "api_bridge", "mcp_bridge", "docker_bridge", "no_install"} {
		if !strings.Contains(out, id) {
			t.Fatalf("compare output missing strategy %s:\n%s", id, out)
		}
	}
}

func TestCompareJSONHasAllTwelveDimensions(t *testing.T) {
	out, _, err := run(t, "compare", "../inspect/testdata/node-repo", "--goal", "use it", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Candidates []struct {
			Strategy    string `json:"strategy"`
			Assessments []struct {
				Dimension string `json:"dimension"`
				Rating    string `json:"rating"`
				Reason    string `json:"reason"`
			} `json:"assessments"`
		} `json:"candidates"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("compare output is not JSON: %v", err)
	}
	if len(payload.Candidates) != 10 {
		t.Fatalf("candidates = %d, want 10", len(payload.Candidates))
	}
	for _, c := range payload.Candidates {
		if len(c.Assessments) != 12 {
			t.Fatalf("%s has %d assessments, want 12", c.Strategy, len(c.Assessments))
		}
	}
}

func TestDoctorRuns(t *testing.T) {
	out, _, err := run(t, "doctor")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "git") {
		t.Fatalf("doctor output should mention tools:\n%s", out)
	}
}

func TestDoctorJSON(t *testing.T) {
	out, _, err := run(t, "doctor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var rep struct {
		Healthy bool `json:"healthy"`
		Checks  []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("doctor output is not JSON: %v", err)
	}
	if !rep.Healthy {
		t.Fatal("a fresh state directory should be healthy")
	}
	if len(rep.Checks) == 0 {
		t.Fatal("doctor must emit checks")
	}
}

func TestUsageStartsEmptyThenCounts(t *testing.T) {
	out, _, err := run(t, "usage", "--json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "total_events") {
		t.Fatalf("usage JSON missing total_events:\n%s", out)
	}
}

func TestUsageRecordsInspectRuns(t *testing.T) {
	state := t.TempDir()
	t.Setenv("GROKINSTALL_HOME", state)
	var inspectOut bytes.Buffer
	root := NewRoot()
	root.SetOut(&inspectOut)
	root.SetErr(&inspectOut)
	root.SetArgs([]string{"--state-dir", state, "inspect", "../inspect/testdata/node-repo"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var usageOut bytes.Buffer
	root2 := NewRoot()
	root2.SetOut(&usageOut)
	root2.SetErr(&usageOut)
	root2.SetArgs([]string{"--state-dir", state, "usage", "--json"})
	if err := root2.Execute(); err != nil {
		t.Fatal(err)
	}
	var sum struct {
		TotalEvents int `json:"total_events"`
	}
	if err := json.Unmarshal(usageOut.Bytes(), &sum); err != nil {
		t.Fatalf("usage output is not JSON: %v\n%s", err, usageOut.String())
	}
	if sum.TotalEvents < 1 {
		t.Fatal("inspect should have been recorded in usage")
	}
}

func TestUnknownSourceIsAnError(t *testing.T) {
	_, _, err := run(t, "inspect", filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected an error for a missing source")
	}
}

func TestGitLabSourceExplainsPart1Limitation(t *testing.T) {
	_, _, err := run(t, "inspect", "https://gitlab.com/acme/widget")
	if err == nil {
		t.Fatal("expected gitlab to be rejected in Part 1")
	}
	if !strings.Contains(err.Error(), "Part 1") {
		t.Fatalf("error should mention Part 1: %v", err)
	}
}

func TestVersionFlag(t *testing.T) {
	var out bytes.Buffer
	root := NewRoot()
	root.SetOut(&out)
	root.SetArgs([]string{"--version"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "grokinstall") {
		t.Fatalf("version output unexpected: %s", out.String())
	}
}

func TestVersionCommandReportsDevelopmentBuild(t *testing.T) {
	out, _, err := run(t, "version")
	if err != nil {
		t.Fatal(err)
	}
	// A development build must say so rather than claiming to be a release.
	if !strings.Contains(out, "development build") {
		t.Fatalf("a development build should identify itself: %s", out)
	}
}

func TestVersionJSON(t *testing.T) {
	out, _, err := run(t, "version", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		Version     string `json:"version"`
		GoVersion   string `json:"go_version"`
		Platform    string `json:"platform"`
		Development bool   `json:"development"`
	}
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatalf("version --json invalid: %v", err)
	}
	if info.Version == "" || info.GoVersion == "" || info.Platform == "" {
		t.Fatalf("version JSON incomplete: %+v", info)
	}
}
