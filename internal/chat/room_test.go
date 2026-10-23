package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

const testDeadline = 2 * time.Second

func startRoom(t *testing.T) (*Room, context.CancelFunc) {
	t.Helper()
	cr := NewRoom("general")
	ctx, cancel := context.WithCancel(context.Background())
	go cr.Run(ctx)
	t.Cleanup(cancel)
	return cr, cancel
}

// privateFrames is how many frames a joining client gets first: welcome, users, history.
const privateFrames = 3

func newTestClient(nick string, buf int) *Client {
	return &Client{send: make(chan []byte, buf), registered: make(chan struct{}), nickname: nick}
}

func register(t *testing.T, cr *Room, c *Client) {
	t.Helper()
	select {
	case cr.register <- c:
	case <-time.After(testDeadline):
		t.Fatal("timeout registering")
	}
}

func unregister(t *testing.T, cr *Room, c *Client) {
	t.Helper()
	select {
	case cr.unregister <- c:
	case <-time.After(testDeadline):
		t.Fatal("timeout unregistering")
	}
}

// broadcast queues text as if it had been read from from's connection.
func broadcast(t *testing.T, cr *Room, from *Client, text string) {
	t.Helper()
	select {
	case cr.broadcast <- inbound{from: from, text: text}:
	case <-time.After(testDeadline):
		t.Fatal("timeout broadcasting")
	}
}

// decode parses an outbound frame, requiring exactly the four envelope
// fields and a known type.
func decode(t *testing.T, b []byte) message {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatalf("invalid JSON %q: %v", b, err)
	}
	for _, k := range []string{"type", "from", "text", "ts"} {
		if _, ok := fields[k]; !ok {
			t.Fatalf("frame %q misses field %q", b, k)
		}
	}
	if len(fields) != 4 {
		t.Fatalf("frame %q has %d fields, want 4", b, len(fields))
	}
	var m message
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("frame %q: %v", b, err)
	}
	switch m.Type {
	case typeChat, typeJoin, typeLeave, typeWelcome:
	default:
		t.Fatalf("unknown type in %q", b)
	}
	return m
}

// decodeUsers parses a users frame, requiring exactly type/users/ts and an
// array (never null).
func decodeUsers(t *testing.T, b []byte) usersFrame {
	t.Helper()
	fields := frameFields(t, b, "users")
	if !isJSONArray(fields["users"]) {
		t.Fatalf("users frame %q: users is not an array", b)
	}
	var f usersFrame
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("frame %q: %v", b, err)
	}
	if f.Type != typeUsers {
		t.Fatalf("frame %q: type %q, want users", b, f.Type)
	}
	return f
}

// decodeHistory parses a history frame, requiring exactly type/messages/ts, an
// array (never null) and only chat entries with the four envelope fields.
func decodeHistory(t *testing.T, b []byte) historyFrame {
	t.Helper()
	fields := frameFields(t, b, "messages")
	if !isJSONArray(fields["messages"]) {
		t.Fatalf("history frame %q: messages is not an array", b)
	}
	var raw struct {
		Type     string            `json:"type"`
		Messages []json.RawMessage `json:"messages"`
		TS       int64             `json:"ts"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("frame %q: %v", b, err)
	}
	if raw.Type != typeHistory {
		t.Fatalf("frame %q: type %q, want history", b, raw.Type)
	}
	f := historyFrame{Type: raw.Type, Messages: make([]message, 0, len(raw.Messages)), TS: raw.TS}
	for _, entry := range raw.Messages {
		m := decode(t, entry)
		if m.Type != typeChat {
			t.Fatalf("history entry %q is not a chat", entry)
		}
		f.Messages = append(f.Messages, m)
	}
	return f
}

// frameFields requires b to be a JSON object with exactly type, key and ts.
func frameFields(t *testing.T, b []byte, key string) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatalf("invalid JSON %q: %v", b, err)
	}
	for _, k := range []string{"type", key, "ts"} {
		if _, ok := fields[k]; !ok {
			t.Fatalf("frame %q misses field %q", b, k)
		}
	}
	if len(fields) != 3 {
		t.Fatalf("frame %q has %d fields, want 3", b, len(fields))
	}
	return fields
}

func isJSONArray(raw json.RawMessage) bool {
	return len(raw) > 0 && raw[0] == '['
}

// nextRaw waits for the next frame on c and returns it undecoded.
func nextRaw(t *testing.T, c *Client) []byte {
	t.Helper()
	select {
	case m, ok := <-c.send:
		if !ok {
			t.Fatal("send closed, wanted a message")
		}
		return m
	case <-time.After(testDeadline):
		t.Fatal("timeout waiting for a message")
	}
	return nil
}

// next waits for the next frame on c and decodes it.
func next(t *testing.T, c *Client) message {
	t.Helper()
	return decode(t, nextRaw(t, c))
}

// expect waits for the next message on c and compares it to want, ignoring ts.
func expect(t *testing.T, c *Client, typ, from, text string) {
	t.Helper()
	got := next(t, c)
	if got.Type != typ || got.From != from || got.Text != text {
		t.Fatalf("got (%s, %q, %q), want (%s, %q, %q)", got.Type, got.From, got.Text, typ, from, text)
	}
}

// expectWelcome consumes the private join frames of c (welcome, users, history)
// in order, checks the welcome and that all three share one ts, and returns
// users and history for further assertions.
func expectWelcome(t *testing.T, c *Client, name string) (usersFrame, historyFrame) {
	t.Helper()
	w := next(t, c)
	if w.Type != typeWelcome || w.From != name || w.Text != "" {
		t.Fatalf("got (%s, %q, %q), want (welcome, %q, \"\")", w.Type, w.From, w.Text, name)
	}
	users := decodeUsers(t, nextRaw(t, c))
	hist := decodeHistory(t, nextRaw(t, c))
	if users.TS != w.TS || hist.TS != w.TS {
		t.Fatalf("private frames differ in ts: welcome %d, users %d, history %d", w.TS, users.TS, hist.TS)
	}
	return users, hist
}

// expectUsers compares the nicknames of a users frame, in order.
func expectUsers(t *testing.T, got usersFrame, want ...string) {
	t.Helper()
	if !slices.Equal(got.Users, want) {
		t.Fatalf("users = %q, want %q", got.Users, want)
	}
}

// expectHistory compares type, from and text of every entry, ignoring ts.
func expectHistory(t *testing.T, got historyFrame, want ...message) {
	t.Helper()
	if len(got.Messages) != len(want) {
		t.Fatalf("history has %d messages, want %d: %+v", len(got.Messages), len(want), got.Messages)
	}
	for i, m := range got.Messages {
		if m.Type != want[i].Type || m.From != want[i].From || m.Text != want[i].Text {
			t.Fatalf("history[%d] = %+v, want %+v", i, m, want[i])
		}
	}
}

// expectEmpty fails if a frame is already queued on c.
func expectEmpty(t *testing.T, c *Client) {
	t.Helper()
	select {
	case raw := <-c.send:
		t.Fatalf("unexpected frame %s", raw)
	default:
	}
}

// expectClosed waits until c.send is closed, discarding queued messages.
func expectClosed(t *testing.T, c *Client) {
	t.Helper()
	deadline := time.After(testDeadline)
	for {
		select {
		case _, ok := <-c.send:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("timeout waiting for send to close")
		}
	}
}

// syncRoom waits until Run has processed everything queued so far: it
// broadcasts a marker from c and consumes c's welcome frames, join and the marker.
// c must be freshly registered and the only frames pending on it.
func syncRoom(t *testing.T, cr *Room, c *Client) {
	t.Helper()
	select {
	case <-c.registered: // makes the read of c.nickname below race-free
	case <-time.After(testDeadline):
		t.Fatal("timeout waiting for registration")
	}
	broadcast(t, cr, c, "sync-marker")
	expectWelcome(t, c, c.nickname)
	expect(t, c, typeJoin, c.nickname, "")
	expect(t, c, typeChat, c.nickname, "sync-marker")
}

func TestJoinNoticeDelivered(t *testing.T) {
	cr, _ := startRoom(t)
	a := newTestClient("alice", 8)
	register(t, cr, a)
	expectWelcome(t, a, "alice")
	expect(t, a, typeJoin, "alice", "")
}

func TestSameNicknameIndependent(t *testing.T) {
	cr, _ := startRoom(t)
	a1 := newTestClient("bob", 8)
	a2 := newTestClient("bob", 8)
	register(t, cr, a1)
	expectWelcome(t, a1, "bob")
	expect(t, a1, typeJoin, "bob", "")
	register(t, cr, a2)
	expect(t, a1, typeJoin, "bob (2)", "")
	expectWelcome(t, a2, "bob (2)")
	expect(t, a2, typeJoin, "bob (2)", "")

	broadcast(t, cr, a1, "hi")
	expect(t, a1, typeChat, "bob", "hi")
	expect(t, a2, typeChat, "bob", "hi")

	unregister(t, cr, a1)
	expectClosed(t, a1)
	expect(t, a2, typeLeave, "bob", "")

	broadcast(t, cr, a2, "again")
	expect(t, a2, typeChat, "bob (2)", "again")
}

func TestUnregisterClosesOnceAndNoticeOnce(t *testing.T) {
	cr, _ := startRoom(t)
	a := newTestClient("a", 8)
	b := newTestClient("b", 8)
	register(t, cr, a)
	register(t, cr, b)
	expectWelcome(t, a, "a")
	expect(t, a, typeJoin, "a", "")
	expect(t, a, typeJoin, "b", "")
	expectWelcome(t, b, "b")
	expect(t, b, typeJoin, "b", "")

	unregister(t, cr, a)
	unregister(t, cr, a) // no-op, must not panic
	expectClosed(t, a)
	expect(t, b, typeLeave, "a", "")

	broadcast(t, cr, b, "sync")
	expect(t, b, typeChat, "b", "sync") // no second leave notice queued before this
}

// A buffer of privateFrames+2 holds exactly the private frames + own join for "slow" and
// the join of "ok", so the next broadcast message is what overflows it and
// triggers the drop.
func TestUnregisterAfterSlowDropIsNoop(t *testing.T) {
	cr, _ := startRoom(t)
	slow := newTestClient("slow", privateFrames+2)
	ok := newTestClient("ok", 16)
	register(t, cr, slow)
	register(t, cr, ok)
	expectWelcome(t, ok, "ok")
	expect(t, ok, typeJoin, "ok", "") // both join notices have been fanned out by now
	broadcast(t, cr, ok, "m1")        // slow buffer full -> dropped
	expect(t, ok, typeChat, "ok", "m1")
	expect(t, ok, typeLeave, "slow", "") // announced right after the frame that dropped it
	expectClosed(t, slow)

	unregister(t, cr, slow) // must not panic or emit notice
	broadcast(t, cr, ok, "sync")
	expect(t, ok, typeChat, "ok", "sync")
}

// Same sizing as above: the drop is caused by the broadcast message, and the
// slow client still gets everything queued before it was dropped.
func TestSlowClientDroppedOthersReceive(t *testing.T) {
	cr, _ := startRoom(t)
	slow := newTestClient("slow", privateFrames+2)
	fast := newTestClient("fast", 64)
	register(t, cr, slow)
	register(t, cr, fast)
	expectWelcome(t, fast, "fast")
	expect(t, fast, typeJoin, "fast", "")

	broadcast(t, cr, fast, "x")
	expect(t, fast, typeChat, "fast", "x")
	expect(t, fast, typeLeave, "slow", "")
	expectWelcome(t, slow, "slow")
	expect(t, slow, typeJoin, "slow", "")
	expect(t, slow, typeJoin, "fast", "")
	expectClosed(t, slow)

	broadcast(t, cr, fast, "y") // the room keeps serving the remaining clients
	expect(t, fast, typeChat, "fast", "y")
}

func TestSaturationNoDeadlock(t *testing.T) {
	cr := NewRoom("general")
	listener := newTestClient("l", sendBufferSize)
	// Fill the broadcast queue before Run starts so it is full when traffic begins.
	for i := 0; i < cap(cr.broadcast); i++ {
		cr.broadcast <- inbound{from: listener, text: "pre"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go cr.Run(ctx)

	// trySend avoids leaking goroutines if the hub deadlocks.
	trySend := func(ch chan<- inbound, m inbound) bool {
		select {
		case ch <- m:
			return true
		case <-time.After(testDeadline * 3):
			t.Error("timeout sending to hub under saturation")
			return false
		}
	}
	trySendClient := func(ch chan<- *Client, c *Client) bool {
		select {
		case ch <- c:
			return true
		case <-time.After(testDeadline * 3):
			t.Error("timeout sending client to hub under saturation")
			return false
		}
	}

	var wg sync.WaitGroup
	// Drain-capable registered listener so the hub keeps flowing; it is also
	// the sender, so fanOut is exercised.
	register(t, cr, listener)
	go func() {
		for range listener.send {
		}
	}()

	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				if !trySend(cr.broadcast, inbound{from: listener, text: "m"}) {
					return
				}
			}
		}()
		go func() {
			defer wg.Done()
			c := newTestClient("c", 4)
			if trySendClient(cr.register, c) {
				trySendClient(cr.unregister, c)
			}
		}()
	}

	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	select {
	case <-finished:
	case <-time.After(testDeadline * 4):
		t.Fatal("deadlock under saturation")
	}

	// The hub must still be alive afterwards.
	last := newTestClient("last", 8)
	register(t, cr, last)
	expectWelcome(t, last, "last")
	expect(t, last, typeJoin, "last", "")
}

func TestContextCancelClosesAll(t *testing.T) {
	cr, cancel := startRoom(t)
	a := newTestClient("a", 8)
	b := newTestClient("b", 8)
	register(t, cr, a)
	register(t, cr, b)
	cancel()
	expectClosed(t, a)
	expectClosed(t, b)
	select {
	case <-cr.done:
	case <-time.After(testDeadline):
		t.Fatal("done not closed")
	}
}

// CHAT-11: welcome is private and first; the newcomer's own join follows.
func TestWelcomeFirst(t *testing.T) {
	cr, _ := startRoom(t)
	a := newTestClient("a", 8)
	b := newTestClient("Bob", 8)
	c := newTestClient("bob", 8)
	register(t, cr, a)
	register(t, cr, b)
	register(t, cr, c)

	expectWelcome(t, c, "bob (2)")
	expect(t, c, typeJoin, "bob (2)", "")
	expectWelcome(t, b, "Bob")
	expect(t, b, typeJoin, "Bob", "")
	expect(t, b, typeJoin, "bob (2)", "") // others get only the join
	expectWelcome(t, a, "a")
	expect(t, a, typeJoin, "a", "")
	expect(t, a, typeJoin, "Bob", "")
	expect(t, a, typeJoin, "bob (2)", "")
}

// CHAT-12: a nickname is free again after any removal path.
func TestNameReleasedOnRemove(t *testing.T) {
	t.Run("unregister", func(t *testing.T) {
		cr, _ := startRoom(t)
		bob := newTestClient("bob", 8)
		obs := newTestClient("obs", 16)
		register(t, cr, bob)
		register(t, cr, obs)
		unregister(t, cr, bob)
		expectClosed(t, bob)

		again := newTestClient("BOB", 8)
		register(t, cr, again)
		expectWelcome(t, again, "BOB")
	})
	t.Run("slow drop", func(t *testing.T) {
		cr, _ := startRoom(t)
		bob := newTestClient("bob", privateFrames+1) // private frames + own join; obs's join overflows it
		obs := newTestClient("obs", 16)
		register(t, cr, bob)
		register(t, cr, obs)
		expectWelcome(t, obs, "obs")
		expect(t, obs, typeJoin, "obs", "")
		expect(t, obs, typeLeave, "bob", "") // bob was dropped by obs's join
		expectClosed(t, bob)

		again := newTestClient("bob", 8)
		register(t, cr, again)
		users, _ := expectWelcome(t, again, "bob")
		expectUsers(t, users, "bob", "obs") // the dropped bob is gone from the snapshot
	})
}

// CHAT-10: a message from a client that is no longer in the room is discarded.
func TestBroadcastFromNonMemberDropped(t *testing.T) {
	cr, _ := startRoom(t)
	slow := newTestClient("slow", privateFrames+1)
	ok := newTestClient("ok", 16)
	register(t, cr, slow)
	register(t, cr, ok)
	expectWelcome(t, ok, "ok")
	expect(t, ok, typeJoin, "ok", "")
	expect(t, ok, typeLeave, "slow", "") // slow was dropped by ok's join
	expectClosed(t, slow)

	broadcast(t, cr, slow, "ghost")
	broadcast(t, cr, ok, "sync")
	expect(t, ok, typeChat, "ok", "sync") // "ghost" would have come first

	late := newTestClient("late", 8)
	register(t, cr, late)
	_, hist := expectWelcome(t, late, "late")
	expectHistory(t, hist, message{Type: typeChat, From: "ok", Text: "sync"}) // never "ghost"
}

// A chat queued right before unregister must be relayed before the leave,
// whichever of the two channels Run's select happens to pick first.
func TestChatBeforeLeaveOnDisconnect(t *testing.T) {
	// Drive the hub's handlers directly (no Run goroutine) so the "broadcast
	// queued, then unregister" interleaving is deterministic.
	cr := NewRoom("general")
	a := newTestClient("a", 8)
	b := newTestClient("b", 8)
	for _, c := range []*Client{a, b} {
		cr.clients[c] = struct{}{}
		cr.taken[foldKey(c.nickname)] = struct{}{}
	}

	cr.broadcast <- inbound{from: a, text: "bye"}
	cr.leave(a)

	expect(t, b, typeChat, "a", "bye")
	expect(t, b, typeLeave, "a", "")
}

// The drain before a leave can itself slow-drop the leaving client; it must
// not be removed (and its send closed) a second time, nor announced twice.
func TestLeaveAfterDrainDroppedClient(t *testing.T) {
	cr := NewRoom("general")
	a := newTestClient("a", 0) // any delivery to a overflows
	b := newTestClient("b", 8)
	for _, c := range []*Client{a, b} {
		cr.clients[c] = struct{}{}
		cr.taken[foldKey(c.nickname)] = struct{}{}
	}

	cr.broadcast <- inbound{from: a, text: "bye"}
	cr.leave(a) // panics with "close of closed channel" if a is removed twice

	expect(t, b, typeChat, "a", "bye")
	expect(t, b, typeLeave, "a", "") // announced once, by the drain's publish
	expectEmpty(t, b)
}

// CHAT-02: shutdown closes every send channel without leave notices.
func TestShutdownIsSilent(t *testing.T) {
	cr, cancel := startRoom(t)
	a := newTestClient("a", 8)
	b := newTestClient("b", 8)
	register(t, cr, a)
	register(t, cr, b)
	cancel()
	for _, c := range []*Client{a, b} {
		for {
			select {
			case raw, ok := <-c.send:
				if !ok {
					goto closed
				}
				var peek struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal(raw, &peek); err != nil {
					t.Fatalf("invalid JSON %q: %v", raw, err)
				}
				if peek.Type == typeLeave {
					t.Fatalf("unexpected leave frame on shutdown: %s", raw)
				}
				continue
			case <-time.After(testDeadline):
				t.Fatal("timeout waiting for send to close")
			}
		closed:
			break
		}
	}
}

// CHAT-10: ts is unix milliseconds taken once per event.
func TestTimestampWindowAndSharedAcrossRecipients(t *testing.T) {
	cr, _ := startRoom(t)
	a := newTestClient("a", 8)
	b := newTestClient("b", 8)
	before := time.Now().UnixMilli()
	register(t, cr, a)
	register(t, cr, b)
	expectWelcome(t, a, "a")
	next(t, a) // join a
	joinA := next(t, a)
	expectWelcome(t, b, "b")
	joinB := next(t, b)
	if joinA.Type != typeJoin || joinB.Type != typeJoin || joinA.TS != joinB.TS {
		t.Fatalf("join ts differ across recipients: %+v vs %+v", joinA, joinB)
	}

	broadcast(t, cr, a, "hi")
	chatA, chatB := next(t, a), next(t, b)
	after := time.Now().UnixMilli()
	if chatA.TS != chatB.TS {
		t.Fatalf("chat ts differ across recipients: %d vs %d", chatA.TS, chatB.TS)
	}
	for _, m := range []message{joinA, chatA} {
		if m.TS < before || m.TS > after {
			t.Fatalf("ts %d outside [%d, %d]", m.TS, before, after)
		}
	}
}

// CHAT-11/17/18: a newcomer gets welcome, users and history (one shared ts)
// before its own join; existing members get only the join.
func TestJoinSequence(t *testing.T) {
	cr, _ := startRoom(t)
	a := newTestClient("a", 16)
	register(t, cr, a)
	expectWelcome(t, a, "a")
	expect(t, a, typeJoin, "a", "")
	broadcast(t, cr, a, "m1")
	broadcast(t, cr, a, "m2")
	m1 := next(t, a)
	m2 := next(t, a)
	if m1.Text != "m1" || m2.Text != "m2" {
		t.Fatalf("a got %q, %q, want m1, m2", m1.Text, m2.Text)
	}

	b := newTestClient("b", 16)
	register(t, cr, b)
	users, hist := expectWelcome(t, b, "b")
	expectUsers(t, users, "a", "b")
	expectHistory(t, hist,
		message{Type: typeChat, From: "a", Text: "m1"},
		message{Type: typeChat, From: "a", Text: "m2"})
	if hist.Messages[0].TS != m1.TS || hist.Messages[1].TS != m2.TS {
		t.Fatalf("replayed ts = %d, %d, want original %d, %d", hist.Messages[0].TS, hist.Messages[1].TS, m1.TS, m2.TS)
	}
	join := next(t, b)
	if join.Type != typeJoin || join.From != "b" || join.TS != users.TS {
		t.Fatalf("own join = %+v, want join b with ts %d", join, users.TS)
	}

	expect(t, a, typeJoin, "b", "") // a gets no private frames
	broadcast(t, cr, a, "sync")
	expect(t, a, typeChat, "a", "sync")
}

// CHAT-17: the snapshot is ordered case-insensitively.
func TestUsersSortedCaseInsensitive(t *testing.T) {
	cr, _ := startRoom(t)
	var last *Client
	for _, n := range []string{"bob", "Alice", "carol"} {
		last = newTestClient(n, 16)
		register(t, cr, last)
	}
	users, _ := expectWelcome(t, last, "carol")
	expectUsers(t, users, "Alice", "bob", "carol")
}

// CHAT-17/18: users and history have exactly type, array and ts; empty arrays
// are [] and not null.
func TestJoinFramesExactShape(t *testing.T) {
	cr, _ := startRoom(t)
	a := newTestClient("a", 8)
	register(t, cr, a)
	next(t, a) // welcome
	for _, want := range []string{`{"type":"users","users":["a"],"ts":`, `{"type":"history","messages":[],"ts":`} {
		raw := string(nextRaw(t, a))
		if !strings.HasPrefix(raw, want) || !strings.HasSuffix(raw, "}") {
			t.Fatalf("frame %s does not match %s...}", raw, want)
		}
	}
}

// CHAT-18: join, leave and welcome frames never enter the history.
func TestHistoryOnlyChat(t *testing.T) {
	cr, _ := startRoom(t)
	a := newTestClient("a", 32)
	b := newTestClient("b", 32)
	register(t, cr, a)
	register(t, cr, b)
	broadcast(t, cr, a, "one")
	broadcast(t, cr, b, "two")
	expectWelcome(t, a, "a")
	for { // wait until both chats have been relayed
		if m := next(t, a); m.Type == typeChat && m.Text == "two" {
			break
		}
	}
	unregister(t, cr, b)
	expectClosed(t, b)

	d := newTestClient("d", 8)
	register(t, cr, d)
	_, hist := expectWelcome(t, d, "d")
	expectHistory(t, hist,
		message{Type: typeChat, From: "a", Text: "one"},
		message{Type: typeChat, From: "b", Text: "two"})
}

// CHAT-18: only the last historySize chats are replayed, oldest first.
func TestHistoryKeepsLast50(t *testing.T) {
	cr, _ := startRoom(t)
	a := newTestClient("a", 64)
	register(t, cr, a)
	expectWelcome(t, a, "a")
	expect(t, a, typeJoin, "a", "")
	const total = historySize + 1
	for i := 0; i < total; i++ {
		broadcast(t, cr, a, strconv.Itoa(i))
	}
	for i := 0; i < total; i++ { // all relayed before b registers
		expect(t, a, typeChat, "a", strconv.Itoa(i))
	}

	b := newTestClient("b", 8)
	register(t, cr, b)
	_, hist := expectWelcome(t, b, "b")
	want := make([]message, 0, historySize)
	for i := 1; i < total; i++ {
		want = append(want, message{Type: typeChat, From: "a", Text: strconv.Itoa(i)})
	}
	expectHistory(t, hist, want...)
}

// CHAT-02/05: a slow client dropped by a fan-out is announced after the
// triggering event, and the leaves it causes are announced too.
func TestSlowDropCascadeAnnounced(t *testing.T) {
	cr := NewRoom("general")
	o := newTestClient("o", 16)
	x := newTestClient("x", 0) // dropped by the chat
	y := newTestClient("y", 1) // takes the chat, dropped by x's leave
	for _, c := range []*Client{o, x, y} {
		cr.clients[c] = struct{}{}
		cr.taken[foldKey(c.nickname)] = struct{}{}
	}

	cr.relay(inbound{from: o, text: "hi"})

	expect(t, o, typeChat, "o", "hi")
	expect(t, o, typeLeave, "x", "")
	expect(t, o, typeLeave, "y", "")
	expectEmpty(t, o)
	expectClosed(t, x)
	expectClosed(t, y)
	if len(cr.clients) != 1 || len(cr.dropped) != 0 {
		t.Fatalf("clients = %d, pending drops = %d, want 1 and 0", len(cr.clients), len(cr.dropped))
	}
}

// A newcomer dropped before its join was fanned out is never announced, and
// its name is free again.
func TestNewcomerDroppedDuringJoinIsSilent(t *testing.T) {
	cr, _ := startRoom(t)
	obs := newTestClient("obs", 16)
	register(t, cr, obs)
	expectWelcome(t, obs, "obs")
	expect(t, obs, typeJoin, "obs", "")

	n := newTestClient("n", privateFrames-1) // welcome and users fit, history overflows
	register(t, cr, n)
	broadcast(t, cr, obs, "sync")           // processed after n's admit; n is not read before that
	expect(t, obs, typeChat, "obs", "sync") // no join or leave for n before it
	expectClosed(t, n)

	again := newTestClient("n", 8)
	register(t, cr, again)
	users, _ := expectWelcome(t, again, "n")
	expectUsers(t, users, "n", "obs") // the dropped newcomer appears once, as the new "n"
}

func TestNewRoomDefaults(t *testing.T) {
	cr := NewRoom("general")
	if cr.id != "general" {
		t.Errorf("id = %q, want general", cr.id)
	}
	if cr.maxMessageSize != 4096 {
		t.Errorf("maxMessageSize = %d, want 4096", cr.maxMessageSize)
	}
	if cr.writeWait != 10*time.Second {
		t.Errorf("writeWait = %v, want 10s", cr.writeWait)
	}
	if cr.pongWait != 60*time.Second {
		t.Errorf("pongWait = %v, want 60s", cr.pongWait)
	}
	if cr.pingPeriod != cr.pongWait*9/10 {
		t.Errorf("pingPeriod = %v, want %v", cr.pingPeriod, cr.pongWait*9/10)
	}
	if cap(cr.broadcast) != sendBufferSize {
		t.Errorf("broadcast cap = %d, want %d", cap(cr.broadcast), sendBufferSize)
	}
}

func TestNormalizeNickname(t *testing.T) {
	tests := []struct {
		name, in, want string
	}{
		{"plain", "alice", "alice"},
		{"empty", "", "Anonymous"},
		{"whitespace only", " \t ", "Anonymous"},
		{"trimmed", "  bob  ", "bob"},
		{"invalid utf8", "\xff", "\uFFFD"},
		{"invalid utf8 inside", "a\xffb", "a\uFFFDb"},
		{"exactly 32 runes", strings.Repeat("a", 32), strings.Repeat("a", 32)},
		{"40 ascii truncated", strings.Repeat("a", 40), strings.Repeat("a", 32)},
		{"40 multibyte truncated by runes", strings.Repeat("é", 40), strings.Repeat("é", 32)},
		{"trailing space after truncation", strings.Repeat("a", 31) + " bbb", strings.Repeat("a", 31)},
		{"newline stripped", "Bob\nAdmin", "BobAdmin"},
		{"tab and CR stripped", "\tAlice\r", "Alice"},
		{"NUL stripped", "a\x00b", "ab"},
		{"DEL stripped", "a\x7fb", "ab"},
		{"C1 NEL stripped", "Zoe\u0085", "Zoe"},
		{"RLO stripped", "Eve\u202eevil", "Eveevil"},
		{"isolates stripped", "\u2066Mallory\u2069", "Mallory"},
		{"LRM and RLM stripped", "a\u200eb\u200fc", "abc"},
		{"embeddings stripped", "a\u202ab\u202bc\u202cd\u202d", "abcd"},
		{"Arabic letter mark stripped", "Ali\u061Cce", "Alice"},
		{"strip before trim", "\u202e  Bob  \u202c", "Bob"},
		{"leading space after strip", "\u202e bob", "bob"},
		{"ZWJ emoji kept", "\U0001F469\u200D\U0001F4BB", "\U0001F469\u200D\U0001F4BB"},
		{"controls only", "\n\u202e\t", "Anonymous"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeNickname(tt.in); got != tt.want {
				t.Fatalf("normalizeNickname(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFoldKeyMatchesEqualFold(t *testing.T) {
	tests := []struct{ a, b string }{
		{"Bob", "bOB"},
		{"bob", "bobby"},
		{"\u03c3", "\u03c2"}, // sigma, final sigma
		{"\u03c3", "\u03a3"},
		{"\u03c2", "\u03a3"},
		{"s", "\u017f"},  // long s
		{"k", "\u212a"},  // Kelvin sign
		{"\u00df", "ss"}, // sharp s is not simple-fold equal to ss
		{"\u00e9", "\u00c9"},
		{"\u00e9", "e"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.a+"/"+tt.b, func(t *testing.T) {
			want := strings.EqualFold(tt.a, tt.b)
			if got := foldKey(tt.a) == foldKey(tt.b); got != want {
				t.Fatalf("foldKey equality(%q, %q) = %v, EqualFold = %v", tt.a, tt.b, got, want)
			}
		})
	}
}

func TestAssignNickname(t *testing.T) {
	a32 := strings.Repeat("a", 32)
	spaced := strings.Repeat("a", 27) + " bbbb"
	suffixed := func(base string, from, to int) []string {
		var out []string
		for n := from; n <= to; n++ {
			out = append(out, truncateRunes(base, maxNicknameRunes-len(" ()")-len(strconv.Itoa(n)))+" ("+strconv.Itoa(n)+")")
		}
		return out
	}
	tests := []struct {
		name  string
		taken []string
		base  string
		want  string
	}{
		{"free", []string{"alice"}, "bob", "bob"},
		{"case-insensitive collision", []string{"Bob"}, "bob", "bob (2)"},
		{"suffix taken", []string{"Bob", "Bob (2)"}, "Bob", "Bob (3)"},
		{"lowest free suffix", []string{"Bob", "Bob (3)"}, "Bob", "Bob (2)"},
		{"32 runes truncated to 28", []string{a32}, a32, strings.Repeat("a", 28) + " (2)"},
		{"two-digit suffix", append([]string{a32}, suffixed(a32, 2, 9)...), a32, strings.Repeat("a", 27) + " (10)"},
		{"truncation lands on space", []string{spaced}, spaced, strings.Repeat("a", 27) + " (2)"},
		{"default name", []string{"Anonymous"}, "Anonymous", "Anonymous (2)"},
		{"literal suffixed request", []string{"Bob", "Bob (2)"}, "bob (2)", "bob (2) (2)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := NewRoom("general")
			for _, n := range tt.taken {
				cr.taken[foldKey(n)] = struct{}{}
			}
			got := cr.assignNickname(tt.base)
			if got != tt.want {
				t.Fatalf("assignNickname(%q) = %q, want %q", tt.base, got, tt.want)
			}
			if n := utf8.RuneCountInString(got); n > maxNicknameRunes {
				t.Fatalf("result has %d runes", n)
			}
		})
	}
}

// dm queues a direct message as if parseInbound had classified it from from's connection.
func dm(t *testing.T, cr *Room, from *Client, to, text string) {
	t.Helper()
	select {
	case cr.broadcast <- inbound{from: from, kind: inboundDM, to: cleanNickname(to), text: text}:
	case <-time.After(testDeadline):
		t.Fatal("timeout queueing dm")
	}
}

// exactFields requires b to be a JSON object with exactly the given keys.
func exactFields(t *testing.T, b []byte, keys ...string) map[string]json.RawMessage {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatalf("invalid JSON %q: %v", b, err)
	}
	for _, k := range keys {
		if _, ok := fields[k]; !ok {
			t.Fatalf("frame %q misses field %q", b, k)
		}
	}
	if len(fields) != len(keys) {
		t.Fatalf("frame %q has %d fields, want %d", b, len(fields), len(keys))
	}
	return fields
}

// decodeDM parses a dm frame with exactly type, from, to, text and ts.
func decodeDM(t *testing.T, b []byte) dmFrame {
	t.Helper()
	exactFields(t, b, "type", "from", "to", "text", "ts")
	var f dmFrame
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("frame %q: %v", b, err)
	}
	if f.Type != typeDM {
		t.Fatalf("frame %q: type %q, want dm", b, f.Type)
	}
	return f
}

// decodeDMError parses a dm_error frame with exactly type, to, reason and ts.
func decodeDMError(t *testing.T, b []byte) dmErrorFrame {
	t.Helper()
	exactFields(t, b, "type", "to", "reason", "ts")
	var f dmErrorFrame
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("frame %q: %v", b, err)
	}
	if f.Type != typeDMError {
		t.Fatalf("frame %q: type %q, want dm_error", b, f.Type)
	}
	return f
}

// expectDM waits for the next frame on c and requires it to be the given dm.
func expectDM(t *testing.T, c *Client, from, to, text string) dmFrame {
	t.Helper()
	got := decodeDM(t, nextRaw(t, c))
	if got.From != from || got.To != to || got.Text != text {
		t.Fatalf("dm = (%q, %q, %q), want (%q, %q, %q)", got.From, got.To, got.Text, from, to, text)
	}
	return got
}

// expectDMError waits for the next frame on c and requires it to be the given dm_error.
func expectDMError(t *testing.T, c *Client, to, reason string) {
	t.Helper()
	got := decodeDMError(t, nextRaw(t, c))
	if got.To != to || got.Reason != reason {
		t.Fatalf("dm_error = (%q, %q), want (%q, %q)", got.To, got.Reason, to, reason)
	}
}

// joinAll registers cs in order and consumes every frame the joins produce, so
// afterwards all queues are empty. Names are read after registration.
func joinAll(t *testing.T, cr *Room, cs ...*Client) {
	t.Helper()
	for i, c := range cs {
		register(t, cr, c)
		select {
		case <-c.registered:
		case <-time.After(testDeadline):
			t.Fatal("timeout waiting for registration")
		}
		expectWelcome(t, c, c.nickname)
		for _, o := range cs[:i+1] {
			expect(t, o, typeJoin, c.nickname, "")
		}
	}
}

// CHAT-22: the recipient and the sender get the same dm; nobody else gets anything.
func TestDMDeliveredAndEchoed(t *testing.T) {
	cr, _ := startRoom(t)
	a, bob, c := newTestClient("A", 16), newTestClient("Bob", 16), newTestClient("C", 16)
	joinAll(t, cr, a, bob, c)

	before := time.Now().UnixMilli()
	dm(t, cr, a, "  bob ", "secreto")
	toBob := expectDM(t, bob, "A", "Bob", "secreto")
	toA := expectDM(t, a, "A", "Bob", "secreto")
	after := time.Now().UnixMilli()
	if toBob.TS != toA.TS || toBob.TS < before || toBob.TS > after {
		t.Fatalf("ts bob=%d a=%d, want equal and in [%d, %d]", toBob.TS, toA.TS, before, after)
	}

	broadcast(t, cr, c, "marker")
	expect(t, c, typeChat, "C", "marker") // a leaked dm would have come first
	expectEmpty(t, c)
}

// CHAT-22: "Ana (2)" is its own recipient.
func TestDMSuffixedRecipient(t *testing.T) {
	cr, _ := startRoom(t)
	ana, ana2, a := newTestClient("Ana", 16), newTestClient("Ana", 16), newTestClient("A", 16)
	joinAll(t, cr, ana, ana2, a)

	dm(t, cr, a, "Ana (2)", "hi")
	expectDM(t, ana2, "A", "Ana (2)", "hi")
	expectDM(t, a, "A", "Ana (2)", "hi")
	broadcast(t, cr, a, "marker")
	expect(t, a, typeChat, "A", "marker")
	expect(t, ana, typeChat, "A", "marker") // the first frame ana gets: no dm before it
	expectEmpty(t, ana)
}

// CHAT-23: errors go to the sender only and keep it in the room.
func TestDMErrors(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := newTestClient("Ana", 16), newTestClient("B", 16)
	joinAll(t, cr, a, b)

	select {
	case cr.broadcast <- inbound{from: a, kind: inboundBadDM}:
	case <-time.After(testDeadline):
		t.Fatal("timeout")
	}
	expectDMError(t, a, "", reasonInvalid)
	dm(t, cr, a, "ANA", "x")
	expectDMError(t, a, "ANA", reasonSelf)
	dm(t, cr, a, "Zoe", "x")
	expectDMError(t, a, "Zoe", reasonOffline)

	broadcast(t, cr, a, "still here")
	expect(t, a, typeChat, "Ana", "still here")
	expect(t, b, typeChat, "Ana", "still here") // no dm_error or dm came first
}

// CHAT-24, CHAT-18: a dm leaves no trace in history.
func TestDMNotInHistory(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := newTestClient("A", 16), newTestClient("B", 16)
	joinAll(t, cr, a, b)

	broadcast(t, cr, a, "hola")
	dm(t, cr, a, "B", "secreto")
	expect(t, a, typeChat, "A", "hola")
	expectDM(t, a, "A", "B", "secreto")
	unregister(t, cr, b)

	c := newTestClient("C", 8)
	register(t, cr, c)
	_, hist := expectWelcome(t, c, "C")
	expectHistory(t, hist, message{Type: typeChat, From: "A", Text: "hola"})
}

// CHAT-24: a reused nickname inherits no earlier dm and receives new ones.
func TestReusedNicknameInheritsNothing(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := newTestClient("A", 16), newTestClient("B", 16)
	joinAll(t, cr, a, b)
	dm(t, cr, a, "B", "one")
	expectDM(t, a, "A", "B", "one")
	unregister(t, cr, b)
	expect(t, a, typeLeave, "B", "")

	// CHAT-23: the recipient left, so it is offline.
	dm(t, cr, a, "B", "x")
	expectDMError(t, a, "B", reasonOffline)

	b2 := newTestClient("b", 16)
	register(t, cr, b2)
	expectWelcome(t, b2, "b")
	expect(t, b2, typeJoin, "b", "")
	expectEmpty(t, b2)

	dm(t, cr, a, "B", "two")
	expectDM(t, b2, "A", "b", "two")
}

// CHAT-22: a dm from a client that was dropped is discarded.
func TestDMFromNonMemberDropped(t *testing.T) {
	cr, _ := startRoom(t)
	slow := newTestClient("slow", privateFrames+1)
	ok := newTestClient("ok", 16)
	register(t, cr, slow)
	register(t, cr, ok)
	expectWelcome(t, ok, "ok")
	expect(t, ok, typeJoin, "ok", "")
	expect(t, ok, typeLeave, "slow", "")
	expectClosed(t, slow)

	dm(t, cr, slow, "ok", "ghost")
	broadcast(t, cr, ok, "sync")
	expect(t, ok, typeChat, "ok", "sync") // a dm or dm_error would have come first
}

// A dm queued right before unregister is routed before the leave (drain uses dispatch).
func TestDMBeforeLeaveOnDisconnect(t *testing.T) {
	cr := NewRoom("general")
	a, b := newTestClient("a", 8), newTestClient("b", 8)
	for _, c := range []*Client{a, b} {
		cr.clients[c] = struct{}{}
		cr.taken[foldKey(c.nickname)] = struct{}{}
	}

	cr.broadcast <- inbound{from: a, kind: inboundDM, to: "b", text: "bye"}
	cr.leave(a)

	expectDM(t, b, "a", "b", "bye")
	expect(t, b, typeLeave, "a", "")
}

// CHAT-22, CHAT-05: a recipient that cannot take its copy is dropped and announced; the echo still arrives.
func TestDMSlowRecipientDropped(t *testing.T) {
	cr := NewRoom("general")
	o, a, x := newTestClient("o", 16), newTestClient("a", 16), newTestClient("x", 0)
	for _, c := range []*Client{o, a, x} {
		cr.clients[c] = struct{}{}
		cr.taken[foldKey(c.nickname)] = struct{}{}
	}

	cr.dispatch(inbound{from: a, kind: inboundDM, to: "x", text: "hi"})

	expectClosed(t, x)
	expectDM(t, a, "a", "x", "hi")
	expect(t, a, typeLeave, "x", "")
	expect(t, o, typeLeave, "x", "")
	expectEmpty(t, o)
}

// lockedBuffer is a log sink safe against writes from goroutines of other tests.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// CHAT-22, CHAT-05: a sender that cannot take its echo is dropped and announced; the recipient still gets the dm.
func TestDMSlowSenderDropped(t *testing.T) {
	cr := NewRoom("general")
	o, r, s := newTestClient("o", 16), newTestClient("r", 16), newTestClient("s", 0)
	for _, c := range []*Client{o, r, s} {
		cr.clients[c] = struct{}{}
		cr.taken[foldKey(c.nickname)] = struct{}{}
	}

	cr.dispatch(inbound{from: s, kind: inboundDM, to: "r", text: "hi"})

	expectClosed(t, s)
	expectDM(t, r, "s", "r", "hi")
	expect(t, r, typeLeave, "s", "")
	expect(t, o, typeLeave, "s", "")
	expectEmpty(t, o)
	expectEmpty(t, r)
}

// CHAT-23, CHAT-05: a dm_error to a sender that cannot take it drops only the sender.
func TestDMErrorSlowSenderDropped(t *testing.T) {
	cr := NewRoom("general")
	o, s := newTestClient("o", 16), newTestClient("s", 0)
	for _, c := range []*Client{o, s} {
		cr.clients[c] = struct{}{}
		cr.taken[foldKey(c.nickname)] = struct{}{}
	}

	cr.dispatch(inbound{from: s, kind: inboundDM, to: "Zoe", text: "hi"})

	expectClosed(t, s)
	expect(t, o, typeLeave, "s", "")
	expectEmpty(t, o)
}

// CHAT-24: no log line carries a dm text or recipient.
func TestDMNotLogged(t *testing.T) {
	buf := &lockedBuffer{}
	orig := log.Writer()
	log.SetOutput(buf)
	t.Cleanup(func() { log.SetOutput(orig) })

	cr := NewRoom("general")
	a, b := newTestClient("Ana", 16), newTestClient("Bea", 16)
	for _, c := range []*Client{a, b} {
		cr.clients[c] = struct{}{}
		cr.taken[foldKey(c.nickname)] = struct{}{}
	}
	cr.dispatch(inbound{from: a, kind: inboundDM, to: "Bea", text: "texto-secreto"})
	cr.dispatch(inbound{from: a, kind: inboundDM, to: "Ana", text: "texto-self"})
	cr.dispatch(inbound{from: a, kind: inboundDM, to: "Zoe-offline", text: "texto-off"})
	cr.dispatch(inbound{from: a, kind: inboundBadDM})

	for _, s := range []string{"texto-secreto", "texto-self", "texto-off", "Zoe-offline", "Bea"} {
		if strings.Contains(buf.String(), s) {
			t.Fatalf("log contains %q: %s", s, buf.String())
		}
	}
}
