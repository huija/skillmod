# Manifest files and addresses

`SKILL.mod` is the declaration people maintain and commit. `SKILL.lock` is the
tool-maintained record of what is installed, and must not be edited by hand.
Every command reads and validates both files, so a hand-edited manifest that
breaks a rule is reported instead of being halfway applied.

## Declaration rules

- A remote entry requires `source`. An entry marked `local = true` must not
  carry `source` or `version`.
- `source` is `<repository>[//<subdirectory>]` and nothing more. A version
  belongs in the `version` field; an `@version` suffix inside `source` is
  rejected.
- The HTTPS transport is implied, so it is never recorded: both files store
  `host/owner/repo`. Any other transport is recorded explicitly because it
  changes how the repository is fetched. A file that still spells out
  `https://` works unchanged and is rewritten to the shorter form the next time
  skillmod writes it.
- A bare `owner/repo` path is completed with `github.com`, the host nearly
  every skill repository lives on, so `openai/skills` and
  `github.com/openai/skills` are the same repository. A first segment that
  already carries a dot is a host, and so is one that carries a port. A first
  segment that is empty, `.`, or `..` reads as a filesystem path — the same for
  a Windows drive letter — and none of those take the `github.com` default.
- `version` is optional. An entry without one tracks the latest immutable
  resolution, which `SKILL.lock` pins. An entry with one is pinned to that tag
  or commit until `update` runs.
- Both files declare `schemaversion = 1`, and every locked `dirhash` must be a
  single `h1:` SHA-256 digest.
- A `SKILL.lock` written before schema versioning has no `schemaversion`. It is
  read as schema version 1 and normalized the next time skillmod writes the
  manifest state. Explicit zero, negative, and unknown versions are rejected.

## Versions

Three immutable forms are accepted. A branch name is rejected, because a mutable
reference cannot be locked.

| Form | Example | Use when |
| --- | --- | --- |
| Semantic-version tag | `v1.2.0`, `code-review/v1.2.0` | The publisher tags releases |
| Commit SHA | a full 40-character SHA | You need one exact revision |
| Pseudo-version | `v1.0.96-0.20260624023612-49f948faa925` | A commit pin; the base is the highest tag when the repository has tags, plain `v0.0.0` when it has none |

`SKILL.lock` additionally records the resolved commit and the content hash, so a
pseudo-version remains resolvable on a machine that has never seen the tag.
Locks written before base tags existed use the plain
`v0.0.0-<timestamp>-<hash>` shape and stay valid; the next `update` migrates
them to the repository's latest tag.

## Repository addresses

```text
<repository>[//<subdirectory-or-skill-name>][@<version>]
```

The `@<version>` suffix belongs to the command line only; it is never recorded
inside `source`.

Addresses must be credential-free. Embedded user information, a query string, a
fragment, and control characters are refused before the address is used or
persisted, so a secret cannot reach `SKILL.mod`, `SKILL.lock`, the bare
repository's configuration, or a diagnostic. The supported transports are
`https`, `http`, `ssh`, and `file`, plus the scp-like `git@host:path` form;
`git://` is refused as unauthenticated and unencrypted. An SSH username is kept
because it identifies the transport, a password is not, and secrets belong in a
Git credential helper, an SSH agent, or the environment.

When `//<subdirectory>` is a single segment, skillmod first tries an exact
subdirectory of the repository root, then falls back to a unique skill name
found anywhere in the repository. Ambiguous names require the full path.
Omitting `//<subdirectory>` makes `get` discover every `SKILL.md` the
repository publishes, wherever that is: the repository root itself, a `skills/`
collection at any depth, an agent collection such as `.agents/skills/` or
`.claude/skills/`, and the root-level directories. Those four are always
scanned, so a repository that publishes a collection can still leave a skill in
another root directory and have it installed.

A repository sometimes publishes the same skill under more than one of those
roots — `skills/` alongside `.openclaw/skills/` is the common case, because an
agent directory is a mirror maintained for one agent. Both would install into
one directory under one name, so `get` keeps a single copy, from the root the
repository's own layout comes from first:

| Kept copy | Discarded copies |
| --- | --- |
| `skills/<name>` | a root-level `<name>/`, and any agent collection |
| root-level `<name>/` | any agent collection |
| an agent collection | nothing, for the same skill — no agent directory outranks another |

Two agents holding the same skill, with no ranking between them, are not
collapsed: `get` reports both paths and asks, the way it does for any other
ambiguous name. Within one collection, two directories sharing a skill name are
two skills, not a duplicate, and are reported the same way.

An exact subdirectory always wins over discovery, so a repository only has to
look conventional to people who never type a path.

## The per-skill `agents` list

`share` records the agents it linked a skill to on that skill's `[[skill]]`
entry:

```toml
[[skill]]
name = "review"
agents = ["claude", "codex"]
```

The declaration is per skill: one skill can go to claude while another goes to
codex, and `sync` recreates the links each entry describes on a new machine
while `verify` reports a missing or drifted one. An agent is named by one
directory segment and read from `.<name>/skills` under the scope root, so the
list is open — `workbuddy` is as valid as `claude` — and only the name is
recorded, never the directory: a stored path would not reproduce elsewhere,
while a name is enough to rebuild the link. A name that is not one portable
directory segment is rejected when the manifest is read rather than skipped.
Names use letters, digits, `.`, `_`, and `-`; a leading dot and letter case are
normalized, so `.Claude` and `claude` are the same destination and cannot appear
together in one list.

An entry without `agents`, or with an empty one, means the skill stays only
in the managed directory. `remove --agent <name>` and
`share --remove --agent <name>` edit the selected entries' lists: name the
skills, use `--all` for all matching entries, or pick them interactively. An
entry left with no agents loses the field entirely. The list is sorted on save,
and one agent cannot appear in it under two spellings.

## Portability

Installation mode, absolute cache paths, and scope are deliberately excluded
from both files, so identical declarations and versions produce identical
manifests on every system and in every installation mode.

## Duplicate published names and aliases

Two sources may publish the same skill name. Install the additional entry with
an explicit directory alias:

```sh
skillmod get --alias review-acme github.com/acme/agent-skills//review
```

Both declarations and both lock records are retained. A lock entry omits `dir`
when the installation directory equals `name`, and records it only for an
aliased entry.

Aliases must be portable names, and all installation directories must stay
distinct after Unicode normalization and case folding, so the same project
behaves identically on Linux, macOS, and Windows. Re-getting the same source
with a different alias keeps the old directory and reports that
`skillmod prune` can remove it.
