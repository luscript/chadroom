package chat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"maps"
	"os"
	"reflect"
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
	return runRoom(t, NewRoom("general"))
}

// startMusicRoom runs a room with a shared player (CHAT-30).
func startMusicRoom(t *testing.T) (*Room, context.CancelFunc) {
	t.Helper()
	return runRoom(t, NewMusicRoom("musica"))
}

func runRoom(t *testing.T, cr *Room) (*Room, context.CancelFunc) {
	t.Helper()
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

// decode parses an outbound frame, requiring type, from, text and ts and a known
// type. A chat also carries id (>= 1) and may carry reply with exactly id, from and
// text (CHAT-35); avatar is optional; bio is allowed only on welcome and join (CHAT-10,
// CHAT-37); nothing else is allowed.
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
	var m message
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("frame %q: %v", b, err)
	}
	allowed := map[string]bool{"type": true, "from": true, "text": true, "ts": true, "avatar": true}
	switch m.Type {
	case typeChat:
		allowed["id"], allowed["reply"] = true, true
		if m.ID < 1 {
			t.Fatalf("chat %q has no id >= 1", b)
		}
		if _, ok := fields["reply"]; ok {
			exactFields(t, fields["reply"], "id", "from", "text")
		}
	case typeJoin, typeWelcome:
		allowed["bio"] = true
	case typeLeave:
	default:
		t.Fatalf("unknown type in %q", b)
	}
	for k := range fields {
		if !allowed[k] {
			t.Fatalf("frame %q has unexpected field %q", b, k)
		}
	}
	return m
}

// decodeUsers parses a users frame, requiring type/users/ts (users an array, never
// null) and allowing only the optional avatars and bios objects, whose keys must all
// be listed in users (CHAT-17).
func decodeUsers(t *testing.T, b []byte) usersFrame {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatalf("invalid JSON %q: %v", b, err)
	}
	for _, k := range []string{"type", "users", "ts"} {
		if _, ok := fields[k]; !ok {
			t.Fatalf("frame %q misses field %q", b, k)
		}
	}
	for k := range fields {
		switch k {
		case "type", "users", "ts", "avatars", "bios":
		default:
			t.Fatalf("users frame %q has unexpected field %q", b, k)
		}
	}
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
	for _, m := range []map[string]string{f.Avatars, f.Bios} {
		for nick, v := range m {
			if !slices.Contains(f.Users, nick) || v == "" {
				t.Fatalf("users frame %q: bad entry %q=%q", b, nick, v)
			}
		}
	}
	for _, k := range []string{"avatars", "bios"} {
		if raw, ok := fields[k]; ok && (len(raw) == 0 || raw[0] != '{') {
			t.Fatalf("users frame %q: %s is not an object", b, k)
		}
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
	if cr.player != nil {
		t.Errorf("player = %+v, want nil (no shared player)", cr.player)
	}
}

// CHAT-30: NewMusicRoom is NewRoom plus an empty player.
func TestNewMusicRoom(t *testing.T) {
	cr := NewMusicRoom("musica")
	if cr.id != "musica" || cr.maxMessageSize != 4096 || cap(cr.broadcast) != sendBufferSize {
		t.Errorf("NewMusicRoom differs from NewRoom defaults: %+v", cr)
	}
	if cr.player == nil || !reflect.DeepEqual(*cr.player, player{}) {
		t.Errorf("player = %+v, want an empty player", cr.player)
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

// decodeDM parses a dm frame with exactly type, from, to, text, ts and id, plus reply_to when present.
func decodeDM(t *testing.T, b []byte) dmFrame {
	t.Helper()
	keys := []string{"type", "from", "to", "text", "ts", "id"}
	var probe map[string]json.RawMessage
	if json.Unmarshal(b, &probe) == nil {
		if _, ok := probe["reply_to"]; ok {
			keys = append(keys, "reply_to")
		}
	}
	exactFields(t, b, keys...)
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

// typingSignal queues a typing signal as if parseInbound had classified it from from's connection.
func typingSignal(t *testing.T, cr *Room, from *Client, to string) {
	t.Helper()
	select {
	case cr.broadcast <- inbound{from: from, kind: inboundTyping, to: cleanNickname(to)}:
	case <-time.After(testDeadline):
		t.Fatal("timeout queueing typing signal")
	}
}

// decodeTyping parses a typing frame: type, from, ts, plus to only for a DM signal.
func decodeTyping(t *testing.T, b []byte) typingDMFrame {
	t.Helper()
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(b, &fields); err != nil {
		t.Fatalf("invalid JSON %q: %v", b, err)
	}
	if _, hasTo := fields["to"]; hasTo {
		exactFields(t, b, "type", "from", "to", "ts")
	} else {
		exactFields(t, b, "type", "from", "ts")
	}
	var f typingDMFrame
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("frame %q: %v", b, err)
	}
	if f.Type != typeTyping {
		t.Fatalf("frame %q: type %q, want typing", b, f.Type)
	}
	return f
}

// expectTyping waits for the next frame on c and requires it to be a typing signal;
// to == "" means a room signal, which must not carry a to field.
func expectTyping(t *testing.T, c *Client, from, to string) {
	t.Helper()
	got := decodeTyping(t, nextRaw(t, c))
	if got.From != from || got.To != to {
		t.Fatalf("typing = (%q, %q), want (%q, %q)", got.From, got.To, from, to)
	}
}

// CHAT-26: a room signal reaches every member except its sender.
func TestTypingRoomExcludesSender(t *testing.T) {
	cr, _ := startRoom(t)
	a, b, c := newTestClient("A", 16), newTestClient("B", 16), newTestClient("C", 16)
	joinAll(t, cr, a, b, c)

	typingSignal(t, cr, a, "")
	expectTyping(t, b, "A", "")
	expectTyping(t, c, "A", "")

	broadcast(t, cr, a, "marker")
	expect(t, a, typeChat, "A", "marker") // no echo of the typing signal
}

// CHAT-26: a DM signal reaches only the recipient, found case-insensitively, with the assigned name in to.
func TestTypingDirectRecipientOnly(t *testing.T) {
	cr, _ := startRoom(t)
	a, bob, c := newTestClient("A", 16), newTestClient("Bob", 16), newTestClient("C", 16)
	joinAll(t, cr, a, bob, c)

	typingSignal(t, cr, a, "  bob ")
	expectTyping(t, bob, "A", "Bob")

	broadcast(t, cr, c, "marker")
	expect(t, a, typeChat, "C", "marker")
	expect(t, c, typeChat, "C", "marker")
}

// CHAT-26: a signal to oneself or to an offline nickname produces no frame and no dm_error.
func TestTypingSelfAndOfflineSilent(t *testing.T) {
	cr, _ := startRoom(t)
	ana, b := newTestClient("Ana", 16), newTestClient("B", 16)
	joinAll(t, cr, ana, b)

	typingSignal(t, cr, ana, "ANA")
	typingSignal(t, cr, ana, "Zoe")
	broadcast(t, cr, b, "marker")
	expect(t, ana, typeChat, "B", "marker")
	expect(t, b, typeChat, "B", "marker")
}

// CHAT-26: a signal from a client that was dropped is discarded.
func TestTypingFromNonMemberDropped(t *testing.T) {
	cr, _ := startRoom(t)
	slow := newTestClient("slow", privateFrames+1)
	ok := newTestClient("ok", 16)
	register(t, cr, slow)
	register(t, cr, ok)
	expectWelcome(t, ok, "ok")
	expect(t, ok, typeJoin, "ok", "")
	expect(t, ok, typeLeave, "slow", "")
	expectClosed(t, slow)

	typingSignal(t, cr, slow, "")
	typingSignal(t, cr, slow, "ok")
	broadcast(t, cr, ok, "sync")
	expect(t, ok, typeChat, "ok", "sync") // a typing frame would have come first
}

// CHAT-26, CHAT-18: typing signals never enter the history.
func TestTypingNotInHistory(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := newTestClient("A", 16), newTestClient("B", 16)
	joinAll(t, cr, a, b)

	typingSignal(t, cr, a, "")
	broadcast(t, cr, a, "hola")
	expectTyping(t, b, "A", "")
	expect(t, b, typeChat, "A", "hola")

	c := newTestClient("C", 8)
	register(t, cr, c)
	_, hist := expectWelcome(t, c, "C")
	expectHistory(t, hist, message{Type: typeChat, From: "A", Text: "hola"})
}

// CHAT-26, CHAT-05: a full buffer skips the signal; the member is neither removed nor announced as left.
func TestTypingFullBufferSkips(t *testing.T) {
	cr := NewRoom("general")
	o, a, b := newTestClient("o", 16), newTestClient("a", 16), newTestClient("b", 1)
	for _, c := range []*Client{o, a, b} {
		cr.clients[c] = struct{}{}
		cr.taken[foldKey(c.nickname)] = struct{}{}
	}
	b.send <- []byte("filler")

	cr.dispatch(inbound{from: a, kind: inboundTyping})
	cr.dispatch(inbound{from: a, kind: inboundTyping, to: "b"})

	if _, ok := cr.clients[b]; !ok {
		t.Fatal("b was removed from clients")
	}
	if _, ok := cr.taken[foldKey("b")]; !ok {
		t.Fatal("b's nickname was released")
	}
	expectTyping(t, o, "a", "")
	expectEmpty(t, o) // no leave for b
	if got := nextRaw(t, b); string(got) != "filler" {
		t.Fatalf("b got %q, want filler", got)
	}
	expectEmpty(t, b)
	expectEmpty(t, a)
}

// CHAT-26: no log line carries a typing signal's recipient.
func TestTypingNotLogged(t *testing.T) {
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
	cr.dispatch(inbound{from: a, kind: inboundTyping})
	cr.dispatch(inbound{from: a, kind: inboundTyping, to: "Bea"})
	cr.dispatch(inbound{from: a, kind: inboundTyping, to: "Ana"})
	cr.dispatch(inbound{from: a, kind: inboundTyping, to: "Zoe-offline"})

	if buf.String() != "" {
		t.Fatalf("typing logged: %s", buf.String())
	}
}

// avatarClient is newTestClient with an avatar; the hub treats it as an opaque string.
func avatarClient(nick, avatar string) *Client {
	c := newTestClient(nick, 16)
	c.avatar = avatar
	return c
}

// expectAvatarFrame requires the next frame on c to carry exactly keys and returns the raw fields.
func expectAvatarFrame(t *testing.T, c *Client, keys ...string) map[string]json.RawMessage {
	t.Helper()
	return exactFields(t, nextRaw(t, c), keys...)
}

// CHAT-28: welcome and join carry the avatar only when the client has one.
func TestAvatarOnWelcomeAndJoin(t *testing.T) {
	cr, _ := startRoom(t)
	obs := newTestClient("obs", 16)
	joinAll(t, cr, obs)

	a := avatarClient("a", "pic-a")
	register(t, cr, a)
	w := expectAvatarFrame(t, a, "type", "from", "text", "ts", "avatar")
	if string(w["avatar"]) != `"pic-a"` {
		t.Fatalf("welcome avatar = %s", w["avatar"])
	}
	exactFields(t, nextRaw(t, a), "type", "users", "avatars", "ts")
	decodeHistory(t, nextRaw(t, a))
	j := exactFields(t, nextRaw(t, a), "type", "from", "text", "ts", "avatar")
	if string(j["avatar"]) != `"pic-a"` || string(j["type"]) != `"join"` {
		t.Fatalf("own join = %v", j)
	}
	j = exactFields(t, nextRaw(t, obs), "type", "from", "text", "ts", "avatar")
	if string(j["avatar"]) != `"pic-a"` || string(j["type"]) != `"join"` {
		t.Fatalf("join seen by obs = %v", j)
	}

	b := newTestClient("b", 16)
	register(t, cr, b)
	if w := next(t, b); w.Type != typeWelcome || w.From != "b" { // decode: exactly 4 fields, no avatar
		t.Fatalf("got %+v, want welcome b", w)
	}
	skip(t, b, 2) // users (lists a's avatar) and history
	expect(t, b, typeJoin, "b", "")
}

// CHAT-28: the users frame maps only members that have an avatar and is omitted when nobody has one.
func TestUsersFrameAvatars(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := avatarClient("a", "pic-a"), newTestClient("b", 16)
	register(t, cr, a)
	register(t, cr, b)
	skip(t, a, privateFrames+2) // private frames, own join, b's join

	c := avatarClient("c", "pic-c")
	register(t, cr, c)
	nextRaw(t, c) // welcome
	f := exactFields(t, nextRaw(t, c), "type", "users", "avatars", "ts")
	var got map[string]string
	if err := json.Unmarshal(f["avatars"], &got); err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"a": "pic-a", "c": "pic-c"}; !maps.Equal(got, want) {
		t.Fatalf("avatars = %v, want %v", got, want)
	}

	// A newcomer without a picture still receives the avatars of the members that have one.
	cr3, _ := startRoom(t)
	register(t, cr3, avatarClient("p", "pic-p"))
	q := newTestClient("q", 16)
	register(t, cr3, q)
	nextRaw(t, q) // welcome
	f = exactFields(t, nextRaw(t, q), "type", "users", "avatars", "ts")
	got = nil
	if err := json.Unmarshal(f["avatars"], &got); err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"p": "pic-p"}; !maps.Equal(got, want) {
		t.Fatalf("avatars for a newcomer without one = %v, want %v", got, want)
	}

	cr2, _ := startRoom(t)
	x, y := newTestClient("x", 16), newTestClient("y", 16)
	joinAll(t, cr2, x, y) // expectWelcome -> decodeUsers: exactly type, users, ts
}

// skip discards the next n frames queued on c.
func skip(t *testing.T, c *Client, n int) {
	t.Helper()
	for range n {
		nextRaw(t, c)
	}
}

// CHAT-28: chat frames and their history entries keep the sender's avatar, even after it leaves.
func TestChatAndHistoryKeepAvatar(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := avatarClient("a", "pic-a"), newTestClient("b", 16)
	register(t, cr, a)
	register(t, cr, b)
	skip(t, a, privateFrames+2) // private frames, own join, b's join
	skip(t, b, privateFrames+1) // private frames, own join

	broadcast(t, cr, a, "from a")
	for _, c := range []*Client{a, b} {
		f := expectAvatarFrame(t, c, "type", "from", "text", "ts", "avatar", "id")
		if string(f["avatar"]) != `"pic-a"` {
			t.Fatalf("chat avatar = %s", f["avatar"])
		}
	}
	broadcast(t, cr, b, "from b")
	expect(t, a, typeChat, "b", "from b") // decode: no avatar field
	expect(t, b, typeChat, "b", "from b")

	unregister(t, cr, a)
	expect(t, b, typeLeave, "a", "")

	late := newTestClient("late", 16)
	register(t, cr, late)
	nextRaw(t, late) // welcome
	nextRaw(t, late) // users
	var hist struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(nextRaw(t, late), &hist); err != nil || len(hist.Messages) != 2 {
		t.Fatalf("history = %+v, err %v", hist, err)
	}
	f := exactFields(t, hist.Messages[0], "type", "from", "text", "ts", "avatar", "id")
	if string(f["avatar"]) != `"pic-a"` {
		t.Fatalf("history avatar = %s", f["avatar"])
	}
	decode(t, hist.Messages[1]) // b has none: exactly four fields
}

// CHAT-28: leave, dm, dm_error and typing frames never carry an avatar.
func TestAvatarNotOnLeaveDMTyping(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := avatarClient("a", "pic-a"), newTestClient("b", 16)
	register(t, cr, a)
	register(t, cr, b)
	skip(t, a, privateFrames+2)
	skip(t, b, privateFrames+1)

	dm(t, cr, a, "b", "psst")
	expectDM(t, b, "a", "b", "psst")
	expectDM(t, a, "a", "b", "psst")
	dm(t, cr, a, "nobody", "x")
	expectDMError(t, a, "nobody", reasonOffline)
	typingSignal(t, cr, a, "")
	expectTyping(t, b, "a", "")
	typingSignal(t, cr, a, "b")
	expectTyping(t, b, "a", "b")

	unregister(t, cr, a)
	if m := next(t, b); m.Type != typeLeave || m.From != "a" { // decode: exactly four fields
		t.Fatalf("got %+v, want leave a", m)
	}
}

// CHAT-28: a nickname reused after its owner left inherits no avatar.
func TestReusedNicknameNoStaleAvatar(t *testing.T) {
	cr, _ := startRoom(t)
	obs, bob := newTestClient("obs", 16), avatarClient("bob", "pic-bob")
	joinAll(t, cr, obs)
	register(t, cr, bob)
	skip(t, obs, 1) // bob's join
	unregister(t, cr, bob)
	expect(t, obs, typeLeave, "bob", "")

	bob2 := newTestClient("bob", 16)
	register(t, cr, bob2)
	expectWelcome(t, bob2, "bob") // users frame passes decodeUsers: no avatars key
	expect(t, bob2, typeJoin, "bob", "")
	expect(t, obs, typeJoin, "bob", "")
}

// decodeMusic parses a music frame with exactly type, video, playing, pos, by, seq, queue and ts.
func decodeMusic(t *testing.T, b []byte) musicFrame {
	t.Helper()
	fields := exactFields(t, b, "type", "video", "playing", "pos", "by", "seq", "queue", "ts")
	var f musicFrame
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatalf("decode music %q: %v", b, err)
	}
	if f.Type != typeMusic {
		t.Fatalf("type = %q, want music", f.Type)
	}
	for _, k := range []string{"pos", "ts", "seq"} { // integers, not 1.5 or 1e3
		if _, err := strconv.ParseInt(string(fields[k]), 10, 64); err != nil {
			t.Fatalf("%s = %s, want an integer", k, fields[k])
		}
	}
	if !bytes.HasPrefix(fields["queue"], []byte("[")) { // [] when empty, never null
		t.Fatalf("queue = %s, want an array", fields["queue"])
	}
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(fields["queue"], &items); err != nil {
		t.Fatalf("decode queue %s: %v", fields["queue"], err)
	}
	for _, it := range items {
		raw, _ := json.Marshal(it)
		item := exactFields(t, raw, "id", "video", "by")
		if _, err := strconv.ParseInt(string(item["id"]), 10, 64); err != nil {
			t.Fatalf("queue id = %s, want an integer", item["id"])
		}
	}
	return f
}

// expectMusic waits for the next frame on c, requires a music frame with this video,
// playing and by, and returns it so callers can check pos.
func expectMusic(t *testing.T, c *Client, video string, playing bool, by string) musicFrame {
	t.Helper()
	f := decodeMusic(t, nextRaw(t, c))
	if f.Video != video || f.Playing != playing || f.By != by {
		t.Fatalf("music = %+v, want video %q playing %v by %q", f, video, playing, by)
	}
	return f
}

// musicCtl queues a validated music control as if it had been read from from's connection.
func musicCtl(t *testing.T, cr *Room, from *Client, cmd musicCmd) {
	t.Helper()
	select {
	case cr.broadcast <- inbound{from: from, kind: inboundMusic, music: cmd}:
	case <-time.After(testDeadline):
		t.Fatal("timeout queueing music control")
	}
}

const (
	v1 = "dQw4w9WgXcQ"
	v2 = "aaaaaaaaaaa"
	v3 = "bbbbbbbbbbb"
)

var (
	playCmd  = musicCmd{action: musicPlay}
	pauseCmd = musicCmd{action: musicPause}
	skipCmd  = musicCmd{action: musicSkip}
)

func addCmd(video string) musicCmd { return musicCmd{action: musicAdd, video: video} }
func addManyCmd(videos ...string) musicCmd {
	return musicCmd{action: musicAddMany, videos: videos}
}

// idList returns n distinct valid video ids.
func idList(n int) []string {
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("v%010d", i)
	}
	return ids
}
func seekCmd(pos int64) musicCmd  { return musicCmd{action: musicSeek, pos: pos} }
func removeCmd(id int64) musicCmd { return musicCmd{action: musicRemove, id: id} }
func endedCmd(seq int64) musicCmd { return musicCmd{action: musicEnded, seq: seq} }

// queueIDs lists the ids of the queued tracks, never nil.
func queueIDs(q []queueItem) []int64 {
	ids := make([]int64, 0, len(q))
	for _, it := range q {
		ids = append(ids, it.ID)
	}
	return ids
}

// CHAT-30: the player's state machine, with fixed clock readings.
func TestPlayerApply(t *testing.T) {
	t0 := time.Unix(5000, 0)
	at := func(ms int64) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }

	type state struct {
		video    string
		playing  bool
		pos      int64 // position at the step's now
		by       string
		seq      int64
		ids      []int64
		finished bool
	}
	snap := func(p *player, now time.Time) state {
		return state{p.video, p.playing, p.position(now), p.by, p.seq, queueIDs(p.queue), p.finished}
	}

	t.Run("no video", func(t *testing.T) {
		for _, c := range []musicCmd{playCmd, pauseCmd, seekCmd(5), skipCmd, endedCmd(1), removeCmd(1)} {
			p := &player{}
			if p.apply(c, "A", t0) {
				t.Fatalf("%s with no video was accepted", c.action)
			}
			if !reflect.DeepEqual(*p, player{}) {
				t.Fatalf("%s with no video changed the state: %+v", c.action, *p)
			}
		}
	})

	// One player walks through the steps; a rejected step must leave the state untouched.
	steps := []struct {
		name string
		cmd  musicCmd
		by   string
		now  int64 // ms after t0
		ok   bool
		want state // checked at now
	}{
		{"first add starts", addCmd(v1), "A", 1000, true, state{v1, true, 0, "A", 1, []int64{}, false}},
		{"add while current queues", addCmd(v2), "B", 2000, true, state{v1, true, 1000, "A", 1, []int64{1}, false}},
		{"second add gets the next id", addCmd(v3), "C", 3000, true, state{v1, true, 2000, "A", 1, []int64{1, 2}, false}},
		{"remove a missing id", removeCmd(99), "A", 3100, false, state{v1, true, 2100, "A", 1, []int64{1, 2}, false}},
		{"remove an existing id", removeCmd(1), "A", 3500, true, state{v1, true, 2500, "A", 1, []int64{2}, false}},
		{"ids are not reused after a remove", addCmd(v2), "B", 4000, true, state{v1, true, 3000, "A", 1, []int64{2, 3}, false}},
		{"pause", pauseCmd, "B", 5000, true, state{v1, false, 4000, "B", 1, []int64{2, 3}, false}},
		{"skip from paused", skipCmd, "D", 6000, true, state{v3, true, 0, "C", 2, []int64{3}, false}},
		{"ended with a stale seq", endedCmd(1), "A", 7000, false, state{v3, true, 1000, "C", 2, []int64{3}, false}},
		{"ended advances", endedCmd(2), "A", 8000, true, state{v2, true, 0, "B", 3, []int64{}, false}},
		{"ended with an empty queue finishes", endedCmd(3), "A", 10000, true, state{v2, false, 2000, "B", 3, []int64{}, true}},
		{"ended again while finished", endedCmd(3), "A", 10500, false, state{v2, false, 2000, "B", 3, []int64{}, true}},
		{"skip with an empty queue", skipCmd, "A", 11000, false, state{v2, false, 2000, "B", 3, []int64{}, true}},
		{"pause keeps finished", pauseCmd, "D", 11500, true, state{v2, false, 2000, "D", 3, []int64{}, true}},
		{"play clears finished", playCmd, "A", 12000, true, state{v2, true, 2000, "A", 3, []int64{}, false}},
		{"ended finishes again", endedCmd(3), "A", 14000, true, state{v2, false, 4000, "A", 3, []int64{}, true}},
		{"seek clears finished", seekCmd(500), "B", 15000, true, state{v2, false, 500, "B", 3, []int64{}, false}},
		{"play from the seek", playCmd, "B", 16000, true, state{v2, true, 500, "B", 3, []int64{}, false}},
		{"ended finishes a third time", endedCmd(3), "B", 17000, true, state{v2, false, 1500, "B", 3, []int64{}, true}},
		{"add while finished with an empty queue", addCmd(v1), "E", 18000, true, state{v1, true, 0, "E", 4, []int64{}, false}},
	}
	p := &player{}
	for _, s := range steps {
		before := *p
		before.queue = slices.Clone(p.queue)
		got := p.apply(s.cmd, s.by, at(s.now))
		if got != s.ok {
			t.Fatalf("%s: accepted = %v, want %v", s.name, got, s.ok)
		}
		if st := snap(p, at(s.now)); !reflect.DeepEqual(st, s.want) {
			t.Fatalf("%s: state = %+v, want %+v", s.name, st, s.want)
		}
		if !s.ok && !reflect.DeepEqual(*p, before) {
			t.Fatalf("%s: a rejected control changed the state: %+v", s.name, *p)
		}
	}

	t.Run("skip from finished with a queue", func(t *testing.T) {
		p := &player{video: v1, seq: 1, finished: true, at: t0, queue: []queueItem{{7, v2, "B"}}}
		if !p.apply(skipCmd, "A", at(500)) {
			t.Fatal("skip rejected")
		}
		if st := snap(p, at(500)); !reflect.DeepEqual(st, state{v2, true, 0, "B", 2, []int64{}, false}) {
			t.Fatalf("state = %+v", st)
		}
	})

	t.Run("ended while paused advances", func(t *testing.T) {
		p := &player{}
		p.apply(addCmd(v1), "A", t0)
		p.apply(addCmd(v2), "B", at(100))
		p.apply(pauseCmd, "A", at(200))
		if !p.apply(endedCmd(1), "C", at(300)) {
			t.Fatal("ended while paused on an unfinished track was rejected")
		}
		if st := snap(p, at(300)); !reflect.DeepEqual(st, state{v2, true, 0, "B", 2, []int64{}, false}) {
			t.Fatalf("state = %+v", st)
		}
	})

	t.Run("queue cap", func(t *testing.T) {
		p := &player{}
		p.apply(addCmd(v1), "A", t0)
		for i := 0; i < maxMusicQueue; i++ {
			if !p.apply(addCmd(v2), "A", t0) {
				t.Fatalf("add %d rejected", i+1)
			}
		}
		if p.apply(addCmd(v3), "A", t0) {
			t.Fatal("add past the cap was accepted")
		}
		if len(p.queue) != maxMusicQueue || p.nextID != maxMusicQueue {
			t.Fatalf("queue = %d, nextID = %d, want %d", len(p.queue), p.nextID, maxMusicQueue)
		}
		p.apply(removeCmd(1), "A", t0)
		if !p.apply(addCmd(v3), "A", t0) {
			t.Fatal("add after a remove rejected")
		}
		if last := p.queue[len(p.queue)-1]; last.ID != maxMusicQueue+1 {
			t.Fatalf("last id = %d, want %d", last.ID, maxMusicQueue+1)
		}
	})

	t.Run("add_many idle", func(t *testing.T) {
		p := &player{}
		if !p.apply(addManyCmd(v1, v2, v3), "A", t0) {
			t.Fatal("add_many rejected")
		}
		want := state{v1, true, 0, "A", 1, []int64{1, 2}, false}
		if st := snap(p, t0); !reflect.DeepEqual(st, want) {
			t.Fatalf("state = %+v, want %+v", st, want)
		}
		if p.queue[0] != (queueItem{1, v2, "A"}) || p.queue[1] != (queueItem{2, v3, "A"}) {
			t.Fatalf("queue = %+v", p.queue)
		}
	})

	t.Run("add_many while playing", func(t *testing.T) {
		p := &player{}
		p.apply(addCmd(v1), "A", t0)
		p.apply(addCmd(v2), "A", t0) // nextID = 1
		if !p.apply(addManyCmd(v3, v3), "B", at(1000)) {
			t.Fatal("add_many rejected")
		}
		want := state{v1, true, 1000, "A", 1, []int64{1, 2, 3}, false}
		if st := snap(p, at(1000)); !reflect.DeepEqual(st, want) {
			t.Fatalf("state = %+v, want %+v", st, want)
		}
		if p.queue[1].By != "B" || p.queue[2].Video != v3 {
			t.Fatalf("queue = %+v", p.queue)
		}
	})

	t.Run("add_many while paused keeps the state", func(t *testing.T) {
		p := &player{}
		p.apply(addCmd(v1), "A", t0)
		p.apply(pauseCmd, "A", at(500))
		p.apply(addManyCmd(v2), "B", at(900))
		want := state{v1, false, 500, "A", 1, []int64{1}, false}
		if st := snap(p, at(900)); !reflect.DeepEqual(st, want) {
			t.Fatalf("state = %+v, want %+v", st, want)
		}
	})

	t.Run("add_many finished with an empty queue", func(t *testing.T) {
		p := &player{}
		p.apply(addCmd(v1), "A", t0)
		p.apply(endedCmd(1), "A", at(1000)) // finished
		if !p.apply(addManyCmd(v2, v3), "B", at(2000)) {
			t.Fatal("add_many rejected")
		}
		want := state{v2, true, 0, "B", 2, []int64{1}, false}
		if st := snap(p, at(2000)); !reflect.DeepEqual(st, want) {
			t.Fatalf("state = %+v, want %+v", st, want)
		}
	})

	t.Run("add_many partial fit", func(t *testing.T) {
		p := &player{}
		p.apply(addCmd(v1), "A", t0)
		p.apply(addManyCmd(idList(maxMusicQueue-2)...), "A", t0)
		if !p.apply(addManyCmd(idList(4)...), "B", t0) {
			t.Fatal("add_many rejected")
		}
		if len(p.queue) != maxMusicQueue || p.nextID != maxMusicQueue {
			t.Fatalf("queue = %d, nextID = %d, want %d", len(p.queue), p.nextID, maxMusicQueue)
		}
		if n := len(p.queue); p.queue[n-1].Video != idList(4)[1] || p.queue[n-1].By != "B" {
			t.Fatalf("last = %+v, want the second of the batch (the rest dropped)", p.queue[n-1])
		}
	})

	t.Run("add_many full", func(t *testing.T) {
		p := &player{}
		p.apply(addCmd(v1), "A", t0)
		p.apply(addManyCmd(idList(maxMusicAddMany)...), "A", t0)
		if len(p.queue) != maxMusicQueue {
			t.Fatalf("setup: queue = %d", len(p.queue))
		}
		before := *p
		before.queue = slices.Clone(p.queue)
		if p.apply(addManyCmd(v2, v3), "B", at(1000)) {
			t.Fatal("add_many on a full queue was accepted")
		}
		if !reflect.DeepEqual(*p, before) {
			t.Fatalf("a rejected add_many changed the state: %+v", *p)
		}
	})

	t.Run("add_many idle with the maximum", func(t *testing.T) {
		p := &player{}
		if !p.apply(addManyCmd(idList(maxMusicAddMany)...), "A", t0) {
			t.Fatal("add_many rejected")
		}
		if p.video != idList(1)[0] || p.seq != 1 || len(p.queue) != maxMusicAddMany-1 {
			t.Fatalf("video %q seq %d queue %d", p.video, p.seq, len(p.queue))
		}
	})

	t.Run("halt", func(t *testing.T) {
		p := &player{}
		p.apply(addCmd(v1), "A", at(0))
		p.apply(addCmd(v2), "B", at(0))
		p.apply(endedCmd(1), "A", at(1000)) // v2 current, by B, seq 2
		p.apply(addCmd(v3), "C", at(1000))
		p.halt(at(5000)) // freezes 4000 ms in; by, queue, seq and finished unchanged
		if st := snap(p, at(99999)); !reflect.DeepEqual(st, state{v2, false, 4000, "B", 2, []int64{2}, false}) {
			t.Fatalf("halt: %+v", st)
		}
		p.halt(at(99999)) // halting a paused player changes nothing
		if p.pos != 4000 {
			t.Fatalf("second halt moved pos to %d", p.pos)
		}
		f := &player{}
		f.apply(addCmd(v1), "A", at(0))
		f.apply(endedCmd(1), "A", at(1000)) // finished, already paused
		f.halt(at(2000))
		if !f.finished || f.pos != 1000 || f.seq != 1 {
			t.Fatalf("halt on a finished player: %+v", *f)
		}
	})
}

// CHAT-30: the frame has the spec's field order, [] for an empty queue, and its own copy of the queue.
func TestPlayerFrameQueue(t *testing.T) {
	now := time.Unix(5000, 0)
	p := &player{}
	p.apply(addCmd(v1), "A", now)
	f := p.frame(now)
	want := fmt.Sprintf(`{"type":"music","video":"%s","playing":true,"pos":0,"by":"A","seq":1,"queue":[],"ts":%d}`, v1, now.UnixMilli())
	if got := string(encode(f)); got != want {
		t.Fatalf("frame = %s, want %s", got, want)
	}

	p.apply(addCmd(v2), "B", now)
	p.apply(addCmd(v3), "C", now)
	f = p.frame(now)
	wantQ := `"queue":[{"id":1,"video":"aaaaaaaaaaa","by":"B"},{"id":2,"video":"bbbbbbbbbbb","by":"C"}]`
	if got := string(encode(f)); !strings.Contains(got, wantQ) {
		t.Fatalf("frame = %s, want it to contain %s", got, wantQ)
	}

	p.apply(skipCmd, "A", now) // mutates the state's queue after the frame was built
	p.apply(removeCmd(2), "A", now)
	if got := string(encode(f)); !strings.Contains(got, wantQ) {
		t.Fatalf("frame queue changed with the state: %s", got)
	}
}

// CHAT-30: add is broadcast to everyone, the sender included, with exactly the documented fields.
func TestMusicAddBroadcast(t *testing.T) {
	cr, _ := startMusicRoom(t)
	a, b := newTestClient("A", 64), newTestClient("B", 64)
	joinAll(t, cr, a, b)

	start := time.Now()
	musicCtl(t, cr, a, addCmd(v1))
	for _, c := range []*Client{a, b} {
		f := expectMusic(t, c, "dQw4w9WgXcQ", true, "A")
		if f.Seq != 1 || len(f.Queue) != 0 {
			t.Fatalf("first add: seq %d queue %+v, want seq 1 and an empty queue", f.Seq, f.Queue)
		}
		if elapsed := time.Since(start).Milliseconds(); f.Pos < 0 || f.Pos > elapsed {
			t.Fatalf("pos = %d, want within 0..%d", f.Pos, elapsed)
		}
		if f.TS < start.UnixMilli() || f.TS > time.Now().UnixMilli() {
			t.Fatalf("ts = %d outside the test window", f.TS)
		}
	}
	expectEmpty(t, a)
	expectEmpty(t, b)
}

// CHAT-30: pause, seek and play keep the position; by follows the sender.
func TestMusicPauseSeekPlay(t *testing.T) {
	cr, _ := startMusicRoom(t)
	a, b := newTestClient("A", 64), newTestClient("B", 64)
	joinAll(t, cr, a, b)
	both := []*Client{a, b}
	start := time.Now()

	musicCtl(t, cr, a, addCmd(v1))
	for _, c := range both {
		expectMusic(t, c, "dQw4w9WgXcQ", true, "A")
	}
	musicCtl(t, cr, b, pauseCmd)
	var pausedAt int64
	for _, c := range both {
		pausedAt = expectMusic(t, c, "dQw4w9WgXcQ", false, "B").Pos
	}
	musicCtl(t, cr, a, seekCmd(60000))
	for _, c := range both {
		if f := expectMusic(t, c, "dQw4w9WgXcQ", false, "A"); f.Pos != 60000 {
			t.Fatalf("seek pos = %d, want exactly 60000 while paused", f.Pos)
		}
	}
	musicCtl(t, cr, b, playCmd)
	for _, c := range both {
		f := expectMusic(t, c, "dQw4w9WgXcQ", true, "B")
		if f.Pos < 60000 || f.Pos > 60000+time.Since(start).Milliseconds() {
			t.Fatalf("play pos = %d, want 60000 plus a little", f.Pos)
		}
	}
	musicCtl(t, cr, a, seekCmd(5000)) // seeking while playing keeps playing
	for _, c := range both {
		f := expectMusic(t, c, "dQw4w9WgXcQ", true, "A")
		if f.Pos < 5000 || f.Pos > 5000+time.Since(start).Milliseconds() {
			t.Fatalf("seek pos = %d, want 5000 plus a little", f.Pos)
		}
	}
	musicCtl(t, cr, a, playCmd) // play while playing is accepted and broadcast
	for _, c := range both {
		expectMusic(t, c, "dQw4w9WgXcQ", true, "A")
	}
	if pausedAt < 0 || pausedAt > time.Since(start).Milliseconds() {
		t.Fatalf("pause pos = %d out of range", pausedAt)
	}
}

// expectMusicAll requires the same music frame (video, playing, by, seq, queue) on every client.
func expectMusicAll(t *testing.T, cs []*Client, video string, playing bool, by string, seq int64, queue []queueItem) {
	t.Helper()
	for _, c := range cs {
		f := expectMusic(t, c, video, playing, by)
		if f.Seq != seq || !slices.Equal(f.Queue, queue) {
			t.Fatalf("music = %+v, want seq %d queue %+v", f, seq, queue)
		}
	}
}

// CHAT-30: add while something is current queues the track and keeps the current state.
func TestMusicAddQueues(t *testing.T) {
	cr, _ := startMusicRoom(t)
	a, b := newTestClient("A", 64), newTestClient("B", 64)
	joinAll(t, cr, a, b)
	both := []*Client{a, b}

	musicCtl(t, cr, a, addCmd(v1))
	expectMusicAll(t, both, v1, true, "A", 1, []queueItem{})
	musicCtl(t, cr, b, addCmd(v2)) // current by/pos unchanged: the frame still says A
	expectMusicAll(t, both, v1, true, "A", 1, []queueItem{{1, v2, "B"}})
	musicCtl(t, cr, a, addCmd(v3))
	expectMusicAll(t, both, v1, true, "A", 1, []queueItem{{1, v2, "B"}, {2, v3, "A"}})
	expectEmpty(t, a)
	expectEmpty(t, b)
}

// CHAT-30: skip starts the head of the queue, attributed to whoever added it.
func TestMusicSkipAdvances(t *testing.T) {
	cr, _ := startMusicRoom(t)
	a, b := newTestClient("A", 64), newTestClient("B", 64)
	joinAll(t, cr, a, b)
	both := []*Client{a, b}

	musicCtl(t, cr, a, addCmd(v1))
	musicCtl(t, cr, b, addCmd(v2))
	musicCtl(t, cr, b, addCmd(v3))
	expectMusicAll(t, both, v1, true, "A", 1, []queueItem{})
	expectMusicAll(t, both, v1, true, "A", 1, []queueItem{{1, v2, "B"}})
	expectMusicAll(t, both, v1, true, "A", 1, []queueItem{{1, v2, "B"}, {2, v3, "B"}})

	musicCtl(t, cr, a, skipCmd)
	for _, c := range both {
		f := expectMusic(t, c, v2, true, "B")
		if f.Seq != 2 || f.Pos > 1000 || !slices.Equal(f.Queue, []queueItem{{2, v3, "B"}}) {
			t.Fatalf("after skip = %+v", f)
		}
	}
	expectEmpty(t, a)
}

// CHAT-30: every client reports the same track ending; only the first report advances.
func TestMusicEndedOnce(t *testing.T) {
	cr, _ := startMusicRoom(t)
	a, b, c := newTestClient("A", 64), newTestClient("B", 64), newTestClient("C", 64)
	joinAll(t, cr, a, b, c)
	all := []*Client{a, b, c}

	musicCtl(t, cr, a, addCmd(v1))
	musicCtl(t, cr, a, addCmd(v2))
	expectMusicAll(t, all, v1, true, "A", 1, []queueItem{})
	expectMusicAll(t, all, v1, true, "A", 1, []queueItem{{1, v2, "A"}})

	for _, from := range all {
		musicCtl(t, cr, from, endedCmd(1))
	}
	expectMusicAll(t, all, v2, true, "A", 2, []queueItem{})

	broadcast(t, cr, a, "after") // Run handled the three reports first, so a second frame would precede it
	for _, c := range all {
		expect(t, c, typeChat, "A", "after")
		expectEmpty(t, c)
	}
}

// CHAT-30: the last track ending leaves the player paused on it; a later add starts anew.
func TestMusicLastTrackEnds(t *testing.T) {
	cr, _ := startMusicRoom(t)
	a, b := newTestClient("A", 64), newTestClient("B", 64)
	joinAll(t, cr, a, b)
	both := []*Client{a, b}

	musicCtl(t, cr, a, addCmd(v1))
	expectMusicAll(t, both, v1, true, "A", 1, []queueItem{})
	musicCtl(t, cr, b, endedCmd(1))
	expectMusicAll(t, both, v1, false, "A", 1, []queueItem{})

	musicCtl(t, cr, b, addCmd(v2))
	for _, c := range both {
		f := expectMusic(t, c, v2, true, "B")
		if f.Seq != 2 || f.Pos > 1000 || len(f.Queue) != 0 {
			t.Fatalf("add after the end = %+v", f)
		}
	}
}

// CHAT-30: remove is by id; a second remove of the same id is discarded.
func TestMusicRemoveById(t *testing.T) {
	cr, _ := startMusicRoom(t)
	a, b := newTestClient("A", 64), newTestClient("B", 64)
	joinAll(t, cr, a, b)
	both := []*Client{a, b}

	musicCtl(t, cr, a, addCmd(v1))
	musicCtl(t, cr, a, addCmd(v2))
	musicCtl(t, cr, a, addCmd(v3))
	expectMusicAll(t, both, v1, true, "A", 1, []queueItem{})
	expectMusicAll(t, both, v1, true, "A", 1, []queueItem{{1, v2, "A"}})
	expectMusicAll(t, both, v1, true, "A", 1, []queueItem{{1, v2, "A"}, {2, v3, "A"}})

	musicCtl(t, cr, b, removeCmd(1))
	expectMusicAll(t, both, v1, true, "A", 1, []queueItem{{2, v3, "A"}})
	musicCtl(t, cr, b, removeCmd(1))
	broadcast(t, cr, a, "after")
	for _, c := range both {
		expect(t, c, typeChat, "A", "after")
		expectEmpty(t, c)
	}
}

// CHAT-30: with a full queue (maxMusicQueue tracks) another add is discarded without any reply.
func TestMusicQueueCap(t *testing.T) {
	cr, _ := startMusicRoom(t)
	a := newTestClient("A", 64)
	joinAll(t, cr, a)

	for i := 0; i < 1+maxMusicQueue; i++ {
		musicCtl(t, cr, a, addCmd(v1))
		f := expectMusic(t, a, v1, true, "A")
		if len(f.Queue) != i {
			t.Fatalf("add %d: queue = %d", i+1, len(f.Queue))
		}
	}
	musicCtl(t, cr, a, addCmd(v2))
	broadcast(t, cr, a, "after")
	expect(t, a, typeChat, "A", "after")
	expectEmpty(t, a)
}

// CHAT-30: one add_many is one frame for everyone, with the first track current and the rest queued.
func TestMusicAddManyBroadcast(t *testing.T) {
	cr, _ := startMusicRoom(t)
	a, b := newTestClient("A", 64), newTestClient("B", 64)
	joinAll(t, cr, a, b)

	musicCtl(t, cr, a, addManyCmd(v1, v2, v3))
	expectMusicAll(t, []*Client{a, b}, v1, true, "A", 1, []queueItem{{1, v2, "A"}, {2, v3, "A"}})
	expectEmpty(t, a)
	expectEmpty(t, b)
}

// CHAT-30: an add_many with a full queue is discarded without any reply.
func TestMusicAddManyFull(t *testing.T) {
	cr, _ := startMusicRoom(t)
	a := newTestClient("A", 64)
	joinAll(t, cr, a)

	musicCtl(t, cr, a, addManyCmd(idList(1+maxMusicQueue)...))
	if f := expectMusic(t, a, idList(1)[0], true, "A"); len(f.Queue) != maxMusicQueue {
		t.Fatalf("queue = %d, want %d", len(f.Queue), maxMusicQueue)
	}
	musicCtl(t, cr, a, addManyCmd(v2, v3))
	broadcast(t, cr, a, "after")
	expect(t, a, typeChat, "A", "after")
	expectEmpty(t, a)
}

// The page's limits are copies of the server's: they must match (CHAT-30).
func TestMusicLimitsMirroredInJS(t *testing.T) {
	js, err := os.ReadFile("../../cmd/chatroom/app.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		fmt.Sprintf("const MUSIC_QUEUE_MAX = %d;", maxMusicQueue),
		fmt.Sprintf("const MUSIC_ADD_MANY_MAX = %d;", maxMusicAddMany),
	} {
		if !bytes.Contains(js, []byte(want)) {
			t.Errorf("app.js does not contain %q", want)
		}
	}
}

// CHAT-30: play, pause, seek, skip, ended and remove with nothing current are discarded without any reply.
func TestMusicNeedsVideo(t *testing.T) {
	cr, _ := startMusicRoom(t)
	a, b := newTestClient("A", 64), newTestClient("B", 64)
	joinAll(t, cr, a, b)

	for _, c := range []musicCmd{playCmd, pauseCmd, seekCmd(5), skipCmd, endedCmd(1), removeCmd(1)} {
		musicCtl(t, cr, a, c)
	}
	broadcast(t, cr, a, "after") // Run handled the controls first, so a reply would precede it
	for _, c := range []*Client{a, b} {
		expect(t, c, typeChat, "A", "after")
		expectEmpty(t, c)
	}
}

// CHAT-30: a newcomer gets the current state right after history, before its join; none while nothing is loaded.
func TestMusicNewcomerSnapshot(t *testing.T) {
	cr, _ := startMusicRoom(t)
	a := newTestClient("A", 64)
	joinAll(t, cr, a)

	empty := newTestClient("E", 64)
	register(t, cr, empty)
	expectWelcome(t, empty, "E")
	expect(t, empty, typeJoin, "E", "") // no music frame: nothing is loaded
	expect(t, a, typeJoin, "E", "")

	before := time.Now()
	musicCtl(t, cr, a, addCmd(v1))
	expectMusic(t, a, "dQw4w9WgXcQ", true, "A")
	expectMusic(t, empty, "dQw4w9WgXcQ", true, "A")
	musicCtl(t, cr, a, addCmd(v2))
	musicCtl(t, cr, a, addCmd(v3))
	for i := 0; i < 2; i++ {
		expectMusic(t, a, "dQw4w9WgXcQ", true, "A")
		expectMusic(t, empty, "dQw4w9WgXcQ", true, "A")
	}
	loaded := time.Now()
	time.Sleep(20 * time.Millisecond)

	joining := time.Now()
	c := newTestClient("C", 64)
	register(t, cr, c)
	expectWelcome(t, c, "C")
	f := expectMusic(t, c, "dQw4w9WgXcQ", true, "A")
	if f.Seq != 1 || !slices.Equal(f.Queue, []queueItem{{1, v2, "A"}, {2, v3, "A"}}) {
		t.Fatalf("snapshot seq %d queue %+v, want seq 1 and the 2 queued tracks", f.Seq, f.Queue)
	}
	if min, max := joining.Sub(loaded).Milliseconds(), time.Since(before).Milliseconds(); f.Pos < min || f.Pos > max {
		t.Fatalf("snapshot pos = %d, want within %d..%d", f.Pos, min, max)
	}
	expect(t, c, typeJoin, "C", "")
	expectEmpty(t, c)
	// The snapshot is private: nobody else gets a second music frame.
	expect(t, a, typeJoin, "C", "")
	expectEmpty(t, a)
}

// CHAT-30: when the last client leaves, the player pauses at the current position.
func TestMusicEmptyRoomPauses(t *testing.T) {
	cr, _ := startMusicRoom(t)
	a := newTestClient("A", 64)
	joinAll(t, cr, a)
	musicCtl(t, cr, a, addCmd(v1))
	musicCtl(t, cr, a, addCmd(v2))
	expectMusic(t, a, "dQw4w9WgXcQ", true, "A")
	expectMusic(t, a, "dQw4w9WgXcQ", true, "A")
	time.Sleep(50 * time.Millisecond)
	unregister(t, cr, a)
	expectClosed(t, a)
	time.Sleep(100 * time.Millisecond)

	b := newTestClient("B", 64)
	register(t, cr, b)
	expectWelcome(t, b, "B")
	f := expectMusic(t, b, "dQw4w9WgXcQ", false, "A")
	if f.Seq != 1 || !slices.Equal(f.Queue, []queueItem{{1, v2, "A"}}) {
		t.Fatalf("seq %d queue %+v, want the queue kept", f.Seq, f.Queue)
	}
	if f.Pos < 50 {
		t.Fatalf("pos = %d, want at least the 50 ms played before A left", f.Pos)
	}
	expect(t, b, typeJoin, "B", "")

	time.Sleep(50 * time.Millisecond) // still paused: the position does not advance
	c := newTestClient("C", 64)
	register(t, cr, c)
	expectWelcome(t, c, "C")
	if g := expectMusic(t, c, "dQw4w9WgXcQ", false, "A"); g.Pos != f.Pos {
		t.Fatalf("paused pos moved from %d to %d", f.Pos, g.Pos)
	}
}

// CHAT-30, CHAT-03: the last client removed as slow also pauses the room.
func TestMusicLastSlowDropPauses(t *testing.T) {
	cr := NewMusicRoom("musica")
	x := newTestClient("x", 0)
	cr.clients[x] = struct{}{}
	cr.taken[foldKey("x")] = struct{}{}
	cr.player.apply(addCmd(v1), "x", time.Now().Add(-time.Hour)) // an hour in, so a stale pos stands out

	cr.dispatch(inbound{from: x, kind: inboundMusic, music: playCmd}) // x cannot take the frame

	expectClosed(t, x)
	if len(cr.clients) != 0 {
		t.Fatalf("clients = %d, want 0", len(cr.clients))
	}
	if cr.player.playing || cr.player.pos < int64(time.Hour/time.Millisecond) {
		t.Fatalf("player = %+v, want paused at about an hour", *cr.player)
	}
}

// CHAT-30, CHAT-21: without a shared player a control is dropped, history is unchanged and chat still flows.
func TestMusicRoomWithoutPlayer(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := newTestClient("A", 64), newTestClient("B", 64)
	joinAll(t, cr, a, b)

	musicCtl(t, cr, a, addCmd(v1))
	musicCtl(t, cr, a, playCmd)
	broadcast(t, cr, a, "hola")
	for _, c := range []*Client{a, b} {
		expect(t, c, typeChat, "A", "hola")
		expectEmpty(t, c)
	}

	c := newTestClient("C", 64)
	register(t, cr, c)
	_, hist := expectWelcome(t, c, "C")
	expectHistory(t, hist, message{Type: typeChat, From: "A", Text: "hola"})
	expect(t, c, typeJoin, "C", "") // no music frame
	expectEmpty(t, c)
}

// CHAT-30, CHAT-18: music frames are never stored in history.
func TestMusicNotInHistory(t *testing.T) {
	cr, _ := startMusicRoom(t)
	a := newTestClient("A", 64)
	joinAll(t, cr, a)
	musicCtl(t, cr, a, addCmd(v1))
	expectMusic(t, a, "dQw4w9WgXcQ", true, "A")
	broadcast(t, cr, a, "hola")
	expect(t, a, typeChat, "A", "hola")

	b := newTestClient("B", 64)
	register(t, cr, b)
	_, hist := expectWelcome(t, b, "B")
	expectHistory(t, hist, message{Type: typeChat, From: "A", Text: "hola"})
	expectMusic(t, b, "dQw4w9WgXcQ", true, "A")
}

// CHAT-21: a control from a client that is no longer a member is ignored.
func TestMusicFromNonMemberDropped(t *testing.T) {
	cr, _ := startMusicRoom(t)
	a, ghost := newTestClient("A", 64), newTestClient("ghost", 64)
	joinAll(t, cr, a)

	musicCtl(t, cr, ghost, addCmd(v1))
	broadcast(t, cr, a, "after")
	expect(t, a, typeChat, "A", "after")
	expectEmpty(t, a)
	expectEmpty(t, ghost)

	b := newTestClient("B", 64) // nothing was loaded, so no snapshot
	register(t, cr, b)
	expectWelcome(t, b, "B")
	expect(t, b, typeJoin, "B", "")
}

// CHAT-30, CHAT-03: a member that cannot take a music frame is removed and announced as left.
func TestMusicSlowClientDropped(t *testing.T) {
	cr := NewMusicRoom("musica")
	o, a, x := newTestClient("o", 16), newTestClient("a", 16), newTestClient("x", 0)
	for _, c := range []*Client{o, a, x} {
		cr.clients[c] = struct{}{}
		cr.taken[foldKey(c.nickname)] = struct{}{}
	}

	cr.dispatch(inbound{from: a, kind: inboundMusic, music: addCmd(v1)})

	expectClosed(t, x)
	for _, c := range []*Client{o, a} {
		expectMusic(t, c, "dQw4w9WgXcQ", true, "a")
		expect(t, c, typeLeave, "x", "")
		expectEmpty(t, c)
	}
	if !cr.player.playing {
		t.Fatal("the room still has members, so the player must keep playing")
	}
}

// chatWithReply queues a room chat that replies to replyTo.
func chatWithReply(t *testing.T, cr *Room, from *Client, text string, replyTo int64) {
	t.Helper()
	select {
	case cr.broadcast <- inbound{from: from, text: text, replyTo: replyTo}:
	case <-time.After(testDeadline):
		t.Fatal("timeout broadcasting")
	}
}

// dmWithReply queues a dm that carries reply_to.
func dmWithReply(t *testing.T, cr *Room, from *Client, to, text string, replyTo int64) {
	t.Helper()
	select {
	case cr.broadcast <- inbound{from: from, kind: inboundDM, to: cleanNickname(to), text: text, replyTo: replyTo}:
	case <-time.After(testDeadline):
		t.Fatal("timeout queueing dm")
	}
}

// CHAT-35: chat and dm frames draw ids from one sequence; both dm copies share theirs.
func TestMessageIDsSharedByChatAndDM(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := newTestClient("A", 16), newTestClient("B", 16)
	joinAll(t, cr, a, b)

	broadcast(t, cr, a, "uno")
	dm(t, cr, a, "B", "dos")
	broadcast(t, cr, b, "tres")

	if m := next(t, a); m.ID != 1 {
		t.Fatalf("first chat id = %d, want 1", m.ID)
	}
	toA := expectDM(t, a, "A", "B", "dos")
	if m := next(t, a); m.ID != 3 {
		t.Fatalf("third frame id = %d, want 3", m.ID)
	}
	if m := next(t, b); m.ID != 1 {
		t.Fatalf("b first chat id = %d, want 1", m.ID)
	}
	toB := expectDM(t, b, "A", "B", "dos")
	if toA.ID != 2 || toB.ID != 2 {
		t.Fatalf("dm ids = %d and %d, want 2 and 2", toA.ID, toB.ID)
	}
	if m := next(t, b); m.ID != 3 {
		t.Fatalf("b third id = %d, want 3", m.ID)
	}
}

// CHAT-35: frames that are not delivered use no id.
func TestDiscardedFramesUseNoID(t *testing.T) {
	cr, _ := startRoom(t)
	a, ghost := newTestClient("A", 16), newTestClient("Ghost", 16)
	joinAll(t, cr, a)

	dm(t, cr, a, "A", "a mi mismo")
	expectDMError(t, a, "A", reasonSelf)
	dm(t, cr, a, "Nadie", "hola")
	expectDMError(t, a, "Nadie", reasonOffline)
	broadcast(t, cr, ghost, "no soy miembro") // never registered: dropped
	broadcast(t, cr, a, "primero")
	if m := next(t, a); m.Text != "primero" || m.ID != 1 {
		t.Fatalf("chat = %+v, want id 1", m)
	}
}

// CHAT-35: a resolved reply embeds {id, from, text}; history keeps id and reply.
func TestReplyResolved(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := newTestClient("A", 16), newTestClient("B", 16)
	joinAll(t, cr, a, b)

	broadcast(t, cr, a, "¿pizza?")
	next(t, a)
	next(t, b)
	chatWithReply(t, cr, b, "dale", 1)
	want := quote{ID: 1, From: "A", Text: "¿pizza?"}
	for _, c := range []*Client{a, b} {
		m := next(t, c)
		if m.ID != 2 || m.Reply == nil || *m.Reply != want {
			t.Fatalf("reply = %+v (%+v), want id 2 quoting %+v", m, m.Reply, want)
		}
	}
}

// CHAT-35: a late joiner's history entry keeps both id and reply.
func TestReplyKeptInHistory(t *testing.T) {
	cr, _ := startRoom(t)
	a := newTestClient("A", 16)
	joinAll(t, cr, a)
	broadcast(t, cr, a, "hola")
	chatWithReply(t, cr, a, "yo mismo", 1)
	next(t, a)
	next(t, a)

	late := newTestClient("Late", 16)
	register(t, cr, late)
	if m := next(t, late); m.Type != typeWelcome {
		t.Fatalf("first frame = %+v", m)
	}
	nextRaw(t, late) // users
	h := decodeHistory(t, nextRaw(t, late))
	if len(h.Messages) != 2 || h.Messages[0].ID != 1 || h.Messages[0].Reply != nil {
		t.Fatalf("history = %+v", h.Messages)
	}
	if r := h.Messages[1].Reply; h.Messages[1].ID != 2 || r == nil || *r != (quote{ID: 1, From: "A", Text: "hola"}) {
		t.Fatalf("history[1] = %+v (%+v)", h.Messages[1], r)
	}
}

// CHAT-35: a reply_to that does not resolve is relayed with an id and no reply, and no error frame.
func TestReplyUnresolvedIgnored(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := newTestClient("A", 512), newTestClient("B", 512)
	joinAll(t, cr, a, b)

	broadcast(t, cr, a, "uno")   // id 1
	dm(t, cr, a, "B", "secreto") // id 2
	if m := next(t, a); m.ID != 1 {
		t.Fatalf("id = %d", m.ID)
	}
	expectDM(t, a, "A", "B", "secreto")
	next(t, b)
	expectDM(t, b, "A", "B", "secreto")

	check := func(replyTo int64, wantID int64) {
		t.Helper()
		chatWithReply(t, cr, a, "x", replyTo)
		m := next(t, a)
		if m.ID != wantID || m.Reply != nil || m.Text != "x" {
			t.Fatalf("reply_to %d: got %+v (%+v), want id %d without reply", replyTo, m, m.Reply, wantID)
		}
	}
	check(999, 3) // unknown
	check(2, 4)   // a DM id
	check(5, 5)   // its own id
	for i := 0; i < replyTargets+1; i++ {
		broadcast(t, cr, a, "relleno")
		next(t, a)
	}
	check(1, 6+replyTargets+1) // evicted
	expectEmpty(t, a)
}

// CHAT-35: the reply ring holds exactly replyTargets room messages. The reply is itself a room
// message, so it is the one that evicts: id 1 resolves after replyTargets-1 more, not after replyTargets.
func TestReplyRingBoundary(t *testing.T) {
	for _, tc := range []struct {
		name    string
		fillers int
		want    bool
	}{
		{"last one still resolves", replyTargets - 1, true},
		{"one more evicts it", replyTargets, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cr, _ := startRoom(t)
			a := newTestClient("A", 16)
			joinAll(t, cr, a)

			broadcast(t, cr, a, "primero") // id 1
			next(t, a)
			for i := 0; i < tc.fillers; i++ {
				broadcast(t, cr, a, "relleno")
				next(t, a)
			}
			chatWithReply(t, cr, a, "r", 1)
			m := next(t, a)
			if wantID := int64(tc.fillers + 2); m.ID != wantID {
				t.Fatalf("id = %d, want %d", m.ID, wantID)
			}
			if tc.want != (m.Reply != nil) {
				t.Fatalf("reply = %+v, want resolved = %v", m.Reply, tc.want)
			}
		})
	}
}

// CHAT-35: a quote is cut at 100 runes and loses trailing whitespace.
func TestReplyQuoteTruncated(t *testing.T) {
	cr, _ := startRoom(t)
	a := newTestClient("A", 16)
	joinAll(t, cr, a)

	long := strings.Repeat("ñ", 300)
	broadcast(t, cr, a, long)
	next(t, a)
	chatWithReply(t, cr, a, "r", 1)
	if m := next(t, a); m.Reply == nil || m.Reply.Text != strings.Repeat("ñ", 100) {
		t.Fatalf("quote = %+v", m.Reply)
	}

	broadcast(t, cr, a, strings.Repeat("a", 99)+" "+strings.Repeat("b", 50)) // id 3: space is rune 100
	next(t, a)
	chatWithReply(t, cr, a, "r", 3)
	if m := next(t, a); m.Reply == nil || m.Reply.Text != strings.Repeat("a", 99) {
		t.Fatalf("quote = %+v, want 99 a's", m.Reply)
	}
	// The message itself is untouched.
	if got := quoteText("hola  \n"); got != "hola" {
		t.Fatalf("quoteText = %q", got)
	}
}

// CHAT-35: replying to a reply quotes only that reply's own text.
func TestReplyQuotesDoNotNest(t *testing.T) {
	cr, _ := startRoom(t)
	a := newTestClient("A", 16)
	joinAll(t, cr, a)

	broadcast(t, cr, a, "uno")
	chatWithReply(t, cr, a, "dos", 1)
	chatWithReply(t, cr, a, "tres", 2)
	next(t, a)
	next(t, a)
	m := next(t, a)
	if m.Reply == nil || *m.Reply != (quote{ID: 2, From: "A", Text: "dos"}) {
		t.Fatalf("reply = %+v", m.Reply)
	}
}

// CHAT-24, CHAT-35: reply_to in a dm is forwarded as is; the ring never holds a dm.
func TestDMReplyToForwarded(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := newTestClient("A", 16), newTestClient("B", 16)
	joinAll(t, cr, a, b)

	dmWithReply(t, cr, a, "B", "respuesta", 41)
	toB := expectDM(t, b, "A", "B", "respuesta")
	toA := expectDM(t, a, "A", "B", "respuesta")
	if toB.ReplyTo != 41 || toA.ReplyTo != 41 || toA.ID != 1 || toB.ID != 1 {
		t.Fatalf("dm copies = %+v / %+v, want id 1 and reply_to 41", toA, toB)
	}

	chatWithReply(t, cr, a, "x", 1) // the dm's id
	if m := next(t, a); m.Reply != nil || m.ID != 2 {
		t.Fatalf("chat = %+v (%+v), want id 2 without reply", m, m.Reply)
	}
}

// CHAT-35: welcome, join and leave carry neither id nor reply.
func TestJoinLeaveWelcomeHaveNoID(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := newTestClient("A", 16), newTestClient("B", 16)
	register(t, cr, a)
	exactFields(t, nextRaw(t, a), "type", "from", "text", "ts")
	nextRaw(t, a)                                               // users
	nextRaw(t, a)                                               // history
	exactFields(t, nextRaw(t, a), "type", "from", "text", "ts") // join A
	register(t, cr, b)
	exactFields(t, nextRaw(t, a), "type", "from", "text", "ts") // join B
	unregister(t, cr, b)
	exactFields(t, nextRaw(t, a), "type", "from", "text", "ts") // leave B
}

// bioClient is avatarClient with a bio; the hub treats both as opaque strings.
func bioClient(nick, avatar, bio string) *Client {
	c := avatarClient(nick, avatar)
	c.bio = bio
	return c
}

// profileUpdate queues a profile update as if readPump had validated and admitted it.
func profileUpdate(t *testing.T, cr *Room, from *Client, avatar, bio string) {
	t.Helper()
	select {
	case cr.broadcast <- inbound{from: from, kind: inboundProfile, avatar: avatar, bio: bio}:
	case <-time.After(testDeadline):
		t.Fatal("timeout queueing profile")
	}
}

// stringMap decodes a JSON object of strings.
func stringMap(t *testing.T, raw json.RawMessage) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return m
}

// CHAT-37: welcome and join carry the bio only when the client has one.
func TestWelcomeAndJoinCarryBio(t *testing.T) {
	cr, _ := startRoom(t)
	obs := newTestClient("obs", 16)
	joinAll(t, cr, obs)

	a := bioClient("a", "", "hola")
	register(t, cr, a)
	w := expectAvatarFrame(t, a, "type", "from", "text", "ts", "bio")
	if string(w["bio"]) != `"hola"` {
		t.Fatalf("welcome bio = %s", w["bio"])
	}
	exactFields(t, nextRaw(t, a), "type", "users", "bios", "ts")
	decodeHistory(t, nextRaw(t, a))
	for _, c := range []*Client{a, obs} {
		j := exactFields(t, nextRaw(t, c), "type", "from", "text", "ts", "bio")
		if string(j["bio"]) != `"hola"` || string(j["type"]) != `"join"` {
			t.Fatalf("join = %v", j)
		}
	}

	b := newTestClient("b", 16)
	register(t, cr, b)
	expectAvatarFrame(t, b, "type", "from", "text", "ts") // no bio key
	skip(t, b, 2)
	expectAvatarFrame(t, b, "type", "from", "text", "ts")
}

// CHAT-17, CHAT-37: bios lists only members that have one and is omitted when nobody does.
func TestUsersFrameBios(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := bioClient("a", "", "bio-a"), newTestClient("b", 16)
	register(t, cr, a)
	register(t, cr, b)
	skip(t, a, privateFrames+2)

	c := bioClient("c", "pic-c", "bio-c")
	register(t, cr, c)
	nextRaw(t, c) // welcome
	f := exactFields(t, nextRaw(t, c), "type", "users", "avatars", "bios", "ts")
	if want := map[string]string{"a": "bio-a", "c": "bio-c"}; !maps.Equal(stringMap(t, f["bios"]), want) {
		t.Fatalf("bios = %s, want %v", f["bios"], want)
	}
	if want := map[string]string{"c": "pic-c"}; !maps.Equal(stringMap(t, f["avatars"]), want) {
		t.Fatalf("avatars = %s, want %v", f["avatars"], want)
	}

	cr2, _ := startRoom(t)
	x := newTestClient("x", 16)
	register(t, cr2, x)
	nextRaw(t, x)
	exactFields(t, nextRaw(t, x), "type", "users", "ts") // nobody has a bio
}

// CHAT-38: an update reaches the whole room, sender included, with the exact shape and key order.
func TestProfileUpdateBroadcast(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := newTestClient("a", 16), newTestClient("b", 16)
	joinAll(t, cr, a, b)

	profileUpdate(t, cr, a, "pic", "hola")
	for _, c := range []*Client{a, b} {
		raw := nextRaw(t, c)
		exactFields(t, raw, "type", "from", "ts", "avatar", "bio")
		s := string(raw)
		if !strings.HasPrefix(s, `{"type":"profile","from":"a","ts":`) || !strings.HasSuffix(s, `,"avatar":"pic","bio":"hola"}`) {
			t.Fatalf("profile frame = %s", s)
		}
	}
	expectEmpty(t, a)
	expectEmpty(t, b)
}

// CHAT-38: an empty update clears the profile; the frame and later users frames omit the fields.
func TestProfileClear(t *testing.T) {
	cr, _ := startRoom(t)
	a := bioClient("a", "pic", "hola")
	register(t, cr, a)
	skip(t, a, privateFrames+1)

	profileUpdate(t, cr, a, "", "")
	exactFields(t, nextRaw(t, a), "type", "from", "ts")

	late := newTestClient("late", 16)
	register(t, cr, late)
	nextRaw(t, late)
	exactFields(t, nextRaw(t, late), "type", "users", "ts")
}

// CHAT-28, CHAT-38: history keeps the avatar a chat was relayed with; users shows the new one.
func TestProfileHistoryKeepsOldAvatar(t *testing.T) {
	cr, _ := startRoom(t)
	a := avatarClient("a", "U")
	register(t, cr, a)
	skip(t, a, privateFrames+1)
	broadcast(t, cr, a, "before")
	skip(t, a, 1)
	profileUpdate(t, cr, a, "V", "")
	skip(t, a, 1)

	late := newTestClient("late", 16)
	register(t, cr, late)
	nextRaw(t, late)
	f := exactFields(t, nextRaw(t, late), "type", "users", "avatars", "ts")
	if got := stringMap(t, f["avatars"]); got["a"] != "V" {
		t.Fatalf("avatars = %v, want a=V", got)
	}
	var hist struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(nextRaw(t, late), &hist); err != nil || len(hist.Messages) != 1 {
		t.Fatalf("history = %+v, err %v", hist, err)
	}
	h := exactFields(t, hist.Messages[0], "type", "from", "text", "ts", "avatar", "id")
	if string(h["avatar"]) != `"U"` {
		t.Fatalf("history avatar = %s, want U", h["avatar"])
	}
}

// CHAT-28, CHAT-38: after an update the sender's next chat carries the new avatar and never a bio.
func TestProfileChangesLaterChatAvatar(t *testing.T) {
	cr, _ := startRoom(t)
	a := avatarClient("a", "old")
	register(t, cr, a)
	skip(t, a, privateFrames+1)

	profileUpdate(t, cr, a, "new", "hola")
	skip(t, a, 1)
	broadcast(t, cr, a, "hi")
	f := expectAvatarFrame(t, a, "type", "from", "text", "ts", "avatar", "id")
	if string(f["avatar"]) != `"new"` {
		t.Fatalf("chat avatar = %s, want new", f["avatar"])
	}
}

// CHAT-38: an update from a client that is no longer a member publishes nothing.
func TestProfileFromRemovedClientIgnored(t *testing.T) {
	cr, _ := startRoom(t)
	a, b := newTestClient("a", 16), newTestClient("b", 16)
	joinAll(t, cr, a, b)
	unregister(t, cr, a)
	expect(t, b, typeLeave, "a", "")

	profileUpdate(t, cr, a, "pic", "ghost")
	broadcast(t, cr, b, "marker")
	expect(t, b, typeChat, "b", "marker") // decode fails on a profile frame
}

// CHAT-38: profile frames are never stored in history.
func TestProfileNotInHistory(t *testing.T) {
	cr, _ := startRoom(t)
	a := newTestClient("a", 16)
	joinAll(t, cr, a)
	profileUpdate(t, cr, a, "pic", "hola")
	skip(t, a, 1)
	broadcast(t, cr, a, "hi")
	skip(t, a, 1)

	late := newTestClient("late", 16)
	register(t, cr, late)
	nextRaw(t, late)
	nextRaw(t, late)
	hist := decodeHistory(t, nextRaw(t, late))
	if len(hist.Messages) != 1 || hist.Messages[0].Type != typeChat {
		t.Fatalf("history = %+v, want only the chat", hist.Messages)
	}
}
