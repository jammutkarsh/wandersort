<!--
Copyright (c) 2026 Utkarsh Chourasia

This file is part of WanderSort.

SPDX-License-Identifier: AGPL-3.0-or-later
-->

# WanderSort Implementation Report

Factual description of the current implementation as of 2026-09-15. No suggested changes.

## 1. Data model

All state lives in one SQLite DB per output path (`.wandersort.db`), which also holds that library's settings (`library_settings`); `~/.wandersort/` keeps only the logs, the location database, the downloaded binaries and the recently-used library list (`libraries`). Schema is versioned via numbered Go migrations (`pkg/db/migrations/001_scanner.go`, `002_file_metadata.go`, `003_vfs.go`), tracked in a `schema_migrations(version, run_at)` table. Rule: edit an existing migration's `CREATE TABLE` rather than adding an `ALTER` (no tag/users yet) — pre-existing DBs on an older schema just get deleted.

**`file_registry`** (001) — one row per physical file (`file_dir`,`file_name` unique):
- `file_size`, `file_modified_at`, `volume_uuid` (nullable)
- `discovered_at`, `last_seen_at`
- `media_type` CHECK(`IMAGE|VIDEO|SIDECAR|RAW|UNKNOWN`), `file_extension`
- `scan_status` CHECK(`DISCOVERED|ANALYZING|ANALYZED|ERROR`)
- `file_origin` default `SOURCE`
- `placed` — 1 once `execute` has landed this file at its target (copy or move alike), 0 otherwise. A fact about the file, not the plan: never cleared, and untouched by a re-scan or a settings replan. The scorer always elects a placed file as the master of its hash, and the VFS phase never re-proposes or deletes its plan row.
- No soft delete: a vanished file's row is hard-deleted at once, along with its `file_metadata` and `virtual_fs_entries` rows.

**`file_metadata`** (002) — one row per file, FK `file_id → file_registry(id) ON DELETE SET NULL`, unique on `(file_hash, file_id)`:
- `file_hash` (`blake3:<hex>`)
- `exif_image_width/height`, `exif_orientation`, `exif_gps_latitude/longitude`
- `exif_make`, `exif_model`
- `exif_date_time_original`, `exif_create_date`, `exif_creation_date` (QuickTime composite, iOS), `exif_media_create_date` (QuickTime track-level, lowest priority)
- `is_screenshot` (0/1)
- `is_master` (0/1) — every file master by default, scorer demotes losers
- `created_at`

**`folder_nodes`** (003) — the plan's folder tree (spec D12): `id`, `parent_id`, `name`, `level` (year, month, screenshots, fallback, orphan, or a Rules level). A folder's path is its ancestors' names joined. Unused folders are hard-deleted (`pruneFolders`); ids are never reused.

**`virtual_fs_entries`** (003) — one row per master, unique on `file_id`:
- `source_path`, `node_id` (its folder), `target_path` (that folder's path + file name)
- `cluster_id` (nullable)
- `status` CHECK(`PROPOSED|APPROVED|DONE|ERROR`)
- `location_node_id` — the folder the location rule level made, for GPS-node lookup; `ON DELETE SET NULL`
- `taken_at` — the file's *folder* date (cluster start, not raw capture time)
- `error` — failure text for ERROR rows
- `created_at`

**`user_labels`** (003) — reviewer-typed rename memory: `label`, `kind` CHECK(`EVENT|SAVED_PLACE` — `SAVED_PLACE` legacy, nothing writes it now), `time_start/end`, `gps_lat/lon`, `created_at`.

No `scan_sessions` table exists — removed. No run/session identity is persisted anywhere; concurrency safety is one OS advisory lock per output dir (`lock.AcquireOutput`).

Sidecar files (`.AAE`) are ordinary `file_registry` rows (`media_type = SIDECAR`) — no separate table.

Config: one `library_settings` row in the library's own database (rules, three toggle bools, saved places), read by `openLibrary`, written by the wizard. No config file, no env vars, no per-setting flags — `--output-path` alone picks which library.

## 2. Scanning and hashing

**Scan** (`pkg/core/scanner`): bounded worker pool walks each root with `filepath.WalkDir`. Files identified by absolute `(file_dir, file_name)` — upsert `ON CONFLICT (file_dir, file_name)`. Each root's volume UUID resolved once (`pkg/volume`) and stamped on every file under it. A file's `scan_status` resets to `DISCOVERED` on upsert when size or mtime changed, or the prior status was `ANALYZING`/`ERROR`, or `--force` was passed; otherwise it's left alone. After a clean walk, `sweep` hard-deletes rows under that root whose `last_seen_at` is still older than the walk's start time — no session identity needed, pure wall-clock cutoff — cascading `virtual_fs_entries` → `file_metadata` → `file_registry` in one transaction, immediately, no retention window. A placed file is never a candidate: its `file_dir`/`file_name` were repointed at a library-relative path once it landed, and that path is never under an absolute scan root.

**Hashing** (`pkg/core/metadata`): one worker per claimed file does a full-byte BLAKE3-256 hash, then runs exiftool on the same file immediately after (page-cache locality — one merged phase, not two). Hashing streams through a pooled 1 MiB buffer via `io.CopyBuffer` (wrapped to defeat `os.File`'s `WriteTo` fast path). File identity for dup detection = `file_hash` value, stored as `blake3:<hex>`. Every file is read in full; there is no unique-size skip. Byte reads are throttled by a `semaphore.Weighted` budget keyed on storage class (`volume.Class`: rotational=1 concurrent, removable=2, unknown=4, solid-state/network=full budget), capped at `min(workers,16)`; exiftool itself runs outside that gate (CPU-bound, one process per worker). Sidecars (`.AAE`) are hashed but never sent to exiftool.

State machine: `DISCOVERED → ANALYZING → ANALYZED`, or `ERROR` on hash failure (stale metadata row deleted). No `HASHING`/`HASHED` states — removed with the phase merge.

## 3. Metadata

Extracted per file via `exiftool -json -n`, parsed by `classifier.ParseMetadata` into `classifier.CommonMetadata` (tolerant map decode — one bad tag doesn't fail the whole file). Fields stored on `file_metadata`: image width/height, orientation (raw int), GPS lat/lon, make, model, and four separate date-time strings (`exif_date_time_original`, `exif_create_date`, `exif_creation_date`, `exif_media_create_date`) — all stored **raw, as exiftool printed them**, no resolution or normalization at write time. `is_screenshot` is a boolean derived from exiftool's Description/UserComment containing the literal string `Screenshot`.

Resolution to a single usable timestamp (`takenAt`), a device string, and swapped width/height for rotated orientations (5–8) happens later and only in memory, during the VFS phase's `deriveAll` pass — never written back to `file_metadata`. `deriveAll`'s timestamp ladder, in priority order: `exif_date_time_original` → `exif_creation_date` (offset stripped via `stripOffset`, since every other timestamp in the system is naive local wall-clock) → `exif_create_date` → file mtime.

An extraction failure is not a file failure: EXIF columns are persisted empty/NULL and the file still reaches `ANALYZED`.

## 4. Scoring

Runs after metadata, before VFS (`pkg/core/scorer`). Groups live `file_metadata` rows by `file_hash` having `COUNT(*) > 1`. Within each group, computes `perFileScore(path)`:
- `+4` if the filename stem is not camera-pattern (`^(_?[A-Z]{1,5}_?)\d+(_\d+)*$`) and contains a letter
- `+3` if the filename is date-prefixed (`YYYYMMDD_`, `YYYY-MM-DD_`, `YYYY_MM_DD_`)
- `+2` if the immediate parent directory name is not generic (`classifier.IsGenericDirName`)
- `-3` if the filename has a duplicate-copy suffix (`(1)`, `copy`, `- Copy`, etc.)

Highest score wins; ties broken by shortest combined `dir+name` length; further ties keep the first row in `(file_dir, file_name)` order (deterministic across re-scans, independent of AUTOINCREMENT). The winner gets `is_master=1`; every other group member gets `is_master=0`. **A group member with `file_registry.placed = 1` wins outright, no scoring at all** — a file that already landed at its target beats any path heuristic about where a not-yet-placed duplicate merely looks like it should live. Non-master copies are never deleted or moved by this phase — they stay in `file_registry`/`file_metadata` untouched, simply excluded from `is_master=1` reads downstream (a non-placed loser is later hard-deleted by `execute`'s end-of-run cleanup once the placed file's hash is known — see §7). A re-scan that shrinks a group to one live survivor re-promotes it (`is_master=1` again) before grouping, since it would otherwise never win a `COUNT(*)>1` group again.

## 5. Planning (`pkg/core/vfs`)

`vfs.Propose(ctx, db, log, geo, appCfg, outputDir)` is the phase entry point: loads masters, resolves saved-place anchors from the library's settings via `location.Resolver.BuildAnchors`, calls `Plan`, then `persist`. `Plan(ctx, masters, cfg, geo, log)` is a pure function of `(masters, config)` — no DB/filesystem/clock inside it — running eight sequential passes over an in-memory `[]masterFile` slice (loaded via one SQL query, `ORDER BY file_dir, file_name` for determinism):

1. **`deriveAll`** — parses EXIF strings into `takenAt` (see timestamp ladder above), swaps width/height for orientation 5–8, computes `device = deviceName(make, model)` (dedupes `"Canon Canon EOS R5"` → `"Canon EOS R5"`).
2. **`resolveLocations`** — reverse-geocodes GPS via a bounding-box + squared-Euclidean-distance query against a local geonames SQLite DB (two-pass: ~10km then ~50km box), cached on a ~1.1km-rounded grid; folds any hit within `location.MaxDistSquared` (~50km) of a saved-place anchor into that anchor's name, setting `atSavedPlace=true`.
3. **`clusterAndSpill`** — sorts all masters by capture time, starts a new cluster on any gap > `ClusterGap` (default 12h). Clusters ≤24h (`maxFolderSpan`) give every member the cluster's start time as `folderDate` (read via `folderTime()` everywhere downstream). A cluster with zero located members gets a dated `eventSegment` string (e.g. `"03-05"` or `"Jun_30-Jul_01"`); a mixed or fully-located cluster decides nothing extra.
4. **`applyNameCase`** — title-cases derived location/device names (`iphone`→`iPhone` via whitelist).
5. **`unsuppressMixedSavedPlaces`** — if `SavedPlacesDateOnly` and a day/parent-folder mixes saved-place and non-saved-place files, forces the saved-place city folder back on (`keepLocationFolder=true`) so the day never shows a bare pile next to a nested folder.
6. **`markUnknownLocations`** — GPS-less files whose sibling location-parent folder has other, located files get `location="Unknown"`.
7. **`mergeSameLocationDays`** — fixed-point loop that labels runs of ≥2 consecutive same-`(year,month,location)` days as a range (`dayOverride`, e.g. `"02_04"`), breaking any day where not every file agrees on one label, re-running until stable.
8. **`buildTargets`** — for each master, builds the actual folder path via `dirFor`: `Year/Month` (`01_January` format, numeric-prefixed) always first; a screenshot short-circuits straight to `Year/Month/Screenshots`; otherwise walks `cfg.Rules` in configured order (`location`, `date`, `device`, `orientation`, `media` — any subset/order), skipping levels `uninformativeLevels` says resolve to one value library-wide when `CollapseLevels` is set (only device/orientation/media ever collapse). `segmentFor` picks each level's segment string; the location level records the path-so-far into `masterFile.locationDir`. Then assigns `targetPath = dir/filename[_N]ext`, collision suffix `_N` assigned sequentially in `(file_dir,file_name)` order, case-insensitively deduped. `captureDirs` is the one exception: files sharing a filename stem in the same source dir (after folding `IMG_E`/`IMG_O` role markers) and agreeing within a 5-minute window and on device, form a "capture group" whose leader (screenshot > non-sidecar > located > canonical-name priority) donates its directory, `locationDir`, and `folderTime()` to every member — so a `.AAE` sidecar follows its photo instead of landing by its own file-mtime. Unpaired sidecars go to a flat `orphan/` folder, bypassing Rules entirely, and are excluded from the review tree.

Four of the eight passes (`deriveAll`, `resolveLocations`, `applyNameCase`, the per-file half of `buildTargets`) run on a bounded worker pool (`forEachMaster`, contiguous chunks of 512) because they write index-disjointly to `masters[i]`; the other four are strictly sequential because they build library-wide maps.

`persist` reads which rows are already decided (`status != PROPOSED`, still a live master) first, deletes every `PROPOSED` row plus any decided row whose file is no longer a live master, then bulk-inserts the new proposal in chunks of 50 rows via the async `BulkWriter`, and flushes before returning. `virtual_fs_entries(file_id)` is uniquely indexed — one row per file, ever.

Settings changes never leave a proposal stale: the wizard's save re-plans the library on the spot, so there is no stamp file and no staleness check any more.

## 6. Review phase (`internal/review`, `pkg/core/vfs/review.go`, `edit.go`)

Status lifecycle: `PROPOSED → APPROVED` (via `Confirm`) `→ DONE`/`ERROR` (via Execute); `APPROVED → PROPOSED` via `ReopenPlan`. `PROPOSED`/`APPROVED` rows are both still reviewable.

`BuildTree(ctx, db)` reads every still-reviewable row over the whole library (no time-slice scoping — issue 19 removed the segment picker), walks each row's folder up through `folder_nodes`, and builds `Node{ID, Name, FileCount, Samples[≤3], Children, Lat, Lon, MergedIDs}`. `Node.ID` is the folder's `folder_nodes` id and survives renames — every edit and every confirm matches on `ID`, never by diffing trees. GPS coordinates attach to the row's `location_node_id`.

Operations, all applied in-memory to the `[]Node` tree and journalled to `.wandersort.draft` next to the database (one synced JSON line per edit, spec D17). The tree on screen is always the plan as loaded with the draft replayed onto it; `[u]` rewrites the draft without its last line and replays the rest:

- **Rename** (`r`) — writes directly onto `Node.Name`. No pending/staged layer.
- **Merge** (`V` select range + `m`) — `vfs.MergeNodes(tree, ids)`: splices the picked nodes out of their parents, finds their lowest common ancestor by walking the tree, appends one merged node there (name = the anchor row's name, or a computed day-range if every pick is a plain Date folder). Same-named children collapse recursively. Folded nodes leave the tree; their IDs move to the survivor's `MergedIDs`.
- **Drop** (`d`) — `vfs.DropNodes`: removes a folder, lifts its children onto its parent. Refused on a top-level (Year) node.
- **Flatten** (`D`) — `vfs.FlattenNodes`: collapses everything below a folder into it (files land directly in it); the folder itself survives.
- **Peek** (`p`) — copies up to 250MB of a folder's source files into a content-hashed, LRU-evicted (5%-of-volume budget) temp cache dir via `atomicfile`-style atomic rename, and opens it. Read-only, touches nothing in the plan.
- **Reset** (`R`) — deletes the draft and shows the plan as proposed. The database is untouched; it never held the edits.
- **Exit** (`esc`, `ctrl+c`) — just leaves. No save step: the edits are already in the draft. Review never copies or moves.

`Confirm(ctx, db, tree)`: first splits any still-reviewable row off a folder shared with copied files (a copy stopped partway), then validates every submitted node ID against `folder_nodes` (`ErrInvalidTree` otherwise), moves folded folders' files and subfolders onto their survivor, gives every tree folder the parent and name the reviewer left it with (so a rename on a Year folder reaches every row nested under it), prunes emptied folders, rewrites each moved row's `target_path` from its folder, re-establishes filename-collision uniqueness (unmoved rows claim first, moved rows take `_N`), flips every `PROPOSED→APPROVED`, and inserts every reviewer-typed rename into `user_labels` for future autocomplete. Two sibling folders renamed to the same name are merged, not rejected. Typed names are NFC'd. `ErrNoProposal` if zero reviewable rows remain (a rescan replaced everything mid-review).

`wandersort execute` is the only place the plan is written: it checks free space for the whole plan, then `vfs.ApplyDraft` replays the draft and `Confirm`s it (apply and approve in one transaction), deletes the draft, and transfers. Replay skips edits whose folders are gone or that change nothing, so a crash between the commit and the delete is harmless. There is no `review --yes`.

## 7. Move phase (`pkg/core/execute`)

`execute.Run(ctx, db, log, outputDir, Options{Mode, DryRun, OnProgress})` selects every `APPROVED` row (`ORDER BY id`), and for each: `os.Stat`s the source, then calls a `transfer` function. `productionTransfer`: for `Move`, tries a same-device `os.Rename` first (atomic, nothing copied); on any rename failure (including cross-device) falls back to `atomicfile.Copy` (temp file + `os.Rename` into place — atomic on the destination), then, for `Move` only, verifies the copied byte count matches the source size before `os.Remove`ing the source. `Copy` mode never unlinks the source. There is no journal file — the durable state *is* `virtual_fs_entries.status`: each row is flipped to `DONE` (with `source_path` and `file_registry.file_dir/file_name` repointed to a **library-relative** path — the same value as `target_path`, spec D9/D10 — and `file_registry.placed` set to 1) or `ERROR` (with the failure text in the `error` column and `placed` left at 0) via the same async `BulkWriter` every phase uses, flushed before `Run` returns. A crash or `ctrl+c` mid-run simply leaves the untouched rows still `APPROVED`; the next `execute` run selects exactly those and continues — no explicit resume state machine, no separate journal. A failed row is left at `ERROR` and is not auto-retried. Deliberately sequential (unmeasured throughput, per an open ticket) — no worker pool. `DryRun` runs the same stat/report logic but calls a no-op transfer and writes no status changes.

At the end of every non-dry run, `cleanupPlacedDuplicates` reads `file_registry.placed = 1` fresh, finds every other `file_registry`/`file_metadata` row sharing a placed file's hash (duplicates the scorer never elected, or a copy of an already-placed file a later scan saw again), and hard-deletes them — id list collected up front, deleted in the same FK order as the scanner's sweep (`virtual_fs_entries` → `file_metadata` → `file_registry`). A run that stops early is picked up by the next one. Never touches disk, and never touches an `ERROR` row (a placed file's hash can't have a second live master to begin with — see §4).

## 8. Re-runs on an already-sorted library

Scanning again: unchanged files keep their `scan_status` (skipped by the metadata phase, since only `DISCOVERED` rows are claimed); changed files (differing size/mtime) reset to `DISCOVERED` and get re-hashed + re-EXIF'd; files no longer seen under a cleanly-walked root are hard-deleted at once (`file_registry`, `file_metadata`, `virtual_fs_entries` — no soft delete, no retention window, see §2), which drops them from scoring/VFS/review immediately. New files added are `DISCOVERED` and flow through normally. The VFS phase always replaces every `PROPOSED` row and keeps `APPROVED`/`DONE`/`ERROR` rows intact — so re-running `scan` proposes destinations for new/changed masters without disturbing an already-approved or already-executed plan, and `wandersort review` shows only the new material as reviewable. A settings change re-plans automatically, without asking: `rebuildTree` flips every `APPROVED` row back to `PROPOSED` first via `ReopenPlan`, then re-runs `Propose`. There is no manual rebuild flag or key any more — `[R]` in the TUI only discards unsaved in-memory edits.

The SD-card case (spec D11): scanning the same photos again after they've been placed (e.g. re-importing a card without wiping it) creates fresh `file_registry` rows for them, sharing the placed file's content hash. The scorer's `placed`-wins rule (§4) keeps the original file the master no matter how the new copy's path scores, so the new rows are never proposed; `execute`'s end-of-run cleanup (§7) then hard-deletes them the next time `execute` runs. Nothing is ever copied twice, and the source is left exactly as re-imported.

Files WanderSort itself wrote to the output directory during Execute are ordinary `file_registry` rows too (`file_dir`/`file_name` updated in place on transfer) — a later scan of the output directory as a *source* would see them as `SOURCE`-origin files like any other, since `file_origin` is not changed by Execute.

## 9. Version identifiers

- **Schema version**: `schema_migrations(version, run_at)` — one row per applied migration (`1`, `2`, `3` currently). No single "schema version" scalar; it's the max applied version implicitly.
- **exiftool**: `pkg/install/exiftool_setup.go` — hardcoded minimum `exiftoolVersion = "13.59"`, checked at startup via `exiftool -ver` against an installed/`$PATH` binary; downloaded+extracted if missing/older.
- **Location DB**: versioned via a companion `location.json` metadata file downloaded alongside `location.db`, containing (at minimum) a SHA-256 hash and row counts, checked by `verifyLocationDB` after download.
- **Hash algorithm**: no explicit version field — BLAKE3-256 is hardcoded (`hashOutputSize = 32`); the `blake3:` prefix on every stored `file_hash` names it.
- **No app-level semantic version** is stored in the DB or config file (repo has no git tag yet).
