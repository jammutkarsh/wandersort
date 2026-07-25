> SPDX-License-Identifier: AGPL-3.0-or-later
>
> Copyright (c) 2026 Utkarsh Chourasia

# CLAUDE.md

Guidance for AI agents working in this repo. Coding rules live in `AGENTS.md`
(symlink to `.agents/AGENTS.md`) — read it first. This file is the **map**: what
lives where and how the pieces fit.

## What WanderSort is

A black-box media organizer. Feed it unorganized photos/videos; it produces an
ordered folder hierarchy. Pipeline runs in ordered phases per scan session:

1. **Scan** — walk roots, build a file index.
2. **Hash** — content-hash for duplicate detection.
3. **EXIF** — extract metadata for later.
4. **Score** — within a duplicate group, elect one master copy (same bytes,
   different storage context — e.g. folder named `Goa Trip 2024`).
5. **VFS** — propose a destination hierarchy for every live master in the
   library (not just the session's), using the user's prior folder-naming as
   context when EXIF is absent. Nothing on disk is touched.

## Entry point & CLI

- `main.go` — builds `config.Defaults()`, constructs `cli.App{Config}`, calls
  `app.Execute()`. **No logger here** — it's built later (see below).
- `internal/cli/` — cobra CLI. One file per command:
  - `root.go` — root cmd, `App` struct, DB/exiftool/resolver lazy-init helpers,
    viper setup, config-override resolution. **`PersistentPreRunE` is the single
    place** flags/env/config-file are resolved (`loadGlobalConfigFile` then
    `applyOverrides`) and the logger is built — so `--output-path` /
    env vars / `config.yaml` all take effect before any logging or DB work.
    Don't rebuild the logger in `main.go`. `loadGlobalConfigFile` also
    **creates** `~/.wandersort/config.yaml` (empty) on the very first command
    of any kind via `config.EnsureGlobalConfigFile` — viper always has a file
    to read; `wandersort config` is what fills it in. **There is no `--debug`
    flag**: the JSON file log is always at debug level (`fileHandler`), the
    console only ever shows `UserKey` lines and warnings, and a verbose console
    made no sense once the TUI owned the screen. `--plain` is the escape hatch.
    **A config file that doesn't parse is a warning, not a failure**:
    `loadGlobalConfigFile` returns the warning text (not an error), the run
    continues on defaults, and it's logged with `UserKey` once the logger
    exists so it reaches the log file too, not just the terminal. Hard-failing
    would let one stray tab in an all-optional settings file brick every
    command — including `wandersort config`, the one that opens the file to
    fix it. Viper only commits parsed values on success, so nothing half-read
    leaks into the settings.
  - `config.go` — `config` cmd: **the settings wizard** (there is no `setup`
    command — dependency downloads belong to `scan`). `buildConfigForm` +
    `tui.FormModel`: a top-down stacked form (answered fields collapse to
    summary rows, StageList-style) written by `config.SaveGlobal` — one
    whole-file marshal, since the wizard always submits every setting and the
    file has no comments to preserve. On success it prints exactly
    `config saved in <path>`. Step order is **output path, workers, rules,
    collapse, then one Home & work step** whose sub-fields are home town, work
    town, "group home/work photos by date only?" and "merge consecutive
    same-location days?" — both folder questions live *after* the towns
    because their examples name the town the user just typed.
    **Every example is computed, not canned**: `examplePath(day, town,
    collapsed)` renders the path the answers *so far* would produce (it walks
    `rulesField.Selected`), so turning off the `location` rule removes the city
    folder from every later example. Examples render in their own block above
    the footer (`tui.Field.Example` → `FormModel.exampleBlock`, the same place
    the scan screen pins warnings) and only for the option under the cursor —
    a description that listed the yes *and* no outcomes at once made the reader
    work out which one was live. Descriptions explain, examples demonstrate.
    Output path expands a leading `~` (`expandHome`, applied at save *and*
    when matching suggestions) and only suggests locations whose parent dir
    exists on this machine, which is what makes the list platform-correct.
    Town inputs validate through `canonicalTown` (gazetteer exact-match).
    **The location DB downloads in the background, with no install screen**:
    `runConfigTUI` kicks off `InitLocationResolver` in a goroutine, feeds its
    byte progress into the form's own row under the banner (`a.InstallProgress`
    → `tui.DownloadMsg`), and hands the form a `gazetteer func() error` closure.
    The Home & work step is the only one that needs the database, so it holds
    on `tui.Field.Await` (showing why, with the bar above it) until the
    goroutine's channel closes; everything above it is answerable meanwhile,
    and a database already on disk reports no bytes so the row never appears.
    That channel is also the happens-before edge making `a.LocationResolver`
    safe to read. A gazetteer that never opens at all (failed download, or
    another wandersort process holding the location DB — it opens
    `locking_mode=EXCLUSIVE`) is **not** treated as "pending": `Await` releases
    and the town is waved through unvalidated and saved as typed, in both
    `townValidator` and `canonicalTownOrTyped`. Blocking there would trap the
    user on a pre-filled field they could only escape by clearing it, and would
    silently drop the towns they already had. `a.Log` is swapped to a sink-less
    TUI logger for the run so the download's log lines can't draw over the
    alt-screen. Prints the raw file to stdout instead of running the wizard
    when `--print`/`-p` is given or stdout/stderr isn't a terminal
    (`wandersort config | grep …`, `> file`) — launching a full-screen wizard
    into a pipe is never what the caller meant.
  - `scan.go` — `scan` cmd (the pipeline). Runs **synchronously** in the
    foreground (`Service.RunScan` → `Workflow.RunScan`) so the user watches
    progress and the exit code reflects pipeline success; logs total elapsed
    time on completion. In TUI mode a first-ever run installs missing
    dependencies on the install screen and swaps into the scan screen in the
    same alt-screen program (`runScanTUI`'s `buildScan` via
    `InstallModel.Next`), Docker-style: first run downloads then works, every
    run after starts the scan immediately. **`scan`/`serve`/`review` are the
    only things that install dependencies** (`App.EnsureDependencies`).
    `--paths/-p` is repeatable + comma-friendly
    (`StringSlice`); `--rules` (also settable via `config.yaml`, see below)
    controls the VFS folder depth for this scan's proposal. Calls
    `syncHomeWorkFromConfig` before the pipeline so any globally-saved home/work
    anchor exists in *this* library's DB before VFS runs.
  - `serve.go` — `serve` cmd, HTTP API (gin) + swagger. Same workflow,
    long-lived; also takes `--rules`.
  - `anchor.go` — the read side of home/work anchors (the write side is the
    `config` wizard, which saves them via `config.SaveGlobal`):
    - `syncHomeWorkFromConfig` (called from `scan`) — reads the global config,
      resolves each anchor's coordinates via `ResolveByName` (guaranteed exact
      hit — the wizard's `canonicalTown` validator only saves gazetteer
      spellings), and inserts an `ANCHOR_HOME`/`ANCHOR_WORK` `user_labels` row
      if this library doesn't have one yet. Idempotent, silent, and a no-op
      with nothing globally set.
    - `exactMatch` — canonical-spelling helper `canonicalTown` uses.
  - `review.go` — `review` cmd: bubbletea **full-tree view** TUI over the VFS
    proposal (issue #8). Renders the whole hierarchy indented, alt-screen
    fullscreen, scrollable. Keys: `n`/`N` **hop to the next/previous row at
    the cursor's own depth** (`jumpSameDepth`), crossing into other branches
    by design — that's what makes `V` then `n``n` select one level across
    several months without arrowing through every folder's contents; stops at
    the ends, never wraps. `enter` accept suggestion, `r` rename (with
    ranked autocomplete — `Tab` fills the top match, `Ctrl-E` widens the search
    radius by another ~10km, typed text also prefix-matches previously
    confirmed `user_labels`), `p` peek (copies up to 250MB of the folder's
    files into a temp dir via `pkg/utils.CopyFiles` and opens that folder —
    read-only, nothing on disk is touched), `a` accept all, `V`/`m`/`u`
    Vim-style merge: `V` starts a contiguous range (sequential — no picking
    rows out of order), `m` **folds every row in the range at the anchor row's
    depth into one node under their lowest common ancestor**
    (`mergeSelection`, `commonPathPrefix` + `findNodeByID`/`removeChildByID` —
    a real tree-splice, not just a rename), named after the first one's
    **own** name — or the rename the reviewer typed on it, never its
    suggestion (an offer nobody accepted; broadcasting one put a name on the
    merged folder that came from no visible choice) — with the summed
    `FileCount`. **Anchor depth is the
    selection rule** — rows deeper than the row `V` was pressed on are that
    folder's own contents and ride along; shallower ones are scaffolding
    spanned to reach the next branch. One rule covers both shapes: leaves from
    different branches (anchor on a leaf) and whole parent folders (anchor on
    a Day). **Merging parents merges their subtrees**: children whose *final*
    names match (`finalName` honours a pending rename, since that's what
    `Confirm` writes) collapse recursively via `mergeInto` — three days in Goa
    give one Goa holding one merged device folder, not three the reviewer then
    has to merge by hand. This is what makes merging work across different Month/Day
    branches (e.g. one camera's photos spread across three months, all folding
    under the Year) — a plain same-path rename only merges nodes that already
    share a parent, since the final path is parent-path + name. **The
    folded-away leaves leave the tree entirely** — their IDs ride along on
    `vfs.Node.MergedIDs`, which is what `Confirm` remaps their files by
    (`remapUnderMerged` also covers anything *below* a merged node, which the
    TUI never produces but the HTTP surface can submit). An earlier version
    left them in place as same-named siblings and let `Confirm`'s
    same-path-collapses-to-one-folder behavior sort it out at write time —
    correct on disk, but the reviewer saw three "Canon EOS 700D" rows next to
    three now-empty Month/Day chains and read it as "merge didn't work".
    Emptied ancestors are pruned and ancestor `FileCount`s recomputed
    (`pruneEmptied`, using a pre-merge leaf-ID set to tell a real leaf from an
    ancestor the merge hollowed out). Rows caught in the range that still have
    children (the Month/Day scaffolding between two branches) are skipped, not
    merged. **`u` undoes structural edits all the way back**, not just the
    last one: every reshaping edit pushes a whole-tree clone
    (`snapshot`/`deepCloneNodes`, capped at `maxUndo` = 100) onto a stack,
    since a structural edit can't be undone by restoring per-row name strings
    the way a plain rename-merge could. Trees are folders only, never files,
    so a clone is cheap. `[L]` clears the stack — a relayout replaces every
    node ID, so older snapshots describe nodes that no longer exist.
    `d`/`D` **remove nesting the reviewer doesn't want**. Both act on
    `selectedRows` — a `[V]` range (every row in it at the anchor row's depth,
    the same rule `m` uses) or just the cursor row when there's no selection.
    Nothing acts tree-wide:
    - `d` (`dropFolders`) drops **each selected folder**, lifting its children
      onto its parent. Refused on a top-level (Year) row: its files would land in
      the library root.
    - `D` (`flattenFolders`) collapses **everything below** each selected
      folder into it, so the whole subtree's files sit directly in it and the folder
      itself stays. Works on a Year, since the Year survives to hold them.
      `2023/April/Indore/Apple iPhone 13` flattened at April is
      `2023/April` with all ten files. `FileCount` is unchanged — it already
      counted the subtree. Over a range the folders stay **separate**: several
      locations under one Day each keep their own folder and lose their
      splits. Folding them together is `m`'s job, not `D`'s.

    **Every structural edit re-sorts the tree by name (`sortTree`, called from
    `reflow`) and the merge puts the cursor on the surviving folder
    (`focusNode`).** Splices append — a merged node, or children lifted by a
    drop — at the end of the parent's list, so a 575-file day jumped below its
    siblings and got reported as "the merge deleted my folder". It hadn't; it
    was just off-screen at the bottom.

    Both record the removed IDs (plus anything already folded into them) on
    the surviving node's `MergedIDs`, so files sitting directly in a removed
    folder remap onto it — same machinery as merge, with `remapUnderMerged`
    covering anything deeper. Both undo via `[u]`. `L`
    cycles a fixed set of
    rules presets and **rebuilds the whole proposal in place**
    (`vfs.New(...).Run` + `BuildTree` — safe mid-review since VFS only reads
    already-hashed masters and replaces the proposal wholesale; resets any
    in-progress renames, since a different depth means different node IDs),
    `c` **save & exit** (the only thing that writes — `q` discards, so `q`
    with pending edits warns once and needs a second `q`; `hasEdits` derives
    "pending" from the rows and the undo snapshot rather than a flag any edit
    path could forget to set). `--yes` accepts every suggestion
    non-interactively; `--rebuild` re-runs `vfs.Run` with the current
    `--rules`/`config.yaml` *before* reviewing, so a config change
    re-proposes the hierarchy without a re-scan or re-hash (`--rules` alone,
    without `--rebuild`, changes nothing).
    **The footer is measured, not assumed to be one line** (`footer()` +
    `visibleRows`): the key help word-wraps to the terminal width (`wrapDim`),
    and the old fixed `height-6` budget let a wrapped help bar push the bottom
    tree rows off the screen.
    **Rename precedence is default name < location suggestion < user's own
    rename** — `enter`/`a` only ever fill from a suggestion when the row's
    `newName` is still empty, never clobbering something the reviewer already
    typed. **Preview caching is content-based, not node-based**
    (`filesSignature`, keyed on the sorted file list, not the node ID): a
    directory with one child chain (e.g. `.../08/Horizontal/Photos`) shares
    literally the same files between its parent and its leaf, so peeking
    either reuses one temp copy instead of making a new one every time; every
    cached copy is `os.RemoveAll`'d once the TUI exits, however it exits.
    **A rejected merge (`statusIsErr`) renders in `tui.Attn`, not
    `tui.DimText`** — a rejection used to look identical to routine status
    text, easy to miss (a reported "merge doesn't work" turned out to be
    either a missed capital-`V` requirement or exactly this — the rejection
    message was there, just easy to overlook). **Visual selection is a
    whole-row background highlight (`tui.Selected`), not a marker
    character** — a `*` prefix in one column was hard to track across a wide
    tree; selected rows render plain (no nested per-segment colour) since an
    inner ANSI reset would cut the background short partway through the line.
  - `report.go` — read-only per-session summary (opens its own RO sqlite conn).
    Lists every `scan_sessions` row (newest first), each with its own
    scanned/hashed/duplicate counts — duplicates are scoped to that session's
    own files via `scan_session_id` (two sessions over different roots can
    have unrelated duplicate pictures, so counts never bleed across sessions).
    Errors out if the DB has no sessions yet; flags the newest session as
    partial if its status isn't terminal (`COMPLETED`/`FAILED`/`CANCELLED`).
    Default output is a bordered table; `--vertical`/`-x` (psql `\x`-style)
    prints each session as expanded label:value pairs for narrow terminals.
  - `report_issue.go` — `report-issue` cmd: zips the log (renamed
    `wandersort.log`) + `about.txt`; db opt-in via `--include-db` (holds paths/GPS).
  - `reset.go` — wipe scan data (confirm prompt unless `--yes`).
  - `help.go` — custom lipgloss-styled help renderer. Kept in `cli` (unlike
    `lock.go`) since it's a one-off cobra `SetHelpFunc`, not reusable
    logic another entry point would need.
  - `internal/cli` holds **only** `root.go` + one file per subcommand (plus the
    `help.go` exception above) — everything else that used to live here moved
    out to its own package so a future TUI entry point can reuse it:
    - `pkg/lock/` — all wandersort file locking: generic PID/O_EXCL
      acquire/reclaim mechanics (`acquire`, `Lock`, `ErrHeld`) plus the two
      domain wrappers — `AcquireOutput` (one scan/serve per output dir, styled
      "already running" message) and `AcquireInstall` (install coordination
      across scan/serve/review: `EnsureDependencies` tries non-blocking first
      so it can log a "waiting…" line, then blocks) — and the lock filenames (`OutputFileName`, `InstallFileName`).
      `EnsureDependencies` (in `root.go`) tries the install lock non-blocking
      first so it can log a `UserKey`-tagged "waiting for another process…"
      line before falling back to the blocking acquire — without that, a
      scan/serve waiting behind an in-progress install just looks hung.
      Only `cli` uses locking today, but the mechanics are generic, so it
      lives in `pkg/` for reuse by other entry points.
    - Styling (help renderer, lock messages, error output) comes from
      `pkg/tui`'s theme — there is no separate `pkg/style` any more; the old
      one was folded into `pkg/tui/theme.go` so full-screen and plain output
      share one palette.

Config precedence: **flag > env > config file > default** (viper's normal
config-file precedence — see `loadGlobalConfigFile`/`applyOverrides` in
`root.go`). Env names are the uppercased flag; `AutomaticEnv` covers the
hyphen-free flags (`WORKERS`, `PORT`, …), and the one hyphenated flag is bound
explicitly (`--output-path` → `OUTPUT_PATH` via `v.BindEnv`). Keep new flags
hyphen-free so they need no explicit bind. Defaults come from `pkg/config`.

The config file is `~/.wandersort/config.yaml` (`pkg/config/global.go`),
created **empty** the first time *any* command runs
(`config.EnsureGlobalConfigFile`) and filled in by `wandersort config`.
**No comments, no template** — the wizard is the documentation, so the file is
just the settings, written whole by `SaveGlobal` (a plain struct marshal; the
old YAML-node surgery existed only to preserve comments). `output-path`,
`workers`, `rules`, and the vfs toggles are read back through plain viper keys
(same `v.GetX(flagName)` calls `applyOverrides` already makes work regardless
of source); `home-work.home`/`home-work.work` are the one thing viper doesn't
manage — `anchor.go` reads them via `config.LoadGlobal`.

## Core pipeline (`pkg/core/`)

- `workflow/` — orchestrator. Two entry points over the same `runSession` phase
  loop (scan→hash→exif→score→vfs): `RunScan` (synchronous, CLI) and `SubmitScan`
  (background goroutine, `serve`; `Close()` waits). `claimRoots` rejects a new
  session whose roots overlap an in-flight session's (in-memory — the output
  lock guarantees one process). `helpers.go` = session status/finalize writes
  plus `CheckOutputSpace` — exported because `review` runs the same check: the
  last look before a plan is approved is exactly where "the output volume is
  too small" is still actionable. It fires **at the end of the session**, next
  to the "run wandersort review" hint, not after the scan phase where it used
  to scroll past mid-pipeline. `NewWorkflow` logs the resolved
  `workers`/`output`/`groupBy` as a `UserKey` line: all three come from
  flag/env/config.yaml, so showing the resolved values up front is the only
  way to see which source won.
  Each phase reports **one** user-facing line: `workflowPhase.summary(count)`
  with the elapsed time appended (`Scanned 15481 files in 1.996s`). It used to
  be two — a count line from `onSuccess` plus a separate `"%s phase took %s"`
  — which is twice the console noise for one fact. `onSuccess` survives for
  side effects only (the post-scan space check); anything user-facing goes in
  `summary`.
- `scanner/` — phase 1. Bounded-worker directory walk. Files are identified by
  absolute `(file_dir, file_name)`; each root's volume UUID is stamped for
  future drive re-anchoring. After a clean walk, `sweep` **soft-deletes**
  (`deleted_at`) rows the session didn't re-see; `purgeExpired` hard-deletes
  them after 30 days (`deletedRetention`), so unplugged drives and transient
  errors self-heal. **No filename-stem capture-grouping** (there used to be
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
- `hasher/` — phase 2. BLAKE3 over full bytes, nothing else. Inserts the
  `file_metadata` row with the hash and **NULL exif columns** — the row exists
  so the exif phase has something to fill in, and the `trg_file_metadata_hashed`
  trigger flips the file to `HASHED` on that insert. A failed hash clears the
  file's stale metadata row. **Known gap:** full-byte hash means
  pixel-identical files with differing metadata land in separate groups.
- `exif/` — phase 3. Runs exiftool once per file (the full tag set the VFS
  needs) and `UPDATE`s the row the hash phase inserted. **Split from `hasher`
  deliberately**: one worker pool doing BLAKE3-then-exiftool made the two costs
  one number, and anything that wants to sit between them (or replace one) had
  to be threaded through the hash loop. Now each claims its own rows, times
  itself, and owns a TUI stage.
  The per-file state machine is what makes it a real phase, not a sub-step:
  `HASHED → ANALYZING → ANALYZED`, using the `ANALYZING`/`ANALYZED` values
  `file_registry`'s CHECK constraint already declared (no migration).
  `scanner`'s upsert resets a stuck `ANALYZING` back to `HASHED`, not to
  `DISCOVERED` — an interrupted extraction shouldn't cost a re-hash; changed
  bytes are checked first and still win. Sidecars (`.AAE`) are filtered out in
  the claim SQL by `media_type` and stay at `HASHED` — they carry no EXIF, so
  spawning exiftool on them is pure waste. **An extraction failure is not a
  file failure**: it warns, persists empty metadata, and marks the file
  `ANALYZED` (the hash and folder context are still enough for the VFS to place
  it). Workers write straight through `db.Writer` — it already serializes every
  operation, so the hasher's separate store goroutine would only add a channel.
  Persists `exif_creation_date` alongside `exif_create_date` — a real
  reported bug: a QuickTime video's `CreateDate` is the raw UTC timestamp with
  no offset, while a photo's `DateTimeOriginal` is local wall-clock; for the
  same real moment these can differ by hours, enough to shift a video into
  the wrong day/cluster next to its photos. `CreationDate` (iOS's composite
  tag) does carry an offset — `vfs.stripOffset` strips it back off before
  parsing (see `deriveAll`'s `takenAt` comment), because every *other*
  timestamp here is naive local wall-clock and applying the real offset would
  shift the video away from siblings that never had one applied.
- `scorer/` — phase 4. Elects master via folder-naming heuristics over live
  (`deleted_at IS NULL`) rows; re-promotes solo survivors of shrunken groups.
- `vfs/` — phase 5. Proposes destinations for every live master in the library
  from the persisted metadata (never re-reads files); each run replaces the
  proposal set wholesale (safe to call again mid-review with a different
  `Config` — see `review.go`'s `[L]` key). `Config.Rules` (below Year/Month)
  is `location`/`orientation`/`device`/`media` in any order, or empty for a
  flat `Year/Month` — set via `--rules`/`config.yaml` (CLI) or cycled live
  in the review TUI. `date` is a Day level, so the full
  `Year/Month/Day/Location/Device/Orientation/Media` shape is
  `--rules date,location,device,orientation,media`; when a `date` level is
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
  absent for that file, and `suggestion_dir` stays NULL so nothing hangs a
  suggestion off it. `BuildTree` also **drops a suggestion equal to the
  folder's own name** (a source folder named after the camera made
  SOURCE_FOLDER suggest the name already on screen).
  `resolveLocations` folds a directly-resolved GPS city
  into a *confirmed* `ANCHOR_HOME`/`ANCHOR_WORK` label when within
  `location.MaxDistSquared` (~50km) of it, so a metro's suburbs land in one
  folder instead of fragmenting by neighbourhood — `anchorCities`
  (`pkg/core/vfs/cluster.go`) deliberately does **not** also fall back to "the
  library's most frequent city" the way it used to: that fabricated a
  location with no temporal/spatial relationship to the cluster being named
  (a real reported bug — a GPS-less DSLR photo "suggested" whatever city
  dominated the user's phone-photo library). No confirmed anchor means the
  suggestion ladder falls through to the source-folder-name rung instead.
  `review.go` (issue #8's shared reconcile core, read by both the CLI TUI and
  the HTTP API) exposes the proposal as a directory tree the reviewer edits
  before `Confirm` writes it back: `BuildTree` also carries one exemplar
  GPS coordinate per location node (`Node.Lat/Lon`) for the TUI's expand-radius
  rename, and `FilesUnder` lists a node's source files for the preview-copy
  feature. **Suggestions attach by path, not by depth:** `dirFor` records the
  folder it emitted for the location level as `virtual_fs_entries.suggestion_dir`,
  and `BuildTree` hangs the suggestion + GPS off exactly that node. The old
  fixed `suggestionDepth = 2` assumed location was Rules' first level, so
  any other order (`--rules device,location`, or a `date` level in front)
  smeared every file's suggestion onto whatever shared Device/Day node sat at
  depth 2 — reported as "one suggestion across the whole tree". No
  `suggestion_dir` (no location level in this proposal) now means no suggestion
  node at all, rather than a misplaced one. `suggestion_dir` arrived as
  migration **004**, deliberately not as an edit to 003 (the usual pre-tag
  rule): 003 has already run on real libraries, and re-creating the schema
  would force a full re-hash. A plain `ALTER TABLE` costs nothing. **`Confirm` merges, it doesn't reject:** two nodes renamed to the
  same final path collapse onto one folder (e.g. two unresolved date clusters
  turning out to be the same place) — this used to be an error before a real
  user hit exactly that case. `Node.MergedIDs` is the other merge path: nodes
  the review TUI folded away are absent from the submitted tree entirely, so
  their IDs — and, via `remapUnderMerged`, anything below them — remap onto
  the survivor's path from there.

## HTTP layer (`internal/api/`)

Only used by `serve`. Standard handler→service→repository split per domain:

- `pipeline/` — start scans, query counts. `service.go` `prepareScanRoots`
  canonicalizes + prunes nested roots (O(n) after lex sort).
- `admin/` — reset/admin ops.
- `interfaces.go`, `middleware.go`, `response.go`, `errors.go` — shared gin glue.

## Supporting packages (`pkg/`)

- `tui/` — the full-screen TUI kit: adaptive palette + semantic styles
  (`theme.go`), the Docker-buildkit-style `StageList` step stack shared by
  scan and its dependency install (`stagelist.go` — stage rows with right-aligned
  elapsed times, a progress bar and a live per-file tail nested under the
  running stage), the app `Shell` (one alt-screen program, `Switch` swaps
  screens without a terminal-restore flash), the `config` wizard
  (`form.go` — `Field.Example` blocks above the footer, `Field.Await` to hold a
  step on a background download, `DownloadMsg` for the progress row, and
  **numbered** option lists: `1)`/`2)` next to every choice, since an
  arrow-only list gives the eye nothing to aim at. A `FieldGroup` holds fields
  of *any* kind, which is what makes the home/work step one screen with two
  inputs and two yes/no questions), and shared chrome (`Banner`/`Footer`/`KeyHint`/`Screen`).
  Design rules live in `pkg/tui/README.md` — new screens compose from this
  kit, never invent colours/markers. The pipeline feeds it through the logger
  only (`pkg/logger/stream.go`: `StreamKey` per-file lines — logged at **Info**,
  not Debug: the TUI handler level-filters at the configured level (`info`), so
  a Debug stream line would never reach the feed/progress-bar at all;
  `console.go` strips StreamKey lines so the plain console never sees them,
  `PhaseKey`/`EventKey`/`ElapsedKey` stage routing); plain mode (`--plain`,
  non-TTY stderr — `tuiEnabled()`) keeps the line console, styled
  with the same theme.
- `config/` — `Defaults()` (hardcoded config only, no env reads) plus
  `global.go`: the `~/.wandersort/config.yaml` machinery — `GlobalConfigPath`,
  `EnsureGlobalConfigFile` (creates it empty if missing), `LoadGlobal`, and
  `SaveGlobal` (whole-file marshal of the `Global` struct — every key the
  wizard collects, nothing else).
- `db/` — sqlite (`modernc.org/sqlite`) open/migrate/retry; `writer.go` batched
  writes; `migrations/` numbered Go migrations; `dbtest/` shared test fixtures
  (fresh migrated DB + seed helpers) used by every pipeline package's tests.
  All stored timestamps are UTC fixed-width nanoseconds via `db.FormatTime`
  (`db.TimeLayout`); convert to the user's local zone only at display time.
- `volume/` — best-effort volume-UUID resolution per scan root (diskutil on
  darwin, /dev/disk/by-uuid on linux, volume GUID via winapi on windows —
  cross-compiled only, untested on real hardware), cached per path; also
  `FreeBytes` for the post-scan output-volume space preflight (warn-only,
  `workflow.warnIfLowSpace`).
- `logger/` — slog-based `Logger` interface; fans out to two handlers. The
  **console** handler (`console.go`) is deliberately minimal for CLI users: a
  coloured level tag + message + dimmed `key=value` attrs, no timestamp/source.
  It shows **only user-facing lines and warnings/errors** — tag a milestone with
  `logger.UserKey` (`log.Info("Scanning…", logger.UserKey, true)`); everything
  untagged is developer detail that goes to the file only. The `sessionId` attr
  is stripped from console lines (printed once at session start, in the message
  text) to avoid spam. There is no debug flag to bypass the console filter —
  the JSON file log always has every record.
  The **JSON file** handler keeps timestamp + source (`AddSource`) and every
  attr (incl. `sessionId`) — that's what `report-issue` ships. Never stdlib `log`.
- `location/` — offline reverse-geocode resolver + its own sqlite DB. `Setup()`
  downloads DB+meta if missing (idempotent) and is the only place that prints
  a user-facing line about it; `New`'s checksum verification is **not**
  `UserKey`-tagged, since it runs on every command that opens the resolver and
  printed a checksum on every single run. A mismatch is still a hard error. `exiftool.Setup()` is the same idea
  for the binary. Both are called lazily by `EnsureDependencies`. `Lookup`
  (single best match, cached/singleflighted) and `Candidates` (ranked list for
  the review TUI's rename picker) share one query and one rule: a plain-spelled
  gazetteer entry ("Banjar") always ranks ahead of a diacritic one ("Banjār")
  at roughly the same distance, via `stripDiacritics`, not just whichever the
  distance sort happened to return. `MaxDistSquared` (exported, reused by
  `vfs.resolveLocations` for anchor-folding — don't redefine it locally) is the
  ~50km acceptance radius, matching the outer bounding box `queryNearest`
  expands to — the two used to disagree, which silently dropped a valid match
  15-40km out instead of using it, fragmenting locations.

  **A place has two names, and which one you use depends on who reads it.**
  `Candidate`/`PlaceMatch` carry both:

  - `FullName` (`fullName`) — city, state and country spelled out,
    `Indore, Madhya Pradesh, India`. **Every list a person picks from shows
    this**: the `config` town suggestions and the review rename picker. Six
    rows reading `Springfield` are not a choice, and the state/country are the
    only thing that tells them apart. Whatever the user picks is what gets
    saved/named — WYSIWYG. `SearchByName` also **dedupes on it**: the gazetteer
    holds two `Banjar, West Java, Indonesia` a few hundred metres apart, and
    listing the same string twice is no more pickable than listing `Banjar`
    twice.
  - `DisplayName` (`disambiguate`) — the *smallest qualifier that makes this
    entry unique*, used where nobody chose anything: the folder `Lookup` names
    automatically. Spelling every folder out to three parts would put
    `Indore, Madhya Pradesh, India` on a library that only ever saw one Indore.

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
  every folder to fix a near-duplicate in the gazetteer. Cost to know about:
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
- `exiftool/` — bundled exiftool wrapper + verify.
- `path/` — path canonicalization / home-relative helpers.
- `utils/download.go` — atomic HTTP download (temp file + rename).
- `utils/copy.go` — `CopyFile` (atomic, same temp-file-then-rename pattern as
  `download.go`) and `CopyFiles` (size-capped batch, used today by the review
  TUI's preview-copy). Deliberately the same primitive a future Execute phase
  (the actual library move) would reuse for its copy half — `CopyFile` alone
  is what that phase would call directly.

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
- **Concurrency wall:** `lock.AcquireOutput` (`pkg/lock/`) takes an
  exclusive PID lock on the output dir, so only one scan *or* serve runs
  against a dir at a time. Log
  lines are already `sessionId`-tagged, so multiple sessions sharing one log
  file interleave cleanly (consumers filter by id) — logs are **not** the wall.
  The lock is. Running scans concurrently would need per-session isolation or a
  single owner that multiplexes sessions.
- `review.go`'s `layoutPresets` is a fixed 4-item list, not derived from
  whatever `Config.Rules` this session's scan actually started with — the
  first `[L]` press might not be a no-op. Revisit if users want custom presets
  instead of cycling a canned list. None of the 4 presets include `device` —
  it's real (`RuleDevice`) but currently reachable only via
  `--rules`/`config.yaml`, never via `[L]`.
- `vfs.resolveLocations`' anchor-fold radius is `location.MaxDistSquared`
  (~50km), not a separate per-user setting. Revisit if a single radius doesn't
  fit both dense and sprawling metros.
- `classifier.ParseMetadata`'s `map[string]any` decode (deliberately tolerant,
  see the package note above) is a different thing from `exiftool.releaseMeta`
  — the latter (the binary's checksum manifest, `exiftool.json`) is already a
  typed struct. Don't conflate the two if asked to "type the exiftool JSON."
