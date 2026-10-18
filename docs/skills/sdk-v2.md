# SDK 2 resource skills

SDK 2 extends [SDK 1](sdk-v1.md) for explicit per-server resource selections.
Both run in `python-3.13-v1`. SDK 1 wire shapes, state budgets and fingerprints
remain unchanged. SDK 2 is included in managed agent 0.0.11 and later.

## Package contract

Use `format: 2`, `sdk: 2` and `resource_collections` in `skill.json`. Each of at
most four collections declares `selection_field`, `label_key`, `help_key`,
`max_discovered` (1–512) and `max_selected` (1–64). The selection field has
`scope: "host"`, `editor: {"kind":"resources","collection":"name"}`, default
`[]`, and exactly this bounded string-array schema (with its selected maximum):

```json
{"type":"array","items":{"type":"string","minLength":1,"maxLength":255},"minItems":0,"maxItems":64}
```

The runtime additionally validates unique keys and a 64-target total across all
collections. Keys are opaque, unique within a collection, at most 255 UTF-8 bytes,
without controls or lone surrogates. Labels/descriptions are plain text, each at
most 256 UTF-8 bytes. Global defaults cannot select resources for servers.

`collect(settings, host)` returns `{observation, resources}`. `observation` is
package-owned and schema checked. `resources` contains every declared collection,
with `{collection, complete, reason, items}`. `reason` is a catalogue message or
null; complete is true exactly when reason is null. Each item has
`{key,label,description,selectable,selection_reason,state}`. State and selection
reason use `{key,params}` catalogue messages; an unselectable item needs a reason.
Total discovery is 512 items. Read selected targets independently of discovery.

`reduce(context)` gets `resource_epochs[collection][key]` and
`evaluation_revision`, alongside the existing settings, observation and clocks.
Persist state by membership epoch. Unrelated selection additions preserve old
epochs; removal/re-addition changes the epoch. The evaluation revision changes
with content or scalar settings, excluding selection arrays. Reset pending evidence
on epoch, revision, boot or continuity change as appropriate to the skill.

`evaluate(context)` returns 1–8 entries, first at `context.now`, strictly increasing
and before `evidence_expires_at`. Each is `{from,subjects,facts}`. Each subject is
`{collection,key,status,reason,facts}` and must cover exactly one selected target;
no duplicate, missing or unselected outcomes. Status is healthy/warning/critical/
unknown. The app computes one aggregate with critical > warning > unknown > healthy.
Empty selection is **Not configured**, with no problem or recovery notifications.

## Read-only service observations

- `systemd.services.list`, arguments `{}`: fixed system-manager enumeration of
  installed and loaded services; canonical IDs, basic properties, boot ID, uptime,
  completeness and an explicit partial reason. Discovery has an 8-second internal
  deadline so failure can return partial evidence within the collection deadline.
- `systemd.services.read`, arguments `{units:[exact names]}`: at most 64 unique
  `.service` identities, queried in batches. Every requested target gets explicit
  available/unreadable evidence. Exact `LoadState=not-found` differs from read failure.

Grants are exactly `{operation}`. All three layers must allow each operation:
package declaration, exact-digest administrator approval, and local agent ceiling.
An explicit local ceiling is never widened by an update. No service actions,
journals, environment, unit contents, arbitrary commands or arbitrary properties.
The CLI adapter uses fixed arguments, validates escaped names, separates operands,
deduplicates aliases, and treats bare templates as non-runnable.

## Budgets and compatibility

| Lane | SDK 2 maximum |
| --- | --- |
| Inventory | 256 KiB |
| Selected observation | 128 KiB |
| Reducer state | 64 KiB |
| Pure execution input/output | 512 KiB |
| Broker aggregate / calls | 512 KiB / 64 |
| Outer transport | 1 MiB |

JSON budgets use the runtime's ASCII-escaped compact encoding. SDK 1 remains
16 KiB state, 256 KiB broker and 1 MiB guest input. The former 64 KiB figure was
metadata inspection input, not the guest context limit.

A new agent verifies the SDK 2 resource probe in the real sandbox before advertising
`X-TinyWarden-Package-Features: skill_resources.python313.v1` on package polling.
Manual run negotiation remains independent. Old servers return schema 1. Upgraded
servers return schema 2 plus the feature echo only when negotiated, and include
`package_format`. Old agents receive SDK 1 only; SDK 2 appears unavailable. SDK 2
runs use schema 2 with separate observation/resources fields. SDK 1 durable queued
run bytes and fingerprints retain their existing meaning.

Author fixtures may supply `resource_epochs` and `evaluation_revision`; `statuses`
compares resource subject statuses in timeline order. The existing validate/test/
collect/pack tools accept SDK 2 without a new host execution path. Official package
admission remains reserved to the trusted publishing/repository path.

The full app persistence, ACL, conflict, history and retention contract is in
[TinyWarden resource architecture](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-resources.md).
