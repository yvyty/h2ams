//go:build unix

package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yvyty/h2ams/internal/config"
	"github.com/yvyty/h2ams/internal/process"
	"github.com/yvyty/h2ams/internal/testutil"
)

// socketHTTP is how a plugin talks to the API over the Unix socket
// (the Go equivalent of `curl --unix-socket`).
func socketHTTP(sock string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
}

func apiDaemon(t *testing.T, apps string) (sock string, client *http.Client, cfgPath string, errCh <-chan error) {
	t.Helper()
	dir := shortTempDir(t)
	cfgPath = filepath.Join(dir, config.DefaultFileName)
	os.WriteFile(cfgPath, []byte("version: 1\ndashboard:\n  enabled: false\napps:\n"+apps), 0o644)
	opts := Options{ConfigPath: cfgPath, Paths: NewPaths(filepath.Join(dir, "state"))}
	errCh = startDaemon(t, opts)
	t.Cleanup(func() {
		Call(opts.Paths.Socket, Request{Cmd: CmdDown}, timeout)
	})
	return opts.Paths.Socket, socketHTTP(opts.Paths.Socket), cfgPath, errCh
}

func getJSON(t *testing.T, c *http.Client, method, path string) (int, Response) {
	t.Helper()
	req, _ := http.NewRequest(method, "http://h2ams"+path, nil)
	res, err := c.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("%s %s: Content-Type %q", method, path, ct)
	}
	var resp Response
	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		t.Fatalf("%s %s: decoding: %v", method, path, err)
	}
	return res.StatusCode, resp
}

func TestAPIStatusIsVersioned(t *testing.T) {
	_, c, cfgPath, _ := apiDaemon(t, fmt.Sprintf("  api:\n    command: %q\n", testutil.Command(testutil.Sleep, "")))
	code, resp := getJSON(t, c, http.MethodGet, "/api/v1/status")
	if code != http.StatusOK || resp.APIVersion != "v1" || resp.ConfigPath != cfgPath || len(resp.Apps) != 1 || resp.Apps[0].Name != "api" {
		t.Errorf("status = %d %+v", code, resp)
	}
}

func TestAPIErrors(t *testing.T) {
	_, c, _, _ := apiDaemon(t, fmt.Sprintf("  api:\n    command: %q\n", testutil.Command(testutil.Sleep, "")))
	tests := []struct {
		method, path string
		code         int
		errPart      string
	}{
		{http.MethodGet, "/api/v1/nope", http.StatusNotFound, "no endpoint GET /api/v1/nope"},
		{http.MethodGet, "/api/v2/status", http.StatusNotFound, "no endpoint"},
		{http.MethodPost, "/api/v1/apps/ghost/stop", http.StatusNotFound, `unknown app "ghost"`},
		{http.MethodPost, "/api/v1/apps/api/explode", http.StatusNotFound, "unknown action"},
		{http.MethodPost, "/api/v1/apps/api/start", http.StatusConflict, "already running"},
		{http.MethodGet, "/api/v1/apps/ghost/logs", http.StatusNotFound, `unknown app "ghost"`},
		{http.MethodGet, "/api/v1/apps/api/logs?lines=-1", http.StatusBadRequest, "lines must be"},
	}
	waitAppState(t, socketOf(t, c), "api", process.StateRunning)
	for _, tt := range tests {
		code, resp := getJSON(t, c, tt.method, tt.path)
		if code != tt.code || !strings.Contains(resp.Error, tt.errPart) || resp.APIVersion != "v1" {
			t.Errorf("%s %s = %d %q (api_version %q), want %d containing %q", tt.method, tt.path, code, resp.Error, resp.APIVersion, tt.code, tt.errPart)
		}
	}
}

// socketOf recovers the socket path from a socketHTTP client, for helpers
// that use Call.
func socketOf(t *testing.T, c *http.Client) string {
	t.Helper()
	var sock string
	tr := c.Transport.(*http.Transport)
	conn, err := tr.DialContext(context.Background(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	sock = conn.RemoteAddr().String()
	conn.Close()
	return sock
}

func TestAPILogsPlainText(t *testing.T) {
	_, c, _, _ := apiDaemon(t, "  api:\n    command: 'for i in 1 2 3 4 5; do echo line-$i; done; sleep 60'\n")
	sock := socketOf(t, c)
	waitAppState(t, sock, "api", process.StateRunning)

	var body string
	if !testutil.WaitFor(timeout, func() bool {
		res, err := c.Get("http://h2ams/api/v1/apps/api/logs?lines=2")
		if err != nil {
			return false
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		body = string(b)
		return strings.HasSuffix(body, "line-4\nline-5\n")
	}) {
		t.Fatalf("lines=2 body = %q", body)
	}
	if strings.Contains(body, "line-3") {
		t.Errorf("lines=2 returned too much: %q", body)
	}

	res, err := c.Get("http://h2ams/api/v1/apps/api/logs?lines=0")
	if err != nil {
		t.Fatal(err)
	}
	all, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.Header.Get("Content-Type") != "text/plain; charset=utf-8" || !strings.Contains(string(all), "api: started") || !strings.Contains(string(all), "line-1") {
		t.Errorf("lines=0: %q (%s)", all, res.Header.Get("Content-Type"))
	}
}

func TestAPILogsFollowAcrossRestart(t *testing.T) {
	_, c, _, _ := apiDaemon(t, "  api:\n    command: echo hello-$$; sleep 60\n")
	sock := socketOf(t, c)
	waitAppState(t, sock, "api", process.StateRunning)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://h2ams/api/v1/apps/api/logs?follow=true&lines=10", nil)
	res, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	var mu sync.Mutex
	var got strings.Builder
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			mu.Lock()
			got.WriteString(sc.Text() + "\n")
			mu.Unlock()
		}
	}()
	text := func() string { mu.Lock(); defer mu.Unlock(); return got.String() }

	if !testutil.WaitFor(timeout, func() bool { return strings.Count(text(), "hello-") == 1 }) {
		t.Fatalf("existing output not streamed: %q", text())
	}
	if _, err := Call(sock, Request{Cmd: CmdRestart, App: "api"}, timeout); err != nil {
		t.Fatal(err)
	}
	if !testutil.WaitFor(timeout, func() bool { return strings.Count(text(), "hello-") == 2 && strings.Contains(text(), "api: stopped") }) {
		t.Fatalf("follow did not continue across restart:\n%s", text())
	}
}

func TestAPIDownStopsDaemon(t *testing.T) {
	_, c, _, errCh := apiDaemon(t, fmt.Sprintf("  api:\n    command: %q\n", testutil.Command(testutil.Sleep, "")))
	code, resp := getJSON(t, c, http.MethodPost, "/api/v1/down")
	if code != http.StatusOK || len(resp.Apps) != 1 || resp.Apps[0].State != process.StateStopped {
		t.Fatalf("down = %d %+v", code, resp)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestAPIWrongMethod(t *testing.T) {
	_, c, _, _ := apiDaemon(t, fmt.Sprintf("  api:\n    command: %q\n", testutil.Command(testutil.Sleep, "")))
	res, err := c.Post("http://h2ams/api/v1/status", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want 405", res.StatusCode)
	}
}

func TestKnownProjects(t *testing.T) {
	base := shortTempDir(t)
	t.Setenv(StateDirEnv, base)
	dir := shortTempDir(t)
	cfgPath := writeConfig(t, dir, map[string]string{"a": testutil.Command(testutil.Sleep, "")})
	paths, err := PathsFor(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	errCh := startDaemon(t, Options{ConfigPath: cfgPath, Paths: paths})

	projects, err := KnownProjects()
	if err != nil || len(projects) != 1 || projects[0].ConfigPath != cfgPath || !projects[0].Running() {
		t.Fatalf("while running: %+v %v", projects, err)
	}

	Call(paths.Socket, Request{Cmd: CmdDown}, timeout)
	<-errCh
	projects, _ = KnownProjects()
	if len(projects) != 1 || projects[0].Running() {
		t.Errorf("after down the project should be known but not running: %+v", projects)
	}

	os.Remove(cfgPath)
	if projects, _ = KnownProjects(); len(projects) != 0 {
		t.Errorf("project with deleted config still listed: %+v", projects)
	}
}
