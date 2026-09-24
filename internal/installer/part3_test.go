package installer

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"grokinstall/internal/manifest"
	"grokinstall/internal/provision"
	"grokinstall/internal/provision/github"
	"grokinstall/internal/receipt"
	"grokinstall/internal/registry"
)

// --- contract gap: every generated contract must point at a working command --

func TestGeneratedContractPointsAtCommandsThatExist(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	if err != nil {
		t.Fatal(err)
	}
	entry, err := ins.Registry.Lookup(res.Capability)
	if err != nil {
		t.Fatal(err)
	}
	m, err := manifest.Load(entry.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	contract := m.ContractText()
	if !strings.Contains(contract, "grokinstall diagnose "+m.Name) {
		t.Fatalf("the contract must point at diagnose:\n%s", contract)
	}
	// Every grokinstall subcommand named in a contract must be one this binary
	// actually implements.
	for _, cmd := range extractCommands(contract) {
		if !knownCommands[cmd] {
			t.Fatalf("contract references unknown command %q:\n%s", cmd, contract)
		}
	}
}

var knownCommands = map[string]bool{
	"install": true, "run": true, "test": true, "list": true, "info": true,
	"capabilities": true, "grokbot": true, "uninstall": true, "inspect": true,
	"plan": true, "compare": true, "doctor": true, "usage": true,
	"diagnose": true, "audit": true,
}

func extractCommands(contract string) []string {
	var out []string
	for _, field := range strings.Fields(contract) {
		field = strings.Trim(field, "',\"")
		if strings.HasPrefix(field, "grokinstall ") {
			out = append(out, strings.TrimPrefix(field, "grokinstall "))
		}
	}
	return out
}

// --- installed versus planned ---------------------------------------------

func TestPlanIsNeverRegisteredAsInstalled(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "openapi.json", `{"openapi":"3.0.0","info":{"title":"x","version":"1"},"paths":{"/a":{"get":{}}}}`)
	write(t, dir, "README.md", "# api\n\nHTTP API.\n")
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "call this API"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Result != receipt.ResultPlanned {
		t.Fatalf("result = %q, want planned", res.Result)
	}
	entries, _ := ins.Registry.List()
	for _, e := range entries {
		if e.State == registry.StatePlanOnly {
			t.Fatal("a plan must never appear in the capability registry")
		}
	}
	if res.PlanPath == "" {
		t.Fatal("the plan must be persisted outside the registry")
	}
}

func TestCapabilitiesDefaultToRunnableOnly(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	if _, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"}); err != nil {
		t.Fatal(err)
	}
	caps, err := ins.Capabilities()
	if err != nil {
		t.Fatal(err)
	}
	if len(caps) != 1 || !caps[0].Runnable {
		t.Fatalf("a verified install must be runnable: %+v", caps)
	}
	if caps[0].State != string(registry.StateReady) {
		t.Fatalf("state = %q", caps[0].State)
	}
}

func TestBrokenCapabilityIsNotOfferedAsRunnable(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ins.Registry.SetState(res.Capability, registry.StateBroken, "target vanished"); err != nil {
		t.Fatal(err)
	}
	caps, _ := ins.Capabilities()
	if len(caps) != 1 {
		t.Fatal("a broken capability is still installed and should be listed")
	}
	if caps[0].Runnable {
		t.Fatal("a broken capability must never be offered as runnable")
	}
}

// --- provisioning -----------------------------------------------------------

func TestRefusalLeavesNoPersistentState(t *testing.T) {
	// A source that declares a CLI it cannot provide, with provisioning off.
	dir := t.TempDir()
	write(t, dir, "package.json", `{"name":"ghost","version":"1.0.0","bin":{"ghost":"bin/ghost.sh"}}`)
	ins := newInstaller(t)
	res, err := ins.Install(Options{
		Source: localSource(t, dir), Goal: "run the ghost CLI",
		Policy: provision.Policy{Mode: provision.PolicyNever},
	})
	if err == nil {
		t.Fatal("expected a refusal")
	}
	entries, _ := ins.Registry.List()
	if len(entries) != 0 {
		t.Fatalf("a refusal must register nothing: %+v", entries)
	}
	if res.Refusal == nil {
		t.Fatal("a refusal must be structured so it can be explained")
	}
	if res.Refusal.Reason == "" {
		t.Fatal("a refusal must state a reason")
	}
	// The receipt still records the attempt for audit.
	if res.ReceiptPath == "" {
		t.Fatal("a refused attempt should still leave a receipt")
	}
}

func TestProvisionNeverBlocksInstall(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"name":"ghost","version":"1.0.0","bin":{"ghost":"bin/ghost.sh"}}`)
	ins := newInstaller(t)
	_, err := ins.Install(Options{
		Source: localSource(t, dir), Goal: "run the ghost CLI",
		Policy: provision.Policy{Mode: provision.PolicyNever},
	})
	if err == nil {
		t.Fatal("provision=never must not silently succeed")
	}
}

func TestUnsupportedSourceBuildIsRefused(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "go.mod", "module example.com/tool\n\ngo 1.21\n")
	write(t, dir, "main.go", "package main\n\nfunc main() {}\n")
	write(t, dir, "install.sh", "#!/bin/sh\necho nope\n")
	write(t, dir, "package.json", `{"name":"tool","version":"1.0.0","bin":{"tool":"bin/tool"}}`)
	ins := newInstaller(t)
	// Without --allow-source-build, a repository that ships its own scripts
	// must not be compiled automatically.
	_, err := ins.Install(Options{
		Source: localSource(t, dir), Goal: "run the tool CLI",
		Policy: provision.Policy{Mode: provision.PolicySafe},
	})
	if err == nil {
		t.Fatal("an unsafe source build must be refused under the safe policy")
	}
	entries, _ := ins.Registry.List()
	if len(entries) != 0 {
		t.Fatalf("a refusal must register nothing: %+v", entries)
	}
}

// --- transaction integrity --------------------------------------------------

func TestFailedVerificationLeavesNoStagingResidue(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "package.json", `{"name":"ghost","version":"1.0.0","bin":{"ghost":"bin/ghost.sh"}}`)
	ins := newInstaller(t)
	_, _ = ins.Install(Options{Source: localSource(t, dir), Goal: "run ghost"})
	entries, err := os.ReadDir(ins.Registry.StagingDir())
	if err == nil {
		for _, e := range entries {
			t.Fatalf("staging residue left behind: %s", e.Name())
		}
	}
}

func TestDirtyStateIsRecordedNotHidden(t *testing.T) {
	reg, err := registry.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(registry.Entry{Name: "d.one", State: registry.StateReady, Source: "path:/d"}); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetState("d.one", registry.StateDirty, "rollback incomplete"); err != nil {
		t.Fatal(err)
	}
	got, _ := reg.Lookup("d.one")
	if got.State != registry.StateDirty {
		t.Fatalf("state = %q", got.State)
	}
	if !strings.Contains(got.DirtyReason, "rollback") {
		t.Fatal("the dirty reason must be retained")
	}
	if got.State.Runnable() {
		t.Fatal("dirty state must never be runnable")
	}
}

func TestDirtyReceiptRequiresState(t *testing.T) {
	r := sampleFailedDirty()
	r.State = ""
	if err := r.Validate(); err == nil {
		t.Fatal("a dirty receipt must record the state that could not be rolled back")
	}
}

func sampleFailedDirty() *receipt.Receipt {
	return &receipt.Receipt{
		Schema: receipt.Schema, InstallID: "gi_x", Source: "path:/x",
		Strategy: "cli_bridge", Result: receipt.ResultDirty,
	}
}

// --- receipts record provisioning -----------------------------------------

func TestReceiptRecordsProvisioningProvenance(t *testing.T) {
	dir := cliFixture(t)
	ins := newInstaller(t)
	res, err := ins.Install(Options{Source: localSource(t, dir), Goal: "run the widget CLI"})
	if err != nil {
		t.Fatal(err)
	}
	r, err := receipt.Load(res.ReceiptPath)
	if err != nil {
		t.Fatal(err)
	}
	if r.Provisioning == nil {
		t.Fatal("a receipt should describe how the executable was obtained")
	}
	if r.Provisioning.Method == "" {
		t.Fatal("provisioning method must be recorded")
	}
	// Every created file must be attributed to GrokInstall.
	for _, f := range r.FilesCreated {
		if f.Owner != "grokinstall" {
			t.Fatalf("ownership must be recorded: %+v", f)
		}
	}
}

func TestGitHubClientIsInjectableForTests(t *testing.T) {
	// The provisioning path must be testable without the network.
	ins := newInstaller(t)
	client := github.NewClient("")
	ins.WithGitHubClient(client)
	if ins.GitHub == nil {
		t.Fatal("client injection failed")
	}
	_ = context.Background()
}

func TestNoInstallStillSucceedsWithPolicyNever(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "README.md", "# notes\n\nOnly prose.\n")
	ins := newInstaller(t)
	res, err := ins.Install(Options{
		Source: localSource(t, dir), Goal: "summarize the notes",
		Policy: provision.Policy{Mode: provision.PolicyNever},
	})
	if err != nil {
		t.Fatalf("no_install is independent of provisioning policy: %v", err)
	}
	if res.Result != receipt.ResultNoInstall {
		t.Fatalf("result = %q", res.Result)
	}
}

func TestSelfInstallStillRefused(t *testing.T) {
	ins := newInstaller(t)
	_, err := ins.Install(Options{Source: localSource(t, selfDir(t)), Goal: "diagnose GrokInstall"})
	if err == nil {
		t.Fatal("self install must be refused")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "grokinstall") {
		t.Fatalf("the refusal should be self-explanatory: %v", err)
	}
	entries, _ := ins.Registry.List()
	if len(entries) != 0 {
		t.Fatalf("self install must register nothing: %+v", entries)
	}
}

func TestInstalledManifestNeverLeavesStagingPath(t *testing.T) {
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
	if m.Execution.Command == "" {
		t.Fatal("a runnable capability must have a command")
	}
	if strings.Contains(m.Execution.Command, string(filepath.Separator)+"staging"+string(filepath.Separator)) {
		t.Fatalf("the manifest points at a staging path that will be deleted: %s", m.Execution.Command)
	}
	if _, err := os.Stat(m.Execution.Command); err != nil {
		t.Fatalf("the manifest command does not exist: %v", err)
	}
}

var _ = json.Marshal
