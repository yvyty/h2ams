package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"

	"github.com/yvyty/h2ams/internal/config"
	"github.com/yvyty/h2ams/internal/process"
	"github.com/yvyty/h2ams/internal/testutil"
)

// dashboardDaemon runs a daemon with the dashboard on a random port and
// returns the dashboard URL and socket path.
func dashboardDaemon(t *testing.T, yaml string) (baseURL, sock string, logBuf *syncWriter) {
	t.Helper()
	dir := shortTempDir(t)
	cfgPath := filepath.Join(dir, config.DefaultFileName)
	if err := os.WriteFile(cfgPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	logBuf = &syncWriter{}
	opts := Options{
		ConfigPath:    cfgPath,
		Paths:         NewPaths(filepath.Join(dir, "state")),
		DashboardAddr: "127.0.0.1:0",
		Log:           logBuf,
	}
	errCh := startDaemon(t, opts)
	t.Cleanup(func() {
		Call(opts.Paths.Socket, Request{Cmd: CmdDown}, timeout)
		<-errCh
	})
	resp, err := Call(opts.Paths.Socket, Request{Cmd: CmdStatus}, timeout)
	if err != nil {
		t.Fatal(err)
	}
	return resp.Dashboard, opts.Paths.Socket, logBuf
}

type syncWriter struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func getStatus(t *testing.T, baseURL string) Response {
	t.Helper()
	res, err := http.Get(baseURL + "/api/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var resp Response
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func post(t *testing.T, url string, header http.Header) (int, Response) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, url, nil)
	for k, v := range header {
		req.Header[k] = v
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var resp Response
	json.NewDecoder(res.Body).Decode(&resp)
	return res.StatusCode, resp
}

func appByName(resp Response, name string) (AppStatus, bool) {
	for _, a := range resp.Apps {
		if a.Name == name {
			return a, true
		}
	}
	return AppStatus{}, false
}

func waitHTTPState(t *testing.T, baseURL, app string, want process.State) AppStatus {
	t.Helper()
	var last AppStatus
	if !testutil.WaitFor(timeout, func() bool {
		last, _ = appByName(getStatus(t, baseURL), app)
		return last.State == want
	}) {
		t.Fatalf("%s never reached %q via HTTP (last %+v)", app, want, last)
	}
	return last
}

func TestDashboardStatusEndpoint(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ports are not measured on Windows yet")
	}
	out := filepath.Join(shortTempDir(t), "port")
	base, _, _ := dashboardDaemon(t, fmt.Sprintf(`version: 1
default_environment: dev
environments:
  dev:
    apps: [web, worker]
apps:
  web:
    command: %q
  worker:
    command: %q
`, testutil.Command(testutil.Listen, out), testutil.Command(testutil.Sleep, "")))

	if !strings.HasPrefix(base, "http://127.0.0.1:") {
		t.Fatalf("dashboard URL = %q", base)
	}
	raw, ok := testutil.ReadFile(out, timeout)
	if !ok {
		t.Fatal("web never reported its port")
	}
	var port int
	fmt.Sscan(raw, &port)

	var resp Response
	if !testutil.WaitFor(timeout, func() bool {
		resp = getStatus(t, base)
		web, _ := appByName(resp, "web")
		return web.State == process.StateRunning && slices.Contains(web.Ports, port)
	}) {
		t.Fatalf("status never showed web on port %d: %+v", port, resp.Apps)
	}
	if resp.Environment != "dev" || resp.Dashboard != base || len(resp.Apps) != 2 {
		t.Errorf("unexpected status: %+v", resp)
	}
	if worker, ok := appByName(resp, "worker"); !ok || worker.PID == 0 {
		t.Errorf("worker missing or without pid: %+v", worker)
	}
}

func TestDashboardServesPage(t *testing.T) {
	base, _, _ := dashboardDaemon(t, "version: 1\napps:\n  a:\n    command: "+fmt.Sprintf("%q", testutil.Command(testutil.Sleep, ""))+"\n")
	res, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "<title>h2ams dashboard</title>") {
		t.Errorf("GET / = %d, body starts %q", res.StatusCode, string(body[:min(80, len(body))]))
	}
	if res.Header.Get("X-Frame-Options") != "DENY" {
		t.Error("missing X-Frame-Options")
	}
}

func TestDashboardActions(t *testing.T) {
	base, sock, _ := dashboardDaemon(t, "version: 1\napps:\n  api:\n    command: "+fmt.Sprintf("%q", testutil.Command(testutil.Sleep, ""))+"\n")
	pid := waitHTTPState(t, base, "api", process.StateRunning).PID

	code, resp := post(t, base+"/api/v1/apps/api/restart", nil)
	if code != http.StatusOK || resp.Error != "" {
		t.Fatalf("restart: %d %+v", code, resp)
	}
	if newPID := waitHTTPState(t, base, "api", process.StateRunning).PID; newPID == pid {
		t.Errorf("restart kept pid %d", pid)
	}

	if code, resp = post(t, base+"/api/v1/apps/api/stop", nil); code != http.StatusOK {
		t.Fatalf("stop: %d %+v", code, resp)
	}
	if api, _ := appByName(resp, "api"); api.State != process.StateStopped {
		t.Errorf("stop response shows %q; UI would not update", api.State)
	}
	// The CLI sees the same state: both go through one service.
	waitAppState(t, sock, "api", process.StateStopped)

	if code, _ = post(t, base+"/api/v1/apps/api/stop", nil); code != http.StatusConflict {
		t.Errorf("stop of stopped app: %d, want 409", code)
	}
	if code, _ = post(t, base+"/api/v1/apps/api/start", nil); code != http.StatusOK {
		t.Errorf("start: %d", code)
	}
	waitHTTPState(t, base, "api", process.StateRunning)
	if code, _ = post(t, base+"/api/v1/apps/api/start", nil); code != http.StatusConflict {
		t.Errorf("start of running app: %d, want 409", code)
	}
	if code, _ = post(t, base+"/api/v1/apps/ghost/restart", nil); code != http.StatusNotFound {
		t.Errorf("unknown app: %d, want 404", code)
	}
	if code, _ = post(t, base+"/api/v1/apps/api/explode", nil); code != http.StatusNotFound {
		t.Errorf("unknown action: %d, want 404", code)
	}
}

func TestDashboardRejectsOtherSites(t *testing.T) {
	base, _, _ := dashboardDaemon(t, "version: 1\napps:\n  api:\n    command: "+fmt.Sprintf("%q", testutil.Command(testutil.Sleep, ""))+"\n")
	waitHTTPState(t, base, "api", process.StateRunning)

	// DNS rebinding: a hostile name that resolves to 127.0.0.1.
	req, _ := http.NewRequest(http.MethodGet, base+"/api/v1/status", nil)
	req.Host = "evil.example:" + strings.TrimPrefix(base, "http://127.0.0.1:")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusForbidden {
		t.Errorf("foreign Host: %d, want 403", res.StatusCode)
	}

	// Cross-site POST from a page the user has open.
	code, _ := post(t, base+"/api/v1/apps/api/stop", http.Header{"Origin": {"https://evil.example"}})
	if code != http.StatusForbidden {
		t.Errorf("cross-site POST: %d, want 403", code)
	}
	if api, _ := appByName(getStatus(t, base), "api"); api.State != process.StateRunning {
		t.Errorf("cross-site POST changed state to %q", api.State)
	}

	// Same-origin POST (what the dashboard sends) is allowed.
	code, _ = post(t, base+"/api/v1/apps/api/restart", http.Header{"Origin": {base}})
	if code != http.StatusOK {
		t.Errorf("same-origin POST: %d, want 200", code)
	}

	// Cross-site WebSocket.
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(base, "http") + "/api/v1/apps/api/logs/ws"
	if conn, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"https://evil.example"}}}); err == nil {
		conn.CloseNow()
		t.Error("cross-site WebSocket was accepted")
	}
}

// wsReader accumulates log stream messages for assertions.
type wsReader struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (r *wsReader) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.buf.String()
}

func readLogs(ctx context.Context, conn *websocket.Conn, into *wsReader) {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		into.mu.Lock()
		into.buf.Write(data)
		into.mu.Unlock()
	}
}

func TestDashboardLogStreamSurvivesRestart(t *testing.T) {
	base, _, _ := dashboardDaemon(t, fmt.Sprintf("version: 1\napps:\n  api:\n    command: %q\n", testutil.Command(testutil.Hello, "")))
	waitHTTPState(t, base, "api", process.StateRunning)

	ctx, cancel := context.WithTimeout(context.Background(), 2*timeout)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(base, "http") + "/api/v1/apps/api/logs/ws?lines=50"
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	var got wsReader
	go readLogs(ctx, conn, &got)

	if !testutil.WaitFor(timeout, func() bool { return strings.Count(got.String(), "hello-") == 1 }) {
		t.Fatalf("existing output not streamed: %q", got.String())
	}

	if code, _ := post(t, base+"/api/v1/apps/api/restart", nil); code != http.StatusOK {
		t.Fatalf("restart: %d", code)
	}
	if !testutil.WaitFor(timeout, func() bool {
		s := got.String()
		return strings.Count(s, "hello-") == 2 && strings.Contains(s, "api: stopped") && strings.Count(s, "api: started") == 2
	}) {
		t.Fatalf("stream did not continue across restart:\n%s", got.String())
	}
}

func TestDashboardLogStreamErrors(t *testing.T) {
	base, _, _ := dashboardDaemon(t, "version: 1\napps:\n  api:\n    command: "+fmt.Sprintf("%q", testutil.Command(testutil.Sleep, ""))+"\n")
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	for path, want := range map[string]int{
		"/api/v1/apps/ghost/logs/ws":         http.StatusNotFound,
		"/api/v1/apps/api/logs/ws?lines=abc": http.StatusBadRequest,
	} {
		_, res, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+path, nil)
		if err == nil || res == nil || res.StatusCode != want {
			code := 0
			if res != nil {
				code = res.StatusCode
			}
			t.Errorf("%s: err=%v status=%d, want %d", path, err, code, want)
		}
	}
}

func TestDashboardPortBusyKeepsDaemonRunning(t *testing.T) {
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer busy.Close()
	port := busy.Addr().(*net.TCPAddr).Port

	dir := shortTempDir(t)
	cfgPath := filepath.Join(dir, config.DefaultFileName)
	os.WriteFile(cfgPath, []byte(fmt.Sprintf("version: 1\ndashboard:\n  port: %d\napps:\n  a:\n    command: %q\n",
		port, testutil.Command(testutil.Sleep, ""))), 0o644)
	var log syncWriter
	opts := Options{ConfigPath: cfgPath, Paths: NewPaths(filepath.Join(dir, "state")), Log: &log}
	errCh := startDaemon(t, opts)
	defer func() {
		Call(opts.Paths.Socket, Request{Cmd: CmdDown}, timeout)
		<-errCh
	}()

	resp, err := Call(opts.Paths.Socket, Request{Cmd: CmdStatus}, timeout)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Dashboard != "" {
		t.Errorf("dashboard URL = %q, want empty when the port is busy", resp.Dashboard)
	}
	if !strings.Contains(log.String(), "dashboard disabled") {
		t.Errorf("no warning logged: %q", log.String())
	}
}

func TestDashboardOnlyBindsLoopback(t *testing.T) {
	var wg sync.WaitGroup
	if _, err := startDashboard(context.Background(), &wg, nil, "0.0.0.0:0"); err == nil {
		t.Fatal("expected refusal to bind a non-loopback address")
	}
}
