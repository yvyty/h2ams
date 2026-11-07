# Changelog

All notable changes to h2ams. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/) as described in the
[README](README.md#versioning).

## [Unreleased]

## [0.2.0] - 2026-11-07

### Added
- `h2ams validate` checks the config with the same rules as `h2ams up`, plus
  that every app's `cwd` exists, without starting anything. `--env NAME` also
  checks the environment, and `--json` prints `{"valid", "config",
  "problems"}` for editors and scripts. It exits non-zero when the config is
  invalid.

### Changed
- macOS binaries are signed with a Developer ID and notarized by Apple when
  the release has the signing secrets, so Gatekeeper accepts them and the
  Homebrew cask no longer strips the quarantine flag.

## [0.1.1] - 2026-11-02

### Fixed
- An app whose `cwd` doesn't exist (or isn't a directory) now fails at once
  with "working directory … does not exist (check cwd in h2ams.yml)",
  instead of restarting with the misleading
  `fork/exec /bin/sh: no such file or directory`.

## [0.1.0] - 2026-11-02

The first release.

### Supervisor and CLI
- Describe your apps in `h2ams.yml`; `h2ams init` writes a commented template.
- `h2ams up` starts a background daemon (one per config file) that runs every
  app and restarts it by policy (`always`, `on-failure`, `never`) with
  exponential backoff and a maximum number of attempts.
- `h2ams status` (with `--json`), `logs` (`-f`, `-n`), `start`, `stop`,
  `restart`, `down`, `envs`.
- Each app runs in its own process group; stopping it stops everything it
  started, with a grace period before it is killed.
- Environments (`environments:` and `default_environment:`) start a subset of
  apps: `h2ams up --env testing`.
- One-shot jobs that exit 0 show as `completed`.

### Observability
- Listening TCP ports, CPU, and memory per app (macOS and Linux), including
  child processes that moved to their own process group.
- Per-app log files with lifecycle lines, rotated at 10 MB.
- Crash-loop detection, logged and optionally sent as a desktop notification.

### Dashboard and API
- Web dashboard on `http://127.0.0.1:9999`: status, start/stop/restart, and
  live logs.
- Versioned local HTTP API, `/api/v1`, on the Unix socket and the dashboard
  port, used by the CLI, the dashboard, and plugins. See
  [docs/api.md](docs/api.md).

### Docker
- Apps can run a container (`docker:` with `image`, `ports`, `volumes`,
  `args`) under the same supervision; `status` shows the container state.
- Container apps fail at once with a clear message when the docker CLI is
  missing. Other apps are unaffected.

### Menu-bar app
- `h2ams-tray` shows a colored dot for all projects and a menu to start,
  stop, and switch environments. `h2ams up` starts it (one instance) when it
  is installed next to `h2ams`; skip with `--no-tray` or `H2AMS_NO_TRAY=1`.

### Platforms
- macOS, Linux, and Windows (10 1803+), tested in CI on every push.
- On Linux, apps get their full grace period on stop even though `/bin/sh`
  doesn't `exec` the command.
- On Windows, apps run through `cmd.exe` and are stopped immediately (console
  programs can't be asked to close from outside); container apps get a
  `cmd.exe` command line.

[Unreleased]: https://github.com/yvyty/h2ams/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/yvyty/h2ams/compare/v0.1.1...v0.2.0
[0.1.1]: https://github.com/yvyty/h2ams/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/yvyty/h2ams/releases/tag/v0.1.0
