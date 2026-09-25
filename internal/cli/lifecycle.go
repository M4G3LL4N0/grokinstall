package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/M4G3LL4N0/grokinstall/internal/installer"
	"github.com/M4G3LL4N0/grokinstall/internal/manifest"
	"github.com/M4G3LL4N0/grokinstall/internal/provision"
	"github.com/M4G3LL4N0/grokinstall/internal/receipt"
	"github.com/M4G3LL4N0/grokinstall/internal/registry"
	"github.com/M4G3LL4N0/grokinstall/internal/source"
	"github.com/M4G3LL4N0/grokinstall/internal/usage"
)

// installerFor builds an installer bound to the resolved state directory.
func (g *globalFlags) installerFor() (*installer.Installer, *registry.Registry, error) {
	reg, err := g.state()
	if err != nil {
		return nil, nil, err
	}
	return installer.New(reg), reg, nil
}

func newInstallCommand(g *globalFlags) *cobra.Command {
	var (
		goal          string
		name          string
		command       string
		dryRun        bool
		replace       bool
		timeoutMs     int
		provisionMode string
		allowScripts  bool
		allowSystem   bool
		allowBuild    bool
	)
	cmd := &cobra.Command{
		Use:     "install SOURCE --goal \"...\"",
		Short:   "Install a capability integration (not the upstream software stack)",
		GroupID: "install",
		Long: `Turns a source into a verified GrokBot capability.

GrokInstall installs the capability integration: a manifest, a thin adapter when
one is genuinely needed, a registry entry and a receipt. It does not install
upstream packages and never runs a project's install scripts.

If the required executable is missing, GrokInstall compares provisioning
routes and takes the smallest safe one. A method that needs more trust than the
safe policy allows is refused with the specific authorization it would need.

A source that needs no installation is a successful outcome, not a failure.

Examples:
  # Install from a public repository
  grokinstall install https://github.com/sharkdp/bat \
    --goal "Let GrokBot use bat to inspect text files"

  # See exactly what would change
  grokinstall install ./my-tool --goal "Let GrokBot run my tool" --dry-run

  # Never provision anything
  grokinstall install ./my-tool --goal "..." --provision=never`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := source.Normalize(args[0])
			if err != nil {
				return err
			}
			ins, reg, err := g.installerFor()
			if err != nil {
				return err
			}
			mode, valid := provision.ParseMode(provisionMode)
			if !valid {
				return fmt.Errorf("invalid --provision value %q: use safe, never or prompt", provisionMode)
			}
			// Approvals are specific to a risk class. There is deliberately no
			// generic --yes that would authorize all of them at once.
			var allow []provision.Authorization
			if allowScripts {
				allow = append(allow, provision.AuthInstallScripts)
			}
			if allowSystem {
				allow = append(allow, provision.AuthSystemPackageManager)
			}
			if allowBuild {
				allow = append(allow, provision.AuthSourceBuild)
			}
			policy := provision.Policy{Mode: mode, Allow: allow}

			start := time.Now()
			res, err := ins.Install(installer.Options{
				Policy:       policy,
				Source:       *src,
				Goal:         goal,
				Name:         name,
				Command:      command,
				DryRun:       dryRun,
				AllowReplace: replace,
				TimeoutMs:    timeoutMs,
			})
			if err != nil {
				if res != nil {
					g.recordUsage(reg, usage.Event{
						Op:             usage.OpInstall,
						Source:         src.Canonical,
						Strategy:       res.Strategy,
						Outcome:        res.Result,
						Detail:         err.Error(),
						DurationMillis: time.Since(start).Milliseconds(),
					})
					if !g.jsonOutput {
						renderInstall(cmd.OutOrStdout(), res)
						if res.Refusal != nil {
							renderRefusal(cmd.OutOrStdout(), res.Refusal)
						}
						renderProvisioningCandidates(cmd.OutOrStdout(), ins.LastCandidates, ins.LastSelected.Method)
					}
				}
				return err
			}
			g.recordUsage(reg, usage.Event{
				Op:             usage.OpInstall,
				Source:         src.Canonical,
				Capability:     res.Capability,
				Strategy:       res.Strategy,
				CacheHit:       res.InspectionCacheHit,
				Outcome:        res.Result,
				DurationMillis: time.Since(start).Milliseconds(),
			})
			if g.jsonOutput {
				return writeJSON(cmd, res)
			}
			renderInstall(cmd.OutOrStdout(), res)
			return nil
		},
	}
	cmd.Flags().StringVar(&goal, "goal", "", "what GrokBot should be able to do with this source")
	cmd.Flags().StringVar(&name, "name", "", "override the derived capability name")
	cmd.Flags().StringVar(&command, "command", "", "override the executable used by a cli_bridge")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would change without changing state")
	cmd.Flags().BoolVar(&replace, "replace", false, "replace an existing capability of the same name")
	cmd.Flags().IntVar(&timeoutMs, "timeout-ms", 0, "per-call timeout for the installed capability")
	cmd.Flags().StringVar(&provisionMode, "provision", "safe", "provisioning policy: safe, never or prompt")
	cmd.Flags().BoolVar(&allowScripts, "allow-install-scripts", false, "authorize package lifecycle scripts to execute")
	cmd.Flags().BoolVar(&allowSystem, "allow-system-package-manager", false, "authorize machine-wide package changes")
	cmd.Flags().BoolVar(&allowBuild, "allow-source-build", false, "authorize compiling untrusted source")
	return cmd
}

func renderInstall(w io.Writer, res *installer.Result) {
	if res.DryRun {
		fmt.Fprintf(w, "Dry run: %s\n", res.Source)
		fmt.Fprintf(w, "\nSelected strategy: %s (%s)\n", res.Strategy, res.Support)
		a := res.PlannedActions
		if len(a.FilesCreated) > 0 {
			fmt.Fprintf(w, "\nFiles that would be created\n")
			for _, f := range a.FilesCreated {
				fmt.Fprintf(w, "  %s\n", f)
			}
		}
		if len(a.AdapterGeneration) > 0 {
			fmt.Fprintf(w, "\nAdapter generation\n  %s\n", a.AdapterGeneration)
		}
		if len(a.RegistryChanges) > 0 {
			fmt.Fprintf(w, "\nRegistry changes\n")
			for _, c := range a.RegistryChanges {
				fmt.Fprintf(w, "  %s\n", c)
			}
		}
		if len(a.ExternalCommands) > 0 {
			fmt.Fprintf(w, "\nExternal commands\n")
			for _, c := range a.ExternalCommands {
				fmt.Fprintf(w, "  %s\n", c)
			}
		}
		fmt.Fprintf(w, "\nDependencies introduced\n  %s\n", orNone(a.Dependencies))
		fmt.Fprintf(w, "\nVerification plan\n")
		for _, c := range a.VerificationPlan {
			fmt.Fprintf(w, "  - %s\n", c)
		}
		if len(a.SecurityConcerns) > 0 {
			fmt.Fprintf(w, "\nSecurity\n")
			for _, c := range a.SecurityConcerns {
				fmt.Fprintf(w, "  - %s\n", c)
			}
		}
		fmt.Fprintf(w, "\n%s\n", res.Explanation)
		return
	}

	switch res.Result {
	case receipt.ResultNoInstall:
		fmt.Fprintf(w, "No installation needed.\n\n")
		fmt.Fprintf(w, "Source: %s\n", res.Source)
		fmt.Fprintf(w, "Goal:   %s\n", res.Goal)
		fmt.Fprintf(w, "Why:    %s\n", res.Explanation)
	case receipt.ResultPlanned:
		fmt.Fprintf(w, "Planned only: %s\n", res.Strategy)
		fmt.Fprintf(w, "This strategy is detected and compared, but it is not runnable in this version.\n")
		fmt.Fprintf(w, "Nothing was registered: a plan is not an installed capability.\n")
		if res.PlanPath != "" {
			fmt.Fprintf(w, "Plan saved: %s\n", res.PlanPath)
		}
	case receipt.ResultDirty:
		fmt.Fprintf(w, "Installation left DIRTY state: rollback could not be completed.\n\n")
		fmt.Fprintf(w, "Source: %s\n", res.Source)
		fmt.Fprintf(w, "Reason: %s\n", res.Explanation)
		fmt.Fprintf(w, "\nRun: grokinstall doctor\n")
	case receipt.ResultFailed:
		fmt.Fprintf(w, "Installation failed: nothing was registered.\n\n")
		fmt.Fprintf(w, "Source:   %s\n", res.Source)
		fmt.Fprintf(w, "Strategy: %s\n", res.Strategy)
		if res.Explanation != "" {
			fmt.Fprintf(w, "Reason:   %s\n", res.Explanation)
		}
		if len(res.Verification.Checks) > 0 {
			fmt.Fprintf(w, "\nVerification\n")
			for _, c := range res.Verification.Checks {
				mark := "ok"
				if !c.Passed {
					mark = "FAIL"
				}
				fmt.Fprintf(w, "  [%s] %s: %s\n", mark, c.Name, c.Detail)
			}
		}
		if res.ReceiptPath != "" {
			fmt.Fprintf(w, "\nReceipt: %s (kept for audit)\n", res.ReceiptPath)
		}
		fmt.Fprintf(w, "\nNo upstream files were changed.\n")
	default:
		fmt.Fprintf(w, "Installed: %s\n\n", res.Capability)
		fmt.Fprintf(w, "Strategy:    %s (%s)\n", res.Strategy, res.Support)
		fmt.Fprintf(w, "Source:      %s\n", res.Source)
		fmt.Fprintf(w, "Manifest:    %s\n", res.ManifestPath)
		if res.AdapterPath != "" {
			fmt.Fprintf(w, "Adapter:     %s\n", res.AdapterPath)
		}
		if res.RuntimeDir != "" {
			fmt.Fprintf(w, "Runtime:     %s (GrokInstall-owned)\n", res.RuntimeDir)
		}
		if res.ProvisioningDetail != nil {
			d := res.ProvisioningDetail
			fmt.Fprintf(w, "\nProvisioning\n")
			fmt.Fprintf(w, "  method:    %s\n", d.Method)
			if d.ArtifactSource != "" {
				fmt.Fprintf(w, "  source:    %s\n", d.ArtifactSource)
			}
			if d.ArtifactVersion != "" {
				fmt.Fprintf(w, "  version:   %s\n", d.ArtifactVersion)
			}
			if d.Asset != "" {
				fmt.Fprintf(w, "  asset:     %s\n", d.Asset)
			}
			if d.ChecksumStatus != "" {
				fmt.Fprintf(w, "  checksum:  %s\n", d.ChecksumStatus)
			}
			if d.ActualChecksum != "" {
				fmt.Fprintf(w, "  hash:      %s\n", d.ActualChecksum[:min(16, len(d.ActualChecksum))])
			}
			for _, n := range d.Notes {
				fmt.Fprintf(w, "  note:      %s\n", n)
			}
		}
		fmt.Fprintf(w, "Receipt:     %s\n", res.ReceiptPath)
		if res.Verification.Passed {
			fmt.Fprintf(w, "\nVerification passed\n")
			for _, c := range res.Verification.Checks {
				fmt.Fprintf(w, "  [ok] %s: %s\n", c.Name, c.Detail)
			}
		}
		fmt.Fprintf(w, "\nNo upstream package was installed and no install script was run.\n")
	}
	if len(res.Warnings) > 0 {
		fmt.Fprintf(w, "\nWarnings\n")
		for _, wmsg := range res.Warnings {
			fmt.Fprintf(w, "  - %s\n", wmsg)
		}
	}
	if res.Result == receipt.ResultInstalled || res.Result == receipt.ResultPlanned {
		if contract := contractFor(res); contract != "" {
			fmt.Fprintf(w, "\nGrokBot contract:\n%s", indentText(contract, "  "))
		}
	}
}

func contractFor(res *installer.Result) string {
	if res.ManifestPath == "" {
		return ""
	}
	m, err := manifest.Load(res.ManifestPath)
	if err != nil {
		return ""
	}
	return m.ContractText()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func orNone(list []string) string {
	if len(list) == 0 {
		return "none"
	}
	return strings.Join(list, ", ")
}

func newRunCommand(g *globalFlags) *cobra.Command {
	var inputFlag string
	cmd := &cobra.Command{
		Use:   "run NAME",
		Short: "Invoke an installed capability",
		Long: "Invokes a capability through the universal runtime and returns a stable envelope:\n" +
			"  {\"ok\":true,\"result\":{},\"artifacts\":[],\"warnings\":[]}\n\n" +
			"Input may be given with --input or piped on stdin. The capability command is\n" +
			"executed with direct argv, a controlled environment, a timeout and bounded output.",
		GroupID: "install",
		Example: `  grokinstall run bat.review --input '{"query":"TODO"}'
  echo '{"query":"TODO"}' | grokinstall run bat.review`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ins, reg, err := g.installerFor()
			if err != nil {
				return err
			}
			payload, err := readInput(inputFlag)
			if err != nil {
				return err
			}
			start := time.Now()
			res, err := ins.Run(args[0], payload)
			if err != nil {
				g.recordUsage(reg, usage.Event{Op: usage.OpRun, Capability: args[0], Outcome: "error", Detail: err.Error()})
				return err
			}
			g.recordUsage(reg, usage.Event{
				Op:                    usage.OpRun,
				Capability:            args[0],
				Outcome:               outcomeOf(res.OK),
				CapabilityInputBytes:  res.InputBytes,
				CapabilityOutputBytes: res.OutputBytes,
				AdapterExecutions:     1,
				DurationMillis:        time.Since(start).Milliseconds(),
			})
			if g.jsonOutput {
				if err := writeJSON(cmd, res); err != nil {
					return err
				}
			} else if res.OK {
				fmt.Fprintln(cmd.OutOrStdout(), res.RawResult)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "capability failed: %s\n", res.Error.Message)
			}
			if !res.OK {
				return errRunFailed
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&inputFlag, "input", "", "JSON input object (or pipe JSON on stdin)")
	return cmd
}

var errRunFailed = fmt.Errorf("capability execution failed")

func outcomeOf(ok bool) string {
	if ok {
		return "ok"
	}
	return "failed"
}

func readInput(flag string) (json.RawMessage, error) {
	if strings.TrimSpace(flag) != "" {
		return json.RawMessage(flag), nil
	}
	info, err := os.Stdin.Stat()
	if err != nil {
		return json.RawMessage(`{}`), nil
	}
	if info.Mode()&os.ModeCharDevice != 0 {
		return json.RawMessage(`{}`), nil
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return json.RawMessage(`{}`), nil
	}
	return json.RawMessage(data), nil
}

func newTestCommand(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "test NAME",
		Short:   "Run a smoke test against an installed capability",
		GroupID: "install",
		Example: "  grokinstall test bat.review",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ins, reg, err := g.installerFor()
			if err != nil {
				return err
			}
			report, err := ins.Test(args[0])
			if err != nil {
				return err
			}
			g.recordUsage(reg, usage.Event{
				Op:             usage.OpTest,
				Capability:     args[0],
				Outcome:        outcomeOf(report.Passed),
				DurationMillis: report.DurationMs,
			})
			if g.jsonOutput {
				if err := writeJSON(cmd, report); err != nil {
					return err
				}
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s: %s\n", report.Capability, passWord(report.Passed))
				for _, c := range report.Checks {
					mark := "ok"
					if !c.Passed {
						mark = "FAIL"
					}
					fmt.Fprintf(cmd.OutOrStdout(), "  [%s] %s: %s\n", mark, c.Name, c.Detail)
				}
			}
			if !report.Passed {
				return fmt.Errorf("smoke test failed for %s", args[0])
			}
			return nil
		},
	}
}

func passWord(ok bool) string {
	if ok {
		return "pass"
	}
	return "FAIL"
}

func newListCommand(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "list",
		Short:   "List installed capabilities",
		GroupID: "install",
		Example: "  grokinstall list",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ins, _, err := g.installerFor()
			if err != nil {
				return err
			}
			caps, err := ins.Capabilities()
			if err != nil {
				return err
			}
			if g.jsonOutput {
				return writeJSON(cmd, caps)
			}
			if len(caps) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No capabilities installed.")
				fmt.Fprintln(cmd.OutOrStdout(), "Run: grokinstall install SOURCE --goal \"...\"")
				return nil
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintf(tw, "NAME\tSTRATEGY\tSUPPORT\tSTATE\tSTATUS\n")
			for _, c := range caps {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", c.Name, c.Strategy, c.Support, c.State, c.Status)
			}
			return tw.Flush()
		},
	}
}

func newInfoCommand(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "info NAME",
		Short:   "Show everything worth knowing about one capability",
		GroupID: "install",
		Example: "  grokinstall info bat.review",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ins, _, err := g.installerFor()
			if err != nil {
				return err
			}
			entry, m, r, err := ins.Info(args[0])
			if err != nil {
				return err
			}
			payload := infoPayload{Entry: entry, Manifest: m}
			if r != nil {
				payload.Receipt = r
				payload.Verification = r.Verification
			}
			if g.jsonOutput {
				return writeJSON(cmd, payload)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Name:       %s\n", entry.Name)
			fmt.Fprintf(cmd.OutOrStdout(), "Goal:       %s\n", entry.Goal)
			fmt.Fprintf(cmd.OutOrStdout(), "Source:     %s\n", entry.Source)
			if m != nil {
				if m.Provenance.CommitSHA != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Commit:     %s\n", m.Provenance.CommitSHA)
				}
				if m.Provenance.Identity != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Identity:   %s\n", m.Provenance.Identity)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Strategy:   %s (%s)\n", m.Strategy, m.Support)
				fmt.Fprintf(cmd.OutOrStdout(), "Status:     %s\n", m.Status)
				fmt.Fprintf(cmd.OutOrStdout(), "Execution:  %s\n", m.Execution.Type)
				if m.Execution.Command != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Command:    %s\n", m.Execution.Command)
				}
				if m.Execution.Handler != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "Handler:    %s\n", m.Execution.Handler)
				}
				if len(m.Input.Fields) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "Input:      %s\n", fieldNames(m.Input.Fields))
				}
				if len(m.Output.Fields) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "Output:     %s\n", fieldNames(m.Output.Fields))
				}
				if len(m.Security.Notes) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "\nSecurity\n")
					for _, n := range m.Security.Notes {
						fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", n)
					}
				}
			}
			if entry.InstallID != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "\nInstall:    %s\n", entry.InstallID)
			}
			if r != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "Receipt:    %s\n", entry.ReceiptPath)
				fmt.Fprintf(cmd.OutOrStdout(), "Verified:   %s\n", passWord(r.Verification.Passed))
			}
			return nil
		},
	}
}

type infoPayload struct {
	Entry        *registry.Entry      `json:"entry"`
	Manifest     *manifest.Manifest   `json:"manifest,omitempty"`
	Verification receipt.Verification `json:"verification"`
	Receipt      *receipt.Receipt     `json:"receipt,omitempty"`
}

func fieldNames(fields []manifest.Field) string {
	names := make([]string, len(fields))
	for i, f := range fields {
		names[i] = f.Name
	}
	return strings.Join(names, ", ")
}

func newCapabilitiesCommand(g *globalFlags) *cobra.Command {
	var all bool
	var state string
	cmd := &cobra.Command{
		Use:   "capabilities",
		Short: "Enumerate callable capabilities for GrokBot",
		Long: "Lists installed capabilities with their input and output contracts.\n" +
			"This is the machine-facing discovery surface: GrokBot can use it without\n" +
			"reading any repository, manifest or documentation.",
		GroupID: "install",
		Example: `  grokinstall capabilities
  grokinstall capabilities --json
  grokinstall capabilities --all`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ins, reg, err := g.installerFor()
			if err != nil {
				return err
			}
			caps, err := ins.Capabilities()
			if err != nil {
				return err
			}
			// By default only runnable capabilities are offered, so GrokBot is
			// never handed something it cannot call.
			visible := caps
			if !all {
				runnable := []installer.CapabilitySummary{}
				for _, c := range caps {
					if c.Runnable {
						runnable = append(runnable, c)
					}
				}
				visible = runnable
			}
			if state != "" {
				visible = capabilityStates(visible, state)
			}
			g.recordUsage(reg, usage.Event{Op: usage.OpCapabilities})
			if g.jsonOutput {
				return writeJSON(cmd, map[string]any{
					"capabilities": visible,
					"total":        len(caps),
					"shown":        len(visible),
				})
			}
			if len(visible) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No runnable capabilities installed.")
				if len(caps) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "%d installed capability(ies) are not runnable; see: grokinstall capabilities --all\n", len(caps))
				}
				return nil
			}
			for _, c := range visible {
				fmt.Fprintf(cmd.OutOrStdout(), "%s  [%s]\n", c.Name, c.State)
				if c.Description != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", c.Description)
				}
				if c.Call != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  call: %s\n", c.Call)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "  not runnable: %s\n", c.Support)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "include capabilities that are not runnable")
	cmd.Flags().StringVar(&state, "state", "", "filter by lifecycle state (ready, broken, dirty)")
	return cmd
}

func newGrokbotCommand(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "grokbot NAME",
		Short:   "Generate the smallest sufficient GrokBot contract for a capability",
		GroupID: "install",
		Example: `  grokinstall grokbot bat.review
  grokinstall grokbot bat.review --json`,
		Long: "Renders a contract derived mechanically from the manifest: when to use it,\n" +
			"how to call it, what goes in, what comes out, and what not to do.\n\n" +
			"The contract contains no repository content, file listings or documentation.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ins, reg, err := g.installerFor()
			if err != nil {
				return err
			}
			entry, m, _, err := ins.Info(args[0])
			if err != nil {
				return err
			}
			if m == nil {
				return fmt.Errorf("manifest for %s could not be read", args[0])
			}
			contract := m.ContractText()
			g.recordUsage(reg, usage.Event{
				Op:                   usage.OpGrokBot,
				Capability:           args[0],
				GrokBotContractBytes: len(contract),
			})
			if g.jsonOutput {
				return writeJSON(cmd, map[string]any{
					"name":     m.Name,
					"goal":     m.Goal,
					"strategy": m.Strategy,
					"support":  m.Support,
					"status":   m.Status,
					"runnable": m.Runnable(),
					"call":     m.CallLine(),
					"use_when": m.Grokbot.UseWhen,
					"do_not":   m.Grokbot.DoNot,
					"input":    m.Input,
					"output":   m.Output,
					"contract": contract,
					"bytes":    len(contract),
					"manifest": entry.ManifestPath,
				})
			}
			fmt.Fprint(cmd.OutOrStdout(), contract)
			return nil
		},
	}
}

func newUninstallCommand(g *globalFlags) *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:     "uninstall NAME",
		Short:   "Remove a capability integration, leaving upstream data untouched",
		GroupID: "operate",
		Example: `  grokinstall uninstall bat.review
  grokinstall uninstall bat.review --dry-run`,
		Long: "Removes only what GrokInstall created: its manifest, its adapter and its\n" +
			"registry entry. Upstream repositories, user data and unrelated files are\n" +
			"never touched. The receipt is preserved as the audit trail.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ins, reg, err := g.installerFor()
			if err != nil {
				return err
			}
			entry, err := reg.Lookup(args[0])
			if err != nil {
				return err
			}
			if dryRun {
				payload := map[string]any{
					"dry_run":      true,
					"uninstall":    args[0],
					"would_remove": []string{entry.ManifestPath, entry.AdapterPath},
					"preserved":    "upstream source, receipts, logs and unrelated files",
				}
				if g.jsonOutput {
					return writeJSON(cmd, payload)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Dry run: would remove\n")
				fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", entry.ManifestPath)
				if entry.AdapterPath != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", entry.AdapterPath)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "\nPreserved: upstream source, receipts, logs, unrelated files\n")
				return nil
			}
			if err := ins.Uninstall(args[0]); err != nil {
				return err
			}
			g.recordUsage(reg, usage.Event{
				Op:         usage.OpUninstall,
				Capability: args[0],
				Outcome:    "ok",
			})
			if g.jsonOutput {
				return writeJSON(cmd, map[string]any{
					"uninstalled": args[0],
					"removed":     []string{entry.ManifestPath, entry.AdapterPath},
					"preserved":   "upstream source, receipts, logs and unrelated files",
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Uninstalled %s\n", args[0])
			fmt.Fprintf(cmd.OutOrStdout(), "Removed: manifest, adapter, registry entry\n")
			fmt.Fprintf(cmd.OutOrStdout(), "Preserved: upstream source, receipt and logs\n")
			return nil
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "show what would be removed without removing it")
	return cmd
}

func indentText(s, prefix string) string {
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n") + "\n"
}
