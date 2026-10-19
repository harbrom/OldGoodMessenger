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
- Resolve account/session behavior, Unicode limits, presence aggregation,
  blocking, minimal anti-spam, and status measurement semantics.
- Verify Kolobok asset licensing and redistribution rights before inclusion.

Use `docs/research/` for evidence and `docs/decisions/` for explicit decisions.
Research must not silently select the stack.

## Public Beta 0.1 scope

- Server with accounts, persistent device sessions, direct messaging, offline
  delivery, paginated persistent history, contacts, presence, blocking, and
  minimal anti-spam.
- Windows XP SP3–11 client with the approved classic interface and desktop
  integration.
- Lightweight Java ME client initially tested on Sony Ericsson K750i.
- Website with account management, downloads, Support Ukraine, and measured
  server status that protects Invisible users.
- Compatibility and behavior validation on relevant target environments before
  claiming support or publishing downloads.

Implementation order and release readiness checks require later scoped tasks.
Foundation Task 001 does not authorize implementation or deployment.

## Possible later work

Java ME Lite builds may be explored if device evidence justifies them. No other
post-beta features are approved. The exclusions in `PRODUCT.md` remain in force.
