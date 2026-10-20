# Security

## Reporting a vulnerability

Please report security issues privately through GitHub's
[private vulnerability reporting](https://github.com/gaius-codius/wicket/security/advisories/new)
rather than opening a public issue. That form is the only supported private
channel; there is no security email address.

Wicket is a personal project maintained in spare time. Expect an
acknowledgement within a week or so, and please give a fix a reasonable window
before disclosing.

## Supported versions

The latest release. There are no backports to earlier tags.

## What Wicket tries to protect

- **Passwords never reach the config file.** They live in the platform keyring
  (Secret Service on Linux, Keychain on macOS), keyed by `service=wicket`, the
  config path and a stable per-profile `profile_id` UUID (not the display
  name). A config file that already contains
  a key named `password`, `pass`, `secret` or `passwd` has it stripped on load,
  reported as a warning, and never written back.
- **Passwords never reach the process table.** The FreeRDP child's command
  line is `/args-from:fd:3` alone. Its arguments, the password last as `/p:`,
  are written a line each to a private pipe on its file descriptor 3, which
  nothing else holds. A profile argument containing `/p:` is refused outright,
  so a hand-edited config cannot smuggle one in, and so is one containing a
  line break, which would reach FreeRDP as an extra argument. `/cert:ignore`
  is refused as well: certificate trust is FreeRDP's decision to present, not
  Wicket's to suppress.
- **Certificate trust stays with FreeRDP.** Wicket never reads or writes
  FreeRDP's certificate store. When FreeRDP has a server certificate it has not
  been told to trust, new or changed, it shows it and asks on its stdin.
  `wicket connect` run from a terminal hands the client that terminal. A
  session started from the TUI has the null device as its stdin, so FreeRDP
  refuses the certificate. The TUI then offers a retry with the terminal
  handed to the client, where FreeRDP asks. Only FreeRDP's answer changes its
  store.
- **A password that cannot be delivered is an error, not a silent fallback.**
  If the write to the child's arguments pipe fails, the client is killed and
  the failure is reported, rather than leaving it to start without them.
- **Config and state are written `0600`**, atomically, with the file and its
  directory synced so a crash cannot leave a half-written config behind.
- **Profile fields reject control characters.** A newline or an escape sequence
  in a hostname would otherwise reach both the terminal and the FreeRDP command
  line.
- **Password length is bounded** at 511 bytes, the most FreeRDP kept when it
  read a password from stdin, so a password saved under that rule is never cut
  short, and, with the arguments bounded too, a pathological value cannot be
  used to stall a write to the client. A saved
  password over the limit is reported when connecting, not used.
- **Keyring prompts are completed, not ignored.** A write against a locked
  keyring is not reported as saved until the prompt is answered. On Linux
  (Secret Service), a prompt nobody answers times out and is dismissed rather
  than hanging. On macOS (Keychain), `SecItem*` calls are synchronous: context
  cancel stops further native steps and rejects results after cancel, but an
  unlock or access dialog already shown cannot be dismissed by Wicket and
  remains until the user responds or the process exits.
- **The installer checks who built a release, not only that it arrived
  whole.** `install.sh` checks each download against the release's
  `SHA256SUMS`, but both come from the same place, so that alone shows only
  that the tarball is complete. Releases from v0.4.0 on carry a signed build
  provenance attestation. When the GitHub CLI 2.68 or newer is installed, the
  installer runs `gh attestation verify` with an exact match on the release
  workflow and the tag, and refuses the download when the check fails,
  including when GitHub cannot be reached or rate limits it. v0.1.0 to v0.3.1
  predate attestations; their tarballs are checked against SHA-256 digests
  kept in the script itself, so a replaced release does not pass.
- **The installer uses only what it fetched over https.** A
  `WICKET_RELEASES_URL` or `WICKET_API_URL` that is not `https://` is
  refused. `curl` and Wget2 refuse a redirect away from https before
  following it. GNU Wget 1.x and busybox cannot, so the installer reads
  their redirects after the download and refuses it if any left https. It uses `curl`, or a `wget` it knows to check certificates
  (GNU Wget 1.x, Wget2, or busybox 1.33 or newer through `openssl` and not
  behind `http_proxy`), and makes it check them whatever `~/.curlrc` or
  `~/.wgetrc` says.

## What Wicket does not protect against

- **Anything running as your user.** Once your keyring is unlocked, any process
  with your privileges can ask it for the same secrets Wicket asks for, read
  Wicket's memory, or attach a debugger. Wicket is not a defence against a
  compromised user account, and root sees everything regardless.
- **The Secret Service transport uses the `plain` algorithm.** The Secret
  Service specification offers `dh-ietf1024-sha256`, which encrypts the secret
  in transit between Wicket and the keyring daemon; Wicket opens the session
  with `plain`, so the password crosses the D-Bus session bus unencrypted.

  The session bus is a socket in your own runtime directory, mode `0700`, so
  the only parties who can observe that traffic are you and root — and both are
  already in a position to obtain the secret by easier means. The practical
  exposure is therefore small on a normal single-user desktop. It is larger
  where the bus is not purely local and trusted: a forwarded or proxied session
  bus, a sandbox layer brokering D-Bus for other applications, or a system
  logging bus traffic. Implementing the encrypted transport is tracked as
  future work.
- **Go strings cannot be wiped.** Password bytes received over D-Bus are
  zeroed as soon as they have been copied, but the copy lives in a Go string
  and may persist in memory until it is collected.
- **An install without a recent GitHub CLI checks less.** With no `gh`, a
  `gh` older than 2.68, or one that asks for a login, the installer says the
  provenance check was skipped and checks a v0.4.0 or later download only
  against `SHA256SUMS` from the same release. Someone who can replace a
  release's files can replace both. Install `gh`, or run the
  `gh attestation verify` command in the README yourself. The installer
  script itself is trusted as it comes from the repository.
- **What FreeRDP does with the connection.** Wicket builds a command line and
  hands over a password. Everything after that — the RDP protocol, certificate
  validation, the remote host — belongs to FreeRDP.
- **A config file you did not write.** Wicket validates what it loads, but a
  config file is a list of hosts to connect to and passwords to hand over.
  Treat it as you would an SSH config.
