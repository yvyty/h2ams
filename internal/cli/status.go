package cli

import (
	"encoding/json"
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
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show the state of every managed app",
		Long: `Show the active environment, the dashboard URL, and each app's state, PID,
listening TCP ports, CPU and memory use, restart count, and uptime.

States:
  starting    launched, not yet confirmed running
  running     up and supervised
  restarting  exited and waiting (with backoff) to be started again
  stopped     exited cleanly, or stopped by "h2ams down"
  failed      crashed and will not be restarted (policy or max_attempts)

CPU is percent of one core and MEM is resident memory, both summed over the
app and every process it started, sampled every 2 seconds. DETAIL shows the
last exit error. Ports, CPU, and MEM are measured on macOS and Linux.`,
		Example: "  h2ams status\n  h2ams status --json\n  h2ams status --config path/to/h2ams.yml",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, paths, err := opts.resolve()
			if err != nil {
				return err
			}
			resp, err := daemon.Call(paths.Socket, daemon.Request{Cmd: daemon.CmdStatus}, 5*time.Second)
			if errors.Is(err, daemon.ErrNotRunning) {
				if asJSON {
					return err // scripts get a non-zero exit, not prose on stdout
				}
				fmt.Fprintln(cmd.OutOrStdout(), daemon.ErrNotRunning)
				return nil
			}
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(resp)
			}
			printStatus(cmd.OutOrStdout(), resp)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the API's status JSON (see docs/api.md)")
	return cmd
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
	env := resp.Environment
	if env == "" {
		env = "(all apps)"
	}
	fmt.Fprintf(w, "Environment: %s\n", env)
	if resp.Dashboard != "" {
		fmt.Fprintf(w, "Dashboard:   %s\n", resp.Dashboard)
	}
	fmt.Fprintln(w)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "APP\tSTATE\tPID\tPORTS\tCPU\tMEM\tRESTARTS\tUPTIME\tDETAIL")
	for _, app := range resp.Apps {
		pid, uptime := "-", "-"
		if app.PID != 0 {
			pid = strconv.Itoa(app.PID)
		}
		if app.State == process.StateRunning && !app.StartedAt.IsZero() {
			uptime = time.Since(app.StartedAt).Round(time.Second).String()
		}
		detail := app.Error
		if detail == "" && app.Container != nil {
			state := app.Container.State
			if state == "" {
				state = "absent"
			}
			detail = fmt.Sprintf("container %s (%s)", state, app.Container.Image)
		}
		if detail == "" {
			detail = "-"
		}
		cpu, mem := "-", "-"
		if app.CPUPercent != nil {
			cpu = fmt.Sprintf("%.1f%%", *app.CPUPercent)
		}
		if app.MemoryBytes > 0 {
			mem = formatBytes(app.MemoryBytes)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%d\t%s\t%s\n",
			app.Name, app.State, pid, formatPorts(app.Ports), cpu, mem, app.Restarts, uptime, detail)
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

// formatBytes renders a byte count with a binary unit, e.g. "12.3 MiB".
func formatBytes(n uint64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := uint64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
