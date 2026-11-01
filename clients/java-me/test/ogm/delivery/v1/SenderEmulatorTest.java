package ogm.delivery.v1;

import java.io.IOException;
import javax.microedition.midlet.MIDlet;

/** Test-only fixture. Go injects the matching context; no identity is sent. */
public final class SenderEmulatorTest extends MIDlet implements Runnable {
    public static final String TEXT = "ASCII \u041f\u0440\u0438\u0432\u0456\u0442 Za\u017c\u00f3\u0142\u0107 e\u0301 \ud83d\ude42\u0000\n";
    protected void startApp() { new Thread(this).start(); }
    protected void pauseApp() { }
    protected void destroyApp(boolean unconditional) { }
    private static void check(boolean value, String message) throws Exception {
        if (!value) throw new Exception(message);
    }
    private static void pending(WriterState state) throws Exception {
        Codec.Frame f = state.pending();
        check(f != null && f.kind == Codec.SEND && f.clientSeq == 1 && f.recipient == 100002 && TEXT.equals(f.text), "Pending payload changed");
    }
    private static void ack(Codec.Frame f, long seq, long id) throws Exception {
        check(f.kind == Codec.ACK && f.clientSeq == seq && f.messageID == id, "Wrong ACK/duplicate message ID");
    }
    public void run() {
        String phase = getAppProperty("OGM-Sender-Phase"), address = getAppProperty("OGM-Server");
        long writer = Long.parseLong(getAppProperty("OGM-Test-Writer"));
        String name = "ogm-sender-" + writer;
        RmsStateStorage file = null;
        WriterState state = null;
        Sender sender = null;
        boolean success = false;
        try {
            boolean restart = "retry".equals(phase) || "verified".equals(phase);
            file = restart ? RmsStateStorage.open(name) : RmsStateStorage.create(name);
            state = restart ? WriterState.open(file, 100001, writer) : WriterState.provision(file, 100001, writer);
            if ("verified".equals(phase)) {
                check(state.lastSequence() == 2 && state.pending() == null, "ACK/new SEND clear not persistent");
            } else {
                sender = new Sender(state); sender.connect(address);
                if ("retry".equals(phase)) {
                    pending(state);
                    ack(sender.retry(), 1, 1); check(state.pending() == null, "Matching ACK did not clear pending");
                    ack(sender.send(100001, "after recovery"), 2, 2);
                } else if ("rejected".equals(phase) || "conflict".equals(phase)) {
                    Codec.Frame result = sender.send(100002, TEXT);
                    int code = "rejected".equals(phase) ? Codec.REJECTED : Codec.CONFLICT;
                    check(result.kind == Codec.ERROR && result.code == code && result.reference == 1, "Wrong definitive outcome");
                    check(state.pending() == null && state.lastSequence() == 1, "Definitive error did not clear correctly");
                    ack(sender.send(100002, "after outcome"), 2, code == Codec.CONFLICT ? 2 : 1);
                } else if ("retryable".equals(phase)) {
                    Codec.Frame result = sender.send(100002, TEXT);
                    check(result.kind == Codec.ERROR && result.code == Codec.RETRYABLE, "Wrong retryable outcome");
                    pending(state); ack(sender.retry(), 1, 1);
                } else {
                    try { sender.send(100002, TEXT); throw new Exception("Injected reply failure did not fail"); }
                    catch (IOException expected) { pending(state); }
                    if (!"lost".equals(phase)) {
                        sender.connect(address); ack(sender.retry(), 1, 1);
                    }
                }
                if (!"lost".equals(phase)) check(state.pending() == null, "Resolved pending retained");
            }
            success = true;
        } catch (Exception failure) {
            System.out.println("SENDER-EMULATOR-FAIL: " + phase + " " + failure.toString());
        } finally {
            // Never let a socket cleanup failure skip RMS closure.
            try { if (sender != null) sender.close(); }
            catch (IOException failure) { success = false; System.out.println("SENDER-EMULATOR-FAIL: socket cleanup " + failure); }
            try { if (state != null) state.close(); else if (file != null) file.close(); }
            catch (IOException failure) { success = false; System.out.println("SENDER-EMULATOR-FAIL: RMS cleanup " + failure); }
            if (success) System.out.println("SENDER-EMULATOR-PASS: " + phase);
            notifyDestroyed();
        }
    }
}
