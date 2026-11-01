[![website](https://img.shields.io/badge/website-wandersort.utkarshchourasia.in-blue)](https://wandersort.utkarshchourasia.in) [![docs](https://img.shields.io/badge/docs-wandersort.utkarshchourasia.in%2Fdocs-blue)](https://wandersort.utkarshchourasia.in/docs) [![CI](https://github.com/jammutkarsh/wandersort/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/jammutkarsh/wandersort/actions/workflows/ci.yml)

# WanderSort

WanderSort is a local-first media organiser that scans your chaotic pile of photos and videos — scattered across hard drives, SD cards, and phone dumps — and structures them into a clean, human-readable folder tree you actually understand.

No cloud uploads. No subscriptions. No AI deciding what's "relevant." Just your files, your rules, your structure.

## Getting Started

WanderSort is a single, self-contained command-line binary.

### Install

[macOS](#macos) · [Windows](#windows) · [Linux](#linux)

#### macOS

```bash
# Homebrew
brew install --cask jammutkarsh/tap/wandersort
```

```bash
# Install script
curl -fsSL https://raw.githubusercontent.com/jammutkarsh/wandersort/main/scripts/install.sh | sh
```

```bash
# Release package
# download the .pkg from https://github.com/jammutkarsh/wandersort/releases/latest and open it
# it is unsigned; if macOS blocks it, run:
sudo xattr -dr com.apple.quarantine /usr/local/bin/wandersort
```

#### Windows

```powershell
# winget
winget install jammutkarsh.wandersort
```

```powershell
# Install script
irm https://raw.githubusercontent.com/jammutkarsh/wandersort/main/scripts/install.ps1 | iex
```

```powershell
# Scoop
scoop bucket add jammutkarsh https://github.com/jammutkarsh/scoop-bucket
scoop install wandersort
```

#### Linux

```bash
# Install script
curl -fsSL https://raw.githubusercontent.com/jammutkarsh/wandersort/main/scripts/install.sh | sh
```

```bash
# Go 1.27+
go install github.com/jammutkarsh/wandersort@latest
```

```bash
# apt
# download the .deb from https://github.com/jammutkarsh/wandersort/releases/latest, then:
sudo apt install ./wandersort_<version>_linux_amd64.deb
```

### Usage

#### TUI

```bash
# settings, adding folders, correcting the plan and copying, all on screen
wandersort
```

Settings (folder layout, home and work, fine-tuning) live in the library itself, so each library keeps its own. `wandersort` opens the one you used last; `--output-path` picks another.

#### CLI

```bash
# dependencies (ExifTool + locationDB) download on first use, in up to three tries
wandersort add --paths ~/Pictures,/Volumes/SD   # plan where every file goes
wandersort organise                              # correct the plan
wandersort copy                                  # copy the files in
wandersort check                                 # later: verify the library
```

Run `wandersort --help`, or `wandersort <command> --help`, for the full command and flag reference.

<!--
### Running unattended

`add`, `copy` and `check` need no terminal. Without one they never prompt and print one line per stage. With no library yet, `add` creates `~/WandersortLibrary` with default settings.

```bash
# every night: plan new uploads, then copy them in
0 2 * * *  wandersort add -p /mnt/uploads -o /mnt/photos && wandersort copy -o /mnt/photos
```

| Exit | Meaning |
|---|---|
| 0 | Finished (also when there was nothing new) |
| 1 | Stopped by an error; the next run carries on |
| 2 | Couldn't start: bad flags or arguments |
| 3 | Finished, but some files failed; each is named |
| 4 | Another wandersort is using the library |

`--json` prints one result object on stdout at the end, failures included; progress stays on stderr. A copy that leaves files out writes an HTML page listing each one, grouped by the drive it came from, beside that run's log in `~/.wandersort/logs/`.
-->

---

## The Problem

You went to the coast six months ago. Shot a ton on your phone — some on a DSLR too. You want to make a reel. You remember the clip of that beach at golden hour, the slow-mo of waves crashing, the photo your friend edited and airdropped back.

Where is any of it?

You open your laptop. `DCIM`. `Backup`. `Camera Roll`. `New Folder (2)`. `Backup_old_old`. A USB drive labelled "Photos 2023" that might actually be from 2022. Forty minutes in, you've found half the clips, zero of the edited versions, and you've already given up on finding the DSLR shots.

**This is the problem.** You've got 40,000 photos. Maybe 200,000. Years of memories spread across devices, drives, and folders — and no sane way to find what you need when you actually need it.

The specifics make it worse:

- **Phone dumps** — 3 copies of the same photo because you backed up twice and forgot.
- **DSLR shoots** — RAW + JPG + sidecar files, all separated from each other.
- **Live Photos** — the `.HEIC`, the `.MOV`, the `.AAE` edit file — scattered across 3 different folders.
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

If you want a visual hierarchy — `2024/08_August/02_04/<city>/sunset_01.heic` instead of `DCIM/100APPLE/IMG_4721.HEIC` — WanderSort is being built for you.

---

## Who This Is Not For

If Google Photos or iCloud works for you — genuinely, no judgement — this isn't your tool. WanderSort is for people who want to *own* their media organisation, not rent it.

---

---

## Architecture

The codebase map is [CLAUDE.md](CLAUDE.md); coding rules are in [AGENTS.md](AGENTS.md).

---

*Inspired by [@WanderWithSky](https://drive.google.com/file/d/1QIDtm5rTkwzkQxyVaPqPN8J81vGuS-AF/view?usp=sharing)*
