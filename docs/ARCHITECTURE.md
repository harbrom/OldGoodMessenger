# Architecture boundaries and open decisions

This document records responsibilities implied by approved product requirements.
It is not an approved implementation design. There is no application code yet.

## Repository responsibilities

| Path | Intended responsibility |
| --- | --- |
| `server/` | Accounts, sessions, messaging, history, contacts, presence, and abuse controls |
| `clients/windows/` | Compact Windows client targeting XP SP3–11 |
| `clients/java-me/` | Lightweight Java ME client, initially tested on Sony Ericsson K750i |
| `website/` | Account management, downloads, Support Ukraine, and real server status |
| `docs/` | Requirements, research, roadmap, and decision records |

These directories do not prescribe service boundaries, deployment units, APIs,
shared libraries, or a build system. Reserved implementation paths contain only
Git directory-retention files.

## Approved constraints

- Clients communicate using OGM's own open protocol, independent of other
  messaging platforms. Its format and transport are undecided.
- The server retains history without automatic age-based expiration and supports
  offline delivery and pagination.
- Accounts support persistent concurrent device sessions; password reset
  invalidates all sessions.
- Invisible presence must appear Offline to others and must not be exposed by
  public statistics. There is no Last Seen feature.
- Emoticon images are client-local; only their text sequences are transmitted.
- Resource use and legacy compatibility constrain every technology evaluation.
- E2EE is excluded. Transport protection, credential storage, and session
  mechanisms still need explicit design decisions.

## Technology status

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

No framework, dependency, schema, wire format, or hosting provider is selected.

## Questions to resolve before dependent implementation

- What Windows toolchain and GUI can meet the target OS range and small footprint?
- What Java ME capabilities and resource budgets are actually available on K750i?
- How are Unicode characters counted for the 500-character limit across clients?
- How are random IDs allocated without collisions and expanded to nine digits?
- Which exact Gmail and Apple/iCloud domains are accepted, and how are addresses
  normalized?
- How do concurrent device sessions determine presence and message delivery state?
- What do blocking and minimal anti-spam controls do in concrete scenarios?
- What does account deletion remove or retain, particularly shared history?
- How are uptime and online account counts measured without exposing Invisible
  users, including through aggregation or changes in counts?

The recommended first technical decision is the Windows compatibility baseline
and toolchain/GUI selection, supported by a focused feasibility evaluation.
Record evidence in research notes and explicit approval in a decision record.
