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

This repository contains the project foundation and approved product requirements.
There is no application code, running service, installer, or download yet.
Languages, storage, transport, GUI frameworks, and hosting remain undecided.

## Repository layout

```text
server/             Reserved for the server
clients/windows/    Reserved for the Windows client
clients/java-me/    Reserved for the Java ME client
website/            Reserved for account management and public information
docs/               Product, roadmap, architecture, website, decisions, research
```

Reserved implementation directories contain only `.gitkeep` files so Git retains
the directory structure.

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
