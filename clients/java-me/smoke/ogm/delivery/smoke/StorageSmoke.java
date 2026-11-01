package ogm.delivery.smoke;

import java.io.IOException;
import javax.microedition.lcdui.*;
import javax.microedition.midlet.MIDlet;
import ogm.delivery.v1.Codec;
import ogm.delivery.v1.HistoryStore;
import ogm.delivery.v1.RmsHistoryStorage;
import ogm.delivery.v1.RmsStateStorage;
import ogm.delivery.v1.StateStorage;
import ogm.delivery.v1.WriterState;

/** Offline diagnostic only. These fixed disposable bindings must never go online. */
public final class StorageSmoke extends MIDlet implements CommandListener, Runnable {
    private static final String WRITER_STORE = "ogm-smoke-writer-v1";
    private static final String HISTORY_STORE = "ogm-smoke-history-v1";
    private static final int ACCOUNT = 100001, RECIPIENT = 100002;
    private static final long WRITER = 12001, HISTORY_NAMESPACE = 12001;
    private static final String TEXT = "OGM offline smoke: \u041f\u0440\u0438\u0432\u0456\u0442 / Za\u017c\u00f3\u0142\u0107 / e\u0301 / \ud83d\ude42";
    private final Form form = new Form("OGM Storage Smoke");
    private final StringItem stateView = new StringItem("State", "Ready (offline)");
    private final StringItem writerView = new StringItem("Writer RMS", "Not inspected");
    private final StringItem historyView = new StringItem("History RMS", "Not inspected");
    private final StringItem resultView = new StringItem("Last result", "No operation yet");
    private final Command inspect = new Command("Inspect RMS", Command.SCREEN, 1);
    private final Command provision = new Command("Provision writer", Command.SCREEN, 2);
    private final Command reserve = new Command("Reserve SEND", Command.SCREEN, 3);
    private final Command verify = new Command("Verify pending", Command.SCREEN, 4);
    private final Command history = new Command("Inspect history", Command.SCREEN, 5);
    private final Command initialize = new Command("Init history", Command.SCREEN, 6);
    private final Command timing = new Command("Timing once", Command.SCREEN, 7);
    private final Command quit = new Command("Exit", Command.EXIT, 8);
    private boolean initialized, busy, paused, exiting, updatePending;
    private String diagnostic = "Ready (offline)", writer = "Not inspected", cached = "Not inspected";
    private String result = "No operation yet";

    protected synchronized void startApp() {
        if (exiting) return;
        paused = false;
        if (!initialized) {
            initialized = true;
            form.append("OGM Storage Smoke 0.0.1\nOffline; no delivery or ACK.\nAccount 100001; test writer 12001");
            form.append(stateView); form.append(writerView); form.append(historyView); form.append(resultView);
            form.addCommand(inspect); form.addCommand(provision); form.addCommand(reserve);
            form.addCommand(verify); form.addCommand(history); form.addCommand(initialize);
            form.addCommand(timing); form.addCommand(quit); form.setCommandListener(this);
        }
        Display.getDisplay(this).setCurrent(form);
        refresh();
    }

    protected synchronized void pauseApp() { paused = true; }

    // Do not wait for RMS on the lifecycle/UI thread. The single worker closes
    // its adapter in finally before completing an outstanding exit request.
    protected synchronized void destroyApp(boolean unconditional) { exiting = true; paused = true; }

    public synchronized void commandAction(final Command command, Displayable displayable) {
        if (command == quit) {
            exiting = true; diagnostic = busy ? "Closing after current operation" : "Closed";
            refresh();
            if (!busy) notifyDestroyed();
            return;
        }
        if (exiting || paused) return;
        if (busy) { result = "Busy; wait for the current operation"; refresh(); return; }
        if (command != inspect && command != provision && command != reserve && command != verify &&
            command != history && command != initialize && command != timing) return;
        busy = true; diagnostic = "Working: " + command.getLabel(); refresh();
        try {
            new Thread(new Runnable() {
                public void run() {
                    boolean success = false;
                    try { operate(command); success = true; }
                    catch (Exception failure) { report("FAILED: " + failure.toString()); }
                    finally {
                        synchronized (StorageSmoke.this) {
                            busy = false;
                            diagnostic = exiting ? "Closed" : (success ? "Ready (offline)" : "Error; no repair attempted");
                            refresh();
                            if (exiting) notifyDestroyed();
                        }
                    }
                }
            }).start();
        } catch (RuntimeException failure) {
            busy = false; diagnostic = "Worker start failed"; report(failure.toString());
        }
    }

    private void operate(Command command) throws IOException {
        if (command == inspect) {
            String failures = "";
            try { readWriter(false); }
            catch (IOException failure) { writerStatus("Unavailable: " + failure); failures = "Writer: " + failure; }
            try { readHistory(false); }
            catch (IOException failure) { historyStatus("Unavailable: " + failure); failures += " History: " + failure; }
            if (failures.length() != 0) throw new IOException(failures);
            report("Both stores inspected; no writes");
        } else if (command == provision) {
            writerStatus("Provisioning requested");
            long start = System.currentTimeMillis();
            StateStorage file = RmsStateStorage.create(WRITER_STORE);
            try { describe(WriterState.provision(file, ACCOUNT, WRITER)); }
            finally { close(file); }
            report("Writer provision/close: " + elapsed(start) + " ms; no pending SEND");
        } else if (command == reserve) {
            writerStatus("Reserving requested");
            long start = System.currentTimeMillis();
            StateStorage file = RmsStateStorage.open(WRITER_STORE);
            try {
                WriterState state = WriterState.open(file, ACCOUNT, WRITER);
                if (state.lastSequence() != 0 || state.pending() != null) throw new IOException("Test SEND already reserved; use Verify pending");
                state.reserve(RECIPIENT, TEXT); describe(state);
            } finally { close(file); }
            report("Open/reserve/close: " + elapsed(start) + " ms; local pending only, NOT delivered");
        } else if (command == verify) {
            long start = System.currentTimeMillis();
            readWriter(true); // Each read opens and closes RMS, including between these reads.
            readWriter(true);
            report("Exact pending verified twice with close/reopen: " + elapsed(start) + " ms; NOT delivered");
        } else if (command == history || command == initialize) {
            long start = System.currentTimeMillis();
            readHistory(command == initialize);
            report((command == initialize ? "History provision/close: " : "History open/read/close: ") + elapsed(start) + " ms");
        } else if (command == timing) measure();
    }

    private void readWriter(boolean exact) throws IOException {
        writerStatus("Opening existing writer");
        StateStorage file = RmsStateStorage.open(WRITER_STORE);
        try {
            WriterState state = WriterState.open(file, ACCOUNT, WRITER);
            if (exact) exact(state);
            describe(state);
        } finally { close(file); }
    }

    private void readHistory(boolean create) throws IOException {
        historyStatus(create ? "Initialization requested" : "Opening existing history");
        StateStorage file = create ? RmsHistoryStorage.create(HISTORY_STORE) : RmsHistoryStorage.open(HISTORY_STORE);
        try {
            HistoryStore state = create ? HistoryStore.provision(file, ACCOUNT, HISTORY_NAMESPACE) : HistoryStore.open(file, ACCOUNT, HISTORY_NAMESPACE);
            historyStatus("OK; checkpoint " + state.checkpoint() + "; ITEM count " + state.size());
        } finally { close(file); }
    }

    private static void exact(WriterState state) throws IOException {
        Codec.Frame frame = state.pending();
        if (state.lastSequence() != 1 || frame == null || frame.kind != Codec.SEND || frame.clientSeq != 1 ||
            frame.recipient != RECIPIENT || !TEXT.equals(frame.text)) throw new IOException("Exact pending SEND mismatch (expected seq 1, to 100002, fixed smoke text)");
    }

    private void describe(WriterState state) throws IOException {
        Codec.Frame frame = state.pending();
        writerStatus("OK; account " + state.accountID() + "; test writer " + state.writerID() +
            "; last seq " + state.lastSequence() + "; pending " + (frame == null ? "none" : "SEND seq " + frame.clientSeq + " to " + frame.recipient + " (local only)"));
    }

    private void measure() throws IOException {
        writerStatus("Timing existing writer");
        long opened = System.currentTimeMillis();
        StateStorage file = RmsStateStorage.open(WRITER_STORE);
        long read, write, closed;
        try {
            WriterState state = WriterState.open(file, ACCOUNT, WRITER); exact(state);
            byte[] bytes = file.read();
            read = elapsed(opened);
            long start = System.currentTimeMillis();
            file.write(bytes); // ONE identical, validated, small snapshot. Never an ACK/reset.
            write = elapsed(start);
        } finally { close(file); }
        closed = System.currentTimeMillis();
        readWriter(true);
        long reopened = elapsed(closed);
        long start = System.currentTimeMillis();
        readHistory(false);
        report("Timing ms: writer open/read " + read + ", identical write " + write +
            ", reopen/read/close " + reopened + ", history open/read/close " + elapsed(start) +
            ". Coarse clock; 0 is possible. Pending unchanged.");
    }

    private static void close(StateStorage file) throws IOException {
        try { file.close(); }
        catch (IOException failure) {
            try { file.close(); } // Bounded retry; never hide the first close failure.
            catch (IOException retry) { throw new IOException(failure + "; close retry: " + retry); }
            throw failure;
        }
    }
    private static long elapsed(long start) { return System.currentTimeMillis() - start; }
    private static String bounded(String value) { return value.length() > 480 ? value.substring(0, 480) : value; }
    private synchronized void writerStatus(String value) { writer = bounded(value); }
    private synchronized void historyStatus(String value) { cached = bounded(value); }
    private synchronized void report(String value) { result = bounded(value); }
    private synchronized void refresh() {
        if (!updatePending && !paused) { updatePending = true; Display.getDisplay(this).callSerially(this); }
    }
    public synchronized void run() {
        stateView.setText(diagnostic); writerView.setText(writer); historyView.setText(cached); resultView.setText(result);
        updatePending = false;
    }
}
