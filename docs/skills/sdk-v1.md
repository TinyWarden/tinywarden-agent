# Python SDK v1 reference

Implemented identifiers: `format: 1`, `sdk: 1`, `runtime: "python-3.13-v1"`.
These identify the package protocol, not the app/agent release. See the
[authoring walkthrough](authoring.md) for the starter, commands and version rules.

## Functions

Define all four functions at module top level in `skill.py`. Values are JSON-safe:
objects/arrays/strings/booleans, finite numbers and integers within ±(2^53−1).
No async entrypoints, custom serialization or dependency installation. Imports run
only inside the isolated guest; top-level code runs each invocation, so keep it
free of collection/side effects. Package files are read-only, and globals/scratch
files do not persist.

| Function | Input | Output / environment |
| --- | --- | --- |
| `validate_settings(settings)` | Complete schema-checked scalar settings. | Up to 64 structured errors or `[]`; pure. |
| `collect(settings, host)` | Effective settings, bounded host broker. | Object matching observation schema; granted read-only host access. |
| `reduce(context)` | Context below with null `state`. | Object matching state schema, ≤16 KiB; pure. |
| `evaluate(context)` | Context with reduction output in `state`. | 1–8 bounded assessment entries; pure. |

Pure functions have no host broker. Cross-field errors use:

```python
return [{"field": "critical_percent",
         "message": {"key": "invalid_thresholds", "params": {}}}]
```

Field names must exist in the settings schema; messages/parameter types match
catalog entries. Exceptions are execution failures, not human-readable errors.

## Context and assessments

```python
{
    "identity": {
        "installation_id": "...", "content_sha256": "...",
        "assignment_id": "...", "generation": 1
    },
    "settings": {...}, "observation": {...}, "previous_state": None,
    "state": None,  # reduce input; its output is passed to evaluate
    "captured_at": 1800000000000, "received_at": 1800000000000,
    "now": 1800000000000, "evidence_expires_at": 1800000600000
}
```

Times are UTC milliseconds; use supplied times, not the guest wall clock. The
engine controls identity, sequence, receipt authority and evidence age. Expired,
superseded or invalid readings cannot refresh health. Previous state may be null,
reset after version selection or pruned; it cannot rely on local persistence.

```python
return [{
    "from": context["now"], "status": "warning",
    "reason": {"key": "memory_usage", "params": {"percent": 85}},
    "facts": [{"key": "used", "label_key": "used_label",
               "kind": "percent", "value": 85}]
}]
```

Statuses: `healthy`, `warning`, `critical`, `unknown`. The first entry starts at
`now`, later entries are strictly ordered, and all precede `evidence_expires_at`.
Scheduled transitions interpret existing evidence over time; they do not recollect.
A weekly systemd skill should use timer/execution context rather than require a
new weekly execution each agent check. Engine evidence freshness remains bounded.

## Messages and facts

Catalog entries have `text` and `parameters`, for example
`{"text":"Memory is {percent}% used.","parameters":{"percent":"number"}}`.
Parameter types are `string`, `number`, `boolean`; label/help entries have no
parameters. English catalog is required, with plain text and no HTML.

Up to 64 facts have unique lowercase keys starting with a letter. Scalar facts
contain exactly `key`, `label_key`, `kind`, `value`:

| Kind | Value |
| --- | --- |
| `text` | String ≤1024 characters; large byte counts can be text. |
| `number` | Finite JSON-safe number. |
| `boolean` | Boolean. |
| `percent` | Number 0–100. |
| `duration` | Nonnegative integer seconds. |
| `time` | Integer UTC milliseconds, from 0 through 8640000000000000. |

Tables contain exactly `key`, `label_key`, `kind: "table"`, `columns`, `rows`,
`truncated`. There are 1–12 unique columns (`key`, `label_key`, scalar `kind`),
≤128 rows with exactly those keys and typed values, and a boolean `truncated`.
Incomplete evidence cannot appear complete. The timeline is ≤256 KiB. Data is
revalidated and escaped; arbitrary HTML, clickable URLs and UI code are unsupported.

The optional [Skill Display API v1](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-display.md)
and [metric history](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-metric-history.md)
add an optional validated `display.json` sidecar that binds
shared widgets to these facts; no uploaded UI code or new collection function.
Packages without it retain the scalar/plain-table rendering described above. Display metadata never determines health or expands host access.

## Host API

Admission requires `interval_seconds`, if declared, to be an integer schema with
explicit bounds contained within 60–86400 seconds. Missing cadence uses the
platform's 300-second default. A package name must be parameter-free and fit 1024
UTF-8 JSON bytes and 2048 UTF-16 code units. Rendered catalog messages must fit
4096 UTF-8 JSON bytes; all settings errors together must fit 64 KiB. These limits
apply after placeholder substitution, in addition to individual parameter limits.

Systemd grants declare unit names as an array, never an object; names cannot begin
with `-`. Broker queries separate fixed options from approved unit operands.
Oversized execution responses and upload envelopes become `output_exceeded` under
the original run identity. Isolation cleanup failures keep their separate pause
behavior. ZIP admission reclaims partial files after child failure before reuse.

Only `collect` gets `host.request(operation, arguments)`. Package request,
exact-version administrator grant and local ceiling must each permit the operation.
Denied requests fail the invocation even if package code catches the exception.
OS read permissions apply; no agent credentials, direct host filesystem, ambient
environment or network are exposed.

| Operation | Arguments | Result |
| --- | --- | --- |
| `files.read` | `path`, `max_bytes` (≤65536), optional boolean `optional`. | Default: UTF-8 `text`, `truncated`. With `optional:true`: `available:true` plus those fields, or `available:false` and a fixed `reason`. |
| `files.stat` | `path`. | `exists`; when true, `size` bytes. |
| `files.list` | `path`, `max_entries` (≤128). | Sorted `entries` names, `truncated`. |
| `filesystems.snapshot` | `{}`. | Existing normalized mount observation; canonical disk contract below. |
| `systemd.properties` | `unit`, `properties` array. | Map of declared properties to bounded strings. |
| `command.capture` | `executable`, concrete `argv`, `inputs`, optional `helpers`, `empty_directories`. | `exit_code`, bounded `stdout`, `stderr_bytes`; no raw stderr. |

Minimal manifest capability and corresponding request:

```json
{"operation":"files.read","paths":["/proc/meminfo"],"roots":[],"max_bytes":16384}
```

```python
snapshot = host.request("files.read", {"path": "/proc/meminfo", "max_bytes": 16384})
if snapshot["truncated"]:
    raise ValueError("incomplete_observation")
```

File grants name exact `paths` and narrow `roots`; links, special files and
protected credential/state/runtime paths are denied. `/proc` uses a fixed small
whitelist. Under `/run`, only the exact files `/run/reboot-required` and
`/run/reboot-required.pkgs` are supported; neither can be a directory grant.
Systemd grants name specific units/properties. Command grants fix
executable/argument alternatives/inputs/timeout; they are not a shell escape.
System executables/libraries are trusted and inputs mounted read-only.
The main executable must be under `/usr/bin` or `/usr/sbin`. Optional helpers
name individual binaries under those directories or `/usr/lib`; each path and
its distro library dependencies must be root-owned and not writable by other
accounts. Helpers still require matching manifest, administrator and local
grants. Distro `/lib` and related `/usr` aliases are recreated in the private
root without exposing additional host directories.
Official manifests and the [runtime contract](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-runtime.md)
document grant forms. The [disk observation contract](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/disk-observations.md)
owns filesystem result fields. A new host capability requires a reviewed platform
contract; a normal skill using existing operations does not.

`files.read` can request `optional:true` for supplementary evidence. Fixed
unavailable reasons are `not_found` (missing), `unreadable` (access denied by the
OS or an ordinary I/O failure), and `invalid_encoding` (invalid UTF-8). No exception
text or partial invalid text is returned. Omitted or false preserves the normal
read/error contract. The flag is a request argument, not a manifest grant field;
it is rejected for stat/list and does not alter any permission intersection.
Ungrantable paths, links, special files, capability denials, worker/resource/
deadline/output failures remain fatal. Use the unavailable result to preserve a
primary observation while reporting missing supplementary information. Deploy
the updated trusted SDK artifact before packages relying on this addition.

## Resource and failure contract

Pure calls: 128 MiB memory, two CPU seconds, five wall seconds. Collection:
256 MiB, 15 CPU seconds, manifest wall limit 1–60 seconds. `timeout_seconds`
can only lower that limit. PID/output/private-scratch limits, read-only mounts,
namespaces, seccomp and whole-group cleanup apply. Collection permits at most
64 broker calls and 256 KiB aggregate raw broker output.

Failure codes include `runtime_unavailable`, `capability_denied`, `broker_limit`,
`observation_unavailable`, `deadline_exceeded`, `resource_exhausted`,
`execution_failed`, `output_invalid`, `output_exceeded`, `cleanup_failed`.
Invalid metadata/schema is rejected before activation. Never treat missing/partial
or failed evidence as healthy. Guest tracebacks/raw diagnostics are not an API;
return typed observations, catalog reasons and facts.

The canonical [package format](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-packages.md)
owns schema, identity and archive limits; the [authoring guide](authoring.md#updates-and-support-limits)
documents the implemented upgrade restrictions.

## Shared display and collection history

Display format 2 supports explicit `current`, `graph`, and `details` section
roles. Collection and assessment facts stay SDK v1. The app supplies one compact
History log at the end, with ten entries per page and a window shared by graphs.
Compatible metric definitions include older package versions; uploading new UI
metadata does not discard measurements. See the canonical
[structure contract](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-widget-structure.md)
and [author guide](authoring.md) for admission rules and examples.
