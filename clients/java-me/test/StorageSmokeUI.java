import java.lang.reflect.Field;
import java.lang.reflect.Method;
import javax.microedition.lcdui.*;
import javax.microedition.midlet.MIDlet;
import javax.microedition.rms.RecordStore;
import org.microemu.MIDletBridge;
import org.microemu.app.Headless;

/** Desktop-only driver. Exercises the actual handset JAR, never packaged in it. */
public final class StorageSmokeUI {
    private static MIDlet app;
    private static int checks;
    private static Object field(String name) throws Exception {
        Field field = app.getClass().getDeclaredField(name); field.setAccessible(true); return field.get(app);
    }
    private static void check(boolean value, String message) throws Exception {
        checks++; if (!value) throw new Exception(message);
    }
    private static void released() throws Exception {
        for (String name : new String[] { "RmsStateStorage", "RmsHistoryStorage" }) {
            Class<?> type = app.getClass().getClassLoader().loadClass("ogm.delivery.v1." + name);
            Field active = type.getDeclaredField("active"); active.setAccessible(true);
            check(!active.getBoolean(null), "RMS adapter owner leaked: " + name);
        }
    }
    private static void ui(final Runnable action) throws Exception {
        final boolean[] done = { false };
        final Throwable[] error = { null };
        Display.getDisplay(app).callSerially(new Runnable() {
            public void run() {
                try { action.run(); } catch (Throwable failure) { error[0] = failure; }
                finally { synchronized (done) { done[0] = true; done.notifyAll(); } }
            }
        });
        synchronized (done) {
            long until = System.currentTimeMillis() + 5000;
            while (!done[0] && System.currentTimeMillis() < until) done.wait(20);
            if (!done[0]) throw new Exception("UI callback timeout");
        }
        if (error[0] != null) throw new Exception("UI failure", error[0]);
    }
    private static void invoke(String name) throws Exception {
        ((CommandListener)app).commandAction((Command)field(name), (Form)field("form"));
    }
    private static void press(final String name) throws Exception {
        ui(new Runnable() { public void run() { try { invoke(name); } catch (Exception e) { throw new RuntimeException(e); } } });
        long until = System.currentTimeMillis() + 5000;
        while (true) {
            synchronized (app) { if (!((Boolean)field("busy")).booleanValue()) break; }
            if (System.currentTimeMillis() > until) throw new Exception("Worker timeout: " + name);
            Thread.sleep(5);
        }
        // Worker enqueues its display update before releasing busy; this callback
        // follows that update and verifies the result is actually visible.
        ui(new Runnable() { public void run() { } });
        check(((StringItem)field("resultView")).getText().equals(field("result")), "UI result stale");
        released();
        System.out.println(name + ": " + field("result"));
    }
    private static void result(String expected) throws Exception {
        check(((String)field("result")).indexOf(expected) >= 0, "Expected result: " + expected + "; actual " + field("result"));
    }
    private static boolean exists(String name) {
        String[] stores = RecordStore.listRecordStores();
        if (stores != null) for (int i = 0; i < stores.length; i++) if (stores[i].equals(name)) return true;
        return false;
    }
    private static byte[] record(String name) throws Exception {
        RecordStore store = RecordStore.openRecordStore(name, false);
        try { check(store.getNumRecords() == 1, "Unexpected RMS record count"); return store.getRecord(1); }
        finally { store.closeRecordStore(); }
    }
    private static int version(String name) throws Exception {
        RecordStore store = RecordStore.openRecordStore(name, false);
        try { return store.getVersion(); } finally { store.closeRecordStore(); }
    }
    private static void equal(byte[] a, byte[] b) throws Exception {
        check(java.util.Arrays.equals(a, b), "Persisted bytes changed unexpectedly");
    }
    private static void lifecycle(String method) throws Exception {
        Method m = app.getClass().getDeclaredMethod(method); m.setAccessible(true); m.invoke(app);
    }
    public static void main(String[] args) {
        try {
            // The driver owns JVM exit, so destruction can be asserted before
            // emitting PASS rather than letting --quit terminate the assertions.
            Headless.main(new String[] { "--rms", "file", args[0] });
            app = MIDletBridge.getCurrentMIDlet();
            check(app.getClass().getName().equals("ogm.delivery.smoke.StorageSmoke"), "Wrong MIDlet");
            final String writer = "ogm-smoke-writer-v1", history = "ogm-smoke-history-v1";
            String phase = args[1];
            if (phase.equals("fresh")) {
                check(!exists(writer) && !exists(history), "Launch auto-created RMS");
                press("inspect"); result("FAILED:");
                check(((String)field("writer")).indexOf("Unavailable") >= 0 && ((String)field("cached")).indexOf("Unavailable") >= 0, "Both missing stores not reported");
                press("reserve"); result("FAILED:"); press("verify"); result("FAILED:");
                press("history"); result("FAILED:"); press("timing"); result("FAILED:");
                check(!exists(writer) && !exists(history), "Missing-state operation auto-created RMS");
            } else if (phase.equals("provision")) {
                // Hold the MIDlet monitor on its UI thread so the worker cannot
                // reach RMS until overlap and pause have been requested.
                ui(new Runnable() { public void run() {
                    try { synchronized (app) { invoke("provision"); invoke("reserve"); lifecycle("pauseApp"); } }
                    catch (Exception e) { throw new RuntimeException(e); }
                } });
                long until = System.currentTimeMillis() + 5000;
                while (true) {
                    synchronized (app) { if (!((Boolean)field("busy")).booleanValue()) break; }
                    if (System.currentTimeMillis() > until) throw new Exception("Paused worker timeout");
                    Thread.sleep(5);
                }
                check(((String)field("writer")).indexOf("last seq 0; pending none") >= 0, "Overlapping command executed");
                released();
                System.out.println("provision: " + field("result"));
                ui(new Runnable() { public void run() { try { lifecycle("startApp"); } catch (Exception e) { throw new RuntimeException(e); } } });
                press("initialize"); result("History provision/close:");
                press("reserve"); result("local pending only, NOT delivered");
                press("verify"); result("Exact pending verified twice");
                check(record(writer).length < 256, "Timing payload not small");
                check(record(history).length == 30, "History should be empty");
            } else if (phase.equals("reopen")) {
                byte[] before = record(writer), beforeHistory = record(history);
                press("inspect"); result("Both stores inspected");
                check(((String)field("writer")).indexOf("last seq 1; pending SEND seq 1 to 100002") >= 0, "Pending not restored");
                check(((String)field("cached")).indexOf("checkpoint 0; ITEM count 0") >= 0, "History not restored");
                press("verify"); result("Exact pending verified twice");
                press("provision"); result("FAILED:");
                press("initialize"); result("FAILED:");
                press("reserve"); result("already reserved");
                int beforeVersion = version(writer), historyVersion = version(history);
                press("timing"); result("Timing ms:");
                check(version(writer) == beforeVersion + 1, "Timing did not perform exactly one small rewrite");
                check(version(history) == historyVersion, "Timing wrote history");
                equal(before, record(writer)); equal(beforeHistory, record(history));
                ui(new Runnable() { public void run() { try { lifecycle("pauseApp"); lifecycle("startApp"); lifecycle("startApp"); } catch (Exception e) { throw new RuntimeException(e); } } });
                check(((Form)field("form")).size() == 5, "Repeated start duplicated UI");
            } else if (phase.equals("corrupt")) {
                // Test-only corruption: the installable MIDlet has no such command.
                RecordStore store = RecordStore.openRecordStore(writer, false);
                try { byte[] bytes = store.getRecord(1); bytes[bytes.length - 1] ^= 1; store.setRecord(1, bytes, 0, bytes.length); }
                finally { store.closeRecordStore(); }
                byte[] before = record(writer);
                press("verify"); result("Corrupt, incompatible or mismatched writer state");
                press("provision"); result("FAILED:");
                press("timing"); result("FAILED:");
                equal(before, record(writer));
            } else if (phase.equals("corrupt-reopen")) {
                byte[] before = record(writer);
                press("inspect"); result("FAILED:");
                check(((String)field("cached")).indexOf("checkpoint 0; ITEM count 0") >= 0, "Independent history unavailable");
                equal(before, record(writer));
                // Exit during a worker operation: defer destruction until finally
                // closes RMS, then check both adapters have released ownership.
                ui(new Runnable() { public void run() {
                    try { synchronized (app) { invoke("history"); invoke("quit"); } }
                    catch (Exception e) { throw new RuntimeException(e); }
                } });
                long until = System.currentTimeMillis() + 5000;
                while (true) {
                    synchronized (app) { if (!((Boolean)field("busy")).booleanValue()) break; }
                    if (System.currentTimeMillis() > until) throw new Exception("Exit worker did not complete");
                    Thread.sleep(5);
                }
                released();
            } else throw new Exception("Unknown phase");
            if (!phase.equals("corrupt-reopen")) {
                ui(new Runnable() { public void run() { try { invoke("quit"); } catch (Exception e) { throw new RuntimeException(e); } } });
                released();
            }
            System.out.println("SMOKE-UI-PASS: " + phase + " (" + checks + " checks)");
            System.exit(0);
        } catch (Throwable failure) {
            failure.printStackTrace(); System.out.println("SMOKE-UI-FAIL: " + failure); System.exit(1);
        }
    }
}
