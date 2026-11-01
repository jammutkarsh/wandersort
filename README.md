[![website](https://img.shields.io/badge/website-wandersort.utkarshchourasia.in-blue)](https://wandersort.utkarshchourasia.in) [![CI](https://github.com/jammutkarsh/wandersort/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/jammutkarsh/wandersort/actions/workflows/ci.yml)

# WanderSort

WanderSort is a local-first media organiser that scans your chaotic pile of photos and videos — scattered across hard drives, SD cards, and phone dumps — and structures them into a clean, human-readable folder tree you actually understand.

[Read more](https://wandersort.utkarshchourasia.in/docs)

## Install

[macOS](#macos) · [Windows](#windows) · [Linux](#linux)

#### macOS

```bash
# Homebrew
brew install --cask jammutkarsh/tap/wandersort
```

```bash
# Install script
curl -fsSL https://wandersort.utkarshchourasia.in/install.sh | sh
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
irm https://wandersort.utkarshchourasia.in/install.ps1 | iex
```

```powershell
# Scoop
scoop bucket add jammutkarsh https://github.com/jammutkarsh/scoop-bucket
scoop install wandersort
```

#### Linux

```bash
# Install script
curl -fsSL https://wandersort.utkarshchourasia.in/install.sh | sh
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

Each library has its own unique settings. To change them, go to the Settings tab in the TUI.

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

*Inspired by [@WanderWithSky](https://drive.google.com/file/d/1QIDtm5rTkwzkQxyVaPqPN8J81vGuS-AF/view?usp=sharing)*
