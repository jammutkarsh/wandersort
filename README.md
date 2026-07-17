> SPDX-License-Identifier: AGPL-3.0-or-later
>
> Copyright (c) 2026 Utkarsh Chourasia

[![CI](https://github.com/jammutkarsh/wandersort/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/jammutkarsh/wandersort/actions/workflows/ci.yml)

# WanderSort

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
- **Home-lab and self-hosting folks** with a stack of HDDs/SSDs and a NAS, who want their media organised on their own hardware — not rented from a cloud.

If you want a visual hierarchy — `2024/Goa/Day-2/sunset_01.heic` instead of `DCIM/100APPLE/IMG_4721.HEIC` — WanderSort is being built for you.

---

## Who This Is Not For

If Google Photos or iCloud works for you — genuinely, no judgement — this isn't your tool. WanderSort is for people who want to *own* their media organisation, not rent it.

---

## Getting Started

WanderSort is a single, self-contained command-line binary.

### Install

Build from source (requires Go 1.26+):

```bash
git clone https://github.com/jammutkarsh/wandersort.git
cd wandersort
make build          # produces ./bin/wandersort
```

### Usage

```bash
# 1. Scan one or more directories. Runs in the foreground until it finishes.
#    Dependencies (ExifTool + location database) install automatically on
#    first run — no separate setup step required.
wandersort scan --paths ~/Pictures,/Volumes/SD

# 2. See what was found — scanned, hashed, and duplicate counts.
wandersort report
```

> Prefer to pre-install the dependencies instead of downloading mid-scan? Run `wandersort setup` first (optional).

Run `wandersort --help`, or `wandersort <command> --help`, for the full command and flag reference. Any flag can also be set through an environment variable (e.g. `OUTPUT_PATH`, `WORKERS`).

An optional REST API server is available via `wandersort serve` for programmatic integrations.

---

## Architecture

see [ARCHITECTURE.md](ARCHITECTURE.md).

---

*Inspired by [@WanderWithSky](https://drive.google.com/file/d/1QIDtm5rTkwzkQxyVaPqPN8J81vGuS-AF/view?usp=sharing)*
