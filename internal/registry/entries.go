package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"grokinstall/internal/cache"
)

// Entry is one installed capability. It points at a manifest rather than
// duplicating it, so the manifest stays the single source of truth and the
// registry stays small enough to read.
type Entry struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Strategy     string   `json:"strategy"`
	Support      string   `json:"support,omitempty"`
	Status       string   `json:"status,omitempty"`
	Source       string   `json:"source"`
	Goal         string   `json:"goal,omitempty"`
	ManifestPath string   `json:"manifest_path,omitempty"`
	AdapterPath  string   `json:"adapter_path,omitempty"`
	ReceiptPath  string   `json:"receipt_path,omitempty"`
	InstallID    string   `json:"install_id,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	InstalledAt  string   `json:"installed_at,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
}

// ManifestMissing reports whether the manifest file is gone.
func (e Entry) ManifestMissing() bool {
	if e.ManifestPath == "" {
		return true
	}
	_, err := os.Stat(e.ManifestPath)
	return err != nil
}

// ManifestHealthy reports whether the manifest file is present and readable.
func (e Entry) ManifestHealthy() bool {
	if e.ManifestPath == "" {
		return false
	}
	f, err := os.Open(e.ManifestPath)
	if err != nil {
		return false
	}
	defer f.Close()
	return true
}

// ErrDuplicate reports a name collision rather than overwriting an install.
var ErrDuplicate = fmt.Errorf("capability name already exists")

// ErrNotFound reports a missing entry.
var ErrNotFound = fmt.Errorf("capability not found")

// Register adds a new entry. A duplicate name is an error: reinstalling must be
// an explicit act, never a silent overwrite.
func (r *Registry) Register(e Entry) error {
	if err := validateName(e.Name); err != nil {
		return err
	}
	entries, err := r.List()
	if err != nil {
		return err
	}
	for _, existing := range entries {
		if existing.Name == e.Name {
			return fmt.Errorf("%w: %s", ErrDuplicate, e.Name)
		}
	}
	entries = append(entries, e)
	return r.WriteRegistry(entries)
}

// Update replaces an existing entry.
func (r *Registry) Update(e Entry) error {
	if err := validateName(e.Name); err != nil {
		return err
	}
	entries, err := r.List()
	if err != nil {
		return err
	}
	found := false
	for i := range entries {
		if entries[i].Name == e.Name {
			entries[i] = e
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrNotFound, e.Name)
	}
	return r.WriteRegistry(entries)
}

// Remove deletes an entry, leaving its files untouched. Removing files is the
// uninstaller's job, and only for resources it owns.
func (r *Registry) Remove(name string) error {
	entries, err := r.List()
	if err != nil {
		return err
	}
	kept := make([]Entry, 0, len(entries))
	found := false
	for _, e := range entries {
		if e.Name == name {
			found = true
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		return fmt.Errorf("%w: %s", ErrNotFound, name)
	}
	return r.WriteRegistry(kept)
}

// Lookup returns one entry.
func (r *Registry) Lookup(name string) (Entry, error) {
	entries, err := r.List()
	if err != nil {
		return Entry{}, err
	}
	for _, e := range entries {
		if e.Name == name {
			return e, nil
		}
	}
	return Entry{}, fmt.Errorf("%w: %s", ErrNotFound, name)
}

// Has reports whether a name is registered.
func (r *Registry) Has(name string) bool {
	_, err := r.Lookup(name)
	return err == nil
}

// List returns every entry, sorted by name for stable output.
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
	entries := payload.Entries
	if len(entries) == 0 {
		entries = payload.Legacy
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return entries, nil
}

// WriteRegistry writes the entry list atomically.
func (r *Registry) WriteRegistry(entries []Entry) error {
	if entries == nil {
		entries = []Entry{}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
	return cache.WriteJSONAtomic(r.RegistryPath(), map[string]any{
		"schema":  Schema,
		"entries": entries,
	})
}

func validateName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("capability name is required")
	}
	if name != strings.ToLower(name) {
		return fmt.Errorf("capability name %q must be lowercase", name)
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
		default:
			return fmt.Errorf("capability name %q contains an invalid character %q", name, r)
		}
	}
	return nil
}

// StagingDir is where an install assembles files before they are committed.
func (r *Registry) StagingDir() string { return filepath.Join(r.Root, "staging") }

// StagingPath returns the staging directory for one install.
func (r *Registry) StagingPath(installID string) string {
	return filepath.Join(r.StagingDir(), installID)
}
