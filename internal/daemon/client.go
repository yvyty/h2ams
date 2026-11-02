package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/yvyty/h2ams/internal/process"
)

// Request commands.
const (
	CmdStatus  = "status"
	CmdDown    = "down"
	CmdStart   = "start"   // needs App
	CmdStop    = "stop"    // needs App
	CmdRestart = "restart" // needs App
)

// Request is one client message. Each connection carries one request.
type Request struct {
	Cmd string `json:"cmd"`
	App string `json:"app,omitempty"`
}

// Response is the daemon's reply.
type Response struct {
	Error       string      `json:"error,omitempty"`
	PID         int         `json:"pid,omitempty"`
	ConfigPath  string      `json:"config_path,omitempty"`
	Environment string      `json:"environment,omitempty"` // empty means all apps
	Dashboard   string      `json:"dashboard,omitempty"`   // URL; empty when disabled or unavailable
	Apps        []AppStatus `json:"apps,omitempty"`
}

// AppStatus is a process status plus what the daemon observes about the
// app's whole process group: listening TCP ports and resource use.
type AppStatus struct {
	process.Status
	Ports       []int    `json:"ports,omitempty"`
	CPUPercent  *float64 `json:"cpu_percent,omitempty"`  // nil until sampled, or unsupported
	MemoryBytes uint64   `json:"memory_bytes,omitempty"` // resident set size
}

// ErrNotRunning means no daemon is listening on the socket.
var ErrNotRunning = errors.New("h2ams is not running")

// Call sends req to the daemon at socket and waits up to timeout for a reply.
func Call(socket string, req Request, timeout time.Duration) (Response, error) {
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		return Response{}, fmt.Errorf("%w (%v)", ErrNotRunning, err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return Response{}, err
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, fmt.Errorf("sending %s request: %w", req.Cmd, err)
	}
	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return Response{}, fmt.Errorf("reading %s response: %w", req.Cmd, err)
	}
	if resp.Error != "" {
		return resp, errors.New(resp.Error)
	}
	return resp, nil
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
