// Package registry owns the GrokInstall state directory: configuration, the
// capability registry and the locations GrokInstall writes to. State is plain
// JSON written atomically.
package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/M4G3LL4N0/grokinstall/internal/cache"
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

// Writable reports whether the state directory can be written to.
func (r *Registry) Writable() error {
	probe := filepath.Join(r.Root, ".write-probe")
	if err := os.WriteFile(probe, []byte("ok"), 0o644); err != nil {
		return err
	}
	return os.Remove(probe)
}
