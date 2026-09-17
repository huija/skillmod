# Usage scenarios

Use the smallest section relevant to the request. Run `skillmod <command>
--help` when exact flags need confirmation for the installed version.

## Choose a scope

Project scope is the default. It keeps `SKILL.mod` and `SKILL.lock` in the
current directory and installs into `.agents/skills/`.

Use `--global` only for user-wide skills. Global declarations live below the
skillmod store, while installations normally go into the user's agent skill
directories. Project and global declarations are intentionally not merged by
ordinary commands. [storage.md](storage.md) lists the exact locations, the
shared cache, and the configuration file.

Before a project-scoped mutation, confirm that the working directory is the
intended project root. Before a global mutation, state explicitly that it will
affect the user's global agent environment.

## Adopt in order: global first, then the project

Bringing a machine under skillmod management is two steps, and the order
matters: the machine-wide skills exist already, and the project builds on them.

1. Adopt the user-wide skills, which is where a machine's shared skills live:

```sh
skillmod --global init --dry-run --yes
skillmod --global init --yes
skillmod --global list
skillmod --global verify
```

This declares everything in `~/.agents/skills/` in
`~/.agents/skillmod/global/SKILL.mod`, recovering provenance from the previous
installer's recorded sources and from the shared snapshot cache. Review the
dry-run report before writing. A skill the previous installer recorded from a
web discovery endpoint has no Git repository behind it: the record names the web
origin, the entry is reported as `unresolved`, and the skill is kept as a local
declaration. Adding the repository that publishes it to `known_sources` lets a
later `init` adopt it as a Git dependency, because the content is then verified
against that repository. A record the previous installer already marked local is
not a gap at all: it stays a plain local declaration, with no note and no
`unresolved` count.

2. Adopt the project, which is the step that makes the project reproducible on
   other machines:

```sh
cd <project>
skillmod init --dry-run --yes
skillmod init --yes
```

3. Keep both scopes aligned from then on. The two manifests are independent, so
   a command that should affect the user's machine needs `--global`:

```sh
skillmod sync && skillmod verify
skillmod --global sync && skillmod --global verify
```

A skill can legitimately be declared in both scopes — the same repository at the
same version then serves both from one shared snapshot on disk. Declare it in
the project when the project depends on it, and globally when the user wants it
available in every project.

## Bootstrap an existing project

Use `init` when skill directories already exist but are not declared:

```sh
skillmod init --yes --dry-run
skillmod init --yes
```

Review the dry-run report before the write. `init` scans `.agents/skills/` in
the selected scope without changing existing directories, links, or files:
directory links are followed for content verification, while broken links,
unverifiable contents, and invalid directory names are reported and skipped.
Identical entries across targets are merged, with every candidate path kept for
provenance recovery.

Provenance is recovered from matching lock records or verified snapshots,
including monorepo subdirectories and aliases. `init --global` also imports the
upstream installer's `.skill-lock.json`, while project init reads
`skills-lock.json`. A recorded Git source and revision stay authoritative even
when the installed directory has drifted; when an upstream record omits its
revision, init adopts the latest immutable resolution only if the contents match
exactly. Sources that cannot be resolved safely are kept as local baselines so
one unresolved entry does not discard the rest of the import.

Import writes both `SKILL.mod` and `SKILL.lock`. Replacing an existing
declaration requires `--force`, which first backs it up as `SKILL.mod.bak`.

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

The repository is always named; `//` then selects the skill inside it. The
second form writes the skill name in place of its path, so a collection is
addressed with two short words instead of the directory layout. An exact
subdirectory always wins over a name, and a unique name resolves to the skill's
real path, so `source` records
`github.com/acme/agent-skills//skills/review` rather than the shorthand that was
typed. When several skills in the repository answer the same name, skillmod
reports the candidate paths and refuses to guess. A repository that is itself a
single skill needs no `//`, as in the third form.

Omitting a version asks skillmod to resolve the latest immutable tag, with a
pseudo-version fallback for an untagged repository. A branch name is rejected,
and the address must be credential-free. [manifests.md](manifests.md) states the
accepted transports, the version forms, and the rules a hand-edited declaration
must satisfy.

Interactive terminals show one line per candidate: use ↑/← and ↓/→ to move,
Space to toggle a selection, D to show or hide the current description and
command, and Enter to confirm. `--yes` installs every discovered skill, so use
it only when the selection is already clear; for a repository that holds several
skills, interactive selection is preferable unless the user asked for all of
them.

Long-running `get` and `update` runs show a compact animated status block on
interactive terminals. Remote version checks request only HEAD, branch, and tag
refs, `update` deduplicates equivalent repository URLs and checks up to four
distinct repositories concurrently, and a repository snapshot is
integrity-checked once per command and reused across skill discovery.

When two sources publish the same skill name, install the additional entry with
an explicit directory alias:

```sh
skillmod get --alias review-acme github.com/acme/agent-skills//review
```

See [manifests.md](manifests.md) for the alias and installation-directory
uniqueness rules.

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

`list`, `why`, and `verify` classify every installation directory the same way,
so they cannot disagree about a target. They also inspect local entries against
the recorded baseline: an edited local entry is reported as `local-drift`, not
as a plain `local`, and an unreadable directory is `unverifiable` everywhere.

Use `why` for one published name or installation alias:

```sh
skillmod why review
```

`why` reports the source, resolved version, commit, dirhash, installation
directory, and each configured target's status. Prefer it over manually
interpreting links inside `.agents/skills/`.

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

For machine consumption, add `--json` and branch on the command and action
identifiers rather than on translated notes or terminal text.
[automation.md](automation.md) lists the full action vocabulary and the report
shape.

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

Do not replace either command with manual cache deletion. `prune` never deletes
link targets or shared snapshots, and there is no automatic cache eviction; see
[storage.md](storage.md) before deleting a snapshot by hand.

## Share installed skills with other agents

skillmod manages one directory, `.agents/skills/`, so an agent that reads
another convention needs a link there. `share` creates those links and records
the agents it linked each skill to on that skill's entry in `SKILL.mod`, which
is what makes the setup reproducible on another machine — and lets one skill go
to claude while another goes to codex:

```sh
skillmod share --dry-run
skillmod share --skill review --agent claude --agent codex
```

Omitting `--skill` and `--agent` lists the installed skills and the registered
agents for interactive selection. `--dir` takes any path but stays a one-off:
an arbitrary destination is not recorded, because it would not reproduce
elsewhere. Registered agents are discovered with `skillmod share --help`, which
names the directory each one reads.

A destination that already holds identical content or a link to the managed copy
is kept; one that holds different content is a conflict, settled with the same
per-conflict policy `get`, `sync`, and `update` use (`--on-conflict=overwrite`
or `=skip`). `.agents/skills/` is refused as a destination, because linking
there would shadow the directory `verify` reads.

After a declaration exists, the links maintain themselves: `sync` recreates a
link that was replaced or drifted, `verify` reports a missing one under an agent
directory that exists on this machine, and removing a skill takes its mirror
links down with it.

To stop sharing one skill with an agent, use `share --remove`, which unlinks
the agents it names from the skills it is given and drops them from those
skills' agent lists — the list belongs to an entry, so the skills must be
named:

```sh
skillmod share --skill review --remove codex --dry-run
skillmod share --skill review --remove codex
```

Destinations holding foreign content are left alone, and an entry left with no
agents loses the field entirely.

## Installation modes

`auto` prefers links to immutable shared snapshots and falls back to copies when
links are unavailable. `copy` creates independent directories. Override the
configured mode for an intentional migration:

```sh
skillmod sync --relink --install-mode=copy --dry-run
skillmod sync --relink --install-mode=copy
```

Use copies when the user needs to edit an installed skill. Do not edit a shared
snapshot through a linked installation; a link points at content shared with
every other project on the machine. Ordinary `sync` preserves matching
installations and is idempotent, while `--relink` deliberately recreates remote
installations in the selected mode. Both respect local modifications, and
`--yes` never forces a conflicting file to be overwritten. Local entries are
recorded and verified without automatic migration.

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
- Address rejected as unsafe: remove the embedded password or token, the query
  string, and the fragment from the URL, and move an `@version` suffix in
  `source` into the `version` field. Credentials belong in a Git credential
  helper, an SSH agent, or the environment.
- Manifest validation error: the declaration and the lock are validated when
  they are read, so report the exact message rather than rewriting the file by
  guesswork. A `SKILL.lock` without `schemaversion` predates schema versioning
  and is treated as schema version 1; explicit unsupported versions still need
  user intervention.
- Snapshot integrity error: do not suppress it. Preserve diagnostics and move
  to the issue-reporting workflow if the source and local state appear valid.
