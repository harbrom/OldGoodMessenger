# Decision records

Approved product requirements are recorded as PS-001 through PS-006 in
[`../PRODUCT.md`](../PRODUCT.md). Their canonical meanings were restored by the
project owner's instructions in Foundation Task 002. Preserve those identifiers;
do not regroup other product requirements under them.

No production implementation architecture has been approved yet. The project
owner authorized Go 1.27.x, framed TCP, a Go console client, and CLDC 1.1/MIDP 2.0
for Implementation Task 004's experimental local PoC only. Its scope and evidence
are recorded in [First Contact](../first-contact.md). SQLite and production GUI
frameworks remain candidates; this authorization does not create a production ADR.

Use this directory for future material technical decisions. A record should state:

- The question, constraints, and related product requirements.
- Options considered and supporting research or verification.
- The selected option and its consequences, or an explicit undecided status.
- Approval provenance and date only when actually known.
- Any record it supersedes.

Use stable filenames such as `ADR-001-short-title.md` when creating a real record.
Clearly distinguish proposed, accepted, rejected, and superseded records. An agent
proposal or research result does not constitute approval. Do not create records
that present unselected technologies as decisions.
