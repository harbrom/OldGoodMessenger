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
	"testing"
	"time"

	"ogm.local/firstcontact/internal/acceptance"
	"ogm.local/firstcontact/internal/deliveryv1"
	"ogm.local/firstcontact/internal/historysync"
	"ogm.local/firstcontact/internal/sqlitestore"
)

func TestJavaMEDurableHistory(t *testing.T) {
	jar := os.Getenv("OGM_JAVA_ME_DURABLE_HISTORY_JAR")
	if jar == "" {
		t.Skip("run scripts/test-java-me-durable-history.ps1 for file-RMS/TCP restart integration")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{1, 8} {
		t.Run(fmt.Sprintf("limit%d", limit), func(t *testing.T) {
			work := t.TempDir()
			s := database(t, filepath.Join(work, "history.sqlite"))
			last := 0
			// Each call after a completed JVM models messages accepted while offline.
			seed := func(to int) {
				t.Helper()
				a, err := acceptance.New(s, func(acceptance.TrustedContext, acceptance.Request) bool { return true })
				if err != nil {
					t.Fatal(err)
				}
				for last < to {
					last++
					acceptJavaDurableFixture(t, a, last, false)
				}
			}
			runJavaDurablePhase(t, root, jar, work, s, "empty", limit, 0, 0, 777)
			seed(1)
			runJavaDurablePhase(t, root, jar, work, s, "one", limit, 0, 1, 777)
			seed(3)
			runJavaDurablePhase(t, root, jar, work, s, "initial", limit, 0, 3, 777)
			seed(10)
			runJavaDurablePhase(t, root, jar, work, s, "resume", limit, 0, 10, 777)
			runJavaDurablePhase(t, root, jar, work, s, "earlier", limit, 0, 10, 777)
			seed(30)
			runJavaDurablePhase(t, root, jar, work, s, "interrupt", limit, 0, 30, 777)
			runJavaDurablePhase(t, root, jar, work, s, "recover", limit, 0, 30, 777)
			runJavaDurablePhase(t, root, jar, work, s, "verified", limit, 0, 30, 777)
			for fault := 1; fault <= 3; fault++ {
				runJavaDurablePhase(t, root, jar, work, s, "fault", limit, fault, 30, 777)
				runJavaDurablePhase(t, root, jar, work, s, "fault-recover", limit, fault, 30, 777)
			}
			// Deliberate rollback fixture: the stored cursor must fail, never reset.
			rewound := database(t, filepath.Join(work, "rewound.sqlite"))
			a, err := acceptance.New(rewound, func(acceptance.TrustedContext, acceptance.Request) bool { return true })
			if err != nil {
				t.Fatal(err)
			}
			for id := 1; id <= 3; id++ {
				acceptJavaDurableFixture(t, a, id, false)
			}
			runJavaDurablePhase(t, root, jar, work, rewound, "rewind", limit, 0, 3, 777)
		})
	}
	t.Run("maximum", func(t *testing.T) {
		work := t.TempDir()
		s := database(t, filepath.Join(work, "maximum.sqlite"))
		a, err := acceptance.New(s, func(acceptance.TrustedContext, acceptance.Request) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		for id := 1; id <= 8; id++ {
			acceptJavaDurableFixture(t, a, id, true)
		}
		runJavaDurablePhase(t, root, jar, work, s, "maximum", 8, 0, 8, 888)
	})
}

func acceptJavaDurableFixture(t *testing.T, a *acceptance.Acceptor, id int, maximum bool) {
	t.Helper()
	from, to := uint32(100001), uint32(100002)
	if id%3 == 0 {
		to = 100001
	} else if id%2 == 0 {
		from, to = 100002, 100001
	}
	message := fmt.Sprintf("%s #%d", javaText, id)
	if maximum {
		message = strings.Repeat("🙂", 500)
	}
	result, err := a.Accept(acceptance.TrustedContext{AccountID: from, WriterID: int64(from-100000) * 11},
		acceptance.Request{ClientSeq: int64(id), Recipient: to, Text: message})
	if err != nil || result.MessageID != int64(id) {
		t.Fatalf("seed acceptance %d: %+v %v", id, result, err)
	}
}

func runJavaDurablePhase(t *testing.T, root, jar, work string, s *sqlitestore.Store, phase string, limit, fault, total int, namespace int64) {
	t.Helper()
	a, err := acceptance.New(s, func(acceptance.TrustedContext, acceptance.Request) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	view := &javaHistoryView{base: s} // Reuse the existing bounded SQLite read observer.
	h, err := historysync.New(view)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	address := ""
	var stop func()
	if phase != "verified" {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address = listener.Addr().String()
		done := make(chan error, 1)
		go func() {
			conn, err := listener.Accept()
			listener.Close() // Exactly one fixture connection; no persistent accept service.
			if err != nil {
				done <- err
				return
			}
			wrapped := &javaDurableInterrupt{Conn: conn, interrupt: phase == "interrupt"}
			done <- Handle(ctx, wrapped, acceptance.TrustedContext{AccountID: 100001, WriterID: 7}, a, h)
		}()
		stop = func() {
			cancel()
			listener.Close()
			result := <-done
			var op *net.OpError
			if result != nil && !errors.Is(result, context.Canceled) && !errors.Is(result, net.ErrClosed) && !errors.Is(result, io.ErrClosedPipe) &&
				!((phase == "interrupt" || phase == "fault") && errors.As(result, &op)) {
				t.Errorf("%s: unexpected handler error: %v", phase, result)
			}
		}
		defer func() {
			if stop != nil {
				stop()
			}
		}()
	}
	info, err := os.Stat(jar)
	if err != nil {
		t.Fatal(err)
	}
	metadata := "MIDlet-Name: OGM Durable History Test\nMIDlet-Version: 0.0.1\nMIDlet-Vendor: OGM\n" +
		"MIDlet-1: OGM Durable History Test,,ogm.delivery.v1.DurableHistoryEmulatorTest\nMicroEdition-Configuration: CLDC-1.1\n" +
		"MicroEdition-Profile: MIDP-2.0\nMIDlet-Permissions: javax.microedition.io.Connector.socket\n" +
		fmt.Sprintf("MIDlet-Jar-URL: file:///%s\nMIDlet-Jar-Size: %d\nOGM-Server: %s\n", filepath.ToSlash(jar), info.Size(), address) +
		fmt.Sprintf("OGM-Durable-Phase: %s\nOGM-Test-Limit: %d\nOGM-Test-Fault: %d\nOGM-Test-Namespace: %d\n", phase, limit, fault, namespace)
	jad := filepath.Join(work, "durable.jad")
	if err := os.WriteFile(jad, []byte(strings.ReplaceAll(metadata, "\n", "\r\n")), 0600); err != nil {
		t.Fatal(err)
	}
	processCtx, stopProcess := context.WithTimeout(context.Background(), 20*time.Second)
	defer stopProcess()
	java := filepath.Join(root, ".tools/java-me/jdk/jdk8u504-b01/bin/java.exe")
	emulator := filepath.Join(root, ".tools/java-me/microemulator-2.0.4.jar")
	command := exec.CommandContext(processCtx, java, "-Djava.awt.headless=true", "-Duser.home="+work, "-cp", emulator,
		"org.microemu.app.Headless", "--rms", "file", "--quit", jad)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "DURABLE-HISTORY-PASS: "+phase) || strings.Contains(string(output), "DURABLE-HISTORY-FAIL") {
		t.Fatalf("%s limit=%d fault=%d: %v\n%s", phase, limit, fault, err, output)
	}
	if stop != nil {
		stop()
		stop = nil
	}
	// Assert the requested missing range, not just final displayed cache contents.
	start := int64(0)
	switch phase {
	case "resume", "interrupt":
		start = 3
		if phase == "interrupt" {
			start = 10
		}
	case "recover":
		start = int64(10 + 2*limit)
	case "fault-recover":
		if fault == 3 {
			start = int64(limit)
		}
	}
	if phase != "verified" && phase != "rewind" && total != 0 {
		wantPages := (total - int(start) + limit - 1) / limit
		if phase == "interrupt" {
			wantPages = 3
		}
		if phase == "fault" {
			wantPages = 1
		}
		if len(view.reads) != wantPages {
			t.Fatalf("%s requested %d pages, want %d", phase, len(view.reads), wantPages)
		}
		for i, read := range view.reads {
			if read.after != start+int64(i*limit) || read.through != int64(total) || read.max != limit+1 {
				t.Fatalf("%s refetched/skipped history or changed snapshot: %+v", phase, read)
			}
		}
	}
	if err := s.Transact(func(tx acceptance.Transaction) error {
		last, err := tx.LastMessageID()
		if err == nil && last != int64(total) {
			return fmt.Errorf("reader changed server allocations: %d", last)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Logf("PASS %s limit=%d fault=%d: separate JVM/file RMS, %d bounded SQLite page reads; resources joined", phase, limit, fault, len(view.reads))
}

type javaDurableInterrupt struct {
	net.Conn
	interrupt bool
	ends      int
}

func (c *javaDurableInterrupt) Write(bytes []byte) (int, error) {
	f, err := deliveryv1.Decode(bytes)
	if err == nil && f.Kind == deliveryv1.End {
		c.ends++
		if c.interrupt && c.ends == 3 {
			_, err := c.Conn.Write(bytes[:len(bytes)/2])
			c.Conn.Close()
			return 0, errors.Join(io.ErrClosedPipe, err)
		}
	}
	return c.Conn.Write(bytes)
}
