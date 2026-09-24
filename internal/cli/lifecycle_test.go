package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// acceptanceEnv builds a throwaway state directory and a source fixture so the
// full install lifecycle can be exercised without touching real state.
func acceptanceEnv(t *testing.T) (state string, source string) {
	t.Helper()
	state = t.TempDir()
	source = t.TempDir()
	write(t, source, "README.md", "# widget\n\nA widget CLI fixture.\n")
	write(t, source, "package.json", `{"name":"widget","version":"1.0.0","bin":{"widget":"bin/widget.sh"}}`)
	script := "#!/bin/sh\ninput=$(cat)\nprintf '{\"ok\":true,\"result\":{\"seen\":%s}}' \"$input\"\n"
	if err := os.MkdirAll(filepath.Join(source, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "bin", "widget.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return state, source
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exec(t *testing.T, state string, args ...string) (string, string, error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	root := NewRoot()
	root.SetOut(&out)
	root.SetErr(&errBuf)
	root.SetArgs(append([]string{"--state-dir", state}, args...))
	err := root.Execute()
	return out.String(), errBuf.String(), err
}

func TestAcceptanceLifecycle(t *testing.T) {
	state, source := acceptanceEnv(t)

	// inspect
	if _, _, err := exec(t, state, "inspect", source); err != nil {
		t.Fatalf("inspect: %v", err)
	}
	// compare
	if _, _, err := exec(t, state, "compare", source, "--goal", "let GrokBot run the widget CLI"); err != nil {
		t.Fatalf("compare: %v", err)
	}
	// install
	out, _, err := exec(t, state, "install", source, "--goal", "let GrokBot run the widget CLI")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	if !strings.Contains(out, "installed") {
		t.Fatalf("install output unexpected:\n%s", out)
	}

	// list
	out, _, err = exec(t, state, "list")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "widget") {
		t.Fatalf("list should show the capability:\n%s", out)
	}

	// capabilities
	out, _, err = exec(t, state, "capabilities")
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	if !strings.Contains(out, "widget") {
		t.Fatalf("capabilities should enumerate it:\n%s", out)
	}

	// grokbot contract
	out, _, err = exec(t, state, "grokbot", "widget.run")
	if err != nil {
		t.Fatalf("grokbot: %v\n%s", err, out)
	}
	if !strings.Contains(out, "CAPABILITY") {
		t.Fatalf("contract shape wrong:\n%s", out)
	}

	// test
	out, _, err = exec(t, state, "test", "widget.run")
	if err != nil {
		t.Fatalf("test: %v\n%s", err, out)
	}
	if !strings.Contains(strings.ToLower(out), "pass") {
		t.Fatalf("smoke test should report a pass:\n%s", out)
	}

	// run
	out, _, err = exec(t, state, "run", "widget.run", "--input", `{"query":"hello"}`)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "hello") {
		t.Fatalf("run should echo input:\n%s", out)
	}

	// info
	out, _, err = exec(t, state, "info", "widget.run")
	if err != nil {
		t.Fatalf("info: %v\n%s", err, out)
	}
	if !strings.Contains(out, "cli_bridge") {
		t.Fatalf("info should show the strategy:\n%s", out)
	}

	// uninstall
	if _, _, err := exec(t, state, "uninstall", "widget.run"); err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	// registry no longer exposes it
	out, _, _ = exec(t, state, "capabilities", "--json")
	var payload struct {
		Capabilities []struct {
			Name string `json:"name"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("capabilities --json invalid: %v\n%s", err, out)
	}
	for _, c := range payload.Capabilities {
		if c.Name == "widget.run" {
			t.Fatal("uninstalled capability is still exposed")
		}
	}
	// upstream source survives
	if _, err := os.Stat(filepath.Join(source, "package.json")); err != nil {
		t.Fatal("upstream source must survive uninstall")
	}
}

func TestInstallJSON(t *testing.T) {
	state, source := acceptanceEnv(t)
	out, _, err := exec(t, state, "install", source, "--goal", "run the widget CLI", "--json")
	if err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	var res struct {
		Result         string `json:"result"`
		Strategy       string `json:"strategy"`
		Capability     string `json:"capability"`
		ManifestPath   string `json:"manifest_path"`
		ReceiptPath    string `json:"receipt_path"`
		PlannedActions struct {
			VerificationPlan []string `json:"verification_plan"`
		} `json:"planned_actions"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("install --json invalid: %v\n%s", err, out)
	}
	if res.Result != "installed" || res.Capability == "" || res.ManifestPath == "" {
		t.Fatalf("install JSON incomplete: %+v", res)
	}
}

func TestInstallDryRunJSON(t *testing.T) {
	state, source := acceptanceEnv(t)
	out, _, err := exec(t, state, "install", source, "--goal", "run the widget CLI", "--dry-run", "--json")
	if err != nil {
		t.Fatalf("dry run: %v\n%s", err, out)
	}
	var res struct {
		DryRun         bool `json:"dry_run"`
		PlannedActions struct {
			FilesCreated     []string `json:"files_created"`
			RegistryChanges  []string `json:"registry_changes"`
			VerificationPlan []string `json:"verification_plan"`
		} `json:"planned_actions"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("dry run JSON invalid: %v\n%s", err, out)
	}
	if !res.DryRun {
		t.Fatal("dry_run flag must be set")
	}
	if len(res.PlannedActions.FilesCreated) == 0 {
		t.Fatal("dry run must list files it would create")
	}
	if len(res.PlannedActions.RegistryChanges) == 0 {
		t.Fatal("dry run must list registry changes")
	}
	if len(res.PlannedActions.VerificationPlan) == 0 {
		t.Fatal("dry run must list the verification plan")
	}
	// nothing registered
	out, _, _ = exec(t, state, "list", "--json")
	if strings.Contains(out, "widget.run") {
		t.Fatalf("dry run must not register anything:\n%s", out)
	}
}

func TestRunJSONEnvelope(t *testing.T) {
	state, source := acceptanceEnv(t)
	_, _, err := exec(t, state, "install", source, "--goal", "run the widget CLI", "--json")
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := exec(t, state, "run", "widget.run", "--input", `{"query":"x"}`, "--json")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	var resp struct {
		OK        bool   `json:"ok"`
		RawResult string `json:"raw_result"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("run --json invalid: %v\n%s", err, out)
	}
	if !resp.OK {
		t.Fatalf("run should succeed: %s", out)
	}
}

func TestRunReadsStdin(t *testing.T) {
	state, source := acceptanceEnv(t)
	_, _, _ = exec(t, state, "install", source, "--goal", "run the widget CLI", "--json")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteString(`{"query":"from-stdin"}`); err != nil {
		t.Fatal(err)
	}
	w.Close()
	oldStdin := os.Stdin
	os.Stdin = r
	defer func() { os.Stdin = oldStdin }()

	out, _, err := exec(t, state, "run", "widget.run", "--json")
	if err != nil {
		t.Fatalf("run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "from-stdin") {
		t.Fatalf("stdin input should reach the capability:\n%s", out)
	}
}

func TestRunUnknownFails(t *testing.T) {
	state, _ := acceptanceEnv(t)
	if _, _, err := exec(t, state, "run", "nope", "--input", "{}"); err == nil {
		t.Fatal("running an unknown capability must fail")
	}
}

func TestListEmpty(t *testing.T) {
	state, _ := acceptanceEnv(t)
	out, _, err := exec(t, state, "list")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(out), "no") {
		t.Fatalf("empty list should say so:\n%s", out)
	}
}

func TestCapabilitiesJSONIsCompact(t *testing.T) {
	state, source := acceptanceEnv(t)
	_, _, _ = exec(t, state, "install", source, "--goal", "run the widget CLI", "--json")
	out, _, err := exec(t, state, "capabilities", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Capabilities []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Input       struct {
				Fields []map[string]any `json:"fields"`
			} `json:"input"`
			Output struct {
				Fields []map[string]any `json:"fields"`
			} `json:"output"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("invalid: %v\n%s", err, out)
	}
	if len(payload.Capabilities) != 1 {
		t.Fatalf("capabilities = %d", len(payload.Capabilities))
	}
	c := payload.Capabilities[0]
	if c.Name == "" || c.Description == "" {
		t.Fatalf("capability entry incomplete: %+v", c)
	}
	// Must not leak implementation detail
	if strings.Contains(out, "package.json") || strings.Contains(out, "README") {
		t.Fatalf("capabilities leaked source detail:\n%s", out)
	}
}

func TestGrokbotContractIsBounded(t *testing.T) {
	state, source := acceptanceEnv(t)
	_, _, _ = exec(t, state, "install", source, "--goal", "run the widget CLI", "--json")
	out, _, err := exec(t, state, "grokbot", "widget.run")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > 8192 {
		t.Fatalf("contract too large: %d bytes", len(out))
	}
}

func TestGrokbotJSON(t *testing.T) {
	state, source := acceptanceEnv(t)
	_, _, _ = exec(t, state, "install", source, "--goal", "run the widget CLI", "--json")
	out, _, err := exec(t, state, "grokbot", "widget.run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Name     string `json:"name"`
		Call     string `json:"call"`
		Contract string `json:"contract"`
		Bytes    int    `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("grokbot --json invalid: %v\n%s", err, out)
	}
	if payload.Name == "" || payload.Call == "" || payload.Bytes == 0 {
		t.Fatalf("grokbot JSON incomplete: %+v", payload)
	}
}

func TestUninstallJSON(t *testing.T) {
	state, source := acceptanceEnv(t)
	_, _, _ = exec(t, state, "install", source, "--goal", "run the widget CLI", "--json")
	out, _, err := exec(t, state, "uninstall", "widget.run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Uninstalled string   `json:"uninstalled"`
		Removed     []string `json:"removed"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("uninstall --json invalid: %v\n%s", err, out)
	}
	if payload.Uninstalled != "widget.run" {
		t.Fatalf("uninstall JSON incomplete: %+v", payload)
	}
}

func TestKnowledgeInstallAndSearch(t *testing.T) {
	state := t.TempDir()
	source := t.TempDir()
	write(t, source, "README.md", "# project\n\nA documented project.\n")
	write(t, source, "docs/auth.md", "# Authentication\n\nUse a bearer token in the Authorization header.\n")
	write(t, source, "docs/deploy.md", "# Deployment\n\nRun the service with containers.\n")

	out, _, err := exec(t, state, "install", source, "--goal", "let GrokBot search these docs", "--json")
	if err != nil {
		t.Fatalf("knowledge install: %v\n%s", err, out)
	}
	var res struct {
		Strategy   string `json:"strategy"`
		Capability string `json:"capability"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Strategy != "knowledge_import" {
		t.Fatalf("strategy = %q, want knowledge_import", res.Strategy)
	}
	out, _, err = exec(t, state, "run", res.Capability, "--input", `{"query":"bearer token"}`, "--json")
	if err != nil {
		t.Fatalf("knowledge run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "auth.md") {
		t.Fatalf("expected the auth doc:\n%s", out)
	}
}

func TestNoInstallIsReportedAsSuccess(t *testing.T) {
	state := t.TempDir()
	source := t.TempDir()
	write(t, source, "README.md", "# notes\n\nOnly prose with no interface.\n")
	out, _, err := exec(t, state, "install", source, "--goal", "summarize the notes", "--json")
	if err != nil {
		t.Fatalf("no_install must exit successfully: %v\n%s", err, out)
	}
	var res struct {
		Result     string `json:"result"`
		Capability string `json:"capability"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if res.Result != "no_install" {
		t.Fatalf("result = %q, want no_install", res.Result)
	}
	if res.Capability != "" {
		t.Fatal("no_install must not fabricate a capability")
	}
}

func TestInfoShowsVerificationAndReceipt(t *testing.T) {
	state, source := acceptanceEnv(t)
	_, _, _ = exec(t, state, "install", source, "--goal", "run the widget CLI", "--json")
	out, _, err := exec(t, state, "info", "widget.run", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		Name         string `json:"name"`
		Strategy     string `json:"strategy"`
		Status       string `json:"status"`
		Verification struct {
			Passed bool `json:"passed"`
		} `json:"verification"`
		Receipt struct {
			InstallID string `json:"install_id"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("info --json invalid: %v\n%s", err, out)
	}
	if !payload.Verification.Passed {
		t.Fatal("info should report passing verification")
	}
	if payload.Receipt.InstallID == "" {
		t.Fatal("info should expose the receipt")
	}
}

func TestTestCommandReportsFailureExit(t *testing.T) {
	state, source := acceptanceEnv(t)
	_, _, _ = exec(t, state, "install", source, "--goal", "run the widget CLI", "--json")
	// break the upstream executable
	if err := os.Remove(filepath.Join(source, "bin", "widget.sh")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := exec(t, state, "test", "widget.run"); err == nil {
		t.Fatal("a broken capability must fail its smoke test with a non-zero exit")
	}
}

func TestUsageTracksInstallsAndRuns(t *testing.T) {
	state, source := acceptanceEnv(t)
	_, _, _ = exec(t, state, "install", source, "--goal", "run the widget CLI", "--json")
	_, _, _ = exec(t, state, "run", "widget.run", "--input", `{"a":1}`, "--json")
	out, _, err := exec(t, state, "usage", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var sum struct {
		Installs int `json:"installs"`
		Runs     int `json:"runs"`
	}
	if err := json.Unmarshal([]byte(out), &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Installs < 1 || sum.Runs < 1 {
		t.Fatalf("usage should record installs and runs: %+v", sum)
	}
}
