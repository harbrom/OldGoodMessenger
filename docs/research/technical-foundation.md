# Technical foundation research

**Status: PROPOSED — NOT APPROVED**

Research date: 2026-10-31. All recommendations, implementation choices, and test
thresholds below are **PROPOSED — NOT APPROVED**. This is research for Architecture
Task 003, not an ADR or implementation authorization.

## Scope and evidence

Objective: the smallest practical Windows test client ↔ OGM Server ↔ Sony
Ericsson K750i Java ME test client. The canonical PS-001 through PS-006 in
[PRODUCT.md](../PRODUCT.md) remain authoritative. The existing
[architecture boundaries](../ARCHITECTURE.md) and [roadmap](../ROADMAP.md) remain
unchanged. A transport experiment does not establish Beta 0.1 readiness.

Repository inspection began on clean `main` at
`59c9f400ca3eab96e2eef06dd8ae7014c0a547b8`. A read-only `git ls-remote` query
confirmed that `origin/main` had the same commit. No remote updates were made.

This report combines current official documentation with historical manufacturer
and specification documents for obsolete targets. Links below were accessed on
the research date. Sony Ericsson documents are identified as mirrored originals
or transcriptions; their platform claims are evidence, not handset test results.
Comparative judgments about simplicity and maintenance are project-specific
inferences. No binaries were built, benchmarks run, or hardware tested.

## Recommended minimal stack — PROPOSED — NOT APPROVED

| Area | Comparison | Proposed direction |
| --- | --- | --- |
| Backend | Go: integrated networking/testing and managed concurrency. C++: tight resource control with more ownership/build work. Rust: memory safety without GC, but more async/runtime concepts. | Go, standard library first, one standalone server process |
| Persistence | SQLite: embedded transactional storage with serialized writes. PostgreSQL: stronger concurrent-writer and operational capabilities with another service to administer. | SQLite for initial persistent server; no database in the transport PoC |
| Legacy transport | Persistent TCP: compact traffic and immediate server delivery. HTTP polling: broader mandatory MIDP API, repeated requests and delayed delivery. | Direct plain TCP first; HTTP only if device/network evidence warrants fallback |
| Windows GUI | wxWidgets 3.2: native controls and documented XP support. Qt 5.6: historical XP deployment support and expired standard support. Win32: fewest GUI dependencies, most manual UI work. | Investigate C++/wxWidgets 3.2; final Windows stack remains undecided |
| PoC clients | A Windows 11 console harness avoids blocking transport research on the GUI; a Java ME MIDlet exercises the actual legacy API. | Go console harness plus CLDC 1.1/MIDP 2.0 MIDlet using `SocketConnection` |

The server runs on modern Windows for development and later Linux/ARM64; it does
**not** need Windows XP support. The Go console harness is a test tool, not the
planned XP-compatible production client. No gateway, web framework, broker,
container platform, or production website is needed for this experiment.

## Backend evaluation — PROPOSED — NOT APPROVED

**Recommend Go.** The official download page currently lists Go 1.27.3 for
Windows/amd64 and Linux/arm64; use a supported patch release when implementation
is authorized. Modern Go supports Windows 10 and later, which fits Windows 11
development. Linux/arm64 is an explicit target. A standard-library PoC avoids a
C compiler and third-party network runtime. Cross-compilation is a useful build
check, but executing the result on Linux/ARM64 remains necessary before claiming
deployment compatibility. Sources: [Go releases](https://go.dev/dl/),
[minimum requirements](https://go.dev/wiki/MinimumRequirements), and
[target configuration](https://go.dev/doc/install/source).

Go's [`net` package](https://pkg.go.dev/net) supplies TCP listeners, connections,
and deadlines. A small goroutine-based connection handler is a readable starting
point. Bound frame sizes, outbound queues, and connection counts; a stalled client
must not retain unlimited memory or block another client. Use the standard test
runner, race detector where supported, and parser tests. Garbage collection,
runtime memory, and linked runtime code mean Go is not a promise of the smallest
RSS or executable. Measure idle/busy RSS, CPU, allocations, and bytes exchanged
before optimizing. No comparative resource numbers have been measured here.

**C++ is feasible but less economical in maintenance effort.** It can avoid a GC
and tightly control allocations. Portable asynchronous networking can use
Boost.Asio, whose implementation uses Windows I/O completion ports and Linux
epoll. That adds a library/build decision; direct OS sockets instead add separate
platform code. Ownership, cancellation, buffer lifetimes, and concurrent access
require more manual care. Windows development needs a C++ compiler/linker setup;
Linux/ARM64 needs a matching target toolchain and libraries. A mature ecosystem
supports long-lived projects, but OGM currently has no server code requiring C++.
Sources: [Asio platform implementation](https://www.boost.org/doc/libs/latest/doc/html/boost_asio/overview/implementation.html)
and [GCC AArch64 target documentation](https://gcc.gnu.org/onlinedocs/gcc/AArch64-Options.html).

**Rust is a credible second choice.** Ownership provides memory safety without a
GC. The current stable target table includes Windows MSVC and Linux AArch64 with
host tools. Cargo and built-in testing help reproducibility; an efficient async
server commonly adds Tokio rather than one OS thread per connection. Rust's
ownership rules, async task lifetimes, cancellation, and dependency management
raise the initial learning and review burden for this small hobby server. Prefer
it if maintainers already have substantial Rust experience or measured resource
needs justify it; neither condition is established here. Sources:
[Rust ownership](https://doc.rust-lang.org/book/ch04-01-what-is-ownership.html),
[stable platform support](https://doc.rust-lang.org/stable/rustc/platform-support.html),
and [Tokio's runtime model](https://tokio.rs/tokio/tutorial).

All three can serve this workload. Go wins on the proposed project's small code
surface, integrated tooling, and ease of contribution, not on an unmeasured CPU
ranking. Keep dependencies few and versions recorded; no language guarantees
future maintainer availability.

## Persistence evaluation — PROPOSED — NOT APPROVED

**Recommend SQLite for the first persistent server.** One OGM process owning a
local database file avoids a second service, database accounts, and network
configuration. Many connected devices do not imply simultaneous database writers:
short transactions can serialize message/session updates while reads serve
paginated history. SQLite documents its single-writer constraint and suitability
for local application storage. Move to PostgreSQL if measured write contention or
multiple server instances require it, not merely because the application has TCP
clients. Source: [appropriate SQLite uses](https://sqlite.org/whentouse.html).

Persistent history and delivery bookkeeping need transaction boundaries. A later
design should commit an accepted message and required delivery state together
before claiming durable acceptance. Reconnect/replay and per-session progress
need explicit semantics; neither SQLite nor PostgreSQL automatically provides
offline delivery or independent synchronization. No complete schema is proposed.
PS-004's absence of age-based expiry means disk usage and backup duration grow;
do not silently introduce retention expiry to manage this.

WAL is a candidate when concurrent reads justify it: readers can overlap a writer,
but there is still only one writer. Keep transactions short and define bounded
busy handling. WAL requires local same-host storage and introduces checkpointing
and sidecar files. If selected, use a maintained SQLite version containing the
WAL-reset fix: 3.51.3 or later, or a documented fixed backport. Do not copy just
the live main database file as a backup. Use the online backup API and prove a
restore. Sources: [SQLite WAL documentation and fix versions](https://sqlite.org/wal.html)
and [online backup API](https://sqlite.org/backup.html).

**PostgreSQL 18 is adequate but unnecessary initially.** Its MVCC and locking
support concurrent transactional workloads; SQL dumps, filesystem backups, and
continuous archiving provide mature recovery options. The extra server lifecycle,
configuration, updates, and backup administration increase the small self-host's
operational burden. It becomes preferable for measured multi-writer pressure or
deployment across several OGM server processes. Sources:
[PostgreSQL 18 architecture](https://www.postgresql.org/docs/18/tutorial-arch.html),
[concurrency control](https://www.postgresql.org/docs/18/mvcc-intro.html), and
[backup methods](https://www.postgresql.org/docs/18/backup.html).

**Open dependency choice:** Go has no built-in SQLite driver. A C-backed driver
introduces a C toolchain and cross-compilation work; a pure-Go implementation
avoids that toolchain but adds its own dependency/version/footprint tradeoffs.
Choose and test a maintained driver in a later persistence task. The PoC uses
memory only. See [Go cgo requirements](https://pkg.go.dev/cmd/cgo).

## K750i transport feasibility

### Documented capabilities and limits

Sony Ericsson's **K750i White Paper, March 2005**, documents CLDC 1.1 (JSR 139),
MIDP 2.0 (JSR 118), and GPRS multislot class 10 (4+2). Its Java section is on
printed page 35; GPRS details are on pages 26 and 57. These are platform/radio
capabilities, not measured throughput. Source:
[manufacturer white paper, mirrored PDF](https://altehandys.de/downloads/wp-se-k750.pdf).

The **Sony Ericsson Java ME Developers Guideline, ninth edition, November 2005,
EN/LZT 108 7584 R9B**, identifies K750 as JP-5 and documents socket and HTTP
connections (pages 36–37). It lists network permissions for untrusted MIDlets
and prohibits unsigned MIDlets from opening sockets on **80, 443, and 8080**
(pages 20–21). It also marks the `Connector.open()` timeout parameter unsupported
(page 37). Source: [manufacturer guideline, mirrored transcription](https://studyres.com/doc/7940699/java%E2%84%A2-platform--micro-edition--for-sony-ericsson-mobile-p...).
Firmware/operator variations and actual prompts still need testing.

MIDP 2.0 mandates HTTP access, but does not establish raw TCP availability on
every MIDP device. `SocketConnection` defines the socket stream API and
`socket://host:port` addressing. The manufacturer evidence above is therefore
material to the K750i decision. Sources: [MIDP 2.0 networking specification](https://docs.oracle.com/javame/config/cldc/ref-impl/midp2.0/jsr118/javax/microedition/io/package-summary.html)
and [SocketConnection API](https://docs.oracle.com/javame/config/cldc/ref-impl/midp2.0/jsr118/javax/microedition/io/SocketConnection.html).

### Transport comparison — PROPOSED — NOT APPROVED

| Choice | Traffic and implementation | Remaining failure modes |
| --- | --- | --- |
| Persistent plain TCP | One outbound connection per client; compact frames; server can send immediately. No polling requests or HTTP headers per message. | Carrier/APN filtering, NAT idle expiry, dropped radio links, permission prompts; stream framing and reconnect handling required |
| HTTP request/response or polling | Standard MIDP `HttpConnection`; send with POST and retrieve pending messages by request. Potential fallback where direct sockets fail. | Headers and repeated empty polls cost traffic; polling interval adds delay. Long polling adds timeout/proxy behavior to investigate |

**Recommend direct TCP first.** Select a configurable permitted port after checking
the handset restriction and endpoint policy; do not assume raw TCP on port 80 is
a workaround. Each client initiates its own outbound connection to the OGM
server, so the phone does not need an inbound reachable address. Try the correct
Internet APN/data profile before blaming the protocol. If repeatable evidence
shows sockets unusable but HTTP works, propose an HTTP endpoint in the same OGM
server. No separate gateway is justified by the evidence so far.

GPRS can have variable delay, constrained uplink, retransmissions, and temporary
outages; nominal radio rates cannot predict chat latency. See
[RFC 3481, TCP over 2.5G/3G networks, February 2003](https://www.rfc-editor.org/rfc/rfc3481.html).
APN restrictions, present-day 2G service, SIM provisioning, DNS, carrier idle
timeouts, charging, and battery effects are unknown for the user's actual network.
Measure them rather than assuming permanent connectivity. Start with sparse
traffic; determine heartbeat needs from idle tests rather than frequent polling.
Use bounded reconnect backoff, and keep blocking I/O off the MIDlet UI thread.
Verify cancellation/close behavior on hardware instead of relying on the timeout
flag documented as unsupported.

**Proposed PoC framing:** a bounded byte-length prefix, a small operation tag, and
explicit UTF-8 text, with only test identity selection and direct-message routing.
Specify the exact layout and frame cap in the implementation task. TCP is a byte
stream: readers must handle split headers/bodies and multiple frames in one read;
senders must finish partial writes. Source:
[RFC 9293, TCP, August 2022](https://www.rfc-editor.org/rfc/rfc9293.html).

Use standard UTF-8, not a platform default or Java `writeUTF`/`readUTF` wire format:
CLDC's latter format is **modified UTF-8**. Validate the selected Java ME codec
against shared byte fixtures, including supplementary characters; font display
and successful byte transport are separate results. Sources:
[RFC 3629, UTF-8, November 2003](https://www.rfc-editor.org/rfc/rfc3629.html) and
[CLDC 1.1 DataInput](https://docs.oracle.com/javame/config/cldc/ref-impl/cldc1.1/jsr139/java/io/DataInput.html).
Wire byte counts do not resolve PS-004's 500-Unicode-character counting policy;
code points versus grapheme clusters remains an explicit product detail to settle.

Plain TCP and plain HTTP expose message content and any transmitted credentials
or bearer tokens to observers on the path, and provide no cryptographic server
authentication or protection against active modification/impersonation. Cellular
link protection does not supply end-to-end confidentiality. Use disposable test
identities and non-sensitive messages. Do not require contemporary TLS on the
handset or describe legacy TLS support as interoperability with current TLS.
This tradeoff is consistent with the project's stated limits; production
authentication design remains a separate decision.

## Windows client feasibility — PROPOSED — NOT APPROVED

| Candidate | XP evidence and suitability | Maintenance/size assessment |
| --- | --- | --- |
| C++ / wxWidgets 3.2 | Version 3.2.12 Windows build documentation lists XP through 11. Native controls fit classic contact lists and chat windows. | Best provisional balance: reusable GUI abstractions and a maintained 3.2 line; trim unused features and measure the actual build |
| C++ / Qt 5.6 Widgets | Qt's 5.6 platform table lists 32-bit XP for deployment only, with standard support ending 2019-03-16. | Capable desktop toolkit, but an old branch and its build/runtime dependencies create more ongoing work; no verified Windows 11 result here |
| Native Win32 | Use only XP-compatible APIs and a compatible compiler/SDK/runtime. OS controls suit a classic UI. | Lowest GUI dependency floor, but layouts, Unicode handling, DPI behavior, and window lifetimes need more application code; future Linux UI work is separate |

Sources: [wxWidgets 3.2.12 Windows build documentation](https://raw.githubusercontent.com/wxWidgets/wxWidgets/v3.2.12/docs/msw/install.md),
[wxWidgets release listing](https://wxwidgets.org/downloads/), and
[Qt 5.6 official platform-table source](https://raw.githubusercontent.com/qt/qtdoc/5.6/doc/src/platforms/supported-platforms.qdocinc).
No binary-size comparison has been measured; toolkit support does not certify
arbitrary prebuilt packages or dependencies.

**Recommend a later wxWidgets 3.2 feasibility build, without finalizing it.**
Microsoft documents `v141_xp` (VS 2017 compiler plus XP-compatible SDK targeting)
for XP SP3 x86; default VS 2019-and-later toolsets do not target XP. It also
identifies **14.27** as the last compatible redistributable file-version family.
An old target macro alone does not make modern imports or runtimes XP-compatible.
Verify availability of the legacy components and their use from the actual
Windows 11 development setup; that host workflow has not been exercised here.
Source: [Microsoft XP targeting and runtime guidance](https://learn.microsoft.com/en-us/cpp/build/configuring-programs-for-windows-xp?view=msvc-170).

Before choosing the production GUI, build one x86 native-controls sample with a
fully recorded compiler/SDK/runtime combination, inspect imported APIs, and run
the same artifact on XP SP3 and Windows 11. Record executable plus required DLL
size and idle memory; test Unicode, multiple windows, and DPI behavior. This
separate feasibility check need not delay a Windows 11 transport harness.

## First implementation task proposal — PROPOSED — NOT APPROVED

**Task 004 proposal: local TCP relay and Java ME interoperability PoC.** Approval
must explicitly cover the proposed stack and required tool setup before work
starts. The current research task authorizes neither implementation nor installs.

Local PATH inspection found `java`, but not `go`, `javac`, `ant`, `preverify`,
`rustc`, `cargo`, `cl`, or `g++`. This is not an exhaustive installed-software
inventory; C++ tools can require a developer shell. No suitable Java ME build
environment has been verified.

Sun Wireless Toolkit **2.5.2_01** is a candidate: Oracle documents CLDC 1.1/MIDP 2.0
and compile/preverify/package workflows. Its historical host requirements list
Windows XP/Linux x86 and Java SE SDK 1.5, not a validated Windows 11 setup.
Locate legitimate tools, check their licenses/availability, and prove a repeatable
build before promising a handset JAR. Use only the target CLDC/MIDP APIs, perform
preverification, and package JAR/JAD with matching profile/configuration and
socket permission metadata. No SDK or emulator was installed here. Sources:
[WTK 2.5.2_01 requirements](https://www.oracle.com/java/technologies/sun-java-wireless-toolkit.html)
and [WTK development lifecycle](https://docs.oracle.com/javame/dev-tools/wtk-cldc-2.5.2-01/UserGuide-html/developing.html).

### Stage A: local implementation and tests

1. Verify the approved Go and Java ME toolchain setup. Record exact versions and
   reproducible build commands. If the Java ME tools do not work on the chosen
   host, report that prerequisite failure; do not substitute emulator success for
   K750i compatibility or silently install another environment.
2. Specify the tiny experimental frame layout, encoding fixtures, byte cap, and
   disconnect behavior. Build a Go relay bound to loopback by default, a Go console
   test client on Windows 11, and a minimal MIDlet with recipient/text controls
   and a received-message view. No framework is needed for the relay.
3. Use two fixed disposable test IDs, in-memory connections, and temporary
   messages. There is no registration, production password/token flow, database,
   history service, presence UI, or website in this milestone. These omissions
   are PoC boundaries, not changes to PS-001 through PS-006.
4. Exercise relay/parser tests and the Windows harness against a local Java ME
   emulator. Close sockets on disconnect, bound queues, and report unsuccessful
   sends clearly. Do not claim delivery across disconnection or server restart;
   durable acceptance, replay, and independent multi-session synchronization come
   in later milestones.

| Local acceptance criterion | Required evidence |
| --- | --- |
| Two concurrent clients | Windows harness and MIDlet emulator remain connected for 10 minutes without one blocking the other |
| Bidirectional messages | At least 10 numbered messages each way, including overlapping sends; exact payloads arrive once in order per sending connection during an uninterrupted run, within 2 seconds locally |
| Unicode | Shared fixtures cover ASCII, accented Latin, Cyrillic, combining marks, and a supplementary code point; byte round-trips match. Record unsupported glyphs separately |
| Boundaries and framing | 500 ASCII characters pass; 501 are rejected for that unambiguous length case. Split/coalesced frames work; malformed UTF-8 and over-cap frames fail without crashing or unbounded allocation |
| Reconnection | Three orderly disconnect/reconnect cycles and one forced drop; after reconnection, both directions work, and stale connection state is removed. No automatic replay promise |
| Resource evidence | Record build sizes, server idle/busy memory and CPU, and bytes sent for the fixed exchange; no invented performance threshold |

Go-only loopback tests are useful earlier evidence but do not satisfy the Java ME
emulator criterion. Neither local phase proves GPRS connectivity or XP support.

### Stage B: separately authorized real-device validation

A K750i using GPRS cannot reach the developer machine's loopback endpoint.
This stage requires a controlled network-accessible OGM endpoint, explicit
authorization for its hosting/network exposure, an active suitable SIM/data plan,
and confirmed 2G packet-data availability. It is not authorized or provisioned by
this research task. Prefer outbound direct connections from both clients; keep
the service limited to disposable test identities and remove exposure after testing.

Install the same preverified MIDlet artifact on the actual K750i. Record firmware,
Java platform properties, carrier/APN settings excluding secrets, permission
prompts, endpoint configuration, and build identifiers. Test DNS and permitted
socket access independently so a failure can be localized.

Real-device acceptance requires both the Windows client and **physical K750i** to
connect simultaneously, exchange at least 10 messages each way with the Unicode
fixtures preserved, and reconnect successfully three times. Use an initial
30-second connected-message/reconnect observation window once packet data and
permissions are ready; log actual latency and failures. Any threshold adjustment
must be explicit, not retroactively reported as a pass. Include 10 minutes of
idle time followed by a message in each direction and one deliberate data
interruption. Record whether connection recovery needs user action or a heartbeat.
Do not record current success: this test has not run.

If direct TCP fails, first inspect device exceptions, permissions, port rules,
APN, DNS, and endpoint reachability. A small HTTP diagnostic is the next proposed
comparison only after that evidence. Introduce fallback behavior only with a
documented need; an operator's transport infrastructure is not a reason to invent
an OGM gateway.

## Remaining uncertainties and approval boundary

- Actual K750i firmware behavior, unsigned permission UX, codecs/fonts, interrupted
  blocking I/O, available heap, and practical idle/battery costs remain untested.
- Current SIM/APN/2G availability, port filtering, NAT timeouts, and a controlled
  endpoint have not been established.
- A repeatable Java ME compiler/preverification/emulator setup on the available
  host is a prerequisite, not an assumed capability.
- wxWidgets 3.2 plus the selected XP toolchain must be built and tested; Qt or
  Win32 has not been ruled out by a benchmark.
- Go resource use, SQLite driver choice, WAL settings, write contention, backups,
  and Linux/ARM64 execution require later measurements. No schema is selected.
- Unicode character counting and production delivery/replay/authentication details
  still require decisions within the existing approved product behavior.

Next practical step: approve or revise Task 004's Go/direct-TCP/Java-ME PoC and
toolchain setup, then execute Stage A. Keep remote exposure as a separate later
authorization. SQLite and the production GUI remain proposed follow-up choices;
this document approves no technologies and creates no ADR.
