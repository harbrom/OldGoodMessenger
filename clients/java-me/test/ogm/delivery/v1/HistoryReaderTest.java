package ogm.delivery.v1;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import javax.microedition.io.SocketConnection;

/** CLDC-only state/stream tests; no history store or checkpoint implementation. */
public final class HistoryReaderTest {
    private static int checks;
    private static void check(boolean value, String message) throws Exception {
        checks++; if (!value) throw new Exception(message);
    }
    private static Codec.Frame item(long cursor, long id) {
        Codec.Frame f = new Codec.Frame(); f.kind = Codec.ITEM; f.cursor = cursor; f.messageID = id;
        f.sender = 100001; f.recipient = 100002; f.writerID = 7; f.clientSeq = id; f.text = "item " + id; return f;
    }
    private static Codec.Frame end(long next, long h) {
        Codec.Frame f = new Codec.Frame(); f.kind = Codec.END; f.next = next; f.through = h; return f;
    }
    private static byte[] wire(Codec.Frame[] frames) throws IOException {
        ByteArrayOutputStream out = new ByteArrayOutputStream();
        for (int i = 0; i < frames.length; i++) Codec.write(out, frames[i]);
        return out.toByteArray();
    }
    private static final class Output extends ByteArrayOutputStream {
        boolean closed, flushed;
        public void flush() { flushed = true; }
        public void close() { closed = true; }
    }
    private static final class Input extends InputStream {
        final ByteArrayInputStream bytes;
        final Output out;
        boolean closed;
        Input(byte[] bytes, Output out) { this.bytes = new ByteArrayInputStream(bytes); this.out = out; }
        public int read() throws IOException {
            if (!out.flushed) throw new IOException("Read before request flush");
            return bytes.read(); // Force split reads independently of TCP packets.
        }
        public void close() { closed = true; }
    }
    private static final class Socket implements SocketConnection {
        final Output output = new Output();
        InputStream input;
        boolean closed, streamFailure, closeFailure;
        Socket(byte[] bytes) { input = new Input(bytes, output); }
        public InputStream openInputStream() { return input; }
        public OutputStream openOutputStream() throws IOException {
            if (streamFailure) throw new IOException("Injected stream-open failure");
            return output;
        }
        public DataInputStream openDataInputStream() { return new DataInputStream(input); }
        public DataOutputStream openDataOutputStream() { return new DataOutputStream(output); }
        public void close() throws IOException { if (closeFailure) throw new IOException("Injected close failure"); closed = true; }
        public void setSocketOption(byte option, int value) { }
        public int getSocketOption(byte option) { return 0; }
        public String getLocalAddress() { return "127.0.0.1"; }
        public int getLocalPort() { return 1; }
        public String getAddress() { return "127.0.0.1"; }
        public int getPort() { return 2; }
    }
    private static final class Factory implements HistoryReader.Opener {
        Socket socket;
        int opens;
        Factory(Socket socket) { this.socket = socket; }
        public SocketConnection open(String address) { opens++; return socket; }
    }
    private static final class Count implements HistoryReader.Consumer {
        int pages, items;
        public void page(HistoryReader.Page page) { pages++; items += page.size(); }
    }
    private static void closed(Socket socket) throws Exception {
        check(socket.closed && ((Input)socket.input).closed && socket.output.closed, "Handles leaked");
    }
    private static void request(Codec.Frame frame, long after, long h, int limit) throws Exception {
        check(frame.kind == Codec.SYNC && frame.after == after && frame.through == h && frame.limit == limit, "Unexpected SYNC/identity handshake");
    }

    private static void validPages() throws Exception {
        final Socket socket = new Socket(wire(new Codec.Frame[]{item(2, 3), item(5, 7), end(5, 9), item(9, 8), end(9, 9), end(0, 0)}));
        final HistoryReader reader = new HistoryReader(100001, 2, new Factory(socket)); reader.connect("localhost:1");
        final int[] pages = {0};
        long result = reader.replay(new HistoryReader.Consumer() {
            public void page(HistoryReader.Page page) throws IOException {
                try {
                    check(reader.position() == (pages[0] == 0 ? 0 : 5), "Progress published before consumption");
                    check(!reader.complete() && page.size() == (pages[0] == 0 ? 2 : 1), "Bad page size/state");
                    check(page.after == (pages[0] == 0 ? 0 : 5) && page.through == 9, "Page snapshot mismatch");
                    check(page.item(0).messageID == (pages[0] == 0 ? 3 : 8), "ITEM ID mismatch");
                    try { page.item(page.size()); throw new Exception("Unused page slots exposed"); } catch (IndexOutOfBoundsException expected) { checks++; }
                    // Reentrant replay cannot issue another SYNC while consuming.
                    try { reader.replay(new Count()); throw new Exception("Concurrent SYNC admitted"); } catch (IOException expected) { checks++; }
                    pages[0]++;
                } catch (Exception failure) { throw new IOException(failure.toString()); }
            }
        });
        check(result == 9 && reader.position() == 9 && reader.snapshot() == 9 && reader.complete() && pages[0] == 2, "Replay progress wrong");
        Count empty = new Count(); check(reader.replay(empty) == 0 && empty.pages == 1 && empty.items == 0, "Fresh empty replay failed");
        ByteArrayInputStream requests = new ByteArrayInputStream(socket.output.toByteArray());
        request(Codec.read(requests), 0, 0, 2); request(Codec.read(requests), 5, 9, 2); request(Codec.read(requests), 0, 0, 2);
        check(requests.available() == 0, "Unexpected extra request"); reader.close(); closed(socket);
        for (int limit = 1; limit <= 8; limit++) {
            Codec.Frame[] frames = new Codec.Frame[limit + 1];
            StringBuffer text = new StringBuffer(); for (int i = 0; i < 500; i++) text.append("\ud83d\ude42");
            for (int i = 0; i < limit; i++) { frames[i] = item(i + 1, i + 1); frames[i].text = text.toString(); }
            frames[limit] = end(limit, limit); // Exactly full terminal pages are valid.
            Socket full = new Socket(wire(frames)); HistoryReader r = new HistoryReader(100001, limit, new Factory(full));
            r.connect("127.0.0.1:65535"); Count count = new Count(); r.replay(count);
            check(count.pages == 1 && count.items == limit, "Full terminal page or page limit wrong");
            r.close(); closed(full);
            check(!r.complete() && r.position() == limit && r.snapshot() == limit, "Close did not clear completion/preserve volatile position");
        }
        Socket gap = new Socket(wire(new Codec.Frame[]{end(9, 9)}));
        HistoryReader r = new HistoryReader(100001, 1, new Factory(gap)); r.connect("localhost:1"); Count count = new Count();
        check(r.replay(7, count) == 9 && count.items == 0 && count.pages == 1, "Empty terminal cursor gap rejected"); r.close();
    }

    private static void fails(byte[] bytes, int limit, long start, int successfulPages, long position) throws Exception {
        Socket socket = new Socket(bytes); Factory factory = new Factory(socket);
        HistoryReader reader = new HistoryReader(100001, limit, factory); reader.connect("localhost:1"); Count count = new Count();
        try { reader.replay(start, count); throw new Exception("Invalid history accepted"); } catch (IOException expected) { checks++; }
        check(count.pages == successfulPages && reader.position() == position && !reader.complete(), "Invalid page published or progress advanced"); closed(socket);
        int before = socket.output.size();
        try { reader.replay(new Count()); throw new Exception("Dirty connection reused"); } catch (IOException expected) { checks++; }
        check(socket.output.size() == before, "Request sent on invalidated stream");
        // New stream and explicit full replay recover without retaining invalid data.
        Socket good = new Socket(wire(new Codec.Frame[]{item(1, 1), end(1, 1)})); factory.socket = good;
        reader.connect("localhost:1"); Count replay = new Count(); reader.replay(replay);
        check(replay.pages == 1 && replay.items == 1 && reader.position() == 1, "Clean reconnect/replay failed"); reader.close(); closed(good);
    }
    private static void invalidPages() throws Exception {
        Codec.Frame foreign = item(1, 1); foreign.sender = 100003; foreign.recipient = 100004;
        Codec.Frame ack = new Codec.Frame(); ack.kind = Codec.ACK; ack.clientSeq = 1; ack.messageID = 1;
        Codec.Frame changed = new Codec.Frame(); changed.kind = Codec.CHANGED; changed.tail = 99;
        Codec.Frame wrongError = new Codec.Frame(); wrongError.kind = Codec.ERROR; wrongError.requestOp = Codec.SYNC; wrongError.reference = 1; wrongError.code = Codec.RETRYABLE;
        Codec.Frame sendError = new Codec.Frame(); sendError.kind = Codec.ERROR; sendError.requestOp = Codec.SEND; sendError.reference = 1; sendError.code = Codec.REJECTED;
        Codec.Frame correlated = new Codec.Frame(); correlated.kind = Codec.ERROR; correlated.requestOp = Codec.SYNC; correlated.reference = 0; correlated.code = Codec.RETRYABLE;
        Codec.Frame[][] invalid = {
            {item(1, 1), item(1, 2), end(1, 1)}, {item(2, 1), item(1, 2), end(2, 2)},
            {item(1, 2), item(2, 2), end(2, 2)}, {item(1, 3), item(2, 2), end(2, 2)},
            {foreign, end(1, 1)}, {item(1, 1), item(2, 2), item(3, 3), end(3, 3)},
            {item(1, 1), end(1, 3)}, {item(1, 1), item(2, 2), end(1, 3)},
            {item(5, 5), end(4, 4)}, {end(1, 2)}, {ack}, {changed}, {wrongError}, {sendError}, {item(1, 1), correlated}
        };
        for (int i = 0; i < invalid.length; i++) fails(wire(invalid[i]), 2, 0, 0, 0);
        fails(wire(new Codec.Frame[]{end(4, 4)}), 1, 5, 0, 5);
        fails(wire(new Codec.Frame[]{item(1, 5), end(1, 2), item(2, 5), end(2, 2)}), 1, 0, 1, 1);
        fails(wire(new Codec.Frame[]{item(1, 5), end(1, 2), item(1, 6), end(2, 2)}), 1, 0, 1, 1);
        fails(wire(new Codec.Frame[]{item(1, 1), end(1, 3), item(2, 2), end(2, 4)}), 1, 0, 1, 1);
        fails(wire(new Codec.Frame[]{item(1, 1), end(1, 2), item(3, 2), end(3, 3)}), 1, 0, 1, 1);
        for (int code = Codec.REJECTED; code <= Codec.RETRYABLE; code++) {
            Codec.Frame error = new Codec.Frame(); error.kind = Codec.ERROR; error.requestOp = Codec.SYNC; error.reference = 7; error.code = code;
            Socket socket = new Socket(wire(new Codec.Frame[]{error}));
            HistoryReader reader = new HistoryReader(100001, 1, new Factory(socket)); reader.connect("localhost:1"); Count count = new Count();
            try { reader.replay(7, count); throw new Exception("SYNC ERROR treated as END"); }
            catch (HistoryReader.SyncException expected) { check(expected.code == code && expected.after == 7, "Correlated ERROR details lost"); }
            check(count.pages == 0 && reader.position() == 7 && !reader.complete(), "ERROR advanced position"); closed(socket);
        }
        byte[] complete = wire(new Codec.Frame[]{item(1, 1), end(1, 1)});
        int[] cuts = {0, 1, 2, 3, 10, complete.length - 19, complete.length - 18, complete.length - 1};
        for (int i = 0; i < cuts.length; i++) {
            byte[] bytes = new byte[cuts[i]]; System.arraycopy(complete, 0, bytes, 0, bytes.length); fails(bytes, 1, 0, 0, 0);
        }
        fails(new byte[]{0, 8}, 1, 0, 0, 0); fails(new byte[]{8, 0}, 1, 0, 0, 0);
        byte[] malformed = wire(new Codec.Frame[]{item(1, 1)}); malformed[2] = 0x7f; fails(malformed, 1, 0, 0, 0);
    }

    private static void consumerErrors() throws Exception {
        for (int mode = 0; mode < 3; mode++) {
            final int failureMode = mode;
            Socket socket = new Socket(wire(new Codec.Frame[]{item(1, 1), end(1, 2), item(2, 2), end(2, 2)}));
            final HistoryReader reader = new HistoryReader(100001, 1, new Factory(socket)); reader.connect("localhost:1");
            final int[] calls = {0};
            try {
                reader.replay(new HistoryReader.Consumer() {
                    public void page(HistoryReader.Page page) throws IOException {
                        if (++calls[0] == 2) {
                            if (failureMode == 0) throw new IOException("Consumer failed");
                            if (failureMode == 1) throw new IllegalStateException("Consumer failed");
                            reader.close();
                        }
                    }
                });
                throw new Exception("Consumer failure reported success");
            } catch (IOException expected) { checks++; }
            check(reader.position() == 1 && reader.snapshot() == 2 && !reader.complete(), "Failed consumption advanced position"); closed(socket);
        }
    }
    private static final class Gate extends InputStream {
        boolean entered, closed;
        public synchronized int read() throws IOException {
            entered = true; notifyAll();
            while (!closed) try { wait(); } catch (InterruptedException e) { throw new IOException("Interrupted"); }
            throw new IOException("Cancelled");
        }
        public synchronized void close() { closed = true; notifyAll(); }
        synchronized void entered() throws Exception {
            long until = System.currentTimeMillis() + 2000;
            while (!entered && System.currentTimeMillis() < until) wait(100);
            check(entered, "Worker did not enter read/open");
        }
    }
    private static final class Opening implements HistoryReader.Opener {
        final Gate gate = new Gate(); final Socket socket;
        Opening(Socket socket) { this.socket = socket; }
        public SocketConnection open(String address) throws IOException { try { gate.read(); } catch (IOException released) { } return socket; }
    }
    private static final class Worker extends Thread {
        final HistoryReader reader; final boolean connect; final Count count = new Count();
        boolean done; Exception failure;
        Worker(HistoryReader reader, boolean connect) { this.reader = reader; this.connect = connect; }
        public void run() {
            try { if (connect) reader.connect("localhost:1"); else reader.replay(count); }
            catch (Exception e) { failure = e; }
            finally { synchronized (this) { done = true; notifyAll(); } }
        }
        void joined() throws Exception {
            synchronized (this) {
                long until = System.currentTimeMillis() + 2000;
                while (!done && System.currentTimeMillis() < until) wait(100);
                check(done && failure instanceof IOException, "Cancellation did not finish");
            }
            join();
        }
    }
    private static void cancellationAndConfiguration() throws Exception {
        Socket socket = new Socket(new byte[0]); Gate input = new Gate(); socket.input = input;
        HistoryReader reader = new HistoryReader(100001, 1, new Factory(socket)); reader.connect("localhost:1");
        Worker worker = new Worker(reader, false); worker.start(); input.entered();
        try { reader.replay(new Count()); throw new Exception("Second outstanding SYNC allowed"); } catch (IOException expected) { checks++; }
        reader.close(); worker.joined();
        check(socket.closed && input.closed && socket.output.closed && reader.position() == 0 && worker.count.pages == 0, "Cancelled page leaked");
        socket = new Socket(new byte[0]); Opening opener = new Opening(socket); reader = new HistoryReader(100001, 1, opener);
        worker = new Worker(reader, true); worker.start(); opener.gate.entered(); reader.close(); opener.gate.close(); worker.joined(); check(socket.closed, "Late open leaked");
        for (int limit = 0; limit <= 9; limit += 9) {
            try { new HistoryReader(100001, limit); throw new Exception("Invalid limit accepted"); } catch (IOException expected) { checks++; }
        }
        try { new HistoryReader(1); throw new Exception("Invalid account accepted"); } catch (IOException expected) { checks++; }
        socket = new Socket(wire(new Codec.Frame[]{end(0, 0)})); Factory factory = new Factory(socket); reader = new HistoryReader(100001, 1, factory);
        String[] invalid = {null, "", ":1", "host", "host:0", "host:65536", "socket://host:1", "host:1;params=x"};
        for (int i = 0; i < invalid.length; i++) {
            try { reader.connect(invalid[i]); throw new Exception("Invalid address accepted"); } catch (IOException expected) { checks++; }
        }
        check(factory.opens == 0, "Invalid host reached Connector");
        reader.connect("localhost:1");
        try { reader.replay(-1, new Count()); throw new Exception("Negative cursor accepted"); } catch (IOException expected) { checks++; }
        try { reader.replay(null); throw new Exception("Null consumer accepted"); } catch (IOException expected) { checks++; }
        socket.closeFailure = true;
        try { reader.close(); throw new Exception("Cleanup error hidden"); } catch (IOException expected) { checks++; }
        check(((Input)socket.input).closed && socket.output.closed, "Close error skipped streams"); socket.closeFailure = false; reader.close();
        socket = new Socket(new byte[0]); socket.streamFailure = true; reader = new HistoryReader(100001, 1, new Factory(socket));
        try { reader.connect("localhost:1"); throw new Exception("Partial open error hidden"); } catch (IOException expected) { checks++; }
        check(socket.closed && ((Input)socket.input).closed, "Partial connection leaked");
    }
    public static void main(String[] args) throws Exception {
        validPages(); invalidPages(); consumerErrors(); cancellationAndConfiguration();
        System.out.println("HISTORY-READER-PASS: " + checks + " checks (pages, gaps, bounds, errors, consumption and cancellation)");
    }
}
