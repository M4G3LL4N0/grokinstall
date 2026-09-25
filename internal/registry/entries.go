package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/M4G3LL4N0/grokinstall/internal/cache"
	"github.com/M4G3LL4N0/grokinstall/internal/strategy"
)

// Entry is one installed capability. It points at a manifest rather than
// duplicating it, so the manifest stays the single source of truth and the
// registry stays small enough to read.
type Entry struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Strategy     string   `json:"strategy"`
	Support      string   `json:"support,omitempty"`
	State        State    `json:"state"`
	Status       string   `json:"status,omitempty"`
	Source       string   `json:"source"`
	Goal         string   `json:"goal,omitempty"`
	ManifestPath string   `json:"manifest_path,omitempty"`
	AdapterPath  string   `json:"adapter_path,omitempty"`
	RuntimeDir   string   `json:"runtime_dir,omitempty"`
	ReceiptPath  string   `json:"receipt_path,omitempty"`
	InstallID    string   `json:"install_id,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	InstalledAt  string   `json:"installed_at,omitempty"`
	UpdatedAt    string   `json:"updated_at,omitempty"`
	// DirtyReason explains a dirty or broken state in one short phrase.
	DirtyReason string `json:"dirty_reason,omitempty"`
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

// PlanEntry is a persisted plan. Plans are stored outside the capability
// registry precisely so a plan can never be mistaken for an installed
// capability.
type PlanEntry struct {
	Schema    string          `json:"schema"`
	PlanID    string          `json:"plan_id"`
	CreatedAt string          `json:"created_at"`
	Source    string          `json:"source"`
	Goal      string          `json:"goal,omitempty"`
	Strategy  strategy.ID     `json:"strategy"`
	Support   string          `json:"support,omitempty"`
	State     State           `json:"state"`
	Reason    string          `json:"reason,omitempty"`
	Manifest  json.RawMessage `json:"draft_manifest,omitempty"`
}

// Register adds a new entry. A duplicate name is an error: reinstalling must be
// an explicit act, never a silent overwrite.
func (r *Registry) Register(e Entry) error {
	if err := validateName(e.Name); err != nil {
		return err
	}
	if e.State == "" {
		e.State = StateReady
	}
	if !e.State.Installed() {
		return fmt.Errorf("only installed capabilities can be registered; %q is %q", e.Name, e.State)
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
	if e.State != "" && !e.State.Validate() {
		return fmt.Errorf("unknown state %q", e.State)
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

// SetState records a lifecycle transition for a capability.
func (r *Registry) SetState(name string, state State, reason string) error {
	if !state.Validate() {
		return fmt.Errorf("unknown state %q", state)
	}
	entries, err := r.List()
	if err != nil {
		return err
	}
	for i := range entries {
		if entries[i].Name == name {
			entries[i].State = state
			entries[i].DirtyReason = reason
			entries[i].UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			return r.WriteRegistry(entries)
		}
	}
	return fmt.Errorf("%w: %s", ErrNotFound, name)
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

// List returns every installed entry, sorted by name for stable output.
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

// PlansDir is where persisted plans live, kept out of the capability registry.
func (r *Registry) PlansDir() string { return filepath.Join(r.Root, "plans") }

// RuntimesDir holds GrokInstall-owned provisioned runtimes.
func (r *Registry) RuntimesDir() string { return filepath.Join(r.Root, "runtimes") }

// RuntimePath returns the owned runtime directory for a capability.
func (r *Registry) RuntimePath(name string) string {
	return filepath.Join(r.RuntimesDir(), name)
}

// SavePlan persists a plan outside the capability registry.
func (r *Registry) SavePlan(p PlanEntry) (string, error) {
	if p.Schema == "" {
		p.Schema = "grokinstall/plan-record/v1"
	}
	if p.State == "" {
		p.State = StatePlanOnly
	}
	if p.CreatedAt == "" {
		p.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if err := os.MkdirAll(r.PlansDir(), 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(r.PlansDir(), p.PlanID+".json")
	return path, cache.WriteJSONAtomic(path, p)
}

// Plans returns every persisted plan.
func (r *Registry) Plans() ([]PlanEntry, error) {
	entries, err := os.ReadDir(r.PlansDir())
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []PlanEntry
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, n := range names {
		data, err := os.ReadFile(filepath.Join(r.PlansDir(), n))
		if err != nil {
			continue
		}
		var p PlanEntry
		if err := json.Unmarshal(data, &p); err != nil {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// MigrationReport records what a registry migration changed.
type MigrationReport struct {
	FromVersion string   `json:"from_version"`
	Entries     int      `json:"entries_before"`
	Migrated    int      `json:"migrated"`
	PlansMoved  []string `json:"plans_moved,omitempty"`
	Details     []string `json:"details,omitempty"`
}

// Migrate brings a Part 2 registry up to the current model. Part 2 allowed
// plan-only capabilities to be registered; those are moved into the plans
// directory so the registry only ever contains installed capabilities.
func (r *Registry) Migrate() (*MigrationReport, error) {
	report := &MigrationReport{FromVersion: "part2"}
	entries, err := r.List()
	if err != nil {
		return nil, err
	}
	report.Entries = len(entries)
	if len(entries) == 0 {
		return report, nil
	}
	var kept []Entry
	normalized := 0
	for _, e := range entries {
		if e.State == "" {
			// Infer the state for entries written before states existed.
			e.State = StateReady
			normalized++
		}
		if e.State == StatePlanOnly {
			report.Migrated++
			report.PlansMoved = append(report.PlansMoved, e.Name)
			report.Details = append(report.Details,
				"moved plan-only capability "+e.Name+" out of the capability registry")
			continue
		}
		kept = append(kept, e)
	}
	if report.Migrated == 0 && normalized == 0 {
		return report, nil
	}
	if err := r.WriteRegistry(kept); err != nil {
		return nil, err
	}
	if normalized > 0 && report.Migrated == 0 {
		report.Details = append(report.Details,
			"assigned ready state to "+itoa(normalized)+" entries written before lifecycle states existed")
	}
	return report, nil
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}
