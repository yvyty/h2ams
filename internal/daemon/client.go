package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/yvyty/h2ams/internal/process"
)

// APIVersion is the version of the HTTP API under /api/v1. Breaking changes
// get a new prefix; additive changes (new fields, new endpoints) do not.
const APIVersion = "v1"

// Commands understood by Call. Each maps to one API endpoint.
const (
	CmdStatus  = "status"
	CmdDown    = "down"
	CmdStart   = "start"   // needs App
	CmdStop    = "stop"    // needs App
	CmdRestart = "restart" // needs App
)

// Request names one API call for Call.
type Request struct {
	Cmd string `json:"cmd"`
	App string `json:"app,omitempty"`
}

// Response is the body of every JSON API reply.
type Response struct {
	APIVersion  string      `json:"api_version,omitempty"`
	Error       string      `json:"error,omitempty"`
	PID         int         `json:"pid,omitempty"`
	ConfigPath  string      `json:"config_path,omitempty"`
	Socket      string      `json:"socket,omitempty"`      // Unix socket serving this API
	Environment string      `json:"environment,omitempty"` // empty means all apps
	Dashboard   string      `json:"dashboard,omitempty"`   // URL; empty when disabled or unavailable
	Apps        []AppStatus `json:"apps,omitempty"`
}

// AppStatus is a process status plus what the daemon observes about the
// app's whole process group: listening TCP ports and resource use.
type AppStatus struct {
	process.Status
	Ports       []int          `json:"ports,omitempty"`
	CPUPercent  *float64       `json:"cpu_percent,omitempty"`  // nil until sampled, or unsupported
	MemoryBytes uint64         `json:"memory_bytes,omitempty"` // resident set size
	Container   *ContainerInfo `json:"container,omitempty"`    // set for Docker apps
}

// ContainerInfo describes the container behind a Docker app.
type ContainerInfo struct {
	Name  string `json:"name"`
	Image string `json:"image"`
	State string `json:"state,omitempty"` // from `docker inspect`, e.g. "running"
}

// ErrNotRunning means no daemon is listening on the socket.
var ErrNotRunning = errors.New("h2ams is not running")

// socketHost is the placeholder host in URLs sent over the Unix socket.
const socketHost = "h2ams"

// Client calls a daemon's HTTP API over its Unix socket.
type Client struct {
	http *http.Client
}

// NewClient returns a Client for the daemon at socket. timeout bounds each call.
func NewClient(socket string, timeout time.Duration) *Client {
	dialer := &net.Dialer{Timeout: time.Second}
	return &Client{
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					conn, err := dialer.DialContext(ctx, "unix", socket)
					if err != nil {
						return nil, &dialError{err}
					}
					return conn, nil
				},
				DisableKeepAlives: true,
			},
		},
	}
}

// dialError marks a failure to reach the socket at all.
type dialError struct{ err error }

func (e *dialError) Error() string { return e.err.Error() }
func (e *dialError) Unwrap() error { return e.err }

// Status returns the daemon's status.
func (c *Client) Status() (Response, error) {
	return c.do(http.MethodGet, "/api/"+APIVersion+"/status")
}

// Act runs start, stop, or restart on app.
func (c *Client) Act(action, app string) (Response, error) {
	return c.do(http.MethodPost, fmt.Sprintf("/api/%s/apps/%s/%s", APIVersion, url.PathEscape(app), url.PathEscape(action)))
}

// Down stops every app and shuts the daemon down.
func (c *Client) Down() (Response, error) {
	return c.do(http.MethodPost, "/api/"+APIVersion+"/down")
}

func (c *Client) do(method, path string) (Response, error) {
	req, err := http.NewRequest(method, "http://"+socketHost+path, nil)
	if err != nil {
		return Response{}, err
	}
	res, err := c.http.Do(req)
	if err != nil {
		var de *dialError
		if errors.As(err, &de) {
			return Response{}, fmt.Errorf("%w (%v)", ErrNotRunning, de.err)
		}
		return Response{}, err
	}
	defer res.Body.Close()

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return Response{}, fmt.Errorf("reading response: %w", err)
	}
	var resp Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return Response{}, fmt.Errorf("%s %s: %s", method, path, res.Status)
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	if res.StatusCode >= 300 {
		return resp, fmt.Errorf("%s %s: %s", method, path, res.Status)
	}
	return resp, nil
}

// Call is a convenience wrapper mapping a Request onto the API.
func Call(socket string, req Request, timeout time.Duration) (Response, error) {
	c := NewClient(socket, timeout)
	switch req.Cmd {
	case CmdStatus:
		return c.Status()
	case CmdDown:
		return c.Down()
	case CmdStart, CmdStop, CmdRestart:
		return c.Act(req.Cmd, req.App)
	default:
		return c.do(http.MethodPost, "/api/"+APIVersion+"/"+url.PathEscape(req.Cmd))
	}
}

// Running reports whether a daemon is accepting connections on socket.
func Running(socket string) bool {
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}
