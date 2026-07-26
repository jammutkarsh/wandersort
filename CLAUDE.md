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

- `main.go` — builds `config.Defaults()` and calls `cli.Execute(cfg)`. **No
  logger here** — it's built later (see below).
- `internal/cli/` — cobra CLI. One file per command, plus `app.go` and `root.go`:
  - `app.go` — `Execute(cfg)`, **the package's only exported symbol**, plus the
    unexported `app` struct and everything hanging off it: DB/exiftool/resolver
    lazy-init (`initAppDB`, `initLocationResolver`, `initExiftool`,
    `ensureDependencies`, `closeDBs`), the install-progress hook, `syncAnchors`,
    and `tuiEnabled`. Nothing outside this package needs the app or its state,
    so nothing else is exported — `main.go` has one way in and no struct to
    assemble. `tuiEnabled` decides TUI vs plain line logging (plain when
    `--plain` is set or stderr isn't a terminal); the TUI draws to stderr so
    stdout stays clean for piping.
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
    fix it.
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
    Town inputs validate through `canonicalTown` (gazetteer exact-match).
    **The location DB downloads in the background, with no install screen**:
    `runConfigTUI` kicks off `initLocationResolver` in a goroutine, feeds its
    byte progress into the form's own row above the footer (`a.InstallProgress`
    → `tui.DownloadMsg`, in the same block the examples pin to), and hands the
    form a `gazetteer func() error` closure. Once the download finishes the row
    **persists as a dim `✓ … done` line** rather than vanishing (a bar that
    disappears the moment it fills reads as a failure); the `Finished` message
    carries no label, so `FormModel.Update` keeps the one from the byte reports
    — and ignores a `Finished` with no prior bytes, which is the only message
    an already-on-disk database sends, so it never gets mentioned at all.
    The Home & work step is the only one that needs the database, so it holds
    on `tui.Field.Await` (showing why) until the
    goroutine's channel closes; everything above it is answerable meanwhile.
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
    foreground (`Workflow.RunScan`, which canonicalizes and prunes the roots
    itself and returns the ones actually walked) so the user watches
    progress and the exit code reflects pipeline success; logs total elapsed
    time on completion. **There is no install screen: the scan starts
    immediately and missing dependencies download in the background**, with
    each pipeline phase waiting only on its own dependency (`workflow.Deps`) —
    scan/hash need nothing, exif blocks on exiftool, vfs blocks on the
    location DB. `runScanTUI` runs `a.ensureDependencies` in a goroutine with
    a checkpoint after exiftool (`exifReady`/`allReady` channels — closing
    them is the happens-before edge for `a.ExiftoolPath`/`a.LocationResolver`);
    the `Deps` closures log a "Waiting for …" `UserKey` line only when a phase
    actually stalls, and download byte progress renders as rows under the
    banner (`tui.InstallProgressMsg` → `ScanModel.viewDownloads`, persisting
    as `✓ done` like the config wizard's bar). In practice the location DB is
    already on disk — the (mandatory) `wandersort config` downloads it — so a
    first scan usually only fetches exiftool; the vfs gate is the fallback for
    a config run whose download failed. A download that fails mid-pipeline
    fails the session at the phase that needed it; files stay `HASHED` and the
    next run resumes. Anchor sync (`a.syncAnchors` → `vfs.SyncAnchors`) happens
    inside the `Location` dep closure — the earliest point the resolver exists,
    and vfs is the only phase that reads anchors. **`scan`/`review` are
    the only things that install dependencies** (`app.ensureDependencies`,
    which installs exiftool *then* the location DB — the small download
    unblocks the earlier phase; the big one has the whole pipeline to hide
    behind).
    **`scan` refuses to run before `wandersort config` has** — see
    `requireConfigured` in `root.go`. The config file every command creates is
    empty, so an unconfigured first scan silently builds its entire folder
    proposal (output path, rules, home/work anchors) from defaults, and the
    user then redoes the whole thing. The gate is absolute: `-o` and env vars
    don't satisfy it, because the anchors and rules aren't reachable that way.
    `--paths/-p` is repeatable + comma-friendly
    (`StringSlice`); `config.yaml`'s `rules` key (see below) controls the VFS
    folder depth for this scan's proposal — no CLI flag, set it via
    `wandersort config`. The plain path (`--plain`/non-TTY) keeps the simple
    order: blocking `ensureDependencies`, then `syncAnchors`, then
    the pipeline with `workflow.ReadyDeps`.
  - **There is no `anchor.go`.** The read side of home/work anchors is
    `app.syncAnchors` (reads the global config) delegating to
    `vfs.SyncAnchors` (resolves each name via `ResolveByName` — a guaranteed
    exact hit, since the wizard's `canonicalTown` validator only saves
    gazetteer spellings — and inserts the `ANCHOR_HOME`/`ANCHOR_WORK`
    `user_labels` row if this library lacks one). It lives in `core/vfs`
    because `resolveLocations` is the only thing that reads those rows: the
    phase that consumes an anchor owns writing it. The write side is the
    `config` wizard (`config.SaveGlobal`), and its canonical-spelling helpers
    `exactMatch`/`canonicalNameOf` sit in `config.go` next to their only caller.
  - `review.go` — `review` cmd: cobra wiring only (db-exists check, output
    lock, `vfs.ProposalSession`, the `--rebuild` guard via `approvedCount`,
    `vfs.BuildTree`), then hands off to `internal/review`. It also holds
    `newReviewScreen`, which builds the embedded screen `scan` swaps into.
    **The TUI itself lives in `internal/review/`** — see below.

- `internal/review/` — the bubbletea **full-tree view** TUI over the VFS
  proposal (issue #8), extracted from `internal/cli` because it was 60% of that
  package's lines while cobra wiring is the rest. Its whole exported surface is
  four functions in `run.go`:
  - `Run(ctx, Options)` — standalone full-screen review; writes the approved
    plan and runs the free-space check.
  - `AcceptAll(ctx, Options)` — `--yes`: take every suggestion, no TUI.
  - `Screen(ctx, Options) tea.Model` — the same review as an app-shell screen,
    so `scan` can swap into it inside its own program (`screen.go`, which
    finalizes in-program: on save it runs `vfs.Confirm` itself, then
    `tui.Switch(nil)` quits the shell).
  - `Outcome(m tea.Model) (confirmed, err, ok)` — how an embedded review ended.

  `Options` carries `DB`/`SessionID`/`Tree`/`Resolver`/`Log`/`OutputDir`; a nil
  `Resolver` just disables rename autocomplete. `copy.go` holds the unexported
  `copyFiles`/`copyFile` the peek feature uses (same atomic
  temp-file-then-rename pattern as `pkg/deps`); it moved here with the TUI
  because the preview is its only caller.

  The model (`review.go`): renders the whole hierarchy indented, alt-screen
  fullscreen, scrollable. Keys: `n`/`N` **hop to the next/previous row at
  the cursor's own depth** (`jumpSameDepth`), crossing into other branches
  by design — that's what makes `V` then `n``n` select one level across
  several months without arrowing through every folder's contents; stops at
  the ends, never wraps. `enter` accept suggestion, `r` rename (with
  ranked autocomplete — `Tab` fills the top match, `Ctrl-E` widens the search
  radius by another ~10km, typed text also prefix-matches previously
  confirmed `user_labels`), `p` peek (copies up to 250MB of the folder's
  files into a temp dir via `copyFiles` and opens that folder —
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
  TUI never produces itself but the tree-splice logic in `vfs.Confirm` still
  guards against). An earlier version
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
  so a clone is cheap.
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
  covering anything deeper. Both undo via `[u]`.
  `c` **save & exit** (the only thing that writes — `q` discards, so `q`
  with pending edits warns once and needs a second `q`; `hasEdits` derives
  "pending" from the rows and the undo snapshot rather than a flag any edit
  path could forget to set). `--yes` accepts every suggestion
  non-interactively; `--rebuild` re-runs `vfs.Run` with the current
  `config.yaml` `rules` *before* reviewing, so a config change
  re-proposes the hierarchy without a re-scan or re-hash (editing
  `config.yaml` alone, without `--rebuild`, changes nothing until the next
  `wandersort scan`).
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
  the old two-space indent plus a `└ ` on every level made sibling and child
  look alike. An unaccepted suggestion renders as `⇢ name` in `tui.Attn`
  (amber = still wants a keypress), not as dim `(N files, suggested: …)`
  parentheses next to the count.
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

  - `report.go` — read-only per-session summary (opens its own RO sqlite conn).
    Lists every `scan_sessions` row (newest first), each with its own
    scanned/hashed/duplicate counts — duplicates are scoped to that session's
    own files via `scan_session_id` (two sessions over different roots can
    have unrelated duplicate pictures, so counts never bleed across sessions).
    Errors out if the DB has no sessions yet; flags the newest session as
    partial if its status isn't terminal (`COMPLETED`/`FAILED`/`CANCELLED`).
    Default output is a bordered table; `--vertical`/`-x` (psql `\x`-style)
    prints each session as expanded label:value pairs for narrow terminals.
  - `issue.go` — `issue` cmd: zips the log (renamed
    `wandersort.log`) + `about.txt`; db opt-in via `--include-db` (holds paths/GPS).
  - `reset.go` — wipe scan data (confirm prompt unless `--yes`).
  - `help.go` — custom lipgloss-styled help renderer. Kept in `cli` (unlike
    `lock.go`) since it's a one-off cobra `SetHelpFunc`, not reusable
    logic another entry point would need.
  - `internal/cli` holds **only** `app.go` + `root.go` + one file per
    subcommand (plus the `help.go` exception above). **No single-function
    files**: the old `tui.go` (just `tuiEnabled`) and `anchor.go` were folded
    into `app.go` and `config.go`/`core/vfs` respectively. Everything else that
    used to live here moved
    out to its own package so a future TUI entry point can reuse it:
    - `pkg/lock/` — all wandersort file locking: generic PID/O_EXCL
      acquire/reclaim mechanics (`acquire`, `Lock`, `ErrHeld`) plus the two
      domain wrappers — `AcquireOutput` (one scan per output dir, styled
      "already running" message) and `AcquireInstall` (install coordination
      across scan/review: `ensureDependencies` tries non-blocking first
      so it can log a "waiting…" line, then blocks) — and the lock filenames (`OutputFileName`, `InstallFileName`).
      `ensureDependencies` (in `app.go`) tries the install lock non-blocking
      first so it can log a `UserKey`-tagged "waiting for another process…"
      line before falling back to the blocking acquire — without that, a
      scan waiting behind an in-progress install just looks hung.
      Only `cli` uses locking today, but the mechanics are generic, so it
      lives in `pkg/` for reuse by other entry points.
    - Styling (help renderer, lock messages, error output) comes from
      `pkg/tui`'s theme — there is no separate `pkg/style` any more; the old
      one was folded into `pkg/tui/theme.go` so full-screen and plain output
      share one palette.

Config precedence: **flag > env > config file > default**, entirely inside
`config.Resolve` (`pkg/config/config.go`) — the single place all four layers
meet. `internal/cli/root.go`'s `flagOverridesFrom` builds the flag layer from
cobra's `cmd.Flags()` (only for the settings `Resolve` knows about:
`output-path`, `workers`, `collapse-levels`, `home-work-date-only`,
`merge-same-location-days` — checking `.Changed` so an unset flag reads as
`nil`, not its zero value); `Resolve` reads the env layer itself via
`os.Getenv` (`OUTPUT_PATH`, `WORKERS`, …) and the file layer via
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
set by `Resolve`) — `rules`, the three toggle bools, and
`home-work.home`/`home-work.work` are only read from the file once
`output-path` is present, since a `bool` field can't otherwise tell "key
absent" from "explicit false" the way `Resolve`'s flag/env layers can (a nil
pointer vs. a real value). `home-work.*` has no flag or env of its own —
`Resolve` doesn't touch it at all; `app.syncAnchors` reads it straight via
`config.LoadGlobal`.

## Core pipeline (`pkg/core/`)

- `workflow/` — orchestrator. `RunScan` runs the `runSession` phase loop
  (scan→hash→exif→score→vfs) synchronously on the calling goroutine, so a CLI
  invocation streams progress and blocks until the scan finishes. It runs
  `scanRoots` first — canonicalize, drop duplicates, prune any root nested under
  another (O(n) after a lex sort) — returning the roots actually walked
  alongside the session ID. That pruning used to live in an HTTP layer
  (removed along with `serve` — this is now a single-entry-point CLI), which
  meant `wandersort scan` reached through `internal/api` to get at it. `NewWorkflow` takes a
  `Deps` — two blocking getters for the downloadable dependencies — instead of
  a resolver and exiftool path: the exif and vfs components are built lazily
  inside their phase closures, calling `deps.Exiftool()`/`deps.Location()`
  right before running, so a first-ever TUI scan walks and hashes while the
  downloads are still going. Plain-console scans, which install everything up
  front, wrap the values in `workflow.ReadyDeps`. `claimRoots` rejects a new
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
  proposal set wholesale (safe to call again mid-review — see `review.go`'s
  `--rebuild` flag). `Config.Rules` (below Year/Month)
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
  `review.go` (issue #8's reconcile core, read by the CLI TUI) exposes the
  proposal as a directory tree the reviewer edits
  before `Confirm` writes it back: `BuildTree` also carries one exemplar
  GPS coordinate per location node (`Node.Lat/Lon`) for the TUI's expand-radius
  rename, and `FilesUnder` lists a node's source files for the preview-copy
  feature. **Suggestions attach by path, not by depth:** `dirFor` records the
  folder it emitted for the location level as `virtual_fs_entries.suggestion_dir`,
  and `BuildTree` hangs the suggestion + GPS off exactly that node. The old
  fixed `suggestionDepth = 2` assumed location was Rules' first level, so
  any other order (`rules: [device, location]`, or a `date` level in front)
  smeared every file's suggestion onto whatever shared Device/Day node sat at
  depth 2 — reported as "one suggestion across the whole tree". No
  `suggestion_dir` (no location level in this proposal) now means no suggestion
  node at all, rather than a misplaced one. `suggestion_dir` is a column of
  **003's `CREATE TABLE`**, not its own migration — the pre-tag rule (no tag
  yet, so no users) says edit the existing migration rather than stack an
  `ALTER` on it. The cost is that `migrations.Run` tracks versions
  individually: a database where 003 is already recorded will never get the
  column, and the vfs phase then fails at runtime on the INSERT. **Deleting
  `.wandersort.db` is the fix**, and `wandersort reset` is not — the file
  itself has to go. Same applies to any future edit of an already-run
  migration. **`Confirm` merges, it doesn't reject:** two nodes renamed to the
  same final path collapse onto one folder (e.g. two unresolved date clusters
  turning out to be the same place) — this used to be an error before a real
  user hit exactly that case. `Node.MergedIDs` is the other merge path: nodes
  the review TUI folded away are absent from the submitted tree entirely, so
  their IDs — and, via `remapUnderMerged`, anything below them — remap onto
  the survivor's path from there.

## Supporting packages (`pkg/`)

- `tui/` — the full-screen TUI kit: adaptive palette + semantic styles
  (`theme.go`), the Docker-buildkit-style `StageList` step stack shared by
  scan and its dependency install (`stagelist.go` — stage rows with right-aligned
  elapsed times, a progress bar and a live per-file tail nested under the
  running stage), the app `Shell` (one alt-screen program, `Switch` swaps
  screens without a terminal-restore flash), the `config` wizard
  (`form.go` — `Field.Example` blocks above the footer, `Field.Describe` for a
    description that depends on the answer under the cursor — prose belongs
    there, not in the example, which renders in a narrow column and truncates;
    descriptions word-wrap to the body width (`descriptionBlock`), so hard line
    breaks in them are re-flowed — `Field.Await` to hold a
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
  attr (incl. `sessionId`) — that's what `issue` ships. Never stdlib `log`.
- `location/` — offline reverse-geocode resolver + its own sqlite DB. `Setup()`
  downloads DB+meta if missing (idempotent) and is the only place that prints
  a user-facing line about it; `New`'s checksum verification is **not**
  `UserKey`-tagged, since it runs on every command that opens the resolver and
  printed a checksum on every single run. A mismatch is still a hard error. `exiftool.Setup()` is the same idea
  for the binary. Both are called lazily by `app.ensureDependencies`. `Lookup`
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
- `deps/` — **the one place a downloadable dependency is fetched.**
  `Download(ctx, dest, url, wantSHA256, onProgress)` writes atomically (temp
  file + rename), reports byte progress, and — when `wantSHA256` is non-empty —
  verifies the digest and removes `dest` on mismatch. `SHA256File` is exported
  for the callers that verify at a different moment. `exiftool.Setup` passes the
  digest from its release manifest; `location.Setup` passes `""` because the
  expected hash ships in the metadata file it downloads next, and `location.New`
  checks it when opening. **There is no `pkg/utils`** — a package named for
  nothing in particular is where unrelated helpers accumulate; `download.go` and
  `hash.go` had exactly these two callers, and `copy.go` had one (the review
  TUI's preview), so it went to `internal/review` as unexported `copyFiles`.

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
go build ./... # quick compile check
```

## Open cleanup notes (not yet done)

- `report.go` builds a raw sqlite DSN + inline SQL in the CLI layer, bypassing
  `pkg/db`. Read-only by design, but a layering shortcut — move it next to
  `db.ResetAll` if it grows.
- **Concurrency wall:** `lock.AcquireOutput` (`pkg/lock/`) takes an
  exclusive PID lock on the output dir, so only one scan runs
  against a dir at a time. Log
  lines are already `sessionId`-tagged, so multiple sessions sharing one log
  file interleave cleanly (consumers filter by id) — logs are **not** the wall.
  The lock is. Running scans concurrently would need per-session isolation or a
  single owner that multiplexes sessions.
- `vfs.resolveLocations`' anchor-fold radius is `location.MaxDistSquared`
  (~50km), not a separate per-user setting. Revisit if a single radius doesn't
  fit both dense and sprawling metros.
- `classifier.ParseMetadata`'s `map[string]any` decode (deliberately tolerant,
  see the package note above) is a different thing from `exiftool.releaseMeta`
  — the latter (the binary's checksum manifest, `exiftool.json`) is already a
  typed struct. Don't conflate the two if asked to "type the exiftool JSON."
