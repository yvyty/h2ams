# H2AMS

**High Availability Application Management Service.** Run all the apps you
develop against (APIs, frontends, workers) from one `h2ams.yml` instead of a
pile of terminal tabs. A background daemon keeps them running, restarts them
when they crash, shows which ports they use, and keeps their logs.

```console
$ h2ams status
Environment: dev
Dashboard:   http://127.0.0.1:9999

APP     STATE    PID    PORTS  CPU   MEM       RESTARTS  UPTIME  DETAIL
api     running  14958  8080   0.4%  24.3 MiB  0         3m12s   -
web     running  14955  5173   1.2%  88.1 MiB  0         3m12s   -
worker  running  14956  -      0.9%  3.1 MiB   0         3m12s   -
```

## Why

- **No more scattered terminals.** One file describes every app, its command,
  working directory, and environment.
- **No more accidental kills.** Apps run under a daemon, not your terminal, so
  closing a tab doesn't stop them. Crashed apps restart with backoff.
- **See what's running where.** `h2ams status` shows each app's state, PID,
  and the TCP ports it listens on.
- **Logs that survive restarts.** Output from every run is kept on disk.
- **A local dashboard.** <http://127.0.0.1:9999> shows every app with CPU and
  memory, streams logs live, and has start/stop/restart buttons.
- **Know when something is stuck.** Optional desktop notifications when an app
  is crash-looping.
- **Containers too.** Run a database or queue from a Docker image right next
  to your native apps, with the same restarts, logs, and controls.
- **Scriptable.** A versioned local HTTP API ([docs/api.md](docs/api.md)) for
  plugins and scripts, and a menu-bar app, [h2ams-tray](docs/tray.md).

## Install

Requires Go 1.27 or newer. Runs on macOS and Linux; Windows support is
best-effort (see [Platform support](#platform-support)).

```sh
go install github.com/yvyty/h2ams/cmd/h2ams@latest
```

Or build from a checkout:

```sh
git clone <repo-url> h2ams && cd h2ams
go build -o bin/h2ams ./cmd/h2ams
```

Check it works with `h2ams --version`.

## Quick start

```sh
cd my-project
h2ams init            # writes a commented template h2ams.yml
$EDITOR h2ams.yml     # describe your apps
h2ams up              # start the daemon and your apps
h2ams status          # see state, PIDs, and ports
h2ams logs api -f     # stream one app's output (Ctrl-C to stop)
h2ams restart api     # restart one app
open http://127.0.0.1:9999   # or use the dashboard
h2ams down            # stop everything
```

The full walkthrough is in [docs/getting-started.md](docs/getting-started.md).

## Configuration

A complete, commented example is in [examples/h2ams.yml](examples/h2ams.yml).

```yaml
version: 1
default_environment: dev

environments:
  dev:
    apps: [api, web]

apps:
  api:
    command: go run ./cmd/api
    cwd: ./api
    env:
      PORT: "8080"
    restart:
      policy: on-failure
      max_attempts: 5
      backoff: 1s
  web:
    command: npm run dev
    cwd: ./web

dashboard:
  port: 9999

notifications:
  crash_loop: true
```

| Field | Required | Default | Description |
| --- | --- | --- | --- |
| `version` | yes | | Schema version. Must be `1`. |
| `default_environment` | no | all apps | Environment `h2ams up` starts when `--env` is not given. |
| `environments.<name>.apps` | no | | List of app names started together. |
| `apps.<name>` | yes (at least one) | | One managed app. Names may use letters, digits, `.`, `_`, and `-`. |
| `apps.<name>.command` | one of `command` / `docker` | | Command line, run through `/bin/sh -c` (`cmd /C` on Windows). |
| `apps.<name>.docker.image` | with `docker` | | Image to run, e.g. `postgres:16`. See [Docker apps](#docker-apps). |
| `apps.<name>.docker.ports` | no | | Published ports, `[ip:]host:container[/proto]`, e.g. `"5432:5432"`. |
| `apps.<name>.docker.volumes` | no | | `host:container[:opts]`. Host paths starting with `./`, `../`, or `~/` are resolved; others are named volumes. |
| `apps.<name>.docker.args` | no | | Arguments after the image (the container's command). |
| `apps.<name>.cwd` | no | config file's directory | Working directory. Relative paths are resolved from the config file. |
| `apps.<name>.env` | no | | Extra environment variables, added to the daemon's environment. For Docker apps, passed into the container. |
| `apps.<name>.restart.policy` | no | `on-failure` | `always`, `on-failure` (non-zero exit only), or `never`. |
| `apps.<name>.restart.max_attempts` | no | `5` | Restarts in a row before the app is marked `failed`. The count resets once a run lasts 10s. |
| `apps.<name>.restart.backoff` | no | `1s` | Delay before the first restart; doubles each time, up to 30s. Go duration syntax (`500ms`, `2s`). |
| `dashboard.enabled` | no | `true` | Serve the web dashboard. |
| `dashboard.port` | no | `9999` | Dashboard port, always on `127.0.0.1`. Give each project its own port if you run several at once. |
| `notifications.crash_loop` | no | `false` | Desktop notification when an app restarts 3 times within a minute, or gives up (`failed`). |

Unknown fields are rejected, and every problem in the file is reported at once:

```console
$ h2ams up
Error: h2ams.yml: invalid config:
  - apps.api.command: required
  - apps.web.restart.policy: must be one of always, on-failure, never; got "sometimes"
```

## Commands

| Command | What it does |
| --- | --- |
| `h2ams init [--force]` | Write a template config. Refuses to overwrite an existing file without `--force`. |
| `h2ams up [--env NAME]` | Validate the config, start the daemon in the background, and launch the environment's apps. |
| `h2ams status [--json]` | Show the environment, dashboard URL, and each app's state, PID, ports, CPU, memory, restarts, uptime, and last error. `--json` prints the API's status object. |
| `h2ams logs <app> [-n N] [-f]` | Show the last N lines (default 100, `0` for all) of an app's output; `-f` follows new output. |
| `h2ams start <app>` | Start a stopped or failed app. |
| `h2ams stop <app>` | Stop one app and keep it stopped; everything else keeps running. |
| `h2ams restart <app>` | Stop an app (if running) and start it again. |
| `h2ams envs` | List environments and their apps, marking the default and the active one. |
| `h2ams down` | Stop every app and the daemon. |

Every command accepts `-c, --config PATH` (default `h2ams.yml`). Run
`h2ams <command> --help` for details and examples.

### App states

| State | Meaning |
| --- | --- |
| `starting` | Launched, not yet confirmed running. |
| `running` | Up and supervised. |
| `restarting` | Exited; waiting out the backoff before starting again. |
| `completed` | Exited successfully and the policy doesn't restart it, such as a one-shot build. |
| `stopped` | Stopped by `h2ams stop` or `h2ams down`. |
| `failed` | Crashed and won't be restarted: the policy is `never`, or `max_attempts` ran out. |

## Dashboard

While the daemon runs, open the URL printed by `h2ams up` and `h2ams status`
(default <http://127.0.0.1:9999>). It shows the active environment and every
app with its state, ports (as links), CPU, memory, restarts, and uptime,
refreshed every 2 seconds. Click an app to stream its logs live; the stream
continues across restarts. The Start, Stop, and Restart buttons do exactly
what the CLI commands do.

The dashboard only listens on `127.0.0.1` and rejects requests that come from
other websites, so pages open in your browser cannot control your apps. If
the port is taken (for example by another project's daemon), the daemon keeps
running without a dashboard and logs a warning to `daemon.log`; set
`dashboard.port` to fix it. JSON is available at `/api/status`.

## Docker apps

Give an app a `docker` block instead of a `command` to run it as a
container:

```yaml
apps:
  db:
    docker:
      image: postgres:16
      ports: ["127.0.0.1:5432:5432"]
      volumes: ["./data:/var/lib/postgresql/data"]
    env:
      POSTGRES_PASSWORD: dev
```

h2ams runs `docker run --rm --name h2ams-<project>-<app> ...` in the
foreground as the supervised process, so restart policies, `logs`, `start`,
`stop`, and `restart` work exactly as for native apps. Stopping sends
`SIGTERM`, which the docker CLI forwards to the container. Any leftover
container with the same name is removed (`docker rm -f`) before each start
and after each exit. `status` shows the container's state (from
`docker inspect`) and its published ports. CPU and memory aren't shown for
containers.

It needs the `docker` CLI on `PATH` and a running Docker daemon (Docker
Desktop, Colima, OrbStack, or Docker Engine). Set `H2AMS_DOCKER` to use a
different binary, such as `podman`. Docker is only needed for container apps:
if the CLI is missing, those apps fail at once with "docker CLI not found",
and every other app runs normally.

## API and plugins

Each daemon serves a versioned HTTP API, `/api/v1`, on its Unix socket and on
the dashboard port: status, start, stop, restart, logs (plain text or
streamed), and down. The CLI, dashboard, and tray all use it.

```sh
SOCK=$(h2ams status --json | jq -r .socket)
curl --unix-socket "$SOCK" http://h2ams/api/v1/status
curl --unix-socket "$SOCK" -X POST http://h2ams/api/v1/apps/api/restart
curl --unix-socket "$SOCK" -N "http://h2ams/api/v1/apps/api/logs?follow=true"
```

See [docs/api.md](docs/api.md) for every endpoint, the versioning policy,
and a plugin example.

## Menu-bar app

`h2ams-tray` shows a colored dot (grey, green, amber, red) for all your
projects and a menu to start, stop, and switch environments. It is a
separate binary; install it next to `h2ams` and `h2ams up` starts it for
you (one icon for every project). Use `h2ams up --no-tray` or
`H2AMS_NO_TRAY=1` to skip it. See [docs/tray.md](docs/tray.md).

## Environments

An environment is a named list of apps. `h2ams up --env testing` starts only
the apps in `testing`; without `--env`, `up` uses `default_environment`, or
every app if none is set. `h2ams status`, `h2ams envs`, and the dashboard show
which environment is active. To switch, run `h2ams down` and then
`h2ams up --env <name>`.

## How it works

- **One daemon per config file.** `h2ams up` starts `h2ams daemon` detached
  from your terminal. Commands find it through a Unix socket keyed by the
  config file's absolute path, so separate projects never collide. The CLI
  talks to it over the same HTTP API that plugins use.
- **Process groups.** Each app runs in its own process group. Stopping an app
  sends `SIGTERM` to the whole group (so `npm run dev` takes its children with
  it), then `SIGKILL` once the whole group is gone or after 5 seconds. If
  the wrapping shell exits first (as `/bin/sh` does on Linux), the rest of the
  group still gets the full grace period.
- **Logs.** Each app's stdout and stderr are appended to `logs/<app>.log` in
  the state directory, along with `[h2ams ...]` lines marking every start,
  exit, restart, and stop. A log over 10 MB is rotated to `<app>.log.1` when
  the daemon next starts.
- **Monitoring.** Every 2 seconds the daemon samples CPU (percent of one core)
  and resident memory for each app's whole process group, plus descendants
  that moved to groups of their own (such as the esbuild and tailwind watchers
  that Phoenix starts through Erlang). Every second it
  checks for crash loops: 3 automatic restarts within a minute, or an app
  giving up. Alerts go to `daemon.log` and, if `notifications.crash_loop` is
  on, to a desktop notification (once per episode).
- **State directory.** `~/Library/Caches/h2ams/<hash>/` on macOS,
  `~/.cache/h2ams/<hash>/` on Linux. Override the base with `H2AMS_STATE_DIR`.
  It holds the socket, `daemon.log` (the daemon's own messages), `logs/`, and
  `project.json` (which config it belongs to, used by `h2ams-tray`).

## Platform support

| | macOS | Linux | Windows |
| --- | --- | --- | --- |
| Start, stop, restart | ✅ | ✅ | best-effort (`taskkill`), untested |
| Port detection | ✅ via `lsof` | ✅ via `/proc` | not yet |
| CPU and memory | ✅ via `ps` | ✅ via `/proc` | not yet |
| Desktop notifications | ✅ via `osascript` | ✅ via `notify-send` (libnotify) | not yet |
| Dashboard and API | ✅ | ✅ | ✅ |
| Docker apps | ✅ | ✅ | best-effort, untested |
| `h2ams-tray` | ✅ (cgo build) | ✅ (StatusNotifierItem desktops) | ✅, manual check |
| Apps die if the daemon is killed with `SIGKILL` | ❌ (stop with `h2ams down`) | ✅ | ❌ |

## Troubleshooting

- **`h2ams is already running`**: a daemon for this config is up. Use
  `h2ams status`, or `h2ams down` and then `h2ams up` to restart it.
- **The daemon exits right after `up`**: read the `daemon.log` path printed in
  the error.
- **An app is `failed`**: `h2ams logs <app>` shows its output and the exit
  reason. Fix it, then `h2ams down && h2ams up`.
- **Logs are empty or delayed**: some runtimes buffer output when it isn't a
  terminal. For Python, set `PYTHONUNBUFFERED: "1"` in the app's `env`.
- **`socket path is ... over the ... OS limit`**: the state directory path is
  too long for a Unix socket. Set `H2AMS_STATE_DIR` to a shorter directory.
- **No ports shown**: the app may not have started listening yet, it may
  listen on UDP only, or you are on Windows.
- **No dashboard URL in `h2ams status`**: it is disabled, or its port was
  busy when the daemon started. `daemon.log` says which; change
  `dashboard.port` and restart with `h2ams down && h2ams up`.
- **No desktop notifications**: set `notifications.crash_loop: true`. On
  Linux install `notify-send` (package `libnotify-bin` or `libnotify`). On
  macOS, allow notifications for Script Editor in System Settings. Failures
  are logged to `daemon.log`.

## Development

```sh
go test ./...                        # unit and integration tests (builds the binary)
go test -short ./...                 # skip the tests that build binaries
H2AMS_TEST_DOCKER=1 go test ./...    # also run a real container (needs Docker)
go vet ./...
```

Docker tests use a fake `docker` script by default, so they need no Docker.

Set the version at build time with
`-ldflags "-X github.com/yvyty/h2ams/internal/cli.Version=v0.1.0"`.
