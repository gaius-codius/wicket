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

A password typed into the form is saved to the keyring on Ctrl+S; leaving the
field empty keeps whatever is already stored. To drop a stored password, tick
`forget password`, which appears only when editing an existing profile. Typing
a password clears that checkbox and ticking it clears the typed password, so
the two are never asked for at once.

Paste with your terminal's paste shortcut; Ctrl+V is not bound. A paste may
carry one trailing line break, which is dropped; anything spanning more than
one line is refused rather than silently truncated.

Wicket keeps its chrome inside the window at any size. Below about five rows
it asks you to resize; between there and a full-height terminal it drops
spacing, the divider and then the footer keys before it gives up any of the
list. The status line outlives the footer, since a warning you never see is
lost while the keys are in the help view. Dialogs shed their explanatory note
first, so the delete confirmation always names what it is about to delete and
the password prompt always shows its field.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security reports go through
[private vulnerability reporting](https://github.com/gaius-codius/wicket/security/advisories/new)
rather than a public issue; [SECURITY.md](SECURITY.md) describes what Wicket
protects and what it does not.

## License

MIT. See [LICENSE](LICENSE).
