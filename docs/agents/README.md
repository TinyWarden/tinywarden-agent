# Host agent guide for LLM assistants

The [file map](codebase-map.md) lists exact ownership. Read root
[AGENTS.md](../../AGENTS.md) and [CONTRIBUTING.md](../../CONTRIBUTING.md) first.

| Area | Source | Checks |
| --- | --- | --- |
| Executable and orchestration | `cmd/`, `internal/cli/`, `internal/agent/` | CLI, startup, heartbeat, package-worker and Fleet tests |
| Transport and durable identity | `internal/controlplane/`, `internal/config/`, `internal/state/` | Bounded responses, exact retries, locking and recovery |
| Installation and release trust | `internal/installation/`, `internal/release/`, `deploy/`, `scripts/release/` | Signature vectors, installer/update recovery and complete bundles |
| Host observation and runtime bridge | `internal/host/`, `internal/skillruntime/` | Filesystem helper, runtime verification and shared execution capacity |
| Compiled compatibility | `internal/legacy/` | Retained assignments, queues, fixed recipes and pause/resume |
| Python contracts, host access, isolation and admission | `sdk/python/runtime/{contracts,host,isolation,packages}/` | SDK unit tests in `sdk/python/tests/`; process boundaries in `tests/integration/` |
| Skill authoring | `sdk/python/skill_tool.py`, `sdk/python/authoring/`, `examples/skills/` | Independent init/validate/test/collect/pack and native sandbox cases |
| Source and documentation tooling | `scripts/checks/source_check.py`, map roles | Guard tests, generated map and links |

`python3 -B scripts/checks/runtime_assets.py --check` checks the trusted SDK asset set.
`./scripts/verify.sh --batch` runs the repository verification profile. The
`--skills-author` profile runs SDK/authoring checks without the Go gate and needs
supported delegated native cgroup limits. `--sandbox` includes the ordinary Go
gate plus authoring and runtime isolation cases. Use captured observations for deterministic skill tests and a controlled
host for real collection; report missing sandbox prerequisites as failures.

Keep root `supervisor.py`, `launcher.py`, `seccomp.py`, `probe/` and
`resource_probe/` paths stable. Old installed updaters verify these assets before
running the new binary. The root seccomp module delegates to `isolation/seccomp.py`;
it is not a second implementation. Include package initializers in `artifact.json`.
Lower-level Go owners must not depend on daemon or installation orchestration.
Worker ledgers stay with their worker; shared persistence must retain one lock.

Preserve immutable published releases, signing key ownership and pinned asset
identity. Check actual formats and supported releases before describing capability.

Fleet control changes must preserve durable negotiated epochs across every lane,
quiescent pause acknowledgements and heartbeat independence. Managed reconnection
checks target identity before consuming a replacement code. See
[Fleet contracts](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/fleet-management.md).

## Canonical references

- [Architecture](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/architecture.md)
  and [agent protocol](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/agent-protocol.md):
  preserve identity, counters, exact request bytes, leases and original receipts.
- [SDK lifecycle](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/skills/sdk.md),
  [host broker](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/skills/host-access.md)
  and [resources](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/skills/resources.md):
  reuse bounded operations; separate collection from pure interpretation.
  Author fixtures mirror the shared [clock contract](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/skills/sdk.md#clock-contract).
  Evaluation-time normalization does not replace pre-collection transport deadline checks.
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
