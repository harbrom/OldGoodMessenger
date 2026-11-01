# Interactive local Delivery v1 console

Task 010B's experimental developer tool, **not a production server**. Alice
(100001) and Bob (100002) are trusted contexts injected in Go, not authenticated
accounts selected through TCP. Each has a real `tcp4` connection established
through `127.0.0.1:0`; the temporary listeners close after accepting their pairs.
There is no external endpoint, handshake, encryption, broadcast or Java ME change.
The existing First Contact relay and console remain unchanged.

From the repository root in Windows PowerShell, using the existing portable Go
toolchain and cached pinned SQLite dependency:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 build -o .build/ogm-local.exe ./cmd/ogm-local
New-Item -ItemType Directory -Force .build/local-delivery | Out-Null
& .build/ogm-local.exe -db .build/local-delivery/messages.sqlite
```

`-db` is required and accepts a filesystem path, not a DSN. Its parent directory
must exist. Use local storage with reliable SQLite locking/fsync, not network
storage. Existing databases are preserved; incompatible schemas fail without
resetting them. SQLite creates WAL/SHM sidecars as needed. Keep databases and
builds under ignored `.build/`; do not commit them. See the existing
[SQLite adapter and durability limitations](../../internal/sqlitestore/README.md).

Enter one command per line:

```text
help
send alice bob Hello from Alice
send bob alice Hello from Bob
send alice alice Self-message test
sync alice
sync bob
retry alice
retry bob
quit
```

The remainder after the recipient's first separator is exact UTF-8 text, including
additional leading/trailing spaces. Text must contain 1..500 Unicode scalars and
at most 2000 bytes. Terminal input is line-based; valid Unicode reaching stdin
is preserved without normalization. Terminal rendering/input encoding depends on
the host console; the tool does not change its settings. Input lines are bounded
to 4096 bytes. Unknown commands/accounts and extra arguments are rejected.

SEND prints participants, writer and sequence, then ACK/message ID or a distinct
REJECTED, CONFLICT or RETRYABLE outcome. ACK means SQLite acceptance, not recipient
receipt. `sync` explicitly replays that account's entire history from zero, in
pages of at most eight ITEMs, holding the same snapshot H across pages. ITEM prints
all wire fields; END prints next/through/count. A failed replay is incomplete,
even if some ITEMs were printed. There is no local history cache/checkpoint, live
push, automatic sync or delivery guarantee. Run `sync` again to see newer commits.

Each startup generates fresh positive 63-bit test writer namespaces using
`crypto/rand`, distinct for Alice and Bob (cross-run collision is negligibly
probable, not mathematically impossible). They are test-only, not production
server-issued identities. Each writer allocates increasing sequences without
wrapping. An uncertain SEND retains its exact sequence, recipient and bytes in
memory, blocks a replacement SEND, and is resent only by `retry`. Connection
errors discard/join the old handler; the next operation reconnects with the same
in-run writer. RETRYABLE is not ACK and keeps the pending request. Definitive
rejection/conflict clears it without reusing the sequence. With no pending request,
`retry` reports an error. There is no fault-injection command in this tool.

Use `quit` (or stdin EOF) for orderly socket/handler/database closure. Pending
uncertainty produces a shutdown warning. **There is no durable outbox or crash-safe
client retry**: exit loses pending state, and that message may already be stored.
Do not manually recreate an uncertain message expecting cross-run deduplication.
Forced termination has no orderly-cleanup guarantee.

For the restart demonstration, send the example messages, `quit`, run the **same
launch command with the same `-db` path**, then `sync alice` and `sync bob`.
Committed history and IDs remain; writers are new. Self-messages appear once in
their owner's history. Alice's self-messages are absent from Bob's history.

Short checks:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 test -count=1 ./cmd/ogm-local
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 test ./...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 vet ./...
```

Tests use real temporary SQLite databases and IPv4 loopback TCP, including two
separate console subprocesses reopening the same database. Faults are injected
only by test adapters to check uncertain commits and retry behavior. These checks
do not establish physical power-loss resistance, production security, or any new
legacy-device compatibility.
