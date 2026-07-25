> SPDX-License-Identifier: AGPL-3.0-or-later
>
> Copyright (c) 2026 Utkarsh Chourasia

# WanderSort TUI design system

Every full-screen surface (scan, config, review, reset) is built from the same
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
  Used by the scan screen; any future long-running phase UI should sit on it
  too. There is no install screen — background dependency downloads render as
  rows on the screen they'd otherwise block (`InstallProgressMsg` on scan,
  `DownloadMsg` on the config form), persisting as a dim `✓ done` line.
- `Footer(help, w)` / `KeyHint(key, action)` — dim key-help bar. Every screen's
  keys go through `KeyHint`, so a key is always the brand colour and its action
  always dim; a hand-written `"[r] rename"` string reads as prose, not as keys.
  A hint's internal spaces are non-breaking, so a width-wrapped footer only
  ever breaks *between* hints — `c save & exit` splitting after the `c` reads
  as a stray key.
- `Row(left, right, w)` — one full-width line: left truncated with `…`, right
  aligned to the terminal edge. The right column is the screen's one number
  (elapsed time on scan/install, file count on review).
- `Screen(body, footer, h)` — pins the footer to the terminal bottom.
- `Shell` (`shell.go`) — one alt-screen program hosting one screen at a time;
  `Switch(next)` swaps screens without a terminal-restore flash.
- `FormModel` (`form.go`) — the config wizard, themed with the same
  tokens. Rules new screens inherit from it:
  - **Pickable options are numbered** (`1) ● yes`). The number is the key that
    picks them; arrows are the fallback, not the only way in.
  - **Examples don't live in descriptions.** A field's `Example` closure shows
    only the option under the cursor. On a wide terminal (≥100 cols) it
    renders as a bordered box in the right-hand column (`examplePanel` —
    the fields cap at `formBodyMaxW` and every spare column goes to the box,
    since the example tree is the thing that gets truncated; the layout is
    stable across steps: the column is reserved whenever *any* field has an
    example); on a narrow one it falls back to a block above the footer
    (`exampleBlock`).
  - **A step can wait.** `Field.Await` returns why the step isn't answerable
    yet; the form shows that reason instead of a control and refuses to
    advance.
  - **Background downloads report into the screen they're blocking**
    (`DownloadMsg` → one bar above the footer, the same block the examples pin
    to), rather than getting an install screen of their own. Nothing renders
    until the first byte arrives, so an already-installed dependency is never
    mentioned — and once finished the bar collapses to a dim `✓ … done` line
    instead of vanishing, since a bar that disappears the moment it fills
    reads as a failure.
  - A `FieldGroup` is a screen, not an input list: its members can be any
    kind, answered in order.
- The review tree (`internal/cli/review.go`) is not a component, but it obeys
  the same rules and is the pattern to copy for any future list screen:
  `Screen` + `Banner` + a measured `header()`/`footer()` pair, `Row` per line
  with the file count in the right column, `Selected` on the cursor row (and on
  a `[V]` range), and `KeyHint`s in the footer that change with the mode.

## Data flow

The logger is the pub/sub bus (`pkg/logger/stream.go`): the pipeline never
imports `tui`. `UserKey` = milestone (everywhere), `StreamKey` = per-item feed
line (TUI + file log only), `PhaseKey`/`EventKey`/`ElapsedKey` = stage routing.
Plain mode (`--plain`, non-TTY stderr) keeps the line-based console
logger; one-off styled prints there (help, lock messages, errors) use this
package's theme styles directly — one palette for both modes.
