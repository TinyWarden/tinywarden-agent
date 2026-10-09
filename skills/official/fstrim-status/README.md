# Filesystem trim

Checks the server’s automatic trim schedule and its last recorded result.
Successful completion evidence survives a server reboot. A weekly schedule is
assessed against its expected run, rather than the age of a status reading.

## Widget

- **Last trim:** completed successfully, failed, or no completed run recorded.
- **Completed at:** the server-reported finish time, when available. This is not
  the time TinyWarden noticed the result. A retained result survives restarts for
  up to 90 days; missing completion evidence never gets an invented timestamp.
- **Automatic trim:** enabled, disabled, not active, unable to confirm, or running
  when last checked.
- **Next scheduled run:** the confirmed upcoming time from the server’s timer.
  An overdue outstanding run is shown separately in Details, not as the next run.

Details appears only for a problem or an unconfirmed result, with a readable
explanation. An overdue result includes its expected run time. Normal results
have no supporting technical-state dump. All content uses SDK display format 2;
no custom app renderer is required.

History lists monitoring checks, not actual trim executions. Run now rechecks
status; it does not run trim or change the schedule. A successful service result
does not prove that device space was physically reclaimed or how many bytes were
reclaimed. The skill cannot read logs to establish a failure’s underlying cause.

Python SDK v1. Verified support: Debian 13 on amd64. No maintenance action is
performed. Settings and individual server inheritance are supplied by the engine.

## Notification Details

`notifications.json` declares a bounded, translated Details paragraph from accepted
assessment evidence and effective settings. The app owns notification triggers,
severity, identities, headers and links; only bold, italic and underline are supported.
Missing or invalid bound values keep the generic notification. See the public app
[Details API](https://github.com/TinyWarden/tinywarden/blob/main/docs/architecture/skill-notification-details.md).
