package chat

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// startServer runs room.Run and an httptest server exposing GET /ws.
// Timeout fields must be overridden on room BEFORE calling it.
func startServer(t *testing.T, room *Room) (srv *httptest.Server, cancel context.CancelFunc) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/ws", room.HandleWebSocket)

	ctx, cancel := context.WithCancel(context.Background())
	go room.Run(ctx)
	srv = httptest.NewServer(engine)
	t.Cleanup(func() {
		cancel()
		srv.Close()
	})
	return srv, cancel
}

// dialOrigin dials /ws with an explicit Origin header and no join handshake.
func dialOrigin(srv *httptest.Server, nick, origin string) (*websocket.Conn, *http.Response, error) {
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?nickname=" + nick
	return websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": []string{origin}})
}

// dialRaw connects with a same-host Origin and returns without reading anything.
func dialRaw(t *testing.T, srv *httptest.Server, nick string) *websocket.Conn {
	t.Helper()
	conn, _, err := dialOrigin(srv, nick, srv.URL)
	if err != nil {
		t.Fatalf("dial %s: %v", nick, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// dial connects and waits for the client's own join notice, so it is known to
// be registered with the room when dial returns.
func dial(t *testing.T, srv *httptest.Server, nick string) *websocket.Conn {
	t.Helper()
	conn := dialRaw(t, srv, nick)
	readUntil(t, conn, nick+" joined! 🎉")
	return conn
}

// readText reads one message, failing the test on error or deadline.
func readText(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(testDeadline))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(msg)
}

// readUntil skips messages until one equals want.
func readUntil(t *testing.T, conn *websocket.Conn, want string) {
	t.Helper()
	for {
		if got := readText(t, conn); got == want {
			return
		}
	}
}

// expectReadError reads (skipping messages) until the connection errors.
// A deadline expiry counts as a failure: the connection must really be closed.
func expectReadError(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(testDeadline))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatalf("connection still open after %v: %v", testDeadline, err)
			}
			return
		}
	}
}

// expectCloseCode reads (skipping messages) until the connection errors and
// requires the error to be a close frame with the given code.
func expectCloseCode(t *testing.T, conn *websocket.Conn, code int) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(testDeadline))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			var ce *websocket.CloseError
			if !errors.As(err, &ce) {
				t.Fatalf("expected close frame %d, got %v", code, err)
			}
			if ce.Code != code {
				t.Fatalf("close code = %d, want %d", ce.Code, code)
			}
			return
		}
	}
}

// expectLeaveWithoutBroadcast reads from observer until nick leaves, failing
// if any message from nick was broadcast first.
func expectLeaveWithoutBroadcast(t *testing.T, observer *websocket.Conn, nick string) {
	t.Helper()
	for {
		msg := readText(t, observer)
		if strings.HasPrefix(msg, nick+": ") {
			t.Fatalf("rejected message was broadcast: %q", msg)
		}
		if msg == nick+" left. 👋" {
			return
		}
	}
}

// startReader reads in the background (which also answers pings) and forwards
// messages; the channel is closed when the connection errors.
func startReader(conn *websocket.Conn) <-chan string {
	ch := make(chan string, 64)
	go func() {
		defer close(ch)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			ch <- string(msg)
		}
	}()
	return ch
}

// recvUntil waits for want on ch, failing if ch closes or the deadline passes.
func recvUntil(t *testing.T, ch <-chan string, want string) {
	t.Helper()
	timeout := time.After(testDeadline)
	for {
		select {
		case msg, ok := <-ch:
			if !ok {
				t.Fatalf("connection closed while waiting for %q", want)
			}
			if msg == want {
				return
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %q", want)
		}
	}
}

// CHAT-01: a message from one client reaches every client with the nickname prefix.
func TestBroadcastReachesAllClients(t *testing.T) {
	srv, _ := startServer(t, NewRoom(nil))
	a := dial(t, srv, "A")
	b := dial(t, srv, "B")

	if err := a.WriteMessage(websocket.TextMessage, []byte("hola")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, a, "A: hola")
	readUntil(t, b, "A: hola")
}

// SRV-01: origin validation.
func TestWebSocketOriginCheck(t *testing.T) {
	tests := []struct {
		name    string
		allowed []string
		origin  func(srv *httptest.Server) string
		wantOK  bool
	}{
		{"foreign origin rejected", nil, func(*httptest.Server) string { return "https://evil.example" }, false},
		{"null origin rejected", nil, func(*httptest.Server) string { return "null" }, false},
		{"same host accepted", nil, func(s *httptest.Server) string { return s.URL }, true},
		{"allowlisted origin accepted", []string{"http://allowed.example"}, func(*httptest.Server) string { return "http://allowed.example" }, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := startServer(t, NewRoom(tt.allowed))
			conn, resp, err := dialOrigin(srv, "A", tt.origin(srv))
			if tt.wantOK {
				if err != nil {
					t.Fatalf("expected successful dial, got %v", err)
				}
				conn.Close()
				return
			}
			if err == nil {
				conn.Close()
				t.Fatal("expected dial to fail")
			}
			if resp == nil || resp.StatusCode != http.StatusForbidden {
				t.Fatalf("expected 403, got resp=%v err=%v", resp, err)
			}
		})
	}
}

// CHAT-06: oversized frames close the sender; frames at the limit pass.
func TestOversizedMessageClosesSender(t *testing.T) {
	room := NewRoom(nil)
	room.maxMessageSize = 16
	srv, _ := startServer(t, room)
	a := dial(t, srv, "A")
	b := dial(t, srv, "B")

	if err := a.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", 17))); err != nil {
		t.Fatal(err)
	}
	expectReadError(t, a)

	// B must see A leave and must never see A's oversized message.
	expectLeaveWithoutBroadcast(t, b, "A")
}

// CHAT-06: a frame of exactly maxMessageSize is accepted.
func TestMessageAtSizeLimitIsBroadcast(t *testing.T) {
	room := NewRoom(nil)
	room.maxMessageSize = 16
	srv, _ := startServer(t, room)
	a := dial(t, srv, "A")
	b := dial(t, srv, "B")

	payload := strings.Repeat("x", 16)
	if err := a.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
		t.Fatal(err)
	}
	readUntil(t, b, "A: "+payload)
}

// shortPings configures keepalive timing for the ping tests.
func shortPings(room *Room) {
	room.pongWait = 500 * time.Millisecond
	room.pingPeriod = room.pongWait * 9 / 10
}

// CHAT-07: a client that never reads never answers pings and gets dropped.
func TestUnresponsiveClientIsDropped(t *testing.T) {
	room := NewRoom(nil)
	shortPings(room)
	srv, _ := startServer(t, room)

	observer := startReader(dialRaw(t, srv, "B")) // reads (and answers pings) from the start
	recvUntil(t, observer, "B joined! 🎉")
	dialRaw(t, srv, "X") // never calls ReadMessage

	recvUntil(t, observer, "X left. 👋")
}

// CHAT-07: a client that keeps reading answers pings and stays connected.
func TestResponsiveClientSurvivesPingPeriods(t *testing.T) {
	room := NewRoom(nil)
	shortPings(room)
	srv, _ := startServer(t, room)

	a := dialRaw(t, srv, "A")
	aMsgs := startReader(a)
	bMsgs := startReader(dialRaw(t, srv, "B"))
	recvUntil(t, aMsgs, "A joined! 🎉")
	recvUntil(t, bMsgs, "B joined! 🎉")

	// Wait well past pongWait: without pong replies both would be dropped.
	time.Sleep(2 * room.pongWait)

	if err := a.WriteMessage(websocket.TextMessage, []byte("still here")); err != nil {
		t.Fatal(err)
	}
	recvUntil(t, bMsgs, "A: still here")
	recvUntil(t, aMsgs, "A: still here")
}

// CHAT-07: any received frame (not only a pong) extends the read deadline.
// A never reads, so it never answers pings, but it keeps sending text frames
// for longer than pongWait and must stay connected.
func TestAnyFrameExtendsReadDeadline(t *testing.T) {
	room := NewRoom(nil)
	shortPings(room)
	srv, _ := startServer(t, room)

	bMsgs := startReader(dialRaw(t, srv, "B"))
	recvUntil(t, bMsgs, "B joined! 🎉")
	a := dialRaw(t, srv, "A")

	const frames = 8
	interval := room.pongWait / 3 // 8 frames span about 2.7x pongWait
	for i := 0; i < frames; i++ {
		if err := a.WriteMessage(websocket.TextMessage, []byte("m")); err != nil {
			t.Fatalf("frame %d: write failed, connection dropped: %v", i, err)
		}
		time.Sleep(interval) // pacing only; the assertions are the receives below
	}
	for i := 0; i < frames; i++ {
		recvUntil(t, bMsgs, "A: m")
	}
}

// CHAT-09: binary frames close the sender with 1003 and are not broadcast.
func TestBinaryFrameClosesSender(t *testing.T) {
	srv, _ := startServer(t, NewRoom(nil))
	a := dial(t, srv, "A")
	b := dial(t, srv, "B")

	if err := a.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	expectCloseCode(t, a, websocket.CloseUnsupportedData)
	expectLeaveWithoutBroadcast(t, b, "A")
}

// CHAT-09: invalid UTF-8 text closes the sender with 1007 and is not broadcast.
func TestInvalidUTF8ClosesSender(t *testing.T) {
	srv, _ := startServer(t, NewRoom(nil))
	a := dial(t, srv, "A")
	b := dial(t, srv, "B")

	if err := a.WriteMessage(websocket.TextMessage, []byte{'h', 0xff}); err != nil {
		t.Fatal(err)
	}
	expectCloseCode(t, a, websocket.CloseInvalidFramePayloadData)
	expectLeaveWithoutBroadcast(t, b, "A")
}

// CHAT-04/CHAT-09: nicknames are normalized before they reach any frame.
func TestNicknameNormalization(t *testing.T) {
	tests := []struct {
		name  string
		query string // raw (already escaped) value of ?nickname=
		want  string
	}{
		{"none", "", "Anonymous"},
		{"whitespace", "%20%20", "Anonymous"},
		{"invalid utf8", "%ff", "\uFFFD"},
		{"40 runes", url.QueryEscape(strings.Repeat("é", 40)), strings.Repeat("é", 32)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := startServer(t, NewRoom(nil))
			conn := dialRaw(t, srv, tt.query)
			if got, want := readText(t, conn), tt.want+" joined! 🎉"; got != want {
				t.Fatalf("join notice = %q, want %q", got, want)
			}
		})
	}
}

// CHAT-05: when the hub drops a slow client, its connection is closed.
// A real slow client cannot be produced deterministically (the kernel buffers
// megabytes before a write blocks, and sendBufferSize is a const), so this
// test registers a server-side Client with a 1-slot send buffer whose pumps are
// not running yet. Its own join notice fills the buffer, a broadcast from
// another client overflows it, and the hub drops it. Only then are the pumps
// started: writePump must see the closed channel and close the connection.
func TestDroppedSlowClientConnectionIsClosed(t *testing.T) {
	room := NewRoom(nil)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/ws", room.HandleWebSocket)
	slowCh := make(chan *Client, 1)
	engine.GET("/slow", func(c *gin.Context) {
		conn, err := room.upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		slow := &Client{conn: conn, send: make(chan []byte, 1), nickname: "slow"}
		room.register <- slow
		slowCh <- slow
	})
	ctx, cancel := context.WithCancel(context.Background())
	go room.Run(ctx)
	srv := httptest.NewServer(engine)
	t.Cleanup(func() {
		cancel()
		srv.Close()
	})

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	peer, _, err := websocket.DefaultDialer.Dial(wsURL+"/slow", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.Close() })
	var slow *Client
	select {
	case slow = <-slowCh:
	case <-time.After(testDeadline):
		t.Fatal("slow client never registered")
	}

	b := dial(t, srv, "B")
	if err := b.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, b, "B: hi") // the broadcast overflowed slow's buffer; the pumps below may start before or after the drop, both must end closed

	go slow.writePump(room)
	go slow.readPump(room)
	expectReadError(t, peer)
}

// countPumps counts goroutines currently running a Client read or write pump.
func countPumps() int {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	count := 0
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "(*Client).readPump") || strings.Contains(g, "(*Client).writePump") {
			count++
		}
	}
	return count
}

// CHAT-08: cancelling the context closes connected clients and the room.
func TestShutdownClosesClients(t *testing.T) {
	room := NewRoom(nil)
	srv, cancel := startServer(t, room)
	a := dial(t, srv, "A")

	cancel()
	expectReadError(t, a)
	select {
	case <-room.done:
	case <-time.After(testDeadline):
		t.Fatal("room.done not closed after cancel")
	}
}

// CHAT-08: connecting after shutdown must not hang and must end up closed.
func TestDialAfterShutdownDoesNotHang(t *testing.T) {
	room := NewRoom(nil)
	srv, cancel := startServer(t, room)
	cancel()
	select {
	case <-room.done:
	case <-time.After(testDeadline):
		t.Fatal("room.done not closed after cancel")
	}

	conn, _, err := dialOrigin(srv, "late", srv.URL)
	if err != nil {
		return // refusing the connection outright is also acceptable
	}
	defer conn.Close()
	expectReadError(t, conn)
}

// CHAT-05: connecting and disconnecting clients must not leak pump goroutines.
func TestNoGoroutineLeakAfterDisconnect(t *testing.T) {
	srv, _ := startServer(t, NewRoom(nil))

	for i := 0; i < 20; i++ {
		dial(t, srv, "c").Close()
	}

	deadline := time.Now().Add(3 * time.Second)
	for {
		n := countPumps()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d client pump goroutines still running 3s after disconnect", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
