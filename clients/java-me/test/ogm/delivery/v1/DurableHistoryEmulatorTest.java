package ogm.delivery.v1;

import java.io.IOException;
import javax.microedition.midlet.MIDlet;

/** Local-only fixture; each phase uses a separate JVM with the same file RMS. */
public final class DurableHistoryEmulatorTest extends MIDlet implements Runnable {
    private static final String TEXT = "ASCII \u041f\u0440\u0438\u0432\u0456\u0442 Za\u017c\u00f3\u0142\u0107 e\u0301 \ud83d\ude42\u0000\n";
    protected void startApp() { new Thread(this).start(); }
    protected void pauseApp() { }
    protected void destroyApp(boolean unconditional) { }
    private static void check(boolean value, String message) throws IOException { if (!value) throw new IOException(message); }

    private static String text(long id, boolean maximum) {
        if (!maximum) return TEXT + " #" + id;
        StringBuffer buffer = new StringBuffer(); for (int i = 0; i < 500; i++) buffer.append("\ud83d\ude42"); return buffer.toString();
    }
    private static void item(Codec.Frame frame, long id, boolean maximum) throws IOException {
        int sender = id % 3 != 0 && id % 2 == 0 ? 100002 : 100001;
        int recipient = id % 3 == 0 || sender == 100002 ? 100001 : 100002;
        check(frame.kind == Codec.ITEM && frame.cursor == id && frame.messageID == id && frame.clientSeq == id,
            "Exact retained cursor/ID/sequence mismatch");
        check(frame.sender == sender && frame.recipient == recipient && frame.writerID == (sender - 100000) * 11 && frame.text.equals(text(id, maximum)),
            "Retained metadata/Unicode mismatch");
    }
    private static void cache(HistoryStore store, long cp, boolean maximum) throws IOException {
        check(store.checkpoint() == cp && store.size() == (cp < 8 ? (int)cp : 8), "Wrong durable cursor/cache count");
        long first = cp < 8 ? 1 : cp - 7;
        for (int i = 0; i < store.size(); i++) item(store.item(i), first + i, maximum);
    }
    private static final class Verify implements HistoryReader.Consumer {
        final HistoryStore store; final HistoryReader.Consumer durable;
        final long start, h; final boolean maximum; final long oldCheckpoint;
        long next; int callbacks;
        Verify(HistoryStore store, long start, long h, boolean maximum) throws IOException {
            this.store = store; this.start = start; this.h = h; this.maximum = maximum;
            next = start + 1; oldCheckpoint = store.checkpoint(); durable = store.consumer(start);
        }
        public void page(HistoryReader.Page page) throws IOException {
            callbacks++;
            check(page.accountID == 100001 && page.after == next - 1 && page.through == h, "Wrong page progression/snapshot");
            for (int i = 0; i < page.size(); i++) {
                HistoryReader.Item value = page.item(i);
                Codec.Frame frame = new Codec.Frame(); frame.kind = Codec.ITEM;
                frame.cursor = value.cursor; frame.messageID = value.messageID; frame.sender = value.sender; frame.recipient = value.recipient;
                frame.writerID = value.writerID; frame.clientSeq = value.clientSeq; frame.text = value.text;
                item(frame, next++, maximum);
            }
            durable.page(page);
            check(store.checkpoint() == (oldCheckpoint > page.next ? oldCheckpoint : page.next), "Checkpoint not committed after callback");
        }
        void finished() throws IOException { check(next == h + 1 && callbacks > 0, "Missing messages or completed pages"); }
    }
    private static final class Fault implements StateStorage {
        final StateStorage base;
        final int mode;
        int writes;
        Fault(StateStorage base, int mode) { this.base = base; this.mode = mode; }
        public byte[] read() throws IOException { return base.read(); }
        public void write(byte[] bytes) throws IOException {
            writes++;
            // Provision is write 1, staged ITEMs write 2, checkpoint write 3.
            int at = mode == 1 ? 2 : 3;
            if (writes == at && mode != 3) throw new IOException("Injected failure before RMS write");
            base.write(bytes);
            if (writes == at && mode == 3) throw new IOException("Injected uncertain checkpoint reply after RMS success");
        }
        public void close() throws IOException { base.close(); }
    }

    public void run() {
        String phase = getAppProperty("OGM-Durable-Phase"), address = getAppProperty("OGM-Server");
        int limit = Integer.parseInt(getAppProperty("OGM-Test-Limit"));
        int fault = Integer.parseInt(getAppProperty("OGM-Test-Fault"));
        long namespace = Long.parseLong(getAppProperty("OGM-Test-Namespace"));
        boolean maximum = "maximum".equals(phase);
        String name = fault > 0 ? "ogm-fault-" + fault : ("empty".equals(phase) || "one".equals(phase) || maximum ? "ogm-" + phase : "ogm-durable-history");
        boolean create = "initial".equals(phase) || "empty".equals(phase) || "one".equals(phase) || maximum || "fault".equals(phase);
        StateStorage file = null; HistoryStore store = null; WriterState writer = null; RmsStateStorage outbox = null; HistoryReader reader = null;
        boolean success = false;
        try {
            if (!"empty".equals(phase) && !"one".equals(phase) && !maximum) {
                outbox = "initial".equals(phase) ? RmsStateStorage.create("ogm-independent-outbox") : RmsStateStorage.open("ogm-independent-outbox");
                writer = "initial".equals(phase) ? WriterState.provision(outbox, 100001, 7) : WriterState.open(outbox, 100001, 7);
                if ("initial".equals(phase)) writer.reserve(100002, "independent pending SEND");
            }
            file = create ? RmsHistoryStorage.create(name) : RmsHistoryStorage.open(name);
            if ("fault".equals(phase)) file = new Fault(file, fault);
            store = create ? HistoryStore.provision(file, 100001, namespace) : HistoryStore.open(file, 100001, namespace);
            long from = store.checkpoint();
            long target = 30;
            if ("empty".equals(phase)) target = 0;
            if ("one".equals(phase)) target = 1;
            if ("initial".equals(phase)) target = 3;
            if ("resume".equals(phase) || "earlier".equals(phase)) target = 10;
            if (maximum) target = 8;
            if ("resume".equals(phase)) check(from == 3, "Restart did not load cursor 3");
            if ("recover".equals(phase)) check(from == 10 + 2 * limit, "Interrupted process lost committed pages");
            if ("fault-recover".equals(phase)) {
                check(from == (fault == 3 ? limit : 0), "Uncertain outcome guessed rather than reopened");
                check(store.size() == (fault == 1 ? 0 : limit), "Staged ITEM snapshot not recovered");
            }
            if ("verified".equals(phase)) cache(store, 30, false);
            else if ("rewind".equals(phase)) {
                cache(store, 30, false); reader = new HistoryReader(100001, limit); reader.connect(address);
                try { reader.replay(from, store.consumer(from)); throw new IOException("REWIND-ACCEPTED"); }
                catch (HistoryReader.SyncException expected) { check(expected.code == Codec.CURSOR_INVALID && expected.after == 30, "Wrong server rewind error"); }
                cache(store, 30, false);
            } else {
                if ("earlier".equals(phase)) { check(from == 10, "Earlier replay checkpoint missing"); from = 0; }
                reader = new HistoryReader(100001, limit); reader.connect(address);
                Verify verify = new Verify(store, from, target, maximum);
                if ("fault".equals(phase) || "interrupt".equals(phase)) {
                    try { reader.replay(from, verify); throw new IOException("MISSING-FAULT"); }
                    catch (IOException expected) { check(!"MISSING-FAULT".equals(expected.getMessage()), "Failure treated as success"); }
                    if ("fault".equals(phase)) {
                        check(reader.position() == 0 && !reader.complete(), "Uncommitted callback advanced reader");
                        try { store.checkpoint(); throw new IOException("POISONED-STORE-USED"); }
                        catch (IOException expected) { check(!"POISONED-STORE-USED".equals(expected.getMessage()), "Store not poisoned"); }
                    } else {
                        check(verify.callbacks == 2 && reader.position() == 10 + 2 * limit, "Interrupted page reached consumer");
                        cache(store, 10 + 2 * limit, false);
                    }
                } else {
                    reader.replay(from, verify); verify.finished(); cache(store, target, maximum);
                    if (maximum) check(file.read().length == HistoryStore.MAX_RECORD, "Maximum RMS record not verified");
                }
            }
            if (writer != null) check(writer.lastSequence() == 1 && writer.pending().clientSeq == 1 && writer.pending().recipient == 100002 &&
                "independent pending SEND".equals(writer.pending().text), "History changed independent outbox");
            if ("initial".equals(phase)) {
                store.close(); store = null; file = null;
                mismatched(name, 100002, namespace); mismatched(name, 100001, namespace + 1);
                mismatched("ogm-independent-outbox", 100001, namespace); // Wrong format cannot overwrite SEND state.
            }
            success = true;
        } catch (Exception failure) { System.out.println("DURABLE-HISTORY-FAIL: " + phase + " " + failure); }
        finally {
            try { if (reader != null) reader.close(); } catch (IOException e) { success = false; System.out.println("DURABLE-HISTORY-FAIL: socket " + e); }
            try { if (store != null) store.close(); else if (file != null) file.close(); } catch (IOException e) { success = false; System.out.println("DURABLE-HISTORY-FAIL: history " + e); }
            try { if (writer != null) writer.close(); else if (outbox != null) outbox.close(); } catch (IOException e) { success = false; System.out.println("DURABLE-HISTORY-FAIL: outbox " + e); }
            if (success) System.out.println("DURABLE-HISTORY-PASS: " + phase);
            notifyDestroyed();
        }
    }
    private static void mismatched(String name, int account, long namespace) throws IOException {
        RmsHistoryStorage file = RmsHistoryStorage.open(name);
        try {
            try { HistoryStore.open(file, account, namespace); throw new IOException("BINDING-ACCEPTED"); }
            catch (IOException expected) { check(!"BINDING-ACCEPTED".equals(expected.getMessage()), "Wrong binding accepted"); }
        } finally { file.close(); }
    }
}
