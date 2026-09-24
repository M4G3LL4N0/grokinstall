package registry

import (
	"os"
	"path/filepath"
	"testing"
)

func newStore(t *testing.T) *Registry {
	t.Helper()
	r, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNewCreatesStateLayout(t *testing.T) {
	r := newStore(t)
	for _, d := range []string{"manifests", "adapters", "receipts", "cache", "diagnostics", "logs"} {
		if _, err := os.Stat(filepath.Join(r.Root, d)); err != nil {
			t.Fatalf("missing %s: %v", d, err)
		}
	}
}

func TestConfigRoundTrip(t *testing.T) {
	r := newStore(t)
	cfg, err := r.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxFileBytes = 1234
	if err := r.SaveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	back, err := r.LoadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if back.MaxFileBytes != 1234 {
		t.Fatalf("max file bytes = %d, want 1234", back.MaxFileBytes)
	}
}

func TestRegistryStartsEmpty(t *testing.T) {
	r := newStore(t)
	entries, err := r.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no installed capabilities in Part 1, got %d", len(entries))
	}
}

func TestRegistryIsValidJSONFile(t *testing.T) {
	r := newStore(t)
	data, err := os.ReadFile(r.RegistryPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("registry.json must exist")
	}
}

func TestDoctorDetectsCorruptConfig(t *testing.T) {
	r := newStore(t)
	if err := os.WriteFile(r.ConfigPath(), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := r.LoadConfig(); err == nil {
		t.Fatal("expected an error for corrupt config")
	}
}
