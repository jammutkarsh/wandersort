# E2E QA Suite

Ordered easiest/most-independent → hardest/most-dependent. `[H]` = human tester,
`[A]` = AI agent. One line each: **command → expected**.

Only what's built today is listed (scan → hash → score → vfs → review). The
Execute/move stage isn't written yet, so it isn't here.

## 0. Smoke & help (no side effects)

1. `[H]` `wandersort --help` → prints styled help, lists all subcommands, exit 0.
2. `[A]` `wandersort --version` → prints a version string, exit 0.
3. `[A]` `wandersort bogus-cmd` → unknown-command error, non-zero exit.
4. `[A]` `wandersort scan --help` → shows `--paths/-p`, `--workers/-w`, `--group-by`, exit 0.
5. `[A]` `wandersort report` *(empty DB)* → clean "no sessions yet" error, non-zero exit, no stack trace.

## 1. Dependency setup (network, idempotent)

6. `[H]` `wandersort setup` *(fresh machine)* → downloads ExifTool + location DB, exit 0.
7. `[A]` `wandersort setup` *(run again)* → detects deps present, no re-download, exit 0.
8. `[A]` delete exiftool binary, then `wandersort scan -p <dir>` → auto-reinstalls missing dep mid-run, exit 0.
9. `[A]` `wandersort setup`/`wandersort scan` *(any dep download)* → "checksum verified" line appears on the console, not just in the log file.
10. `[H]` `wandersort setup` *(fresh machine, interactive TTY)* → prompts for home town, shows ranked matches, typing the exact name auto-selects; then prompts "Work town same as Home? [Y/n]" — enter/`y` reuses the home town, `n` opens the same picker for work.
11. `[A]` `wandersort setup < /dev/null` (no TTY) → dependency install still runs, anchor prompt is silently skipped, exit 0 (never hangs).
12. `[A]` `wandersort setup` *(anchors already set)* → no re-prompt.

## 2. Classifier (pure, fast, independent)

13. `[A]` scan a dir with `.jpg .png .heic .mp4 .mov .cr2 .dng .aae` → each classified Image/Video/RAW/Sidecar correctly.
14. `[A]` scan a dir with a `.txt`/`.pdf` mixed in → non-media logged/skipped, not indexed, nothing lost.
15. `[A]` scan a dir with `.JPG` / `.HeIc` (mixed case) → classified same as lowercase.
16. `[A]` scan a file with no extension → skipped cleanly, no crash.

## 3. Scan phase (filesystem walk)

17. `[H]` `wandersort scan -p ~/Pictures` → walks recursively, reports scanned count, exit 0; final log line reports total elapsed time (e.g. "Scan complete in 1.2s").
18. `[A]` `wandersort scan -p ./a -p ./b` and `-p ./a,./b` → both forms scan both roots identically.
19. `[A]` scan a dir containing `.DS_Store`/`Thumbs.db`/`.git`/`node_modules` → noise skipped, not counted.
20. `[A]` `wandersort scan -p /does/not/exist` → clear error, non-zero exit, no partial garbage in DB.
21. `[A]` `wandersort scan` *(no `-p`)* → usage error asking for paths, non-zero exit.
22. `[A]` scan nested roots `-p /photos -p /photos/2024` → nested root pruned, files counted once.
23. `[H]` `wandersort scan -p <dir> -w 1` vs `-w 8` → same final counts, both exit 0.
24. `[A]` `wandersort scan -p <dir>` *(no `--debug`)* → console shows a "phase took" line for each of scan/hash/score/vfs, not just their result-count lines.

## 4. Hash + EXIF phase (depends on scan)

25. `[A]` scan a dir with the same file copied 3× → all three land in one duplicate/content group.
26. `[A]` scan a photo with GPS EXIF → lat/lon/timestamp extracted and persisted.
27. `[A]` scan a file with stripped/absent EXIF → hashes fine, empty metadata, no crash.
28. `[A]` scan a 0-byte file and a large (>1GB) file → both hash without error or OOM.
29. `[A]` corrupt/truncate a media file, then scan → hash failure isolated to that file, session continues.
30. `[A]` scan an iOS video with both `CreateDate` and `CreationDate` tags → both persisted (`exif_create_date`, `exif_creation_date`).

## 5. Score phase (depends on hash — elects master)

31. `[A]` same bytes in `New Folder/` and `Goa Trip 2024/` → the well-named folder's copy elected master.
32. `[A]` a unique (non-duplicated) file → elected master by default.
33. `[A]` re-scan after deleting all-but-one copy of a group → lone survivor promoted to master.

## 6. VFS phase (depends on score — proposes tree)

34. `[A]` scan photos with capture dates → proposal groups them by date (e.g. `2024/…`).
35. `[A]` scan photos with GPS → proposal reflects resolved location in the path.
36. `[A]` scan photos with no EXIF at all → proposal falls back to existing folder-name context, no crash.
37. `[A]` run scan twice unchanged → VFS proposal set replaced wholesale, no duplicate proposals.
38. `[A]` `wandersort scan -p <dir> --group-by none` → proposal is flat `Year/Month`, no location/orientation/media subfolders.
39. `[A]` `wandersort scan -p <dir> --group-by location` → proposal is `Year/Month/Location` only.
40. `[A]` `wandersort scan -p <dir> --group-by bogus` → clear "invalid --group-by value" error, non-zero exit, nothing scanned.
41. `[A]` a real Live Photo pair (`IMG_1234.HEIC`+`.MOV`, same stem, matching GPS/timestamp) with `--group-by location` (no media split) → both land in the same target directory — because their own derived data agrees, not because of stem-matching.
42. `[A]` two files sharing a filename stem but from unrelated shoots (e.g. an old iPhone reusing `IMG_0042` across different years) → land in *different* target directories, each by its own derived date (no forced stem-based grouping — this used to force them together, a real reported bug).
43. `[A]` a photo (`DateTimeOriginal`) and a same-moment iOS video (`CreateDate` raw-UTC, `CreationDate` offset-aware) → both land in the same day/cluster, not shifted apart by the video's UTC-vs-local skew.
44. `[A]` an `.aae` sidecar with no EXIF timestamp of its own → falls back to file mtime like any other file, gets a target path, no crash.

## 7. Location resolution & anchors (depends on hash — GPS-tagged files)

45. `[A]` two GPS points ~15-40km apart, no anchor set → both resolve to *some* named place (previously: silently unlocated past ~11km even though the search box reached 50km).
46. `[A]` a coordinate near a gazetteer entry with a diacritic in its name (e.g. "Banjār") and a plain-spelled entry at ~the same distance → the plain-spelled name ("Banjar") is proposed, not the diacritic one.
47. `[H]` set a home anchor (via `setup`), then scan two GPS points both within ~50km of it but resolving to different suburb names → both fold into the anchor's folder name, not two separate suburb folders.
48. `[A]` scan with no anchors configured → resolved suburbs still get their own individual folders (no unwanted folding).
49. `[A]` scan a library where most GPS photos resolve to the same city, plus a separate fully-unresolved cluster (e.g. a GPS-less DSLR photo, no EVENT label overlap) with **no confirmed anchor set** → the unresolved cluster's suggestion is the source folder name, *not* the library's dominant city (`anchorCities` no longer guesses from frequency).

## 8. Review (interactive correction, depends on VFS)

50. `[H]` `wandersort review` *(no proposal yet)* → clean "run scan first" error, non-zero exit.
51. `[H]` `wandersort review` → full indented tree renders, alt-screen, scrollable with `↑/↓`/`j`/`k`.
52. `[A]` `wandersort review --yes` → every suggested name accepted non-interactively, `Confirm` runs, exit 0.
53. `[H]` press `enter` on a suggested node → suggestion accepted inline, cursor advances.
54. `[H]` manually rename a node with `r`, then move off and back and press `enter` on it again → the manual rename is kept, **not** overwritten by the suggestion (precedence: default name < location suggestion < user's rename).
55. `[H]` press `r` on any node → rename prompt opens pre-filled; for a node with a GPS coordinate, ranked place candidates appear below the input.
56. `[H]` while renaming, press `Tab` → input fills with the top-ranked candidate.
57. `[H]` while renaming, press `Ctrl-E` → candidate list widens (~10km more) and may show more/farther options.
58. `[H]` while renaming, type a name matching an earlier confirmed folder → it appears in the candidate list (`user_labels` prefix match).
59. `[H]` press `p` on a folder → spinner shows "Copying preview…", then the OS file browser opens a **temp folder** containing up to 250MB of that folder's files; the originals are untouched.
60. `[H]` press `p` on a folder with 0 files (shouldn't normally happen) → clean inline error, no crash.
61. `[H]` press `p` on a folder whose entire content is one child chain (e.g. `2017/April/08` with only child `Horizontal/Photos`), then press `p` on that leaf child too → **the same temp folder** opens both times (content-based cache — see `filesSignature`), not two separate copies.
62. `[H]` press `V` (capital — lowercase `v` does nothing) over a selection whose whole line is highlighted (not just a marker character), move down to select 2+ sibling folders, press `m` → all selected folders rename to the first one's resolved name.
63. `[H]` select 2+ leaf folders from **different** Month/Day branches under the same Year (e.g. the same camera's photos spread across three months — `V` from one branch's leaf down through another's), press `m` → all reparent directly under the shared Year and merge, instead of being rejected for "not sharing a parent."
64. `[H]` press `m` with no prior `V`, with only 1 leaf in the selection, with a selection covering only structural (non-leaf) rows, or with leaves that share no common ancestor at all (different Years) → rejected with a **visibly flagged** (warning-colored, not dim) status message, no merge happens.
65. `[H]` after a successful merge (either same-parent or cross-branch), press `u` → the whole pre-merge tree is restored (structure, not just names); pressing `u` again does nothing (single-level undo, no redo).
66. `[H]` press `L` → status line reports the new layout name, tree rebuilds to match (e.g. flat `Year/Month` with `group-by: none`); any renames typed before pressing `L` are gone (expected — different depth means different nodes).
67. `[A]` press `L` before a location resolver is available (e.g. location DB unreachable) → no-op, no crash.
68. `[H]` rename two different unresolved date-clusters to the exact same real place name, then `c` confirm → both collapse into one folder (not rejected as a naming collision).
69. `[H]` press `c` → "Folder structure approved" message; `q` without `c` → "review cancelled — nothing changed", DB untouched.
70. `[H]` peek (`p`) two or more different folders during one review session, then quit (`q`) or confirm (`c`) → every temp preview folder created during the session is gone afterward (check `$TMPDIR`).

## 9. Report (read-only, depends on a prior scan)

71. `[H]` `wandersort report` after a scan → table with scanned/hashed/duplicate counts for that session.
72. `[A]` `wandersort report -x` → same data as expanded label:value pairs.
73. `[A]` two scans over different roots, then `report` → two rows, duplicate counts don't bleed across sessions.
74. `[A]` `report` while a scan is mid-run → newest session flagged partial (non-terminal status).

## 10. Re-scan / incremental (depends on prior scan — determinism)

75. `[A]` scan a dir, then scan the exact same dir again → counts stable, pipeline deterministic.
76. `[A]` scan, delete some files on disk, re-scan → missing rows soft-deleted, not counted live.
77. `[A]` scan, add new files, re-scan → only new files processed, existing groups intact.

## 11. Reset & report-issue (destructive / packaging)

78. `[H]` `wandersort reset` → confirmation prompt; answering no leaves data intact.
79. `[A]` `wandersort reset --yes` → all scan data wiped, `report` then errors "no sessions".
80. `[A]` `wandersort report-issue` → produces a zip with `wandersort.log` + `about.txt`, no DB by default.
81. `[A]` `wandersort report-issue --include-db` → zip additionally contains the DB.

## 12. Serve / HTTP API (depends on everything — hardest)

82. `[H]` `wandersort serve` → starts on default port 7658, `GET /ping` returns 200.
83. `[A]` `GET /internal/v1/swagger/index.html` → Swagger UI loads.
84. `[A]` `POST /internal/v1/pipeline/start` with valid roots → starts a background session, returns session id.
85. `[A]` `GET /internal/v1/pipeline/count` → returns file counts matching a `report`.
86. `[A]` `POST /internal/v1/pipeline/start` with a nested/duplicate root → roots canonicalized + pruned.
87. `[A]` `POST /internal/v1/admin/reset` → wipes data like the CLI reset.
88. `[A]` start `serve`, then run `scan` against the same output dir → second process blocked by output lock, clear message.
89. `[A]` `wandersort serve --group-by location` → sessions started against this server propose `Year/Month/Location` only.

## 13. Config precedence & global config file (cross-cutting)

90. `[A]` `WORKERS=2 wandersort scan -p <dir> -w 8` → flag wins (8 workers), env ignored.
91. `[A]` `OUTPUT_PATH=/tmp/ws wandersort scan -p <dir>` → DB + logs written under `/tmp/ws`.
92. `[A]` `wandersort --debug scan -p <dir>` → console shows full developer log lines, not just user milestones.
93. `[A]` any command on a machine with no `~/.wandersort/config.yaml` → the file is created with the commented template before the command does anything else.
94. `[H]` `wandersort config` *(`$EDITOR` set)* → opens the file in `$EDITOR`.
95. `[A]` `wandersort config` *(`$EDITOR` unset)* → prints the file's contents to stdout instead, exit 0.
96. `[A]` add `output-path: /tmp/ws-cfg` to `config.yaml`, then `wandersort scan -p <dir>` *(no `-o` flag)* → DB + logs written under `/tmp/ws-cfg`; `-o` on the command line still overrides it.
97. `[A]` add `group-by: [none]` to `config.yaml`, then scan with no `--group-by` flag → flat `Year/Month` proposal; `--group-by location` on the command line still overrides it.
98. `[H]` hand-edit `config.yaml` to add a comment and an extra key, then run `wandersort setup` to change an anchor → the hand-added comment and key both survive; only `anchors.home`/`anchors.work` changed.
99. `[H]` set `debug: true` in `config.yaml` (not `--debug`) → same effect as the flag; note the template's commented default is `false`, so just uncommenting the line without changing the value has no effect.
