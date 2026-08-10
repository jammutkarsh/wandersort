> SPDX-License-Identifier: AGPL-3.0-or-later
>
> Copyright (c) 2026 Utkarsh Chourasia

# CLAUDE.md

Guidance for AI agents working in this repo. Coding rules live in `AGENTS.md`
(symlink to `.agents/AGENTS.md`) — read it first. This file is the **map**: what
lives where and how the pieces fit.

## What WanderSort is

A black-box media organizer. Feed it unorganized photos/videos; it produces an
ordered folder hierarchy. Pipeline runs in ordered phases per `wandersort scan`
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

- `main.go` — builds `config.Defaults()` and calls `cli.Execute(cfg)`. **No
  logger here** — it's built later (see below).
- `internal/cli/` — cobra CLI. One file per command, plus `app.go` and `root.go`:
  - `app.go` — `Execute(cfg)`, **the package's only exported symbol**, plus the
    unexported `app` struct and everything hanging off it: `initAppDB`,
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
    `ensureOutput` is the shell's lazy `lockOutput` + `initAppDB` pair (stored
    on `app.outLock`, idempotent): the wizard writes only `config.yaml`, so a
    session that opens it first holds nothing, and a second `wandersort` is
    refused at the point it would really collide rather than at launch. The
    subcommands still take their own lock and unlock it themselves.
    `reloadConfig` re-runs `config.Resolve` after the shell's own wizard
    rewrote `config.yaml` mid-session — with `app.overrides`, the flag layer
    `PersistentPreRunE` resolved with, so a flag still beats what was just
    saved. **The output path is the one setting it holds back**: the database
    and the lock are already open on the old one, so it keeps the old
    `AppDBPath`/`LogFile` and returns a note saying it takes effect next
    launch. Everything else (rules, toggles, saved places) is live from that
    moment — which is what makes the stamp check and a mid-run
    `Workflow.UpdateConfig` agree on one `a.Config`.
  - `root.go` — root cmd and flag-name constants. **`PersistentPreRunE` is the
    single place** config is resolved: it ensures `~/.wandersort/config.yaml`
    exists (`config.EnsureGlobalConfigFile`), builds a `config.FlagOverrides`
    from the invoked command's cobra flags (`flagOverridesFrom`), and calls
    `config.Resolve` — the one function in `pkg/config` that layers flag > env
    > file > default and returns the fully-resolved `*Configuration`. The
    logger is built right after, from the resolved config, so `--output-path`
    / env vars / `config.yaml` all take effect before any logging or DB work.
    Don't rebuild the logger in `main.go`. There is no global registry here
    any more (no viper): every other command's own flags — `--yes`,
    `--plain`, `--rebuild`, `--vertical`, `--print`, `--paths` — are read
    straight off `cmd.Flags()` inside that command's own `RunE`, since `cmd`
    is already in scope there. `tuiEnabled` takes the invoked `cmd` for the
    same reason. **There is no `--debug`
    flag**: the JSON file log is always at debug level (`fileHandler`), the
    console only ever shows `UserKey` lines and warnings, and a verbose console
    made no sense once the TUI owned the screen. `--plain` is the escape hatch.
    **A config file that doesn't parse is a warning, not a failure**:
    `config.Resolve` returns the warning text (not an error), the run
    continues on defaults, and it's logged with `UserKey` once the logger
    exists so it reaches the log file too, not just the terminal. Hard-failing
    would let one stray tab in an all-optional settings file brick every
    command — including `wandersort config`, the one that opens the file to
    fix it. The root cmd's own `RunE` is `shell.go`'s `runRoot`: bare
    `wandersort` opens the unified app, and `--plain` / a piped stderr still
    prints help.
  - `shell.go` — the **unified TUI shell**, and **the only full-screen entry
    point there is**: a tab bar plus one live screen per tab (`shellModel`), so
    scan, the settings wizard and review are one session instead of three
    programs. `runShell(shellStart)` takes which tab to open on, and *every*
    interactive command goes through it — bare `wandersort` (the folder input),
    `scan -p …` (`shellStart.paths`, so the run starts without asking),
    `config`, and `review` (`shellStart.rebuild` for `--rebuild`). **A
    subcommand is a starting point, not a smaller app**: each one used to build
    its own `tea.Program` around a single screen, so `wandersort scan` could
    not reach the settings and `wandersort config` could not start a scan —
    `ctrl+t` existed only where this file drew the tab bar, and naming a
    subcommand was enough to lose it (a reported bug). `Init` asks for the
    opening tab by *message* (`tui.StartScanMsg` / `openConfigMsg` /
    `tui.OpenReviewMsg`) rather than placing the screen itself, because
    bubbletea calls `Init` on a copy and any container mutation there is
    discarded. `shellStart.rebuild` is consumed by the first `openReview` and
    cleared: after that the reviewer is inside the app, where `[R]` is how they
    ask, and silently re-proposing on every later `ctrl+t` would discard edits
    nobody chose to lose. Keeping all three screens alive at once is the whole
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
    tab runs `openReview` (the same `ensureOutput` + optional rebuild +
    `newReviewScreen` cmd `ctrl+t` and `wandersort review`
    use) and **leaves the tab where it is until
    the screen lands**, so there is never a blank frame. The tab bar's
    `✓ ready` follows `canReview`, not `reviewReady`, for the same reason: a
    plan left on disk is as ready as one this session prefetched, and a plain
    dim tab says nothing about it being there at all.
    **`ctrl+c` anywhere quits the app** — being dropped back on
    the folder input instead was a reported bug. While a scan is running it
    goes to the scan screen so the cancel guard gets a say. Otherwise it is
    still *forwarded* to the active screen first, so the review's
    unsaved-edits guard can warn once; `quitReq` is what turns the screen's
    answer (`Done`, or the `SwitchMsg{nil}` a review hands back with) into a
    quit rather than a walk home, and any other keystroke clears it — the user
    stayed, so a later save must go home as usual.
    **A wizard save is picked up without a relaunch** (`configSaved`, run when
    an embedded `FormModel` reports `Done()` without an abort or an error):
    `a.reloadConfig` re-resolves the settings, and a scan still running has its
    workflow retargeted through `wf.UpdateConfig` — which is why `shellModel`
    keeps the `*workflow.Workflow` the scan tab was built with, and why
    `newScanScreen` returns it alongside the screen. That is the whole reason
    the settings tab is worth having *during* a scan: the folders the run ends
    up proposing are the ones just asked for, with no rebuild prompt
    afterwards. Changing the output path mid-session is the exception — it
    surfaces as `reloadConfig`'s note on the home screen's error line, which is
    also where a plain `Settings saved in <path>` goes when there is no note:
    the wizard closes back into the shell instead of ending a process, so a
    printed receipt has nowhere to land and the home screen's line is the only
    confirmation the save gets.
  - `config.go` — `config` cmd: **the settings wizard** (there is no `setup`
    command — dependency downloads belong to `scan`). `buildConfigForm` +
    `tui.FormModel`: a top-down stacked form (answered fields collapse to
    summary rows, StageList-style) written by `config.SaveGlobal` — one
    whole-file marshal, since the wizard always submits every setting and the
    file has no comments to preserve. The command itself is four lines —
    `--print`/non-TTY dumps the file, everything else is
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
  - `scan.go` — `scan` cmd (the pipeline). Two functions and a helper: the
    interactive path is `runShell(shellStart{tab: tabScan, paths: paths})` —
    the same session a bare `wandersort` opens, just landing on the scan tab —
    and `runScanPlain` is everything else. **`--paths` is not
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
    `review --rebuild` never runs the metadata phase, so it starts the location
    database alone).
    **`scan` never requires `wandersort config` to have run first** — an
    unconfigured first scan just builds its folder proposal (output path,
    rules, saved-place anchors) from defaults. Running `wandersort config`
    later and then `wandersort review --rebuild` re-proposes the hierarchy
    from the new settings without a re-scan. `--paths/-p` is repeatable + comma-friendly
    (`StringSlice`); `config.yaml`'s `rules` key (see below) controls the VFS
    folder depth for this scan's proposal — no CLI flag, set it via
    `wandersort config`. The plain path (`--plain`/non-TTY) keeps the simple
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
  - `review.go` — `review` cmd: a three-way switch and nothing else.
    `--yes` is `confirmReviewAll` (its own lock, DB, `vfs.BuildTree` and
    `review.ConfirmAll`, all inline — no TUI to defer any of it to, and the
    only place a missing `.wandersort.db` is a hard error: the interactive
    path opens the app and says so on the home screen instead, since that user
    has a scan tab one `ctrl+t` away and refusing to start hides it);
    interactive is `runShell(shellStart{tab: tabReview, rebuild: rebuild})`, so
    a reviewer who finds the folders wrong can fix the settings and come back
    without relaunching; **a non-TTY without `--yes` is now an error** naming
    `--yes`, rather than drawing an alt-screen into a pipe. There is no session
    lookup before
    `BuildTree` — `virtual_fs_entries` always holds exactly one proposal
    batch (the VFS phase replaces every unapproved row every run), so an
    empty tree from `BuildTree` alone means "nothing to review yet".
    **`--rebuild` has no approved-plan guard any more** (and `ApprovedCount`
    is gone with it): `persist` keeps approved rows, so a rebuild has no
    confirmed work left to discard — it re-proposes what nobody signed off.
    It also
    holds `newReviewScreen`, which builds the embedded screen `scan` swaps into.
    **The TUI itself lives in `internal/review/`** — see below.
    It also owns the **rebuild prompt**: `settingsChanged(outputDir)` compares
    the `.wandersort.cfg` stamp against `vfs.ConfigStamp(vfs.ConfigFor(a.Config))`
    (see `pkg/core/vfs/snapshot.go`). Nothing re-proposes on its own, so this
    comparison is the only way a settings change ever becomes visible. **The
    CLI does not ask the question itself** — it hands the answer to the review
    as `review.Options.SettingsChanged` and the rebuild itself as
    `Options.Rebuild` (`a.rebuildTree` = `vfs.Propose` + `BuildTree`), and the
    review raises its own full-screen yes/no over the tree. One asking place,
    two entry points (the shell opening review — however it was asked for —
    and a settings save while a review is already on screen), instead of an
    interstitial screen per entry point — the earlier version had a pre-load
    `review.Prompt` *and* a `tui.ConfirmModel` interstitial in
    `newReviewScreen` *and* a banner, and they could each fire for the same
    change. `--yes` has nobody to ask: one `UserKey` warning
    naming `review --rebuild`, then the existing tree.
    **The comparison is of the settings, not of "did the wizard run"** — a
    reported bug: a trip through the wizard that changes something and changes
    it back has the same stamp, and must not raise a question about nothing.
    `shell.configSaved` therefore calls `settingsChanged` before forwarding
    `review.SettingsChangedMsg`, rather than forwarding on every save.
    **A build-time check alone can never be enough**, which is the other half
    of the same reported bug: in the shell the settings can move while the
    review is on screen, and no check that runs when a screen is *built* will
    see it. That is what `SettingsChangedMsg` is for.

- `internal/review/` — the bubbletea **full-tree view** TUI over the VFS
  proposal (issue #8), extracted from `internal/cli` because it was 60% of that
  package's lines while cobra wiring is the rest. Its whole exported surface is
  three functions in `review.go`:
  - `Screen(ctx, Options) tea.Model` — the review as an app-shell screen, and
    **the only interactive entry point** (`screen.go`, which finalizes
    in-program: on save it runs `vfs.Confirm` and the free-space check itself,
    then `tui.Switch(nil)` hands back to the shell).
  - `ConfirmAll(ctx, Options)` — `--yes`: write the proposal as-is, no TUI.
  - `Outcome(m tea.Model) (confirmed, err, ok)` — how an embedded review ended.

  There is **no standalone `Run` and no loading screen** any more: every
  full-screen command is the same shell opened on a different tab, so a review
  is always hosted, and the shell's own `openReview` already does the slow work
  (lock, DB, optional rebuild, `BuildTree`) off the UI goroutine with the tab
  bar saying `opening…`. `Options.Load` went with them.

  `Options` carries `DB`/`Tree`/`Resolver`/`Log`/`OutputDir`; a nil
  `Resolver` just disables rename autocomplete.

  **A big library opens on the segment picker, not the tree** (`segments.go`,
  `pickerModel`): `vfs.Segments` decides (nil = one slice, so go straight to
  the tree as every review did before), and `segmentsFor` runs that check in
  both entry points — `Screen` and the loading screen's `treeLoadedMsg`.
  `[enter]` builds that slice's tree in a **fresh query** off the UI goroutine
  (a segment is a `taken_at` range, which is what the database is for — not a
  filter over an already-loaded tree), `[ctrl+x]` discards a saved one's
  approval and re-opens it, `[esc]` warns once while slices are still
  unreviewed. `[R]`
  is a second, library-wide question — the same reset the per-slice screen's
  `[R]` raises, but re-proposing every unsaved slice at once, and raised
  automatically on a settings change even while this list (not a slice) is on
  screen, so noticing a settings change never requires opening a slice first.
  The per-slice screen is the ordinary
  `screen` wrapper carrying `seg` (what `Confirm` approves) and `host` (the
  picker snapshot it returns to on save, via `reenter`) — which is also where
  `Outcome`/`Run` read `saved` from, since a segmented review confirms as it
  goes rather than once at the end. **`host` is also what `[esc]` inside a
  slice goes back to** (`Model.hosted`/`back` → `screen`'s `Switch(*s.host)`): a
  reported bug — leaving a slice unsaved used to `Switch(nil)` and end the whole
  review, so a reviewer who opened the wrong year had no way back to the list
  and the remaining slices were unreachable. `open` clears `opening` on the
  snapshot it takes, since that snapshot is taken mid-open and would otherwise
  come back showing a spinner that never stops. `--yes` never segments: `ConfirmAll` is a
  decision about the whole library. `copy.go` holds the unexported
  `copyFiles`/`copyFile` the peek feature uses (same atomic
  temp-file-then-rename pattern `pkg/install` downloads with); it moved here
  with the TUI because the preview is its only caller.

  **The tree-reshaping rules themselves — merge, drop, flatten, and the
  tree-walking helpers they share — live in `pkg/core/vfs/edit.go`
  (`vfs.MergeNodes`/`DropNodes`/`FlattenNodes`/`SortTree`/`CloneTree`/
  `FindNode`), not on `Model`.** They take a `[]vfs.Node` and a list of IDs and
  return the edited tree plus what happened; nothing in that file knows a
  keypress or a row exists. `Model`'s `mergeSelection`/`dropFolders`/
  `flattenFolders` are thin callers: resolve `selectedRows()` into an ID list,
  call across the seam, then apply the result back onto cursor/undo/status
  state the tree edit itself has no business touching. This is also
  `MergedIDs`' one owner now — `vfs.Confirm` was already the other half of
  that invariant (interpreting what this file writes), so putting both in
  `pkg/core/vfs` means one package, not two, understands it. The payoff: a
  tree edit is tested by stating a tree, calling the function, asserting the
  result (`pkg/core/vfs/edit_test.go`) — no `tea.KeyMsg`, no terminal.
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
  (`vfs.MergeNodes`, via `commonPathPrefix` + `FindNode`/`removeChildByID` —
  a real tree-splice, not just a rename), named after **the row `V` was
  pressed on** — its own name, which is already the rename the reviewer typed
  on it, since a rename is written straight onto the node — with the summed
  `FileCount`. `mergeSelection` pulls the anchor's ID to the front of the
  slice it hands `vfs.MergeNodes` (which always keeps `ids[0]`) precisely so
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
  `vfs.Node.MergedIDs`, which is what `Confirm` remaps their files by
  (`prefixRewriter` also covers anything *below* a merged node, since it
  rewrites on the longest remapped ancestor rather than on an exact path
  match). An earlier version
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
  `Model.applyEdit`, which pushes a whole-tree clone first (`Model.snapshot`
  calling `vfs.CloneTree`, capped at `maxUndo` = 100) onto a stack. Trees are
  folders only, never files, so a clone is cheap.
  `d`/`D` **remove nesting the reviewer doesn't want**. Both act on
  `selectedRows` — a `[V]` range (every row in it at the anchor row's depth,
  the same rule `m` uses) or just the cursor row when there's no selection.
  Nothing acts tree-wide:
  - `d` (`dropFolders` → `vfs.DropNodes`) drops **each selected folder**,
    lifting its children onto its parent. Refused on a top-level (Year) row:
    its files would land in the library root.
  - `D` (`flattenFolders` → `vfs.FlattenNodes`) collapses **everything below** each selected
    folder into it, so the whole subtree's files sit directly in it and the folder
    itself stays. Works on a Year, since the Year survives to hold them.
    `2023/April/Indore/Apple iPhone 13` flattened at April is
    `2023/April` with all ten files. `FileCount` is unchanged — it already
    counted the subtree. Over a range the folders stay **separate**: several
    locations under one Day each keep their own folder and lose their
    splits. Folding them together is `m`'s job, not `D`'s.

  **Every structural edit re-sorts the tree by name (`vfs.SortTree`, called
  from `reflow`) and the merge puts the cursor on the surviving folder
  (`focusNode`).** Splices append — a merged node, or children lifted by a
  drop — at the end of the parent's list, so a 575-file day jumped below its
  siblings and got reported as "the merge deleted my folder". It hadn't; it
  was just off-screen at the bottom.

  Both record the removed IDs (plus anything already folded into them) on
  the surviving node's `MergedIDs`, so files sitting directly in a removed
  folder remap onto it — same machinery as merge, with `prefixRewriter`
  covering anything deeper. Both undo via `[u]`.
  **`esc` is the only way out, and the only thing that writes** — there is no
  separate save key any more. It always raises a full-screen Save/Discard ask
  (`askExit`/`exitChoice`, drawn the same way the config wizard's own `[esc]`
  ask is): `[enter]` accepts the highlighted default (Save — approving the
  proposal exactly as offered still needs a key, edits or not), a second
  `[esc]` inside it forcefully discards. Save hosted goes back to the
  time-slice picker with this slice approved (`reenter`); Discard hosted goes
  back with it still unreviewed (`back`); neither hosted just ends the review.
  `ctrl+c` is the unconditional escape hatch — never saves, warns once
  (`hasEdits` is just "the undo stack is non-empty", since every edit
  snapshots) and needs a second `ctrl+c` to actually discard and leave.
  `--yes` confirms the proposal
  as-is, non-interactively; `--rebuild` re-runs `vfs.Run` with the current
  `config.yaml` `rules` *before* reviewing, so a config change
  re-proposes the hierarchy without a re-scan or re-hash (editing
  `config.yaml` alone, without `--rebuild`, changes nothing until the next
  `wandersort scan`).
  **`R` is `--rebuild` from inside the review, and the screen calls it
  "reset the plan"** — the same thing without
  quitting and relaunching, which is the only form of it the shell can offer at
  all (there is no command line to add a flag to once the app is open). Capital
  `R` because `r` is rename. **One verb, two reasons**: the settings moved
  under the plan, or the reviewer simply wants their edits thrown away and the
  folders proposed again — the same act either way, so `raiseRebuildAsk` takes
  only a `settingsMoved` bool and it picks the wording
  (`rebuildAskTitle`/`rebuildAskText`), nothing else. It does not reset on the
  keypress: it raises `askRebuild`, a
  **full-screen yes/no drawn as `tui.ConfirmModel`** (the dialog `reset` asks
  with), which is also what `SettingsChangedMsg` and `Options.SettingsChanged`
  raise. Three ways in, one question. It was a dim line above the key bar
  first, and the reported verdict was the obvious one — **nobody reads that**;
  a plan that no longer matches the settings is worth the screen. The modal
  owns the keyboard until answered (only `ctrl+c` falls through, so the app is
  never trapped), and **`y` rebuilds on that press** — an earlier
  warn-once-then-act on `[R]` meant pressing it twice, which read as "the first
  press only dismissed the message". The modal *is* the warning: its text
  names the unsaved edits it discards. It no longer names an approved-file
  count — a reset keeps approved rows now, so there was nothing to warn about.
  The rebuild itself runs `Options.Rebuild` (the caller's hook: this package
  has neither the settings nor the vfs phase, and must not grow either) off the
  UI goroutine behind the same spinner `[p]` uses, then replaces the tree
  wholesale — undo stack, cursor and selection with it, since they all describe
  folders that may no longer exist. A nil `Options.Rebuild` hides the key and
  never raises the question, rather than offering something the host can't do.
  **`Rebuild` takes the screen's `*vfs.Segment`**: re-proposing is always
  library-wide (`vfs.Propose` replaces every unapproved row), but the tree it
  hands back has to stay scoped, or a reset inside the 2017 slice replaces it
  with every year at once — a reported bug, and one that fired on entry too,
  since a settings change raises the same modal.
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
  `config` wizard's town inputs, which is where reviewers expect it from. **Preview caching is content-based, not node-based**
  (`filesSignature`, keyed on the sorted file list, not the node ID): a
  directory with one child chain (e.g. `.../08/Horizontal/Photos`) shares
  literally the same files between its parent and its leaf, so peeking
  either reuses one temp copy instead of making a new one every time; every
  cached copy is `os.RemoveAll`'d once the TUI exits, however it exits.
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
- `issue.go` — `issue` cmd: zips the log (renamed
    `wandersort.log`) + `about.txt`; db opt-in via `--include-db` (holds paths/GPS).
- `reset.go` — wipe scan data (confirm prompt unless `--yes`), plus the
    `.wandersort.cfg` stamp, which describes a proposal that no longer exists.
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

Config precedence: **flag > env > config file > default**, entirely inside
`config.Resolve` (`pkg/config/config.go`) — the single place all four layers
meet. `internal/cli/root.go`'s `flagOverridesFrom` builds the flag layer from
cobra's `cmd.Flags()` (only for the settings `Resolve` knows about:
`output-path`, `collapse-levels`, `saved-places-date-only`,
`merge-same-location-days` — checking `.Changed` so an unset flag reads as
`nil`, not its zero value); `Resolve` reads the env layer itself via
`os.Getenv` (`OUTPUT_PATH`, `SEGMENT_MONTHS`, …) and the file layer via
`LoadGlobal`. There is no viper anywhere in this codebase — every other
command's own flags (`--yes`, `--plain`, …) are read straight off `cmd.Flags()`
in their own `RunE`, no env-var fallback for those (never documented, so
dropping it lost nothing). Keep new *config*-affecting flag names hyphen-free
to match their env var by uppercasing alone. Defaults come from
`config.Defaults()`, which `Resolve` calls as its base layer.

The config file is `~/.wandersort/config.yaml` (`pkg/config/config.go`),
created **empty** the first time *any* command runs
(`config.EnsureGlobalConfigFile`) and filled in by `wandersort config`.
**No comments, no template** — the wizard is the documentation, so the file is
just the settings, written whole by `SaveGlobal` (a plain struct marshal; the
old YAML-node surgery existed only to preserve comments). `output-path` is
the marker that the file has been through the wizard (`Configuration.Configured`,
set by `Resolve`) — `rules`, the three toggle bools, and `saved-places` are
only read from the file once `output-path` is present, since a `bool` field
can't otherwise tell "key absent" from "explicit false" the way `Resolve`'s
flag/env layers can (a nil pointer vs. a real value). `saved-places` has no
flag or env of its own — `Resolve` doesn't touch it at all; `app.syncAnchors`
reads it straight via `config.Load`. `segment-months` (0 = auto, else 3/6/12 —
the review's time-slice size, see `vfs.Segments`) is an int, so it goes through
`pick` and needs no wizard gate; it has an env var
(`SEGMENT_MONTHS`) and no flag. It is deliberately **not** in `ConfigStamp`:
it changes how a plan is reviewed, never where a file lands, so it must not
raise the reset prompt.

## Core pipeline (`pkg/core/`)

- `workflow/` — orchestrator. `RunScan` runs the `runSession` phase loop
  (scan→metadata→score→vfs) synchronously on the calling goroutine, so a CLI
  invocation streams progress and blocks until the scan finishes. It runs
  `scanRoots` first — canonicalize, drop duplicates, prune any root nested under
  another (O(n) after a lex sort) — returning the roots actually walked. That
  pruning used to live in an HTTP layer (removed along with `serve` — this is
  now a single-entry-point CLI), which meant `wandersort scan` reached through
  `internal/api` to get at it. `NewWorkflow` takes a `Deps` — two blocking
  getters for the downloadable dependencies — instead of a resolver and
  exiftool path: the metadata and vfs components are built lazily inside their
  phase closures, calling `deps.Exiftool()`/`deps.Location()` right before
  running, so a first-ever TUI scan walks while the downloads are still going.
  The metadata phase is the first to block on exiftool now that hashing no
  longer runs ahead of it — the walk is all the cover the download gets. Plain-console scans, which install everything up front, wrap
  the values in `workflow.ReadyDeps`. **`appCfg` is swappable while the
  pipeline runs** (`UpdateConfig`, mutex-guarded, plus a dirty flag): the shell
  hosts the settings wizard and the scan in one program, so a save can land
  mid-run. Only the vfs phase re-reads it — every phase above it has already
  used what it needed — and it does so in a small loop: `takeConfig` for a
  pass, then re-run if `configChanged` reports a save arrived *during* it.
  Let-it-finish-then-re-run rather than cancel mid-flight: `vfs.Propose`
  replaces the proposal wholesale and is idempotent, so a second pass costs one
  pass and needs no context surgery or half-written state. It is also what
  keeps `BuildAnchors` (which replaces `r.Anchors` in place and is read
  lock-free from the parallel `Lookup`) strictly sequential — never call
  `vfs.Propose` concurrently with itself. **There is no `scan_sessions` table and
  no in-memory run-overlap guard either** — `RunScan` takes no ID and returns
  none; two scans can never race against the same output dir because
  `lock.AcquireOutput`'s exclusive PID lock already serializes at the process
  level, and within one process `RunScan` only ever runs once, synchronously,
  per `scan` invocation (the old `claimRoots`/`activeRoots` map existed for
  the since-removed `serve` API's concurrent sessions and was unreachable
  dead code by the time it was deleted). `helpers.go` holds `CheckOutputSpace`
  — exported because `review` runs the same check: the last look before a
  plan is approved is exactly where "the output volume is too small" is still
  actionable. It fires **at the end of the run**, next to the "run wandersort
  review" hint, not after the scan phase where it used to scroll past
  mid-pipeline. `NewWorkflow` logs the resolved `workers`/`output`/`groupBy`
  as a `UserKey` line: `output` and `groupBy` come from flag/env/config.yaml,
  so showing the resolved values up front is the only way to see which source
  won. **`Workers` is not a setting** — there is no `--workers` flag, no
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
- `scanner/` — phase 1. Bounded-worker directory walk. Files are identified by
  absolute `(file_dir, file_name)`; each root's volume UUID is stamped for
  future drive re-anchoring. `Run` captures `scanStartedAt := time.Now()`
  once, before any walking begins; after a clean walk, `sweep` **soft-deletes**
  (`deleted_at`) rows under that root whose `last_seen_at` is still older than
  `scanStartedAt` — i.e. the walk didn't re-see them. This replaced an earlier
  session-identity check (`scan_session_id != this session`) with a pure
  wall-clock cutoff once sessions were removed: `storeScan`'s upsert always
  sets `last_seen_at` to the write-time `now()` for every file it touches,
  which is guaranteed to land after `scanStartedAt`, so the two checks are
  equivalent — one just doesn't need an identity to compare against.
  `purgeExpired` hard-deletes swept rows after 30 days (`deletedRetention`),
  so unplugged drives and transient errors self-heal. **No filename-stem
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
  them again for tidiness**. A merged phase also collapses the state machine to
  `DISCOVERED → ANALYZING → ANALYZED`: there is no `HASHING`/`HASHED` any more
  (dropped from `file_registry`'s CHECK constraint, along with 002's
  `trg_file_metadata_hashed` trigger, which existed to flip a file to `HASHED`
  on the hash-only insert). Nothing is half-persisted, so `scanner`'s upsert
  resets a stuck `ANALYZING` straight back to `DISCOVERED` — an interrupted run
  re-reads the file rather than resuming from a hash it never wrote. A failed
  hash clears the file's stale metadata row and parks it at `ERROR`.
  **Known gap:** full-byte hash means pixel-identical files with differing
  metadata land in separate groups.
  **A file whose byte length occurs exactly once in the library is never
  read** (`uniqueSizes`, `fileRecord.sizeUnique`): nothing can share its
  content, so the hash would confirm a duplicate group of one. Measured on a
  107k-file library: 15,246 files, **187.5 GiB, 23.9% of the bytes**. Its
  `file_metadata` row carries `hash_kind = 'size'` (`db.HashSize`) and a
  `sizeDerivedHash(fileID)` stand-in in `file_hash`. **That stand-in must stay
  unique per file** — the scorer groups duplicates by `file_hash` alone, so a
  shared sentinel would report every unread file as a copy of every other one.
  exiftool still runs on it; only the byte read is skipped.
  **`rehashOutdatedSizeHashes` is the other half, and is not optional.** "No
  other file is this long" is true only of the library as it stood; a later
  scan that adds a same-size file makes it false, and the failure is silent —
  two identical files reported as distinct, which is worse than the read it
  saved. It runs first thing in `Run`, before the count and the claim, and
  sends every stale `hash_kind = 'size'` row back to `DISCOVERED` (the
  newcomer is already there). `metadata_test.go` covers exactly that
  two-scan case; deleting the call makes it fail with the corruption in the
  message.
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
  a claim order gets — don't "improve" it. A closing unscoped pass
  (`pendingVolume.all`, which is *not* the same as an empty `uuid`) catches
  any straggler the grouping missed. `readCost` clamps to `[1, budget]`: a
  cost above the budget would block forever.
  Sidecars (`.AAE`) are claimed and hashed like anything else but never handed
  to exiftool (`fileRecord.mediaType`, checked in the worker) — they carry no
  EXIF, so spawning exiftool on them is pure waste. **An extraction failure is
  not a file failure**: it warns, persists empty EXIF columns, and marks the
  file `ANALYZED` (the hash and folder context are still enough for the VFS to
  place it). Workers write straight through `db.Writer` — it already serializes
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
- `scorer/` — phase 3. Elects master via folder-naming heuristics over live
  (`deleted_at IS NULL`) rows; re-promotes solo survivors of shrunken groups.
- `vfs/` — phase 4. **`docs/vfs-pipeline.md` is the long-form walkthrough of
  this package** — every SQL query, all eight `Plan` passes in call order, the
  concurrency patterns, and an edge-case catalogue naming the bug behind each
  rule. Read it before changing anything here; the notes below are the map,
  that document is the territory.
  Proposes destinations for every live master in the library
  from the persisted metadata (never re-reads files); each run replaces every
  *unapproved* row and leaves an approved plan alone (safe to call again
  mid-review — see `review.go`'s
  `--rebuild` flag). `persist` **flushes the writer before returning**: the
  writer is an async FIFO, and every caller reads the rows straight back
  (`[R]` re-proposes then calls `BuildTree` immediately), so without it the
  review redrew the proposal this run had just replaced — a reported
  "rebuild doesn't rebuild" bug. **`vfs.Propose` is the phase as one call** — it builds its
  own `Config` via `ConfigFor` and resolves the saved-place anchors via
  `BuildAnchors` before running. Assembling those is part of the phase, not of
  its callers: `workflow`'s vfs phase and `cli/review --rebuild` used to run
  the same four-line ritual (load the config file again, build anchors, copy
  `resolver.Anchors` onto the `Config`, `New(...).Run`) and either could drift
  from the other. `New` stays for a test, or a caller that wants to state the
  `Config` itself. **`Propose` also writes the config stamp** on success
  (`snapshot.go`: `ConfigStamp`/`WriteStamp`/`ReadStamp`, `.wandersort.cfg` in
  the output directory) — the same argument, one rung up: every path to a fresh
  proposal goes through `Propose`, and only `Propose` holds both the `Config`
  and the output directory, so neither caller has to remember. A failed stamp
  write warns and lets the proposal stand; a *missing* stamp is never a
  settings change (`ReadStamp` reports `ok=false`), so a pre-stamp library
  never prompts. **The stamp hashes a subset, not the config file**: `Rules`,
  the three folder toggles and `SavedPlaces` — the settings that can move a
  file. `Workers` is not in it because it is not a setting at all any more,
  and saved places are hashed as **typed names, not resolved anchors**,
  so the check never needs the location database. `Config.SavedPlaces` exists
  for that reason alone — `ConfigFor` copies it beside the anchors.
  One output folder has one rule set, which is why the stamp is a file in it
  rather than a database row: it outlives re-scans and new source folders.
  `Config.Rules` (below Year/Month)
  is `location`/`orientation`/`device`/`media` in any order, or empty for a
  flat `Year/Month` — set via `config.yaml`'s `rules` key (`wandersort config`
  wizard only, no CLI flag). `date` is a Day level, so the full
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
  re-proposes the existing photos inside it. `collapse-levels: false` in
  `config.yaml` (or `COLLAPSE_LEVELS=false`) forces the full nesting; it has
  no CLI flag. `vfs.ConfigFor` (which takes the whole `*config.Configuration`,
  so a new vfs-relevant setting doesn't churn its signature — and is therefore
  the one place `vfs` imports `pkg/config`, meaning `config` can never import
  `vfs`) is the single place the `none` sentinel
  is turned into a nil `Rules` — `workflow` and `review --rebuild` both go
  through it. The month segment is **number-first (`06_June`)**: a bare month
  name sorts alphabetically, which put `December` above `November` in the
  review tree and in every file browser. **The location ladder has no device
  or `Unsorted` rung**: resolved city → dated event segment (skipped when a
  `date` level already carries the date) → *nothing*. It used to fall back to
  the device name, which put a location folder named after the camera right
  next to the real device folder (`…/Canon EOS 700D/Canon EOS 700D/`) — wrong
  information, and duplicated. Unknown location now means the level is simply
  absent for that file, and `location_dir` stays NULL — *unless* located
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
  review is for. `dayKey`/`runKey`/`monthKey` (plan.go) name the three
  groupings so the difference between "the folder a file lands in" and "the run
  it belongs to" is in the type, not in the reader's head.
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

  Known gap: a sidecar whose only sibling is a video has no group at all —
  `captureDirs` skips videos so a Live Photo `.MOV` isn't forced across the
  Photos/Videos split — so it falls back to its own mtime (12 files in one real
  15k library).
  **A file's folder date is a stored fact, not a folder name**
  (`segments.go` + `virtual_fs_entries.taken_at`): `masterFile.folderDate` is
  the *cluster's* start, written to every member by `clusterAndSpill` before
  either early-continue, so one event that runs over a month or New Year
  boundary lands in one Year/Month folder instead of being torn in two. Read it
  through `masterFile.folderTime()` (folderDate, falling back to takenAt for
  the unclustered `PreviewPaths` samples) — `monthParts` (the Year/Month pair
  `dirFor` and `locationParent` share), `mergeSameLocationDays`' month key and
  `persist`'s `taken_at` column all go through it, so nothing can disagree
  about which month a file is in. Known ceiling (`ponytail:` in `plan.go`): the
  merge's *day* is still the file's own day-of-month, and 31 and 01 aren't
  consecutive ints, so a boundary-crossing run gives sibling `31` and `Jan_01`
  folders rather than a `31_01` range. **A file whose own month differs from
  its cluster's month gets a month-qualified day folder** (`crossesFolderMonth`
  → `Jan_01`, matching `eventSegment`'s cross-month shape) and is left out of
  `mergeSameLocationDays` entirely — a bare `01` under `12_December` reads as
  Dec 01 *and lands on top of the real Dec 01 files*, which was a reported bug
  (Jan 1 videos filed under `12_December/01/Banjar`).
  `Segments(ctx, db, months)` buckets the reviewable rows by that column —
  calendar-aligned (years / Jan–Jun / quarters), `months <= 0` picking years
  over a >3-year span and half-years otherwise, undated rows last in their own
  `Undated` bucket, and **nil for "don't segment"** (one bucket, or nothing
  dated). Because every cluster member shares a folder date, **a segment
  boundary can never split one event**. Segmenting on `taken_at` rather than on
  the path is the whole point: a path is what the reviewer renames.
  `BuildTree(ctx, db, seg)` and `Confirm(ctx, db, roots, seg)` take a `nil`
  segment for the whole library. `Confirm` scopes only the *approval*: the
  renames go through `prefixRewriter` (longest remapped ancestor wins), so a
  Year renamed in one slice's tree carries onto the rows of every other slice
  under it — otherwise one year would become two folders on disk. `ReopenSegment`
  puts a saved slice back to PROPOSED.
  `review.go` (issue #8's reconcile core, read by the CLI TUI) exposes the
  proposal as a directory tree the reviewer edits
  before `Confirm` writes it back: `BuildTree` also carries one exemplar
  GPS coordinate per location node (`Node.Lat/Lon`) for the TUI's expand-radius
  rename, and `FilesUnder` lists a node's source files for the preview-copy
  feature. **The GPS attaches by path, not by depth:** `dirFor` records the
  folder it emitted for the location level as
  `virtual_fs_entries.location_dir`, and `BuildTree` hangs the coordinate off
  exactly that node. The old fixed `suggestionDepth = 2` assumed location was
  Rules' first level, so any other order (`rules: [device, location]`, or a
  `date` level in front) hung it on whatever shared Device/Day node sat at
  depth 2. No `location_dir` (no location level in this proposal) means no
  GPS-bearing node, rather than a wrong one. `captureDirs` copies the group
  leader's `locationDir` onto every member: `buildTargets` short-circuits
  `dirFor` for a grouped file, so without that copy the file wrote a NULL
  `location_dir` and its folder silently lost GPS-radius renames (8185 of
  15024 entries in one real library). `location_dir` and `taken_at` are columns of
  **003's `CREATE TABLE`**, not their own migrations — the pre-tag rule (no tag
  yet, so no users) says edit the existing migration rather than stack an
  `ALTER` on it. The cost is that `migrations.Run` tracks versions
  individually: a database where 003 is already recorded will never get the
  column, and the vfs phase then fails at runtime on the INSERT. **Deleting
  `.wandersort.db` is the fix**, and `wandersort reset` is not — the file
  itself has to go. Same applies to any future edit of an already-run
  migration — `file_metadata.hash_kind` was added to **002's `CREATE TABLE`**
  the same way, and a pre-existing database fails the metadata phase with
  `no such column: m.hash_kind` until it is deleted.
  **`Confirm` merges, it doesn't reject:** two nodes renamed to the
  same final path collapse onto one folder (e.g. two unresolved date clusters
  turning out to be the same place) — this used to be an error before a real
  user hit exactly that case. `Node.MergedIDs` is the other merge path: nodes
  the review TUI folded away are absent from the submitted tree entirely, so
  their IDs — and, via `prefixRewriter`, anything below them — remap onto
  the survivor's path from there. `prefixRewriter` is one function doing two
  jobs on purpose: a merged node's descendants and a segment's out-of-slice
  rows are the same question ("this directory sits under a path that moved"),
  so a longest-ancestor-wins rewriter answers both and there is no second
  remap pass that could disagree with the first.

## Supporting packages (`pkg/`)

- `tui/` — the full-screen TUI kit: adaptive palette + semantic styles
  (`theme.go`), the Docker-buildkit-style `StageList` step stack shared by
  scan and its dependency install (`stagelist.go` — stage rows with right-aligned
  elapsed times, a progress bar and a live per-file tail nested under the
  running stage), the `SwitchMsg`/`Switch` pair a screen hands control on with
  (`shell.go` — the one-screen `Shell` host that used to live beside them is
  gone: `internal/cli`'s tab container is the only host now, and a kit type
  with zero implementations is flexibility nobody asked for), the `config` wizard
  (`form.go` — `Field.Example` blocks above the footer, `Field.Describe` for a
    description that depends on the answer under the cursor — prose belongs
    there, not in the example, which renders in a narrow column and truncates;
    descriptions word-wrap to the body width (`descriptionBlock`), so hard line
    breaks in them are re-flowed — `Field.Await` to hold a
  step on a background download, `DownloadMsg` for the progress row, and
  **numbered** option lists: `1)`/`2)` next to every choice, since an
  arrow-only list gives the eye nothing to aim at. A `FieldGroup` holds fields
  of *any* kind, which is what makes the Saved places step one screen with two
  inputs and two yes/no questions; `FormModel.Embedded` mirrors the review
  model's own embedded mode — the three quit points go through `finish()`,
  which sets `done` instead of `tea.Quit` when the shell owns the program, and
  the container polls `Done()`), the shell's landing screen
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
  `reset`; a screen that wants the question *inside* itself — the review's
  rebuild modal — drives its own keys and uses `ConfirmModel` for the layout
  only, built per frame (a bubbletea model copied by value can't safely hold a
  pointer into its own fields, which is what its `Value` is).
  Design rules live in `pkg/tui/README.md` — new screens compose from this
  kit, never invent colours/markers. The pipeline feeds it through the logger
  only (`pkg/logger/stream.go`: `StreamKey` per-file lines — logged at **Info**,
  not Debug: the TUI handler level-filters at the configured level (`info`), so
  a Debug stream line would never reach the feed/progress-bar at all;
  `console.go` strips StreamKey lines so the plain console never sees them,
  `PhaseKey`/`EventKey`/`ElapsedKey` stage routing); plain mode (`--plain`,
  non-TTY stderr — `tuiEnabled()`) keeps the line console, styled
  with the same theme.
- `config/` — **one file**, `config.go`: `Defaults()` (hardcoded config only,
  no env reads), `Resolve` (the flag > env > file > default precedence chain
  — see above), `FlagOverrides` (the neutral, framework-agnostic struct the
  CLI's cobra flags translate into before calling `Resolve` — this package
  imports no CLI framework), and the `~/.wandersort/config.yaml` machinery
  below it — `GlobalConfigPath`, `EnsureGlobalConfigFile` (creates it empty if
  missing), `LoadGlobal`, and `SaveGlobal` (whole-file marshal of the
  `Global` struct — every key the wizard collects, nothing else). There are
  still two shapes here: `Configuration` (resolved, runtime — gains a
  `Configured` field once `Resolve` has run) and `Global` (on-disk); `Resolve`
  is now the one place that maps one onto the other.
- `db/` — sqlite (`modernc.org/sqlite`) open/migrate/retry; `writer.go` batched
  writes; `reset.go` `DB.ResetAll` (the FK-safe factory wipe behind
  `wandersort reset` — it lives here, not in the CLI layer, because it is a
  database operation); `migrations/` numbered
  Go migrations; `dbtest/` shared test fixtures
  (fresh migrated DB + seed helpers) used by every pipeline package's tests.
  All stored timestamps are UTC fixed-width nanoseconds via `db.FormatTime`
  (`db.TimeLayout`); convert to the user's local zone only at display time.
- `volume/` — best-effort volume-UUID resolution per scan root (diskutil on
  darwin, /dev/disk/by-uuid on linux, volume GUID via winapi on windows —
  cross-compiled only, untested on real hardware), cached per path; also
  `FreeBytes` for the post-scan output-volume space preflight (warn-only,
  `workflow.warnIfLowSpace`). Also `Class`/`ClassForPath` (`class.go`) — how a
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
  attr — that's what `issue` ships. Never stdlib `log`.
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
- `exiftool/` — `Extractor`: runs an already-installed exiftool binary
  (`-json -n`) and parses its output via `classifier.ParseMetadata`. That's
  the whole package — no version check, no download, no install directory.
  Those live in `pkg/install` (`setupExiftool`; see below), which is the one
  place that resolves *a path* to hand `exiftool.New`.
- `path/` — path canonicalization / home-relative helpers, plus
  `SanitizeSegment` (moved from `pkg/core/vfs`): what a derived *segment*
  (not a full path) is allowed to contain — strips `/\:,` and whitespace to
  `-`, collapses runs, trims. `vfs` calls it for every folder segment
  (device/orientation/media/date, renames — `plan.go`, `review.go`).
  `pkg/location` imports it too, for `FolderName`. **This package imports
  nothing else in the project**, which is what makes it safe to depend on from
  anywhere — the rule lives once, not once per caller. `RelativeToHome`/
  `ExpandPath` are the `$HOME` ↔ `~` conversion, both directions — already
  the one place that logic lives; the
  `config` wizard's output-path suggestion list (`internal/cli/config.go`)
  builds every candidate path through `RelativeToHome` and reads typed input
  back through `ExpandPath`, so a suggestion is never shown with the raw home
  directory spelled out.
- **There is no `pkg/deps` and no `pkg/utils`** — a package named for nothing
  in particular is where unrelated helpers accumulate. The atomic download
  (temp file + rename, byte progress, SHA256 verify) is `install.downloadFile`,
  next to its only callers; `copy.go` had one caller (the review TUI's
  preview), so it went to `internal/review` as unexported `copyFiles`.
- `install/` — **the one place a downloadable dependency's version, download
  location, on-disk layout, fetch, and readiness are all known.** `pkg/exiftool`
  and `pkg/location` only ever run the already-installed binary or query an
  already-open, already-verified database — neither knows a version number, a
  download URL, or a file path; all of that lives here instead:
  - `exiftool_setup.go` — `setupExiftool` (version-gated: `$PATH` or
    `binDir`, else download+extract), `fetchReleaseMeta`, `checkVersion`,
    `extractTarGz`. Moved verbatim from the old `pkg/exiftool/verify.go`.
  - `location_setup.go` — `downloadLocationDB`, `verifyLocationDB`
    (checksum + `geonames_cities` row count against the published meta), and
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
  channel instead of racing a shared field. `review --rebuild` uses
  `StartLocationOnly` (it only re-runs the vfs phase, never exif) and the
  plain interactive path reuses the same `Coordinator` if `--rebuild` already
  built one, rather than installing twice.

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
