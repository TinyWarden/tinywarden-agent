# Managed installation and updates

Supported platform: **Debian 13, x64 (amd64)**. A release contains one static Go
executable, trusted Python SDK assets, systemd templates and legal notices. Python
and sandbox dependencies come from Debian. Ordinary skill packages remain separate.

## Install

In TinyWarden, select **Add server**, enter its name, and copy the setup code and
installation command. Download the entire official installer before running it:

```sh
tw_install_dir=$(mktemp -d)
curl --fail --location --proto '=https' --proto-redir '=https' \
  --output "$tw_install_dir/install.sh" \
  https://github.com/TinyWarden/tinywarden-agent/releases/latest/download/install.sh \
  && sudo bash "$tw_install_dir/install.sh"
rm -rf -- "$tw_install_dir"
```

Inspect the installer before executing it if desired. A versioned release URL
can replace `latest`. The first installer is trusted through official GitHub HTTPS;
its embedded verification key does not independently prove that initial download.
Established installations use their already installed verification key.

The installer prompts for the setup code with input hidden. It displays the app
destination and verifies TLS. The code contains an encoded HTTPS origin and a
single-use enrollment token that expires after 15 minutes. It is sensitive;
encoding is not encryption. Never put it in command arguments or environment
variables. Automation can use `--setup-code-file /root/setup-code.txt`, a root-owned
regular file with permissions 0600. Remove that file after installation.

Prerequisites: Python 3.13, bubblewrap, libseccomp2, OpenSSL, systemd with delegated
cgroup v2 CPU/memory/PID controls; bootstrap also needs curl, CA certificates, tar
and coreutils. Missing requirements stop installation with instructions. **The
installer never installs dependencies or changes existing directory permissions.**

The default location is `/opt/tinywarden-agent`. To choose another dedicated,
persistent root-owned directory, add `--install-dir /srv/tinywarden-agent`.
Permissions, ACLs, space, filesystem execution and path ancestry are checked.
Paths with spaces are supported; quotes, backslashes and control characters are rejected. Shared roots, writable parents, symlinks and
temporary filesystems are rejected. Relocation and parallel installations are
not supported. Add `--no-auto-updates` to prohibit automatic updates locally.

```text
<base>/
  config.json              root-owned; group-readable, 0640
  current -> releases/<version>/
  releases/<version>/      executable, runtime, units, platform identity, notices
  data/                    private service-account identity, skills and queues
  updater/                 root-owned update status, receipt and recovery records
```

Only the three unit registrations under `/etc/systemd/system` live outside the
base. The system account and journal are OS-managed. The agent runs as
`tinywarden-agent`, without root privileges. Update mode runs briefly as root and
does not execute skill code. Privileged skills are not supported by this platform.

## Automatic and manual updates

Automatic updates default On. The app's general Settings page controls the default;
each server can inherit it or choose On/Off. The local root-owned prohibition wins.
The daily timer includes random delay and remembers attempts across reboot.
Only a signed, compatible stable release with a fresh direct app policy can install.

```sh
sudo /opt/tinywarden-agent/current/tinywarden-agent check
sudo /opt/tinywarden-agent/current/tinywarden-agent update
sudo /opt/tinywarden-agent/current/tinywarden-agent auto-updates off
sudo /opt/tinywarden-agent/current/tinywarden-agent auto-updates on
```

For a custom base add `--install-dir /srv/tinywarden-agent` to these commands.
Manual updates can bypass automatic-policy Off, but never signature checks,
compatibility or downgrade prevention. Downloads and validation happen before
stopping monitoring. Activation switches the complete executable/runtime release.
Identity, credentials, counters and queues are retained. An uncertain installation
shows **Installed, awaiting confirmation** until the actual agent reports the new
binary/runtime. Normally only the active release is retained; the previous release
stays temporarily for interrupted recovery or while an old process still runs.

On failure inspect maintenance status and service logs. `repair` makes a bounded
forward attempt using the verified recorded target. It does not downgrade software,
restore old state or create a new enrollment. Preserve an unfinished installation
and use `repair` to resume its existing enrollment. Never delete identity to repair
an update. A disabled or masked service is not enabled by scheduled updates.

## Adopt the known manual installation

The official installer accepts `--adopt` for the known 0.0.7 layout at
`/usr/local/bin/tinywarden-agent`, `/etc/tinywarden-agent/agent.json` and
`/var/lib/tinywarden-agent`. It rejects custom runtime paths and service overrides.
Adoption quiesces monitoring, copies state as the unprivileged account, preserves
root-owned recovery copies, and binds the existing host and agent IDs. It consumes
no setup code. After confirmation, the old executable, config, data and verified
SDK are removed; protected recovery copies of the old executable/config/unit remain
inside the managed base. Unknown absolute references in stored queues require manual migration.

## Uninstall

```sh
sudo /opt/tinywarden-agent/current/tinywarden-agent --uninstall
```

The prompt offers:

1. **Stop and disable** the monitoring service and update timer, keeping installation
   files and enrolled state. They can later be enabled again without enrollment.
2. **Purge** the local installation, including credentials, skills and queues.

Both choices remind you that **server records and history in the app are retained**.
For automation, choose `--keep-data` or `--purge` explicitly. Only the recognized
managed directory and unit links are removed. The OS account, prerequisites and
journal logs remain. Removing a server record from the app is a separate action.

## Release authenticity

Release assets are fixed to `TinyWarden/tinywarden-agent`. Exact manifest bytes
are Ed25519-signed; the manifest binds archive size/hash, binary/runtime identity,
platform and source-version range. Hashes alone do not establish authenticity.
The private signing key belongs only to release maintainers. Key rotation requires
a trusted software change; unsigned release payloads cannot nominate a new key.
If the key is lost or compromised, disable automatic updates locally and publish
recovery instructions through a trusted channel.
