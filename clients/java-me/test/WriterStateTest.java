import java.io.IOException;
import ogm.delivery.v1.Codec;
import ogm.delivery.v1.StateStorage;
import ogm.delivery.v1.WriterState;

/** CLDC-only executable tests; the fake shares bytes across independent handles. */
public final class WriterStateTest {
    private static int checks;
    private static final int ACCOUNT = 100001, RECIPIENT = 100002;
    private static final long WRITER = 7;

    private static void check(boolean value, String message) throws Exception {
        checks++;
        if (!value) throw new Exception(message);
    }

    private static final class Disk {
        byte[] record;
        int writes, mode;
    }

    private static final class Memory implements StateStorage {
        private final Disk disk;
        private boolean closed;
        Memory(Disk disk) { this.disk = disk; }
        public byte[] read() throws IOException {
            if (closed || disk.mode == 4) throw new IOException("Injected read failure");
            return cloneBytes(disk.record);
        }
        public void write(byte[] value) throws IOException {
            if (closed) throw new IOException("Closed fake");
            disk.writes++;
            if (disk.mode == 1) throw new IOException("Failed before write");
            disk.record = cloneBytes(value);
            if (disk.mode == 3) disk.record[0] ^= 1;
            if (disk.mode == 2 || disk.mode == 3) throw new IOException("Write outcome uncertain");
        }
        public void close() throws IOException {
            if (disk.mode == 5) throw new IOException("Injected close failure");
            closed = true;
        }
    }

    private static byte[] cloneBytes(byte[] bytes) {
        if (bytes == null) return null;
        byte[] result = new byte[bytes.length];
        System.arraycopy(bytes, 0, result, 0, bytes.length);
        return result;
    }

    private static void same(byte[] a, byte[] b) throws Exception {
        check(a != null && b != null && a.length == b.length, "Byte lengths differ");
        for (int i = 0; i < a.length; i++) check(a[i] == b[i], "Byte mismatch");
    }

    private static byte[] hex(String text) {
        byte[] value = new byte[text.length() / 2];
        for (int i = 0; i < value.length; i++) value[i] = (byte)Integer.parseInt(text.substring(i * 2, i * 2 + 2), 16);
        return value;
    }

    private static WriterState fresh(Disk disk) throws IOException {
        return WriterState.provision(new Memory(disk), ACCOUNT, WRITER);
    }
    private static WriterState reopen(Disk disk) throws IOException {
        return WriterState.open(new Memory(disk), ACCOUNT, WRITER);
    }
    private static Codec.Frame ack(long seq) {
        Codec.Frame frame = new Codec.Frame(); frame.kind = Codec.ACK; frame.clientSeq = seq; frame.messageID = 42;
        return frame;
    }
    private static Codec.Frame failure(long seq, int code) {
        Codec.Frame frame = new Codec.Frame(); frame.kind = Codec.ERROR;
        frame.requestOp = Codec.SEND; frame.reference = seq; frame.code = code;
        return frame;
    }
    private static void reserveFails(WriterState state, int recipient, String text) throws Exception {
        try { state.reserve(recipient, text); throw new Exception("Invalid reserve succeeded"); }
        catch (IOException expected) { checks++; }
    }
    private static void resolveFails(WriterState state, Codec.Frame frame) throws Exception {
        try { state.resolve(frame); throw new Exception("Invalid resolve succeeded"); }
        catch (IOException expected) { checks++; }
    }
    private static void blocked(WriterState state) throws Exception {
        reserveFails(state, RECIPIENT, "must not allocate");
        try { state.pending(); throw new Exception("Unverified pending exposed"); } catch (IOException expected) { checks++; }
        try { state.lastSequence(); throw new Exception("Unverified counter exposed"); } catch (IOException expected) { checks++; }
        resolveFails(state, ack(1));
    }
    private static void openFails(Disk disk, int account, long writer) throws Exception {
        Memory handle = new Memory(disk);
        try {
            try { WriterState.open(handle, account, writer); throw new Exception("Invalid state opened"); }
            catch (IOException expected) { checks++; }
        } finally { handle.close(); }
    }

    private static void restartAndResponses() throws Exception {
        Disk disk = new Disk();
        WriterState state = fresh(disk);
        check(state.accountID() == ACCOUNT && state.writerID() == WRITER && state.lastSequence() == 0 && state.pending() == null, "Bad provisioning");
        check(disk.record.length == 30, "Idle layout size");
        // Independent idle-layout oracle: big-endian fields plus Python zlib CRC32.
        same(disk.record, hex("4f474d4f01000186a1000000000000000700000000000000000077b868fd"));
        check(disk.writes == 1, "Provision did not persist");
        String text = "ASCII \u041f\u0440\u0438\u0432\u0456\u0442 Za\u017c\u00f3\u0142\u0107 e\u0301 \ud83d\ude42\u0000\n";
        Codec.Frame sent = state.reserve(RECIPIENT, text);
        byte[] exact = Codec.encode(sent);
        check(sent.clientSeq == 1 && state.lastSequence() == 1, "Sequence not reserved");
        sent.recipient = ACCOUNT; sent.clientSeq = 999; sent.text = "caller mutation";
        same(exact, Codec.encode(state.pending()));
        reserveFails(state, RECIPIENT, "replacement");
        int writes = disk.writes;
        // A lost ACK/transport failure changes nothing; simply close/reopen.
        state.close(); state = reopen(disk);
        same(exact, Codec.encode(state.pending()));
        check(state.writerID() == WRITER && state.lastSequence() == 1, "Identity/counter lost");
        check(!state.resolve(failure(1, Codec.RETRYABLE)), "RETRYABLE acted as ACK");
        resolveFails(state, ack(2));
        Codec.Frame bad = ack(1); bad.messageID = 0; resolveFails(state, bad);
        resolveFails(state, failure(2, Codec.REJECTED));
        bad = failure(1, Codec.REJECTED); bad.requestOp = Codec.SYNC; resolveFails(state, bad);
        resolveFails(state, failure(1, Codec.CURSOR_INVALID));
        bad = new Codec.Frame(); bad.kind = Codec.CHANGED; bad.tail = 1; resolveFails(state, bad);
        resolveFails(state, null);
        check(disk.writes == writes, "Unexpected response wrote state");
        same(exact, Codec.encode(state.pending()));
        check(state.resolve(ack(1)), "Matching ACK not cleared");
        state.close(); state = reopen(disk);
        check(state.pending() == null && state.lastSequence() == 1, "ACK clear not persisted");
        resolveFails(state, ack(1));
        check(state.reserve(ACCOUNT, "self").clientSeq == 2, "Self-message sequence wrong");
        check(state.pending().recipient == ACCOUNT, "Self-message changed");
        check(state.resolve(failure(2, Codec.REJECTED)), "REJECTED not cleared");
        state.close(); state = reopen(disk);
        check(state.pending() == null && state.lastSequence() == 2, "Rejected sequence reused");
        check(state.reserve(RECIPIENT, "conflict").clientSeq == 3, "Sequence reused after rejection");
        check(state.resolve(failure(3, Codec.CONFLICT)), "CONFLICT not cleared");
        state.close(); state = reopen(disk);
        check(state.pending() == null && state.lastSequence() == 3, "Conflict clear not persisted");
        check(state.reserve(RECIPIENT, "next").clientSeq == 4, "Sequence reused after conflict");
        state.close(); state.close(); blocked(state);
    }

    private static String repeat(String text, int count) {
        StringBuffer buffer = new StringBuffer();
        for (int i = 0; i < count; i++) buffer.append(text);
        return buffer.toString();
    }

    private static void textLimits() throws Exception {
        Disk disk = new Disk(); WriterState state = fresh(disk);
        String maximum = repeat("\ud83d\ude42", 500);
        state.reserve(RECIPIENT, maximum);
        check(disk.record.length == WriterState.MAX_RECORD, "Maximum record not 2045 bytes");
        state.close(); state = reopen(disk);
        check(state.pending().text.equals(maximum), "Supplementary text did not reopen");
        check(Codec.encode(state.pending()).length == 2015, "Maximum SEND size");
        state.resolve(ack(1));
        int writes = disk.writes;
        reserveFails(state, RECIPIENT, repeat("x", 501));
        reserveFails(state, RECIPIENT, repeat("\ud83d\ude42", 501));
        reserveFails(state, RECIPIENT, "\ud800"); reserveFails(state, RECIPIENT, "\udc00");
        reserveFails(state, RECIPIENT, ""); reserveFails(state, RECIPIENT, null);
        reserveFails(state, 99999, "bad id"); reserveFails(state, 1000000000, "bad id");
        check(disk.writes == writes && state.lastSequence() == 1, "Invalid text allocated/wrote");
        state.reserve(RECIPIENT, repeat("x", 500)); state.resolve(ack(2));
        state.reserve(RECIPIENT, "\u0000"); state.close(); state = reopen(disk);
        check(state.pending().text.equals("\u0000"), "NUL-only message lost");
        state.close();
    }

    // Independent test mutation checksum, intentionally not production API.
    private static void fixChecksum(byte[] data) {
        long crc = 0xffffffffL;
        for (int i = 0; i < data.length - 4; i++) {
            crc ^= data[i] & 255;
            for (int j = 0; j < 8; j++) crc = ((crc & 1) != 0) ? (crc >>> 1) ^ 0xedb88320L : crc >>> 1;
        }
        crc ^= 0xffffffffL;
        for (int i = 0; i < 4; i++) data[data.length - 4 + i] = (byte)(crc >>> (24 - i * 8));
    }

    private static void corruptAndMissing() throws Exception {
        Disk disk = new Disk(); openFails(disk, ACCOUNT, WRITER);
        check(disk.writes == 0, "Missing state silently initialized");
        WriterState state = fresh(disk);
        byte[] idle = cloneBytes(disk.record);
        state.close();
        openFails(disk, ACCOUNT + 1, WRITER); openFails(disk, ACCOUNT, WRITER + 1);
        openFails(disk, 1, WRITER); openFails(disk, ACCOUNT, 0);
        // Every single-byte corruption fails its checksum.
        for (int i = 0; i < idle.length; i++) {
            disk.record = cloneBytes(idle); disk.record[i] ^= 1; openFails(disk, ACCOUNT, WRITER);
        }
        int[] semanticOffsets = {0, 4, 5, 9, 17, 25};
        for (int i = 0; i < semanticOffsets.length; i++) {
            disk.record = cloneBytes(idle); disk.record[semanticOffsets[i]] = (byte)0xff;
            fixChecksum(disk.record); openFails(disk, ACCOUNT, WRITER);
        }
        disk.record = new byte[WriterState.MAX_RECORD + 1]; openFails(disk, ACCOUNT, WRITER);
        disk.record = new byte[29]; openFails(disk, ACCOUNT, WRITER);
        disk.record = cloneBytes(idle);
        try { fresh(disk); throw new Exception("Existing state reprovisioned"); } catch (IOException expected) { checks++; }
        state = reopen(disk); state.reserve(RECIPIENT, "A"); state.close();
        byte[] pending = cloneBytes(disk.record);
        // Valid checksum cannot hide bad embedded operation, sequence or UTF-8.
        int[] pendingOffsets = {28, 36, 41};
        for (int i = 0; i < pendingOffsets.length; i++) {
            disk.record = cloneBytes(pending); disk.record[pendingOffsets[i]] = (byte)0xff;
            fixChecksum(disk.record); openFails(disk, ACCOUNT, WRITER);
        }
        // Sequence exhaustion survives restart and never wraps or writes.
        disk.record = cloneBytes(idle);
        disk.record[17] = 0x7f;
        for (int i = 18; i < 25; i++) disk.record[i] = (byte)0xff;
        fixChecksum(disk.record);
        state = reopen(disk); int writes = disk.writes;
        reserveFails(state, RECIPIENT, "overflow");
        check(state.lastSequence() == Long.MAX_VALUE && disk.writes == writes, "Overflow changed storage");
        state.close();
        disk.record = null; openFails(disk, ACCOUNT, WRITER);
        check(disk.writes == writes, "Lost state reused old namespace implicitly");
        // After total loss, only external trusted setup can ensure a fresh ID.
        state = WriterState.provision(new Memory(disk), ACCOUNT, WRITER + 1);
        check(state.writerID() == WRITER + 1, "Fresh replacement identity not used"); state.close();
    }

    private static void failedWrites() throws Exception {
        for (int mode = 1; mode <= 3; mode++) {
            Disk disk = new Disk(); WriterState state = fresh(disk);
            disk.mode = mode; reserveFails(state, RECIPIENT, "uncertain");
            int writes = disk.writes; blocked(state);
            check(disk.writes == writes, "Poisoned state attempted a second write");
            state.close(); disk.mode = 0;
            if (mode == 3) { openFails(disk, ACCOUNT, WRITER); continue; }
            state = reopen(disk);
            check(state.lastSequence() == (mode == 1 ? 0 : 1), "Uncertain reserve recovery wrong");
            if (mode == 1) state.reserve(RECIPIENT, "uncertain");
            check(state.pending().text.equals("uncertain"), "Pending lost during recovery");
            disk.mode = mode;
            try { state.resolve(ack(1)); throw new Exception("Failed clear reported success"); } catch (IOException expected) { checks++; }
            blocked(state); state.close(); disk.mode = 0;
            state = reopen(disk);
            check(state.lastSequence() == 1, "Clear reset sequence");
            check((state.pending() != null) == (mode == 1), "Uncertain clear recovery wrong");
            if (state.pending() != null) state.resolve(ack(1));
            check(state.reserve(RECIPIENT, "next").clientSeq == 2, "Clear reused sequence"); state.close();
        }
        for (int mode = 1; mode <= 3; mode++) {
            Disk disk = new Disk(); disk.mode = mode;
            try { fresh(disk); throw new Exception("Failed provisioning succeeded"); } catch (IOException expected) { checks++; }
            disk.mode = 0;
            if (mode == 2) { WriterState state = reopen(disk); check(state.lastSequence() == 0, "Provision recovery"); state.close(); }
            else openFails(disk, ACCOUNT, WRITER);
        }
        Disk disk = new Disk(); WriterState state = fresh(disk); state.close();
        disk.mode = 4; openFails(disk, ACCOUNT, WRITER); disk.mode = 0;
        state = reopen(disk); disk.mode = 5;
        try { state.close(); throw new Exception("Close failure hidden"); } catch (IOException expected) { checks++; }
        blocked(state); disk.mode = 0; state.close();
    }

    public static void main(String[] args) throws Exception {
        restartAndResponses(); textLimits(); corruptAndMissing(); failedWrites();
        System.out.println("WRITER-STATE-PASS: " + checks + " checks (restart, Unicode, responses, corruption and uncertain writes)");
    }
}
