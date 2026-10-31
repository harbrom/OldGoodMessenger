# Architecture boundaries and open decisions

This document records responsibilities implied by approved product requirements.
It is not an approved production implementation design. Implementation Task 004
authorizes the [First Contact local PoC](first-contact.md): Go 1.27.x, framed TCP,
a Go console harness, and CLDC 1.1/MIDP 2.0. That experiment does not select the
production architecture or establish legacy-device compatibility.
The canonical PS-001 through PS-006 mapping in [PRODUCT.md](PRODUCT.md) governs
these constraints; implementation details must preserve those decisions.

## Repository responsibilities

| Path | Intended responsibility |
| --- | --- |
| `server/` | Accounts, sessions, messaging, history, contacts, presence, and abuse controls |
| `clients/windows/` | Compact Windows client targeting XP SP3–11 |
| `clients/java-me/` | Lightweight Java ME client, initially tested on Sony Ericsson K750i |
| `website/` | Account management, downloads, Support Ukraine, and real server status |
| `docs/` | Requirements, research, roadmap, and decision records |

These directories do not prescribe service boundaries, deployment units, APIs,
shared libraries, or a production build system. The PoC also uses `internal/`,
`clients/console/`, and `scripts/`; unimplemented areas retain directory markers.

## Approved constraints

- Clients communicate using OGM's own open protocol, independent of other
  messaging platforms. Independent third-party clients and future self-hosting
  remain planned directions. Protocol format and transport are undecided.
- The server retains history without automatic age-based expiration and supports
  offline delivery and pagination. Sent messages are immutable, limited to 500
  Unicode characters, and have no individual deletion or delete-for-everyone.
  Self-messaging uses standard delivery and history semantics, without a special
  Saved Messages feature, bot platform, or automation API.
- Accounts support persistent concurrent device sessions; password reset
  invalidates all sessions. Each session synchronizes messages independently;
  presence is account-wide, with Online, Away, DND, Invisible, and Offline states.
- Invisible presence must appear Offline to others and must not be exposed by
  public statistics. There is no Last Seen feature.
- Contacts are unilateral and messaging requires no mutual approval. Provide
  personal blocking, server-side rate limiting, new-recipient limits, email
  verification, and basic administrative suspension. There is no public user
  directory or contacts-only messaging mode.
- Email change requires the current password, new-address verification, and
  notification to the old address. Deleted accounts appear as "Deleted user" in
  retained correspondence, subject to applicable data-protection requirements.
- Emoticon images are client-local; only their text sequences are transmitted.
- Resource use and legacy compatibility constrain every technology evaluation.
- An optional short textual user status remains part of the client requirements.
- E2EE is excluded. Transport protection, credential storage, and session
  mechanisms still need explicit design decisions.
- OGM offers no guarantee of message confidentiality, availability, or delivery.

## Production technology status

| Area | Status | Candidates or next evaluation |
| --- | --- | --- |
| Server language/runtime | Undecided | Go is a candidate, not approved |
| Persistent storage | Undecided | SQLite is a candidate, not approved |
| Client/server transport | Undecided | TCP is a candidate, not approved |
| Protocol format, framing, encoding, versioning | Undecided | Evaluate legacy constraints and Unicode needs |
| Windows language, toolchain, GUI framework | Undecided | Verify XP SP3 compatibility and resource use |
| Java ME toolchain and device profiles | Undecided | Research the target K750i environment |
| Website stack and server integration | Undecided | Derive from approved account and status requirements |
| Password storage, transport protection, session mechanisms | Undecided | Evaluate legacy interoperability and explicit tradeoffs |
| Packaging, build tooling, CI, hosting, operations | Undecided | Define only when required by an authorized task |

No production framework, dependency, schema, wire format, or hosting provider is
selected. The PoC's exact experimental framing and build tools are documented
separately, with authorization provenance and limitations.

## Questions to resolve before dependent implementation

- What Windows toolchain and GUI can meet the target OS range and small footprint?
- What Java ME capabilities and resource budgets are actually available on K750i?
- How are Unicode characters counted for the 500-character limit across clients?
- How are random IDs allocated without collisions and expanded to nine digits?
- Which exact Gmail and Apple/iCloud domains are accepted, and how are addresses
  normalized?
- How do concurrent sessions coordinate account-wide presence and synchronize
  messages independently, as required by PS-002?
- How are blocking, rate limiting, new-recipient limits, and administrative
  suspension implemented within PS-003?
- How are deletion and retained correspondence handled consistently with PS-005's
  "Deleted user" label and applicable data-protection requirements?
- How are uptime and online account counts measured without exposing Invisible
  users, including through aggregation or changes in counts?

The recommended first technical decision is the Windows compatibility baseline
and toolchain/GUI selection, supported by a focused feasibility evaluation.
Record evidence in research notes and explicit approval in a decision record.
