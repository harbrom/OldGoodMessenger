package ogm;

import java.io.DataInputStream;
import java.io.DataOutputStream;
import java.io.IOException;
import javax.microedition.io.Connector;
import javax.microedition.io.SocketConnection;

/** One reader and one writer; close may be called by a cancellation thread. */
public final class Transport {
    private SocketConnection socket;
    private DataInputStream input;
    private DataOutputStream output;
    private boolean closed;

    public static boolean validID(int id) { return id == 100001 || id == 100002; }

    public void connect(String address, int id) throws IOException {
        if (!validID(id)) throw new IOException("Use test ID 100001 or 100002");
        SocketConnection opened = (SocketConnection)Connector.open("socket://" + address);
        synchronized (this) {
            if (closed) { opened.close(); throw new IOException("Cancelled"); }
            socket = opened;
        }
        // Keep blocking stream operations outside the cancellation lock.
        input = opened.openDataInputStream();
        output = opened.openDataOutputStream();
        output.writeShort(5);
        output.writeByte(1);
        output.writeInt(id);
        output.flush();
        int size = input.readUnsignedShort();
        int kind = input.readUnsignedByte();
        if (size == 2 && kind == 255) throw new IOException("Server error " + input.readUnsignedByte());
        if (size != 5 || kind != 129 || input.readInt() != id) throw new IOException("Invalid READY");
    }

    public void send(int recipient, String text) throws IOException {
        if (!validID(recipient)) throw new IOException("Unknown test recipient");
        byte[] encoded = Utf8.encode(text);
        output.writeShort(5 + encoded.length);
        output.writeByte(2);
        output.writeInt(recipient);
        output.write(encoded);
        output.flush();
    }

    public Message receive() throws IOException {
        int size = input.readUnsignedShort();
        if (size < 2 || size > 2005) throw new IOException("Invalid frame length");
        int kind = input.readUnsignedByte();
        if (kind == 255 && size == 2) return new Message(0, "Server error " + input.readUnsignedByte());
        if (kind != 130 || size < 6) throw new IOException("Invalid message frame");
        int sender = input.readInt();
        if (!validID(sender)) throw new IOException("Invalid sender");
        byte[] encoded = new byte[size - 5];
        input.readFully(encoded);
        return new Message(sender, Utf8.decode(encoded));
    }

    public void close() {
        SocketConnection current;
        synchronized (this) { closed = true; current = socket; socket = null; }
        if (current != null) try { current.close(); } catch (IOException ignored) { }
        if (input != null) try { input.close(); } catch (IOException ignored) { }
        if (output != null) try { output.close(); } catch (IOException ignored) { }
    }

    public static final class Message {
        public final int sender;
        public final String text;
        Message(int sender, String text) { this.sender = sender; this.text = text; }
    }
}
