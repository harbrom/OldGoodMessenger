package ogm;

import java.io.IOException;

/** Strict standard UTF-8, independent of handset codecs and modified UTF-8. */
public final class Utf8 {
    private Utf8() { }

    public static byte[] encode(String text) throws IOException {
        if (text.length() == 0 || text.length() > 1000) throw invalid();
        byte[] buffer = new byte[2000];
        int count = 0, size = 0;
        for (int i = 0; i < text.length(); i++) {
            int c = text.charAt(i);
            if (c >= 0xd800 && c <= 0xdbff) {
                if (++i == text.length()) throw invalid();
                int low = text.charAt(i);
                if (low < 0xdc00 || low > 0xdfff) throw invalid();
                c = 0x10000 + ((c - 0xd800) << 10) + low - 0xdc00;
            } else if (c >= 0xdc00 && c <= 0xdfff) throw invalid();
            if (++count > 500) throw invalid();
            if (c < 0x80) buffer[size++] = (byte)c;
            else if (c < 0x800) {
                buffer[size++] = (byte)(0xc0 | (c >> 6));
                buffer[size++] = (byte)(0x80 | (c & 63));
            } else if (c < 0x10000) {
                buffer[size++] = (byte)(0xe0 | (c >> 12));
                buffer[size++] = (byte)(0x80 | ((c >> 6) & 63));
                buffer[size++] = (byte)(0x80 | (c & 63));
            } else {
                buffer[size++] = (byte)(0xf0 | (c >> 18));
                buffer[size++] = (byte)(0x80 | ((c >> 12) & 63));
                buffer[size++] = (byte)(0x80 | ((c >> 6) & 63));
                buffer[size++] = (byte)(0x80 | (c & 63));
            }
        }
        byte[] result = new byte[size];
        System.arraycopy(buffer, 0, result, 0, size);
        return result;
    }

    public static String decode(byte[] bytes) throws IOException {
        if (bytes.length == 0 || bytes.length > 2000) throw invalid();
        StringBuffer result = new StringBuffer();
        int count = 0;
        for (int i = 0; i < bytes.length;) {
            int c = bytes[i++] & 255;
            int more = 0, minimum = 0;
            if (c < 0x80) { }
            else if (c >= 0xc2 && c <= 0xdf) { c &= 31; more = 1; minimum = 0x80; }
            else if (c >= 0xe0 && c <= 0xef) { c &= 15; more = 2; minimum = 0x800; }
            else if (c >= 0xf0 && c <= 0xf4) { c &= 7; more = 3; minimum = 0x10000; }
            else throw invalid();
            if (i + more > bytes.length) throw invalid();
            for (int j = 0; j < more; j++) {
                int next = bytes[i++] & 255;
                if ((next & 0xc0) != 0x80) throw invalid();
                c = (c << 6) | (next & 63);
            }
            if (c < minimum || c > 0x10ffff || (c >= 0xd800 && c <= 0xdfff)) throw invalid();
            if (++count > 500) throw invalid();
            if (c < 0x10000) result.append((char)c);
            else {
                c -= 0x10000;
                result.append((char)(0xd800 | (c >> 10)));
                result.append((char)(0xdc00 | (c & 1023)));
            }
        }
        return result.toString();
    }

    private static IOException invalid() {
        return new IOException("Text must be valid Unicode with 1 to 500 code points");
    }
}
