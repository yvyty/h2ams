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

// Version is the build version, set at link time:
//
//	go build -ldflags "-X github.com/yvyty/h2ams/internal/cli.Version=v0.1.0" ./cmd/h2ams
var Version = "dev"

// NewRootCmd builds the h2ams command tree.
func NewRootCmd() *cobra.Command {
	opts := &rootOptions{}
	root := &cobra.Command{
		Use:   "h2ams",
		Short: "Define, run, and supervise local apps from one YAML file",
		Long: `h2ams runs the apps you develop against (APIs, frontends, workers) from one
h2ams.yml instead of a pile of terminal tabs. A background daemon keeps them
running, restarts them when they crash, and shows which ports they use.

Typical workflow:
  h2ams init           create a template h2ams.yml
  h2ams up             start the daemon and your apps
  h2ams status         see each app's state, PID, and ports
  h2ams logs <app> -f  stream an app's output
  h2ams down           stop everything

Each config file gets its own daemon, so several projects can run at once.`,
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&opts.configPath, "config", "c", config.DefaultFileName, "path to the config file")
	root.AddCommand(
		newInitCmd(opts),
		newUpCmd(opts),
		newStatusCmd(opts),
		newDownCmd(opts),
		newLogsCmd(opts),
		newEnvsCmd(opts),
		newValidateCmd(opts),
		newAppActionCmd(opts, daemon.CmdStart, "Start a stopped, completed, or failed app",
			"Start an app that is stopped, completed, or failed. Its restart count starts over."),
		newAppActionCmd(opts, daemon.CmdStop, "Stop one app",
			"Stop one app and keep it stopped (no automatic restarts) until\n\"h2ams start\" or \"h2ams restart\". The daemon and other apps keep running."),
		newAppActionCmd(opts, daemon.CmdRestart, "Restart one app",
			"Stop an app if it is running, then start it again with a fresh restart count."),
		newDaemonCmd(opts),
	)
	return root
}
