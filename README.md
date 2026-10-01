# TinyWarden agent

Requires Go 1.27.1. There are no third-party Go dependencies.

```sh
go test ./...
go build -o ../bin/tinywarden-agent ./cmd/tinywarden-agent
../bin/tinywarden-agent --version
```

The agent supports outbound HTTPS enrollment, heartbeat, disk collection and
fixed baseline observations on Debian 13/amd64. The client
validates the system CA and configured hostname, refuses redirects, persists a
pending request before sending it, and keeps its state in an owned private directory.
It advertises `disk_usage.v1` and performs bounded metadata-only collection without
privileged host actions. User-facing copy is embedded
from `internal/cli/en.json` for future translation.

Create a JSON configuration on the agent host with `control_plane_origin` set to
`https://neutralisp.tinywarden.com` and `state_dir` set to an absolute private path
owned by the agent process. Save a one-use enrollment token in a separate file owned
by that process with mode `0600`. Do not place it in the configuration or command
arguments. Enrollment is one-shot; an uncertain result is recovered by `run` with
the same state directory.

```sh
../bin/tinywarden-agent enroll --config /path/to/agent.json --token-file /path/to/token
../bin/tinywarden-agent run --config /path/to/agent.json
```

For an active host, an operator can issue a replacement token bound to its current
agent ID. Stop the running agent first to release the private state lock, then use
`replace --config /path/to/agent.json --token-file /path/to/token`. If the response
is uncertain, keep the state and start `run`; it retries the saved replacement
request and changes the local credential only after a matching server response.
The [native deployment guide](../docs/deploy/native.md#debian-13-agent-installation)
covers the service account and unit.

The directory is created with mode `0700` or rejected if ownership/mode is unsafe.
`run` holds an exclusive state lock and schedules heartbeat ahead of separate
assignment, collection and upload lanes. An assignment is saved atomically in mode-`0600` `assignments.json`
and scoped to origin, host, agent and credential generation; P1 `state.json` keeps
its strict identity schema. Assignment endpoint absence disables that lane while
heartbeat continues. `disk-sequence.json` is durably advanced before each collection;
`disk-queue.json` keeps at most 100 pending results and 8 MiB with an ambiguous
in-flight result preserved byte-for-byte. A validated assignment lease expires after
24 hours without a successful fetch. Collection runs only through the fixed
`__collect-disk-v1` entry in the same binary with a ten-second limit. Temporary
failures retry within bounds and retain pending heartbeat and disk requests;
authority, TLS and heartbeat protocol failures stop for operator review. The agent
never prints credentials. A disposable Debian 13.4 x86_64 VM passed HTTPS enrollment,
restart and pending-heartbeat recovery during P1.C, then enrolled against the live
control plane during first activation. Its native agent service is enabled.
The P2 in-place VM agent upgrade, preserved state and real assignment/observation
evidence are recorded in the [upgrade record](../docs/deploy/p2-live-upgrade.md).

## Observation runner development

P3.A adds `internal/runner` for the compiled Debian 13 observation policy. Its
whole-recipe validation, fixed internal supervisor, clean unprivileged execution,
separate output caps and retained process-group cleanup are implemented and
covered by [A01–A05](../docs/development/p3-acceptance.md#p3a-implementation-evidence).
Use `go test ./internal/runner ./internal/cli`, `go test -race ./internal/runner`
and `go vet ./internal/runner ./internal/cli` for this boundary. The integration
case builds a disposable executable in the test directory.

`Execute` is called from an independent worker and admits one recipe process-wide;
`Parse` alone never starts work. Raw evidence is transient and excluded from JSON.
The [execution contract](../docs/architecture/recipe-execution.md) owns accepted
profiles and bounds. P3.B adds normalizers; P3.C connects capability advertisement, scheduling,
server delivery and typed results. Local implementation is separate from the
installed P2 service: that service changes only in an approved upgrade window.

## Connected baseline worker

`exec_observe.debian13.v1` is advertised only on supported non-root Debian 13
amd64. `internal/agent` fetches all three baseline assignments every 60 seconds,
rotates among due recipes through one independent worker, and uploads typed
results separately from priority heartbeat and disk work. Recipes never refresh
APT indexes, install packages, trim storage or reboot. The server alone evaluates
health; cached zero APT plans and absent package markers retain unknown assurance.

Private `baseline-assignments.json`, `baseline-sequence.json`, `baseline-queue.json`
and `baseline-pause.json` share the existing state lock and mode-0600 atomic durable
writes. The validated lease is 24 hours; a recipe deadline cannot outlive it.
Sequence and active identity persist before launch; restart records interrupted
work without issuing it twice. Pending results are capped at 100/1 MiB, preserving
one uncertain upload exactly. Terminal conflicts and backwards wall time pause
baseline work durably. Old-server 404 pauses that lane; 401 stops authenticated
work. Never delete state to clear a pause: inspect the cause and use reviewed
credential replacement when required; old-generation files are archived privately.

The [wire/state contract](../docs/architecture/baseline-protocol.md) owns exact
formats. [P3 acceptance](../docs/development/p3-acceptance.md#p3c-implementation-evidence)
records synthetic and real VM proof. The [P3 upgrade plan](../docs/deploy/p3-live-upgrade.md)
owns the separately approved activation; no second product checkout or service.
