import ogm.delivery.v1.Codec;
import java.io.BufferedReader;
import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.FileReader;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import java.util.Arrays;

/** Desktop fixture runner; never packaged into the handset MIDlet or codec. */
public final class DeliveryCodecTest {
    private static void check(boolean value, String message) throws Exception {
        if (!value) throw new Exception(message);
    }
    private static byte[] hex(String value) {
        if (value.length() % 2 != 0) throw new IllegalArgumentException("Odd hex length");
        byte[] bytes = new byte[value.length() / 2];
        for (int i = 0; i < bytes.length; i++) bytes[i] = (byte)Integer.parseInt(value.substring(i * 2, i * 2 + 2), 16);
        return bytes;
    }
    private static Codec.Frame frame(String spec) {
        String[] parts = spec.split(";", -1);
        Codec.Frame f = new Codec.Frame(); f.kind = Integer.parseInt(parts[0], 16);
        for (int i = 1; i < parts.length; i++) {
            String[] pair = parts[i].split("=", -1);
            String key = pair[0], value = pair[1];
            if (key.equals("text")) {
                char[] units = new char[value.length() / 4];
                for (int p = 0; p < units.length; p++) units[p] = (char)Integer.parseInt(value.substring(p * 4, p * 4 + 4), 16);
                f.text = new String(units); continue;
            }
            long n = Long.parseLong(value);
            if (key.equals("seq")) f.clientSeq = n;
            else if (key.equals("to")) f.recipient = (int)n;
            else if (key.equals("from")) f.sender = (int)n;
            else if (key.equals("id")) f.messageID = n;
            else if (key.equals("after")) f.after = n;
            else if (key.equals("through")) f.through = n;
            else if (key.equals("limit")) f.limit = (int)n;
            else if (key.equals("cursor")) f.cursor = n;
            else if (key.equals("writer")) f.writerID = n;
            else if (key.equals("next")) f.next = n;
            else if (key.equals("tail")) f.tail = n;
            else if (key.equals("req")) f.requestOp = (int)n;
            else if (key.equals("ref")) f.reference = n;
            else if (key.equals("code")) f.code = (int)n;
            else throw new IllegalArgumentException("Unknown fixture field: " + key);
        }
        return f;
    }
    private static void same(Codec.Frame a, Codec.Frame b) throws Exception {
        check(a.kind == b.kind && a.clientSeq == b.clientSeq && a.messageID == b.messageID
            && a.after == b.after && a.through == b.through && a.cursor == b.cursor && a.writerID == b.writerID
            && a.next == b.next && a.tail == b.tail && a.reference == b.reference && a.sender == b.sender
            && a.recipient == b.recipient && a.limit == b.limit && a.requestOp == b.requestOp && a.code == b.code
            && (a.text == null ? b.text == null : a.text.equals(b.text)), "Decoded fields differ from fixture");
    }
    private static void rejectWire(byte[] wire) throws Exception {
        try { Codec.decode(wire); throw new Exception("Invalid frame decoded"); } catch (IOException expected) { }
        try { Codec.read(new ByteArrayInputStream(wire)); throw new Exception("Invalid stream decoded"); } catch (IOException expected) { }
    }
    private static void rejectFields(String spec) throws Exception {
        try { Codec.encode(frame(spec)); throw new Exception("Invalid fields encoded"); } catch (IOException expected) { }
    }
    private static final class OneByte extends ByteArrayInputStream {
        OneByte(byte[] bytes) { super(bytes); }
        public synchronized int read(byte[] b, int offset, int length) { return super.read(b, offset, Math.min(1, length)); }
    }
    public static void main(String[] args) throws Exception {
        int valid = 0, invalid = 0, textInvalid = 0, streams = 0;
        BufferedReader input = new BufferedReader(new FileReader(args[0]));
        try {
            String line;
            while ((line = input.readLine()) != null) {
                if (line.startsWith("#") || line.length() == 0) continue;
                String[] p = line.split("\\|", -1);
                try {
                    check(p.length == 4, "Bad fixture row");
                    byte[] wire = p[3].equals("-") ? null : hex(p[3]);
                    if (p[1].equals("valid")) {
                        valid++;
                        Codec.Frame want = frame(p[2]);
                        same(Codec.decode(wire), want);
                        check(Arrays.equals(wire, Codec.encode(want)), "Encoding differs from authoritative wire");
                        same(Codec.read(new OneByte(wire)), want);
                        ByteArrayOutputStream output = new ByteArrayOutputStream();
                        Codec.write(output, want);
                        check(Arrays.equals(wire, output.toByteArray()), "Stream encoding differs");
                        for (int n = 0; n < wire.length; n++) rejectWire(Arrays.copyOf(wire, n));
                    } else if (p[1].equals("invalid")) {
                        invalid++; rejectWire(wire);
                        if (!p[2].equals("-")) rejectFields(p[2]);
                    } else if (p[1].equals("encode-invalid")) {
                        textInvalid++; rejectFields(p[2]);
                    } else if (p[1].equals("stream")) {
                        streams++;
                        try { Codec.decode(wire); throw new Exception("Concatenation decoded as one frame"); } catch (IOException expected) { }
                        ByteArrayInputStream stream = new OneByte(wire);
                        String[] specs = p[2].split("/");
                        for (int n = 0; n < specs.length; n++) same(Codec.read(stream), frame(specs[n]));
                        check(stream.available() == 0, "Trailing stream bytes");
                    } else throw new Exception("Unknown fixture mode");
                } catch (Exception failure) { throw new Exception("Fixture " + p[0] + ": " + failure, failure); }
            }
        } finally { input.close(); }
        check(valid == 26 && invalid == 61 && textInvalid == 4 && streams == 2, "Fixture coverage changed");
        int[] lengths = {0, 8, 2042, 65535};
        for (int i = 0; i < lengths.length; i++) {
            ByteArrayInputStream stream = new ByteArrayInputStream(new byte[] {(byte)(lengths[i] >> 8), (byte)lengths[i], 1, 2, 3});
            try { Codec.read(stream); throw new Exception("Invalid prefix accepted"); } catch (IOException expected) { }
            check(stream.available() == 3, "Body read for invalid prefix");
        }
        Codec.Frame changed = new Codec.Frame(); changed.kind = Codec.CHANGED;
        final byte[] good = Codec.encode(changed);
        InputStream zeroOnce = new ByteArrayInputStream(good) {
            boolean zero = true;
            public synchronized int read(byte[] b, int off, int len) {
                if (zero) { zero = false; return 0; }
                return super.read(b, off, len);
            }
        };
        same(Codec.read(zeroOnce), changed);
        try { Codec.read(new InputStream() { public int read() throws IOException { throw new IOException("Read failure"); } });
            throw new Exception("Read failure lost"); } catch (IOException expected) { }
        try { Codec.write(new OutputStream() { public void write(int b) throws IOException { throw new IOException("Write failure"); } }, changed);
            throw new Exception("Write failure lost"); } catch (IOException expected) { }
        try { Codec.encode(null); throw new Exception("Null frame encoded"); } catch (IOException expected) { }
        System.out.println("PASS: 93 shared fixtures (26 valid, 61 invalid, 4 surrogate encode failures, 2 streams); split/truncated frames, bounded reads and I/O failures");
    }
}
