# WanderSort TUI design system

Every full-screen surface is built from this kit so the app reads as one product. New screens compose from these pieces; no new colours, markers or layout rules.

## Tokens (`theme.go`)

Adaptive palette, resolved per light/dark terminal:

| Role | Use for |
|---|---|
| `Primary` | brand, focus, the running-stage marker, key hints |
| `Success` / `Warn` / `Error` | done / warning / failure, never decoration |
| `Fg` / `Muted` / `Subtle` | primary text / secondary text / borders and pending rows |
| `Highlight` | selected-row background (whole line, no marker characters) |

Use the semantic styles (`Title`, `Text`, `DimText`, `FaintTxt`, `OK`, `Attn`, `Bad`, `Selected`, `Box`), not the colours.

## Layout rules

- **Alt-screen, full width and height.** `Banner` at the top, `Footer` pinned to the last row by `Screen`; the rest is live content.
- **The right column is the screen's one number** (elapsed time, file count). Content truncates with `…`; the number never does.
- **Measure, don't assume.** Footers wrap on narrow terminals; budget rows with `lipgloss.Height`.

## Components

- `Banner(subtitle)`: branded title box.
- `StageList`: buildkit-style step stack. One ` => [i/N] Name` row per stage, a progress bar and streaming file tail under the running one, finished stages collapsed to one line. Driven by `logger.Event`s.
- `Footer(help, w)` / `KeyHint(key, action)`: every key goes through `KeyHint` (non-breaking spaces, so wrapping only happens between hints).
- `Row(left, right, w)`: one full-width line, right column aligned to the edge.
- `Screen(body, footer, h)`: pins the footer to the bottom.
- `Tab` / `Leave` / `SwitchMsg`: how the shell hosts screens. Screens never quit the program; they hand back with `Leave`.
- `FormModel`: the settings wizard.
  - Options are numbered; arrows are the fallback.
  - `Field.Example` shows only the option under the cursor: a bordered right column on wide terminals (≥ 100 cols), a block above the footer otherwise.
  - `Field.Await` holds a step until it can be answered, showing why.
  - Background downloads report into the screen they block (`DownloadMsg`, `InstallProgressMsg`) and settle as a dim `✓ done` line. An already-present dependency shows nothing.
  - A `FieldGroup` is one screen whose members can be any field kind.
- The review tree (`internal/review`) is the pattern for list screens: `Screen` + `Banner` + measured header/footer, a `Row` per line, `Selected` on the cursor row and `[V]` range, mode-dependent `KeyHint`s.

## Data flow

The logger is the bus (`pkg/logger/stream.go`); the pipeline never imports `tui`. `UserKey` = milestone (everywhere), `StreamKey` = per-item line (TUI and file log), `PhaseKey`/`EventKey`/`ElapsedKey` = stage routing. Plain mode (`--plain`, non-TTY stderr) uses the line console with the same theme styles.
