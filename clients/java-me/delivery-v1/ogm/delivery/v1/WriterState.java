package ogm.delivery.v1;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.io.IOException;

/** One provisioned writer and one persistent SEND. No networking or UI work. */
public final class WriterState {
    public static final int MIN_RECORD = 30, MAX_RECORD = 2045;
    private static final int MAGIC = 0x4f474d4f; // OGMO
    private final StateStorage storage;
    private final int account;
    private final long writer;
    private long last;
    private Codec.Frame pending;
    private boolean ready = true, closed;

    private WriterState(StateStorage storage, int account, long writer) {
        this.storage = storage; this.account = account; this.writer = writer;
    }

    private static void identity(StateStorage storage, int account, long writer) throws IOException {
        if (storage == null || account < 100000 || account > 999999999 || writer <= 0)
            throw new IOException("Invalid trusted account/writer provisioning");
    }

    /** Only for a NEW namespace supplied by trusted setup, never loss recovery.
     * Takes ownership on success; caller must close storage on any failure. */
    public static WriterState provision(StateStorage storage, int account, long freshWriter) throws IOException {
        identity(storage, account, freshWriter);
        if (storage.read() != null) throw new IOException("State already exists; never reset a writer");
        WriterState state = new WriterState(storage, account, freshWriter);
        state.persist(0, null);
        return state;
    }

    /** Missing/corrupt/mismatched state fails, without writing or initializing.
     * Takes ownership on success; caller must close storage on any failure. */
    public static WriterState open(StateStorage storage, int account, long writer) throws IOException {
        identity(storage, account, writer);
        byte[] record = storage.read();
        if (record == null) throw new IOException("Writer state missing; fresh trusted provisioning required");
        WriterState state = new WriterState(storage, account, writer);
        state.load(record);
        return state;
    }

    private void usable() throws IOException {
        if (!ready || closed) throw new IOException("Writer unavailable; close and reopen verified state before reuse");
    }

    public synchronized int accountID() throws IOException { usable(); return account; }
    public synchronized long writerID() throws IOException { usable(); return writer; }
    public synchronized long lastSequence() throws IOException { usable(); return last; }

    /** A fresh frame copy prevents caller mutation of the retained retry payload. */
    public synchronized Codec.Frame pending() throws IOException { usable(); return copy(pending); }

    /** Return for transmission ONLY after a successful whole-record write. */
    public synchronized Codec.Frame reserve(int recipient, String text) throws IOException {
        usable();
        if (pending != null) throw new IOException("A SEND is already pending; retry its exact payload");
        if (last == Long.MAX_VALUE) throw new IOException("Client sequence exhausted; fresh trusted writer required");
        Codec.Frame next = new Codec.Frame();
        next.kind = Codec.SEND; next.clientSeq = last + 1; next.recipient = recipient; next.text = text;
        persist(next.clientSeq, next); // Codec validates IDs and strict UTF-8 before storage.
        return copy(pending);
    }

    /** True means pending was durably cleared. RETRYABLE returns false and writes
     * nothing. Transport failure/lost ACK require no call: just retain pending. */
    public synchronized boolean resolve(Codec.Frame reply) throws IOException {
        usable();
        if (pending == null) throw new IOException("No pending SEND to resolve");
        if (reply == null || (reply.kind != Codec.ACK && reply.kind != Codec.ERROR))
            throw new IOException("Unexpected outbox response");
        Codec.encode(reply); // Validate exactly the existing wire fields/ranges.
        if (reply.kind == Codec.ACK) {
            if (reply.clientSeq != pending.clientSeq) throw new IOException("ACK sequence mismatch");
        } else {
            if (reply.requestOp != Codec.SEND || reply.reference != pending.clientSeq)
                throw new IOException("ERROR does not match pending SEND");
            if (reply.code == Codec.RETRYABLE) return false;
            if (reply.code != Codec.REJECTED && reply.code != Codec.CONFLICT)
                throw new IOException("Unexpected SEND error code");
        }
        persist(last, null);
        return true;
    }

    private static Codec.Frame copy(Codec.Frame source) {
        if (source == null) return null;
        Codec.Frame frame = new Codec.Frame();
        frame.kind = Codec.SEND; frame.clientSeq = source.clientSeq;
        frame.recipient = source.recipient; frame.text = source.text;
        return frame;
    }

    private void persist(long sequence, Codec.Frame frame) throws IOException {
        byte[] wire = frame == null ? null : Codec.encode(frame);
        ByteArrayOutputStream bytes = new ByteArrayOutputStream(MIN_RECORD + (wire == null ? 0 : wire.length));
        DataOutputStream out = new DataOutputStream(bytes);
        out.writeInt(MAGIC); out.writeByte(1); out.writeInt(account);
        out.writeLong(writer); out.writeLong(sequence); out.writeByte(frame == null ? 0 : 1);
        if (wire != null) out.write(wire);
        byte[] body = bytes.toByteArray();
        out.writeInt(checksum(body, body.length));
        byte[] record = bytes.toByteArray();
        // A failed write, even one that actually committed, poisons this instance.
        ready = false;
        storage.write(record);
        last = sequence; pending = frame;
        ready = true;
    }

    private void load(byte[] record) throws IOException {
        if (record.length < MIN_RECORD || record.length > MAX_RECORD) throw corrupt();
        int bodyLength = record.length - 4;
        DataInputStream sum = new DataInputStream(new ByteArrayInputStream(record, bodyLength, 4));
        if (sum.readInt() != checksum(record, bodyLength)) throw corrupt();
        DataInputStream in = new DataInputStream(new ByteArrayInputStream(record, 0, bodyLength));
        if (in.readInt() != MAGIC || in.readUnsignedByte() != 1 || in.readInt() != account || in.readLong() != writer)
            throw corrupt();
        long sequence = in.readLong();
        int flag = in.readUnsignedByte();
        if (sequence < 0 || flag > 1) throw corrupt();
        Codec.Frame frame = null;
        if (flag == 1) {
            frame = Codec.read(in);
            if (frame.kind != Codec.SEND || frame.clientSeq != sequence) throw corrupt();
        }
        if (in.available() != 0) throw corrupt();
        last = sequence; pending = frame;
    }

    private static IOException corrupt() { return new IOException("Corrupt, incompatible or mismatched writer state; no reset allowed"); }

    // CRC-32/ISO-HDLC, for accidental corruption only, not authentication/rollback.
    private static int checksum(byte[] bytes, int length) {
        int crc = -1;
        for (int i = 0; i < length; i++) {
            crc ^= bytes[i] & 255;
            for (int bit = 0; bit < 8; bit++) crc = (crc >>> 1) ^ ((crc & 1) == 0 ? 0 : 0xedb88320);
        }
        return ~crc;
    }

    /** Invalidates state immediately. A failed close may be retried. */
    public synchronized void close() throws IOException {
        ready = false;
        if (!closed) { storage.close(); closed = true; }
    }
}
