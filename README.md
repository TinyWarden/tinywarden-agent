# TinyWarden agent

Requires Go 1.27.1. There are no third-party Go dependencies.

```sh
go test ./...
go build -o ../bin/tinywarden-agent ./cmd/tinywarden-agent
../bin/tinywarden-agent --version
```

P1.C supports outbound HTTPS enrollment and heartbeat on Debian 13. The client
validates the system CA and configured hostname, refuses redirects, persists a
pending request before sending it, and keeps its state in an owned private directory.
It performs no checks or privileged host actions yet. User-facing copy is embedded
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
`run` holds an exclusive state lock, sends a heartbeat, then waits the recorded
interval with jitter. Temporary failures retry within bounds and retain the pending
request; authority, TLS and protocol failures stop for operator review. The agent
never prints credentials. A disposable Debian 13.4 x86_64 VM passed HTTPS enrollment,
restart and pending-heartbeat recovery; production installation belongs to P1.D.
