package chat

import (
	"context"
	"encoding/json"
	"net/http/httptest"
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
	cr := NewRoom(nil)
	ctx, cancel := context.WithCancel(context.Background())
	go cr.Run(ctx)
	t.Cleanup(cancel)
	return cr, cancel
}

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

// next waits for the next frame on c and decodes it.
func next(t *testing.T, c *Client) message {
	t.Helper()
	select {
	case m, ok := <-c.send:
		if !ok {
			t.Fatal("send closed, wanted a message")
		}
		return decode(t, m)
	case <-time.After(testDeadline):
		t.Fatal("timeout waiting for a message")
	}
	return message{}
}

// expect waits for the next message on c and compares it to want, ignoring ts.
func expect(t *testing.T, c *Client, typ, from, text string) {
	t.Helper()
	got := next(t, c)
	if got.Type != typ || got.From != from || got.Text != text {
		t.Fatalf("got (%s, %q, %q), want (%s, %q, %q)", got.Type, got.From, got.Text, typ, from, text)
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
// broadcasts a marker from c and consumes c's welcome, join and the marker.
// c must be freshly registered and the only frames pending on it.
func syncRoom(t *testing.T, cr *Room, c *Client) {
	t.Helper()
	select {
	case <-c.registered: // makes the read of c.nickname below race-free
	case <-time.After(testDeadline):
		t.Fatal("timeout waiting for registration")
	}
	broadcast(t, cr, c, "sync-marker")
	expect(t, c, typeWelcome, c.nickname, "")
	expect(t, c, typeJoin, c.nickname, "")
	expect(t, c, typeChat, c.nickname, "sync-marker")
}

func TestJoinNoticeDelivered(t *testing.T) {
	cr, _ := startRoom(t)
	a := newTestClient("alice", 8)
	register(t, cr, a)
	expect(t, a, typeWelcome, "alice", "")
	expect(t, a, typeJoin, "alice", "")
}

func TestSameNicknameIndependent(t *testing.T) {
	cr, _ := startRoom(t)
	a1 := newTestClient("bob", 8)
	a2 := newTestClient("bob", 8)
	register(t, cr, a1)
	expect(t, a1, typeWelcome, "bob", "")
	expect(t, a1, typeJoin, "bob", "")
	register(t, cr, a2)
	expect(t, a1, typeJoin, "bob (2)", "")
	expect(t, a2, typeWelcome, "bob (2)", "")
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
	expect(t, a, typeWelcome, "a", "")
	expect(t, a, typeJoin, "a", "")
	expect(t, a, typeJoin, "b", "")
	expect(t, b, typeWelcome, "b", "")
	expect(t, b, typeJoin, "b", "")

	unregister(t, cr, a)
	unregister(t, cr, a) // no-op, must not panic
	expectClosed(t, a)
	expect(t, b, typeLeave, "a", "")

	broadcast(t, cr, b, "sync")
	expect(t, b, typeChat, "b", "sync") // no second leave notice queued before this
}

// A buffer of 3 holds exactly welcome + join for "slow" and the join of "ok",
// so the next broadcast message is what overflows it and triggers the drop.
func TestUnregisterAfterSlowDropIsNoop(t *testing.T) {
	cr, _ := startRoom(t)
	slow := newTestClient("slow", 3)
	ok := newTestClient("ok", 16)
	register(t, cr, slow)
	register(t, cr, ok)
	expect(t, ok, typeWelcome, "ok", "")
	expect(t, ok, typeJoin, "ok", "") // both join notices have been fanned out by now
	broadcast(t, cr, ok, "m1")        // slow buffer full -> dropped
	expect(t, ok, typeChat, "ok", "m1")
	expectClosed(t, slow)

	unregister(t, cr, slow) // must not panic or emit notice
	broadcast(t, cr, ok, "sync")
	expect(t, ok, typeChat, "ok", "sync")
}

// Same sizing as above: the drop is caused by the broadcast message, and the
// slow client still gets everything queued before it was dropped.
func TestSlowClientDroppedOthersReceive(t *testing.T) {
	cr, _ := startRoom(t)
	slow := newTestClient("slow", 3)
	fast := newTestClient("fast", 64)
	register(t, cr, slow)
	register(t, cr, fast)
	expect(t, fast, typeWelcome, "fast", "")
	expect(t, fast, typeJoin, "fast", "")

	broadcast(t, cr, fast, "x")
	expect(t, fast, typeChat, "fast", "x")
	expect(t, slow, typeWelcome, "slow", "")
	expect(t, slow, typeJoin, "slow", "")
	expect(t, slow, typeJoin, "fast", "")
	expectClosed(t, slow)

	broadcast(t, cr, fast, "y") // the room keeps serving the remaining clients
	expect(t, fast, typeChat, "fast", "y")
}

func TestSaturationNoDeadlock(t *testing.T) {
	cr := NewRoom(nil)
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
	expect(t, last, typeWelcome, "last", "")
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

	expect(t, c, typeWelcome, "bob (2)", "")
	expect(t, c, typeJoin, "bob (2)", "")
	expect(t, b, typeWelcome, "Bob", "")
	expect(t, b, typeJoin, "Bob", "")
	expect(t, b, typeJoin, "bob (2)", "") // others get only the join
	expect(t, a, typeWelcome, "a", "")
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
		expect(t, again, typeWelcome, "BOB", "")
	})
	t.Run("slow drop", func(t *testing.T) {
		cr, _ := startRoom(t)
		bob := newTestClient("bob", 2) // welcome + own join; obs's join overflows it
		obs := newTestClient("obs", 16)
		register(t, cr, bob)
		register(t, cr, obs)
		syncRoom(t, cr, obs) // obs's join has been fanned out, so bob was dropped
		expectClosed(t, bob)

		again := newTestClient("bob", 8)
		register(t, cr, again)
		expect(t, again, typeWelcome, "bob", "")
	})
}

// CHAT-10: a message from a client that is no longer in the room is discarded.
func TestBroadcastFromNonMemberDropped(t *testing.T) {
	cr, _ := startRoom(t)
	slow := newTestClient("slow", 2)
	ok := newTestClient("ok", 16)
	register(t, cr, slow)
	register(t, cr, ok)
	syncRoom(t, cr, ok) // ok's join has been fanned out, so slow was dropped
	expectClosed(t, slow)

	broadcast(t, cr, slow, "ghost")
	broadcast(t, cr, ok, "sync")
	expect(t, ok, typeChat, "ok", "sync") // "ghost" would have come first
}

// A chat queued right before unregister must be relayed before the leave,
// whichever of the two channels Run's select happens to pick first.
func TestChatBeforeLeaveOnDisconnect(t *testing.T) {
	// Drive the hub's handlers directly (no Run goroutine) so the "broadcast
	// queued, then unregister" interleaving is deterministic.
	cr := NewRoom(nil)
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
// not be removed (and its send closed) a second time.
func TestLeaveAfterDrainDroppedClient(t *testing.T) {
	cr := NewRoom(nil)
	a := newTestClient("a", 0) // any delivery to a overflows
	b := newTestClient("b", 8)
	for _, c := range []*Client{a, b} {
		cr.clients[c] = struct{}{}
		cr.taken[foldKey(c.nickname)] = struct{}{}
	}

	cr.broadcast <- inbound{from: a, text: "bye"}
	cr.leave(a) // panics with "close of closed channel" if a is removed twice

	expect(t, b, typeChat, "a", "bye")
	select {
	case raw := <-b.send:
		t.Fatalf("slow drop must be silent, got %s", raw)
	default:
	}
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
				if m := decode(t, raw); m.Type == typeLeave {
					t.Fatalf("unexpected leave frame on shutdown: %+v", m)
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
	next(t, a) // welcome a
	next(t, a) // join a
	joinA := next(t, a)
	next(t, b) // welcome b
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

func TestCheckOrigin(t *testing.T) {
	tests := []struct {
		name    string
		origin  string
		allowed []string
		want    bool
	}{
		{"no origin", "", nil, true},
		{"same host", "http://example.com", nil, true},
		{"same host case-insensitive", "http://EXAMPLE.com", nil, true},
		{"other host", "http://evil.com", nil, false},
		{"null", "null", nil, false},
		{"null allowlisted", "null", []string{"null"}, false},
		{"allowlisted exact", "https://app.other.com", []string{"https://app.other.com"}, true},
		{"allowlist is exact match", "http://app.other.com", []string{"https://app.other.com"}, false},
		{"malformed", "http://[::1", nil, false},
		{"no host", "foo", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := NewRoom(tt.allowed)
			r := httptest.NewRequest("GET", "http://example.com/ws", nil)
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if got := cr.checkOrigin(r); got != tt.want {
				t.Fatalf("checkOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
			}
		})
	}
}

func TestNewRoomDefaults(t *testing.T) {
	cr := NewRoom(nil)
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
			cr := NewRoom(nil)
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
