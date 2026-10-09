# Native agent installation and operations

Supported first target: Debian 13 amd64. Build/package in this repository.
Installation or upgrade requires authorization for the specific host; the
packaging command performs no remote action.

## Install

Create a dedicated non-root tinywarden-agent service account, a private
/var/lib/tinywarden-agent directory owned by that account, and an
/etc/tinywarden-agent directory readable only by the administrator and agent.
Store agent.json there, using the approved public HTTPS control-plane origin
and the absolute private state directory. Set configuration permissions so
the agent can read it and other accounts cannot.

Install the reviewed binary as /usr/local/bin/tinywarden-agent and
infra/systemd/tinywarden-agent.service as the system service. Enroll once as
the service account with an owned mode-0600 token file, then remove that token
file. Enable/start tinywarden-agent.service after enrollment is confirmed.

The service template uses User/Group=tinywarden-agent, NoNewPrivileges, a
private umask, whole-cgroup cleanup and a bounded stop. Restart=on-abnormal
lets transient network recovery remain inside the agent while terminal
authority/configuration failures stop for operator attention.

## Upgrade and recover

### Python package platform

SDK v1 needs distro Python 3.13, bubblewrap and libseccomp2 on Debian 13 amd64.
Install these host prerequisites explicitly, then install the packaged
sdk/python/runtime directory at /usr/local/lib/tinywarden-agent/runtime, owned by
root and not writable by the agent. Its artifact.json pins each trusted launcher
and policy asset. The native unit delegates cpu/memory/pids controllers and uses
DelegateSubgroup=supervisor; execution remains unavailable without enforced limits.

Installing platform assets selects the generic package lane on the next restart.
Existing identity, heartbeat and old queues are preserved; compiled collectors no
longer run concurrently. A corrupt runtime/queue cannot fall back to a clean legacy
reading. Heartbeat continues and runtime unavailability is reported. Absence of
platform assets retains compiled compatibility for an existing installation.

Enabled compatible packages are downloaded automatically from the authenticated
control plane into the private state directory's skills/<content_sha256> cache.
Archive bytes and executable content are verified independently; validation and
publication use the trusted delegated supervisor, never installation hooks. ZIP
upload and digest-specific permission review happen in the app. The agent does not
accept arbitrary URLs or forward its credential to a package process. Failed
transfers leave the existing cache and identity intact.

skill_operations is a local operation ceiling: omission enables the six read-only
SDK operations, while an explicit [] denies all host observations. Administrator
grants can never exceed that ceiling. Prepared official packages may still be
provisioned explicitly with the supervisor's publish action.

Read the [runtime contract](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-runtime.md)
before enabling community code. A new compatible package needs no binary rebuild.

Stop the existing service; preserve /etc/tinywarden-agent and all owned
/var/lib/tinywarden-agent files, ownership and permissions. Replace only the
reviewed binary/unit, reload systemd if its unit changed, and restart. Check
version, contact, assignment delivery and later skill readings. Do not reset
state, credentials, generation, queues or sequence numbers.

A host restart starts the enabled service automatically. Inspect service
status/logs when contact fails; stale readings can also reflect unavailable
configuration or unsupported observations. Never print tokens, credentials
or unrestricted state in diagnostics.

Credential replacement is explicit: stop, issue a host-bound replacement
token through the app, run replace as the agent account, preserve uncertain
state and start run to recover it. Do not delete state to clear a pause.
Filesystem trim observation is schedule-aware on the server; it does not
run fstrim or require a fresh weekly execution after every reboot.

### Manual collection support

Agent0.0.5 advertises `skill_runs.manual.v1` and supports the app's **Run now** action
through its existing collector and sandbox. Deploy app/schema018 first, then upgrade
this binary preserving identity/configuration/state. Packages and SDK need no changes.
New agents read older state; after manual state is written, a binary-only downgrade
is unsupported. Recover forward without resetting counters or restoring old state.
See the [manual-run API](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-manual-runs.md).

### Contact recovery and diagnostics

Agent0.0.6 keeps the existing protocol and state format. After five retryable
heartbeat failures it retries every30–33 seconds with the default cadence, while
honoring a longer valid server Retry-After. Successful exact replay is saved before
one fresh heartbeat is scheduled a second later. Other lanes continue independently.

The service journal includes bounded heartbeat failure/recovery diagnostics:
category, stage, attempt, duration, retry delay and HTTP status when available.
DNS, timeout, connection rejection, response interruption and local state failures
can be distinguished without printing secrets or raw errors. A terminal authority
or protocol failure still requires operator correction. Upgrade the app/schema020
first, then replace only the agent binary and restart its existing service.
