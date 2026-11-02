package cli

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/yvyty/h2ams/internal/config"
	"github.com/yvyty/h2ams/internal/daemon"
)

func newEnvsCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "envs",
		Short: "List the environments in the config",
		Long: "List each environment and its apps. The default environment (started by\n" +
			"\"h2ams up\" without --env) is marked with *, and the one the running\n" +
			"daemon was started with is marked active.",
		Example: "  h2ams envs\n  h2ams up --env testing",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgPath, paths, err := opts.resolve()
			if err != nil {
				return err
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			active, running := "", false
			if resp, err := daemon.Call(paths.Socket, daemon.Request{Cmd: daemon.CmdStatus}, 2*time.Second); err == nil {
				active, running = resp.Environment, true
			}
			printEnvs(cmd.OutOrStdout(), cfg, active, running)
			return nil
		},
	}
}

func printEnvs(w io.Writer, cfg *config.Config, active string, running bool) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ENVIRONMENT\tAPPS\tSTATUS")
	names := make([]string, 0, len(cfg.Environments))
	for name := range cfg.Environments {
		names = append(names, name)
	}
	sort.Strings(names)

	row := func(name, label string, apps []string) {
		var marks []string
		if name == cfg.DefaultEnvironment {
			marks = append(marks, "default")
		}
		if running && name == active {
			marks = append(marks, "active")
		}
		status := strings.Join(marks, ", ")
		if status == "" {
			status = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", label, strings.Join(apps, ", "), status)
	}
	for _, name := range names {
		label := name
		if name == cfg.DefaultEnvironment {
			label += " *"
		}
		row(name, label, cfg.Environments[name].Apps)
	}
	if cfg.DefaultEnvironment == "" {
		all, _ := cfg.AppsFor("")
		row("", "(all apps) *", all)
	}
	tw.Flush()
}
