#!/bin/sh
# Install or update wicket from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/gaius-codius/wicket/main/install.sh | sh
#
# Run it again to update. Pass arguments after `sh -s --`:
#
#   ... | sh -s -- --version v0.1.0    install a given release
#   ... | sh -s -- --uninstall         remove the binary
#
# WICKET_BINDIR sets where the binary goes (default ~/.local/bin).
# Config, state and keyring entries are never touched.
set -eu

REPO="gaius-codius/wicket"
RELEASES="${WICKET_RELEASES_URL:-https://github.com/$REPO/releases}"
BINDIR="${WICKET_BINDIR:-$HOME/.local/bin}"

say() { printf '%s\n' "$*"; }
die() { printf 'wicket install: %s\n' "$*" >&2; exit 1; }

usage() {
	cat <<'USAGE'
Usage: install.sh [--version vX.Y.Z] [--uninstall]

Installs the latest wicket release into ~/.local/bin (override with
WICKET_BINDIR), or updates an existing install. Checks the download against
the release's SHA256SUMS.

  --version vX.Y.Z   install that release instead of the latest
  --uninstall        remove the binary; config, state and keyring stay
USAGE
}

fetch() { # url dest
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL --proto-redir '=https' -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		wget -q -O "$2" "$1"
	else
		die "need curl or wget"
	fi
}

latest_tag() {
	if command -v curl >/dev/null 2>&1; then
		# /releases/latest redirects to /releases/tag/<tag>.
		url=$(curl -fsSLI --proto-redir '=https' -o /dev/null -w '%{url_effective}' "$RELEASES/latest") ||
			die "could not reach $RELEASES"
		tag=${url##*/}
	else
		tag=$(wget -qO- "https://api.github.com/repos/$REPO/releases/latest" |
			sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
	fi
	case "$tag" in
	v[0-9]*) printf '%s\n' "$tag" ;;
	*) die "no release found at $RELEASES" ;;
	esac
}

sha256() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	else
		die "need sha256sum or shasum to check the download"
	fi
}

installed_version() {
	[ -x "$BINDIR/wicket" ] || return 0
	"$BINDIR/wicket" --version 2>/dev/null | cut -d ' ' -f 2
}

uninstall() {
	if [ ! -e "$BINDIR/wicket" ]; then
		say "nothing to remove at $BINDIR/wicket"
		return
	fi
	rm -f "$BINDIR/wicket"
	say "removed $BINDIR/wicket"
	say "config, state and keyring entries were left in place"
}

main() {
	tag=""
	while [ $# -gt 0 ]; do
		case "$1" in
		--version)
			[ $# -ge 2 ] || die "--version needs a tag, e.g. v0.1.0"
			tag=$2
			shift 2
			;;
		--uninstall)
			uninstall
			return
			;;
		-h | --help)
			usage
			return
			;;
		*)
			usage >&2
			die "unknown option: $1"
			;;
		esac
	done

	[ "$(uname -s)" = Linux ] || die "wicket runs on Linux only for now"
	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "no build for $(uname -m); try: go install github.com/$REPO/cmd/wicket@latest" ;;
	esac

	[ -n "$tag" ] || tag=$(latest_tag)
	case "$tag" in v*) ;; *) tag="v$tag" ;; esac
	version=${tag#v}

	current=$(installed_version)
	if [ "$current" = "$version" ]; then
		say "wicket $version is already installed at $BINDIR/wicket"
		return
	fi

	name="wicket_${version}_linux_${arch}"
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT INT TERM

	say "downloading wicket $version ($arch)…"
	fetch "$RELEASES/download/$tag/$name.tar.gz" "$tmp/$name.tar.gz" ||
		die "download failed: $RELEASES/download/$tag/$name.tar.gz"
	fetch "$RELEASES/download/$tag/SHA256SUMS" "$tmp/SHA256SUMS" ||
		die "could not download SHA256SUMS for $tag"

	want=$(awk -v f="$name.tar.gz" '$2 == f { print $1 }' "$tmp/SHA256SUMS")
	[ -n "$want" ] || die "$name.tar.gz is not listed in SHA256SUMS"
	[ "$(sha256 "$tmp/$name.tar.gz")" = "$want" ] || die "checksum mismatch for $name.tar.gz"

	tar -xzf "$tmp/$name.tar.gz" -C "$tmp"
	mkdir -p "$BINDIR"
	# Copy next to the target, then rename, so a running wicket is not
	# overwritten in place and a failed copy leaves the old one.
	cp "$tmp/$name/wicket" "$BINDIR/.wicket.new"
	chmod 755 "$BINDIR/.wicket.new"
	mv -f "$BINDIR/.wicket.new" "$BINDIR/wicket"

	if [ -n "$current" ]; then
		say "updated wicket $current -> $version in $BINDIR"
	else
		say "installed wicket $version to $BINDIR/wicket"
	fi

	case ":$PATH:" in
	*":$BINDIR:"*) ;;
	*)
		say "note: $BINDIR is not on PATH; add this to your shell profile:"
		say "  export PATH=\"$BINDIR:\$PATH\""
		;;
	esac

	for c in sdl-freerdp3 xfreerdp3; do
		if command -v "$c" >/dev/null 2>&1; then
			return
		fi
	done
	say "warning: no FreeRDP 3 client on PATH; install sdl-freerdp3 or xfreerdp3" >&2
}

# Everything runs from here, so a download cut off mid-script does nothing.
main "$@"
