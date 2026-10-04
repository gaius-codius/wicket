<p align="center">
  <img src="docs/logo/wicket-dark.svg" width="128" alt="wicket">
</p>

# Wicket

Terminal UI for FreeRDP connections. Pick a profile, connect, come back
when the session ends.

![Connection list](docs/screenshots/list.png)

<p>
  <img src="docs/screenshots/edit.png" alt="Edit form" width="49%">
  <img src="docs/screenshots/help.png" alt="Help" width="49%">
</p>

## Platform

Linux and macOS. Passwords go in the Secret Service keyring
(`org.freedesktop.secrets`) on Linux, and in the Keychain on macOS.
Windows does not build.

## Install

```
curl -fsSL https://raw.githubusercontent.com/gaius-codius/wicket/main/install.sh | sh
```

This installs the latest release for amd64 or arm64 into `~/.local/bin` (set
`WICKET_BINDIR` to change it) after checking it against the release's
`SHA256SUMS`. Run the same command again to update.

```
... | sh -s -- --version v0.1.0   # a given release
... | sh -s -- --uninstall        # remove the binary
```

Config, state, and keyring entries are never touched. Or, with Go:

```
go install github.com/gaius-codius/wicket/cmd/wicket@latest
```

Needs a FreeRDP 3 client on `PATH`. New profiles use the first one found:

1. `sdl-freerdp3`, then `sdl-freerdp`
2. `xfreerdp3`, then `xfreerdp`

Linux packages usually install the names with a `3` suffix.

### macOS

Homebrew's `freerdp` formula installs the same clients
without that suffix:

```
brew install freerdp
```

That puts `sdl-freerdp` and `xfreerdp` on `PATH`. Wicket discovers and offers
those names the same way as `sdl-freerdp3` and `xfreerdp3`. A new profile
uses `sdl-freerdp` when that is the first of the list above that exists.

## Run

```
wicket                 # TUI
wicket connect work    # named profile, no TUI
wicket import remmina  # from ~/.local/share/remmina
wicket import rdp FILE...
wicket --help
wicket --version
```

`wicket connect` is for keybindings and scripts. It does not create a config.
On a non-TTY there is no password prompt, so save the password from the TUI
first. Press `?` in the TUI for keys and field help.

`wicket import` brings Remmina (`*.remmina`, RDP only) or Microsoft `.rdp`
files into wicket profiles. Import only — there is no export or sync.
Passwords are never imported; you enter them on first connect. Use
`--dry-run` to preview, and `--rename` to append `-2`, `-3`… on name
collisions (otherwise collisions are skipped). It exits 1 when entries were
found but none was imported.

## Config

| | |
|-|-|
| Config | `WICKET_CONFIG`, else `$XDG_CONFIG_HOME/wicket/config.toml`, else `~/.config/wicket/config.toml` |
| State | `WICKET_STATE`, else `$XDG_STATE_HOME/wicket/state.toml`, else `~/.local/state/wicket/state.toml` |
| Theme | `WICKET_THEME`, else `[ui] theme` in config, else `auto` |

Passwords live in the platform keyring (libsecret on Linux, Keychain on macOS), never in TOML. Each profile has a stable `id` (UUID) used as the keyring identity so a renamed or recreated display name cannot claim another profile's secret. Hand-editing `config.toml` is
fine: a save from the TUI changes only the values, keys and profiles it has
to, and leaves the rest of the file, comments and formatting included, as it
was. Deleting a profile also removes the comment lines directly above it. A
file laid out in a way Wicket cannot edit in place, such as profiles written
as an inline array, is rewritten in full instead, and the TUI says so.

Keys named `password`, `pass`, `secret`, or `passwd` are stripped on save,
with their line. A password in a comment is left alone, so do not keep one
there either.

Sharing is per profile, set in the form or by hand:

```toml
[[profiles]]
name = "work"
host = "192.168.1.20"
user = "jdoe"
clipboard = false   # on unless turned off, as in FreeRDP
multimon = true     # full screen across every monitor
share_home = true   # your home folder as a drive on the remote machine
shares = [
  { path = "~/Documents" },
  { path = "/data/projects", name = "projects" },
]
```

`share_home` shares all of your home folder, read-write, dotfiles and
`~/.ssh` included. Only turn it on for machines you trust.

`shares` lists specific local folders as named drives (`/drive:name,path` in
FreeRDP). Paths must be absolute or `~/…`, and must exist as directories when
you save or connect. Leave `name` out to derive it from the folder name.
`share_home` and a share named `home` cannot both be set.

Keys left out keep their default, and a save only writes the ones changed.

## Theme

```toml
[ui]
theme = "auto"
```

`WICKET_THEME` overrides config for one run.

| Value | Meaning |
|-------|---------|
| `auto` | Omarchy if `~/.local/state/omarchy/current/theme/colors.toml` exists and is readable, else Verdigris |
| `wicket` | Verdigris, matched to the terminal |
| `wicket-dark`, `wicket-light` | Fixed Verdigris variant |
| `omarchy` | Omarchy (terminal colours + warning if missing or unreadable) |
| `terminal` | Terminal ANSI colours |

## Keys

| Key | Action |
|-----|--------|
| `j` / `k`, arrows | Move |
| `g` / `G`, Home / End | First / last |
| PgUp / PgDn | Page |
| `/` | Filter (Esc clears) |
| `s` | Sort by recent use (not saved) |
| `Enter` | Connect |
| `n` / `e` / `D` | New / edit / delete |
| `y` | Copy to a new profile (password not copied) |
| `?` | Help |
| `q`, Ctrl+C | Quit |

Form: Ctrl+S saves, Esc cancels. ←/→ picks the scale and the client
(`custom…` takes any binary name). Leave the password field empty to keep the
stored one; use `forget password` when editing to clear it.

## Sessions

`Enter` starts FreeRDP in its own window; Wicket stays on the status view
until that window closes, then cleans up the process group. During a session,
`Ctrl+C` stops FreeRDP (interrupt, then terminate, then kill). If FreeRDP
fails, or the session ends within a few seconds, you get the reason and can
retry or enter a new password. A logoff or disconnect goes back to the list.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Security:
[advisories](https://github.com/gaius-codius/wicket/security/advisories/new)
and [SECURITY.md](SECURITY.md).

CI runs `gofmt`, `go vet`, `go test ./...`, and `go test -race ./internal/secret`
on pushes and pull requests to `main`.

## License

MIT. See [LICENSE](LICENSE).
