package ogm.delivery.v1;

import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import javax.microedition.io.Connector;
import javax.microedition.io.SocketConnection;

/** Read-only, volatile replay. Run on one worker; caller supplies trusted identity. */
public final class HistoryReader {
    public static final int DEFAULT_LIMIT = 1;
    public interface Consumer { void page(Page page) throws IOException; }
    interface Opener { SocketConnection open(String address) throws IOException; }

    /** Immutable wire ITEM value; returning it cannot corrupt replay validation. */
    public static final class Item {
        public final long cursor, messageID, writerID, clientSeq;
        public final int sender, recipient;
        public final String text;
        private Item(Codec.Frame frame) {
            cursor = frame.cursor; messageID = frame.messageID; writerID = frame.writerID;
            clientSeq = frame.clientSeq; sender = frame.sender; recipient = frame.recipient; text = frame.text;
        }
    }

    /** Only constructed after complete ITEM/END validation; at most limit items. */
    public static final class Page {
        public final long after, next, through;
        private final Item[] items;
        private final int count;
        private Page(long after, Codec.Frame end, Item[] items, int count) {
            this.after = after; next = end.next; through = end.through; this.items = items; this.count = count;
        }
        public int size() { return count; }
        public Item item(int index) {
            if (index < 0 || index >= count) throw new IndexOutOfBoundsException();
            return items[index];
        }
    }

    /** A correlated server ERROR is a failed page, never successful completion. */
    public static final class SyncException extends IOException {
        public final int code;
        public final long after;
        private SyncException(Codec.Frame frame) {
            super("SYNC failed: code=" + frame.code + " after=" + frame.reference);
            code = frame.code; after = frame.reference;
        }
    }

    private final int account, limit;
    private final Opener opener;
    private Link current;
    private boolean busy, complete;
    private long position, snapshot;

    public HistoryReader(int trustedAccount) throws IOException { this(trustedAccount, DEFAULT_LIMIT); }
    public HistoryReader(int trustedAccount, int pageLimit) throws IOException {
        this(trustedAccount, pageLimit, new Opener() {
            public SocketConnection open(String address) throws IOException {
                return (SocketConnection)Connector.open("socket://" + address, Connector.READ_WRITE, true);
            }
        });
    }
    HistoryReader(int trustedAccount, int pageLimit, Opener opener) throws IOException {
        if (trustedAccount < 100000 || trustedAccount > 999999999 || pageLimit < 1 || pageLimit > 8 || opener == null)
            throw new IOException("Valid trusted account, page limit 1..8 and socket opener required");
        account = trustedAccount; limit = pageLimit; this.opener = opener;
    }

    /** Last successfully consumed page position and H; neither is persistent. */
    public synchronized long position() { return position; }
    public synchronized long snapshot() { return snapshot; }
    public synchronized boolean complete() { return complete; }

    public void connect(String address) throws IOException {
        address(address);
        Link link;
        synchronized (this) {
            if (busy) throw new IOException("Reader operation already in progress");
            if (current != null && !current.released()) throw new IOException("Close the previous connection first");
            busy = true; current = link = new Link(); complete = false;
        }
        try {
            SocketConnection socket = opener.open(address);
            link.socket(socket); link.input(socket.openInputStream()); link.output(socket.openOutputStream()); link.check();
        } catch (IOException failure) { throw cleanup(link, failure); }
        catch (RuntimeException failure) { throw cleanup(link, new IOException("History connect failed: " + failure)); }
        finally { idle(); }
    }

    /** Explicit full replay from zero. No automatic polling or persistent checkpoint. */
    public long replay(Consumer consumer) throws IOException { return replay(0, consumer); }

    /** Synchronously fetch pages from after, holding H across continuation requests.
     * Consumer runs outside locks after valid END. Its failure never advances progress. */
    public long replay(long after, Consumer consumer) throws IOException {
        if (after < 0 || consumer == null) throw new IOException("Nonnegative replay cursor and consumer required");
        Link link;
        synchronized (this) {
            if (busy) throw new IOException("Reader operation already in progress");
            if (current == null) throw new IOException("Connect before replay");
            current.check(); link = current; busy = true;
            position = after; snapshot = 0; complete = false;
        }
        long h = 0, lastID = 0;
        try {
            while (true) {
                Codec.Frame request = new Codec.Frame(); request.kind = Codec.SYNC;
                request.after = after; request.through = h; request.limit = limit;
                link.check(); Codec.write(link.output, request); link.output.flush();
                Page page = readPage(link.input, after, h, lastID);
                link.check();
                long nextID = page.size() == 0 ? lastID : page.item(page.size() - 1).messageID;
                consumer.page(page);
                synchronized (this) {
                    link.check();
                    position = page.next; snapshot = page.through; complete = page.next == page.through;
                }
                after = page.next; h = page.through; lastID = nextID;
                page = null; // Do not keep the previous page while reading the next.
                if (after == h) return h;
            }
        } catch (IOException failure) { throw cleanup(link, failure); }
        catch (RuntimeException failure) { throw cleanup(link, new IOException("History replay/consumer failed: " + failure)); }
        finally { idle(); }
    }

    private Page readPage(InputStream input, long after, long h, long lastID) throws IOException {
        Item[] items = new Item[limit];
        int count = 0;
        long lastCursor = after;
        while (true) {
            Codec.Frame frame = Codec.read(input);
            switch (frame.kind) {
            case Codec.ITEM:
                if (count >= limit || frame.cursor <= lastCursor || frame.messageID <= lastID ||
                    (frame.sender != account && frame.recipient != account) || (h != 0 && frame.cursor > h)) throw invalid();
                items[count++] = new Item(frame); lastCursor = frame.cursor; lastID = frame.messageID;
                break;
            case Codec.END:
                if (frame.through < lastCursor || frame.next < after || (h != 0 && frame.through != h) ||
                    (frame.next < frame.through && (count != limit || frame.next != lastCursor))) throw invalid();
                return new Page(after, frame, items, count);
            case Codec.ERROR:
                if (frame.requestOp != Codec.SYNC || frame.reference != after) throw invalid();
                throw new SyncException(frame);
            default: throw invalid(); // Includes ACK/CHANGED; no interleaves in this reader.
            }
        }
    }

    private static IOException invalid() { return new IOException("Invalid history ITEM/END ordering, account, bounds or response"); }
    private synchronized void idle() { busy = false; }

    /** Cancellation thread may close handles; join the worker before reconnecting. */
    public void close() throws IOException {
        Link link;
        synchronized (this) { link = current; }
        try { if (link != null) link.close(); }
        finally { synchronized (this) { complete = false; } }
    }
    private static IOException cleanup(Link link, IOException original) {
        try { link.close(); }
        catch (IOException failure) { return new IOException(original.toString() + "; cleanup: " + failure); }
        return original;
    }

    // Isolated socket ownership keeps Sender's validated durable retry path unchanged.
    private static void address(String value) throws IOException {
        if (value == null || value.length() > 259) throw new IOException("Use an explicit host:port");
        int colon = value.indexOf(':');
        if (colon <= 0 || colon != value.lastIndexOf(':') || colon == value.length() - 1) throw new IOException("Use DNS/IPv4 host:port");
        for (int i = 0; i < colon; i++) {
            char c = value.charAt(i);
            if (!(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '.' && c != '-')
                throw new IOException("Invalid socket host");
        }
        int port = 0;
        if (value.length() - colon - 1 > 5) throw new IOException("Socket port must be 1..65535");
        for (int i = colon + 1; i < value.length(); i++) {
            char c = value.charAt(i); if (c < '0' || c > '9') throw new IOException("Invalid socket port");
            port = port * 10 + c - '0';
        }
        if (port < 1 || port > 65535) throw new IOException("Socket port must be 1..65535");
    }

    private static final class Link {
        private SocketConnection socket;
        private InputStream input;
        private OutputStream output;
        private boolean cancelled, released;
        synchronized void socket(SocketConnection value) throws IOException { socket = value; released = false; check(); }
        synchronized void input(InputStream value) throws IOException { input = value; released = false; check(); }
        synchronized void output(OutputStream value) throws IOException { output = value; released = false; check(); }
        synchronized void check() throws IOException { if (cancelled) throw new IOException("History connection closed/cancelled; reconnect required"); }
        synchronized boolean released() { return released; }
        synchronized void close() throws IOException {
            if (released) return;
            cancelled = true;
            IOException failure = null;
            if (socket != null) try { socket.close(); } catch (IOException e) { failure = append(failure, e); }
            if (input != null) try { input.close(); } catch (IOException e) { failure = append(failure, e); }
            if (output != null) try { output.close(); } catch (IOException e) { failure = append(failure, e); }
            if (failure != null) throw failure;
            released = true;
        }
        private static IOException append(IOException first, IOException next) {
            return first == null ? next : new IOException(first.toString() + "; " + next);
        }
    }
}
