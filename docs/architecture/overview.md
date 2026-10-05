# Agent architecture

| Module | Responsibility |
| --- | --- |
| cmd/tinywarden-agent | Small executable adapter. |
| internal/cli | Catalog-backed commands and fixed child-helper dispatch. |
| internal/agent | HTTPS protocol, credentials, durable state, assignment caches, sequences, bounded queues, priority heartbeat and independent workers. |
| internal/runner | Fixed recipe validation, contained non-root subprocesses, deadlines, clean environment, output limits and process-group cleanup for compiled compatibility. |
| internal/skills/runtime | Asset integrity/readiness, shared execution slot and the bounded trusted Python supervisor client. |
| sdk/python/runtime | Canonical SDK, admission, cgroup/namespace/seccomp isolation and read-only host broker. |
| sdk/python/authoring and skill_tool.py | Starter, static validation, deterministic ZIP packaging and isolated local tests. |
| skills/official | Four independent Python packages with stable legacy aliases. |
| skills/examples | Independently installable SDK example, never installed automatically. |
| internal/skills/protocol | Immutable compiled observation shapes shared by transport and skill adapters. |
| internal/skills/builtin | Registry and whole-recipe/normalization guards for compiled compatibility. |
| internal/skills/builtin/{disk,packages,reboot,trim} | Skill-owned collectors, recipes and bounded normalizers. |
| internal/skills/builtin/shared | Bounded common output parsing. |

The server owns authority, policy, assessment, history and notifications. The
agent owns local execution, durable requests and safe reporting. Python package
execution uses the generic bounded SDK. ZIP upload, disabled-first review and
authenticated package delivery are implemented; remote maintenance actions are not.

The selected [installable skill design](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-platform.md)
defines the implemented engine/package boundary, official source ownership and unchanged
core requirement. The [Python package runtime](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-runtime.md)
requires isolation before activation. The agent remains Go; Python is for standalone
skill packages. Existing compiled recipes are not a sandbox for community code.
See [skill authoring](../skills/authoring.md) and the [SDK reference](../skills/sdk-v1.md).

## Protocol ownership

Canonical server contracts live in the app repository:
[enrollment/heartbeat/disk](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/agent-protocol.md),
[baseline assignment and state](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/baseline-protocol.md),
and [recipe execution](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/recipe-execution.md).
Endpoints, schema versions, capability IDs, digest tuples, credential generations
and durable state formats are compatibility contracts.

Authored JSON examples under internal/*/testdata are used locally. The app vendors
those needed by its tests, pinned by Git revision and per-file SHA-256; intentional
changes must update both consumers and their affected tests. Ordinary builds and
tests never read another checkout or fetch examples from the network.

State is private (owned mode-0700 directory, mode-0600 files), atomically written
and protected by one process lock. Requests persist before transmission and
uncertain requests replay exact bytes. Transient outages retry within bounds;
terminal authority/configuration errors stop or pause their owning lane. Fixed
recipes never refresh APT indexes, install packages, trim or reboot. Raw output
is transient and excluded from reported results.
