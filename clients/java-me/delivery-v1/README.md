# Delivery v1 writer state and outbox

Task 011A authorizes isolated CLDC 1.1/MIDP 2.0 writer state; Task 011B adds a
SEND-only TCP component; Task 011C adds read-only, volatile TCP history replay.
Task 011D adds an isolated, bounded RMS history cache and durable checkpoint consumer.
These experiments are **not production-approved**. First Contact, its MIDlet
package, wire formats and server behavior remain unchanged. There is no UI,
production authentication, writer issuance or production synchronization approval.
Run persistence on a worker thread in any future integration, never the UI thread.

## Ownership and provisioning

`WriterState` owns one account/writer, the last allocated sequence, and at most
one pending SEND. `StateStorage` is a small whole-record test seam;
`RmsStateStorage` uses private MIDP RMS. One adapter may be open per VM, preventing
two component owners from allocating under the same writer. State methods are
synchronized. Other code/MIDlets in the suite must not access this RMS store
concurrently; RMS itself supplies no application-level lock or compare-and-swap.

Trusted local setup explicitly supplies a valid numeric account and a **positive,
fresh, never previously used writer namespace**. No writer is derived from time,
phone/account data or handset randomness. The example IDs below are disposable
test fixtures, not an issuance algorithm or authentication.

```java
// Once, for a genuinely new namespace supplied by trusted setup:
RmsStateStorage file = RmsStateStorage.create("ogm-delivery-state");
WriterState state = null;
try {
    state = WriterState.provision(file, 100001, 7);
    Codec.Frame send = state.reserve(100002, "Hello");
    // Only now could a future caller transmit send. No network code here.
} finally {
    if (state != null) state.close();
    else file.close(); // Factory failures leave ownership with the caller.
}
```

On ordinary restart use `RmsStateStorage.open(name)` and
`WriterState.open(file, expectedAccount, expectedWriter)` instead, with the same
trusted identity; handle/close ownership is the same. Open never creates a store.
Create refuses every existing store, including empty/corrupt stores. Provision
refuses existing record bytes; it is never an automatic fallback from open.
Neither implementation deletes records, repairs corruption or resets counters.

Missing, empty, incompatible or irrecoverably corrupt state is an explicit
failure requiring a **fresh trusted writer**, not restoration of the previous
writer with sequence zero. Preserve the old store for diagnosis; explicit trusted
setup can select a new store name and fresh namespace. After total erasure, local
code cannot know which IDs were previously used: preventing explicit reprovisioning
with an old ID is the trusted setup's responsibility. Do not restore an older
valid snapshot under the same writer. Uninstallation can remove suite RMS data.

## Version 1 persistent record

Exactly RMS record ID 1; unexpected/missing/deleted records fail. Big-endian:

| Offset | Bytes | Field |
| --- | --- | --- |
| 0 | 4 | Magic `4f 47 4d 4f` (`OGMO`) |
| 4 | 1 | Format version `01` |
| 5 | 4 | Account ID, 100000..999999999 |
| 9 | 8 | Positive trusted writer ID, at most 2^63−1 |
| 17 | 8 | Last allocated sequence, 0..2^63−1 |
| 25 | 1 | Pending flag, exactly 0 or 1 |
| 26 | 0 or 16..2015 | Complete existing length-prefixed SEND, if pending |
| end−4 | 4 | CRC-32/ISO-HDLC of all preceding bytes |

Idle size: **30 bytes**. Pending size: **46..2045 bytes**, including at most
2000 UTF-8 text bytes. Pending sequence must equal the positive last sequence.
Open checks size, checksum, version, expected identity, flag, exact embedded SEND
layout and absence of trailing bytes. The adapter checks size before allocating.
CRC detects accidental corruption, not tampering or rollback of a valid record.
Writes use one complete bounded snapshot, not a journal, queue or history cache.

## Reservation, retry and responses

`reserve(recipient, text)` rejects a second pending SEND and sequence exhaustion.
It validates through the existing codec/strict `ogm.Utf8`, atomically writes the
incremented counter and complete payload together, then returns a frame copy.
Text retains exact standard UTF-8 semantics: combining marks count separately,
supplementary code points count once, NUL/newline are preserved; unpaired surrogates,
empty text, over 500 scalars or over 2000 bytes are rejected without allocating a
sequence. Returned mutable frames never alias the internal pending frame.

`pending()` returns that exact SEND after restart or for retry. Transport failure
or a lost ACK needs no state change. `resolve(reply)` accepts only a syntactically
valid matching ACK (including positive message ID) or SEND ERROR with the pending
sequence. ACK, REJECTED and CONFLICT persistently clear pending while retaining
the counter. RETRYABLE returns false without writing; it is never ACK. Unexpected
operations, invalid fields and mismatched sequences fail without clearing pending.
No ACK duplicate cache, server message-ID storage or history checkpoint is added.

Only a successful storage update publishes the new state to callers. Any write
error makes the state instance unusable, even if the storage actually committed.
Close it, reopen and validate the actual stored snapshot before proceeding.
Failure before reservation leaves the old counter only if reopening proves the
old record survived; no SEND was returned. An uncertain clear can recover either
the pending request or its cleared state. Corruption never chooses a guessed
counter or silently rolls back. Close invalidates the instance immediately; a
failed close must be retried before opening another adapter. A leaked handle from
a failed factory cleanup blocks further adapters until VM restart.

## SEND-only TCP lifecycle (Task 011B)

Give `Sender` an already-open, trusted `WriterState`. On one worker thread, call
`connect("127.0.0.1:<port>")`, then `send(recipient, text)` or `retry()`, and
eventually `close()`. The address is an outbound DNS/IPv4 `host:port`, with an
explicit host and port 1..65535; URLs, parameters, omitted hosts and IPv6 are
rejected. Test listeners use only `127.0.0.1:0`. This component creates no listener
and sends no account/writer handshake. A future production boundary must establish
peer identity and bind replies to the provisioned writer before using these frames.

`send` durably reserves before writing. `retry` obtains the exact persisted SEND
without reserving. Both write one bounded existing frame, flush, and read one
complete response. A matching ACK or correlated SEND ERROR alone reaches
`WriterState.resolve`; the returned `Codec.Frame` preserves ACK versus REJECTED,
CONFLICT and RETRYABLE. ACK indicates server storage acceptance, not recipient
delivery. Successful write/flush alone never clears the outbox. New SEND while
pending is refused; RETRYABLE keeps pending and permits explicit retry.

I/O errors, platform timeout exceptions and malformed/unexpected/mismatched replies
close the socket and both streams, report an error and preserve unresolved state.
Explicitly reconnect, then retry; reconnect itself never allocates or transmits.
After an RMS resolution error the state is unusable: close the sender, join its
worker, close/reopen/verify RMS and construct a new Sender. Recovery uses what RMS
actually retained; an uncertain ACK clear may have committed.

One operation may run at a time. There are no background workers, concurrent
writers, queues, automatic reconnects or timers. The caller must give the sender
exclusive use of its WriterState during operations. A cancellation thread may
call `close()` while the worker blocks; then join the worker before reconnecting
or closing RMS. Handles returned after a cancelled open are closed, and cleanup
errors are reported and may be retried. Cancellation may race with an already
received matching ACK/RMS commit; it cannot undo accepted storage operations.
It can also leave a newly reserved SEND pending before any bytes were transmitted.

The implementation uses [MIDP 2.0 SocketConnection](https://docs.oracle.com/javame/config/cldc/ref-impl/midp2.0/jsr118/javax/microedition/io/SocketConnection.html)
and `Connector.open(..., READ_WRITE, true)`. The
[Connector timeout flag](https://docs.oracle.com/javame/config/cldc/ref-impl/midp2.0/jsr118/javax/microedition/io/Connector.html)
is only a platform hint, **not a guaranteed timeout or millisecond deadline**.
Closing acquired handles supports cancellation, but an implementation blocked
inside Connector.open may return only later; no immediate-open-cancellation or
physical GPRS timeout behavior is established. The Go harness bounds each emulator
process to 20 seconds; that test guard is not a handset networking policy.

Run the isolated sender checks with the installed tools:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-java-me-sender.ps1
```

This compiles/preverifies the component and CLDC-only focused tests, then runs
`TestJavaMESender` in the existing Go loopback package with a separately packaged
test MIDlet. Ordinary `go test ./...` skips that opt-in emulator test; the script
supplies its fixture JAR explicitly. No First Contact artifacts are rebuilt.

The test-only Go harness injects account 100001 and writer 7 for the restart
scenario, matching trusted RMS provisioning. Other outcomes use distinct disposable
writers 11..16 and databases. Nothing selects identities through TCP. Temporary
SQLite files and file-backed emulator RMS are owned by the Go test and cleaned
up after listeners and handlers stop; generated JARs stay ignored under `.build/`.

Verified locally: real TCP through unchanged `deliveryloopback.Handle`, exact
ASCII/Ukrainian/Polish/combining/supplementary/NUL/newline bytes in SQLite, commit
before deliberately dropped ACK, separate-JVM RMS reopen/retry returning original
ID 1, then a new SEND with sequence/ID 2 and durable pending clear on another
restart. Additional emulator cases verify REJECTED, CONFLICT, RETRYABLE, malformed,
unexpected and mismatched replies and recovery. Focused stream-seam tests cover
flush failure, timeout exceptions, uncertain RMS ACK clears, partial reads,
resource cleanup and cancellation during read/open without sleeps. The restart
phases also reopen SQLite; each phase verifies committed rows/IDs and joins
handlers before closing the store.

These are emulator/desktop results, not new K750i hardware evidence. The sender
does not retrieve history; the separate reader below is volatile only. Production
authentication, encryption, CHANGED handling, GUI,
public/GPRS integration, hard deadlines and physical power-loss guarantees remain
outside this experiment.

## Volatile history reader (Task 011C)

`new HistoryReader(trustedAccount)` defaults to **one ITEM per page**; the second
constructor argument selects 1..8. Account context comes from the trusted caller,
not a network handshake. Run `connect`, `replay` and its consumer on a worker,
separately from Sender's connection and durable outbox:

```java
HistoryReader reader = new HistoryReader(100001, 2); // Disposable trusted fixture.
try {
    reader.connect("127.0.0.1:<port>");
    reader.replay(new HistoryReader.Consumer() {
        public void page(HistoryReader.Page page) throws java.io.IOException {
            for (int i = 0; i < page.size(); i++) {
                HistoryReader.Item item = page.item(i);
                // Consume this validated immutable value; no durable claim here.
            }
        }
    });
} finally { reader.close(); }
```

`replay(consumer)` explicitly starts from zero; `replay(after, consumer)` starts
from a chosen nonnegative cursor. It returns the terminal H on success. Each call
captures fresh H from its first END, then holds that H across every continuation
until `next=through`. Exactly one SYNC is outstanding; overlapping/reentrant
operations are refused. There is no background polling or automatic retry.

ITEM cursor/message IDs must strictly increase within and across this replay;
every record must include the trusted account. Full nonterminal pages require
`next=last_item_cursor`; terminal pages, including full/empty pages and legitimate
cursor gaps, obey `next=H`. Fresh bounds are checked at END, continuation H cannot
change, and no more than the requested ITEM count is accepted. ACK and CHANGED
are unsupported interleaves and invalidate the connection; they never advance
progress. A valid END is necessary before any page is passed to the consumer.

`Page` contains immutable ITEM values and `after`, `next`, `through` metadata.
Each replay initializes `position()` to the chosen start and `snapshot()` to zero;
subsequent advancement reflects only pages whose consumer returned normally.
`complete()` becomes true only after successful terminal consumption. Inside a
callback these getters still describe previously consumed pages. Consumer
IOException/runtime failure prevents that page's advancement and closes the
connection. Observed cancellation aborts replay; it cannot undo consumption that
already completed. Callback side effects cannot be rolled back: a replay may
repeat them, so the caller must handle duplicates. `close()` clears completion
but retains the last volatile position/H for inspection.

Correlated SYNC ERROR throws `HistoryReader.SyncException` with `code` and `after`.
Protocol errors, malformed/truncated frames and I/O errors also fail explicitly.
Discarding the connection removes partial or unread frames; reconnect and invoke
a new replay from zero or a deliberately chosen position. Previous successful
pages remain volatile observations, never a persisted checkpoint. Another thread
may call `close()` for cancellation; join the worker before reconnecting. Socket
ownership is isolated to leave Sender unchanged; the platform timeout/open
cancellation limitations described above still apply.

The reader retains at most one page array of 1..8 immutable ITEMs plus one
transient codec frame/decoding buffers. Text is bounded by the existing 500-scalar /
2000-byte rules (at most 1000 UTF-16 units per ITEM). Eight maximum-size ITEMs and
END occupy at most 16363 wire bytes; this is not an exact heap measurement.
There is no whole-history cache, queue or RMS checkpoint. Consumers must release
pages rather than accumulate them if they want the same bounded memory behavior.

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-java-me-history.ps1
```

The script compiles against CLDC 1.1/MIDP 2.0 only, preverifies class version 45.3,
runs focused tests, then opts into `TestJavaMEHistory`. Normal Go checks skip this
emulator test without its explicit fixture JAR. Twenty-two separate emulator JVM
cases use real `127.0.0.1:0` TCP and temporary SQLite through unchanged Handle and
history services. Committed fixtures come from acceptance; sparse cursor tests
decorate the read view without altering SQLite rows/schema or allocation rules.

Verified cases include empty/single/multi-page histories at limits 1/2/8,
account isolation, both message directions and self-messages, exact Unicode and
identity fields, full terminal pages and empty tail gaps. A deterministic commit
after the first END is excluded from old H and included by fresh replay; Go checks
the exact `(after,H,limit+1)` reads. Faults cut ITEM/END, reorder IDs, alter END/H,
inject foreign/unexpected/malformed replies and mismatched errors. Reconnect
retrieves the complete expected history. Another thread closes the real reader
socket during consumption; stream-seam tests also cancel blocked read/open and
check consumer failures without sleeps. Listeners/handlers stop before SQLite
and temporary files are closed/removed. Generated test JARs stay in ignored `.build/`.

HistoryReader alone remains **read-only, volatile retrieval, not durable
synchronization or a delivery receipt**. A restart loses its in-memory position.
The separate Task 011D consumer below adds a bounded persisted working set and
checkpoint. No GUI, notification networking, production authentication or physical
K750i/GPRS deployment is implemented. Emulator results establish neither handset
compatibility nor physical power-loss survival.

## Durable bounded history (Task 011D)

`HistoryStore` retains the newest **eight** complete ITEMs by message ID, plus a
durable account cursor. Use a distinct RMS name from the SEND outbox. The new
`RmsHistoryStorage` adapter reuses the small `StateStorage` seam; it permits one
serial history owner per VM, independently of `RmsStateStorage`'s writer owner.
WriterState and its format are unchanged. Do all storage work on a worker thread.

Trusted setup supplies the account and a positive 63-bit **local test server-history
namespace**. For example, `777` is only a fixture label. It is not an endpoint,
wire field, authentication token, server-issued identity or production restoration
scheme. Open must match both persisted bindings exactly. The caller must associate
the correct endpoint with that namespace; this protocol cannot authenticate or
automatically discover a changed logical server history. Production history identity,
backup restoration and replacement semantics remain unresolved. Do not restore old
RMS state or silently reuse a checkpoint against a different server history.

For initial provisioning, explicitly call `RmsHistoryStorage.create(name)` and
`HistoryStore.provision(file, account, namespace)`. Both refuse existing state.
For ordinary restart, open without creation and use the actual stored checkpoint:

```java
RmsHistoryStorage file = RmsHistoryStorage.open("ogm-history-cache");
HistoryStore store = null;
HistoryReader reader = null;
try {
    store = HistoryStore.open(file, 100001, 777);
    long after = store.checkpoint();
    reader = new HistoryReader(100001, 1);
    reader.connect("127.0.0.1:<port>");
    reader.replay(after, store.consumer(after));
} finally {
    try { if (reader != null) reader.close(); }
    finally { if (store != null) store.close(); else file.close(); }
}
```

`checkpoint()`, `size()` and detached `item(index)` inspect the recovered state.
`consumer(after)` allows `0 <= after <= checkpoint`; each new consumer invalidates
the old replay binding. It checks local Page.accountID, exact after progression,
fixed H, increasing IDs/cursors and membership. Page.accountID is new **local-only
metadata** from HistoryReader's trusted account, not a protocol change. Earlier
replay uses `reader.replay(0, store.consumer(0))` without regressing the checkpoint.
The reader still validates the complete page/END before calling the consumer.

For a state-changing page, the consumer:

1. Checks all records against the original retained cache and builds a bounded
   newest-eight merge. Identical retained records are idempotent; conflicting
   metadata/text/cursors fail before writing.
2. Writes the entire merged cache snapshot with the **old checkpoint**.
3. Only after that succeeds, writes another complete snapshot with
   `max(old_checkpoint, END.next)`.
4. Publishes the new in-memory state and returns success only after the second
   storage operation succeeds. Already-identical replay may require no writes.

No multi-record transaction is assumed: each snapshot is one individually atomic
RMS record update. If the first succeeds and the second fails, reopen can recover
new cached ITEMs with the old checkpoint. Resume from that checkpoint safely
upserts duplicates. A checkpoint write may commit and then report an error; the
current instance is poisoned and the consumer reports failure, even if the stored
checkpoint is newer. Close/reopen and validate the actual bytes before continuing.
No individual ITEM, ACK, CHANGED or partial network page advances the checkpoint.
RMS read/write errors, incompatible records and corruption are explicit failures;
there is no automatic deletion, repair, migration or reset. Retain old stores for
diagnosis and use separately authorized explicit provisioning if recovery is impossible.
Retry a failed close before reopening; an unreleased adapter can require VM restart.

### History snapshot format, version 1

Exactly RMS record ID 1, big-endian:

| Offset | Bytes | Field |
| --- | --- | --- |
| 0 | 4 | Magic `4f 47 4d 48` (`OGMH`) |
| 4 | 1 | Version `01` |
| 5 | 4 | Trusted account ID |
| 9 | 8 | Positive local test server-history namespace |
| 17 | 8 | Completed cursor, 0..2^63−1 |
| 25 | 1 | Retained ITEM count, 0..8 |
| 26 | variable | That many complete existing length-prefixed ITEM frames |
| end−4 | 4 | CRC-32/ISO-HDLC of preceding bytes |

Size is **30..16374 bytes**. The adapter checks record count/ID and size before
allocating; load checks CRC, version, identity bindings, strict codec syntax,
membership, ordering and trailing bytes. Cache cursors beyond checkpoint are
intentional staged-write recovery state. CRC detects accidental corruption,
not tampering or rollback of an otherwise valid old record.

Eviction is only of local cache entries: older server history remains retrievable.
A failed checkpoint update may already have changed/evicted cache entries. Older
replay does not displace the newest retained messages. Conflict detection covers
retained IDs; an eight-entry cache cannot remember or compare every evicted record.
The server remains authoritative for those records. A checkpoint represents
completed retrieval under this cache policy, **not full local history retention,
a read receipt or a delivery guarantee**. A future cursor rejected by the server,
or a page snapshot below already-known progress, fails without resetting state.

Memory is bounded by the page, eight cached records, a bounded merge and snapshot
serialization buffers; it never grows with total history length. Whole-snapshot
writes trade extra bounded flash traffic for simple recovery. The maximum-size
record works in MicroEmulator, but heap/flash cost, record limits and durability
on physical K750i remain unmeasured. RMS success relies on the platform's storage
contract; no emulator or injected failure test certifies physical power-loss safety.

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-java-me-durable-history.ps1
```

This runs CLDC 1.1/MIDP 2.0 compilation, class version 45.3/preverification,
focused tests, and **31 separate emulator JVM phases** using file RMS and the
unchanged Go Handle with loopback TCP/temporary SQLite. At limits 1 and 8, initial
cursor 3 is reopened after offline additions and only IDs 4..10 are fetched.
Truncating the third END leaves cursors 12/26; restarted processes fetch only the
remaining IDs through 30. Earlier replay preserves checkpoint 10 and the newest
eight entries. Tests inject failure before staged persistence, before checkpoint
update, and after actual RMS checkpoint success but before its reply; new JVMs
recover and replay from the real stored cursor. Go checks exact SYNC read bounds.
Other cases cover empty/single histories, wrong bindings, independent pending SEND
preservation, maximum-size Unicode/RMS records and an explicit cursor-invalid
server rollback. No public listener, physical deployment or new dependency is used.

## Verification and durability limits

Use the existing installed tools only, from the repository root:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-java-me-outbox.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-delivery-codec.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 test ./...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 vet ./...
```

The outbox script compiles against only CLDC 1.1/MIDP 2.0 APIs, preverifies with
existing ProGuard 6.2.2, checks class versions 45.3/CLDC StackMaps, and executes
the preverified state-machine tests. Its separate test MIDlet uses MicroEmulator
2.0.4 **file RMS** across three separate JVMs: reserve, reopen/retry/ACK, then
reopen cleared state and reserve sequence 2. It also tests RMS missing/empty,
corrupt, oversized, extra/deleted records and exclusive ownership. No socket is
opened. Emulator configuration, RMS files, JAR/JAD and logs stay under a fresh,
ignored `.build/java-me-outbox/` directory; First Contact artifacts are untouched.

Injected test writes cover failure before commit, commit followed by an error,
and corrupted uncertain data. These are application recovery tests, not a claim
that compliant RMS normally performs torn writes. The
[MIDP 2.0 RecordStore contract](https://docs.oracle.com/javame/config/cldc/ref-impl/midp2.0/jsr118/javax/microedition/rms/RecordStore.html)
specifies individual atomic, synchronous, serialized operations and persistent
records, with best-effort integrity across normal reboots/battery changes.
One record avoids a multi-record transaction assumption. RMS has no portable
application fsync/barrier API. Emulator success cannot prove K750i flash behavior,
survival of arbitrary physical power loss, hardware integrity or resistance to
restoring old storage. This component has not been run on the physical K750i.
