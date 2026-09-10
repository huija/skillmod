# Usage scenarios

Use the smallest section relevant to the request. Run `skillmod <command>
--help` when exact flags need confirmation for the installed version.

## Choose a scope

Project scope is the default. It keeps `SKILL.mod` and `SKILL.lock` in the
current directory and installs into project adapter directories such as
`.agents/skills/`.

Use `--global` only for user-wide skills. Global declarations live below the
skillmod store, while installations normally go into the user's agent skill
directories. Project and global declarations are intentionally not merged by
ordinary commands.

Before a project-scoped mutation, confirm that the working directory is the
intended project root. Before a global mutation, state explicitly that it will
affect the user's global agent environment.

## Bootstrap an existing project

Use `init` when skill directories already exist but are not declared:

```sh
skillmod init --yes --dry-run
skillmod init --yes
```

Review the dry-run report before the write. `init` preserves installed files,
attempts to recover verified provenance, and keeps unknown sources as local
baselines. A project with an existing `SKILL.mod` requires `--force`; preview
that operation first because the declaration will be rebuilt and backed up.

Use global scope only when requested:

```sh
skillmod --global init --yes --dry-run
skillmod --global init --yes
```

## Add a remote skill

Addresses have this form:

```text
<repository>[//<subdirectory-or-skill-name>][@<version>]
```

Examples:

```sh
skillmod get github.com/acme/agent-skills//skills/review@v1.2.0
skillmod get github.com/acme/agent-skills//review
skillmod get github.com/acme/single-skill
```

Omitting a version asks skillmod to resolve the latest immutable tag, with a
pseudo-version fallback for an untagged repository. A branch name is rejected.

When two sources publish the same skill name, install the additional entry with
an explicit directory alias:

```sh
skillmod get --alias review-acme github.com/acme/agent-skills//review
```

Use `--yes` only when the requested selection is already clear. For a repository
that contains several skills, interactive selection is preferable unless the
user asked to install all discovered skills.

## Reproduce a declared environment

After cloning a project containing `SKILL.mod` and `SKILL.lock`, run:

```sh
skillmod sync
skillmod verify
```

`sync` installs or aligns declared content and is idempotent. It does not
silently overwrite local modifications. If it returns exit code 3, inspect the
per-target results: some work completed, while conflicting targets were kept.

Use `sync --relink` only to deliberately recreate matching remote installations
using the configured install mode:

```sh
skillmod sync --relink --dry-run
skillmod sync --relink
```

## Inspect state and provenance

Use `list` for the complete declaration overview:

```sh
skillmod list
```

Use `why` for one published name or installation alias:

```sh
skillmod why review
```

`why` reports the source, resolved version, commit, dirhash, installation
directory, and each configured target's status. Prefer it over manually
interpreting links inside `.agents/skills/` or `.claude/skills/`.

## Verify in CI

Use:

```sh
skillmod verify
```

Interpret exits as:

| Exit | Meaning |
| --- | --- |
| `0` | All checked entries are consistent |
| `1` | Operational or input error |
| `2` | Drift detected |
| `3` | Partial completion for commands that safely preserved targets |

For machine consumption, add `--json`. Parse action fields and structured
target results; do not branch on translated notes or terminal text.

## Update dependencies

Inspect current state first, then update all remote entries or selected names:

```sh
skillmod list
skillmod update
skillmod update review
```

A published name can represent more than one aliased declaration; an
installation alias selects its corresponding entry. Review the output whenever
the selector may not be unique.

skillmod refuses an accidental semantic-version downgrade when newer tags have
disappeared. Use `--allow-downgrade` only after confirming that the remote tag
removal was intentional:

```sh
skillmod update review --allow-downgrade
```

## Remove a dependency

Use `remove` when the declaration itself should go away:

```sh
skillmod remove review --dry-run
skillmod remove review
```

Clean managed installations are removed transactionally. Locally modified or
unverifiable directories are preserved and reported as partial completion.

Use `prune` when declarations were already edited and stale installations or
lock records remain:

```sh
skillmod prune --dry-run
skillmod prune
```

Do not replace either command with manual cache deletion.

## Installation modes

`auto` prefers links to immutable shared snapshots and falls back to copies when
links are unavailable. `copy` creates independent directories. Override the
configured mode for an intentional migration:

```sh
skillmod sync --relink --install-mode=copy --dry-run
skillmod sync --relink --install-mode=copy
```

Use copies when the user needs to edit an installed skill. Do not edit a shared
snapshot through a linked installation.

## Diagnose common failures

1. Confirm the intended scope and working directory.
2. Run `git --version` and `skillmod --version`.
3. Run `skillmod list` and, for a specific entry, `skillmod why <selector>`.
4. Run `skillmod verify` and record its exit code.
5. For a planned mutation, rerun with `--dry-run` when supported.

Common interpretations:

- `SKILL.mod not found`: initialize the intended scope or change to the correct
  project root.
- Drift or a preserved conflict: inspect local modifications; do not overwrite
  them without the user's decision.
- Network unavailable: an already materialized immutable snapshot may work
  offline, but latest-version resolution requires remote references.
- Branch rejected: select a tag, a full commit SHA, or omit the version for
  immutable resolution.
- Snapshot integrity error: do not suppress it. Preserve diagnostics and move
  to the issue-reporting workflow if the source and local state appear valid.
