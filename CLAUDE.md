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
4. **VFS** — propose a destination hierarchy for every live master in the
   library (not just the session's), using the user's prior folder-naming as
   context when EXIF is absent. Nothing on disk is touched.

## Entry point & CLI

- `main.go` — builds `config.Defaults()`, constructs `cli.App{Config}`, calls
  `app.Execute()`. **No logger here** — it's built later (see below).
- `internal/cli/` — cobra CLI. One file per command:
  - `root.go` — root cmd, `App` struct, DB/exiftool/resolver lazy-init helpers,
    viper setup, config-override resolution. **`PersistentPreRunE` is the single
    place** flags/env/config-file are resolved (`loadGlobalConfigFile` then
    `applyOverrides`) and the logger is built — so `--debug` / `--output-path` /
    env vars / `config.yaml` all take effect before any logging or DB work.
    Don't rebuild the logger in `main.go`. `loadGlobalConfigFile` also
    **creates** `~/.wandersort/config.yaml` from a commented template on the
    very first command of any kind (not just `setup`/`config`) via
    `config.EnsureGlobalConfigFile` — the file is always there to find and edit.
    **A config file that doesn't parse is a warning, not a failure**:
    `loadGlobalConfigFile` returns the warning text (not an error), the run
    continues on defaults, and it's logged with `UserKey` once the logger
    exists so it reaches the log file too, not just the terminal. Hard-failing
    would let one stray tab in an all-optional settings file brick every
    command — including `wandersort config`, the one that opens the file to
    fix it. Viper only commits parsed values on success, so nothing half-read
    leaks into the settings.
  - `config.go` — `config` cmd: opens the global config file in `$EDITOR`.
    Prints it to stdout instead when `--print`/`-p` is given, when stdout
    isn't a terminal (`wandersort config | grep …`, `> file`), or when
    `$EDITOR` is unset — launching a full-screen editor into a pipe is never
    what the caller meant. Just a shortcut to a file `loadGlobalConfigFile`
    already guarantees exists.
  - `scan.go` — `scan` cmd (the pipeline). Runs **synchronously** in the
    foreground (`Service.RunScan` → `Workflow.RunScan`) so the user watches
    progress and the exit code reflects pipeline success; logs total elapsed
    time on completion. `--paths/-p` is repeatable + comma-friendly
    (`StringSlice`); `--group-by` (also settable via `config.yaml`, see below)
    controls the VFS folder depth for this scan's proposal. Calls
    `syncAnchorsFromConfig` before the pipeline so any globally-saved home/work
    anchor exists in *this* library's DB before VFS runs.
  - `serve.go` — `serve` cmd, HTTP API (gin) + swagger. Same workflow,
    long-lived; also takes `--group-by`.
  - `setup.go` — downloads exiftool + location DB, then (interactively, once)
    prompts for home/work anchors via `promptAndSaveAnchors` — see `anchor.go`.
    **Optional** — scan/serve auto-install missing deps via
    `App.EnsureDependencies`. Uses a *non-blocking* install lock: if a
    scan/serve is already installing, setup steps aside.
  - `anchor.go` — home/work anchor plumbing, split into a write side and a
    read side because anchors are a **global** setting but `resolveLocations`
    needs them per-library:
    - `promptAndSaveAnchors` (called from `setup`) — interactive, skipped
      outside a TTY. Names come from `location.PlaceMatch` (`SearchByName`),
      never free text, via `pickPlace`'s numbered-picker loop (typing the exact
      match name auto-selects, no separate number entry needed). Work defaults
      to "same as Home" (`sameAsHomeAnswer`, a `[Y/n]` prompt) since most
      people don't commute across metros. Saves via `config.SaveAnchors`.
    - `syncAnchorsFromConfig` (called from `scan`) — reads the global config,
      resolves each anchor's coordinates via `ResolveByName` (guaranteed exact
      hit — the name came from the gazetteer in the first place), and inserts
      an `ANCHOR_HOME`/`ANCHOR_WORK` `user_labels` row if this library doesn't
      have one yet. Idempotent, silent, and a no-op with nothing globally set.
  - `review.go` — `review` cmd: bubbletea **full-tree view** TUI over the VFS
    proposal (issue #8). Renders the whole hierarchy indented, alt-screen
    fullscreen, scrollable. Keys: `enter` accept suggestion, `r` rename (with
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
    merged. Single-level undo (`u`) snapshots the whole tree
    (`deepCloneNodes`) before the splice, since a structural edit can't be
    undone by restoring per-row name strings the way a plain rename-merge
    could. `d`/`D` **remove nesting the reviewer doesn't want**, both anchored
    on the cursor row — nothing acts tree-wide:
    - `d` (`dropFolder`) drops **that one folder**, lifting its children onto
      its parent. Refused on a top-level (Year) row: its files would land in
      the library root.
    - `D` (`flattenFolder`) collapses **everything below** that folder into
      it, so the whole subtree's files sit directly in it and the folder
      itself stays. Works on a Year, since the Year survives to hold them.
      `2023/April/Indore/Apple iPhone 13` flattened at April is
      `2023/April` with all ten files. `FileCount` is unchanged — it already
      counted the subtree.

    Both record the removed IDs (plus anything already folded into them) on
    the surviving node's `MergedIDs`, so files sitting directly in a removed
    folder remap onto it — same machinery as merge, with `remapUnderMerged`
    covering anything deeper. Both undo via the same `[u]` snapshot. `L`
    cycles a fixed set of
    group-by presets and **rebuilds the whole proposal in place**
    (`vfs.New(...).Run` + `BuildTree` — safe mid-review since VFS only reads
    already-hashed masters and replaces the proposal wholesale; resets any
    in-progress renames, since a different depth means different node IDs),
    `c` **save & exit** (the only thing that writes — `q` discards, so `q`
    with pending edits warns once and needs a second `q`; `hasEdits` derives
    "pending" from the rows and the undo snapshot rather than a flag any edit
    path could forget to set). `--yes` accepts every suggestion
    non-interactively; `--rebuild` re-runs `vfs.Run` with the current
    `--group-by`/`config.yaml` *before* reviewing, so a config change
    re-proposes the hierarchy without a re-scan or re-hash (`--group-by` alone,
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
    **A rejected merge (`statusIsErr`) renders in `style.Warn`, not
    `style.Dim`** — a rejection used to look identical to routine status
    text, easy to miss (a reported "merge doesn't work" turned out to be
    either a missed capital-`V` requirement or exactly this — the rejection
    message was there, just easy to overlook). **Visual selection is a
    whole-row background highlight (`style.Selected`), not a marker
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
    `lock.go`/`style.go`) since it's a one-off cobra `SetHelpFunc`, not reusable
    logic another entry point would need.
  - `internal/cli` holds **only** `root.go` + one file per subcommand (plus the
    `help.go` exception above) — everything else that used to live here moved
    out to its own package so a future TUI entry point can reuse it:
    - `pkg/lock/` — all wandersort file locking: generic PID/O_EXCL
      acquire/reclaim mechanics (`acquire`, `Lock`, `ErrHeld`) plus the two
      domain wrappers — `AcquireOutput` (one scan/serve per output dir, styled
      "already running" message) and `AcquireInstall` (install coordination
      across scan/serve/setup: blocking for scan/serve, non-blocking for
      setup) — and the lock filenames (`OutputFileName`, `InstallFileName`).
      `EnsureDependencies` (in `root.go`) tries the install lock non-blocking
      first so it can log a `UserKey`-tagged "waiting for another process…"
      line before falling back to the blocking acquire — without that, a
      scan/serve waiting behind an in-progress install just looks hung.
      Only `cli` uses locking today, but the mechanics are generic, so it
      lives in `pkg/` for reuse by other entry points.
    - `pkg/style/` — shared lipgloss palette/styles (`style.Err`,
      `style.Success`, `style.Warn`, `style.Header`, `style.Dim`,
      `style.Desc`, `style.Selected` — background highlight for a
      whole-line multi-row selection). One place to change a color; used by
      `cli` today, meant for a future TUI too.

Config precedence: **flag > env > config file > default** (viper's normal
config-file precedence — see `loadGlobalConfigFile`/`applyOverrides` in
`root.go`). Env names are the uppercased flag; `AutomaticEnv` covers the
hyphen-free flags (`WORKERS`, `PORT`, …), and the one hyphenated flag is bound
explicitly (`--output-path` → `OUTPUT_PATH` via `v.BindEnv`). Keep new flags
hyphen-free so they need no explicit bind. Defaults come from `pkg/config`.

The config file is `~/.wandersort/config.yaml` (`pkg/config/global.go`),
created from a commented template the first time *any* command runs
(`config.EnsureGlobalConfigFile`) — edit it with `wandersort config`.
`output-path`, `workers`, `debug`, `group-by` are plain viper keys read straight
out of it (no dedicated struct — same `v.GetX(flagName)` calls `applyOverrides`
already makes work regardless of source). Template footgun to know about:
each commented-out line shows the flag's own *default* value (`# debug:
false`) — uncommenting without also changing the value is a no-op, and reads
like the setting doesn't work. `anchors.home`/`anchors.work` are the
one thing viper doesn't manage: `pkg/config.Global` reads them, and
`SaveAnchors` writes them via targeted YAML-node surgery (not a full
struct-marshal) so every other key, and every comment, survives — a struct
round-trip would silently erase both.

## Core pipeline (`pkg/core/`)

- `workflow/` — orchestrator. Two entry points over the same `runSession` phase
  loop (scan→hash→score→vfs): `RunScan` (synchronous, CLI) and `SubmitScan`
  (background goroutine, `serve`; `Close()` waits). `claimRoots` rejects a new
  session whose roots overlap an in-flight session's (in-memory — the output
  lock guarantees one process). `helpers.go` = session status/finalize writes.
  `run`'s `"%s phase took %s"` log carries `logger.UserKey` — every phase's
  timing is visible without `--debug` (it used to be developer-only, another
  instance of the same missing-`UserKey` class of bug as the checksum logs).
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
- `hasher/` — phase 2. BLAKE3 over full bytes + exiftool EXIF (the full tag
  set the VFS needs — exiftool runs exactly once per file, at hash time).
  A failed hash clears the file's stale metadata row. **Known gap:** full-byte
  hash means pixel-identical files with differing metadata land in separate
  groups. Persists `exif_creation_date` alongside `exif_create_date` — a real
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
- `vfs/` — phase 4. Proposes destinations for every live master in the library
  from the persisted metadata (never re-reads files); each run replaces the
  proposal set wholesale (safe to call again mid-review with a different
  `Config` — see `review.go`'s `[L]` key). `Config.GroupBy` (below Year/Month)
  is `location`/`orientation`/`device`/`media` in any order, or empty for a
  flat `Year/Month` — set via `--group-by`/`config.yaml` (CLI) or cycled live
  in the review TUI. `date` is a Day level, so the full
  `Year/Month/Day/Location/Device/Orientation/Media` shape is
  `--group-by date,location,device,orientation,media`; when a `date` level is
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
  is turned into a nil `GroupBy` — `workflow` and `review --rebuild` both go
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
  fixed `suggestionDepth = 2` assumed location was GroupBy's first level, so
  any other order (`--group-by device,location`, or a `date` level in front)
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

- `config/` — `Defaults()` (hardcoded config only, no env reads) plus
  `global.go`: the `~/.wandersort/config.yaml` machinery — `GlobalConfigPath`,
  `EnsureGlobalConfigFile` (writes `configTemplate` if missing), `LoadGlobal`,
  and `SaveAnchors` (see the config-precedence note above for why anchors get
  a hand-rolled YAML-node writer instead of a plain struct marshal).
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
  text) to avoid spam. `--debug` bypasses both filters and shows every record.
  The **JSON file** handler keeps timestamp + source (`AddSource`) and every
  attr (incl. `sessionId`) — that's what `report-issue` ships. Never stdlib `log`.
- `location/` — offline reverse-geocode resolver + its own sqlite DB. `Setup()`
  downloads DB+meta if missing (idempotent); `exiftool.Setup()` is the same idea
  for the binary. Both are called lazily by `EnsureDependencies`. `Lookup`
  (single best match, cached/singleflighted) and `Candidates` (ranked list for
  the review TUI's rename picker) share one query and one rule: a plain-spelled
  gazetteer entry ("Banjar") always ranks ahead of a diacritic one ("Banjār")
  at roughly the same distance, via `stripDiacritics`, not just whichever the
  distance sort happened to return. `MaxDistSquared` (exported, reused by
  `vfs.resolveLocations` for anchor-folding — don't redefine it locally) is the
  ~50km acceptance radius, matching the outer bounding box `queryNearest`
  expands to — the two used to disagree, which silently dropped a valid match
  15-40km out instead of using it, fragmenting locations. `ResolveByName`
  forward-geocodes a name to coordinates (exact match — expects a name that
  came from `SearchByName`, e.g. an anchor); `SearchByName` is the prefix
  search behind the `wandersort setup` anchor picker, so a typed-then-selected
  town name can never drift from what the gazetteer actually calls it.
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
  whatever `Config.GroupBy` this session's scan actually started with — the
  first `[L]` press might not be a no-op. Revisit if users want custom presets
  instead of cycling a canned list. None of the 4 presets include `device` —
  it's real (`GroupByDevice`) but currently reachable only via
  `--group-by`/`config.yaml`, never via `[L]`.
- `vfs.resolveLocations`' anchor-fold radius is `location.MaxDistSquared`
  (~50km), not a separate per-user setting. Revisit if a single radius doesn't
  fit both dense and sprawling metros.
- `classifier.ParseMetadata`'s `map[string]any` decode (deliberately tolerant,
  see the package note above) is a different thing from `exiftool.releaseMeta`
  — the latter (the binary's checksum manifest, `exiftool.json`) is already a
  typed struct. Don't conflate the two if asked to "type the exiftool JSON."
