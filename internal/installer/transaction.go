package installer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"grokinstall/internal/manifest"
	"grokinstall/internal/plan"
	"grokinstall/internal/provision"
	"grokinstall/internal/receipt"
	"grokinstall/internal/registry"
	"grokinstall/internal/source"
	"grokinstall/internal/strategy"
)

// ProvisionPolicy selects how much provisioning an install may perform.
type ProvisionPolicy = provision.Policy

// Transaction phases, in order. A failure before the commit phase must leave no
// trace beyond a failed receipt.
const (
	phasePlan      = "plan"
	phaseProvision = "provision_stage"
	phaseAdapter   = "adapter_stage"
	phaseManifest  = "manifest_stage"
	phaseVerify    = "verify"
	phaseCommit    = "commit_files"
	phaseRegister  = "register"
	phaseReceipt   = "write_receipt"
)

// transaction tracks staged work so a partial failure can be rolled back
// precisely rather than guessed at.
type transaction struct {
	phases      []string
	committed   []string // files moved into final state
	registryDir string
	// registeredName is set once the registry entry exists.
	registeredName string
}

func (t *transaction) advance(phase string) { t.phases = append(t.phases, phase) }

// rollback removes everything this transaction committed. It reports whether
// the rollback could prove it restored the previous state.
func (t *transaction) rollback() (complete bool, leftovers []string) {
	complete = true
	for _, p := range t.committed {
		if err := os.RemoveAll(p); err != nil {
			complete = false
			leftovers = append(leftovers, p)
		} else if _, err := os.Stat(p); err == nil {
			complete = false
			leftovers = append(leftovers, p)
		}
	}
	return complete, leftovers
}

// provisioningNeed builds the provisioning request for a source. The required
// command name falls back to the repository name when no entrypoint was
// declared, because a release artifact is usually named after the project.
func provisioningNeed(src source.Source, entrypoints []string, projectName, command string) (provision.Need, bool) {
	need := provision.Need{PreferExisting: command}
	switch {
	case command != "":
		need.Name = command
	case projectName != "":
		// The project name is the reliable signal. A monorepo's nested example
		// packages can declare unrelated console scripts, and provisioning one
		// of those would be a guess.
		need.Name = projectName
	case src.Repo != "":
		need.Name = src.Repo
	case len(entrypoints) > 0:
		need.Name = entrypoints[0]
	}
	if need.Name == "" {
		need.Name = "tool"
	}
	if src.Kind == source.KindGitHub {
		need.Owner = src.Owner
		need.Repo = src.Repo
		return need, true
	}
	if src.LocalPath != "" {
		need.SourceDir = src.LocalPath
	}
	return need, true
}

// ecosystemFromEvidence names the packaging ecosystem an inspection found.
func ecosystemFromEvidence(built *plan.Plan) string {
	for _, ev := range built.Evidence {
		switch ev.Finding {
		case "python_project":
			return "python"
		case "javascript_project":
			return "npm"
		case "rust_project":
			return "cargo"
		case "go_project":
			return "go"
		}
	}
	return ""
}

// packageEcosystem reports the package ecosystem a source declares, so
// provisioning can offer an accurate alternative instead of saying "unresolved".
func packageEcosystem(localPath string) string {
	if localPath == "" {
		return ""
	}
	has := func(name string) bool {
		_, err := os.Stat(filepath.Join(localPath, name))
		return err == nil
	}
	switch {
	case has("pyproject.toml") || has("setup.py") || has("requirements.txt"):
		return "python"
	case has("package.json"):
		return "npm"
	case has("Cargo.toml"):
		return "cargo"
	}
	return ""
}

// provisionExecutable satisfies an execution requirement under the policy. It
// returns a structured refusal instead of falling back to an unsafe route.
func (ins *Installer) provisionExecutable(ctx context.Context, need provision.Need, policy ProvisionPolicy, stageDir string) (*provision.Result, *provision.Refusal, error) {
	engine := &provision.Engine{
		RuntimesDir:      ins.Registry.RuntimesDir(),
		Policy:           policy,
		GitHub:           ins.GitHub,
		AllowSourceBuild: policy.GrantsOne(provision.AuthSourceBuild),
		WorkDir:          stageDir,
	}
	candidates := engine.Discover(ctx, need)
	selected, refusal := provision.Select(candidates, policy)
	if refusal != nil {
		refusal.Executable = need.Name
		ins.LastCandidates = candidates
		return nil, refusal, nil
	}
	ins.LastCandidates = candidates
	ins.LastSelected = selected

	result, err := engine.Perform(ctx, selected, need, filepath.Join(stageDir, "provision"))
	if err != nil {
		var r *provision.Refusal
		if errors.As(err, &r) {
			r.Executable = need.Name
			return nil, r, nil
		}
		return nil, nil, err
	}
	return result, nil, nil
}

// commitRuntime moves a provisioned runtime into its final owned location.
func commitRuntime(reg *registry.Registry, capabilityName string, result *provision.Result) (string, provision.Metadata, error) {
	return provision.Commit(reg, capabilityName, result)
}

// provisionReceipt converts a provisioning result into receipt data.
func provisionReceipt(result *provision.Result, approvals []provision.Authorization) *receipt.Provisioning {
	if result == nil {
		return nil
	}
	p := &receipt.Provisioning{
		Method:            string(result.Method),
		ArtifactSource:    result.Source,
		ArtifactVersion:   result.Version,
		Asset:             result.Asset,
		PublishedChecksum: result.PublishedChecksum,
		ActualChecksum:    result.ActualChecksum,
		ChecksumStatus:    result.ChecksumStatus,
		BuildCommand:      result.BuildCommand,
		RuntimeDir:        result.RuntimeDir,
		PackageMutations:  result.PackageMutation,
		Notes:             result.Notes,
	}
	for _, a := range approvals {
		p.Approvals = append(p.Approvals, string(a))
	}
	if p.Notes == nil {
		p.Notes = []string{}
	}
	return p
}

// applyApprovals copies the policy's explicit approvals into a result so the
// receipt records exactly what was authorized.
func applyApprovals(result *provision.Result, policy ProvisionPolicy) []provision.Authorization {
	var used []provision.Authorization
	for _, a := range policy.Allow {
		used = append(used, a)
	}
	if result != nil {
		result.Approvals = used
	}
	return used
}

// installPlanOnly records a plan without registering a capability. Part 3 makes
// this the only way a non-runnable strategy is persisted.
func (ins *Installer) installPlanOnly(res *Result, chosen strategy.ID, support manifest.Support, explanation string) error {
	planID := res.InstallID
	_, err := ins.Registry.SavePlan(registry.PlanEntry{
		PlanID:   planID,
		Source:   res.Source,
		Goal:     res.Goal,
		Strategy: chosen,
		Support:  string(support),
		State:    registry.StatePlanOnly,
		Reason:   explanation,
	})
	if err != nil {
		return err
	}
	res.Result = receipt.ResultPlanned
	res.PlanPath = filepath.Join(ins.Registry.PlansDir(), planID+".json")
	res.Explanation = explanation
	return nil
}

// now formats the current time consistently across receipts and entries.
func now() string { return time.Now().UTC().Format(time.RFC3339) }

// readRuntimeExecutable resolves the executable inside an owned runtime using
// the path recorded in its metadata, which is authoritative. The capability name
// and the binary name often differ, so guessing would produce a manifest that
// points at a staging path.
func readRuntimeExecutable(runtimeDir, name string) (string, bool) {
	if data, err := os.ReadFile(filepath.Join(runtimeDir, "metadata.json")); err == nil {
		var meta provision.Metadata
		if json.Unmarshal(data, &meta) == nil && meta.Executable != "" {
			candidate := filepath.Join(runtimeDir, meta.Executable)
			if info, sErr := os.Stat(candidate); sErr == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
				return candidate, true
			}
		}
	}
	candidates := []string{
		filepath.Join(runtimeDir, "bin", name),
		filepath.Join(runtimeDir, name),
	}
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return c, true
		}
	}
	return "", false
}

// verifyRuntime checks that an owned runtime is intact before it is used.
func verifyRuntime(runtimeDir string) receipt.Verification {
	v := receipt.Verification{Performed: true}
	metaPath := filepath.Join(runtimeDir, "metadata.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		v.Checks = append(v.Checks, receipt.Check{Name: "runtime metadata present", Detail: err.Error()})
		return v
	}
	var meta provision.Metadata
	if err := json.Unmarshal(data, &meta); err != nil {
		v.Checks = append(v.Checks, receipt.Check{Name: "runtime metadata parses", Detail: err.Error()})
		return v
	}
	exe := filepath.Join(runtimeDir, meta.Executable)
	info, err := os.Stat(exe)
	switch {
	case err != nil:
		v.Checks = append(v.Checks, receipt.Check{Name: "runtime executable present", Detail: err.Error()})
	case info.Mode()&0o111 == 0:
		v.Checks = append(v.Checks, receipt.Check{Name: "runtime executable is executable", Detail: info.Mode().String()})
	default:
		v.Checks = append(v.Checks, receipt.Check{Name: "runtime executable present", Detail: meta.Executable})
	}
	v.Passed = true
	for _, c := range v.Checks {
		if c.Detail == "" {
			v.Passed = false
		}
	}
	if len(v.Checks) == 0 {
		v.Passed = false
	}
	return v
}

// markDirty records an installation whose rollback could not be proven.
func (ins *Installer) markDirty(name, reason string) error {
	return ins.Registry.SetState(name, registry.StateDirty, reason)
}

// sharedRuntimeOwners reports which capabilities claim a runtime directory, so
// uninstall never removes a runtime another capability still uses.
func (ins *Installer) sharedRuntimeOwners(runtimeDir string) []string {
	entries, err := ins.Registry.List()
	if err != nil {
		return nil
	}
	var owners []string
	abs, _ := filepath.Abs(runtimeDir)
	for _, e := range entries {
		if e.RuntimeDir == "" {
			continue
		}
		other, _ := filepath.Abs(e.RuntimeDir)
		if other == abs {
			owners = append(owners, e.Name)
		}
	}
	return owners
}

func describeCandidates(candidates []provision.Candidate) string {
	var b strings.Builder
	for i, c := range candidates {
		if i > 0 {
			b.WriteString("; ")
		}
		fmt.Fprintf(&b, "%s: %s", c.Method, c.Summary())
	}
	return b.String()
}
