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
)

// Opt-in companion to the sender's existing emulator harness. All new fixture
// code stays in _test.go; Handle, history service and SQLite schema are unchanged.
func TestJavaMEHistory(t *testing.T) {
	jar := os.Getenv("OGM_JAVA_ME_HISTORY_JAR")
	if jar == "" {
		t.Skip("run scripts/test-java-me-history.ps1 for TCP/SQLite emulator history tests")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	java := filepath.Join(root, ".tools/java-me/jdk/jdk8u504-b01/bin/java.exe")
	emulator := filepath.Join(root, ".tools/java-me/microemulator-2.0.4.jar")
	info, err := os.Stat(jar)
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name    string
		account uint32
		limit   int
		count   int
		simple  bool
		fault   bool
	}{
		{"empty", 100001, 1, 0, true, false}, {"one", 100001, 1, 1, true, false},
		{"default", 100001, 1, 18, false, false}, {"limit2", 100001, 2, 18, false, false},
		{"limit8", 100001, 8, 18, false, false}, {"bob", 100002, 2, 18, false, false},
		{"eve", 100003, 2, 18, false, false}, {"absent", 100004, 2, 18, false, false},
		{"full", 100001, 8, 8, true, false}, {"gaps", 100001, 2, 18, false, false},
		{"snapshot", 100001, 2, 18, false, false}, {"cancel", 100001, 2, 18, false, false},
		{"partial-item", 100001, 2, 18, false, true}, {"partial-end", 100001, 2, 18, false, true},
		{"bad-order", 100001, 2, 18, false, true}, {"bad-end", 100001, 2, 18, false, true},
		{"snapshot-change", 100001, 2, 18, false, true}, {"foreign", 100001, 2, 18, false, true},
		{"unexpected", 100001, 2, 18, false, true}, {"malformed", 100001, 2, 18, false, true},
		{"error-mismatch", 100001, 2, 18, false, true}, {"cursor-error", 100001, 2, 18, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			work := t.TempDir()
			s := database(t, filepath.Join(work, "history.sqlite"))
			a, err := acceptance.New(s, func(_ acceptance.TrustedContext, r acceptance.Request) bool {
				return r.Recipient >= 100001 && r.Recipient <= 100004
			})
			if err != nil {
				t.Fatal(err)
			}
			for id := 1; id <= tc.count; id++ {
				from, to := uint32(100001), uint32(100002)
				if !tc.simple {
					switch {
					case id%6 == 4:
						from, to = 100003, 100003
					case id%6 == 3:
						to = 100001
					case id%6 == 0:
						from, to = 100002, 100002
					case id%2 == 0:
						from, to = 100002, 100001
					}
				}
				result, err := a.Accept(acceptance.TrustedContext{AccountID: from, WriterID: int64(from-100000) * 11},
					acceptance.Request{ClientSeq: int64(id), Recipient: to, Text: fmt.Sprintf("%s #%d", javaText, id)})
				if err != nil || result.MessageID != int64(id) {
					t.Fatalf("seed acceptance: %+v %v", result, err)
				}
			}
			view := &javaHistoryView{base: s, gaps: tc.name == "gaps"}
			h, err := historysync.New(view)
			if err != nil {
				t.Fatal(err)
			}
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
						wrapped := &javaHistoryFault{Conn: conn, phase: tc.name, once: &once, acceptor: a}
						// Locally selected fixture context, never supplied over TCP.
						results <- Handle(ctx, wrapped, acceptance.TrustedContext{AccountID: tc.account, WriterID: 7}, a, h)
					}()
				}
			}()
			stop := func() {
				cancel()
				listener.Close()
				<-finished
				handlers.Wait()
			}
			t.Cleanup(stop) // Registered after DB: join handlers before closing SQLite.
			jad := filepath.Join(work, "history.jad")
			metadata := "MIDlet-Name: OGM History Test\nMIDlet-Version: 0.0.1\nMIDlet-Vendor: OGM\n" +
				"MIDlet-1: OGM History Test,,ogm.delivery.v1.HistoryEmulatorTest\nMicroEdition-Configuration: CLDC-1.1\n" +
				"MicroEdition-Profile: MIDP-2.0\nMIDlet-Permissions: javax.microedition.io.Connector.socket\n" +
				fmt.Sprintf("MIDlet-Jar-URL: file:///%s\nMIDlet-Jar-Size: %d\nOGM-Server: %s\n", filepath.ToSlash(jar), info.Size(), listener.Addr()) +
				fmt.Sprintf("OGM-History-Phase: %s\nOGM-Test-Account: %d\nOGM-Test-Limit: %d\nOGM-Test-Maximum: %d\nOGM-Test-Simple: %t\nOGM-Test-Fault: %t\n",
					tc.name, tc.account, tc.limit, tc.count, tc.simple, tc.fault)
			if err := os.WriteFile(jad, []byte(strings.ReplaceAll(metadata, "\n", "\r\n")), 0600); err != nil {
				t.Fatal(err)
			}
			processCtx, stopProcess := context.WithTimeout(context.Background(), 20*time.Second)
			defer stopProcess()
			cmd := exec.CommandContext(processCtx, java, "-Djava.awt.headless=true", "-Duser.home="+work, "-cp", emulator,
				"org.microemu.app.Headless", "--rms", "file", "--quit", jad)
			output, err := cmd.CombinedOutput()
			if err != nil || !strings.Contains(string(output), "HISTORY-EMULATOR-PASS: "+tc.name) || strings.Contains(string(output), "HISTORY-EMULATOR-FAIL") {
				t.Fatalf("emulator history %s: %v\n%s", tc.name, err, output)
			}
			stop()
			for len(results) > 0 {
				err := <-results
				var op *net.OpError
				if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) && !errors.Is(err, io.ErrClosedPipe) &&
					!((tc.fault || tc.name == "cancel") && errors.As(err, &op)) {
					t.Fatalf("unexpected handler error: %v", err)
				}
			}
			wantMessages := tc.count
			if tc.name == "snapshot" {
				wantMessages++
				if len(view.reads) != 13 { // Six old pages, seven pages of fresh H.
					t.Fatalf("unexpected snapshot page count: %+v", view.reads)
				}
				for i, read := range view.reads {
					wantH, wantAfter := int64(12), int64(i*2)
					if i >= 6 {
						wantH, wantAfter = 13, int64((i-6)*2)
					}
					if read.through != wantH || read.after != wantAfter || read.max != 3 {
						t.Fatalf("SYNC changed/skipped snapshot or page bounds: %+v", read)
					}
				}
			}
			if err := s.Transact(func(tx acceptance.Transaction) error {
				last, err := tx.LastMessageID()
				if err == nil && last != int64(wantMessages) {
					return fmt.Errorf("read-only replay changed message allocation: %d", last)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			t.Log("exact account history/pages verified in separate emulator JVM; loopback handlers joined before SQLite close")
		})
	}
}

type javaHistoryRead struct {
	after, through int64
	max            int
}

type javaHistoryView struct {
	base  historysync.Store
	gaps  bool
	reads []javaHistoryRead
}

func (s *javaHistoryView) View(account uint32, fn func(historysync.Reader) error) error {
	return s.base.View(account, func(r historysync.Reader) error { return fn(javaHistoryReader{Reader: r, owner: s}) })
}

type javaHistoryReader struct {
	historysync.Reader
	owner *javaHistoryView
}

func (r javaHistoryReader) Tail() (int64, error) {
	tail, err := r.Reader.Tail()
	if r.owner.gaps {
		tail = tail*2 + 1
	}
	return tail, err
}

func (r javaHistoryReader) Records(after, through int64, max int) ([]historysync.Record, error) {
	r.owner.reads = append(r.owner.reads, javaHistoryRead{after, through, max})
	if r.owner.gaps {
		after, through = after/2, through/2
	}
	rows, err := r.Reader.Records(after, through, max)
	if r.owner.gaps {
		// Sparse read-view fixture only; SQLite rows/schema/acceptance are untouched.
		for i := range rows {
			rows[i].Reference.Cursor *= 2
		}
	}
	return rows, err
}

type javaHistoryFault struct {
	net.Conn
	phase       string
	once        *sync.Once
	acceptor    *acceptance.Acceptor
	items, ends int
}

func (c *javaHistoryFault) Write(bytes []byte) (int, error) {
	f, err := deliveryv1.Decode(bytes)
	if err != nil {
		return c.Conn.Write(bytes)
	}
	if f.Kind == deliveryv1.Item {
		c.items++
	}
	if f.Kind == deliveryv1.End {
		c.ends++
	}
	if c.phase == "snapshot" && f.Kind != deliveryv1.End {
		return c.Conn.Write(bytes)
	}
	if c.phase == "snapshot" && f.Kind == deliveryv1.End {
		n, err := c.Conn.Write(bytes)
		c.once.Do(func() {
			if err != nil {
				return
			}
			_, err = c.acceptor.Accept(acceptance.TrustedContext{AccountID: 100002, WriterID: 22},
				acceptance.Request{ClientSeq: 19, Recipient: 100001, Text: "late"})
		})
		return n, err // Commit late message after first END, before next SYNC is read.
	}
	target := f.Kind == deliveryv1.Item && c.items == 1
	switch c.phase {
	case "partial-end", "bad-end":
		target = f.Kind == deliveryv1.End && c.ends == 1
	case "bad-order":
		target = f.Kind == deliveryv1.Item && c.items == 2
	case "snapshot-change":
		target = f.Kind == deliveryv1.End && c.ends == 2
	}
	fault := false
	if target {
		c.once.Do(func() { fault = true })
	}
	if !fault {
		return c.Conn.Write(bytes)
	}
	switch c.phase {
	case "partial-item", "partial-end":
		_, err := c.Conn.Write(bytes[:len(bytes)/2])
		c.Conn.Close()
		return 0, errors.Join(io.ErrClosedPipe, err)
	case "bad-order":
		f.MessageID = 1
	case "bad-end":
		f.Next--
	case "snapshot-change":
		f.Through++
	case "foreign":
		f.Sender, f.Recipient = 100003, 100004
	case "unexpected":
		f = deliveryv1.Frame{Kind: deliveryv1.Ack, ClientSeq: 1, MessageID: 1}
	case "error-mismatch":
		f = deliveryv1.Frame{Kind: deliveryv1.Failure, RequestOp: deliveryv1.Sync, Reference: 1, Code: deliveryv1.CursorInvalid}
	case "malformed":
		_, err := c.Conn.Write([]byte{0, 8})
		return len(bytes), err
	default:
		return c.Conn.Write(bytes)
	}
	if err := deliveryv1.Write(c.Conn, f); err != nil {
		return 0, err
	}
	return len(bytes), nil
}
