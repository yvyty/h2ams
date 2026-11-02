// Package cli wires up the h2ams command-line interface.
package cli

import (
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/yvyty/h2ams/internal/config"
	"github.com/yvyty/h2ams/internal/daemon"
)

// rootOptions holds flags shared by every subcommand.
type rootOptions struct {
	configPath string
}

// resolve returns the absolute config path and the daemon paths for it.
func (o *rootOptions) resolve() (string, daemon.Paths, error) {
	abs, err := filepath.Abs(o.configPath)
	if err != nil {
		return "", daemon.Paths{}, err
	}
	paths, err := daemon.PathsFor(abs)
	return abs, paths, err
}

// NewRootCmd builds the h2ams command tree.
func NewRootCmd() *cobra.Command {
	opts := &rootOptions{}
	root := &cobra.Command{
		Use:           "h2ams",
		Short:         "Define, run, and supervise local apps from one YAML file",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&opts.configPath, "config", "c", config.DefaultFileName, "path to the config file")
	root.AddCommand(
		newInitCmd(),
		newUpCmd(opts),
		newStatusCmd(opts),
		newDownCmd(opts),
		newDaemonCmd(opts),
	)
	return root
}
