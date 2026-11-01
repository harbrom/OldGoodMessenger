# First Contact: local transport PoC

## Authorization and boundaries

Implementation Task 004, explicitly requested by the project owner, authorizes
Go 1.27.x with standard-library TCP, compact length framing, standard UTF-8,
a Go console test client, and a CLDC 1.1/MIDP 2.0 MIDlet **for this experiment only**.
This records the instruction's provenance, not a production architecture decision.
The earlier [technical research](research/technical-foundation.md) remains a
historical proposal. [PS-001 through PS-006](PRODUCT.md) are unchanged.

There are two disposable identities, 100001 and 100002, with one connection per
identity. Duplicate connections are rejected. There are no passwords, tokens,
accounts, persistence, history, offline delivery, acknowledgements, replay,
presence, contact management, database, HTTP endpoint, or website. In-memory
messages can be lost on disconnection, queue overflow, or process exit. These
PoC limits do not replace the approved multi-session and history requirements.

The relay defaults to **127.0.0.1:19090**. Task 005B-1 authorizes an explicit
`-listen IP:port` configuration for a future controlled physical-device test;
wildcard addresses remain rejected. This is preparation only: all validation
traffic in this task uses loopback, with no LAN/public listener, firewall changes,
router changes, or deployment. External exposure requires separate authorization.
Plain TCP provides no confidentiality, peer authentication, or protection from
active modification. Any local process can claim either disposable identity.
Use non-sensitive fixtures only.

## Toolchain and host evidence

The inspected host reports **Windows 10 Enterprise LTSC 2021, 21H2, build
19044.7725**, x64, with an Intel Core i5-9400F. It is not Windows 11. Windows 11,
XP, Linux/ARM64 execution, and physical K750i/GPRS messaging remain untested.
The owner's confirmed K750i installation/UI smoke test is recorded below.

| Tool | Exact version and use |
| --- | --- |
| Go | Official Windows/amd64 ZIP, **1.27.3**; relay, console, tests |
| Eclipse Temurin JDK | **8u504-b01**, `javac 1.8.0_504`, HotSpot **25.504-b01**, Windows x64 ZIP |
| MicroEmulator | **2.0.4**, Java SE headless emulator and desktop test-driver API |
| CLDC API / MIDP API | MicroEmulator `cldcapi11` / `midpapi20` **2.0.4**, compile-only CLDC 1.1 / MIDP 2.0 APIs |
| ProGuard | **6.2.2**, CLDC preverification; no shrinking, optimization, or obfuscation |
| Python | Existing **3.9.5**, standard-library local integration/measurement harness |
| PowerShell / Git | Existing **5.1.19041.7725** / **2.32.0.windows.1** |

Inventory included PATH, registry software entries, and standard installation
directories. Existing Oracle JRE 8u45 and Visual Studio 2017 installations
(Build Tools 15.9.28307.1525, Enterprise 15.9.28307.1585, MSVC 14.16.27023)
were found outside a usable Go/Java ME PATH. They are not used for these builds.
No Go, Java ME SDK, or suitable preverification workflow was initially available.
The inventory was not exhaustive: access to the user-home listing and CIM OS
query was denied; OS registry/version queries supplied the host evidence above.

Portable tools are stored under `.tools/`. No system-wide installer, administrator
elevation, permanent PATH modification, or framework installation was used.
The setup scripts pin SHA-256 hashes and stop on mismatch before extraction or
execution. Official project release channels are used, not third-party archives:

- [Go official releases](https://go.dev/dl/): Windows ZIP and published SHA-256.
- [Eclipse Adoptium releases](https://adoptium.net/temurin/releases/): official
  Temurin GitHub release ZIP, verified against the Adoptium API checksum.
- [MicroEmulator project](https://github.com/barteo/microemu): project-published
  `org.microemu` 2.0.4 artifacts from Maven Central. Repository checksums were
  checked at acquisition; exact SHA-256 pins are in the setup script.
- [ProGuard preverification documentation](https://www.guardsquare.com/manual/configuration/usage):
  `-microedition` produces CLDC `StackMap` attributes. The 6.2.2 project-published
  `net.sf.proguard:proguard-base` artifact comes from Maven Central.

Tool archives remain ignored and are not redistributed in Git. The MIDlet JAR
contains only OGM classes and metadata; it does not bundle API stubs, emulator
code, or ProGuard. No external runtime library is needed by the Go executables.
The portable open-source workflow avoids executing an obsolete SDK installer.

## Reproduce the builds

Run these commands in PowerShell from the repository root. `-ExecutionPolicy
Bypass` applies only to the launched PowerShell process; the host's persistent
execution policy is not changed. Review the scripts before running them.

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/setup-go.ps1
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/setup-java-me.ps1

powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 build -trimpath -ldflags=-s -o .build/ogm-server.exe ./server
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 build -trimpath -ldflags=-s -o .build/ogm-console.exe ./clients/console
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-java-me.ps1
```

Downloads require outbound HTTPS. Subsequent builds use local tools. The Go
wrapper selects the pinned executable, sets `GOTOOLCHAIN=local`, disables module
proxy downloads, and keeps build/module caches in `.cache/`. `go.mod` has no
third-party dependencies. Pass individual native flags through the wrapper;
the commands above avoid Windows PowerShell's multiword argument quoting issue.

Java compilation uses `-source 1.3 -target 1.1` and **only** the CLDC/MIDP API
JARs as its boot classpath, followed by ProGuard `-microedition -target 1.1`.
Obsolete source/target warnings are expected. No missing-reference warnings are
suppressed. Packaging produces:

```text
.build/java-me/ogm-first-contact/ogm-first-contact.jar
.build/java-me/ogm-first-contact/ogm-first-contact.jad
```

The JAD records CLDC-1.1, MIDP-2.0, socket permission, relative JAR URL, and exact
JAR size. `OGM-Server` is configurable in the JAD and MIDlet form. It is deliberately
absent from the manifest so the emulator does not override the JAD value.
JAR ZIP timestamps can differ between builds; this is a pinned, repeatable build
workflow, not a claim of byte-for-byte reproducible JARs.

## Manual local use

Start `.build/ogm-server.exe` in one terminal (default `127.0.0.1:19090`). In two
other terminals, run `.build/ogm-console.exe -id 100001` and
`.build/ogm-console.exe -id 100002`. Set terminal input/output encoding to UTF-8
when exercising non-ASCII text. Commands are:

```text
/send 100002 Hello
/send 100001 A self-message when connected as 100001
/disconnect
/connect
/quit
```

### Explicit listener configuration

The existing `-listen` option now accepts an explicitly selected local interface
address. Without it, the server still binds only `127.0.0.1:19090`:

```powershell
.build/ogm-server.exe
.build/ogm-server.exe -listen 127.0.0.1:19091
.build/ogm-server.exe -listen 127.0.0.1:0
```

Port `0` requests a temporary OS-assigned port. Startup prints the actual bound
address; use that address with the console client's `-server` option. An occupied
port or address not assigned locally fails with a clear bind error, without
falling back to another address.

For a separately authorized future interface test, the same syntax accepts a
specific assigned LAN IPv4 address, for example `-listen <LAN-IPv4>:19090`
(replace the placeholder). IPv6 literals require brackets: `[IPv6]:port`.
Hostnames, empty hosts, wildcard addresses (`0.0.0.0`, `::`, including mapped
forms), multicast addresses, nondecimal ports, and ports outside 0..65535 are
rejected. No hostname lookup or implicit all-interface binding occurs. Do not
start a LAN/public listener as part of the current preparation task.

The console accepts `-server 127.0.0.1:19090`. Reconnection is explicit; after an
abrupt drop, retry `/connect` if cleanup has not yet released the identity.
No client promises automatic replay. Stop the relay with Ctrl+C.

For a manual emulator UI, close the console using 100002, then run:

```powershell
& .tools/java-me/jdk/jdk8u504-b01/bin/java.exe -Dfile.encoding=UTF-8 -jar .tools/java-me/microemulator-2.0.4.jar .build/java-me/ogm-first-contact/ogm-first-contact.jad
```

This opens a desktop emulator window for interactive use; automated validation
uses its headless entry point. The MIDlet provides Server, Test ID, Recipient,
Message, and Events fields plus Connect, Send, Disconnect, and Quit commands.
Network work is off the UI thread. Only one send can be outstanding. The event
view retains at most 2,048 UTF-16 code units and coalesces UI update callbacks.
The automated checks exercise controls programmatically, not handset keys or fonts.

## Experimental wire format

Every frame begins with a **two-byte unsigned big-endian body length**, excluding
the prefix. Bodies are 2 through 2,005 bytes; the maximum wire frame is 2,007 bytes.
All integer fields below are unsigned, big-endian. There is no delimiter,
terminating NUL, compression, version negotiation, TLS, or Java modified UTF-8.

| Direction / operation | Body layout | Exact body size |
| --- | --- | --- |
| Client HELLO `01` | opcode + 4-byte test ID | 5 |
| Server READY `81` | opcode + 4-byte accepted test ID | 5 |
| Client SEND `02` | opcode + 4-byte recipient ID + standard UTF-8 text | 6..2005 |
| Server MESSAGE `82` | opcode + 4-byte sender ID + standard UTF-8 text | 6..2005 |
| Server FAILURE `ff` | opcode + 1-byte error code | 2 |

Failure codes: `01` invalid handshake, `02` recipient unavailable/queue closed,
`03` identity already connected. Excess connections close immediately. Malformed
post-handshake frames close that sender. A failure frame is best effort, not a
message acknowledgement, and has no message correlation ID. A successful socket
write or absence of an error does not prove recipient delivery.

HELLO must be first; wait for READY before SEND. Only 100001 and 100002 are valid
IDs. The server derives MESSAGE's sender from the established connection, not
from a client-supplied sender field. Self-routing uses the same path.

Text must contain **1..500 Unicode scalar values**, at most 2,000 UTF-8 bytes.
This counting rule is experimental; production grapheme/code-point counting is
still unresolved. Combining marks count individually, supplementary values count
once, and empty text is rejected. Overlong UTF-8, surrogate encodings, stray
continuations, truncation, and values above U+10FFFF are rejected. The MIDlet
handles valid UTF-16 surrogate pairs explicitly and rejects unpaired surrogates.
`writeUTF`/`readUTF` are never used. NUL/newline byte fixtures are tested at codec
level; the console's line-command interface is not a multiline editor.

Example: HELLO for 100001 is `00 05 01 00 01 86 a1`. Sending ASCII `A` to
100002 is `00 06 02 00 01 86 a2 41`: seven bytes of framing/identity overhead
per text frame. TCP segmentation and coalescing do not define message boundaries.

## Bounds and connection lifecycle

- At most 8 admitted TCP connections, including pending handshakes; only two can
  hold distinct active identities. Excess connections are promptly closed.
- Each connection has a 16-frame outbound queue. Queue insertion never waits;
  overflow disconnects the slow recipient. A closed queue is not durable storage.
- At most 32,000 text payload bytes per full active queue, plus bounded frame,
  string, channel, and runtime overhead. At most two active queues can hold
  routed text. This is an application bound, not a process RSS or kernel-buffer cap.
- Five seconds to finish HELLO; five seconds per write and to finish an inbound
  frame after its first byte. Idle established connections have no deadline or
  heartbeat. These local experimental timeouts are not validated for GPRS.
- Reader/writer failures close the connection; identity removal checks the peer
  instance so stale cleanup cannot remove a different session. Server cancellation
  closes the listener and all admitted connections, then joins handlers.
- CLDC sockets lack a portable reliable cancellation/deadline guarantee across
  devices. Disconnect closes streams and the connection on worker threads. A
  handset stuck inside `Connector.open` may need application restart; no unbounded
  sequence of new connection workers is created while the previous one is alive.

Frame/queue/admission bounds limit retained memory. They do not impose a CPU
rate limit or replace production anti-spam requirements.

## Automated verification and results

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 test -count=1 ./...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 vet ./...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 test ./internal/wire -run '^$' -fuzz FuzzRead -fuzztime 20s -parallel 2
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 test ./internal/wire -run '^$' -bench . -benchmem
C:/Python39/python.exe -u scripts/test-local.py --ui --idle-seconds 600
```

Use the actual Python 3 interpreter path on another host. The harness starts only
its own loopback relay, console, and headless emulator; it terminates those child
processes in cleanup. It validates the packaged MIDlet's inputs, commands, received
text, and reconnection through a desktop-only driver. Reflection in that driver
is not included in the handset JAR. It also verifies all seven packaged classes
are version 45.3, finds CLDC StackMap metadata, checks the JAD size, and compares
the MIDlet UTF-8 codec with independent JDK UTF-8 reference bytes.

For an additional transport-only emulator test MIDlet, run
`scripts/build-java-me.ps1 -Test` through the same PowerShell invocation, followed
by `scripts/test-local.py` without `--ui`. This test artifact is separate from the
installable UI MIDlet. Both use the same `Transport` and `Utf8` source classes.

Measured on the host above during Task 004:

| Check | Observed result |
| --- | --- |
| Go tests | Pass: concurrent bidirectional routing, self-routing, split/coalesced frames, partial writes, maximum lengths, malformed input, duplicate IDs, offline errors, ten reconnect cycles, TCP reset cleanup, stalled-frame expiry, queue overflow, admission cap, shutdown, loopback restriction |
| Unicode | Pass: ASCII, Ukrainian, Polish, combining marks, supplementary characters; 500 ASCII and 500 supplementary values accepted; 501 rejected; malformed bytes/surrogates rejected. The Java codec also passed comparison with independent JDK UTF-8 bytes for every valid Unicode scalar |
| `go vet` | Pass |
| Parser fuzzing | Pass: 20 seconds, 2 workers, 1,882,542 executions; no failing corpus |
| Parser microbenchmark | 321.1 ns/op, 194 B/op, 5 allocations/op; one run, not a network throughput result |
| Java build/preverification | Pass; CLDC/MIDP-only API compile, ProGuard success, 45.3 class versions and StackMap inspection |
| Packaged MIDlet UI in headless MicroEmulator | Pass: 41 exact Unicode echo exchanges through the Go console, four emulator connections, three reconnections, and 600.016 seconds idle followed by working message exchange. Each ordinary local echo constrained to 2 seconds |
| Relay load | Pass: 20,000 routed messages, each 500 supplementary values / 2,000 UTF-8 bytes, with exact byte checks |
| JAR/JAD and Markdown checks | Pass: matching JAR size, seven OGM-only classes at version 45.3, CLDC StackMap inspection, internal Markdown file links, and `git diff --check` |
| Go race detector | Blocked: default cgo disabled; retry with `CGO_ENABLED=1` fails because `gcc` is absent from PATH. Existing MSVC is not a substitute for the required Go C toolchain |

The first emulator run failed because generated JAD attributes were separated by
a blank line. The next exposed manifest/JAD server-address precedence. Both
packaging issues were corrected and the successful UI test used the resulting
artifact. PowerShell initially blocked script execution and mishandled forwarded
build flags; the documented process-local invocation and single-value flags work.
These failures are not counted as successful checks.

Build sizes with the commands above: relay **2,452,992 bytes**, console
**2,393,600 bytes**, installable MIDlet JAR **9,429 bytes**, JAD **366 bytes**.
Sizes exclude toolchains and runtime OS components. The test-only MIDlet JAR is
not the handset artifact. No visible font rendering, actual KVM installation,
GPRS, XP, or Windows 11 compatibility is established by these results.

The saved completed run records **600.016 seconds** of idle time. Relay working
set changed from 6,995,968 to 7,004,160 bytes and private bytes from 13,447,168 to
13,455,360; measured CPU delta was 0 seconds at the available timer resolution.
The subsequent 20,000-message load took 1.422 seconds, used 0.890625 CPU seconds,
and increased sampled working set from 7,032,832 to 13,172,736 bytes and private
bytes from 13,467,648 to 19,644,416. These are samples, not peak measurements,
steady-state heap estimates, or performance guarantees. The recovery audit
confirmed the saved JSON's PASS status and matched all 41 console payloads
against the expected Unicode fixtures; it did not repeat these long/load tests.

The 41-echo exchange accounts for 3,076 application bytes across all four relay
directions, excluding handshakes, TCP/IP overhead, ACKs, and retransmission.
The load accounts for 80,280,000 application bytes on the same basis. These are
calculated from exact exchanged frames, not packet-capture traffic measurements.
The harness writes local logs, class inspection, and JSON measurements under
`.build/`; they are ignored and can be reproduced.

### Finalization: reconnect assertion and short checks

The original UI driver cleared only the rendered event widget, leaving old
`Connected` entries in the MIDlet's retained buffer. The test now clears both
the retained buffer and its view immediately before starting each connection
attempt, on the UI thread while holding the same monitor used by `event()`.
This creates a fresh event boundary: an earlier entry cannot reappear and satisfy
the new connection assertion. No application code or wire behavior changed.

A negative regression check seeds an old `Connected as 100002` entry, then
attempts a connection using 100001, which the console already owns. The assertion
must time out rather than accept the stale entry. The driver then completes four
successful connections, three reconnections, and 41 exact Unicode echoes.

```powershell
C:/Python39/python.exe -u -B scripts/test-local.py --quick
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 test -count=1 -run 'Test(BidirectionalAndSelfMessaging|ReconnectDuplicateAndOffline|AbruptDisconnectAndBoundaries)$' ./internal/relay
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 vet ./...
git diff --check
```

The short emulator check passes with the corrected assertion and validates the
existing JAR/JAD. `--quick` selects the UI driver with no idle delay, skips the
exhaustive codec and 20,000-message load tests, and saves separate logs/results
under `.build/short-check/`. The original completed-run logs and measurements
remain intact. Targeted Go routing/reconnection tests, `go vet`, internal Markdown
file links, and whitespace checks also pass. The 600.016-second run predates the
test-only assertion correction; it was not repeated during finalization.

Emulator exchanges are sequential request/echo pairs; overlapping bidirectional
sends are covered by Go tests. Java SE headless emulator results do not establish
physical K750i/GPRS connectivity, full handset rendering or cancellation behavior,
or Windows XP/Windows 11 compatibility. Those remain unverified. The actual host
was Windows 10 Enterprise LTSC; Go race detection remains blocked by missing
`gcc` rather than being reported as a pass.

## Physical K750i installation smoke test: November 1, 2026

The project owner reports that the unsigned MIDlet was installed and successfully
launched on a physical Sony Ericsson K750i. Text input, navigation, and normal
application exit were verified. This is owner-provided physical-device evidence,
not an inference from MicroEmulator. No physical GPRS connection or message
exchange has been verified. Firmware, network profile, network permission prompts,
full glyph coverage, and interrupted socket cancellation remain unverified.

## Task 005B-1: server preparation and local validation

Only listener configuration changed. The wire protocol, Java ME application,
two disposable identities, eight-connection admission cap, 16-frame queues,
2,005-byte body limit, and five-second deadlines are unchanged. The production
architecture and canonical PS-001 through PS-006 remain unchanged.

Short checks passed:

- `go test -count=1 ./...`: existing relay/protocol tests plus explicit listener
  parsing, invalid addresses/ports, mapped-wildcard rejection, loopback binding,
  and occupied-port error coverage. LAN address acceptance is tested by parsing
  only, without opening a LAN socket.
- `go vet ./...` and `git diff --check`.
- Built server without flags: startup and OS socket inspection confirmed only
  `127.0.0.1:19090` on that port.
- Built server with `-listen 127.0.0.1:0`: printed a real allocated loopback port;
  two instances of the unchanged console client exchanged one message each way.
- Built-server invalid configurations (empty/wildcard host, hostname, nonnumeric
  or out-of-range port) exited unsuccessfully with configuration errors.

Builds and local results are ignored under `.build/`; listener/console evidence
is under `.build/gprs-preparation/`. No ten-minute idle, 20,000-message load,
exhaustive Unicode, emulator, LAN connectivity, or GPRS tests were repeated here.

### Read-only network readiness assessment

The active Ethernet adapter has a private RFC 1918 IPv4 address, a private IPv4
gateway, and the preferred default IPv4 route. This suggests a local router/NAT;
it does not establish the router's WAN address, static addressing, upstream NAT,
carrier-grade NAT, ISP filtering, or inbound public reachability. A virtual/VPN
adapter also exists; do not confuse its address with the LAN or a public endpoint.
Exact host/gateway addresses are intentionally excluded from committed docs.
See [RFC 1918](https://www.rfc-editor.org/rfc/rfc1918).

Windows Firewall read-only COM inspection reports active Private and Public
profiles, both enabled, with default inbound blocking and outbound allowance.
The PowerShell CIM firewall query was denied; the COM fallback succeeded without
elevation. Per-program/port exceptions were not audited. A future authorized test
must review the applicable profile and any existing allow/block rules; if needed,
use a temporary inbound rule scoped to the selected TCP port/program and test
sources, then remove it afterward. Do not disable the firewall. No rules were
created or changed. Source: [Microsoft Windows Firewall rules](https://learn.microsoft.com/en-us/windows/security/operating-system-security/network-security/windows-firewall/rules).

Public IPv4 reachability cannot be established from this local evidence. No
external port scan, public-IP lookup, router administration, or public-endpoint
probe was performed. A private LAN listener alone will not make the PC reachable
from GPRS.

## Requirements for the separately authorized GPRS test

First confirm a controlled endpoint's public IPv4 reachability and obtain explicit
authorization for temporary external exposure. If using this PC behind its router,
verify the router WAN/public IPv4 and upstream NAT/CGNAT situation before planning
any port forwarding, plus the required firewall permission. No such changes or
public endpoint were provisioned here. Select a single interface/address and a
permitted external TCP port, retain the disposable IDs, and define test duration
and cleanup. The unauthenticated plaintext PoC remains limited to test traffic.

Confirm the handset's firmware, SIM/data plan, functioning 2G packet data, Internet
APN/Java profile, and socket permission behavior. Avoid the unsigned-handset
80/443/8080 port restriction described in the research. Enter the authorized
reachable host and port in the existing MIDlet Server field; its current
`127.0.0.1:19090` default refers to the phone itself, not this development PC.
No MIDlet code or protocol change is required to select an endpoint.

Then record permission prompts, DNS/socket errors, exact Unicode byte preservation
versus glyph rendering, bidirectional messages, reconnection, and idle behavior.
Use the [research acceptance criteria](research/technical-foundation.md) as the
starting proposal. Local listener validation and the physical UI smoke test do
not prove GPRS delivery. Production architecture, authentication, history, session
synchronization, Windows GUI, and Unicode counting remain later decisions within
approved product scope.
