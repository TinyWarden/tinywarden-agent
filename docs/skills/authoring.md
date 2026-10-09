# Write a TinyWarden skill

A standalone Python SDK v1 skill can be uploaded, configured and delivered without
changing, rebuilding or restarting the app or agent. Verified target: Debian 13
amd64, Python 3.13 plus its standard library. This guide does not install a service.

The zero-dependency author tool is `sdk/python/skill_tool.py`. Use a reviewed SDK
checkout and keep your own package in another directory. No app checkout, Go build
or database is needed. The SDK and `skills/examples/memory-pressure` can also be
copied together into a standalone source kit, without any compiled core source.

## Create and edit

From the SDK checkout:

```sh
python3.13 -I -S -B sdk/python/skill_tool.py init ../acme-memory \
  --id acme/memory-pressure --publisher 'Acme'
python3.13 -I -S -B sdk/python/skill_tool.py validate ../acme-memory
```

The destination must not exist. Choose a stable lowercase `namespace/name`;
`tinywarden` and the four official aliases are reserved. The working starter reads
`/proc/meminfo`, calculates memory use from `MemTotal - MemAvailable`, and applies
warning/critical thresholds. It never installs itself or changes a host. Preserve
its Apache-2.0 license when deriving code from it.

| File | Responsibility |
| --- | --- |
| `skill.json` | Identity/version, compatibility, defaults, field labels, requested host access and limits. |
| `skill.py` | Validation, collection, reduction and evaluation. |
| `settings.schema.json` | Closed scalar settings and bounds. |
| `observation.schema.json` | Bounded normalized collection output. |
| `state.schema.json` | Bounded persistent state returned by reduction. |
| `messages/en.json` | Plain English labels/reasons/help with typed named parameters. |
| `display.json` (optional) | Typed app-owned widgets and bounded history metrics; no UI code. |
| `tests.json` | Captured observations and expected errors/state/assessments. |
| `README.md`, `LICENSE` | Meaning, limitations, access requirements and redistribution terms. |

Update the catalog name/description and README when adapting the starter. Package
text belongs in the catalog; reasons and facts use catalog keys, not HTML or custom
UI. Declare only needed capabilities. No shell hooks, native binaries, pip downloads
or requirements/lock files belong in the package. Pure Python helpers and bounded
JSON fixtures are supported. See the [SDK reference](sdk-v1.md).

`validate` checks paths, metadata, schema subset, catalog, defaults, function
declarations and content identity. It **does not execute code**, run cross-field
validation or prove correctness/safety. It accepts directories and ZIPs. Use it
for your own local authoring material; uploaded untrusted archives undergo separate
bounded admission on the server.

## Test captured observations

The starter has healthy, warning/critical boundary, override and invalid-threshold
cases. Tests execute the actual `validate_settings`, `reduce` and `evaluate` functions
inside the unchanged isolated SDK. Fixtures do not run collection or simulate a
live host. No host-import or unsandboxed fallback exists.

Execution needs Python 3.13, bubblewrap, libseccomp2, unprivileged namespaces and
delegated cgroup v2 CPU/memory/PID controllers. A native systemd user session can
provide delegation without creating an agent service:

```sh
systemd-run --user --wait --pipe --collect \
  -p 'Delegate=cpu memory pids' -p DelegateSubgroup=supervisor \
  -p NoNewPrivileges=yes \
  /usr/bin/python3.13 -I -S -B "$PWD/sdk/python/skill_tool.py" \
  test "$PWD/../acme-memory"
```

Use absolute paths in transient units. If these facilities are unavailable, ask
your administrator to prepare a supported test machine. `runtime:runtime_unavailable`
is a failed prerequisite, not a skipped pass. This does not alter a production
service, agent credentials or database. A user-unit `PrivateTmp` namespace does
not substitute for SDK isolation.

Commands emit JSON and return nonzero on failure. Tests list cases and results;
runtime failure stops the sequence. Use `--cases /absolute/cases.json` or the
package's `tests.json`, including when testing a ZIP:

```json
{
  "format": 1,
  "cases": [{
    "name": "Low available memory warns",
    "observation": {
      "total_bytes": "1000000", "available_bytes": "150000", "used_percent": 85
    },
    "expect": {"state": {}, "statuses": ["warning"]}
  }]
}
```

There are 1–32 uniquely named cases. Optional `settings` defaults to manifest
defaults. Other inputs are `observation`, `previous_state` (null if omitted), and
UTC millisecond `captured_at`, `received_at`, `now`, `evidence_expires_at`. The
default fixed clock is `1800000000000`, with ten minutes of evidence validity.
Reduction produces state; the fixture cannot bypass it with supplied state.

`expect` supports exact `errors`, exact `state`, ordered `statuses` and/or exact
`timeline`. Successful validation requires an observation and at least one
state/timeline/status assertion. Omitted errors means `[]`. A nonempty expected
error list tests only validation. Schema-invalid values fail setup rather than
counting as package validation errors. The reference defines exact output shapes.

## Try real collection

Review manifest capabilities first. `collect` explicitly approves **all declared
access on this local machine**, using the same delegated isolation. It has no
agent credential and makes no connection to the app. Use a non-root account on a
test machine with ordinary OS read permissions:

```sh
systemd-run --user --wait --pipe --collect \
  -p 'Delegate=cpu memory pids' -p DelegateSubgroup=supervisor \
  -p NoNewPrivileges=yes \
  /usr/bin/python3.13 -I -S -B "$PWD/sdk/python/skill_tool.py" \
  collect "$PWD/../acme-memory" --approve-declared-grants \
  --output "$PWD/../memory-observation.json"
```

`--settings` supplies effective settings JSON instead of defaults. Optional output
is a new file outside the package, containing observation JSON for test fixtures.
Inspect truncation/incomplete evidence; variable real readings do not replace
deterministic boundary tests. `filesystems.snapshot` also needs
`--filesystem-helper` pointing to an existing trusted agent binary for the generic
mount observer, without rebuilding it. Missing helpers return `runtime_unavailable`.
Commands, units and files depend on the local test machine. Installed agents also
enforce their operation ceiling and protected configuration/state/runtime paths.

## Package and install

```sh
python3.13 -I -S -B sdk/python/skill_tool.py pack ../acme-memory \
  --output ../acme-memory-1.0.0.zip
python3.13 -I -S -B sdk/python/skill_tool.py validate ../acme-memory-1.0.0.zip
```

`pack` uses the production deterministic ZIP writer and checks its result through
the production ZIP reader/loader, without executing code. Output must be new and
outside the source directory. Keep `.git`, editor metadata, environments and
`__pycache__` outside the package: unsupported payloads are rejected, not silently
excluded. The eight required files must be at the ZIP root, without a wrapping
directory. Limits: 10 MiB compressed, 20 MiB unpacked, 8 MiB per file, 256 files.

Included docs/tests affect `content_sha256`; ZIP bytes have `archive_sha256`.
Neither proves safety or publisher identity. Upload via **Skills**, review requested
access, then explicitly enable. Imports start disabled; compatible agents download
the exact approved package. Settings, field overrides, facts, history and alerts
use the generic engine. An ordinary SDK v1 package needs no core/database migration.

## Updates and support limits

For shared tables, meters, gauges and charts, see the
[Skill Display API](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-display.md).
Keep useful SDK v1 facts as stored evidence and the descriptorless fallback.
The optional `display.json` explicitly chooses shared widgets, with labels in
the existing English catalog and no HTML/CSS/JavaScript in the package.

- Change bare `major.minor.patch` whenever distributed package bytes change. An
  existing ID/version cannot be reused with different content.
- A new upload is not automatically selected. Exact-version access requires fresh
  review and explicit selection.
- Selection currently requires an identical settings schema and saved settings
  accepted by the new code. State starts fresh; old readings retain their original
  interpretation/catalog. No settings/state migration hooks exist. `state_version`
  describes state format and does not authorize a migration.
- Only Debian 13 amd64 is verified. Unsupported SDK/runtime/capabilities are rejected.
  A new host operation/runtime is a separate platform change; combining current
  operations requires no app/agent modification.
- Failed/partial collection, denied access, invalid output, resource exhaustion and
  expired evidence cannot become healthy. Engine contact/authority/freshness still
  apply to package assessments.
- Python alone is not a sandbox: the engine enforces namespaces, cgroups, seccomp
  and limits. Packages still need access review. Marketplace discovery, public
  vetting, signing and tarballs are deferred.

Canonical contracts: [package format](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-packages.md)
and [runtime boundary](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-runtime.md).

## Display a table, usage bar and history

The starter includes `display.json`: a memory table with readable byte capacities,
a usage meter in its percent column, and a line chart bound to `used_percent`.
The meter uses the exact observation's warning/critical settings. `region` is the
stable series key; its human label comes from the catalog. Keep row identities
stable across runs and avoid joining by a changing display label.

`validate` also checks the closed descriptor, references, units and bounds without
executing code. `test` checks emitted assessment facts; it cannot manufacture real
chart history. After upload and enablement, the app captures new accepted readings
automatically, retaining up to 90 days. Two or more real readings demonstrate the
chart; no per-skill core edits, rebuild or restart is needed. Compatible earlier
versions remain in the same graph. Unrepresented data remains in Details.

See the [shared Playbook guide](https://github.com/TinyWarden/tinywarden/blob/main/docs/ui/playbook.md)
and `/playbook` on your installation for actual component examples. You upload
only JSON declarations and ordinary SDK facts: no HTML, CSS, JavaScript or SVG.

## Shared presentation options

New descriptors use display `format: 2`. Every section requires `role`:
`current` for results, `graph` for temporal widgets, or `details` for supporting
facts. Include at least one current section. Format 2 rejects `collapsed`,
`disclosure` and widget `default_window`. The app owns section order, expansion
and the common 24 h / 7 d / 30 d / 90 d range. Format 1 remains accepted, normalized
by widget type; its legacy expansion/range hints do not override the shared frame.

History is app-owned and last: ten collections per page, Time / Result / Note,
without replaying old widgets or repeating chart values. Graph point/bucket values
remain accessible through pointer and keyboard inspection. A failed collection
still appears in History; useful current limitations belong in the current summary.
Update the generic Python inspector to support format 2 before activating such
packages on an older installation. This platform upgrade does not change Go core,
SDK number, collectors, capability grants or wire protocol; later new skills use
this interface without platform edits. See the
[shared structure/API contract](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-widget-structure.md).

Scalar text sources with an enum may use
`muted_values: ["unverified"]` to subdue those specific enum values visually.
This does not change their health or turn present data into missing data.

The app supplies the skill card, chart range buttons, reading table, recent runs
and per-server Settings dialog. Numeric facts use the shared mono style. Sparse
history uses real sample times; dense ranges use bounded summaries that preserve
minimum and maximum values. Genuine evidence gaps remain explicit.
See the [shared presentation contract](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-presentation.md).

## Notification Details

Declare optional `notifications.json` format 1. The app owns when an email is sent,
its subject, severity, sender, recipient, identity and server link. Skills may
provide one paragraph from accepted reason parameters, scalar facts, effective
settings and translated catalog phrases. Structured **bold**, *italic* and
underline are the only styles. No raw HTML, general Markdown or links.

See the complete [Details API and schema](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-notification-details.md)
for exact bindings, plural forms, limits and fallback. Validation rejects malformed
declarations; missing values omit Details without suppressing an eligible email.
The public `skills/examples/memory-pressure/notifications.json` demonstrates
community declarations using all three styles, without modifying the app or agent.
Collector, reducer, evaluation and protocol shapes are unchanged. Compatible
older installed agents ignore this extra file and still collect the skill.

### Choosing useful Details

The app renders only the widgets explicitly declared in `display.json`; it does
not dump unreferenced facts or table columns into Details. Use Details for useful
investigation data, such as pending package names, not copies of current results,
settings or collection History. Valid empty tables and empty unencoded text
without an empty-value enum label disappear in Details; truncated tables, numeric
zero and boolean false remain. Missing/broken declared bindings show unavailable
data. When all Details widgets are empty, the disclosure is omitted. Packages
without a display descriptor retain the generic facts fallback. No HTML is allowed.
