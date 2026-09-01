# skillmod localization catalogs

This directory is the shared source of truth for translatable skillmod copy.
Catalogs use the flat `{{locale}}.po` convention so every consumer selects a
language through the same path and the same parsing flow.

- `en_US.po` contains United States English translations.
- `zh_CN.po` contains Simplified Chinese translations.
- Both files have exactly the same `msgid` set.
- The CLI embeds both PO catalogs at build time.
- Website and documentation tooling may consume the same locale files.

The CLI honors an explicit `SKILLMOD_LANG` first. Otherwise it checks
`LC_ALL`, `LC_MESSAGES`, and `LANG` in that order. Missing or unsupported
locales fall back to English.

Application code should call `i18n.Text("English source message")` or
`i18n.Format("English source message", args...)`; translated strings must not
be written beside call sites.

After adding or changing source messages, regenerate the catalogs:

```bash
go generate ./internal/i18n
```

The generator preserves existing translations, updates source references, and
fails when a new message has no Chinese translation. Fill the empty `msgstr`
and run it again.
