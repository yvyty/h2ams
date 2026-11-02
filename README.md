# H2AMS

**High Availability Application Management Service.** Run all the apps you
develop against (APIs, frontends, workers) from one `h2ams.yml` instead of a
pile of terminal tabs. A background daemon keeps them running, restarts them
when they crash, shows which ports they use, and keeps their logs.

```console
$ h2ams up
h2ams started (pid 4120, log ~/Library/Caches/h2ams/75abe6bb80dc/daemon.log)

APP     STATE    PID   PORTS  RESTARTS  UPTIME  DETAIL
api     running  4121  8080   0         0s      -
web     running  4122  5173   0         0s      -
worker  running  4123  -      0         0s      -
```

## Why

- **No more scattered terminals.** One file describes every app, its command,
  working directory, and environment.
- **No more accidental kills.** Apps run under a daemon, not your terminal, so
  closing a tab doesn't stop them. Crashed apps restart with backoff.
- **See what's running where.** `h2ams status` shows each app's state, PID,
  and the TCP ports it listens on.
- **Logs that survive restarts.** Output from every run is kept on disk.

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
```

| Field | Required | Default | Description |
| --- | --- | --- | --- |
| `version` | yes | | Schema version. Must be `1`. |
| `default_environment` | no | all apps | Environment `h2ams up` starts when `--env` is not given. |
| `environments.<name>.apps` | no | | List of app names started together. |
| `apps.<name>` | yes (at least one) | | One managed app. Names may use letters, digits, `.`, `_`, and `-`. |
| `apps.<name>.command` | yes | | Command line, run through `/bin/sh -c` (`cmd /C` on Windows). |
| `apps.<name>.cwd` | no | config file's directory | Working directory. Relative paths are resolved from the config file. |
| `apps.<name>.env` | no | | Extra environment variables, added to the daemon's environment. |
| `apps.<name>.restart.policy` | no | `on-failure` | `always`, `on-failure` (non-zero exit only), or `never`. |
| `apps.<name>.restart.max_attempts` | no | `5` | Restarts in a row before the app is marked `failed`. The count resets once a run lasts 10s. |
| `apps.<name>.restart.backoff` | no | `1s` | Delay before the first restart; doubles each time, up to 30s. Go duration syntax (`500ms`, `2s`). |

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
| `h2ams status` | Show each app's state, PID, ports, restart count, uptime, and last error. |
| `h2ams logs <app> [-n N] [-f]` | Show the last N lines (default 100, `0` for all) of an app's output; `-f` follows new output. |
| `h2ams down` | Stop every app and the daemon. |

Every command accepts `-c, --config PATH` (default `h2ams.yml`). Run
`h2ams <command> --help` for details and examples.

### App states

| State | Meaning |
| --- | --- |
| `starting` | Launched, not yet confirmed running. |
| `running` | Up and supervised. |
| `restarting` | Exited; waiting out the backoff before starting again. |
| `stopped` | Exited cleanly (and the policy doesn't restart it), or stopped by `h2ams down`. |
| `failed` | Crashed and won't be restarted: the policy is `never`, or `max_attempts` ran out. |

## How it works

- **One daemon per config file.** `h2ams up` starts `h2ams daemon` detached
  from your terminal. Commands find it through a Unix socket keyed by the
  config file's absolute path, so separate projects never collide.
- **Process groups.** Each app runs in its own process group. Stopping an app
  sends `SIGTERM` to the whole group (so `npm run dev` takes its children with
  it), then `SIGKILL` after 5 seconds.
- **Logs.** Each app's stdout and stderr are appended to `logs/<app>.log` in
  the state directory, along with `[h2ams ...]` lines marking every start,
  exit, restart, and stop. A log over 10 MB is rotated to `<app>.log.1` when
  the daemon next starts.
- **State directory.** `~/Library/Caches/h2ams/<hash>/` on macOS,
  `~/.cache/h2ams/<hash>/` on Linux. Override the base with `H2AMS_STATE_DIR`.
  It holds the socket, `daemon.log` (the daemon's own messages), and `logs/`.

## Platform support

| | macOS | Linux | Windows |
| --- | --- | --- | --- |
| Start, stop, restart | ✅ | ✅ | best-effort (`taskkill`), untested |
| Port detection | ✅ via `lsof` | ✅ via `/proc` | not yet |
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

## Development

```sh
go test ./...          # unit and integration tests (builds the binary once)
go test -short ./...   # skip the binary end-to-end test
go vet ./...
```

Set the version at build time with
`-ldflags "-X github.com/yvyty/h2ams/internal/cli.Version=v0.1.0"`.
