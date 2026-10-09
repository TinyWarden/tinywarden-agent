# Package updates

Simulates pending APT upgrades using the server’s locally cached package lists. It does not install packages or refresh lists, and does not verify repository freshness or security-update counts.

Python SDK v1. Verified support: Debian 13 on amd64. No maintenance action is performed.
Settings and individual server inheritance are supplied by the TinyWarden engine.

The read-only command declares dpkg, APT's file/mirror/HTTP(S) method helpers and
`/etc/apt/mirrors` so Debian mirror-based sources can resolve cached package plans.
The helpers run inside the same isolated command: network sockets remain denied,
host inputs remain read-only, and APT hooks/authentication files are not exposed.
SDK assets must support explicitly approved helpers under `/usr/lib` and preserve
the distro's merged `/usr` aliases. Failed or incomplete simulations remain Unknown.

## Notification Details

`notifications.json` declares a bounded, translated Details paragraph from accepted
assessment evidence and effective settings. The app owns notification triggers,
severity, identities, headers and links; only bold, italic and underline are supported.
Missing or invalid bound values keep the generic notification. See the public app
[Details API](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-notification-details.md).

## Pending package list

Details displays package names, installed/available versions and planned upgrades,
new dependencies, removals or held-back packages using the shared table API.
Only recognized `Inst`/`Remv` records and the held-back names stanza are extracted
from the existing simulation. No additional command or access permission is used.
Missing versions appear as —; APT's held-back names alone do not supply versions
or reasons. The list keeps at most 100 rows, labels truncation and reports partial
parsing separately. Unrecognized details never alter authoritative counts or
severity and arbitrary command output is never published. With no pending actions
and a complete parse, Details is omitted. Compatible older graph data is retained.
