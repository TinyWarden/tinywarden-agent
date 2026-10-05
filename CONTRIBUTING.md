# Contributing

This repository owns the host executable, tests, service and packaging. Read
[architecture and protocol ownership](docs/architecture/overview.md) before changing
wire formats, compiled recipes or durable state. Use a focused branch and pull
request with concrete verification. A [standalone SDK v1 skill](docs/skills/authoring.md)
using supported operations needs no app or agent modification. Adding a host
capability or execution runtime requires explicit platform/policy design.

Use focused local checks while working and the complete phase-end gate once
at release closeout. GitHub/security checks are manual phase-closeout actions.
Stage intended file additions/removals, maintain authored map roles and run
python3 scripts/source_check.py --write. Keep each tracked path documented.
Agent CLI copy belongs in internal/cli/en.json; author-tool help belongs in
sdk/python/authoring/en.json. Preserve legal notices.

The app vendors required authored examples at an exact agent revision. When
those JSON files change, intentionally update its manifest/examples and run
affected tests in both products. Never introduce a sibling-checkout or
network-fetch requirement into ordinary tests. Never commit private state,
configuration, tokens, credentials or internal process notes.
