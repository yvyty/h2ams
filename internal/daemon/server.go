package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/yvyty/h2ams/internal/config"
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
	AppOutput   io.Writer // where app stdout/stderr go; nil discards
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
	specs := Specs(cfg, filepath.Dir(configPath), names, opts.AppOutput)

	if err := os.MkdirAll(opts.Paths.Dir, 0o700); err != nil {
		return fmt.Errorf("creating state directory: %w", err)
	}
	ln, err := listen(opts.Paths.Socket)
	if err != nil {
		return err
	}

	mgr := process.NewManager()
	if err := mgr.Start(specs); err != nil {
		ln.Close()
		return err
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &server{
		mgr:    mgr,
		cancel: cancel,
		info: Response{
			PID:         os.Getpid(),
			ConfigPath:  configPath,
			Environment: opts.Environment,
		},
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.serve(ln, &wg)
	}()

	<-ctx.Done()
	ln.Close()
	mgr.StopAll()
	wg.Wait()
	return nil
}

// Specs turns config apps into process specs. Relative cwds resolve against
// baseDir (the config file's directory).
func Specs(cfg *config.Config, baseDir string, names []string, out io.Writer) []process.Spec {
	specs := make([]process.Spec, 0, len(names))
	for _, name := range names {
		app := cfg.Apps[name]
		dir := app.Cwd
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(baseDir, dir)
		}
		specs = append(specs, process.Spec{
			Name:        name,
			Command:     app.Command,
			Dir:         dir,
			Env:         mergeEnv(os.Environ(), app.Env),
			Policy:      app.Restart.Policy,
			MaxAttempts: app.Restart.MaxAttempts,
			Backoff:     time.Duration(app.Restart.Backoff),
			Stdout:      out,
			Stderr:      out,
		})
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

type server struct {
	mgr      *process.Manager
	cancel   context.CancelFunc
	info     Response
	downOnce sync.Once
}

func (s *server) serve(ln net.Listener, wg *sync.WaitGroup) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.handle(conn)
		}()
	}
}

func (s *server) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))

	var req Request
	resp := s.info
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		resp.Error = "invalid request: " + err.Error()
		_ = json.NewEncoder(conn).Encode(resp)
		return
	}

	switch req.Cmd {
	case CmdStatus:
		resp.Apps = s.mgr.Statuses()
	case CmdDown:
		s.downOnce.Do(s.mgr.StopAll)
		resp.Apps = s.mgr.Statuses()
		defer s.cancel() // shut down after the reply is written
	default:
		resp.Error = fmt.Sprintf("unknown command %q", req.Cmd)
	}
	_ = json.NewEncoder(conn).Encode(resp)
}
