package daemon

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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

// serveHTTP serves handler on ln until ctx is done, then shuts down
// gracefully. Long-lived log streams end via ctx too.
func serveHTTP(ctx context.Context, wg *sync.WaitGroup, ln net.Listener, handler http.Handler) {
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
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
		if srv.Shutdown(shutdownCtx) != nil {
			srv.Close()
		}
	}()
}

// startDashboard serves the web UI and API on addr until ctx is done and
// returns its URL. It only binds loopback addresses, because the API can stop
// and start processes.
func startDashboard(ctx context.Context, wg *sync.WaitGroup, a *api, addr string) (string, error) {
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
	serveHTTP(ctx, wg, ln, a.dashboardHandler(port))
	return fmt.Sprintf("http://127.0.0.1:%d", port), nil
}

// api implements the versioned HTTP API (see docs/api.md). The same handlers
// serve the Unix socket (CLI, plugins) and the dashboard port (browser).
type api struct {
	base     context.Context // ends log streams when the daemon stops
	svc      *service
	shutdown func() // stops the daemon after a down request
}

const prefix = "/api/" + APIVersion

// routes registers the API on mux.
func (a *api) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+prefix+"/status", a.status)
	mux.HandleFunc("POST "+prefix+"/apps/{app}/{action}", a.action)
	mux.HandleFunc("GET "+prefix+"/apps/{app}/logs", a.logsText)
	mux.HandleFunc("GET "+prefix+"/apps/{app}/logs/ws", a.logsWebSocket)
	mux.HandleFunc("POST "+prefix+"/down", a.down)
	mux.HandleFunc(catchAll, func(w http.ResponseWriter, r *http.Request) {
		if allow := allowedMethod(mux, r); allow != "" {
			w.Header().Set("Allow", allow)
			writeError(w, http.StatusMethodNotAllowed, fmt.Sprintf("%s %s: use %s", r.Method, r.URL.Path, allow))
			return
		}
		writeError(w, http.StatusNotFound, fmt.Sprintf("no endpoint %s %s", r.Method, r.URL.Path))
	})
}

// catchAll answers unknown /api/ paths with JSON instead of the default page.
const catchAll = "/api/"

// allowedMethod returns the other method that path supports, if any, so a
// wrong method gets 405 rather than falling through to the catch-all 404.
func allowedMethod(mux *http.ServeMux, r *http.Request) string {
	for _, m := range []string{http.MethodGet, http.MethodPost} {
		if m == r.Method {
			continue
		}
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, pattern := mux.Handler(probe); pattern != catchAll && pattern != "" {
			return m
		}
	}
	return ""
}

// socketHandler is the API as served on the Unix socket. The socket lives in
// a 0700 directory, so only the user can reach it; no browser checks needed.
func (a *api) socketHandler() http.Handler {
	mux := http.NewServeMux()
	a.routes(mux)
	return mux
}

// dashboardHandler is the API plus the web page, behind browser protections.
func (a *api) dashboardHandler(port int) http.Handler {
	mux := http.NewServeMux()
	a.routes(mux)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'")
		_, _ = w.Write(dashboardHTML)
	})
	return guard(mux, port)
}

func (a *api) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.svc.status())
}

func (a *api) action(w http.ResponseWriter, r *http.Request) {
	err := a.svc.act(r.PathValue("action"), r.PathValue("app"))
	resp := a.svc.status()
	code := http.StatusOK
	if err != nil {
		resp.Error = err.Error()
		code = actionErrorCode(err)
	}
	writeJSON(w, code, resp)
}

func (a *api) down(w http.ResponseWriter, _ *http.Request) {
	a.svc.stopAll()
	writeJSON(w, http.StatusOK, a.svc.status())
	a.shutdown()
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

// logRequest validates the app and ?lines= shared by both log endpoints.
func (a *api) logRequest(w http.ResponseWriter, r *http.Request) (path string, lines int, ok bool) {
	app := r.PathValue("app")
	if !a.svc.hasApp(app) {
		writeError(w, http.StatusNotFound, fmt.Sprintf("unknown app %q", app))
		return "", 0, false
	}
	lines = defaultLogLines
	if v := r.URL.Query().Get("lines"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "lines must be a non-negative integer (0 for all)")
			return "", 0, false
		}
		lines = n
	}
	lines = min(lines, maxLogLines) // 0 still means all
	return a.svc.paths.AppLog(app), lines, true
}

// streamContext ends when the client goes away or the daemon stops.
func (a *api) streamContext(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(a.base, cancel)
	return ctx, func() { stop(); cancel() }
}

// logsText returns an app's log as plain text: the last ?lines= lines, and
// with ?follow=true everything appended afterwards, across restarts, until
// the client disconnects. Works with curl.
func (a *api) logsText(w http.ResponseWriter, r *http.Request) {
	path, lines, ok := a.logRequest(w, r)
	if !ok {
		return
	}
	follow, _ := strconv.ParseBool(r.URL.Query().Get("follow"))

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	out := &flushWriter{w: w, rc: http.NewResponseController(w)}
	offset, err := logs.Tail(out, path, lines)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		fmt.Fprintf(out, "\n[h2ams] reading log failed: %v\n", err)
		return
	}
	if !follow {
		return
	}
	_ = out.rc.Flush() // send headers now, even if the log is empty
	ctx, cancel := a.streamContext(r.Context())
	defer cancel()
	_ = logs.Follow(ctx, out, path, offset, logFollowInterval)
}

// flushWriter flushes after every write so followed output arrives promptly.
type flushWriter struct {
	w  io.Writer
	rc *http.ResponseController
}

func (f *flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err == nil {
		_ = f.rc.Flush()
	}
	return n, err
}

// logsWebSocket streams the same data as logsText with follow=true, as binary
// WebSocket messages (log bytes need not be valid UTF-8). Used by the
// dashboard.
func (a *api) logsWebSocket(w http.ResponseWriter, r *http.Request) {
	path, lines, ok := a.logRequest(w, r)
	if !ok {
		return
	}
	// Accept rejects cross-origin handshakes by default.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx, cancel := a.streamContext(conn.CloseRead(r.Context()))
	defer cancel()

	out := &wsWriter{ctx: ctx, conn: conn}
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

// guard protects the dashboard port from other sites in the user's browser:
//   - the Host header must name loopback on our port, which defeats DNS
//     rebinding (evil.example resolving to 127.0.0.1);
//   - state-changing requests must come from our own origin, which defeats
//     cross-site form posts.
//
// Non-browser clients (curl, plugins) send no Origin and pass.
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

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, Response{APIVersion: APIVersion, Error: msg})
}
