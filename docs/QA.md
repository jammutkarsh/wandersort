# E2E QA Checklist

Manual checks, easiest first. `[H]` = human, `[A]` = agent. One line each: **command → expected**.
Run the binary with `HOME` pointed at a scratch folder: `--output-path` becomes the default library.

## 1. Smoke & help

1. `[H]` `wandersort --help` → styled help listing add, organise, copy, check, admin; exit 0.
2. `[A]` `wandersort bogus-cmd` → unknown-command error, non-zero exit.
3. `[A]` `wandersort add --help` → shows `--paths/-p` and `--force`; no `--workers`.
4. `[A]` bare `wandersort --plain` (or piped stderr) → prints help instead of opening the app.

## 2. Settings & dependencies

4a. `[H]` `shift+tab` (hinted on the top line) or a click on a tab cycles Add → Organise → Copy → Settings, skipping tabs with nothing to do; the mouse wheel moves like ↑/↓. A click on any key hint in a footer or on a numbered choice, a settings row, an Organise row or a button presses it; a click on the open `?` list closes it. The top line, every screen's body and its footer start at the same left margin, and right-hand numbers end at the same right margin.

5. `[H]` first-ever `wandersort` → getting-ready screen downloads exiftool and the locationDB with progress bars, then opens the Add tab; no setup questions come first.
6. `[H]` dependencies already on disk → getting ready shows for a moment (`✓ found`), then the tabs; the last library opens without asking.
6a. `[H]` network blocked (`HTTPS_PROXY=http://127.0.0.1:9`) → each failed dependency named, "Try switching to a better network", 10 s countdown, `enter` retries now; after try 3 any key quits with exit 1; the next launch tries 3 more times.
7. `[H]` Settings → typing a town name lists full `<city>, <state>, <country>` names, never two identical rows; the saved choice resolves to the right city on the next scan.
8. `[H]` Settings → Folder layout → layouts are plain rows (no numbers), ↑↓ enter or a click picks one, the example tree follows the option under the cursor; `Custom…` adds the "Which folders inside each month?" step with Day, Place, Camera, Portrait or landscape, Photo or video; `ctrl+b` goes back a step.
9c. `[H]` Settings → Extra options → "Skip folders", "No home town folder", "One folder per trip" each sit on one row with equal-width yes/no buttons at the same right edge; ↑/↓ move between the three rows without changing any answer and ←/→ set yes/no on the focused one; the focused one's description is above the footer; the example shows only in the side panel (terminal ≥ 100 columns) and a narrower screen has none; a click on any button (even a question not in focus) picks that side, the example updates, and the form stays on screen; the first question's example shows the same three files with and without the camera, orientation and photo/video folders.
9. `[H]` Settings with a library open → a list of answers (the library as "<folder>, in your home folder", the layout by its folders, e.g. "Year › Month › Day › Place", "Extra options 2 of 3 on"); enter changes one row and returns to the list with "Saved"; `esc save` saves what's on screen without asking; Extra options shows its three questions under one heading, no "1)" title, answers read "One folder per trip yes"; a changed setting re-plans at once, an unchanged save re-plans nothing; `ctrl+c` leaves without saving.
9a. `[H]` Settings → Library folder → an empty folder starts a new library with the same settings and nothing planned; a folder WanderSort organised opens with its own; the old library is untouched and opens again when chosen.
10. `[A]` delete the exiftool binary, then `add` → it is downloaded again before the run starts.

## 3. Add (scan → metadata → vfs)

11. `[A]` `add -p <dir>` without a terminal → first line names library and layout, then one sentence per stage (`Found 15,481 files in 1.9s`), then `next:`; `--json` adds one result object on stdout; unreadable files exit 3.
11a. `[H]` `add` in the app → "Adding N folders" with Find files / Read dates and places / Sort into folders, the file being read and a time-left guess; finished stages show no times; fewer sorted than found says how many were skipped; then "Folders ready" with the choices Organise folders, Copy into library, Add more folders (no numbers; the picked one is a purple box round the text, bold and underlined, like a picked yes/no button and every other pick; ↑↓ enter or a click). A run that leaves nothing to copy skips it and returns to Add with "No new files to add."; `⚠ N problems noted while adding. Details: <log>` sits above the footer.
11b. `[H]` Add → drag a folder from the file manager onto the terminal → it is listed at once, quoted or escaped paths included; the footer hint reads `enter scan` once a folder is listed and `enter add` while a path is being typed; `ctrl+o` (hint "choose a folder", also clickable) opens the system folder window and the folder chosen is listed, and closing the window adds nothing; `enter` on an empty line with nothing listed does not scan; typing a path still lists completions, `tab` fills the top one.
11c. `[H]` `esc` twice quickly on a main screen → quits when nothing runs; `esc` inside a sub-menu (the `?` list, a setting being edited, a rename or selection in Organise, a question) only closes it and shows no "esc again to quit"; while a scan or copy runs the top line warns "busy" instead, and `esc` on that tab stops it (footer `esc stop`, then `ctrl+c quit now` while it winds down).
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
22. `[H]` `esc` during Metadata, then add again → only unread files are read.
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
41. `[H]` tree renders with box-drawing guides, a blank line under the tabs, `+` and blue on folders the copy makes, file counts right-aligned, scrolls; footer shows five keys; `?` draws the full list over the tree, any key closes it; an edit shows "✓ … · u undo" until the next key.
42. `[H]` `→`/`←` (or `n`/`N`) → next/previous row at the same depth across branches; stops at the ends.
43. `[H]` `r` → rename with ranked place suggestions; `↑/↓` pick, `tab` fills, `ctrl+e` (more places) widens the radius; names typed in earlier reviews are offered.
44. `[H]` `r`, `m`, `x` on a Year or Month folder → refused with a ⚠ status line.
45. `[H]` `space` (or `shift+↑↓`) + extend + `m` → one folder (hint `m combine`) under the common ancestor, named after the row the selection started on (or the spanned day range for Date folders); emptied chains pruned; same-named children merged recursively; cursor on the result, in name order.
46. `[H]` `m` with one row, no selection, or across Years → flagged rejection, no change.
47. `[H]` `x` → folder removed, what was inside lifts to its parent; `f` → subtree gathered into the folder, count unchanged; over a selection each folder stays separate.
48. `[H]` `u` repeatedly → each edit undone in reverse order, whatever its kind, then "nothing left to undo".
49. `[H]` `ctrl+u` → draft deleted, tree back to the proposed plan.
50. `[H]` `p` → preview copy (≤ 250 MB) opens in the OS browser; a parent and its only-child leaf open the same copy; copies survive quitting.
51. `[H]` edits, then `kill -9` and reopen → same tree; `esc` → home says the edits wait for `copy`.
52. `[H]` narrow terminal (~50 cols) → key help wraps, last tree row still visible.

## 6. Copy

53. `[A]` `copy` after review edits → files land under the edited folders, draft gone, `.wandersort.db.zst` written.
54. `[A]` `copy --dry-run` → reports the edited target paths, writes nothing.
55. `[A]` too little free space → refuses before changing anything; draft still there.
56. `[A]` after `copy`, every source file is byte-identical and still in place.
56a. `[H]` `copy` in a terminal → Copy tab: size, free space, edits; sizes in KB/MB/GB; enter runs Check space / Apply edits (only with edits) / Back up records / Copy & check with a byte bar; ends with "All done" or the files left out by reason.
57. `[A]` a source edited after the scan (same size) → `checksum-mismatch` error, nothing lands, source kept; exit 3; `~/.wandersort/logs/<run>.html` lists it under its drive with what to do.
58. `[A]` an occupied target name → lands at `_1`, nothing overwritten; the same file already there → recorded, not copied twice.
59. `[A]` kill mid-run, run again → resumes; nothing copied twice; files that failed last run are tried again.
60. `[A]` re-add the same card after copy → nothing proposed again.

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

70. `[A]` a second process against the same library → "already running (PID …)", exit 4.
71. `[A]` a session that only looks around → no log file, nothing written into the library.
72. `[A]` `--plain` → plain line log on the console; the JSON log in `~/.wandersort/logs/` has full detail either way.
