# Message delivery protocol

**PROPOSED — NOT APPROVED.** Task 006 design, November 1, 2026; no implementation authorization or production decision.

## Scope and evidence

Preserves [PS-001 through PS-006](../PRODUCT.md), [architecture boundaries](../ARCHITECTURE.md), and the [roadmap](../ROADMAP.md).
Builds on [First Contact](../first-contact.md), including owner-reported physical K750i/GPRS bidirectional messaging.
Reviewed at PR #2 merge `5e2f901`; existing [wire codec](../../internal/wire/wire.go), [relay](../../internal/relay/relay.go),
and [MIDlet transport](../../clients/java-me/src/ogm/Transport.java) remain unchanged. Their results do not validate this design.
Go and direct TCP are this task's constraints. SQLite is preferred for later evaluation, **not an approved dependency**.
No gateways, brokers, new transport, schema, presence, contacts, registration, GUI, or production authentication design.

## Boundary and identifiers

- These are delivery revision 1 layouts, incompatible with PoC HELLO/SEND. A future version/authentication handshake must explicitly
  select this revision and establish trusted `(account_id, writer_id)` before these operations; never auto-detect or accept a claimed sender.
  Disposable PoC IDs are not authenticated accounts. Test harnesses may inject context locally, without implying production authentication.
- `account_id`: 4 bytes, numeric OGM ID 100000..999999999, preserving six-to-nine-digit expansion.
- `writer_id`: 8-byte, server-issued, account-bound installation namespace; persistent across reconnects and reauthentication.
  Issuance/authorization belongs to the future boundary, not this delivery protocol. Never reuse a namespace after local state loss.
- `client_seq`: 8-byte client-generated increasing counter scoped to that writer. Persist the increment and complete pending submission
  before sending any bytes; gaps are allowed. Concurrent connections using one writer must coordinate this allocation.
  Deduplication key is `(account_id, writer_id, client_seq)`; counters from distinct devices cannot collide.
- `message_id`: 8-byte server-assigned unique ID within one logical server history, increasing in durable acceptance order, never reused.
  Serialize publication with commits: no later ID becomes visible before earlier acceptance resolves. Gaps are legal.
  A conversation is the unordered pair of participant IDs; ascending message ID defines its order, including simultaneous sends.
- `cursor`: 8-byte account-local increasing feed position, independent of message ID. One reference per participant per message;
  self-messages get **one** reference. Sender and recipient cursor values are independent and may differ. Feed order follows durable acceptance order.
  Each device stores its own cursor; one device's progress never advances another's. No server-side delivery/read receipt is implied.
- All 8-byte values use 1..2^63-1, allowing signed Java `long`; cursor zero means start, and snapshot zero requests a fresh bound.
  Refuse allocation on exhaustion; never wrap. IDs/cursors are scoped to the logical server history, not portable across servers.
  Restore/migration must preserve their uniqueness and committed progress; an older backup cannot silently resume as the same history.

## Lifecycle, acceptance, and retries

1. Client durably reserves a sequence and pending `(recipient, exact UTF-8 bytes)`, then sends SEND. Keep one unacknowledged SEND per device.
2. Server first checks the deduplication key. Identical recipient/bytes return the original ACK without new history/feed entries;
   a different payload under that key returns CONFLICT. No normalization or hash-only equality shortcut.
3. A new submission must pass recipient existence and PS-003 blocking/abuse/account policy gates before acceptance; their mechanisms
   remain outside scope. No mutual contact approval is added. Offline status alone is never a rejection.
4. Atomically commit the immutable message, retry key/payload association, both account feed references (one for self), and allocation
   high-water marks. Only then send ACK and notify active sessions of both accounts, including the sending device.
   Enforce uniqueness of the retry key and `(account, message_id)` feed reference; racing retries recheck the winning transaction.
5. ACK guarantees this transaction is durably accepted and recoverable after process restart and ordinary power loss **if the storage
   stack honors its durability contract**. It does not mean any recipient connected, received, displayed, or read the message.
   Hardware failure, data loss, and restoration of an older backup are outside this guarantee; it is not a product service guarantee.
6. Lost ACK, timeout, or uncertain disconnect: retry the **same key and bytes** after reconnecting, with bounded backoff.
   If commit happened, return the original ACK; otherwise accept once. Do not allocate a new sequence for an uncertain send.
   Keep deduplication state at least as long as its accepted message; no automatic age-based expiry of history or retry protection.

ACK marks an outbox entry accepted, not synchronized. ACK must never advance a device cursor.
Incoming records identify the originating writer/sequence, so the sender can reconcile a pending entry even if ACK was lost.
All devices upsert by message ID; the originating writer also merges its matching pending entry instead of appending another copy.
Self-routing follows these same rules: one stored message, one account reference, one displayed record, including on the sending device.
There is practical idempotency and replay, **no exactly-once network delivery claim**.

## Framing and operations

Reuse the PoC's two-byte unsigned **big-endian body length**, excluding the prefix; body starts with a one-byte opcode.
All multibyte integers are big-endian; no padding, terminators, compression, or Java modified UTF-8.
Proposed body cap is **2041 bytes** (wire cap 2043), raised from PoC's 2005 only to accommodate delivery metadata.
Reject lengths below 9 or above 2041 before allocating; then require the exact operation layout and numeric ranges.
`u8/u32/u64` below mean 1/4/8-byte wire fields, subject to the ranges above. `text` occupies the remaining body bytes.

| Direction / operation | Body fields, in exact order | Body bytes |
| --- | --- | --- |
| C→S SEND `10` | opcode:u8, client_seq:u64, recipient:u32, text | 14..2013 |
| S→C ACK `90` | opcode:u8, client_seq:u64, message_id:u64 | 17 |
| C→S SYNC `11` | opcode:u8, after:u64, through:u64, limit:u8 | 18 |
| S→C ITEM `91` | opcode:u8, cursor:u64, message_id:u64, sender:u32, recipient:u32, writer_id:u64, client_seq:u64, text | 42..2041 |
| S→C END `92` | opcode:u8, next:u64, through:u64 | 17 |
| S→C CHANGED `93` | opcode:u8, current_account_tail:u64 | 9 |
| S→C ERROR `ff` | opcode:u8, request_opcode:u8, reference:u64, code:u8 | 11 |

ERROR references SEND's client sequence or SYNC's `after`, scoped to this connection/context.
Codes: `01` REJECTED (invalid recipient/text or policy denial; no new acceptance), `02` CONFLICT (key already has different content),
`03` CURSOR_INVALID (invalid range/unknown future position), `04` RETRYABLE (outcome may be uncertain; retry same SEND key).
Do not expose blocking/suspension details. Unknown opcodes, bad sizes, invalid numeric fields, or protocol-state violations close the
connection; ERROR is best effort only when correlation is safely available. An errored SYNC has no END and advances no checkpoint.
ACK/CHANGED may interleave a SYNC response; ITEM records belong only to its single outstanding SYNC and arrive in cursor order.

Text proposal: **1..500 Unicode scalar values and 1..2000 UTF-8 bytes**, matching the experimental codec, pending explicit approval
of PS-004's counting detail. A supplementary scalar counts once; combining marks count separately, not as grapheme clusters.
Reject invalid/overlong UTF-8, encoded surrogates, values above U+10FFFF, and unpaired Java UTF-16 surrogates; preserve valid bytes,
including NUL/newline, without normalization. Java `String.length()` alone is insufficient. Use explicit standard UTF-8, never readUTF/writeUTF.

Representative complete frames (hex; spaces/newlines are presentation only):

```text
SEND seq=1, recipient=100002, text="A":
00 0e 10 00 00 00 00 00 00 00 01 00 01 86 a2 41
SEND seq=2, recipient=100002, text="Я🙂" (two scalars, six UTF-8 bytes):
00 13 10 00 00 00 00 00 00 00 02 00 01 86 a2 d0 af f0 9f 99 82
ACK seq=1, message_id=42:
00 11 90 00 00 00 00 00 00 00 01 00 00 00 00 00 00 00 2a
SYNC after=0, fresh snapshot, limit=2:
00 12 11 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 00 02
ITEM cursor=7, id=42, 100001→100002, writer=3, seq=1, text="A":
00 2a 91 00 00 00 00 00 00 00 07 00 00 00 00 00 00 00 2a
00 01 86 a1 00 01 86 a2 00 00 00 00 00 00 00 03 00 00 00 00 00 00 00 01 41
END next=7, through=7:
00 11 92 00 00 00 00 00 00 00 07 00 00 00 00 00 00 00 07
```

## Independent synchronization and bounded delivery

- CHANGED is a coalescible hint, not a message or receipt. Notify every active session of each participant after commit, and send
  the current account tail when a session becomes ready. Register notifications before capturing that initial tail to avoid a missed wakeup.
- On every reconnect, issue SYNC from the locally committed cursor even without a hint. Live delivery uses the **same** SYNC/ITEM path;
  this adds one small request/page but avoids a second ordering/deduplication mechanism. No offline memory queue is required.
- SYNC selects only the established account's sent and received history, independent of contact approval. With `through=0`, server
  captures its committed account tail H; otherwise use the supplied prior snapshot bound H. Require `0 <= after <= H <= current_tail`.
  Snapshot H is a number, not a transaction held across requests; immutable committed records cannot be inserted behind it.
- Return at most `limit` records in `(after,H]`, ascending cursor; limit must be 1..8. END repeats H. If records remain, `next` is
  the last returned cursor; otherwise `next=H`, including an empty page. Gaps need not yield records. Never return an empty nonterminal page.
  Continue with `after=next, through=H` until `next=H`; then fetch a fresh snapshot if a newer hint was observed.
- Only one SYNC is outstanding per connection. A device may request an earlier cursor (including zero) to page retained history;
  this is a separate retrieval position and must not overwrite a newer committed synchronization checkpoint. No age-based server pruning.
- Process ITEM one at a time; upsert durably before considering it stored. Persist END's `next` only after validating the complete page:
  expected snapshot, increasing in-range cursors/IDs, participant membership, and count <= limit. Require `after <= next <= H`;
  nonterminal END requires a nonempty full page and `next=last_item_cursor`; terminal END requires `next=H >= last_item_cursor`.
  An empty page requires terminal END. For a fresh snapshot, validate its H against `after` and all received cursors at END.
- Disconnect before valid END: keep the old checkpoint, replay the page and upsert duplicates. Never advance to CHANGED's tail,
  an ACK's ID, or a single ITEM's cursor. If local storage fails, stop sync without advancing; a volatile cache cannot promise restart recovery.
- Persist only a bounded local working set if necessary; server history remains authoritative. Records deliberately evicted locally
  can be retrieved again from an earlier position. Do not confuse cache eviction with individual server-side deletion.
- Notifications arriving during a page update a pending maximum tail. Coalesce them rather than queue one per message. If delivery
  queues fill or a frame stalls, close that session; committed history survives. Never silently drop the only pending change hint.
- No client-side delivery ACK is needed: server retains history regardless of retrieval. Devices do not suppress delivery to other devices.
  TCP segmentation/coalescing never defines frame boundaries; read full bounded frames, finish partial writes, and serialize each writer.

## Java ME constraints and failure cases

- Eight maximum-size ITEM frames plus END cost at most **16363 wire bytes** per page; limit=1 is allowed. Stream rows with roughly
  one frame buffer plus decoded text; never allocate an entire history/page to construct a response. Local storage overhead needs measurement.
- Keep I/O off the UI thread and close failed sockets; reuse the existing strict UTF-8 approach and bounded queues/workers.
  Fixed-width counters avoid UUID libraries or dependence on handset randomness. Persist writer/counter/outbox/cursor via a bounded local store.
  Partial local writes need recovery before retrying; lost local state requires a fresh writer namespace, never counter reset under the old one.
- Mid-SEND disconnect: either no commit or one committed record; retry resolves uncertainty. Mid-ACK/ITEM/END: discard the partial frame,
  reconnect and retry/resync. After server restart, reconstruct IDs, feed tails, and retry associations from committed storage before serving.
- A sender's ACK and its own ITEM may arrive in either order; merging by ID and writer/sequence handles both. Concurrent devices keep
  separate cursors/writers and converge on the same immutable messages. Duplicate ACK/CHANGED frames are harmless when consistent.
  Repeated SYNC after END replays records without new history; pipelined SYNC violates the single-outstanding rule.
  Duplicate/out-of-order ITEM within a page aborts it without checkpoint advancement; reconnect/replay upserts already stored items.
- Require bounded frame-completion/write deadlines and reconnect backoff; PoC's five-second deadlines are **not** a validated GPRS policy.
  Half-open detection, timeouts, and any future heartbeat need hardware evidence; no new heartbeat opcode or polling interval is selected here.
- Plain TCP has no confidentiality, authenticated peer identity, or tamper resistance. Dedupe keys are not secrets or authorization.
  Active attackers, catastrophic storage loss, deleted-account/data-erasure handling, and server-history replacement need separate boundaries.
  Retained correspondence must preserve PS-005's "Deleted user" behavior; this protocol adds no nickname/deletion operations.

Tradeoffs: a single acceptance order and long-lived retry records simplify recovery but serialize writes and grow with history.
Per-account feed references add persistence overhead while keeping device cursors small; eight-row pages trade round trips for bounded heap.
There are no timestamps or read/delivery receipts in this proposal. No throughput, heap, or new hardware-compatibility claim is established.

## Proposed sequence and blocking decisions

1. Obtain approval for delivery semantics, layouts, and Unicode counting; retain this proposal until explicitly approved.
2. Smallest implementation task: add an isolated revision-1 codec and shared Go/Java byte fixtures with boundary, split/coalesced-frame,
   malformed-input, and supplementary-scalar tests. Leave the validated PoC unchanged; no database, authentication, or network exposure.
3. Then test transaction/retry/page state machines with injected local contexts and fault points; separately approve storage/driver and
   durability configuration before claiming durable ACK. Integrate device synchronization only after those semantics pass crash/replay tests.

Blocking decisions for the dependent portions (not requests to change approved product scope):

- Approve Unicode scalar counting versus grapheme counting for the existing 500-character requirement.
- Approve a persistent storage dependency and concrete crash/power-loss durability contract/settings for ACK.
- Define the future version/authentication boundary's trusted account and stable writer-namespace lifecycle before production integration.
- Resolve legal/account-deletion effects on retained correspondence and deduplication state before implementing erasure; no automatic expiry is proposed.

These choices and every wire/behavior recommendation above remain **PROPOSED — NOT APPROVED**.
