// Package manifest defines the canonical capability contract.
//
// The manifest is what GrokBot understands: a small, complete description of
// how to call a capability, never an implementation. It carries enough
// information to execute (command, argv, cwd, environment, timeout, bounds)
// and nothing about the upstream repository's internals.
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

// MaxContractBytes bounds the generated GrokBot contract.
const MaxContractBytes = 8192

var nameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Support states how much runtime support a strategy has.
type Support string

// Support levels. These are deliberately explicit: detection is never blurred
// with installation.
const (
	// SupportReady means the capability is installed and executable.
	SupportReady Support = "supported"
	// SupportExperimental means it runs, with a narrower or less-tested path.
	SupportExperimental Support = "experimental"
	// SupportPlanOnly means it is detected and planned, but not runnable.
	SupportPlanOnly Support = "plan_only"
	// SupportUnsupported means GrokInstall will not attempt it.
	SupportUnsupported Support = "unsupported"
	// SupportNone is the result of a deliberate no-install decision.
	SupportNone Support = "none"
)

// ValidSupport reports whether a support level is known.
func ValidSupport(s Support) bool {
	switch s {
	case SupportReady, SupportExperimental, SupportPlanOnly, SupportUnsupported, SupportNone:
		return true
	}
	return false
}

// Executable reports whether a support level implies a runnable capability.
func (s Support) Executable() bool {
	return s == SupportReady || s == SupportExperimental
}

// Execution types.
const (
	ExecutionSubprocess = "subprocess"
	ExecutionBuiltin    = "builtin"
	ExecutionNone       = "none"
)

// Input modes.
const (
	ModeStdinJSON = "stdin_json"
	ModeArgv      = "argv"
	ModeNone      = "none"
)

// Manifest is an installed capability contract.
type Manifest struct {
	Schema     string     `json:"schema"`
	Name       string     `json:"name"`
	Version    string     `json:"version"`
	Source     string     `json:"source"`
	Goal       string     `json:"goal"`
	Strategy   string     `json:"strategy"`
	Support    Support    `json:"support"`
	Status     string     `json:"status"`
	Execution  Execution  `json:"execution"`
	Input      Schema     `json:"input"`
	Output     Schema     `json:"output"`
	Grokbot    Grokbot    `json:"grokbot"`
	Cache      Cache      `json:"cache"`
	Security   Security   `json:"security"`
	Provenance Provenance `json:"provenance"`
}

// Execution describes how a capability runs, completely and explicitly.
type Execution struct {
	Type           string            `json:"type"`
	Supported      bool              `json:"supported"`
	Command        string            `json:"command,omitempty"`
	Args           []string          `json:"args,omitempty"`
	InputMode      string            `json:"input_mode,omitempty"`
	ArgvMap        map[string]string `json:"argv_map,omitempty"`
	Handler        string            `json:"handler,omitempty"`
	HandlerConfig  map[string]string `json:"handler_config,omitempty"`
	WorkingDir     string            `json:"cwd,omitempty"`
	Env            map[string]string `json:"env,omitempty"`
	TimeoutMs      int               `json:"timeout_ms,omitempty"`
	MaxOutputBytes int               `json:"max_output_bytes,omitempty"`
	ExpectJSON     bool              `json:"expect_json,omitempty"`
	Note           string            `json:"note,omitempty"`
}

// Schema is a small input or output description.
type Schema struct {
	Fields []Field `json:"fields"`
}

// Field is one input or output field.
type Field struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// Grokbot is the handoff contract.
type Grokbot struct {
	UseWhen   string `json:"use_when"`
	DoNot     string `json:"do_not"`
	OnFailure string `json:"on_failure,omitempty"`
}

// Cache describes whether results may be reused. Execution results are not
// cached by default: caching is a semantic decision, not a convenience.
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
	OwnedByGrokinstall bool     `json:"owned_by_grokinstall"`
	Notes              []string `json:"notes,omitempty"`
}

// Provenance records where the manifest came from, without embedding source.
type Provenance struct {
	Source         string          `json:"source"`
	Identity       string          `json:"identity,omitempty"`
	CommitSHA      string          `json:"commit_sha,omitempty"`
	InspectedAt    string          `json:"inspected_at,omitempty"`
	InstallID      string          `json:"install_id,omitempty"`
	ReceiptPath    string          `json:"receipt_path,omitempty"`
	GrokInstallVer string          `json:"grokinstall_version,omitempty"`
	Evidence       []evidence.Item `json:"evidence,omitempty"`
}

// Validate checks the invariants a registered manifest must satisfy. In
// particular, a manifest may never claim execution support it cannot deliver.
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
	// Every manifest must declare its support level: this is what keeps
	// "detected" from silently reading as "installed".
	if m.Support == "" {
		return fmt.Errorf("manifest support level is required (supported, experimental, plan_only, unsupported, none)")
	}
	if !ValidSupport(m.Support) {
		return fmt.Errorf("unknown support level %q", m.Support)
	}

	switch m.Execution.Type {
	case ExecutionSubprocess:
		if strings.TrimSpace(m.Execution.Command) == "" {
			return fmt.Errorf("subprocess execution requires a command")
		}
	case ExecutionBuiltin:
		if strings.TrimSpace(m.Execution.Handler) == "" {
			return fmt.Errorf("builtin execution requires a handler")
		}
	case ExecutionNone, "":
		if m.Execution.Supported {
			return fmt.Errorf("execution type %q cannot be marked supported", m.Execution.Type)
		}
	default:
		return fmt.Errorf("unknown execution type %q", m.Execution.Type)
	}

	if m.Execution.Supported && !m.Support.Executable() {
		return fmt.Errorf("execution.supported is true but support is %q", m.Support)
	}
	if m.Execution.Supported && m.Execution.Type == ExecutionSubprocess && strings.TrimSpace(m.Execution.Command) == "" {
		return fmt.Errorf("execution.supported is true but no command is registered")
	}
	for _, f := range m.Input.Fields {
		if strings.TrimSpace(f.Name) == "" {
			return fmt.Errorf("input fields require a name")
		}
	}
	for _, f := range m.Output.Fields {
		if strings.TrimSpace(f.Name) == "" {
			return fmt.Errorf("output fields require a name")
		}
	}
	return nil
}

// Runnable reports whether this capability can actually be invoked.
func (m *Manifest) Runnable() bool {
	return m.Execution.Supported && m.Support.Executable()
}

// CallLine is the exact command GrokBot uses to invoke the capability.
func (m *Manifest) CallLine() string {
	return fmt.Sprintf("grokinstall run %s --input '<json>'", m.Name)
}

// ContractText renders the smallest sufficient GrokBot handoff, derived
// mechanically from the manifest. It never contains repository paths, source
// files or documentation text.
func (m *Manifest) ContractText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "CAPABILITY\n%s\n\n", m.Name)
	fmt.Fprintf(&b, "USE WHEN\n%s\n\n", orDefault(m.Grokbot.UseWhen, "the described operation is requested"))

	if !m.Runnable() {
		fmt.Fprintf(&b, "STATUS\n%s\n\n", m.planOnlyStatus())
		fmt.Fprintf(&b, "DO NOT\nThis capability is not executable: %s\n", m.Execution.Note)
		return clamp(b.String())
	}

	fmt.Fprintf(&b, "CALL\n%s\n", m.CallLine())
	if fields := describeFields(m.Input.Fields); fields != "" {
		fmt.Fprintf(&b, "\nINPUT\n%s\n", fields)
	}
	if fields := describeFields(m.Output.Fields); fields != "" {
		fmt.Fprintf(&b, "\nOUTPUT\n%s\n", fields)
	}
	fmt.Fprintf(&b, "\nDO NOT\n%s\n", orDefault(m.Grokbot.DoNot,
		"load the implementation repository into GrokBot before invoking this capability"))
	failure := m.Grokbot.OnFailure
	if failure == "" {
		failure = fmt.Sprintf("Run:\ngrokinstall diagnose %s", m.Name)
	}
	fmt.Fprintf(&b, "\nON FAILURE\n%s\n", failure)
	return clamp(b.String())
}

func (m *Manifest) planOnlyStatus() string {
	switch m.Support {
	case SupportPlanOnly:
		return fmt.Sprintf("plan only (%s is detected but not installed)", m.Strategy)
	case SupportNone:
		return "no installation was needed"
	case SupportUnsupported:
		return fmt.Sprintf("%s is not supported", m.Strategy)
	default:
		return "not executable"
	}
}

func describeFields(fields []Field) string {
	if len(fields) == 0 {
		return ""
	}
	var b strings.Builder
	for i, f := range fields {
		if i >= 12 {
			fmt.Fprintf(&b, "... and %d more\n", len(fields)-12)
			break
		}
		required := ""
		if f.Required {
			required = " (required)"
		}
		desc := f.Description
		if desc != "" {
			desc = " - " + truncate(desc, 80)
		}
		fmt.Fprintf(&b, "%s: %s%s%s\n", f.Name, f.Type, required, desc)
	}
	return strings.TrimRight(b.String(), "\n")
}

func clamp(s string) string {
	if len(s) <= MaxContractBytes {
		return s
	}
	return s[:MaxContractBytes-1] + "…"
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// JSON serializes the manifest.
func (m *Manifest) JSON() ([]byte, error) { return json.MarshalIndent(m, "", "  ") }

// Save writes a manifest atomically into dir and returns its path.
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

// Parse decodes a manifest and checks its schema.
func Parse(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	if m.Schema != SchemaID {
		return nil, fmt.Errorf("manifest schema must be %q, got %q", SchemaID, m.Schema)
	}
	return &m, nil
}

// Load reads a manifest by path.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("load manifest %s: %w", path, err)
	}
	m, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return m, nil
}
