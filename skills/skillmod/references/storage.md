# Storage and configuration

skillmod keeps one shared content store for every scope, so projects, global
skills, and CI runs reuse the same immutable snapshots instead of downloading
the same repository again.

## Where a declaration lives, and where it installs

| Scope | Declaration and lock | Default installation directory |
| --- | --- | --- |
| Project (default) | `SKILL.mod` and `SKILL.lock` in the current directory | `.agents/skills/` in the current directory |
| Global (`--global`) | `$SKILLMOD_HOME/global/`, default `~/.agents/skillmod/global/` | `~/.agents/skills/` |

A skill installs into `.agents/skills/` in the selected scope, and skillmod
manages no other directory convention. All commands accept `--global`
(`-g`); ordinary commands do not merge the project and global manifests.

## The cache

There is only one cache. Both scopes use `$SKILLMOD_HOME/pkg/mod/`, defaulting
to `~/.agents/skillmod/pkg/mod/`. The `global/` directory holds manifests, not a
second snapshot cache.

```text
~/.agents/skillmod/pkg/mod/
├── github.com/anthropics/skills@v0.0.0-.../  # Directly browsable full-repository snapshot
└── cache/
    ├── vcs/                                  # Bare Git repositories, keyed internally by hash
    ├── download/                             # Repository versions, refs, and resolution metadata
    └── locks/
```

Snapshots are readable and immutable. HTTPS, default-port SSH, and `.git` URL
variants of the same repository share one snapshot, and the identity is
credential-free, so a token in a URL can never become part of a cache key.

The first request for a `repo@version` materializes the whole repository
snapshot. Adding another skill from the same version later validates and
installs it straight from that local subdirectory, without invoking Git or
touching the remote. An explicit `@commit` reuses an existing snapshot of that
commit the same way. Requesting latest, or running `skillmod update`, keeps
online refresh semantics.

Set `SKILLMOD_HOME` to relocate the store.

`--dry-run` leaves manifests and installations untouched, though remote
provenance verification may still populate the shared cache.

## Installation mode

`auto` prefers native directory symlinks into the shared read-only snapshots and
falls back to byte-for-byte copies when links are unavailable, including Windows
without link privileges. `copy` always creates an independent directory.
`--install-mode` overrides the machine configuration for one command.

Links point at a snapshot shared with every other project on the machine, so
editing through a link edits that shared content. Detach before editing:

```sh
skillmod sync --relink --install-mode=copy
```

## Sharing to other agents

`share` places a symlink named after each skill into the chosen agent
directory, such as `.claude/skills/<skill>`, and points it at the managed
`.agents/skills/<skill>`. The managed directory stays the one real copy:
editing the managed skill is visible through every link at once, and there is
nothing to synchronize. Links use the same `auto` fallback as installs, so a
filesystem without symlink support receives a byte-preserving copy instead,
and re-running `share` restores links that were replaced. Destinations are
unmanaged: they appear in no manifest or lock file, and `verify` never reads
them.

Symlinks inside skill contents remain unsupported.

`prune` removes stale installation entries without deleting link targets or
snapshots. There is no automatic cache eviction; before deleting a snapshot by
hand, confirm that no installed link still uses it. Never delete the cache as a
shortcut for removing one dependency.

## Configuration

Machine-level settings live in a config file whose location follows
`os.UserConfigDir()`:

| OS | Path |
| --- | --- |
| Linux | `~/.config/skillmod/config.toml` |
| macOS | `~/Library/Application Support/skillmod/config.toml` |
| Windows | `%AppData%\skillmod\config.toml` |

```toml
install_mode = "auto" # auto / copy
known_sources = ["github.com/acme/agent-skills"]
```

There is no installation-directory setting: skillmod manages `.agents/skills/`,
and only that, so a project has one place to review. `known_sources` lists the
repositories `init` and `list` look in when recovering provenance; it is a
discovery hint, never a declaration.

The `agents` setting was removed together with the platform adapters. A
configuration that still selects a platform skillmod no longer manages, such as
`claude-code`, fails every command with a message naming the file and the
offending entry, because skillmod would otherwise install somewhere other than
the configuration says. Deleting the entry is the fix; `agents = ["agents"]` is
tolerated silently, because it described the one directory skillmod still
manages. Installations that were placed in a removed directory are no longer
seen: `verify` reports them missing until `sync` recreates them under
`.agents/skills/`, and the old copies can be deleted once the project verifies
clean.

Installation directories are artifacts reconstructed from the lock file.
Projects should add `.agents/skills/` to `.gitignore` and commit only
`SKILL.mod` and `SKILL.lock`.
