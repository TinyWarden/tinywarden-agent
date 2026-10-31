# Host agent and SDK

For installation and maintenance, use the [host-agent installation guide](deploy/native.md).
The [TinyWarden documentation index](https://github.com/TinyWarden/tinywarden/blob/main/docs/README.md)
is the starting point for product workflows and cross-repository contracts.

- [Build a skill](https://github.com/TinyWarden/tinywarden/blob/main/docs/build-skills/README.md)
- [Add actions and use the action API](https://github.com/TinyWarden/tinywarden/blob/main/docs/build-skills/actions.md)
- [Show actions on server pages and in Fleet](https://github.com/TinyWarden/tinywarden/blob/main/docs/build-skills/actions.md#show-actions-on-server-pages-and-in-fleet)
- [Every public Python SDK call and author command](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/skills/sdk.md)
- [Monitoring host operations and exact arguments/results](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/skills/host-access.md)
- [Action descriptors, context methods and return values](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/skills/actions.md)
- [Capture native output and read execution transcripts](https://github.com/TinyWarden/tinywarden/blob/main/docs/build-skills/actions.md#report-useful-progress)
- [Skill and runtime compatibility](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/skills/compatibility.md)
- [Agent operations](https://github.com/TinyWarden/tinywarden/blob/main/docs/operating/agents.md)
- [Every agent HTTP endpoint](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/agent-endpoints.md)
- [Contribute to the agent and SDK](../CONTRIBUTING.md)
- [LLM assistant instructions](../AGENTS.md)

This repository owns the Go host agent and generic Python SDK/runtime. Official
monitoring and action packages live in the separate skills repository. Deployment templates
are in deploy/, SDK source and unit tests in sdk/python/, runnable examples in
examples/skills/, and native process acceptance in tests/integration/.


Agent 0.0.24 adds a generic bounded work-wait endpoint and supports the optional
action-local verification policy (explicit `local` or the application default).
Explicit `host_health` is retired for new app operations, although historical SDK
descriptors may still parse it. The operation and its evidence are declared by each
skill and documented in the [action contract](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/skills/actions.md#choosing-verification).
The agent carries no APT refresh or upgrade recipe. Omitted fields retain descriptor syntax compatibility and now use the app's
action-local default. Packages that explicitly declare the field need 0.0.24+.
Failed waits or hint-cache faults use bounded backoff before ordinary authenticated
job polling and recovery delivery; the optional delivery hint does not grant execution.

Agent 0.0.25 adds optional structured action logging: catalogue messages and closed
runtime diagnostic codes, a bounded private job log and an independent authenticated
upload lane. The skill owns domain messages; logging does not change execution,
approval, verification, contact or freshness. Existing compatible actions can run
without detailed logging. See the human guide linked above for authoring and the
[logging contract](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/skills/actions.md#operation-logging)
for all SDK/HTTP fields, limits and delivery behavior.

Agent 0.0.26 adds generic native stdout/stderr capture, runtime tracebacks without
locals and optional `context.output(text, stream="stdout")`. Skills keep command
execution and domain policy; the runtime supplies only bounded diagnostics and
process supervision. See the [Full transcript author guide](https://github.com/TinyWarden/tinywarden/blob/main/docs/build-skills/actions.md#report-useful-progress)
and [complete SDK/HTTP logging reference](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/skills/actions.md#operation-logging)
for streams, limits, redaction, delivery and old-run compatibility.


Agent 0.0.27 adds prompt monitoring work hints, priority for requested checks and
responsive result-upload retries. Every existing compatible skill uses these
transport changes without package edits or new SDK calls. See the
[work-wait HTTP contract](https://github.com/TinyWarden/tinywarden/blob/main/docs/reference/agent-endpoints.md#prompt-work-delivery)
and [operator diagnostics](https://github.com/TinyWarden/tinywarden/blob/main/docs/operating/agents.md#monitoring-checks).
