# Reboot status

Checks whether installed software has requested a reboot using Debian’s
[reboot-request convention](https://www.debian.org/doc/debian-policy/ch-opersys.html#s-signalingreboot).
A request raises Warning. No request passes this check without guaranteeing that
restarting is unnecessary.

## Widget and package evidence

The current result says Requested or No request detected. Details lists recorded
requesting packages alphabetically when available. Missing, unreadable, invalid or
partial package information does not clear a current reboot warning. Normal
results have no Details. Names are server-reported evidence; the list is optional
and does not establish every cause of a reboot request.

The collector checks /run/reboot-required, optionally reads up to 16 KiB from
/run/reboot-required.pkgs, then confirms that the request still exists before
showing names. A disappeared request discards the list. It accepts bounded package
identifiers, removes duplicates, and shows up to 100 names. Malformed lines and an
incomplete last line from a truncated read are discarded without displaying raw
contents. Byte truncation or malformed records marks the list partial; a row
limit also uses the shared table-limit message. Existing observations without
package fields remain readable.

History lists status checks, not confirmed restart events. Run now rechecks the
request; it does not reboot the server. No request time or restart time is inferred.
Settings and individual server inheritance are supplied by the engine.

Python SDK v1. Verified support: Debian 13 on amd64. This version uses the optional
files.read response and exact companion-file permission in the updated trusted
runtime. Update the app inspector and installed agent SDK before activating it;
no compiled agent binary change is required. No maintenance action is performed.

## Notification Details

`notifications.json` declares a bounded, translated Details paragraph from accepted
assessment evidence and effective settings. The app owns notification triggers,
severity, identities, headers and links; only bold, italic and underline are supported.
Missing or invalid bound values keep the generic notification. See the public app
[Details API](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-notification-details.md).
