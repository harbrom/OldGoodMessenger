package ogm.delivery.v1;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.io.IOException;

/** Eight-message durable working set; server history remains authoritative. */
public final class HistoryStore {
    public static final int CAPACITY = 8, MIN_RECORD = 30, MAX_RECORD = 16374;
    private static final int MAGIC = 0x4f474d48; // OGMH
    private final StateStorage storage;
    private final int account;
    private final long namespace;
    private long checkpoint;
    private Codec.Frame[] cache = new Codec.Frame[CAPACITY];
    private int count;
    private boolean ready = true, closed;
    private Replay active;

    private HistoryStore(StateStorage storage, int account, long namespace) {
        this.storage = storage; this.account = account; this.namespace = namespace;
    }
    private static void binding(StateStorage storage, int account, long namespace) throws IOException {
        if (storage == null || account < 100000 || account > 999999999 || namespace <= 0)
            throw new IOException("Trusted account and positive local server-history namespace required");
    }
    /** Explicit NEW store only. Caller closes storage on factory failure. */
    public static HistoryStore provision(StateStorage storage, int account, long namespace) throws IOException {
        binding(storage, account, namespace);
        if (storage.read() != null) throw new IOException("History already exists; no automatic reset");
        HistoryStore state = new HistoryStore(storage, account, namespace);
        state.ready = false;
        storage.write(state.encode(0, state.cache, 0));
        state.ready = true;
        return state;
    }
    /** Does not create, repair or rebind; caller closes storage on failure. */
    public static HistoryStore open(StateStorage storage, int account, long namespace) throws IOException {
        binding(storage, account, namespace);
        byte[] bytes = storage.read();
        if (bytes == null) throw new IOException("History state missing; explicit trusted provisioning required");
        HistoryStore state = new HistoryStore(storage, account, namespace);
        state.load(bytes);
        return state;
    }
    private void usable() throws IOException {
        if (!ready || closed) throw new IOException("History unavailable; close/reopen and verify before continuing");
    }
    public synchronized long checkpoint() throws IOException { usable(); return checkpoint; }
    public synchronized int size() throws IOException { usable(); return count; }
    public synchronized Codec.Frame item(int index) throws IOException {
        usable();
        if (index < 0 || index >= count) throw new IOException("History cache index out of range");
        Codec.Frame old = cache[index], frame = new Codec.Frame();
        frame.kind = Codec.ITEM; frame.cursor = old.cursor; frame.messageID = old.messageID;
        frame.sender = old.sender; frame.recipient = old.recipient; frame.writerID = old.writerID;
        frame.clientSeq = old.clientSeq; frame.text = old.text;
        return frame; // Detached value; text itself is immutable.
    }

    /** One serial replay binding. Creating another invalidates the old consumer.
     * Earlier retrieval is allowed, but starting beyond the durable cursor is not. */
    public synchronized HistoryReader.Consumer consumer(long after) throws IOException {
        usable();
        if (after < 0 || after > checkpoint) throw new IOException("Replay must start at or before the durable checkpoint");
        active = new Replay(after);
        return active;
    }
    private final class Replay implements HistoryReader.Consumer {
        long after, through, lastID;
        boolean started, done;
        Replay(long after) { this.after = after; }
        public void page(HistoryReader.Page page) throws IOException {
            synchronized (HistoryStore.this) {
                usable();
                if (active != this || done) throw new IOException("Stale/completed history consumer");
                try {
                    commit(page, this);
                    after = page.next; through = page.through; started = true;
                    if (page.size() > 0) lastID = page.item(page.size() - 1).messageID;
                    done = page.next == page.through;
                } catch (IOException failure) { active = null; throw failure; }
            }
        }
    }

    private void commit(HistoryReader.Page page, Replay replay) throws IOException {
        if (page == null || page.accountID != account || page.after != replay.after || page.size() > CAPACITY ||
            page.next < page.after || page.next > page.through || page.through < checkpoint ||
            (count > 0 && page.through < cache[count - 1].cursor) ||
            (replay.started && page.through != replay.through)) throw invalid();
        Codec.Frame[] next = new Codec.Frame[CAPACITY];
        System.arraycopy(cache, 0, next, 0, count);
        int size = count;
        long cursor = page.after, id = replay.lastID;
        for (int i = 0; i < page.size(); i++) {
            HistoryReader.Item item = page.item(i);
            Codec.Frame frame = new Codec.Frame();
            frame.kind = Codec.ITEM; frame.cursor = item.cursor; frame.messageID = item.messageID;
            frame.sender = item.sender; frame.recipient = item.recipient;
            frame.writerID = item.writerID; frame.clientSeq = item.clientSeq; frame.text = item.text;
            if (frame.cursor <= cursor || frame.cursor > page.through || frame.messageID <= id ||
                (frame.sender != account && frame.recipient != account)) throw invalid();
            Codec.encode(frame); // Reuse the strict bounded field/UTF-8 rules.
            cursor = frame.cursor; id = frame.messageID;
            // Check against the original retained set even if this page evicts it.
            for (int j = 0; j < count; j++) {
                Codec.Frame old = cache[j];
                if (old.messageID == frame.messageID) {
                    if (!same(old, frame)) throw new IOException("Conflicting immutable cached message ID");
                } else if ((old.messageID < frame.messageID && old.cursor >= frame.cursor) ||
                    (old.messageID > frame.messageID && old.cursor <= frame.cursor)) throw invalid();
            }
            int at = 0;
            while (at < size && next[at].messageID < frame.messageID) at++;
            if (at < size && next[at].messageID == frame.messageID) continue;
            if (size == CAPACITY) {
                if (at == 0) continue; // Older than the newest eight: do not displace newer history.
                System.arraycopy(next, 1, next, 0, --size); at--;
            }
            System.arraycopy(next, at, next, at + 1, size - at);
            next[at] = frame; size++;
        }
        if (page.through < cursor || (page.next < page.through && (page.size() == 0 || page.next != cursor))) throw invalid();
        long committed = page.next > checkpoint ? page.next : checkpoint;
        boolean changed = size != count;
        for (int i = 0; !changed && i < size; i++) changed = !same(cache[i], next[i]);
        if (!changed && committed == checkpoint) return; // Already persisted identical replay.
        byte[] snapshot = encode(checkpoint, next, size);
        ready = false;
        storage.write(snapshot); // Complete ITEM snapshot succeeds BEFORE publishing END.next.
        snapshot = encode(committed, next, size);
        storage.write(snapshot); // One individually atomic record write, not a multi-record transaction.
        cache = next; count = size; checkpoint = committed; ready = true;
    }

    private static boolean same(Codec.Frame a, Codec.Frame b) {
        return a.cursor == b.cursor && a.messageID == b.messageID && a.sender == b.sender && a.recipient == b.recipient &&
            a.writerID == b.writerID && a.clientSeq == b.clientSeq && a.text.equals(b.text);
    }
    private byte[] encode(long cursor, Codec.Frame[] frames, int size) throws IOException {
        ByteArrayOutputStream bytes = new ByteArrayOutputStream(MIN_RECORD + size * 64);
        DataOutputStream out = new DataOutputStream(bytes);
        out.writeInt(MAGIC); out.writeByte(1); out.writeInt(account); out.writeLong(namespace); out.writeLong(cursor); out.writeByte(size);
        for (int i = 0; i < size; i++) Codec.write(out, frames[i]);
        byte[] body = bytes.toByteArray(); out.writeInt(checksum(body, body.length));
        return bytes.toByteArray();
    }
    private void load(byte[] bytes) throws IOException {
        if (bytes.length < MIN_RECORD || bytes.length > MAX_RECORD) throw invalid();
        int length = bytes.length - 4;
        DataInputStream sum = new DataInputStream(new ByteArrayInputStream(bytes, length, 4));
        if (sum.readInt() != checksum(bytes, length)) throw invalid();
        DataInputStream in = new DataInputStream(new ByteArrayInputStream(bytes, 0, length));
        if (in.readInt() != MAGIC || in.readUnsignedByte() != 1 || in.readInt() != account || in.readLong() != namespace) throw invalid();
        checkpoint = in.readLong(); count = in.readUnsignedByte();
        if (checkpoint < 0 || count > CAPACITY) throw invalid();
        long cursor = 0, id = 0;
        for (int i = 0; i < count; i++) {
            Codec.Frame frame = Codec.read(in);
            if (frame.kind != Codec.ITEM || frame.cursor <= cursor || frame.messageID <= id ||
                (frame.sender != account && frame.recipient != account)) throw invalid();
            cursor = frame.cursor; id = frame.messageID; cache[i] = frame;
        }
        if (in.available() != 0) throw invalid();
        // Cache cursors beyond checkpoint are valid after staged ITEM success / checkpoint failure.
    }
    private static IOException invalid() { return new IOException("Invalid, incompatible or mismatched history state/page; no reset"); }
    private static int checksum(byte[] bytes, int size) {
        int crc = -1;
        for (int i = 0; i < size; i++) {
            crc ^= bytes[i] & 255;
            for (int j = 0; j < 8; j++) crc = (crc >>> 1) ^ ((crc & 1) == 0 ? 0 : 0xedb88320);
        }
        return ~crc;
    }
    public synchronized void close() throws IOException {
        ready = false; active = null;
        if (!closed) { storage.close(); closed = true; }
    }
}
