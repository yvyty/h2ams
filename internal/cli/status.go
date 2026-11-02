package cli

import (
	"errors"
	"fmt"
	"io"
	"strconv"
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
		Args:  cobra.NoArgs,
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
		Args:  cobra.NoArgs,
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
	fmt.Fprintln(tw, "APP\tSTATE\tPID\tRESTARTS\tUPTIME\tDETAIL")
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
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", app.Name, app.State, pid, app.Restarts, uptime, detail)
	}
	tw.Flush()
}
