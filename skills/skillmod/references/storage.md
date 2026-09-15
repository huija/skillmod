# Storage and configuration

skillmod keeps one shared content store for every scope, so projects, global
skills, and CI runs reuse the same immutable snapshots instead of downloading
the same repository again.

## Where a declaration lives, and where it installs

| Scope | Declaration and lock | Default installation directory |
| --- | --- | --- |
| Project (default) | `SKILL.mod` and `SKILL.lock` in the current directory | `.agents/skills/` in the current directory |
| Global (`--global`) | `$SKILLMOD_HOME/global/`, default `~/.agents/skillmod/global/` | `~/.agents/skills/` |

With the Claude Code adapter enabled, installations also go to
`.claude/skills/` in the same scope. All commands accept `--global`; ordinary
commands do not merge the project and global manifests.

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
agents = ["agents", "claude-code"]
install_mode = "auto" # auto / copy
```

Installation directories are artifacts reconstructed from the lock file.
Projects should add `.agents/skills/` to `.gitignore`, and `.claude/skills/`
when the Claude adapter is enabled, and commit only `SKILL.mod` and
`SKILL.lock`.
