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
# Releases published before the release workflow attested its builds.
UNATTESTED="v0.1.0 v0.1.1 v0.2.0 v0.3.0 v0.3.1"

say() { printf '%s\n' "$*"; }
die() { printf 'wicket install: %s\n' "$*" >&2; exit 1; }

usage() {
	cat <<'USAGE'
Usage: install.sh [--version vX.Y.Z] [--uninstall]

Installs the latest wicket release into ~/.local/bin (override with
WICKET_BINDIR), or updates an existing install. Checks the download against
the release's SHA256SUMS and, when gh is installed and logged in, checks that
the release workflow built it.

  --version vX.Y.Z   install that release instead of the latest
  --uninstall        remove the binary; config, state and keyring stay
USAGE
}

# run runs a command in the background and waits for it. A shell takes a
# trapped signal only once the foreground command returns, so a TERM sent to
# the script alone would wait out a stalled download; it does interrupt wait.
# stop then kills the command, whose pid is in $child while it runs. A
# signal that arrives before $child is set is held until it is, or stop
# would miss the command.
run() {
	starting=1
	"$@" &
	child=$!
	starting=""
	[ -z "$stopping" ] || stop "$stopping"
	run_status=0
	wait "$child" || run_status=$?
	child=""
	return "$run_status"
}

# stop kills the running command, removes the temp dir and exits with $1.
# Background commands ignore INT and QUIT, so it has to kill them for
# Ctrl-C and Ctrl-\ too.
stop() {
	if [ -n "$starting" ]; then
		stopping=$1
		return
	fi
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
		wget_https "$1" "$2"
	else
		die "need curl or wget"
	fi
}

# wget_https fetches $1 into $2 with wget, and refuses the result if wget
# followed a redirect to anything but https. Wget2 (Fedora's wget) has
# --https-only for that, but skips such a redirect, writes nothing and exits
# 0. GNU wget 1.x only applies --https-only to recursive downloads, and
# busybox has no such option, so for them this reads the Location headers
# printed for -S; a download that came over plain http is never used. A log
# with no status line means this wget did not print its headers, and an
# unchecked redirect is not let through.
wget_https() {
	: >"$tmp/wget-version"
	run wget --version >"$tmp/wget-version" 2>/dev/null || :
	if grep -q 'Wget2' "$tmp/wget-version"; then
		rm -f "$2"
		run wget -q --https-only -O "$2" "$1" || return
		[ -e "$2" ] || die "refused a redirect away from https while fetching $1"
		return
	fi
	run wget -S -O "$2" "$1" 2>"$tmp/wget.log" || return
	grep -q 'HTTP/' "$tmp/wget.log" ||
		die "wget did not show its redirects, so they could not be checked; install curl"
	# A Location with a scheme other than https. One without a scheme is
	# relative and keeps https.
	if grep -i '^ *location: *[a-z][a-z0-9+.-]*:' "$tmp/wget.log" |
		grep -iqv '^ *location: *https:'; then
		die "refused a redirect away from https while fetching $1"
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
		wget_https "https://api.github.com/repos/$REPO/releases/latest" "$tmp/latest" || :
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

# provenance checks, with gh, that the release workflow built tarball $1 from
# $tag. SHA256SUMS comes from the same place as the tarball, so it shows the
# download is whole, not who made it. When gh cannot check (not installed,
# too old, not logged in) this says so and the install carries on; a check
# that runs and fails stops it. A release that is meant to be attested and
# is not fails like a forged one, as the two look the same.
provenance() {
	case " $UNATTESTED " in
	*" $tag "*)
		say "note: $tag predates build attestations; provenance not checked"
		return
		;;
	esac
	if ! command -v gh >/dev/null 2>&1; then
		say "note: provenance not checked; install gh to check it"
		return
	fi
	# gh has had attestation verify since 2.49, and --source-ref since later.
	: >"$tmp/gh-help"
	run gh attestation verify --help >"$tmp/gh-help" 2>/dev/null || :
	if ! grep -q -- '--source-ref' "$tmp/gh-help"; then
		say "note: provenance not checked; this gh is too old to check it"
		return
	fi
	pv_status=0
	run gh attestation verify "$1" --repo "$REPO" \
		--signer-workflow "$REPO/.github/workflows/release.yml" \
		--source-ref "refs/tags/$tag" >"$tmp/provenance" 2>&1 || pv_status=$?
	case "$pv_status" in
	0) say "verified provenance: built by $REPO's release workflow from $tag" ;;
	# gh's exit status when it needs to be logged in.
	4) say "note: provenance not checked; gh is not logged in (gh auth login)" ;;
	*)
		cat "$tmp/provenance" >&2
		die "provenance check failed for $name.tar.gz; not installing it"
		;;
	esac
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

	# curl keeps redirects on https (--proto-redir), and wget_https checks
	# them; the first request has to be https as well.
	case "$RELEASES" in
	https://*) ;;
	*) die "WICKET_RELEASES_URL must be an https URL: $RELEASES" ;;
	esac

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

	child="" starting="" stopping=""
	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	# A signal trap that only cleaned up would let the script carry on
	# without $tmp. Stop with the status the shell would give.
	trap 'stop 130' INT
	trap 'stop 131' QUIT
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
	provenance "$tmp/$name.tar.gz"

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
# A TERM that arrives while the client and watcher are being started is held
# until both pids are known, as in run.
freerdp3_probe() {
	fr_starting=1 fr_stopping=""
	trap 'if [ -n "$fr_starting" ]; then fr_stopping=1; else freerdp3_probe_stop; fi' TERM
	"$1" --version >"$fr_out" 2>&1 </dev/null &
	fr_pid=$!
	(
		# KILL, as a TERM that reaches sleep before it has started
		# can be lost.
		fr_sleep="" fr_held=""
		trap 'fr_held=1; [ -z "$fr_sleep" ] || { kill -KILL "$fr_sleep"; exit 0; }' TERM
		sleep 3 &
		fr_sleep=$!
		[ -z "$fr_held" ] || { kill -KILL "$fr_sleep"; exit 0; }
		# After an interrupt $tmp is gone and the marker cannot be
		# written; the client must still be killed. printf, not :, as
		# a redirection error on a special builtin exits sh.
		wait "$fr_sleep" && {
			printf '' >"$fr_out.timeout" || :
			kill -KILL "$fr_pid"
		}
	) &
	fr_watch=$!
	fr_starting=""
	[ -z "$fr_stopping" ] || freerdp3_probe_stop
	wait "$fr_pid" || :
	kill "$fr_watch" || :
	wait "$fr_watch" || :
}

freerdp3_probe_stop() {
	kill -KILL "$fr_pid" 2>/dev/null || :
	kill "$fr_watch" 2>/dev/null || :
	wait
	exit 143
}

# Everything runs from here, so a download cut off mid-script does nothing.
main "$@"
