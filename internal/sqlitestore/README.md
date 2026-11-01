# Isolated SQLite acceptance and history adapter

Task 009A authorizes file-backed acceptance; Task 009B adds the isolated
`historysync.Store.View` read adapter. Delivery v1 remains proposed for production
integration. There is no TCP server, protocol ACK emission, authentication,
client change, or persistent device checkpoint implementation here.

Dependency: **modernc.org/sqlite v1.61.1**, pinned in `go.mod`/`go.sum`. Its published
module requires Go 1.26.0; the existing **Go 1.27.3** remains unchanged. The tested
Windows/amd64 runtime reports **SQLite 3.54.1**. No cgo, GCC, vendor tree, or system
installation is required. Linux execution has not been verified by this task.
See the [driver documentation](https://pkg.go.dev/modernc.org/sqlite).

## Schema version 1

- `messages`: immutable through the adapter API; positive message ID primary key,
  original sender/writer/sequence, recipient, and exact UTF-8 bytes in a BLOB.
  The unique `(sender, writer_id, client_seq)` index **is** the retry association:
  exactly one per message, containing its original recipient and bytes in the
  same row. No redundant retry table or duplicate payload is needed.
- `allocation`: singleton global committed message-ID high-water mark.
- `account_cursors`: account-local committed cursor high-water marks.
- `feeds`: foreign keys to the message and account cursor row; primary key
  `(account_id, cursor)` and unique `(account_id, message_id)` prevent duplicates.

STRICT tables, range/byte-length checks, foreign keys, and unique indexes enforce
basic integrity. Existing shared Go validators enforce strict UTF-8 and the
500-scalar limit. `Record` validates exact participant/reference cardinality,
allocations, and self-message uniqueness before inserting. No account tables or
account existence/authentication assumptions are added. Arbitrary external SQL
writers are outside this adapter's integrity/immutability contract.

Opening an empty database creates schema/allocation state atomically and sets
`PRAGMA user_version=1`. Reopening verifies the version, required table definitions
and allocation row, preserving data. Unsupported versions, nonempty unversioned
databases, and incompatible layouts are rejected; nothing is reset or migrated.

## Transactions and durability

One pooled connection per store keeps local ordering simple. The driver's
`_txlock=immediate` starts **BEGIN IMMEDIATE before retry lookup or policy**.
SQLite's file-level writer reservation serializes separate store instances too;
`database/sql`'s default deferred transaction alone would be insufficient.
See [SQLite transactions](https://www.sqlite.org/lang_transaction.html).

The callback executes once. Message/retry row, both feed references (one for
self), and all high-water marks commit together. Errors/panics roll back staged
state; a failed `Record` also prevents commit if a callback ignores that error.
Only successful COMMIT returns acceptance. A commit-reply error can be uncertain:
retry the original key/recipient/bytes, including after closing and reopening.

Every physical connection receives WAL, `synchronous=FULL` (2), `foreign_keys=ON`
(1), and `busy_timeout=5000` milliseconds through fixed driver DSN parameters.
Active values are verified at initialization and before each callback; unsafe
settings fail closed. Replacement connections receive the same settings. Busy
timeouts bound SQLite lock waits, not time spent queued behind other callbacks.
No automatic callback replay is performed. Callers must not reenter the store
inside policy/transaction callbacks and should quiesce operations before Close.

[FULL synchronization](https://www.sqlite.org/pragma.html) and
[WAL](https://www.sqlite.org/wal.html) support durable commits subject to correct
filesystem, OS, and hardware flush/locking behavior. WAL requires same-host local
storage. `Open` accepts an escaped filesystem path, requires an existing parent,
and rejects memory/URI/UNC inputs; it cannot detect every network-mounted path.
Do not copy only the main file of a live WAL database as a backup.

Tests use temporary files and genuine new connections after Close. They verify
reopening, rollback (including deferred FK failure at COMMIT), lost-reply recovery,
two-instance concurrency, constraints and PRAGMAs, including connection replacement.
They **do not simulate physical power failure** or certify storage hardware.
No database, WAL, SHM, cache, or downloaded dependency is committed.

## History snapshots and bounded reads

`View` uses `BeginTx` with `ReadOnly: true`. In pinned modernc.org/sqlite v1.61.1,
`newTx` in `tx.go` bypasses `_txlock=immediate` for that option and executes a
deferred `BEGIN`. The initial account-tail SELECT pins the SQLite WAL snapshot
before invoking the callback; Tail returns that captured high-water mark. Joined
feed/message reads use the same transaction and cannot see later commits.
The Reader interface exposes no writes; this driver option is a transaction-mode
selection, not an authentication or SQL permission boundary.

`Records` scopes every query to the established account and `(after, through]`,
orders by account cursor, and uses a parameterized SQL LIMIT of at most 9 (page
plus one lookahead). The existing `(account_id, cursor)` primary-key index and
message primary key suffice; EXPLAIN tests verify indexed access without a sort.
No schema, index, driver, or acceptance transaction changes are required.
Exact BLOB bytes and all original identity fields are reconstructed. Cursor gaps
are preserved. A dangling message reference causes a read error, not omission.

Rows and transactions are closed on success/error; callback failure rolls back
the read transaction. The service returns no partial page on a read/view error.
No transaction is kept between pages: later pages reuse numeric H, and fresh
views observe new commits. Reads never update history or device checkpoints.

The existing single-connection pool is reused, so calls through the same store
instance wait for a view to finish. Do not reenter it from the callback. In WAL
mode, an independent store instance can commit acceptance while that view remains
open; a channel-gated test verifies this with actual temporary SQLite files.
`Close` retains its existing ownership of the only pool; no extra lifecycle is
introduced. Callbacks should stay short, since pinned readers can delay WAL
checkpoint progress. Power-loss and production integration limitations remain.

For a fresh checkout, download pinned modules with the existing portable Go
toolchain before using the project's intentionally offline Go wrapper:

```powershell
$env:GOTOOLCHAIN = 'local'
$env:GOPROXY = 'https://proxy.golang.org'
$env:GOSUMDB = 'sum.golang.org'
$env:GOMODCACHE = Join-Path (Get-Location) '.cache/go-mod'
& .tools/go-1.27.3/go/bin/go.exe mod download
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 test ./internal/sqlitestore
```
