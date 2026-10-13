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
# Where the wget path reads the latest tag; curl reads it from $RELEASES.
API="${WICKET_API_URL:-https://api.github.com/repos/$REPO}"
BINDIR="${WICKET_BINDIR:-$HOME/.local/bin}"
# Releases published before the release workflow attested its builds, and
# the SHA-256 of each of their tarballs. Their SHA256SUMS sits next to the
# tarball it vouches for, so these are checked against digests that ship
# with this script instead. New releases are attested and never go here.
UNATTESTED="v0.1.0 v0.1.1 v0.2.0 v0.3.0 v0.3.1"
UNATTESTED_SUMS="
32c585684f94d6a9b1be05b20f478749fe63ddbe2c4137df1c171ca0c08a940f  wicket_0.1.0_linux_amd64.tar.gz
a1048d4b614817793cfd577803a3f6d032d2eb563322220b3f9da4afa235356b  wicket_0.1.0_linux_arm64.tar.gz
c8c97d637b1757ffeeb429b80be8c769321bec7bebf6053f32772495c362d228  wicket_0.1.1_linux_amd64.tar.gz
5513cbbc70d7d89ebf559fa90ec665656c899b809660e034b709767f3109a430  wicket_0.1.1_linux_arm64.tar.gz
6d7993337a85233813e31e412427d5f6d372a9412be16896fe86cb8f91567951  wicket_0.2.0_linux_amd64.tar.gz
a0af6c3f600007bd19ee502a048ae2e91b910bd1c893efa780c0d9a075639ed0  wicket_0.2.0_linux_arm64.tar.gz
e7df04d385202d8ba1cda4a83d5644dc13baf15e3bc539e42f720d604bd96bcb  wicket_0.3.0_linux_amd64.tar.gz
5b594a96091a9a94a2b8e2d8b0c3cc60ded9a7af0a8f039fbdda453a14781039  wicket_0.3.0_linux_arm64.tar.gz
4a32ad85af5c43f557be96b7773d5723d4c5e78480cdb2592d9869e20b02d669  wicket_0.3.1_darwin_amd64.tar.gz
14a5232f0d416e6cf782e3670b0f22bb16db756f3870bb574afad4eb969f7011  wicket_0.3.1_darwin_arm64.tar.gz
7496a37a6ad73db67fd07dd0bff216bf625d3d0d920f8c971581dcfae48c01c8  wicket_0.3.1_linux_amd64.tar.gz
96013e67cb9385ea4e27fbd94b3ef1527121b7e9fda03be7cbc29ee783aab09c  wicket_0.3.1_linux_arm64.tar.gz
"

say() { printf '%s\n' "$*"; }
die() { printf 'wicket install: %s\n' "$*" >&2; exit 1; }

usage() {
	cat <<'USAGE'
Usage: install.sh [--version vX.Y.Z] [--uninstall]

Installs the latest wicket release into ~/.local/bin (override with
WICKET_BINDIR), or updates an existing install. Checks the download against
the release's SHA256SUMS and, when gh 2.68 or newer is installed, checks that
the release workflow built it (releases up to v0.3.1: against digests kept in
this script).

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

# stop kills the running command, cleans up and exits with $1. Background
# commands ignore INT and QUIT, so it has to kill them for Ctrl-C and Ctrl-\
# too. A command that ignores TERM as well gets 2 s and then KILL, from a
# watcher: kill -0 cannot tell, as a child that has exited stays a zombie
# until it is waited for.
stop() {
	if [ -n "$starting" ]; then
		stopping=$1
		return
	fi
	if [ -n "$child" ]; then
		kill_tree TERM "$child"
		(
			sleep 2
			kill_tree KILL "$child"
		) &
		st_watch=$!
		wait "$child" 2>/dev/null || :
		kill_tree KILL "$st_watch"
		wait "$st_watch" 2>/dev/null || :
	fi
	cleanup
	exit "$1"
}

# cleanup removes the temp dir and, once the copy into $BINDIR has started,
# the copy, which is gone by then if it was renamed into place.
cleanup() {
	rm -rf "$tmp"
	[ -z "$copying" ] || rm -f "$BINDIR/.wicket.new"
}

# kill_tree sends signal $1 to process $2 and everything under it, the
# deepest first, so none is handed to init before it is found. Without job
# control there is no process group to signal, and set -m in sh can take the
# terminal from the script, so this walks the tree with pgrep. Busybox wget
# hands https to a forked helper that would hold the connection until the
# server drops it, and a FreeRDP client can be a wrapper around the real
# binary. Where there is no pgrep only $2 itself is signalled.
kill_tree() {
	for kt_child in $(pgrep -P "$2" 2>/dev/null); do
		kill_tree "$1" "$kt_child"
	done
	kill "-$1" "$2" 2>/dev/null || :
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
#
# Busybox runs openssl s_client for https, which checks the certificate
# since 1.33; before that it did not ask openssl to, and said nothing.
# Without openssl on PATH, or built without that helper, it falls back to TLS
# of its own that checks nothing, and says so. Either way anyone on the path
# could serve a tarball with a SHA256SUMS to match, so both are refused, the
# second failed download or not. Busybox has no --version; it prints its
# usage, which names its version, instead.
wget_https() {
	: >"$tmp/wget-version"
	run wget --version >"$tmp/wget-version" 2>&1 || :
	if grep -q 'BusyBox' "$tmp/wget-version"; then
		bb_version=$(sed -n 's/.*BusyBox v\([0-9]*\.[0-9]*\).*/\1/p' "$tmp/wget-version" | head -n 1)
		case "$bb_version" in
		"" | 0.* | 1.[0-9] | 1.[12][0-9] | 1.3[0-2])
			die "busybox wget ${bb_version:-of unknown version} does not check TLS certificates; install curl, or busybox 1.33 or newer with openssl"
			;;
		esac
	fi
	if grep -q 'Wget2' "$tmp/wget-version"; then
		rm -f "$2"
		run wget -q --https-only -O "$2" "$1" || return
		[ -e "$2" ] ||
			die "wget2 wrote nothing for $1; it skips a redirect away from https"
		return
	fi
	wg_status=0
	run wget -S -O "$2" "$1" 2>"$tmp/wget.log" || wg_status=$?
	if grep -q 'TLS certificate validation not implemented' "$tmp/wget.log"; then
		die "this wget (busybox without openssl) does not check TLS certificates; install curl, or openssl for busybox to use"
	fi
	[ "$wg_status" -eq 0 ] || return "$wg_status"
	grep -q 'HTTP/' "$tmp/wget.log" ||
		die "wget did not show its redirects, so they could not be checked; install curl"
	# Every Location has to be https or start with /, which keeps the
	# scheme. Anything else is refused, including a value the log shows in
	# a form this cannot read, such as one after a tab wget escaped.
	if grep -i '^[[:space:]]*location:' "$tmp/wget.log" |
		grep -iEqv '^[[:space:]]*location:[[:space:]]*(https://|/)'; then
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
		# All of it: a tag can hold a /, and its last part alone could
		# name another release.
		case "$url" in
		*/releases/tag/*) tag=${url#*/releases/tag/} ;;
		*) tag="" ;;
		esac
	else
		: >"$tmp/latest"
		wget_https "$API/releases/latest" "$tmp/latest" || :
		tag=$(sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' "$tmp/latest" | head -n 1)
	fi
	# Only a plain vX.Y.Z, as the tag goes into a path and a URL.
	case "$tag" in
	v*[!0-9.]* | v*..* | v*. | v*.*.*.*) die "no release found at $RELEASES" ;;
	v[0-9]*.[0-9]*.[0-9]*) ;;
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

# provenance checks that tarball $1 is the one the release workflow built
# from $tag. SHA256SUMS comes from the same place as the tarball, so it
# shows the download is whole, not who made it. A release from before the
# workflow attested its builds is checked against UNATTESTED_SUMS. Any other
# is checked with gh: when gh cannot check (not installed, too old) this
# says so and the install carries on; a check that runs and fails stops it. A release that is meant to be attested and is not fails
# like a forged one, as the two look the same.
provenance() {
	case " $UNATTESTED " in
	*" $tag "*)
		pinned=$(printf '%s\n' "$UNATTESTED_SUMS" | awk -v f="$name.tar.gz" '$2 == f { print $1 }')
		[ -n "$pinned" ] || die "$name.tar.gz was never published for $tag"
		[ "$(sha256 "$1")" = "$pinned" ] ||
			die "$name.tar.gz does not match the digest this installer has for $tag"
		say "checked against the digest this installer has for $tag"
		return
		;;
	esac
	if ! command -v gh >/dev/null 2>&1; then
		say "note: provenance not checked; install gh to check it"
		return
	fi
	# gh before 2.66 crashes on the bundles GitHub's API now returns, and
	# 2.68 is the first whose help shows it is new enough (--source-ref).
	: >"$tmp/gh-help"
	run gh attestation verify --help >"$tmp/gh-help" 2>/dev/null || :
	if ! grep -q -- '--source-ref' "$tmp/gh-help"; then
		say "note: provenance not checked; this gh is too old to check it"
		return
	fi
	# The certificate names the workflow and the ref it ran on, so an exact
	# match ties the tarball to release.yml and to $tag. GH_HOST, or gh
	# would look on whichever host it defaults to; with it, gh also checks
	# without a login, as the attestations of a public repo are public.
	pv_status=0
	run env GH_HOST=github.com gh attestation verify "$1" --repo "$REPO" \
		--cert-identity "https://github.com/$REPO/.github/workflows/release.yml@refs/tags/$tag" \
		>"$tmp/provenance" 2>&1 || pv_status=$?
	case "$pv_status" in
	0) say "verified provenance: built by $REPO's release workflow from $tag" ;;
	# gh's exit status when it wants a login, should a version ask again.
	4) say "note: provenance not checked; gh is not logged in (gh auth login)" ;;
	*)
		cat "$tmp/provenance" >&2
		say "If gh could not reach GitHub, or was rate limited (set GH_TOKEN), fix that and run this again." >&2
		die "provenance check failed for $name.tar.gz; not installing it"
		;;
	esac
}

# installed_version sets $current to the installed wicket's version, or to
# nothing. Through run and a file, as in latest_tag: a trapped signal would
# wait for a binary that hangs.
installed_version() {
	current=""
	[ -x "$BINDIR/wicket" ] || return 0
	run "$BINDIR/wicket" --version >"$tmp/installed" 2>/dev/null || :
	current=$(cut -d ' ' -f 2 "$tmp/installed")
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
	case "$API" in
	https://*) ;;
	*) die "WICKET_API_URL must be an https URL: $API" ;;
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

	child="" starting="" stopping="" copying=""
	tmp=$(mktemp -d)
	trap cleanup EXIT
	# A signal trap that only cleaned up would let the script carry on
	# without $tmp. Stop with the status the shell would give.
	trap 'stop 129' HUP
	trap 'stop 130' INT
	trap 'stop 131' QUIT
	trap 'stop 143' TERM

	[ -n "$tag" ] || latest_tag
	case "$tag" in v*) ;; *) tag="v$tag" ;; esac
	version=${tag#v}

	installed_version
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
	# overwritten in place and a failed copy leaves the old one. Through
	# run, as a copy to a slow disk is stopped like a download.
	copying=1
	run cp "$tmp/$name/wicket" "$BINDIR/.wicket.new"
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
# stop kills the probe, it takes the client (with kill_tree, any process it
# started) and the watcher with it. A process the client starts and leaves
# behind when it exits on its own is not stopped: by then it belongs to init
# and nothing ties it to the client.
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
			kill_tree KILL "$fr_pid"
		}
	) &
	fr_watch=$!
	fr_starting=""
	[ -z "$fr_stopping" ] || freerdp3_probe_stop
	wait "$fr_pid" || :
	# KILL, as the watcher loses a TERM that comes before its trap is set,
	# and would then report a timeout for a client that answered.
	kill_tree KILL "$fr_watch"
	wait "$fr_watch" || :
}

freerdp3_probe_stop() {
	kill_tree KILL "$fr_pid"
	kill_tree KILL "$fr_watch"
	wait
	exit 143
}

# Everything runs from here, so a download cut off mid-script does nothing.
main "$@"
