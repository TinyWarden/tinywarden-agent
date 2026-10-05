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
