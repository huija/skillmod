# Prerequisites and installation

Use this reference for first-time setup, executable upgrades, and PATH
diagnosis. Prefer release binaries so users do not need a Go toolchain.

## 1. Inspect before installing

Run read-only checks appropriate for the active shell.

POSIX shells:

```sh
command -v git
git --version
command -v skillmod
skillmod --version
```

PowerShell:

```powershell
Get-Command git -ErrorAction SilentlyContinue
git --version
Get-Command skillmod -ErrorAction SilentlyContinue
skillmod --version
```

Interpret each command independently. A missing `skillmod` executable is
expected during first-time setup. If both commands already work, report their
paths and versions and do not reinstall unless the user requested an upgrade,
which `skillmod upgrade` performs in place (see below).

## 2. Ensure Git is available

skillmod invokes the system `git` executable. If Git is missing, explain that
it must be installed and placed on PATH before skillmod can fetch sources.

Offer the current platform's normal option, but do not run a package manager or
elevated installer without authorization:

- macOS: Xcode Command Line Tools or `brew install git` when Homebrew is already
  the user's package manager.
- Windows: Git for Windows, commonly through
  `winget install --id Git.Git -e --source winget`.
- Debian/Ubuntu: the distribution `git` package through `apt`.
- Fedora/RHEL: the distribution `git` package through `dnf`.
- Arch Linux: the distribution `git` package through `pacman`.

When package-manager details may have changed, use the current instructions at
https://git-scm.com/downloads/ rather than inventing flags. After installation,
start a new terminal if PATH has not refreshed and rerun `git --version`.

## 3. Select the release asset

Use the latest non-prerelease release from:

https://github.com/huija/skillmod/releases/latest

Determine the machine's operating system and architecture instead of asking
the user to guess. Map common architecture names as follows:

| Machine value | Release architecture |
| --- | --- |
| `x86_64`, `AMD64` | `amd64` |
| `aarch64`, `arm64`, `ARM64` | `arm64` |

Release archives follow these shapes, where `<version>` omits the leading `v`:

```text
skillmod_<version>_linux_amd64.tar.gz
skillmod_<version>_linux_arm64.tar.gz
skillmod_<version>_darwin_amd64.tar.gz
skillmod_<version>_darwin_arm64.tar.gz
skillmod_<version>_windows_amd64.zip
skillmod_<version>_windows_arm64.zip
```

Also obtain `checksums.txt`. Resolve asset URLs from GitHub release metadata or
the release page; do not guess a version or silently substitute a source-build
artifact.

## 4. Download and verify

Download into a newly created temporary directory. Before extracting or
executing the archive, verify its SHA-256 digest against the exact entry in
`checksums.txt`.

- Linux commonly provides `sha256sum`.
- macOS provides `shasum -a 256`.
- PowerShell provides `Get-FileHash -Algorithm SHA256`.

Treat a missing checksum entry or mismatch as a hard failure. Delete the
downloaded artifact and do not install it. Never disable verification merely to
make installation continue.

## 5. Put the executable on PATH

Prefer a user-writable directory:

- Linux/macOS: `${XDG_BIN_HOME:-$HOME/.local/bin}`.
- Windows: a dedicated directory such as
  `%LOCALAPPDATA%\Programs\skillmod\bin`.

Create the directory if needed, extract only the expected `skillmod` or
`skillmod.exe` executable, and set the executable bit on Unix. If the selected
directory is not on PATH, explain the exact shell/profile change and obtain
authorization before editing it. Use `/usr/local/bin` or another system-wide
directory only when the user explicitly prefers a system-wide installation and
understands the required permissions.

Do not use `sudo curl ... | sh`, execute an unverified download, or append
duplicate PATH entries.

## 6. Verify the installation

Run:

```sh
skillmod --version
skillmod --help
git --version
```

If the current process cannot see a newly updated PATH, invoke the executable by
its full path for verification and tell the user to open a new terminal. Report
the installed executable path, version, and whether Git was found.

## 7. Upgrade an installed executable

An installed executable upgrades itself:

```sh
skillmod upgrade --check   # report whether a newer release exists
skillmod upgrade           # install it
skillmod --version
```

`upgrade` reads the release published for the current operating system and
architecture, verifies the archive against that release's `checksums.txt`, and
replaces the running executable. It needs network access to `api.github.com` and
`github.com`, and it discards an archive whose checksum does not match instead
of installing it. `--dry-run` downloads and verifies without replacing the
executable, `--check` stops before downloading, and `--tag <tag>` installs a
specific release.

An executable placed by a package manager, or one in a directory the user cannot
write, should be upgraded through that package manager or reinstalled from the
release archive instead. `upgrade` names the path it could not replace rather
than asking for elevation.

## Alternative: build with Go

When the user already has the Go version required by the repository and prefers
a source installation, offer:

```sh
go install github.com/huija/skillmod@latest
```

Verify that `go env GOBIN`, or the first `go env GOPATH` entry plus `/bin`, is
on PATH. Do not install Go solely to avoid using the published binary unless the
user chooses that route.
