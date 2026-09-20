# Library re-scan (deferred)

Status: needs-triage
Priority: P3
Type: task
Blocked by: 06, 12

Deferred until the rest of this spec lands.

## What

A separate command that scans the library itself, only to bring the database
in line with the disk:

- Files deleted from the library: dropped from the database.
- Files renamed or moved by hand: treated as new, unplaced input.
- No tombstones: a deleted photo that arrives again is placed again.
- Users are told in docs not to move or delete files by hand, and to run this
  command if they did.

Also here: refuse a source scan root that is, contains, or sits inside the
library. Issue 06 prevents duplicate copies meanwhile.

## Why

Spec "Deferred". Keeps beta small; the fundamentals (issues 01–17) come first.

## Comments
