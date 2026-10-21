# Contributing to the host agent

This repository owns the host executable, Python SDK, isolated runtime and generic
host observation capabilities. Platform ownership is described in the
[TinyWarden contributor guide](https://github.com/TinyWarden/tinywarden/blob/main/docs/contributing/README.md).
For exact source locations use the [file map](docs/agents/codebase-map.md).
Use the central [development](https://github.com/TinyWarden/tinywarden/blob/main/docs/contributing/development.md)
and [verification](https://github.com/TinyWarden/tinywarden/blob/main/docs/contributing/verification.md)
guides for prerequisites, isolation and check selection.

```sh
python3 scripts/source_check.py
./scripts/verify.sh --batch
```

Maintain `scripts/map-roles.json`, stage intended path additions/removals, and run
`python3 scripts/source_check.py --write` to regenerate the map. The complete
release gate is `./scripts/verify.sh --phase-end`; it additionally requires the
pinned security tools used in the repository workflow.

Preserve enrolled identity, counters, queues and uncertain retries. SDK/runtime
changes require compatibility and sandbox tests. Do not weaken isolation or add
an unsandboxed fallback. A compatible skill using existing operations belongs in
a skill repository.

App integrations consume pinned assets and fixture contracts. Change those pins
intentionally and verify affected consumers; ordinary tests must work independently
of sibling checkouts. Preserve licenses, runtime integrity manifests and public
signing keys. Update the owning human/reference guidance under the
[documentation policy](https://github.com/TinyWarden/tinywarden/blob/main/docs/contributing/documentation.md).
LLM assistants should read [AGENTS.md](AGENTS.md).
