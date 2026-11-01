package ogm.delivery.v1;

import java.io.IOException;
import javax.microedition.rms.RecordStore;
import javax.microedition.rms.RecordStoreException;
import javax.microedition.rms.RecordStoreNotFoundException;

/** Private MIDP 2.0 RMS adapter. Exactly one adapter may be open in this VM. */
public final class RmsStateStorage implements StateStorage {
    private static boolean active;
    private RecordStore store;
    private boolean fresh, failed;

    private RmsStateStorage(RecordStore store, boolean fresh) { this.store = store; this.fresh = fresh; }

    /** Never creates a missing store. A missing record is NOT fresh state. */
    public static synchronized RmsStateStorage open(String name) throws IOException {
        return acquire(name, false);
    }

    /** Explicit trusted provisioning only. Refuses ANY existing store, even empty. */
    public static synchronized RmsStateStorage create(String name) throws IOException {
        return acquire(name, true);
    }

    private static RmsStateStorage acquire(String name, boolean create) throws IOException {
        if (active) throw new IOException("An RMS writer adapter is already open; close it first");
        if (name == null || name.length() == 0 || name.length() > 32) throw new IOException("Invalid RMS store name");
        RecordStore opened = null;
        boolean success = false;
        try {
            if (create) {
                try {
                    opened = RecordStore.openRecordStore(name, false);
                } catch (RecordStoreNotFoundException missing) {
                    // Explicit creation, never an implicit fallback from open().
                }
                if (opened != null) throw new IOException("RMS store already exists; provisioning cannot reset it");
                opened = RecordStore.openRecordStore(name, true, RecordStore.AUTHMODE_PRIVATE, false);
                if (opened.getNumRecords() != 0 || opened.getNextRecordID() != 1 || opened.getVersion() != 0)
                    throw new IOException("RMS store is not new");
            } else {
                opened = RecordStore.openRecordStore(name, false);
            }
            RmsStateStorage result = new RmsStateStorage(opened, create);
            active = true; success = true;
            return result;
        } catch (RecordStoreException failure) {
            throw rms(failure);
        } finally {
            if (!success && opened != null) {
                try { opened.closeRecordStore(); }
                catch (RecordStoreException closeFailure) {
                    active = true; // An unclosed handle must block another owner until VM restart.
                    throw rms(closeFailure);
                }
            }
        }
    }

    private void usable() throws IOException {
        if (store == null || failed) throw new IOException("RMS unavailable; close and reopen before reuse");
    }

    private void shape() throws RecordStoreException, IOException {
        if (store.getNumRecords() != 1 || store.getNextRecordID() != 2)
            throw new IOException("Missing or unexpected RMS records; no automatic repair");
    }

    public synchronized byte[] read() throws IOException {
        usable();
        try {
            if (fresh) return null;
            shape();
            int size = store.getRecordSize(1);
            if (size < WriterState.MIN_RECORD || size > WriterState.MAX_RECORD)
                throw new IOException("Invalid RMS record size");
            byte[] bytes = new byte[size];
            if (store.getRecord(1, bytes, 0) != size) throw new IOException("RMS record changed while reading");
            return bytes;
        } catch (RecordStoreException failure) {
            failed = true; throw rms(failure);
        } catch (IOException failure) {
            failed = true; throw failure;
        }
    }

    public synchronized void write(byte[] record) throws IOException {
        usable();
        if (record == null || record.length < WriterState.MIN_RECORD || record.length > WriterState.MAX_RECORD)
            throw new IOException("Invalid writer record size");
        try {
            if (fresh) {
                if (store.getNumRecords() != 0 || store.getNextRecordID() != 1 || store.getVersion() != 0)
                    throw new IOException("RMS store changed before provisioning");
                if (store.addRecord(record, 0, record.length) != 1) throw new IOException("Unexpected RMS record ID");
                fresh = false;
            } else {
                shape();
                store.setRecord(1, record, 0, record.length);
            }
        } catch (RecordStoreException failure) {
            failed = true; throw rms(failure);
        } catch (IOException failure) {
            failed = true; throw failure;
        }
    }

    public synchronized void close() throws IOException {
        if (store == null) return;
        failed = true;
        try {
            store.closeRecordStore();
            store = null;
            synchronized (RmsStateStorage.class) { active = false; }
        } catch (RecordStoreException failure) { throw rms(failure); }
    }

    private static IOException rms(RecordStoreException failure) {
        return new IOException("RMS operation failed: " + failure.toString());
    }
}
