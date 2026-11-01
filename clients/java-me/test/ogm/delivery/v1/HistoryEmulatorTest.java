package ogm.delivery.v1;

import java.io.IOException;
import javax.microedition.midlet.MIDlet;

/** Independent fixture oracle; every phase is launched in a separate emulator JVM. */
public final class HistoryEmulatorTest extends MIDlet implements Runnable {
    private static final String TEXT = "ASCII \u041f\u0440\u0438\u0432\u0456\u0442 Za\u017c\u00f3\u0142\u0107 e\u0301 \ud83d\ude42\u0000\n";
    protected void startApp() { new Thread(this).start(); }
    protected void pauseApp() { }
    protected void destroyApp(boolean unconditional) { }
    private static void check(boolean value, String message) throws IOException { if (!value) throw new IOException(message); }

    private static int sender(int id, boolean simple) {
        if (simple) return 100001;
        if (id == 19 || id % 6 == 0 || id % 2 == 0 && id % 6 != 4) return 100002;
        return id % 6 == 4 ? 100003 : 100001;
    }
    private static int recipient(int id, boolean simple) {
        if (simple) return 100002;
        if (id == 19) return 100001;
        if (id % 6 == 4) return 100003;
        if (id % 6 == 3) return 100001;
        if (id % 6 == 0) return 100002;
        return id % 2 == 1 ? 100002 : 100001;
    }
    private static int total(int account, int maximum, boolean simple) {
        int count = 0;
        for (int id = 1; id <= maximum; id++) if (sender(id, simple) == account || recipient(id, simple) == account) count++;
        return count;
    }

    private static final class Verify implements HistoryReader.Consumer {
        final HistoryReader reader;
        final int account, limit, maximum, expected;
        final boolean simple, gaps;
        final long h;
        int seen, pages, callbacks, nextID = 1;
        long lastNext;
        Verify(HistoryReader reader, int account, int limit, int maximum, boolean simple, boolean gaps) {
            this.reader = reader; this.account = account; this.limit = limit; this.maximum = maximum;
            this.simple = simple; this.gaps = gaps; expected = total(account, maximum, simple);
            h = gaps ? expected * 2 + 1 : expected;
        }
        public void page(HistoryReader.Page page) throws IOException {
            callbacks++; // Count visibility even if the independent oracle rejects it.
            check(reader.position() == lastNext && !reader.complete(), "Progress advanced before consumer returned");
            check(page.after == lastNext && page.through == h && page.size() <= limit, "Wrong page boundary/snapshot");
            for (int i = 0; i < page.size(); i++) {
                while (nextID <= maximum && sender(nextID, simple) != account && recipient(nextID, simple) != account) nextID++;
                check(nextID <= maximum, "Extra or cross-account history item");
                HistoryReader.Item item = page.item(i);
                int from = sender(nextID, simple), to = recipient(nextID, simple);
                seen++;
                check(item.cursor == (gaps ? seen * 2 : seen) && item.messageID == nextID && item.clientSeq == nextID,
                    "Exact cursor/message/sequence order mismatch");
                check(item.sender == from && item.recipient == to && item.writerID == (from - 100000) * 11,
                    "Participant/writer mismatch");
                check(item.text.equals(nextID == 19 ? "late" : TEXT + " #" + nextID), "Exact Unicode mismatch");
                nextID++;
            }
            long next = seen == expected ? h : (gaps ? seen * 2 : seen);
            check(page.next == next, "Wrong END.next"); lastNext = next; pages++;
        }
        void finished() throws IOException {
            check(seen == expected && callbacks == pages && pages == (expected == 0 ? 1 : (expected + limit - 1) / limit), "Missing messages/pages");
            check(reader.position() == h && reader.snapshot() == h && reader.complete(), "Incomplete replay");
        }
    }

    public void run() {
        String phase = getAppProperty("OGM-History-Phase"), address = getAppProperty("OGM-Server");
        int account = Integer.parseInt(getAppProperty("OGM-Test-Account"));
        int limit = Integer.parseInt(getAppProperty("OGM-Test-Limit"));
        int maximum = Integer.parseInt(getAppProperty("OGM-Test-Maximum"));
        boolean simple = "true".equals(getAppProperty("OGM-Test-Simple")), gaps = "gaps".equals(phase);
        boolean fault = "true".equals(getAppProperty("OGM-Test-Fault"));
        HistoryReader reader = null;
        boolean success = false;
        try {
            reader = "default".equals(phase) ? new HistoryReader(account) : new HistoryReader(account, limit);
            check(reader.position() == 0 && reader.snapshot() == 0 && !reader.complete(), "Unexpected durable client state");
            reader.connect(address);
            if ("cancel".equals(phase)) {
                final HistoryReader active = reader;
                try {
                    reader.replay(new HistoryReader.Consumer() {
                        public void page(HistoryReader.Page page) throws IOException {
                            final IOException[] failure = {null};
                            Thread cancel = new Thread(new Runnable() {
                                public void run() { try { active.close(); } catch (IOException e) { failure[0] = e; } }
                            });
                            cancel.start();
                            try { cancel.join(); } catch (InterruptedException e) { throw new IOException("Cancel interrupted"); }
                            if (failure[0] != null) throw failure[0];
                        }
                    });
                    throw new IOException("CANCEL-NOT-OBSERVED");
                } catch (IOException expected) {
                    check(!"CANCEL-NOT-OBSERVED".equals(expected.getMessage()) && reader.position() == 0 && !reader.complete(), "Cancellation published progress");
                }
                reader.connect(address);
            }
            if (fault) {
                Verify partial = new Verify(reader, account, limit, maximum, simple, gaps);
                long start = "cursor-error".equals(phase) ? 9999 : 0;
                try { reader.replay(start, partial); throw new IOException("MISSING-FAULT"); }
                catch (IOException expected) {
                    check(!"MISSING-FAULT".equals(expected.getMessage()), "Fault was silently accepted");
                    if ("cursor-error".equals(phase)) {
                        check(expected instanceof HistoryReader.SyncException && ((HistoryReader.SyncException)expected).code == Codec.CURSOR_INVALID,
                            "Correlated SYNC error not surfaced");
                    }
                    long position = "snapshot-change".equals(phase) ? 2 : start;
                    check(reader.position() == position && !reader.complete(), "Failed page advanced position");
                    check(partial.seen == ("snapshot-change".equals(phase) ? 2 : 0), "Partial page exposed to caller");
                    check(partial.callbacks == ("snapshot-change".equals(phase) ? 1 : 0), "Invalid page reached consumer validation");
                }
                try { reader.replay(partial); throw new IOException("DIRTY-STREAM-USED"); }
                catch (IOException expected) { check(!"DIRTY-STREAM-USED".equals(expected.getMessage()), "Reconnect not required"); }
                reader.connect(address);
            }
            Verify verified = new Verify(reader, account, limit, maximum, simple, gaps);
            reader.replay(verified); verified.finished();
            if (gaps) {
                check(reader.replay(verified.expected * 2, new HistoryReader.Consumer() {
                    public void page(HistoryReader.Page page) throws IOException {
                        check(page.size() == 0 && page.next == 25 && page.through == 25, "Empty terminal gap mismatch");
                    }
                }) == 25, "Terminal gap replay failed");
            }
            if ("snapshot".equals(phase)) {
                Verify fresh = new Verify(reader, account, limit, 19, false, false);
                reader.replay(0, fresh); fresh.finished();
            }
            success = true;
        } catch (Exception failure) {
            System.out.println("HISTORY-EMULATOR-FAIL: " + phase + " " + failure);
        } finally {
            try { if (reader != null) reader.close(); }
            catch (IOException failure) { success = false; System.out.println("HISTORY-EMULATOR-FAIL: cleanup " + failure); }
            if (success) System.out.println("HISTORY-EMULATOR-PASS: " + phase);
            notifyDestroyed();
        }
    }
}
