# Delivery v1 writer state and outbox

Task 011A authorizes this isolated CLDC 1.1/MIDP 2.0 component. It does not change
First Contact, its MIDlet package, the wire codec, or the server. There is no UI,
networking, authentication, writer issuance, history or synchronization here.
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
