# Contributing to skillmod

Thanks for taking a look. This file covers the parts of the workflow that only
matter when you change skillmod itself. For installing and operating the CLI,
see [README.md](README.md) and the agent skill under
[skills/skillmod](skills/skillmod/SKILL.md).

## Before you start

- Go 1.26.1 or later, and `git` on `PATH`.
- Run `make check` before opening a pull request; CI enforces the same gate.

## The gate

```sh
make check
```

`make check` runs:

| Target | What it enforces |
| --- | --- |
| `format-check` | `gofmt` is clean |
| `tidy-check` | `go mod tidy -diff` reports no change |
| `coverage` | Total statement coverage is at least `COVERAGE_MIN` (75%) |
| `vet` | `go vet ./...` |
| `lint` | A pinned golangci-lint, version in `.golangci-version` |

CI additionally runs `govulncheck` and a cross-platform test matrix
(`test.yml`, `cross.yml`), so keep tests working on macOS and Windows: path
separators, symlink privileges, and file modes differ. `make test` runs the
suite alone, and `make build` writes a development binary with the current Git
revision embedded.

## User-facing text

Every user-facing string lives in a gettext/POSIX catalog under
[`locales/`](locales/), not in Go source. Code passes a short message key:

```go
i18n.Text("cli.get.long")
i18n.Format("engine.sync.conflicting_targets_kept", skipped)
```

`en_US.po` holds the English wording and is the source of truth. Key naming
follows `internal/i18n/msgkey`, documented in
[`locales/README.md`](locales/README.md). After adding or rewording text,
regenerate the catalogs:

```sh
make generate
```

Generation fails when a key has no English or Chinese text; fill the reported
`msgstr` values in `locales/` and run it again. Keys the source no longer uses
are pruned, and a catalog whose bytes do not match what the source produces
fails with `is stale; run go generate ./internal/i18n`.

Machine-readable JSON field names and action identifiers are not translated and
must not move into a catalog.

## Documentation

The README pair is the project's front door; the operational detail lives in
[`skills/skillmod/references/`](skills/skillmod/references/), which is also the
skill published to agents. When behavior changes, update the reference file that
owns it rather than growing the README, and keep the English and Chinese READMEs
structurally parallel: `docs_test.go` compares their heading outline and checks
that both still link to the reference files.

Documentation that automation depends on is asserted by tests, so a change to
the JSON action vocabulary fails the build until the documented table in
`skills/skillmod/references/automation.md` matches `internal/engine`.

## Commits

Keep a change and its documentation and tests together, and describe why the
behavior changed. A message that only restates the diff is harder to review than
one that names the defect being fixed.
