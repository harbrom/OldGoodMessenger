package ogm.delivery.v1;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.InterruptedIOException;
import java.io.OutputStream;
import javax.microedition.io.SocketConnection;

/** CLDC-only tests of the real sender using a package-local socket seam. */
public final class SenderTest {
    private static int checks;
    private static void check(boolean value, String message) throws Exception {
        checks++;
        if (!value) throw new Exception(message);
    }
    private static final class Disk implements StateStorage {
        byte[] record;
        int writes, failAt;
        boolean commitError;
        public byte[] read() { return record; }
        public void write(byte[] bytes) throws IOException {
            if (++writes == failAt) {
                if (commitError) record = bytes;
                throw new IOException("Injected RMS uncertainty");
            }
            record = bytes;
        }
        public void close() { }
    }
    private static WriterState provision(Disk disk) throws IOException { return WriterState.provision(disk, 100001, 7); }
    private static WriterState reopen(Disk disk) throws IOException { return WriterState.open(disk, 100001, 7); }
    private static Codec.Frame ack(long seq) {
        Codec.Frame f = new Codec.Frame(); f.kind = Codec.ACK; f.clientSeq = seq; f.messageID = 42; return f;
    }
    private static Codec.Frame error(int code) {
        Codec.Frame f = new Codec.Frame(); f.kind = Codec.ERROR; f.requestOp = Codec.SEND; f.reference = 1; f.code = code; return f;
    }
    private static final class Capture extends ByteArrayOutputStream {
        boolean flushed, closed;
        int fail;
        public void flush() throws IOException {
            flushed = true;
            if (fail == 1) throw new IOException("Injected flush failure");
        }
        public void write(byte[] b, int off, int len) {
            if (fail == 2) throw new IllegalStateException("Injected stream failure");
            super.write(b, off, len);
        }
        public void close() { closed = true; }
    }
    private static class Reply extends InputStream {
        final ByteArrayInputStream bytes;
        final Capture output;
        boolean closed, timeout;
        Reply(byte[] bytes, Capture output) { this.bytes = new ByteArrayInputStream(bytes); this.output = output; }
        public int read() throws IOException {
            if (!output.flushed) throw new IOException("Read before flush");
            if (timeout) throw new InterruptedIOException("Injected timeout");
            return bytes.read(); // One byte at a time, also through InputStream.read(byte[]).
        }
        public void close() { closed = true; }
    }
    private static class Socket implements SocketConnection {
        final Capture output = new Capture();
        InputStream input;
        boolean closed, streamFailure, closeFailure;
        Socket(byte[] bytes) { input = new Reply(bytes, output); }
        public InputStream openInputStream() { return input; }
        public OutputStream openOutputStream() throws IOException {
            if (streamFailure) throw new IOException("Injected stream-open failure");
            return output;
        }
        public DataInputStream openDataInputStream() { return new DataInputStream(input); }
        public DataOutputStream openDataOutputStream() { return new DataOutputStream(output); }
        public void close() throws IOException {
            if (closeFailure) throw new IOException("Injected socket-close failure");
            closed = true;
        }
        public void setSocketOption(byte option, int value) { }
        public int getSocketOption(byte option) { return 0; }
        public String getLocalAddress() { return "127.0.0.1"; }
        public int getLocalPort() { return 1; }
        public String getAddress() { return "127.0.0.1"; }
        public int getPort() { return 2; }
    }
    private static final class Factory implements Sender.Opener {
        Socket next;
        int opens;
        Factory(Socket socket) { next = socket; }
        public SocketConnection open(String address) throws IOException { opens++; return next; }
    }
    private static void sendFails(Sender sender) throws Exception {
        try { sender.send(100002, "text"); throw new Exception("SEND unexpectedly succeeded"); }
        catch (IOException expected) { checks++; }
    }
    private static void exact(Codec.Frame a, Codec.Frame b) throws Exception {
        byte[] x = Codec.encode(a), y = Codec.encode(b);
        check(x.length == y.length, "Retry length changed");
        for (int i = 0; i < x.length; i++) check(x[i] == y[i], "Retry bytes changed");
    }
    private static void closed(Socket socket) throws Exception {
        check(socket.closed && ((Reply)socket.input).closed && socket.output.closed, "Connection handles leaked");
    }

    private static void replies() throws Exception {
        int[] codes = {0, Codec.REJECTED, Codec.CONFLICT, Codec.RETRYABLE};
        for (int i = 0; i < codes.length; i++) {
            Disk disk = new Disk(); WriterState state = provision(disk);
            Socket socket = new Socket(Codec.encode(codes[i] == 0 ? ack(1) : error(codes[i])));
            Factory factory = new Factory(socket);
            Sender sender = new Sender(state, factory); sender.connect("127.0.0.1:1234");
            String text = "\u041f\u0440\u0438\u0432\u0456\u0442 Za\u017c\u00f3\u0142\u0107 e\u0301 \ud83d\ude42\u0000\n";
            Codec.Frame result = sender.send(100002, text);
            check(socket.output.flushed, "SEND not flushed");
            Codec.Frame sent = Codec.decode(socket.output.toByteArray());
            check(sent.kind == Codec.SEND && sent.clientSeq == 1 && sent.recipient == 100002 && text.equals(sent.text), "Unexpected wire/identity handshake");
            check(result.kind == (codes[i] == 0 ? Codec.ACK : Codec.ERROR) && result.code == codes[i], "Outcome lost");
            check((state.pending() != null) == (codes[i] == Codec.RETRYABLE), "Wrong outbox resolution");
            sender.close(); closed(socket); state.close(); state = reopen(disk);
            check(state.lastSequence() == 1 && (state.pending() != null) == (codes[i] == Codec.RETRYABLE), "Restart outcome wrong");
            if (state.pending() != null) {
                Codec.Frame pending = state.pending();
                socket = new Socket(Codec.encode(ack(1)));
                factory = new Factory(socket);
                sender = new Sender(state, factory); sender.connect("localhost:1234");
                sendFails(sender); // A replacement SEND must not reach TCP.
                check(socket.output.size() == 0, "Replacement SEND transmitted");
                socket = new Socket(Codec.encode(ack(1))); factory.next = socket;
                sender.connect("localhost:1234"); // Previous rejected operation closed socket.
                check(sender.retry().messageID == 42, "Retry not acknowledged");
                exact(pending, Codec.decode(socket.output.toByteArray())); sender.close();
            }
            state.close();
        }
    }

    private static void invalidReplies() throws Exception {
        Codec.Frame changed = new Codec.Frame(); changed.kind = Codec.CHANGED; changed.tail = 1;
        Codec.Frame wrongError = error(Codec.REJECTED); wrongError.reference = 2;
        Codec.Frame wrongOp = error(Codec.REJECTED); wrongOp.requestOp = Codec.SYNC;
        byte[][] replies = {new byte[0], new byte[]{0, 8}, new byte[]{8, 0}, new byte[]{0, 17, (byte)0x90},
            Codec.encode(changed), Codec.encode(ack(2)), Codec.encode(wrongError), Codec.encode(wrongOp), Codec.encode(error(Codec.CURSOR_INVALID))};
        for (int i = 0; i < replies.length + 3; i++) {
            Disk disk = new Disk(); WriterState state = provision(disk);
            Socket socket = new Socket(i < replies.length ? replies[i] : Codec.encode(ack(1)));
            if (i == replies.length) ((Reply)socket.input).timeout = true;
            if (i == replies.length + 1) socket.output.fail = 1;
            if (i == replies.length + 2) socket.output.fail = 2;
            Sender sender = new Sender(state, new Factory(socket)); sender.connect("127.0.0.1:1234");
            sendFails(sender); closed(socket);
            Codec.Frame pending = state.pending(); check(pending != null && pending.clientSeq == 1, "Uncertain SEND cleared");
            state.close(); state = reopen(disk); exact(pending, state.pending());
            Socket good = new Socket(Codec.encode(ack(1))); sender = new Sender(state, new Factory(good));
            sender.connect("127.0.0.1:1234"); check(sender.retry().messageID == 42, "Restart retry failed");
            exact(pending, Codec.decode(good.output.toByteArray()));
            sender.close(); state.close();
        }
    }

    private static void storageAndConnectionErrors() throws Exception {
        for (int mode = 0; mode < 2; mode++) {
            Disk disk = new Disk(); WriterState state = provision(disk);
            disk.failAt = 3; disk.commitError = mode == 1;
            Socket socket = new Socket(Codec.encode(ack(1)));
            Sender sender = new Sender(state, new Factory(socket)); sender.connect("127.0.0.1:1234");
            sendFails(sender); closed(socket);
            try { state.pending(); throw new Exception("Uncertain RMS result usable"); } catch (IOException expected) { checks++; }
            try { sender.connect("127.0.0.1:1234"); throw new Exception("Sender reused unverified writer"); } catch (IOException expected) { checks++; }
            state.close(); state = reopen(disk);
            check(state.lastSequence() == 1 && (state.pending() == null) == (mode == 1), "RMS clear recovery wrong");
            state.close();
        }
        Disk disk = new Disk(); WriterState state = provision(disk);
        Socket socket = new Socket(Codec.encode(ack(1))); socket.streamFailure = true;
        Sender sender = new Sender(state, new Factory(socket));
        try { sender.connect("localhost:1"); throw new Exception("Stream-open error hidden"); } catch (IOException expected) { checks++; }
        check(socket.closed && ((Reply)socket.input).closed && state.lastSequence() == 0, "Partial connection leaked");
        Factory factory = new Factory(new Socket(Codec.encode(ack(1)))); sender = new Sender(state, factory);
        String[] invalid = {null, "", ":1", "host", "host:0", "host:65536", "host:-1", "host:000001", "socket://host:1", "host:1;param=x", "::1:1"};
        for (int i = 0; i < invalid.length; i++) {
            try { sender.connect(invalid[i]); throw new Exception("Invalid address accepted"); } catch (IOException expected) { checks++; }
        }
        check(factory.opens == 0, "Invalid address reached Connector");
        sender.connect("localhost:1234");
        try { sender.connect("localhost:1234"); throw new Exception("Live socket replaced"); } catch (IOException expected) { checks++; }
        socket = factory.next; socket.closeFailure = true;
        try { sender.close(); throw new Exception("Close error hidden"); } catch (IOException expected) { checks++; }
        check(((Reply)socket.input).closed && socket.output.closed, "Close failure skipped other handles");
        socket.closeFailure = false; sender.close(); closed(socket); state.close();
    }

    private static final class Gate extends InputStream {
        boolean entered, closed;
        public synchronized int read() throws IOException {
            entered = true; notifyAll();
            while (!closed) try { wait(); } catch (InterruptedException e) { throw new IOException("Interrupted"); }
            throw new IOException("Cancelled read");
        }
        public synchronized void close() { closed = true; notifyAll(); }
        synchronized void entered() throws Exception {
            long until = System.currentTimeMillis() + 2000;
            while (!entered && System.currentTimeMillis() < until) wait(100);
            check(entered, "Worker did not enter read");
        }
    }
    private static final class Worker extends Thread {
        final Sender sender;
        final boolean connect;
        Exception failure;
        boolean done;
        Worker(Sender sender, boolean connect) { this.sender = sender; this.connect = connect; }
        public void run() {
            try { if (connect) sender.connect("localhost:1"); else sender.retry(); }
            catch (Exception e) { failure = e; }
            finally { synchronized (this) { done = true; notifyAll(); } }
        }
        void joined() throws Exception {
            synchronized (this) {
                long until = System.currentTimeMillis() + 2000;
                while (!done && System.currentTimeMillis() < until) wait(100);
                check(done && failure instanceof IOException, "Cancellation did not finish with an error");
            }
            join(); // CLDC 1.1 has only the no-argument Thread.join().
        }
    }
    private static final class Opening implements Sender.Opener {
        final Socket socket;
        boolean entered, release;
        Opening(Socket socket) { this.socket = socket; }
        public synchronized SocketConnection open(String address) throws IOException {
            entered = true; notifyAll();
            while (!release) try { wait(); } catch (InterruptedException e) { throw new IOException("Interrupted"); }
            return socket;
        }
        synchronized void entered() throws Exception {
            long until = System.currentTimeMillis() + 2000;
            while (!entered && System.currentTimeMillis() < until) wait(100);
            check(entered, "Worker did not enter open");
        }
        synchronized void release() { release = true; notifyAll(); }
    }
    private static void cancellation() throws Exception {
        Disk disk = new Disk(); WriterState state = provision(disk); Codec.Frame original = state.reserve(100002, "cancelled");
        Socket socket = new Socket(new byte[0]); Gate input = new Gate(); socket.input = input;
        Sender sender = new Sender(state, new Factory(socket)); sender.connect("localhost:1");
        Worker worker = new Worker(sender, false); worker.start(); input.entered();
        // Concurrent operations fail immediately; close does not take the I/O lock.
        sendFails(sender); sender.close(); worker.joined();
        check(socket.closed && input.closed && socket.output.closed, "Cancellation leaked handles"); exact(original, state.pending());
        socket = new Socket(new byte[0]); Opening opener = new Opening(socket);
        sender = new Sender(state, opener); worker = new Worker(sender, true); worker.start(); opener.entered();
        sender.close(); opener.release(); worker.joined();
        check(socket.closed, "Late Connector result leaked"); exact(original, state.pending()); state.close();
    }

    public static void main(String[] args) throws Exception {
        replies(); invalidReplies(); storageAndConnectionErrors(); cancellation();
        System.out.println("SENDER-TEST-PASS: " + checks + " checks (responses, exact retry, RMS failures, cleanup and cancellation)");
    }
}
