// Package diagnose explains why an installed capability is not working, using
// direct evidence wherever it exists.
//
// It never guesses. Each finding names the symptom, the evidence that was
// actually observed, the probable root cause, a confidence level, the affected
// component, the smallest fix and the command that verifies the fix.
package diagnose

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"grokinstall/internal/manifest"
	"grokinstall/internal/provision"
	"grokinstall/internal/receipt"
	"grokinstall/internal/registry"
)

// Severity ranks how urgently a finding needs attention.
type Severity string

// Severities, deliberately not inflated.
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

// Finding is one diagnosis.
type Finding struct {
	Severity     Severity `json:"severity"`
	Symptom      string   `json:"symptom"`
	Evidence     []string `json:"evidence"`
	RootCause    string   `json:"root_cause"`
	Confidence   string   `json:"confidence"`
	Component    string   `json:"component"`
	Fix          string   `json:"smallest_fix"`
	Verification string   `json:"verification_command"`
}

// Report is a diagnosis of one or all capabilities.
type Report struct {
	Capability string    `json:"capability,omitempty"`
	CheckedAt  time.Time `json:"checked_at"`
	Healthy    bool      `json:"healthy"`
	Findings   []Finding `json:"findings"`
}

// Engine diagnoses installed capabilities.
type Engine struct {
	Registry *registry.Registry
}

// New builds a diagnosis engine.
func New(reg *registry.Registry) *Engine { return &Engine{Registry: reg} }

// All diagnoses every installed capability.
func (e *Engine) All() ([]Report, error) {
	entries, err := e.Registry.List()
	if err != nil {
		return nil, err
	}
	var out []Report
	for _, entry := range entries {
		rep, err := e.One(entry.Name)
		if err != nil {
			out = append(out, Report{
				Capability: entry.Name,
				CheckedAt:  time.Now().UTC(),
				Healthy:    false,
				Findings: []Finding{{
					Severity:     SeverityCritical,
					Symptom:      "capability cannot be diagnosed",
					Evidence:     []string{err.Error()},
					RootCause:    "the registry entry could not be read",
					Confidence:   "high",
					Component:    "registry",
					Fix:          "run grokinstall doctor and repair the registry",
					Verification: "grokinstall doctor",
				}},
			})
			continue
		}
		out = append(out, *rep)
	}
	return out, nil
}

// One diagnoses a single capability.
func (e *Engine) One(name string) (*Report, error) {
	entry, err := e.Registry.Lookup(name)
	if err != nil {
		return nil, err
	}
	rep := &Report{Capability: name, CheckedAt: time.Now().UTC(), Healthy: true}
	add := func(f Finding) {
		rep.Findings = append(rep.Findings, f)
		if f.Severity == SeverityCritical || f.Severity == SeverityHigh {
			rep.Healthy = false
		}
	}

	// 1. Lifecycle state.
	switch entry.State {
	case registry.StateDirty:
		add(Finding{
			Severity:     SeverityCritical,
			Symptom:      "installation is in a dirty state",
			Evidence:     []string{"registry state: dirty", "reason: " + entry.DirtyReason},
			RootCause:    "an install mutation partially applied and rollback could not prove complete restoration",
			Confidence:   "high",
			Component:    "transaction",
			Fix:          "uninstall the capability, then reinstall it",
			Verification: "grokinstall install " + entry.Source,
		})
	case registry.StateBroken:
		add(Finding{
			Severity:     SeverityHigh,
			Symptom:      "capability is marked broken",
			Evidence:     []string{"registry state: broken", "reason: " + entry.DirtyReason},
			RootCause:    "a previous verification or health check failed",
			Confidence:   "high",
			Component:    "capability",
			Fix:          "run the fix below, then reinstall if it persists",
			Verification: "grokinstall test " + name,
		})
	}

	// 2. Manifest presence and validity.
	if entry.ManifestPath == "" {
		add(Finding{
			Severity:     SeverityCritical,
			Symptom:      "capability has no manifest",
			Evidence:     []string{"registry entry has no manifest_path"},
			RootCause:    "the manifest was removed or never written",
			Confidence:   "high",
			Component:    "manifest",
			Fix:          "reinstall the capability",
			Verification: "grokinstall install " + entry.Source,
		})
		return rep, nil
	}
	if _, err := os.Stat(entry.ManifestPath); err != nil {
		add(Finding{
			Severity:     SeverityCritical,
			Symptom:      "capability manifest is missing",
			Evidence:     []string{"expected at " + entry.ManifestPath, "stat: " + err.Error()},
			RootCause:    "the manifest was removed after installation",
			Confidence:   "high",
			Component:    "manifest",
			Fix:          "reinstall the capability",
			Verification: "grokinstall install " + entry.Source,
		})
		return rep, nil
	}
	m, err := manifest.Load(entry.ManifestPath)
	if err != nil {
		add(Finding{
			Severity:     SeverityCritical,
			Symptom:      "capability manifest is corrupt",
			Evidence:     []string{entry.ManifestPath, err.Error()},
			RootCause:    "the manifest file is not valid GrokInstall JSON",
			Confidence:   "high",
			Component:    "manifest",
			Fix:          "reinstall the capability to rewrite the manifest",
			Verification: "grokinstall install " + entry.Source,
		})
		return rep, nil
	}
	if err := m.Validate(); err != nil {
		add(Finding{
			Severity:     SeverityHigh,
			Symptom:      "capability manifest is invalid",
			Evidence:     []string{entry.ManifestPath, err.Error()},
			RootCause:    "the manifest does not satisfy the schema",
			Confidence:   "high",
			Component:    "manifest",
			Fix:          "reinstall the capability",
			Verification: "grokinstall install " + entry.Source,
		})
	}

	// 3. Execution target.
	if m.Execution.Type == manifest.ExecutionSubprocess {
		e.diagnoseTarget(add, name, entry, m)
	}
	if m.Execution.Type == manifest.ExecutionBuiltin && entry.AdapterPath != "" {
		if _, err := os.Stat(entry.AdapterPath); err != nil {
			add(Finding{
				Severity:     SeverityCritical,
				Symptom:      "capability adapter is missing",
				Evidence:     []string{"expected at " + entry.AdapterPath, "stat: " + err.Error()},
				RootCause:    "the adapter directory was removed after installation",
				Confidence:   "high",
				Component:    "adapter",
				Fix:          "reinstall the capability to rebuild the adapter",
				Verification: "grokinstall test " + name,
			})
		}
	}

	// 4. Owned runtime integrity.
	if entry.RuntimeDir != "" {
		e.diagnoseRuntime(add, name, entry)
	}

	// 5. Receipt.
	if entry.ReceiptPath != "" {
		r, err := receipt.Load(entry.ReceiptPath)
		if err != nil {
			add(Finding{
				Severity:     SeverityMedium,
				Symptom:      "install receipt is unreadable",
				Evidence:     []string{entry.ReceiptPath, err.Error()},
				RootCause:    "the receipt file is corrupt",
				Confidence:   "high",
				Component:    "receipt",
				Fix:          "audit the capability; the receipt cannot be trusted",
				Verification: "grokinstall audit " + name,
			})
		} else if r.Capability != name {
			add(Finding{
				Severity:     SeverityMedium,
				Symptom:      "install receipt does not match the registry entry",
				Evidence:     []string{"receipt capability: " + r.Capability, "registry capability: " + name},
				RootCause:    "registry and receipt have diverged",
				Confidence:   "high",
				Component:    "receipt",
				Fix:          "reinstall the capability so receipt and registry agree",
				Verification: "grokinstall info " + name,
			})
		}
	} else {
		add(Finding{
			Severity:     SeverityLow,
			Symptom:      "install receipt is missing",
			Evidence:     []string{"registry entry has no receipt_path"},
			RootCause:    "the receipt was removed or never recorded",
			Confidence:   "high",
			Component:    "receipt",
			Fix:          "uninstall and reinstall to regenerate the receipt",
			Verification: "grokinstall audit " + name,
		})
	}

	sort.SliceStable(rep.Findings, func(i, j int) bool {
		return severityRank[rep.Findings[i].Severity] < severityRank[rep.Findings[j].Severity]
	})
	return rep, nil
}

func (e *Engine) diagnoseTarget(add func(Finding), name string, entry registry.Entry, m *manifest.Manifest) {
	target := m.Execution.Command
	info, err := os.Stat(target)
	if err != nil {
		root := "the executable is absent at the registered path"
		fix := "reinstall the capability to re-provision the executable"
		if entry.RuntimeDir == "" {
			fix = "provide the executable and reinstall with --command"
		}
		add(Finding{
			Severity:     SeverityCritical,
			Symptom:      name + " cannot execute",
			Evidence:     []string{"manifest target: " + target, "filesystem: target missing"},
			RootCause:    root,
			Confidence:   "high",
			Component:    "execution",
			Fix:          fix,
			Verification: "grokinstall test " + name,
		})
		return
	}
	if info.IsDir() {
		add(Finding{
			Severity:     SeverityCritical,
			Symptom:      "execution target is a directory",
			Evidence:     []string{"manifest target: " + target, "filesystem: target is a directory"},
			RootCause:    "the registered command points at a directory",
			Confidence:   "high",
			Component:    "execution",
			Fix:          "reinstall with the correct --command",
			Verification: "grokinstall test " + name,
		})
		return
	}
	if info.Mode()&0o111 == 0 {
		add(Finding{
			Severity:     SeverityHigh,
			Symptom:      "execution target is not executable",
			Evidence:     []string{"manifest target: " + target, "mode: " + info.Mode().String()},
			RootCause:    "the executable bit was removed",
			Confidence:   "high",
			Component:    "execution",
			Fix:          "chmod +x the target, or reinstall the capability",
			Verification: "grokinstall test " + name,
		})
	}
}

func (e *Engine) diagnoseRuntime(add func(Finding), name string, entry registry.Entry) {
	if _, err := os.Stat(entry.RuntimeDir); err != nil {
		add(Finding{
			Severity:     SeverityCritical,
			Symptom:      "provisioned runtime is missing",
			Evidence:     []string{"runtime directory: " + entry.RuntimeDir, "stat: " + err.Error()},
			RootCause:    "the GrokInstall-owned runtime was removed",
			Confidence:   "high",
			Component:    "runtime",
			Fix:          "reinstall the capability to re-provision the runtime",
			Verification: "grokinstall test " + name,
		})
		return
	}
	metaPath := filepath.Join(entry.RuntimeDir, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		add(Finding{
			Severity:     SeverityMedium,
			Symptom:      "runtime metadata is missing",
			Evidence:     []string{"expected at " + metaPath},
			RootCause:    "the runtime was created without metadata or metadata was removed",
			Confidence:   "high",
			Component:    "runtime",
			Fix:          "reinstall the capability to regenerate runtime metadata",
			Verification: "grokinstall audit " + name,
		})
		return
	}
	var meta provision.Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		add(Finding{
			Severity:     SeverityMedium,
			Symptom:      "runtime metadata is corrupt",
			Evidence:     []string{metaPath, err.Error()},
			RootCause:    "metadata.json is not valid runtime metadata",
			Confidence:   "high",
			Component:    "runtime",
			Fix:          "reinstall the capability",
			Verification: "grokinstall audit " + name,
		})
		return
	}
	if meta.Executable == "" {
		add(Finding{
			Severity:     SeverityMedium,
			Symptom:      "runtime metadata does not record an executable",
			Evidence:     []string{metaPath, "metadata.executable is empty"},
			RootCause:    "runtime metadata is incomplete",
			Confidence:   "high",
			Component:    "runtime",
			Fix:          "reinstall the capability",
			Verification: "grokinstall audit " + name,
		})
	}
	if modified := modifiedRuntimeFiles(entry.RuntimeDir, meta); len(modified) > 0 {
		add(Finding{
			Severity:     SeverityCritical,
			Symptom:      "provisioned runtime was modified after installation",
			Evidence:     append([]string{"runtime: " + entry.RuntimeDir}, modified...),
			RootCause:    "files no longer match the hashes recorded at provisioning time",
			Confidence:   "high",
			Component:    "runtime",
			Fix:          "reinstall the capability to restore a verified runtime",
			Verification: "grokinstall audit " + name,
		})
	}
}

// modifiedRuntimeFiles compares recorded hashes against what is on disk.
func modifiedRuntimeFiles(runtimeDir string, meta provision.Metadata) []string {
	var out []string
	paths := make([]string, 0, len(meta.Checksums))
	for p := range meta.Checksums {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		full := filepath.Join(runtimeDir, rel)
		actual, err := fileHash(full)
		if err != nil {
			out = append(out, rel+": missing")
			continue
		}
		if actual != meta.Checksums[rel] {
			out = append(out, rel+": content changed")
		}
	}
	return out
}

// Render writes a human-readable diagnosis.
func (r *Report) Render(w io.Writer) {
	if r.Capability == "" {
		fmt.Fprintln(w, "No capability named.")
		return
	}
	if r.Healthy && len(r.Findings) == 0 {
		fmt.Fprintf(w, "%s: no problems found\n", r.Capability)
		return
	}
	for i, f := range r.Findings {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "[%s]\n", strings.ToUpper(string(f.Severity)))
		fmt.Fprintf(w, "SYMPTOM\n  %s\n", f.Symptom)
		fmt.Fprintln(w, "\nEVIDENCE")
		for _, e := range f.Evidence {
			fmt.Fprintf(w, "  %s\n", e)
		}
		fmt.Fprintf(w, "\nROOT CAUSE\n  %s\n", f.RootCause)
		fmt.Fprintf(w, "\nCONFIDENCE\n  %s\n", f.Confidence)
		fmt.Fprintf(w, "\nAFFECTED COMPONENT\n  %s\n", f.Component)
		fmt.Fprintf(w, "\nFIX\n  %s\n", f.Fix)
		fmt.Fprintf(w, "\nVERIFY\n  %s\n", f.Verification)
	}
}
