---
name: skillmod
description: Install, configure, use, and troubleshoot skillmod, or prepare a well-formed issue for huija/skillmod. Use when a user wants to manage Agent Skills through SKILL.mod and SKILL.lock, bootstrap an existing project, synchronize or inspect installations, update or remove skills, install the skillmod binary, or report a skillmod problem. Do not use for developing the skillmod source code itself.
---

# skillmod

Help users install and operate skillmod safely. Match the user's language and
experience level. Explain the outcome first, then show only the commands needed
for the current task.

## Route the request

- For prerequisite checks, first-time installation, upgrades of the executable,
  or PATH problems, read [references/setup.md](references/setup.md).
- For project/global setup, dependency operations, CI, inspection, updates,
  removal, or troubleshooting, read
  [references/use-cases.md](references/use-cases.md).
- When the user explicitly wants to report a problem, or diagnosis strongly
  indicates a skillmod defect, read
  [references/issue-reporting.md](references/issue-reporting.md).

Read only the references needed for the current request. Installation is not
required before explaining concepts or drafting an issue.

## Core workflow

1. Establish whether the user is operating on project scope or global scope.
   Do not add `--global` unless the user wants user-wide skills.
2. At the start of setup or diagnosis, check that both `git` and `skillmod` are
   available. Do not repeat those checks during ordinary operations when the
   current session has already established them, and do not reinstall a working
   executable merely because installation was mentioned in an earlier step.
3. Inspect existing `SKILL.mod`, `SKILL.lock`, configuration, and installed
   directories before proposing a mutation. Preserve unrelated user changes.
4. Prefer `--dry-run` before `init --force`, `sync --relink`, `remove`, or
   `prune` when the result is not already obvious to the user.
5. Run the smallest command that satisfies the request and verify its result.
   Use `skillmod list`, `skillmod why <name-or-alias>`, or `skillmod verify`
   rather than inferring state from directory names alone.

## Invariants

- Treat `SKILL.mod` as the human-maintained declaration and `SKILL.lock` as
  tool-maintained state. Do not hand-edit `SKILL.lock` unless recovery is the
  explicit task and a backup has been made.
- Do not silently overwrite a locally modified installation. Exit code 3 means
  independent work completed while one or more targets were safely preserved;
  inspect the report instead of treating it as total failure or total success.
- Exit code 2 from `verify` means drift. Exit code 1 is an operational error.
- Branch names are mutable and cannot be locked. Use a semantic-version tag, a
  full 40-character commit SHA, or let skillmod resolve an immutable version.
- Installation directories are generated state. Preserve `SKILL.mod` and
  `SKILL.lock` in version control; do not commit installed skill directories
  unless the project deliberately chooses to do so.
- Shared snapshots may be referenced by links from multiple projects. Never
  delete the cache as a shortcut for removing one dependency.
- Downloading executables, changing PATH, installing packages, authenticating
  GitHub CLI, and creating an issue are external mutations. Confirm the user's
  intent immediately before any such action not already explicitly requested.

## Reporting results

State which scope was affected, which declaration or installation changed,
whether verification passed, and whether anything was preserved because of a
conflict. For JSON workflows, make decisions from stable fields and action
values rather than localized `note` text.
