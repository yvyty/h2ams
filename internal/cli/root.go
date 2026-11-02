// Package cli wires up the h2ams command-line interface.
package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCmd builds the h2ams command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "h2ams",
		Short:         "Define, run, and supervise local apps from one YAML file",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newInitCmd())
	return root
}
