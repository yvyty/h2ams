package daemon

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/yvyty/h2ams/internal/config"
	"github.com/yvyty/h2ams/internal/logs"
	"github.com/yvyty/h2ams/internal/notify"
	"github.com/yvyty/h2ams/internal/process"
)

// maxSocketPath is the smallest sun_path limit across supported OSes (macOS:
// 104 bytes including the terminating NUL).
const maxSocketPath = 103

// Options configures a daemon run.
type Options struct {
	ConfigPath  string
	Environment string // empty uses the config's default environment
	Paths       Paths

	// DashboardAddr overrides the dashboard listen address (tests use
	// "127.0.0.1:0"). Empty means 127.0.0.1 on the configured port.
	DashboardAddr string
	// Log receives the daemon's own warnings; nil discards them.
	Log io.Writer
	// Notifier overrides desktop notifications (tests use a fake). It is
	// used only when notifications.crash_loop is enabled in the config.
	Notifier notify.Notifier
}

// Run starts the configured apps and serves requests until ctx is canceled or
// a down request arrives. All apps are stopped before Run returns.
func Run(ctx context.Context, opts Options) error {
	configPath, err := filepath.Abs(opts.ConfigPath)
	if err != nil {
		return err
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	names, err := cfg.AppsFor(opts.Environment)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(opts.Paths.Dir, 0o700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	// Record the project before listening: a socket accepts connections as
	// soon as it listens, and callers (h2ams-tray, KnownProjects) take a
	// reachable socket to mean the project is registered.
	if err := writeProject(opts.Paths, configPath); err != nil && opts.Log != nil {
		fmt.Fprintf(opts.Log, "%s recording project for h2ams-tray failed: %v\n", time.Now().Format(time.RFC3339), err)
	}
	ln, err := listen(opts.Paths.Socket)
	if err != nil {
		return err
	}

	logFiles, err := openLogs(opts.Paths, names)
	if err != nil {
		ln.Close()
		return err
	}
	defer closeAll(logFiles)
	project := filepath.Base(opts.Paths.Dir)
	specs := Specs(cfg, filepath.Dir(configPath), project, names, func(app string) io.Writer { return logFiles[app] })

	mgr := process.NewManager()
	if err := mgr.Start(specs); err != nil {
		ln.Close()
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	logf := func(format string, args ...any) {
		if opts.Log != nil {
			fmt.Fprintf(opts.Log, "%s "+format+"\n", append([]any{time.Now().Format(time.RFC3339)}, args...)...)
		}
	}

	svc := &service{
		mgr:   mgr,
		paths: opts.Paths,
		info: Response{
			PID:         os.Getpid(),
			ConfigPath:  configPath,
			Socket:      opts.Paths.Socket,
			Environment: cfg.ResolveEnvironment(opts.Environment),
		},
		containers: dockerApps(cfg, project, names),
	}

	var wg sync.WaitGroup
	var notifier notify.Notifier
	if cfg.Notifications.CrashLoop {
		notifier = opts.Notifier
		if notifier == nil {
			notifier = notify.Desktop()
		}
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		svc.monitor(ctx, notifier, logf)
	}()

	a := &api{base: ctx, svc: svc, shutdown: cancel}
	if cfg.Dashboard.IsEnabled() {
		addr := opts.DashboardAddr
		if addr == "" {
			addr = fmt.Sprintf("127.0.0.1:%d", cfg.Dashboard.Port)
		}
		url, err := startDashboard(ctx, &wg, a, addr)
		if err != nil {
			// Another project's daemon may hold the port; keep supervising.
			logf("dashboard disabled: %v (set dashboard.port in the config)", err)
		} else {
			svc.info.Dashboard = url
			logf("dashboard listening on %s", url)
		}
	}

	// Serve the socket last: connections made since listen wait in the
	// backlog, so every request sees the final svc.info.
	serveHTTP(ctx, &wg, ln, a.socketHandler())

	<-ctx.Done()
	ln.Close()
	mgr.StopAll()
	wg.Wait()
	return nil
}

// openLogs opens (and rotates if needed) one append-only log per app. The
// files persist across restarts, so output survives an app crash.
func openLogs(paths Paths, names []string) (map[string]*os.File, error) {
	files := make(map[string]*os.File, len(names))
	for _, name := range names {
		f, err := logs.Open(paths.AppLog(name))
		if err != nil {
			closeAll(files)
			return nil, fmt.Errorf("opening log for %s: %w", name, err)
		}
		files[name] = f
	}
	return files, nil
}

func closeAll(files map[string]*os.File) {
	for _, f := range files {
		f.Close()
	}
}

// Specs turns config apps into process specs. Relative cwds resolve against
// baseDir (the config file's directory). project names Docker containers.
// output returns where each app's stdout, stderr, and lifecycle events go;
// nil discards them.
func Specs(cfg *config.Config, baseDir, project string, names []string, output func(app string) io.Writer) []process.Spec {
	specs := make([]process.Spec, 0, len(names))
	for _, name := range names {
		app := cfg.Apps[name]
		var out io.Writer
		if output != nil {
			out = output(name)
		}
		dir := app.Cwd
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(baseDir, dir)
		}
		spec := process.Spec{
			Name:        name,
			Command:     app.Command,
			Dir:         dir,
			Env:         mergeEnv(os.Environ(), app.Env),
			Policy:      app.Restart.Policy,
			MaxAttempts: app.Restart.MaxAttempts,
			Backoff:     time.Duration(app.Restart.Backoff),
			Stdout:      out,
			Stderr:      out,
			Events:      out,
		}
		if app.Docker != nil {
			container := containerName(project, name)
			spec.Command = dockerRunCommand(project, container, dir, app)
			spec.Env = os.Environ() // app env goes into the container via -e
			spec.Cleanup = func() { removeContainer(container) }
			spec.Precheck = checkDocker
		}
		specs = append(specs, spec)
	}
	return specs
}

func mergeEnv(base []string, extra map[string]string) []string {
	keys := make([]string, 0, len(extra))
	for k := range extra {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	env := append([]string(nil), base...)
	for _, k := range keys {
		env = append(env, k+"="+extra[k]) // later entries win in os/exec
	}
	return env
}

// listen binds the socket, refusing if another daemon is live and clearing a
// stale socket file left by one that crashed.
func listen(socket string) (net.Listener, error) {
	if len(socket) > maxSocketPath {
		return nil, fmt.Errorf("socket path is %d bytes, over the %d-byte OS limit: %s (set %s to a shorter directory)",
			len(socket), maxSocketPath, socket, StateDirEnv)
	}
	if Running(socket) {
		return nil, fmt.Errorf("h2ams is already running (socket %s)", socket)
	}
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("removing stale socket: %w", err)
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return nil, fmt.Errorf("listening on %s: %w", socket, err)
	}
	return ln, nil
}
