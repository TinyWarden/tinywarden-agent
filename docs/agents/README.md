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

Maintenance mirror handling follows the central
[maintenance contract](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/maintenance-actions.md).
Validate declared local mirror lists, pin original bytes and protect auxiliary
cache entries during native use, preserving the existing directory group and mode
and restoring its original ownership. Mirror-list items never count as package archives.
`internal/maintenance/apt/helper/00_support.py` keeps anchored input traversal and
source-tree hashing together so aliases cannot be validated one way and hashed
another. Mirror transport and cache ownership stay in `helper/15_archives.py`.

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
  Host helpers and isolated guests use a fixed C.UTF-8 locale and UTC timezone;
  do not inherit host locale/timezone or add package-specific date conversions.
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

Maintenance foundations live in `internal/maintenance`; Debian adapter source is
embedded from `internal/maintenance/apt/helper`, outside the Python monitoring SDK.
The shared `internal/hostmutation` guard preserves the deployed updater lock path.
Read the [maintenance contracts](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/maintenance-actions.md)
before adding an action. Native package tests require an explicitly designated
disposable host; ordinary unit checks cannot establish package-install safety.

Read-only reconciliation has its own scoped authorization and immutable outbox;
it does not reuse a revoked agent credential or infer original apply success from
a repaired system. Preserve app acceptance, local locked fence release and final
receipt ordering. The root poller may deliver an already released receipt, but
cannot initiate an audit or reset old execution. Pending outboxes block installer
mutations until acknowledged. The canonical maintenance reference owns the exact
contract and CLI input rules; keep secrets out of arguments and ordinary reports.

Normal terminal acknowledgement also has a resumable release boundary. Under the
common root guard and idle worker-cgroup check, discovery may remove only its
matching durably acknowledged fence. Keep the receipt and refuse foreign/corrupt
scope, uncertain execution and pending recovery delivery. Recovery closures use
their own ordered outbox protocol; never reuse normal cleanup to bypass it.

Managed remote requests reuse the installation lock, signed target checks and
transaction recovery. Preserve the root-only request journal and retry terminal
acknowledgements without reinstalling. See the canonical
[update protocol](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/agent-updates.md).


Safe preparation/download wrappers in `internal/maintenance/budget.go` use the
boot clock for whole-phase limits; do not wrap `Apply` in cancellation or a timeout.
Trusted input hashing checks root ownership through every original/symlink path
component, including empty/absent inputs. Native proof of input ownership and
signed HTTP repository provider behavior belongs on the designated disposable host.
