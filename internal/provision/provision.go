package provision

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
)

// Method identifies how a provisioning candidate obtains an executable.
type Method string

// Provisioning methods, in the order the policy prefers them. The order is the
// product's safety argument: each step further down means more trust, more
// mutation, or both.
const (
	// MethodExisting uses a compatible executable already on the machine.
	MethodExisting Method = "already_installed"
	// MethodRelease downloads a published upstream release artifact.
	MethodRelease Method = "github_release"
	// MethodSourceBuild builds from source into an owned runtime.
	MethodSourceBuild Method = "source_build"
	// MethodPackageManager installs through an ecosystem package manager.
	MethodPackageManager Method = "package_manager"
	// MethodSystemPackageManager changes system-wide package state.
	MethodSystemPackageManager Method = "system_package_manager"
	// MethodUserCommand is a command the user supplied explicitly.
	MethodUserCommand Method = "user_command"
	// MethodUnresolved means nothing safe is available.
	MethodUnresolved Method = "unresolved"
)

// methodRank encodes the policy preference. Lower is safer.
var methodRank = map[Method]int{
	MethodExisting:             1,
	MethodRelease:              2,
	MethodSourceBuild:          3,
	MethodPackageManager:       4,
	MethodSystemPackageManager: 5,
	MethodUserCommand:          6,
	MethodUnresolved:           7,
}

// Ownership describes who controls the resulting files.
type Ownership string

// Ownership levels.
const (
	// OwnershipNone changes nothing.
	OwnershipNone Ownership = "none"
	// OwnershipGrokinstall places files under ~/.grokinstall/runtimes.
	OwnershipGrokinstall Ownership = "grokinstall"
	// OwnershipExternal is pre-existing, user-managed software.
	OwnershipExternal Ownership = "external"
	// OwnershipSystem changes machine-wide state.
	OwnershipSystem Ownership = "system"
)

// Authorization names a specific approval that would unlock a candidate. There
// is deliberately no generic "yes": each approval names the exact risk class.
type Authorization string

// Authorizations.
const (
	// AuthInstallScripts permits package lifecycle scripts to execute.
	AuthInstallScripts Authorization = "allow_install_scripts"
	// AuthSystemPackageManager permits machine-wide package changes.
	AuthSystemPackageManager Authorization = "allow_system_package_manager"
	// AuthSourceBuild permits compiling untrusted source.
	AuthSourceBuild Authorization = "allow_source_build"
	// AuthNetwork permits reaching the network during provisioning.
	AuthNetwork Authorization = "allow_network"
)

// Candidate is one way to obtain the required executable.
type Candidate struct {
	Method  Method `json:"method"`
	Source  string `json:"source"`
	Version string `json:"version,omitempty"`
	Asset   string `json:"asset,omitempty"`
	// Commands are the exact argv entries that would run, for disclosure.
	Commands [][]string `json:"commands,omitempty"`

	NetworkAccess     bool            `json:"network_access"`
	FilesystemChanges []string        `json:"filesystem_changes,omitempty"`
	LifecycleScripts  bool            `json:"lifecycle_scripts"`
	RequiresPrivilege bool            `json:"requires_privilege"`
	Ownership         Ownership       `json:"ownership"`
	Reversible        bool            `json:"reversible"`
	Verification      string          `json:"verification"`
	Requires          []Authorization `json:"requires_authorization,omitempty"`
	Risks             []string        `json:"risks,omitempty"`
	// Notes are neutral, factual remarks about the route.
	Notes []string `json:"notes,omitempty"`
	// RequiresSourceBuild marks a candidate that compiles untrusted source.
	RequiresSourceBuild bool `json:"requires_source_build,omitempty"`

	// Path is the resolved executable for candidates that already exist.
	Path string `json:"path,omitempty"`
}

// Safe reports whether a candidate may run under the default policy without any
// additional authorization. A candidate is safe only if it asks for no
// approval, needs no privilege, and mutates nothing outside GrokInstall.
func (c Candidate) Safe() bool {
	if len(c.Requires) > 0 {
		return false
	}
	if c.RequiresPrivilege {
		return false
	}
	if c.Method == MethodSystemPackageManager {
		return false
	}
	return true
}

// Rank returns the policy ordering position of a candidate.
func (c Candidate) Rank() int { return methodRank[c.Method] }

// Summary is the one-line description used in comparisons and refusals.
func (c Candidate) Summary() string {
	switch c.Method {
	case MethodExisting:
		return "already installed: " + c.Path
	case MethodRelease:
		return "upstream release artifact: " + c.Asset
	case MethodSourceBuild:
		return "build from source: " + c.Source
	case MethodPackageManager:
		return "package manager: " + c.Source
	case MethodSystemPackageManager:
		return "system package manager: " + c.Source
	case MethodUserCommand:
		return "user-provided command: " + c.Path
	default:
		return "no safe provisioning route available"
	}
}

// Compare orders candidates by the policy preference. The smallest safe route
// wins; ties break on the narrower ownership and then on the shorter name so
// the result is deterministic.
func Compare(candidates []Candidate) []Candidate {
	out := append([]Candidate{}, candidates...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank() != out[j].Rank() {
			return out[i].Rank() < out[j].Rank()
		}
		if out[i].Ownership != out[j].Ownership {
			return ownershipRank(out[i].Ownership) < ownershipRank(out[j].Ownership)
		}
		return out[i].Summary() < out[j].Summary()
	})
	return out
}

func ownershipRank(o Ownership) int {
	switch o {
	case OwnershipNone:
		return 0
	case OwnershipGrokinstall:
		return 1
	case OwnershipExternal:
		return 2
	case OwnershipSystem:
		return 3
	default:
		return 4
	}
}

// Select picks the safest available candidate for a policy, or explains why
// nothing is selectable.
func Select(candidates []Candidate, policy Policy) (Candidate, *Refusal) {
	ordered := Compare(candidates)
	if len(ordered) == 0 {
		return Candidate{}, &Refusal{
			Reason:   "no provisioning route was discovered for the required executable",
			Required: []Authorization{},
		}
	}
	switch policy.Mode {
	case PolicyNever:
		return Candidate{}, &Refusal{
			Reason:       "provisioning is disabled (--provision=never)",
			Required:     requiredFor(ordered[0]),
			Safest:       ordered[0],
			Alternatives: ordered,
		}
	case PolicySafe, PolicyPrompt:
		for _, c := range ordered {
			if c.Method == MethodUnresolved {
				continue
			}
			if c.Safe() || policy.Grants(c) {
				return c, nil
			}
		}
		return Candidate{}, &Refusal{
			Reason:       "the safest available method requires authorization beyond the safe policy",
			Required:     requiredFor(ordered[0]),
			Safest:       ordered[0],
			Alternatives: ordered,
		}
	default:
		return ordered[0], nil
	}
}

// Policy is the provisioning policy for one install.
type Policy struct {
	Mode  Mode
	Allow []Authorization
}

// Mode selects how much provisioning is permitted.
type Mode string

// Policy modes.
const (
	// PolicySafe permits only candidates that need no extra authorization.
	PolicySafe Mode = "safe"
	// PolicyNever disables provisioning entirely.
	PolicyNever Mode = "never"
	// PolicyPrompt behaves like safe, and surfaces decisions to the caller
	// rather than prompting on a terminal.
	PolicyPrompt Mode = "prompt"
)

// ParseMode validates a policy mode.
func ParseMode(s string) (Mode, bool) {
	switch Mode(strings.ToLower(strings.TrimSpace(s))) {
	case PolicySafe:
		return PolicySafe, true
	case PolicyNever:
		return PolicyNever, true
	case PolicyPrompt:
		return PolicyPrompt, true
	default:
		return "", false
	}
}

// Grants reports whether the policy carries every specific authorization a
// candidate needs. A candidate that needs privilege or would change machine-wide
// state always requires its matching authorization, even if the candidate did
// not spell it out: an approval flag can never be a generic bypass.
func (p Policy) Grants(c Candidate) bool {
	needed := requiredFor(c)
	if len(needed) == 0 {
		return true
	}
	for _, need := range needed {
		found := false
		for _, have := range p.Allow {
			if have == need {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// GrantsOne reports whether a single authorization was granted.
func (p Policy) GrantsOne(a Authorization) bool {
	for _, have := range p.Allow {
		if have == a {
			return true
		}
	}
	return false
}

// requiredFor lists every authorization a candidate needs, including the ones
// implied by its risk class.
func requiredFor(c Candidate) []Authorization {
	seen := map[Authorization]bool{}
	var out []Authorization
	for _, a := range c.Requires {
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	if c.RequiresPrivilege && !seen[AuthSystemPackageManager] {
		out = append(out, AuthSystemPackageManager)
	}
	if c.Method == MethodSystemPackageManager && !seen[AuthSystemPackageManager] {
		out = append(out, AuthSystemPackageManager)
	}
	return out
}

// Refusal is a structured explanation of why provisioning did not proceed. It
// is designed to be shown to a user or a future GrokBot, not to be parsed as
// prose.
type Refusal struct {
	Executable   string          `json:"executable,omitempty"`
	Reason       string          `json:"reason"`
	Safest       Candidate       `json:"safest,omitempty"`
	Required     []Authorization `json:"required_authorization,omitempty"`
	Alternatives []Candidate     `json:"alternatives,omitempty"`
	BlockedBy    []string        `json:"blocked_by,omitempty"`
	Details      []string        `json:"details,omitempty"`
}

// Error lets a Refusal travel through ordinary error paths while remaining
// inspectable by callers that type-assert it.
func (r *Refusal) Error() string {
	if len(r.Details) == 0 {
		return r.Reason
	}
	return r.Reason + ": " + strings.Join(r.Details, "; ")
}

// Explain renders the refusal for a terminal.
func (r *Refusal) Explain(w io.Writer) {
	if r == nil {
		return
	}
	fmt.Fprintln(w, "PROVISIONING BLOCKED")
	if r.Executable != "" {
		fmt.Fprintf(w, "\nExecutable:\n%s\n", r.Executable)
	}
	if r.Safest.Method != "" {
		fmt.Fprintf(w, "\nSafest available method:\n%s\n", r.Safest.Summary())
		if len(r.Safest.Risks) > 0 {
			fmt.Fprintln(w, "\nRisk:")
			for _, risk := range r.Safest.Risks {
				fmt.Fprintf(w, "  - %s\n", risk)
			}
		}
		if r.Safest.LifecycleScripts {
			fmt.Fprintln(w, "  - the package declares lifecycle scripts that would execute")
		}
		if r.Safest.RequiresPrivilege {
			fmt.Fprintln(w, "  - the method requires elevated privileges")
		}
	}
	if len(r.Required) > 0 {
		fmt.Fprintln(w, "\nRequired authorization:")
		for _, a := range r.Required {
			fmt.Fprintf(w, "  --%s\n", strings.ReplaceAll(string(a), "_", "-"))
		}
	}
	fmt.Fprintf(w, "\nWhy:\n%s\n", r.Reason)
	if len(r.Alternatives) > 0 {
		fmt.Fprintln(w, "\nAlternatives:")
		for _, c := range r.Alternatives {
			fmt.Fprintf(w, "  %-26s %s\n", c.Method, c.Summary())
			// Surface the risk of each route, not only the preferred one, so a
			// reader can choose a different trade-off deliberately.
			if c.LifecycleScripts {
				fmt.Fprintf(w, "  %-26s   contains lifecycle scripts that would execute\n", "")
			}
			if c.RequiresPrivilege {
				fmt.Fprintf(w, "  %-26s   requires elevated privileges\n", "")
			}
			if c.Ownership == OwnershipSystem {
				fmt.Fprintf(w, "  %-26s   changes machine-wide package state\n", "")
			}
			for _, risk := range c.Risks {
				fmt.Fprintf(w, "  %-26s   %s\n", "", risk)
			}
			for _, a := range c.Requires {
				fmt.Fprintf(w, "  %-26s   requires --%s\n", "", strings.ReplaceAll(string(a), "_", "-"))
			}
		}
	}
}

// Result records what provisioning actually did.
type Result struct {
	Method            Method          `json:"method"`
	Source            string          `json:"source"`
	Version           string          `json:"version,omitempty"`
	Asset             string          `json:"asset,omitempty"`
	ActualChecksum    string          `json:"actual_checksum,omitempty"`
	PublishedChecksum string          `json:"published_checksum,omitempty"`
	ChecksumStatus    string          `json:"checksum_status,omitempty"`
	BuildCommand      []string        `json:"build_command,omitempty"`
	RuntimeDir        string          `json:"runtime_dir,omitempty"`
	ExecutablePath    string          `json:"executable_path,omitempty"`
	FilesCreated      []string        `json:"files_created,omitempty"`
	PackageMutation   []string        `json:"package_manager_mutations,omitempty"`
	Approvals         []Authorization `json:"approvals,omitempty"`
	Notes             []string        `json:"notes,omitempty"`
}

// Metadata is persisted alongside an owned runtime so a later run can detect
// modification and explain ownership without re-deriving it.
type Metadata struct {
	Schema         string            `json:"schema"`
	Capability     string            `json:"capability"`
	Source         string            `json:"source"`
	Method         Method            `json:"method"`
	Version        string            `json:"version,omitempty"`
	Asset          string            `json:"asset,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	Executable     string            `json:"executable"`
	Checksums      map[string]string `json:"checksums,omitempty"`
	ChecksumStatus string            `json:"checksum_status,omitempty"`
	Ownership      Ownership         `json:"ownership"`
	Approvals      []Authorization   `json:"approvals,omitempty"`
	Notes          []string          `json:"notes,omitempty"`
}

// MetadataSchema identifies the runtime metadata format.
const MetadataSchema = "grokinstall/runtime/v1"
