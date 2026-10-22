package chat

import (
	"context"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
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
	return &Client{send: make(chan []byte, buf), nickname: nick}
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

func broadcast(t *testing.T, cr *Room, msg string) {
	t.Helper()
	select {
	case cr.broadcast <- []byte(msg):
	case <-time.After(testDeadline):
		t.Fatal("timeout broadcasting")
	}
}

// expect waits for the next message on c and compares it to want.
func expect(t *testing.T, c *Client, want string) {
	t.Helper()
	select {
	case m, ok := <-c.send:
		if !ok {
			t.Fatalf("send closed, wanted %q", want)
		}
		if string(m) != want {
			t.Fatalf("got %q, want %q", m, want)
		}
	case <-time.After(testDeadline):
		t.Fatalf("timeout waiting for %q", want)
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

func TestJoinNoticeDelivered(t *testing.T) {
	cr, _ := startRoom(t)
	a := newTestClient("alice", 8)
	register(t, cr, a)
	expect(t, a, "alice joined! 🎉")
}

func TestSameNicknameIndependent(t *testing.T) {
	cr, _ := startRoom(t)
	a1 := newTestClient("bob", 8)
	a2 := newTestClient("bob", 8)
	register(t, cr, a1)
	expect(t, a1, "bob joined! 🎉")
	register(t, cr, a2)
	expect(t, a1, "bob joined! 🎉")
	expect(t, a2, "bob joined! 🎉")

	broadcast(t, cr, "bob: hi")
	expect(t, a1, "bob: hi")
	expect(t, a2, "bob: hi")

	unregister(t, cr, a1)
	expectClosed(t, a1)
	expect(t, a2, "bob left. 👋")

	broadcast(t, cr, "bob: again")
	expect(t, a2, "bob: again")
}

func TestUnregisterClosesOnceAndNoticeOnce(t *testing.T) {
	cr, _ := startRoom(t)
	a := newTestClient("a", 8)
	b := newTestClient("b", 8)
	register(t, cr, a)
	register(t, cr, b)
	expect(t, a, "a joined! 🎉")
	expect(t, a, "b joined! 🎉")
	expect(t, b, "b joined! 🎉")

	unregister(t, cr, a)
	unregister(t, cr, a) // no-op, must not panic
	expectClosed(t, a)
	expect(t, b, "a left. 👋")

	broadcast(t, cr, "sync")
	expect(t, b, "sync") // no second leave notice queued before this
}

// A buffer of 2 holds exactly the two join notices ("slow" and "ok"), so the
// next broadcast message is what overflows it and triggers the drop.
func TestUnregisterAfterSlowDropIsNoop(t *testing.T) {
	cr, _ := startRoom(t)
	slow := newTestClient("slow", 2)
	ok := newTestClient("ok", 16)
	register(t, cr, slow)
	register(t, cr, ok)
	expect(t, ok, "ok joined! 🎉") // both join notices have been fanned out by now
	broadcast(t, cr, "m1")         // slow buffer full -> dropped
	expect(t, ok, "m1")
	expectClosed(t, slow)

	unregister(t, cr, slow) // must not panic or emit notice
	broadcast(t, cr, "sync")
	expect(t, ok, "sync")
}

// Same sizing as above: the drop is caused by the broadcast message, and the
// slow client still gets everything queued before it was dropped.
func TestSlowClientDroppedOthersReceive(t *testing.T) {
	cr, _ := startRoom(t)
	slow := newTestClient("slow", 2)
	fast := newTestClient("fast", 64)
	register(t, cr, slow)
	register(t, cr, fast)
	expect(t, fast, "fast joined! 🎉")

	broadcast(t, cr, "x")
	expect(t, fast, "x")
	expect(t, slow, "slow joined! 🎉")
	expect(t, slow, "fast joined! 🎉")
	expectClosed(t, slow)

	broadcast(t, cr, "y") // the room keeps serving the remaining clients
	expect(t, fast, "y")
}

func TestSaturationNoDeadlock(t *testing.T) {
	cr := NewRoom(nil)
	// Fill the broadcast queue before Run starts so it is full when traffic begins.
	for i := 0; i < cap(cr.broadcast); i++ {
		cr.broadcast <- []byte("pre")
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go cr.Run(ctx)

	// trySend avoids leaking goroutines if the hub deadlocks.
	trySend := func(ch chan<- []byte, m []byte) bool {
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
	// Drain-capable listener so the hub keeps flowing.
	listener := newTestClient("l", 1)
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
				if !trySend(cr.broadcast, []byte("m")) {
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
	expect(t, last, "last joined! 🎉")
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeNickname(tt.in); got != tt.want {
				t.Fatalf("normalizeNickname(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
