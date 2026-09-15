# Exit codes and JSON reports

Use these when a script, a CI job, or an agent needs to decide something from a
skillmod run instead of a person reading its output.

## Exit codes

| Exit | Meaning |
| --- | --- |
| `0` | All checked entries are consistent |
| `1` | Operational or input error |
| `2` | Drift detected |
| `3` | Partial completion: independent work finished, and one or more targets were safely preserved |

`get`, `sync`, `update`, and `remove` return `3` when they keep a target instead
of overwriting it. That is not total failure and not total success; read the
report to see what was preserved and why.

An in-progress command prints its human summary as usual. Add `--json` to also
emit a structured report on stdout.

## JSON reports

A report separates the command that ran from the outcome of each declaration and
of each installation directory.

| Field | Meaning | `action` values |
| --- | --- | --- |
| `action` | The command that produced the report | `get`, `init`, `list`, `prune`, `remove`, `share`, `sync`, `update`, `upgrade`, `verify`, `why` |
| `entries[].action` | The aggregate outcome of one declaration | `conflict`, `drift`, `install`, `installed`, `keep`, `local`, `local-drift`, `matched`, `missing`, `partial`, `prune`, `remove`, `skip`, `stale`, `unlocked`, `unresolved`, `unverifiable`, `update` |
| `entries[].targetResults[].action` | The outcome of one installation directory | `drift`, `install`, `installed`, `keep`, `missing`, `remove`, `skip`, `unlocked`, `unverifiable` |

Per-directory facts appear only in `targetResults`, which is where a conflicting
or unreadable directory stays visible while the rest of the command succeeds.

`upgrade` uses the same shape with `action: "upgrade"` and one entry named
`skillmod`, so a job can watch for a release without downloading it. The entry
action is `keep` when the running version is the requested one and `update` when
a newer release exists, with `entries[0].version` naming that release. The
executable's own `targetResults[0].action` says what happened to the file:
`installed` means it was replaced, `install` means a dry run verified it, and
`keep` means it was left alone. The report is written even when the upgrade
fails, so `--json` always decodes one document.

Add `--json` to `list`, `why`, and `verify` for the report shape shown below:

```json
{
  "action": "verify",
  "entries": [
    {
      "name": "gh-fix-ci",
      "source": "github.com/openai/skills//skills/.curated/gh-fix-ci",
      "action": "drift",
      "version": "v0.0.0-20260624023612-49f948faa925",
      "requestedVersion": "v0.0.0-20260624023612-49f948faa925",
      "commit": "49f948faa9258a0c61caceaf225e179651397431",
      "dirhash": "h1:kiGlVBeTCF8Q9f0rPATnDn1jBn/obT3TTTL8D8gdCbM=",
      "directory": "gh-fix-ci",
      "note": "contents do not match the lock",
      "targetResults": [
        {
          "path": "/project/.agents/skills/gh-fix-ci",
          "action": "drift"
        }
      ]
    }
  ]
}
```

Inspection reports (`list`, `why`, and `verify`) also carry `requestedVersion`
for remote entries: it is the exact `SKILL.mod` value, while `version` is the
installed version from `SKILL.lock`. An empty `requestedVersion` means the
declaration tracks latest, which the lock pins.

Branch on the command and action identifiers, which are stable and never
translated. An entry's `note` is translated prose for people, and an
`entry.action` aggregates its target results into the most actionable status, so
prefer `targetResults` when one directory matters more than the declaration as a
whole.
