package ogm.delivery.v1;

import java.io.IOException;
import javax.microedition.rms.RecordStore;
import javax.microedition.rms.RecordStoreException;
import javax.microedition.rms.RecordStoreNotFoundException;

/** Separate private RMS snapshot store. One history adapter owner per VM. */
public final class RmsHistoryStorage implements StateStorage {
    private static boolean active;
    private RecordStore store;
    private boolean fresh, failed;
    private RmsHistoryStorage(RecordStore store, boolean fresh) { this.store = store; this.fresh = fresh; }
    public static synchronized RmsHistoryStorage create(String name) throws IOException { return acquire(name, true); }
    public static synchronized RmsHistoryStorage open(String name) throws IOException { return acquire(name, false); }
    private static RmsHistoryStorage acquire(String name, boolean create) throws IOException {
        if (active) throw new IOException("History RMS adapter already open");
        if (name == null || name.length() == 0 || name.length() > 32) throw new IOException("Invalid history RMS name");
        RecordStore opened = null; boolean success = false;
        try {
            if (create) {
                try { opened = RecordStore.openRecordStore(name, false); }
                catch (RecordStoreNotFoundException missing) { }
                if (opened != null) throw new IOException("History RMS exists; cannot reset");
                opened = RecordStore.openRecordStore(name, true, RecordStore.AUTHMODE_PRIVATE, false);
                if (opened.getNumRecords() != 0 || opened.getNextRecordID() != 1 || opened.getVersion() != 0)
                    throw new IOException("History RMS not new");
            } else opened = RecordStore.openRecordStore(name, false);
            active = true; success = true; return new RmsHistoryStorage(opened, create);
        } catch (RecordStoreException failure) { throw rms(failure); }
        finally {
            if (!success && opened != null) try { opened.closeRecordStore(); }
            catch (RecordStoreException failure) { active = true; throw rms(failure); }
        }
    }
    private void usable() throws IOException { if (store == null || failed) throw new IOException("History RMS unavailable; close/reopen required"); }
    private void shape() throws IOException, RecordStoreException {
        if (store.getNumRecords() != 1 || store.getNextRecordID() != 2) throw new IOException("Missing/unexpected history RMS records");
    }
    public synchronized byte[] read() throws IOException {
        usable();
        try {
            if (fresh) return null;
            shape(); int size = store.getRecordSize(1);
            if (size < HistoryStore.MIN_RECORD || size > HistoryStore.MAX_RECORD) throw new IOException("Invalid history RMS size");
            byte[] bytes = new byte[size];
            if (store.getRecord(1, bytes, 0) != size) throw new IOException("History RMS changed during read");
            return bytes;
        } catch (RecordStoreException failure) { failed = true; throw rms(failure); }
        catch (IOException failure) { failed = true; throw failure; }
    }
    public synchronized void write(byte[] bytes) throws IOException {
        usable();
        if (bytes == null || bytes.length < HistoryStore.MIN_RECORD || bytes.length > HistoryStore.MAX_RECORD) throw new IOException("Invalid history snapshot size");
        try {
            if (fresh) {
                if (store.getNumRecords() != 0 || store.getNextRecordID() != 1 || store.getVersion() != 0) throw new IOException("History RMS changed before provisioning");
                if (store.addRecord(bytes, 0, bytes.length) != 1) throw new IOException("Unexpected history RMS record ID");
                fresh = false;
            } else { shape(); store.setRecord(1, bytes, 0, bytes.length); }
        } catch (RecordStoreException failure) { failed = true; throw rms(failure); }
        catch (IOException failure) { failed = true; throw failure; }
    }
    public synchronized void close() throws IOException {
        if (store == null) return;
        failed = true;
        try { store.closeRecordStore(); store = null; synchronized (RmsHistoryStorage.class) { active = false; } }
        catch (RecordStoreException failure) { throw rms(failure); }
    }
    private static IOException rms(RecordStoreException failure) { return new IOException("History RMS failed: " + failure); }
}
