# Approved product requirements

Status: approved requirements supplied in Foundation Task 001.

The task supplied the product requirements without separate historical PS texts.
PS-001 through PS-006 below organize those requirements by topic for reference;
the numbering does not introduce additional product or technical approval.

## PS-001 — Mission, scope, and release targets

- Mission: **Make old devices useful again.**
- Build a lightweight, text-only messenger inspired by early-2000s ICQ and QIP.
- Favor small, simple, resource-efficient software with the same minimalist
  messaging experience on modern and legacy devices.
- Use OGM's own open protocol, independent of external messaging platforms.
- Legacy compatibility takes priority over sophisticated security.
- Use an MIT-licensed GitHub monorepo and English documentation. GitHub is the
  intended repository platform; Foundation Task 001 creates only a local repository.
- Public Beta 0.1 includes a server, a Windows XP SP3–11 client, and a Java ME
  client initially tested on Sony Ericsson K750i.
- OGM is an experimental hobby project, not a guaranteed secure or reliable
  communication service.
- Exclude calls, video, file transfers, groups, stickers, reactions,
  advertisements, social feeds, AI features, complex bots, E2EE, and unnecessary
  enterprise functionality. Avoid unnecessary features and overengineering.

## PS-002 — Accounts and sessions

- Assign random, unique numeric OGM IDs, starting with six digits and supporting
  future expansion to nine digits.
- Provide website registration, email-link activation, password recovery, email
  changes, and account deletion.
- Initially accept Gmail and Apple/iCloud email providers.
- Require passwords of at least 15 characters, without mandatory character classes.
- Keep authenticated device sessions persistent.
- Allow multiple simultaneous sessions per account.
- Password reset invalidates all sessions.
- Set and change nicknames in clients.

## PS-003 — Messaging and history

- Support text-only, one-to-one conversations.
- Limit each message to 500 Unicode characters.
- Keep server-side history without automatic age-based expiration.
- Support offline message delivery and paginated history.
- Do not support message editing or individual message deletion.
- Allow self-messaging: users may add their own OGM ID as a contact.

## PS-004 — Contacts, presence, and abuse controls

- Allow unilateral contact addition by OGM ID, without approval.
- Permit messages to any registered OGM ID, subject to blocking and minimal
  anti-spam controls.
- Provide blocking and minimal anti-spam protection.
- Support Online, Away, DND, Invisible, and Offline presence.
- Invisible appears Offline to other users.
- Do not provide Last Seen.
- Account for multiple simultaneous sessions; aggregation details remain undecided.

## PS-005 — Classic client experience

- Use a compact desktop interface, a classic contact list, and separate chat windows.
- Follow a Frutiger Aero / Y2K / ICQ / QIP-inspired visual direction.
- Include built-in animated Kolobok emoticons stored locally in clients. Only
  emoticon text sequences are transmitted.
- Provide a Windows installer, system tray integration, optional autostart,
  and notifications.
- Keep the Java ME application lightweight; Lite builds are a possibility,
  not a committed release deliverable.
- Verify third-party emoticon redistribution rights before bundling assets;
  this foundation includes no emoticon files.

## PS-006 — Website and public information

- Provide account management, downloads, and Support Ukraine content.
- Include a live server-status section with real uptime and the number of online
  accounts.
- Do not expose Invisible users through presence statistics.
- Include a Support Ukraine badge in the repository README.
- Never present invented server metrics or link to unverified services.

See [website requirements](WEBSITE.md) for the website-specific checklist.

## Boundaries and unresolved details

These requirements do not select an implementation stack. Go, SQLite, TCP, and
GUI frameworks are candidates only. Languages, data storage, transport, protocol
encoding, authentication mechanisms, and deployment remain undecided.

Implementation needs further decisions on Unicode character counting, exact
accepted email domains, identifier allocation and expansion, account-deletion
effects on shared history, blocking behavior, anti-spam limits, session and
presence aggregation, and privacy-preserving status measurement. Do not silently
turn these open questions into product requirements.
