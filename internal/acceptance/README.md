# Isolated message acceptance core

Task 008A authorizes these acceptance semantics only. Delivery architecture
remains **PROPOSED — NOT APPROVED** for production integration.

The caller supplies a trusted account/writer context; the request has no sender
field. Numeric validation does not authenticate anyone or establish writer
ownership. Existence and acceptance policy are injected; the core neither knows
nor rejects offline presence. All negative policy decisions become the same
`ErrRejected`, without exposing blocking details.

The shared Delivery v1 validators enforce numeric IDs and strict UTF-8 with
1..500 Unicode scalars and at most 2000 text bytes. Retry comparison uses exact
recipient and string bytes, without normalization. Accepted duplicates return
the original identity before evaluating current policy or allocating anything.

`Store.Transact` serializes retry lookup, policy, allocation and recording across
all acceptors sharing the store. It stages the immutable message, retry payload
association, account feed references and allocation high-water marks, then
publishes all or nothing. Self-messages have one reference. ID order describes
commits; concurrent callers can observe their function returns in another order.
Callbacks execute once, may not escape the transaction, and have no external
side effects. Policy must not reenter the store. A future implementation must
provide these transaction guarantees; this task selects no database or schema.

`Accept` exposes a nonzero result only when the store reports commit success.
Every error returns a zero result. A commit error can mean the result is unknown:
retry exactly the same key/recipient/bytes to recover the original acceptance.
No notification hooks or protocol ACK emission exist in this package.

The fake exists only in `_test.go`: a mutex serializes transactions, private
copies provide rollback, and snapshots expose committed value copies. Failures
cover reads, partial record/feed writes, rejected commit, and a committed change
whose reply is deliberately lost. A channel gate verifies staged data and
acceptance results are not published before commit. Concurrency tests use small
fixed groups, not timing-dependent expected winners or stress runs.

**Memory is not durable storage.** Test commits do not survive process exit and
do not meet Delivery v1's durable-ACK contract. A real store must establish and
test its crash/power-loss durability before these results could justify ACKs.
There is no relay integration, networking, authentication, synchronization,
notification fanout, persistent history, or client change here.
