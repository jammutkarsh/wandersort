> SPDX-License-Identifier: AGPL-3.0-or-later
>
> Copyright (c) 2026 Utkarsh Chourasia

# E2E QA Suite

Ordered easiest/most-independent → hardest/most-dependent. `[H]` = human tester,
`[A]` = AI agent. One line each: **command → expected**.

Only what's built today is listed (scan → hash → exif → score → vfs → review). The
Execute/move stage isn't written yet, so it isn't here.

## 0. Smoke & help (no side effects)

1. `[H]` `wandersort --help` → prints styled help, lists all subcommands, exit 0.
2. `[A]` `wandersort --version` → prints a version string, exit 0.
3. `[A]` `wandersort bogus-cmd` → unknown-command error, non-zero exit.
4. `[A]` `wandersort scan --help` → shows `--paths/-p`, `--workers/-w`, `--group-by`, exit 0.
5. `[A]` `wandersort report` *(empty DB)* → clean "no sessions yet" error, non-zero exit, no stack trace.

## 1. Dependency install (network, idempotent, scan-driven)

6. `[H]` `wandersort scan -p <dir>` *(fresh machine)* → the install screen downloads ExifTool + location DB with progress bars, then swaps in-place into the scan screen; exit 0.
7. `[A]` `wandersort scan -p <dir>` *(run again)* → no install screen at all, the scan screen appears immediately.
8. `[A]` dependencies already installed → no checksum line on the console; the verification is still in the log file. Same for `scan`/`serve`/`review`.
9. `[A]` `wandersort scan -p <dir>` → **one** console line per phase carrying both the count and the time (`Scanned N files in 1.996s`), not a separate count line and timing line.
10. `[A]` `wandersort scan -p <dir>` → before the first phase, one line reports the resolved `workers`, `output` directory and `groupBy` (`Year/Month/location, orientation, media`, or `Year/Month/none (flat Year/Month)`); changing any of them via flag, env or `config.yaml` is reflected there.
11. `[H]` scan into an output volume smaller than the library → the "Output volume may be too small" warning appears **at the end**, immediately before the "Run 'wandersort review'" line, not in the middle of the pipeline.
12. `[H]` `wandersort review` on that same library → the same warning is printed once the review is confirmed, before "Folder structure approved".
13. `[A]` delete exiftool binary, then `wandersort scan -p <dir>` → auto-reinstalls missing dep mid-run, exit 0.
14. `[A]` `wandersort scan` *(any dep download)* → "checksum verified" line appears in the scan feed, not just in the log file.
15. `[A]` `wandersort setup` → unknown-command error (the command is gone; settings live in `wandersort config`).
16. `[H]` `wandersort config` *(fresh machine, location DB not downloaded yet)* → the wizard opens immediately with no install screen, a `⬇ Location database` bar runs under the banner, and the Home & work step shows "⏳ Waiting for the location database to finish downloading…" until it completes, then unblocks on its own.
17. `[A]` `wandersort config` *(location DB present)* → no download bar at any point; town suggestions work from the first keystroke.
17a. `[H]` `wandersort config` → typing `Hyderabad` in the home-town field lists `Hyderabad, Telangana, India` and `Hyderabad, Sindh, Pakistan` — full names, never two identical rows (`Banjar` lists each West Java entry once). Picking one saves that exact string; re-opening `config` accepts it without re-typing, and a scan resolves it to the right city. A unique town still shows in full (`Indore, Madhya Pradesh, India`) but auto-named folders for it stay bare (`Indore`).
17b. `[H]` `wandersort config` → every option list is numbered; pressing `2` on a yes/no picks "no" without advancing, and the `example` block above the key help changes to that option's path. Turning off the `location` rule removes the city folder from every later example.

## 2. Classifier (pure, fast, independent)

18. `[A]` scan a dir with `.jpg .png .heic .mp4 .mov .cr2 .dng .aae` → each classified Image/Video/RAW/Sidecar correctly.
19. `[A]` scan a dir with a `.txt`/`.pdf` mixed in → non-media logged/skipped, not indexed, nothing lost.
20. `[A]` scan a dir with `.JPG` / `.HeIc` (mixed case) → classified same as lowercase.
21. `[A]` scan a file with no extension → skipped cleanly, no crash.

## 3. Scan phase (filesystem walk)

22. `[H]` `wandersort scan -p ~/Pictures` → walks recursively, reports scanned count, exit 0; final log line reports total elapsed time (e.g. "Scan complete in 1.2s").
23. `[A]` `wandersort scan -p ./a -p ./b` and `-p ./a,./b` → both forms scan both roots identically.
24. `[A]` scan a dir containing `.DS_Store`/`Thumbs.db`/`.git`/`node_modules` → noise skipped, not counted.
25. `[A]` `wandersort scan -p /does/not/exist` → clear error, non-zero exit, no partial garbage in DB.
26. `[A]` `wandersort scan` *(no `-p`)* → usage error asking for paths, non-zero exit.
27. `[A]` scan nested roots `-p /photos -p /photos/2024` → nested root pruned, files counted once.
28. `[H]` `wandersort scan -p <dir> -w 1` vs `-w 8` → same final counts, both exit 0.
29. `[A]` `wandersort scan -p <dir>` → console shows a "phase took" line for each of scan/hash/exif/score/vfs, not just their result-count lines.

## 4. Hash phase (depends on scan)

30. `[A]` scan a dir with the same file copied 3× → all three land in one duplicate/content group.
31. `[A]` scan a 0-byte file and a large (>1GB) file → both hash without error or OOM.
32. `[A]` corrupt/truncate a media file, then scan → hash failure isolated to that file, session continues.
33. `[H]` scan a dir → the TUI shows **Hash** and **Metadata** as two separate stage rows, each with its own bar and its own elapsed time.

## 4a. EXIF phase (depends on hash)

34. `[A]` scan a photo with GPS EXIF → lat/lon/timestamp extracted and persisted onto the row the hash phase created (one `file_metadata` row per file, not two).
35. `[A]` scan a file with stripped/absent EXIF → hashes fine, empty metadata, no crash.
36. `[A]` scan an iOS video with both `CreateDate` and `CreationDate` tags → both persisted (`exif_create_date`, `exif_creation_date`).
37. `[A]` scan a dir containing `.AAE` sidecars → no ExifTool run for them; they stay at `scan_status = 'HASHED'` while media files end at `'ANALYZED'`.
38. `[H]` kill the scan (ctrl+c) during the Metadata stage, then re-scan the same unchanged dir → the interrupted files resume from `HASHED` (the Hash stage reports ~0 files, Metadata re-reads them).

## 5. Score phase (depends on hash — elects master)

39. `[A]` same bytes in `New Folder/` and `Goa Trip 2024/` → the well-named folder's copy elected master.
40. `[A]` a unique (non-duplicated) file → elected master by default.
41. `[A]` re-scan after deleting all-but-one copy of a group → lone survivor promoted to master.

## 6. VFS phase (depends on score — proposes tree)

42. `[A]` scan photos with capture dates → proposal groups them by date (e.g. `2024/…`).
43. `[A]` scan photos with GPS → proposal reflects resolved location in the path.
44. `[A]` scan photos with no EXIF at all → proposal falls back to existing folder-name context, no crash.
45. `[A]` run scan twice unchanged → VFS proposal set replaced wholesale, no duplicate proposals.
46. `[A]` `wandersort scan -p <dir> --group-by none` → proposal is flat `Year/Month`, no location/orientation/media subfolders.
47. `[A]` `wandersort scan -p <dir> --group-by location` → proposal is `Year/Month/Location` only.
48. `[A]` `wandersort scan -p <dir> --group-by date,location,device,orientation,media` → proposal is `Year/Month/Day/Location/Device/Orientation/Media`.
49. `[H]` same, on a library where every file is a vertical iPhone shot but there are both photos and videos → the `Device` and `Vertical` folders are gone; `Photos`/`Videos` remains. Day and location folders are always kept even when there's only one of each.
50. `[H]` a photo-only library, then scan again after adding one video → the `Photos` folder reappears and the existing photos are re-proposed inside it (the proposal is rebuilt library-wide every run).
51. `[A]` set `collapse-levels: false` in `config.yaml`, then `wandersort review --rebuild` → the full nesting is proposed again, one folder per level.
52. `[A]` same, with files whose GPS doesn't resolve → **no location folder at all** for those files (never one named after the camera, and never a second dated folder next to the Day). The device folder, if that level is in group-by, appears exactly once.
53. `[H]` a library whose source folders are named after the camera (`.../Canon EOS 700D/*.JPG`) → no folder offers `suggested:` with the name it already has.
54. `[H]` merge two folders that both carry a `suggested:` name → the merged folder keeps the **first** selected folder's own name; no suggestion is applied by the merge. Typing a rename on the first folder first → that rename is what the merged folder gets.
55. `[H]` with location second in `--group-by` (e.g. `date,location`) → `wandersort review` shows the `suggested:` hint on the **location** folder, not on the Day or Device folder above it.
56. `[A]` `wandersort scan -p <dir> --group-by bogus` → clear "invalid --group-by value" error, non-zero exit, nothing scanned.
57. `[A]` a real Live Photo pair (`IMG_1234.HEIC`+`.MOV`, same stem, matching GPS/timestamp) with `--group-by location` (no media split) → both land in the same target directory — because their own derived data agrees, not because of stem-matching.
58. `[A]` two files sharing a filename stem but from unrelated shoots (e.g. an old iPhone reusing `IMG_0042` across different years) → land in *different* target directories, each by its own derived date (no forced stem-based grouping — this used to force them together, a real reported bug).
59. `[A]` a photo (`DateTimeOriginal`) and a same-moment iOS video (`CreateDate` raw-UTC, `CreationDate` offset-aware) → both land in the same day/cluster, not shifted apart by the video's UTC-vs-local skew.
60. `[A]` an `.aae` sidecar with no EXIF timestamp of its own → falls back to file mtime like any other file, gets a target path, no crash.

## 7. Location resolution & anchors (depends on hash — GPS-tagged files)

61. `[A]` two GPS points ~15-40km apart, no anchor set → both resolve to *some* named place (previously: silently unlocated past ~11km even though the search box reached 50km).
62. `[A]` a coordinate near a gazetteer entry with a diacritic in its name (e.g. "Banjār") and a plain-spelled entry at ~the same distance → the plain-spelled name ("Banjar") is proposed, not the diacritic one.
63. `[H]` set a home anchor (via `wandersort config`), then scan two GPS points both within ~50km of it but resolving to different suburb names → both fold into the anchor's folder name, not two separate suburb folders.
64. `[A]` scan with no anchors configured → resolved suburbs still get their own individual folders (no unwanted folding).
65. `[A]` scan a library where most GPS photos resolve to the same city, plus a separate fully-unresolved cluster (e.g. a GPS-less DSLR photo, no EVENT label overlap) with **no confirmed anchor set** → the unresolved cluster's suggestion is the source folder name, *not* the library's dominant city (`anchorCities` no longer guesses from frequency).

## 8. Review (interactive correction, depends on VFS)

66. `[H]` `wandersort review` *(no proposal yet)* → clean "run scan first" error, non-zero exit.
67. `[H]` `wandersort review` → full indented tree renders, alt-screen, scrollable with `↑/↓`/`j`/`k`.
68. `[H]` press `n` on a nested folder → the cursor jumps to the next folder at the **same indent level**, even under a different parent; `N` goes back. Past the last one at that level it reports "no more folders at this level" instead of wrapping.
69. `[H]` press `V`, then `n` a few times, then `D` → the whole level across several branches is selected and flattened, without arrowing through the folders in between.
70. `[A]` `wandersort review --yes` → every suggested name accepted non-interactively, `Confirm` runs, exit 0.
71. `[H]` press `enter` on a suggested node → suggestion accepted inline, cursor advances.
72. `[H]` manually rename a node with `r`, then move off and back and press `enter` on it again → the manual rename is kept, **not** overwritten by the suggestion (precedence: default name < location suggestion < user's rename).
73. `[H]` press `r` on any node → rename prompt opens pre-filled; for a node with a GPS coordinate, ranked place candidates appear below the input.
74. `[H]` while renaming, press `Tab` → input fills with the top-ranked candidate.
75. `[H]` while renaming, press `Ctrl-E` → candidate list widens (~10km more) and may show more/farther options.
76. `[H]` while renaming, type a name matching an earlier confirmed folder → it appears in the candidate list (`user_labels` prefix match).
77. `[H]` press `p` on a folder → spinner shows "Copying preview…", then the OS file browser opens a **temp folder** containing up to 250MB of that folder's files; the originals are untouched.
78. `[H]` press `p` on a folder with 0 files (shouldn't normally happen) → clean inline error, no crash.
79. `[H]` press `p` on a folder whose entire content is one child chain (e.g. `2017/April/08` with only child `Horizontal/Photos`), then press `p` on that leaf child too → **the same temp folder** opens both times (content-based cache — see `filesSignature`), not two separate copies.
80. `[H]` press `V` (capital — lowercase `v` does nothing) over a selection whose whole line is highlighted (not just a marker character), move down to select 2+ sibling folders, press `m` → they collapse into **one** folder named after the first one's resolved name, carrying the combined file count.
81. `[H]` select 2+ leaf folders from **different** Month/Day branches under the same Year (e.g. the same camera's photos spread across three months — `V` from one branch's leaf down through another's), press `m` → exactly **one** folder remains under the shared Year with all the files; the emptied Month/Day chains are pruned, not left behind as hollow rows.
82. `[H]` press `m` with no prior `V`, with only 1 leaf in the selection, with a selection covering only structural (non-leaf) rows, or with leaves that share no common ancestor at all (different Years) → rejected with a **visibly flagged** (warning-colored, not dim) status message, no merge happens.
83. `[H]` merge a folder that has siblings sorting after it (e.g. days `21`+`22` among `16 20 21 22 25`) → the merged folder stays in **name order** among its siblings, not appended at the bottom of the list, and the cursor lands on it.
84. `[H]` after a successful merge (either same-parent or cross-branch), press `u` → the whole pre-merge tree is restored (structure, not just names); keep pressing `u` → every earlier structural edit is undone in turn, back to the tree the review opened with; one more `u` reports "nothing left to undo" rather than doing nothing silently.
85. `[H]` a trip spanning several days with `--group-by date,location,device` → `V` on the first Day row, extend through the last, `m` → **one** Day folder remains, holding **one** location folder, holding one folder per genuinely distinct device — no duplicate same-named children left to merge by hand.
86. `[H]` rename one child so it matches a sibling-branch child's name, then merge the parents → the two collapse into one (matching is on the name that will be written, not the proposed one).
87. `[H]` `V` on a leaf then extend across branches vs. `V` on a parent then extend → the merge acts at the depth of the row `V` was pressed on, both times.
88. `[H]` put the cursor on a folder with several levels under it (e.g. `April` over `Indore/Apple iPhone 13`) and press `D` → everything below it collapses into it, its file count is unchanged, and **only that branch** changes — a sibling month keeps its subtree.
89. `[H]` a Day holding several locations, each split further by device/orientation → `V` on the first location, extend past the last, `D` → **every selected location** loses its splits and keeps its own folder; they are not merged into one, and the Day above is untouched.
90. `[H]` same selection with `d` instead → each selected location is dropped and its children lift onto the Day.
91. `[H]` after a multi-folder `V`+`D`, press `u` once → all of it is undone in a single step.
92. `[H]` press `D` on a top-level (Year) row → allowed; the whole year flattens into the Year folder itself.
93. `[H]` press `D` on a row with nothing below it → flagged rejection, tree unchanged.
94. `[H]` press `d` on a folder that still has children → only that one folder goes, its children reattach to its parent, same-named folders elsewhere untouched.
95. `[H]` press `d` on a top-level (Year) row → flagged rejection (its files would land in the library root; the message points at `[D]`).
96. `[H]` remove nesting with `d`/`D`, then `c` confirm, then re-open review → the removed segments are gone from every affected path and **no files were lost**, including files that sat directly in a removed folder.
97. `[H]` interleave several `m`/`d`/`D` edits, then press `u` repeatedly → each is undone in reverse order, whatever mix of edit types they were.
98. `[H]` press `L` → status line reports the new layout name, tree rebuilds to match (e.g. flat `Year/Month` with `group-by: none`); any renames typed before pressing `L` are gone (expected — different depth means different nodes).
99. `[A]` press `L` before a location resolver is available (e.g. location DB unreachable) → no-op, no crash.
100. `[H]` rename two different unresolved date-clusters to the exact same real place name, then `c` confirm → both collapse into one folder (not rejected as a naming collision).
101. `[H]` press `c` → "Folder structure approved" message; `q` without `c` → "review cancelled — nothing changed", DB untouched.
102. `[H]` peek (`p`) two or more different folders during one review session, then quit (`q`) or confirm (`c`) → every temp preview folder created during the session is gone afterward (check `$TMPDIR`).
103. `[H]` resize the terminal narrow (~50 cols) during a review → the key help wraps instead of running off the edge, and the tree shrinks to match: no rows are pushed off the bottom, the last row and the help are both visible.
104. `[H]` a library spanning several months (e.g. November and December) → months are listed chronologically in the review tree **and** on disk (`11_November` before `12_December`), not alphabetically.
105. `[H]` make a rename or a merge/drop, then press `q` → warning that changes are unsaved; press any other key, then `q` again → warns again; press `q` twice in a row → exits, DB untouched.
106. `[H]` press `q` with nothing edited → exits immediately, no warning.
107. `[A]` `wandersort review --rebuild --group-by device` *(after a scan)* → proposal re-proposed as `Year/Month/Device` without re-scanning or re-hashing; review opens on the new tree.
108. `[A]` set `group-by: [location]` in `config.yaml`, then `wandersort review --rebuild` with no flag → the config's levels are used.
109. `[A]` `wandersort review --group-by device` *(no `--rebuild`)* → the existing proposal is reviewed unchanged; the flag alone does not re-propose.

## 9. Report (read-only, depends on a prior scan)

110. `[H]` `wandersort report` after a scan → table with scanned/hashed/duplicate counts for that session.
111. `[A]` `wandersort report -x` → same data as expanded label:value pairs.
112. `[A]` two scans over different roots, then `report` → two rows, duplicate counts don't bleed across sessions.
113. `[A]` `report` while a scan is mid-run → newest session flagged partial (non-terminal status).

## 10. Re-scan / incremental (depends on prior scan — determinism)

114. `[A]` scan a dir, then scan the exact same dir again → counts stable, pipeline deterministic.
115. `[A]` scan, delete some files on disk, re-scan → missing rows soft-deleted, not counted live.
116. `[A]` scan, add new files, re-scan → only new files processed, existing groups intact.

## 11. Reset & issue (destructive / packaging)

117. `[H]` `wandersort reset` → confirmation prompt; answering no leaves data intact.
118. `[A]` `wandersort reset --yes` → all scan data wiped, `report` then errors "no sessions".
119. `[A]` `wandersort issue` → produces a zip with `wandersort.log` + `about.txt`, no DB by default.
120. `[A]` `wandersort issue --include-db` → zip additionally contains the DB.

## 12. Serve / HTTP API (depends on everything — hardest)

121. `[H]` `wandersort serve` → starts on default port 7658, `GET /ping` returns 200.
122. `[A]` `GET /internal/v1/swagger/index.html` → Swagger UI loads.
123. `[A]` `POST /internal/v1/pipeline/start` with valid roots → starts a background session, returns session id.
124. `[A]` `GET /internal/v1/pipeline/count` → returns file counts matching a `report`.
125. `[A]` `POST /internal/v1/pipeline/start` with a nested/duplicate root → roots canonicalized + pruned.
126. `[A]` `POST /internal/v1/admin/reset` → wipes data like the CLI reset.
127. `[A]` start `serve`, then run `scan` against the same output dir → second process blocked by output lock, clear message.
128. `[A]` `wandersort serve --group-by location` → sessions started against this server propose `Year/Month/Location` only.

## 13. Config precedence & global config file (cross-cutting)

129. `[A]` `WORKERS=2 wandersort scan -p <dir> -w 8` → flag wins (8 workers), env ignored.
130. `[A]` `OUTPUT_PATH=/tmp/ws wandersort scan -p <dir>` → DB + logs written under `/tmp/ws`.
131. `[A]` `wandersort scan -p <dir> --plain` → console shows the plain line log; the JSON log file holds the full developer detail either way.
132. `[A]` any command on a machine with no `~/.wandersort/config.yaml` → the file is created with the commented template before the command does anything else.
133. `[H]` `wandersort config` *(`$EDITOR` set)* → opens the file in `$EDITOR`.
134. `[H]` `wandersort config` *(interactive TTY)* → full-screen wizard, in order: output path, workers, rules, collapse, then one Home & work step holding home town, work town, "group home/work photos by date only?" and "merge consecutive same-location days?"; completing it prints exactly `config saved in ~/.wandersort/config.yaml`, ctrl+c prints "Cancelled — nothing saved." and writes nothing.
135. `[A]` `wandersort config --print` / `-p` → prints the saved file to stdout, no wizard, exit 0.
136. `[A]` `wandersort config | cat` → prints the file instead of launching the wizard into the pipe; `wandersort config > out.yaml` writes the file's contents.
137. `[A]` add `output-path: /tmp/ws-cfg` to `config.yaml`, then `wandersort scan -p <dir>` *(no `-o` flag)* → DB + logs written under `/tmp/ws-cfg`; `-o` on the command line still overrides it.
138. `[A]` add `group-by: [none]` to `config.yaml`, then scan with no `--group-by` flag → flat `Year/Month` proposal; `--group-by location` on the command line still overrides it.
139. `[A]` a freshly created `config.yaml` (first ever command) is empty, and one written by the wizard has no comments — every key present is a real setting.
140. `[A]` put invalid YAML in `config.yaml` (e.g. a leading tab), then run any command → a **warning** naming the file and the parse error, the command continues on defaults, exit code unchanged by the bad file.
141. `[A]` same broken file, then `wandersort config --print` → still works (the command that fixes the file must not be locked out by it).
142. `[A]` `wandersort scan --debug` → unknown-flag error (the flag is gone; the JSON log file is always at debug level, use `--plain` for line output).
