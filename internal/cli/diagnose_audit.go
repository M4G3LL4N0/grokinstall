package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"grokinstall/internal/audit"
	"grokinstall/internal/diagnose"
	"grokinstall/internal/installer"
	"grokinstall/internal/provision"
	"grokinstall/internal/registry"
	"grokinstall/internal/usage"
)

func newDiagnoseCommand(g *globalFlags) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "diagnose [NAME]",
		Short: "Explain why a capability is not working, using direct evidence",
		Long: "Reports the symptom, the evidence that was observed, the probable root\n" +
			"cause, a confidence level, the affected component, the smallest fix and the\n" +
			"command that verifies the fix.\n\n" +
			"With no argument, every installed capability is diagnosed.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := g.state()
			if err != nil {
				return err
			}
			engine := diagnose.New(reg)
			start := time.Now()

			var reports []diagnose.Report
			if all || len(args) == 0 {
				list, derr := engine.All()
				if derr != nil {
					return derr
				}
				reports = list
			} else {
				rep, derr := engine.One(args[0])
				if derr != nil {
					return derr
				}
				reports = []diagnose.Report{*rep}
			}
			g.recordUsage(reg, usage.Event{
				Op: usage.OpDiagnose, Capability: strings.Join(args, ","),
				Outcome: outcomeOf(allHealthy(reports)), DurationMillis: time.Since(start).Milliseconds(),
			})

			if g.jsonOutput {
				payload := map[string]any{"reports": reports}
				if err := writeJSON(cmd, payload); err != nil {
					return err
				}
			} else {
				for i := range reports {
					if i > 0 {
						fmt.Fprintln(cmd.OutOrStdout())
					}
					reports[i].Render(cmd.OutOrStdout())
				}
			}
			if !allHealthy(reports) {
				return fmt.Errorf("diagnosis found problems")
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "diagnose every installed capability")
	return cmd
}

func allHealthy(reports []diagnose.Report) bool {
	for _, r := range reports {
		if !r.Healthy {
			return false
		}
	}
	return true
}

func newAuditCommand(g *globalFlags) *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "audit [NAME]",
		Short: "Inspect an installed integration and report evidence-backed findings",
		Long: "A deterministic review of one integration: manifest validity, receipt\n" +
			"consistency, runtime ownership and integrity, source identity, permissions,\n" +
			"security approvals, checksum provenance, adapter integrity and the GrokBot\n" +
			"contract. It does not execute the capability and does not inflate severity.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			reg, err := g.state()
			if err != nil {
				return err
			}
			engine := audit.New(reg)

			var reports []audit.Report
			if all || len(args) == 0 {
				entries, lerr := reg.List()
				if lerr != nil {
					return lerr
				}
				for _, e := range entries {
					rep, aerr := engine.One(e.Name)
					if aerr != nil {
						continue
					}
					reports = append(reports, *rep)
				}
			} else {
				rep, aerr := engine.One(args[0])
				if aerr != nil {
					return aerr
				}
				reports = []audit.Report{*rep}
			}
			g.recordUsage(reg, usage.Event{Op: usage.OpAudit, Capability: strings.Join(args, ",")})

			if g.jsonOutput {
				if err := writeJSON(cmd, map[string]any{"reports": reports}); err != nil {
					return err
				}
			} else {
				for i := range reports {
					if i > 0 {
						fmt.Fprintln(cmd.OutOrStdout())
					}
					reports[i].Render(cmd.OutOrStdout())
				}
			}
			for _, r := range reports {
				if !r.Passed {
					return fmt.Errorf("audit failed for %s", r.Capability)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "audit every installed capability")
	return cmd
}

// renderRefusal prints a structured provisioning refusal.
func renderRefusal(w io.Writer, r *provision.Refusal) {
	if r == nil {
		return
	}
	r.Explain(w)
}

// renderProvisioningCandidates shows the comparison GrokInstall made.
func renderProvisioningCandidates(w io.Writer, candidates []provision.Candidate, selected provision.Method) {
	if len(candidates) == 0 {
		return
	}
	fmt.Fprintf(w, "\nPROVISIONING CANDIDATES\n")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, c := range candidates {
		mark := ""
		if c.Method == selected {
			mark = "  <- selected"
		}
		fmt.Fprintf(tw, "  %-24s %s%s\n", c.Method, c.Summary(), mark)
	}
	tw.Flush()
}

// capabilityStates filters capabilities for discovery output.
func capabilityStates(caps []installer.CapabilitySummary, state string) []installer.CapabilitySummary {
	if state == "" {
		return caps
	}
	var out []installer.CapabilitySummary
	for _, c := range caps {
		if c.State == state {
			out = append(out, c)
		}
	}
	return out
}

var _ = json.Marshal
var _ registry.Entry
