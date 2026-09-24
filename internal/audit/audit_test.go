package audit

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"grokinstall/internal/manifest"
	"grokinstall/internal/provision"
	"grokinstall/internal/receipt"
	"grokinstall/internal/registry"
)

func newEngine(t *testing.T) *Engine {
	t.Helper()
	reg, err := registry.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return New(reg)
}

type fixture struct {
	engine  *Engine
	entry   registry.Entry
	bin     string
	runtime string
}

func install(t *testing.T, opts ...func(*manifest.Manifest, *registry.Entry)) *fixture {
	t.Helper()
	e := newEngine(t)
	binDir := t.TempDir()
	bin := filepath.Join(binDir, "tool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho tool\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{
		Schema: manifest.SchemaID, Name: "demo.cli", Version: "1",
		Source: "path:/demo", Strategy: "cli_bridge", Support: manifest.SupportReady,
		Status:    "ready",
		Execution: manifest.Execution{Type: manifest.ExecutionSubprocess, Command: bin, Supported: true},
		Grokbot:   manifest.Grokbot{UseWhen: "run demo"},
		Cache:     manifest.Cache{Enabled: false},
	}
	entry := registry.Entry{
		Name: "demo.cli", Strategy: "cli_bridge", Support: string(m.Support),
		State: registry.StateReady, Source: m.Source, InstallID: "gi_test",
	}
	for _, opt := range opts {
		opt(m, &entry)
	}
	path, err := m.Save(e.Registry.ManifestsDir())
	if err != nil {
		t.Fatal(err)
	}
	entry.ManifestPath = path

	// A matching receipt is part of a clean install.
	r := &receipt.Receipt{
		Schema: receipt.Schema, InstallID: entry.InstallID, Source: m.Source,
		Strategy: m.Strategy, Capability: entry.Name, Result: receipt.ResultInstalled,
		Verification: receipt.Verification{Performed: true, Passed: true},
	}
	rpath, err := r.Save(e.Registry.ReceiptsDir())
	if err != nil {
		t.Fatal(err)
	}
	entry.ReceiptPath = rpath

	if err := e.Registry.Register(entry); err != nil {
		t.Fatal(err)
	}
	return &fixture{engine: e, entry: entry, bin: bin}
}

func TestAuditCleanInstallPasses(t *testing.T) {
	f := install(t)
	rep, err := f.engine.One("demo.cli")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Passed {
		var fails []string
		for _, finding := range rep.Findings {
			if !finding.Pass {
				fails = append(fails, finding.Check+": "+finding.Detail)
			}
		}
		t.Fatalf("clean install should pass, failures: %v", fails)
	}
}

func TestAuditDetectsModifiedBinary(t *testing.T) {
	f := install(t)
	// Attach an owned runtime with a recorded hash, then modify the file.
	runtimeDir := filepath.Join(f.engine.Registry.RuntimesDir(), "demo.cli")
	if err := os.MkdirAll(filepath.Join(runtimeDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := hashFile(f.bin)
	if err != nil {
		t.Fatal(err)
	}
	meta := provision.Metadata{
		Schema: provision.MetadataSchema, Capability: "demo.cli", Method: provision.MethodRelease,
		Executable: "bin/tool", Checksums: map[string]string{"bin/tool": sum},
		ChecksumStatus: "verified", Ownership: provision.OwnershipGrokinstall,
	}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(runtimeDir, "metadata.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "bin", "tool"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.entry.RuntimeDir = runtimeDir
	if err := f.engine.Registry.Update(f.entry); err != nil {
		t.Fatal(err)
	}
	rep, _ := f.engine.One("demo.cli")
	if rep.Passed {
		t.Fatal("a modified runtime file must fail the audit")
	}
	found := false
	for _, finding := range rep.Findings {
		if finding.Check == "runtime integrity" && !finding.Pass {
			found = true
			if finding.Severity != SeverityCritical {
				t.Fatalf("a tampered runtime is critical, got %q", finding.Severity)
			}
		}
	}
	if !found {
		t.Fatalf("expected a runtime integrity failure: %+v", rep.Findings)
	}
}

func TestAuditDetectsOwnershipMismatch(t *testing.T) {
	f := install(t)
	// A runtime outside the GrokInstall state directory is an ownership problem.
	outside := t.TempDir()
	f.entry.RuntimeDir = outside
	if err := f.engine.Registry.Update(f.entry); err != nil {
		t.Fatal(err)
	}
	rep, _ := f.engine.One("demo.cli")
	found := false
	for _, finding := range rep.Findings {
		if finding.Check == "runtime ownership" && !finding.Pass && finding.Severity == SeverityCritical {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a critical ownership failure: %+v", rep.Findings)
	}
}

func TestAuditReportsUnverifiedArtifactHonestly(t *testing.T) {
	f := install(t)
	runtimeDir := filepath.Join(f.engine.Registry.RuntimesDir(), "demo.cli")
	os.MkdirAll(runtimeDir, 0o755)
	sum, _ := hashFile(f.bin)
	meta := provision.Metadata{
		Schema: provision.MetadataSchema, Capability: "demo.cli", Method: provision.MethodRelease,
		Executable: "tool", Checksums: map[string]string{"tool": sum},
		ChecksumStatus: "unavailable-upstream", Ownership: provision.OwnershipGrokinstall,
	}
	data, _ := json.Marshal(meta)
	os.WriteFile(filepath.Join(runtimeDir, "metadata.json"), data, 0o644)
	f.entry.RuntimeDir = runtimeDir
	f.engine.Registry.Update(f.entry)

	rep, _ := f.engine.One("demo.cli")
	found := false
	for _, finding := range rep.Findings {
		if finding.Check == "artifact checksum" {
			found = true
			if finding.Severity != SeverityLow {
				t.Fatalf("a missing upstream checksum is low severity, got %q", finding.Severity)
			}
		}
	}
	if !found {
		t.Fatalf("audit must report checksum provenance: %+v", rep.Findings)
	}
}

func TestAuditDetectsMissingTarget(t *testing.T) {
	f := install(t)
	if err := os.Remove(f.bin); err != nil {
		t.Fatal(err)
	}
	rep, _ := f.engine.One("demo.cli")
	if rep.Passed {
		t.Fatal("a missing target must fail the audit")
	}
}

func TestAuditDetectsSupportMismatch(t *testing.T) {
	e := newEngine(t)
	bin := filepath.Join(t.TempDir(), "tool")
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	m := &manifest.Manifest{
		Schema: manifest.SchemaID, Name: "x.cli", Version: "1", Source: "path:/x",
		Strategy: "api_bridge", Support: manifest.SupportPlanOnly, Status: "plan-only",
		Execution: manifest.Execution{Type: manifest.ExecutionNone, Supported: false},
	}
	path, _ := m.Save(e.Registry.ManifestsDir())
	// Register a plan-only capability as installed: the audit must catch it.
	entry := registry.Entry{Name: "x.cli", Strategy: "api_bridge", State: registry.StateReady, Source: "path:/x", ManifestPath: path}
	if err := e.Registry.Register(entry); err != nil {
		t.Fatal(err)
	}
	rep, _ := e.One("x.cli")
	found := false
	for _, finding := range rep.Findings {
		if finding.Check == "plan versus installed" && !finding.Pass && finding.Severity == SeverityCritical {
			found = true
		}
	}
	if !found {
		t.Fatalf("a plan-only capability registered as ready must be flagged: %+v", rep.Findings)
	}
}

func TestAuditSeverityIsNotInflated(t *testing.T) {
	f := install(t)
	rep, _ := f.engine.One("demo.cli")
	for _, finding := range rep.Findings {
		if finding.Pass {
			continue
		}
		if finding.Severity == SeverityCritical {
			t.Fatalf("a clean install should have no critical finding: %+v", finding)
		}
	}
}

func TestAuditRenderIsReadable(t *testing.T) {
	f := install(t)
	rep, _ := f.engine.One("demo.cli")
	var buf bytes.Buffer
	rep.Render(&buf)
	out := buf.String()
	if !strings.Contains(out, "audit passed") {
		t.Fatalf("render:\n%s", out)
	}
	for _, want := range []string{"manifest validity", "receipt consistency", "runtime ownership"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}

func TestAuditDetectsCorruptReceipt(t *testing.T) {
	f := install(t)
	if err := os.WriteFile(f.entry.ReceiptPath, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := f.engine.One("demo.cli")
	found := false
	for _, finding := range rep.Findings {
		if finding.Check == "receipt consistency" && !finding.Pass {
			found = true
		}
	}
	if !found {
		t.Fatalf("a corrupt receipt must fail the audit: %+v", rep.Findings)
	}
}

func TestAuditDetectsDirtyState(t *testing.T) {
	f := install(t)
	if err := f.engine.Registry.SetState("demo.cli", registry.StateDirty, "rollback incomplete"); err != nil {
		t.Fatal(err)
	}
	rep, _ := f.engine.One("demo.cli")
	if rep.Passed {
		t.Fatal("dirty state must fail the audit")
	}
}
