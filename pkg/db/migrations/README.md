> SPDX-License-Identifier: AGPL-3.0-or-later
>
> Copyright (c) 2026 Utkarsh Chourasia

# Database Migrations

## Why Go Instead of SQL Files?

WanderSort targets a **single-binary desktop app** using embedded SQLite. Writing migrations in Go instead of standalone `.sql` files gives us:

- **No external dependencies.** The old setup used `golang-migrate/migrate` which required an embedded filesystem of `.sql` files and imported driver packages. The custom runner is ~40 lines of code with zero third-party imports.
- **Single binary.** SQL strings live as `const` values compiled directly into the binary. No `embed.FS`, no file I/O, no risk of missing migration files at runtime.
- **Transactional safety.** Each migration runs inside a `BEGIN`/`COMMIT` transaction. If any statement fails, the entire migration rolls back — the database never ends up in a half-applied state.
- **Minimal tracking.** A `schema_migrations` table with two columns (`version`, `run_at`) is auto-created. On startup, the runner reads `MAX(version)` and applies anything newer.

## How It Works

Each migration is a Go file exporting a `Migration` struct:

```go
var Scanner = Migration{
    Version:     1,
    Description: "scanner schema",
    SQL:         scannerSQL, // const with CREATE TABLE statements
}
```

All migrations are registered in a single ordered slice:

```go
var All = []Migration{Scanner, Hasher}
```

`db.go` calls `migrations.Run(db)` on startup. The runner:

1. Creates `schema_migrations` if it doesn't exist.
2. Reads the highest applied version.
3. For each migration with `Version > current`, executes the SQL in a transaction and records the version.

To add a new migration: create a new `.go` file with the next version number and append it to `All`.

---

## Current Schema

### `schema_migrations`

Auto-created by the migration runner. Tracks which migrations have been applied.

| Column   | Type    | Notes                        |
|----------|---------|------------------------------|
| version  | INTEGER | PRIMARY KEY, migration number |
| run_at   | TEXT    | ISO 8601 timestamp            |

---

### `scan_sessions`

**Purpose:** Track each scan invocation — its progress, status, and error state. Enables **incremental scanning** (only process new/changed files) and **resumability** (pick up where a failed scan left off).

```sql
    id TEXT PRIMARY KEY,
    started_at TEXT NOT NULL,
    completed_at TEXT,
    status TEXT NOT NULL CHECK(status IN ('SCAN','HASH','SCORE','FAILED','CANCELLED')),
```

- `id`: UUID as TEXT. **Why TEXT not INTEGER?** Sessions are created by the application before any DB insert (passed into workers). UUID avoids autoincrement coordination across goroutines.
- `status`: Phase/state machine. Success path is `SCAN -> HASH -> SCORE`; terminal failure states are `FAILED` and `CANCELLED`.

```sql
    root_paths TEXT NOT NULL,
```

- `root_paths`: JSON array of scanned directories. **Why store it?** A single scan can cover multiple roots (`["/Photos", "/Backups"]`). Without this, you can't answer "which directories did scan X cover?" — needed for incremental re-scan logic.

```sql
    files_discovered INTEGER NOT NULL DEFAULT 0,
    files_skipped INTEGER NOT NULL DEFAULT 0,
    files_new INTEGER NOT NULL DEFAULT 0,
    files_modified INTEGER NOT NULL DEFAULT 0,
    files_hashed INTEGER NOT NULL DEFAULT 0,
    errors_encountered INTEGER NOT NULL DEFAULT 0,
    last_error TEXT,
```

**The progress counters:**

- `files_discovered`: Total files found during filesystem walk. **Why?** UI progress bar denominator.
- `files_skipped`: Files excluded by extension filter or symlink rules. **Why separate from discovered?** So you can report "scanned 50k files, skipped 2k non-media files".
- `files_new`: Files not previously in `file_registry`. **Why track?** Tells you how much new content was found — useful for "nothing new since last scan" feedback.
- `files_modified`: Files whose mtime changed since last seen. **Why?** These need re-hashing even though they already had a hash.
- `files_hashed`: Count of files hashed during this session (added in v2). **Why separate counter?** Hashing is a distinct phase from discovery — you might discover 50k files but only hash 5k new ones.
- `errors_encountered` / `last_error`: **Why?** If a scan fails on 3 out of 50k files (permission denied, corrupt path), you still want the scan to succeed. These let the UI show "completed with 3 errors" and the last error message for debugging.

---

### `file_registry`

**Purpose:** The **census** of every file ever scanned. It answers: "Does this file exist in my system?" One row per unique file path.

```sql
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_path TEXT NOT NULL,
    file_size TEXT NOT NULL,
    file_modified_at TEXT NOT NULL,
    file_hash TEXT,
```

**The "fingerprint" columns:**

- `file_path`: Absolute or relative path. **Why needed?** To track where the file lives RIGHT NOW (before reorganization).
- `file_hash`: BLAKE3 hash. **Why nullable?** Populated later during the hashing phase. Discovery is fast (just stat the file), hashing is slow (read every byte). Keeping them separate lets you scan first, hash later.
- `file_size`: Bytes as TEXT. **Why?** Quick pre-filter before hashing — if sizes differ, files are definitely not duplicates. Avoids expensive hash comparison.
- `file_modified_at`: Filesystem mtime. **Why?** Detect if a file changed since last scan. If mtime is unchanged, skip re-hashing — massive performance win on re-scans.

```sql
    discovered_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    scan_session_id TEXT NOT NULL,
    source_root TEXT NOT NULL,
```

**The "audit trail" columns:**

- `discovered_at`: When we first saw this file. **Why?** You can query "show me files added this week".
- `last_seen_at`: Updated on every scan that encounters this file. **Why?** If `last_seen_at` is older than the latest scan, the file was deleted from disk. Critical for stale-file detection.
- `scan_session_id`: Which scan found this file. **Why?** Links back to `scan_sessions` for incremental scan logic. If file isn't seen in the latest scan session, it may have been moved or deleted.
- `source_root`: Which root directory the file was found under. **Why?** You might scan 5 different drives. This tracks provenance — "this file came from the backup drive, not the phone import".

```sql
    media_type TEXT NOT NULL CHECK(media_type IN ('IMAGE','VIDEO','SIDECAR','RAW','UNKNOWN')),
    file_extension TEXT NOT NULL,
```

**The "classification" columns:**

- `media_type`: Enum-like value with CHECK constraint. **Why?** Different processing pipelines per type:
  - `IMAGE` → Extract EXIF, candidate for organization
  - `VIDEO` → Different metadata extraction (duration, codec)
  - `SIDECAR` → Don't organize separately, attach to primary file (.aae, .xmp)
  - `RAW` → Might pair with JPEG (.CR2 + .jpg)
  - `UNKNOWN` → Matched extension filter but couldn't classify
- `file_extension`: Normalized lowercase extension (`.jpg` not `.JPG`). **Why?** Fast filtering in queries without string operations on the full path. Also used by the classifier to determine `media_type`.

```sql
    scan_status TEXT NOT NULL DEFAULT 'DISCOVERED'
        CHECK(scan_status IN ('DISCOVERED','HASHING','HASHED','ANALYZING','ANALYZED','ERROR')),
```

**The "workflow state" column:**

- `scan_status`: State machine. **Why?** The processing pipeline happens in stages:
  1. `DISCOVERED` → File found during walk
  2. `HASHING` → Currently being hashed (in-progress marker)
  3. `HASHED` → BLAKE3 computed
  4. `ANALYZING` → Metadata extraction in progress
  5. `ANALYZED` → Metadata extracted
  6. `ERROR` → Something failed (permission denied, corrupt file)

  This lets you **resume interrupted work**: "Hash all files where `scan_status = 'DISCOVERED'`". The `HASHING`/`ANALYZING` in-progress states prevent two workers from processing the same file.

```sql
    path_type TEXT NOT NULL DEFAULT 'RELATIVE' CHECK(path_type IN ('RELATIVE','ABSOLUTE')),
    file_origin TEXT NOT NULL DEFAULT 'SOURCE',
```

- `path_type`: Whether `file_path` is relative to `source_root` or an absolute path. **Why?** Relative paths are portable — if you move the root directory, paths still resolve. Absolute paths are needed for edge cases where the file is outside any root.
- `file_origin`: Where the file came from. Default `SOURCE` means "found during a user-initiated scan". **Why?** Future use: files created by the organizer (copies) could be marked `ORGANIZED` to distinguish them from originals.

```sql
    capture_stem TEXT,
    capture_role TEXT,
```

**The "capture group" columns:**

- `capture_stem`: Base filename without extension (e.g., `IMG_001` from `IMG_001.jpg`). **Why?** Groups companion files together. When you shoot on an iPhone, you get `IMG_001.jpg`, `IMG_001.aae`, maybe `IMG_001.mov` (Live Photo) — all sharing the same stem.
- `capture_role`: Role within the capture group (e.g., `primary`, `sidecar`, `raw`). **Why?** When organizing, you need to know which file is the "main" one and which are companions that should follow it.

```sql
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
```

- `created_at` / `updated_at`: Row timestamps. `updated_at` defaults on insert and is explicitly set by each application update path. **Why not a trigger?** The write paths are narrow and explicit, which avoids hidden write amplification from self-updating triggers.

**Indexes:**

- `UNIQUE(file_path, source_root)`: Prevent duplicate entries. One row per file path per root. **Why compound?** The same relative path `Photos/IMG_001.jpg` could exist under two different roots.
- `idx_file_registry_hash`: **Critical for deduplication.** Query "find all files with this hash" in milliseconds, not full table scan.
- `idx_file_registry_session`: Fast lookup of all files found in a specific scan.
- `idx_file_registry_status`: Query "get next batch of files to hash" efficiently. The worker pool queries this constantly.
- `idx_file_registry_source_root`: Filter files by which root they came from.
- `idx_file_registry_media_type`: Filter by media type (e.g., "show me only videos").
- `idx_file_registry_origin`: Filter by file origin.
- `idx_file_registry_capture`: Group by capture stem + role for companion file matching.

---

### `file_metadata`

**Purpose:** Links files to their content hash and caches per-hash EXIF data. The scorer uses this table to find duplicate files and elect a master.

**How deduplication works:**
Each hashed file inserts one row into `file_metadata` — `(file_hash, file_id)` pairs are unique, but multiple files can share the same hash. The scorer queries:

```sql
SELECT file_hash FROM file_metadata
GROUP BY file_hash
HAVING COUNT(*) > 1
```

This yields duplicate hashes. For each hash, the scorer JOINs `file_registry` to load paths for all files, scores them on filename quality and directory context, and elects the best as master by updating `file_metadata.file_id` for the winning row.

```sql
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_hash TEXT NOT NULL,
    file_id INTEGER REFERENCES file_registry(id) ON DELETE SET NULL,
```

- `file_hash`: BLAKE3 content hash. One row per hashed file — multiple files with the same hash form a duplicate group.
- `file_id`: The file_registry row this metadata belongs to. Set by the hasher on INSERT. Updated by the scorer to point to the master copy for each duplicate group.

```sql
    exif_image_width        INTEGER,
    exif_image_height       INTEGER,
    exif_gps_latitude       REAL,
    exif_gps_longitude      REAL,
    exif_make               TEXT,
    exif_model              TEXT,
    exif_date_time_original TEXT,
    exif_create_date        TEXT,
```

- EXIF columns: Cached per file during hashing. `date_time_original` and `create_date` are used by the scorer (+10 base signal). `image_width`/`image_height` and `gps_latitude`/`gps_longitude` are available for the VFS phase.

```sql
    created_at TEXT DEFAULT (datetime('now'))
```

**Trigger:**
```sql
CREATE TRIGGER trg_file_metadata_hashed
AFTER INSERT ON file_metadata
FOR EACH ROW
BEGIN
    UPDATE file_registry SET scan_status = 'HASHED' WHERE id = NEW.file_id;
END;
```
When the hasher inserts a row, the trigger automatically marks the corresponding `file_registry` row as `HASHED`.

**Indexes:**
- `idx_file_metadata_hash_file` (UNIQUE): Prevents duplicate (hash, file) entries and serves hash-only lookups via the leftmost column.
