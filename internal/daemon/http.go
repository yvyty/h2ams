package daemon

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/yvyty/h2ams/internal/logs"
	"github.com/yvyty/h2ams/internal/process"
)

//go:embed dashboard.html
var dashboardHTML []byte

const (
	defaultLogLines   = 200
	maxLogLines       = 5000
	logFollowInterval = 200 * time.Millisecond
)

// startDashboard serves the web UI on addr until ctx is done and returns its
// URL. It only binds loopback addresses: the API can stop and start processes.
func startDashboard(ctx context.Context, wg *sync.WaitGroup, svc *service, addr string) (string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("dashboard address %s is not a loopback IP", addr)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", err
	}
	port := ln.Addr().(*net.TCPAddr).Port

	srv := &http.Server{
		Handler:           newDashboardHandler(ctx, svc, port),
		ReadHeaderTimeout: 5 * time.Second,
	}
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = srv.Serve(ln)
	}()
	go func() {
		defer wg.Done()
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	return fmt.Sprintf("http://127.0.0.1:%d", port), nil
}

// newDashboardHandler builds the dashboard's routes. base ends log streams
// when the daemon shuts down; port is the port actually being served.
func newDashboardHandler(base context.Context, svc *service, port int) http.Handler {
	d := &dashboard{base: base, svc: svc}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", d.index)
	mux.HandleFunc("GET /api/status", d.status)
	mux.HandleFunc("POST /api/apps/{app}/{action}", d.action)
	mux.HandleFunc("GET /api/apps/{app}/logs", d.logs)
	return guard(mux, port)
}

type dashboard struct {
	base context.Context
	svc  *service
}

func (d *dashboard) index(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'")
	_, _ = w.Write(dashboardHTML)
}

func (d *dashboard) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.svc.status())
}

func (d *dashboard) action(w http.ResponseWriter, r *http.Request) {
	app, action := r.PathValue("app"), r.PathValue("action")
	err := d.svc.act(action, app)
	resp := d.svc.status()
	code := http.StatusOK
	if err != nil {
		resp.Error = err.Error()
		code = actionErrorCode(err)
	}
	writeJSON(w, code, resp)
}

func actionErrorCode(err error) int {
	switch {
	case errors.Is(err, process.ErrUnknownApp), errors.Is(err, errBadAction):
		return http.StatusNotFound
	case errors.Is(err, process.ErrAlreadyRunning), errors.Is(err, process.ErrNotRunning):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// logs streams an app's log over a WebSocket: the last ?lines= lines (default
// 200) and then everything appended, across restarts, until either side
// closes. Messages are binary so log bytes need not be valid UTF-8.
func (d *dashboard) logs(w http.ResponseWriter, r *http.Request) {
	app := r.PathValue("app")
	if !d.svc.hasApp(app) {
		writeJSON(w, http.StatusNotFound, Response{Error: fmt.Sprintf("unknown app %q", app)})
		return
	}
	lines := defaultLogLines
	if v := r.URL.Query().Get("lines"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeJSON(w, http.StatusBadRequest, Response{Error: "lines must be a non-negative integer"})
			return
		}
		lines = min(n, maxLogLines)
	}

	// Accept rejects cross-origin handshakes by default.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(conn.CloseRead(r.Context()))
	defer cancel()
	stop := context.AfterFunc(d.base, cancel)
	defer stop()

	out := &wsWriter{ctx: ctx, conn: conn}
	path := d.svc.paths.AppLog(app)
	offset, err := logs.Tail(out, path, lines)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		conn.Close(websocket.StatusInternalError, "reading log failed")
		return
	}
	if err := logs.Follow(ctx, out, path, offset, logFollowInterval); err != nil && ctx.Err() == nil {
		conn.Close(websocket.StatusInternalError, "following log failed")
		return
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

// wsWriter sends each Write as one binary WebSocket message.
type wsWriter struct {
	ctx  context.Context
	conn *websocket.Conn
}

func (w *wsWriter) Write(p []byte) (int, error) {
	if err := w.conn.Write(w.ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

// guard protects the dashboard from other sites in the user's browser:
//   - the Host header must name loopback on our port, which defeats DNS
//     rebinding (evil.example resolving to 127.0.0.1);
//   - state-changing requests must come from our own origin, which defeats
//     cross-site form posts.
func guard(next http.Handler, port int) http.Handler {
	p := strconv.Itoa(port)
	allowed := map[string]bool{
		"127.0.0.1:" + p: true,
		"localhost:" + p: true,
		"[::1]:" + p:     true,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !allowed[r.Host] {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !allowed[u.Host] {
					http.Error(w, "forbidden origin", http.StatusForbidden)
					return
				}
			}
		}
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
