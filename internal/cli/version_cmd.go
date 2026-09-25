package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newVersionCommand(g *globalFlags) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		Short:   "Show version, commit and build information",
		GroupID: "project",
		Example: "  grokinstall version --json",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			info := Info()
			if g.jsonOutput {
				return writeJSON(cmd, info)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "grokinstall %s\n", info.Version)
			if info.Commit != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "commit:     %s\n", info.Commit)
			}
			if info.BuildDate != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "built:     %s\n", info.BuildDate)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "go:        %s\n", info.GoVersion)
			fmt.Fprintf(cmd.OutOrStdout(), "platform:  %s\n", info.Platform)
			if info.Development {
				fmt.Fprintf(cmd.OutOrStdout(), "\ndevelopment build: not a release artifact\n")
			}
			return nil
		},
	}
}
