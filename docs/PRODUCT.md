# Approved product requirements

Status: approved product requirements, with canonical decision identifiers restored
as instructed in Foundation Task 002. Preserve the PS-001 through PS-006 mapping;
do not regroup other requirements under these identifiers.

## Mission and product boundaries

- Mission: **Make old devices useful again.**
- Build a lightweight, text-only messenger inspired by early-2000s ICQ and QIP.
- Favor small, simple, resource-efficient software with the same minimalist
  messaging experience on modern and legacy devices.
- Use OGM's own open protocol, independent of external messaging platforms,
  supporting independent third-party client implementations and future self-hosting.
- Legacy compatibility takes priority over sophisticated security.
- Use an MIT-licensed GitHub monorepo and English documentation. GitHub is the
  intended repository platform; Foundation Task 001 creates only a local repository.
- OGM is an experimental hobby project with no guarantee of message
  confidentiality, availability, or delivery.
- Exclude calls, video, file transfers, groups, stickers, reactions,
  advertisements, social feeds, AI features, complex bots, E2EE, and unnecessary
  enterprise functionality. Avoid unnecessary features and overengineering.

## PS-001 — Public Beta 0.1 Release Strategy

- Release a server, a Windows XP SP3–11 client, and a Java ME client, initially
  validated on Sony Ericsson K750i.
- Minor noncritical defects are acceptable. Core functionality must operate reliably.

## PS-002 — Multi-Session & Presence

- Allow multiple concurrent sessions per account.
- Presence is account-wide: Online, Away, DND, Invisible, or Offline.
- Invisible is displayed as Offline to other users.
- Do not provide Last Seen.
- Each session synchronizes messages independently.

## PS-003 — Contacts & Minimal Anti-Spam

- Allow unilateral contact addition by numeric OGM ID, without mutual approval.
- Permit messaging any registered OGM ID without mutual approval, subject to
  personal blocking and minimal anti-spam controls.
- Provide personal blocking, server-side rate limiting, new-recipient limits,
  email verification, and basic administrative suspension.
- Do not provide a public user directory or a contacts-only messaging mode.

## PS-004 — Message History & Limits

- Support text-only, one-to-one conversations.
- Keep server-side history without automatic age-based expiration.
- Support offline message delivery and paginated history.
- Limit each message to 500 Unicode characters.
- Sent messages are immutable. Do not provide individual message editing,
  individual message deletion, or delete-for-everyone.

## PS-005 — Account & Identity Management

- Assign random, unique numeric OGM IDs, starting with six digits and supporting
  future expansion to nine digits.
- Provide website registration, email-link activation, password recovery, email
  changes, and account deletion.
- Initially accept Gmail and Apple/iCloud email providers, with email verification.
- Require passwords of at least 15 characters, without mandatory character classes.
- Keep authenticated device sessions persistent.
- Password reset invalidates all sessions.
- Email change requires the current password and verification of the new address,
  with notification to the old address.
- Set and change nicknames in clients.
- Deleted accounts appear as "Deleted user" in retained correspondence, subject
  to applicable data-protection requirements.

## PS-006 — Self-Messaging

- Users may add their own OGM ID as an ordinary contact and send messages to
  themselves. Standard delivery and history semantics apply.
- Do not provide a dedicated Saved Messages feature, bot platform, or special
  automation API.

## Classic client experience

- Use a compact desktop interface, a classic contact list, and separate chat windows.
- Follow a Frutiger Aero / Y2K / ICQ / QIP-inspired visual direction.
- Include built-in animated Kolobok emoticons stored locally in clients. Only
  emoticon text sequences are transmitted.
- Provide a Windows installer, system tray integration, optional autostart,
  and notifications.
- Support an optional short textual user status.
- Keep the Java ME application lightweight; Lite builds are a possibility,
  not a committed release deliverable.
- Verify third-party emoticon redistribution rights before bundling assets;
  this foundation includes no emoticon files.

## Website and public information

- Provide account management, downloads, and Support Ukraine content.
- Include a live server-status section with real uptime and the number of visibly
  online accounts.
- Do not expose Invisible users through presence statistics.
- Include a Support Ukraine badge in the repository README.
- Never present invented server metrics or link to unverified services.

See [website requirements](WEBSITE.md) for the website-specific checklist.

## Planned directions beyond Beta 0.1

Android and Linux clients, developer documentation, independent client
implementations, and future self-hosting are planned directions, not Beta 0.1
deliverables. No implementation technologies or release dates are selected here.

## Boundaries and unresolved details

These requirements do not select an implementation stack. Go, SQLite, TCP, and
GUI frameworks are candidates only. Languages, data storage, transport, protocol
encoding, authentication mechanisms, and deployment remain undecided.

Implementation needs further decisions on Unicode character counting, exact
accepted email domains, identifier allocation and expansion, deletion and data
handling details consistent with PS-005, blocking behavior, rate-limit and
new-recipient thresholds, session synchronization, account-wide presence
coordination, and privacy-preserving status measurement. These details must
preserve the approved behavior above; do not reopen it as undecided or silently
turn implementation proposals into product requirements.
