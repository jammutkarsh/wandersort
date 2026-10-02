# CLAUDE.md

Map of the codebase. Coding rules live in `AGENTS.md` (symlink to `.agents/AGENTS.md`), imported here:

@AGENTS.md

## What WanderSort is

A local media organizer: point it at photo/video folders, it plans a readable folder tree in an output folder (the *library*) and copies files in (sources are never modified or deleted). One SQLite database per library (`.wandersort.db`) is the only durable state; an OS file lock on the library means one process at a time.

Flow: `add` (scan → metadata → vfs) proposes a plan, `organise` edits it, `copy` applies it, `check` verifies it later.

## Commands (`internal/cli`)

One file per command, plus `app.go` (the `app` struct, `openLibrary`, `confirm`), `root.go` (flags, library choice), `exit.go` (exit codes, `--json`) and `shell.go` (the TUI).

| Command | File | What |
|---|---|---|
| `wandersort` | `shell.go` | Full-screen shell: getting ready, then Add / Organise / Copy / Settings tabs, `shift+tab` cycles |
| `add -p …` | `add.go` | Runs the pipeline (shell on the Add tab, or plain with `--plain`/`--json`/no terminal) |
| `organise` | `organise.go` | Shell on the Organise tab; also `rebuildTree`, `newReviewScreen` |
| `copy` | `copy.go` | Applies the draft and copies files in (shell on the Copy tab, or plain; `--dry-run`, `--json`) |
| `check` | `check.go` | Verifies placed files (`--full` re-hashes, `--json`) |
| `admin clear\|db\|report` | `admin_*.go` | Preview cache, `--reset`/`--restore`, issue zip |

- Settings live in the library (`library_settings`), not a global file. The only per-run choice is which library: `--output-path`, else the most recent (`~/.wandersort/libraries`).
- `openLibrary` is the one way to open a library: check folder, take lock, open DB, load settings, remember it. A session that never opens one writes nothing.
- The shell opens on the getting-ready screen (`tui.ReadyModel`) until exiftool and the locationDB are installed: `install.MaxTries` tries, asking for a better network between them; giving up exits 1. Then it opens the last library, if there is one.
- Settings is a shell tab (`settings_form.go`, `settings_examples.go`): a three-step setup (library, layout, home) when no library is open, else a list of rows edited one at a time. Saving a changed setting re-plans at once. Changing the library folder switches libraries (`app.switchLibrary`): an organised folder opens with its own settings, any other starts a new library with the current settings; nothing planned comes along and the old library is left as it is.
- Exit codes (`exit.go`): 0 done, 1 error, 2 bad usage, 3 finished with failed files, 4 library busy. `--json` prints one result object on stdout at the end.
- A copy that leaves files out writes an HTML page (`report.Failures`, `report.SaveHTML`) beside the run's log, same name, `.html` (`logger.File.Page`).
- `state.go` answers "what can the user do next" (`libraryState`).
- Screens never call `tea.Quit`; they hand back with `tui.Leave`. The shell owns the program.

## Pipeline (`pkg/core`)

- `workflow`: `RunScan(ctx, paths, force)` runs scan → metadata → vfs synchronously; refuses roots overlapping the library.
- `scanner`: walks roots into `file_registry`. Each scan has a counter stamp; rows under a clean root with an older stamp are swept (hard delete, dependants cascade). A changed file (size/mtime) is a new row.
- `metadata`: the only pass that reads bytes: BLAKE3 hash then exiftool per file, one `file_metadata` row. Reads are throttled by storage class; volumes drain fastest first.
- `vfs`: plans every unplaced master. `elect.go` picks one copy per hash each run; `plan.go`'s `Plan` derives facts, resolves locations, then `assignTargetPaths` (numbered steps, order is the rule). Plan is persisted as `folder_nodes` (folder tree with ids and `bounds`) + `virtual_fs_entries`. `route.go` sends new files into matching placed folders. `draft.go` is the review edit journal (`.wandersort.draft`); `edit.go` holds merge/drop/flatten.
- `execute` (the `copy` command's engine): `Run` checks space, applies the draft, backs up the DB, transfers each pending row (`land.go`: hash-verified copy that never replaces an existing file), then forgets duplicates of placed files. Sequential and resumable.
- `verify`: `check`'s engine; forgets placed files that are gone, records damage as `VERIFY` errors.
- `library`: whole-library maintenance: `Reset` (backup, wipe, drop the draft) and `Restore` (restore, prove it opens, drop the draft).

## Supporting packages (`pkg`)

| Package | Owns |
|---|---|
| `tui` | Design system and screens (see `pkg/tui/README.md`) |
| `db` | Library `DB` (pragmas in the DSN, `BulkWriter`, `Forget`), `ReadOnly` (location DB, report), `state.go` (file transitions), `errors.go`, backup/restore, `migrations/` |
| `config` | Runtime paths, library `Settings` (one JSON value; `HomeTown`/`WorkTown`), library history, `CheckLibrary` |
| `install` | Versions, downloads, verification and readiness of exiftool and the location DB (`Coordinator`, up to `MaxTries` tries) |
| `location` | Offline reverse/forward geocoding over the geonames DB; name qualifiers and suggestions |
| `exiftool` | Runs the installed exiftool (`-stay_open` pool) |
| `classifier` | Extension → media type, ignored dirs, exiftool JSON → `CommonMetadata` |
| `path` | Segment/file-name sanitizing, `ToLibrary` (NFC) vs `ToSourcePath` (bytes kept), root reduction |
| `atomicfile` | Durable copy, no-replace rename, synced `MkdirAll` |
| `volume` | Volume UUID, storage class, free space, drive name (`Label`) |
| `logger` | slog fan-out: console (only `UserKey` lines + warnings), JSON file log, TUI events |
| `lock` | OS advisory locks (output dir, installs) |
| `report` | Scrubbed export of the `errors` table; the failure page a copy writes |

## Rules that bite

- Hash and EXIF stay one pass (page cache). `readerOnly` in `metadata` and the hidden `WriteTo` in `atomicfile` are load-bearing.
- Source paths are stored byte-exact (`ToSourcePath`); library paths are NFC (`ToLibrary`).
- Every time is wall clock with no zone: EXIF times as written, file and run times via `db.FormatTime` (`db.TimeLayout`).
- Placed files are never re-proposed or moved; folders holding them are never reused by a new plan.
- Year and Month folders are fixed in review (`vfs.ErrFixedFolder`).
- One day lives in exactly one date folder.
- A row means a file that exists. Records end only through `DB.Forget` (sweep, `check`, duplicate cleanup alike: no backup, no limit). `placed` changes only in `pkg/db/state.go`.
- Execute only copies: no code path deletes or modifies a source file.
- Connection pragmas live in the DSN (`appDSN`); `synchronous=FULL`, `mmap_size=0`, foreign keys asserted.
- Every write goes through `db.Writer` (`Write` batched, `WriteSync` when the outcome matters); `DB.SQL` is for reads. `Flush` returns writes lost since the last flush.
- `BulkWriter` ops must touch nothing outside their transaction (failed batches replay).
- No tag yet: edit existing migrations in place; delete `.wandersort.db` after such an edit.
- Imports point down. `pkg/path`, `pkg/logger`, `pkg/lock`, `pkg/atomicfile` import nothing else in the project.
- Run the CLI with `HOME` set to a scratch folder: `--output-path` becomes the default library.

## Build / test

```bash
make build   # bin/wandersort
make test    # fetches test deps, then go test ./...
make lint    # gofumpt
```

## Docs

- `docs/accepted-risks.md`: findings accepted on purpose; don't re-raise them.
- `docs/QA.md`: manual end-to-end checklist.
- `pkg/db/migrations/README.md`: how migrations work.
- Issues and specs: markdown under `.scratch/<feature>/`.
