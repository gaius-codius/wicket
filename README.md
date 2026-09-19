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

## Theme

At start, Wicket reads `$HOME/.local/state/omarchy/current/theme/colors.toml` and maps Omarchy tokens onto its chrome. A missing or broken theme file falls back per role and does not block the TUI.

## Keys (list)

| Key | Action |
|-----|--------|
| `j` / `k`, arrows | Move |
| `Enter` | Connect |
| `n` | New profile |
| `e` | Edit selected |
| `D` | Delete selected |
| `?` | Help |
| `q` | Quit |
