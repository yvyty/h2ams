# h2ams-tray

`h2ams-tray` puts a dot in your menu bar (macOS) or system tray (Linux,
Windows) that shows how all your h2ams projects are doing, with a menu to
start, stop, and switch their environments.

| Dot | Meaning |
| --- | --- |
| Grey | Nothing is running. |
| Green | Every app in every running project is running. |
| Amber | Something is starting, restarting, or stopped. A `completed` one-shot job counts as healthy. |
| Red | An app has failed. |

The menu lists every project h2ams has run (found through the `project.json`
each daemon writes in its state directory, see [api.md](api.md#connecting)):

```
h2ams: 1 project running, 1 app failed
──────────────
shop: dev, 1 failed        ▸  api: running :8080
                              worker: failed
                              ──────────────
                              ✓ dev (default)
                              Switch to testing
                              ──────────────
                              Open dashboard
                              Stop
blog: stopped              ▸  Start all apps
──────────────
Refresh
Quit h2ams-tray
```

It refreshes every 3 seconds and right after each action. Actions run the
`h2ams` CLI (`up --env`) or call the daemon API (`down`), so they behave
exactly like the commands. When an action fails, the tray shows a desktop
notification with the error.

## Install

`h2ams-tray` is a separate binary because tray support needs cgo on macOS,
while `h2ams` itself stays cgo-free. Every install method in the
[README](../README.md#install) (Homebrew, release archives, `go install`)
puts both binaries in the same directory, which is what `h2ams up` needs.

You don't need to start it yourself: `h2ams up` starts the `h2ams-tray`
installed next to it, so the icon appears whenever h2ams is running. Only
one tray runs at a time (it holds `tray.lock` in the state directory), and
it serves every project, so `up` in a second project doesn't add a second
icon. Quitting the tray leaves your apps running; the next `h2ams up` brings
it back.

`up` skips the tray when:

- you pass `--no-tray` or set `H2AMS_NO_TRAY=1`;
- you are connected over SSH;
- on Linux, there is no graphical session (`DISPLAY` or `WAYLAND_DISPLAY`).

You can also start it by hand with `h2ams-tray &`. Its output goes to
`tray.log` in the state directory when `up` starts it.

### Building per platform

| Target | Command | Needs |
| --- | --- | --- |
| macOS | `go build ./cmd/h2ams-tray` | Xcode command-line tools (cgo). Build on a Mac. |
| Linux | `CGO_ENABLED=0 GOOS=linux go build ./cmd/h2ams-tray` | Nothing to build. At runtime, a desktop with StatusNotifierItem support (KDE, GNOME with the AppIndicator extension, XFCE, ...). |
| Windows | `GOOS=windows go build -ldflags -H=windowsgui ./cmd/h2ams-tray` | Nothing. `-H=windowsgui` avoids a console window. |

Start it at login with your desktop's usual mechanism (macOS Login Items, a
`~/.config/autostart/h2ams-tray.desktop` file, or the Windows Startup folder).

## Verification

Automated (`go test ./internal/tray/`):

- health, summary, and menu contents for running, failed, stopped, and
  environment-less projects;
- project discovery, duplicate names, and broken configs;
- icons decode as PNG (and ICO on Windows) with the right colors;
- an end-to-end run against the real `h2ams` binary: start an environment,
  switch to another, stop, checking the menu at each step.

The cross-builds for macOS (arm64, amd64), Linux (amd64, arm64), and Windows
(amd64) succeed.

Drawing the icon and clicking the menu need a real desktop, so check them by
hand before a release:

1. Run `h2ams-tray`. A grey dot appears, and the menu says "nothing running".
   Run `h2ams-tray` again: it exits at once and no second dot appears.
2. Quit the tray. In a project, run `h2ams up`: it prints "h2ams-tray started
   in the menu bar", the dot appears, and within 3 seconds it turns green with
   the project "running" and its apps and ports.
3. Kill one app's process so it restarts: the dot turns amber, then green.
4. Make an app fail (for example `command: exit 1` with `max_attempts: 1`):
   the dot turns red and the title shows "1 failed".
5. From the menu, choose **Switch to** another environment. The apps change
   and the check mark moves.
6. Choose **Open dashboard**: the browser opens it.
7. Choose **Stop**: the dot turns grey and the project offers **Start**.
8. Choose **Start**: the project runs again.
9. Rename the config file so `up` fails, then choose **Start**: a
   notification shows the error.
10. Choose **Quit h2ams-tray**: the icon disappears and the apps keep
    running.
