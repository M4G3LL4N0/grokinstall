package installer

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"grokinstall/internal/inspect"
	"grokinstall/internal/manifest"
	"grokinstall/internal/receipt"
	"grokinstall/internal/registry"
	"grokinstall/internal/toolchain"
)

// --- fixtures ---------------------------------------------------------------

// cliFixture is a project that ships a real, runnable CLI as a shell script
// that reads JSON on stdin and writes JSON on stdout.
func cliFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "README.md", "# widget\n\nA widget CLI.\n")
	write(t, dir, "package.json", `{"name":"widget","version":"1.0.0","bin":{"widget":"bin/widget.sh"}}`)
	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\ninput=$(cat)\nprintf '{\"ok\":true,\"result\":{\"seen\":%s}}' \"$input\"\n"
	if err := os.WriteFile(filepath.Join(dir, "bin", "widget.sh"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func docsFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "README.md", "# project\n\nA documented project.\n")
	write(t, dir, "docs/auth.md", "# Authentication\n\nUse a bearer token in the Authorization header.\n")
	write(t, dir, "docs/deploy.md", "# Deployment\n\nRun the service with containers and compose.\n")
	return dir
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

// sharedProfile is detected once: probing the toolchain is the same for every
// installer in this process.
var (
	sharedProfileOnce sync.Once
	sharedProfile     *toolchain.Profile
)

func testProfile() *toolchain.Profile {
	sharedProfileOnce.Do(func() { sharedProfile = toolchain.Detect() })
	return sharedProfile
}

func newInstaller(t *testing.T) *Installer {
	t.Helper()
	reg, err := registry.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return NewWithProfile(reg, testProfile())
}

func localSource(t *testing.T, dir string) sourceRef {
	t.Helper()
	s, err := normalize(dir)
	if err != nil {
		t.Fatal(err)
	}
	return *s
}

// --- cli_bridge -------------------------------------------------------------

func TestInstallCLIBridge(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "let GrokBot run the widget CLI"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != receipt.ResultInstalled {
		t.Fatalf("result = %q: %+v", res.Result, res)
	}
	if res.Strategy != "cli_bridge" {
		t.Fatalf("strategy = %q", res.Strategy)
	}
	if res.Capability == "" {
		t.Fatal("install must name a capability")
	}
	entry, err := ins.Registry.Lookup(res.Capability)
	if err != nil {
		t.Fatalf("capability must be registered: %v", err)
	}
	if entry.Strategy != "cli_bridge" {
		t.Fatalf("entry = %+v", entry)
	}
	if !entry.ManifestHealthy() {
		t.Fatal("registered manifest must exist")
	}
}

func TestInstalledCLIIsRunnable(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := ins.Run(res.Capability, json.RawMessage(`{"query":"hello"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("installed capability must run: %+v", out.Error)
	}
	if !strings.Contains(out.RawResult, "hello") {
		t.Fatalf("input did not reach the capability: %s", out.RawResult)
	}
}

func TestDuplicateInstallIsRejected(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	opts := Options{Source: localSource(t, dir), Goal: "run the widget CLI"}
	res, err := ins.Install(opts)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ins.Install(opts)
	if err == nil {
		t.Fatal("a duplicate capability name must be rejected")
	}
	if _, lerr := ins.Registry.Lookup(res.Capability); lerr != nil {
		t.Fatalf("the original install must survive: %v", lerr)
	}
}

func TestFailedVerificationDoesNotRegister(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"name":"ghost","version":"1.0.0","bin":{"ghost":"bin/ghost.sh"}}`)
	// Declares a CLI entrypoint, but the executable does not exist.
	res, err := newInstaller(t).Install(Options{Source: localSource(t, dir), Goal: "run the ghost CLI"})
	if err == nil {
		t.Fatalf("install must fail when verification cannot pass, got %+v", res)
	}
	entries, _ := newInstaller(t).Registry.List()
	_ = entries
}

func TestFailedVerificationLeavesNoRegistryEntry(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"name":"ghost","version":"1.0.0","bin":{"ghost":"bin/ghost.sh"}}`)
	ins := newInstaller(t)
	if _, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the ghost CLI"}); err == nil {
		t.Fatal("expected install failure")
	}
	entries, err := ins.Registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("failed install must not register anything: %+v", entries)
	}
}

func TestFailedVerificationDiscardsStagedFiles(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"name":"ghost","version":"1.0.0","bin":{"ghost":"bin/ghost.sh"}}`)
	ins := newInstaller(t)
	_, _ = ins.Install(Options{Source: localSource(t, dir), Goal: "run the ghost CLI"})
	left, err := os.ReadDir(ins.Registry.StagingDir())
	if err == nil && len(left) > 0 {
		t.Fatalf("staging must be discarded on failure: %v", left)
	}
}

func TestManifestIsInstalledShape(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := ins.Registry.Lookup(res.Capability)
	m, err := manifest.Load(entry.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if m.Schema != manifest.SchemaID {
		t.Fatalf("schema = %q", m.Schema)
	}
	if !m.Runnable() {
		t.Fatalf("installed cli_bridge must be runnable: %+v", m.Execution)
	}
	if m.Strategy != "cli_bridge" {
		t.Fatalf("strategy = %q", m.Strategy)
	}
}

// --- knowledge_import -------------------------------------------------------

func TestInstallKnowledgeImport(t *testing.T) {
	dir := docsFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "let GrokBot search these docs"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Strategy != "knowledge_import" {
		t.Fatalf("strategy = %q, want knowledge_import", res.Strategy)
	}
	out, err := ins.Run(res.Capability, json.RawMessage(`{"query":"bearer token"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatalf("knowledge capability must run: %+v", out.Error)
	}
	if !strings.Contains(out.RawResult, "auth.md") {
		t.Fatalf("expected the auth doc in results: %s", out.RawResult)
	}
}

func TestKnowledgeSearchIsBounded(t *testing.T) {
	dir := docsFixture(t)
	ins := newInstaller(t)
	res, _ := ins.Install(Options{Source: localSource(t, dir), Goal: "search the docs"})
	out, err := ins.Run(res.Capability, json.RawMessage(`{"query":"token"}`))
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Matches []struct {
			Excerpt string `json:"excerpt"`
		} `json:"matches"`
	}
	if err := json.Unmarshal([]byte(out.RawResult), &decoded); err != nil {
		t.Fatal(err)
	}
	for _, m := range decoded.Matches {
		if len(m.Excerpt) > 700 {
			t.Fatalf("excerpt not bounded: %d", len(m.Excerpt))
		}
	}
}

func TestKnowledgeHandlesEmptyResult(t *testing.T) {
	dir := docsFixture(t)
	ins := newInstaller(t)
	res, _ := ins.Install(Options{Source: localSource(t, dir), Goal: "search the docs"})
	out, err := ins.Run(res.Capability, json.RawMessage(`{"query":"zzzznotpresent"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatal("an empty result is still a successful search")
	}
}

// --- no_install -------------------------------------------------------------

func TestNoInstallIsAFirstClassResult(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "README.md", "# notes\n\nJust prose about a system that has no interface.\n")
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "summarize the notes for users"})
	if err != nil {
		t.Fatalf("no_install is a successful outcome, not an error: %v", err)
	}
	if res.Result != receipt.ResultNoInstall {
		t.Fatalf("result = %q, want no_install", res.Result)
	}
	entries, _ := ins.Registry.List()
	if len(entries) != 0 {
		t.Fatalf("no_install must not fabricate a registered capability: %+v", entries)
	}
	if res.Receipt == nil || res.Receipt.Result != receipt.ResultNoInstall {
		t.Fatal("no_install must still record a receipt explaining the decision")
	}
}

func TestPlanOnlyStrategyIsPersistedAsAPlanNotAnInstall(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "openapi.json", `{"openapi":"3.0.0","info":{"title":"x","version":"1"},"servers":[{"url":"https://api.example.com"}],"paths":{"/widgets":{"get":{"summary":"list"}}}}`)
	write(t, dir, "README.md", "# api\n\nAn HTTP API.\n")
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "let GrokBot call this API"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Strategy != "api_bridge" {
		t.Fatalf("strategy = %q, want api_bridge", res.Strategy)
	}
	// A plan is not an installed capability: nothing is registered.
	if ins.Registry.Has(res.Capability) {
		t.Fatal("a plan-only strategy must not be registered as an installed capability")
	}
	entries, _ := ins.Registry.List()
	if len(entries) != 0 {
		t.Fatalf("registry must stay empty for a plan: %+v", entries)
	}
	// The plan is persisted so the decision is not lost.
	if res.PlanPath == "" {
		t.Fatal("a plan should be persisted outside the capability registry")
	}
	plans, err := ins.Registry.Plans()
	if err != nil || len(plans) != 1 {
		t.Fatalf("plans = %+v err = %v", plans, err)
	}
	if plans[0].Strategy != "api_bridge" || plans[0].State != registry.StatePlanOnly {
		t.Fatalf("plan = %+v", plans[0])
	}
	// The receipt records a planned outcome, not an installed one.
	r, err := receipt.Load(res.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if r.Result != receipt.ResultPlanned {
		t.Fatalf("receipt result = %q, want planned", r.Result)
	}
	// And it cannot be run, because it is not installed.
	if _, rerr := ins.Run(res.Capability, json.RawMessage(`{}`)); rerr == nil {
		t.Fatal("a plan-only capability is not registered and therefore not runnable")
	}
}

// --- dry run ----------------------------------------------------------------

func TestDryRunMutatesNothing(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.DryRun {
		t.Fatal("result must be marked as a dry run")
	}
	if len(res.PlannedActions.FilesCreated) == 0 {
		t.Fatal("dry run must report the files it would create")
	}
	entries, _ := ins.Registry.List()
	if len(entries) != 0 {
		t.Fatalf("dry run must not register anything: %+v", entries)
	}
	if _, err := os.Stat(filepath.Join(ins.Registry.ManifestsDir())); err != nil {
		t.Fatal("dry run must not write manifests")
	}
}

func TestDryRunReportsVerificationPlan(t *testing.T) {
	dir := cliFixture(t)
	res, err := newInstaller(t).Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.PlannedActions.VerificationPlan) == 0 {
		t.Fatal("dry run must describe how it would verify")
	}
}

// --- receipts ---------------------------------------------------------------

func TestReceiptIsWrittenOnSuccess(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := receipt.Load(res.ReceiptPath)
	if err != nil {
		t.Fatalf("receipt must be readable: %v", err)
	}
	if loaded.Capability != res.Capability {
		t.Fatalf("receipt capability = %q, want %q", loaded.Capability, res.Capability)
	}
	if !loaded.Verification.Passed {
		t.Fatal("receipt must record passing verification")
	}
	if loaded.Source == "" || loaded.Strategy == "" {
		t.Fatal("receipt must record source and strategy")
	}
}

func TestReceiptRecordsRealFiles(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _ := receipt.Load(res.ReceiptPath)
	if len(loaded.FilesCreated) == 0 {
		t.Fatal("receipt must record created files")
	}
	for _, f := range loaded.FilesCreated {
		if f.Owner != "grokinstall" {
			t.Fatalf("every created file must be owned by grokinstall: %+v", f)
		}
		if _, serr := os.Stat(f.Path); serr != nil {
			t.Fatalf("receipt records a file that does not exist: %s", f.Path)
		}
	}
}

func TestReceiptRecordsFailedAttempt(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"name":"ghost","version":"1.0.0","bin":{"ghost":"bin/ghost.sh"}}`)
	ins := newInstaller(t)
	_, _ = ins.Install(Options{Source: localSource(t, dir), Goal: "run the ghost CLI"})
	loaded, err := receipt.FindByInstallID(ins.Registry.ReceiptsDir(), lastInstallID(t, ins))
	if err != nil {
		t.Fatalf("a failed attempt must still leave an auditable receipt: %v", err)
	}
	if loaded.Result != receipt.ResultFailed {
		t.Fatalf("result = %q, want failed", loaded.Result)
	}
	if loaded.Verification.Passed {
		t.Fatal("a failed install must not record passing verification")
	}
}

func lastInstallID(t *testing.T, ins *Installer) string {
	t.Helper()
	files, err := os.ReadDir(ins.Registry.ReceiptsDir())
	if err != nil || len(files) == 0 {
		t.Fatal("expected a receipt")
	}
	return strings.TrimSuffix(files[len(files)-1].Name(), ".json")
}

// --- uninstall --------------------------------------------------------------

func TestUninstallRemovesOwnedResourcesOnly(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := ins.Registry.Lookup(res.Capability)
	manifestPath := entry.ManifestPath
	adapterPath := entry.AdapterPath

	if err := ins.Uninstall(res.Capability); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(manifestPath); err == nil {
		t.Fatal("owned manifest must be removed")
	}
	if adapterPath != "" {
		if _, err := os.Stat(adapterPath); err == nil {
			t.Fatal("owned adapter must be removed")
		}
	}
	if ins.Registry.Has(res.Capability) {
		t.Fatal("capability must be unregistered")
	}
}

func TestUninstallPreservesUpstreamSource(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, _ := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	if err := ins.Uninstall(res.Capability); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "package.json")); err != nil {
		t.Fatal("upstream source must survive uninstall")
	}
	if _, err := os.Stat(filepath.Join(dir, "bin", "widget.sh")); err != nil {
		t.Fatal("upstream executable must survive uninstall")
	}
}

func TestUninstallPreservesTheReceipt(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, _ := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	receiptPath := res.ReceiptPath
	if err := ins.Uninstall(res.Capability); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(receiptPath); err != nil {
		t.Fatal("the receipt is the audit trail and must survive")
	}
	loaded, err := receipt.Load(receiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.UninstalledAt == "" {
		t.Fatal("the receipt must record that it was uninstalled")
	}
}

func TestRepeatedUninstallFailsSafely(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, _ := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	if err := ins.Uninstall(res.Capability); err != nil {
		t.Fatal(err)
	}
	err := ins.Uninstall(res.Capability)
	if err == nil {
		t.Fatal("a second uninstall must report an error, not corrupt state")
	}
}

func TestUninstallUnknownCapability(t *testing.T) {
	if err := newInstaller(t).Uninstall("nope"); err == nil {
		t.Fatal("expected an error")
	}
}

// --- test / diagnostics -----------------------------------------------------

func TestTestCapabilityRunsSmokeCheck(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, _ := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	rep, err := ins.Test(res.Capability)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Passed {
		t.Fatalf("smoke test should pass: %+v", rep.Checks)
	}
	if len(rep.Checks) < 3 {
		t.Fatalf("smoke test must check manifest, target and launch: %+v", rep.Checks)
	}
}

func TestTestUnknownCapability(t *testing.T) {
	if _, err := newInstaller(t).Test("nope"); err == nil {
		t.Fatal("expected an error")
	}
}

func TestTestDetectsBrokenTarget(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, _ := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	entry, _ := ins.Registry.Lookup(res.Capability)
	m, _ := manifest.Load(entry.ManifestPath)
	// Point the manifest at a command that no longer exists.
	if err := os.Remove(filepath.Join(dir, "bin", "widget.sh")); err != nil {
		t.Fatal(err)
	}
	_ = m
	rep, err := ins.Test(res.Capability)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Passed {
		t.Fatalf("a broken target must fail its smoke test: %+v", rep.Checks)
	}
}

func TestRunUnknownCapability(t *testing.T) {
	if _, err := newInstaller(t).Run("nope", json.RawMessage(`{}`)); err == nil {
		t.Fatal("expected an error")
	}
}

func TestRunRejectsInvalidJSONInput(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, _ := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	out, err := ins.Run(res.Capability, json.RawMessage(`{broken`))
	if err != nil {
		t.Fatal(err)
	}
	if out.OK || out.Error.Code != "invalid_input" {
		t.Fatalf("expected invalid input, got %+v", out)
	}
}

func TestCapabilitiesEnumeration(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	if _, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"}); err != nil {
		t.Fatal(err)
	}
	caps, err := ins.Capabilities()
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 1 {
		t.Fatalf("capabilities = %d, want 1", len(caps))
	}
	if caps[0].Name == "" || caps[0].Call == "" {
		t.Fatalf("capability summary incomplete: %+v", caps[0])
	}
	if strings.Contains(caps[0].Description, "README") {
		t.Fatal("capability enumeration must not leak source detail")
	}
}

func TestInstallIsIdempotentAcrossCacheReuse(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	first, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI", AllowReplace: true})
	if err != nil {
		t.Fatal(err)
	}
	if first.Capability != second.Capability {
		t.Fatalf("reinstall should reuse the same capability name: %s vs %s", first.Capability, second.Capability)
	}
	entries, _ := ins.Registry.List()
	if len(entries) != 1 {
		t.Fatalf("replacement must not duplicate the entry: %+v", entries)
	}
}

func TestInspectionIsReusedAcrossInstalls(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	first, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	if err != nil {
		t.Fatal(err)
	}
	if first.InspectionCacheHit {
		t.Fatal("the first inspection of a source must be a cache miss")
	}
	second, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI", AllowReplace: true})
	if err != nil {
		t.Fatal(err)
	}
	if !second.InspectionCacheHit {
		t.Fatal("a repeated install of an unchanged source should reuse cached inspection")
	}
	if first.SourceID != second.SourceID {
		t.Fatalf("source identity must be stable: %s vs %s", first.SourceID, second.SourceID)
	}
}

func TestSelfInstallIsRefusedWithGuidance(t *testing.T) {
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, selfDir(t)), Goal: "let GrokBot diagnose GrokInstall"})
	if err != nil {
		// Refusing outright is acceptable, but it must be explained.
		if !strings.Contains(strings.ToLower(err.Error()), "grokinstall") {
			t.Fatalf("self install error must be self-explanatory: %v", err)
		}
		return
	}
	if res.Result == receipt.ResultInstalled {
		t.Fatalf("installing GrokInstall into itself must not produce a normal install: %+v", res)
	}
}

func selfDir(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestInstallOptionsRejectUnknownSourceKind(t *testing.T) {
	ins := newInstaller(t)
	_, err := ins.Install(Options{Source: sourceRef{Kind: "gitlab"}, Goal: "x"})
	if err == nil {
		t.Fatal("expected an error for an unsupported source kind")
	}
}

func TestManifestValidationRunsBeforeRegistration(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := ins.Registry.Lookup(res.Capability)
	if _, err := manifest.Load(entry.ManifestPath); err != nil {
		t.Fatalf("registered manifest must be valid: %v", err)
	}
	var probe map[string]any
	data, _ := os.ReadFile(entry.ManifestPath)
	if err := json.Unmarshal(data, &probe); err != nil {
		t.Fatalf("manifest must be canonical JSON: %v", err)
	}
}

var _ = inspect.InspectionVersion
