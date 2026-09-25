// Package receipt records exactly what an install changed.
//
// A receipt is the audit trail that makes uninstall, repair, update and audit
// possible. It records real observed changes, never inferred ones, and it
// records ownership so cleanup can never guess.
package receipt

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/M4G3LL4N0/grokinstall/internal/cache"
)

// Schema identifies the receipt format.
const Schema = "grokinstall/receipt/v1"

// FileChange is one file an install created, changed or owns.
type FileChange struct {
	Path     string `json:"path"`
	Action   string `json:"action,omitempty"`
	Owner    string `json:"owner"`
	Size     int64  `json:"size,omitempty"`
	Checksum string `json:"checksum,omitempty"`
}

// Command is one command actually executed during an install.
type Command struct {
	Binary   string   `json:"binary"`
	Args     []string `json:"args,omitempty"`
	Reason   string   `json:"reason,omitempty"`
	ExitCode int      `json:"exit_code"`
	Duration string   `json:"duration,omitempty"`
}

// Check is one verification step and its actual outcome.
type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail,omitempty"`
}

// Verification records what was actually checked after staging.
type Verification struct {
	Performed bool    `json:"performed"`
	Passed    bool    `json:"passed"`
	Checks    []Check `json:"checks,omitempty"`
	Output    string  `json:"output_sample,omitempty"`
}

// Provisioning records what was done to obtain an executable. It is the record
// that makes provisioning reversible and auditable. It never contains secrets.
type Provisioning struct {
	Method            string            `json:"method"`
	ArtifactSource    string            `json:"artifact_source,omitempty"`
	ArtifactVersion   string            `json:"artifact_version,omitempty"`
	Asset             string            `json:"asset,omitempty"`
	PublishedChecksum string            `json:"published_checksum,omitempty"`
	ActualChecksum    string            `json:"actual_checksum,omitempty"`
	ChecksumStatus    string            `json:"checksum_status,omitempty"`
	BuildCommand      []string          `json:"build_command,omitempty"`
	RuntimeDir        string            `json:"runtime_dir,omitempty"`
	Ownership         string            `json:"ownership,omitempty"`
	PackageMutations  []string          `json:"package_manager_mutations,omitempty"`
	Approvals         []string          `json:"security_approvals,omitempty"`
	Notes             []string          `json:"notes,omitempty"`
	Checksums         map[string]string `json:"runtime_checksums,omitempty"`
}

// Receipt is the record of one mutating install.
type Receipt struct {
	Schema            string        `json:"schema"`
	InstallID         string        `json:"install_id"`
	Timestamp         string        `json:"timestamp"`
	Source            string        `json:"source"`
	SourceID          string        `json:"source_id,omitempty"`
	CommitSHA         string        `json:"source_commit,omitempty"`
	Goal              string        `json:"goal,omitempty"`
	Strategy          string        `json:"strategy"`
	Support           string        `json:"support,omitempty"`
	Capability        string        `json:"capability,omitempty"`
	State             string        `json:"state,omitempty"`
	FilesCreated      []FileChange  `json:"files_created,omitempty"`
	FilesModified     []FileChange  `json:"files_modified,omitempty"`
	CommandsExecuted  []Command     `json:"commands_executed,omitempty"`
	DependenciesAdded []string      `json:"dependencies_introduced"`
	Provisioning      *Provisioning `json:"provisioning,omitempty"`
	Verification      Verification  `json:"verification"`
	Result            string        `json:"result"`
	Warnings          []string      `json:"warnings,omitempty"`
	UninstalledAt     string        `json:"uninstalled_at,omitempty"`
	UninstallID       string        `json:"uninstall_id,omitempty"`
}

// Results a receipt can carry.
const (
	ResultInstalled = "installed"
	ResultPlanned   = "planned"
	ResultNoInstall = "no_install"
	ResultFailed    = "failed"
	ResultUninstall = "uninstalled"
	ResultDirty     = "dirty"
)

// Validate checks receipt invariants. A receipt may never claim a successful
// install whose verification did not pass.
func (r *Receipt) Validate() error {
	if r.Schema == "" {
		r.Schema = Schema
	}
	if r.InstallID == "" {
		return fmt.Errorf("receipt install_id is required")
	}
	if r.Source == "" {
		return fmt.Errorf("receipt source is required")
	}
	if r.Result == "" {
		return fmt.Errorf("receipt result is required")
	}
	if r.Result == ResultInstalled && !r.Verification.Passed {
		return fmt.Errorf("receipt cannot claim an installed result without passing verification")
	}
	if r.Result == ResultInstalled && r.Capability == "" {
		return fmt.Errorf("an installed receipt must name the capability")
	}
	if r.Result == ResultPlanned && !r.Verification.Passed {
		return fmt.Errorf("a planned receipt requires a passing manifest validation")
	}
	if r.Result == ResultDirty && r.State == "" {
		return fmt.Errorf("a dirty receipt must record the state that could not be rolled back")
	}
	// A receipt must never carry secret material: it is long-lived audit data.
	if r.Provisioning != nil && containsSecretLike(r.Provisioning.Approvals) {
		return fmt.Errorf("receipt provisioning data looks like it contains credentials")
	}
	return nil
}

// JSON serializes the receipt.
func (r *Receipt) JSON() ([]byte, error) { return json.MarshalIndent(r, "", "  ") }

// Save validates and writes the receipt atomically.
func (r *Receipt) Save(dir string) (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	data, err := r.JSON()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, r.InstallID+".json")
	if err := cache.WriteFileAtomic(path, append(data, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Load reads a receipt by path.
func Load(path string) (*Receipt, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load receipt %s: %w", path, err)
	}
	var r Receipt
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("parse receipt %s: %w", path, err)
	}
	return &r, nil
}

// FindByInstallID looks up a receipt inside a receipts directory.
func FindByInstallID(dir, installID string) (*Receipt, error) {
	return Load(filepath.Join(dir, installID+".json"))
}

// containsSecretLike is a cheap guard so a provisioning note can never become a
// place where a token is written down.
func containsSecretLike(values []string) bool {
	for _, v := range values {
		lower := strings.ToLower(v)
		for _, marker := range []string{"ghp_", "sk-", "bearer ", "password=", "token="} {
			if strings.Contains(lower, marker) {
				return true
			}
		}
	}
	return false
}
