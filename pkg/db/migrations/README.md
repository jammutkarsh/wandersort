# Why

## **TABLE 1: `file_registry`**

**Purpose:** The **census** of every file you've ever scanned. It answers: "Does this file exist in my system?"

```sql
CREATE TABLE file_registry (
    id BIGSERIAL PRIMARY KEY,
```

**Why BIGSERIAL?** You might have 500k files (300GB ÷ 600KB avg). BIGSERIAL handles up to 9 quintillion rows.

```sql
    -- Physical identity
    file_path TEXT NOT NULL,              
    file_hash CHAR(64) NOT NULL,          
    file_size BIGINT NOT NULL,
    file_modified_at TIMESTAMP NOT NULL,
```

**The "fingerprint" columns:**

- `file_path`: Absolute path like `D:/Photos/2023/IMG_001.jpg`. **Why needed?** To track where the file lives RIGHT NOW (before reorganization).
- `file_hash`: BLAKE3 hash. **Why?** This is the file's **content identity**. Two files with identical hashes are byte-for-byte identical, even if named differently.
- `file_size`: Bytes. **Why?** Quick pre-filter before hashing (if size differs, they're definitely not duplicates).
- `file_modified_at`: File system timestamp. **Why?** Detect if file changed since last scan (avoid re-hashing unchanged files).

```sql
    -- Discovery metadata
    discovered_at TIMESTAMP DEFAULT NOW(),
    scan_session_id UUID NOT NULL,        
    source_root TEXT NOT NULL,             
```

**The "audit trail" columns:**

- `discovered_at`: When we first saw this file. **Why?** You can query "show me files added this week".
- `scan_session_id`: Which scan found this. **Why?** Critical for incremental scans. If file isn't seen in latest scan, it was deleted.
- `source_root`: Which root path (`D:/Photos` or `E:/Backups`). **Why?** You might scan 5 different drives. This tracks which drive each file came from.

```sql
    -- File type classification
    media_type VARCHAR(20),                -- IMAGE, VIDEO, SIDECAR, RAW
    file_extension VARCHAR(10),
```

**The "classification" columns:**

- `media_type`: Enum-like value. **Why?** Different processing pipelines:
  - `IMAGE` → Extract EXIF
  - `VIDEO` → Run ffmpeg
  - `SIDECAR` → Don't organize separately, attach to primary file
  - `RAW` → Might pair with JPEG
- `file_extension`: Normalized extension (`.jpg` not `.JPG`). **Why?** Fast filtering in queries without string operations on path.

```sql
    -- Processing state
    scan_status VARCHAR(20) DEFAULT 'DISCOVERED',  
```

**The "workflow state" column:**

- `scan_status`: State machine. **Why?** Your scan happens in stages:
  1. `DISCOVERED` → File found during walk
  2. `HASHED` → BLAKE3 computed
  3. `ANALYZED` → Metadata extracted
  4. `ERROR` → Something failed (permission denied, corrupt file)

This lets you resume interrupted scans: "Hash all files where scan_status='DISCOVERED'".

```sql
    UNIQUE(file_path),
    INDEX idx_hash (file_hash),
    INDEX idx_session (scan_session_id),
    INDEX idx_status (scan_status)
);
```

**Indexes explained:**

- `UNIQUE(file_path)`: Prevent duplicate entries. One row per file path.
- `INDEX idx_hash`: **Critical for deduplication.** Query "find all files with this hash" in milliseconds, not seconds.
- `INDEX idx_session`: Fast cleanup of old scans.
- `INDEX idx_status`: Query "get next 1000 files to hash" efficiently.

---

## **TABLE 2: `file_relationships`**

**Purpose:** Model the fact that **some files travel together** (iPhone .jpg + .aae, DSLR .CR2 + .jpg).

```sql
CREATE TABLE file_relationships (
    id BIGSERIAL PRIMARY KEY,
    primary_file_id BIGINT REFERENCES file_registry(id),
    related_file_id BIGINT REFERENCES file_registry(id),
    relationship_type VARCHAR(20),  -- SIDECAR_AAE, RAW_PAIR, LIVE_PHOTO
    
    UNIQUE(primary_file_id, related_file_id, relationship_type)
);
```

**Why this table exists:**

**Problem without it:**

```text
You organize IMG_001.jpg to /2023/May/Wedding/
But IMG_001.aae stays behind in old folder → edits lost
```

**Solution:**

```sql
-- When copying IMG_001.jpg, query:
SELECT related_file_id FROM file_relationships 
WHERE primary_file_id = 12345 AND relationship_type = 'SIDECAR_AAE'

-- Result: Also copy IMG_001.aae to same destination
```

**Column details:**

- `primary_file_id`: The "main" file (JPEG).
- `related_file_id`: The "companion" file (AAE sidecar).
- `relationship_type`: **Why enum-like?** Different handling rules:
  - `SIDECAR_AAE` → Always copy together
  - `RAW_PAIR` → User might choose to only organize JPEGs, leave RAWs in archive
  - `LIVE_PHOTO` → The .MOV component of an iPhone Live Photo

**Why UNIQUE constraint?**
Prevent duplicate relationships: `(IMG_001.jpg, IMG_001.aae, SIDECAR_AAE)` inserted twice.

---

## **TABLE 3: `content_groups`**

**Purpose:** The **deduplication brain**. Groups files with identical content and picks the "best" version.

```sql
CREATE TABLE content_groups (
    id BIGSERIAL PRIMARY KEY,
    content_hash CHAR(64) UNIQUE NOT NULL,  
    master_file_id BIGINT REFERENCES file_registry(id),  
    total_copies INT DEFAULT 1,
```

**Why separate table instead of just using `file_registry.file_hash`?**

**Scenario:**

```text
Hash abc123 found in:
- D:/Backup/IMG_001.jpg (no EXIF)
- D:/iPhone/IMG_001.jpg (has EXIF, GPS)
- E:/Old/IMG_001.jpg (no EXIF)
```

Without `content_groups`:

```sql
-- How do you query "which is the master"?
SELECT * FROM file_registry WHERE file_hash = 'abc123' 
-- Returns 3 rows, but which one do you organize?
```

With `content_groups`:

```sql
SELECT master_file_id FROM content_groups WHERE content_hash = 'abc123'
-- Returns: The iPhone version (has metadata, highest score)
```

**Column details:**

- `content_hash`: The BLAKE3 hash (acts as group key).
- `master_file_id`: Points to the **best** file in this group. **Why needed?** During organization, only copy the master, ignore duplicates.
- `total_copies`: Count of duplicates. **Why?** UI can show "3 duplicates found, saving 12MB".

```sql
    -- Master selection criteria
    has_metadata BOOLEAN DEFAULT FALSE,
    metadata_richness_score INT DEFAULT 0,  -- Higher = better
```

**Scoring columns:**

- `has_metadata`: Quick check if ANY file in group has EXIF. **Why?** Faster than joining to `file_metadata` table.
- `metadata_richness_score`: Numeric score for master election. **Why separate column?** Avoids recalculating score on every query.

**Score calculation example:**

```text
File A: EXIF timestamp + GPS + meaningful folder = 10 + 5 + 2 = 17
File B: EXIF timestamp only = 10
File C: No metadata = 0

→ File A is master
```

---

## **TABLE 4: `content_group_members`**

**Purpose:** The **many-to-many link** between content groups and files.

```sql
CREATE TABLE content_group_members (
    group_id BIGINT REFERENCES content_groups(id),
    file_id BIGINT REFERENCES file_registry(id),
    is_master BOOLEAN DEFAULT FALSE,
    
    PRIMARY KEY(group_id, file_id)
);
```

**Why not just add `group_id` column to `file_registry`?**

**Because of this edge case:**

```text
File is discovered, hash computed → Group created, file marked as master

Later scan: File is DELETED from disk

You run cleanup: DELETE FROM file_registry WHERE path doesn't exist

Now: content_groups.master_file_id points to DELETED row → broken foreign key
```

**With junction table:**

```sql
-- Delete file
DELETE FROM content_group_members WHERE file_id = 999;
DELETE FROM file_registry WHERE id = 999;

-- Re-elect master
UPDATE content_groups SET master_file_id = (
    SELECT file_id FROM content_group_members 
    WHERE group_id = X ORDER BY score DESC LIMIT 1
);
```

**Column details:**

- `is_master`: Denormalized flag. **Why?** Faster queries than always joining to `content_groups`.

---

## **TABLE 5: `file_metadata`**

**Purpose:** Store **extracted EXIF/XMP data** separate from file registry.

```sql
CREATE TABLE file_metadata (
    file_id BIGINT PRIMARY KEY REFERENCES file_registry(id),
    
    -- EXIF/XMP data
    captured_at TIMESTAMP,
    camera_make VARCHAR(100),
    camera_model VARCHAR(100),
    
    -- GPS
    latitude DECIMAL(10, 8),
    longitude DECIMAL(11, 8),
    
    -- Video specific
    duration_seconds INT,
    codec VARCHAR(50),
```

**Why separate table instead of columns in `file_registry`?**

1. **Not all files have metadata:**
   - Sidecar files (.aae) don't have EXIF
   - Screenshots don't have GPS
   - Corrupted files can't be parsed

   Separate table = sparse data doesn't bloat main table with NULLs.

2. **Metadata extraction is SLOW:**

   ```text
   Scanning 500k files: 30 minutes (just walking filesystem)
   Extracting EXIF from 500k files: 5 hours (opening every file)
   ```

   You can scan first (populate `file_registry`), then extract metadata later (populate `file_metadata` in batches).

**Column details:**

- `captured_at`: The EXIF `DateTimeOriginal`. **Why most important?** Determines which Year/Month folder file goes into.
- `latitude/longitude`: GPS coordinates. **Why DECIMAL(10,8)?** Standard precision for GPS (e.g., `28.61234567, 77.20345678`).
- `duration_seconds`: Video length. **Why needed?** Detect if iPhone Live Photo (.MOV is 1-3 seconds vs normal video).

```sql
    -- Source of metadata
    metadata_source VARCHAR(50),  -- EXIF, FILENAME, INFERRED_PEER, INFERRED_PATH
    confidence_score INT,          -- 0-100
```

**The "trust level" columns:**

- `metadata_source`: **Critical for debugging.** If user complains "why is this photo in wrong year?", you can trace back.
  
  Examples:
  - `EXIF` → 100% reliable
  - `INFERRED_PEER` → 70% reliable (copied from neighbors)
  - `INFERRED_PATH` → 30% reliable (guessed from folder name)

- `confidence_score`: Numeric rating. **Why?** UI can show warning icon for low-confidence files.

**Example query:**

```sql
-- "Show me files that might be wrongly dated"
SELECT fr.file_path, fm.captured_at, fm.metadata_source
FROM file_registry fr
JOIN file_metadata fm ON fm.file_id = fr.id
WHERE fm.confidence_score < 50;
```

---

## **TABLE 6: `folder_context`**

**Purpose:** Extract **semantic meaning** from folder names.

```sql
CREATE TABLE folder_context (
    id BIGSERIAL PRIMARY KEY,
    folder_path TEXT UNIQUE NOT NULL,
    
    -- Extracted tokens
    year_hint INT,
    event_hint TEXT,                -- "ABS_Farewell", "Diwali_2018"
    location_hint TEXT,
```

**Why this table exists:**

**Your semi-organized folders contain clues:**

```text
D:/Photos/2018/Diwali/
          ^^^^  ^^^^^^
          year  event
```

**Without this table:**
Every time you process a file, you'd tokenize the path:

```go
// SLOW: Parsing path for every file
for each file in folder {
    year = extractYear(file.path)  // Regex on every iteration
    event = extractEvent(file.path)
}
```

**With this table:**

```go
// FAST: Parse folder once, reuse for all files
folder_context = db.get("/Photos/2018/Diwali")
// Already computed: year_hint=2018, event_hint="Diwali"

for each file in folder {
    file.year = folder_context.year_hint  // Instant lookup
}
```

**Column details:**

- `year_hint`: Extracted via regex `(19|20)\d{2}`. **Why INT not TEXT?** So you can query `WHERE year_hint BETWEEN 2015 AND 2020`.
- `event_hint`: The unique folder token. **Why TEXT?** Free-form names like `"ABS_Farewell"`, `"Sujal_Wedding"`.

```sql
    -- AI analysis
    ai_event_name TEXT,             -- Gemini's interpretation
    ai_confidence DECIMAL(3,2),     -- 0.00 - 1.00
```

**The AI normalization columns:**

**Problem:**

```text
User has folders:
- D:/College/CS-H 7th Sem/
- E:/Backups/College_2023/
- F:/Old/College Memories/

These are ALL the same event, but different names.
```

**Solution:**

```sql
-- Send all event hints to Gemini API
SELECT DISTINCT event_hint FROM folder_context;

-- Gemini returns: {"College": ["CS-H 7th Sem", "College_2023", "College Memories"]}

-- Update DB
UPDATE folder_context 
SET ai_event_name = 'College' 
WHERE event_hint IN ('CS-H 7th Sem', 'College_2023', 'College Memories');
```

Now when organizing, all three folders map to same event: `2023/09_Sep/College/`.

**Why `ai_confidence` column?**
Gemini might be uncertain:

- `"College"` → 0.95 confidence (very sure)
- `"Random_Stuff"` → 0.30 confidence (low certainty)

UI can flag low-confidence for user review.

```sql
    -- User corrections
    user_confirmed_event TEXT,
    user_confirmed_location TEXT,
```

**The "human override" columns:**

AI said "College", but user corrects to "University Farewell" → store in `user_confirmed_event`.

**Why separate column instead of updating `ai_event_name`?**
Preserve the audit trail:

```sql
SELECT ai_event_name, user_confirmed_event FROM folder_context WHERE folder_path = '...';

-- Shows: AI guessed "College", user corrected to "University Farewell"
```

You can later retrain your AI model with this correction data.

---

## **TABLE 7: `virtual_organization`**

**Purpose:** The **proposal layer**. Nothing is moved until user approves.

```sql
CREATE TABLE virtual_organization (
    id BIGSERIAL PRIMARY KEY,
    file_id BIGINT REFERENCES file_registry(id),
    
    -- Proposed destination
    proposed_year INT,
    proposed_month INT,
    proposed_event TEXT,
    proposed_location TEXT,
    proposed_sub_location TEXT,
```

**Why this entire table exists:**

**Critical insight:** You're reorganizing 300GB. If you auto-move files and get it wrong, **data recovery is hard**.

**Instead:**

```text
1. Scan files (read-only)
2. Populate virtual_organization with proposals
3. User reviews in UI: "2018/May/Diwali" ✓ or ✗
4. Only then execute physical copy
```

**Column details:**

- `proposed_year/month`: Derived from `file_metadata.captured_at`. **Why separate columns?** So you can query `GROUP BY proposed_year` to show stats.
- `proposed_event`: From folder context or AI. **Why TEXT?** User-defined names.
- `proposed_location/sub_location`: From GPS reverse-geocoding. Example: `India` / `Indore`.

```sql
    -- Reasoning
    decision_source VARCHAR(50),    -- EXIF_GPS, FOLDER_NAME, AI_INFERENCE, USER_EDIT
    decision_confidence DECIMAL(3,2),
```

**The "explainability" columns:**

**Why needed?** User sees proposal and thinks "Why did you put this in May?"

UI shows:

```text
File: IMG_001.jpg
Proposed: 2023/05_May/Wedding/Indore
Reasoning: EXIF_GPS (Confidence: 95%)
```

User can then decide to override low-confidence proposals.

```sql
    -- User interaction
    user_approved BOOLEAN DEFAULT FALSE,
    user_modified_at TIMESTAMP,
```

**The "workflow gate" columns:**

**Critical:** This is what controls execution.

```sql
-- Execution engine ONLY processes approved files
SELECT * FROM virtual_organization WHERE user_approved = TRUE;
```

User workflow:

1. System proposes organization
2. User clicks "Approve All" or individually approves
3. `user_approved` flips to TRUE
4. Execution runs

`user_modified_at` tracks when user last changed proposal (for audit).

```sql
    -- Final destination path (computed)
    target_path TEXT,               -- /Year/Month/Event/filename.jpg
    
    UNIQUE(file_id)
);
```

**Why `target_path` column?**

**Computed from other columns:**

```sql
target_path = proposed_year + '/' + proposed_month + '/' + proposed_event + '/' + filename
```

**Why store it instead of computing on-the-fly?**

1. **Performance:** Execution engine processes 100k files. Computing path 100k times is slower than reading precomputed value.
2. **Immutability:** If you change path format logic later, old proposals stay as-is.

**Why UNIQUE(file_id)?**
One proposal per file. If you reprocess, you UPDATE existing row, don't INSERT duplicate.

---

## **TABLE 8: `execution_log`**

**Purpose:** The **permanent record** of what was copied where.

```sql
CREATE TABLE execution_log (
    id BIGSERIAL PRIMARY KEY,
    file_id BIGINT REFERENCES file_registry(id),
    
    source_path TEXT NOT NULL,
    destination_path TEXT NOT NULL,
    
    copied_at TIMESTAMP,
    verified BOOLEAN DEFAULT FALSE,  -- Hash match check
```

**Why this table exists:**

**Critical for safety:**

```text
User clicks "Execute" → 50k files copied

Week later: "Where did IMG_001.jpg go?"

Query execution_log:
- Source: D:/Old/IMG_001.jpg
- Destination: F:/Library/2023/05_May/Wedding/IMG_001.jpg
- Verified: TRUE (hash matched after copy)
```

**Column details:**

- `source_path`: Where file came FROM. **Why needed?** After reorganization, original folders might be deleted. This is the only record.
- `destination_path`: Where file went TO.
- `verified`: Did hash match after copy? **Critical safety check.**

```sql
    execution_session_id UUID NOT NULL,
    
    INDEX idx_session (execution_session_id)
);
```

**Why `execution_session_id`?**

**Scenario:**

```text
Day 1: Execute 30k files (session A)
Day 2: User adds more photos, approves 5k files
Day 3: Execute 5k files (session B)

User asks: "What did you copy on Day 3?"

Query: SELECT * FROM execution_log WHERE execution_session_id = 'session-B'
```

Without session IDs, you'd need to rely on timestamp ranges (less reliable).

---

## **TABLE 9: `scan_sessions`**

**Purpose:** Track **incremental scans** for idempotent re-scanning.

```sql
CREATE TABLE scan_sessions (
    id UUID PRIMARY KEY,
    started_at TIMESTAMP DEFAULT NOW(),
    completed_at TIMESTAMP,
    status VARCHAR(20),  -- RUNNING, COMPLETED, FAILED
    
    root_paths JSONB,    -- ["D:/Photos", "E:/Backups"]
    files_discovered INT DEFAULT 0,
    files_hashed INT DEFAULT 0,
    files_analyzed INT DEFAULT 0
);
```

**Why this table exists:**

**Problem:** You scan 300GB, laptop crashes at 80%.

**Without scan sessions:**

```text
Restart → Scan all 300GB again (wasted time)
```

**With scan sessions:**

```sql
-- Resume from last session
SELECT MAX(id) FROM scan_sessions WHERE status = 'FAILED';

-- Re-scan only files not processed in that session
SELECT * FROM file_registry 
WHERE scan_session_id != <failed_session_id>;
```

**Column details:**

- `root_paths`: JSONB array. **Why JSONB?** Flexible - you might scan 2 roots today, 5 roots tomorrow.
- `files_discovered/hashed/analyzed`: Progress counters. **Why?** UI shows progress bar.

**Example UI:**

```text
Scan Session: abc-123
Status: RUNNING
Progress: 45,000 / 50,000 files hashed (90%)
```
