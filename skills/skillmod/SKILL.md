---
name: skillmod
description: Install, configure, use, and troubleshoot skillmod, or prepare a well-formed issue for huija/skillmod. Use when a user wants to manage Agent Skills through SKILL.mod and SKILL.lock, bootstrap an existing project or machine, synchronize or inspect installations, update or remove skills, install or upgrade the skillmod binary, or report a skillmod problem. Do not use for developing the skillmod source code itself.
---

# skillmod

skillmod manages Agent Skills the way Go modules manage dependencies. The
project declares what it needs in `SKILL.mod`, skillmod pins the exact
content in `SKILL.lock`, and `skillmod sync` makes every machine agree.

Reach for it when skills have to be the same on more than one machine, when
CI has to prove the installed skills are the declared ones, or when several
agents on one machine should share one installed set.

Help the user install and run skillmod safely. Answer in their language,
match their experience, and lead with the outcome before the commands.

## Where to look

| The request is about | Read |
| --- | --- |
| Installing or upgrading the executable, prerequisites, PATH problems | [references/setup.md](references/setup.md) |
| Setting up a project or the machine, adding or removing skills, CI, checking state | [references/use-cases.md](references/use-cases.md) |
| What belongs in `SKILL.mod`, address and version forms, aliases | [references/manifests.md](references/manifests.md) |
| Cache layout, install modes, the config file, where things live | [references/storage.md](references/storage.md) |
| Exit codes, `--json`, what a script should branch on | [references/automation.md](references/automation.md) |
| Reporting a bug | [references/issue-reporting.md](references/issue-reporting.md) |

Read only what the current request needs. Explaining a concept or drafting an
issue does not require an installation.

## How to work

1. Settle the scope first. Project is the default; add `--global` only when
   the user means the skills for the whole machine. The two scopes have
   separate manifests and never merge.
2. Before diagnosing, check that `git` and `skillmod` both run. After that,
   don't re-check them or reinstall a working executable just because
   installation came up earlier in the conversation.
3. Look before you touch: read `SKILL.mod`, `SKILL.lock`, the config file,
   and the installed directories, and leave anything unrelated alone.
4. Run `--dry-run` first when the result isn't obvious — `init --force`,
   `sync --relink`, `sync --adopt`, `remove`, `prune`.
5. Then run the smallest command that does the job and check what it did.
   Use `list`, `why <name>`, or `verify` rather than guessing from
   directory names.

## Rules worth keeping

- `SKILL.mod` is for people and goes in version control. `SKILL.lock` is
  skillmod's own; don't hand-edit it unless recovery is the task and a
  backup exists.
- Never overwrite a locally edited installation to make a report look
  clean. Exit code 3 means part of the work was done and some targets were
  kept on purpose — read the report, don't retry blindly. Exit code 2 means
  drift. Exit code 1 means something went wrong.
- Branch names can't be locked. Use a tag, a full 40-character commit, or
  let skillmod pick an immutable version.
- Keep addresses credential-free: no passwords or tokens in the URL, no
  query strings, no fragments. `source` records `host/owner/repo`, and the
  host defaults to `github.com`, so `owner/repo` is enough.
- Installed skill directories are build output. Don't commit them unless the
  project decided to.
- One installed copy can be linked from several projects. Never "fix" a
  removal by deleting the cache.
- Downloading a binary, changing `PATH`, installing packages, or filing an
  issue reaches outside the machine — confirm right before doing it, unless
  the user already asked for it.

## Reporting back

Say which scope was affected, what changed, whether `verify` passed, and
whether anything was kept because of a conflict. For `--json` runs, decide
from the stable fields and action values, never from the translated `note`
text.
