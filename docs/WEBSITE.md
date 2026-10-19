# Website requirements

The website is part of Public Beta 0.1. This document records requirements only;
there is no implemented website, deployment, public URL, or verified service link.
The stack, hosting, email delivery provider, and server integration are undecided.

## Account management

- Registration with a random, unique numeric OGM ID, initially six digits and
  supporting future expansion to nine.
- Email-link activation and password recovery.
- Email changes and account deletion.
- Initially accept Gmail and Apple/iCloud email providers; the exact domain
  allowlist and validation rules remain undecided.
- Require passwords of at least 15 characters without mandatory character classes.
- Password reset invalidates all authenticated device sessions.
- Nicknames are set and changed in clients.

Token mechanisms, account-management flows, and deletion effects on retained
history require explicit decisions before implementation.

## Downloads and project information

- Provide downloads for the approved Windows and Java ME clients when actual
  release artifacts exist.
- Describe the mission and the intended legacy-device experience.
- Explain that OGM is an experimental hobby project with no guarantee of secure
  or reliable communication.
- Do not advertise untested compatibility or publish invented download links.

## Support Ukraine

Include Support Ukraine content. The repository README uses a local badge.
External organizations, donation destinations, and other service links must be
verified before publication; none is selected by this foundation.

## Live server status

- Show real uptime and the number of online accounts from actual measurements.
- Do not expose Invisible users through presence statistics. Invisible accounts
  must not be counted as visibly online or revealed by another public metric.
- Multiple sessions must not inflate a count of accounts into a count of devices.
- Do not introduce Last Seen through status data or another website feature.
- If measurements are unavailable, report that status is unavailable instead of
  displaying fabricated uptime, online counts, or an invented healthy state.

The definition of uptime, treatment of Away/DND, measurement source, refresh
cadence, staleness rules, and privacy safeguards remain undecided. Do not expose
public statistics until their behavior satisfies the Invisible requirement.

This foundation records no live measurements and operates no status endpoint.
