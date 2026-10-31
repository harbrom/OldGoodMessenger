import java.lang.reflect.Field;
import javax.microedition.lcdui.*;
import javax.microedition.midlet.MIDlet;
import org.microemu.MIDletBridge;
import org.microemu.app.Headless;

/** Desktop-only driver for the unchanged, packaged MIDlet in MicroEmulator. */
public final class EmulatorUI {
    private static MIDlet app;

    private static Object control(String name) throws Exception {
        Field field = app.getClass().getDeclaredField(name);
        field.setAccessible(true);
        return field.get(app);
    }

    private static void onUI(final Runnable action) throws Exception {
        final boolean[] done = { false };
        final Throwable[] failure = { null };
        Display.getDisplay(app).callSerially(new Runnable() {
            public void run() {
                try { action.run(); } catch (Throwable e) { failure[0] = e; }
                synchronized (done) { done[0] = true; done.notifyAll(); }
            }
        });
        synchronized (done) {
            long deadline = System.currentTimeMillis() + 5000;
            while (!done[0] && System.currentTimeMillis() < deadline) done.wait(100);
            if (!done[0]) throw new Exception("UI callback timed out");
        }
        if (failure[0] != null) throw new Exception("UI failure", failure[0]);
    }

    private static void press(final String name) throws Exception {
        final Command command = (Command)control(name);
        final Form form = (Form)control("form");
        onUI(new Runnable() {
            public void run() { ((CommandListener)app).commandAction(command, form); }
        });
    }

    private static void awaitText(final String text) throws Exception {
        final StringItem received = (StringItem)control("received");
        final boolean[] found = { false };
        long deadline = System.currentTimeMillis() + 2000;
        while (System.currentTimeMillis() < deadline) {
            onUI(new Runnable() {
                public void run() { found[0] = received.getText().indexOf(text) >= 0; }
            });
            if (found[0]) return;
            Thread.sleep(10);
        }
        throw new Exception("UI did not display expected text: " + text + "; events=" + received.getText());
    }

    private static void exchange(final String text) throws Exception {
        final TextField input = (TextField)control("message");
        onUI(new Runnable() { public void run() { input.setString(text); } });
        press("send");
        awaitText("100001: " + text);
    }

    private static void connectFresh() throws Exception {
        final Field events = app.getClass().getDeclaredField("events");
        events.setAccessible(true);
        final StringItem view = (StringItem)control("received");
        final Command command = (Command)control("connect");
        final Form form = (Form)control("form");
        onUI(new Runnable() {
            public void run() {
                // Reset the retained buffer as well as its view. Hold the same
                // monitor as event() until this attempt has started, so only
                // events from this new boundary can satisfy the assertion.
                synchronized (app) {
                    try { events.set(app, ""); }
                    catch (IllegalAccessException e) { throw new RuntimeException(e); }
                    view.setText("");
                    ((CommandListener)app).commandAction(command, form);
                }
            }
        });
        awaitText("Connected as 100002");
    }

    private static void assertStaleConnectionIgnored() throws Exception {
        final Field events = app.getClass().getDeclaredField("events");
        events.setAccessible(true);
        final StringItem view = (StringItem)control("received");
        final TextField identity = (TextField)control("identity");
        onUI(new Runnable() {
            public void run() {
                synchronized (app) {
                    try { events.set(app, "\nConnected as 100002"); }
                    catch (IllegalAccessException e) { throw new RuntimeException(e); }
                    view.setText("Connected as 100002");
                    // The console already owns this ID, so this attempt must fail.
                    identity.setString("100001");
                }
            }
        });
        boolean rejected = false;
        try { connectFresh(); }
        catch (Exception e) {
            if (!e.getMessage().startsWith("UI did not display expected text: Connected as 100002")) throw e;
            rejected = true;
        }
        if (!rejected) throw new Exception("Stale connection event satisfied a rejected attempt");
        Thread.sleep(300);
        onUI(new Runnable() { public void run() { identity.setString("100002"); } });
        System.out.println("PASS: stale Connected event cannot satisfy a rejected connection attempt");
    }

    public static void main(String[] args) {
        try {
            Headless.main(new String[] { "--quit", args[0] });
            app = MIDletBridge.getCurrentMIDlet();
            if (!app.getClass().getName().equals("ogm.FirstContact")) throw new Exception("Wrong MIDlet");
            assertStaleConnectionIgnored();
            String[] fixtures = {"ASCII", "\u041f\u0440\u0438\u0432\u0456\u0442", "Za\u017c\u00f3\u0142\u0107", "e\u0301", "\ud83d\ude42"};
            for (int cycle = 0; cycle < 4; cycle++) {
                connectFresh();
                for (int i = 0; i < 10; i++) exchange("C" + cycle + "-" + i + " " + fixtures[i % fixtures.length]);
                if (cycle == 0) {
                    Thread.sleep(Long.parseLong(args[1]));
                    exchange("AFTER-IDLE");
                }
                press("disconnect");
                Thread.sleep(300);
            }
            System.out.println("OGM-EMULATOR-PASS: packaged UI, 41 echoes, four connections");
            press("quit");
        } catch (Throwable e) {
            e.printStackTrace();
            System.out.println("OGM-EMULATOR-FAIL: " + e.toString());
            System.exit(1);
        }
    }
}
