> SPDX-License-Identifier: AGPL-3.0-or-later
>
> Copyright (c) 2026 Utkarsh Chourasia

# E2E QA Suite

Ordered easiest/most-independent → hardest/most-dependent. `[H]` = human tester,
`[A]` = AI agent. One line each: **command → expected**.

Only what's built today is listed (scan → hash → score → vfs). The move/apply
stage isn't written yet, so it isn't here.

## 0. Smoke & help (no side effects)

1. `[H]` `wandersort --help` → prints styled help, lists all subcommands, exit 0.
2. `[A]` `wandersort --version` → prints a version string, exit 0.
3. `[A]` `wandersort bogus-cmd` → unknown-command error, non-zero exit.
4. `[A]` `wandersort scan --help` → shows `--paths/-p` and `--workers/-w`, exit 0.
5. `[A]` `wandersort report` *(empty DB)* → clean "no sessions yet" error, non-zero exit, no stack trace.

## 1. Dependency setup (network, idempotent)

6. `[H]` `wandersort setup` *(fresh machine)* → downloads ExifTool + location DB, exit 0.
7. `[A]` `wandersort setup` *(run again)* → detects deps present, no re-download, exit 0.
8. `[A]` delete exiftool binary, then `wandersort scan -p <dir>` → auto-reinstalls missing dep mid-run, exit 0.

## 2. Classifier (pure, fast, independent)

9. `[A]` scan a dir with `.jpg .png .heic .mp4 .mov .cr2 .dng .aae` → each classified Image/Video/RAW/Sidecar correctly.
10. `[A]` scan a dir with a `.txt`/`.pdf` mixed in → non-media logged/skipped, not indexed, nothing lost.
11. `[A]` scan a dir with `.JPG` / `.HeIc` (mixed case) → classified same as lowercase.
12. `[A]` scan a file with no extension → skipped cleanly, no crash.

## 3. Scan phase (filesystem walk)

13. `[H]` `wandersort scan -p ~/Pictures` → walks recursively, reports scanned count, exit 0.
14. `[A]` `wandersort scan -p ./a -p ./b` and `-p ./a,./b` → both forms scan both roots identically.
15. `[A]` scan a dir containing `.DS_Store`/`Thumbs.db`/`.git`/`node_modules` → noise skipped, not counted.
16. `[A]` `wandersort scan -p /does/not/exist` → clear error, non-zero exit, no partial garbage in DB.
17. `[A]` `wandersort scan` *(no `-p`)* → usage error asking for paths, non-zero exit.
18. `[A]` scan nested roots `-p /photos -p /photos/2024` → nested root pruned, files counted once.
19. `[H]` `wandersort scan -p <dir> -w 1` vs `-w 8` → same final counts, both exit 0.

## 4. Hash + EXIF phase (depends on scan)

20. `[A]` scan a dir with the same file copied 3× → all three land in one duplicate/content group.
21. `[A]` scan a photo with GPS EXIF → lat/lon/timestamp extracted and persisted.
22. `[A]` scan a file with stripped/absent EXIF → hashes fine, empty metadata, no crash.
23. `[A]` scan a 0-byte file and a large (>1GB) file → both hash without error or OOM.
24. `[A]` corrupt/truncate a media file, then scan → hash failure isolated to that file, session continues.

## 5. Score phase (depends on hash — elects master)

25. `[A]` same bytes in `New Folder/` and `Goa Trip 2024/` → the well-named folder's copy elected master.
26. `[A]` a unique (non-duplicated) file → elected master by default.
27. `[A]` re-scan after deleting all-but-one copy of a group → lone survivor promoted to master.

## 6. VFS phase (depends on score — proposes tree)

28. `[A]` scan photos with capture dates → proposal groups them by date (e.g. `2024/…`).
29. `[A]` scan photos with GPS → proposal reflects resolved location in the path.
30. `[A]` scan photos with no EXIF at all → proposal falls back to existing folder-name context, no crash.
31. `[A]` run scan twice unchanged → VFS proposal set replaced wholesale, no duplicate proposals.

## 7. Report (read-only, depends on a prior scan)

32. `[H]` `wandersort report` after a scan → table with scanned/hashed/duplicate counts for that session.
33. `[A]` `wandersort report -x` → same data as expanded label:value pairs.
34. `[A]` two scans over different roots, then `report` → two rows, duplicate counts don't bleed across sessions.
35. `[A]` `report` while a scan is mid-run → newest session flagged partial (non-terminal status).

## 8. Re-scan / incremental (depends on prior scan — determinism)

36. `[A]` scan a dir, then scan the exact same dir again → counts stable, pipeline deterministic.
37. `[A]` scan, delete some files on disk, re-scan → missing rows soft-deleted, not counted live.
38. `[A]` scan, add new files, re-scan → only new files processed, existing groups intact.

## 9. Reset & report-issue (destructive / packaging)

39. `[H]` `wandersort reset` → confirmation prompt; answering no leaves data intact.
40. `[A]` `wandersort reset --yes` → all scan data wiped, `report` then errors "no sessions".
41. `[A]` `wandersort report-issue` → produces a zip with `wandersort.log` + `about.txt`, no DB by default.
42. `[A]` `wandersort report-issue --include-db` → zip additionally contains the DB.

## 10. Serve / HTTP API (depends on everything — hardest)

43. `[H]` `wandersort serve` → starts on default port 7658, `GET /ping` returns 200.
44. `[A]` `GET /internal/v1/swagger/index.html` → Swagger UI loads.
45. `[A]` `POST /internal/v1/pipeline/start` with valid roots → starts a background session, returns session id.
46. `[A]` `GET /internal/v1/pipeline/count` → returns file counts matching a `report`.
47. `[A]` `POST /internal/v1/pipeline/start` with a nested/duplicate root → roots canonicalized + pruned.
48. `[A]` `POST /internal/v1/admin/reset` → wipes data like the CLI reset.
49. `[A]` start `serve`, then run `scan` against the same output dir → second process blocked by output lock, clear message.

## 11. Config precedence (cross-cutting)

50. `[A]` `WORKERS=2 wandersort scan -p <dir> -w 8` → flag wins (8 workers), env ignored.
51. `[A]` `OUTPUT_PATH=/tmp/ws wandersort scan -p <dir>` → DB + logs written under `/tmp/ws`.
52. `[A]` `wandersort --debug scan -p <dir>` → console shows full developer log lines, not just user milestones.
