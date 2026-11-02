package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/yvyty/h2ams/internal/config"
	"github.com/yvyty/h2ams/internal/logs"
)

const followInterval = 200 * time.Millisecond

func newLogsCmd(opts *rootOptions) *cobra.Command {
	var (
		lines  int
		follow bool
	)
	cmd := &cobra.Command{
		Use:   "logs <app>",
		Short: "Show an app's output (stdout, stderr, and restarts)",
		Long: "Show the last lines of an app's output. Logs are kept on disk across\n" +
			"restarts and after \"h2ams down\". Use -f to keep streaming new output.",
		Example: "  h2ams logs api\n" +
			"  h2ams logs api -n 20\n" +
			"  h2ams logs api -f",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			app := args[0]
			cfgPath, paths, err := opts.resolve()
			if err != nil {
				return err
			}
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			if _, ok := cfg.Apps[app]; !ok {
				return fmt.Errorf("unknown app %q", app)
			}

			out := cmd.OutOrStdout()
			path := paths.AppLog(app)
			offset, err := logs.Tail(out, path, lines)
			if errors.Is(err, fs.ErrNotExist) {
				if !follow {
					fmt.Fprintf(cmd.ErrOrStderr(), "no logs yet for %s\n", app)
					return nil
				}
				offset, err = 0, nil
			}
			if err != nil || !follow {
				return err
			}

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			return logs.Follow(ctx, out, path, offset, followInterval)
		},
	}
	cmd.Flags().IntVarP(&lines, "lines", "n", 100, "number of lines to show (0 for all)")
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep streaming new output")
	return cmd
}
