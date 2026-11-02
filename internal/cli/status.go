package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/yvyty/h2ams/internal/daemon"
	"github.com/yvyty/h2ams/internal/process"
)

const downTimeout = time.Minute

func newStatusCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the state of every managed app",
		Long: `Show each app's state, PID, listening TCP ports, restart count, and uptime.

States:
  starting    launched, not yet confirmed running
  running     up and supervised
  restarting  exited and waiting (with backoff) to be started again
  stopped     exited cleanly, or stopped by "h2ams down"
  failed      crashed and will not be restarted (policy or max_attempts)

DETAIL shows the last exit error. Ports are detected on macOS and Linux.`,
		Example: "  h2ams status\n  h2ams status --config path/to/h2ams.yml",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, paths, err := opts.resolve()
			if err != nil {
				return err
			}
			resp, err := daemon.Call(paths.Socket, daemon.Request{Cmd: daemon.CmdStatus}, 5*time.Second)
			if errors.Is(err, daemon.ErrNotRunning) {
				fmt.Fprintln(cmd.OutOrStdout(), daemon.ErrNotRunning)
				return nil
			}
			if err != nil {
				return err
			}
			printStatus(cmd.OutOrStdout(), resp)
			return nil
		},
	}
}

func newDownCmd(opts *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "down",
		Short: "Stop every app and the daemon",
		Long: "Stop every app (SIGTERM to its whole process group, then SIGKILL after 5s)\n" +
			"and shut down the daemon. Logs are kept; see \"h2ams logs\".",
		Example: "  h2ams down",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, paths, err := opts.resolve()
			if err != nil {
				return err
			}
			resp, err := daemon.Call(paths.Socket, daemon.Request{Cmd: daemon.CmdDown}, downTimeout)
			if errors.Is(err, daemon.ErrNotRunning) {
				fmt.Fprintln(cmd.OutOrStdout(), daemon.ErrNotRunning)
				return nil
			}
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Stopped %d app(s) and the h2ams daemon\n", len(resp.Apps))
			return nil
		},
	}
}

func printStatus(w io.Writer, resp daemon.Response) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "APP\tSTATE\tPID\tPORTS\tRESTARTS\tUPTIME\tDETAIL")
	for _, app := range resp.Apps {
		pid, uptime := "-", "-"
		if app.PID != 0 {
			pid = strconv.Itoa(app.PID)
		}
		if app.State == process.StateRunning && !app.StartedAt.IsZero() {
			uptime = time.Since(app.StartedAt).Round(time.Second).String()
		}
		detail := app.Error
		if detail == "" {
			detail = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			app.Name, app.State, pid, formatPorts(app.Ports), app.Restarts, uptime, detail)
	}
	tw.Flush()
}

func formatPorts(ports []int) string {
	if len(ports) == 0 {
		return "-"
	}
	parts := make([]string, len(ports))
	for i, p := range ports {
		parts[i] = strconv.Itoa(p)
	}
	return strings.Join(parts, ",")
}
