# Instructions for coding agents

## Mandatory English-only policy

- All coding agents must write responses, progress updates, explanations,
  questions, and final task reports in English.
- All source code, code comments, documentation, commit messages, Pull Request
  titles and descriptions, and review summaries must be written in English.
- This policy applies to all future tasks unless the project owner explicitly
  instructs otherwise.

## Read before working

Read `README.md`, `docs/PRODUCT.md`, `docs/ROADMAP.md`,
`docs/ARCHITECTURE.md`, and any records in `docs/decisions/` before changing the
project. Read `docs/WEBSITE.md` for website or account-management work and relevant
notes in `docs/research/`. Inspect existing files and Git status first; preserve
unrelated user changes.

## Respect the approved scope

- Treat PS-001 through PS-006 in `docs/PRODUCT.md` as approved product requirements.
- Preserve their canonical mapping: PS-001 release strategy; PS-002 multi-session
  and presence; PS-003 contacts and minimal anti-spam; PS-004 message history and
  limits; PS-005 account and identity management; PS-006 self-messaging. Do not
  regroup or renumber them; keep other requirements outside these identifiers.
- Preserve the mission: make old devices useful again through small, simple,
  resource-efficient software. Legacy compatibility is a primary constraint.
- Do not add features, dependencies, scaffolding, or abstractions without a need
  in the authorized task. Do not implement explicitly excluded features.
- Never invent or silently approve architecture. Go, SQLite, TCP, GUI frameworks,
  and other technologies remain candidates until an explicit decision is recorded.
- If work depends on an unresolved material choice, explain the choice and seek
  a decision before implementing it. Routine details within approved scope do not
  require a new approval process.
- Research and proposals are not approved decisions. Record approval provenance
  when adding or changing a decision; do not manufacture dates or approvers.
- Keep documentation in English and update it when authorized behavior changes.
- Keep all project files and Git metadata in this repository root. Do not create
  another nested `OldGoodMessenger` project directory.

## Work and verification

- Follow the current task's authorization. Do not deploy, publish, push, create
  remotes, install frameworks, start services, or open firewall ports unless the
  task authorizes that work.
- Do not fetch competitor source code unless explicitly requested.
- Never commit credentials or personal secrets. Include third-party assets only
  after verifying their licenses and redistribution terms.
- Do not fabricate measurements, server availability, download links, test
  results, or external service endorsements. Verify external links before adding
  them; leave unavailable facts explicitly unavailable.
- Protect Invisible presence in every user-visible path, including aggregate
  statistics. Do not introduce Last Seen.
- Run checks appropriate to the change and report their actual results. Do not
  claim compatibility with legacy devices without relevant evidence.
- After each task, provide a verifiable report: changed files and purpose,
  checks performed and results, Git/commit status, unresolved assumptions,
  missing tools or permissions, and remaining limitations or next decisions.
  Distinguish completed work from proposals and untested behavior.

## Foundation boundary

Foundation Task 001 authorizes documentation, minimal directories, local Git
initialization on `main`, and an initial commit if author information is configured.
It does not authorize application code, framework installation, deployment,
remote repository creation, or pushing changes. Later tasks must explicitly
authorize their own implementation scope.
