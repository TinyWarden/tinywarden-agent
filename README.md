# TinyWarden host agent

The host agent connects to [TinyWarden](https://github.com/TinyWarden/tinywarden)
over outbound HTTPS, sends heartbeat and observations, and runs compatible Python
monitoring skills with bounded host access. The application evaluates health.

Verified host platform: Debian 13 on amd64. Skills are distributed independently
through [skill repositories](https://github.com/TinyWarden/tinywarden-skills).
A skill using supported operations needs no host-agent binary change.

This repository owns the reusable SDK and execution runtime. The separate skills
repository owns official monitoring packages, tests and independent releases.
Its build tools use an exact SDK pin; the repositories do not need adjacent
checkouts to build or test.

- [Installation and operations](docs/deploy/native.md)
- [Agent and SDK documentation](docs/README.md)
- [TinyWarden documentation](https://github.com/TinyWarden/tinywarden/blob/main/docs/README.md)
- [Contributing](CONTRIBUTING.md)

## Development

Use the Go version in [go.mod](go.mod). The host executable uses the Go standard
library. SDK work additionally uses Python 3.13 and the supported Linux sandbox.

```sh
go test ./...
mkdir -p bin
go build -o bin/tinywarden-agent ./cmd/tinywarden-agent
bin/tinywarden-agent --version
```

See the [configuration example](deploy/config/agent.example.json) for manual installations.
Authored source is Apache-2.0; preserve [LICENSE](LICENSE) and
[third-party notices](THIRD_PARTY_NOTICES.md).
