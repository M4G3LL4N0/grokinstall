// Package registry owns the GrokInstall state directory: configuration, the
// capability registry and the locations GrokInstall writes to. State is plain
// JSON written atomically.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"grokinstall/internal/cache"
)

// Schema is the state schema identifier.
const Schema = "grokinstall/state/v1"

// Config is the user configuration.
type Config struct {
	Schema        string            `json:"schema"`
	Version       string            `json:"version"`
	MaxFileBytes  int64             `json:"max_file_bytes,omitempty"`
	MaxTotalBytes int64             `json:"max_total_bytes,omitempty"`
	GitHubToken   string            `json:"github_token,omitempty"`
	Workers       map[string]string `json:"workers,omitempty"`
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{Schema: Schema, Version: "v1", Workers: map[string]string{}}
}

// Entry is one installed capability. Part 1 writes none; the type exists so the
// registry shape is stable for later parts.
type Entry struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Strategy     string   `json:"strategy"`
	Source       string   `json:"source"`
	Goal         string   `json:"goal,omitempty"`
	ManifestPath string   `json:"manifest_path,omitempty"`
	AdapterPath  string   `json:"adapter_path,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	InstalledAt  string   `json:"installed_at,omitempty"`
}

// Registry is the state directory plus its canonical JSON files.
type Registry struct {
	Root  string
	Cache *cache.Store
}

// New prepares the state directory.
func New(root string) (*Registry, error) {
	store, err := cache.New(root)
	if err != nil {
		return nil, err
	}
	return &Registry{Root: store.Root, Cache: store}, nil
}

func (r *Registry) ConfigPath() string     { return filepath.Join(r.Root, "config.json") }
func (r *Registry) RegistryPath() string   { return filepath.Join(r.Root, "registry.json") }
func (r *Registry) ManifestsDir() string   { return filepath.Join(r.Root, "manifests") }
func (r *Registry) AdaptersDir() string    { return filepath.Join(r.Root, "adapters") }
func (r *Registry) ReceiptsDir() string    { return filepath.Join(r.Root, "receipts") }
func (r *Registry) DiagnosticsDir() string { return filepath.Join(r.Root, "diagnostics") }
func (r *Registry) LogsDir() string        { return filepath.Join(r.Root, "logs") }

// LoadConfig reads config.json, returning defaults when it is absent.
func (r *Registry) LoadConfig() (Config, error) {
	data, err := os.ReadFile(r.ConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultConfig(), nil
		}
		return DefaultConfig(), err
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return DefaultConfig(), fmt.Errorf("parse %s: %w", r.ConfigPath(), err)
	}
	if cfg.Schema == "" {
		cfg.Schema = Schema
	}
	return cfg, nil
}

// SaveConfig writes config.json atomically.
func (r *Registry) SaveConfig(cfg Config) error {
	if cfg.Schema == "" {
		cfg.Schema = Schema
	}
	return cache.WriteJSONAtomic(r.ConfigPath(), cfg)
}

// List returns installed capability entries.
func (r *Registry) List() ([]Entry, error) {
	data, err := os.ReadFile(r.RegistryPath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var payload struct {
		Schema  string  `json:"schema"`
		Entries []Entry `json:"entries"`
		Legacy  []Entry `json:"capabilities"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse %s: %w", r.RegistryPath(), err)
	}
	if len(payload.Entries) > 0 {
		return payload.Entries, nil
	}
	return payload.Legacy, nil
}

// WriteRegistry writes the entry list atomically.
func (r *Registry) WriteRegistry(entries []Entry) error {
	if entries == nil {
		entries = []Entry{}
	}
	return cache.WriteJSONAtomic(r.RegistryPath(), map[string]any{
		"schema":  Schema,
		"entries": entries,
	})
}

// Writable reports whether the state directory can be written to.
func (r *Registry) Writable() error {
	probe := filepath.Join(r.Root, ".write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return err
	}
	return os.Remove(probe)
}
