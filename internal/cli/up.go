package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/yvyty/h2ams/internal/config"
	"github.com/yvyty/h2ams/internal/daemon"
)

const readyTimeout = 5 * time.Second

func newUpCmd(opts *rootOptions) *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:   "up",
		Short: "Start the daemon and every app in an environment",
		Long: "Validate the config, start the h2ams daemon in the background, and launch\n" +
			"every app in the environment. Without --env, up uses default_environment\n" +
			"from the config, or every app if none is set.\n\n" +
			"The daemon keeps running after this command returns. It restarts apps\n" +
			"according to their restart policy until you run \"h2ams down\".",
		Example: "  h2ams up\n" +
			"  h2ams up --env dev\n" +
			"  h2ams up --config path/to/h2ams.yml",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgPath, paths, err := opts.resolve()
			if err != nil {
				return err
			}
			// Validate in the foreground so errors reach the user, not the log.
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			if _, err := cfg.AppsFor(env); err != nil {
				return err
			}
			if daemon.Running(paths.Socket) {
				return errors.New(`h2ams is already running; see "h2ams status" or run "h2ams down"`)
			}

			exited, err := spawnDaemon(cfgPath, env, paths)
			if err != nil {
				return err
			}
			resp, err := waitReady(paths, exited)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "h2ams started (pid %d, log %s)\n\n", resp.PID, paths.Log)
			printStatus(cmd.OutOrStdout(), resp)
			return nil
		},
	}
	cmd.Flags().StringVarP(&env, "env", "e", "", "environment to start")
	return cmd
}

// spawnDaemon re-executes this binary as a detached `h2ams daemon`.
func spawnDaemon(cfgPath, env string, paths daemon.Paths) (<-chan error, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(paths.Log, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening daemon log: %w", err)
	}
	defer logFile.Close()

	c := exec.Command(exe, "daemon", "--config", cfgPath, "--env", env)
	c.Stdout = logFile
	c.Stderr = logFile
	c.SysProcAttr = detachAttr()
	if err := c.Start(); err != nil {
		return nil, fmt.Errorf("starting daemon: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- c.Wait() }()
	return exited, nil
}

// waitReady polls the daemon until it answers or exits.
func waitReady(paths daemon.Paths, exited <-chan error) (daemon.Response, error) {
	deadline := time.After(readyTimeout)
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			return daemon.Response{}, fmt.Errorf("daemon exited during startup (%v); see %s", err, paths.Log)
		case <-deadline:
			return daemon.Response{}, fmt.Errorf("daemon did not become ready within %v; see %s", readyTimeout, paths.Log)
		case <-tick.C:
			if resp, err := daemon.Call(paths.Socket, daemon.Request{Cmd: daemon.CmdStatus}, time.Second); err == nil {
				return resp, nil
			}
		}
	}
}

// newDaemonCmd is the hidden foreground supervisor that `up` launches.
func newDaemonCmd(opts *rootOptions) *cobra.Command {
	var env string
	cmd := &cobra.Command{
		Use:    "daemon",
		Short:  "Run the supervisor in the foreground (used by up)",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfgPath, paths, err := opts.resolve()
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			log := cmd.OutOrStdout()
			fmt.Fprintf(log, "%s daemon starting (pid %d, config %s)\n", time.Now().Format(time.RFC3339), os.Getpid(), cfgPath)
			err = daemon.Run(ctx, daemon.Options{
				ConfigPath:  cfgPath,
				Environment: env,
				Paths:       paths,
				Log:         log,
			})
			fmt.Fprintf(log, "%s daemon stopped\n", time.Now().Format(time.RFC3339))
			return err
		},
	}
	cmd.Flags().StringVarP(&env, "env", "e", "", "environment to start")
	return cmd
}
