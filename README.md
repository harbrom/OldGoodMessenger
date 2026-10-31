# OldGoodMessenger (OGM)

![Support Ukraine](docs/assets/support-ukraine.svg)

**Make old devices useful again.**

OGM is an open-source, lightweight, text-only instant messenger inspired by
ICQ and QIP from the early 2000s. It aims to offer the same minimalist messaging
experience on modern and legacy devices through its own independent protocol.

Public Beta 0.1 targets a server, a Windows XP SP3–11 client, and a Java ME client
initially tested on the Sony Ericsson K750i. These are planned targets, not
currently available applications.

OGM is an experimental hobby project, not a guaranteed secure or reliable
communication service. Legacy compatibility takes priority over sophisticated
security. End-to-end encryption is outside the approved scope.

## Current state

This repository contains approved product requirements and a local transport
experiment: a Go TCP relay, a Go console client, and a CLDC 1.1/MIDP 2.0 MIDlet.
See [First Contact setup, protocol, and verification](docs/first-contact.md).
These choices are approved for Implementation Task 004's PoC only. Production
architecture, storage, GUI frameworks, and hosting remain undecided. There is no
deployed service, production installer, or published download.

## Repository layout

```text
server/             Experimental loopback TCP relay entry point
internal/           PoC framing, relay, test-client support, and Go tests
clients/console/    Go console transport test client (modern Windows)
clients/windows/    Reserved for the Windows client
clients/java-me/     Experimental MIDlet and compatibility tests
scripts/            Portable tool setup, builds, and local verification
website/            Reserved for account management and public information
docs/               Product, roadmap, architecture, website, decisions, research
```

Unimplemented areas retain `.gitkeep` files. Tool downloads, build artifacts,
and test caches are local and ignored by Git.

## Project documentation

- [Approved product requirements](docs/PRODUCT.md)
- [Roadmap](docs/ROADMAP.md)
- [Architecture boundaries and open decisions](docs/ARCHITECTURE.md)
- [Website requirements](docs/WEBSITE.md)
- [Technical decision records](docs/decisions/README.md)
- [Research notes](docs/research/README.md)
- [Instructions for coding agents](AGENTS.md)

Read the product requirements before proposing implementation changes. Keep work
small and avoid expanding the approved scope. Project documentation is in English.

## Support Ukraine

OGM supports Ukraine. The badge above is a local asset. No donation provider or
external support link has been selected or verified.

## License

[MIT](LICENSE). Third-party assets, including any future Kolobok emoticon bundle,
require separate license and redistribution verification before inclusion.
