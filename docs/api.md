# h2ams HTTP API (v1)

Every h2ams daemon serves a local HTTP API with the same capabilities as the
CLI: status, start, stop, restart, logs, and shutdown. The CLI, the web
dashboard, and `h2ams-tray` all use it, and so can your plugins and scripts.

## Versioning

All endpoints live under `/api/v1`, and every JSON response carries
`"api_version": "v1"`.

- **Additive changes** keep `v1`: new endpoints, new response fields, new
  optional query parameters. Clients must ignore fields they don't know.
- **Breaking changes** (removed or renamed fields or endpoints, changed
  meanings) will ship under `/api/v2`, with `v1` kept alongside it for at least
  one release.

## Connecting

The API is served in two places. Both serve identical endpoints.

| Where | Address | Who can reach it |
| --- | --- | --- |
| Unix socket | `<state dir>/<hash>/h2ams.sock` | Only your user (the directory is `0700`). Works even with the dashboard disabled. **Use this from plugins.** |
| Dashboard port | `http://127.0.0.1:9999` (or `dashboard.port`) | Loopback only. Rejects requests whose `Host` isn't loopback, and state-changing requests with a foreign `Origin`. |

Find the socket for a project with `h2ams status --json`:

```console
$ h2ams status --json | jq -r .socket
/Users/you/Library/Caches/h2ams/75abe6bb80dc/h2ams.sock
```

Then call it with curl. The host part of the URL is ignored over a socket:

```sh
SOCK=$(h2ams status --json | jq -r .socket)
curl --unix-socket "$SOCK" http://h2ams/api/v1/status
```

To find every project h2ams has run (the way `h2ams-tray` does), read
`<state dir>/*/project.json`; each holds `{"config_path": "..."}`. The project
is running when its `h2ams.sock` accepts connections. The state directory is
`~/Library/Caches/h2ams` on macOS, `~/.cache/h2ams` on Linux, or
`$H2AMS_STATE_DIR`.

## Responses and errors

JSON endpoints return a **status object** (below), on success and on error.
Errors set `error` and an HTTP status code:

| Code | Meaning |
| --- | --- |
| `200` | OK. |
| `400` | Bad query parameter. |
| `404` | Unknown app, action, or endpoint. |
| `405` | Wrong HTTP method; the `Allow` header names the right one. |
| `409` | The action conflicts with the app's state (start while running, stop while stopped). |
| `500` | The daemon failed to carry out the action. |

```json
{"api_version": "v1", "error": "unknown app \"ghost\""}
```

## Status object

```json
{
  "api_version": "v1",
  "pid": 4120,
  "config_path": "/Users/you/my-project/h2ams.yml",
  "socket": "/Users/you/Library/Caches/h2ams/75abe6bb80dc/h2ams.sock",
  "environment": "dev",
  "dashboard": "http://127.0.0.1:9999",
  "apps": [
    {
      "name": "api",
      "state": "running",
      "pid": 4121,
      "restarts": 0,
      "started_at": "2026-11-02T01:22:23-03:00",
      "ports": [8080],
      "cpu_percent": 0.4,
      "memory_bytes": 25481216
    },
    {
      "name": "db",
      "state": "running",
      "pid": 4122,
      "restarts": 0,
      "started_at": "2026-11-02T01:22:23-03:00",
      "ports": [5432],
      "container": {"name": "h2ams-75abe6bb80dc-db", "image": "postgres:16", "state": "running"}
    }
  ]
}
```

| Field | Type | Notes |
| --- | --- | --- |
| `pid` | int | The daemon's PID. |
| `config_path` | string | Absolute path of the config file. |
| `socket` | string | Unix socket serving this API. |
| `environment` | string | Active environment; omitted when every app runs (no environment). |
| `dashboard` | string | Dashboard URL; omitted when disabled or its port was busy. |
| `apps[].name` | string | App name from the config. |
| `apps[].state` | string | `starting`, `running`, `restarting`, `stopped`, or `failed`. |
| `apps[].pid` | int | Omitted unless running. Also the app's process group ID. |
| `apps[].restarts` | int | Automatic restarts since the app was last started on purpose. |
| `apps[].exit_code` | int | Last exit code; omitted when 0. |
| `apps[].error` | string | Last exit error, or why the app gave up. |
| `apps[].started_at` | RFC 3339 | When the current run started; omitted if never started. |
| `apps[].ports` | int[] | Listening TCP ports (detected), or published ports for Docker apps. |
| `apps[].cpu_percent` | number | Percent of one core, summed over the process group. Omitted until sampled, for Docker apps, and on Windows. |
| `apps[].memory_bytes` | int | Resident memory, summed over the process group. Same omissions. |
| `apps[].container` | object | Docker apps only: container `name`, `image`, and `state` from `docker inspect`. |

## Endpoints

### `GET /api/v1/status`

The status object.

```sh
curl --unix-socket "$SOCK" http://h2ams/api/v1/status
```

### `POST /api/v1/apps/{app}/start`

Starts an app that is `stopped` or `failed`, with a fresh restart count.
`409` if it is already starting, running, or restarting.

### `POST /api/v1/apps/{app}/stop`

Stops an app and keeps it stopped (no automatic restarts). The process group
gets `SIGTERM`, then `SIGKILL` after 5 seconds. `409` if it isn't running.

### `POST /api/v1/apps/{app}/restart`

Stops the app if it is running, then starts it again.

All three return the status object as it is after the action:

```sh
curl --unix-socket "$SOCK" -X POST http://h2ams/api/v1/apps/api/restart
```

### `GET /api/v1/apps/{app}/logs`

The app's log as `text/plain`: its stdout, stderr, and `[h2ams ...]`
lifecycle lines, across restarts.

| Query | Default | Meaning |
| --- | --- | --- |
| `lines` | `200` | Last N lines; `0` for all. Capped at 5000. |
| `follow` | `false` | Keep the response open and stream new output until the client disconnects. |

```sh
curl --unix-socket "$SOCK" "http://h2ams/api/v1/apps/api/logs?lines=50"
curl --unix-socket "$SOCK" -N "http://h2ams/api/v1/apps/api/logs?follow=true"
```

### `GET /api/v1/apps/{app}/logs/ws`

The same stream as `logs?follow=true` over a WebSocket, as **binary** messages
(log output need not be valid UTF-8; decode with a streaming UTF-8 decoder).
Accepts `lines`. Used by the dashboard. Cross-origin handshakes are refused.

### `POST /api/v1/down`

Stops every app, returns the final status object, then shuts the daemon
down. Starting a daemon is not part of the API (there is no daemon to talk to
yet); run `h2ams up`.

## Writing a plugin

A plugin is any program that calls this API. A minimal Go example that
restarts every failed app:

```go
package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
)

func main() {
	sock := os.Args[1] // from `h2ams status --json | jq -r .socket`
	c := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}

	res, err := c.Get("http://h2ams/api/v1/status")
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()
	var status struct {
		Apps []struct{ Name, State string }
	}
	json.NewDecoder(res.Body).Decode(&status)

	for _, app := range status.Apps {
		if app.State == "failed" {
			r, err := c.Post("http://h2ams/api/v1/apps/"+app.Name+"/start", "", nil)
			if err == nil {
				r.Body.Close()
			}
		}
	}
}
```

From Go code inside this repository, `daemon.NewClient(socket, timeout)` wraps
the same calls.
