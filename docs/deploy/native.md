# Install and operate the host agent

The supported host platform is Debian 13 on amd64 with systemd and delegated cgroup
v2 CPU, memory and PID controls. Prepare Python 3.13, bubblewrap, libseccomp2,
OpenSSL, curl, CA certificates, tar and coreutils. The installer checks prerequisites
and does not install missing system packages.

## Install

In TinyWarden choose **Add server**, enter a name and obtain the setup code. Download
the official installer completely before running it:

```sh
tw_install_dir=$(mktemp -d)
curl --fail --location --proto '=https' --proto-redir '=https'   --output "$tw_install_dir/install.sh"   https://github.com/TinyWarden/tinywarden-agent/releases/latest/download/install.sh   && sudo bash "$tw_install_dir/install.sh"
rm -rf -- "$tw_install_dir"
```

Enter the setup code at the hidden prompt. Treat it as a credential and keep it out
of command arguments and logs. Automation can use `--setup-code-file` with a
root-owned regular file of mode 0600; remove the file after use. The initial
installer is obtained through official GitHub HTTPS. Its embedded key verifies
release signatures; established installations use their installed trusted key.

The default installation directory is `/opt/tinywarden-agent`. The installer
creates a dedicated service account and installs a complete binary/runtime release.
Monitoring runs without root privileges. Use `--install-dir` to select a dedicated
persistent root-owned directory or `--no-auto-updates` to disable automatic updates
locally. The installer validates permissions and the target layout.

Check server contact and skill observations in the app after enrollment. Services
needs per-server monitoring selections; discovering a service does not select it.

## Updates and recovery

For the default installation:

```sh
sudo /opt/tinywarden-agent/current/tinywarden-agent check
sudo /opt/tinywarden-agent/current/tinywarden-agent update
sudo /opt/tinywarden-agent/current/tinywarden-agent auto-updates off
sudo /opt/tinywarden-agent/current/tinywarden-agent auto-updates on
```

For a custom base pass the same `--install-dir` used during installation. Automatic
updates follow app policy and the local prohibition. Manual updates still verify
release signatures, compatibility and downgrade rules. Preserve the configuration,
identity, counters, skills and queued observations during updates.

Inspect the installed maintenance status and service journal when an update or
contact fails. `repair` resumes a recorded interrupted installation/update with a
verified target. Preserve uncertain state and recover forward; deleting identity
or queue files is not an update recovery procedure. Diagnostic output must not
expose credentials or raw private state.

## Manual installation

For an explicitly managed manual layout, build/package the reviewed source and
create a dedicated non-root `tinywarden-agent` account. Install the executable as
`/usr/local/bin/tinywarden-agent`, the trusted `sdk/python/runtime` directory as
`/usr/local/lib/tinywarden-agent/runtime`, and the supplied
[service template](../../infra/systemd/tinywarden-agent.service) as a system service.
Keep executable/runtime assets root-owned and not writable by the service account.

Use [config/agent.example.json](../../config/agent.example.json) for
`/etc/tinywarden-agent/agent.json`. Set the public HTTPS app origin and an absolute
state directory owned by the service account, normally `/var/lib/tinywarden-agent`.
Allow only the administrator and service account to read configuration; keep the
one-use enrollment token in a separate mode-0600 file.

Run enrollment as the service account:

```sh
/usr/local/bin/tinywarden-agent enroll --config /etc/tinywarden-agent/agent.json   --token-file /path/to/enrollment-token
```

Remove the token file after confirmed enrollment. Install/reload the unit and enable
`tinywarden-agent.service`. For uncertain enrollment run the agent with the same
configuration/state to recover. For a manual upgrade stop the service, replace the
reviewed executable/runtime/unit as needed and restart, preserving private state.

## Uninstall

```sh
sudo /opt/tinywarden-agent/current/tinywarden-agent --uninstall
```

The prompt offers stopping/disabling while retaining files or purging the managed
installation. Use `--keep-data` or `--purge` for an explicit automated choice.
Server records and history in the app are managed separately. Keep the installation
when identity/state is still needed for recovery.
