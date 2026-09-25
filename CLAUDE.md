> SPDX-License-Identifier: AGPL-3.0-or-later
>
> Copyright (c) 2026 Utkarsh Chourasia

# CLAUDE.md

Guidance for AI agents working in this repo. Coding rules live in `AGENTS.md`
(symlink to `.agents/AGENTS.md`) — read it first. This file is the **map**: what
lives where and how the pieces fit.

## What WanderSort is

A black-box media organizer. Feed it unorganized photos/videos; it produces an
ordered folder hierarchy. Pipeline runs in ordered phases per `wandersort add`
invocation — there is no persisted session/run record and no in-memory run
identity either; the output DB (one per output path) is the durable state,
and `lock.AcquireOutput`'s exclusive PID lock is what already guarantees only
one scan ever runs against it at a time (see "Conventions" below):

1. **Scan** — walk roots, build a file index.
2. **Metadata** — one read pass per file: content-hash for duplicate detection
   and EXIF extraction, back to back so exiftool's read hits the page cache the
   hash just warmed.
3. **Score** — within a duplicate group, elect one master copy (same bytes,
   different storage context — e.g. folder named `Goa Trip 2024`).
4. **VFS** — propose a destination hierarchy for every live master in the
   library (not just this run's), using the user's prior folder-naming as
   context when EXIF is absent. Nothing on disk is touched.

## Entry point & CLI

- `main.go` — calls `cli.Execute()` and prints its error. **No logger here**
  — it's built later (see below).
- `internal/cli/` — cobra CLI. One file per command, plus `app.go` and `root.go`.
  **The command set is `add`, `organise`, `execute`, `check` and `admin
  clear|db|report`**. The old names are gone: `review` is `organise`
  (`organise.go`; it opens the shell's Organise tab, still `tabReview` and the
  `internal/review` package inside), `scan` is `add` (`add.go`), `verify` is `check`
  (`check.go`), `issue` is `admin report` (`admin_report.go`), `reset` is
  `admin clear` (`admin_clear.go`, the peek copies) and `admin db --reset`,
  `recover` is `admin db --restore` (`admin_db.go`), and there is no `config`
  command — the settings wizard is only a shell tab (`settings_form.go`,
  `settings_examples.go`). Notes below that still say `wandersort scan` or
  `config` describe the same code under its old name.
  - `app.go` — `Execute(cfg)`, **the package's only exported symbol**, plus the
    unexported `app` struct and everything hanging off it: `openLibrary`,
    `closeDBs`, `lockOutput`, `isTuiEnabled`, and `newDeps` — the one-line
    constructor for a `pkg/install.Coordinator` (see below), stored on
    `app.Deps`. Exiftool path / location resolver readiness used to be four
    raw fields (`ExiftoolPath`, `LocationResolver`, `LocationDB`,
    `InstallProgress`) a background goroutine wrote and a pipeline goroutine
    read, with the happens-before edge documented in a comment rather than
    enforced by a type; `app` now holds exactly one field for all of it
    (`Deps *install.Coordinator`), and every read goes through a getter that
    blocks until the value is actually there. Nothing outside this package
    needs the app or its state, so nothing else is exported — `main.go` has
    one way in and no struct to assemble. `tuiEnabled` decides TUI vs plain
    line logging (plain when `--plain` is set or stderr isn't a terminal);
    the TUI draws to stderr so stdout stays clean for piping.
    `openLibrary` is **the one way any command or the shell opens the output
    folder**, lazily and once per session: `config.CheckLibrary` (empty, or
    already holds `.wandersort.db`), then the output lock, then `db.New`,
    then **`config.LoadSettings` — this library's own settings** (spec D2) —
    and finally `config.Configuration.Remember`, which puts the folder at the
    front of the recently-used list (D3). `closeDBs` releases lock and
    database. The order is the point: a refused folder gets
    nothing written into it, a process that loses the lock race creates no
    file either (the lock file it opened is the winner's), and only a folder
    that really opened as a library is remembered as one. A session that
    never scans or reviews writes nothing outside `~/.wandersort` — no lock
    file at launch, no cleanup pass at exit.
    **The settings a run plans with are the open library's, not a global
    file's**: `a.Config.Settings` is whatever `openLibrary` read, so scanning
    into an existing library organizes it the way it was already organized
    however another library is set. `saveSettings` is the other direction —
    the wizard's save — and **it is what opens the library on a config-first
    session**: it takes the output folder the form collected, runs
    `openLibrary` on it, writes the row, and updates `a.Config`. Quitting
    before that save writes nothing anywhere. The settings tab is unreachable
    while a scan runs (`nextTab`, spec D5), so a running workflow never sees
    its settings move; the output folder never moves at all once the library
    is open, which is why the wizard stops asking for it (see `config.go`).
  - `root.go` — root cmd and flag-name constants. **`PersistentPreRunE`
    decides one thing only: which library this invocation is about.**
    `config.New()` builds the runtime paths and starts from the settings
    defaults in code; the output folder is the most recently used one that
    still exists (the history in `~/.wandersort/libraries`), and
    `--output-path` overrides it. **The settings themselves are not resolved
    here and there is no layering left** — no `config.yaml`, no
    `OUTPUT_PATH`/`COLLAPSE_LEVELS` env vars, no `--collapse-levels` /
    `--saved-places-date-only` / `--merge-same-location-days` flags: rules,
    toggles and saved places live in the library's database and are read by
    `openLibrary` (spec D2). A setting that can move a file is that library's,
    so a flag that silently planned one library by another's rules had no
    honest meaning. The logger is built right after, so the startup line can
    name the output folder.
    Don't rebuild the logger in `main.go`. There is no global registry here
    (no viper): every other command's own flags — `--yes`,
    `--plain`, `--vertical`, `--print`, `--paths` — are read
    straight off `cmd.Flags()` inside that command's own `RunE`, since `cmd`
    is already in scope there. `tuiEnabled` takes the invoked `cmd` for the
    same reason. **There is no `--debug`
    flag**: the JSON file log is always at debug level (`fileHandler`), the
    console only ever shows `UserKey` lines and warnings, and a verbose console
    made no sense once the TUI owned the screen. `--plain` is the escape hatch.
    The root cmd's own `RunE` is `shell.go`'s `runRoot`: bare
    `wandersort` opens the unified app, and `--plain` / a piped stderr still
    prints help.
  - `shell.go` — the **unified TUI shell**, and **the only full-screen entry
    point there is**: a tab bar plus one live screen per tab (`shellModel`), so
    scan, the settings wizard and review are one session instead of three
    programs. `runShell(shellStart)` takes which tab to open on, and *every*
    interactive command goes through it — bare `wandersort` (the folder input),
    `scan -p …` (`shellStart.paths`, so the run starts without asking),
    `config`, and `review`. **A
    subcommand is a starting point, not a smaller app**: each one used to build
    its own `tea.Program` around a single screen, so `wandersort scan` could
    not reach the settings and `wandersort config` could not start a scan —
    `ctrl+t` existed only where this file drew the tab bar, and naming a
    subcommand was enough to lose it (a reported bug). `Init` asks for the
    opening tab by *message* (`tui.StartScanMsg` / `openConfigMsg` /
    `tui.OpenReviewMsg`) rather than placing the screen itself, because
    bubbletea calls `Init` on a copy and any container mutation there is
    discarded. Keeping all three screens alive at once is the whole
    point — the scan model has to go on receiving its log events while a form
    is on top of it. Routing: `ctrl+t` cycles (skipping review
    only when `canReview` says there is nothing there); every other key goes to
    the **active** tab only;
    `WindowSizeMsg` goes to all screens at `Height-1` (the container owns the
    tab-bar line); **everything else is broadcast to every live screen**, which
    is how `LogEventMsg`/`InstallProgressMsg` reach a backgrounded scan without
    this file naming the scan screen's unexported messages. Broadcast is safe
    for the animation ticks because every bubbles spinner/progress/textinput
    tick carries its own model ID and a foreign one is dropped, not answered.
    `tui.SwitchMsg` is intercepted rather than forwarded: a non-nil `Next` is
    the scan's prefetched review, opened straight away only if the user is
    watching the scan (never yanked out of a half-answered form — the tab bar
    says `Review ✓ ready` instead); a **nil `Next` does not quit** — the review
    handing back means one plan is settled,
    not that the session is over, so the scan tab goes back to a fresh
    `tui.HomeModel` carrying the finished scan's stage summaries
    (`ScanModel.Summary`) and the review's outcome line. **`scanTabHome` does
    the same on `ctrl+t` back into a finished scan's tab** — the run is over,
    so returning there means "scan something else"; without it the tab showed
    that one finished run forever and a second folder meant quitting the app,
    which was a reported bug and the opposite of the point. A *failed* run
    keeps its screen (`ScanModel.Failed`): that screen is the only place the
    reason is written. That is the small-library-first flow: organize one
    folder, then add more without leaving the app. A second scan reuses the already-open DB/lock and the
    already-started `a.Deps` (the Coordinator closes its readiness channels, so
    `Start` can only ever be called once — `runShell` does it eagerly, once).
    **`reviewReady` (a screen is stashed) is not the same question as
    `canReview` (the tab can be entered at all)** — conflating them was a
    reported bug twice over: a relaunch over an earlier run's proposal, and the
    session right after a save (which drops the stashed screen), both said
    `Review — waiting for scan` forever, because only the *scan's* prefetch
    ever set the flag. `canReview` is `reviewReady || a.hasProposal()` (the
    database file on disk, since nothing else writes one) **and not while a
    scan is running** — that run replaces the proposal wholesale, so the tree
    on disk is about to be stale. `ctrl+t` into a reviewable-but-unprefetched
    tab runs `openReview` (the same `openLibrary` + `newReviewScreen` cmd
    `ctrl+t` and `wandersort review` use) and **leaves the tab where it is until
    the screen lands**, so there is never a blank frame. The tab bar's
    `✓ ready` follows `canReview`, not `reviewReady`, for the same reason: a
    plan left on disk is as ready as one this session prefetched, and a plain
    dim tab says nothing about it being there at all.
    **`ctrl+c` anywhere quits the app** — being dropped back on
    the folder input instead was a reported bug. While a scan is running it
    goes to the scan screen so the cancel guard gets a say. Otherwise it is
    still *forwarded* to the active screen first, so the screen gets its say
    (the wizard's own guard); `quitReq` is what turns the screen's
    answer (`Done`, or the `SwitchMsg{nil}` a review hands back with) into a
    quit rather than a walk home, and any other keystroke clears it — the user
    stayed, so a later save must go home as usual.
    **A wizard save is picked up without a relaunch** (`configSaved`, run when
    an embedded `FormModel` reports `Done()` without an abort or an error):
    **a changed setting re-plans the library at once, no question asked**
    (spec D20), by dropping any stashed review screen — its folder IDs are
    about to stop existing — and running `a.rebuildTree` off the UI goroutine
    (`replan` / `replanDoneMsg`). **A failed re-plan is logged as a `UserKey`
    warning, not just drawn**: `execute` used to refuse a plan built under
    older settings, and without that check a silently failed re-plan would
    leave it copying files under settings the user has already changed —
    while the on-screen half is one home-screen line that isn't drawn at all
    if a scan screen is up. Calling `rebuildTree` straight from the save
    used to be a nil-pointer crash, because the library could still be
    unopened; it can't now — **the save itself opens it** (`app.saveSettings`
    writes the settings into the library, so there is one by the time this
    runs). The user stays on the scan tab: the next visit to the review tab
    builds a screen over the new plan.
    `settingsChanged` and the `.wandersort.cfg` stamp are gone with the
    global config file — the only way settings can move under a plan now is
    this save, so the gate is a plain comparison of the settings the wizard
    opened on (`shellModel.settingsBefore`, recorded by `openConfig`) against
    the ones it saved (`config.Settings.Equal`): a visit that changes nothing
    throws no plan away. The settings tab is unreachable while a scan
    runs (`nextTab` skips it), so there is never a running workflow to
    retarget — that mid-scan case, and the prompt asking whether to apply a
    change, both used to exist and both are gone: re-planning stored metadata
    is cheap, so a save just takes effect.
    `Settings saved in <path>` goes to the home screen's error line: the
    wizard closes back into the shell instead of ending a process, so a
    printed receipt has nowhere to land and that line is the only
    confirmation the save gets.
  - `settings_form.go` / `settings_examples.go` — **the settings wizard**, a
    shell tab (the `config` command that used to host it is gone; there is
    no `setup` command either — dependency downloads belong to `add`). `buildConfigForm` +
    `tui.FormModel`: a top-down stacked form (answered fields collapse to
    summary rows, StageList-style) written by `app.saveSettings` into the
    library's own `library_settings` row — one whole row, since the wizard
    always submits every setting. **The form is seeded from the library it is
    about to overwrite**: `newConfigScreen` opens an existing library before
    building the fields, so a visit to the settings can never replace one
    library's rules with another's defaults. **The output-path field is only
    there while the answer can still change anything** — with a library open
    its folder is fixed (spec D5), so the field is left out rather than shown
    and ignored; with none open, the save is what creates the library, and
    quitting first writes nothing anywhere. Its suggestions lead with the
    recently-used libraries (`Configuration.History`).
    The command itself is four lines —
    `--print`/non-TTY prints the settings (`printSettings`: the library's if
    there is one, else the defaults a first save would start from — it does
    not create a library to answer), everything else is
    `runShell(shellStart{tab: tabConfig})`; the wizard is a shell tab
    (`newConfigScreen`), so answering the settings and then scanning with them
    is one session. The **download progress row** works there because
    `runShell` reports `install.PhaseLocation` bytes as `tui.DownloadMsg` as
    well as `InstallProgressMsg` (the form knows nothing about install phases,
    the scan screen knows nothing about `DownloadMsg`), and settles it with a
    `Finished` off the blocking `Deps.Location()` getter. Step order is
    **output path, rules,
    then one Saved places step** whose
    sub-fields are home town, work
    town, "group saved-place photos by date only?" and "merge consecutive
    same-location days?" — both folder questions live *after* the towns
    because their examples name the town the user just typed.
    **Every example is computed, not canned**: `examplePath(day, town,
    collapsed)` builds the path the answers *so far* would produce (it walks
    `rulesField.Selected`), so turning off the `location` rule removes the city
    folder from every later example. **Examples render as the review screen's
    guided tree, not as slash paths** (`treeExample` in `config.go` — shared
    prefixes fold into one branch point), because the settings shape a tree
    and the example should look like the thing being shaped: the merge-days
    "no" answer shows three sibling day branches under one month, which no
    flat path list conveys. On a wide terminal (≥100 cols) the example sits in
    a bordered box in the right-hand column, next to the question, instead of
    above the footer (`tui.FormModel.examplePanel`/`sidePanel`/`bodyW`); narrow
    terminals keep the footer block. Examples render in their own block above
    the footer (`tui.Field.Example` → `FormModel.exampleBlock`, the same place
    the scan screen pins warnings) and only for the option under the cursor —
    a description that listed the yes *and* no outcomes at once made the reader
    work out which one was live. Descriptions explain, examples demonstrate.
    Output path expands a leading `~` (`expandHome`, applied at save *and*
    when matching suggestions) and only suggests locations whose parent dir
    exists on this machine, which is what makes the list platform-correct.
    Town inputs validate through `location.Resolver.Canonical` (geonames
    exact-match) — the *write* side of `ResolveByName`, and in `pkg/location`
    for that reason: one package decides both the spelling an anchor is saved
    as and how that spelling is found again, so a saved town always resolves.
    The wizard keeps only the "did you mean" message.
    **The location DB downloads in the background, with no install screen**:
    `runShell` starts the `pkg/install.Coordinator` for the whole session and
    feeds its byte progress into the form's own row above the footer
    (`tui.DownloadMsg`, in the same block the examples pin to), settling it
    with a `Finished` message. The wizard's `geonames`
    closure is `a.Deps.LocationNow` — the **non-blocking** getter, which reports
    `install.ErrPending` while the download runs and the resolver (or a
    permanent failure) once it doesn't. Used by
    `townValidator`/`canonicalTownOrTyped`/`suggestTown`, all of which take the
    resolver from `geonames()`'s return value rather than a package-level
    field. Once the download finishes the row **persists as a dim `✓ … done`
    line** rather than vanishing (a bar that disappears the moment it fills
    reads as a failure); the `Finished` message carries no label, so
    `FormModel.Update` keeps the one from the byte reports — and ignores a
    `Finished` with no prior bytes, which is the only message an
    already-on-disk database sends, so it never gets mentioned at all.
    The Saved places step is the only one that needs the database, so it holds
    on `tui.Field.Await` (showing why) until `geonames()` stops returning
    `install.ErrPending`; everything above it is answerable meanwhile.
    `Coordinator`'s internal channel is the happens-before edge making the
    resolver safe to read, enforced by the type rather than documented in a
    comment (see `pkg/install` below). A geonames database that never opens at all
    (a failed download — `location.db` opens `mode=ro`, so concurrent
    wandersort processes never contend for it) is **not** treated as "pending":
    `Await` releases and the town is waved through unvalidated and saved as
    typed, in both `townValidator` and `canonicalTownOrTyped`. Blocking there
    would trap the user on a pre-filled field they could only escape by
    clearing it, and would silently drop the towns they already had. `a.Log`
    is swapped to a TUI logger for the whole session so the download's log
    lines can't draw over the alt-screen. Prints the raw file to stdout
    instead of running the wizard when `--print`/`-p` is given or
    stdout/stderr isn't a terminal (`wandersort config | grep …`, `> file`) —
    launching a full-screen wizard into a pipe is never what the caller meant.
  - `add.go` — `add` cmd (was `scan`; the pipeline). Two functions and a helper: the
    interactive path is `runShell(shellStart{tab: tabScan, paths: paths})` —
    the same session a bare `wandersort` opens, just landing on the scan tab —
    and `runAddPlain` is everything else. **`--paths` is not
    `MarkFlagRequired`**: without it the scan tab opens on its own folder
    input, which is the answer, and refusing to open the app over a question it
    is about to ask made `scan` the one command that couldn't just be run. The
    plain path is the one place it really is required, and says so there —
    there is no screen to ask on. Runs
    **synchronously** in the
    foreground (`Workflow.RunScan`, which canonicalizes and prunes the roots
    itself and returns the ones actually walked) so the user watches
    progress and the exit code reflects pipeline success; logs total elapsed
    time on completion. **There is no install screen: the scan starts
    immediately and missing dependencies download in the background**, with
    each pipeline phase waiting only on its own dependency (`workflow.Deps`) —
    scan needs nothing, metadata blocks on exiftool, vfs blocks on the
    location DB. `runShell` builds a `pkg/install.Coordinator` (`a.newDeps`,
    stored on `a.Deps`) and calls `Start` once for the session;
    `newScanScreen` wires `workflow.Deps.Exiftool`/
    `Location` to `a.Deps.Exiftool`/`Location` — each logs a
    "Waiting for …" `UserKey` line only when that phase actually stalls behind
    its own still-running download, and returns immediately once ready with no
    hand-rolled channels or `await` closure in `scan.go` itself. They stay
    closures rather than method values because the TUI path builds the
    workflow before the Coordinator exists. Download byte
    progress renders as rows under the banner (`tui.InstallProgressMsg` →
    `ScanModel.viewDownloads`, persisting as `✓ done` like the config
    wizard's bar). In practice the location DB is already on disk — the
    (mandatory) `wandersort config` downloads it — so a first scan usually
    only fetches exiftool; the vfs gate is the fallback for a config run whose
    download failed. A download that fails mid-pipeline fails the run at the
    phase that needed it; files stay `HASHED` and the next run resumes.
    Anchors are resolved inside `vfs.Propose`, which the vfs phase calls once
    the resolver exists — vfs is the only phase that reads them, so nothing
    above it has to carry them. **`scan`/`review`/the shell are the only things
    that install dependencies** (`pkg/install.Coordinator.Start`/`StartLocationOnly`,
    which install exiftool *then* the location DB when both are asked for —
    the small download unblocks the earlier phase; the big one has the whole
    pipeline to hide behind — and each command asks only for what it needs:
    a settings-triggered re-plan (`rebuildTree`) never runs the metadata
    phase, so it starts the location database alone).
    **`scan` never requires `wandersort config` to have run first** — an
    unconfigured first scan just builds its folder proposal (output path,
    rules, saved-place anchors) from defaults. Running `wandersort config`
    later re-proposes the hierarchy from the new settings right away, no
    re-scan needed (`configSaved`). `--paths/-p` is repeatable + comma-friendly
    (`StringSlice`); the library's own `rules` setting (see below) controls
    the VFS folder depth for this scan's proposal — no CLI flag, no env var,
    set it via `wandersort config`. The plain path (`--plain`/non-TTY) keeps the simple
    order: blocking `Deps.Start` + `Deps.Exiftool`/`Deps.Location`, then the
    pipeline with the same `workflow.Deps`.
  - **There is no `anchor.go`, and no anchor row in the database.** Anchors are
    built in memory, per run, by `location.Resolver.BuildAnchors`, from the
    global config's `SavedPlaces` — positional: index 0 home, 1 work,
    everything after another frequently-stayed-at place, all anchored the same
    way. Each name resolves via `ResolveByName` (a guaranteed exact hit, since
    the wizard's `Canonical` validator only saves geonames spellings).
    `BuildAnchors` **returns** the set and also keeps it on the resolver, which
    needs it for `cityClaimed`; `vfs.Propose` takes the returned value.
    Callers never read the field back — an anchor is a value passed on, not
    state two packages share, which is what the old
    `BuildAnchors`-then-copy-`resolver.Anchors` ritual (duplicated in
    `workflow` and `cli/review`) made it. `user_labels`' `SAVED_PLACE` kind is
    legacy: nothing writes it, the CHECK constraint just still allows it.
  - `organise.go` — `organise` cmd (was `review`): interactive is
    `runShell(shellStart{tab: tabReview})`, so a reviewer who finds the
    folders wrong can fix the settings and come back without relaunching; a
    missing library says so on the home screen rather than refusing to start
    (that user has a scan tab one `ctrl+t` away). **A non-TTY is an error**
    pointing at `wandersort execute`, rather than drawing an alt-screen into a
    pipe. **There is no `--yes`, `--copy` or `--move` any more** (spec D18,
    issue 15): review never writes the plan, so there is nothing to confirm
    non-interactively — `execute` alone applies, approves and transfers.
    There is no session
    lookup before
    `BuildTree` — `virtual_fs_entries` always holds exactly one proposal
    batch (the VFS phase replaces every unapproved row every run), so an
    empty tree from `BuildTree` alone means "already organized", not
    "nothing proposed".
    **There is no `--rebuild` flag, no manual rebuild, and no stale-settings
    check either**: settings live in the library's own database and only the
    wizard can change them, and that save re-plans on the spot
    (`shell.configSaved`), so a tree `newReviewScreen` finds always matches
    the settings that built it. `rebuildTree` just calls `Propose`: **there
    is no `ReopenPlan` and no plan status to reopen** (issue 20) — every row
    whose file is not yet placed and has no `TRANSFER` error is pending, and
    `persist` replaces exactly those, so the whole library is re-proposed
    under the settings as they now stand while a file already placed or
    failed keeps its row (a re-plan moving a *transferred* file was a bug
    once). It also
    holds `newReviewScreen`, which builds the embedded screen `scan` swaps
    into, and is also where a shell-side settings save re-plans from
    (`shell.configSaved`, see above, which routes through `newReviewScreen`)
    — one function, rather than each caller re-deriving "reopen then
    propose". The re-plan also throws the review draft away (`Propose`
    removes it, see `pkg/core/vfs` below): its folder IDs no longer exist.
    `newReviewScreen` opens the draft over the tree (`vfs.OpenDraft`) and
    hands it to the screen as `Options.Draft`.
    **The TUI itself lives in `internal/review/`** — see below; it has no
    rebuild concept of its own any more, only `[R]` as a reset of the draft
    (see the `Model` notes below).
  - `execute.go` — `execute` cmd: asks the `--move` question (`confirm`;
    `--copy`, the default, never does — it never touches a source), then
    calls `execute.Run` and prints the report. **The spec-D18 order lives in
    `execute.Run`, not here** (see `pkg/core/execute` below), so issue 21's
    copy screen calls the same one function and can't get it wrong; the
    command only hands it `OnApplied`, which sweeps the peek copies
    (`review.CleanPreviews` is in `internal/`, which `pkg` can't import).
    Safe to re-run: `execute.Run` only ever selects pending rows (file not
    placed, no `TRANSFER` error), so a placed file is skipped and a run stopped partway resumes on
    its own next time; a crash between the apply and the draft's removal
    replays as a no-op.

- `internal/review/` — the bubbletea **full-tree view** TUI over the VFS
  proposal (issue #8), extracted from `internal/cli` because it was 60% of that
  package's lines while cobra wiring is the rest. Its whole exported surface is
  `Screen(ctx, Options) tea.Model` (plus `CleanPreviews`): the review as an
  app-shell screen, and **the only interactive entry point**. It writes
  nothing but the draft file; `[esc]`/`ctrl+c` hand back to the shell with
  `tui.Switch(nil)`, and the shell's home line says the edits are kept for
  `wandersort execute`. There is no `ConfirmAll`, no `Outcome`/`Result`, no
  `screen.go` finalize wrapper any more — they existed only for a save step
  and in-review transfers, both gone (issue 15).

  There is **no standalone `Run` and no loading screen** any more: every
  full-screen command is the same shell opened on a different tab, so a review
  is always hosted, and the shell's own `openReview` already does the slow work
  (lock, DB, an out-of-date proposal's own re-plan, `BuildTree`) off the UI
  goroutine with the tab bar saying `opening…`. `Options.Load` went with them.

  `Options` carries `DB`/`Tree`/`Edits`/`Resolver`/`Log`/`OutputDir`; a nil
  `Resolver` just disables rename autocomplete. `Tree` is the plan as the
  database holds it, `Edits` the draft to replay onto it, and `OutputDir`
  where the draft lives. There is no `Rebuild` or
  `SettingsChanged` field any more — `cli/review.go` re-plans before the
  screen is ever built (see above), so the screen never needs to ask.

  **Review is one tree over the whole library — there is no time-slice
  picker.** `wandersort review` on a multi-year library opens straight on the
  full tree; `BuildTree`/`Confirm`/`ReopenPlan` all take no scoping
  argument any more (spec D18, issue 19). A picker existed so a big library
  could be reviewed and saved a year at a time — the edit file (issue 15)
  keeps progress across sessions instead, the tree's own top level is already
  the years, and a reviewer plans once and transfers together, so slicing was
  a second screen for the same thing.
  **Edits go to a journal, not the database** (spec D17, `vfs.DraftFileName`
  = `.wandersort.draft` next to the database, `pkg/core/vfs/draft.go`). Every
  landed edit appends one JSON line (`vfs.Edit`: `rename` with node/from/to;
  `merge` with every node, anchor first; `drop`/`flatten` with every node of
  the `[V]` range, since the range is one edit and one `[u]`) and syncs it;
  a failed write undoes the edit too. **`vfs.Draft` is the whole of it** —
  `OpenDraft(dir, base)`, then `Apply(Edit)`, `Undo`, `Reset`, `Tree`,
  `Edits`. It keeps `base` (the tree as loaded, never edited) and the edits;
  its tree is always base with the edits replayed. **One dispatch
  (`applyEdit`) runs an edit whether it is made live or replayed from the
  file**, so the rules — fixed folders included — are stated once: there
  used to be a second switch in the replay and a second fixed-folder check
  in the TUI's rename, free to drift. The file helpers
  (`readDraft`/`appendDraft`/`writeDraft`) and the tree edits are unexported;
  only `RemoveDraft` (a re-plan, a reset without a tree) and `ApplyDraft`
  are left beside the type. **Replay is
  idempotent** — an edit naming folders that are gone, or that changes
  nothing, is skipped — which is what makes a crash between `ApplyDraft`'s
  commit and its file removal harmless. A torn last line (a crash
  mid-append) is dropped **and the file rewritten without it** (`readDraft`;
  likewise a whole last edit missing only its newline is kept and the file
  rewritten with one):
  left in place, the next append glued onto it, lost that edit, and the one
  after made every read fail — review would not open and execute refused,
  with `[R]` unreachable inside the review that no longer opened (caught in
  review). **Review never copies or moves**:
  `[x]`/`[X]`, `transfer.go`, the move question and the progress bar are
  gone; `wandersort execute` (and issue 21's copy screen) is the only place
  files move.
  `copy.go` holds the unexported
  `copyFiles`/`copyFile` the peek feature uses (same atomic
  temp-file-then-rename pattern `pkg/install` downloads with); it moved here
  with the TUI because the preview is its only caller.

  **The tree-reshaping rules themselves — merge, drop, flatten, and the
  tree-walking helpers they share — live in `pkg/core/vfs/edit.go`
  (`mergeNodes`/`dropNodes`/`flattenNodes`/`sortTree`/`cloneTree`, all
  unexported, reached only through `Draft.Apply`; `FindNode` stays exported),
  not on `Model`.** They take a `[]vfs.Node` and a list of IDs and
  return the edited tree plus what happened; nothing in that file knows a
  keypress or a row exists. `Model`'s `mergeSelection`/`dropFolders`/
  `flattenFolders` are thin callers: resolve `selectedRows()` into an ID list,
  hand the draft a `vfs.Edit`, then word the returned `vfs.Outcome` for the
  status line and apply it back onto cursor/status state the tree edit itself
  has no business touching. This is also
  `MergedIDs`' one owner now — `vfs.Confirm` was already the other half of
  that invariant (interpreting what this file writes), so putting both in
  `pkg/core/vfs` means one package, not two, understands it. The payoff: a
  tree edit is tested by stating a tree, calling the function, asserting the
  result (`pkg/core/vfs/edit_test.go`, and `draft_test.go`'s
  `TestDraftSession` for apply/refuse/undo/reset through the `Draft`) — no
  `tea.KeyMsg`, no terminal.
  `review_test.go` still drives some of the same edits through keypresses,
  but that's now testing the wiring (selection → ID list → seam call), not
  the reshaping logic itself.

  The model (`review.go`): renders the whole hierarchy indented, alt-screen
  fullscreen, scrollable. Keys: `n`/`N` **hop to the next/previous row at
  the cursor's own depth** (`jumpSameDepth`), crossing into other branches
  by design — that's what makes `V` then `n``n` select one level across
  several months without arrowing through every folder's contents; stops at
  the ends, never wraps. `r` rename (with
  ranked autocomplete — `Tab` fills the top match, `Ctrl-E` widens the search
  radius by another ~10km, typed text also prefix-matches previously
  typed `user_labels`), `p` peek (copies up to 250MB of the folder's
  files into a temp dir via `copyFiles` and opens that folder —
  read-only, nothing on disk is touched), `V`/`m`/`u`
  Vim-style merge: `V` starts a contiguous range (sequential — no picking
  rows out of order), `m` **folds every row in the range at the anchor row's
  depth into one node under their lowest common ancestor**
  (`mergeNodes`, via `chainTo`/`commonChain` +
  `FindNode`/`removeChildByID` — a real tree-splice, not just a rename),
  named after **the row `V` was pressed on** — its own name, which is
  already the rename the reviewer typed on it, since a rename is written
  straight onto the node — with the summed
  `FileCount`. `mergeSelection` pulls the anchor's ID to the front of the
  edit it hands the draft (`mergeNodes` always keeps `ids[0]`) precisely so
  this holds regardless of which direction the range was extended in:
  `selectedRows()` normalizes low/high to tree order for iteration, so
  extending *upward* from the anchor would otherwise silently hand naming to
  whichever row ended up topmost instead of the one actually pressed.
  **Exception: merging plain Date-level folders proposes the day
  range they jointly span instead** (`combinedDayRange`/`formatDayRange`,
  `pkg/core/vfs/edit.go`) — merging `01_02` through `24_26` names the result
  `01_26`, the same `"%02d_%02d"` shape `mergeSameLocationDays` itself would
  have proposed had it seen the days as one run to begin with. This only
  fires when every pick's name parses as a Date folder (`"03"` or `"01_02"`).
  Anything not day-shaped at the anchor's depth (a location, a device, a
  folder the reviewer renamed to something of their own) falls back to the
  anchor's own name as usual — a rename *is* the node's name (see the rename
  paragraph below), so no separate "was it renamed?" check is needed.
  **Anchor
  depth is the
  selection rule** — rows deeper than the row `V` was pressed on are that
  folder's own contents and ride along; shallower ones are scaffolding
  spanned to reach the next branch. One rule covers both shapes: leaves from
  different branches (anchor on a leaf) and whole parent folders (anchor on
  a Day). **Merging parents merges their subtrees**: children whose names
  match collapse recursively via `mergeInto`/`childByName` (both unexported,
  `pkg/core/vfs/edit.go`) — three days in Goa
  give one Goa holding one merged device folder, not three the reviewer then
  has to merge by hand. This is what makes merging work across different Month/Day
  branches (e.g. one camera's photos spread across three months, all folding
  under the Year) — a plain same-path rename only merges nodes that already
  share a parent, since the final path is parent-path + name. **The
  folded-away leaves leave the tree entirely** — their IDs ride along on
  `vfs.Node.MergedIDs`, which is what `Confirm` moves their files by (a
  folded folder's subfolders move onto the survivor too, so anything *below*
  it follows). An earlier version
  left them in place as same-named siblings and let `Confirm`'s
  same-path-collapses-to-one-folder behavior sort it out at write time —
  correct on disk, but the reviewer saw three "Canon EOS 700D" rows next to
  three now-empty Month/Day chains and read it as "merge didn't work".
  Emptied ancestors are pruned and ancestor `FileCount`s recomputed
  (`pruneEmptied`, using a pre-merge leaf-ID set to tell a real leaf from an
  ancestor the merge hollowed out). Rows caught in the range that still have
  children (the Month/Day scaffolding between two branches) are skipped, not
  merged. **`u` undoes every edit all the way back**, not just the
  last one: every edit — renames included — goes through
  `Model.applyEdit` into `Draft.Apply`, which journals it; `[u]`
  (`Draft.Undo`) drops the draft's last line (temp file + rename) and
  replays the rest onto `base`.
  The journal is the whole history, so there is no snapshot stack and no cap.
  `d`/`D` **remove nesting the reviewer doesn't want**. Both act on
  `selectedRows` — a `[V]` range (every row in it at the anchor row's depth,
  the same rule `m` uses) or just the cursor row when there's no selection.
  Nothing acts tree-wide:
  - `d` (`dropFolders` → `dropNodes`) drops **each selected folder**,
    lifting its children onto its parent. Refused on a Year or Month row
    (see fixed folders below).
  - `D` (`flattenFolders` → `flattenNodes`) collapses **everything below** each selected
    folder into it, so the whole subtree's files sit directly in it and the folder
    itself stays. Refused on a Year (its Months would go); works on a Month,
    since the Month survives to hold them.
    `2023/April/Indore/Apple iPhone 13` flattened at April is
    `2023/April` with all ten files. `FileCount` is unchanged — it already
    counted the subtree. Over a range the folders stay **separate**: several
    locations under one Day each keep their own folder and lose their
    splits. Folding them together is `m`'s job, not `D`'s.

  **Year and Month folders are fixed** (spec D26, `vfs.ErrFixedFolder`,
  decided by `Node.Level` from `folder_nodes.level`, never by depth): `[r]`,
  `[m]` over them and `[d]` are refused with a `⚠` status line, `[D]` on a
  Year too. Settings can't turn them off and new files find them by name
  (D15), so a renamed month made the next batch plan a second `03_March`
  beside it. The guards sit in `mergeNodes`/`dropNodes`/`flattenNodes`
  and `applyEdit`'s rename case — once, for live edits and replay alike.

  **Every structural edit re-sorts the tree by name (`sortTree`, run by
  `Draft.Apply` and after each replayed edit) and the merge puts the cursor on the surviving folder
  (`focusNode`).** Splices append — a merged node, or children lifted by a
  drop — at the end of the parent's list, so a 575-file day jumped below its
  siblings and got reported as "the merge deleted my folder". It hadn't; it
  was just off-screen at the bottom.

  Both record the removed IDs (plus anything already folded into them) on
  the surviving node's `MergedIDs`, so files sitting directly in a removed
  folder remap onto it — same machinery as merge, subfolders included. Both
  undo via `[u]`.
  **There is no save step** (spec D18): every edit is already in the draft,
  so `esc` (after clearing a `[V]` selection) and `ctrl+c` just leave — no
  Save/Discard question, no unsaved-edits warning. **There is no `--rebuild`
  flag and no manual rebuild inside the review** — a stale proposal (the
  settings moved since it was built) re-plans itself before the screen is
  ever shown, over in `cli/review.go` (see above); by the time a reviewer
  sees a tree, it already matches the current settings.
  **`R` is a plain reset, not a rebuild, and asks nothing** — it deletes the
  draft file (`Draft.Reset`) and shows `base`, the plan as proposed. The database is
  untouched (it never held the edits), so there is no query and no spinner.
  Capital `R` because `r` is rename. Cursor and selection reset with it.
  **The screen is built from `pkg/tui`, like scan and config** — it used to
  hand-roll its own chrome and looked like a different program: `tui.Screen`
  pins the footer to the terminal's last row, `header()` is banner + one
  summary line (folder/file totals in the right column), every tree line is a
  `tui.Row` with the file count aligned at the right edge (the kit's "right
  column is the screen's one number" rule), the key bar is `tui.Footer` +
  `tui.KeyHint`s that swap merge keys in while `[V]` is active, and the peek
  spinner is the same `bubbles` dot spinner the scan screen runs, not a
  hand-rolled `|/-\` ticker. **`?` opens a full-screen key reference**
  (`helpView`, grouped Moving/Naming/Reshaping/Leaving with one-line
  explanations; any key returns) — the footer names the keys, the help
  explains them. **Both the header and the footer are measured**
  (`visibleRows` via `lipgloss.Height`), never a fixed line count: the key
  help word-wraps on a narrow terminal, and the old fixed `height-6` budget
  let a wrapped help bar push the bottom tree rows off the screen.
  Rows are drawn with real box-drawing guides (`reviewRow.guide`, computed in
  `flattenTree` because a row can't tell it's a last child from its depth) —
  the old two-space indent plus a `└` on every level made sibling and child
  look alike.
  **A rename is written straight onto `Node.Name`** (`applyRename`, an
  `applyEdit` like merge and drop) — there is no pending-rename layer, no
  `row.newName`, and therefore no `old → new` arrow on the row. There used to
  be one, and it was a reported bug twice over: the arrow made an applied
  rename read as still-unapplied, and because the name lived in a row field
  keyed by node ID rather than in the tree, `[u]` restored the tree but left
  the arrow and its text behind. **There is no suggestion concept at all any
  more** — no `Node.Suggestions`, no `⇢` row marker, no `enter` accept, no
  `a` accept-all, no `AcceptAll`, and no `suggestion`/`suggestion_source`
  columns: the pipeline hands the reviewer a finished plan, and they edit it
  by renaming, merging, dropping and flattening. A second "accept" verb next
  to `[r]` was one concept too many for the same act, and a row that showed
  both a name and a competing offer for it never read as a decided plan.
  What the reviewer *does* type is remembered (`vfs.Confirm` writes every
  renamed folder's name to `user_labels`, and `vfs.Labels` reads them back —
  the writer and the reader of that table live in one package, rather than the
  TUI running its own `SELECT` against a schema it otherwise knows nothing
  about) and comes back as a `used before` rename completion next time.

  **The rename dropdown's ranking is `pkg/location`'s, not this package's**
  (`location.Resolver.Suggest`, taking a `SuggestQuery` of prefix + prefetched
  nearby `Candidate`s + prior labels). It ranks nearby places, then prior
  names, then a geonames prefix search; dedupes on the folder name; and caps
  the list. `internal/review/autocomplete.go` is now just the per-row
  `Candidates` fetch (refreshed only by `[r]` and `ctrl+e`, since the radius is
  a TUI concern) and one call. That package already owns which qualifier a name
  needs, how it reads in a list, and what is safe on disk — a caller that ranks
  and sanitizes for itself is re-deriving all three.
  **`↑`/`↓` walk the rename dropdown** (`Model.suggCursor`, `-1` = nothing
  picked), `tab` fills the picked-or-top match and `enter` on a picked row
  fills it rather than applying — the same completion behaviour as the
  `config` wizard's town inputs, which is where reviewers expect it from. **Preview caching is content-based and outlives the
  session** (`preview.go`): a copy lives at `previewDirFor(files)` —
  `$TMPDIR/wandersort-previews/<sha256 of the sorted file list>` — so a
  directory with one child chain (e.g. `.../08/Horizontal/Photos`), which
  shares literally the same files between its parent and its leaf, resolves
  to one copy for both, **and so does the same folder peeked in a later
  session**. That is what the fixed root buys over the old
  `os.MkdirTemp` name plus an in-memory `previewDirs` map (both gone, with
  `filesSignature`): re-copying gigabytes because the reviewer quit and came
  back is the thing worth avoiding. **A directory existing under that name means
  the copy finished**, because the copy is made in a `.copying-*` staging dir
  next to it and `os.Rename`d into place — same directory, so same
  filesystem, so atomic. A peek killed partway (ctrl+c, SIGKILL, disk full)
  therefore cannot leave three of forty files under the hash name for a later
  session to reuse as a cache hit, which is the failure the old
  wipe-on-exit temp dirs never had to defend against. There is no completion
  marker file (there was one; the rename does the same job with one mechanism
  instead of two), and no partial-dir cleanup path: a leftover `.copying-*`
  is never a hit and `makeRoom` evicts it like any other. A `Rename` that
  fails because the target is already there means another process copied the
  same folder first — its copy is this copy, so take it. **The copies are capped at 5% of the
  volume** (`makeRoom`, budget = `(free + what the copies already hold) /
  20`): before a new copy, the **least recently opened** dirs are evicted
  until it fits, and one that can't fit in the whole budget is refused rather
  than half-copied. LRU, not oldest-created — a reuse `touch`es the dir, so
  peeking a dozen folders in one session can't evict the one being looked at
  now. `maxPreviewBytes` (250MB) still caps a single peek. Nothing
  is deleted on exit any more — **the sweep happens when the plan is
  written** (`CleanPreviews`, run from `execute.Run`'s `OnApplied` once the
  draft is applied)
  and from `wandersort admin clear`, since at that point there is nothing left to
  peek at. Leaving a review deliberately keeps the copies for the next
  session.
  **A rejected merge (`statusIsErr`) renders in `tui.Attn`, not
  `tui.DimText`** — a rejection used to look identical to routine status
  text, easy to miss (a reported "merge doesn't work" turned out to be
  either a missed capital-`V` requirement or exactly this — the rejection
  message was there, just easy to overlook), and it carries a `⚠`.
  A live `[V]` range also says so above the key bar (`-- SELECT -- n
  folders`). **Selection is a whole-row background highlight
  (`tui.Selected`), not a marker character** — a `*` prefix in one column was
  hard to track across a wide tree. The **cursor row** carries the same
  highlight (plus a `❯` in `tui.Primary`), matching how the config wizard
  marks the option under the cursor; highlighted rows render plain (no nested
  per-segment colour) since an inner ANSI reset would cut the background
  short partway through the line.

Back in `internal/cli/`:

- **There is no `report` command.** It used to print per-session
    scanned/hashed/duplicate counts read from `scan_sessions`; once that
    table was dropped (no persisted session/run record — see "What
    WanderSort is" above), there was nothing left for it to read, so it was
    deleted rather than rewritten. `wandersort review` is the natural next
    step after `scan` now.
- `check.go` — `check` cmd (was `verify`): `pkg/core/verify` plus the one thing that
    package deliberately doesn't do, which is say it out loud. **Every
    failing file is named on screen, not only in the log** — a count is not
    something a person can act on, and these are their photos. **Grouped, one
    line per file**: one heading per kind (gone, changed, unreadable, not as
    recorded — `groupProblems`) with the paths under it, and no per-file log
    warning beside the list (it only said everything twice). `--full`
    re-reads contents; without it the pass is existence and size and the
    output says so, and points at `--full` for the rest. A damaged database
    names `wandersort admin db --restore`; strays are listed as safe to delete. Exits
    non-zero only on real problems, never on strays or forgotten files alone —
    forgetting leaves the records true.
- `admin_report.go` — `admin report` (was `issue`): zips the newest `issueLogs` (5) non-empty logs
    other than its own run's, under `logs/`, + `about.txt`, into the **current
    directory**, not the library; db opt-in via `--include-db` (holds paths/GPS).
    Always ships `errors.json` from the `errors` table (see `pkg/report`),
    home directory rewritten to `$HOME` — in the logs too; `--redact-paths`
    replaces every path in `errors.json` and **leaves the logs out** (free
    text can't be promised path-free), and can't be combined with
    `--include-db`.
- `admin_clear.go` / `admin_db.go --reset` (were `reset` / `reset --db`) —
    `admin clear` clears only the peek copies
    (`review.CleanPreviews()`), asks nothing, and names `--db` for the rest:
    nothing about throwing away a cache is worth a question. `admin db --reset` is
    the factory wipe (confirm prompt unless `--yes`). **A library already
    holding files gets a pointed question** (`db.PlacedCount`): "Forget the N
    files already in the library?" — they stay on disk but nothing records
    them, so a re-imported card copies them in twice. **An already-empty
    database is not wiped at all** (`db.IsEmpty`, "nothing to reset"): its
    backup would replace the one holding what the earlier reset deleted with
    an empty copy, leaving `--restore` nothing to bring back. Otherwise it
    **backs the database up first** (`db.Backup`, the same
    `.wandersort.db.zst` execute writes — a failed backup stops the wipe),
    then clears the rows, the review draft — which describes a proposal that
    no longer exists — and the peek copies, which outlive a review session.
    **The library's settings row survives a reset** (`db.ResetAll` doesn't
    touch `library_settings`): a factory wipe of the *data* is not a request
    to forget which folders the user wants. `config.CheckLibrary` points a folder holding the backup
    but no database at `admin db --restore` instead of refusing it as foreign.
- `admin_db.go --restore` (was `recover`): `db.Restore` puts
    `.wandersort.db.zst` back (confirm unless `--yes`), which is what makes
    both `--reset` and an execute run undoable. The backup is kept. **Not `openLibrary`**: that
    would open the very database being replaced. It takes the output lock
    (keeps other wandersort processes out), then `Restore`:
  - **decompresses the backup** (zstd) into a temp file beside the live
      database, removed on every path, then checks that (our
      `application_id`, `PRAGMA quick_check`) — a corrupt or truncated
      backup is refused, by name, before anything is written;
  - refuses with `db.ErrInUse` while **any** connection has the live file
      open, even an idle sqlite browser — detected by
      `locking_mode=EXCLUSIVE` + `journal_mode=DELETE`, which SQLite only
      allows with every other connection gone, and whose lock is then held
      until close so nobody opens it mid-restore;
  - **keeps the database it is about to replace** as
      `.wandersort.db.before-restore` (`keepBeforeRestore`) — a byte copy
      under the exclusive lock, so it works on a database too damaged for
      SQLite to read, with `VACUUM INTO` as the fallback (Windows can't read
      SQLite's locked byte range past 1 GiB). A restore run by mistake is
      undone by renaming it back;
  - copies through SQLite's online backup API (`NewRestore`) on that same
      connection — **never a file swap**: the pages go through SQLite's own
      rollback journal, so a crash rolls back to the old database, and there
      is no `-wal` file to delete by hand and get wrong.
    Then it opens the result once with `db.New` to prove it is a library.
- `app.go`'s `confirm` is the one yes/no prompt (`execute --move`,
    `admin db --reset`, `admin db --restore`): a `tui.ConfirmModel` in the TUI, y/N on stdin
    under `--plain`/non-TTY.
- `state.go` — **the one answer to "what can the user do next?"**
    (`libraryState`, `app.readState`, `app.libraryExists`). It used to be
    eighteen predicates across four packages: five spellings of "is there a
    plan" alone — one method and four inline `os.Stat(AppDBPath)` calls with
    four different error strings — two of "are there review edits" that
    disagreed about an unreadable draft, and a tab bar running a filesystem
    syscall from `View()` on **every rendered frame**. Each was right alone
    and they drifted as a set.
    The struct is in two halves, and the split is load-bearing rather than
    tidy: opening a library takes the exclusive lock and creates the database
    if it isn't there, and a session that only looks around must do neither
    (`openLibrary`). So `Exists` (a stat) and `Edits` (the draft) are always
    true, while `Open` and `Planned` are zero until this session has really
    opened it. `Planned` is `execute.Pending`'s file count — the one
    definition of "waiting to transfer", next to the rows `execute.Run`
    reads, so the CLI writes no SQL of its own. `CanReview()` reads `Exists`
    when shut and `Planned > 0` when open — so a library with everything already organized stops saying
    `✓ ready` and then telling the reviewer there is nothing there. A count
    that fails is treated as "reachable": a library we can't count is still a
    library, and the screen behind the tab reports the real error.
    `shellModel.lib` caches it, refreshed only where it can have changed —
    `scanReadyMsg` (the library just opened), `handleSwitch` (a scan handed
    over its review, or a review handed back), `replanDoneMsg`, and `ctrl+t`,
    which is the one key that asks the question.
- `help.go` — custom lipgloss-styled help renderer. Kept in `cli` (unlike
    `lock.go`) since it's a one-off cobra `SetHelpFunc`, not reusable
    logic another entry point would need.
- `internal/cli` holds **only** `app.go` + `root.go` + `shell.go` + one file
    per subcommand (plus the `help.go` exception above). **No single-function
    files**: the old `tui.go` (just `tuiEnabled`) and `anchor.go` were folded
    into `app.go` and `core/vfs` respectively. Everything else that
    used to live here moved
    out to its own package so a future TUI entry point can reuse it:
  - `pkg/lock/` — all wandersort file locking: generic acquire mechanics
      (`acquire`, `Lock`, `ErrHeld`) plus the two domain wrappers —
      `AcquireOutput` (one scan per output dir) and `AcquireInstall` (install coordination across
      scan/review — see `pkg/install` below, the one caller) — and the lock
      filenames (`OutputFileName`, `InstallFileName`). The lock itself is a
      real OS advisory lock (`tryFlock` — `unix.Flock` in `lock_unix.go`,
      `windows.LockFileEx` in `lock_windows.go`, same per-platform-file split
      as `pkg/volume`), not a hand-rolled PID file: the kernel releases it
      the instant the holding process's file descriptor closes, crash or
      SIGKILL included, so there is no dead-PID staleness check and no
      leftover lock file that ever needs deleting by hand — the PID still
      written into the file is only there so the "already running" message can
      name the holder. **This package renders nothing**: a held output lock
      comes back as `*lock.AlreadyRunningError{PID}` and `cli.app.lockOutput`
      styles it. It used to import `pkg/tui` to colour that string, which put
      a full-screen TUI kit underneath a file lock.
      `Coordinator`
      tries the install lock non-blocking first so it can log a
      `UserKey`-tagged "waiting for another process…" line before falling
      back to the blocking acquire — without that, a scan waiting behind an
      in-progress install just looks hung. Only `cli` (via `pkg/install`)
      uses locking today, but the mechanics are generic, so it lives in
      `pkg/` for reuse by other entry points.
  - Styling (help renderer, lock messages, error output) comes from
      `pkg/tui`'s theme — there is no separate `pkg/style` any more; the old
      one was folded into `pkg/tui/theme.go` so full-screen and plain output
      share one palette.

**Settings live in the library, not in a file or the environment** (spec D2).
`library_settings` holds one row per library — `rules`, the three folder
toggles and the saved-place names — read by `app.openLibrary`
(`config.LoadSettings`) and written by the wizard (`config.SaveSettings`, via
`app.saveSettings`). A library that has never been through the wizard has no
row and keeps `config.DefaultSettings()`. **There is no config precedence
chain left**: no `~/.wandersort/config.yaml`, no `config.Resolve`/`Load`/
`Save`, no `Overrides`/`TriBool`, no `OUTPUT_PATH`/`COLLAPSE_LEVELS`/
`SAVED_PLACES_DATE_ONLY`/`MERGE_SAME_LOCATION_DAYS` env vars, no per-setting
flags, and no yaml dependency. A setting that can move a file belongs to the
folder the files land in — a global file meant a second scan into an
already-organized library could quietly plan it by some other library's
rules. The only thing left to choose per invocation is *which* library:
`--output-path`, else the most recently used one.

`~/.wandersort/libraries` is that history (spec D3, `pkg/config/history.go`):
one folder per line, newest first, at most 100, and folders that no longer
exist are dropped as the list is read — an unplugged drive's library is not
one to offer. `Remember` is called by `openLibrary`, the one moment a folder
is known to really be a library; `config.New` reads the list for its default
output folder, and the wizard's output-path field offers it first.

There is no viper anywhere in this codebase — every other
command's own flags (`--yes`, `--plain`, …) are read straight off `cmd.Flags()`
in their own `RunE`. **There is no `segment-months` setting any more** — it
sized the review's now-removed time-slice picker (issue 19); review is one
tree over the whole library.

## Core pipeline (`pkg/core/`)

- `workflow/` — orchestrator. `RunScan(ctx, paths, force)` runs the phase loop
  (scan→metadata→vfs) synchronously on the calling goroutine, so a CLI
  invocation streams progress and blocks until the scan finishes. It runs
  `scanRoots` first — canonicalize, drop duplicates, prune any root nested under
  another (`path.ReduceRoots`, checked against *every* kept root: a lex sort
  does not keep a folder's descendants adjacent — `/a b` sorts between `/a`
  and `/a/c` — and a last-only check walked `/a/c` twice) — returning the
  roots actually walked. **A root that is the library, holds it, or sits in
  it is refused** (`ErrOverlapsLibrary`, `path.Overlaps`) before anything is
  walked: scanning the library as a source re-read placed files and planned
  any untracked file in it back into the library. That
  pruning used to live in an HTTP layer (removed along with `serve` — this is
  now a single-entry-point CLI), which meant `wandersort scan` reached through
  `internal/api` to get at it. `NewWorkflow` takes a `Deps` — two blocking
  getters for the downloadable dependencies — instead of a resolver and
  exiftool path: the metadata and vfs components are built lazily inside their
  phase closures, calling `deps.Exiftool()`/`deps.Location()` right before
  running, so a first-ever TUI scan walks while the downloads are still going.
  The metadata phase is the first to block on exiftool now that hashing no
  longer runs ahead of it. In practice both `add` paths now wait for both
  downloads before any phase starts (`waitForDeps`, `add.go`), so a failed
  download is one clear error instead of a phase failing mid-run; the lazy
  getters remain the seam. **`appCfg` is fixed for the run** —
  there is no `UpdateConfig`/mid-run retargeting any more: the settings tab
  is unreachable while a scan runs (`cli/shell.go`'s `nextTab`), so nothing
  can save a change for the vfs phase to pick up mid-flight. `BuildAnchors`
  (which replaces `r.Anchors` in place and is read
  lock-free from the parallel `Lookup`) is called exactly once per run for the
  same reason — never call
  `vfs.Propose` concurrently with itself. **There is no `scan_sessions` table and
  no in-memory run-overlap guard either** — `RunScan` takes no ID and returns
  none; two scans can never race against the same output dir because
  `lock.AcquireOutput`'s exclusive PID lock already serializes at the process
  level, and within one process `RunScan` only ever runs once, synchronously,
  per `scan` invocation (the old `claimRoots`/`activeRoots` map existed for
  the since-removed `serve` API's concurrent sessions and was unreachable
  dead code by the time it was deleted). `pkg/volume/space.go` holds `CheckOutputSpace`
  — exported because `review` runs the same check: the last look before a
  plan is approved is exactly where "the output volume is too small" is still
  actionable. It fires **at the end of the run**, next to the "run wandersort
  review" hint, not after the scan phase where it used to scroll past
  mid-pipeline. `NewWorkflow` logs the resolved `workers`/`output`/`groupBy`
  as a `UserKey` line: `output` comes from `--output-path` or the most
  recently used library and `groupBy` from the library's own settings, so
  showing them up front is the only way to see what this run will do. **`Workers` is not a setting** — there is no `--workers` flag, no
  `WORKERS` env var, no `workers` key and no wizard step. It sizes the
  goroutine and exiftool pools, both CPU-bound, so `runtime.NumCPU()` is the
  right number; the one disk-bound thing in the pipeline (the metadata phase's
  byte reads) is throttled by the storage class instead (see
  `pkg/core/metadata`). One hand-set number could only ever be wrong for one
  of the two.
  Each phase reports **one** user-facing line: `workflowPhase.summary(count)`
  with the elapsed time appended (`Scanned 15481 files in 1.996s`). It used to
  be two — a count line from `onSuccess` plus a separate `"%s phase took %s"`
  — which is twice the console noise for one fact. `onSuccess` survives for
  side effects only (the post-scan space check); anything user-facing goes in
  `summary`. Per-run counters (`files_discovered`, `files_hashed`, …) are not
  persisted anywhere — they were columns on `scan_sessions`, and the TUI's
  progress bar and the phase-summary lines above were always driven by the
  phase's own return value and by `StreamKey` log lines, never by reading
  those columns back, so nothing was lost when the table went away.
  **A run's outcome is its error, not a status string**: a phase failure
  comes back as `%s phase failed: %w`, a cancellation as `pipeline cancelled
  during %s phase: %w`, so `errors.Is(err, context.Canceled)` works at the
  call site. (`RunScan` used to return `errors.New` of a message, which lost
  the chain; the `db.StatusCompleted/Failed/Cancelled` strings existed only
  for that and are gone.) **The context is a `RunScan` parameter, never a
  `Workflow` field**: the shell runs many scans in one process, and a stored
  context outliving its scan is the trap.
- `scanner/` — phase 1. Bounded-worker directory walk. Files are identified by
  absolute `(file_dir, file_name)`; each root's volume UUID is stamped for
  future drive re-anchoring. `Run` numbers the scan — `MAX(last_seen_scan) +
  1`, newer than every stored row, and the output lock means no other scan
  takes the same number — and `storeScan` stamps it on every row it sees;
  after a clean walk, `sweep` **hard-deletes** rows under that root whose
  `last_seen_scan` is older, i.e. the walk didn't re-see them. **A counter,
  never a clock**: it was a `time.Now()` cutoff against `last_seen_at`, and a
  wall clock steps backwards (NTP, waking from sleep), so a file seen in that
  window compared as unseen and lost its hash and plan. UTC doesn't help — the
  clock itself moves. `last_seen_at` is still written, for people reading the
  table. **The sweep also keeps rows the walk may not have been looking at**:
  a root that walks clean but *empty* while the library knows files under it
  (an unmounted drive's mount point is an empty, readable folder) sweeps
  nothing and says so; rows stamped with a different volume UUID than the one
  now at the root are kept (macOS mounts every unnamed card at `/Volumes/NO
  NAME`, so a second card would have swept the first's rows). Unknown UUIDs
  on either side sweep as before.
  **"Clean" means the root walked, not that every folder under it did**: the
  walk records the folders it couldn't list and the files it couldn't stat
  (`walkGaps`), and `sweep` keeps rows under them — unseen is not gone (a
  transient EIO used to sweep a folder's hashes and plan away). Past
  `maxSweepGaps` the sweep is skipped for that root. **The upsert op does
  nothing outside its transaction**: the writer replays a failed batch op by
  op, so a `WaitGroup.Done()` it once carried fired twice and panicked the
  scan.
  **There is no soft delete, no `deleted_at`, and no retention window any
  more** (spec D10: only what is in the library matters) — `sweep` deletes
  the vanished file's `file_registry` row, in one transaction, immediately;
  its metadata, plan and error rows go by `ON DELETE CASCADE` (`file_metadata`,
  `virtual_fs_entries` and `errors` all cascade from `file_registry`).
  **A changed file is a new file** (issue 20): `storeScan` deletes the row
  when size or mtime differs (or under `--force`) and inserts a fresh one, so
  the file is read and planned from scratch — nothing in the pipeline moves a
  row backwards any more, and there is no reset path in the upsert.
  **Symlinked files are skipped** with a warning, like directory links
  (which `WalkDir` never follows): a link's recorded size is the link's, so
  `check` called every one damaged, and a Linux move carried the link, not
  the photo, into the library.
  A placed file is never a sweep candidate: `execute` repoints its
  `file_dir`/`file_name` at a library-relative path once it lands (see
  `execute/` below), and a relative path is never under an absolute scan
  root. **No filename-stem
  capture-grouping** (there used to be
  one, `capture.go`'s `DeriveCapture` — deleted): it force-paired files
  sharing a base filename (e.g. `IMG_8017.HEIC`+`.MOV`+`.JPG`) into one target
  directory on the assumption that same-stem meant same-capture (Live Photo
  pairs, RAW+JPG). Wrong assumption — phone/camera filename counters get
  reused across entirely unrelated shoots (a real reported bug, especially
  with old iPhone photos), so it was forcing unrelated files together. Every
  master now gets its target directory from its own derived data only
  (`vfs.go`'s `dirFor`, per file) — a real Live Photo pair still lands
  together because its members genuinely share GPS/timestamp, not because of
  stem-matching.
- `metadata/` — phase 2, **the pipeline's only pass that reads file bytes**.
  One worker BLAKE3-hashes a file and then runs exiftool over that same file,
  back to back, and persists both as a single `file_metadata` row. Hashing and
  EXIF were **two phases once** — each claiming its own rows, each with its own
  TUI stage — and the reason they are one again is the page cache: the hash
  streams every byte of every file, then the exif phase came back for the same
  files thousands of files later, by which point the cache had evicted them and
  the header read went to disk a second time. Reading each file once, with the
  two consumers adjacent, is the entire point of this package; **don't split
  them again for tidiness**. **There is no scan status at all** (issue 20;
  `file_registry.scan_status` is gone): a file is *unread* when it has no
  `file_metadata` row (`unreadFiles`, one
  predicate shared by the progress total, `pendingVolumes` and the producer's
  pages). Hash and EXIF land in one row, so nothing is half-persisted, and
  **handing a file out writes nothing**: the producer pages through the unread
  files per volume (`nextBatch`, `readBatchSize` = 256, forward-only `id`
  cursor — the predicate only ever shrinks, so it can neither repeat a file
  nor skip one). An interrupted run left no claim to reset; the files in flight
  are simply read again. A file that cannot be read gets a `READ` `errors` row
  (op `open`/`hash`, `attempts` bumped) and **is tried again on every
  `add`** — most failures are a card reader or a cable, and a file skipped
  for good was a photo left out of the library with nobody told. The cursor
  still hands it out once per run; the run ends with `N files could not be
  read`, and `execute` names every never-read source (`execute.LeftBehind`)
  in its `--move` question and at the end of every run, so a source is not
  treated as done while it still holds unread files. A panic in a worker is recovered per file (`readOne`), recorded as
  `kind = 'panic'` with the real stack, and the run goes on.
  **Known gap:** full-byte hash means pixel-identical files with differing
  metadata land in separate groups.
  **Every file is read in full — there is no unique-size skip** (spec D7): a
  size stand-in cannot verify a copy or match a file seen again, and
  placement, duplicate detection and copy verification all key on content.
  Stored as `blake3:<hex>` (`hashPrefix`), so the algorithm is named in the
  value. The old `uniqueSizes`/`sizeDerivedHash`/`rehashOutdatedSizeHashes`
  prefilter and `file_metadata.hash_kind` are gone; the cost is ~29 minutes
  of extra reading on the 783 GiB HDD run.
  **The read itself streams through a pooled 1 MiB buffer**
  (`hashBufferSize`, `hashBuffers`) rather than `io.Copy`'s 32 KiB — 783 GiB
  at 32 KiB is ~25.6 million read syscalls. It goes through `readerOnly`,
  and **that wrapper is load-bearing**: `*os.File` implements `io.WriterTo`,
  which `io.CopyBuffer` prefers, and whose generic fallback allocates its own
  32 KiB — without the wrapper the buffer argument is silently ignored and
  the change is a no-op that still reads correct. Nothing is lost by hiding
  it: `File.WriteTo` only has a fast path when the destination is a socket,
  and this one is a hasher.
  **The byte read is throttled by the storage class, the worker pool is not**
  (`readCost`/`readTargets`/`readFile`, `semaphore.Weighted`). A budget of
  `min(workers, maxReadBudget=16)` is charged per file by the class of the
  volume it lives on: rotational costs the whole budget (1 read at a time),
  removable half (2), unknown a quarter (4), solid-state and network 1 each
  (the full budget). Same idea as Postgres' per-device `random_page_cost`,
  except it throttles rather than plans. **Only `hashFile` is inside the
  gate.** Shrinking `workers` instead would shrink the exiftool process pool
  with it (`exiftool.NewPool(path, workers)`), and exiftool is CPU-bound Perl
  reading a header the hash just warmed in the page cache — on the measured
  100k-file run that trade would have cost more in the exif half than the
  hash half could win. The cap is on reads for the same reason: a 64-core box
  wants 64 exiftool processes and never wants 64 concurrent reads. The
  producer drains **one volume at a time, fastest first**
  (`pendingVolumes`/`pendingVolume`), which does not change total wall time
  but front-loads progress so an interrupted run has the cheap files done;
  **`ORDER BY id` within a volume is untouched**, because id is discovery
  order is walk order is roughly directory order, which is as seek-friendly as
  a read order gets — don't "improve" it. A closing unscoped pass
  (`pendingVolume.all`, which is *not* the same as an empty `uuid`) catches
  any volume the grouping missed; it excludes the volumes already drained,
  since their last files may still be in flight and would look unread. `readCost` clamps to `[1, budget]`: a
  cost above the budget would block forever.
  Sidecars (`.AAE`) are read and hashed like anything else but never handed
  to exiftool (`fileRecord.mediaType`, checked in the worker) — they carry no
  EXIF, so spawning exiftool on them is pure waste. **An extraction failure is
  not a file failure**: it warns, persists empty EXIF columns, and the
  file counts as read, with no `errors` row (the hash and folder context are still enough for the VFS to
  place it). **Except when exiftool itself failed** (`exiftool.ErrProcess`:
  the process died, hung past `extractTimeout`, or its pipes broke): that is a
  `READ` row with op `exiftool`, because the tags are unknown rather than
  empty, and an empty row would mark the file read and plan it by its file
  date for good. The next `add` retries it. (A pool that never started — no
  binary — still persists empty rows; the workflow installs exiftool before
  this phase, so that path is tests only.) Workers write straight through `db.Writer` — it already serializes
  every operation, so a separate store goroutine would only add a channel.
  Persists `exif_creation_date` alongside `exif_create_date` — a real
  reported bug: a QuickTime video's `CreateDate` is the raw UTC timestamp with
  no offset, while a photo's `DateTimeOriginal` is local wall-clock; for the
  same real moment these can differ by hours, enough to shift a video into
  the wrong day/cluster next to its photos. `CreationDate` (iOS's composite
  tag) does carry an offset — `vfs.stripOffset` strips it back off before
  parsing (see `deriveAll`'s `takenAt` comment), because every *other*
  timestamp here is naive local wall-clock and applying the real offset would
  shift the video away from siblings that never had one applied.
- `vfs/elect.go` — duplicate election. **There is no `scorer/` phase and no
  `file_metadata.is_master` column any more**: `electMasters` picks one copy
  per hash, by folder-naming heuristics, from the rows `loadMasters` already
  holds, every run. A hash with a placed member is dropped in SQL before the
  election ever sees it — that file *is* the master of its hash (spec
  D10/D11), which is what keeps a re-imported card from being copied a second
  time. Computing it beats persisting it: a persisted answer went stale
  between runs, and a re-scanned `Goa Trip` duplicate out-scored the placed
  copy, flipped its flag, and got proposed again (issue 06/07).
- `vfs/` — phase 4. **`Plan` is two halves**: `deriveAll`/`resolveLocations`,
  which are the only passes needing anything from outside (the metadata the
  scan stored, and geonames), and `shape`, which turns those derived facts
  into paths and is a pure function of them. The split exists so the pass
  order is written down once: `PreviewPaths` — the config wizard's examples —
  used to hand-run two of the six shape passes and *fake* a third's output
  (`Sample.DayOverride`, a literal `"02_04"` handed to the renderer), which
  made the wizard a second, shorter pipeline that disagreed with the real one
  about what a setting produces. It runs `shape` now, so an example is the
  real proposal by construction rather than by somebody keeping two lists in
  step. The drift was not hypothetical: the preview skipped `applyNameCase`,
  so it promised a `Canon-EOS-700D` folder the pipeline would never create
  (it title-cases to `Canon-Eos-700d` — that it mangles the name at all is a
  separate bug, see the ceiling note below). `Sample` is now exactly a master
  as it stands after the first half, and carries nothing `shape` works out
  for itself. The `skip` set (`uninformativeLevels`) is computed once in
  `shape` and passed down, rather than recomputed over every master by each
  of the three passes that need it; only device/orientation/media ever
  collapse and nothing in `shape` changes those, so the answer was the same
  all three times. **Known ceiling:** `caseName`/`titleWord` lower-case every
  character after the first of any word not in `caseWhitelist`, so an
  all-caps model name becomes `Eos`, `700d`. The whitelist is the only
  defence and it is per-word.
- `vfs/` — **`docs/vfs-pipeline.md` is the long-form walkthrough of
  this package** — every SQL query, all eight `Plan` passes in call order, the
  concurrency patterns, and an edge-case catalogue naming the bug behind each
  rule. Read it before changing anything here; the notes below are the map,
  that document is the territory.
  Proposes destinations for every live, not-yet-placed master in the library
  from the persisted metadata (never re-reads files); each run replaces every
  *unapproved* row and leaves an approved plan alone (safe to call again
  mid-review — see `cli/review.go`'s `rebuildTree`). **A placed file
  (`file_registry.placed = 1`) is never re-proposed**: `loadMasters` filters
  `placed = 0` outright and drops any hash group with a placed member (see
  `vfs/elect.go` above), and `persist` never deletes a placed file's row. Its row is the plan from
  here on; nothing here touches it. `persist` **flushes the
  writer before returning**: the
  writer is an async FIFO, and every caller reads the rows straight back
  (`rebuildTree` re-proposes then calls `BuildTree` immediately), so without it
  the review redrew the proposal this run had just replaced — a reported
  "rebuild doesn't rebuild" bug. **`vfs.Propose` is the phase as one call** — it builds its
  own `Config` via `ConfigFor` and resolves the saved-place anchors via
  `BuildAnchors` before running. Assembling those is part of the phase, not of
  its callers: `workflow`'s vfs phase and `cli/review.go`'s `rebuildTree` used
  to run
  the same four-line ritual (load the config file again, build anchors, copy
  `resolver.Anchors` onto the `Config`, `New(...).Run`) and either could drift
  from the other. `New` stays for a test, or a caller that wants to state the
  `Config` itself. **`Propose` first deletes the review draft**
  (`RemoveDraft`, spec D19/D20): a new proposal means new folder IDs, so a
  scan's vfs phase and a settings re-plan both throw away edits made against
  the old one. (A scan cancelled before its vfs phase leaves the draft, which
  is right — the proposal it was made against is still there.)
  **There is no config stamp** (`snapshot.go`, `.wandersort.cfg`,
  `ConfigStamp`/`WriteStamp`/`ReadStamp` — all gone with issue 10): the
  settings a proposal was built under are the library's own, nothing outside
  the app can edit them, and the one thing that can — the wizard — re-plans
  as it saves. A file recording them so a later run could notice they had
  moved was answering a question that can no longer be asked.
  `Config.SavedPlaces` stays: it is the typed names beside the resolved
  anchors, and `ConfigFor` copies it.
  `Config.Rules` (below Year/Month)
  is `location`/`orientation`/`device`/`media` in any order, or empty for a
  flat `Year/Month` — set via the library's `rules` setting (`wandersort
  config` wizard only, no CLI flag, no env var). `date` is a Day level, so the full
  `Year/Month/Day/Location/Device/Orientation/Media` shape is
  `rules: [date, location, device, orientation, media]`; when a `date` level is
  present the location ladder **skips its dated `eventSegment` rung** (falling
  through to device/fallback) — otherwise an unresolved location renders a
  second date right next to the Day folder (`…/03/03-05/`).
  **A collapsible level that resolves to one folder name library-wide is
  dropped** (`uninformativeLevels` + `collapsibleLevels`): `…/Goa/iPhone/
  Vertical/Photos/` is four folders deep to reach one folder when every file
  is a vertical iPhone photo. Only `device`/`orientation`/`media` collapse —
  `date` and `location` never do, since they're how a person recognizes a
  folder and the review TUI's merge is the deliberate way to fold days
  together. Measured **library-wide, not per-branch**: a level kept under one
  Day and dropped under the next would give the tree a different depth
  depending on where you stand, worse to navigate than one extra folder. It
  self-corrects — `loadMasters` is library-wide and each run replaces the
  proposal, so the first video a later scan finds brings `Photos` back and
  re-proposes the existing photos inside it. Answering the wizard's collapse
  question with "no" forces the full nesting; there is no flag and no env
  var for it. `vfs.ConfigFor` (which takes the whole `*config.Configuration`,
  so a new vfs-relevant setting doesn't churn its signature — and is therefore
  the one place `vfs` imports `pkg/config`, meaning `config` can never import
  `vfs`) is the single place the `none` sentinel
  is turned into a nil `Rules` — `workflow` and `cli/review.go`'s
  `rebuildTree` both go through it.
  The month segment is **number-first (`06_June`)**: a bare month
  name sorts alphabetically, which put `December` above `November` in the
  review tree and in every file browser. **The location ladder has no device
  or `Unsorted` rung**: resolved city → dated event segment (skipped when a
  `date` level already carries the date) → *nothing*. It used to fall back to
  the device name, which put a location folder named after the camera right
  next to the real device folder (`…/Canon EOS 700D/Canon EOS 700D/`) — wrong
  information, and duplicated. Unknown location now means the level is simply
  absent for that file, and `location_node_id` stays NULL — *unless* located
  siblings share its parent folder, in which case `markUnknownLocations`
  (`plan.go`) names it `Unknown` (`vfs.UnknownLocation`) so it stops sitting
  loose next to real location folders. Only then: a folder whose files are
  *all* unlocated gets a single `Unknown` child saying nothing the parent
  didn't. "Siblings" is `locationParent` — the segments `dirFor` emits above
  the location level, so any `Rules` order works. It runs **before**
  `mergeSameLocationDays`, which is the point: an `Unknown` is a location like
  any other from there on, so its days fold into ranges on the same terms as
  everyone else's instead of the GPS-less files being the only ones left
  un-merged. Known ceiling (marked `ponytail:` in the source): the sibling
  test is pre-merge, so a located sibling that the day-merge later lifts into
  a range folder leaves its `Unknown` behind alone in the single day.
  **One day, one date folder** — the invariant `mergeSameLocationDays` exists
  under, and the one it used to break. Runs are computed per
  `(year, month, location)`, but the *folder* is per day, so one location's run
  could pull half a day into a range and leave the rest behind as a sibling: a
  real 15k library had `01_02` next to `02`, and day 28 in **four** different
  date folders (`26_31`, `28`, `28_30`, `28_31`) — 37 torn days in all. The
  merge now labels, then checks that every file of a day agrees on that label
  (a file with no location votes "no range"); a day that disagrees is dropped
  from merging and **acts as a break**, which can settle the runs around it, so
  it repeats to a fixed point (each pass only adds a broken day, so it
  terminates). Cost: on a trip where each day holds several places with
  different runs, most days stay unmerged — that is what `[V]`/`[m]` in the
  review is for. Days are keyed by calendar date (`calendarDay`, plan.go), and
  runs by location over those days.
  **`SavedPlacesDateOnly`'s suppression is per day, not per file**
  (`unsuppressMixedSavedPlaces`, which runs *before* `markUnknownLocations` so
  the lifted city is what makes the neighbouring `Unknown` appear at all):
  dropping the city folder is right when the day is nothing but everyday shots
  and wrong the moment it holds anything else — the saved-place files sit loose
  while their neighbours are nested one level down, and the day reads as
  half-sorted. A day holding both gets `02/Indore` *and* `02/Goa` (or
  `02/Unknown`), never a bare pile next to a folder. The lift is a separate
  field (`keepLocationFolder`), not a mutation of `atSavedPlace`: where the
  file was taken is a fact, whether its folder shows is a decision.
  `resolveLocations` folds a directly-resolved GPS city
  into a *confirmed* `ANCHOR_HOME`/`ANCHOR_WORK` label when within
  `location.MaxDistSquared` (~50km) of it, so a metro's suburbs land in one
  folder instead of fragmenting by neighbourhood. A cluster with nothing
  located at all gets a dated event segment (`clusterAndSpill`,
  `pkg/core/vfs/cluster.go`) and no invented place name: an earlier version
  ranked a name for it out of confirmed labels, anchor cities and source
  folder names, which fabricated locations with no relationship to the
  cluster (a real reported bug — a GPS-less DSLR photo was "suggested"
  whatever city dominated the user's phone-photo library). That whole ladder
  is gone with the suggestion concept. **`clusterAndSpill` no longer spills
  either**: it used to hand a cluster's GPS-less members the majority city of
  its located ones. Same bug in a smaller radius — a 12h cluster is most of a
  day, so a DSLR shot nine hours after a phone photo inherited the phone's
  city, and since most GPS-less files sit near a saved place, in practice it
  named nearly all of them after the saved place (a real reported bug: `Unknown`
  never appeared anywhere in a 15k-file library because spillover had already
  eaten every candidate). The rule is now strict — no GPS, no place — and the
  reviewer merges an `Unknown` into its neighbour with `[V]`/`[m]` if they
  know better. `majorityCity` went with it. Only a cluster with *nothing*
  located still decides anything (the dated event segment); a mixed cluster
  decides nothing and assigns no `cluster_id`.
  `captureDirs` (`plan.go`) is the one exception to "every master derives its
  own directory": files in the same source dir sharing a `captureStem`
  (extension dropped, `IMG_E`/`IMG_O` folded to `IMG_`) are one capture split
  across extensions, and all take the leader's directory. Two rules earn their
  keep:
  - **Agreement is a window, not an instant** (`captureAgreementWindow`,
    5 min). It used to require the EXIF times to match to the second, which an
    iPhone edit breaks: `IMG_E0231.JPG` carried a `DateTimeOriginal` 13s after
    `IMG_0231.PNG`, so the group was discarded and `IMG_0231.AAE` stranded in a
    date folder while both screenshots went to Screenshots (a real reported
    bug). What the check actually defends against — a reused filename counter
    from a later shoot — is hours or days out, never minutes, so the window
    costs nothing. A genuine reuse (`IMG_1051.HEIC` on the 14th,
    `IMG_1051.JPG` on the 28th) still splits the group and leaves its sidecar
    behind; that is the correct answer, since nothing says which one it belongs
    to.
  - **Leader order is screenshot > non-sidecar > located > canonical name.**
    Screenshot first because `dirFor` short-circuits Rules for one, so a
    sidecar of a screenshot has to follow it into `Screenshots`; sidecar last
    because it carries no derived data of its own and would otherwise drag a
    whole group into whatever its file mtime implied.

  **Name collisions** (`buildTargets`, spec D25): the earlier capture keeps the
  plain name and later ones get `_2`, `_3` — ranked by capture time then hash
  of the file's *group leader* (`orderTime`/`orderHash`), then its own name,
  never by source path, so the same files get the same names from any folder
  layout. **A capture group takes one suffix** (`pairKey` + `assignSuffix`: the
  lowest number free for every member): Apple Photos pairs an `.AAE` with its
  photo by name, so a photo that collides while its edit doesn't must still
  drag the edit to `_2` with it. A Live Photo `.MOV` joins its photo's group
  when within `liveVideoWindow` (1 second — one shutter press; the edit group keeps 5 minutes) and not naming a different device (`pairLiveVideos`) — suffix only, never
  folder. Names compare as `nameKey` (NFC, lowercased — a map key, not
  `EqualFold`), and files already placed in the library (`Config.Placed`, from
  `placedPaths`) hold their names. `Confirm` re-resolves review collisions
  through the same `assignSuffix` and placed set, grouping by source dir +
  `captureStem` in row order (`ponytail:` there — no time window, not D25's
  capture-time order).

  Known gap: a sidecar whose only sibling is a video has no group at all —
  `captureDirs` skips videos so a Live Photo `.MOV` isn't forced across the
  Photos/Videos split.
  **A sidecar `captureDirs` can't pair — no group at all, or a group its own
  agreement checks rejected — goes to `vfs.OrphanDir` (`"orphan"`) instead of
  its own mtime fallback.** An `.AAE` carries no EXIF and is meaningless
  without the photo it edits — useful only for re-importing into Apple
  Photos, never for browsing — so an unpaired one is junk to be held
  somewhere, not filed next to real folders by whatever its file mtime
  happens to say (12 files in one real 15k library, previously scattered
  through the real hierarchy, often alone). The short-circuit is in
  `buildTargets`, the same shape `IsScreenshot`'s already is: check first,
  bypass `dirFor`/Rules entirely, one flat folder for the whole library. A
  *paired* sidecar is unaffected — it already took the leader's directory
  before this check ever runs. `OrphanDir` is excluded from `BuildTree`
  (`review.go`, the same `substr` prefix-compare `FilesUnder` uses, not
  `LIKE` — nothing to review about junk) and therefore never renamed,
  merged, or seen by a reviewer; `Confirm` never
  asks about it, and there is no sign-off state to sweep it into. It still flows through Execute
  like any other row, landing at `<output>/orphan/` on disk.
  **A file's folder date is a stored fact, not a folder name**
  (`masterFile.folderDate`, in-memory only — nothing persists it as its own
  column; `persist` only ever writes the *path* it produces): it is
  the *cluster's* start, written to every member by `clusterAndSpill` before
  either early-continue, so one event that runs over a month or New Year
  boundary lands in one Year/Month folder instead of being torn in two —
  **but only for a cluster spanning at most `maxFolderSpan` (24h)**. A cluster
  grows for as long as consecutive shots stay inside `ClusterGap`, so a holiday
  shot every few hours is one unbroken cluster running for a week; handing all
  of it the start month filed the Jan 05 photos under `12_December/Jan_05`, the
  same wrong-Year-tree failure the rule exists to prevent (a reported bug).
  Past that span every file keeps its own month and the day-merge folds the
  days into ranges as usual. The cap is a whole-cluster decision, never per
  member, so one day can never be split across two month folders.
  `captureDirs` copies the group leader's `folderTime()` onto every member for
  the same reason it copies `dirLevels`: a member takes the leader's
  *directory*, so its own folder date has to be the one that directory came
  from, or it surfaces in the review tree as one lone folder out of another
  year (reported — a sidecar has no EXIF time and falls back to a file mtime
  months away). Read it
  through `masterFile.folderTime()` (folderDate, falling back to takenAt
  before clustering has run) — `monthParts` (the Year/Month pair
  `dirFor` and `locationParent` share) goes through it, so nothing can disagree
  about which month a file is in. **`mergeSameLocationDays` numbers days by
  calendar date** (`calendarDay`), so a same-place run crossing a month or
  year end is one run, and **the whole run takes its first day's Year/Month**
  (spec D27: it sets every member's `folderDate`): Goa 28 Aug–4 Sep is
  `2024/08_August/Aug_28-Sep_04/Goa` (`dayRange` — `28_31` inside one month,
  `eventSegment`'s cross-month shape across one). A crossing day whose files
  disagree still breaks the run. **A file whose own month differs from its
  folder's month** (`crossesFolderMonth` — a run's September days, or a short
  cluster's Jan 01 under December) states its **full date** — year, month and
  day in one alternative (`fullDate`) — in its day folder's bounds and every
  folder's above it (the year folder only across a year end), so a later
  batch's Goa on 2 Sep matches the placed August trip and Goa on 28 Sep
  doesn't match August's plain `28`. Unmerged, such a file gets a
  month-qualified day folder (`Jan_01`) — a bare `01` under `12_December`
  reads as Dec 01 *and lands on top of the real Dec 01 files*, which was a
  reported bug (Jan 1 videos filed under `12_December/01/Banjar`).
  `BuildTree(ctx, db)` and `Confirm(ctx, db, roots)` always cover the whole
  library — there is no time-slice scoping any more (issue 19).
  There is no `ReopenPlan`: a plan row has no status (issue 20).
  **The plan is a persisted folder tree** (`folder_nodes`, spec D12,
  `folders.go`): `id`, `parent_id`, `name`, `level`. Every entry points at
  its folder (`virtual_fs_entries.node_id`); a folder's path is its
  ancestors' names joined, and `vfs.Node.ID` is the folder's row id, so a
  rename or move never changes it. `target_path` stays on the entry as
  folder path + file name — `execute` reads it — and `Confirm` rewrites it
  from the folders whenever an edit moves a file. `dirFor` records the level
  of every segment it emits (`masterFile.dirLevels`: `year`, `month`,
  `screenshots`, `fallback`, `orphan`, or a Rules name); `persist` stores it
  as `folder_nodes.level`. `persist` finds-or-creates each folder chain by
  (parent, name) (`folderIndex.ensure`), so re-planning an unchanged library
  gives every folder back its id. **A folder holding a placed file (or above
  one) is never reused by a new proposal** (`loadFolders`): a review rename
  of a shared folder would rename where the placed file is recorded, and
  placed files never move. A failed transfer's (`TRANSFER` error) folder counts as
  placed here (`placedFoldersCTE`): its row keeps the `target_path` it
  failed at, so a shared-folder rename would leave that path stale. A new
  file routed into a placed folder (below) gets a same-path twin chain
  instead — one folder on disk, two rows (`ponytail:` there: each transfer
  into a twin adds another placed row at that path; `route` takes the
  oldest).
  **`Confirm` applies the same rule at save time** (`splitPlacedFolders`,
  before the edits): a copy stopped partway leaves approved files beside or
  under copied ones, so every still-reviewable row whose folder chain touches
  a placed file's folder moves onto a same-path chain of its own, and the
  submitted tree's IDs are remapped onto it (`remapIDs`). Without it a review
  rename of a shared folder rewrote where the copied files are recorded while
  on disk they stayed put.
  **Every folder stores what it holds** (`folder_nodes.bounds`, `vfs.Bounds`,
  spec D13/D14): a JSON array of alternatives (`vfs.Constraint`, an AND of
  levels, each a set, days too: `{"date":[3,20]}` is the 3rd and the 20th,
  never between). A file fits a folder if it matches any alternative
  (`Bounds.Matches`); a folder's full range is its bounds AND its ancestors'.
  `[{}]` holds anything, `[]` nothing. Alternatives differing on one level
  fold into one (`Bounds.with`, exact), so an ordinary folder has one. The
  planner fills them (`boundsFor`, per `dirFor` segment, collected per folder
  in `persist`, which rewrites them every plan); the review edits transform
  them in `edit.go` (merge = alternatives side by side, each pick first
  intersected with the folders between it and the common parent and its
  subtree with its ancestors (`pushDown`) — "Canon on the 3rd or the 20th",
  never "any day"; drop = pairwise `Intersect` into each lifted child;
  flatten/rename unchanged) and `Confirm` stores the result as the tree
  carries it: the rules live once, and `apply` never re-derives them.
  **New files are placed through the placed tree** (`route.go`, spec D15):
  after `dirFor`, `buildTargets` asks `placedTree.route` for the deepest
  placed folder the file matches *completely* — walk from the root, descend
  into a child only if a complete match exists beneath it (first in id
  order). "Complete" means the chain's bounds cover every level the file's
  own planned path states (`statement`: each level's value from the folder
  that level made, so a cross-month `Jan_01` day's own year never overrides
  the year folder's). Screenshots/fallback/orphan folders bound nothing, so
  they match only a file whose rules put it in one (`specialLevels`). A hit
  replaces the path and points `dirLevels`/`dirBounds` at the placed chain
  (`dirBounds` is `[]Bounds` for that reason); a miss keeps the planned
  path, which already reaches every placed ancestor it matches by name —
  Delhi on 2 March next to placed `01_03/Goa Trip` lands in `02/Delhi`.
  With location turned off, a file states no place, so a placed city
  folder never matches it and it stays in the day (D6: new rules for new
  batches). The planner never touches placed files; `Run` loads the tree
  (`placedFoldersCTE`) and every placed file's capture time, which
  `clusterAndSpill` reads read-only (D16): placed times move a cluster's
  start and end, never join its members, so 00:30 on Jan 1 continuing a
  placed Dec 31 evening lands under `12_December`. The event segment is
  still the new members' own days.
  `location_node_id` is `ON DELETE SET NULL`: a merge can move a file out
  from under its old place folder, which the save then prunes, and the file
  just loses that GPS link — it isn't under that place any more. (Without it
  the prune failed on the foreign key: a reported bug.) Names typed in review
  go through `path.ToLibrary` (NFC) in `readTree`, like every planner name.
  **Folders are hard-deleted, not soft-deleted**: `pruneFolders` (end of
  `persist` and of `Confirm`) deletes every folder no entry sits in, directly
  or below. `AUTOINCREMENT` never hands an id out twice, so a stale id (a
  future draft file's) can only miss, never hit a different folder — which
  was the only thing a `deleted_at` would have bought. A placed file's folder
  holds an entry, so it is never pruned.
  `review.go` (issue #8's reconcile core, read by the CLI TUI) exposes the
  proposal as a directory tree the reviewer edits
  before `Confirm` writes it back: `BuildTree` also carries one exemplar
  GPS coordinate per location node (`Node.Lat/Lon`) for the TUI's expand-radius
  rename, and `FilesUnder` lists a node's source files for the preview-copy
  feature. **The GPS attaches to a folder, not a depth:** the segment
  `dirFor` marks with the `location` level is stored as
  `virtual_fs_entries.location_node_id`, and `BuildTree` hangs the coordinate
  off exactly that folder. The old fixed `suggestionDepth = 2` assumed
  location was Rules' first level, so any other order (`rules: [device,
  location]`, or a `date` level in front) hung it on whatever shared Device/Day
  node sat at depth 2. No location folder (no location level in this
  proposal) means no GPS-bearing node, rather than a wrong one. `captureDirs`
  copies the group leader's `dirLevels` onto every member: `buildTargets`
  short-circuits `dirFor` for a grouped file, so without that copy the file
  had no location folder and its folder silently lost GPS-radius renames
  (8185 of 15024 entries in one real library). `folder_nodes`, `node_id` and
  `location_node_id` are part of **003's `CREATE TABLE`**, not their own
  migration — the pre-tag rule (no tag
  yet, so no users) says edit the existing migration rather than stack an
  `ALTER` on it. `library_settings` was added to 003 the same way. The cost
  is that `migrations.Run` tracks versions
  individually: a database where 003 is already recorded will never get the
  new table or columns, and the run then fails at runtime on the first
  query. **Deleting `.wandersort.db` (or the whole
  library folder) is the fix**, and `wandersort reset` is not — the file
  itself has to go. Same applies to any future edit of an already-run
  migration. `file_metadata.hash_kind` was removed from **002's `CREATE TABLE`**
  and `file_hash` gained its `blake3:` prefix the same way: **every hash in an
  older database is in the old format** (bare hex, or a `size:<id>` stand-in),
  and nothing rewrites them. A file read before and after that change would
  look like two different files, so the same card photo never matches its
  placed copy and is proposed and copied again, and `cleanupPlacedDuplicates`
  never removes it. Delete `.wandersort.db` (or the whole library folder)
  before scanning again.
  **`Confirm` merges, it doesn't reject:** two sibling folders renamed to the
  same name become one folder — the later folds into the first (`readTree`)
  — (e.g. two unresolved date clusters turning out to be the same place);
  this used to be an error before a real user hit exactly that case.
  `Node.MergedIDs` is the other merge path: nodes the review TUI folded away
  are absent from the submitted tree entirely, so `treeEdits.apply` moves
  their still-reviewable files and all their subfolders onto the survivor,
  then gives every folder in the tree the parent and name the reviewer left
  it with (folds first, so a subfolder the tree places explicitly lands where
  the tree says). Because a file's path is its folder's ancestors, a rename
  or merge on a Year folder carries onto every row nested under it with no
  path rewriting at all.
- `execute/` — the phase `vfs.go`'s package doc used to call "future work":
  the one thing in this codebase that writes the user's media files. Reads
  every pending row of `virtual_fs_entries` (`db.PendingTransfer`: file not
  placed, no `TRANSFER` error), places `source_path` at `outputDir/target_path`,
  and records the outcome: success sets `file_registry.placed` and deletes the
  file's `errors` rows, failure writes a `TRANSFER` row (op `stat`/`mkdir`/
  `copy`/`rename`/`hash`/`remove-source`, from `stepError`) through
  `db.RecordError` — so "which files failed and why" is a query. There is no
  row status any more. **`Run` owns the spec-D18 order** (`prepare`): first
  `checkFits` (`volume.TransferNeeds`: `Pending`'s bytes, twice the database
  — its page count, which the backup's `VACUUM INTO` never exceeds; the
  plain and compressed copies coexist briefly — and a reserve of 1 GiB or 1%
  of the volume, whichever is more, against `volume.Space` via the `spaceOf`
  test seam; a refusal is `*NotEnoughSpaceError` and changes nothing,
  `ponytail:` it counts a same-volume move's files too), then
  `vfs.ApplyDraft`, then `Options.OnApplied`, then the backup and the
  transfers. **A dry run reports the edited paths**: `vfs.PreviewDraft`
  applies the draft inside `BulkWriter.DryRun`, a transaction that is always
  rolled back, and reads the pending rows there — it used to skip the draft
  and report paths the real run would not use. A draft with no edits skips
  `Confirm` entirely (nothing to apply). `Run(ctx, db, log, outputDir, Options{Mode, DryRun, OnApplied})` is the whole
  surface; `Mode` is `Copy` (the zero value — ship the safe default) or
  `Move`. **Deliberately sequential**, per
  `.tickets/apply-phase-unmeasured.md`: nothing has ever measured this
  phase's throughput, so there is nothing yet to size a worker pool against
  — don't add one speculatively. **Resumable by construction, not by an
  explicit state machine**: it only ever selects pending rows, so a run
  that stops partway (crash, ctrl+c, a bad file) leaves every untouched row
  exactly where the next run picks it up; a file that failed keeps its
  `TRANSFER` row (and the folder it was planned into) and is not retried
  automatically. The seam is one function, not an
  `FS` interface (that shape was considered and rejected — a large interface
  learned to vary one behaviour is a shallow adapter): `transfer(ctx, mode,
  src, dst, want, commit) (string, error)` places one file, atomically, and
  returns where it landed. **`commit` is the ordering, not a callback for
  tidiness**: it runs once the file is at its landing path and verified, and
  *before* a move unlinks the source, so the database records the file as
  being in the library before its only other copy is destroyed. A crash
  between the two leaves a duplicate; the other order left the library
  holding a file nothing knew about and no source to re-read it from. A
  `commit` error **undoes the transfer** — a copy is removed (the name was
  free until it took it), a same-device move is renamed back — and the row
  is counted failed: a failed row is not pending, so a file left in the
  library behind it would never be reconciled. **A same-device move commits
  inside its rename** (`atomicfile.RenameCommit`): after the new link lands
  and its folder is synced, before the old name is unlinked. It used to
  commit after the rename, on the theory that `alreadyLanded` recovers the
  gap — but only `execute` runs `alreadyLanded`, and a scan before it swept
  the vanished source's row, leaving a library file nothing tracks. The
  commit is a callback, so `pkg/atomicfile` still imports nothing. Where hard
  links don't exist (exFAT, some network mounts) the move is one rename and
  the commit can only follow it (`ponytail:` there). **A taken destination is
  seen before a byte is copied** (`os.Lstat` in `productionTransfer`): a copy
  only learns it at the final link, so each collision used to cost the whole
  file again. **New folders go through `atomicfile.MkdirAll`**, which syncs
  each created folder into its parent — on exFAT/FAT (no journal) a power cut
  could otherwise drop the folder, and the file in it, after a move had
  deleted the source.
  Two real implementations —
  `productionTransfer` (a same-device `atomicfile.Rename` for `Move`, else
  `atomicfile.Copy`; `Copy` never unlinks `src`, `Move` only does once the
  copy is verified. **Every copy is hash-verified** (spec D22): the bytes
  stream through `metadata.NewHasher` as they are written, and the temp file
  is linked into place only if `metadata.HashString` matches the stored
  `file_hash` (`want`) — a mismatch (source changed since the scan) is a
  `TRANSFER` error (`checksum-mismatch`) naming both hashes, nothing at the destination, source kept.
  A same-device rename copies no bytes, so `Run` only allows it for a
  source whose size and date still match the scan; one that changed goes
  through the hash-checked copy instead (`moveCopying`) and a mismatch is
  refused with the source kept. **Never
  overwrites** (spec D21):
  both fail with `fs.ErrExist` on an occupied destination, and that error
  moves on to `name_1.ext`, `name_2.ext`… — **unless the taken name already
  holds this file** (`holds`: same size, then `metadata.HashFile` equals
  `want`), which is where it lands: a crash between an earlier copy landing
  and its row committing, or `admin db --restore` putting placed rows back to
  pending, would
  otherwise place a second copy at `_1`. A move there hashes the **source**
  too before deleting it — the library copy matching the scan says nothing
  about a source edited since (same size), and deleting that edit was a bug
  caught in review. A move whose file landed verified but
  whose source can't be removed (`errSourceNotRemoved`) is recorded placed
  with a warning, not as an error — the library holds the file, so the database
  must too. `markPlaced` writes the
  landed name back to `target_path`, and to `source_path` and
  `file_registry.file_dir`/`file_name` too — **library-relative, the same
  value as `target_path`** (spec D9/D10: the database travels with the
  library, so a placed file's path must not depend on where it's mounted).
  `markPlaced` also sets `file_registry.placed = 1` on success, copy and
  move alike; `markFailed` never touches it, so a failed transfer
  stays `placed = 0`. **Both are `WriteSync`, not the fire-and-forget
  `Write` every other phase uses, and both return their error** — this is the
  one row whose absence the user pays for in photos. `WriteSync` runs in a
  transaction of its own, never inside a batch: in a batch it once reported
  nil for an op a later op's failure had rolled back. Batched writes drop a
  failed batch into a log line (`writer.go`'s `flush` has nowhere to return
  one), so the run could report `Done: 4812 files` with every `placed = 1`
  rolled back, and in move mode the sources were already gone. `Report.Done`
  now counts rows that committed, not transfers that touched the disk) and
  `dryRunTransfer` (touches no file — `Run` already `os.Stat`s the source
  before calling `transfer`, so a dry run's `Report` is real byte/file counts
  for zero I/O; it still calls `commit`, which writes nothing on a dry run
  and is what counts the row). **A missing source is not automatically a
  failure** (`alreadyLanded`): a crash after the file landed but before its
  row committed leaves exactly that, and for a move the original is already
  gone. Before writing the row off, the planned name and the `_N` names
  beside it are checked for a file whose bytes hash to what the scan
  recorded; a hit is recorded placed. Without it, every file the last run had
  in flight became a permanent `TRANSFER` error that is never retried, on
  files sitting correctly in the library the whole time. A missing source
  with nothing in the library is still a plain failure. Reports
  through the same contract every other phase does
  (`logger.PhaseKey`/`EventKey`/`ElapsedKey`, `UserKey` line with the byte
  total from `volume.HumanBytes`), per that same ticket's ask for phase
  timing and bytes-not-just-files. Before any transfer (not on a dry run)
  it writes `.wandersort.db.zst` via `db.Backup` (spec D24; a failed backup
  stops the run). `VACUUM INTO` a plain `.db.tmp`, verified (`application_id`,
  `quick_check` — a compressed file can't be checked, so verify first), then
  zstd-compressed into `.zst.tmp` and renamed over the old backup, so a
  failed backup never costs the previous one. **The backup never has the
  live database's bytes (zstd magic vs the SQLite header), and in practice
  not its size,** because it is compressed — there is no
  stamp table and no padding any more (they existed only to keep a
  duplicate finder from pairing the two). **At the end of every run (not on a dry run), `cleanupPlacedDuplicates`
  hard-deletes every other `file_registry`/`file_metadata` row sharing a
  placed file's hash** — the duplicates the election passed over, and a copy
  of an already-placed file a later scan saw again. It reads
  `file_registry.placed = 1` fresh every run (so a run that stops early is
  picked up by the next one), collects the target ids in one query up front,
  and deletes the `file_registry` rows alone — a fixed id list; metadata, plan and
  error rows go by `ON DELETE CASCADE`, which is what orders it now. A file
  with a `READ` error has no metadata row, so it is never a target: the
  election never proposes a second copy of a placed hash (see `vfs/elect.go`
  above). **The caller holds the
  output lock**
  (`lock.AcquireOutput`), same contract `add` has — `execute.Run` assumes
  it, it does not take it.
  **The cleanup only forgets a duplicate while the file it duplicates is
  actually there** — it stats the placed file and compares its size first.
  Forgetting is irreversible and the database's knowledge of the surviving
  copies is the only record of them, so a placed file that lost its bytes
  (a crash, a bad sector, anything outside WanderSort) must not take them
  with it. Existence and size, not a hash: this runs after every transfer,
  and re-reading the whole library each time is what `check --full` is for.
- `verify/` — the phase nothing else does: **is what the database recorded
  still true on disk?** Every placed file's hash is stored at scan time and
  checked once, while the copy is written; after that the bytes are never
  read again, so bitrot, a bad sector, a sync client rewriting a file or a
  backup tool truncating one are all invisible. `Run(ctx, db, log, outputDir,
  Options{Full, OnProgress}) (Report, error)` is the whole surface. Quick
  (the default) stats every placed file for existence and size — a deletion
  or a truncation for the cost of one stat, the two failures a user is most
  likely to have caused themselves; `Full` re-reads the bytes and compares
  them with `file_metadata.file_hash`, which is the only way to see a changed
  byte and the whole reason the hash is stored. It also runs
  `PRAGMA integrity_check` on the **live** database (only the backup is ever
  checked otherwise, as it is written, so a corrupt page is otherwise found
  by whichever query happens to touch it — `integrity_check`, not
  `quick_check`, because the skipped index-against-table comparison is
  exactly what catches a plan pointing at folders that aren't there), and
  walks the library for leftover `.copy-*` files: full-size copies of the
  user's photos that a crashed transfer left in their own folders, which
  nothing else in WanderSort ever collects. **Reported, never deleted** —
  removing files is `execute`'s job, and a verify that deletes is one nobody
  runs twice. Only `placed = 1` rows: an unplaced file still lives at its
  source, where the user may legitimately have changed it, and the plan is a
  proposal about it rather than a record of it. **A placed file that is gone
  is forgotten, not kept as a problem** (`Report.Forgotten`, `forget`): after
  a backup, its registry row is deleted and its hash, plan and error rows go
  by cascade. Otherwise its hash stayed "placed", so a copy still at a source
  was never planned again, and every check listed it again (reported: files
  deleted from the library, then `add` proposed 0 and `execute` said
  nothing to transfer). A file that is there but wrong keeps its rows —
  damage to look at, not a deletion to accept. Failures go into the
  `errors` table as `VERIFY` rows and a file that verifies has its row
  deleted, so the table keeps holding only live problems (D29) and
  `wandersort admin report` ships exactly the failures still true. Sequential,
  like `execute` and for the same reason: nothing has measured it, so there is
  nothing to size a pool against. **Results go through the batched writer, not
  `WriteSync`**: with `synchronous=FULL` a synced transaction per file flushed
  the drive's cache once per placed file — most of a 100k-file check on a hard
  disk — and a lost result only costs a re-check. `Run` flushes before it
  reports.

## Supporting packages (`pkg/`)

- `tui/` — the full-screen TUI kit: adaptive palette + semantic styles
  (`theme.go`), the Docker-buildkit-style `StageList` step stack shared by
  scan and its dependency install (`stagelist.go` — stage rows with right-aligned
  elapsed times, a progress bar and a live per-file tail nested under the
  running stage), **the seam the container hosts screens through**
  (`tab.go` — `Tab` is `tea.Model` plus `Busy()`, the one fact about a screen
  the container cannot work out for itself: work in flight that must not be
  interrupted, replaced, or have its settings retargeted under it. There was
  no interface at all before: the container knew each screen by its concrete
  type and reached past `tea.Model` in **28 places** for five different method
  sets, so adding a screen meant adding an assertion to every one of them; it
  is 3 now, all of them one implementation's own post-mortem facts
  (`Failed`/`Cancelled`/`DepsFailure`/`Summary`), which is where they belong.
  **`Leave` is the one way out**, and screens no longer call `tea.Quit`: the
  container owns the program, and a screen that ends it takes the other tabs
  with it — a scan still running underneath included. It replaced nine
  hand-back paths: two screens quitting directly (six sites), one reporting
  through a `Done()` the container polled after every keystroke, one handing
  back a nil `SwitchMsg`, and a `quitReq` flag on the container to reinterpret
  the last two as a quit. A screen says `Leave{Quit}` — "done with the app"
  versus "done here" — and what that costs is the container's decision),
  the `SwitchMsg`/`Switch` pair, now strictly a hand*over* of a screen built
  for another tab (the scan passing over its prefetched review; a nil `Next`
  means nothing and is ignored), the `config` wizard
  (`form.go` — `Field.Example` blocks above the footer, `Field.Describe` for a
    description that depends on the answer under the cursor — prose belongs
    there, not in the example, which renders in a narrow column and truncates;
    descriptions word-wrap to the body width (`descriptionBlock`), so hard line
    breaks in them are re-flowed — `Field.Await` to hold a
  step on a background download, `DownloadMsg` for the progress row, and
  **numbered** option lists: `1)`/`2)` next to every choice, since an
  arrow-only list gives the eye nothing to aim at. A `FieldGroup` holds fields
  of *any* kind, which is what makes the Saved places step one screen with two
  inputs and two yes/no questions; every quit point goes through `finish()`,
  which hands back a `tui.Leave` carrying why it ended — aborted, an error, or
  a finished save — so the container reads one message instead of polling
  `Done()` and then three more getters. There is no `Embedded` flag any more:
  every form is hosted, since the `config` subcommand is a shell tab too, and
  a flag with one setter and one reader was describing a path that no longer
  exists), the shell's landing screen
  (`home.go` — `HomeModel`: the scan-folder list, **one path per enter**, which
  is what keeps folders with spaces working with no quoting or comma-escaping.
  Folders are held expanded (the scan needs real paths) and rendered back
  through `path.RelativeToHome`, the way they were typed and the way every
  completion offers them. `↑` pulls the most recently added folder straight
  back into the input to edit — one key, not select-then-enter — but only
  once the completion dropdown is out of the way, since `↑` is the dropdown's
  key first; `ctrl+x` drops the last folder outright. There is no
  select-cursor state at all: editing removes the folder from the list
  immediately, so there's nothing left to have a cursor on.
  Force re-scan (`ctrl+g`) asks first, and that ask takes only `[enter]`
  (confirm) and `[esc]` (cancel) — a decision that re-reads every file from
  disk gets exactly two keys, no `y`/`n`, no arrows (`ConfirmModel.Keys`
  overrides the modal's default `y`/`n` footer for this one). Ctrl+t already
  reaches the review tab, so there is no separate review key here.
  Shell-style directory completion through the injected `HomeConfig.Suggest`
  (`cli.suggestDirs`, shared with the wizard's output-path field), `StartScanMsg`
  on an empty enter, and `HomeErrMsg` so a held
  output lock renders on the screen instead of taking the app down. Every
  command is ctrl-chorded because the input is always focused, so letters are
  ordinary text; completions are refreshed synchronously — they read the local
  filesystem, so the wizard's debounce would buy nothing), and shared chrome
  (`Banner`/`Footer`/`KeyHint`/`Screen`).
  The scan screen switches straight into the prefetched review the moment
  it's ready — a scan is run in order to review it, and the session continues
  afterwards either way, so there is no "continue?" prompt in the way.
  Mid-scan `ctrl+c` is warn-once-then-act: the first press cancels and
  says what that costs, a second gives up on a pipeline that won't unwind.
  **`ctrl+c` is the one quit key on every screen.**
  `ConfirmModel` quits its own program on an answer, which is right for
  `reset`; a screen that wants the question *inside* itself — the config
  wizard's `[esc]` Save/Discard ask — drives its own keys and uses `ConfirmModel` for
  the layout only, built per frame (a bubbletea model copied by value can't
  safely hold a pointer into its own fields, which is what its `Value` is).
  Design rules live in `pkg/tui/README.md` — new screens compose from this
  kit, never invent colours/markers. The pipeline feeds it through the logger
  only (`pkg/logger/stream.go`: `StreamKey` per-file lines — logged at **Info**,
  not Debug: the TUI handler level-filters at the configured level (`info`), so
  a Debug stream line would never reach the feed/progress-bar at all;
  `console.go` strips StreamKey lines so the plain console never sees them,
  `PhaseKey`/`EventKey`/`ElapsedKey` stage routing); plain mode (`--plain`,
  non-TTY stderr — `tuiEnabled()`) keeps the line console, styled
  with the same theme.
- `config/` — three files. `config.go`: `New()` (this machine's paths plus
  the settings defaults, output folder = the most recently used library),
  `SetOutput`/`OutputDir`, and `CheckLibrary` (may this folder be a library —
  empty, absent, or already holding `.wandersort.db`). `settings.go`:
  `Settings` (rules, the three toggles, saved places), `DefaultSettings`,
  `Equal` (the "did this save change anything" test), and
  `LoadSettings`/`SaveSettings` over the library's own `library_settings`
  row. `history.go`: `History`/`Remember`, the recently-used libraries.
  `Configuration` embeds `Settings`, so `a.Config.Rules` reads the open
  library's rules and `vfs.ConfigFor` needs no change. **No YAML, no env
  reads, no flag layering, no CLI framework** — the precedence chain,
  `Overrides`/`TriBool` and the whole `config.yaml` machinery went with issue
  10.
- `report/` — what `wandersort admin report` ships from the `errors` table
  (`report.Errors`): every row as named fields (stage, op, kind, attempts,
  the file's media type/extension/size/volume class, `detail` nested) with
  paths replaced — the home directory becomes the literal `$HOME` by exact,
  segment-bounded replacement (no guessing, `ScrubHome` for the logs);
  `--redact-paths` swaps every path for `<source>/<name>/<target>/<library>/<path>`.
  An unrecorded path runs to a quote, newline or Go's `op path: reason`
  colon, **spaces included** — stopping at a space leaked the personal part
  of `Goa Trip 2024`. Also a grouped summary
  (`31 x READ/open/permission-denied at metadata.go:412, .HEIC, removable`)
  for `about.txt`. `issue` opens the database read-only on its own, never
  through `openLibrary`.
- `db/` — sqlite (`modernc.org/sqlite`) open/migrate/retry. `db.New(ctx,
  path, log)` opens the library database (migrations, writer; `ctx` bounds
  the pre-upgrade backup); `db.OpenLocation(path, log)` opens the read-only
  geonames file — two functions, not one switching on a type. **Every
  table is `STRICT`**: a value of the wrong type fails at the write that
  sent it, not later as a wrong sum or a timestamp sorting out of place.
  The schema, not the code's ordering, holds the row-count invariants:
  one `file_metadata` row per file (`file_id UNIQUE`), one plan row per file,
  and `user_labels` as a set (`UNIQUE (label, kind)`). **Every
  connection-scoped pragma rides in the DSN** (`appDSN`), not in an `Exec`
  after opening: `foreign_keys`, `locking_mode`, `busy_timeout`, `cache_size`,
  `temp_store`, `mmap_size`, `synchronous` are per-*connection* settings in
  SQLite, and they used to be set through a pool that was only pinned to one
  connection afterwards — so a connection opened in that window, or opened
  later to replace a retired one, ran with **foreign keys off**, silently
  turning every `ON DELETE CASCADE` this codebase relies on (the scanner's
  sweep, execute's duplicate cleanup, `ResetAll`) into an orphan-row
  generator. The pool is pinned before the first query now, and
  `assertPragmas` reads `foreign_keys` and `synchronous` back and refuses the
  library if they didn't take. The driver applies `_pragma` entries in
  *lexicographic* order, so nothing in `appDSN` may depend on running before
  anything else in it; what is left in an `Exec` (`page_size`,
  `journal_mode`, `auto_vacuum`, `application_id`) is database-scoped and
  lives in the file's own header. **`synchronous` is `FULL`, not `NORMAL`**:
  under `NORMAL`, WAL mode does not fsync at commit, so a power loss drops an
  unbounded tail of *committed* transactions — including the rows saying a
  photo is in the library and its source may be deleted. Writes are batched
  (see `writer.go`), so the cost is a handful of fsyncs a second rather than
  one per row; `fullfsync` is set too, which is the only thing that flushes
  the drive's own cache on darwin. **`mmap_size` is 0**: the library usually
  lives on an external or network drive, and when it drops mid-read a mapped
  page faults with SIGBUS and kills the process (mid-execute included), where
  `read()` returns an error SQLite handles. **Opening a library with pending
  migrations backs it up first** (`PreMigrationBackupFileName`,
  `.wandersort.db.pre-upgrade.zst`, apart from the regular backup so the next
  execute doesn't overwrite it), and **a database recording a migration
  version this build doesn't know is refused** (`migrations.ErrNewerSchema`):
  an older build would otherwise run against a schema it was never written
  for. The backup file (`Backup`) is `Sync`ed and its folder synced after the
  rename — without both, a power cut could put an empty file under the
  backup's name. `errors.go`:
  the `errors` table's writer, `RecordError(ctx, tx, fileID, stage, op, err)`
  — one row per (file, stage), replaced with `attempts` bumped; derives `kind`
  from the error, stores `detail` as JSON (message, unwrapped chain, frames,
  errno). Three stages can fail one file — `READ`, `TRANSFER` and `VERIFY`
  (planning is library-wide, so it fails the run, not a file). Go errors carry no stack, so frames are taken where the pipeline
  sees the failure (`db.WithStack`, before the write is queued on the writer's
  goroutine; `db.PanicError` for a recovered panic's real stack) — they name a
  code path, no more. `db.PendingTransfer(col)` is the one SQL definition of
  "not placed and no `TRANSFER` error". There are no `db.Status*` file states.
  **The `errors` table is part of 001's `CREATE TABLE`**, beside the registry
  it points at, not its own migration — the same pre-tag rule the vfs notes
  above spell out, and the same cost, except harsher: 001 also lost
  `scan_status`, so a database that already recorded it has **no `errors`
  table at all** and fails on the first query any phase runs, not just on one
  column. 002's `file_metadata.file_id` became `NOT NULL … ON DELETE CASCADE`
  and 003 lost `virtual_fs_entries.status`/`error` the same way. **Delete
  `.wandersort.db` (or the whole library folder)** — `wandersort reset` does
  not do it, the file itself has to go. The app DB runs
  `locking_mode=EXCLUSIVE` on its one pooled connection, so while wandersort
  has a library open every other client (sqlite3 CLI, DB browsers) gets
  "database is locked" — reads included. Nothing in-process may open a second
  connection to the live file; `issue --include-db` copies raw bytes, so it is
  unaffected. `backup.go`: `Backup`/`Restore` (see `execute/` and
  `cli/admin_db.go --restore`); `writer.go` batched
  writes — **no deadline** (a 5s one used to abort slow batches and doom
  their fallback with the same dead context), a failed batch replays op by
  op so an op must touch nothing outside its tx, and `WriteSync` bypasses the
  batch entirely; `reset.go` `DB.ResetAll` (the FK-safe factory wipe behind
  `wandersort admin db --reset` — it lives here, not in the CLI layer, because it is a
  database operation); `migrations/` numbered
  Go migrations; `dbtest/` shared test fixtures
  (fresh migrated DB + seed helpers) used by every pipeline package's tests.
  All stored timestamps are UTC fixed-width nanoseconds via `db.FormatTime`
  (`db.TimeLayout`); convert to the user's local zone only at display time.
- `volume/` — best-effort volume-UUID resolution per scan root (diskutil on
  darwin, /dev/disk/by-uuid on linux, volume GUID via winapi on windows —
  cross-compiled only, untested on real hardware), cached per path; also
  `Space`/`FreeBytes` for the post-scan output-volume space preflight
  (warn-only, `CheckOutputSpace`) and `TransferNeeds` for execute's hard
  check. Also `Class`/`ClassForPath` (`class.go`) — how a
  volume behaves under concurrent reads (`ClassRotational`/`SolidState`/
  `Removable`/`Network`/`Unknown`), read by `pkg/core/metadata` to size its
  read budget. Same best-effort contract as `ForPath`: **`ClassUnknown` is a
  first-class answer, not a failure**, and the consumer maps it to a
  conservative default. Deliberately a package-level function rather than a
  `Resolver` method — it is called once per volume per run, so the second
  `diskutil` spawn is not worth a cache rewrite; the consumer caches by volume
  UUID instead. darwin reads `SolidState`/`BusProtocol`/`RAIDMaster` out of the
  plist `uuidForPath` already fetches (`classFromDiskutil`, pure); linux reads
  `/sys/block/<disk>/queue/rotational` and `removable`, falling back from a
  partition name to its whole disk by *asking sysfs*, not by pattern-matching
  (`/dev/loop0` and `/dev/sda1` look alike to a stripping rule and are not);
  windows returns `ClassUnknown` — the `IOCTL_STORAGE_QUERY_PROPERTY` structs
  are not in `x/sys` and CI is ubuntu-only, so there is no machine to test it
  on. Detection is the initial guess, not the answer: a RAID can be mixed
  (treated as rotational, the safe read), a NAS says nothing about its backing
  store, and `rotational=0` cannot separate an NVMe from a USB 2.0 stick
  everywhere.
- `logger/` — slog-based `Logger` interface; fans out to two handlers. The
  **console** handler (`console.go`) is deliberately minimal for CLI users: a
  coloured level tag + message + dimmed `key=value` attrs, no timestamp/source.
  It shows **only user-facing lines and warnings/errors** — tag a milestone with
  `logger.UserKey` (`log.Info("Scanning…", logger.UserKey, true)`); everything
  untagged is developer detail that goes to the file only. `PhaseKey`/
  `EventKey`/`ElapsedKey` are stripped from console lines (`consoleHiddenKeys`
  in `console.go`) — they exist for the TUI's phase routing, not for a human
  reading the plain console. There is no debug flag to bypass the console
  filter — the JSON file log always has every record.
  The **JSON file** handler keeps timestamp + source (`AddSource`) and every
  attr — that's what `issue` ships. **One file per process, in
  `~/.wandersort/logs/`, never in the library, and only for runs worth
  keeping** (`file.go`). A `File` buffers in memory until `Persist`, which
  creates `<UTC start time>Z_<pid>.log`, flushes, and prunes to the newest
  `keepLogs` = 20 (`Recent` lists them newest first). `openLibrary` persists
  (the run touches user data), `fileHandler`'s `persistOnWarn` persists on the
  first Warn/Error (a launch that went wrong), and so does a buffer past
  `maxBuffered` (1 MiB). Anything else — someone looking around the app — is
  dropped at exit, so it neither leaves a file nor evicts a scan's log from
  the 20. Cost: a crash before any of those loses the buffer. A log is about
  a run, not about the output folder, so a mid-session output change moves
  nothing. `PersistentPreRunE` builds the one `File` (`app.logFile`) and the
  shell's `NewTUI` is handed the same one. The first line of every log names
  the command and output folder, since the log no longer sits
  next to it. Never stdlib `log`.
- `location/` — offline reverse-geocode resolver over an already-open, already-
  verified sqlite DB. **This package has no idea where that DB came from,
  what version it needs to be, or what its checksum should be** — downloading,
  versioning, and verifying it is entirely `pkg/install`'s job
  (`install.OpenLocationResolver`; see below). `NewResolver` just wraps an
  opened `*db.DB` — a query-only constructor, not a setup path. `Lookup`
  (single best match, cached on a ~1.1km grid — the *cache key* is rounded, the
  query itself runs on the real coordinates; there is **no** singleflight, so a
  cold cache lets concurrent `resolveLocations` workers duplicate a query)
  and `Candidates` (ranked list for
  the review TUI's rename picker) share one query and one rule: a plain-spelled
  geonames entry ("Banjar") always ranks ahead of a diacritic one ("Banjār")
  at roughly the same distance, via `stripDiacritics`, not just whichever the
  distance sort happened to return. `MaxDistSquared` (exported, reused by
  `vfs.resolveLocations` for anchor-folding — don't redefine it locally) is the
  ~50km acceptance radius, matching the outer bounding box `queryNearest`
  expands to — the two used to disagree, which silently dropped a valid match
  15-40km out instead of using it, fragmenting locations.

  **A place has two names, and which one you use depends on who reads it.**
  `Candidate`/`PlaceMatch` carry both:

  - `FullName` (`fullName`) — city, state and country spelled out,
    `Indore, Madhya Pradesh, India`. **Every list a person browses shows
    this**: the `config` town picker and the review rename dropdown's
    `label` (`Suggestion.Label`, from this package's own `Suggest`). Six
    rows reading `Springfield` are not a choice, and the state/country are the
    only thing that tells them apart while scrolling the list. `SearchByName`
    also **dedupes on it**: the geonames database holds two
    `Banjar, West Java, Indonesia` a few hundred metres apart, and listing the
    same string twice is no more pickable than listing `Banjar` twice.
  - `DisplayName` (`disambiguate`) — the *smallest qualifier that makes this
    entry unique*: unqualified unless the name genuinely collides. `Lookup`
    writes it straight into a folder path (through `path.SanitizeSegment`);
    the rename dropdown never writes it raw (see `FolderName`).
  - `FolderName` — `DisplayName` run through `path.SanitizeSegment`, computed
    once in `fillNames` alongside the other two names. **This is the whole
    point of the split**: this package already knows which qualifier a name
    needs, so it also owns turning that into something safe to write as a
    directory name — a caller (`Suggestion.Value`) just takes it, no local
    sanitizing call of its own. The sanitizing rule itself is `pkg/path`'s and
    is *imported*, not copied: this package used to carry a byte-for-byte fork
    of it to stay dependency-free, defending against a cycle that cannot exist
    (`pkg/path` imports nothing in this project), at the cost of two copies
    that had to be edited together or folder names quietly diverged. A real
    reported bug motivated the name split itself (not the duplication) though:
    the rename dropdown used to
    sanitize `FullName` straight into the folder value, so a `Bhopal` with
    exactly one geonames row still autocompleted to
    `Bhopal-Madhya-Pradesh-India` on disk, even though the *list* correctly
    needed no qualifier to tell it apart from anything. `label` (`FullName`)
    and `value` (`FolderName`) come from different fields on purpose:
    browsing a list of candidates and deciding what a unique city's folder
    should be named are different questions, and conflating them either broke
    the picker (bare `DisplayName` everywhere loses context when several real
    matches share a name) or broke the folder (`FullName` everywhere
    over-qualifies a name nothing collides with).

  The ladder behind `DisplayName` is computed from three correlated counts
  (`nameCountsSQL`: rows with this name, distinct countries, rows with this
  name in *this* row's country):

  - unique name → `Shimla`
  - repeats inside this country → `Springfield, Illinois` (**state**, admin1 —
    not `region`, which is admin2 and means nothing to a reader)
  - only repeats abroad → `Hyderabad, India`

  The in-country case takes the state even when the name also occurs abroad —
  no other Springfield in Illinois exists to collide with. Two rows for the
  same name in the same state stay identical; a third qualifier would lengthen
  every folder to fix a near-duplicate in the geonames database. Cost to know about:
  globally-repeated famous names get a qualifier too (`Paris, France`,
  `San Jose, United States`). A population tiebreak would fix that; it isn't
  worth the rule until someone complains.

  Because a saved anchor is now `Hyderabad, Telangana, India`, **name→coordinate
  lookup has to honour everything after the city**: `ResolveByName` (and its
  stripped fallback) splits the string with `splitQualified` and keeps the row
  whose state/country account for *every* qualifier (`matchesQualifiers`), or
  the Indian home town resolves to the Pakistani city. All three forms resolve,
  so anchors saved before this still work — no qualifiers matches any row.
  `SearchByName` takes the same treatment, since the wizard re-searches the
  full name it saved itself, and `cli.exactMatch` saves the full form for the
  same reason. The anchor *fold* in `vfs.resolveLocations` is coordinate-based,
  so none of this affects it.
- `classifier/` — extension-based media type detection (`classifier.go`) and
  `ParseMetadata` (`models.go`), which decodes exiftool JSON into a generic map
  and reads only the `CommonMetadata` keys it needs. **Tolerant by design:** a
  type mismatch on any single exiftool tag no longer fails the whole decode
  (this replaced 11 giant strict per-format structs). No per-format files.
  `ShouldIgnoreDir` also skips **other photo apps' library bundles** by
  suffix, case-insensitively (`libraryBundleSuffixes`: `.photoslibrary`,
  `.photolibrary`, `.aplibrary`, `.lrdata`, …): their thumbnails look like
  media, and moving their originals out breaks that app's library.
- `exiftool/` — `Extractor`: runs an already-installed exiftool binary
  (`-json -n`) and parses its output via `classifier.ParseMetadata`. That's
  the whole package — no version check, no download, no install directory.
  Those live in `pkg/install` (`setupExiftool`; see below), which is the one
  place that resolves *a path* to hand `exiftool.New`. **A path with a line
  break is refused** (`ErrUnsafePath`): the `-@` argument file is one argument
  per line, so a newline in a filename would turn the rest of it into
  exiftool options — and exiftool writes files. **Every call has a timeout**
  (`extractTimeout`, 2 min) and a cancelled `ctx` does the same: both kill the
  process, since a read blocked on its stdout can't be interrupted any other
  way. That leaves the worker dead (`ErrProcess`); `Pool.Extract` replaces a
  dead worker before handing it out.
- `path/` — path canonicalization / home-relative helpers, plus
  `SanitizeSegment` (moved from `pkg/core/vfs`): what a derived *segment*
  (not a full path) is allowed to contain — `,`, whitespace and everything
  some library filesystem refuses (`/\:*?"<>|`, control characters; exFAT
  and NTFS are what photo drives usually are) become `-`, runs collapse,
  ends trim, names cap at 255 bytes, and Windows device names (`CON`, `NUL`…)
  get a `_`. `vfs` calls it for every folder segment (device/orientation/
  media/date, renames — `plan.go`, `review.go`). `SanitizeFileName` is the
  same rule for a file's own name, keeping spaces, dots and case and leaving
  room for a `_N` suffix; `buildTargets` runs every planned name through it.
  `pkg/location` imports it too, for `FolderName`. **This package imports
  nothing else in the project**, which is what makes it safe to depend on from
  anywhere — the rule lives once, not once per caller. `RelativeToHome`/
  `ExpandPath` are the `$HOME` ↔ `~` conversion, both directions — already
  the one place that logic lives; the
  `config` wizard's output-path suggestion list (`internal/cli/config.go`)
  builds every candidate path through `RelativeToHome` and reads typed input
  back through `ExpandPath`, so a suggestion is never shown with the raw home
  directory spelled out. `ToLibrary`/`FromLibrary` (NFC + `/`) are for
  in-library path columns (`target_path`, `folder_nodes.name`) — paths this
  app built itself, so folding every segment to one spelling costs nothing.
  `ToSourcePath`/`FromSourcePath` (separator only, real `filepath.ToSlash`/
  `FromSlash`) are for source-path columns (`source_path`,
  `file_registry.file_dir`/`file_name`) — paths the filesystem handed the
  scanner, never NFC-folded: Linux and Windows compare a filename byte for
  byte, so folding an NFD-spelled name (normal for anything a Mac wrote) to
  NFC before storing it makes that real file permanently unfindable there,
  and can even collide two distinct Linux files that differ only in
  normalization form into one row.
- **There is no `pkg/deps` and no `pkg/utils`** — a package named for nothing
  in particular is where unrelated helpers accumulate. The atomic download
  (temp file + rename, byte progress, SHA256 verify) is `install.downloadFile`,
  next to its only caller, and stays that way — it streams from an
  `http.Response.Body`, not a local file, so it is a genuinely different
  shape from the copy below rather than the same rule twice.
- `atomicfile/` — `Copy`, `Rename`/`RenameCommit`, `MkdirAll` and `SyncDir`.
  `RenameCommit` runs a caller's commit between the new link landing (its
  folder synced) and the old name going — execute's move records the file
  there; a failed commit undoes the link, and a source that can't be removed
  after a commit is `ErrSourceLeft` (recorded, both names) rather than an
  undo. `MkdirAll` syncs each folder it creates into its parent (exFAT/FAT
  have no journal to order that). `Copy(src, dest, tee, check) (int64, error)`: a temp file
  in dest's directory, then `Rename`, so a failure partway never leaves a
  partial file at dest. **The temp file is `Sync`ed before the rename and
  `Rename` fsyncs the directories it touched** (`syncDirs`/`syncDir`): closing
  a file only hands its bytes to the page cache, and a rename's *dirent* is
  not on stable storage until the filesystem's own commit interval — so
  without either, a power loss could leave a zero-filled file under a name the
  plan considers finished, or, for a move, take the new name and the old one
  both and lose the photo outright. `check`'s hash verified the bytes that
  went through the hasher in memory, not the bytes on the disk, which is why
  the file sync is what makes that check mean anything. On darwin `Sync` is
  `F_FULLFSYNC`, so it reaches the drive's own cache. Directory fsync is a
  no-op on Windows (no directory-handle flush; NTFS orders its own metadata),
  and a filesystem that refuses it (`EINVAL`/`ENOTSUP`/`EPERM` — most network
  mounts) is not treated as a failed transfer. `tee` sees every byte copied
  and `check` runs before
  the link (execute's hash verify; preview passes `nil, nil`); the copy keeps
  src's permission bits minus execute bits (FAT/exFAT cards report 0777) and
  its mtime (`CreateTemp`'s 0600 hid the library from a media server, and a
  fresh mtime lost the date EXIF-less files plan by). Reads go through
  `io.CopyBuffer` with a 1 MiB buffer and `WriteTo` hidden — the same trap
  as `metadata`'s `readerOnly`. `Rename` is the **no-replace** rename (`os.Rename` silently
  replaces on macOS/Linux): hard link then unlink, `fs.ErrExist` when the
  target is taken, `ErrSourceKept` (link undone) when the source can't be
  removed, check-then-rename where hard links don't exist (exFAT, some
  network mounts — racy, marked `ponytail:`). **Order matters**: first, one
  directory entry however spelled (case, NFD, symlinked parent —
  `sameEntry`) is "already there", return nil and touch nothing; only after
  that do two links to one file mean a crash-interrupted move to finish
  (`halfDoneMove`, which also demands a link count ≥ 2). Swap or merge the
  two and a move onto its own path deletes the file's only name — a real
  bug in review. This was `review.copyFile`,
  duplicated verbatim for the peek feature; `pkg/core/execute`'s `Copy` mode
  needed the identical thing, and the design ticket that placed execute's
  seam called this out by name as "about to be duplicated a third time" —
  extracted on that third caller, not before, per **imports point down
  only** (both callers are above it).
  Imports nothing else in the project, like `pkg/path`/`pkg/logger`/`pkg/lock`.
- `install/` — **the one place a downloadable dependency's version, download
  location, on-disk layout, fetch, and readiness are all known.** `pkg/exiftool`
  and `pkg/location` only ever run the already-installed binary or query an
  already-open, already-verified database — neither knows a version number, a
  download URL, or a file path; all of that lives here instead:
  - `exiftool_setup.go` — `setupExiftool` (version-gated: `$PATH` or
    `binDir`, else download+extract), `fetchReleaseMeta`, `checkVersion`,
    `extractTarGz`. Moved verbatim from the old `pkg/exiftool/verify.go`.
  - `location_setup.go` — `downloadLocationDB`, `verifyLocationDB`
    (checksum + `geonames_cities` row count against the published meta —
    fetched **first and required**, and a database failing verification is
    removed with it, since the download is skipped while the file exists), and
    `OpenLocationResolver` (download → open → verify → `location.NewResolver`
    — the **exported** entry point both `Coordinator` and
    `pkg/location/locationtest` use, so a test exercising a `Resolver`
    exercises the app's exact setup path, not a hand-rolled approximation).
    `LocationDownloadBaseURL`/`LocationDBFileName`/`LocationMetaFileName` live
    here too, moved from the old `pkg/location/setup.go`.

  On top of that, `Coordinator` owns the install
  order (exiftool first — the small download the earlier metadata phase waits on;
  the location database behind it, since only the last phase, vfs, needs it),
  the shared install lock, download byte-progress fan-out (`Options.OnProgress`,
  phase `"exiftool"`/`"location"`), and readiness. `Start` installs both;
  `StartLocationOnly` installs just the location database, for a caller (the
  config wizard) with no use for exiftool. Every caller gets a getter, never a
  raw channel, and there is **one getter per dependency**, not a silent/logging
  pair: `Exiftool`/`Location` block, and log a `UserKey` "Waiting for … to
  finish" line only if the call actually has to wait. Narration is a property
  of waiting, not of who asked — the old `Await*` twins meant a caller could
  pick the wrong one and silently lose the line that stops a stalled scan
  looking hung. `LocationNow` never blocks and reports `install.ErrPending`
  while the install runs, so a form validator running on every keystroke can
  tell "ask again later" from "this will never work" — a distinction it needs,
  since it holds the field on the first and waves it through on the second.
  `LocationDBIfReady` is the same non-blocking peek at the raw handle, for
  `closeDBs` at shutdown. This replaced four raw `*app` fields
  (`ExiftoolPath`, `LocationResolver`, `LocationDB`, `InstallProgress`) a
  background goroutine wrote and a pipeline goroutine read, with the
  happens-before edge documented in a comment rather than enforced by a type —
  `app` now holds one field (`Deps *install.Coordinator`), built per command by
  `app.newDeps`, and every read blocks on the Coordinator's own internal
  channel instead of racing a shared field. `StartLocationOnly` is for a
  caller that only re-runs the vfs phase, never exif.

## Conventions that bite if ignored

- Bounded worker pools, never fire-and-forget goroutines.
- Wrap errors with `%w`. Upserts over SELECT-then-INSERT.
- **Imports point down only.** An edge that would point back up — a lower
  package reaching for a higher one's constant, style, or type — means the
  logic is in the wrong package, not that the edge is needed. Two real ones
  were removed for exactly this: `lock → tui` (to colour an error string) and
  `migrations → config` (for a schema literal). `pkg/path`, `pkg/logger` and
  `pkg/lock` import nothing else in the project, which is what makes them safe
  to depend on from anywhere.
- **A module owns its whole domain.** If a caller is re-deriving a rule the
  module already knows — how a place name is spelled, what a folder segment may
  contain, what a phase's `Config` is assembled from — the code belongs in the
  module, not the caller. The test: if two callers do it, they will drift.
- **Never duplicate a rule to avoid an import.** Check whether the cycle is
  real first; `pkg/location` carried a byte-for-byte fork of
  `path.SanitizeSegment` for a cycle that could not have existed.

## Build / test

```bash
make build     # -> bin/wandersort
make test      # go test -v ./...
make lint      # gofumpt -l -w .
go build ./... # quick compile check
```

## Accepted risks

`docs/accepted-risks.md` lists audit findings that were looked at and
accepted on purpose (one backup generation, same-host download checksums,
no untracked-file check, duplicates re-hashed each `add`). Don't raise them
again as new findings; change that file if the reason no longer holds.

## Open cleanup notes (not yet done)

- **Concurrency wall:** `lock.AcquireOutput` (`pkg/lock/`) takes an
  exclusive OS advisory lock on the output dir, so only one scan runs against
  a dir at a time — that lock is the entire wall, there is no other run-identity
  mechanism backing it up. Running scans concurrently against one output dir
  would need a real per-run isolation mechanism (there was one, keyed by a
  now-removed `sessionID`, built for the since-removed `serve` API's
  concurrent sessions — see `workflow/` above) or a single owner process that
  multiplexes runs.
- `vfs.resolveLocations`' anchor-fold radius is `location.MaxDistSquared`
  (~50km), not a separate per-user setting. Revisit if a single radius doesn't
  fit both dense and sprawling metros.
- `classifier.ParseMetadata`'s `map[string]any` decode (deliberately tolerant,
  see the package note above) is a different thing from `exiftool.releaseMeta`
  — the latter (the binary's checksum manifest, `exiftool.json`) is already a
  typed struct. Don't conflate the two if asked to "type the exiftool JSON."

## Agent skills

### Issue tracker

Issues/specs tracked as markdown files under `.scratch/<feature>/`. See
`docs/agents/issue-tracker.md`.

### Triage labels

Default five canonical roles used as-is. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` + `docs/adr/` at repo root. See
`docs/agents/domain.md`.
