package historysync

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"ogm.local/firstcontact/internal/acceptance"
	"ogm.local/firstcontact/internal/deliveryv1"
)

var alice = acceptance.TrustedContext{AccountID: 100001, WriterID: 9}
var bob = acceptance.TrustedContext{AccountID: 100002, WriterID: 8}
var carol = acceptance.TrustedContext{AccountID: 100003, WriterID: 7}

func service(t *testing.T, store Store) *Service {
	t.Helper()
	s, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func accept(t *testing.T, store *fixtureStore, who acceptance.TrustedContext, seq int64, recipient uint32, text string) {
	t.Helper()
	a, err := acceptance.New(store, func(acceptance.TrustedContext, acceptance.Request) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Accept(who, acceptance.Request{ClientSeq: seq, Recipient: recipient, Text: text}); err != nil {
		t.Fatal(err)
	}
}
func seed(t *testing.T, count int) *fixtureStore {
	t.Helper()
	store := newStore()
	for i := 1; i <= count; i++ {
		accept(t, store, alice, int64(i), bob.AccountID, fmt.Sprintf("message %d", i))
	}
	return store
}
func request(after, through int64, limit byte) deliveryv1.Frame {
	return deliveryv1.Frame{Kind: deliveryv1.Sync, After: after, Through: through, Limit: limit}
}
func page(t *testing.T, s *Service, account uint32, req deliveryv1.Frame) []deliveryv1.Frame {
	t.Helper()
	frames, err := s.Page(TrustedContext{account}, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) < 1 || len(frames) > int(req.Limit)+1 || frames[len(frames)-1].Kind != deliveryv1.End {
		t.Fatalf("invalid page shape: %+v", frames)
	}
	for i, frame := range frames {
		if i < len(frames)-1 && frame.Kind != deliveryv1.Item {
			t.Fatal("unexpected operation before END")
		}
		b, err := frame.Marshal()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := deliveryv1.Decode(b)
		if err != nil || decoded != frame {
			t.Fatal("page frame is not valid Delivery v1 syntax")
		}
	}
	return frames
}
func end(t *testing.T, frames []deliveryv1.Frame, next, through int64) {
	t.Helper()
	want := deliveryv1.Frame{Kind: deliveryv1.End, Next: next, Through: through}
	if frames[len(frames)-1] != want {
		t.Fatalf("END %+v, want %+v", frames[len(frames)-1], want)
	}
}

func TestPagingLimitsAndExactBoundaries(t *testing.T) {
	for _, count := range []int{0, 1, 2, 8, 9, 16} {
		for _, limit := range []byte{1, 2, 8} {
			t.Run(fmt.Sprintf("count=%d/limit=%d", count, limit), func(t *testing.T) {
				store := seed(t, count)
				before := store.snapshot()
				s := service(t, store)
				after, through := int64(0), int64(0)
				for calls := 0; ; calls++ {
					if calls > count {
						t.Fatal("pagination did not terminate")
					}
					frames := page(t, s, alice.AccountID, request(after, through, limit))
					n := count - int(after)
					if n > int(limit) {
						n = int(limit)
					}
					if len(frames) != n+1 {
						t.Fatal("wrong item count")
					}
					for i, f := range frames[:n] {
						if f.Cursor != after+int64(i)+1 || f.MessageID != f.Cursor {
							t.Fatal("unexpected item order")
						}
					}
					next := after + int64(n)
					if next == int64(count) {
						end(t, frames, int64(count), int64(count))
					} else {
						end(t, frames, next, int64(count))
					}
					if count > 0 && store.requested.Load() != int32(limit)+1 {
						t.Fatal("bounded lookahead not requested")
					}
					if next == int64(count) {
						break
					}
					after, through = next, int64(count)
				}
				if !reflect.DeepEqual(before, store.snapshot()) {
					t.Fatal("paging mutated history")
				}
			})
		}
	}
}

func TestCompleteItemFieldsAndAccountScope(t *testing.T) {
	store := newStore()
	text := "ASCII\x00\n\u041f\u0440\u0438\u0432\u0456\u0442 Za\u017c\u00f3\u0142\u0107 e\u0301 \U0001f642"
	accept(t, store, carol, 1, bob.AccountID, "unrelated to Alice")
	accept(t, store, alice, 17, bob.AccountID, text)
	accept(t, store, alice, 18, alice.AccountID, "self")
	accept(t, store, carol, 2, carol.AccountID, "another account's self-message")
	s := service(t, store)
	before := store.snapshot()
	frames := page(t, s, alice.AccountID, request(0, 0, 8))
	want := []deliveryv1.Frame{
		{Kind: deliveryv1.Item, Cursor: 1, MessageID: 2, Sender: alice.AccountID, Recipient: bob.AccountID, WriterID: alice.WriterID, ClientSeq: 17, Text: text},
		{Kind: deliveryv1.Item, Cursor: 2, MessageID: 3, Sender: alice.AccountID, Recipient: alice.AccountID, WriterID: alice.WriterID, ClientSeq: 18, Text: "self"},
		{Kind: deliveryv1.End, Next: 2, Through: 2},
	}
	if !reflect.DeepEqual(frames, want) {
		t.Fatalf("complete fields differ: %+v", frames)
	}
	bobFrames := page(t, s, bob.AccountID, request(1, 0, 8))
	if len(bobFrames) != 2 || bobFrames[0].MessageID != 2 || bobFrames[0].Cursor != 2 {
		t.Fatal("sender and recipient cursor positions conflated")
	}
	// Historical replay and returned value changes never alter stored messages.
	frames[0].Text = "changed"
	frames[0].Cursor = 99
	if got := page(t, s, alice.AccountID, request(0, 2, 8)); !reflect.DeepEqual(got, want) {
		t.Fatal("historical replay changed")
	}
	if !reflect.DeepEqual(before, store.snapshot()) {
		t.Fatal("read changed accepted state")
	}
}

func TestNewMessagesBetweenPages(t *testing.T) {
	store := seed(t, 3)
	s := service(t, store)
	first := page(t, s, alice.AccountID, request(0, 0, 1))
	end(t, first, 1, 3)
	accept(t, store, alice, 4, bob.AccountID, "new after snapshot")
	second := page(t, s, alice.AccountID, request(1, 3, 2))
	end(t, second, 3, 3)
	if len(second) != 3 || second[0].MessageID != 2 || second[1].MessageID != 3 {
		t.Fatal("old snapshot included new acceptance")
	}
	end(t, page(t, s, alice.AccountID, request(3, 3, 8)), 3, 3)
	fresh := page(t, s, alice.AccountID, request(3, 0, 8))
	end(t, fresh, 4, 4)
	if len(fresh) != 2 || fresh[0].Text != "new after snapshot" {
		t.Fatal("fresh snapshot missed new acceptance")
	}
}

func TestCursorGapsAndEmptyTerminalPages(t *testing.T) {
	store := seed(t, 6)
	// Synthetic sparse allocations; this does not implement message deletion.
	refs := store.state.feeds[alice.AccountID]
	store.state.feeds[alice.AccountID] = []acceptance.FeedReference{refs[0], refs[2], refs[4]}
	before := store.snapshot()
	s := service(t, store)
	first := page(t, s, alice.AccountID, request(1, 0, 1))
	end(t, first, 3, 6)
	if len(first) != 2 || first[0].Cursor != 3 {
		t.Fatal("after is not exclusive")
	}
	second := page(t, s, alice.AccountID, request(3, 6, 1))
	end(t, second, 6, 6)
	if len(second) != 2 || second[0].Cursor != 5 {
		t.Fatal("full terminal page did not bridge tail gap")
	}
	empty := page(t, s, alice.AccountID, request(5, 6, 8))
	end(t, empty, 6, 6)
	if len(empty) != 1 {
		t.Fatal("empty terminal page contained records")
	}
	inside := page(t, s, alice.AccountID, request(2, 4, 8))
	end(t, inside, 4, 4)
	if len(inside) != 2 || inside[0].Cursor != 3 {
		t.Fatal("numeric gap bounds rejected or inclusive range broken")
	}
	gap := page(t, s, alice.AccountID, request(1, 2, 8))
	end(t, gap, 2, 2)
	if len(gap) != 1 {
		t.Fatal("gap unexpectedly contained records")
	}
	if !reflect.DeepEqual(before, store.snapshot()) {
		t.Fatal("sparse replay mutated state")
	}
}

func TestInvalidRequestsAndFutureBounds(t *testing.T) {
	store := seed(t, 3)
	s := service(t, store)
	before := store.snapshot()
	cases := []struct {
		name    string
		account uint32
		req     deliveryv1.Frame
		want    error
	}{
		{"zero account", 0, request(0, 0, 1), ErrInvalid}, {"short account", 99999, request(0, 0, 1), ErrInvalid},
		{"long account", 1000000000, request(0, 0, 1), ErrInvalid},
		{"wrong operation", alice.AccountID, deliveryv1.Frame{Kind: deliveryv1.Send, Limit: 1}, ErrInvalid},
		{"zero limit", alice.AccountID, request(0, 0, 0), ErrInvalid}, {"large limit", alice.AccountID, request(0, 0, 9), ErrInvalid},
		{"negative after", alice.AccountID, request(-1, 0, 1), ErrCursorInvalid}, {"negative through", alice.AccountID, request(0, -1, 1), ErrCursorInvalid},
		{"reversed bounds", alice.AccountID, request(2, 1, 1), ErrCursorInvalid},
		{"future after", alice.AccountID, request(4, 0, 1), ErrCursorInvalid}, {"future snapshot", alice.AccountID, request(0, 4, 1), ErrCursorInvalid},
		{"future both", alice.AccountID, request(4, 4, 1), ErrCursorInvalid},
		{"empty account unknown bound", 999999999, request(0, 1, 1), ErrCursorInvalid},
		{"maximum future position", alice.AccountID, request(1<<63-1, 0, 1), ErrCursorInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			frames, err := s.Page(TrustedContext{tc.account}, tc.req)
			if frames != nil || !errors.Is(err, tc.want) {
				t.Fatalf("got page %+v, error %v", frames, err)
			}
		})
	}
	empty := page(t, s, 999999999, request(0, 0, 1))
	end(t, empty, 0, 0)
	if !reflect.DeepEqual(before, store.snapshot()) {
		t.Fatal("invalid requests mutated history")
	}
	if _, err := New(nil); err != ErrInvalid {
		t.Fatal("nil store accepted")
	}
}

func TestReadFailuresPublishNoPage(t *testing.T) {
	for _, stage := range []string{"open", "tail", "records", "close"} {
		t.Run(stage, func(t *testing.T) {
			store := seed(t, 1)
			store.fail = stage
			before := store.snapshot()
			s := service(t, store)
			frames, err := s.Page(TrustedContext{alice.AccountID}, request(0, 0, 8))
			if frames != nil || err != errRead {
				t.Fatalf("partial page or lost error: %+v %v", frames, err)
			}
			if !reflect.DeepEqual(before, store.snapshot()) {
				t.Fatal("read failure changed history")
			}
			store.fail = ""
			end(t, page(t, s, alice.AccountID, request(0, 0, 8)), 1, 1)
		})
	}
}

func TestInconsistentStorePublishesNoPage(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Record)
	}{
		{"foreign feed", func(r *Record) { r.Reference.AccountID = carol.AccountID }},
		{"zero cursor", func(r *Record) { r.Reference.Cursor = 0 }},
		{"cursor above snapshot", func(r *Record) { r.Reference.Cursor = 3 }},
		{"mismatched message reference", func(r *Record) { r.Reference.MessageID = 99 }},
		{"zero message ID", func(r *Record) { r.Reference.MessageID = 0; r.Message.ID = 0 }},
		{"invalid sender", func(r *Record) { r.Message.Key.AccountID = 0 }},
		{"invalid recipient", func(r *Record) { r.Message.Recipient = 0 }},
		{"nonparticipant", func(r *Record) { r.Message.Key.AccountID = bob.AccountID; r.Message.Recipient = carol.AccountID }},
		{"invalid writer", func(r *Record) { r.Message.Key.WriterID = 0 }},
		{"invalid sequence", func(r *Record) { r.Message.Key.ClientSeq = 0 }},
		{"invalid UTF-8", func(r *Record) { r.Message.Text = string([]byte{0xed, 0xa0, 0x80}) }},
	}
	for _, tc := range cases {
		for _, index := range []int{0, 1} {
			t.Run(fmt.Sprintf("%s/row=%d", tc.name, index), func(t *testing.T) {
				store := seed(t, 2)
				for _, ref := range store.state.feeds[alice.AccountID] {
					store.override = append(store.override, Record{ref, store.state.messages[ref.MessageID]})
				}
				tc.mutate(&store.override[index])
				frames, err := service(t, store).Page(TrustedContext{alice.AccountID}, request(0, 0, 1))
				if frames != nil || err != ErrStoreState {
					t.Fatalf("invalid returned/lookahead row exposed: %+v %v", frames, err)
				}
			})
		}
	}
	for _, mode := range []string{"duplicate cursor", "decreasing message ID", "excess rows", "negative tail"} {
		t.Run(mode, func(t *testing.T) {
			store := seed(t, 2)
			for _, ref := range store.state.feeds[alice.AccountID] {
				store.override = append(store.override, Record{ref, store.state.messages[ref.MessageID]})
			}
			switch mode {
			case "duplicate cursor":
				store.override[1].Reference.Cursor = 1
			case "decreasing message ID":
				store.override[1].Reference.MessageID = 1
				store.override[1].Message.ID = 1
			case "excess rows":
				store.override = append(store.override, store.override[1])
			case "negative tail":
				store.state.tails[alice.AccountID] = -1
			}
			frames, err := service(t, store).Page(TrustedContext{alice.AccountID}, request(0, 0, 1))
			if frames != nil || err != ErrStoreState {
				t.Fatalf("invalid store state exposed: %+v %v", frames, err)
			}
		})
	}
}

func TestMaximumScalarAndCursorBounds(t *testing.T) {
	store := newStore()
	max := int64(1<<63 - 1)
	store.state.lastID = max - 1
	store.state.tails[alice.AccountID] = max - 1
	store.state.tails[bob.AccountID] = max - 1
	text := strings.Repeat("\U0001f642", 500)
	accept(t, store, acceptance.TrustedContext{AccountID: alice.AccountID, WriterID: max}, max, bob.AccountID, text)
	frames := page(t, service(t, store), alice.AccountID, request(max-1, 0, 8))
	end(t, frames, max, max)
	if len(frames) != 2 || frames[0].Cursor != max || frames[0].MessageID != max || frames[0].WriterID != max || frames[0].ClientSeq != max || frames[0].Text != text {
		t.Fatal("maximum fields changed")
	}
}

func TestConsistentReadDuringAcceptance(t *testing.T) {
	store := seed(t, 2)
	s := service(t, store)
	captured := make(chan struct{})
	release := make(chan struct{})
	attempted := make(chan struct{}, 1)
	store.writeAttempt = attempted
	var gate, cleanup sync.Once
	store.tailRead = func() { gate.Do(func() { close(captured); <-release }) }
	defer cleanup.Do(func() { close(release) })
	type readResult struct {
		frames []deliveryv1.Frame
		err    error
	}
	read := make(chan readResult, 1)
	write := make(chan error, 1)
	go func() {
		frames, err := s.Page(TrustedContext{alice.AccountID}, request(0, 0, 8))
		read <- readResult{frames, err}
	}()
	select {
	case <-captured:
	case <-time.After(3 * time.Second):
		t.Fatal("tail capture did not start")
	}
	a, err := acceptance.New(store, func(acceptance.TrustedContext, acceptance.Request) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, err := a.Accept(alice, acceptance.Request{ClientSeq: 3, Recipient: bob.AccountID, Text: "concurrent"})
		write <- err
	}()
	select {
	case <-attempted:
	case <-time.After(3 * time.Second):
		t.Fatal("acceptance did not reach write boundary")
	}
	select {
	case <-write:
		t.Fatal("acceptance published inside pinned read view")
	default:
	}
	cleanup.Do(func() { close(release) })
	select {
	case got := <-read:
		if got.err != nil || len(got.frames) != 3 {
			t.Fatalf("read failed: %+v", got)
		}
		end(t, got.frames, 2, 2)
	case <-time.After(3 * time.Second):
		t.Fatal("read did not finish")
	}
	select {
	case err := <-write:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("acceptance did not finish")
	}
	old := page(t, s, alice.AccountID, request(0, 2, 8))
	if len(old) != 3 {
		t.Fatal("old snapshot changed")
	}
	end(t, old, 2, 2)
	fresh := page(t, s, alice.AccountID, request(2, 0, 8))
	if len(fresh) != 2 || fresh[0].Text != "concurrent" {
		t.Fatal("new commit missing")
	}
	end(t, fresh, 3, 3)
}
