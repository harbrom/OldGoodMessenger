package ogm;

import javax.microedition.midlet.MIDlet;

/** Test-only MIDlet using the exact production Transport and Utf8 classes. */
public final class EmulatorTest extends MIDlet implements Runnable {
    private Transport current;
    protected void startApp() { new Thread(this).start(); }
    protected void pauseApp() { }
    protected void destroyApp(boolean unconditional) { if (current != null) current.close(); }

    public void run() {
        try {
            String[] fixtures = {
                "ASCII", "\u041f\u0440\u0438\u0432\u0456\u0442",
                "Za\u017c\u00f3\u0142\u0107", "e\u0301", "\ud83d\ude42"
            };
            for (int cycle = 0; cycle < 4; cycle++) {
                current = new Transport();
                current.connect(getAppProperty("OGM-Server"), 100002);
                for (int i = 0; i < 10; i++) {
                    String sent = "C" + cycle + "-" + i + " " + fixtures[i % fixtures.length];
                    long started = System.currentTimeMillis();
                    current.send(100001, sent);
                    Transport.Message got = current.receive();
                    if (got.sender != 100001 || !sent.equals(got.text)) throw new Exception("Echo mismatch");
                    if (System.currentTimeMillis() - started > 2000) throw new Exception("Local echo exceeded two seconds");
                }
                if (cycle == 0) {
                    // The harness can request a longer idle test without changing application code.
                    String idle = getAppProperty("OGM-Test-Idle-Millis");
                    if (idle != null) Thread.sleep(Long.parseLong(idle));
                    current.send(100001, "AFTER-IDLE");
                    if (!"AFTER-IDLE".equals(current.receive().text)) throw new Exception("Idle mismatch");
                }
                current.close(); current = null;
                Thread.sleep(250);
            }
            System.out.println("OGM-EMULATOR-PASS: 41 echoes, four connections");
        } catch (Exception e) {
            System.out.println("OGM-EMULATOR-FAIL: " + e.toString());
        } finally {
            if (current != null) current.close();
            notifyDestroyed();
        }
    }
}
