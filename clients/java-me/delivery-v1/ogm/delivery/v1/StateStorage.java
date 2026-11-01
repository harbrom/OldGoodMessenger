package ogm.delivery.v1;

import java.io.IOException;

/** Single-owner, whole-record storage seam; implementations must bound reads. */
public interface StateStorage {
    /** Null is allowed only for an explicitly created, not-yet-provisioned store. */
    byte[] read() throws IOException;
    /** Success means the complete record was persisted; errors may be uncertain. */
    void write(byte[] record) throws IOException;
    void close() throws IOException;
}
