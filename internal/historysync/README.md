# Isolated server-side history synchronization

Task 008B-1 authorizes this read-only core only. The delivery architecture remains
**PROPOSED — NOT APPROVED** for production integration.

`Service.Page` takes a caller-established account context separately from the
decoded SYNC frame. It returns at most eight ITEM frames and one END, using the
existing Delivery v1 types. Account ownership is the caller's responsibility.

`Store.View` must supply a consistent committed account tail, feed references,
and immutable joined message contents for one callback. A read transaction or
equivalent snapshot is a storage implementation responsibility. No transaction
or per-device state is retained between calls. The test-only fixture uses an
RWMutex and accepts messages through the existing acceptance core.

Each page captures H from the committed tail when `through=0`; otherwise it
reuses the supplied H, requiring `0 <= after <= H <= current_tail`. Reads select
only that account's `(after,H]` records. The store materializes at most `limit+1`
rows: one lookahead establishes whether more remain. A future store must return
the earliest complete bounded selection, not silently truncate it or load the
whole history. The test fixture scans its small in-memory feed; no database or
production query implementation is selected here.

If more remain, END.next is the last returned cursor; otherwise it is H, even
for an empty page or a tail gap. Exactly full pages can be terminal. Snapshot
isolation across calls relies on immutable committed records and no insertion
behind H, as required by the acceptance/protocol contract. Later accepted
messages are retrieved by a fresh snapshot, not added to an old one.

Cursors are numeric positions, not authenticated snapshot tokens. Positions
inside gaps are valid bounds under the documented range rule; future/out-of-range
bounds are rejected. This implementation does not invent an issued-cursor registry
or require a message at every position. The account context always scopes reads.

Rows are checked for account membership, increasing cursor/message ID, valid
message fields/text, matching references and bounds, including lookahead. Invalid
store output or any read/view-close error returns no page. These checks cannot
prove a store omitted no records; the bounded read contract remains essential.

There are no writes, client checkpoints, authentication, notifications, sockets,
or durability claims. Historical replay does not change accepted state. Volatile
test commits do not establish persistent history or production delivery guarantees.
