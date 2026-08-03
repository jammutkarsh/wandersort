> SPDX-License-Identifier: AGPL-3.0-or-later
>
> Copyright (c) 2026 Utkarsh Chourasia

# Architecture

How WanderSort is put together — the map of the codebase for contributors. If
you're a *user*, start with the [README](README.md); this document is about the
code.

WanderSort is a single Go binary and a foreground CLI (`config`, `scan`,
`review`, `issue`, `reset`). No daemon, no server, no external services —
everything is local: a SQLite file, a downloaded ExifTool binary, and an offline
location database.

## The pipeline

Every `scan` runs the same ordered phases. Nothing on disk is moved or copied
during any of them — the pipeline only *reads* files and writes to its own
database. There is no persisted run record: the output database is the durable
state, and an exclusive lock on the output directory is what guarantees only
one scan is ever running against it.

| # | Phase | What it does |
| --- | --- | --- |
| 1 | **Scan** | Walk the given roots, classify every media file, record it in the file index. |
| 2 | **Hash** | BLAKE3-hash each file's full bytes, for duplicate detection. |
| 3 | **EXIF** | Read each hashed file's metadata once via ExifTool. Its own phase, with its own timing and progress — a file ExifTool can't read is still a usable file, so a failure here never fails the run. |
| 4 | **Score** | Within a duplicate group, elect one *master* copy using folder-naming heuristics. |
| 5 | **VFS** | Propose a destination folder tree for every live master in the library. Purely a proposal — nothing touches disk. |

`wandersort review` then presents that proposal as an editable tree and writes
the approved plan. The **move/apply** stage — where approved files are actually
relocated — is not built yet.

## Layout

```bash
main.go            entry point — build config, hand off to the CLI
internal/cli/      cobra commands (one file per subcommand)
internal/review/   the full-screen review TUI over the VFS proposal
pkg/core/          the pipeline: scanner, hasher, exif, scorer, vfs, workflow
pkg/               supporting packages (db, config, location, logger, tui, …)
```

`internal/` is code with no reuse contract outside this binary; `pkg/` is code
written so a future entry point could reuse it.

## The dependency graph

Imports point **down** only, and every package sits at exactly one level. If a
change would make an edge point back up — a lower package reaching for a
higher one's constant, style, or type — that is the signal the logic is in the
wrong package, not that the edge is needed.

```
main
 └── internal/cli ──────── internal/review
       │                        │
       ├── core/workflow ───────┤
       │     └── core/{scanner,hasher,exif,scorer,vfs}
       │                        │
       ├── install             │
       └────────────────┬──────┘
                        │
        config · location · exiftool · classifier · volume · tui
                        │
              db ── db/migrations
                        │
             path · logger · lock
```

Three rules keep it that shape:

- **A module owns its whole domain.** `pkg/location` decides everything about
  what a place is called — the qualifier a name needs, how it reads in a list,
  what is safe to write as a directory, which spelling a saved anchor is stored
  as, and how a rename picker ranks completions. A caller asks; it does not
  re-derive.
- **The bottom row renders nothing and knows no policy.** `pkg/lock` reports a
  held lock as a typed error and lets the CLI style it; `pkg/db/migrations`
  holds its own SQL literals rather than importing settings.
- **One rule, one place.** A derived folder segment is sanitized by
  `path.SanitizeSegment`, called from wherever it is needed rather than copied.

## Entry point & CLI (`internal/cli/`)

`main.go` builds `config.Defaults()` and calls `cli.Execute()`. It deliberately
builds **no logger**: flags, env vars, and the logger are all resolved in one
place, `root.go`'s `PersistentPreRunE`, so `--output-path` and env overrides
take effect before any logging or DB work happens.

One file per command, plus `app.go` (the `app` struct, the output lock, the
database handles, and the dependency `Coordinator`) and `root.go`:

| Command | Purpose |
| --- | --- |
| `config` | Full-screen settings wizard. Downloads the location database in the background. Prints the file instead when `--print` or a non-interactive terminal. |
| `scan` | Run the pipeline synchronously in the foreground. `--paths/-p` is repeatable and comma-friendly. Missing dependencies download in the background while the walk and hash phases run. |
| `review` | Review and confirm the proposed folder tree. `--rebuild` re-proposes from the current settings without a re-scan; `--yes` confirms as-is. |
| `reset` | Wipe all scan data (prompts unless `--yes`). |
| `issue` | Zip up logs (and optionally the DB) for a bug report. |

Config precedence is **flag > env > config file > default**, resolved entirely
inside `config.Resolve`. Env names are the uppercased flag name, so keep new
config-affecting flag names hyphen-free. There is no viper: a command's own
flags (`--yes`, `--plain`, `--rebuild`, …) are read straight off `cmd.Flags()`
in that command's `RunE`.

Only one `scan` or `review` runs against a given output directory at a time,
enforced by an exclusive OS advisory lock (`pkg/lock`).

## Core pipeline (`pkg/core/`)

- **`workflow/`** — the orchestrator. `RunScan` canonicalizes and prunes the
  scan roots, then runs the phase loop synchronously on the calling goroutine,
  so a CLI invocation streams progress and blocks until the scan finishes. Each
  phase waits only on its *own* downloadable dependency (`Deps`): scan and hash
  need nothing, exif blocks on ExifTool, vfs blocks on the location database.
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
  proposal set. `vfs.Propose` is the phase as one call: it builds its own
  `Config` from the user's settings and resolves the saved-place anchors, so
  `scan` and `review --rebuild` share one path instead of two assemblies of the
  same parts. `review.go` in the same package is the reconcile core — it
  exposes the proposal as an editable tree, and `Confirm` writes the approved
  plan back.

## Supporting packages (`pkg/`)

- **`config/`** — `Defaults()`, `Resolve` (the whole precedence chain), and the
  `~/.wandersort/config.yaml` machinery. No CLI framework imported.
- **`db/`** — SQLite (`modernc.org/sqlite`) open/migrate/retry, a batched bulk
  writer, the factory reset, and numbered Go migrations. All timestamps are
  stored as UTC fixed-width nanoseconds and converted to local time only at
  display.
- **`classifier/`** — extension-based media-type detection and a *tolerant*
  ExifTool-JSON decoder (a type mismatch on one tag doesn't fail the decode).
- **`exiftool/`** — runs an already-installed ExifTool binary. It resolves no
  version and downloads nothing.
- **`location/`** — offline reverse-geocode resolver over an already-open,
  already-verified database. Owns place naming end to end: reverse lookup,
  ranked candidates, prefix search, canonical spelling for a saved anchor, and
  the rename picker's ranked suggestions.
- **`install/`** — the one place a downloadable dependency's version, URL,
  on-disk layout, and readiness are known. `Coordinator` owns the install
  order, the shared install lock, download progress, and the getters every
  caller waits on.
- **`volume/`** — best-effort volume-UUID resolution per root (for drive
  re-anchoring) and the output free-space preflight.
- **`tui/`** — the full-screen kit: palette, stage list, app shell, wizard form,
  and shared chrome. Design rules live in `pkg/tui/README.md`.
- **`logger/`** — slog-based logger fanning out to two handlers: a minimal
  coloured **console** handler that shows only lines tagged `logger.UserKey`
  plus warnings, and a full **JSON file** handler (what `issue` ships). The file
  log is always at debug level; there is no flag to bypass the console filter.
- **`lock/`**, **`path/`** — file locking, path canonicalization and segment
  sanitizing. Neither imports anything else in the project.

## Conventions

- Bounded worker pools, never fire-and-forget goroutines.
- Wrap errors with `%w`. Prefer upserts over SELECT-then-INSERT.
- A package that formats for a terminal is a TUI package. Everything else
  returns values and typed errors.
