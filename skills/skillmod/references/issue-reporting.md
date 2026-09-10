# Reporting a skillmod issue

Use this workflow only when the user asks to report an issue or agrees after a
diagnosis suggests a skillmod defect. Creating a GitHub issue is an external,
public mutation: always show the destination and final draft, then obtain
explicit authorization immediately before submission.

Repository:

```text
huija/skillmod
```

## 1. Check GitHub CLI

Run:

```sh
gh --version
gh auth status
```

If `gh` is missing, offer the current official installation instructions at
https://cli.github.com/manual/installation. Common options include Homebrew on
macOS and WinGet on Windows, while Linux repository setup varies by
distribution and should be taken from the official page.

Do not install or authenticate `gh` without the user's consent. Authentication
is normally initiated with:

```sh
gh auth login
```

If the user does not want GitHub CLI, prepare the same issue draft and direct
them to https://github.com/huija/skillmod/issues/new instead.

## 2. Confirm the problem is actionable

Reproduce only with commands that are safe for the current state. Prefer
`--dry-run`, `list`, `why`, and `verify`. Do not destroy the failing state before
collecting diagnostics.

Capture, when available:

```sh
skillmod --version
git --version
gh --version
```

Also record:

- Operating system and architecture.
- Installation method and executable path.
- Project or global scope.
- The exact skillmod command and exit code.
- Expected behavior and actual behavior.
- Minimal reproduction steps.
- Whether the behavior is consistent or intermittent.

Include the smallest relevant excerpts of `SKILL.mod`, `SKILL.lock`, and command
output. Before sharing, redact access tokens, credentials, private repository
URLs, usernames, home-directory paths, internal hostnames, and unrelated skill
declarations. Never upload an entire private manifest or Git configuration by
default.

When reproducible in English, setting `SKILLMOD_LANG=en` can make diagnostics
easier for maintainers to search, but do not rerun a mutating command solely to
translate its output.

## 3. Search for duplicates

Derive two or three concise keywords from the symptom and search open and
closed issues:

```sh
gh issue list --repo huija/skillmod --state all --search "<keywords>" --limit 20
```

Open likely matches with `gh issue view`. If an existing issue covers the same
root problem, summarize it and ask whether the user wants to add new diagnostic
information there. Do not create a duplicate merely because versions or paths
differ.

## 4. Draft the report

Use this structure:

````markdown
## Summary

One or two sentences describing the defect and impact.

## Environment

- skillmod version:
- Git version:
- OS / architecture:
- Installation method:
- Scope: project or global

## Steps to reproduce

1.
2.
3.

## Expected behavior


## Actual behavior


## Command output

```text
Sanitized output and exit code
```

## Additional context

Minimal sanitized manifest excerpts or other relevant details.
````

Choose a factual title in the form `<command>: <observable problem>`. Do not
diagnose an unproven root cause in the title. Do not request labels that have
not been confirmed to exist.

## 5. Submit only after approval

Write the approved body to a temporary file to preserve formatting and avoid
shell interpolation, then run:

```sh
gh issue create --repo huija/skillmod --title "<title>" --body-file <body-file>
```

Return the created issue URL. If submission fails, report the error and retain
the sanitized draft; do not repeatedly retry authentication or issue creation
without the user's direction.
