package ogm.delivery.v1;

import java.io.IOException;
import javax.microedition.midlet.MIDlet;
import javax.microedition.rms.RecordStore;

/** Test-only, no-network MIDlet. Each phase runs in a separate emulator JVM. */
public final class RmsOutboxTest extends MIDlet implements Runnable {
    private static final String NAME = "ogm-outbox-test";
    private static final String TEXT = "RMS \u041f\u0440\u0438\u0432\u0456\u0442 e\u0301 \ud83d\ude42\u0000\n";
    protected void startApp() { new Thread(this).start(); }
    protected void pauseApp() { }
    protected void destroyApp(boolean unconditional) { }

    private static void check(boolean value) throws Exception {
        if (!value) throw new Exception("RMS outbox assertion failed");
    }

    public void run() {
        String phase = getAppProperty("OGM-State-Phase");
        WriterState state = null;
        RmsStateStorage file = null;
        boolean success = false;
        try {
            if ("provision".equals(phase)) {
                // open() must not initialize a missing store.
                try { RmsStateStorage.open(NAME); throw new Exception("Missing store opened"); }
                catch (IOException expected) { }
                file = RmsStateStorage.create(NAME);
                state = WriterState.provision(file, 100001, 7);
                check(state.reserve(100002, TEXT).clientSeq == 1);
            } else {
                try { RmsStateStorage.create(NAME); throw new Exception("Existing store replaced"); }
                catch (IOException expected) { }
                file = RmsStateStorage.open(NAME);
                state = WriterState.open(file, 100001, 7);
                check(state.accountID() == 100001 && state.writerID() == 7 && state.lastSequence() == 1);
                if ("retry".equals(phase)) {
                    Codec.Frame frame = state.pending();
                    check(frame != null && frame.clientSeq == 1 && frame.recipient == 100002 && TEXT.equals(frame.text));
                    Codec.Frame error = new Codec.Frame(); error.kind = Codec.ERROR;
                    error.requestOp = Codec.SEND; error.reference = 1; error.code = Codec.RETRYABLE;
                    check(!state.resolve(error));
                    Codec.Frame ack = new Codec.Frame(); ack.kind = Codec.ACK; ack.clientSeq = 1; ack.messageID = 42;
                    check(state.resolve(ack));
                } else if ("resolved".equals(phase)) {
                    check(state.pending() == null);
                    check(state.reserve(100001, "self").clientSeq == 2);
                } else throw new Exception("Unknown phase");
            }
            // A second adapter cannot concurrently allocate under the same writer.
            try { RmsStateStorage.open(NAME); throw new Exception("Second adapter opened"); }
            catch (IOException expected) { }
            state.close(); state = null; file = null;
            if ("resolved".equals(phase)) adapterGuards();
            success = true;
        } catch (Exception failure) {
            System.out.println("RMS-OUTBOX-FAIL: " + phase + " " + failure.toString());
        } finally {
            try {
                if (state != null) state.close();
                else if (file != null) file.close();
            } catch (IOException failure) {
                success = false;
                System.out.println("RMS-OUTBOX-FAIL: cleanup " + failure.toString());
            }
            if (success) System.out.println("RMS-OUTBOX-PASS: " + phase);
            notifyDestroyed();
        }
    }

    private static void adapterGuards() throws Exception {
        // Test-only raw stores: confirm empty, extra, missing, corrupt and large
        // records fail without automatic reset/deletion. All files stay in .build.
        RmsStateStorage empty = RmsStateStorage.create("ogm-empty"); empty.close();
        try { RmsStateStorage.create("ogm-empty"); throw new Exception("Empty store reset"); }
        catch (IOException expected) { }
        invalid("ogm-empty");
        RecordStore raw = RecordStore.openRecordStore("ogm-corrupt", true);
        raw.addRecord(new byte[30], 0, 30); raw.closeRecordStore();
        invalid("ogm-corrupt");
        raw = RecordStore.openRecordStore("ogm-large", true);
        raw.addRecord(new byte[WriterState.MAX_RECORD + 1], 0, WriterState.MAX_RECORD + 1); raw.closeRecordStore();
        invalid("ogm-large");
        raw = RecordStore.openRecordStore("ogm-extra", true);
        raw.addRecord(new byte[30], 0, 30); raw.addRecord(new byte[30], 0, 30); raw.closeRecordStore();
        invalid("ogm-extra");
        raw = RecordStore.openRecordStore("ogm-deleted", true);
        int id = raw.addRecord(new byte[30], 0, 30);
        raw.deleteRecord(id); raw.closeRecordStore(); // Only test fixture simulates external loss.
        invalid("ogm-deleted");
    }

    private static void invalid(String name) throws Exception {
        RmsStateStorage file = RmsStateStorage.open(name);
        try {
            try { WriterState.open(file, 100001, 7); throw new Exception("Invalid RMS accepted: " + name); }
            catch (IOException expected) { }
        } finally { file.close(); }
    }
}
