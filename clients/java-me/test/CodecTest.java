import ogm.Utf8;
import java.io.IOException;
import java.util.Arrays;

/** Desktop assertions against independent JDK standard UTF-8 reference bytes. */
public final class CodecTest {
    public static void main(String[] args) throws Exception {
        String[] fixtures = {"ASCII", "\u041f\u0440\u0438\u0432\u0456\u0442", "Za\u017c\u00f3\u0142\u0107",
            "e\u0301", "\ud83d\ude42", "\u0000\n", repeat("a", 500), repeat("\ud83d\ude42", 500)};
        for (int i = 0; i < fixtures.length; i++) {
            compare(fixtures[i]);
        }
        // Cover every valid scalar against the independent JDK implementation,
        // in bounded chunks compatible with the experimental message limit.
        StringBuilder chunk = new StringBuilder();
        int count = 0;
        for (int scalar = 0; scalar <= 0x10ffff; scalar++) {
            if (scalar >= 0xd800 && scalar <= 0xdfff) continue;
            chunk.appendCodePoint(scalar);
            if (++count == 500) { compare(chunk.toString()); chunk.setLength(0); count = 0; }
        }
        if (count != 0) compare(chunk.toString());
        String[] invalidText = {"", repeat("a", 501), repeat("\ud83d\ude42", 501), "\ud800", "\udc00", "\ud800x"};
        for (int i = 0; i < invalidText.length; i++) {
            try { Utf8.encode(invalidText[i]); throw new Exception("Invalid text accepted"); }
            catch (IOException expected) { }
        }
        byte[][] invalid = {{}, {(byte)0xc0, (byte)0x80}, {(byte)0xed, (byte)0xa0, (byte)0x80},
            {(byte)0xf4, (byte)0x90, (byte)0x80, (byte)0x80}, {(byte)0x80}, {(byte)0xe2, 0x41},
            {(byte)0xf0, (byte)0x80, (byte)0x80, (byte)0x80}, new byte[501], new byte[2001]};
        for (int i = 0; i < invalid.length; i++) {
            try { Utf8.decode(invalid[i]); throw new Exception("Invalid bytes accepted"); }
            catch (IOException expected) { }
        }
        System.out.println("PASS: Java UTF-8 fixtures, every valid scalar, bounds, malformed bytes and surrogates");
    }
    private static void compare(String text) throws Exception {
        byte[] reference = text.getBytes("UTF-8");
        if (!Arrays.equals(reference, Utf8.encode(text))) throw new Exception("Encoding mismatch");
        if (!text.equals(Utf8.decode(reference))) throw new Exception("Decoding mismatch");
    }
    private static String repeat(String value, int count) {
        StringBuffer result = new StringBuffer();
        for (int i = 0; i < count; i++) result.append(value);
        return result.toString();
    }
}
