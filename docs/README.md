# Host agent and SDK

For installation and maintenance, use the [host-agent installation guide](deploy/native.md).
The [TinyWarden documentation index](https://github.com/TinyWarden/tinywarden/blob/main/docs/README.md)
is the starting point for product workflows and cross-repository contracts.

- [Build a skill](https://github.com/TinyWarden/tinywarden/blob/main/docs/build-skills/README.md)
- [Add actions and use the action API](https://github.com/TinyWarden/tinywarden/blob/main/docs/build-skills/actions.md)
- [Agent operations](https://github.com/TinyWarden/tinywarden/blob/main/docs/operating/agents.md)
- [Every agent HTTP endpoint](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/agent-endpoints.md)
- [Contribute to the agent and SDK](../CONTRIBUTING.md)
- [LLM assistant instructions](../AGENTS.md)

This repository owns the Go host agent and generic Python SDK/runtime. Official
monitoring packages live in the separate skills repository. Deployment templates
are in deploy/, SDK source and unit tests in sdk/python/, runnable examples in
examples/skills/, and native process acceptance in tests/integration/.
