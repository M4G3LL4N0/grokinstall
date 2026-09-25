// Package audit inspects an installed integration and reports evidence-backed
// findings. It is a deterministic review, not a vague AI critique, and it does
// not execute the capability it audits.
package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/M4G3LL4N0/grokinstall/internal/manifest"
	"github.com/M4G3LL4N0/grokinstall/internal/provision"
	"github.com/M4G3LL4N0/grokinstall/internal/receipt"
	"github.com/M4G3LL4N0/grokinstall/internal/registry"
)

// Severity ranks audit findings. Levels are used honestly: an audit that calls
// everything critical is not an audit.
type Severity string

// Severities.
const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityInfo     Severity = "info"
)

var severityRank = map[Severity]int{
	SeverityCritical: 0,
	SeverityHigh:     1,
	SeverityMedium:   2,
	SeverityLow:      3,
	SeverityInfo:     4,
}

// Finding is one audit result.
type Finding struct {
	Check    string   `json:"check"`
	Severity Severity `json:"severity"`
	Pass     bool     `json:"pass"`
	Detail   string   `json:"detail"`
}

// Report is the audit of one capability.
type Report struct {
	Capability string           `json:"capability"`
	CheckedAt  time.Time        `json:"checked_at"`
	Passed     bool             `json:"passed"`
	Findings   []Finding        `json:"findings"`
	Counts     map[Severity]int `json:"counts"`
}

// Engine audits installed capabilities.
type Engine struct {
	Registry *registry.Registry
}

// New builds an audit engine.
func New(reg *registry.Registry) *Engine { return &Engine{Registry: reg} }

// One audits a single capability.
func (e *Engine) One(name string) (*Report, error) {
	entry, err := e.Registry.Lookup(name)
	if err != nil {
		return nil, err
	}
	rep := &Report{
		Capability: name,
		CheckedAt:  time.Now().UTC(),
		Counts:     map[Severity]int{},
	}
	add := func(check string, pass bool, severity Severity, detail string) {
		rep.Findings = append(rep.Findings, Finding{Check: check, Pass: pass, Severity: severity, Detail: detail})
		if !pass {
			rep.Counts[severity]++
		}
	}

	// Registry entry coherence.
	add("registered capability", true, SeverityInfo,
		"registry entry state: "+string(entry.State))
	switch entry.State {
	case registry.StateDirty:
		add("installation state", false, SeverityCritical,
			"installation is dirty: "+entry.DirtyReason)
	case registry.StateBroken:
		add("installation state", false, SeverityHigh, "installation is marked broken")
	case registry.StateReady:
		add("installation state", true, SeverityInfo, "installation is ready")
	default:
		add("installation state", false, SeverityMedium, "unexpected state: "+string(entry.State))
	}

	// Manifest validity.
	if entry.ManifestPath == "" {
		add("manifest validity", false, SeverityCritical, "no manifest path is registered")
		return rep.finish(), nil
	}
	m, err := manifest.Load(entry.ManifestPath)
	if err != nil {
		add("manifest validity", false, SeverityCritical, err.Error())
		return rep.finish(), nil
	}
	if err := m.Validate(); err != nil {
		add("manifest validity", false, SeverityHigh, err.Error())
	} else {
		add("manifest validity", true, SeverityInfo, "schema "+m.Schema+" is valid")
	}

	// Support level honesty, including agreement with the registry state. A
	// plan-only manifest registered as ready would otherwise let GrokBot
	// discover a capability that cannot be called.
	switch m.Support {
	case manifest.SupportReady, manifest.SupportExperimental:
		if m.Runnable() {
			add("support declaration", true, SeverityInfo, "declared "+string(m.Support)+" and runnable")
		} else {
			add("support declaration", false, SeverityHigh,
				"declared "+string(m.Support)+" but execution is not supported")
		}
	default:
		if m.Runnable() {
			add("support declaration", false, SeverityCritical,
				"declared "+string(m.Support)+" but claims runnable")
		} else {
			add("support declaration", true, SeverityInfo, "declared "+string(m.Support))
		}
	}
	if !m.Support.Executable() && entry.State.Runnable() {
		add("plan versus installed", false, SeverityCritical,
			"registry marks this capability runnable but its manifest declares "+string(m.Support)+
				"; a plan must not be presented as an installed capability")
	} else {
		add("plan versus installed", true, SeverityInfo,
			"manifest support "+string(m.Support)+" agrees with registry state "+string(entry.State))
	}

	// Execution target and permissions.
	if m.Execution.Type == manifest.ExecutionSubprocess {
		info, err := os.Stat(m.Execution.Command)
		switch {
		case err != nil:
			add("runtime target", false, SeverityCritical, "target missing: "+err.Error())
		case info.IsDir():
			add("runtime target", false, SeverityCritical, "target is a directory")
		default:
			add("runtime target", true, SeverityInfo, m.Execution.Command)
			if info.Mode()&0o111 == 0 {
				add("target permissions", false, SeverityHigh, "target is not executable: "+info.Mode().String())
			} else if info.Mode()&0o022 != 0 {
				add("target permissions", false, SeverityMedium, "target is group/world writable: "+info.Mode().String())
			} else {
				add("target permissions", true, SeverityInfo, "mode "+info.Mode().String())
			}
		}
		// An unexpected writable directory around the target is a finding.
		if m.Execution.WorkingDir != "" {
			if di, err := os.Stat(m.Execution.WorkingDir); err == nil && di.Mode()&0o022 != 0 {
				add("working directory permissions", false, SeverityLow,
					"working directory is writable by others: "+m.Execution.WorkingDir)
			}
		}
	}

	// Runtime ownership and integrity.
	if entry.RuntimeDir != "" {
		e.auditRuntime(add, entry)
	} else {
		add("runtime ownership", true, SeverityInfo,
			"no provisioned runtime: the executable is externally owned and GrokInstall does not manage it")
	}

	// Adapter integrity.
	if entry.AdapterPath != "" {
		if _, err := os.Stat(entry.AdapterPath); err != nil {
			add("adapter integrity", false, SeverityHigh, "adapter missing: "+err.Error())
		} else {
			add("adapter integrity", true, SeverityInfo, entry.AdapterPath)
		}
	}

	// Receipt consistency.
	e.auditReceipt(add, entry)

	// Source identity is recorded.
	if m.Provenance.Identity == "" && entry.Source == "" {
		add("source identity", false, SeverityMedium, "no source identity is recorded")
	} else {
		add("source identity", true, SeverityInfo, firstNonEmpty(m.Provenance.Identity, entry.Source))
	}

	// Cache metadata.
	if m.Cache.Enabled {
		add("cache metadata", false, SeverityLow,
			"capability execution caching is enabled without an explicit semantic justification")
	} else {
		add("cache metadata", true, SeverityInfo, "execution results are not cached")
	}

	// GrokBot contract must be derivable and bounded.
	contract := m.ContractText()
	switch {
	case len(contract) > manifest.MaxContractBytes:
		add("grokbot contract", false, SeverityMedium, fmt.Sprintf("contract is %d bytes", len(contract)))
	case strings.Contains(contract, entry.Source) && strings.HasPrefix(entry.Source, "path:/"):
		add("grokbot contract", false, SeverityLow, "contract leaks the local source path")
	default:
		add("grokbot contract", true, SeverityInfo, fmt.Sprintf("%d bytes", len(contract)))
	}

	return rep.finish(), nil
}

func (e *Engine) auditRuntime(add func(string, bool, Severity, string), entry registry.Entry) {
	root := e.Registry.Root
	abs, err := filepath.Abs(entry.RuntimeDir)
	if err != nil {
		add("runtime ownership", false, SeverityHigh, err.Error())
		return
	}
	rootAbs, _ := filepath.Abs(root)
	if abs != rootAbs && !strings.HasPrefix(abs, rootAbs+string(filepath.Separator)) {
		add("runtime ownership", false, SeverityCritical,
			"runtime is outside the GrokInstall state directory: "+abs)
		return
	}
	add("runtime ownership", true, SeverityInfo, "runtime is GrokInstall-owned")

	metaPath := filepath.Join(entry.RuntimeDir, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		add("runtime metadata", false, SeverityMedium, "metadata missing: "+err.Error())
		return
	}
	var meta provision.Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		add("runtime metadata", false, SeverityMedium, "metadata is corrupt: "+err.Error())
		return
	}
	add("runtime metadata", true, SeverityInfo, "method "+string(meta.Method))

	// Checksum provenance honesty.
	switch meta.ChecksumStatus {
	case "verified":
		add("artifact checksum", true, SeverityInfo, "verified against a published checksum")
	case "unavailable-upstream":
		add("artifact checksum", true, SeverityLow,
			"upstream published no checksum; the artifact was not verified by a published hash")
	case "":
		add("artifact checksum", false, SeverityLow, "checksum status was not recorded")
	default:
		add("artifact checksum", false, SeverityMedium, "unknown checksum status: "+meta.ChecksumStatus)
	}

	// Integrity: compare recorded hashes against what is on disk now.
	if len(meta.Checksums) == 0 {
		add("runtime integrity", false, SeverityLow, "no file hashes were recorded for the runtime")
		return
	}
	var modified []string
	var missing []string
	paths := make([]string, 0, len(meta.Checksums))
	for p := range meta.Checksums {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		full := filepath.Join(entry.RuntimeDir, rel)
		actual, err := hashFile(full)
		if err != nil {
			missing = append(missing, rel)
			continue
		}
		if actual != meta.Checksums[rel] {
			modified = append(modified, rel)
		}
	}
	switch {
	case len(modified) > 0:
		add("runtime integrity", false, SeverityCritical,
			"files changed since provisioning: "+strings.Join(modified, ", "))
	case len(missing) > 0:
		add("runtime integrity", false, SeverityHigh,
			"files are missing from the runtime: "+strings.Join(missing, ", "))
	default:
		add("runtime integrity", true, SeverityInfo,
			fmt.Sprintf("%d files match their recorded hashes", len(paths)))
	}
}

func (e *Engine) auditReceipt(add func(string, bool, Severity, string), entry registry.Entry) {
	if entry.ReceiptPath == "" {
		add("receipt consistency", false, SeverityMedium, "no receipt is recorded for this capability")
		return
	}
	r, err := receipt.Load(entry.ReceiptPath)
	if err != nil {
		add("receipt consistency", false, SeverityHigh, "receipt unreadable: "+err.Error())
		return
	}
	if r.Capability != entry.Name {
		add("receipt consistency", false, SeverityHigh,
			fmt.Sprintf("receipt names capability %q but the registry names %q", r.Capability, entry.Name))
		return
	}
	if r.Result != receipt.ResultInstalled && r.Result != receipt.ResultPlanned {
		add("receipt consistency", false, SeverityMedium,
			"receipt records outcome "+r.Result+" for a registered capability")
		return
	}
	add("receipt consistency", true, SeverityInfo,
		"install "+r.InstallID+" recorded with outcome "+r.Result)

	// Every file the receipt claims to own must still be accounted for.
	if r.Result == receipt.ResultInstalled && !r.Verification.Passed {
		add("receipt verification", false, SeverityHigh,
			"receipt records an installed outcome without passing verification")
	}
}

func (rep *Report) finish() *Report {
	sort.SliceStable(rep.Findings, func(i, j int) bool {
		if rep.Findings[i].Pass != rep.Findings[j].Pass {
			return !rep.Findings[i].Pass
		}
		return severityRank[rep.Findings[i].Severity] < severityRank[rep.Findings[j].Severity]
	})
	rep.Passed = rep.Counts[SeverityCritical] == 0 && rep.Counts[SeverityHigh] == 0
	return rep
}

// Render writes a human-readable audit.
func (r *Report) Render(w io.Writer) {
	fmt.Fprintf(w, "Audit: %s\n\n", r.Capability)
	for _, f := range r.Findings {
		mark := "ok  "
		if !f.Pass {
			mark = "FAIL"
		}
		fmt.Fprintf(w, "  [%s] %-8s %-24s %s\n", mark, f.Severity, f.Check, f.Detail)
	}
	fmt.Fprintf(w, "\n  critical=%d high=%d medium=%d low=%d\n",
		r.Counts[SeverityCritical], r.Counts[SeverityHigh], r.Counts[SeverityMedium], r.Counts[SeverityLow])
	if r.Passed {
		fmt.Fprintln(w, "\n  audit passed")
	} else {
		fmt.Fprintln(w, "\n  audit failed")
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
