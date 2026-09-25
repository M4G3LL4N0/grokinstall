package diagnostics

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/M4G3LL4N0/grokinstall/internal/manifest"
	"github.com/M4G3LL4N0/grokinstall/internal/registry"
)

// Check names used by the state checks.
const (
	checkDirty     = "dirty_installs"
	checkBroken    = "broken_capabilities"
	checkOrphanRt  = "orphan_runtimes"
	checkOrphanMan = "orphan_manifests"
	checkIntegrity = "runtime_integrity"
	checkPerms     = "permissions"
)

// runStateChecks inspects the installed capabilities and the state directory for
// problems that a user must act on.
func runStateChecks(reg *registry.Registry, rep *Report) {
	add := func(c Check) {
		rep.Checks = append(rep.Checks, c)
		switch c.Status {
		case StatusOK:
			rep.Summary.OK++
		case StatusWarn:
			rep.Summary.Warn++
		case StatusFail:
			rep.Summary.Fail++
			rep.Healthy = false
		}
	}

	entries, err := reg.List()
	if err != nil {
		add(Check{Name: "capability_registry", Status: StatusFail, Required: true,
			Detail: err.Error(), Action: "repair or delete " + reg.RegistryPath()})
		return
	}
	if len(entries) == 0 {
		add(Check{Name: "capabilities", Status: StatusOK, Required: true,
			Detail: "no capabilities installed"})
	}

	var dirty, broken []string
	knownManifests := map[string]bool{}
	knownRuntimes := map[string]bool{}

	for _, e := range entries {
		switch e.State {
		case registry.StateDirty:
			dirty = append(dirty, e.Name+" ("+e.DirtyReason+")")
		case registry.StateBroken:
			broken = append(broken, e.Name+" ("+e.DirtyReason+")")
		}
		if e.ManifestPath != "" {
			knownManifests[filepath.Clean(e.ManifestPath)] = true
		}
		if e.RuntimeDir != "" {
			knownRuntimes[filepath.Clean(e.RuntimeDir)] = true
		}
	}

	switch {
	case len(dirty) > 0:
		add(Check{Name: checkDirty, Status: StatusFail, Required: true,
			Detail: fmt.Sprintf("%d install(s) left dirty state: %s", len(dirty), strings.Join(dirty, "; ")),
			Action: "uninstall and reinstall the affected capabilities",
		})
	default:
		add(Check{Name: checkDirty, Status: StatusOK, Required: true, Detail: "no dirty installs"})
	}

	switch {
	case len(broken) > 0:
		add(Check{Name: checkBroken, Status: StatusFail, Required: true,
			Detail: fmt.Sprintf("%d capability(ies) are broken: %s", len(broken), strings.Join(broken, "; ")),
			Action: "run grokinstall diagnose for details, then repair or reinstall",
		})
	default:
		add(Check{Name: checkBroken, Status: StatusOK, Required: true, Detail: "no broken capabilities"})
	}

	// Missing or modified executables are reported per capability.
	var missingTargets, modified []string
	for _, e := range entries {
		if e.ManifestPath == "" {
			continue
		}
		m, mErr := manifest.Load(e.ManifestPath)
		if mErr != nil {
			missingTargets = append(missingTargets, e.Name+" (manifest unreadable)")
			continue
		}
		if m.Execution.Type == manifest.ExecutionSubprocess && m.Execution.Command != "" {
			if _, sErr := os.Stat(m.Execution.Command); sErr != nil {
				missingTargets = append(missingTargets, e.Name+" ("+m.Execution.Command+" missing)")
			}
		}
		if e.RuntimeDir != "" && runtimeModified(e.RuntimeDir) {
			modified = append(modified, e.Name)
		}
	}
	switch {
	case len(missingTargets) > 0:
		add(Check{Name: "capability_targets", Status: StatusFail, Required: true,
			Detail: strings.Join(missingTargets, "; "),
			Action: "run grokinstall diagnose for each capability, then reinstall"})
	default:
		add(Check{Name: "capability_targets", Status: StatusOK, Required: true, Detail: "all capability targets are present"})
	}
	switch {
	case len(modified) > 0:
		add(Check{Name: checkIntegrity, Status: StatusFail, Required: true,
			Detail: "runtime files changed since provisioning: " + strings.Join(modified, "; "),
			Action: "run grokinstall audit for details; reinstall if the change was not intentional"})
	default:
		add(Check{Name: checkIntegrity, Status: StatusOK, Required: true, Detail: "runtime integrity verified"})
	}

	// Orphans: files GrokInstall owns that no capability claims.
	orphanRuns := orphans(reg.RuntimesDir(), knownRuntimes)
	switch {
	case len(orphanRuns) > 0:
		add(Check{Name: checkOrphanRt, Status: StatusWarn, Required: false,
			Detail: "runtime directories with no registered capability: " + strings.Join(orphanRuns, ", "),
			Action: "remove them manually if no capability needs them"})
	default:
		add(Check{Name: checkOrphanRt, Status: StatusOK, Required: false, Detail: "no orphan runtimes"})
	}
	orphanMans := orphans(reg.ManifestsDir(), knownManifests)
	switch {
	case len(orphanMans) > 0:
		add(Check{Name: checkOrphanMan, Status: StatusWarn, Required: false,
			Detail: "manifests with no registry entry: " + strings.Join(orphanMans, ", "),
			Action: "remove them manually, or reinstall the capability"})
	default:
		add(Check{Name: checkOrphanMan, Status: StatusOK, Required: false, Detail: "no orphan manifests"})
	}

	// Corrupt receipts.
	var corruptReceipts []string
	receiptFiles, rErr := os.ReadDir(reg.ReceiptsDir())
	if rErr == nil {
		for _, f := range receiptFiles {
			if !strings.HasSuffix(f.Name(), ".json") {
				continue
			}
			data, readErr := os.ReadFile(filepath.Join(reg.ReceiptsDir(), f.Name()))
			if readErr != nil {
				corruptReceipts = append(corruptReceipts, f.Name())
				continue
			}
			var probe map[string]any
			if json.Unmarshal(data, &probe) != nil {
				corruptReceipts = append(corruptReceipts, f.Name())
			}
		}
	}
	switch {
	case len(corruptReceipts) > 0:
		add(Check{Name: "receipts", Status: StatusWarn, Required: false,
			Detail: "unreadable receipts: " + strings.Join(corruptReceipts, ", "),
			Action: "these receipts cannot be trusted for uninstall or audit"})
	default:
		add(Check{Name: "receipts", Status: StatusOK, Required: false, Detail: "all receipts are readable"})
	}

	add(checkPermissions(reg))
}

func orphans(dir string, known map[string]bool) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if !known[filepath.Clean(filepath.Join(dir, e.Name()))] {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}

// runtimeModified reports whether a runtime file no longer matches its
// recorded hash.
func runtimeModified(runtimeDir string) bool {
	data, err := os.ReadFile(filepath.Join(runtimeDir, "metadata.json"))
	if err != nil {
		return true
	}
	var meta struct {
		Checksums map[string]string `json:"checksums"`
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return true
	}
	if len(meta.Checksums) == 0 {
		return false
	}
	for rel, want := range meta.Checksums {
		got, hErr := hashFile(filepath.Join(runtimeDir, rel))
		if hErr != nil || got != want {
			return true
		}
	}
	return false
}

func checkPermissions(reg *registry.Registry) Check {
	if info, err := os.Stat(reg.Root); err == nil && info.Mode()&0o022 != 0 {
		return Check{Name: checkPerms, Status: StatusWarn, Required: false,
			Detail: "state directory is writable by others: " + reg.Root,
			Action: "chmod 700 " + reg.Root + " so only you can modify installed capabilities"}
	}
	return Check{Name: checkPerms, Status: StatusOK, Required: false, Detail: "state directory permissions are private"}
}
