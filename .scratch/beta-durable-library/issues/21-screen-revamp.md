# Screen revamp: settings flow and copy screen

Status: ready-for-human
Priority: P1
Type: task
Blocked by: none (10 is done)

## What

One pass over the app's screens: the settings tab becomes a first-run flow,
and copy/move gets a screen of its own. Both are tabs of the same shell as
scan and review, so the whole session is one program.

### Settings tab (was issue 11)

- First screen: home town, work town, output folder (with the library
  history from issue 10 as suggestions).
- Then ask "Configure advanced settings?". No goes to the scan tab. Yes
  shows the remaining settings, then the scan tab.
- Values stay in memory until the output folder is picked; then the library
  database is opened or created and the values written to it.
- Read-only while a scan runs (D5); the tab says why.
- Saving with a plan open re-plans it straight away (D20); the tab says so
  before the save, as a line of text, not a question.
- A later launch opens on the **scan tab**, over the most recently used
  library (issue 10 made the history the default output folder). The
  settings flow is the first-run path, not the front door.

### Copy screen (was issue 21)

Review no longer transfers anything (issue 15), so copy and move need their
own place. `wandersort execute` opens the app on this tab, the same way
`review` opens on the review tab. Plain mode (`--plain` or no terminal)
keeps today's line output.

Before anything is written:

- Show what is about to happen: file count, total size, free space at the
  output, and copy or move.
- Copy is the default. Choosing move asks once, defaulting to Cancel (D23).
- Too little free space stops here, before anything is applied or copied.
- Then apply the review's draft file and transfer (the order issue 15
  defines).

While it runs:

- A progress bar over files and bytes, the file being copied, and elapsed
  time.
- Verification is visible per file: each copy is hashed while it is written
  and compared with the stored hash (issue 08, done); the screen shows
  verified and failed counts as they happen.
- `ctrl+c` stops after the current file (each file lands whole or not at
  all); the next run carries on from there.

When it ends:

- A summary: copied or moved, verified, failed, bytes, time.
- Each failed file with its reason (mismatch shows both hashes, missing
  source, no space), readable from the screen, not only the log.
- The tab returns to the scan tab's home screen with the summary line, like
  a finished review does.

The tab is reachable only when there is something to transfer, and never
while a scan runs.

### Finishing a copy that stopped

A copy that stopped partway is already resumable — the next run picks up
every file that is not in the library yet and did not fail. What is missing
is the app noticing and saying so, and that matters for more than
convenience: **a scan run between the stop and the restart re-plans the
files still waiting, and a folder the reviewer dropped or flattened comes
back for them** (spec D28). Offering to finish the copy first is what keeps
the user out of that, and it is cheaper than teaching the planner to
remember a removed folder.

So: when the app opens over a library whose last copy did not finish, the
copy tab says so and offers to finish it, before the scan tab is used for
anything else. Not a modal — a line on the home screen and the tab marked
the way a ready review is (`Copy — 412 files left`), so someone who meant to
do something else still can. The scan tab keeps a one-line warning while
that is true, naming what it costs: scanning now re-plans the files still
waiting.

Knowing it stopped needs one fact on disk, not a column (issue 20 took the
statuses out and this must not put one back). The review's own draft file
already marks the two states around it, so use it rather than inventing a
second marker:

- `.wandersort.draft` present — edits made, no copy started.
- `.wandersort.copying` present — `ApplyDraft` renamed the draft to it as it
  wrote the edits into the plan; a copy is in flight. The rename is atomic
  and is what makes the distinction survive a power loss, which the output
  lock cannot: the OS drops that lock the moment the process dies.
- Neither — idle.

`ApplyDraft` renames instead of deleting; the copy deletes
`.wandersort.copying` only after a run that transferred everything with
nothing failed. A run that stops or has failures leaves it, which is the
point. Replay is idempotent, so a leftover file replaying onto the tree is a
no-op — the same property that already makes a crash between the apply and
the delete harmless.

One thing to get right: `Propose` deletes `.wandersort.draft` on every
re-plan and must leave `.wandersort.copying` alone — it is not a set of
edits to throw away, it is the record that a copy is unfinished. A copy that
ends with everything transferred clears it; so does `reset --db`.

## Why

Spec D4, D5, D23. Most users only need the three basic settings, and the one
step that writes their photos can take hours — it needs its own place to show
progress, prove each copy is intact, and report what failed, instead of a
status line inside review.

## Acceptance

Settings:

- Quitting before an output folder is chosen writes nothing anywhere.
- Picking an existing library pre-fills its stored values.
- Advanced settings unchanged in content; only their placement moves.
- A launch with a library in the history opens on the scan tab.

Copy:

- `wandersort execute` in a terminal opens the app on the copy tab; with
  `--plain` it prints lines as today.
- Move asks once, defaulting to Cancel; copy asks nothing.
- Too little free space: message on screen, nothing applied or copied.
- A hash mismatch shows on screen as a failed file with both hashes; the
  source is kept and the run continues.
- `ctrl+c` mid-run, then run again: finishes the rest, copies nothing twice.
- The copy tab is not reachable during a scan or with nothing to transfer.

Finishing a stopped copy:

- `ctrl+c` mid-copy, quit, relaunch: the home screen says a copy did not
  finish and the copy tab says how many files are left; finishing it copies
  only those.
- Kill the process outright (not ctrl+c) mid-copy, relaunch: the same, since
  the marker is a file and not a held lock.
- A copy that transfers everything with nothing failed: no such offer on the
  next launch, and `.wandersort.copying` is gone.
- A copy that ends with failures: the file stays, and the next launch says
  so — a failure is not a finished copy.
- Drop a folder in review, copy half, quit, relaunch, finish the copy: the
  dropped folder does not come back. (Scanning first instead is the case
  D28 allows to break — the scan tab warns before it does.)
- Re-plan from the settings while a copy is unfinished: the edits file is
  replaced as usual, the unfinished-copy marker is not.

## Comments

- 2026-09-18 — Launch and first-run flow, agreed while doing issue 01:
  - Launch writes only the per-process log (`~/.wandersort/logs/`). No lock
    file and no database until the flow below reaches the output folder.
  - On a first run the settings tab is the first page, and the output folder
    is its first question. The answer stays in memory; `config.CheckLibrary`
    runs on it, but nothing is written anywhere.
  - A confirm prompt follows the output folder:
    - **Existing library**: `openLibrary` runs at confirm (check, lock,
      database), because the rest of the form is pre-filled from the settings
      stored in it.
    - **Empty or new folder**: every answer stays in memory. `openLibrary`
      runs when the form is saved (the whole form, not one particular step),
      then the settings are written to the new database. Quitting before that
      writes nothing, which is this issue's first acceptance line.
  - After saving, go to the scan tab.
  - The lock is taken with the database (issue 01's `openLibrary`), not at
    scan start: settings, review, approvals, execute and reset all write the
    database too. No start-up jitter — `flock`/`LockFileEx` already picks
    exactly one winner however close two processes start; the only race is
    check-then-create, and `openLibrary` orders it check → lock → create.
  - The open question there — whether a later launch opens on settings or on
    scan — is answered above: scan, over the last-used library.
