// Package manifest defines the canonical capability contract. The manifest is
// what GrokBot understands: a tiny description of how to use a capability, not
// an implementation.
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"grokinstall/internal/cache"
	"grokinstall/internal/evidence"
)

// SchemaID identifies the manifest schema.
const SchemaID = "grokinstall/v1"

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Manifest is the capability contract.
type Manifest struct {
	Schema       string     `json:"schema"`
	Name         string     `json:"name"`
	Version      string     `json:"version"`
	Source       string     `json:"source"`
	Goal         string     `json:"goal"`
	Capabilities []string   `json:"capabilities"`
	Execution    Execution  `json:"execution"`
	Inputs       []Param    `json:"inputs"`
	Outputs      []Param    `json:"outputs"`
	Grokbot      Grokbot    `json:"grokbot"`
	Cache        Cache      `json:"cache"`
	Security     Security   `json:"security"`
	Provenance   Provenance `json:"provenance"`
}

// Execution describes how the capability runs.
type Execution struct {
	Strategy       string   `json:"strategy"`
	Command        string   `json:"command,omitempty"`
	Args           []string `json:"args,omitempty"`
	JSONIO         bool     `json:"json_io"`
	WorkingDir     string   `json:"working_dir,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	RequiresBuild  bool     `json:"requires_build,omitempty"`
	Supported      bool     `json:"supported"`
	Note           string   `json:"note,omitempty"`
}

// Param is one input or output field.
type Param struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Grokbot is the tiny handoff contract.
type Grokbot struct {
	UseWhen   string `json:"use_when"`
	DoNot     string `json:"do_not"`
	OnFailure string `json:"on_failure,omitempty"`
}

// Cache describes when results may be reused.
type Cache struct {
	Enabled     bool   `json:"enabled"`
	Key         string `json:"key,omitempty"`
	Description string `json:"description,omitempty"`
}

// Security records what the integration is allowed to do.
type Security struct {
	ExecutesSourceCode bool     `json:"executes_source_code"`
	RequiresApproval   bool     `json:"requires_approval,omitempty"`
	NetworkAccess      bool     `json:"network_access,omitempty"`
	Notes              []string `json:"notes,omitempty"`
}

// Provenance records where the manifest came from.
type Provenance struct {
	Source      string          `json:"source"`
	Identity    string          `json:"identity,omitempty"`
	CommitSHA   string          `json:"commit_sha,omitempty"`
	InspectedAt string          `json:"inspected_at,omitempty"`
	GrokInstall string          `json:"grokinstall_version,omitempty"`
	Evidence    []evidence.Item `json:"evidence,omitempty"`
}

// Validate checks the invariants a manifest must satisfy.
func (m *Manifest) Validate() error {
	if m.Schema != SchemaID {
		return fmt.Errorf("manifest schema must be %q, got %q", SchemaID, m.Schema)
	}
	if m.Name == "" {
		return fmt.Errorf("manifest name is required")
	}
	if !nameRe.MatchString(m.Name) {
		return fmt.Errorf("manifest name %q must be lowercase letters, digits, dot, dash or underscore", m.Name)
	}
	if m.Source == "" {
		return fmt.Errorf("manifest source is required")
	}
	return nil
}

// ContractText renders the smallest sufficient GrokBot handoff.
func (m *Manifest) ContractText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "CAPABILITY\n%s\n\n", m.Name)
	fmt.Fprintf(&b, "USE WHEN\n%s\n\n", orDefault(m.Grokbot.UseWhen, "the described operation is requested"))
	fmt.Fprintf(&b, "CALL\n%s\n\n", m.callLine())
	fmt.Fprintf(&b, "DO NOT\n%s\n", orDefault(m.Grokbot.DoNot, "load the implementation repository into GrokBot"))
	if m.Grokbot.OnFailure != "" {
		fmt.Fprintf(&b, "\nON FAILURE\n%s\n", m.Grokbot.OnFailure)
	}
	return b.String()
}

func (m *Manifest) callLine() string {
	if m.Execution.Command == "" {
		return "(execution is not available in this version)"
	}
	if strings.ContainsAny(m.Execution.Command, " \t") {
		return fmt.Sprintf("%q", m.Execution.Command)
	}
	return m.Execution.Command
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// JSON serializes the manifest.
func (m *Manifest) JSON() ([]byte, error) { return json.MarshalIndent(m, "", "  ") }

// Save writes a manifest atomically into dir.
func (m *Manifest) Save(dir string) (string, error) {
	if err := m.Validate(); err != nil {
		return "", err
	}
	data, err := m.JSON()
	if err != nil {
		return "", err
	}
	path := filepath.Join(dir, m.Name+".json")
	if err := cache.WriteFileAtomic(path, append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Load reads a manifest by path.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest %s: %w", path, err)
	}
	return &m, nil
}
