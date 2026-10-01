# E2E QA Checklist

Manual checks, easiest first. `[H]` = human, `[A]` = agent. One line each: **command → expected**.
Run the binary with `HOME` pointed at a scratch folder: `--output-path` becomes the default library.

## 1. Smoke & help

1. `[H]` `wandersort --help` → styled help listing add, organise, execute, check, admin; exit 0.
2. `[A]` `wandersort bogus-cmd` → unknown-command error, non-zero exit.
3. `[A]` `wandersort add --help` → shows `--paths/-p` and `--force`; no `--workers`.
4. `[A]` bare `wandersort --plain` (or piped stderr) → prints help instead of opening the app.

## 2. Settings & dependencies

5. `[H]` first-ever `wandersort` → opens on the Settings tab; the location database downloads in a progress row while earlier steps are answerable; the Saved places step waits for it, then unblocks.
6. `[A]` dependencies already on disk → no download row, no checksum line on the console (still in the log).
7. `[H]` Settings → typing a town name lists full `<city>, <state>, <country>` names, never two identical rows; the saved choice resolves to the right city on the next scan.
8. `[H]` Settings → option lists are numbered; the example tree beside each question changes with the option under the cursor; turning off the `location` rule drops the city level from later examples.
9. `[H]` change a setting and save → the plan is re-proposed at once; an unchanged save re-plans nothing.
10. `[A]` delete the exiftool binary, then `add` → it is downloaded again before the run starts.

## 3. Add (scan → metadata → vfs)

11. `[A]` `add -p <dir>` → one line per phase with count and time (`Scanned N files in 1.9s`); first line names workers, output and rules.
12. `[A]` `add -p ./a -p ./b` and `-p ./a,./b` → identical result; nested roots (`/x` and `/x/y`) walked once.
13. `[A]` `add -p /does/not/exist` → clear error, non-zero exit, nothing written.
14. `[A]` `add --plain` with no `-p` → error asking for paths.
15. `[A]` `add -p <the library or a folder inside it>` → refused ("overlaps the library").
16. `[A]` folder with `.DS_Store`, `.Spotlight-V100`, `._*` AppleDouble files, `.photoslibrary` bundles → skipped, no warnings flood.
17. `[A]` mixed-case extensions (`.JPG`, `.HeIc`), no-extension files, `.txt` → media classified, others skipped, no crash.
18. `[A]` same file copied 3× → one duplicate group, one master; the copy in a descriptively named folder wins.
19. `[A]` 0-byte file and a >1 GB file → both hashed; every `file_hash` is `blake3:<64 hex>`.
20. `[A]` an unreadable file (`chmod 000`) → run finishes with `1 file could not be read`; `admin report` ships a `READ/open/permission-denied` row; fixed and re-added → read, error row gone.
21. `[A]` `.AAE` sidecars → no exiftool call, still a metadata row, no error row.
22. `[H]` ctrl+c during Metadata, then add again → only unread files are read.
23. `[H]` external spinning disk → `Storage detected … class=rotational concurrentReads=1`; SSD files read first in a mixed library.
24. `[H]` output volume smaller than the library → "may be too small" warning at the end of the run.
25. `[A]` add the same unchanged folder twice → identical plan, no duplicate proposals.
26. `[A]` delete files on disk, add again → their rows are gone; add new files → only they are read.

## 4. Plan (vfs)

27. `[A]` dated photos → `Year/MM_Month/…`, months sorted numerically.
28. `[A]` GPS photos → a city folder; two points 15–40 km apart both resolve to a place.
29. `[A]` GPS points within ~50 km of a saved place → folded into that place's folder; without saved places, suburbs stay separate.
30. `[A]` a plain and a diacritic geonames spelling at the same distance → the plain one is used.
31. `[A]` no EXIF at all → falls back to file date, no crash; unresolved GPS → no location level (never a device name), `Unknown` only beside located siblings.
32. `[A]` rules `none` → flat `Year/Month`; `date,location,device,orientation,media` → full nesting.
33. `[H]` every file one device and orientation → those levels collapse; adding a video brings `Photos/Videos` back on the next plan.
34. `[A]` consecutive same-place days with merge on → one `02_04` range folder; a day split between places stays unmerged.
35. `[A]` a trip crossing a month end → one run under its first month (`Aug_28-Sep_04`); a Jan 01 file pulled into December gets `Jan_01`, never a bare `01`.
36. `[A]` Live Photo pair (same stem, same moment) → same suffix; two unrelated shoots reusing one filename counter → separate folders.
37. `[A]` photo + same-moment iOS video (UTC `CreateDate`, offset `CreationDate`) → same day.
38. `[A]` unpaired `.AAE` → `orphan/`, not shown in review.
39. `[A]` two files planned to one name → earlier capture keeps it, later gets `_2`; a capture group shares one suffix.

## 5. Organise (review)

40. `[H]` no plan yet → home screen says so; the app stays open.
41. `[H]` tree renders with box-drawing guides, file counts right-aligned, scrolls; `?` shows the key reference.
42. `[H]` `n`/`N` → next/previous row at the same depth across branches; stops at the ends.
43. `[H]` `r` → rename with ranked place suggestions; `↑/↓` pick, `tab` fills, `ctrl+e` widens the radius; names typed in earlier reviews are offered.
44. `[H]` `r`, `m`, `d` on a Year or Month folder → refused with a ⚠ status line.
45. `[H]` `V` + extend + `m` → one folder under the common ancestor, named after the row `V` was pressed on (or the spanned day range for Date folders); emptied chains pruned; same-named children merged recursively; cursor on the result, in name order.
46. `[H]` `m` with one row, no `V`, or across Years → flagged rejection, no change.
47. `[H]` `d` → folder dropped, children lift to its parent; `D` → subtree collapsed into the folder, count unchanged; over a `V` range each folder stays separate.
48. `[H]` `u` repeatedly → each edit undone in reverse order, whatever its kind, then "nothing left to undo".
49. `[H]` `R` → draft deleted, tree back to the proposed plan.
50. `[H]` `p` → preview copy (≤ 250 MB) opens in the OS browser; a parent and its only-child leaf open the same copy; copies survive quitting.
51. `[H]` edits, then `kill -9` and reopen → same tree; `esc` → home says the edits wait for `execute`.
52. `[H]` narrow terminal (~50 cols) → key help wraps, last tree row still visible.

## 6. Execute

53. `[A]` `execute` after review edits → files land under the edited folders, draft gone, `.wandersort.db.zst` written.
54. `[A]` `execute --dry-run` → reports the edited target paths, writes nothing.
55. `[A]` too little free space → refuses before changing anything; draft still there.
56. `[H]` `execute --move` → asks first (names never-read files); `--yes` skips the question; sources removed only after verified copies.
57. `[A]` a source edited after the scan (same size) → `checksum-mismatch` error, nothing lands, source kept.
58. `[A]` an occupied target name → lands at `_1`, nothing overwritten; the same file already there → recorded, not copied twice.
59. `[A]` kill mid-run, run again → resumes; nothing copied twice.
60. `[A]` re-add the same card after execute → nothing proposed again.

## 7. Check

61. `[A]` `check` → existence and size of every placed file; says `--full` re-reads contents.
62. `[A]` truncate a placed file → listed under its problem heading, non-zero exit; corrupt bytes → only `check --full` finds it.
63. `[A]` delete a placed file → listed as gone and forgotten; the next check doesn't list it; `add` can bring a source copy back.
64. `[A]` leftover `.copy-*` file → listed as safe to delete, exit 0.

## 8. Admin

65. `[A]` `admin clear` → preview copies removed, no question, database untouched.
66. `[H]` `admin db --reset` → confirmation (mentions placed files); "no" keeps data; `--yes` backs up then wipes, keeping settings.
67. `[A]` `admin db --reset` on an empty database → "nothing to reset", backup untouched.
68. `[A]` `admin db --restore --yes` → data back, `.wandersort.db.before-restore` kept; with the database open elsewhere → refused.
69. `[A]` `admin report` → zip in the current directory: `about.txt`, up to 5 recent logs (`$HOME` in place of the home directory), `errors.json`; `--include-db` adds the database; `--redact-paths` replaces paths and leaves logs out.

## 9. Concurrency & logs

70. `[A]` a second process against the same library → "already running (PID …)".
71. `[A]` a session that only looks around → no log file, nothing written into the library.
72. `[A]` `--plain` → plain line log on the console; the JSON log in `~/.wandersort/logs/` has full detail either way.
