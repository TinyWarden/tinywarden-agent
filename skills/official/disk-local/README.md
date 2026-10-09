# Disk space

Reads space usage for each supported writable local filesystem and compares it with warning and critical thresholds. Writable filesystems determine attention; mounts that share capacity are identified. It does not check inodes, quotas or network filesystems.

Python SDK v1. Verified support: Debian 13 on amd64. No maintenance action is performed.
Settings and individual server inheritance are supplied by the TinyWarden engine.

## Notification Details

`notifications.json` declares a bounded, translated Details paragraph from accepted
assessment evidence and effective settings. The app owns notification triggers,
severity, identities, headers and links; only bold, italic and underline are supported.
Missing or invalid bound values keep the generic notification. See the public app
[Details API](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-notification-details.md).

## Filesystem scope

Read-only mounts are omitted from collected observations, current tables and new
graph samples. This excludes systemd credential mounts without path-specific
exceptions. Writable memory filesystems such as `/tmp` and `/run` remain monitored,
because they can fill up. Unknown/missing capacity on a writable mount remains
explicit; filtering does not turn incomplete coverage into Healthy. Stable series
identities and metric bindings are unchanged for retained writable-filesystem history.

## Server Details

Details is omitted when the filesystem readings are complete. If a writable
filesystem cannot be checked, it lists that mount path with a translated reason
(for example, it disappeared during the check or its space usage could not be
read). Incomplete filesystem lists and changes during collection receive a
readable explanation. Empty diagnostic facts and tables are hidden by the shared
renderer; healthy usage tables are never duplicated in Details.

Routine coverage labels and excluded kernel/network filesystem counts remain
internal evidence, without appearing in Details. This presentation is defined
entirely by the skill's evaluator, `display.json` and message catalog. Observation
schema, thresholds, permissions and historical metric bindings are unchanged.
