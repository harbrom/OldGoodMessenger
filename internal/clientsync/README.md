# Client history synchronization reference

Task 008B-2 authorizes this isolated Go state machine only. Delivery architecture
remains **PROPOSED — NOT APPROVED** for production integration.

The caller establishes the account, binds a local store to one server history /
account / device, and serializes method calls. A numeric account is not proof of
identity. `New` loads the checkpoint and requires initial synchronization, even
when no CHANGED hint exists. `Next` issues one SYNC from that checkpoint, or the
next retrieval position within H. A second outstanding request is rejected.
`NeedsSync` reports initial, continuation, or newer-hint work; calling `Next`
manually when idle also allows a new snapshot after exhaustion.

ITEM fields use existing codec validation. The account must participate;
cursors and message IDs must strictly increase within the page, count must stay
within the limit, and known snapshot bounds apply immediately. For a fresh H,
the final bound is checked at END. Store upserts are keyed by message ID and
retain the complete canonical ITEM, including account-local cursor and exact
text. Identical replay is idempotent; conflicting immutable records must fail.

Only a valid complete END calls `AdvanceCheckpoint`. The store atomically saves
`max(current, candidate)` and returns its actual value; successful earlier item
writes must precede this write. Empty pages are terminal, full pages may be
terminal, and cursor gaps are accepted. A nonterminal page must be full and its
next position must match its last ITEM. No cryptographic completeness proof is
claimed. `Replay(after)` starts earlier history without regressing checkpoints;
continuations use END.next independently of the newer checkpoint.

ITEM writes may remain after a malformed END, write error, or interruption.
Any protocol/storage error aborts the page and requires recovery. `Disconnect`
also abandons request state. After discarding the old stream and buffered frames,
call `Reconnect` to read the actual checkpoint and force a fresh SYNC. Write
errors may mean a write committed without a reply; no synchronization success is
reported. `Checkpoint` is unavailable until recovery. Read failures leave recovery
required, and regressing/invalid stored checkpoints are rejected.

CHANGED coalesces a maximum tail hint and never commits a checkpoint. Valid ACKs
are ignored only as interleaves during an active page; an idle ACK is rejected
by this synchronization-only consumer. A future dispatcher must route sending /
outbox traffic separately. ERROR and other unsupported frames abort this consumer.
The wire has no page request ID: the caller's active request and clean stream
boundary supply correlation; this reference adds no new wire fields.

The test-only memory store retains state across instance recreation, with write,
read, and lost-reply faults. It does not survive production process crashes or
power loss and makes no durable-storage claim. A bounded in-memory adapter tests
the existing server history service without sockets. No production outbox,
SQLite, authentication, networking, Java ME, GUI, or First Contact changes exist.
