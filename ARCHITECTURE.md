> SPDX-License-Identifier: AGPL-3.0-or-later
>
> Copyright (c) 2026 Utkarsh Chourasia

# Architecture

How WanderSort is put together — the map of the codebase for contributors. If
you're a *user*, start with the [README](README.md); this document is about the
code.

WanderSort is a single Go binary. It runs as a foreground CLI (`scan`, `report`,
…) and can also expose the same pipeline over a local REST API (`serve`). No
daemon, no external services — everything is local: a SQLite file, a bundled
ExifTool binary, and an offline location database.

## The pipeline

Every `scan` runs the same ordered phases within one **scan session**. Nothing
on disk is moved or copied during any of them — the pipeline only *reads* files
and *writes* to its own database.

| # | Phase | What it does |
| --- | --- | --- |
| 1 | **Scan** | Walk the given roots, classify every media file, record it in the file index. |
| 2 | **Hash** | BLAKE3-hash each file's full bytes, for duplicate detection. |
| 3 | **EXIF** | Read each hashed file's metadata once via ExifTool. Its own phase, with its own timing and progress — a file that ExifTool can't read is still a usable file, so a failure here never fails the session. |
| 4 | **Score** | Within a duplicate group, elect one *master* copy using folder-naming heuristics. |
| 5 | **VFS** | Propose a destination folder tree for every live master in the library. Purely a proposal — nothing touches disk. |

The **move/apply** stage — where the user approves a proposal and files are
actually relocated — is not built yet. Everything up to and including the VFS
proposal is.

## Layout

```bash
main.go            entry point — build config, hand off to the CLI
internal/cli/      cobra commands (one file per subcommand)
internal/api/      HTTP handlers for `serve` (only used by serve)
pkg/core/          the pipeline: scanner, hasher, exif, scorer, vfs, workflow
pkg/               supporting packages (db, config, logger, …)
```

`internal/` is code with no reuse contract outside this binary; `pkg/` is code
written so a future entry point (e.g. a TUI) could reuse it.

## Entry point & CLI (`internal/cli/`)

`main.go` builds `config.Defaults()`, constructs the cobra `App`, and calls
`Execute()`. It deliberately builds **no logger** — flags, env vars, and the
logger are all resolved in one place, `root.go`'s `PersistentPreRunE`, so that
`--debug` / `--output-path` / env overrides take effect before any logging or DB
work happens.

One file per command:

| Command | Purpose |
| --- | --- |
| `scan` | Run the full pipeline synchronously in the foreground. `--paths/-p` is repeatable and comma-friendly. |
| `serve` | Long-lived HTTP API (gin) with Swagger docs. Same pipeline, background sessions. |
| `report` | Read-only per-session summary — scanned / hashed / duplicate counts, newest first. `--vertical/-x` for narrow terminals. |
| `setup` | Pre-download dependencies (ExifTool + location DB). Optional — `scan`/`serve` auto-install what's missing. |
| `reset` | Wipe all scan data (prompts unless `--yes`). |
| `report-issue` | Zip up logs (and optionally the DB) for a bug report. |

Config precedence is **flag > env > default**. Env names are the uppercased
flag; keep new flags hyphen-free so `AutomaticEnv` picks them up without an
explicit bind. Defaults live in `pkg/config` (hardcoded, no env reads).

Concurrency is bounded by an exclusive PID lock on the output directory
(`pkg/lock`), so only one `scan` *or* `serve` runs against a given directory at a
time. Styled terminal output (colours, help) lives in `pkg/style` and `help.go`.

## Core pipeline (`pkg/core/`)

- **`workflow/`** — the orchestrator. Two entry points over the same phase loop:
  `RunScan` (synchronous, used by the CLI) and `SubmitScan` (background
  goroutine, used by `serve`). Rejects a new session whose roots overlap an
  in-flight one.
- **`scanner/`** — phase 1. Bounded-worker directory walk; files are identified
  by absolute `(dir, name)`. Rows not re-seen in a clean walk are *soft-deleted*
  and hard-deleted after 30 days, so unplugged drives and transient errors
  self-heal instead of vanishing permanently.
- **`hasher/`** — phase 2. BLAKE3 over full file bytes. Inserts each file's
  metadata row holding only the hash. *Known gap:* full-byte hashing means two
  pixel-identical files with different metadata land in separate groups.
- **`exif/`** — phase 3. One ExifTool run per file, filling in the row phase 2
  inserted. It claims its own rows (`HASHED → ANALYZING → ANALYZED`), so an
  interrupted run resumes without re-hashing anything. Sidecars (`.AAE`) are
  skipped — they carry no EXIF.
- **`scorer/`** — phase 4. Elects a master within each duplicate group over the
  live (non-deleted) rows; re-promotes the lone survivor when a group shrinks.
- **`vfs/`** — phase 5. Proposes destinations for every live master from the
  persisted metadata (never re-reads the files). Each run replaces the whole
  proposal set.

## HTTP layer (`internal/api/`)

Only used by `serve`. Standard handler → service → repository split per domain,
mounted under `/internal/v1`:

| Method | Route | Purpose |
| --- | --- | --- |
| `POST` | `/internal/v1/pipeline/start` | Start a scan session. |
| `GET` | `/internal/v1/pipeline/count` | File counts. |
| `POST` | `/internal/v1/admin/reset` | Wipe scan data. |
| `GET` | `/ping` | Health check. |
| `GET` | `/internal/v1/swagger/*` | Swagger UI. |

## Supporting packages (`pkg/`)

- **`config/`** — `Defaults()`; hardcoded config only.
- **`db/`** — SQLite (`modernc.org/sqlite`) open/migrate/retry, a batched bulk
  writer, and numbered Go migrations. All timestamps are stored as UTC
  fixed-width nanoseconds and converted to local time only at display.
- **`classifier/`** — extension-based media-type detection and a *tolerant*
  ExifTool-JSON decoder (a type mismatch on one tag no longer fails the whole
  decode).
- **`exiftool/`** — bundled ExifTool wrapper + verification.
- **`location/`** — offline reverse-geocode resolver with its own SQLite DB;
  self-downloads if missing.
- **`volume/`** — best-effort volume-UUID resolution per root (for future drive
  re-anchoring) and a free-space preflight.
- **`logger/`** — slog-based logger fanning out to two handlers: a minimal
  coloured **console** handler that shows only user-facing lines (tagged with
  `logger.UserKey`) and a full **JSON file** handler (what `report-issue`
  ships). `--debug` bypasses both filters.
- **`lock/`**, **`style/`**, **`path/`**, **`utils/`** — file locking, terminal
  styling, path canonicalization, atomic downloads.

## Conventions

- Every pipeline function takes `sessionID uuid.UUID` right after `ctx`; the log
  key is `sessionId`. Progress is surfaced **only** through session-keyed logs —
  there is no separate status channel.
- Bounded worker pools, never fire-and-forget goroutines.
- Wrap errors with `%w`. Prefer upserts over SELECT-then-INSERT.
