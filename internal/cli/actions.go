package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/yvyty/h2ams/internal/daemon"
)

// actionTimeout covers a graceful stop (5s) plus slack.
const actionTimeout = 30 * time.Second

// newAppActionCmd builds `h2ams start|stop|restart <app>`. The dashboard's
// buttons send the same daemon requests.
func newAppActionCmd(opts *rootOptions, action, short, long string) *cobra.Command {
	return &cobra.Command{
		Use:     action + " <app>",
		Short:   short,
		Long:    long,
		Example: fmt.Sprintf("  h2ams %s api", action),
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, paths, err := opts.resolve()
			if err != nil {
				return err
			}
			resp, err := daemon.Call(paths.Socket, daemon.Request{Cmd: action, App: args[0]}, actionTimeout)
			if errors.Is(err, daemon.ErrNotRunning) {
				return fmt.Errorf(`%w; run "h2ams up" first`, daemon.ErrNotRunning)
			}
			if err != nil {
				return err
			}
			printStatus(cmd.OutOrStdout(), resp)
			return nil
		},
	}
}
