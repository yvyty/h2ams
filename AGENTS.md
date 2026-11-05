# AGENTS.md — h2ams

Instructions for coding agents working in this repository. h2ams (High
Availability Application Management Service) is a Go CLI plus background daemon
that runs and supervises local dev apps from one `h2ams.yml`, with a companion
menu-bar app, `h2ams-tray`.

User-facing behaviour, config format, and per-OS support live in `README.md`
and `docs/`. Keep this file about how to work in the repository; do not copy
product decisions into it.

---

## Layout

```text
.
├── cmd/
│   ├── h2ams/          # CLI + daemon entrypoint (cobra; `h2ams daemon` is a subcommand)
│   └── h2ams-tray/     # tray binary (fyne.io/systray)
├── internal/
│   ├── cli/            # command tree, detaching the daemon, `init` template
│   ├── config/         # h2ams.yml model and validation
│   ├── daemon/         # supervisor, Unix-socket HTTP API (/api/v1), dashboard.html (embedded), Docker apps
│   ├── process/        # process groups, start/stop/restart with backoff
│   ├── ports/          # listening ports: lsof (macOS), /proc (Linux), Windows stub
│   ├── resources/      # CPU and memory: ps (macOS), /proc (Linux), Windows stub
│   ├── logs/ notify/   # per-app logs with rotation; desktop notifications
│   ├── tray/           # tray model, autostart, single-instance lock
│   └── testutil/       # helper processes for tests
├── docs/               # api.md, getting-started.md, tray.md (shipped in archives)
├── examples/h2ams.yml
├── pkg/                # empty placeholder; nothing public is exported yet
├── .goreleaser.yaml    # release builds, archives, Homebrew cask
└── .github/workflows/  # ci.yml, release-check.yml (PRs touching release files), release.yml (tags)
```

Platform code is split by build tags and file suffixes (`_unix.go`,
`_windows.go`, `_linux.go`). When you add behaviour to one, add or stub the
others so every target still builds.

---

## Commands

```sh
go build ./cmd/h2ams ./cmd/h2ams-tray
go test ./...                       # full suite; some tests build the binaries
go test -short ./...                # skip the binary-building tests
go test -race -count=1 ./...        # what CI runs, on Linux, macOS and Windows
go vet ./...
gofmt -l .                          # CI fails on any output

H2AMS_TEST_DOCKER=1 go test -run RealDocker ./internal/daemon/   # needs Docker
H2AMS_TEST_TRAY=1   go test -run TrayBinarySmoke ./internal/tray/ # starts the real tray
```

Go version comes from `go.mod` (1.27.2). Docker tests default to a fake
`docker` script via `H2AMS_DOCKER`, so the normal suite needs no Docker.

---

## Invariants

- **`h2ams` stays cgo-free.** CI cross-compiles it with `CGO_ENABLED=0` for
  every OS/arch. Only `h2ams-tray` on macOS uses cgo, which is why the tray is
  a separate binary and why releases build on a macOS runner. Do not add a cgo
  dependency to `cmd/h2ams` or anything it imports.
- **One daemon per config file.** State lives in
  `<user cache dir>/h2ams/<first 12 hex chars of sha256(abs config path)>/` (socket,
  `daemon.log`, `logs/`, `project.json`). Tests must set `H2AMS_STATE_DIR` to
  a temp dir so they never touch a real user's daemons.
- **The CLI talks to the daemon over the same HTTP API as plugins.** API
  changes are bound by the `/api/v1` contract in `README.md#versioning` and
  `docs/api.md`: fields and states may be added, never removed or renamed. A
  breaking change needs `/api/v2` served alongside `v1`.
- **Stops act on the whole process group.** `SIGTERM` to the group, then
  `SIGKILL` after 5 s. Windows has no graceful console stop and kills the tree
  at once; that is documented, not a bug to fix.
- **Pre-1.0 semver.** A minor release may change config or CLI only if the
  CHANGELOG says how to migrate; patch releases only fix bugs.

---

## Releases

- Pushing a `v*` tag runs GoReleaser (`release.yml`) and creates a **draft**
  release that is reviewed and published by hand. `-rc.N` tags are
  prereleases.
- The Homebrew cask in `yvyty/homebrew-tap` is **generated** by GoReleaser and
  says `DO NOT EDIT`. Change `.goreleaser.yaml` instead. It is published only
  for final releases, and only when the `HOMEBREW_TAP_TOKEN` secret exists.
- Keep the tap token written exactly as `{{ .Env.HOMEBREW_TAP_TOKEN }}`.
  GoReleaser rejects other forms (see #5 and #6).
- macOS binaries are ad-hoc signed by build hooks. Check `.goreleaser.yaml`
  for whether Developer ID signing and notarization are enabled before
  editing the cask's quarantine step, the release footer, or the README
  install notes. Those three must stay consistent with each other.
- Any PR touching `.goreleaser.yaml` or the release workflows runs
  `release-check.yml`: `goreleaser check`, a snapshot build, and smoke runs of
  the archives on all three OSes. Run `goreleaser check` locally first.

---

## Workflow

- `main` is protected: changes land by pull request with the required status
  checks green. Branches are named `feat/…`, `fix/…`, `docs/…`, `release/vX.Y.Z`.
- Commits follow Conventional Commits with a scope where it helps
  (`fix(process): …`, `fix(release): …`); one logical change per commit.
- Add user-visible changes under `## [Unreleased]` in `CHANGELOG.md` (Keep a
  Changelog format).
- `.plan/` holds the git-ignored development plan (branches → milestones →
  issues). Comments may cite plan issues such as "Issue 4.2.2". Treat the plan
  as local context, never commit it, and do not assume another checkout has it.
- This file is required at the repo root by the yvyty org convention; do not
  delete or move it.
