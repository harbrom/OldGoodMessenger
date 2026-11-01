# Delivery v1 shared codec fixtures

Both codec suites read the same checked-in [fixtures.txt](fixtures.txt).
Expected bytes follow the [protocol layouts](../../docs/research/message-delivery-protocol.md),
independently packed with Python's standard-library `struct` and UTF-8 encoder;
the six design examples were also compared with their literal documented hex.
Neither Go nor Java codec generated this oracle. No fixture generation occurs during tests.

Each ASCII line is `name|mode|fields|wire_hex`. Comments start with `#`.
The first field token is a hexadecimal opcode; subsequent `key=value` integers
are decimal. `text` is literal UTF-16BE code-unit hex so valid pairs and unpaired
surrogates are represented without platform encoding dependencies. Wire hex is
standard UTF-8 where valid. `-` means that column is inapplicable; empty hex is
an empty byte sequence. Stream expectations separate field sets with `/`.

- 26 `valid`: all six design examples; every operation and all four error codes;
  ASCII, Ukrainian, Polish, combining marks, supplementary scalars, NUL/newline;
  minimum one-scalar messages, 500 ASCII/supplementary scalars, maximum ITEM body;
  six/nine-digit ID bounds, 2^63-1 counters, empty and nonterminal END, fresh SYNC,
  batch limits 1 and 8, and empty/nonempty change hints.
- 61 `invalid`: malformed UTF-8, empty/501-scalar text, malformed lengths,
  unknown opcode, IDs outside their range, zero/negative positive-only fields,
  signed-bit u64 values, invalid SYNC bounds/limit, END bounds, error references,
  request opcodes/codes, and fixed operation sizes both short and long.
- 4 `encode-invalid`: unpaired high/low surrogates and invalid surrogate ordering.
  Go represents these as invalid UTF-8 strings, rather than replacing them with U+FFFD.
- 2 `stream`: concatenated operations and decreasing ITEM cursor/message IDs.
  The latter is intentionally valid syntax; ordering across frames belongs to
  a future synchronization state machine, not either codec.

Both suites independently assert decoded fields and exact encoded bytes. Every
valid frame is also read in one-byte chunks and rejected at every truncation
point; invalid global lengths must fail before reading/allocating their bodies.
Go additionally exercises partial/zero writes; both suites check I/O failures.
Whole-buffer decode rejects concatenated frames; stream read consumes one frame.
Malformed streams must be abandoned; neither codec attempts resynchronization.
Only the operation's named fields are serialized; unused frame fields are ignored.

From the repository root, using existing portable tools (no downloads):

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 test -count=1 ./...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/go.ps1 vet ./...
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-delivery-codec.ps1
```

Java production sources compile against CLDC 1.1/MIDP 2.0 APIs and undergo CLDC
preverification. The desktop runner uses Java SE only for fixture loading and
assertions; it is excluded from handset sources. The isolated codec reuses only
the existing pure `ogm.Utf8` helper, never the PoC transport. It is outside the
MIDlet build's source tree. Ignored outputs go under `.build/delivery-v1/`.
These tests establish codec behavior, not v1 handset, networking, durability,
authentication, message ordering, or synchronization-state compatibility.
