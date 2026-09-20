# Wicket

A small door into another machine.

Wicket is a terminal UI for saved FreeRDP connections: pick a profile, connect,
come back when the session ends. Inspired by [Vigiles](https://github.com/gaius-codius/vigiles)
in stack and interaction grammar, not a clone of it.

## Build

Requires Go 1.24 or newer, and a FreeRDP 3 SDL client on `PATH` (`sdl-freerdp3` by default).

```
go build -o wicket ./cmd/wicket
```

Install the binary somewhere on your `PATH`.

## Run

```
wicket                 # start the TUI
wicket connect work    # connect a named profile without the TUI
wicket --help
```

`wicket connect` is for Hyprland keybindings and scripts. It does not create a config file. On a non-TTY (no password prompt), the profile must already have a password saved from the TUI.

## Config and state

- Config: `WICKET_CONFIG`, else `$XDG_CONFIG_HOME/wicket/config.toml`, else `~/.config/wicket/config.toml`
- Last-used timestamps: `WICKET_STATE`, else `$XDG_STATE_HOME/wicket/state.toml`, else `~/.local/state/wicket/state.toml`

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

At start, Wicket reads `$HOME/.local/state/omarchy/current/theme/colors.toml` and maps Omarchy tokens onto its chrome. A missing or broken theme file falls back per role and does not block the TUI.

Text is held to the WCAG AA contrast ratio against the theme's own background.
A theme whose foreground would be unreadable there is overridden, so the UI is
legible on every installed theme rather than only on most of them.

## Keys (list)

| Key | Action |
|-----|--------|
| `j` / `k`, arrows | Move |
| `g` / `G`, Home / End | First / last |
| PgUp / PgDn | Page (one screen of rows) |
| `/` | Filter by name or host (Esc clears) |
| `Enter` | Connect |
| `n` | New profile |
| `e` | Edit selected |
| `D` | Delete selected |
| `?` | Help for the current view |
| `q`, Ctrl+C | Quit |

In the form, ↑/↓ or Tab move between fields, text fields take the usual
cursor keys (←/→, Home/End, Ctrl+W, Ctrl+U), Ctrl+S saves, and Esc cancels
(asking first if you changed anything). The form scrolls to the focused field,
so every field is reachable in a short terminal. `?` opens help only when a
checkbox has focus: in a text field it types a literal `?`. The same is true of
the password dialog, where Tab leaves the field first.

Paste with your terminal's paste shortcut; Ctrl+V is not bound. A paste may
carry one trailing line break, which is dropped; anything spanning more than
one line is refused rather than silently truncated.

Wicket keeps its chrome inside the window at any size. Below about five rows
it asks you to resize; between there and a full-height terminal it drops the
divider, spacing and status line before it gives up any of the list.
