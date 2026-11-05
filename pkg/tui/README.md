# WanderSort TUI design system

Every full-screen surface is built from this kit so the app reads as one product. New screens compose from these pieces; no new colours, markers or layout rules.

## Tokens (`theme.go`)

Adaptive palette, resolved per light/dark terminal:

| Role | Use for |
|---|---|
| `Primary` | brand, focus, the running-stage marker, key hints |
| `Success` / `Warn` / `Error` | done / warning / failure, never decoration |
| `Fg` / `Muted` / `Subtle` | primary text / secondary text / hints and pending rows; all three stay at 4.5:1 or more (`theme_test.go`) |
| `Highlight` | the picked item's box (`Pill`: the text in a Highlight box, bold and underlined) |
| `Border` | lines only (box borders), never text |

Use the semantic styles (`Title`, `Text`, `DimText`, `FaintTxt`, `OK`, `Attn`, `Bad`, `Selected`, `NewFolder`, `Box`), not the colours. `Prompt()` is the `❯` in front of every text input.

Spacing: `Gutter` (2 cells) each side of the terminal. The shell sizes screens to `Inner(w)` and draws them through `Frame`, so the top line, every body and every footer share one left and one right margin. A screen draws from column 0 and never indents by the gutter itself; list rows use a 2-cell marker column (`❯ `).

## Layout rules

- **One top line.** `shift+tab` or a click cycles the tabs. The shell draws `Brand()`, the tabs (Add, Organise, Copy, Settings; `●` where something waits), a `shift+tab` hint and the library's folder name on the right. Screens draw nothing above their content. `KeyHelp` explains `●` and the tab keys on every screen.
- **At most five keys in a footer**, ending with `MoreKeys()` (`? help`). The rest go in the screen's `KeyGroup`s, drawn over the screen by `KeyHelp` on `?`; any key closes it. In a text input `?` is text unless the input is empty.
- **The right column is the screen's one number** (elapsed time, file count, size). Content truncates with `…`; the number never does. A finished stage shows no time.
- **Plain words.** One verb per action: Add, Organise, Copy. Sizes in KB/MB/GB (`volume.HumanBytes`), folders as "<name>, in your home folder" (`path.Resolver.Friendly`), never `~`. Meaning never rides on colour alone (new folders are `+` and blue). Errors go through `Explain`: a known system cause (permission, full or missing drive, busy library) becomes a sentence saying what to do; the log keeps the cause. The footer's `enter` hint names the highlighted choice ("enter Copy into library"), never "go". Jargon words stay out of the screens: Combine and Gather (not merge, flatten), Start over (not discard the plan).
- **Sub-menus close on one esc, with no quit hint.** A screen that can sit inside its own sub-menu (the `?` list, an edit, a rename, a selection, a question) implements `Nester`; the shell's "esc again to quit" only arms from a main screen.
- **esc says what it does:** `done` leaves a tab, `cancel` leaves a mode, `not now` declines a question, `save` keeps a one-setting edit.
- **Keys don't differ by case alone** and no vim-isms: `space` selects, arrows move.
- **Nothing is numbered.** A choice is a row; the picked one is a `Pill` (a purple box round the text, bold and underlined), the same pick as a yes/no button, a tab, a settings row, an Organise row and a completion. Options are one row each, on their own text column, so they read as options and not as info lines (`choiceRow`); ↑/↓ and enter, or a click, pick it.
- **Everything is clickable.** A footer hint, a choice, a settings row, an Organise row, a confirm button and the `?` list press their keys on a left click. Draw them through `KeyHint` or `Clickable(text, keys…)`; the shell finds them with `ClickTargets`. A yes/no question is a row: the question on the left, two equal-width `yes` `no` buttons at the right edge (the picked one filled, bold and underlined; the labels never change). In a group every question is such a row, and a click on a button walks there and picks that side without moving on, so the example shows the result. ↑/↓ move between the questions without answering; ←/→ answer the focused one. The focused question's one-sentence description sits above the footer. The example shows only in the side panel (terminal ≥ 100 columns); a narrower screen leaves it out. A form's questions are clickable too: a done step or an unfocused question in a group walks there with `ctrl+b`/`enter`. A hint whose label is a range or a choice (`↑↓`, `1-3`, `y/n`) is not one key and stays plain. Stopping a scan or copy is `esc stop`; `ctrl+c quit now` shows only while it winds down.
- **Warnings wait for the end of a run**, then show once; the log has every one.
- **Measure, don't assume.** Footers wrap on narrow terminals; budget rows with `lipgloss.Height`.

## Components

- `ReadyModel`: the getting-ready screen every session opens on. Lists the dependencies, shows their downloads (`InstallProgressMsg`), asks for a better network between tries (`RetryMsg`, 10 s countdown, enter retries now) and gives up after the last (`DepsFailedMsg`).
- `StageList`: one row per stage (`○` pending, spinner running, `✓` done, `✗` failed), elapsed time on the right, the running stage's bar and the item it is on. `Remaining`/`TimeLeft` guess the time left from the bar's rate. Used by the plan and copy screens.
- `ScanModel`: Find files / Read dates and places / Sort into folders, then the "what next" choice; warnings only the log holds are counted above the footer with the log's path (`ScanConfig.LogPath`).
- `CopyModel`: what a copy would do, the copy (bytes on the bar), and what it did, naming files left out by reason.
- `FormModel`: the settings setup. `FieldSelect` (single choice), `FieldMultiSelect`, `FieldConfirm`, `FieldInput`, `FieldGroup` (one screen, any kinds). `Field.Skip` leaves a follow-up step out; `Heading` adds a title (and "step N of M" beside it when there is more than one); a lone step is not numbered and a lone `FieldGroup` drops its own title; `Then` lets a host screen keep going after it ends; `EscSaves` makes esc save without asking. `ctrl+b` goes back a question (in the footer only when esc doesn't save). `Field.Example` shows only the option under the cursor: a bordered right column on wide terminals (≥ 100 cols); narrower ones show none.
- `SettingsModel`: a library's settings as a list; enter edits one row with a one-step `FormModel`.
- `HomeModel`: the Add tab's folder list, built by dropping folders on the terminal (a paste) or typing one path per enter.
- `Footer(help, w)` / `KeyHint(key, action)`: every key goes through `KeyHint` (non-breaking spaces, so wrapping only happens between hints).
- `Row(left, right, w)`, `Screen(body, footer, h)`: one full-width line; a footer pinned to the bottom.
- `Tab` / `Leave` / `SwitchMsg`: how the shell hosts screens. Screens never quit the program; they hand back with `Leave`.
- `Ask` / `ConfirmModel`: a full-screen yes/no before something hard to undo. ←/→ move the highlight (starting on no for `Ask`), enter takes it, esc is no.
- `PickFolder` / `CanPickFolder`: the system folder window (`osascript`, PowerShell, `zenity` or `kdialog`). `HomeConfig.PickFolder` is nil when there is none, and Add then leaves the `ctrl+o` hint out.
- The review tree (`internal/review`) is the pattern for list screens: a measured header and footer, a `Row` per line, `Selected` on the cursor row and the selection, mode-dependent `KeyHint`s. Modes: browse (`↑↓ ← → r space p u`), selecting (`m x f esc cancel`), renaming (`↑↓ ctrl+e enter esc cancel`).

## Data flow

The logger is the bus (`pkg/logger/stream.go`); the pipeline never imports `tui`. `UserKey` = milestone (everywhere), `StreamKey` = per-item line (TUI and file log), `PhaseKey`/`EventKey`/`ElapsedKey` = stage routing. The copy screen takes `execute.Options.OnStep`/`OnProgress` callbacks instead. Plain mode (`--plain`, `--json`, non-TTY stderr) prints milestones as sentences, one per stage, and keeps the `warn` tag for warnings.
