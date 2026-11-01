# Local Delivery v1 TCP experiment

Task 010A authorizes this loopback-only integration, not production protocol or
authentication approval. `Handle` receives an existing IPv4 loopback connection,
caller-established account/writer context, and acceptance/history services. It
creates no listener or executable. Both socket endpoints must be IPv4 loopback;
tests create only `127.0.0.1:0` listeners and inject context in Go after Accept.
No account identity, login, credentials or handshake is accepted from the stream.

Requests are sequential. Valid SEND goes through the existing acceptance core;
only successful commit produces ACK with the original sequence and message ID.
Identical retries return the original ID. Valid SYNC produces the complete bounded
history page, ITEMs in order followed by END, scoped to the injected account.
There are no queues, broadcasts or CHANGED notifications. A recipient need not
be connected: its accepted history is retrieved by a later SYNC.

Semantic SEND validation/rejection maps to REJECTED, retry conflicts to CONFLICT,
invalid history bounds to CURSOR_INVALID, and other service/storage errors to
RETRYABLE. ERROR contains only opcode, correlation value and code, never database
paths, errors or policy details. These failures leave the frame stream usable.
Malformed codec input (including syntactically invalid fields), server-only
opcodes, and I/O errors close the connection; no partial frame is processed.

The existing codec bounds bodies to 2041 bytes and finishes partial writes.
Request completion has a five-second deadline after the first byte; each complete
response has a five-second write deadline. Waiting for a new request has no idle
deadline. These are local test policies, not validated GPRS settings. Each handler
retains at most one request and one eight-ITEM/END page, without per-client queues.
The caller bounds admitted connections; this package supplies no accept loop.

The connection is closed on every exit. Context cancellation closes blocked I/O
and the handler joins its close callback before returning. Already-started store
operations are not cancellable through the established application interfaces;
they may still commit after disconnect/cancellation. Stop/join handlers before
closing services or SQLite. Lost ACKs and uncertain commit errors require retrying
the same writer/sequence/recipient/bytes, never sending a replacement identity.

Tests use the real SQLite adapter, with WAL/FULL durability assumptions unchanged.
No ACK is sent after an uncertain commit error. Successful ACK means accepted
storage commit, not recipient receipt/display, production reliability, or proven
power-loss resistance. Callers must provide a store with the required durability.

The test policy allows only disposable fixture recipients. It is not production
account-existence, blocking, abuse-control or authentication enforcement. There
is no encryption, registration, session fanout, persistent client outbox/checkpoint,
Java ME integration, GUI, public endpoint or production authentication here.
The First Contact relay and wire format are unchanged.
