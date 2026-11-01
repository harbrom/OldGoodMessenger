package deliveryloopback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ogm.local/firstcontact/internal/acceptance"
	"ogm.local/firstcontact/internal/deliveryv1"
	"ogm.local/firstcontact/internal/historysync"
	"ogm.local/firstcontact/internal/sqlitestore"
)

const javaText = "ASCII Привіт Zażółć e\u0301 🙂\x00\n"

// Opt-in: scripts/test-java-me-sender.ps1 compiles/preverifies the fixture first.
// Ordinary Go checks need no Java installation, downloads or live emulator.
func TestJavaMESender(t *testing.T) {
	jar := os.Getenv("OGM_JAVA_ME_SENDER_JAR")
	if jar == "" {
		t.Skip("run scripts/test-java-me-sender.ps1 for file-RMS/TCP emulator integration")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	java := filepath.Join(root, ".tools", "java-me", "jdk", "jdk8u504-b01", "bin", "java.exe")
	emulator := filepath.Join(root, ".tools", "java-me", "microemulator-2.0.4.jar")
	for _, path := range []string{jar, java, emulator} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	work := t.TempDir()
	home := filepath.Join(work, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	jad := filepath.Join(work, "sender.jad")
	info, err := os.Stat(jar)
	if err != nil {
		t.Fatal(err)
	}
	// Forward slashes preserve a local Windows file URI; no HTTP download involved.
	jarURL := "file:///" + filepath.ToSlash(jar)
	metadata := "MIDlet-Name: OGM Sender Test\nMIDlet-Version: 0.0.1\nMIDlet-Vendor: OGM\n" +
		"MIDlet-1: OGM Sender Test,,ogm.delivery.v1.SenderEmulatorTest\nMicroEdition-Configuration: CLDC-1.1\n" +
		"MicroEdition-Profile: MIDP-2.0\nMIDlet-Permissions: javax.microedition.io.Connector.socket\n" +
		fmt.Sprintf("MIDlet-Jar-URL: %s\nMIDlet-Jar-Size: %d\n", jarURL, info.Size())
	dbPath := filepath.Join(work, "messages.sqlite")
	for _, phase := range []string{"lost", "retry", "verified", "rejected", "conflict", "retryable", "malformed", "unexpected", "mismatch"} {
		t.Run(phase, func(t *testing.T) {
			writer := int64(7)
			switch phase {
			case "rejected":
				writer = 11
			case "conflict":
				writer = 12
			case "retryable":
				writer = 13
			case "malformed":
				writer = 14
			case "unexpected":
				writer = 15
			case "mismatch":
				writer = 16
			}
			path := dbPath
			if writer != 7 {
				path = filepath.Join(work, phase+".sqlite")
			}
			trusted := acceptance.TrustedContext{AccountID: 100001, WriterID: writer}
			s, err := sqlitestore.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			var store acceptance.Store = s
			if phase == "retryable" {
				store = &javaFailOnce{base: s}
			}
			a, err := acceptance.New(store, func(_ acceptance.TrustedContext, req acceptance.Request) bool {
				return (req.Recipient == 100001 || req.Recipient == 100002) && !(phase == "rejected" && req.ClientSeq == 1)
			})
			if err != nil {
				t.Fatal(err)
			}
			h, err := historysync.New(s)
			if err != nil {
				t.Fatal(err)
			}
			if phase == "conflict" {
				if _, err := a.Accept(trusted, acceptance.Request{ClientSeq: 1, Recipient: 100002, Text: "original"}); err != nil {
					t.Fatal(err)
				}
			}
			// At most two connections are expected in any phase. No remote identity
			// selection; every accepted connection gets the same Go fixture context.
			ctx, cancel := context.WithCancel(context.Background())
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			var once sync.Once
			var handlers sync.WaitGroup
			finished := make(chan struct{})
			results := make(chan error, 2)
			go func() {
				defer close(finished)
				for i := 0; i < 2; i++ {
					conn, err := listener.Accept()
					if err != nil {
						return
					}
					handlers.Add(1)
					go func() {
						defer handlers.Done()
						wrapped := &javaReplyFault{Conn: conn, phase: phase, once: &once}
						results <- Handle(ctx, wrapped, trusted, a, h)
					}()
				}
			}()
			// Cleanup order is important: stop accept loop, join all handlers, then DB.
			stop := func() {
				cancel()
				listener.Close()
				<-finished
				handlers.Wait()
			}
			t.Cleanup(stop)
			properties := metadata + fmt.Sprintf("OGM-Server: %s\nOGM-Test-Writer: %d\nOGM-Sender-Phase: %s\n", listener.Addr(), writer, phase)
			if err := os.WriteFile(jad, []byte(strings.ReplaceAll(properties, "\n", "\r\n")), 0600); err != nil {
				t.Fatal(err)
			}
			processCtx, stopProcess := context.WithTimeout(context.Background(), 20*time.Second)
			defer stopProcess()
			cmd := exec.CommandContext(processCtx, java, "-Djava.awt.headless=true", "-Duser.home="+home,
				"-cp", emulator, "org.microemu.app.Headless", "--rms", "file", "--quit", jad)
			output, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "SENDER-EMULATOR-PASS: "+phase) || strings.Contains(string(output), "SENDER-EMULATOR-FAIL") {
				t.Fatalf("emulator %s: %v\n%s", phase, err, output)
			}
			stop()
			for len(results) > 0 {
				err := <-results
				if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) {
					t.Fatalf("unexplained handler error: %v", err)
				}
			}
			wantCount := 1
			if phase == "retry" || phase == "verified" || phase == "conflict" {
				wantCount = 2
			}
			if err := s.View(100001, func(r historysync.Reader) error {
				tail, err := r.Tail()
				if err != nil {
					return err
				}
				rows, err := r.Records(0, tail, 8)
				if err != nil {
					return err
				}
				if len(rows) != wantCount || tail != int64(wantCount) {
					return fmt.Errorf("duplicate/missing committed history: tail=%d records=%d want=%d", tail, len(rows), wantCount)
				}
				for i, row := range rows {
					wantText, wantSeq, recipient := javaText, int64(1), uint32(100002)
					if phase == "conflict" && i == 0 {
						wantText = "original"
					} else if phase == "rejected" || (phase == "conflict" && i == 1) {
						wantText, wantSeq = "after outcome", 2
					} else if i == 1 {
						wantText, wantSeq, recipient = "after recovery", 2, 100001
					}
					m := row.Message
					if m.ID != int64(i+1) || m.Key != (acceptance.RetryKey{AccountID: 100001, WriterID: writer, ClientSeq: wantSeq}) || m.Recipient != recipient || m.Text != wantText {
						return fmt.Errorf("stored identity/UTF-8 mismatch: %+v", m)
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			t.Log("verified emulator phase, committed SQLite contents and harness cleanup")
		})
		if t.Failed() { // Later restart phases must not mask an earlier lost-ACK failure.
			break
		}
	}
}

type javaFailOnce struct {
	base acceptance.Store
	once sync.Once
}

func (s *javaFailOnce) Transact(fn func(acceptance.Transaction) error) error {
	fail := false
	s.once.Do(func() { fail = true })
	if fail {
		return errors.New("test-only retryable store failure")
	}
	return s.base.Transact(fn)
}

// All reply corruption is test-only and occurs when Handle writes its response,
// AFTER actual acceptance committed. Every reconnect uses the unchanged handler.
type javaReplyFault struct {
	net.Conn
	phase string
	once  *sync.Once
}

func (c *javaReplyFault) Write(bytes []byte) (int, error) {
	fault := false
	c.once.Do(func() { fault = true })
	if fault {
		switch c.phase {
		case "lost":
			c.Conn.Close()
			return 0, io.ErrClosedPipe
		case "malformed":
			if _, err := c.Conn.Write([]byte{0, 8}); err != nil {
				return 0, err
			}
			return len(bytes), nil
		case "unexpected":
			if err := deliveryv1.Write(c.Conn, deliveryv1.Frame{Kind: deliveryv1.Changed, Tail: 1}); err != nil {
				return 0, err
			}
			return len(bytes), nil
		case "mismatch":
			if err := deliveryv1.Write(c.Conn, deliveryv1.Frame{Kind: deliveryv1.Ack, ClientSeq: 2, MessageID: 1}); err != nil {
				return 0, err
			}
			return len(bytes), nil
		}
	}
	return c.Conn.Write(bytes)
}
