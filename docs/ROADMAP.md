# Roadmap

This is a scope and sequencing guide, not a delivery schedule. No release dates
or implementation compatibility claims are established.

## Foundation Task 001

- Establish the minimal repository layout, MIT license, and agent instructions.
- Record approved product requirements as PS-001 through PS-006.
- Document architecture boundaries, website requirements, and open questions.
- Initialize local Git on `main` and commit if author information is configured.

The foundation contains no application implementation or deployed service.

## Next: technical decisions and focused research

- Define the legacy compatibility acceptance criteria, beginning with Windows
  XP SP3 and the Sony Ericsson K750i Java ME environment.
- Evaluate Windows toolchain and GUI options against those criteria.
- Propose the server language, storage, transport, and protocol with explicit
  tradeoffs; obtain approval before implementation.
- Resolve Unicode character counting, session synchronization, account-wide
  presence coordination, blocking, anti-spam thresholds, and status measurement details
  within the canonical PS-001 through PS-006 requirements in `PRODUCT.md`.
- Verify Kolobok asset licensing and redistribution rights before inclusion.

Use `docs/research/` for evidence and `docs/decisions/` for explicit decisions.
Research must not silently select the stack.

## Implementation Task 004: local First Contact experiment

The owner authorized a Go TCP relay, Go console harness, and CLDC 1.1/MIDP 2.0
MIDlet for a local PoC only. See [build instructions and measured verification](first-contact.md).
Production account, history, presence, and multi-session requirements remain
unchanged. Physical K750i testing and any network-accessible endpoint require a
separate task; local emulator results do not establish handset compatibility.

## Public Beta 0.1 scope

- Server with accounts, persistent concurrent device sessions, independent message
  synchronization, direct and self-messaging, offline delivery, paginated immutable
  history, contacts, account-wide presence, blocking, server-side rate limiting,
  new-recipient limits, email verification, and basic administrative suspension.
- Windows XP SP3–11 client with the approved classic interface and desktop
  integration.
- Lightweight Java ME client initially validated on Sony Ericsson K750i.
- Website with account management, downloads, Support Ukraine, and measured
  server status that protects Invisible users.
- Compatibility and behavior validation on relevant target environments before
  claiming support or publishing downloads.
- Core functionality must operate reliably; minor noncritical defects are
  acceptable under PS-001.

Implementation order and release readiness checks require later scoped tasks.
Foundation Task 001 does not authorize implementation or deployment.

## Planned directions beyond Beta 0.1

Android and Linux clients, developer documentation, independent third-party client
implementations using the open protocol, and future self-hosting are planned
directions, not Beta 0.1 deliverables. No dates or implementation stacks are selected.

Java ME Lite builds may be explored if device evidence justifies them; lightweight
packaging remains optional. The exclusions in `PRODUCT.md` remain in force.
