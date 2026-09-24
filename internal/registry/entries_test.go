package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func newReg(t *testing.T) *Registry {
	t.Helper()
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func entry(name string) Entry {
	return Entry{
		Name:         name,
		Version:      "1",
		Strategy:     "cli_bridge",
		Support:      "supported",
		Status:       "ready",
		Source:       "path:/tmp/" + name,
		ManifestPath: "/tmp/manifests/" + name + ".json",
		InstallID:    "gi_test",
	}
}

func TestRegisterAddsEntry(t *testing.T) {
	r := newReg(t)
	if err := r.Register(entry("widget.cli")); err != nil {
		t.Fatal(err)
	}
	got, err := r.Lookup("widget.cli")
	if err != nil {
		t.Fatal(err)
	}
	if got.Strategy != "cli_bridge" {
		t.Fatalf("entry = %+v", got)
	}
}

func TestRegisterRejectsDuplicateName(t *testing.T) {
	r := newReg(t)
	if err := r.Register(entry("widget.cli")); err != nil {
		t.Fatal(err)
	}
	err := r.Register(entry("widget.cli"))
	if err == nil {
		t.Fatal("duplicate registration must be rejected")
	}
	if _, lerr := r.Lookup("widget.cli"); lerr != nil {
		t.Fatalf("the original entry must survive a rejected duplicate: %v", lerr)
	}
}

func TestRegisterRejectsInvalidName(t *testing.T) {
	r := newReg(t)
	if err := r.Register(entry("Bad Name")); err == nil {
		t.Fatal("invalid names must be rejected")
	}
}

func TestUpdateReplacesEntry(t *testing.T) {
	r := newReg(t)
	_ = r.Register(entry("a.one"))
	updated := entry("a.one")
	updated.Status = "degraded"
	updated.Version = "2"
	if err := r.Update(updated); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Lookup("a.one")
	if got.Status != "degraded" || got.Version != "2" {
		t.Fatalf("update did not apply: %+v", got)
	}
}

func TestUpdateUnknownEntryFails(t *testing.T) {
	r := newReg(t)
	if err := r.Update(entry("missing")); err == nil {
		t.Fatal("updating an unknown entry must fail")
	}
}

func TestRemoveDeletesEntry(t *testing.T) {
	r := newReg(t)
	_ = r.Register(entry("a.one"))
	_ = r.Register(entry("a.two"))
	if err := r.Remove("a.one"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Lookup("a.one"); err == nil {
		t.Fatal("entry should be gone")
	}
	if _, err := r.Lookup("a.two"); err != nil {
		t.Fatal("other entries must survive")
	}
}

func TestRemoveUnknownFailsSafely(t *testing.T) {
	r := newReg(t)
	if err := r.Remove("nope"); err == nil {
		t.Fatal("removing an unknown entry must report an error")
	}
}

func TestListIsSorted(t *testing.T) {
	r := newReg(t)
	_ = r.Register(entry("z.last"))
	_ = r.Register(entry("a.first"))
	got, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "a.first" {
		t.Fatalf("list not sorted: %+v", got)
	}
}

func TestLookupUnknownReportsNotFound(t *testing.T) {
	r := newReg(t)
	_, err := r.Lookup("nope")
	if err == nil {
		t.Fatal("expected not found")
	}
}

func TestCorruptedRegistryIsNotSilentlyDestroyed(t *testing.T) {
	r := newReg(t)
	_ = r.Register(entry("a.one"))
	if err := os.WriteFile(r.RegistryPath(), []byte("{corrupt"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.List(); err == nil {
		t.Fatal("a corrupted registry must report an error")
	}
	data, err := os.ReadFile(r.RegistryPath())
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{corrupt" {
		t.Fatal("a failed read must not rewrite or destroy registry state")
	}
	if err := r.Register(entry("a.two")); err == nil {
		t.Fatal("writes must be refused while the registry is corrupt")
	}
}

func TestStaleManifestIsReportedNotHidden(t *testing.T) {
	r := newReg(t)
	e := entry("a.one")
	e.ManifestPath = filepath.Join(t.TempDir(), "gone.json")
	_ = r.Register(e)
	got, _ := r.Lookup("a.one")
	if got.ManifestHealthy() {
		t.Fatal("a missing manifest must be reported as unhealthy")
	}
	if got.ManifestMissing() {
		return
	}
	t.Fatal("a missing manifest should be reported as missing")
}

func TestHealthReportsHealthyManifest(t *testing.T) {
	r := newReg(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "a.one.json")
	if err := os.WriteFile(path, []byte(`{"schema":"grokinstall/v1"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	e := entry("a.one")
	e.ManifestPath = path
	_ = r.Register(e)
	got, _ := r.Lookup("a.one")
	if !got.ManifestHealthy() {
		t.Fatal("an existing manifest must report healthy")
	}
}

func TestEntryDoesNotDuplicateManifestContent(t *testing.T) {
	r := newReg(t)
	e := entry("a.one")
	_ = r.Register(e)
	data, err := os.ReadFile(r.RegistryPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > 2000 {
		t.Fatalf("registry entry should point at a manifest, not duplicate it: %d bytes", len(data))
	}
}

func TestReceiptPathIsRecorded(t *testing.T) {
	r := newReg(t)
	e := entry("a.one")
	e.ReceiptPath = "/tmp/receipts/gi_1.json"
	_ = r.Register(e)
	got, _ := r.Lookup("a.one")
	if got.ReceiptPath != e.ReceiptPath {
		t.Fatal("receipt path must be recorded for uninstall and audit")
	}
}

func TestRegisterReplacesOnlyAfterExplicitRemove(t *testing.T) {
	r := newReg(t)
	_ = r.Register(entry("a.one"))
	if err := r.Register(entry("a.one")); err == nil {
		t.Fatal("expected a duplicate error")
	}
	if err := r.Remove("a.one"); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(entry("a.one")); err != nil {
		t.Fatalf("reinstall after removal should work: %v", err)
	}
}
