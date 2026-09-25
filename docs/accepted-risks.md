> SPDX-License-Identifier: AGPL-3.0-or-later
>
> Copyright (c) 2026 Utkarsh Chourasia

# Accepted risks

Findings from code audits that were looked at and accepted on purpose. An
audit (human or tool) should not report these again as new problems. If the
reason under one stops being true, update or delete the entry and treat it as
open.

Recorded 2026-09-25.

## One database backup, beside the library

`execute` and `admin db --reset` keep a single backup,
`.wandersort.db.zst`, in the library folder, overwritten each time. A logic
bug that damages rows will be backed up by the next `execute`, and a failed
drive takes the database and its backup together.

**Accepted because:** one backup covers the common cases (a crash, a restore
run by mistake, an unwanted reset) for now. Several generations, or a
second copy off the library drive, are the upgrade path when that stops
being enough.

## Download checksums come from the same host as the downloads

The exiftool archive and the location database are verified against a
SHA-256 manifest fetched from the same server. That catches a corrupted
download, not a tampered one: whoever controls the host controls what runs.
`extractTarZst` also joins archive entry names into the install folder
without rejecting `..`, and the manifest's file name is joined the same way;
both are only reachable through a tampered manifest or archive.

**Accepted because:** the hosts are the project's own. Build-time pinned
checksums or a signed manifest (an embedded ed25519 key) are the upgrade
path.

## `check` does not look for files the database doesn't know

`wandersort check` verifies every placed file and reports leftover `.copy-*`
temp files, but does not walk the library for files no row records (copied
in by hand, left after a reset).

**Accepted because:** files are not supposed to be copied into a library by
hand. A re-sync command is planned and will cover reconciling the folder
with the database.

## Duplicates are hashed again on every `add`

After `execute`, the duplicate cleanup forgets every other copy of a placed
file (spec D10). A card imported again is therefore re-read in full on every
`add`, found to be duplicates, and forgotten again.

**Accepted because:** it costs time, never data, and keeping rows for files
that are not in the library goes against D10.
