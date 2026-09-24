// Package cli wires the Part 1 commands: inspect, plan, compare, doctor, usage.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"grokinstall/internal/diagnostics"
	"grokinstall/internal/inspect"
	"grokinstall/internal/plan"
	"grokinstall/internal/registry"
	"grokinstall/internal/source"
	"grokinstall/internal/toolchain"
	"grokinstall/internal/usage"
)

// Version is the GrokInstall version reported by --version.
const Version = "0.2.0"

type globalFlags struct {
	jsonOutput bool
	stateDir   string
}

// NewRoot builds the command tree.
func NewRoot() *cobra.Command {
	g := &globalFlags{}

	root := &cobra.Command{
		Use:   "grokinstall",
		Short: "Universal installation intelligence for GrokBot",
		Long: "GrokInstall inspects a source, identifies the capability GrokBot actually needs,\n" +
			"compares integration approaches, and resolves the smallest useful toolchain.\n\n" +
			"Install the capability, not the complexity.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	// Bind directly to the shared struct so values are read after parsing.
	root.PersistentFlags().BoolVar(&g.jsonOutput, "json", false, "emit machine-readable JSON")
	root.PersistentFlags().StringVar(&g.stateDir, "state-dir", "", "override the GrokInstall state directory (default ~/.grokinstall)")

	root.AddCommand(
		newInspectCommand(g),
		newPlanCommand(g),
		newCompareCommand(g),
		newDoctorCommand(g),
		newUsageCommand(g),
		newInstallCommand(g),
		newRunCommand(g),
		newTestCommand(g),
		newListCommand(g),
		newInfoCommand(g),
		newCapabilitiesCommand(g),
		newGrokbotCommand(g),
		newUninstallCommand(g),
		newDiagnoseCommand(g),
		newAuditCommand(g),
	)
	return root
}

// Execute runs the CLI.
func Execute() error {
	return NewRoot().Execute()
}

func (g *globalFlags) state() (*registry.Registry, error) {
	dir := g.stateDir
	if dir == "" {
		dir = os.Getenv("GROKINSTALL_HOME")
	}
	if dir == "" {
		home, err := toolchain.HomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home directory: %w", err)
		}
		dir = filepath.Join(home, ".grokinstall")
	}
	return registry.New(dir)
}

func (g *globalFlags) usageDir(reg *registry.Registry) string { return reg.LogsDir() }

// recordUsage appends a measured event. Telemetry must never break a command.
func (g *globalFlags) recordUsage(reg *registry.Registry, ev usage.Event) {
	_ = usage.Record(g.usageDir(reg), ev)
}

func writeJSON(cmd *cobra.Command, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(cmd.OutOrStdout(), string(data))
	return err
}

func newInspectCommand(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "inspect SOURCE",
		Short: "Inspect a local directory or public GitHub repository",
		Long: "Reads a source deterministically and reports evidence-backed findings.\n" +
			"Inspection never executes project code, install scripts or build steps.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := source.Normalize(args[0])
			if err != nil {
				return err
			}
			reg, err := g.state()
			if err != nil {
				return err
			}
			start := time.Now()
			res, err := inspect.Analyze(context.Background(), *src, reg.Cache, inspect.Options{})
			if err != nil {
				return err
			}
			_ = usage.Record(g.usageDir(reg), usage.Event{
				Op:             usage.OpInspect,
				Source:         src.Canonical,
				CacheHit:       res.CacheHit,
				DurationMillis: time.Since(start).Milliseconds(),
			})
			if g.jsonOutput {
				return writeJSON(cmd, res)
			}
			renderInspection(cmd.OutOrStdout(), res)
			return nil
		},
	}
}

func renderInspection(w io.Writer, res *inspect.Result) {
	fmt.Fprintf(w, "Source:    %s\n", res.Source.Canonical)
	if res.Source.Kind == source.KindGitHub {
		fmt.Fprintf(w, "Repository: %s\n", res.Source.URL)
	}
	if res.CommitSHA != "" {
		fmt.Fprintf(w, "Commit:    %s\n", res.CommitSHA)
	}
	fmt.Fprintf(w, "Identity:  %s\n", res.Identity)
	fmt.Fprintf(w, "Cache:     %s\n", cacheWord(res.CacheHit))
	if res.Meta.Name != "" {
		fmt.Fprintf(w, "Name:      %s", res.Meta.Name)
		if res.Meta.Version != "" {
			fmt.Fprintf(w, " %s", res.Meta.Version)
		}
		fmt.Fprintln(w)
	}
	if res.Meta.Description != "" {
		fmt.Fprintf(w, "About:     %s\n", res.Meta.Description)
	}
	if len(res.Meta.Languages) > 0 {
		fmt.Fprintf(w, "Languages: %s\n", strings.Join(res.Meta.Languages, ", "))
	}
	if len(res.Meta.Entrypoints) > 0 {
		fmt.Fprintf(w, "Commands:  %s\n", strings.Join(res.Meta.Entrypoints, ", "))
	}
	fmt.Fprintf(w, "Files:     %d inspected", len(res.Files))
	if res.Truncated {
		fmt.Fprint(w, " (truncated by inspection limits)")
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "\nEvidence\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, ev := range res.Evidence {
		fmt.Fprintf(tw, "  %s\t%s\t%s\n", ev.Finding, ev.Confidence, strings.Join(ev.Evidence, "; "))
	}
	tw.Flush()

	if notes := securityNotes(res); len(notes) > 0 {
		fmt.Fprintf(w, "\nSecurity\n")
		for _, n := range notes {
			fmt.Fprintf(w, "  - %s\n", n)
		}
	}
	fmt.Fprintf(w, "\nNothing was installed or executed by this command.\n")
}

func securityNotes(res *inspect.Result) []string {
	var out []string
	if res.Security.PromptInjection {
		out = append(out, "source contains instruction-like text; recorded as untrusted data, never followed")
	}
	for _, s := range res.Security.InstallScripts {
		out = append(out, "install script detected and deliberately not executed: "+s)
	}
	for _, s := range res.Security.SecretLikeFiles {
		out = append(out, "secret-like file present; contents were not imported: "+s)
	}
	for _, s := range res.Security.NetworkDownloads {
		out = append(out, "file contains a network download command: "+s)
	}
	for _, s := range res.Security.Privileged {
		out = append(out, "file requests elevated privileges: "+s)
	}
	return out
}

func cacheWord(hit bool) string {
	if hit {
		return "hit"
	}
	return "miss"
}

func newPlanCommand(g *globalFlags) *cobra.Command {
	var goal string
	cmd := &cobra.Command{
		Use:   "plan SOURCE --goal \"...\"",
		Short: "Build an evidence-backed integration plan",
		Long: "Runs the Part 1 pipeline end to end:\n" +
			"normalize, inspect, collect evidence, discover capabilities, interpret the goal,\n" +
			"generate strategies, compare, resolve the toolchain, plan.\n\n" +
			"Nothing is installed. A Context Pack is produced so no whole repository is ever\n" +
			"handed to a model.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := source.Normalize(args[0])
			if err != nil {
				return err
			}
			reg, err := g.state()
			if err != nil {
				return err
			}
			start := time.Now()
			p, err := plan.Build(context.Background(), plan.Options{
				Source:  *src,
				Goal:    goal,
				Store:   reg.Cache,
				Profile: toolchain.Detect(),
			})
			if err != nil {
				return err
			}
			_ = usage.Record(g.usageDir(reg), usage.Event{
				Op:                   usage.OpPlan,
				Source:               src.Canonical,
				CacheHit:             p.Inspection.CacheHit,
				Strategy:             string(p.Comparison.Recommended),
				ContextPackBytes:     p.ContextPackBytes,
				GrokBotContractBytes: len(p.Manifest.ContractText()),
				DurationMillis:       time.Since(start).Milliseconds(),
			})
			if g.jsonOutput {
				return writeJSON(cmd, p)
			}
			renderPlan(cmd.OutOrStdout(), p)
			return nil
		},
	}
	cmd.Flags().StringVar(&goal, "goal", "", "what GrokBot should be able to do with this source")
	return cmd
}

func renderPlan(w io.Writer, p *plan.Plan) {
	fmt.Fprintf(w, "GrokInstall plan (planning only; no changes were made)\n\n")
	fmt.Fprintf(w, "Source:  %s\n", p.Source.Canonical)
	if p.Goal != "" {
		fmt.Fprintf(w, "Goal:    %s\n", p.Goal)
	}
	if p.IsSelf {
		fmt.Fprintf(w, "Note:    this source is GrokInstall itself; prefer doctor and diagnosis over reinstalling.\n")
	}
	fmt.Fprintln(w)

	fmt.Fprintf(w, "Understanding\n")
	for _, u := range p.Understanding {
		fmt.Fprintf(w, "  - %s\n", u)
	}

	fmt.Fprintf(w, "\nRecommended strategy: %s\n", p.Comparison.Recommended)
	fmt.Fprintf(w, "  %s\n", p.Comparison.RecommendationReason)
	if ev := p.Comparison.RecommendationEvidence; len(ev) > 0 {
		for _, e := range ev {
			fmt.Fprintf(w, "  evidence: %s\n", e)
		}
	}

	fmt.Fprintf(w, "\nToolchain\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, n := range p.Toolchain {
		version := ""
		if n.Version != "" {
			version = " " + firstLine(n.Version)
		}
		resolved := ""
		if n.Resolved != "" && n.Resolved != n.Tool {
			resolved = fmt.Sprintf(" (using %s)", n.Resolved)
		}
		fmt.Fprintf(tw, "  %s\t%s%s\n", n.Tool, n.Label, resolved+version)
	}
	tw.Flush()

	fmt.Fprintf(w, "\nContext Pack: %d bytes (goal, evidence, capabilities, questions, constraints only)\n", p.ContextPackBytes)

	if len(p.Unresolved) > 0 {
		fmt.Fprintf(w, "\nUnresolved questions\n")
		for _, q := range p.Unresolved {
			fmt.Fprintf(w, "  - %s\n", q)
		}
	}

	if notes := p.Security.Notes; len(notes) > 0 {
		fmt.Fprintf(w, "\nSecurity\n")
		for _, n := range notes {
			fmt.Fprintf(w, "  - %s\n", n)
		}
	}

	fmt.Fprintf(w, "\nDraft capability manifest (%s, execution supported: %v)\n", p.Manifest.Schema, p.ManifestSupported)
	fmt.Fprintf(w, "%s", indent(p.Manifest.ContractText(), "  "))
	fmt.Fprintf(w, "\nThis command plans only. Use `grokinstall install` to create a capability.\n")
}

func newCompareCommand(g *globalFlags) *cobra.Command {
	var goal string
	cmd := &cobra.Command{
		Use:   "compare SOURCE --goal \"...\"",
		Short: "Compare integration strategies across twelve dimensions",
		Long: "Compares every candidate strategy on GrokBot footprint, feature coverage,\n" +
			"local execution, external cost, latency, maintenance, security, privacy,\n" +
			"cacheability, dependencies, implementation effort and portability.\n" +
			"Ratings are qualitative and evidence-based; no savings are invented.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			src, err := source.Normalize(args[0])
			if err != nil {
				return err
			}
			reg, err := g.state()
			if err != nil {
				return err
			}
			start := time.Now()
			p, err := plan.Build(context.Background(), plan.Options{
				Source:  *src,
				Goal:    goal,
				Store:   reg.Cache,
				Profile: toolchain.Detect(),
			})
			if err != nil {
				return err
			}
			_ = usage.Record(g.usageDir(reg), usage.Event{
				Op:             usage.OpCompare,
				Source:         src.Canonical,
				CacheHit:       p.Inspection.CacheHit,
				Strategy:       string(p.Comparison.Recommended),
				DurationMillis: time.Since(start).Milliseconds(),
			})
			if g.jsonOutput {
				return writeJSON(cmd, p.Comparison)
			}
			renderComparison(cmd.OutOrStdout(), p)
			return nil
		},
	}
	cmd.Flags().StringVar(&goal, "goal", "", "what GrokBot should be able to do with this source")
	return cmd
}

func renderComparison(w io.Writer, p *plan.Plan) {
	fmt.Fprintf(w, "Strategy comparison for %s\n", p.Source.Canonical)
	if p.Goal != "" {
		fmt.Fprintf(w, "Goal: %s\n\n", p.Goal)
	} else {
		fmt.Fprintln(w)
	}

	cmp := p.Comparison
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", "STRATEGY", "APPLICABLE", "FEASIBILITY", "SUPPORT")
	for _, c := range cmp.Candidates {
		mark := "yes"
		if !c.Applicable {
			mark = "no"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", c.Strategy, mark, c.Feasibility, c.Support)
	}
	tw.Flush()

	fmt.Fprintf(w, "\nRecommended: %s\n  %s\n", cmp.Recommended, cmp.RecommendationReason)

	fmt.Fprintf(w, "\nDimensions (qualitative, evidence-based)\n")
	for _, c := range cmp.Candidates {
		if !c.Applicable {
			continue
		}
		fmt.Fprintf(w, "\n  %s (%s)\n", c.Strategy, c.Support)
		fmt.Fprintf(w, "    %s\n", c.Reason)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, a := range c.Assessments {
			fmt.Fprintf(tw, "    %s\t%s\t%s\n", a.Dimension, a.Rating, a.Reason)
		}
		tw.Flush()
	}

	fmt.Fprintf(w, "\n  not applicable:\n")
	for _, c := range cmp.Candidates {
		if !c.Applicable {
			fmt.Fprintf(w, "    %s\t%s\n", c.Strategy, c.Reason)
		}
	}
}

func newDoctorCommand(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the local environment and state, with actionable fixes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := g.state()
			if err != nil {
				return err
			}
			rep := diagnostics.Run(reg.Root, toolchain.Detect())
			_ = usage.Record(g.usageDir(reg), usage.Event{Op: usage.OpDoctor})

			if g.jsonOutput {
				if err := writeJSON(cmd, rep); err != nil {
					return err
				}
			} else {
				renderDoctor(cmd.OutOrStdout(), rep)
			}
			if !rep.Healthy {
				return fmt.Errorf("doctor found problems that need fixing")
			}
			return nil
		},
	}
}

// renderDoctor groups output so action-required problems are impossible to
// miss, and optional tooling never looks like a failure.
func renderDoctor(w io.Writer, rep *diagnostics.Report) {
	fmt.Fprintf(w, "GrokInstall doctor\n")
	fmt.Fprintf(w, "State: %s\n\n", rep.StateDir)

	section := func(title string, want diagnostics.Status) {
		var rows []diagnostics.Check
		for _, c := range rep.Checks {
			if c.Status == want {
				rows = append(rows, c)
			}
		}
		if len(rows) == 0 {
			return
		}
		fmt.Fprintf(w, "%s\n", title)
		for _, c := range rows {
			fmt.Fprintf(w, "  %-24s %s\n", c.Name, c.Detail)
			if c.Action != "" {
				fmt.Fprintf(w, "  %-24s -> %s\n", "", c.Action)
			}
		}
		fmt.Fprintln(w)
	}

	section("CRITICAL (action required)", diagnostics.StatusFail)
	section("ACTION REQUIRED", diagnostics.StatusWarn)
	section("OK", diagnostics.StatusOK)
	fmt.Fprintf(w, "  %d ok, %d action required, %d failed\n", rep.Summary.OK, rep.Summary.Warn, rep.Summary.Fail)
}

func newUsageCommand(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "usage",
		Short: "Show what GrokInstall has actually done",
		Long: "Reports measured usage only: operations, cache hits and misses, context\n" +
			"pack sizes and contract sizes. No token savings are estimated.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := g.state()
			if err != nil {
				return err
			}
			sum, err := usage.Aggregate(g.usageDir(reg))
			if err != nil {
				return err
			}
			if g.jsonOutput {
				return writeJSON(cmd, sum)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Usage (measured)\n")
			fmt.Fprintf(cmd.OutOrStdout(), "  operations:        %d\n", sum.TotalEvents)
			fmt.Fprintf(cmd.OutOrStdout(), "  installs:          %d\n", sum.Installs)
			fmt.Fprintf(cmd.OutOrStdout(), "  runs:              %d\n", sum.Runs)
			fmt.Fprintf(cmd.OutOrStdout(), "  tests:             %d\n", sum.Tests)
			fmt.Fprintf(cmd.OutOrStdout(), "  uninstalls:        %d\n", sum.Uninstalls)
			fmt.Fprintf(cmd.OutOrStdout(), "  cache hits:        %d\n", sum.CacheHits)
			fmt.Fprintf(cmd.OutOrStdout(), "  cache misses:      %d\n", sum.CacheMisses)
			fmt.Fprintf(cmd.OutOrStdout(), "  worker calls:      %d\n", sum.WorkerInvocations)
			fmt.Fprintf(cmd.OutOrStdout(), "  context pack bytes: %d\n", sum.ContextPackBytes)
			fmt.Fprintf(cmd.OutOrStdout(), "  contract bytes:     %d\n", sum.GrokBotContractBytes)
			fmt.Fprintf(cmd.OutOrStdout(), "  capability input:   %d bytes\n", sum.CapabilityInputBytes)
			fmt.Fprintf(cmd.OutOrStdout(), "  capability output:  %d bytes\n", sum.CapabilityOutputBytes)
			keys := make([]string, 0, len(sum.ByOp))
			for k := range sum.ByOp {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				fmt.Fprintf(cmd.OutOrStdout(), "    %-16s %d\n", k, sum.ByOp[k])
			}
			return nil
		},
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n") + "\n"
}
