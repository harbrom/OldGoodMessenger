# K750i offline storage smoke test

Task 012A authorizes this separate diagnostic MIDlet only. **Physical handset
results are pending.** Earlier [First Contact](first-contact.md) hardware results
do not establish Delivery v1 storage compatibility. Product scope and production
architecture are unchanged.

## Build and artifacts

Use the installed portable tools, from the repository root:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-k750i-smoke.ps1
C:/Python39/python.exe -u -B scripts/test-k750i-smoke.py
```

The second command rebuilds and tests the actual packaged MIDlet. On another host,
use its Python 3 executable. No tool installation or download is performed.

- JAR: `.build/java-me/ogm-storage-smoke/ogm-storage-smoke.jar`
- JAD: `.build/java-me/ogm-storage-smoke/ogm-storage-smoke.jad`
- Build observed here: **21,312-byte JAR**, **293-byte JAD**, version **0.0.1**.

The installed JDK 8u504-b01 compiles against only CLDC 1.1/MIDP 2.0 APIs.
ProGuard 6.2.2 shrinks unused code and preverifies class version 45.3 with CLDC
StackMaps; it does not optimize or obfuscate. Compilation includes HistoryReader
for HistoryStore's transitive type signatures, but the unused replay/networking
path is removed from this JAR. Package checks reject socket/emulator dependencies
and unrelated classes. There are no network permissions, desktop test runners,
API stubs, external runtime libraries, or First Contact classes in the package.
All generated artifacts and emulator logs/RMS remain ignored under `.build/`.
Existing First Contact sources and artifacts are preserved.

## Transfer and install

Use the Bluetooth JAR-transfer workflow that already installed First Contact on
the owner's handset. Send **ogm-storage-smoke.jar**, accept the transfer, and choose
installation into Applications when offered. JAR-only installation is intended:
all required MIDlet metadata is also in its manifest. Keep the matching JAD with
the build; if the handset requests a descriptor, transfer both unchanged together.
Do not replace or uninstall First Contact.

Alternatively, with an already-working USB/file-transfer setup, copy the package
to the Memory Stick's `\MSSEMC\Media files\other\`, safely disconnect, locate it
in File manager / Other, and select installation. The
[Sony Ericsson K750i user guide](https://www-support-downloads.sonymobile.com/k750/userguide_EN_K750.pdf)
describes Bluetooth reception on printed page 62, Java transfer folder on page 65,
and Applications management on page 70. Menu wording may vary with firmware.
No PC Suite installation, network profile, SIM data session, server, or public
endpoint is needed. Record any installation error or unexpected permission prompt;
this package requests no network access.

## Handset command sequence

Launch **OGM Storage Smoke**. Scroll the standard Form to see state, writer,
history, and the last result; commands may be under the handset's More menu.
Startup does not create or inspect RMS automatically. Wait for each operation
to finish before selecting the next command.

| Command | Expected first-run result |
| --- | --- |
| Inspect RMS | Both stores missing/unavailable; visible failure, no creation |
| Provision writer | Explicit new writer; account 100001, disposable test writer 12001, last sequence 0, pending none |
| Init history | Explicit separate empty history; checkpoint 0, ITEM count 0 |
| Reserve SEND | Sequence 1, recipient 100002, pending SEND; **local only, not delivered** |
| Verify pending | Exact sequence, recipient and fixed Unicode payload validated twice across close/reopen |
| Inspect history | Checkpoint 0, ITEM count 0, no writes |
| Timing once | Coarse millisecond timings; one identical small writer-record rewrite, reopen verification, read-only history check |
| Exit | Clean application exit, deferred until any active storage operation closes |

The fixed text is `OGM offline smoke: Привіт / Zażółć / é / 🙂` (the accented
`e` is U+0065 U+0301). Verification compares the entire stored string including
the supplementary surrogate pair, not rendered glyphs. The UI reports validation
instead of displaying a conversation. No ACK is fabricated and no SEND leaves
the phone. The pending entry is deliberately never cleared.

After **Exit**, launch the same installed application again. Run **Inspect RMS**,
**Verify pending**, and **Inspect history**: expect sequence 1/pending SEND and
checkpoint 0/count 0. Do not reinstall or remove application data between launches.
Optionally repeat after an ordinary phone reboot. Do not remove the battery or
interrupt writes to test power-loss durability in this task.

Repeated Provision writer / Init history must refuse existing stores, and a
second Reserve SEND must refuse the existing pending entry. Neither is a reset.
Dedicated private names are `ogm-smoke-writer-v1` and `ogm-smoke-history-v1`;
history's local test namespace is 12001. These fixed disposable bindings must
never be used online or reused for production writer provisioning.

## Timing, failures, and lifecycle

Record provisioning and reserve durations from their last-result screens before
another command replaces them. Those durations include open/provision or reserve
and close. Verify pending measures two complete open/validate/close cycles.
Timing once reports writer open/read, one identical write, reopen/read/close, and
history open/read/close separately. All use `System.currentTimeMillis()`; zero,
clock changes, and coarse resolution are possible. These are observations, not
performance guarantees. Run the timing command once per manual run; it requires
the reserved pending SEND and initialized history. No large records are written.

Storage runs on one worker. Overlapping commands are refused; pause allows an
already-started operation to finish and close, while blocking new commands until
resume. Output retains only the current bounded status/result. Both RMS adapter
types close after every operation, including failed factories; a close failure
gets one retry and remains a visible failure. If ownership remains unavailable,
exit and relaunch rather than repeatedly starting work.

Missing state after a previously successful provision/relaunch, a sequence other
than 1, missing/mismatched pending data, CRC/format errors, or nonzero history
checkpoint/count are failures for this offline sequence. Stop and capture the
visible exception and last successful command. No automatic repair, deletion,
reset, or fallback provisioning exists. Preserve the installed suite/data for
diagnosis; do not try to recover by overwriting stores.

## Verification evidence and limits

MicroEmulator 2.0.4 exercises the packaged LCDUI application in separate JVMs
with file RMS: missing-state refusal, explicit provisioning/reserve, reopened
exact pending/empty history, repeated-operation refusal, one-write timing bound,
corruption rejection without repair across restarts, adapter release, overlapping
commands, pause/resume/repeated start, and exit during work. The desktop driver
injects corruption only into its isolated emulator store; the handset MIDlet has
no corruption command. Logs are in `.build/k750i-smoke-test/<run>/`.

Final local verification passed on the existing Windows 10 host:

| Check | Observed result |
| --- | --- |
| `scripts/test-k750i-smoke.py` | 115 checks across five JVMs; package metadata, 45.3/StackMaps, unchanged First Contact artifacts |
| `scripts/test-java-me-sender.ps1` | 690 focused checks and nine emulator phases |
| `scripts/test-java-me-outbox.ps1` | 346 focused checks and three RMS restart phases |
| `scripts/test-java-me-history.ps1` | 307 focused checks and 22 emulator phases |
| `scripts/test-java-me-durable-history.ps1` | 1,566 focused checks and 31 emulator phases |
| `scripts/test-delivery-codec.ps1` | 93 shared fixtures |
| `scripts/go.ps1 test ./...` / `vet ./...` | Pass; ordinary Go test results were cached |

In one final desktop emulator run, writer provisioning/close took 6 ms, reserve
open/write/close 3 ms, two exact reopen checks 1 ms, and history provisioning/close
2 ms. The separate timing command observed writer read 0 ms, identical write 1 ms,
reopen/read/close 0 ms, history open/read/close 0 ms. These coarse desktop samples
are not handset measurements or performance targets. Obsolete Java source/target
warnings are expected with this installed compatibility toolchain.

Emulator success does not prove K750i installation, key navigation, firmware RMS
behavior, timings, flash endurance, arbitrary power-loss safety, heap limits, or
large populated history support. This MIDlet only initializes/reads empty history;
it does not synthesize ITEMs, server replies, or checkpoint advancement. Existing
[durable-history tests](../clients/java-me/delivery-v1/README.md) cover populated
snapshots in the emulator separately.

## Manual results template

```text
Date / tester:
Device model: Sony Ericsson K750i
Firmware version (if known):
JAR size/version: 21312 bytes / 0.0.1
Installation and launch outcome / exact installation error:
Command outcomes (including expected missing/existing-store refusals):
After Exit + relaunch: writer / last sequence / pending / checkpoint / ITEM count:
Ordinary reboot + relaunch (optional):
Observed ms: provision / reserve / verify close-reopen / history read:
Timing once ms: writer read / identical write / reopen-read-close / history read:
Pause/resume, navigation, normal exit:
Exact exceptions / last successful command / photo references:
Overall: PASS / FAIL / INCOMPLETE (explain):
```
