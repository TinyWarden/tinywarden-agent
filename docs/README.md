# Host agent and SDK

For installation and maintenance, use the [host-agent installation guide](deploy/native.md).
The [TinyWarden documentation index](https://github.com/TinyWarden/tinywarden/blob/main/docs/README.md)
is the starting point for product workflows and cross-repository contracts.

- [Build a skill](https://github.com/TinyWarden/tinywarden/blob/main/docs/build-skills/README.md)
- [Add actions and use the action API](https://github.com/TinyWarden/tinywarden/blob/main/docs/build-skills/actions.md)
- [Add items to Fleet Skill actions](https://github.com/TinyWarden/tinywarden/blob/main/docs/build-skills/actions.md#add-items-to-fleet-skill-actions)
- [Agent operations](https://github.com/TinyWarden/tinywarden/blob/main/docs/operating/agents.md)
- [Every agent HTTP endpoint](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/agent-endpoints.md)
- [Contribute to the agent and SDK](../CONTRIBUTING.md)
- [LLM assistant instructions](../AGENTS.md)

This repository owns the Go host agent and generic Python SDK/runtime. Official
monitoring packages live in the separate skills repository. Deployment templates
are in deploy/, SDK source and unit tests in sdk/python/, runnable examples in
examples/skills/, and native process acceptance in tests/integration/.


Agent 0.0.24 adds a generic bounded work-wait endpoint and supports the optional
`local`/`host_health` action verification policy. The policy is declared by each
skill and documented in the [action contract](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/skills/actions.md#choosing-verification).
The agent carries no APT refresh or upgrade recipe. Existing descriptors without
the new field retain their previous behavior; packages that use it need 0.0.24+.
Failed waits or hint-cache faults use bounded backoff before ordinary authenticated
job polling and recovery delivery; the optional delivery hint does not grant execution.
