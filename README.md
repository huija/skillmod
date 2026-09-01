# skillmod — go mod for Agent Skills

English | [简体中文](README_CN.md)

A skill dependency manager for agent projects, inspired by Go modules:
**declare dependencies in the project (`SKILL.mod`) + lock content (`SKILL.lock`) + reconcile with one command (`sync`)**.

How this relates to AGENTS.md: AGENTS.md tells an agent *how to behave*; SKILL.mod declares *which capabilities it needs*.

## The problem

Skills—packaged instructions and scripts—shape agent behavior, but managing them is still stuck in a pre-dependency-manager era: manual copies, Git submodules, and platform-specific marketplaces. As a result, agents behave differently across machines in the same team, failures cannot be traced back to the exact version in use, and tampered content can go unnoticed.

skillmod applies the Go module model to skills: declarations in `SKILL.mod`, content-addressed locks in `SKILL.lock` using dirhash, and idempotent reconciliation through `skillmod sync`. Every machine receives exactly the same set of skills.

## v0.1 scope

- **Seven CLI commands**: `init` / `get` / `sync` / `list` / `update` / `prune` / `verify`
- **Direct Git sources**, analogous to Go's direct mode: a skill is either a tagged repository or a monorepo subdirectory (`<repo>//<subdir>`). Publishing means creating a tag; no server or registry is required.
- **Three version forms**: semantic-version tags, commit SHAs, and pseudo-versions for repositories without tags. Branch names are rejected because mutable references cannot be locked.
- **Shared persistent storage**: readable, immutable full-repository snapshots are stored at `~/.agents/skillmod/pkg/mod/<host>/<owner>/<repo>@<version>`. Bare Git repositories, refs, and resolution metadata live under `pkg/mod/cache`. HTTPS, default-port SSH, and `.git` URL variants share storage. Set `SKILLMOD_HOME` to override the location.
- **Copy-based installation**: byte-for-byte copies with Windows support. `sync` never deletes files automatically; confirmed cleanup is handled by `prune`.
- **Flat 1:1 dependencies**: no transitive dependency resolution and no constraint solver. The `requires` field is reserved.
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
[[skill]]
name = "code-review"
source = "github.com/acme/agent-skills//code-review"
version = "code-review/v1.2.0"
commit = "7f3a9c1e00000000000000000000000000000000"
dirhash = "h1:4wYq0b..."
```

```bash
skillmod init                                          # Scan existing skills and create declarations
skillmod get github.com/anthropics/skills//skills/pdf  # Add a skill; use a pseudo-version when no tag exists
skillmod sync                                          # Reconcile with the lock file, idempotently
skillmod verify                                        # Validate in CI; drift produces a non-zero exit code
```

### Command output language

Command help, summaries, prompts, errors, and human-readable JSON notes follow `SKILLMOD_LANG` when it is explicitly set. Otherwise, skillmod reads the first non-empty system locale in `LC_ALL` → `LC_MESSAGES` → `LANG`. English and Chinese locale values are recognized; missing or unsupported locales fall back to English. Use `SKILLMOD_LANG=zh` to select Chinese explicitly. Locale-style values such as `en_US.UTF-8` and `zh_CN.UTF-8` are also accepted.

```bash
SKILLMOD_LANG=zh skillmod sync
```

Machine-readable JSON field names and action identifiers are not translated.

Translations are maintained centrally as symmetric gettext/POSIX locale catalogs under [`locales/`](locales/): `en_US.po` and `zh_CN.po` contain the same msgid set. The CLI embeds both files, and future website or documentation tooling can select the corresponding `{{locale}}.po` through the same path. `SKILLMOD_LANG=en` and `SKILLMOD_LANG=zh` remain convenient aliases. After changing user-facing source messages, run:

```bash
go generate ./internal/i18n
```

The first request for a given `repo@version` materializes a complete repository snapshot. Adding another skill from the same version later validates and installs it directly from the local subdirectory, without invoking Git or accessing the remote. An explicit `@commit` can likewise reuse an existing snapshot of that repository commit. Omitting the version to request latest, or running `skillmod update`, retains online refresh semantics.

```text
~/.agents/skillmod/pkg/mod/
├── github.com/anthropics/skills@v0.0.0-.../  # Directly browsable full-repository snapshot
└── cache/
    ├── vcs/                                  # Bare Git repositories, keyed internally by hash
    ├── download/                             # Repository versions, refs, and resolution metadata
    └── locks/
```

Store v2 does not automatically migrate or remove the old root-level `cache/` directory or hash-named subtree snapshots. The new layout is materialized on first use. Old directories can be removed manually after confirming that older binaries will no longer be used.

Skills are installed into the project's `.agents/skills/` directory by default. To also install them for Claude Code, configure:

```toml
# ~/.config/skillmod/config.toml
agents = ["agents", "claude-code"]
```

Installation directories are artifacts reconstructed from the lock file. Projects should add `.agents/skills/` to `.gitignore` and, when the Claude adapter is enabled, also ignore `.claude/skills/`. Commit only `SKILL.mod` and `SKILL.lock`.

## Non-goals for v0.1

- No website or registry service; that is reserved for a future skill-hub project.
- No transitive dependencies or version-constraint solving.
- No telemetry or skill-content security scanning; those belong to a later product line.

## Documentation

The design documents are currently written in Chinese.

| Document | Contents |
|---|---|
| [docs/prd-v0.1.md](docs/prd-v0.1.md) | Final product specification: commands, fields, error cases, and acceptance criteria |
| [docs/dev-design.md](docs/dev-design.md) | Engineering design: technology choices, modules, flows, tests, and milestones |
| [docs/design.md](docs/design.md) | Flow diagrams, implementation notes, and design-decision history |
| [docs/prd-feedback.md](docs/prd-feedback.md) | Archive of three review rounds |

## Roadmap beyond v0.1

- v0.2 candidates: a meta-skill that lets an agent fetch skills through an approval gate, and a SessionStart hook that runs `sync` automatically.
- Supply-chain security scanning, enterprise policy gates, and a central registry once the ecosystem matures.

> Supply-chain security principle for the meta-skill era: **agents may initiate changes, but people must approve them**.
> The hard gate is the host's Bash permission prompt; the soft gate is the meta-skill's policy boundary. Security comes from approval gates, not from hiding tools.

## License

skillmod is released under the [MIT License](LICENSE).
