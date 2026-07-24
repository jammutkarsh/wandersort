> SPDX-License-Identifier: AGPL-3.0-or-later
>
> Copyright (c) 2026 Utkarsh Chourasia

# WanderSort TUI design system

Every full-screen surface (scan, setup, review, reset) is built from the same
small kit so the app reads as one product. If you add a screen, compose it from
these pieces — don't invent new colours, markers, or layout rules.

## Tokens (`theme.go`)

Adaptive truecolor palette — each colour resolves per light/dark terminal:

| Role | Use for |
|---|---|
| `Primary` | brand, focus, the running-stage marker, key hints |
| `Success` / `Warn` / `Error` | done / warning / failure — semantic only, never decoration |
| `Fg` / `Muted` / `Subtle` | primary text / secondary text / borders & pending rows |
| `Highlight` | selected-row background (whole-line, no marker characters) |

Semantic styles wrap them: `Title`, `Text`, `DimText`, `FaintTxt`, `OK`,
`Attn`, `Bad`, `Selected`, `Box`. Use the style, not the colour, so a palette
change stays one-file.

## Layout rules

- **Alt-screen, full width, full height.** Chrome at top (`Banner`), key help
  at bottom (`Footer` inside `Screen`, which pins it to the terminal's last
  row). Whatever vertical space is left belongs to the screen's live content —
  never a fixed-height window with a void under it.
- **Right column is time.** Durations right-align at the terminal edge
  (`row()` in `stagelist.go`); content truncates with `…`, the time never does.
- **One footer, measured.** Key help wraps on narrow terminals; measure it
  (`lipgloss.Height`) instead of assuming one line when budgeting rows.

## Components

- `Banner(subtitle)` — branded title box, top of every screen.
- `StageList` (`stagelist.go`) — the Docker-buildkit step stack: one
  ` => [i/N] Name` row per pipeline stage, elapsed time live at the right
  edge, and ` => => ` sub-rows under the running stage (progress bar +
  streaming tail of the files being processed). Finished stages collapse to
  their one-line summary. Drive it from `logger.Event`s: `PhaseKey`/`EventKey`
  route start/done, `StreamKey` lines feed the tail, count attrs feed the bar.
  Used by scan and setup-install; any future long-running phase UI should sit
  on it too.
- `Footer(help, w)` / `KeyHint(key, action)` — dim key-help bar.
- `Screen(body, footer, h)` — pins the footer to the terminal bottom.
- `Shell` (`shell.go`) — one alt-screen program hosting one screen at a time;
  `Switch(next)` swaps screens without a terminal-restore flash.
- `FormModel` (`form.go`) — the huh-based setup wizard, themed with the same
  tokens.

## Data flow

The logger is the pub/sub bus (`pkg/logger/stream.go`): the pipeline never
imports `tui`. `UserKey` = milestone (everywhere), `StreamKey` = per-item feed
line (TUI + file log only), `PhaseKey`/`EventKey`/`ElapsedKey` = stage routing.
Plain mode (`--plain`, `--debug`, non-TTY stderr) keeps the line-based console
logger; one-off styled prints there (help, lock messages, errors) use this
package's theme styles directly — one palette for both modes.
