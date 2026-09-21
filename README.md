# Wicket

A small door into another machine.

Wicket is a terminal UI for saved FreeRDP connections: pick a profile, connect,
come back when the session ends. Inspired by [Vigiles](https://github.com/gaius-codius/vigiles)
in stack and interaction grammar, not a clone of it.

## Platform

Linux. Wicket stores passwords in the Secret Service keyring
(`org.freedesktop.secrets`), which is what GNOME Keyring and KWallet provide.

It compiles for macOS and the BSDs, but there is no Secret Service there, so
passwords cannot be saved and every connection asks for one. A Keychain
backend is tracked as [#1](https://github.com/gaius-codius/wicket/issues/1).
Windows does not build.

## Install

With a Go toolchain:

```
go install github.com/gaius-codius/wicket/cmd/wicket@latest
```

Or from a clone:

```
go build -o wicket ./cmd/wicket
```

and put the binary somewhere on your `PATH`. You also need a FreeRDP 3 SDL
client on `PATH` — `sdl-freerdp3` by default, configurable per profile.

## Run

```
wicket                 # start the TUI
wicket connect work    # connect a named profile without the TUI
wicket --help          # or -h, or: wicket help
wicket --version       # or -v, or: wicket version
```

`wicket connect` is for Hyprland keybindings and scripts. It does not create a config file. On a non-TTY (no password prompt), the profile must already have a password saved from the TUI.

## Config and state

- Config: `WICKET_CONFIG`, else `$XDG_CONFIG_HOME/wicket/config.toml`, else `~/.config/wicket/config.toml`
- Last-used timestamps: `WICKET_STATE`, else `$XDG_STATE_HOME/wicket/state.toml`, else `~/.local/state/wicket/state.toml`
- Theme: `WICKET_THEME`, else `theme` under `[ui]` in `config.toml`, else `auto` (see [Theme](#theme))

Passwords are never stored in TOML. They live in libsecret (Secret Service / `org.freedesktop.secrets`).

You can hand-edit `config.toml`. Wicket keeps any keys and tables it does not
know about, so settings for other tools survive. **Comments and formatting do
not survive a save from the TUI**: saving rewrites the file from the parsed
values. Keep comments you care about somewhere else, or edit the file by hand
only. Keys named `password`, `pass`, `secret`, or `passwd` are stripped on save
and reported, so a password never gets written back to disk.

A save takes a lock beside the file and re-reads it first, so an edit made in
another editor (or another Wicket) while a form was open is not overwritten.
Profile fields reject control characters: a newline or an escape sequence in a
host or a name would otherwise reach the terminal and the FreeRDP command line.

## Theme

Wicket's own theme is Verdigris, a quiet green-grey with a copper mark, in a
dark and a light variant. Pick a theme in `config.toml`:

```toml
[ui]
theme = "auto"
```

or for one run with `WICKET_THEME=wicket-light wicket`. A non-empty
`WICKET_THEME` wins over `[ui] theme`, and with neither set the theme is `auto`.

| Value | What you get |
|-------|--------------|
| `auto` | The Omarchy theme when `~/.local/state/omarchy/current/theme/colors.toml` is readable, otherwise Verdigris in your terminal's light or dark |
| `wicket` | Verdigris, light or dark to match the terminal, ignoring Omarchy |
| `wicket-dark`, `wicket-light` | That Verdigris variant, whatever the terminal |
| `omarchy` | The Omarchy theme; without a readable theme file, terminal colours and a warning |
| `terminal` | Your terminal's own ANSI colours, with the selected row in reverse video |

To match the terminal, Wicket asks it for its background colour when the TUI
starts, without waiting for the answer. Until one arrives, or if the terminal
never replies, Wicket draws in terminal colours, which read on any background.
`wicket connect` never asks.

An unknown value is treated as `auto` and reported on the status line; a bad
setting never stops Wicket from starting.

Text is held to the WCAG AA contrast ratio. For an Omarchy theme that is
checked against the theme's own background and, once the terminal has said
what its background really is, against that too, so the UI stays legible even
when the terminal is not using the theme. A missing or broken role in an
Omarchy theme falls back to Verdigris.

## Keys (list)

| Key | Action |
|-----|--------|
| `j` / `k`, arrows | Move |
| `g` / `G`, Home / End | First / last |
| PgUp / PgDn | Page (one screen of rows) |
| `/` | Filter by name or host (Esc clears) |
| `s` | Sort most recently used first, or back to file order (not saved) |
| `Enter` | Connect |
| `n` | New profile |
| `e` | Edit selected |
| `D` | Delete selected |
| `?` | Help for the current view |
| `q`, Ctrl+C | Quit |

Each row shows when the profile was last used, on the right; the narrowest
layout keeps that for the selected row only. `s` sorts by it, most recent
first, with profiles never used at the end in file order, and the header says
`recent first` while it is on; the selection stays on the same profile. A
filter underlines the part of the name or host it matched. The selected
profile's details say whether a password is saved in the keyring (`● saved in
keyring`, `asks when connecting`, or `keyring unavailable`); Wicket finds out
from the keyring's metadata alone, so this never unlocks the keyring or asks
for anything. In a wide terminal the details also show the `wicket connect`
command for the profile, quoted for the shell where it needs to be.

The form groups its fields under CONNECTION (name, host, user, domain),
DISPLAY (size, fullscreen, dynamic resolution, scale), PASSWORD and ADVANCED
(client), and ↑/↓ or Tab move through them in that order. Text fields take the
usual cursor keys (←/→, Home/End, Ctrl+W, Ctrl+U); Space or Enter switches an
on/off field, and ←/→ choose the scale. The line under the fields says what
the focused one is for. Ctrl+S saves, and Esc cancels (asking first if you
changed anything; the header shows `● modified` while there is something to
lose). If a save is refused, the error appears under the field at fault and
the focus moves there. The form scrolls to the focused field, with a count of
the fields above and below, so every field is reachable in a short terminal.
`?` opens help only when an on/off or scale field has focus: in a text field
it types a literal `?`. The same is true of the password dialog, where Tab
leaves the field first.

A password typed into the form is saved to the keyring on Ctrl+S; leaving the
field empty keeps whatever is already stored. To drop a stored password, switch
on `forget password`, which appears only when editing an existing profile.
Typing a password switches that off and switching it on clears the typed
password, so the two are never asked for at once.

Paste with your terminal's paste shortcut; Ctrl+V is not bound. A paste may
carry one trailing line break, which is dropped; anything spanning more than
one line is refused rather than silently truncated.

Wicket keeps its chrome inside the window at any size. Below about five rows
it asks you to resize; between there and a full-height terminal it drops
spacing, the divider and then the footer keys before it gives up any of the
list. The status line outlives the footer, since a warning you never see is
lost while the keys are in the help view. Dialogs shed their explanatory note
first, so the delete confirmation always names what it is about to delete and
the password prompt always shows its field. The first-run screen keeps its
heading and the `n` key ahead of the config path, theme and shell hint.

The status line marks what it reports: `•` a note, `✓` something that fully
happened, `▲` a warning (such as a theme setting that could not be honoured)
and `✗` something that did not happen. A save or delete is reported as
`✓ Saved <name>.` or `✓ Deleted <name>.` only when every part of it worked;
if the profile was written but, say, the password could not be stored, the
line says so with `✗`. In the footer, the key each view is for is drawn in the
accent colour, and only the `y` that confirms a delete is drawn in the danger
colour.

When a session ends within a few seconds, Wicket shows how the client exited
and offers `Enter` to retry or `n` to type a new password; a short session is
not always a wrong password, so nothing is changed until you choose.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security reports go through
[private vulnerability reporting](https://github.com/gaius-codius/wicket/security/advisories/new)
rather than a public issue; [SECURITY.md](SECURITY.md) describes what Wicket
protects and what it does not.

## License

MIT. See [LICENSE](LICENSE).
