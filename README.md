# TinyWarden agent scaffold

Requires Go 1.27.1. There are no third-party Go dependencies.

```sh
go test ./...
go build -o ../bin/tinywarden-agent ./cmd/tinywarden-agent
../bin/tinywarden-agent --version
```

The CLI supports help/version only. Default execution and unsupported operations
exit with status 2. It has no enrollment, network, check runner, privileged action,
configuration or persistent state. User-facing copy is embedded from
`internal/cli/en.json` for future translation.
