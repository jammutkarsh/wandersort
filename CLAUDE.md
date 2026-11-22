# CLAUDE.md

Map of the codebase. Coding rules live in `AGENTS.md` (symlink to `.agents/AGENTS.md`), imported here:

@AGENTS.md

## What WanderSort is

A local media organizer: point it at photo/video folders, it plans a readable folder tree in an output folder (the *library*) and copies files in (sources are never modified or deleted). One SQLite database per library (`.wandersort.db`) is the only durable state; an OS file lock on the library means one process at a time.

Flow: `add` (scan → metadata → vfs) proposes a plan, `organise` edits it, `copy` applies it, `check` verifies it later.

## Commands (`internal/cli`)

One file per command, plus `app.go` (the `app` struct embedding `*library.Session`, `confirm`), `root.go` (flags, library choice), `exit.go` (exit codes, `--json`) and `shell.go` (the TUI).

| Command | File | What |
|---|---|---|
| `wandersort` | `shell.go` | Full-screen shell: getting ready, then Add / Organise / Copy / Settings tabs, `shift+tab` or a click cycles |
| `add -p …` | `add.go` | Runs the pipeline (shell on the Add tab, or plain with `--plain`/`--json`/no terminal) |
| `organise` | `organise.go` | Shell on the Organise tab; also `newReviewScreen` |
| `copy` | `copy.go` | Applies the draft and copies files in (shell on the Copy tab, or plain; `--dry-run`, `--json`) |
| `check` | `check.go` | Verifies placed files (`--full` re-hashes, `--json`) |
| `admin clear\|db\|report` | `admin_*.go` | Preview cache, `--reset`/`--restore`, issue zip |

- Settings live in the library (`library_settings`), not a global file. The only per-run choice is which library: `--output-path`, else the most recent (`~/.wandersort/libraries`). A library opened before whose database and backup are gone from its folder (unplugged drive, even with an empty mount point left) is refused (`config.ErrLibraryMissing`), never started afresh.
- `library.Session.Open` is the one way to open a library (`Switch` shares its steps): check folder, take lock, open DB, load settings, remember it. Every session method that needs the library opens it itself: `Scan`, `Copy`, `Check` and `SaveSettings` create one where none is; `Review`, `CopyPlan`, `Discard` and `Reset` only open an existing one (`ErrNotOpen` otherwise); `Restore` closes the library it has open and opens it again after. A session that never opens one writes nothing. `library.New` builds a session around its own copy of the configuration; every method is safe from any goroutine, and its state is read through getters (`OutputDir`, `Settings`, `Configuration`, `DB`, `Log`, `LogFile`). The shell runs on its own session, whose log also reaches the screen. `Session.Shutdown` stops the session's running work (`Scan`, `Copy`, `SaveSettings`, `Review`, `CopyPlan` contexts are bound to it), waits for it and closes the library; front ends quit through it, and a session shut down opens nothing again (`ErrClosing`). The CLI's `app` shadows `Open`, `SaveSettings` and `Restore` to explain a busy library in the terminal; `Switch` (Settings tab only) is left to `explain.Error`.
- The shell opens on the getting-ready screen (`tui.ReadyModel`) until exiftool and the locationDB are installed: `install.MaxTries` tries, asking for a better network between them; giving up exits 1. Then it opens the last library, if there is one.
- The shell opens on the Add tab. A folder dropped on the terminal arrives as a paste; `path.Resolver.Dropped` unquotes and unescapes it. Path completion (tab, dropdown) stays beside drop on Add and in the library field. `ctrl+o` on Add opens the system folder window (`tui.PickFolder`, passed in on `HomeConfig`; absent when the system has none). `esc esc` quits when no scan or copy runs, else warns in the tab bar; `esc` on a running scan or copy stops it. A scan with nothing to copy skips the folders-ready screen and returns to Add (`vfs.ErrNothingNew`).
- Organise edits go through `Session.Review` (`library.Review`: tree, draft, labels, library folders, resolver with saved places): one review per plan, shared by every caller, its resolver waited for before the library is held. `Review.Peek` copies a folder's files for a viewer, read from that review's library. It and its `Apply`/`Undo`/`Reset` are refused beside a job or re-plan (`*BusyError`, `ErrReplanning`), while a discard, reset or switch holds the library (`ErrBusy`), and once the plan it was built over moved on or the library closed (`ErrStaleReview`): a copy applies the draft and removes it, so an edit made meanwhile would be lost. The shell also keeps the Organise tab shut then.
- Library reset is CLI only (`admin db --reset`); no key binding.
- Settings is a shell tab (`settings_form.go`, `settings_examples.go`): one list of rows (library folder, folder layout, home, extra options) edited one at a time; with no library open it shows the defaults and the first save creates the library. Work is off: it saves as home (`config.Settings.SetHome`). The rules behind the form live in core: ready-made layouts and level order (`vfs.Layouts`, `vfs.RuleOrder`, `vfs.CustomLayout`), a town's saved spelling (`location.Resolver.CheckTown`, `TownName`); the form keeps only wording. Saving goes through `Session.SaveSettings`, one blocking call any goroutine may make: a change rebuilds the plan and writes it with the settings in one transaction (`vfs.Propose`), so the library never holds settings its plan was not built from, and a failed or cancelled save changes nothing. The form hands its answer over (`app.settingsToSave`) and the shell runs the save off the UI goroutine. The save registers a `WorkGroup.StartRewrite`: it runs alone on the library, and from the hand-over until the last one lands (`shellModel.replanning`) Organise, Copy, Settings and a new scan are refused, and the session itself refuses settings saves and switches (`library.ErrReplanning`). Quitting cancels it. Every change to the plan (a re-plan or job registered, a switch, discard or restore) moves `WorkGroup.Plan` on; a review built when it read less is dropped however late it lands. Changing the library folder switches libraries (`Session.Switch`): a library opens with its own settings, a new one (no settings saved, nothing recorded) takes the current settings; nothing planned comes along and the old library is left as it is. The new library is ready before the old one closes, so a failed switch changes nothing.
- Exit codes (`exit.go`): 0 done, 1 error, 2 bad usage, 3 finished with failed files, 4 library busy. `--json` prints one result object on stdout at the end.
- A copy that leaves files out writes an HTML page (`report.Failures`, `report.SaveHTML`) beside the run's log, same name, `.html` (`logger.File.Page`); `FailureReport.ByReason` groups them for a screen. `Session.CopyPlan` is what a copy would do now (files, bytes, edits, room), read while the library can't be switched.
- The shell draws the top line and owns the gutters: screens get `tui.Inner(w)` and draw from column 0; `tui.Frame` adds the margin. Mouse: a click on a tab opens it, the wheel is ↑/↓, and a click on any `tui.Clickable` span (every `KeyHint`, numbered choice, settings row, Organise row, confirm button, the `?` list) presses its keys (`tui.ClickTargets` reads them back from the drawn view). Errors shown to the user go through `explain.Error`.
- `Session.State` answers "what can the user do next" (`library.State`): counts, `Running`, `Replanning`, and `CanOrganise`/`CanCopy`/`CanScan`/`CanChangeSettings`. `Session.Live` refreshes the running work on an earlier `State` without the database; the shell's tab rules read it. The shell still flags a settings save from its hand-over (`app.settingsToSave`), before the save goroutine registers it.
- Work on a library goes through the session, so every front end gets the same rules: `Scan` and `Copy` run as `WorkGroup` jobs, one at a time and never beside a re-plan (`library.ErrBusy` as `*BusyError` naming the job, `ErrReplanning`, `ErrClosing`), and read the database, folder and settings only once they hold the library. A switch, discard, reset or restore (`WorkGroup.HoldLibrary`) is refused beside a job or re-plan (`*BusyError` naming the job, `ErrReplanning`) but waits out short reads (a review being built, `CopyPlan`), and no job or re-plan starts while it waits. A short read (`Review`, `CopyPlan`, `Review.Peek`) is refused during a re-plan (`ErrReplanning`) rather than waiting it out. The shell's tab rules only hide what the session would refuse.
- Screens never call `tea.Quit`; they hand back with `tui.Leave`. The shell owns the program.
- `internal/review` is the Organise screen (`review.Screen`): keys, edits through a `library.Review`, rename completion, previews, rendering. Merge order (the selection's first folder names the result) is `vfs.MergeEdit`.

## Pipeline (`pkg/core`)

- `workflow`: `RunScan(ctx, paths, force)` runs scan → metadata → vfs synchronously; refuses roots overlapping the library. `Deps.OnProgress` hears typed progress (`Progress{Phase, Done, Total}`) for the walk, the read and the plan step's start; the terminal UI reads the same from log events.
- `scanner`: walks roots into `file_registry`. Each scan has a counter stamp; rows under a clean root with an older stamp are swept (hard delete, dependants cascade). A file is its volume plus path (two cards mounted in turn at one path are different files); a changed file (size/mtime) is a new row.
- `metadata`: the only pass that reads bytes: BLAKE3 hash then exiftool per file, one `file_metadata` row. Reads are throttled by storage class; volumes drain fastest first.
- `vfs`: plans every unplaced master. `elect.go` picks one copy per hash each run; `plan.go`'s `Plan` derives facts, resolves locations, then `assignTargetPaths` (numbered steps, order is the rule). Plan is persisted as `folder_nodes` (folder tree with ids and `bounds`) + `virtual_fs_entries`. `route.go` sends new files into matching placed folders. `draft.go` is the review edit journal (`.wandersort.draft`); each edit carries the fingerprint of the plan it was made on and replays only onto that plan; `edit.go` holds merge/drop/flatten.
- `execute` (the `copy` command's engine): `Run` checks space, applies the draft, backs up the DB, transfers each pending row (`land.go`: hash-verified copy that never replaces an existing file), then forgets duplicates of placed files. Sequential and resumable.
- `verify`: `check`'s engine; forgets placed files that are gone, records damage as `VERIFY` errors.
- `library`: one process's hold on a library (`Session`: open, close, switch, save settings, state, `Review`, `Scan`, `Copy`, `Check` (jobs), `Discard`, `Reset`, `Restore` under the lock, `WorkGroup` for work running on it) and whole-library maintenance: `reset` (backup, wipe, drop the draft) and `restore` (restore, prove it opens, drop the draft). `execute.Run` and `workflow.RunScan` are the engines `Session.Copy` and `Session.Scan` run as jobs; front ends call the session.

## Supporting packages (`pkg`)

| Package | Owns |
|---|---|
| `tui` | Design system and screens (see `pkg/tui/README.md`) |
| `explain` | Errors as sentences a person can act on, for every front end |
| `preview` | Cache of a review's peek copies, one root per library (`Copy`, `Root`, `DirFor`, `Clean`, `CleanAll`); a review peeks through `library.Review.Peek` |
| `db` | Library `DB` (pragmas in the DSN, `BulkWriter`, `Forget`), `ReadOnly` (location DB, report), `state.go` (file transitions), `errors.go`, backup/restore, `migrations/` |
| `config` | Runtime paths (`New` uses `~/.wandersort`, `NewAt` any app folder), library `Settings` (one JSON value; `HomeTown`/`WorkTown`), library history, `CheckLibrary` |
| `install` | Versions, downloads, verification and readiness of exiftool and the location DB (`Coordinator`, up to `MaxTries` tries) |
| `location` | Offline reverse/forward geocoding over the geonames DB; name qualifiers and suggestions |
| `exiftool` | Runs the installed exiftool (`-stay_open` pool); each process is leashed, so it ends even when this process is killed |
| `classifier` | Extension → media type, ignored dirs, exiftool JSON → `CommonMetadata` |
| `path` | Segment/file-name sanitizing, `ToLibrary` (NFC) vs `ToSourcePath` (bytes kept), root reduction |
| `atomicfile` | Durable copy, no-replace rename, synced `MkdirAll` |
| `volume` | Volume UUID, storage class, free space, drive name (`Label`) |
| `human` | Counts in words: thousands separators, "1 file" vs "3 files" |
| `logger` | slog fan-out: console (only `UserKey` lines + warnings), JSON file log, TUI events |
| `lock` | OS advisory locks (output dir, installs) |
| `report` | Scrubbed export of the `errors` table; the failure page a copy writes; the issue zip (`Issue`) |

## Rules that bite

- Hash and EXIF stay one pass (page cache). The hidden `WriteTo` in `atomicfile.ContextReader` (the hash and the copy both read through it) is load-bearing.
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
- Imports point down. `pkg/path`, `pkg/logger`, `pkg/lock`, `pkg/atomicfile`, `pkg/human` import nothing else in the project.
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
