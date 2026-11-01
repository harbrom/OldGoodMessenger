package ogm.delivery.v1;

import java.io.EOFException;
import java.io.IOException;
import java.io.InputStream;
import java.io.OutputStream;
import ogm.Utf8;

/** Stateless Delivery v1 syntax only; not used by the First Contact MIDlet. */
public final class Codec {
    public static final int SEND = 0x10, SYNC = 0x11, ACK = 0x90, ITEM = 0x91;
    public static final int END = 0x92, CHANGED = 0x93, ERROR = 0xff;
    public static final int REJECTED = 0x01, CONFLICT = 0x02, CURSOR_INVALID = 0x03, RETRYABLE = 0x04;
    public static final int MAX_BODY = 2041;

    private Codec() { }

    /** Only fields in the selected operation are encoded; unused fields are ignored. */
    public static final class Frame {
        public int kind, sender, recipient, limit, requestOp, code;
        public long clientSeq, messageID, after, through, cursor, writerID, next, tail, reference;
        public String text;
    }

    private static IOException invalid() { return new IOException("Invalid Delivery Protocol v1 frame"); }
    private static boolean id(int value) { return value >= 100000 && value <= 999999999; }

    // Text size/validity is checked with the bounded, strict standard UTF-8 helper.
    private static int size(Frame f, int textSize) throws IOException {
        switch (f.kind) {
        case SEND:
            if (f.clientSeq > 0 && id(f.recipient) && textSize > 0) return 13 + textSize;
            break;
        case ACK:
            if (f.clientSeq > 0 && f.messageID > 0) return 17;
            break;
        case SYNC:
            if (f.after >= 0 && f.through >= 0 && (f.through == 0 || f.after <= f.through) && f.limit >= 1 && f.limit <= 8) return 18;
            break;
        case ITEM:
            if (f.cursor > 0 && f.messageID > 0 && id(f.sender) && id(f.recipient) && f.writerID > 0 && f.clientSeq > 0 && textSize > 0) return 41 + textSize;
            break;
        case END:
            if (f.next >= 0 && f.through >= f.next && (f.next > 0 || f.through == 0)) return 17;
            break;
        case CHANGED:
            if (f.tail >= 0) return 9;
            break;
        case ERROR:
            if (f.code >= REJECTED && f.code <= RETRYABLE && ((f.requestOp == SEND && f.reference > 0) || (f.requestOp == SYNC && f.reference >= 0))) return 11;
            break;
        default: break;
        }
        throw invalid();
    }

    private static int putLong(byte[] b, int p, long value) {
        for (int i = 7; i >= 0; i--) b[p++] = (byte)(value >>> (8 * i));
        return p;
    }
    private static int putInt(byte[] b, int p, int value) {
        for (int i = 3; i >= 0; i--) b[p++] = (byte)(value >>> (8 * i));
        return p;
    }
    private static long getLong(byte[] b, int p) {
        long value = 0;
        for (int i = 0; i < 8; i++) value = (value << 8) | (b[p++] & 255);
        return value;
    }
    private static int getInt(byte[] b, int p) {
        int value = 0;
        for (int i = 0; i < 4; i++) value = (value << 8) | (b[p++] & 255);
        return value;
    }

    /** Encode one length-prefixed frame, never more than MAX_BODY + 2 bytes. */
    public static byte[] encode(Frame f) throws IOException {
        if (f == null) throw invalid();
        byte[] text = null;
        if (f.kind == SEND || f.kind == ITEM) {
            if (f.text == null) throw invalid();
            text = Utf8.encode(f.text);
        }
        int n = size(f, text == null ? 0 : text.length);
        byte[] b = new byte[n + 2];
        b[0] = (byte)(n >>> 8); b[1] = (byte)n; b[2] = (byte)f.kind;
        int p = 3;
        switch (f.kind) {
        case SEND: p = putLong(b, p, f.clientSeq); p = putInt(b, p, f.recipient); break;
        case ACK: p = putLong(b, p, f.clientSeq); p = putLong(b, p, f.messageID); break;
        case SYNC: p = putLong(b, p, f.after); p = putLong(b, p, f.through); b[p] = (byte)f.limit; break;
        case ITEM:
            p = putLong(b, p, f.cursor); p = putLong(b, p, f.messageID);
            p = putInt(b, p, f.sender); p = putInt(b, p, f.recipient);
            p = putLong(b, p, f.writerID); p = putLong(b, p, f.clientSeq); break;
        case END: p = putLong(b, p, f.next); p = putLong(b, p, f.through); break;
        case CHANGED: p = putLong(b, p, f.tail); break;
        case ERROR: b[p++] = (byte)f.requestOp; p = putLong(b, p, f.reference); b[p] = (byte)f.code; break;
        default: throw invalid();
        }
        if (text != null) System.arraycopy(text, 0, b, p, text.length);
        return b;
    }

    /** Require exactly one complete frame; use read for concatenated streams. */
    public static Frame decode(byte[] b) throws IOException {
        if (b == null || b.length < 2) throw invalid();
        int n = ((b[0] & 255) << 8) | (b[1] & 255);
        if (n < 9 || n > MAX_BODY || b.length != n + 2) throw invalid();
        Frame f = new Frame(); f.kind = b[2] & 255;
        switch (f.kind) {
        case SEND: if (n < 14 || n > 2013) throw invalid(); break;
        case ACK: case END: if (n != 17) throw invalid(); break;
        case SYNC: if (n != 18) throw invalid(); break;
        case ITEM: if (n < 42 || n > MAX_BODY) throw invalid(); break;
        case CHANGED: if (n != 9) throw invalid(); break;
        case ERROR: if (n != 11) throw invalid(); break;
        default: throw invalid();
        }
        int p = 3;
        switch (f.kind) {
        case SEND: f.clientSeq = getLong(b, p); p += 8; f.recipient = getInt(b, p); p += 4; break;
        case ACK: f.clientSeq = getLong(b, p); p += 8; f.messageID = getLong(b, p); break;
        case SYNC: f.after = getLong(b, p); p += 8; f.through = getLong(b, p); p += 8; f.limit = b[p] & 255; break;
        case ITEM:
            f.cursor = getLong(b, p); p += 8; f.messageID = getLong(b, p); p += 8;
            f.sender = getInt(b, p); p += 4; f.recipient = getInt(b, p); p += 4;
            f.writerID = getLong(b, p); p += 8; f.clientSeq = getLong(b, p); p += 8; break;
        case END: f.next = getLong(b, p); p += 8; f.through = getLong(b, p); break;
        case CHANGED: f.tail = getLong(b, p); break;
        case ERROR: f.requestOp = b[p++] & 255; f.reference = getLong(b, p); p += 8; f.code = b[p] & 255; break;
        default: throw invalid();
        }
        int textSize = 0;
        if (f.kind == SEND || f.kind == ITEM) {
            textSize = b.length - p;
            byte[] text = new byte[textSize];
            System.arraycopy(b, p, text, 0, textSize);
            f.text = Utf8.decode(text);
        }
        size(f, textSize);
        return f;
    }

    /** Validate length before allocating; never read beyond this frame. */
    public static Frame read(InputStream input) throws IOException {
        int first = input.read(), second;
        if (first < 0 || (second = input.read()) < 0) throw new EOFException();
        int n = (first << 8) | second;
        if (n < 9 || n > MAX_BODY) throw invalid();
        byte[] b = new byte[n + 2]; b[0] = (byte)first; b[1] = (byte)second;
        int p = 2;
        while (p < b.length) {
            int count = input.read(b, p, b.length - p);
            if (count < 0) throw new EOFException();
            if (count == 0) { int value = input.read(); if (value < 0) throw new EOFException(); b[p++] = (byte)value; }
            else p += count;
        }
        return decode(b);
    }

    /** Does not flush or close the caller's stream. */
    public static void write(OutputStream output, Frame f) throws IOException { output.write(encode(f)); }
}
