package ogm.delivery.v1;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import javax.microedition.io.SocketConnection;

/** CLDC-only restart/failure tests through actual validated HistoryReader pages. */
public final class HistoryStoreTest {
    private static int checks;
    private static void check(boolean value, String message) throws Exception { checks++; if (!value) throw new Exception(message); }
    private static final class Disk {
        byte[] bytes, staged;
        int writes, failAt, mode;
    }
    private static byte[] copy(byte[] b) {
        if (b == null) return null;
        byte[] result = new byte[b.length]; System.arraycopy(b, 0, result, 0, b.length); return result;
    }
    private static final class Memory implements StateStorage {
        final Disk disk;
        Memory(Disk disk) { this.disk = disk; }
        public byte[] read() throws IOException { if (disk.mode == 4) throw new IOException("Injected reopen failure"); return copy(disk.bytes); }
        public void write(byte[] bytes) throws IOException {
            disk.writes++;
            if (disk.writes == disk.failAt && disk.mode == 1) throw new IOException("Before write");
            disk.staged = copy(disk.bytes); disk.bytes = copy(bytes);
            if (disk.writes == disk.failAt) {
                if (disk.mode == 3) disk.bytes[0] ^= 1;
                throw new IOException("Uncertain write");
            }
        }
        public void close() { }
    }
    private static HistoryStore fresh(Disk disk) throws IOException { return HistoryStore.provision(new Memory(disk), 100001, 77); }
    private static HistoryStore reopen(Disk disk) throws IOException { return HistoryStore.open(new Memory(disk), 100001, 77); }
    private static Codec.Frame item(int id) {
        Codec.Frame f = new Codec.Frame(); f.kind = Codec.ITEM; f.cursor = id; f.messageID = id;
        f.sender = id % 2 == 0 ? 100002 : 100001; f.recipient = 100001; f.writerID = 7; f.clientSeq = id;
        f.text = "\u041f\u0440\u0438\u0432\u0456\u0442 Za\u017c\u00f3\u0142\u0107 e\u0301 \ud83d\ude42\u0000\n #" + id;
        return f;
    }
    private static Codec.Frame end(long next, long through) { Codec.Frame f = new Codec.Frame(); f.kind = Codec.END; f.next = next; f.through = through; return f; }
    private static byte[] wire(Codec.Frame[] frames) throws IOException {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        for (int i = 0; i < frames.length; i++) Codec.write(out, frames[i]); return out.toByteArray();
    }
    private static final class Socket implements SocketConnection {
        final InputStream input; final OutputStream output = new ByteArrayOutputStream();
        boolean closed;
        Socket(byte[] bytes) { input = new ByteArrayInputStream(bytes); }
        public InputStream openInputStream() { return input; }
        public OutputStream openOutputStream() { return output; }
        public DataInputStream openDataInputStream() { return new DataInputStream(input); }
        public DataOutputStream openDataOutputStream() { return new DataOutputStream(output); }
        public void close() { closed = true; }
        public void setSocketOption(byte key, int value) { }
        public int getSocketOption(byte key) { return 0; }
        public String getLocalAddress() { return "127.0.0.1"; }
        public int getLocalPort() { return 1; }
        public String getAddress() { return "127.0.0.1"; }
        public int getPort() { return 2; }
    }
    private static HistoryReader reader(int account, int limit, final Socket socket) throws IOException {
        HistoryReader r = new HistoryReader(account, limit, new HistoryReader.Opener() {
            public SocketConnection open(String address) { return socket; }
        }); r.connect("127.0.0.1:1"); return r;
    }
    private static byte[] history(int first, int last, int limit) throws IOException {
        ByteArrayOutputStream bytes = new ByteArrayOutputStream();
        int i = first;
        if (first > last) Codec.write(bytes, end(last, last));
        while (i <= last) {
            int size = 0;
            while (i <= last && size++ < limit) Codec.write(bytes, item(i++));
            Codec.write(bytes, end(i - 1, last));
        }
        return bytes.toByteArray();
    }
    private static void play(HistoryStore store, int first, int last, int limit) throws Exception {
        Socket socket = new Socket(history(first, last, limit)); HistoryReader r = reader(100001, limit, socket);
        try { r.replay(first - 1, store.consumer(first - 1)); check(r.position() == last && store.checkpoint() >= last, "Successful checkpoint mismatch"); }
        finally { r.close(); }
        check(socket.closed, "Reader not closed");
    }
    private static void blocked(HistoryStore store) throws Exception {
        try { store.checkpoint(); throw new Exception("Poisoned checkpoint exposed"); } catch (IOException expected) { checks++; }
        try { store.size(); throw new Exception("Poisoned cache exposed"); } catch (IOException expected) { checks++; }
        try { store.consumer(0); throw new Exception("Poisoned store reused"); } catch (IOException expected) { checks++; }
    }
    private static void openFails(Disk disk, int account, long namespace) throws Exception {
        try { HistoryStore.open(new Memory(disk), account, namespace); throw new Exception("Bad binding/state opened"); } catch (IOException expected) { checks++; }
    }
    private static byte[] hex(String text) {
        byte[] out = new byte[text.length() / 2];
        for (int i = 0; i < out.length; i++) out[i] = (byte)Integer.parseInt(text.substring(2*i, 2*i+2), 16); return out;
    }
    private static void same(byte[] a, byte[] b) throws Exception {
        check(a.length == b.length, "Different record lengths");
        for (int i = 0; i < a.length; i++) check(a[i] == b[i], "Record bytes changed");
    }
    private static void basic() throws Exception {
        Disk disk = new Disk(); HistoryStore store = fresh(disk);
        same(disk.bytes, hex("4f474d4801000186a1000000000000004d0000000000000000008899673a"));
        check(store.checkpoint() == 0 && store.size() == 0, "Bad initial state");
        play(store, 1, 0, 1); check(disk.writes == 1, "Empty identical history wrote state");
        play(store, 1, 1, 1); check(disk.writes == 3, "Missing ITEM-before-checkpoint writes");
        Disk staged = new Disk(); staged.bytes = copy(disk.staged); HistoryStore previous = reopen(staged);
        check(previous.checkpoint() == 0 && previous.size() == 1, "Checkpoint preceded item persistence"); previous.close();
        Codec.Frame detached = store.item(0); detached.text = "mutated";
        check(store.item(0).text.equals(item(1).text), "Cache alias escaped");
        store.close(); store = reopen(disk); check(store.checkpoint() == 1, "Checkpoint did not reopen");
        play(store, 2, 12, 1);
        check(store.size() == 8 && store.item(0).messageID == 5 && store.item(7).messageID == 12, "Oldest-ID eviction failed");
        int writes = disk.writes; byte[] exact = copy(disk.bytes);
        play(store, 1, 12, 8); check(store.checkpoint() == 12 && disk.writes == writes, "Old identical replay regressed/wrote checkpoint"); same(exact, disk.bytes);
        store.close(); store = reopen(disk); check(store.size() == 8 && store.checkpoint() == 12, "Eviction lost checkpoint");
        for (int i = 0; i < 8; i++) same(Codec.encode(store.item(i)), Codec.encode(item(i + 5)));
        store.close(); blocked(store);
    }
    private static void conflictAndBinding() throws Exception {
        Disk disk = new Disk(); HistoryStore store = fresh(disk); play(store, 1, 2, 1);
        int writes = disk.writes;
        Codec.Frame changed = item(1); changed.text = "conflict";
        Socket socket = new Socket(wire(new Codec.Frame[]{changed, end(1, 2)})); HistoryReader r = reader(100001, 1, socket);
        try { r.replay(store.consumer(0)); throw new Exception("Conflicting ID accepted"); } catch (IOException expected) { checks++; }
        finally { r.close(); }
        check(store.checkpoint() == 2 && disk.writes == writes, "Conflict changed durable state");
        Codec.Frame both = item(1); both.recipient = 100002;
        socket = new Socket(wire(new Codec.Frame[]{both, end(1, 2)})); r = reader(100002, 1, socket);
        try { r.replay(store.consumer(0)); throw new Exception("Other account's page accepted"); } catch (IOException expected) { checks++; }
        finally { r.close(); }
        check(disk.writes == writes, "Account-local cursor transferred");
        Disk isolated = new Disk(); HistoryStore alice = fresh(isolated);
        socket = new Socket(wire(new Codec.Frame[]{both, end(1, 1)})); r = reader(100002, 1, socket);
        try { r.replay(alice.consumer(0)); throw new Exception("Common-message page bypassed account binding"); } catch (IOException expected) { checks++; }
        finally { r.close(); }
        check(alice.checkpoint() == 0 && alice.size() == 0 && isolated.writes == 1, "Other account initialized shared-message cache"); alice.close();
        // A callback tied to a different replay must never write stale pages.
        HistoryReader.Consumer old = store.consumer(0); store.consumer(0);
        socket = new Socket(history(1, 2, 1)); r = reader(100001, 1, socket);
        try { r.replay(old); throw new Exception("Stale consumer accepted"); } catch (IOException expected) { checks++; }
        finally { r.close(); }
        try { store.consumer(3); throw new Exception("Future replay position accepted"); } catch (IOException expected) { checks++; }
        socket = new Socket(wire(new Codec.Frame[]{end(0, 0)})); r = reader(100001, 1, socket);
        try { r.replay(store.consumer(0)); throw new Exception("Server history rewind accepted"); } catch (IOException expected) { checks++; }
        finally { r.close(); }
        store.close(); openFails(disk, 100002, 77); openFails(disk, 100001, 78);
    }
    private static void failures() throws Exception {
        for (int mode = 1; mode <= 2; mode++) {
            Disk failed = new Disk(); failed.failAt = 1; failed.mode = mode;
            try { fresh(failed); throw new Exception("Failed provisioning reported success"); } catch (IOException expected) { checks++; }
            failed.mode = 0;
            if (mode == 1) openFails(failed, 100001, 77);
            else { HistoryStore recovered = reopen(failed); check(recovered.checkpoint() == 0 && recovered.size() == 0, "Uncertain provisioning recovery wrong"); recovered.close(); }
        }
        for (int phase = 0; phase < 2; phase++) for (int mode = 1; mode <= 3; mode++) {
            Disk disk = new Disk(); HistoryStore store = fresh(disk);
            disk.failAt = disk.writes + 1 + phase; disk.mode = mode;
            Socket socket = new Socket(history(1, 3, 8)); HistoryReader r = reader(100001, 8, socket);
            try { r.replay(store.consumer(0)); throw new Exception("Failed write reported page success"); } catch (IOException expected) { checks++; }
            check(r.position() == 0 && !r.complete(), "Reader advanced before durable checkpoint"); r.close();
            int writes = disk.writes; blocked(store); check(disk.writes == writes, "Poisoned store wrote again");
            store.close(); disk.mode = 0;
            if (mode == 3) { openFails(disk, 100001, 77); continue; }
            store = reopen(disk);
            long cp = phase == 1 && mode == 2 ? 3 : 0;
            check(store.checkpoint() == cp, "Uncertain outcome guessed instead of reopened");
            check(store.size() == (phase == 0 && mode == 1 ? 0 : 3), "Staged cache recovery wrong");
            play(store, (int)cp + 1, 3, 1);
            check(store.size() == 3 && store.checkpoint() == 3, "Replay duplicated/lost staged ITEMs");
            store.close();
        }
        Disk disk = new Disk(); HistoryStore store = fresh(disk);
        byte[] bytes = history(1, 3, 1); byte[] truncated = new byte[bytes.length - 5]; System.arraycopy(bytes, 0, truncated, 0, truncated.length);
        Socket socket = new Socket(truncated); HistoryReader r = reader(100001, 1, socket);
        try { r.replay(store.consumer(0)); throw new Exception("Interrupted END succeeded"); } catch (IOException expected) { checks++; }
        check(store.checkpoint() == 2 && store.size() == 2 && r.position() == 2, "Interrupted page committed"); r.close(); store.close();
        store = reopen(disk); play(store, 3, 3, 1); store.close();
    }
    private static void crc(byte[] bytes) {
        long crc = 0xffffffffL;
        for (int i = 0; i < bytes.length - 4; i++) {
            crc ^= bytes[i] & 255;
            for (int j = 0; j < 8; j++) crc = (crc & 1) == 0 ? crc >>> 1 : (crc >>> 1) ^ 0xedb88320L;
        }
        crc ^= 0xffffffffL;
        for (int i = 0; i < 4; i++) bytes[bytes.length-4+i] = (byte)(crc >>> (24-i*8));
    }
    private static void corruptionAndLimits() throws Exception {
        Disk disk = new Disk(); openFails(disk, 100001, 77); check(disk.writes == 0, "Missing state initialized");
        HistoryStore store = fresh(disk); byte[] valid = copy(disk.bytes); store.close();
        for (int i = 0; i < valid.length; i++) { disk.bytes = copy(valid); disk.bytes[i] ^= 1; openFails(disk, 100001, 77); }
        int[] offsets = {0, 4, 5, 9, 17, 25};
        for (int i = 0; i < offsets.length; i++) { disk.bytes = copy(valid); disk.bytes[offsets[i]] = (byte)0xff; crc(disk.bytes); openFails(disk, 100001, 77); }
        disk.bytes = new byte[HistoryStore.MAX_RECORD + 1]; openFails(disk, 100001, 77);
        disk.bytes = new byte[29]; openFails(disk, 100001, 77);
        disk.bytes = copy(valid); disk.mode = 4; openFails(disk, 100001, 77); disk.mode = 0;
        try { fresh(disk); throw new Exception("Existing history reset"); } catch (IOException expected) { checks++; }
        store = reopen(disk); play(store, 1, 2, 1); store.close();
        byte[] cached = copy(disk.bytes);
        int second = 26 + Codec.encode(item(1)).length;
        disk.bytes = copy(cached); disk.bytes[28] = 0x7f; crc(disk.bytes); openFails(disk, 100001, 77);
        disk.bytes = copy(cached); disk.bytes[69] = (byte)0xff; crc(disk.bytes); openFails(disk, 100001, 77);
        disk.bytes = copy(cached); System.arraycopy(disk.bytes, 37, disk.bytes, second + 11, 8); crc(disk.bytes); openFails(disk, 100001, 77);
        disk.bytes = copy(valid); store = reopen(disk);
        Codec.Frame[] frames = new Codec.Frame[9]; StringBuffer text = new StringBuffer(); for (int i = 0; i < 500; i++) text.append("\ud83d\ude42");
        for (int i = 0; i < 8; i++) { frames[i] = item(i + 1); frames[i].text = text.toString(); } frames[8] = end(8, 8);
        Socket socket = new Socket(wire(frames)); HistoryReader r = reader(100001, 8, socket); r.replay(store.consumer(0)); r.close();
        check(disk.bytes.length == HistoryStore.MAX_RECORD && store.checkpoint() == 8, "Maximum cache record size wrong");
        store.close(); store = reopen(disk); check(store.size() == 8 && store.item(7).text.equals(text.toString()), "Maximum Unicode lost"); store.close();
        // Existing independent writer/outbox snapshot is unchanged by history operations.
        Disk outboxDisk = new Disk(); WriterState writer = WriterState.provision(new Memory(outboxDisk), 100001, 7); writer.reserve(100002, "pending outbox");
        byte[] outbox = copy(outboxDisk.bytes); store = reopen(disk); check(store.checkpoint() == 8, "History reopen failed"); store.close(); same(outbox, outboxDisk.bytes);
        check(writer.pending().text.equals("pending outbox") && writer.lastSequence() == 1, "Independent outbox changed"); writer.close();
    }
    public static void main(String[] args) throws Exception {
        basic(); conflictAndBinding(); failures(); corruptionAndLimits();
        System.out.println("HISTORY-STORE-PASS: " + checks + " checks (commit order, restart, replay, eviction, binding, faults and corruption)");
    }
}
