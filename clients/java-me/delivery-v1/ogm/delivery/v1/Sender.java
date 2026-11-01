package ogm.delivery.v1;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import javax.microedition.io.Connector;
import javax.microedition.io.SocketConnection;

/** Sequential worker-thread sender. Caller owns WriterState; close cancels I/O. */
public final class Sender {
    // Package-local seam for deterministic stream/open/cancellation tests only.
    interface Opener { SocketConnection open(String address) throws IOException; }
    private final WriterState state;
    private final Opener opener;
    private Link current;
    private boolean busy;

    public Sender(WriterState state) throws IOException {
        this(state, new Opener() {
            public SocketConnection open(String address) throws IOException {
                return (SocketConnection)Connector.open("socket://" + address, Connector.READ_WRITE, true);
            }
        });
    }

    Sender(WriterState state, Opener opener) throws IOException {
        if (state == null || opener == null) throw new IOException("Open writer state and socket opener required");
        state.writerID(); // Fail before I/O if the caller's state needs recovery.
        this.state = state; this.opener = opener;
    }

    /** Explicitly connect/reconnect. No identity/version/authentication bytes. */
    public void connect(String address) throws IOException {
        address(address);
        Link link;
        synchronized (this) {
            if (busy) throw new IOException("Sender operation already in progress");
            if (current != null && !current.released()) throw new IOException("Close the previous connection first");
            state.writerID();
            busy = true; current = link = new Link();
        }
        try {
            SocketConnection socket = opener.open(address);
            link.socket(socket);
            link.input(socket.openInputStream());
            link.output(socket.openOutputStream());
            link.check();
        } catch (IOException failure) {
            throw cleanup(link, failure);
        } catch (RuntimeException failure) {
            throw cleanup(link, new IOException("Socket operation failed: " + failure.toString()));
        } finally { idle(); }
    }

    /** Reserve durably before sending; refuses to replace an existing pending SEND. */
    public Codec.Frame send(int recipient, String text) throws IOException {
        return exchange(false, recipient, text);
    }

    /** Resend only the exact persisted request; never allocate on retry/reconnect. */
    public Codec.Frame retry() throws IOException { return exchange(true, 0, null); }

    private synchronized Link begin() throws IOException {
        if (busy) throw new IOException("Sender operation already in progress");
        if (current == null) throw new IOException("Connect before sending");
        current.check();
        busy = true;
        return current;
    }

    private synchronized void idle() { busy = false; }

    private Codec.Frame exchange(boolean retry, int recipient, String text) throws IOException {
        Link link = begin();
        try {
            Codec.Frame request = retry ? state.pending() : state.reserve(recipient, text);
            if (request == null) throw new IOException("No pending SEND to retry");
            link.check();
            Codec.write(link.output, request);
            link.output.flush();
            Codec.Frame reply = Codec.read(link.input);
            boolean ack = reply.kind == Codec.ACK && reply.clientSeq == request.clientSeq;
            boolean error = reply.kind == Codec.ERROR && reply.requestOp == Codec.SEND && reply.reference == request.clientSeq
                && (reply.code == Codec.REJECTED || reply.code == Codec.CONFLICT || reply.code == Codec.RETRYABLE);
            if (!ack && !error) throw new IOException("Unexpected or uncorrelated SEND response");
            link.check();
            // Only matching replies reach RMS. A failed clear poisons WriterState;
            // close/join this sender, then reopen RMS before constructing a new one.
            state.resolve(reply);
            return reply; // ACK vs the three ERROR codes remains explicit to caller.
        } catch (IOException failure) {
            throw cleanup(link, failure);
        } catch (RuntimeException failure) {
            throw cleanup(link, new IOException("SEND operation failed: " + failure.toString()));
        } finally { idle(); }
    }

    /** May be called by a cancellation thread, without waiting for the I/O lock.
     * Join the worker before reconnecting or closing/reopening WriterState. */
    public void close() throws IOException {
        Link link;
        synchronized (this) { link = current; }
        if (link != null) link.close();
    }

    private static IOException cleanup(Link link, IOException original) {
        try { link.close(); }
        catch (IOException failure) { return new IOException(original.toString() + "; cleanup: " + failure.toString()); }
        return original;
    }

    // Accept an outbound DNS/IPv4 host:port only. An omitted host could request
    // an inbound server socket in GCF; URLs/parameters and IPv6 are not accepted.
    private static void address(String value) throws IOException {
        if (value == null || value.length() > 259) throw new IOException("Use an explicit host:port");
        int colon = value.indexOf(':');
        if (colon <= 0 || colon != value.lastIndexOf(':') || colon == value.length() - 1)
            throw new IOException("Use an explicit DNS/IPv4 host:port");
        for (int i = 0; i < colon; i++) {
            char c = value.charAt(i);
            if (!(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '.' && c != '-')
                throw new IOException("Invalid socket host");
        }
        int port = 0;
        if (value.length() - colon - 1 > 5) throw new IOException("Socket port must be 1..65535");
        for (int i = colon + 1; i < value.length(); i++) {
            char c = value.charAt(i);
            if (c < '0' || c > '9') throw new IOException("Socket port must be 1..65535");
            port = port * 10 + c - '0';
        }
        if (port < 1 || port > 65535) throw new IOException("Socket port must be 1..65535");
    }

    /** Resource installation races safely with cancellation, including open(). */
    private static final class Link {
        private SocketConnection socket;
        private InputStream input;
        private OutputStream output;
        private boolean cancelled, released;

        synchronized void socket(SocketConnection value) throws IOException {
            socket = value; released = false; check();
        }
        synchronized void input(InputStream value) throws IOException {
            input = value; released = false; check();
        }
        synchronized void output(OutputStream value) throws IOException {
            output = value; released = false; check();
        }
        synchronized void check() throws IOException { if (cancelled) throw new IOException("Connection closed/cancelled"); }
        synchronized boolean released() { return released; }

        synchronized void close() throws IOException {
            if (released) return;
            cancelled = true;
            IOException failure = null;
            // Close all handles: closing just the connection may leave streams alive.
            if (socket != null) try { socket.close(); } catch (IOException e) { failure = append(failure, e); }
            if (input != null) try { input.close(); } catch (IOException e) { failure = append(failure, e); }
            if (output != null) try { output.close(); } catch (IOException e) { failure = append(failure, e); }
            if (failure != null) throw failure;
            released = true;
        }
        private static IOException append(IOException first, IOException next) {
            return first == null ? next : new IOException(first.toString() + "; " + next.toString());
        }
    }
}
