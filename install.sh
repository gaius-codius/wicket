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

# run runs a command in the background and waits for it. A shell takes a
# trapped signal only once the foreground command returns, so a TERM sent to
# the script alone would wait out a stalled download; it does interrupt wait.
# stop then kills the command, whose pid is in $child while it runs.
run() {
	"$@" &
	child=$!
	run_status=0
	wait "$child" || run_status=$?
	child=""
	return "$run_status"
}

# stop kills the running command, removes the temp dir and exits with $1.
# Background commands ignore INT, so it has to kill them for Ctrl-C too.
stop() {
	if [ -n "$child" ]; then
		kill "$child" 2>/dev/null || :
		wait "$child" 2>/dev/null || :
	fi
	rm -rf "$tmp"
	exit "$1"
}

fetch() { # url dest
	if command -v curl >/dev/null 2>&1; then
		run curl -fsSL --proto-redir '=https' -o "$2" "$1"
	elif command -v wget >/dev/null 2>&1; then
		run wget -q -O "$2" "$1"
	else
		die "need curl or wget"
	fi
}

# latest_tag sets $tag to the latest release. It writes to a file rather
# than a command substitution, which a trapped signal would wait for.
latest_tag() {
	if command -v curl >/dev/null 2>&1; then
		# /releases/latest redirects to /releases/tag/<tag>.
		run curl -fsSLI --proto-redir '=https' -o /dev/null -w '%{url_effective}' \
			"$RELEASES/latest" >"$tmp/latest" || die "could not reach $RELEASES"
		url=$(cat "$tmp/latest")
		tag=${url##*/}
	else
		: >"$tmp/latest"
		run wget -qO "$tmp/latest" "https://api.github.com/repos/$REPO/releases/latest" || :
		tag=$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$tmp/latest" | head -n 1)
	fi
	case "$tag" in
	v[0-9]*) ;;
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

	os=$(uname -s)
	case "$os" in
	Linux) goos=linux ;;
	Darwin) goos=darwin ;;
	*) die "wicket runs on Linux and macOS; try: go install github.com/$REPO/cmd/wicket@latest" ;;
	esac
	case "$(uname -m)" in
	x86_64 | amd64) arch=amd64 ;;
	aarch64 | arm64) arch=arm64 ;;
	*) die "no build for $(uname -m); try: go install github.com/$REPO/cmd/wicket@latest" ;;
	esac

	child=""
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	# A signal trap that only cleaned up would let the script carry on
	# without $tmp. Stop with the status the shell would give.
	trap 'stop 130' INT
	trap 'stop 143' TERM

	[ -n "$tag" ] || latest_tag
	case "$tag" in v*) ;; *) tag="v$tag" ;; esac
	version=${tag#v}

	current=$(installed_version)
	if [ "$current" = "$version" ]; then
		say "wicket $version is already installed at $BINDIR/wicket"
		return
	fi

	name="wicket_${version}_${goos}_${arch}"

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

	# The suffixed names first: they need no probe, and order does not matter
	# for a warning.
	for c in sdl-freerdp3 xfreerdp3 sdl-freerdp xfreerdp; do
		if freerdp3 "$c"; then
			return
		fi
	done
	say "warning: no FreeRDP 3 client on PATH; install sdl-freerdp3 or xfreerdp3 (Homebrew: sdl-freerdp or xfreerdp)" >&2
}

# freerdp3 reports whether client $1 is on PATH and is FreeRDP 3. A name
# with the 3 suffix says so itself. One without it can be FreeRDP 2 on Linux,
# so it has to print "This is FreeRDP version 3.<minor>" for --version within
# a few seconds. Its exit status is not read: a build may report its version
# and still exit non-zero. There is no timeout(1) on macOS, hence the watcher,
# which takes its sleep with it when it is stopped. The probe runs in the
# background (see run) with stderr discarded, or bash reports the killed
# client as a job.
freerdp3() {
	command -v "$1" >/dev/null 2>&1 || return 1
	case "$1" in *3) return 0 ;; esac
	fr_out="$tmp/freerdp-version"
	rm -f "$fr_out" "$fr_out.timeout"
	run freerdp3_probe "$1" 2>/dev/null || :
	[ ! -e "$fr_out.timeout" ] && grep -q 'This is FreeRDP version 3\.[0-9]' "$fr_out"
}

# freerdp3_probe runs "$1 --version" into $fr_out, killing it after 3 s. When
# stop kills the probe, it takes the client and the watcher with it.
freerdp3_probe() {
	fr_pid="" fr_watch=""
	trap '[ -z "$fr_pid" ] || kill -KILL "$fr_pid" || :
		[ -z "$fr_watch" ] || kill "$fr_watch" || :
		wait; exit 143' TERM
	"$1" --version >"$fr_out" 2>&1 </dev/null &
	fr_pid=$!
	(
		trap 'kill "$fr_sleep" 2>/dev/null; exit 0' TERM
		sleep 3 &
		fr_sleep=$!
		# After an interrupt $tmp is gone and the marker cannot be
		# written; the client must still be killed. printf, not :, as
		# a redirection error on a special builtin exits sh.
		wait "$fr_sleep" && {
			printf '' >"$fr_out.timeout" || :
			kill -KILL "$fr_pid"
		}
	) &
	fr_watch=$!
	wait "$fr_pid" || :
	kill "$fr_watch" || :
	wait "$fr_watch" || :
}

# Everything runs from here, so a download cut off mid-script does nothing.
main "$@"
