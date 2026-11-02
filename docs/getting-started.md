# Getting started

This guide takes a project with two apps, an API and a frontend, from
"one terminal tab each" to being managed by h2ams. It takes about five
minutes.

To follow along without a real project, each app here is Python's built-in
web server. Swap in your own commands (`go run`, `npm run dev`, `rails s`, ...)
as you go.

## 1. Install

```sh
brew install --cask yvyty/tap/h2ams   # or download a release, or go install
h2ams --version
```

See the [README](../README.md#install) for release downloads, Windows, and
`go install` (including adding `~/go/bin` to your `PATH`).

## 2. Create a config

From the root of your project:

```console
$ mkdir -p api web
$ h2ams init
Created /Users/you/my-project/h2ams.yml
Edit it to describe your apps, then run "h2ams up".
```

The template contains two example apps with every option commented. Replace
it with your own apps:

```yaml
version: 1
default_environment: dev

environments:
  dev:
    apps: [api, web]

apps:
  api:
    command: python3 -m http.server 8080 --bind 127.0.0.1
    cwd: ./api
    env:
      PYTHONUNBUFFERED: "1"

  web:
    command: python3 -m http.server 5173 --bind 127.0.0.1
    cwd: ./web
    env:
      PYTHONUNBUFFERED: "1"
    restart:
      policy: always
```

Each app needs a `command`. The `cwd` is relative to the config file. With no
`restart` block an app is restarted when it crashes (`on-failure`), up to 5
times in a row. The [configuration reference](../README.md#configuration) lists
every field.

## 3. Start everything

```console
$ h2ams up
h2ams started (pid 80081, log ~/Library/Caches/h2ams/737a885f5ac5/daemon.log)

APP  STATE    PID    PORTS  RESTARTS  UPTIME  DETAIL
api  running  80083  -      0         0s      -
web  running  80082  -      0         0s      -
```

`up` checks the config first and refuses to start if anything is wrong. Then
it starts a background daemon and returns, so you get your terminal back.
Closing the terminal doesn't stop your apps.

## 4. Check status and ports

Ports appear once each app starts listening, usually within a second:

```console
$ h2ams status
APP  STATE    PID    PORTS  RESTARTS  UPTIME  DETAIL
api  running  80083  8080   0         9s      -
web  running  80082  5173   0         9s      -
```

PORTS covers the app and anything it spawned, so a dev server launched by
`npm run dev` still shows up under `web`.

## 5. Watch a crash get fixed for you

Kill the frontend the way an accident would:

```console
$ kill $(pgrep -f "http.server 5173")
$ h2ams status
APP  STATE    PID    PORTS  RESTARTS  UPTIME  DETAIL
api  running  80083  8080   0         11s     -
web  running  80303  5173   1         1s      -
```

h2ams noticed the exit, waited out the backoff (1s, doubling on each
consecutive restart), and started `web` again with a new PID.

## 6. Read the logs

```console
$ h2ams logs web
[h2ams 2026-11-02T00:55:35-03:00] web: started (pid 80082)
Serving HTTP on 127.0.0.1 port 5173 (http://127.0.0.1:5173/) ...
[h2ams 2026-11-02T00:55:45-03:00] web: exited: signal: terminated
[h2ams 2026-11-02T00:55:45-03:00] web: restarting in 1s (attempt 1 of 5)
[h2ams 2026-11-02T00:55:46-03:00] web: started (pid 80303)
Serving HTTP on 127.0.0.1 port 5173 (http://127.0.0.1:5173/) ...
```

Output from every run is kept, with `[h2ams ...]` lines marking what happened.
Use `-n 20` for the last 20 lines, or `-f` to keep streaming (Ctrl-C to stop):

```sh
h2ams logs api -f
```

Python is set to `PYTHONUNBUFFERED: "1"` above because Python buffers output
when it isn't writing to a terminal, so without it the logs would lag.

## 7. Use the dashboard

`h2ams up` and `h2ams status` print a dashboard address:

```console
$ h2ams status
Environment: dev
Dashboard:   http://127.0.0.1:9999

APP  STATE    PID    PORTS  CPU   MEM       RESTARTS  UPTIME  DETAIL
api  running  80083  8080   0.0%  24.3 MiB  0         1m2s    -
web  running  80303  5173   0.0%  24.4 MiB  1         51s     -
```

Open it in your browser. You get the same table, refreshed every 2 seconds,
with Start, Stop, and Restart buttons. Click an app to stream its logs live.
The URL remembers the selection (`#app=web`), so you can bookmark it.

The same actions are on the command line:

```sh
h2ams restart api   # stop and start again
h2ams stop web      # stop one app; it stays stopped
h2ams start web     # start it again
```

## 8. Stop everything

```console
$ h2ams down
Stopped 2 app(s) and the h2ams daemon
```

Each app and its children get `SIGTERM`, then `SIGKILL` if they are still
running after 5 seconds. Logs stay on disk, so `h2ams logs web` still works.

## Next steps

- **Group apps into environments.** Add an `environments.backend` entry and
  start only that set with `h2ams up --env backend`. `h2ams envs` lists them.
- **Get told about crash loops.** Add `notifications: {crash_loop: true}` for
  a desktop notification when an app keeps crashing.
- **Tune restarts.** Use `policy: never` for one-off jobs, and raise
  `max_attempts` for apps that take a while to become healthy.
- **Run several projects.** Each config file gets its own daemon. Use
  `--config path/to/h2ams.yml` to manage a project from anywhere.
- **Read the full example.** [examples/h2ams.yml](../examples/h2ams.yml)
  shows a Go API, a Vite frontend, and a Python worker.
- **Get help on any command.** `h2ams <command> --help` shows its options and
  examples.
