# Beta: durable library

Source: `changes-before-v0dotX.md` (external low-level design), reviewed and
narrowed in discussion on 2026-09-16/17. This spec records what was decided,
not the original document. Anything in that document not listed here is
either deferred (see bottom) or rejected.

## Goal

Once files are placed in a library, the library is state the user owns. New
scans add to it; nothing already placed is moved or renamed by the app. User
edits in review carry meaning for future files, so a folder renamed to
"Goa Trip" keeps receiving the photos that belong in it.

## Decisions

### Library and settings

- **D1. The output folder is a library.** It must be empty or already contain
  `.wandersort.db`. The database lives in the library root so it travels with
  the user's own backups.
- **D2. Settings live in the library database.** `~/.wandersort/config.yaml`,
  the flag/env/file layering and the `.wandersort.cfg` stamp go away. A new
  library starts from the defaults in code.
- **D3. Output folder history.** `~/.wandersort/` keeps up to 100 recently used
  output folders, newest first, oldest dropped. Entries whose path no longer
  exists are dropped when the list is read.
- **D4. Settings tab.** Shows home town, work town and output folder, then asks
  "Configure advanced settings?". Values are held in memory until the output
  folder is picked, then written to that library's database.
- **D5. Settings change between scans.** The output folder is fixed once
  the library exists. Other settings are locked only while a scan runs. After
  a scan, saving a changed setting re-plans at once (D20).
  Files already transferred never move. A "re-arrange previous files" option is
  deferred.
- **D6. Frozen layout.** The collapse heuristic and every rule apply per batch
  at plan time. Files placed under one set of rules stay where they are when
  the rules change; later batches follow the new rules. The resulting
  inconsistency is the user's choice.

### Files and paths

- **D7. Always a full content hash**, stored as `blake3:<hex>`. The unique-size
  skip is removed. Known cost: on the 2026-08-07 783 GiB HDD run it skipped
  187.5 GiB, roughly 29 minutes of reading.
- **D8. Store only the metadata tags the pipeline needs.** No extractor
  version, no offset tags. A missing timezone means local time. Every stored
  column is used by a folder level; the decoder keeps no tag it doesn't
  store. The Live Photo content identifier was considered and rejected: a
  picture and its video pair by folder, filename and a 1-second window
  instead.
- **D9. Paths.** Paths inside the library are stored relative to the output
  folder, using `/` on every OS and Unicode NFC — the app chose every
  segment's spelling, so folding it to one form is free. Source paths stay
  absolute and are stored exactly as the filesystem gave them, with only a
  Windows backslash turned into `/`: never NFC, since the app doesn't own
  that spelling and folding it breaks byte-exact lookup on Linux/Windows.
- **D10. Placed rows point at the library.** After copy or move, the
  file's row and `source_path` hold its library-relative path, and the file's
  row is marked `placed`. The mark belongs to the file, not the plan, so it
  outlives every re-plan. After each
  execute run, other rows sharing a placed file's hash are deleted. Vanished
  never-placed files are deleted at the next scan, with no retention window.
  Rows only; files on disk are never deleted. Cost: copy mode re-hashes old
  photos left on a card at every re-import.
- **D11. No duplicate copies.** A source file whose hash matches an already
  placed file is not proposed: a placed file is always the master of its hash.
  In move mode other identical copies stay at the source.

- **D28. No status columns.** A stage's input is what the next fact is
  missing for: no metadata row means "needs reading", a plan row means
  "planned", a `placed` file is in the library. No claim column: one process
  holds the output lock and the work queue is in memory. **A file whose size
  or modified time changed is a new file** — its row and everything hanging
  off it are deleted and it is read and planned from scratch, so nothing in
  the pipeline ever moves backwards.
  **Known ceiling:** with no approved state, a re-plan that runs while some of
  a review's files are still untransferred — a copy stopped partway, then a
  scan — re-proposes them. A rename or a merge survives it, because the folder
  is still there holding the transferred files and the leftovers are routed
  back into it by its bounds (D15). A drop or a flatten does not: the folder
  is gone, so the removed level comes back for whatever has not moved yet, and
  the library holds one file beside a folder of the same day. Nothing is lost
  and dropping again fixes it. Re-running the copy alone is unaffected — it
  only ever picks up what has not been transferred. **The answer is to keep
  the user out of that order, not to teach the planner to remember a removed
  folder**: the app knows a copy did not finish and offers to finish it
  before the scan tab is used for anything else, and warns on the scan tab
  while that is true (issue 21).
- **D29. Failures live in one `errors` table**, one row per file and stage:
  the step, an error kind from a fixed list, and a JSON detail of named fields —
  the full message, the unwrapped chain layer by layer, the code frames that
  recorded it, and the syscall error when there is one (a caught panic stores
  its real stack). A row is
  deleted as soon as it stops being true — the stage succeeds, the file is
  placed, or the file's row goes — so the table only ever holds live
  problems. `wandersort issue` exports every column, with the home directory in the detail
  rewritten to `$HOME` (the username is the identifier; `--redact-paths`
  blanks paths entirely), so a bug report carries every failure and no photo data.

### Folder tree and future files

- **D12. The plan is a persisted tree.** Folder nodes have stable IDs and
  parents; files link to a node. A node no file uses is deleted outright;
  IDs are never reused, so a stale ID can only miss.
- **D13. Each node stores a constraint for its own level** (year = 2024,
  month = 3, day in {1, 2, 3}, place in {Mumbai, Panji, Baga}, device,
  orientation, media). Days are a set, never a range: merging the 3rd and the
  20th holds those two days, not the 10th. A constraint is a list of
  alternatives (any one matches), each alternative an AND of levels, so a
  merge of "Goa on the 3rd" and "Manali on the 20th" does not admit Goa on the
  20th. A node's full range is its constraint AND all its ancestors'.
- **D14. Review edits transform constraints.**
  - rename: constraint unchanged.
  - merge: the kept node gets the merged nodes' alternatives side by side.
    Each merged node's alternatives first take on the constraints of its
    folders between it and the new parent, since the merge moves it out from
    under them. Same-named children merge the same way.
  - drop: the dropped node's constraint is pushed into each child.
  - flatten: the node keeps its own constraint; descendants are removed.
- **D15. Placing a new file.** Walk from the root. Descend into a child only if
  a complete match exists somewhere beneath it. Otherwise create folders from
  the deepest matching ancestor using the library's rules. Consequences
  accepted:
  - Delhi on 2 March next to `01_03/Goa Trip` lands in `02/Delhi`, so a day can
    appear in two date folders across batches. Within one batch the planner
    still keeps one day in one date folder.
  - A dropped level can reappear for new data (drop `12`, a later Delhi photo
    from the 12th creates `12/Delhi`).
- **D16. Month boundary rule stays** (`maxFolderSpan`, 24h). Clustering reads
  placed files read-only so a new file continuing a placed cluster gets the
  same folder month.
- **D26. Year and month folders are fixed.** They cannot be switched off in
  settings, and D15 finds them by name, so review cannot rename, merge or drop
  them, or flatten a year. Otherwise a renamed month (`March Trip`) makes the
  next batch create a second `03_March` beside it.
- **D27. A same-place run crossing a month or year stays together** under
  its first day's year and month: `2024/08_August/Aug_28-Sep_04/Goa`,
  `2024/12_December/Dec_30-Jan_02/Goa`. Its later days add their
  full date to the folders above, so a later batch can still join the trip.

### Review

- **D17. Edits go to a session file, not the database.** `.wandersort.draft`
  next to the database, JSON Lines, one edit per line: `rename` (old and new
  name), `merge` (every node ID, anchor first), `drop`, `flatten` (the parent
  node). Undo removes the last line. Replay is idempotent, so a crash between
  applying and deleting the file is harmless.
- **D18. No save step; copy or move applies the file.** Review is one tree for
  the whole library, with no time slices. Leaving review keeps every edit (it
  is already in the file); there is no save or discard question. Reset deletes
  the file. Review itself never copies or moves; that happens only in the
  copy screen (`wandersort execute`). Copy or move first checks free space
  for the whole plan (too little stops it before anything changes), then
  applies every edit to the database tree in one transaction, approves the
  whole plan, deletes the file, then transfers everything. Copying part of a
  plan is not supported: people plan once and transfer together. After a
  crash the file is replayed when review reopens.
- **D19. A new scan means a new plan.** Scanning a folder discards the draft
  file and rebuilds the proposal for every file not yet transferred.
  Transferred files are kept. Review edits not yet copied are lost; accepted,
  since a scan between reviewing and copying is rare.
- **D20. Settings changes re-plan on their own; review only resets.** Saving
  changed settings immediately throws away the draft file and the saved (not
  yet transferred) marks, and re-runs the vfs phase over every file not yet
  transferred. No question is asked. Transferred files are never re-planned.
  There is no manual rebuild: `[R]` in review is reset, which deletes the
  draft file and shows the plan as proposed.
  `review --rebuild`, settings updates during a scan and the settings-changed
  prompt are removed.

### Execute

- **D21. Never overwrite.** Planning adds `_N` against names known to the
  database; at write time the destination is created exclusively and an
  existing name takes the next `_N`.
- **D22. Hash while copying.** Compare against the stored hash. On mismatch
  remove the temp file, mark the row as an error and continue. The source is
  never deleted on a mismatch.
- **D23. Copy is the recommended mode.** Move always asks yes/no.
- **D24. Back up the database before each execute run**, via `VACUUM INTO`,
  verified and then zstd-compressed to `.wandersort.db.zst` — the database's
  name plus the compressor's extension, never a `.bak`. One backup,
  overwritten each time. It must never look to a duplicate finder like a
  second copy of the live database that one of the two could be deleted —
  compression is what guarantees that, so the marker table and the size
  padding that used to buy it are gone.
- **D25. `_N` order is capture time, then hash.** Names are compared in NFC,
  case-insensitively.

## Deferred

- Re-scanning the library to pick up deleted files; a manually renamed or
  moved file comes back as new input (issue 18).
- Refusing a source folder that is, contains or sits inside the library
  (issue 18; issue 06 covers most of the harm meanwhile).
- Partial review and transfer: reviewing and copying one part of the plan
  (a year, a folder) while the rest waits.
- A re-arrange option for transferred files (re-planning placed files under
  new settings).
- Changing the output folder of an existing library.
- Cloud backends and a storage boundary.
- Drift suggestions, component versions, dismissed suggestions.
- Tombstones: a deleted photo that arrives again is placed again.
- Near-duplicate detection (see `.tickets/near-duplicate-detection.md`).

Rejected: running scan and hash in parallel. The walk was 16.6 s of a 3h 38m
run; the hash is limited by disk reads, and walking the same disk at the same
time would slow it down on HDDs and SD cards.

## Issues

| # | Issue | Blocked by |
|---|---|---|
| 18 | Library re-scan (deferred) | — |
| 21 | Screen revamp: settings flow, copy screen, finishing a stopped copy | — |

Everything else — 01 through 17, 19, 20, 22, 23 — is built and committed, or
folded into a ticket that was. The decisions they settled are the D-list above;
that is what survives them.

## Priority

- **P1:** 21. The settings flow and the copy screen are the last of the beta
  surface, and the copy screen carries the one correctness piece left: noticing
  a copy that did not finish and offering to finish it, which is what keeps a
  scan from re-planning the files still waiting (D28).
- **P3, deferred:** 18.

## Pickup order

21, then 18 if it is ever taken off the shelf. The lanes below are kept as the
record of how the built work was sequenced and why.

### How the built work was sequenced

Four lanes, grouped by the files they touch, so parallel work rarely conflicted.

| Lane | Area | Order |
|---|---|---|
| A | `pkg/core/execute`, `pkg/atomicfile` | 02, 09, 06, 08 |
| B | `pkg/core/metadata`, scorer, `buildTargets` | 04, 03, 17 |
| C | `pkg/path`, `pkg/core/vfs` tree | 05, 12, 13, 14, 22 |
| D | `internal/cli`, `pkg/config`, `internal/review` | 01, 16, 19, 10, 15, 20, 21, 23 |

Lane C was the critical path — 05, 12, 13, 14 was the longest chain and the
biggest work, and 05 also unblocked 06 and 17 in other lanes. 04 went before 03
so migration 002 was edited once. 02 and 09 went first in lane A: no
dependencies, small, and they closed the overwrite risk immediately. 16 went
early in lane D so 12, 13 and 15 ported only the re-plan on settings save, and
19 followed it for the same reason — the time-slice picker was the other big
piece of review code they would otherwise have carried forward.

Serial order as built: 01, 02, 09, 16, 19, 05, 06, 04, 03, 08, 17, 12, 13, 14,
22, 15, 10, 20, 23. What is left is 21 (18 deferred).
