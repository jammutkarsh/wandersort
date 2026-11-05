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

Use the semantic styles (`Title`, `Text`, `DimText`, `FaintTxt`, `OK`, `Attn`, `Bad`, `Selected`, `NewFolder`, `Box`), not the colours. `Prompt()` is the `❯` in front of every text input.

Spacing: `Gutter` (2 cells) each side of the terminal. The shell sizes screens to `Inner(w)` and draws them through `Frame`, so the top line, every body and every footer share one left and one right margin. A screen draws from column 0 and never indents by the gutter itself; list rows use a 2-cell marker column (`❯ `).

## Layout rules

- **One top line.** `shift+tab` or a click cycles the tabs. The shell draws `Brand()`, the tabs (Add, Organise, Copy, Settings; `●` where something waits), a `shift+tab` hint and the library's folder name on the right. Screens draw nothing above their content. `KeyHelp` explains `●` and the tab keys on every screen.
- **At most five keys in a footer**, ending with `MoreKeys()`. The rest go in the screen's `KeyGroup`s, drawn over the screen by `KeyHelp` on `?`; any key closes it. In a text input `?` is text unless the input is empty.
- **The right column is the screen's one number** (elapsed time, file count, size). Content truncates with `…`; the number never does. A finished stage shows no time.
- **Plain words.** One verb per action: Add, Organise, Copy. Sizes in KB/MB/GB (`volume.HumanBytes`), folders as "<name>, in your home folder" (`path.Resolver.Friendly`), never `~`. Meaning never rides on colour alone (new folders are `+` and blue).
- **esc says what it does:** `done` leaves a tab, `cancel` leaves a mode, `not now` declines a question, `save` keeps a one-setting edit.
- **Keys don't differ by case alone** and no vim-isms: `space` selects, arrows move.
- **Choices are numbered** (`❯ 1)`); a digit picks one, arrows are the fallback.
- **Warnings wait for the end of a run**, then show once; the log has every one.
- **Measure, don't assume.** Footers wrap on narrow terminals; budget rows with `lipgloss.Height`.

## Components

- `ReadyModel`: the getting-ready screen every session opens on. Lists the dependencies, shows their downloads (`InstallProgressMsg`), asks for a better network between tries (`RetryMsg`, 10 s countdown, enter retries now) and gives up after the last (`DepsFailedMsg`).
- `StageList`: one row per stage (`○` pending, spinner running, `✓` done, `✗` failed), elapsed time on the right, the running stage's bar and the item it is on. `Remaining`/`TimeLeft` guess the time left from the bar's rate. Used by the plan and copy screens.
- `ScanModel`: Find files / Read dates and places / Sort into folders, then the numbered "what next" choice; warnings only the log holds are counted above the footer with the log's path (`ScanConfig.LogPath`).
- `CopyModel`: what a copy would do, the copy (bytes on the bar), and what it did, naming files left out by reason.
- `FormModel`: the settings setup. `FieldSelect` (numbered single choice), `FieldMultiSelect`, `FieldConfirm`, `FieldInput`, `FieldGroup` (one screen, any kinds). `Field.Skip` leaves a follow-up step out; `Heading` adds a title (and "step N of M" beside it when there is more than one); a lone step is not numbered and a lone `FieldGroup` drops its own title; `Then` lets a host screen keep going after it ends; `EscSaves` makes esc save without asking. `ctrl+b` goes back a question (in the footer only when esc doesn't save). `Field.Example` shows only the option under the cursor: a bordered right column on wide terminals (≥ 100 cols), a block above the footer otherwise.
- `SettingsModel`: a library's settings as a list; enter edits one row with a one-step `FormModel`.
- `HomeModel`: the Add tab's folder list, built by dropping folders on the terminal (a paste) or typing one path per enter.
- `Footer(help, w)` / `KeyHint(key, action)`: every key goes through `KeyHint` (non-breaking spaces, so wrapping only happens between hints).
- `Row(left, right, w)`, `Screen(body, footer, h)`: one full-width line; a footer pinned to the bottom.
- `Tab` / `Leave` / `SwitchMsg`: how the shell hosts screens. Screens never quit the program; they hand back with `Leave`.
- `Ask` / `ConfirmModel`: a full-screen yes/no before something hard to undo. ←/→ move the highlight (starting on no for `Ask`), enter takes it, esc is no.
- The review tree (`internal/review`) is the pattern for list screens: a measured header and footer, a `Row` per line, `Selected` on the cursor row and the selection, mode-dependent `KeyHint`s. Modes: browse (`↑↓ ← → r space p u`), selecting (`m x f esc cancel`), renaming (`↑↓ ctrl+e enter esc cancel`).

## Data flow

The logger is the bus (`pkg/logger/stream.go`); the pipeline never imports `tui`. `UserKey` = milestone (everywhere), `StreamKey` = per-item line (TUI and file log), `PhaseKey`/`EventKey`/`ElapsedKey` = stage routing. The copy screen takes `execute.Options.OnStep`/`OnProgress` callbacks instead. Plain mode (`--plain`, `--json`, non-TTY stderr) prints milestones as sentences, one per stage, and keeps the `warn` tag for warnings.
