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
