# WanderSort

**Your photos tell the story of your life. They deserve better than a black-box algorithm deciding what you see.**

WanderSort is a local-first media organiser that scans your chaotic pile of photos and videos — scattered across hard drives, SD cards, and phone dumps — and structures them into a clean, human-readable folder tree you actually understand.

No cloud uploads. No subscriptions. No AI deciding what's "relevant." Just your files, your rules, your structure.

---

## The Problem

You went to Goa six months ago. Shot a ton on your phone — some on a DSLR too. You want to make a reel. You remember the clip of that beach at golden hour, the slow-mo of waves crashing, the photo your friend edited and airdropped back.

Where is any of it?

You open your laptop. `DCIM`. `Backup`. `Camera Roll`. `New Folder (2)`. `Backup_old_old`. A USB drive labelled "Photos 2023" that might actually be from 2022. Forty minutes in, you've found half the clips, zero of the edited versions, and you've already given up on finding the DSLR shots.

**This is the problem.** You've got 40,000 photos. Maybe 200,000. Years of memories spread across devices, drives, and folders — and no sane way to find what you need when you actually need it.

The specifics make it worse:

- **Phone dumps** — 3 copies of the same photo because you backed up twice and forgot.
- **DSLR shoots** — RAW + JPG + sidecar files, all separated from each other.
- **iPhone Live Photos** — the `.HEIC`, the `.MOV`, the `.AAE` edit file — scattered across 3 different folders.
- **Edited variants** — `IMG_E3162.HEIC` sitting next to `IMG_3162.HEIC` with no way to know which is which.

WanderSort's goal is simple: **you should never have to dig through that mess again.**

Point it at your drives. Let it scan, hash, group, and score. Review the proposed structure. Hit go. Your files land where they belong — organised by date, by location, by event — in a folder tree that makes sense to a human being, not an algorithm.

And the next time you dump 500 photos from a trip? Just point WanderSort at the folder. It already knows the structure. It already knows what's duplicated. It just slots the new files in.

Your memories. Your structure. Your machine.

---

## Who This Is For

WanderSort is for people who care about where their files actually live.

- **Content creators** who shoot in multiple formats and need a predictable folder structure they can navigate and rely on.
- **Photographers** with years of RAW + JPG pairs, sidecar files, and edited variants that need to stay grouped — not scattered.
- **Digital hoarders** (respectfully) who have terabytes of memories and want to finally organise them — and *keep* them organised going forward.
- **Anyone who refuses to pay a tech giant** a recurring fee to store and sort what's already theirs.

If you want a visual hierarchy — `2024/Goa/Day-2/sunset_01.heic` instead of `DCIM/100APPLE/IMG_4721.HEIC` — WanderSort is being built for you.

---

## Who This Is Not For

If Google Photos or iCloud works for you — genuinely, no judgement — this isn't your tool. WanderSort is for people who want to *own* their media organisation, not rent it.

---

## How It Works

WanderSort processes your media through a multi-stage pipeline. You point it at one or more directories, and it does the rest.

```text
┌──────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐
 │            | SCAN │────────▶│ CLASSIFY │────────▶│ HASH │────────▶│ SCORE │────────▶│ ORGANISE │      │
└──────────┘     └──────────┘     └──────────┘     └──────────┘     └──────────┘
  Discover                                             Identify                                    Fingerprint                                 Rank                               Build the
  every file                                            what it is                                  every byte                                  the best copy                folder tree
```

### Stage 1 — Scan

Point WanderSort at any directory (or many). It recursively walks every path, discovers every media file, and skips the noise (`.DS_Store`, `Thumbs.db`, `.git`, `node_modules`).

Every file gets classified into one of four types:

| Type | What It Means | Formats |
|------|---------------|---------|
| **Image** | Standard photos | `.jpg` `.jpeg` `.png` `.heic` `.bmp` `.webp` |
| **Video** | Video recordings | `.mp4` `.mov` |
| **RAW** | Camera originals | `.cr2` `.dng` |
| **Sidecar** | Edit metadata | `.aae` |

Files that don't match any known media type are logged separately — nothing gets silently lost.

### Stage 2 — Capture Grouping

This is where WanderSort gets smart about *relationships*.

When you take a photo on an iPhone, you don't get one file. You get several:

```text
IMG_3162.HEIC        ← The original photo
IMG_3162.MOV         ← The Live Photo video
IMG_3162.AAE         ← The edit sidecar
IMG_E3162.HEIC       ← The edited version
IMG_E3162.MOV        ← The edited Live Photo video
```

WanderSort understands these patterns. It groups related files into **capture groups** — files from the same shutter press, regardless of format or variant. Same logic applies to DSLR pairs like `_MG_1721.JPG` + `_MG_1721.CR2`.

Every file in a capture group gets a **role**: `ORIGINAL`, `LIVE_VIDEO`, `SIDECAR`, `EDITED`, `RAW`, etc. No manual tagging. No guesswork. The relationships are derived from the filenames themselves.

### Stage 3 — Hash

Every file is fingerprinted using **BLAKE3** — a streaming hash that processes files of any size with constant memory (about 32KB of overhead, whether the file is 2MB or 20GB).

The hash creates **content groups**: byte-identical files across *any* location on *any* drive are grouped together. If the same `sunset.heic` exists in `~/Photos`, `~/Backup`, and `/Volumes/USB-Drive/old`, WanderSort knows it's the same file, three times.

**Capture groups tell you what belongs together. Content groups tell you what's duplicated.**

### Stage 4 — Score *(work in progress)*

Once files are hashed and grouped, WanderSort scores each copy to determine which one should be the **master** — the definitive version to keep. Scoring considers:

- Metadata richness (EXIF data, GPS coordinates, timestamps)
- Directory path quality (a folder named `2024/Goa` beats `New Folder/Untitled`)
- Filename patterns (date-stamped names score higher than generic camera sequences)

### Stage 5 — Organise *(work in progress)*

The final stage: WanderSort builds a virtual folder tree based on the metadata it has gathered — dates, locations, device info — and proposes the new structure. You review it. You approve it. Only then does it move (or copy) your files.

Nothing moves without your explicit sign-off.

---

## The Pipeline Philosophy

```text
Scan → Deduce → Review → Execute
```

WanderSort never touches your files until you tell it to. The entire pipeline is designed around **non-destructive discovery**: scan everything, figure out the relationships and duplicates, show you the plan, and only act when you say go.

---

## Metadata Extraction

WanderSort extracts deep EXIF metadata from your files using [ExifTool](https://exiftool.org/) — and it understands 11 different file format structures natively:

| Data | What WanderSort Reads |
|------|-----------------------|
| **Camera** | Make, Model, Lens, Software |
| **Timestamps** | Original capture date, modification date, file system dates |
| **Exposure** | ISO, Aperture, Shutter Speed, Focal Length, Flash, Metering Mode |
| **GPS** | Latitude, Longitude, Altitude |
| **Dimensions** | Width, Height, Megapixels, Orientation |

This metadata powers the scoring engine and, ultimately, the folder structure decisions.

---

## Architecture

WanderSort runs as a local server with an API-driven architecture. The backend is written in Go for raw performance — concurrent file walking, parallel hashing, and batched database writes make it capable of processing terabytes without breaking a sweat.

**Key internals:**

- **SQLite** for the file registry — portable, zero-config, single-file database
- **BLAKE3** for hashing — cryptographic-grade speed, streaming with constant memory
- **Concurrent pipeline** — worker pools for scanning, hashing, and scoring with bounded concurrency
- **WebSocket status streaming** — real-time progress updates as files are processed
- **Serialised writes** — a bulk writer ensures database consistency without sacrificing throughput
- **Capture grouping** — understands iPhone and DSLR filename conventions natively

---

## Current Status

WanderSort is under active development. Here's what's built and what's coming:

| Stage | Status |
|-------|--------|
| File scanner with concurrent directory walking | ✅ Done |
| File classifier (13 media formats, 4 categories) | ✅ Done |
| Capture group detection (iPhone & DSLR patterns) | ✅ Done |
| BLAKE3 hashing with content group deduplication | ✅ Done |
| EXIF metadata extraction (11 format-specific parsers) | ✅ Done |
| SQLite schema with full file registry | ✅ Done |
| API with WebSocket real-time status | ✅ Done |
| Metadata scoring engine | 🔧 In progress |
| Deduplication with master file selection | 🔧 In progress |
| Virtual folder tree builder | 📋 Planned |
| Review UI for approving organisation proposals | 📋 Planned |
| Safe copy/move engine with verification | 📋 Planned |

---

*Inspired by [@WanderWithSky](https://drive.google.com/file/d/1QIDtm5rTkwzkQxyVaPqPN8J81vGuS-AF/view?usp=sharing)*
