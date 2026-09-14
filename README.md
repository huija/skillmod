# skillmod — go mod for Agent Skills

English | [简体中文](README_CN.md)

A skill dependency manager for agent projects, inspired by Go modules:
**declare dependencies in the project (`SKILL.mod`) + lock content (`SKILL.lock`) + reconcile with one command (`sync`)**.

How this relates to AGENTS.md: AGENTS.md tells an agent *how to behave*; SKILL.mod declares *which capabilities it needs*.

## The problem

Skills—packaged instructions and scripts—shape agent behavior, but managing them is still stuck in a pre-dependency-manager era: manual copies, Git submodules, and platform-specific marketplaces. As a result, agents behave differently across machines in the same team, failures cannot be traced back to the exact version in use, and tampered content can go unnoticed.

skillmod applies the Go module model to skills: declarations in `SKILL.mod`, content-addressed locks in `SKILL.lock` using dirhash, and idempotent reconciliation through `skillmod sync`. Every machine receives exactly the same set of skills.

## Install

### Agent-guided installation (recommended)

With Node.js/npm available, install the companion Agent Skill globally so your
coding agent can set up and operate skillmod from any project:

```bash
npx skills add huija/skillmod --skill skillmod --global
```

Then ask your agent:

```text
Install skillmod and make it available on PATH.
```

The skill checks the Git prerequisite and any existing skillmod installation,
selects the release binary for the current operating system and architecture,
verifies it against the published SHA-256 checksums, installs it in a
user-writable directory on `PATH`, and verifies the result. It also covers
project/global workflows, CI, troubleshooting, and sanitized GitHub issue
reports. Omit `--global` when the guidance should be available only in the
current project.

### Manual installation

Without Go, download the archive for your platform and `checksums.txt` from
[GitHub Releases](https://github.com/huija/skillmod/releases), verify the
archive, extract it, and put `skillmod` on your `PATH`.

With Go 1.26.1 or later:

```bash
go install github.com/huija/skillmod@latest
```

For local development from a repository checkout:

```bash
make install
```

This installs `skillmod` into `go env GOBIN`, or the first `GOPATH/bin` entry (`%GOPATH%\bin` on Windows) when `GOBIN` is unset, and embeds the current Git revision as the development version. On Windows, run the Makefile from Git Bash (which provides `sh`) or override the destination, for example with `make install INSTALL_DIR=/usr/local/bin`. The selected directory must be on `PATH`.

## Prerequisites

skillmod invokes the system `git` executable to fetch sources, so Git must be installed and on `PATH` (on Windows, install [Git for Windows](https://gitforwindows.org/); it is not preinstalled). SSH remotes additionally require `ssh` on `PATH`.

## Capabilities

- **Nine CLI commands**: `init` / `get` / `sync` / `list` / `why` / `update` / `remove` / `prune` / `verify`
- **Direct Git sources**, analogous to Go's direct mode: a skill is either a tagged repository or a monorepo subdirectory (`<repo>//<subdir>`). Publishing means creating a tag; no server or registry is required.
- **Three version forms**: semantic-version tags, commit SHAs, and pseudo-versions for repositories without tags. Branch names are rejected because mutable references cannot be locked.
- **Shared persistent storage**: readable, immutable full-repository snapshots are stored at `~/.agents/skillmod/pkg/mod/<host>/<owner>/<repo>@<version>`. Bare Git repositories, refs, and resolution metadata live under `pkg/mod/cache`. HTTPS, default-port SSH, and `.git` URL variants share storage. Set `SKILLMOD_HOME` to override the location.
- **Link-first installation**: `auto` links to shared read-only snapshots, falling back to byte-for-byte copies when links are unavailable, including Windows without link privileges. Use `copy` for an independent directory.
- **Adopt existing skills**: `init` discovers directories and directory links. `--global` manages user-wide skills using the same shared cache as projects.
- **Flat 1:1 dependencies**: no transitive dependency resolution and no constraint solver.
- **Zero telemetry**

## Example

```toml
# SKILL.mod (maintained by people and committed)
schemaversion = 1

[[skill]]
name = "code-review"
source = "github.com/acme/agent-skills//code-review"
version = "code-review/v1.2.0"

[[skill]]
name = "legacy-notes"
local = true
```

```toml
# SKILL.lock (tool-managed; deterministic and timestamp-free)
schemaversion = 1

[[skill]]
name = "code-review"
source = "github.com/acme/agent-skills//code-review"
version = "code-review/v1.2.0"
commit = "7f3a9c1e00000000000000000000000000000000"
dirhash = "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
```

```bash
skillmod init                                          # Scan existing skills and create declarations
skillmod get github.com/anthropics/skills//skills/pdf  # Add a skill; use a pseudo-version when no tag exists
skillmod get github.com/openai/skills//gh-fix-ci       # A unique skill name can abbreviate a nested path
skillmod sync                                          # Reconcile with the lock file, idempotently
skillmod why pdf                                       # Explain provenance and per-target status
skillmod remove pdf                                    # Remove its declaration and clean managed installs
skillmod verify                                        # Validate in CI; drift produces a non-zero exit code
```

A single segment after `//` first addresses an exact root subdirectory, then falls back to a unique skill name anywhere below `skills/`; ambiguous names require the full path. When `//<subdir>` is omitted, `get` discovers a root `SKILL.md` and every `SKILL.md` below `skills/`. Interactive terminals show a compact, colored one-line list; displayed commands omit the redundant `https://` prefix and prefer a unique skill-name shorthand when safe. Use ↑/← for the previous item, ↓/→ for the next item, Space to toggle selections, D to show or hide the current description and command, and Enter to confirm. `--yes` installs every discovered skill.

When different sources publish the same skill name, install the additional entry with `--alias <directory>`. Both declarations and lock records are retained; lock entries omit `dir` when the installation directory equals `name` and record it only for aliases. Aliases must be portable names and all installation directories must remain distinct after Unicode normalization and case folding so the same project works on Linux, macOS, and Windows. Re-getting the same source with another alias keeps the old directory and reports that `skillmod prune` can remove it. `skillmod update <name>` updates all entries with that published name; pass an alias to update only that installation.

Long-running `get` and `update` operations show a compact animated status block on interactive terminals: the primary stage appears beside the spinner, with simultaneous detail states on a muted second line. Remote version checks request only HEAD, branch, and tag refs; `update` deduplicates equivalent repository URLs and checks up to four distinct repositories concurrently. Cached repository snapshots are integrity-checked once per command and reused across skill-name discovery and batch selection.

### Adopt existing directories: project and global scopes

```bash
skillmod init --yes --dry-run          # Preview the current project's import
skillmod init --yes                    # Adopt existing project skills
skillmod --global init --yes           # Adopt existing user-wide skills
skillmod --global list
skillmod --global verify
```

`init` scans `.agents/skills/` and `.claude/skills/` in the selected scope without changing existing directories, links, or files. Directory links are followed for content verification. Broken links, unverifiable contents, and invalid directory names are reported and skipped. Different contents at the same directory name across platforms must be reconciled or renamed first. Identical entries across adapters are merged while every candidate path remains available for provenance recovery.

Provenance is recovered from matching existing lock records or verified skillmod snapshots, including monorepo subdirectories and aliases. `init --global` also imports the upstream installer's `.skill-lock.json`, while project init reads `skills-lock.json`. A recorded Git source and revision remain authoritative even when the installed directory has drifted; when an upstream record omits its revision, init adopts the latest immutable resolution only if its contents exactly match the installed skill. Sources that cannot be resolved safely are retained as local baselines so one unresolved entry does not discard the rest of the import. Import writes both `SKILL.mod` and `SKILL.lock`; replacing an existing declaration requires `--force`, which first backs it up as `SKILL.mod.bak`.

| Scope | Declaration and lock location | Default installation directory |
| --- | --- | --- |
| Project (default) | `SKILL.mod` and `SKILL.lock` in the current directory | `.agents/skills/` in the current directory |
| Global (`--global`) | `$SKILLMOD_HOME/global/`, default `~/.agents/skillmod/global/` | `~/.agents/skills/` |

**There is only one cache**: both scopes use `$SKILLMOD_HOME/pkg/mod/`, defaulting to `~/.agents/skillmod/pkg/mod/`. The `global/` directory contains manifests, not another snapshot cache. With the Claude Code adapter enabled, global installations use `~/.claude/skills/`. All nine commands support `--global`; project commands do not automatically merge the global manifest. `--dry-run` leaves manifests and installations untouched, though remote provenance verification may populate the shared cache.

### Installation modes and migrating existing copies

```bash
skillmod sync --relink --dry-run       # Preview reinstalling locked remote skills
skillmod sync --relink                 # Convert matching copies to links, with copy fallback
skillmod --global sync --relink        # The same operation for global skills
skillmod sync --relink --install-mode=copy  # Detach into independent, editable directories
```

Ordinary `sync` preserves matching installations and remains idempotent. `--relink` explicitly reinstalls remote entries using the selected mode. Both respect local-modification conflict handling; `--yes` does not force conflicting files to be overwritten. Local entries are recorded and verified without automatic migration.

`get`, `sync`, `update`, and `remove` return exit code 3 when independent work completed but one or more targets were safely preserved. In JSON, the top-level `action` identifies the command (`get`, `init`, `list`, `prune`, `remove`, `sync`, `update`, `verify`, or `why`); each entry's `action` is its aggregate outcome (`conflict`, `drift`, `install`, `installed`, `keep`, `local`, `local-drift`, `matched`, `missing`, `partial`, `prune`, `remove`, `skip`, `stale`, `unlocked`, `unresolved`, `unverifiable`, or `update`). Per-directory facts live only in `targetResults`, whose actions are `drift`, `install`, `installed`, `keep`, `missing`, `remove`, `skip`, `unlocked`, or `unverifiable`. Inspection reports (`list`, `why`, and `verify`) also include `requestedVersion` for remote entries: it is the exact `SKILL.mod` value (an empty string tracks latest), while `version` is the installed version from `SKILL.lock`. `update` never silently moves to a lower semantic version when a newer tag disappears; use `--allow-downgrade` for an intentional downgrade.

`skillmod remove <name-or-alias>` removes matching declarations and clean managed installations as one recoverable transaction. Locally modified or unverifiable directories are preserved and produce partial completion. `skillmod why <name-or-alias>` shows the source, resolved version, commit, dirhash, alias directory, and status of every configured installation target.

`auto` prefers native directory symlinks and falls back to copies if link creation fails; `copy` always creates an independent directory. `--install-mode` overrides the machine configuration's `install_mode` setting. Links point at shared read-only snapshots; detach with `copy` before editing. Symlinks inside skill contents remain unsupported.

Installation mode, absolute cache paths, and scope are excluded from `SKILL.mod` and `SKILL.lock`. Identical declarations and versions therefore produce identical manifests and locks across systems and installation modes. Updating switches only the selected scope's installation entry. `prune` removes stale installation entries without deleting link targets or shared snapshots. There is no automatic cache eviction; before manually deleting a snapshot, ensure that no installed link uses it.

### Command output language

Command help, summaries, prompts, errors, and human-readable JSON notes follow `SKILLMOD_LANG` when it is explicitly set. Otherwise, skillmod reads the first non-empty system locale in `LC_ALL` → `LC_MESSAGES` → `LANG`. English and Chinese locale values are recognized; missing or unsupported locales fall back to English. Use `SKILLMOD_LANG=zh` to select Chinese explicitly. Locale-style values such as `en_US.UTF-8` and `zh_CN.UTF-8` are also accepted.

```bash
SKILLMOD_LANG=zh skillmod sync
```

Machine-readable JSON field names and action identifiers are not translated.

Translations are maintained centrally as symmetric gettext/POSIX locale catalogs under [`locales/`](locales/): `en_US.po` and `zh_CN.po` contain the same message-key set. The CLI embeds both files. `SKILLMOD_LANG=en` and `SKILLMOD_LANG=zh` remain convenient aliases.

Source code passes a short message key — `i18n.Text("cli.get.long")` — and the wording lives in the catalog, with `en_US.po` holding the English text. After adding or rewording user-facing text, regenerate the catalogs:

```bash
go generate ./internal/i18n
```

Generation fails when a key has no English or Chinese text; fill the reported `msgstr` values in `locales/` and run it again. See [`locales/README.md`](locales/README.md) for the key naming rule.

The first request for a given `repo@version` materializes a complete repository snapshot. Adding another skill from the same version later validates and installs it directly from the local subdirectory, without invoking Git or accessing the remote. An explicit `@commit` can likewise reuse an existing snapshot of that repository commit. Omitting the version to request latest, or running `skillmod update`, retains online refresh semantics.

```text
~/.agents/skillmod/pkg/mod/
├── github.com/anthropics/skills@v0.0.0-.../  # Directly browsable full-repository snapshot
└── cache/
    ├── vcs/                                  # Bare Git repositories, keyed internally by hash
    ├── download/                             # Repository versions, refs, and resolution metadata
    └── locks/
```

Skills are installed into the project's `.agents/skills/` directory by default. To also install them for Claude Code, set `agents` in the config file, whose location follows `os.UserConfigDir()`:

| OS      | Config path                                          |
| ------- | ---------------------------------------------------- |
| Linux   | `~/.config/skillmod/config.toml`                     |
| macOS   | `~/Library/Application Support/skillmod/config.toml` |
| Windows | `%AppData%\skillmod\config.toml`                     |

```toml
agents = ["agents", "claude-code"]
install_mode = "auto" # auto / copy
```

Installation directories are artifacts reconstructed from the lock file. Projects should add `.agents/skills/` to `.gitignore` and, when the Claude adapter is enabled, also ignore `.claude/skills/`. Commit only `SKILL.mod` and `SKILL.lock`.

## Current limitations

- No registry service.
- No transitive dependencies or version-constraint solving.
- No telemetry or skill-content security scanning.

## License

skillmod is released under the [MIT License](LICENSE).
