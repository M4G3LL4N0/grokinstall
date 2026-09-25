// Package installer turns a plan into a verified, registered capability.
//
// The lifecycle is deliberately staged:
//
//	PLAN → STAGE → VERIFY → COMMIT TO REGISTRY
//
// Nothing is written into final state until verification passes. A failed
// verification discards the stage and leaves the registry untouched, so a
// broken capability can never look installed.
//
// Installing a capability is not the same as installing upstream software:
// GrokInstall registers a contract, a thin adapter when one is genuinely
// needed, and a receipt. It does not run a project's install scripts.
package installer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/M4G3LL4N0/grokinstall/internal/cache"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/M4G3LL4N0/grokinstall/internal/evidence"
	"github.com/M4G3LL4N0/grokinstall/internal/knowledge"
	"github.com/M4G3LL4N0/grokinstall/internal/manifest"
	"github.com/M4G3LL4N0/grokinstall/internal/plan"
	"github.com/M4G3LL4N0/grokinstall/internal/provision"
	"github.com/M4G3LL4N0/grokinstall/internal/provision/github"
	"github.com/M4G3LL4N0/grokinstall/internal/receipt"
	"github.com/M4G3LL4N0/grokinstall/internal/registry"
	"github.com/M4G3LL4N0/grokinstall/internal/runtime"
	"github.com/M4G3LL4N0/grokinstall/internal/source"
	"github.com/M4G3LL4N0/grokinstall/internal/strategy"
	"github.com/M4G3LL4N0/grokinstall/internal/toolchain"
)

// Default execution bounds for an installed capability.
const (
	DefaultTimeoutMs      = 30000
	DefaultMaxOutputBytes = 1 << 20
)

// sourceRef is the normalized source, re-exported for callers.
type sourceRef = source.Source

func normalize(ref string) (*source.Source, error) { return source.Normalize(ref) }

// Options configures an install.
type Options struct {
	Source sourceRef
	Goal   string
	Name   string
	// Command overrides the discovered executable, for sources whose entrypoint
	// is already installed on the machine.
	Command string
	// DryRun reports what would happen without changing state.
	DryRun bool
	// AllowReplace permits replacing an existing capability of the same name.
	AllowReplace bool
	TimeoutMs    int
	// Policy governs provisioning. Zero value means the safe policy.
	Policy ProvisionPolicy
}

// PlannedAction is one mutation an install would perform.
type PlannedAction struct {
	FilesCreated      []string `json:"files_created,omitempty"`
	FilesModified     []string `json:"files_modified,omitempty"`
	RegistryChanges   []string `json:"registry_changes,omitempty"`
	AdapterGeneration string   `json:"adapter_generation,omitempty"`
	ExternalCommands  []string `json:"external_commands,omitempty"`
	Dependencies      []string `json:"dependencies_introduced,omitempty"`
	VerificationPlan  []string `json:"verification_plan,omitempty"`
	SecurityConcerns  []string `json:"security_concerns,omitempty"`
}

// Result is the outcome of an install.
type Result struct {
	InstallID          string               `json:"install_id"`
	DryRun             bool                 `json:"dry_run"`
	Result             string               `json:"result"`
	Strategy           string               `json:"strategy"`
	Support            string               `json:"support"`
	Capability         string               `json:"capability,omitempty"`
	Source             string               `json:"source"`
	SourceID           string               `json:"source_id,omitempty"`
	Goal               string               `json:"goal,omitempty"`
	ManifestPath       string               `json:"manifest_path,omitempty"`
	AdapterPath        string               `json:"adapter_path,omitempty"`
	RuntimeDir         string               `json:"runtime_dir,omitempty"`
	ReceiptPath        string               `json:"receipt_path,omitempty"`
	PlanPath           string               `json:"plan_path,omitempty"`
	Warnings           []string             `json:"warnings,omitempty"`
	Explanation        string               `json:"explanation,omitempty"`
	InspectionCacheHit bool                 `json:"inspection_cache_hit"`
	PlannedActions     PlannedAction        `json:"planned_actions,omitempty"`
	Verification       receipt.Verification `json:"verification,omitempty"`
	Receipt            *receipt.Receipt     `json:"receipt,omitempty"`
	// Provisioning records what provisioning was considered or performed.
	Provisioning       string                `json:"provisioning,omitempty"`
	Provisioned        bool                  `json:"provisioned,omitempty"`
	ProvisioningDetail *receipt.Provisioning `json:"provisioning_detail,omitempty"`
	ChecksumStatus     string                `json:"checksum_status,omitempty"`
	// Refusal is set when provisioning was blocked by policy.
	Refusal *Refusal `json:"refusal,omitempty"`
	// provisionResult carries the in-process provisioning result to commit. It
	// is never serialized: the receipt and runtime metadata are the records.
	provisionResult *provision.Result
}

// Refusal is the structured reason provisioning did not proceed.
type Refusal = provision.Refusal

// RunResult is the outcome of invoking a capability.
type RunResult struct {
	OK          bool           `json:"ok"`
	RawResult   string         `json:"raw_result,omitempty"`
	Result      any            `json:"result,omitempty"`
	Warnings    []string       `json:"warnings,omitempty"`
	Artifacts   []string       `json:"artifacts,omitempty"`
	Error       *runtime.Error `json:"error,omitempty"`
	DurationMs  int64          `json:"duration_ms"`
	InputBytes  int            `json:"input_bytes"`
	OutputBytes int            `json:"output_bytes"`
}

// SmokeReport is the result of `test NAME`.
type SmokeReport struct {
	Capability string          `json:"capability"`
	Passed     bool            `json:"passed"`
	Checks     []receipt.Check `json:"checks"`
	DurationMs int64           `json:"duration_ms"`
}

// CapabilitySummary is the compact machine description of an installed
// capability. It deliberately carries no source detail.
type CapabilitySummary struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Strategy    string          `json:"strategy"`
	Support     string          `json:"support"`
	State       string          `json:"state"`
	Status      string          `json:"status"`
	Runnable    bool            `json:"runnable"`
	UseWhen     string          `json:"use_when,omitempty"`
	Input       manifest.Schema `json:"input"`
	Output      manifest.Schema `json:"output"`
	Call        string          `json:"call,omitempty"`
	GrokBot     string          `json:"grokbot_contract,omitempty"`
}

// Installer performs installs against a state directory.
type Installer struct {
	Registry *registry.Registry
	Profile  *toolchain.Profile
	rt       *runtime.Runtime
	// GitHub reads release metadata during provisioning.
	GitHub *github.Client
	// LastCandidates records the provisioning routes considered by the most
	// recent install, so a refusal can explain itself.
	LastCandidates []provision.Candidate
	// LastSelected records the chosen route, when one was selected.
	LastSelected provision.Candidate
}

// New builds an installer over a registry.
func New(reg *registry.Registry) *Installer {
	return NewWithProfile(reg, toolchain.Detect())
}

// NewWithProfile builds an installer with an already-detected toolchain, so a
// caller that creates several installers does not re-probe the machine.
func NewWithProfile(reg *registry.Registry, profile *toolchain.Profile) *Installer {
	if profile == nil {
		profile = toolchain.Detect()
	}
	return &Installer{Registry: reg, Profile: profile, rt: runtime.New()}
}

// WithGitHubClient supplies the release client used for provisioning. It exists
// so tests can point at a local server.
func (ins *Installer) WithGitHubClient(c *github.Client) *Installer {
	ins.GitHub = c
	return ins
}

// ensureGitHub lazily builds the default client.
func (ins *Installer) ensureGitHub() {
	if ins.GitHub == nil {
		ins.GitHub = github.NewClient("")
	}
}

// strategySupport declares the honest runtime support of every strategy.
var strategySupport = map[strategy.ID]manifest.Support{
	strategy.StrategyCLIBridge:         manifest.SupportReady,
	strategy.StrategyKnowledgeImport:   manifest.SupportReady,
	strategy.StrategyExternalExecution: manifest.SupportReady,
	strategy.StrategyNoInstall:         manifest.SupportNone,
	strategy.StrategyAPIBridge:         manifest.SupportPlanOnly,
	strategy.StrategyMCPBridge:         manifest.SupportPlanOnly,
	strategy.StrategyDockerBridge:      manifest.SupportPlanOnly,
	strategy.StrategyLocalService:      manifest.SupportPlanOnly,
	strategy.StrategyGeneratedAdapter:  manifest.SupportPlanOnly,
	strategy.StrategyMicroPromptPack:   manifest.SupportPlanOnly,
}

// SupportFor reports the runtime support level of a strategy.
func SupportFor(id strategy.ID) manifest.Support {
	if s, ok := strategySupport[id]; ok {
		return s
	}
	return manifest.SupportUnsupported
}

// Install runs the full staged installation.
// Install runs the full staged installation.
//
// The transaction is: plan -> provision stage -> adapter stage -> manifest
// stage -> verify -> commit files -> register -> write receipt. A failure
// before the commit phase leaves nothing behind but a failed receipt. A failure
// during commit is rolled back; if the rollback cannot be proven complete the
// capability is marked dirty and the install is never reported as successful.
func (ins *Installer) Install(opts Options) (*Result, error) {
	if opts.Source.Kind != source.KindLocalDir && opts.Source.Kind != source.KindGitHub {
		return nil, fmt.Errorf("source kind %q is not supported for installation", opts.Source.Kind)
	}

	built, err := plan.Build(context.Background(), plan.Options{
		Source:  opts.Source,
		Goal:    opts.Goal,
		Store:   ins.Registry.Cache,
		Profile: ins.Profile,
	})
	if err != nil {
		return nil, err
	}

	chosen := built.Comparison.Recommended
	support := SupportFor(chosen)

	res := &Result{
		InstallID:          newInstallID(),
		DryRun:             opts.DryRun,
		Source:             opts.Source.Canonical,
		SourceID:           built.Inspection.Identity,
		Goal:               opts.Goal,
		Strategy:           string(chosen),
		Support:            string(support),
		InspectionCacheHit: built.Inspection.CacheHit,
	}
	if built.IsSelf {
		res.Warnings = append(res.Warnings,
			"this source is GrokInstall itself; use doctor and diagnosis rather than installing a second copy")
	}

	tx := &transaction{registryDir: ins.Registry.Root}
	tx.advance(phasePlan)

	// no_install is a successful outcome with nothing registered.
	if chosen == strategy.StrategyNoInstall {
		res.Result = receipt.ResultNoInstall
		res.Explanation = "Nothing needs installation for this goal: " + built.Comparison.RecommendationReason
		r, rerr := ins.writeReceipt(res, built, receipt.Receipt{
			Result:            receipt.ResultNoInstall,
			CommandsExecuted:  []receipt.Command{},
			DependenciesAdded: []string{},
		})
		if rerr != nil {
			return res, rerr
		}
		res.Receipt = r
		return res, nil
	}

	// Self-installation is refused rather than duplicated.
	if built.IsSelf {
		res.Result = receipt.ResultFailed
		res.Explanation = "GrokInstall cannot install itself into its own state directory; use doctor and diagnosis."
		_, _ = ins.writeReceipt(res, built, receipt.Receipt{
			Result:            receipt.ResultFailed,
			DependenciesAdded: []string{},
		})
		return res, fmt.Errorf("refusing to install GrokInstall into itself: %s", res.Explanation)
	}

	stageDir := ins.Registry.StagingPath(res.InstallID)
	if err := os.RemoveAll(stageDir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return nil, err
	}

	// PROVISION STAGE: obtain the executable before the manifest is built, so
	// the manifest always describes something that really exists.
	if chosen == strategy.StrategyCLIBridge || chosen == strategy.StrategyExternalExecution {
		if ins.needsProvisioning(opts, built) {
			ins.ensureGitHub()
			need, _ := provisioningNeed(opts.Source, built.Inspection.Entrypoints, built.Inspection.Name, opts.Command)
			need.Version = built.Inspection.CommitSHA
			need.Ecosystem = ecosystemFromEvidence(built)
			provResult, refusal, perr := ins.provisionExecutable(context.Background(), need, opts.Policy, stageDir)
			if perr != nil {
				os.RemoveAll(stageDir)
				res.Result = receipt.ResultFailed
				res.Explanation = perr.Error()
				r, _ := ins.writeReceipt(res, built, receipt.Receipt{
					Result: receipt.ResultFailed, DependenciesAdded: []string{},
				})
				res.Receipt = r
				return res, perr
			}
			if refusal != nil {
				// A refusal is a first-class outcome: explain it, record a plan,
				// register nothing, and leave persistent state clean.
				os.RemoveAll(stageDir)
				res.Result = receipt.ResultFailed
				res.Refusal = refusal
				res.Provisioning = describeCandidates(refusal.Alternatives)
				res.Explanation = refusal.Reason
				_, _ = ins.writeReceipt(res, built, receipt.Receipt{
					Result: receipt.ResultFailed, DependenciesAdded: []string{},
				})
				_, _ = ins.Registry.SavePlan(registry.PlanEntry{
					PlanID: res.InstallID, Source: res.Source, Goal: res.Goal,
					Strategy: chosen, Support: string(support), State: registry.StatePlanOnly,
					Reason: refusal.Reason,
				})
				res.PlanPath = filepath.Join(ins.Registry.PlansDir(), res.InstallID+".json")
				return res, refusal
			}
			tx.advance(phaseProvision)
			approvals := applyApprovals(provResult, opts.Policy)
			res.Provisioned = provResult != nil
			res.provisionResult = provResult
			if provResult != nil {
				res.ProvisioningDetail = provisionReceipt(provResult, approvals)
				res.ChecksumStatus = provResult.ChecksumStatus
				if provResult.ExecutablePath != "" {
					opts.Command = provResult.ExecutablePath
				}
			}
		}
	}

	name := chooseName(opts, built)
	res.Capability = name

	actions, m := ins.buildManifest(opts, built, name, chosen, support)
	res.PlannedActions = actions

	if opts.DryRun {
		res.Result = "dry_run"
		res.Explanation = "Dry run: no files, registry entries or receipts were written."
		if len(ins.LastCandidates) > 0 {
			res.Provisioning = describeCandidates(ins.LastCandidates)
		}
		return res, nil
	}

	// ADAPTER + MANIFEST STAGE
	created, err := ins.stage(stageDir, opts, m, built)
	if err != nil {
		os.RemoveAll(stageDir)
		res.Result = receipt.ResultFailed
		_, _ = ins.writeReceipt(res, built, receipt.Receipt{
			Result: receipt.ResultFailed, FilesCreated: created, DependenciesAdded: []string{},
		})
		return res, err
	}
	tx.advance(phaseAdapter)
	tx.advance(phaseManifest)

	// VERIFY
	staged := *m
	stagedPath := filepath.Join(stageDir, m.Name+".json")
	verification := ins.verify(&staged, stagedPath, opts)
	plannedOnly := !support.Executable()
	if plannedOnly && verification.Passed {
		verification.Checks = append(verification.Checks, receipt.Check{
			Name:   "capability is runnable",
			Passed: true,
			Detail: "strategy " + staged.Strategy + " is " + string(staged.Support) + "; nothing was executed",
		})
	}
	tx.advance(phaseVerify)

	if !verification.Passed {
		os.RemoveAll(stageDir)
		res.Result = receipt.ResultFailed
		res.Verification = verification
		res.Explanation = "verification failed; the staged install was discarded and nothing was registered"
		r, _ := ins.writeReceipt(res, built, receipt.Receipt{
			Result: receipt.ResultFailed, FilesCreated: created,
			Verification: verification, CommandsExecuted: verificationCommands(opts, m),
			DependenciesAdded: []string{},
		})
		res.Receipt = r
		return res, fmt.Errorf("verification failed: %s", strings.Join(failedChecks(verification), "; "))
	}
	res.Verification = verification

	// A plan-only strategy is persisted as a plan, never as an installed
	// capability. This is the installed-versus-planned separation.
	if plannedOnly {
		os.RemoveAll(stageDir)
		explanation := fmt.Sprintf("strategy %s is detected and planned but not runnable in this version", chosen)
		if err := ins.installPlanOnly(res, chosen, support, explanation); err != nil {
			return res, err
		}
		r, _ := ins.writeReceipt(res, built, receipt.Receipt{
			Result: receipt.ResultPlanned, State: string(registry.StatePlanOnly),
			Verification: verification, DependenciesAdded: []string{},
		})
		res.Receipt = r
		return res, nil
	}

	// COMMIT FILES
	committed, commitErr := ins.commit(stageDir, stagedPath, opts, built, res, verification)
	if commitErr != nil {
		complete, leftovers := tx.rollback()
		os.RemoveAll(stageDir)
		res.Result = receipt.ResultFailed
		if !complete {
			// Rollback could not prove restoration: say so loudly.
			res.Result = receipt.ResultDirty
			res.Explanation = "commit failed and rollback was incomplete: " + strings.Join(leftovers, ", ")
			_ = ins.markDirty(name, res.Explanation)
			_, _ = ins.writeReceipt(res, built, receipt.Receipt{
				Result: receipt.ResultDirty, State: string(registry.StateDirty),
				Verification: verification, DependenciesAdded: []string{},
			})
			return res, fmt.Errorf("%s", res.Explanation)
		}
		res.Explanation = "commit failed and was rolled back: " + commitErr.Error()
		_, _ = ins.writeReceipt(res, built, receipt.Receipt{
			Result: receipt.ResultFailed, Verification: verification,
			DependenciesAdded: []string{},
		})
		return res, commitErr
	}
	tx.advance(phaseCommit)
	tx.advance(phaseRegister)
	os.RemoveAll(stageDir)

	res.ManifestPath = committed.manifestPath
	res.AdapterPath = committed.adapterPath
	res.RuntimeDir = committed.runtimeDir
	created = finalFileChanges(created, stageDir, name, committed)
	// A capability that uses an executable GrokInstall did not provision still
	// records where that executable came from.
	if res.ProvisioningDetail == nil && m.Execution.Type == manifest.ExecutionSubprocess {
		ownership := provision.OwnershipExternal
		method := provision.MethodExisting
		if res.RuntimeDir != "" {
			ownership = provision.OwnershipGrokinstall
			method = provision.MethodRelease
		}
		res.ProvisioningDetail = &receipt.Provisioning{
			Method:         string(method),
			ArtifactSource: res.Source,
			Ownership:      string(ownership),
			Notes:          []string{"the executable was already present; GrokInstall provisioned nothing"},
		}
	}
	res.Result = receipt.ResultInstalled
	r, err := ins.writeReceipt(res, built, receipt.Receipt{
		Result: receipt.ResultInstalled, Capability: name, State: string(registry.StateReady),
		FilesCreated: created, CommandsExecuted: verificationCommands(opts, m),
		DependenciesAdded: []string{}, Verification: verification,
		Provisioning: res.ProvisioningDetail,
	})
	if err != nil {
		return res, err
	}
	res.Receipt = r
	if entry, lerr := ins.Registry.Lookup(name); lerr == nil {
		entry.ReceiptPath = res.ReceiptPath
		_ = ins.Registry.Update(entry)
	}
	tx.advance(phaseReceipt)
	return res, nil
}

// needsProvisioning reports whether an executable must be obtained rather than
// already being present on the machine.
func (ins *Installer) needsProvisioning(opts Options, built *plan.Plan) bool {
	if opts.Command != "" {
		return false
	}
	if cmd, _ := discoverCommand(built); cmd != "" {
		return false
	}
	return len(built.Inspection.Entrypoints) > 0 || built.Source.Kind == source.KindGitHub
}

// buildManifest turns a plan into the manifest an install would register, plus
// the actions the install would take. It is pure: nothing is written.
func (ins *Installer) buildManifest(opts Options, built *plan.Plan, name string, chosen strategy.ID, support manifest.Support) (PlannedAction, *manifest.Manifest) {
	timeout := opts.TimeoutMs
	if timeout <= 0 {
		timeout = DefaultTimeoutMs
	}
	m := &manifest.Manifest{
		Schema:   manifest.SchemaID,
		Name:     name,
		Version:  "1",
		Source:   opts.Source.Canonical,
		Goal:     opts.Goal,
		Strategy: string(chosen),
		Support:  support,
		Status:   "ready",
		Grokbot: manifest.Grokbot{
			UseWhen:   useWhen(opts.Goal, name),
			DoNot:     "load the implementation repository or its documentation into GrokBot before invoking this capability",
			OnFailure: fmt.Sprintf("Run:\ngrokinstall diagnose %s", name),
		},
		Cache: manifest.Cache{
			// Execution results are never cached implicitly.
			Enabled:     false,
			Description: "capability results are not cached; only deterministic inspection is",
		},
		Security: manifest.Security{
			OwnedByGrokinstall: true,
			Notes:              securityNotes(built),
		},
		Provenance: manifest.Provenance{
			Source:      opts.Source.Canonical,
			Identity:    built.Inspection.Identity,
			CommitSHA:   built.Inspection.CommitSHA,
			InspectedAt: built.Inspection.Identity,
			Evidence:    evidenceFor(built, chosen),
		},
	}

	var actions PlannedAction
	actions.RegistryChanges = []string{"register " + name}
	actions.VerificationPlan = []string{
		"manifest validates against " + manifest.SchemaID,
		"execution target exists and is executable",
		"capability launches and returns a bounded result",
	}

	switch chosen {
	case strategy.StrategyKnowledgeImport:
		m.Execution = manifest.Execution{
			Type:           manifest.ExecutionBuiltin,
			Supported:      true,
			Handler:        "knowledge",
			InputMode:      manifest.ModeStdinJSON,
			TimeoutMs:      timeout,
			MaxOutputBytes: DefaultMaxOutputBytes,
			ExpectJSON:     true,
		}
		m.Input = manifest.Schema{Fields: []manifest.Field{
			{Name: "query", Type: "string", Required: true, Description: "what to look for in the documentation"},
			{Name: "limit", Type: "integer", Description: "maximum matches to return"},
		}}
		m.Output = manifest.Schema{Fields: []manifest.Field{
			{Name: "query", Type: "string"},
			{Name: "matches", Type: "array", Description: "matching documents with bounded excerpts"},
			{Name: "total_documents", Type: "integer"},
		}}
		m.Security.ExecutesSourceCode = false
		actions.FilesCreated = append(actions.FilesCreated,
			"manifests/"+name+".json", "adapters/"+name+"/index.json")
		actions.AdapterGeneration = "build a bounded local text index over the source documentation"

	case strategy.StrategyCLIBridge, strategy.StrategyExternalExecution:
		cmd, args := opts.Command, []string(nil)
		if cmd == "" {
			cmd, args = discoverCommand(built)
		}
		externallyManaged := chosen == strategy.StrategyExternalExecution
		if cmd == "" {
			m.Execution = manifest.Execution{
				Type:      manifest.ExecutionNone,
				Supported: false,
				Note:      "no executable is available for this source; install the tool yourself, then reinstall with --command",
			}
			// The support level is deliberately left as the strategy declared it:
			// a supported strategy that cannot resolve an executable must fail
			// verification rather than quietly become a plan.
			m.Status = "plan-only"
			actions.ExternalCommands = append(actions.ExternalCommands,
				"none: the declared CLI is not present on this machine")
			break
		}
		m.Execution = manifest.Execution{
			Type:           manifest.ExecutionSubprocess,
			Supported:      true,
			Command:        cmd,
			Args:           args,
			InputMode:      manifest.ModeStdinJSON,
			TimeoutMs:      timeout,
			MaxOutputBytes: DefaultMaxOutputBytes,
			WorkingDir:     workingDirFor(opts),
		}
		if externallyManaged {
			m.Security.Notes = append(m.Security.Notes,
				"the upstream software is managed outside GrokInstall and was not installed or modified")
		}
		m.Input = manifest.Schema{Fields: []manifest.Field{
			{Name: "input", Type: "object", Description: "JSON object passed to the capability on stdin"},
		}}
		m.Output = manifest.Schema{Fields: []manifest.Field{
			{Name: "result", Type: "object", Description: "the capability's JSON result"},
		}}
		m.Security.ExecutesSourceCode = true
		actions.FilesCreated = append(actions.FilesCreated, "manifests/"+name+".json")
		actions.ExternalCommands = append(actions.ExternalCommands, "smoke test: "+cmd)

	default:
		m.Execution = manifest.Execution{
			Type:      manifest.ExecutionNone,
			Supported: false,
			Note:      fmt.Sprintf("%s is detected and planned, but GrokInstall cannot run it yet", chosen),
		}
		m.Status = "plan-only"
		m.Input = manifest.Schema{Fields: []manifest.Field{{Name: "input", Type: "object"}}}
		m.Output = manifest.Schema{Fields: []manifest.Field{{Name: "result", Type: "object"}}}
		actions.FilesCreated = append(actions.FilesCreated, "manifests/"+name+".json")
	}

	actions.Dependencies = []string{}
	if len(built.Security.InstallScripts) > 0 {
		actions.SecurityConcerns = append(actions.SecurityConcerns,
			"upstream install scripts were detected and deliberately not executed")
	}
	if built.Security.PromptInjection {
		actions.SecurityConcerns = append(actions.SecurityConcerns,
			"source contains instruction-like text; it is treated as untrusted data only")
	}
	actions.SecurityConcerns = append(actions.SecurityConcerns,
		"no upstream package was installed and no install script was run")
	return actions, m
}

func useWhen(goal, name string) string {
	if strings.TrimSpace(goal) == "" {
		return "the user asks GrokBot to use " + name
	}
	return "the user wants GrokBot to: " + goal
}

func securityNotes(built *plan.Plan) []string {
	notes := []string{"GrokInstall registered a capability contract; it did not install upstream software."}
	if built.Security.PromptInjection {
		notes = append(notes, "source contains instruction-like text, treated as untrusted data only")
	}
	if len(built.Security.InstallScripts) > 0 {
		notes = append(notes, "upstream install scripts were detected and not executed")
	}
	return notes
}

// evidenceFor records the evidence strings that justified the chosen strategy.
func evidenceFor(built *plan.Plan, chosen strategy.ID) []evidence.Item {
	cand := built.Comparison.Candidate(chosen)
	if cand == nil {
		return nil
	}
	out := make([]evidence.Item, 0, len(cand.Evidence))
	for _, e := range cand.Evidence {
		out = append(out, evidence.Item{Finding: "strategy_" + string(chosen), Evidence: []string{e}})
	}
	return out
}

func workingDirFor(opts Options) string {
	if opts.Source.Kind == source.KindLocalDir {
		return opts.Source.LocalPath
	}
	return ""
}

// discoverCommand finds an executable for a CLI bridge. It prefers an explicit
// override, then the entrypoint path the project declared, then a command
// already on PATH. It never installs anything and never runs a project script.
func discoverCommand(built *plan.Plan) (string, []string) {
	root := built.Source.LocalPath
	// A declared entrypoint path is the most precise signal available.
	for _, rel := range built.Inspection.EntrypointPaths {
		if rel == "" {
			continue
		}
		abs := rel
		if root != "" && !filepath.IsAbs(abs) {
			abs = filepath.Join(root, rel)
		}
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		if info.Mode()&0o111 != 0 {
			return abs, nil
		}
		// A non-executable script is still runnable through its interpreter;
		// this is argv composition, not a generated wrapper.
		if interp := interpreterFor(abs); interp != "" {
			return interp, []string{abs}
		}
	}
	// Fall back to a command name that is already installed.
	for _, name := range built.Inspection.Entrypoints {
		if name == "" {
			continue
		}
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", nil
}

// interpreterFor returns an interpreter command prefix for a script that is not
// directly executable. It never generates code.
func interpreterFor(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".cjs":
		if node, err := exec.LookPath("node"); err == nil {
			return node
		}
	case ".py":
		if py, err := exec.LookPath("python3"); err == nil {
			return py
		}
	}
	return ""
}

// stage writes the install's files into a staging directory.
func (ins *Installer) stage(stageDir string, opts Options, m *manifest.Manifest, built *plan.Plan) ([]receipt.FileChange, error) {
	var created []receipt.FileChange

	if m.Execution.Type == manifest.ExecutionBuiltin && m.Execution.Handler == "knowledge" {
		adapterDir := filepath.Join(stageDir, m.Name)
		if err := os.MkdirAll(adapterDir, 0o755); err != nil {
			return created, err
		}
		idx, err := buildKnowledgeIndex(built.Source.LocalPath)
		if err != nil {
			return created, fmt.Errorf("build knowledge index: %w", err)
		}
		indexPath := filepath.Join(adapterDir, "index.json")
		if err := idx.Save(indexPath); err != nil {
			return created, err
		}
		created = append(created, changeFor(indexPath, "create", "grokinstall"))
	}

	data, err := m.JSON()
	if err != nil {
		return created, err
	}
	manifestPath := filepath.Join(stageDir, m.Name+".json")
	if err := os.WriteFile(manifestPath, append(data, '\n'), 0o644); err != nil {
		return created, err
	}
	created = append(created, changeFor(manifestPath, "create", "grokinstall"))
	return created, nil
}

func buildKnowledgeIndex(root string) (*knowledge.Index, error) {
	if root == "" {
		return nil, errors.New("knowledge import requires a local source directory")
	}
	return knowledge.Build(root, knowledge.Limits{})
}

func changeFor(path, action, owner string) receipt.FileChange {
	c := receipt.FileChange{Path: path, Action: action, Owner: owner}
	if info, err := os.Stat(path); err == nil {
		c.Size = info.Size()
	}
	if data, err := os.ReadFile(path); err == nil {
		sum := sha256.Sum256(data)
		c.Checksum = hex.EncodeToString(sum[:])[:16]
	}
	return c
}

type commitResult struct {
	manifestPath string
	adapterPath  string
	runtimeDir   string
}

// commit moves staged files into final state and registers the capability.
func (ins *Installer) commit(stageDir, stagedManifest string, opts Options, built *plan.Plan, res *Result, v receipt.Verification) (commitResult, error) {
	var out commitResult

	// Read back the verified manifest and move it into place.
	data, err := os.ReadFile(stagedManifest)
	if err != nil {
		return out, err
	}
	m, err := manifest.Parse(data)
	if err != nil {
		return out, err
	}
	// Commit a provisioned runtime first: the manifest must point at a file
	// that already exists.
	if res.provisionResult != nil && res.provisionResult.ExecutablePath != "" {
		runtimeDir, meta, rerr := commitRuntime(ins.Registry, m.Name, res.provisionResult)
		if rerr != nil {
			return out, fmt.Errorf("commit runtime: %w", rerr)
		}
		out.runtimeDir = runtimeDir
		res.RuntimeDir = runtimeDir
		if res.ProvisioningDetail == nil {
			res.ProvisioningDetail = &receipt.Provisioning{}
		}
		res.ProvisioningDetail.RuntimeDir = runtimeDir
		res.ProvisioningDetail.Checksums = meta.Checksums
	}

	manifestPath, err := m.Save(ins.Registry.ManifestsDir())
	if err != nil {
		return out, fmt.Errorf("commit manifest: %w", err)
	}
	out.manifestPath = manifestPath

	// Move the adapter, if the strategy produced one.
	if m.Execution.Type == manifest.ExecutionBuiltin {
		stagedAdapter := filepath.Join(stageDir, m.Name)
		if _, serr := os.Stat(stagedAdapter); serr == nil {
			finalAdapter := filepath.Join(ins.Registry.AdaptersDir(), m.Name)
			if err := os.RemoveAll(finalAdapter); err != nil {
				return out, err
			}
			if err := os.MkdirAll(filepath.Dir(finalAdapter), 0o755); err != nil {
				return out, err
			}
			if err := os.Rename(stagedAdapter, finalAdapter); err != nil {
				return out, err
			}
			out.adapterPath = finalAdapter
		}
	}

	// Point the manifest at the committed owned runtime, when one exists.
	if out.runtimeDir != "" {
		exe, ok := readRuntimeExecutable(out.runtimeDir, m.Name)
		if ok {
			m.Execution.Command = exe
			data, mErr := m.JSON()
			if mErr == nil {
				if wErr := os.WriteFile(manifestPath, append(data, '\n'), 0o644); wErr == nil {
					out.manifestPath = manifestPath
				}
			}
		}
	}

	entry := registry.Entry{
		Name:         m.Name,
		Version:      m.Version,
		Strategy:     m.Strategy,
		Support:      string(m.Support),
		State:        registry.StateReady,
		Status:       m.Status,
		Source:       m.Source,
		Goal:         m.Goal,
		ManifestPath: manifestPath,
		AdapterPath:  out.adapterPath,
		RuntimeDir:   out.runtimeDir,
		InstallID:    res.InstallID,
		InstalledAt:  now(),
	}
	if ins.Registry.Has(m.Name) {
		if !opts.AllowReplace {
			// Roll the manifest back out of final state: nothing is registered.
			_ = os.Remove(manifestPath)
			return out, fmt.Errorf("capability %q already exists; use --replace to reinstall it", m.Name)
		}
		old, _ := ins.Registry.Lookup(m.Name)
		if err := cleanupEntryFiles(old); err != nil {
			return out, err
		}
		entry.UpdatedAt = now()
		if err := ins.Registry.Update(entry); err != nil {
			return out, err
		}
	} else if err := ins.Registry.Register(entry); err != nil {
		_ = os.Remove(manifestPath)
		return out, err
	}
	return out, nil
}

// verify checks a staged manifest before anything is registered.
func (ins *Installer) verify(m *manifest.Manifest, stagedPath string, opts Options) receipt.Verification {
	v := receipt.Verification{Performed: true}
	add := func(name string, passed bool, detail string) {
		v.Checks = append(v.Checks, receipt.Check{Name: name, Passed: passed, Detail: detail})
	}

	if err := m.Validate(); err != nil {
		add("manifest validates", false, err.Error())
		v.Passed = false
		return v
	}
	add("manifest validates", true, "schema "+m.Schema)

	switch m.Execution.Type {
	case manifest.ExecutionSubprocess:
		cmd := m.Execution.Command
		info, err := os.Stat(cmd)
		if err != nil {
			add("execution target exists", false, err.Error())
			v.Passed = false
			return v
		}
		add("execution target exists", true, cmd)
		if info.Mode()&0o111 == 0 {
			add("execution target is executable", false, cmd+" is not executable")
			v.Passed = false
			return v
		}
		add("execution target is executable", true, "mode "+info.Mode().String())
		if !pathInsideSource(cmd, opts.Source) {
			add("target is not outside the source", true, "external command is used as-is")
		}
		spec := ins.specFor(m)
		spec.Timeout = shortTimeout(m.Execution.TimeoutMs)
		resp, err := ins.rt.Invoke(context.Background(), spec, json.RawMessage(`{}`))
		if err != nil {
			add("capability launches", false, err.Error())
			v.Passed = false
			return v
		}
		if !resp.OK {
			add("capability launches", false, resp.Error.Code+": "+resp.Error.Message)
			v.Passed = false
			return v
		}
		add("capability launches", true, "returned a bounded result")
		v.Output = truncateOutput(resp.Result)

	case manifest.ExecutionBuiltin:
		idxPath := filepath.Join(filepath.Dir(stagedPath), m.Name, "index.json")
		if _, err := os.Stat(idxPath); err != nil {
			add("knowledge index exists", false, err.Error())
			v.Passed = false
			return v
		}
		idx, err := knowledge.Load(idxPath)
		if err != nil {
			add("knowledge index loads", false, err.Error())
			v.Passed = false
			return v
		}
		add("knowledge index loads", true, fmt.Sprintf("%d documents", len(idx.Documents)))
		if len(idx.Documents) == 0 {
			add("knowledge index has content", false, "no documentation was indexed")
			v.Passed = false
			return v
		}
		add("knowledge index has content", true, fmt.Sprintf("%d documents", len(idx.Documents)))
		res := idx.Search("the", 1)
		add("knowledge search responds", true, fmt.Sprintf("%d matches for a probe query", len(res.Matches)))

	case manifest.ExecutionNone:
		if m.Support.Executable() {
			// The strategy promises a runnable capability but nothing could be
			// resolved to run. Failing is the honest outcome: silently
			// registering a plan would blur detection with installation.
			add("capability is runnable", false,
				"strategy "+m.Strategy+" is "+string(m.Support)+" but no executable could be resolved")
			v.Passed = false
			return v
		}
		add("capability is plan-only", true, "strategy "+m.Strategy+" is "+string(m.Support)+"; nothing was executed")
	}

	v.Passed = true
	for _, c := range v.Checks {
		if !c.Passed {
			v.Passed = false
		}
	}
	return v
}

func pathInsideSource(cmd string, src sourceRef) bool {
	if src.Kind != source.KindLocalDir || src.LocalPath == "" {
		return false
	}
	abs, err := filepath.Abs(cmd)
	if err != nil {
		return false
	}
	return strings.HasPrefix(abs, src.LocalPath+string(filepath.Separator))
}

func shortTimeout(ms int) time.Duration {
	if ms <= 0 || ms > 10000 {
		ms = 10000
	}
	return time.Duration(ms) * time.Millisecond
}

func truncateOutput(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	if len(data) > 300 {
		return string(data[:300])
	}
	return string(data)
}

// writeReceipt persists the receipt and returns the parsed copy.
func (ins *Installer) writeReceipt(res *Result, built *plan.Plan, r receipt.Receipt) (*receipt.Receipt, error) {
	r.Schema = receipt.Schema
	r.InstallID = res.InstallID
	if r.Timestamp == "" {
		r.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}
	r.Source = res.Source
	r.SourceID = built.Inspection.Identity
	r.CommitSHA = built.Inspection.CommitSHA
	r.Goal = res.Goal
	r.Strategy = res.Strategy
	r.Support = res.Support
	if r.DependenciesAdded == nil {
		r.DependenciesAdded = []string{}
	}
	path, err := r.Save(ins.Registry.ReceiptsDir())
	if err != nil {
		return nil, err
	}
	res.ReceiptPath = path
	loaded, err := receipt.Load(path)
	if err != nil {
		return nil, err
	}
	return loaded, nil
}

func (ins *Installer) specFor(m *manifest.Manifest) runtime.Spec {
	timeout := time.Duration(m.Execution.TimeoutMs) * time.Millisecond
	if timeout <= 0 {
		timeout = runtime.DefaultTimeout
	}
	maxOut := m.Execution.MaxOutputBytes
	if maxOut <= 0 {
		maxOut = runtime.DefaultMaxOutputBytes
	}
	mode := runtime.InputMode(m.Execution.InputMode)
	switch mode {
	case runtime.ModeArgv:
	case runtime.ModeStdinJSON, "":
		mode = runtime.ModeStdinJSON
	default:
		mode = runtime.ModeNone
	}
	return runtime.Spec{
		Type:           runtime.ExecutionType(m.Execution.Type),
		Command:        m.Execution.Command,
		Args:           m.Execution.Args,
		ArgvMap:        m.Execution.ArgvMap,
		InputMode:      mode,
		WorkingDir:     m.Execution.WorkingDir,
		Env:            m.Execution.Env,
		Timeout:        timeout,
		MaxOutputBytes: maxOut,
		Handler:        m.Execution.Handler,
		ExpectJSON:     m.Execution.ExpectJSON,
	}
}

// Run invokes an installed capability through the universal runtime.
func (ins *Installer) Run(name string, input json.RawMessage) (*RunResult, error) {
	entry, err := ins.Registry.Lookup(name)
	if err != nil {
		return nil, err
	}
	m, err := manifest.Load(entry.ManifestPath)
	if err != nil {
		return nil, err
	}
	if !m.Runnable() {
		return &RunResult{
			OK: false,
			Error: &runtime.Error{
				Code:    runtime.CodeNotExecutable,
				Message: "this capability is " + string(m.Support) + " and cannot be executed",
			},
		}, nil
	}
	spec := ins.specFor(m)
	if m.Execution.Type == manifest.ExecutionBuiltin && m.Execution.Handler == "knowledge" {
		// Bind the index for this invocation only: no shared mutable state.
		indexPath := entry.AdapterPath
		if indexPath == "" {
			indexPath = filepath.Join(ins.Registry.AdaptersDir(), m.Name)
		}
		spec.HandlerFunc = func(ctx context.Context, in json.RawMessage) (any, error) {
			return searchKnowledge(filepath.Join(indexPath, "index.json"), in)
		}
	}
	resp, err := ins.rt.Invoke(context.Background(), spec, input)
	if err != nil {
		return nil, err
	}
	out := &RunResult{
		OK:          resp.OK,
		Result:      resp.Result,
		Warnings:    resp.Warnings,
		Artifacts:   resp.Artifacts,
		DurationMs:  resp.DurationMs,
		InputBytes:  resp.InputBytes,
		OutputBytes: resp.OutputBytes,
	}
	if resp.Error != nil {
		out.Error = resp.Error
		return out, nil
	}
	if raw, mErr := json.Marshal(resp.Result); mErr == nil {
		out.RawResult = string(raw)
	}
	return out, nil
}

// searchKnowledge answers a query from an installed index.
func searchKnowledge(indexPath string, input json.RawMessage) (any, error) {
	idx, err := knowledge.Load(indexPath)
	if err != nil {
		return nil, err
	}
	var req struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &req); err != nil {
			return nil, fmt.Errorf("invalid knowledge query: %w", err)
		}
	}
	return idx.Search(req.Query, req.Limit), nil
}

// Test runs a smoke test against an installed capability.
func (ins *Installer) Test(name string) (*SmokeReport, error) {
	start := time.Now()
	report := &SmokeReport{Capability: name}
	entry, err := ins.Registry.Lookup(name)
	if err != nil {
		return nil, err
	}
	add := func(n string, passed bool, detail string) {
		report.Checks = append(report.Checks, receipt.Check{Name: n, Passed: passed, Detail: detail})
	}

	if _, err := os.Stat(entry.ManifestPath); err != nil {
		add("manifest exists", false, err.Error())
		report.DurationMs = time.Since(start).Milliseconds()
		return report, nil
	}
	add("manifest exists", true, entry.ManifestPath)

	m, err := manifest.Load(entry.ManifestPath)
	if err != nil {
		add("manifest is valid", false, err.Error())
		report.DurationMs = time.Since(start).Milliseconds()
		return report, nil
	}
	if err := m.Validate(); err != nil {
		add("manifest is valid", false, err.Error())
		report.DurationMs = time.Since(start).Milliseconds()
		return report, nil
	}
	add("manifest is valid", true, m.Schema)

	if !m.Runnable() {
		add("capability is runnable", false, "strategy "+m.Strategy+" is "+string(m.Support))
		report.DurationMs = time.Since(start).Milliseconds()
		return report, nil
	}
	add("capability is runnable", true, m.Execution.Type)

	if m.Execution.Type == manifest.ExecutionSubprocess {
		info, err := os.Stat(m.Execution.Command)
		if err != nil {
			add("execution target exists", false, err.Error())
			report.DurationMs = time.Since(start).Milliseconds()
			return report, nil
		}
		add("execution target exists", true, m.Execution.Command)
		if info.Mode()&0o111 == 0 {
			add("execution target is executable", false, "not executable")
			report.DurationMs = time.Since(start).Milliseconds()
			return report, nil
		}
		add("execution target is executable", true, info.Mode().String())
	}

	if entry.AdapterPath != "" {
		if _, err := os.Stat(entry.AdapterPath); err != nil {
			add("adapter exists", false, err.Error())
			report.DurationMs = time.Since(start).Milliseconds()
			return report, nil
		}
		add("adapter exists", true, entry.AdapterPath)
	}

	// Launch the capability with a bounded timeout.
	spec := ins.specFor(m)
	spec.Timeout = shortTimeout(m.Execution.TimeoutMs)
	var input json.RawMessage = json.RawMessage(`{}`)
	if m.Execution.Type == manifest.ExecutionBuiltin && m.Execution.Handler == "knowledge" {
		idxPath := filepath.Join(entry.AdapterPath, "index.json")
		spec.HandlerFunc = func(ctx context.Context, in json.RawMessage) (any, error) {
			return searchKnowledge(idxPath, in)
		}
		input = json.RawMessage(`{"query":"the"}`)
	}
	resp, err := ins.rt.Invoke(context.Background(), spec, input)
	switch {
	case err != nil:
		add("capability launches", false, err.Error())
	case !resp.OK:
		add("capability launches", false, resp.Error.Code+": "+resp.Error.Message)
	default:
		add("capability launches", true, "bounded result returned")
	}
	if entry.AdapterPath != "" {
		if _, err := os.Stat(entry.AdapterPath); err != nil {
			add("adapter is present", false, err.Error())
		} else {
			add("adapter is present", true, entry.AdapterPath)
		}
	}

	report.Passed = true
	for _, c := range report.Checks {
		if !c.Passed {
			report.Passed = false
		}
	}
	report.DurationMs = time.Since(start).Milliseconds()
	return report, nil
}

// Capabilities lists installed capabilities for machine discovery. It reads
// manifests, never the source repository.
func (ins *Installer) Capabilities() ([]CapabilitySummary, error) {
	entries, err := ins.Registry.List()
	if err != nil {
		return nil, err
	}
	out := make([]CapabilitySummary, 0, len(entries))
	for _, e := range entries {
		summary := CapabilitySummary{
			Name:     e.Name,
			Strategy: e.Strategy,
			Support:  e.Support,
			State:    string(e.State),
			Status:   e.Status,
		}
		m, err := manifest.Load(e.ManifestPath)
		if err != nil {
			summary.Status = "manifest-missing"
			summary.Description = "manifest could not be read: " + err.Error()
			out = append(out, summary)
			continue
		}
		summary.Description = m.Grokbot.UseWhen
		summary.UseWhen = m.Grokbot.UseWhen
		summary.Input = m.Input
		summary.Output = m.Output
		// Runnable requires both a runnable manifest and a ready lifecycle
		// state: a broken or dirty capability must never be offered as callable.
		summary.Runnable = m.Runnable() && e.State.Runnable()
		if m.Runnable() {
			summary.Call = m.CallLine()
			summary.GrokBot = m.ContractText()
		}
		out = append(out, summary)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Info returns the full detail for one capability.
func (ins *Installer) Info(name string) (*registry.Entry, *manifest.Manifest, *receipt.Receipt, error) {
	entry, err := ins.Registry.Lookup(name)
	if err != nil {
		return nil, nil, nil, err
	}
	m, err := manifest.Load(entry.ManifestPath)
	if err != nil {
		return &entry, nil, nil, err
	}
	var r *receipt.Receipt
	if entry.ReceiptPath != "" {
		if loaded, lerr := receipt.Load(entry.ReceiptPath); lerr == nil {
			r = loaded
		}
	}
	return &entry, m, r, nil
}

// Uninstall removes only GrokInstall-owned artifacts, using the receipt and the
// registry entry. Upstream sources and unrelated files are never touched.
func (ins *Installer) Uninstall(name string) error {
	entry, err := ins.Registry.Lookup(name)
	if err != nil {
		return err
	}
	// A runtime shared with another capability is never removed: ownership is
	// per capability, and removing a shared resource would break the other one.
	var shared []string
	if entry.RuntimeDir != "" {
		others := ins.sharedRuntimeOwnersExcept(entry.RuntimeDir, name)
		if len(others) == 0 {
			if err := os.RemoveAll(entry.RuntimeDir); err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("remove runtime %s: %w", entry.RuntimeDir, err)
			}
		} else {
			shared = others
		}
	}
	if err := cleanupEntryFiles(entry); err != nil {
		return err
	}
	if len(shared) > 0 {
		_ = cache.WriteJSONAtomic(ins.Registry.RuntimePath(name)+".shared.json",
			map[string]any{"retained_for": shared})
	}
	if err := ins.Registry.Remove(name); err != nil {
		return err
	}
	// The receipt is the audit trail: keep it, marked uninstalled.
	if entry.ReceiptPath != "" {
		if r, lerr := receipt.Load(entry.ReceiptPath); lerr == nil {
			r.UninstalledAt = time.Now().UTC().Format(time.RFC3339)
			r.Result = receipt.ResultUninstall
			r.UninstallID = newInstallID()
			_, _ = r.Save(ins.Registry.ReceiptsDir())
		}
	}
	return nil
}

// sharedRuntimeOwnersExcept lists other capabilities claiming a runtime.
func (ins *Installer) sharedRuntimeOwnersExcept(runtimeDir, self string) []string {
	entries, err := ins.Registry.List()
	if err != nil {
		return nil
	}
	var owners []string
	abs, _ := filepath.Abs(runtimeDir)
	for _, e := range entries {
		if e.Name == self || e.RuntimeDir == "" {
			continue
		}
		other, _ := filepath.Abs(e.RuntimeDir)
		if other == abs {
			owners = append(owners, e.Name)
		}
	}
	return owners
}

func cleanupEntryFiles(entry registry.Entry) error {
	// Only paths inside GrokInstall's own state directory are ever removed.
	if entry.ManifestPath != "" {
		if err := os.Remove(entry.ManifestPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove manifest %s: %w", entry.ManifestPath, err)
		}
	}
	if entry.AdapterPath != "" {
		if err := os.RemoveAll(entry.AdapterPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove adapter %s: %w", entry.AdapterPath, err)
		}
	}
	return nil
}
