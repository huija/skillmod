# skillmod localization catalogs

This directory is the shared source of truth for translatable skillmod copy.
Catalogs use the flat `{{locale}}.po` convention so every consumer selects a
language through the same path and the same parsing flow.

- `en_US.po` contains United States English translations and is the source of
  truth for the English wording.
- `zh_CN.po` contains Simplified Chinese translations.
- Both files have exactly the same `msgid` set: message keys, not sentences.
- The CLI embeds both PO catalogs at build time.
- Website and documentation tooling may consume the same locale files.

The CLI honors an explicit `SKILLMOD_LANG` first. Otherwise it checks
`LC_ALL`, `LC_MESSAGES`, and `LANG` in that order. Missing or unsupported
locales fall back to English.

## Message keys

`msgid` is a short identifier; the wording lives in `msgstr`. Application code
calls `i18n.Text("cli.get.long")` or `i18n.Format("cli.get.long", args...)`, and
never carries the user-facing sentence itself. A key missing from the selected
locale falls back to `en_US`, then to the key itself, so an incomplete catalog
degrades to English rather than to blank output.

Keys follow a fixed rule, enforced by `go generate` through
`internal/i18n/msgkey`:

```
<package>[.<file>].<subject>          cli.get.long
                                      engine.verify.missing
                                      store.cache_identity_mismatch
```

- Two or three lowercase `snake_case` segments joined by dots.
- The first segment is the package directory (`cli`, `engine`, `store`, …).
- The second segment is the file stem, kept only when it differs from the
  package name (`cli/get.go` → `cli.get`, `engine/engine.go` → `engine`).
- The subject is derived from the message's role where the AST provides one —
  command help uses `use`, `short`, `long`, and flag usage uses `flag_<name>`
  (`cli.root.flag_json`) — and otherwise from the English wording.
- Each segment is at most 32 characters and the whole key at most 56.

Renaming a key or changing its wording invalidates the existing translation for
that key, so prefer editing `msgstr` alone when only the wording needs work.

## Adding or changing a message

1. Use the key in the source: `i18n.Text("cli.get.short")`.
2. Run the generator:

   ```bash
   go generate ./internal/i18n
   ```

3. It reports keys with an empty `msgstr` and exits non-zero, after writing the
   catalog skeletons. Fill the English text in `en_US.po` and the Chinese text in
   `zh_CN.po`, translated strings must not be written beside call sites.
4. Run the generator again. It prunes keys the source no longer uses, keeps both
   catalogs in sync, and fails rather than shipping an untranslated message.

`go run ./internal/i18n/cmd/cataloggen -root . -list` prints the key inventory
with the English and Chinese fill state, which is the quickest way to review a
catalog change.

## Conventions

- Placeholders use the `%s`-family verbs of Go's `fmt`. The verb sequence of a
  translation must match the English text exactly, in the same order.
- Fragments that a call site concatenates with a value keep their separating
  space in the English text (for example `"missing: "`). A translation may use a
  full-width character instead, but must not glue a word onto the value.
- Values longer than 76 columns are folded over PO continuation lines; never
  wrap them by hand.
- Machine-readable JSON field names and action identifiers are not translated.
