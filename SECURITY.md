# Security

## Supported versions

makeit is in beta. Only the latest 0.x release gets security fixes. Update with `makeit update`
or `makeit self update`.

## Reporting a vulnerability

Report it privately through GitHub's private vulnerability reporting: on
[github.com/youssefKadaouiAbbassi/make-it](https://github.com/youssefKadaouiAbbassi/make-it), open
the Security tab and choose "Report a vulnerability". Do not open a public issue for it.

Include the makeit version (`makeit version`), your OS, and the steps that show the problem.

## Verifying a release

Each release has the binaries, `checksums.txt` (the sha256 of each binary) and
`checksums.txt.minisig`, a [minisign](https://jedisct1.github.io/minisign/) signature of
`checksums.txt` made by the maintainer. The signing key never leaves the maintainer's machine; CI
does not sign.

The release public key (key id `C608595C3727E897`):

```
RWSX6Cc3XFkIxiPHeM4IZ4jBFnOnfiMOROpHoZkjxB3p39XW6qrkkp79
```

Download `checksums.txt`, `checksums.txt.minisig` and your binary from the same release, then:

```console
$ minisign -V -P RWSX6Cc3XFkIxiPHeM4IZ4jBFnOnfiMOROpHoZkjxB3p39XW6qrkkp79 -m checksums.txt
$ sha256sum -c --ignore-missing checksums.txt
```

The trusted comment minisign prints must read `makeit <version> checksums.txt`, for the version
you downloaded. Without `sha256sum` (macOS), compare `shasum -a 256 <binary>` with the binary's
line in `checksums.txt`.

Two repository settings back this up. Releases are immutable: once published, their files cannot
be changed or added to. And only the maintainer's account can create, move or delete a `v*` tag;
no GitHub Action or token can. The release workflow builds only from such a tag.

## What the install scripts check

`install.sh` (Linux and macOS) and `install.ps1` (Windows) carry the release's version and public
key. They:

1. Download the binary for your machine, `checksums.txt` and `checksums.txt.minisig` from that
   release, over https. A release without the signature file is refused.
2. When minisign is installed, verify the signature with the script's own key, and stop if it does
   not verify or if the trusted comment names another release. Without minisign, they print
   "signature not checked: install minisign to verify (sha256 only)" and go on.
3. Check the binary's sha256 against `checksums.txt`, and stop on a mismatch.
4. Run `makeit self install --yes`, which copies makeit to `~/.local/bin`.

After that, `makeit self update` and `makeit update` replace the binary only when `checksums.txt`
carries a valid signature from a key built into the running makeit and the new binary matches its
sha256. That check does not depend on minisign being installed.

## What makeit runs on your machine

makeit runs package managers (with `sudo` on Linux, shown in the plan first), `git`, `gh` and the
`claude` CLI. The plugins it installs run hooks and MCP servers with your user's rights. Each one is
pinned to a commit in the plugin set, except the four language-server plugins, which come from
claude-plugins-official and update with it; [What makeit installs](docs/included.md) lists them and the
settings makeit writes, including `permissions.defaultMode: bypassPermissions`.
