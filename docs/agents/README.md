# Host agent guide for LLM assistants

The [file map](codebase-map.md) lists exact ownership. Read root
[AGENTS.md](../../AGENTS.md) and [CONTRIBUTING.md](../../CONTRIBUTING.md) first.

| Area | Source | Checks |
| --- | --- | --- |
| Host executable and transport | `cmd/`, `internal/` | Go format, vet and affected Go tests |
| Skill admission and isolation | `sdk/python/runtime/`, `tests/runtime/` | Runtime asset integrity, admission/archive tests and affected real sandbox cases |
| Skill authoring | `sdk/python/skill_tool.py`, `sdk/python/authoring/` | Authoring and supported delegated sandbox tests |
| Installation and updates | `scripts/install.sh`, `internal/platform/`, `scripts/release.py` | Installer/update compatibility and package asset inspection |
| Source and documentation tooling | `scripts/source_check.py`, map roles | Guard tests, generated map and links |

`python3 -B scripts/runtime_assets.py --check` checks the trusted SDK asset set.
`./scripts/verify.sh --batch` runs the repository verification profile. The
`--skills-author` profile additionally needs supported delegated native cgroup
limits. Use captured observations for deterministic skill tests and a controlled
host for real collection; report missing sandbox prerequisites as failures.

Preserve immutable published releases, signing key ownership and pinned asset
identity. Check actual formats and supported releases before describing capability.

## Canonical references

- [Architecture](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/architecture.md)
  and [agent protocol](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/agent-protocol.md):
  preserve identity, counters, exact request bytes, leases and original receipts.
- [SDK lifecycle](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/skills/sdk.md),
  [host broker](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/skills/host-access.md)
  and [resources](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/skills/resources.md):
  reuse bounded operations; separate collection from pure interpretation.
- [Verification](https://github.com/TinyWarden/tinywarden/blob/main/docs/contributing/verification.md)
  and [releases](https://github.com/TinyWarden/tinywarden/blob/main/docs/contributing/releases.md):
  local bundles are not signed managed releases; source edits do not update
  consumer pins or installed runtime assets.
- [Installation](../deploy/native.md) and
  [agent operations](https://github.com/TinyWarden/tinywarden/blob/main/docs/operating/agents.md)
  describe current supported host behavior.

Load the affected reference and owning tests. Update its human guide under the
[documentation policy](https://github.com/TinyWarden/tinywarden/blob/main/docs/contributing/documentation.md).
Never weaken sandbox/cleanup checks or silently change provenance to fix a prerequisite.
