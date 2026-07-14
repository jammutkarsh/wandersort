# CLAUDE.md

Guidance for AI agents working in this repo. Coding rules live in `AGENTS.md`
(symlink to `.agents/AGENTS.md`) — read it first. This file is the **map**: what
lives where and how the pieces fit.

## What WanderSort is

A black-box media organizer. Feed it unorganized photos/videos; it produces an
ordered folder hierarchy. Pipeline runs in ordered phases per scan session:

1. **Scan** — walk roots, build a file index.
2. **Hash + EXIF** — content-hash for duplicate detection, extract EXIF for later.
3. **Score** — within a duplicate group, elect one master copy (same bytes,
   different storage context — e.g. folder named `Goa Trip 2024`).
4. **VFS** *(not built yet)* — place masters into a hierarchy, using the user's
   prior folder-naming as context when EXIF is absent.

## Entry point & CLI

- `main.go` — builds `config.Defaults()`, constructs `cli.App{Config}`, calls
  `app.Execute()`. **No logger here** — it's built later (see below).
- `internal/cli/` — cobra CLI. One file per command:
  - `root.go` — root cmd, `App` struct, DB/exiftool/resolver lazy-init helpers,
    viper setup, config-override resolution. **`PersistentPreRunE` is the single
    place** flags/env are resolved (`applyOverrides`) and the logger is built —
    so `--debug` / `--output-path` / env vars actually take effect before any
    logging or DB work. Don't rebuild the logger in `main.go`.
  - `scan.go` — `scan` cmd (the pipeline). Runs **synchronously** in the
    foreground (`Service.RunScan` → `Workflow.RunScan`) so the user watches
    progress and the exit code reflects pipeline success. `--paths/-p` is
    repeatable + comma-friendly (`StringSlice`).
  - `serve.go` — `serve` cmd, HTTP API (gin) + swagger. Same workflow, long-lived.
  - `setup.go` — downloads exiftool + location DB. **Optional** — scan/serve
    auto-install missing deps via `App.EnsureDependencies`. Uses a *non-blocking*
    install lock: if a scan/serve is already installing, setup steps aside.
  - `report.go` — read-only summary of last scan (opens its own RO sqlite conn).
  - `report_issue.go` — `report-issue` cmd: zips the log (renamed
    `wandersort.log`) + `about.txt`; db opt-in via `--include-db` (holds paths/GPS).
  - `reset.go` — wipe scan data (confirm prompt unless `--yes`).
  - `lock.go` — thin CLI layer over `pkg/utils` locking: `acquireOutputLock`
    (one scan/serve per output dir) + the styled "already running" message, and
    the lock filename constants. The generic mechanics live in
    `pkg/utils/lock.go` (`utils.Acquire`, `ErrLockHeld`) so the pipeline and
    future callers reuse them. Install coordination uses `utils.Acquire` with
    `installLockFileName`: blocking for scan/serve, non-blocking for setup.
  - `help.go` — custom lipgloss-styled help renderer.

Config precedence: **flag > env > default**. Env names are the uppercased flag;
`AutomaticEnv` covers the hyphen-free flags (`WORKERS`, `PORT`, …), and the one
hyphenated flag is bound explicitly (`--output-path` → `OUTPUT_PATH` via
`v.BindEnv`). Keep new flags hyphen-free so they need no explicit bind. Defaults
come from `pkg/config`.

## Core pipeline (`pkg/core/`)

- `workflow/` — orchestrator. Two entry points over the same `runSession` phase
  loop (scan→hash→score): `RunScan` (synchronous, CLI) and `SubmitScan`
  (background goroutine, `serve`; `Close()` waits). `helpers.go` = session
  status/finalize writes.
- `scanner/` — phase 1. Bounded-worker directory walk. `capture.go` = capture-
  time extraction.
- `hasher/` — phase 2. BLAKE3 over full bytes + exiftool EXIF. **Known gap
  (TODO #22 in workflow.go):** full-byte hash means pixel-identical files with
  differing metadata land in separate groups.
- `scorer/` — phase 3. Elects master via folder-naming heuristics.

## HTTP layer (`internal/api/`)

Only used by `serve`. Standard handler→service→repository split per domain:

- `pipeline/` — start scans, query counts. `service.go` `prepareScanRoots`
  canonicalizes + prunes nested roots (O(n) after lex sort).
- `admin/` — reset/admin ops.
- `interfaces.go`, `middleware.go`, `response.go`, `errors.go` — shared gin glue.

## Supporting packages (`pkg/`)

- `config/` — `Defaults()`, hardcoded config only (no env reads).
- `db/` — sqlite (`modernc.org/sqlite`) open/migrate/retry; `writer.go` batched
  writes; `migrations/` numbered Go migrations.
- `logger/` — slog-based `Logger` interface; fans out to two handlers. The
  **console** handler (`console.go`) is deliberately minimal for CLI users: a
  coloured level tag + message + dimmed `key=value` attrs, no timestamp/source.
  It shows **only user-facing lines and warnings/errors** — tag a milestone with
  `logger.UserKey` (`log.Info("Scanning…", logger.UserKey, true)`); everything
  untagged is developer detail that goes to the file only. The `sessionId` attr
  is stripped from console lines (printed once at session start, in the message
  text) to avoid spam. `--debug` bypasses both filters and shows every record.
  The **JSON file** handler keeps timestamp + source (`AddSource`) and every
  attr (incl. `sessionId`) — that's what `report-issue` ships. Never stdlib `log`.
- `location/` — offline reverse-geocode resolver + its own sqlite DB. `Setup()`
  downloads DB+meta if missing (idempotent); `exiftool.Setup()` is the same idea
  for the binary. Both are called lazily by `EnsureDependencies`.
- `classifier/` — extension-based media type detection (`classifier.go`) and
  `ParseMetadata` (`models.go`), which decodes exiftool JSON into a generic map
  and reads only the `CommonMetadata` keys it needs. **Tolerant by design:** a
  type mismatch on any single exiftool tag no longer fails the whole decode
  (this replaced 11 giant strict per-format structs). No per-format files.
- `exiftool/` — bundled exiftool wrapper + verify.
- `path/` — path canonicalization / home-relative helpers.
- `utils/download.go` — atomic HTTP download (temp file + rename).
- `utils/lock.go` — generic PID/O_EXCL file lock (`Acquire`, `Unlock`,
  `ErrLockHeld`); stale-lock reclaim; blocking waits honour ctx.

## Conventions that bite if ignored

- Every pipeline function takes `sessionID uuid.UUID` right after `ctx`; log key
  is `"sessionId"` (camelCase). Progress is surfaced **only** through logs keyed
  by `sessionId` — no separate status channel.
- Bounded worker pools, never fire-and-forget goroutines.
- Wrap errors with `%w`. Upserts over SELECT-then-INSERT.

## Build / test

```bash
make build     # -> bin/wandersort
make test      # go test -v ./...
make lint      # gofumpt -l -w .
make swagger   # regenerate docs/ (needs swag)
go build ./... # quick compile check
```

## Open cleanup notes (not yet done)

- `report.go` builds a raw sqlite DSN + inline SQL in the CLI layer, bypassing
  `pkg/db`/repository. Read-only by design, but a layering shortcut — move to a
  repository method if it grows.
- **Concurrency wall:** `AcquireLock` (lock.go) takes an exclusive PID lock on
  the output dir, so only one scan *or* serve runs against a dir at a time. Log
  lines are already `sessionId`-tagged, so multiple sessions sharing one log
  file interleave cleanly (consumers filter by id) — logs are **not** the wall.
  The lock is. Running scans concurrently would need per-session isolation or a
  single owner that multiplexes sessions.
