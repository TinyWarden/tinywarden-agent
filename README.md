# TinyWarden agent

The host-side agent for [TinyWarden](https://github.com/TinyWarden/tinywarden).
It enrolls over outbound HTTPS, reports heartbeat and typed observations, and
executes enabled standalone Python skills through a bounded SDK. Four official
skills observe disk space, package updates, reboot status and filesystem trim.
Compatible community packages are delivered automatically without a binary rebuild.
Skills never install updates, reboot a host or perform trim. The server evaluates
health and sends notifications.

Verified platform: Debian 13 on amd64, running as a dedicated non-root account.
Agent version: 0.0.4.
Go 1.27.2 is required; the module uses only the Go standard library.

## Build

```sh
go test ./...
mkdir -p bin
go build -o bin/tinywarden-agent ./cmd/tinywarden-agent
bin/tinywarden-agent --version
```

The agent builds independently of the app. Copy
[agent.example.json](config/agent.example.json) to a private configuration file
and set the HTTPS control-plane origin and absolute owned state directory.
The one-use enrollment token belongs in a separate mode-0600 file, not in
configuration or command arguments.

```sh
bin/tinywarden-agent enroll --config /path/to/agent.json --token-file /path/to/token
bin/tinywarden-agent run --config /path/to/agent.json
```

An uncertain enrollment is recovered by run with the same state. Replacement
uses a host-bound replacement token and the replace command after stopping the
service; preserve all state and recover uncertain responses with run. Never
delete state to clear an error or re-enroll an existing agent casually.

## Verification and packaging

```sh
./scripts/verify.sh --batch
./scripts/verify.sh --phase-end
python3 scripts/package.py --output /absolute/path/to/tinywarden-agent.tar.gz
```

Verification tooling additionally requires Python 3, Git and native Linux tools.
Phase-end checks require the pinned gitleaks/govulncheck tools; security and GitHub
checks run at release closeout. Packaging requires reviewed source staged in Git,
checks its exact tree, builds the binary and includes the service/install guide
and legal notices. It never installs or changes a running service.

- [Architecture and protocol ownership](docs/architecture/overview.md)
- [Complete codebase map](docs/architecture/codebase-map.md)
- [Installation and operations](docs/deploy/native.md)
- [Verification](docs/verification.md)
- [Write a standalone skill](docs/skills/authoring.md) and [Python SDK v1](docs/skills/sdk-v1.md)
- [Contributing](CONTRIBUTING.md)

Agent CLI copy is embedded from internal/cli/en.json; author-tool help lives in
sdk/python/authoring/en.json, for future translations.
Authored source is Apache-2.0; bundled Go redistribution terms are retained in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
