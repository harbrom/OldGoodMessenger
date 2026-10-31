package ogm;

import javax.microedition.lcdui.*;
import javax.microedition.midlet.MIDlet;

/** Disposable transport experiment, not the production messenger UI. */
public final class FirstContact extends MIDlet implements CommandListener, Runnable {
    private final Form form = new Form("OGM First Contact");
    private final TextField address = new TextField("Server", "127.0.0.1:19090", 100, TextField.ANY);
    private final TextField identity = new TextField("Test ID", "100002", 6, TextField.NUMERIC);
    private final TextField recipient = new TextField("Recipient", "100001", 6, TextField.NUMERIC);
    private final TextField message = new TextField("Message", "", 1000, TextField.ANY);
    private final StringItem received = new StringItem("Events", "Disconnected");
    private final Command connect = new Command("Connect", Command.SCREEN, 1);
    private final Command send = new Command("Send", Command.SCREEN, 2);
    private final Command disconnect = new Command("Disconnect", Command.SCREEN, 3);
    private final Command quit = new Command("Quit", Command.EXIT, 4);
    private Transport transport;
    private boolean workerActive, sending, ready, updatePending, initialized;
    private String events = "";

    protected void startApp() {
        if (initialized) return;
        initialized = true;
        String configured = getAppProperty("OGM-Server");
        if (configured != null) address.setString(configured);
        form.append(address); form.append(identity); form.append(recipient);
        form.append(message); form.append(received);
        form.addCommand(connect); form.addCommand(send); form.addCommand(disconnect); form.addCommand(quit);
        form.setCommandListener(this);
        Display.getDisplay(this).setCurrent(form);
    }

    protected void pauseApp() { }
    protected void destroyApp(boolean unconditional) { stopConnection(); }

    public void commandAction(Command command, Displayable displayable) {
        if (command == quit) { stopConnection(); notifyDestroyed(); return; }
        if (command == disconnect) { stopConnection(); return; }
        if (command == connect) {
            final String host = address.getString();
            final int id;
            try { id = Integer.parseInt(identity.getString()); }
            catch (NumberFormatException e) { event("Invalid ID"); return; }
            final Transport session = new Transport();
            synchronized (this) {
                if (workerActive || sending) { event("Wait for disconnect to finish"); return; }
                workerActive = true; transport = session;
            }
            event("Connecting");
            new Thread(new Runnable() {
                public void run() {
                    try {
                        session.connect(host, id);
                        synchronized (FirstContact.this) {
                            if (transport != session) return;
                            ready = true;
                        }
                        event("Connected as " + id);
                        while (true) {
                            Transport.Message incoming = session.receive();
                            event(incoming.sender + ": " + incoming.text);
                        }
                    } catch (Exception e) { event("Connection ended: " + e.toString()); }
                    finally {
                        session.close();
                        synchronized (FirstContact.this) {
                            if (transport == session) { transport = null; ready = false; }
                            workerActive = false;
                        }
                    }
                }
            }).start();
        } else if (command == send) {
            final String text = message.getString();
            final int target;
            try { target = Integer.parseInt(recipient.getString()); Utf8.encode(text); }
            catch (Exception e) { event(e.toString()); return; }
            final Transport session;
            synchronized (this) {
                if (!ready || sending) { event("Not ready or send in progress"); return; }
                sending = true; session = transport;
            }
            new Thread(new Runnable() {
                public void run() {
                    try { session.send(target, text); event("Sent to socket; no delivery acknowledgement"); }
                    catch (Exception e) { event("Send failed: " + e.toString()); session.close(); }
                    finally { synchronized (FirstContact.this) { sending = false; } }
                }
            }).start();
        }
    }

    private void stopConnection() {
        final Transport session;
        synchronized (this) { session = transport; transport = null; ready = false; }
        if (session != null) new Thread(new Runnable() {
            public void run() { session.close(); }
        }).start();
        event("Disconnect requested");
    }

    // Bound both the retained text and the number of pending UI callbacks.
    private synchronized void event(String text) {
        events = events + "\n" + text;
        if (events.length() > 2048) events = events.substring(events.length() - 2048);
        if (!updatePending) { updatePending = true; Display.getDisplay(this).callSerially(this); }
    }

    public synchronized void run() { received.setText(events); updatePending = false; }
}
