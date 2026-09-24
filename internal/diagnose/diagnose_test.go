package diagnose

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"grokinstall/internal/manifest"
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

func register(t *testing.T, e *Engine, name string, m *manifest.Manifest) registry.Entry {
	t.Helper()
	path, err := m.Save(e.Registry.ManifestsDir())
	if err != nil {
		t.Fatal(err)
	}
	entry := registry.Entry{
		Name:         name,
		Strategy:     m.Strategy,
		Support:      string(m.Support),
		State:        registry.StateReady,
		Source:       m.Source,
		ManifestPath: path,
		InstallID:    "gi_test",
	}
	if err := e.Registry.Register(entry); err != nil {
		t.Fatal(err)
	}
	return entry
}

func subprocessManifest(t *testing.T, command string) *manifest.Manifest {
	t.Helper()
	return &manifest.Manifest{
		Schema: manifest.SchemaID, Name: "demo.cli", Version: "1",
		Source: "path:/demo", Strategy: "cli_bridge", Support: manifest.SupportReady,
		Status:    "ready",
		Execution: manifest.Execution{Type: manifest.ExecutionSubprocess, Command: command, Supported: true},
		Grokbot:   manifest.Grokbot{UseWhen: "run demo"},
	}
}

func TestHealthyCapabilityHasNoFindings(t *testing.T) {
	e := newEngine(t)
	bin := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	register(t, e, "demo.cli", subprocessManifest(t, bin))
	rep, err := e.One("demo.cli")
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Healthy {
		t.Fatalf("expected healthy, findings: %+v", rep.Findings)
	}
}

func TestDiagnoseMissingBinary(t *testing.T) {
	e := newEngine(t)
	register(t, e, "demo.cli", subprocessManifest(t, filepath.Join(t.TempDir(), "gone")))
	rep, err := e.One("demo.cli")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Healthy {
		t.Fatal("a missing binary must not be healthy")
	}
	f := rep.Findings[0]
	if f.Severity != SeverityCritical {
		t.Fatalf("severity = %q", f.Severity)
	}
	if !strings.Contains(f.Symptom, "cannot execute") {
		t.Fatalf("symptom = %q", f.Symptom)
	}
	if len(f.Evidence) == 0 {
		t.Fatal("a finding must carry evidence")
	}
	if f.Confidence != "high" {
		t.Fatalf("direct filesystem evidence deserves high confidence, got %q", f.Confidence)
	}
	if f.Verification == "" {
		t.Fatal("every finding must name a verification command")
	}
}

func TestDiagnoseNonExecutableTarget(t *testing.T) {
	e := newEngine(t)
	bin := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	register(t, e, "demo.cli", subprocessManifest(t, bin))
	rep, _ := e.One("demo.cli")
	found := false
	for _, f := range rep.Findings {
		if strings.Contains(f.Symptom, "not executable") {
			found = true
			if !strings.Contains(strings.Join(f.Evidence, " "), "mode") {
				t.Fatalf("evidence should include the mode: %v", f.Evidence)
			}
		}
	}
	if !found {
		t.Fatalf("expected a not-executable finding: %+v", rep.Findings)
	}
}

func TestDiagnoseMissingManifest(t *testing.T) {
	e := newEngine(t)
	entry := register(t, e, "demo.cli", subprocessManifest(t, "/bin/sh"))
	if err := os.Remove(entry.ManifestPath); err != nil {
		t.Fatal(err)
	}
	rep, _ := e.One("demo.cli")
	if rep.Healthy {
		t.Fatal("a missing manifest must not be healthy")
	}
	if !strings.Contains(rep.Findings[0].Symptom, "manifest is missing") {
		t.Fatalf("symptom = %q", rep.Findings[0].Symptom)
	}
}

func TestDiagnoseCorruptManifest(t *testing.T) {
	e := newEngine(t)
	entry := register(t, e, "demo.cli", subprocessManifest(t, "/bin/sh"))
	if err := os.WriteFile(entry.ManifestPath, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, _ := e.One("demo.cli")
	found := false
	for _, f := range rep.Findings {
		if strings.Contains(f.Symptom, "corrupt") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a corrupt-manifest finding: %+v", rep.Findings)
	}
}

func TestDiagnoseDirtyState(t *testing.T) {
	e := newEngine(t)
	bin := filepath.Join(t.TempDir(), "tool")
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	register(t, e, "demo.cli", subprocessManifest(t, bin))
	if err := e.Registry.SetState("demo.cli", registry.StateDirty, "rollback incomplete"); err != nil {
		t.Fatal(err)
	}
	rep, _ := e.One("demo.cli")
	if rep.Findings[0].Severity != SeverityCritical {
		t.Fatalf("dirty must be critical, got %q", rep.Findings[0].Severity)
	}
	if !strings.Contains(rep.Findings[0].RootCause, "rollback") {
		t.Fatalf("root cause = %q", rep.Findings[0].RootCause)
	}
}

func TestDiagnoseCorruptReceipt(t *testing.T) {
	e := newEngine(t)
	bin := filepath.Join(t.TempDir(), "tool")
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	entry := register(t, e, "demo.cli", subprocessManifest(t, bin))
	receiptPath := filepath.Join(e.Registry.ReceiptsDir(), "gi_test.json")
	os.WriteFile(receiptPath, []byte("{broken"), 0o644)
	entry.ReceiptPath = receiptPath
	if err := e.Registry.Update(entry); err != nil {
		t.Fatal(err)
	}
	rep, _ := e.One("demo.cli")
	found := false
	for _, f := range rep.Findings {
		if strings.Contains(f.Symptom, "receipt is unreadable") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a receipt finding: %+v", rep.Findings)
	}
}

func TestDiagnoseMissingRuntime(t *testing.T) {
	e := newEngine(t)
	bin := filepath.Join(t.TempDir(), "tool")
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	entry := register(t, e, "demo.cli", subprocessManifest(t, bin))
	entry.RuntimeDir = filepath.Join(t.TempDir(), "absent-runtime")
	if err := e.Registry.Update(entry); err != nil {
		t.Fatal(err)
	}
	rep, _ := e.One("demo.cli")
	found := false
	for _, f := range rep.Findings {
		if strings.Contains(f.Symptom, "runtime is missing") {
			found = true
			if f.Severity != SeverityCritical {
				t.Fatalf("a missing runtime is critical, got %q", f.Severity)
			}
		}
	}
	if !found {
		t.Fatalf("expected a runtime finding: %+v", rep.Findings)
	}
}

func TestDiagnoseUnknownCapability(t *testing.T) {
	e := newEngine(t)
	if _, err := e.One("nope"); err == nil {
		t.Fatal("expected not found")
	}
}

func TestDiagnoseAll(t *testing.T) {
	e := newEngine(t)
	bin := filepath.Join(t.TempDir(), "tool")
	os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755)
	register(t, e, "a.good", subprocessManifest(t, bin))
	m := subprocessManifest(t, "/bin/definitely-not-here")
	m.Name = "b.bad"
	register(t, e, "b.bad", m)
	reports, err := e.All()
	if err != nil {
		t.Fatal(err)
	}
	if len(reports) != 2 {
		t.Fatalf("reports = %d", len(reports))
	}
	var healthy, broken int
	for _, r := range reports {
		if r.Healthy {
			healthy++
		} else {
			broken++
		}
	}
	if healthy != 1 || broken != 1 {
		t.Fatalf("healthy=%d broken=%d", healthy, broken)
	}
}

func TestRenderIsHumanReadable(t *testing.T) {
	e := newEngine(t)
	register(t, e, "demo.cli", subprocessManifest(t, filepath.Join(t.TempDir(), "gone")))
	rep, _ := e.One("demo.cli")
	var buf bytes.Buffer
	rep.Render(&buf)
	out := buf.String()
	for _, want := range []string{"SYMPTOM", "EVIDENCE", "ROOT CAUSE", "CONFIDENCE", "AFFECTED COMPONENT", "FIX", "VERIFY"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}

func TestReportJSONRoundTrip(t *testing.T) {
	e := newEngine(t)
	register(t, e, "demo.cli", subprocessManifest(t, "/bin/sh"))
	rep, _ := e.One("demo.cli")
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Findings) != len(rep.Findings) {
		t.Fatal("round trip lost findings")
	}
}
