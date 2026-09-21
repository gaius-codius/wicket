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

- **Passwords never reach the config file.** They live in the Secret Service
  keyring. A config file that already contains a key named `password`, `pass`,
  `secret` or `passwd` has it stripped on load, reported as a warning, and
  never written back.
- **Passwords never reach the process table.** The FreeRDP child is started
  with `/from-stdin:force` and the password is written to its stdin. Building a
  command line containing `/p:` is refused outright, so a hand-edited config
  cannot smuggle one in. `/cert:ignore` is refused for the same reason:
  certificate trust is FreeRDP's decision to present, not Wicket's to suppress.
- **A password that cannot be delivered is an error, not a silent fallback.**
  If the write to the child's stdin fails, the client is killed and the failure
  is reported, rather than leaving it sitting at a prompt it can never satisfy.
- **Config and state are written `0600`**, atomically, with the file and its
  directory synced so a crash cannot leave a half-written config behind.
- **Profile fields reject control characters.** A newline or an escape sequence
  in a hostname would otherwise reach both the terminal and the FreeRDP command
  line.
- **Password length is bounded** at 4096 bytes, so a pathological value cannot
  be used to stall a write to the client.
- **Keyring prompts are completed, not ignored.** A write against a locked
  keyring is not reported as saved until the prompt is answered, and a prompt
  nobody answers times out and is dismissed rather than hanging.

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
- **What FreeRDP does with the connection.** Wicket builds a command line and
  hands over a password. Everything after that — the RDP protocol, certificate
  validation, the remote host — belongs to FreeRDP.
- **A config file you did not write.** Wicket validates what it loads, but a
  config file is a list of hosts to connect to and passwords to hand over.
  Treat it as you would an SSH config.
