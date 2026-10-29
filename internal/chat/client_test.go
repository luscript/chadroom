package chat

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// startServer runs one room behind a Server with no extra allowed origins.
func startServer(t *testing.T, room *Room) (srv *httptest.Server, cancel context.CancelFunc) {
	t.Helper()
	return startRooms(t, nil, room)
}

// startRooms runs a Server over rooms and an httptest server exposing GET /ws.
// Timeout fields must be overridden on every room BEFORE calling it.
func startRooms(t *testing.T, allowed []string, rooms ...*Room) (srv *httptest.Server, cancel context.CancelFunc) {
	t.Helper()
	return startAvatarRooms(t, nil, allowed, rooms...)
}

// startAvatarRooms is startRooms with an avatar validator (nil: avatars off).
func startAvatarRooms(t *testing.T, valid func(string) bool, allowed []string, rooms ...*Room) (srv *httptest.Server, cancel context.CancelFunc) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	s := NewServer(allowed, valid, rooms...)
	engine := gin.New()
	engine.GET("/ws", s.HandleWebSocket)

	ctx, cancel := context.WithCancel(context.Background())
	go s.Run(ctx)
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

// dialRoomRaw connects to the named room with a same-host Origin and returns
// without reading anything.
func dialRoomRaw(t *testing.T, srv *httptest.Server, room, nick string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?nickname=" + nick + "&room=" + url.QueryEscape(room)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": []string{srv.URL}})
	if err != nil {
		t.Fatalf("dial %s in %s: %v", nick, room, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// dialRoom is dial for a named room.
func dialRoom(t *testing.T, srv *httptest.Server, room, nick string) (*websocket.Conn, string) {
	t.Helper()
	conn := dialRoomRaw(t, srv, room, nick)
	m := readMsg(t, conn)
	if m.Type != typeWelcome {
		t.Fatalf("first frame = %+v, want welcome", m)
	}
	decodeUsers(t, readRaw(t, conn))
	decodeHistory(t, readRaw(t, conn))
	return conn, m.From
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

// dial connects and reads the welcome, users and history frames, so the client
// is known to be registered with the room when dial returns and every later
// frame is a 4-field envelope. It returns the assigned nickname.
func dial(t *testing.T, srv *httptest.Server, nick string) (*websocket.Conn, string) {
	t.Helper()
	conn := dialRaw(t, srv, nick)
	m := readMsg(t, conn)
	if m.Type != typeWelcome {
		t.Fatalf("first frame = %+v, want welcome", m)
	}
	decodeUsers(t, readRaw(t, conn))
	decodeHistory(t, readRaw(t, conn))
	return conn, m.From
}

// readRaw reads one frame, failing the test on error or deadline.
func readRaw(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(testDeadline))
	_, msg, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return msg
}

// readMsg reads and decodes one 4-field frame.
func readMsg(t *testing.T, conn *websocket.Conn) message {
	t.Helper()
	return decode(t, readRaw(t, conn))
}

// readUntil skips frames until one matches (typ, from, text); ts is ignored.
func readUntil(t *testing.T, conn *websocket.Conn, typ, from, text string) {
	t.Helper()
	for {
		m := readMsg(t, conn)
		if m.Type == typ && m.From == from && m.Text == text {
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
// if any chat message from nick was broadcast first.
func expectLeaveWithoutBroadcast(t *testing.T, observer *websocket.Conn, nick string) {
	t.Helper()
	for {
		m := readMsg(t, observer)
		if m.Type == typeChat && m.From == nick {
			t.Fatalf("rejected message was broadcast: %+v", m)
		}
		if m.Type == typeLeave && m.From == nick {
			return
		}
	}
}

// startReader reads in the background (which also answers pings) and forwards
// decoded frames; the channel is closed when the connection errors.
func startReader(t *testing.T, conn *websocket.Conn) <-chan message {
	ch := make(chan message, 64)
	go func() {
		defer close(ch)
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var m message
			if err := json.Unmarshal(msg, &m); err != nil {
				t.Errorf("invalid JSON frame %q: %v", msg, err)
				return
			}
			ch <- m
		}
	}()
	return ch
}

// recvUntil waits for (typ, from, text) on ch, failing if ch closes or the deadline passes.
func recvUntil(t *testing.T, ch <-chan message, typ, from, text string) {
	t.Helper()
	timeout := time.After(testDeadline)
	for {
		select {
		case m, ok := <-ch:
			if !ok {
				t.Fatalf("connection closed while waiting for (%s, %q, %q)", typ, from, text)
			}
			if m.Type == typ && m.From == from && m.Text == text {
				return
			}
		case <-timeout:
			t.Fatalf("timed out waiting for (%s, %q, %q)", typ, from, text)
		}
	}
}

// CHAT-01: a message from one client reaches every client as a chat envelope.
func TestBroadcastReachesAllClients(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

	if err := a.WriteMessage(websocket.TextMessage, []byte("hola")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, a, typeChat, "A", "hola")
	readUntil(t, b, typeChat, "A", "hola")
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
			srv, _ := startRooms(t, tt.allowed, NewRoom("general"))
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
	room := NewRoom("general")
	room.maxMessageSize = 16
	srv, _ := startServer(t, room)
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

	if err := a.WriteMessage(websocket.TextMessage, []byte(strings.Repeat("x", 17))); err != nil {
		t.Fatal(err)
	}
	expectReadError(t, a)

	// B must see A leave and must never see A's oversized message.
	expectLeaveWithoutBroadcast(t, b, "A")
}

// CHAT-06: a frame of exactly the default limit, made of characters that need
// JSON escaping, is accepted and its decoded text equals the frame.
func TestMessageAtSizeLimitIsBroadcast(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

	payload := strings.Repeat(`"`, defaultMaxMessageSize)
	if err := a.WriteMessage(websocket.TextMessage, []byte(payload)); err != nil {
		t.Fatal(err)
	}
	readUntil(t, b, typeChat, "A", payload)
}

// shortPings configures keepalive timing for the ping tests.
func shortPings(room *Room) {
	room.pongWait = 500 * time.Millisecond
	room.pingPeriod = room.pongWait * 9 / 10
}

// CHAT-07: a client that never reads never answers pings and gets dropped.
func TestUnresponsiveClientIsDropped(t *testing.T) {
	room := NewRoom("general")
	shortPings(room)
	srv, _ := startServer(t, room)

	observer := startReader(t, dialRaw(t, srv, "B")) // reads (and answers pings) from the start
	recvUntil(t, observer, typeJoin, "B", "")
	dialRaw(t, srv, "X") // never calls ReadMessage

	recvUntil(t, observer, typeLeave, "X", "")
}

// CHAT-07: a client that keeps reading answers pings and stays connected.
func TestResponsiveClientSurvivesPingPeriods(t *testing.T) {
	room := NewRoom("general")
	shortPings(room)
	srv, _ := startServer(t, room)

	a := dialRaw(t, srv, "A")
	aMsgs := startReader(t, a)
	bMsgs := startReader(t, dialRaw(t, srv, "B"))
	recvUntil(t, aMsgs, typeJoin, "A", "")
	recvUntil(t, bMsgs, typeJoin, "B", "")

	// Wait well past pongWait: without pong replies both would be dropped.
	time.Sleep(2 * room.pongWait)

	if err := a.WriteMessage(websocket.TextMessage, []byte("still here")); err != nil {
		t.Fatal(err)
	}
	recvUntil(t, bMsgs, typeChat, "A", "still here")
	recvUntil(t, aMsgs, typeChat, "A", "still here")
}

// CHAT-07: any received frame (not only a pong) extends the read deadline.
// A never reads, so it never answers pings, but it keeps sending text frames
// for longer than pongWait and must stay connected.
func TestAnyFrameExtendsReadDeadline(t *testing.T) {
	room := NewRoom("general")
	shortPings(room)
	srv, _ := startServer(t, room)

	bMsgs := startReader(t, dialRaw(t, srv, "B"))
	recvUntil(t, bMsgs, typeJoin, "B", "")
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
		recvUntil(t, bMsgs, typeChat, "A", "m")
	}
}

// CHAT-09: binary frames close the sender with 1003 and are not broadcast.
func TestBinaryFrameClosesSender(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

	if err := a.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	expectCloseCode(t, a, websocket.CloseUnsupportedData)
	expectLeaveWithoutBroadcast(t, b, "A")
}

// CHAT-09: invalid UTF-8 text closes the sender with 1007 and is not broadcast.
func TestInvalidUTF8ClosesSender(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

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
			srv, _ := startServer(t, NewRoom("general"))
			conn := dialRaw(t, srv, tt.query)
			if m := readMsg(t, conn); m.Type != typeWelcome || m.From != tt.want {
				t.Fatalf("first frame = %+v, want welcome from %q", m, tt.want)
			}
		})
	}
}

// CHAT-05: when the hub drops a slow client, its connection is closed.
// A real slow client cannot be produced deterministically (the kernel buffers
// megabytes before a write blocks, and sendBufferSize is a const), so this
// test registers a server-side Client with a privateFrames+1 slot send buffer whose pumps are
// not running yet. Its private frames and own join fill the buffer,
// the join of another client overflows it, and the hub drops it. Only then are
// the pumps started: writePump must see the closed channel and close the connection.
func TestDroppedSlowClientConnectionIsClosed(t *testing.T) {
	room := NewRoom("general")
	s := NewServer(nil, nil, room)
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.GET("/ws", s.HandleWebSocket)
	slowCh := make(chan *Client, 1)
	engine.GET("/slow", func(c *gin.Context) {
		conn, err := s.upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			return
		}
		slow := &Client{conn: conn, send: make(chan []byte, privateFrames+1), registered: make(chan struct{}), nickname: "slow"}
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

	b, _ := dial(t, srv, "B")
	if err := b.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, b, typeChat, "B", "hi") // B's join already overflowed slow's buffer; the pumps below may start before or after the drop, both must end closed

	go slow.writePump(room)
	go slow.readPump(room)
	expectReadError(t, peer)
}

// CHAT-17/18: a newcomer over a real socket gets welcome, users and the
// history before anything else.
func TestJoinReceivesUsersAndHistory(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	send(t, a, "hola")
	readUntil(t, a, typeChat, "A", "hola")

	b := dialRaw(t, srv, "B")
	if m := readMsg(t, b); m.Type != typeWelcome || m.From != "B" {
		t.Fatalf("first frame = %+v, want welcome B", m)
	}
	expectUsers(t, decodeUsers(t, readRaw(t, b)), "A", "B")
	expectHistory(t, decodeHistory(t, readRaw(t, b)), message{Type: typeChat, From: "A", Text: "hola"})
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
	room := NewRoom("general")
	srv, cancel := startServer(t, room)
	a, _ := dial(t, srv, "A")

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
	room := NewRoom("general")
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
	srv, _ := startServer(t, NewRoom("general"))

	for i := 0; i < 20; i++ {
		conn, _ := dial(t, srv, "c")
		conn.Close()
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

// nextChat reads until a chat frame arrives, failing on a join frame for forbidden.
func nextChat(t *testing.T, conn *websocket.Conn, forbiddenJoin string) message {
	t.Helper()
	for {
		m := readMsg(t, conn)
		if m.Type == typeJoin && m.From == forbiddenJoin {
			t.Fatalf("unexpected join envelope: %+v", m)
		}
		if m.Type == typeChat {
			return m
		}
	}
}

func send(t *testing.T, conn *websocket.Conn, text string) {
	t.Helper()
	if err := conn.WriteMessage(websocket.TextMessage, []byte(text)); err != nil {
		t.Fatal(err)
	}
}

// CHAT-12: concurrent clients asking for the same nickname get distinct names.
func TestConcurrentSameNickname(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	const n = 20

	type result struct {
		name string
		err  error
	}
	results := make(chan result, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, _, err := dialOrigin(srv, "bob", srv.URL)
			if err != nil {
				results <- result{err: err}
				return
			}
			t.Cleanup(func() { conn.Close() })
			conn.SetReadDeadline(time.Now().Add(testDeadline))
			_, raw, err := conn.ReadMessage()
			if err != nil {
				results <- result{err: err}
				return
			}
			var m message
			if err := json.Unmarshal(raw, &m); err != nil || m.Type != typeWelcome {
				results <- result{err: errors.New("bad welcome: " + string(raw))}
				return
			}
			results <- result{name: m.From}
		}()
	}
	wg.Wait()
	close(results)

	seen := map[string]string{}
	for r := range results {
		if r.err != nil {
			t.Fatal(r.err)
		}
		if c := len([]rune(r.name)); c > maxNicknameRunes {
			t.Errorf("name %q has %d runes", r.name, c)
		}
		k := foldKey(r.name)
		if prev, dup := seen[k]; dup {
			t.Errorf("duplicate names %q and %q", prev, r.name)
		}
		seen[k] = r.name
	}
	if len(seen) != n {
		t.Fatalf("got %d distinct names, want %d", len(seen), n)
	}
}

// CHAT-12: collisions ignore case, and a freed name can be reused.
func TestCaseInsensitiveCollision(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, na := dial(t, srv, "Bob")
	b, nb := dial(t, srv, "bob")
	c, nc := dial(t, srv, "BOB")
	if na != "Bob" || nb != "bob (2)" || nc != "BOB (3)" {
		t.Fatalf("names = %q, %q, %q", na, nb, nc)
	}

	a.Close()
	readUntil(t, b, typeLeave, "Bob", "")
	readUntil(t, c, typeLeave, "Bob", "")

	_, nd := dial(t, srv, "bob")
	if nd != "bob" {
		t.Fatalf("reused name = %q, want bob", nd)
	}
}

// CHAT-13: whitespace-only messages are dropped silently; others are untouched.
func TestEmptyMessagesDropped(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

	for _, s := range []string{"", "   ", "\n\t", "x"} {
		send(t, a, s)
	}
	if m := nextChat(t, b, ""); m.From != "A" || m.Text != "x" {
		t.Fatalf("first chat = %+v, want text x from A", m)
	}
	send(t, a, "y")
	if m := nextChat(t, b, ""); m.Text != "y" {
		t.Fatalf("second chat = %+v, want y", m)
	}
	send(t, a, "  hi  ")
	if m := nextChat(t, b, ""); m.Text != "  hi  " {
		t.Fatalf("chat text = %q, want surrounding whitespace preserved", m.Text)
	}
}

// CHAT-10: text that imitates a system notice is just chat; JSON escaping round-trips.
func TestForgedNoticeIsChat(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

	texts := []string{"Bob joined! 🎉", "\nBob: hi", "say \"hi\" \\ <b>\t\n"}
	for _, text := range texts {
		send(t, a, text)
	}
	for _, text := range texts {
		m := nextChat(t, b, "Bob")
		if m.From != "A" || m.Text != text {
			t.Fatalf("chat = %+v, want from A with text %q", m, text)
		}
	}
}

// CHAT-04/CHAT-09: control and bidi characters never reach the assigned nickname.
func TestNicknameControlAndBidiStripped(t *testing.T) {
	tests := []struct{ name, nick, want string }{
		{"newline", "Bob\nAdmin", "BobAdmin"},
		{"bidi and spaces", "‮  Bob  ‬", "Bob"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := startServer(t, NewRoom("general"))
			_, got := dial(t, srv, url.QueryEscape(tt.nick))
			if got != tt.want {
				t.Fatalf("nickname = %q, want %q", got, tt.want)
			}
		})
	}
}

// nextNonJoin reads frames, skipping join notices, and returns the first other one.
func nextNonJoin(t *testing.T, conn *websocket.Conn) []byte {
	t.Helper()
	for {
		raw := readRaw(t, conn)
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			t.Fatalf("invalid JSON %q: %v", raw, err)
		}
		if head.Type != typeJoin {
			return raw
		}
	}
}

// readDM reads the next frame (skipping joins) and requires it to be a dm.
func readDM(t *testing.T, conn *websocket.Conn) dmFrame {
	t.Helper()
	return decodeDM(t, nextNonJoin(t, conn))
}

// readDMError reads the next frame (skipping joins) and requires it to be a dm_error.
func readDMError(t *testing.T, conn *websocket.Conn) dmErrorFrame {
	t.Helper()
	return decodeDMError(t, nextNonJoin(t, conn))
}

// CHAT-21, CHAT-13: classification of inbound frames.
// manyFrame builds an add_many frame for ids.
func manyFrame(ids []string) string {
	b, _ := json.Marshal(ids)
	return `{"type":"music","action":"add_many","videos":` + string(b) + `}`
}

func TestParseInbound(t *testing.T) {
	long := strings.Repeat("ñ", 40)
	tests := []struct {
		name    string
		frame   string
		want    inbound
		discard bool
	}{
		{"plain text", "hola", inbound{text: "hola"}, false},
		{"chat envelope", `{"type":"chat","text":"hi"}`, inbound{text: "hi"}, false},
		{"chat envelope keeps whitespace", `{"type":"chat","text":"  hi  "}`, inbound{text: "  hi  "}, false},
		{"chat blank", `{"type":"chat","text":"  "}`, inbound{}, true},
		{"chat text not a string", `{"type":"chat","text":5}`, inbound{text: `{"type":"chat","text":5}`}, false},
		{"chat without text", `{"type":"chat"}`, inbound{text: `{"type":"chat"}`}, false},
		{"unknown type", `{"type":"poke"}`, inbound{text: `{"type":"poke"}`}, false},
		{"missing type", `{"text":"x"}`, inbound{text: `{"text":"x"}`}, false},
		{"array", `[1,2]`, inbound{text: `[1,2]`}, false},
		{"json string", `"dm"`, inbound{text: `"dm"`}, false},
		{"number", `42`, inbound{text: `42`}, false},
		{"null", `null`, inbound{text: `null`}, false},
		{"malformed", `{"type":"dm"`, inbound{text: `{"type":"dm"`}, false},
		{"type is case sensitive", `{"TYPE":"dm","to":"B","text":"x"}`, inbound{text: `{"TYPE":"dm","to":"B","text":"x"}`}, false},
		{"dm", `{"type":"dm","to":"B","text":"x"}`, inbound{kind: inboundDM, to: "B", text: "x"}, false},
		{"dm to is cleaned", "{\"type\":\"dm\",\"to\":\"  bob‮ \",\"text\":\"x\"}", inbound{kind: inboundDM, to: "bob", text: "x"}, false},
		{"dm to truncated", `{"type":"dm","to":"` + long + `","text":"x"}`, inbound{kind: inboundDM, to: strings.Repeat("ñ", 32), text: "x"}, false},
		{"dm extra field ignored", `{"type":"dm","to":"B","text":"x","extra":1}`, inbound{kind: inboundDM, to: "B", text: "x"}, false},
		{"dm from ignored", `{"type":"dm","from":"C","to":"B","text":"x"}`, inbound{kind: inboundDM, to: "B", text: "x"}, false},
		{"dm without to", `{"type":"dm","text":"x"}`, inbound{kind: inboundBadDM}, false},
		{"dm empty to", `{"type":"dm","to":"","text":"x"}`, inbound{kind: inboundBadDM}, false},
		{"dm blank to", `{"type":"dm","to":"   ","text":"x"}`, inbound{kind: inboundBadDM}, false},
		{"dm to only bidi", "{\"type\":\"dm\",\"to\":\"‮\",\"text\":\"x\"}", inbound{kind: inboundBadDM}, false},
		{"dm to number", `{"type":"dm","to":7,"text":"x"}`, inbound{kind: inboundBadDM}, false},
		{"dm to null", `{"type":"dm","to":null,"text":"x"}`, inbound{kind: inboundBadDM}, false},
		{"dm without text", `{"type":"dm","to":"B"}`, inbound{kind: inboundBadDM}, false},
		{"dm text null", `{"type":"dm","to":"B","text":null}`, inbound{kind: inboundBadDM}, false},
		{"invalid wins over blank", `{"type":"dm","to":7,"text":" "}`, inbound{kind: inboundBadDM}, false},
		{"dm blank text", `{"type":"dm","to":"B","text":"\n"}`, inbound{}, true},
		{"typing room", `{"type":"typing"}`, inbound{kind: inboundTyping}, false},
		{"typing blank text ignored", `{"type":"typing","text":"  "}`, inbound{kind: inboundTyping}, false},
		{"typing text ignored", `{"type":"typing","text":"hola"}`, inbound{kind: inboundTyping}, false},
		{"typing from ignored", `{"type":"typing","from":"C"}`, inbound{kind: inboundTyping}, false},
		{"typing extra field ignored", `{"type":"typing","extra":1}`, inbound{kind: inboundTyping}, false},
		{"typing dm", `{"type":"typing","to":"B"}`, inbound{kind: inboundTyping, to: "B"}, false},
		{"typing to is cleaned", "{\"type\":\"typing\",\"to\":\"  bob‮ \"}", inbound{kind: inboundTyping, to: "bob"}, false},
		{"typing to truncated", `{"type":"typing","to":"` + long + `"}`, inbound{kind: inboundTyping, to: strings.Repeat("ñ", 32)}, false},
		{"typing to number", `{"type":"typing","to":7}`, inbound{}, true},
		{"typing to null", `{"type":"typing","to":null}`, inbound{}, true},
		{"typing to object", `{"type":"typing","to":{}}`, inbound{}, true},
		{"typing empty to", `{"type":"typing","to":""}`, inbound{}, true},
		{"typing blank to", `{"type":"typing","to":"   "}`, inbound{}, true},
		{"typing to only bidi", "{\"type\":\"typing\",\"to\":\"‮\"}", inbound{}, true},
		{"typing key case sensitive", `{"TYPE":"typing"}`, inbound{text: `{"TYPE":"typing"}`}, false},
		{"typing value case sensitive", `{"type":"Typing"}`, inbound{text: `{"type":"Typing"}`}, false},
		{"music add", `{"type":"music","action":"add","video":"dQw4w9WgXcQ"}`, inbound{kind: inboundMusic, music: musicCmd{action: musicAdd, video: "dQw4w9WgXcQ"}}, false},
		{"music add dashes and underscores", `{"type":"music","action":"add","video":"a-_B1c2D3e4"}`, inbound{kind: inboundMusic, music: musicCmd{action: musicAdd, video: "a-_B1c2D3e4"}}, false},
		{"music add extra fields ignored", `{"type":"music","action":"add","video":"dQw4w9WgXcQ","pos":"x","text":"hola","by":"C"}`, inbound{kind: inboundMusic, music: musicCmd{action: musicAdd, video: "dQw4w9WgXcQ"}}, false},
		{"music play", `{"type":"music","action":"play"}`, inbound{kind: inboundMusic, music: musicCmd{action: musicPlay}}, false},
		{"music pause", `{"type":"music","action":"pause","extra":1}`, inbound{kind: inboundMusic, music: musicCmd{action: musicPause}}, false},
		{"music play with text is not chat", `{"type":"music","action":"play","text":"hola"}`, inbound{kind: inboundMusic, music: musicCmd{action: musicPlay}}, false},
		{"music seek", `{"type":"music","action":"seek","pos":60000}`, inbound{kind: inboundMusic, music: musicCmd{action: musicSeek, pos: 60000}}, false},
		{"music seek zero", `{"type":"music","action":"seek","pos":0}`, inbound{kind: inboundMusic, music: musicCmd{action: musicSeek}}, false},
		{"music seek max", `{"type":"music","action":"seek","pos":86400000}`, inbound{kind: inboundMusic, music: musicCmd{action: musicSeek, pos: 86400000}}, false},
		{"music without action", `{"type":"music"}`, inbound{}, true},
		{"music unknown action", `{"type":"music","action":"stop"}`, inbound{}, true},
		{"music action case sensitive", `{"type":"music","action":"PLAY"}`, inbound{}, true},
		{"music action not a string", `{"type":"music","action":5}`, inbound{}, true},
		{"music action null", `{"type":"music","action":null}`, inbound{}, true},
		{"music add without video", `{"type":"music","action":"add"}`, inbound{}, true},
		{"music add video 10 chars", `{"type":"music","action":"add","video":"dQw4w9WgXc"}`, inbound{}, true},
		{"music add video 12 chars", `{"type":"music","action":"add","video":"dQw4w9WgXcQQ"}`, inbound{}, true},
		{"music add video url", `{"type":"music","action":"add","video":"https://youtu.be/dQw4w9WgXcQ"}`, inbound{}, true},
		{"music add video trailing newline", `{"type":"music","action":"add","video":"dQw4w9WgXcQ\n"}`, inbound{}, true},
		{"music add video bad char", `{"type":"music","action":"add","video":"dQw4w9WgX!Q"}`, inbound{}, true},
		{"music add video not a string", `{"type":"music","action":"add","video":12345678901}`, inbound{}, true},
		{"music add video null", `{"type":"music","action":"add","video":null}`, inbound{}, true},
		{"music seek without pos", `{"type":"music","action":"seek"}`, inbound{}, true},
		{"music seek pos null", `{"type":"music","action":"seek","pos":null}`, inbound{}, true},
		{"music seek pos string", `{"type":"music","action":"seek","pos":"5"}`, inbound{}, true},
		{"music seek pos bool", `{"type":"music","action":"seek","pos":true}`, inbound{}, true},
		{"music seek pos negative", `{"type":"music","action":"seek","pos":-1}`, inbound{}, true},
		{"music seek pos fraction", `{"type":"music","action":"seek","pos":1.5}`, inbound{}, true},
		{"music seek pos exponent", `{"type":"music","action":"seek","pos":1e3}`, inbound{}, true},
		{"music seek pos over max", `{"type":"music","action":"seek","pos":86400001}`, inbound{}, true},
		{"music seek pos beyond int64", `{"type":"music","action":"seek","pos":9223372036854775808}`, inbound{}, true},
		{"music load is no longer an action", `{"type":"music","action":"load","video":"dQw4w9WgXcQ"}`, inbound{}, true},
		{"music remove", `{"type":"music","action":"remove","id":1}`, inbound{kind: inboundMusic, music: musicCmd{action: musicRemove, id: 1}}, false},
		{"music remove big id", `{"type":"music","action":"remove","id":4611686018427387904}`, inbound{kind: inboundMusic, music: musicCmd{action: musicRemove, id: 1 << 62}}, false},
		{"music remove id zero", `{"type":"music","action":"remove","id":0}`, inbound{}, true},
		{"music remove id negative", `{"type":"music","action":"remove","id":-1}`, inbound{}, true},
		{"music remove id string", `{"type":"music","action":"remove","id":"1"}`, inbound{}, true},
		{"music remove id fraction", `{"type":"music","action":"remove","id":1.5}`, inbound{}, true},
		{"music remove id exponent", `{"type":"music","action":"remove","id":1e3}`, inbound{}, true},
		{"music remove id null", `{"type":"music","action":"remove","id":null}`, inbound{}, true},
		{"music remove without id", `{"type":"music","action":"remove"}`, inbound{}, true},
		{"music remove id beyond int64", `{"type":"music","action":"remove","id":9223372036854775808}`, inbound{}, true},
		{"music skip", `{"type":"music","action":"skip","id":7,"video":"x","text":"hola"}`, inbound{kind: inboundMusic, music: musicCmd{action: musicSkip}}, false},
		{"music ended", `{"type":"music","action":"ended","seq":1}`, inbound{kind: inboundMusic, music: musicCmd{action: musicEnded, seq: 1}}, false},
		{"music ended seq zero", `{"type":"music","action":"ended","seq":0}`, inbound{}, true},
		{"music ended seq string", `{"type":"music","action":"ended","seq":"1"}`, inbound{}, true},
		{"music ended seq fraction", `{"type":"music","action":"ended","seq":1.5}`, inbound{}, true},
		{"music ended without seq", `{"type":"music","action":"ended"}`, inbound{}, true},
		{"music add_many one", `{"type":"music","action":"add_many","videos":["dQw4w9WgXcQ"]}`, inbound{kind: inboundMusic, music: musicCmd{action: musicAddMany, videos: []string{v1}}}, false},
		{"music add_many 50", manyFrame(idList(50)), inbound{kind: inboundMusic, music: musicCmd{action: musicAddMany, videos: idList(50)}}, false},
		{"music add_many repeated ids", manyFrame([]string{v1, v1}), inbound{kind: inboundMusic, music: musicCmd{action: musicAddMany, videos: []string{v1, v1}}}, false},
		{"music add_many extra keys ignored", `{"type":"music","action":"add_many","videos":["dQw4w9WgXcQ"],"video":"x","text":"hola","by":"C"}`, inbound{kind: inboundMusic, music: musicCmd{action: musicAddMany, videos: []string{v1}}}, false},
		{"music add_many without videos", `{"type":"music","action":"add_many"}`, inbound{}, true},
		{"music add_many videos null", `{"type":"music","action":"add_many","videos":null}`, inbound{}, true},
		{"music add_many videos empty", `{"type":"music","action":"add_many","videos":[]}`, inbound{}, true},
		{"music add_many 51", manyFrame(idList(51)), inbound{}, true},
		{"music add_many videos string", `{"type":"music","action":"add_many","videos":"dQw4w9WgXcQ"}`, inbound{}, true},
		{"music add_many videos object", `{"type":"music","action":"add_many","videos":{}}`, inbound{}, true},
		{"music add_many element null", `{"type":"music","action":"add_many","videos":[null]}`, inbound{}, true},
		{"music add_many element number", `{"type":"music","action":"add_many","videos":[123]}`, inbound{}, true},
		{"music add_many element array", `{"type":"music","action":"add_many","videos":[["dQw4w9WgXcQ"]]}`, inbound{}, true},
		{"music add_many one bad id among good", `{"type":"music","action":"add_many","videos":["dQw4w9WgXcQ","abc","aaaaaaaaaaa"]}`, inbound{}, true},
		{"music add_many id trailing newline", `{"type":"music","action":"add_many","videos":["dQw4w9WgXcQ\n"]}`, inbound{}, true},
		{"music add_many url instead of id", `{"type":"music","action":"add_many","videos":["https://youtu.be/dQw4w9WgXcQ"]}`, inbound{}, true},
		{"music key case sensitive", `{"TYPE":"music","action":"play"}`, inbound{text: `{"TYPE":"music","action":"play"}`}, false},
		{"music value case sensitive", `{"type":"MUSIC","action":"play"}`, inbound{text: `{"type":"MUSIC","action":"play"}`}, false},
		{"empty frame", ``, inbound{}, true},
		{"spaces frame", `  `, inbound{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := parseInbound([]byte(tt.frame))
			if ok == tt.discard {
				t.Fatalf("ok = %v, want %v", ok, !tt.discard)
			}
			if !tt.discard && !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// CHAT-21, CHAT-22: a dm reaches only its recipient and its sender, with the same ts.
func TestDMOverWebSocket(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")
	c, _ := dial(t, srv, "C")

	send(t, a, `{"type":"dm","to":"B","text":"secreto"}`)
	toB, toA := readDM(t, b), readDM(t, a)
	for _, f := range []dmFrame{toB, toA} {
		if f.From != "A" || f.To != "B" || f.Text != "secreto" {
			t.Fatalf("dm = %+v, want A -> B secreto", f)
		}
	}
	if toB.TS != toA.TS {
		t.Fatalf("ts differ: %d vs %d", toB.TS, toA.TS)
	}

	// CHAT-22: a forged "from" in the envelope is ignored.
	send(t, a, `{"type":"dm","from":"C","to":"B","text":"hi"}`)
	if f := readDM(t, b); f.From != "A" || f.To != "B" || f.Text != "hi" {
		t.Fatalf("forged dm = %+v, want from A", f)
	}
	readDM(t, a)

	send(t, c, "sync")
	if m := nextChat(t, c, ""); m.Text != "sync" { // a dm would fail decode first
		t.Fatalf("C got %+v, want sync", m)
	}
}

// CHAT-21: the envelope never sets the sender, and non-envelope JSON stays room chat.
func TestChatEnvelopeAndForgery(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

	send(t, a, `{"type":"chat","from":"Bob","text":"hi"}`)
	if m := nextChat(t, b, ""); m.From != "A" || m.Text != "hi" {
		t.Fatalf("chat = %+v, want A hi", m)
	}
	for _, frame := range []string{`{"type":"poke"}`, `[1,2]`, `{"text":"x"}`, `{"type":"chat","text":5}`} {
		send(t, a, frame)
		if m := nextChat(t, b, ""); m.From != "A" || m.Text != frame {
			t.Fatalf("chat = %+v, want A with the whole frame %q", m, frame)
		}
	}
}

// CHAT-13: blank envelope text is discarded without any frame.
func TestDMWhitespaceDiscarded(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

	send(t, a, `{"type":"chat","text":"  "}`)
	send(t, a, `{"type":"dm","to":"B","text":"\n"}`)
	send(t, a, "hi")
	for _, conn := range []*websocket.Conn{a, b} {
		if m := nextChat(t, conn, ""); m.Text != "hi" {
			t.Fatalf("first chat = %+v, want hi", m)
		}
	}
}

// CHAT-23: invalid dms are answered to the sender and never close the connection.
func TestInvalidDMKeepsConnection(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

	for _, frame := range []string{
		`{"type":"dm","text":"x"}`,
		`{"type":"dm","to":"   ","text":"x"}`,
		`{"type":"dm","to":7,"text":"x"}`,
		`{"type":"dm","to":"B","text":null}`,
	} {
		send(t, a, frame)
		if e := readDMError(t, a); e.To != "" || e.Reason != reasonInvalid {
			t.Fatalf("%s: dm_error = %+v, want invalid with empty to", frame, e)
		}
	}
	send(t, a, "still open")
	if m := nextChat(t, b, ""); m.Text != "still open" { // B saw no dm or dm_error
		t.Fatalf("B got %+v", m)
	}
}

// CHAT-06: the size limit covers the whole dm envelope.
func TestOversizedDMClosesSender(t *testing.T) {
	room := NewRoom("general")
	room.maxMessageSize = 64
	srv, _ := startServer(t, room)
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

	const prefix = `{"type":"dm","to":"B","text":"`
	frame := prefix + strings.Repeat("x", 65-len(prefix)-2) + `"}`
	if len(frame) != 65 {
		t.Fatalf("frame is %d bytes, want 65", len(frame))
	}
	send(t, a, frame)
	expectReadError(t, a)
	expectLeaveWithoutBroadcast(t, b, "A") // a dm would fail decode first
}

// readTyping reads the next frame (skipping joins) and requires it to be a typing signal.
func readTyping(t *testing.T, conn *websocket.Conn) typingDMFrame {
	t.Helper()
	return decodeTyping(t, nextNonJoin(t, conn))
}

// CHAT-26, CHAT-30: the gate admits one event per interval and a discarded one does not move the window.
func TestRateGate(t *testing.T) {
	base := time.Unix(1000, 0)
	tests := []struct {
		name     string
		interval time.Duration
		steps    []struct {
			at   time.Duration
			want bool
		}
	}{
		{"typing", typingInterval, []struct {
			at   time.Duration
			want bool
		}{
			{0, true},
			{999 * time.Millisecond, false},
			{time.Second, true}, // boundary: exactly one interval later
			{1500 * time.Millisecond, false},
			{2 * time.Second, true}, // the discarded 1.5 s did not reset the window
		}},
		{"music", musicInterval, []struct {
			at   time.Duration
			want bool
		}{
			{0, true},
			{259 * time.Millisecond, false},
			{260 * time.Millisecond, true}, // boundary
			{400 * time.Millisecond, false},
			{520 * time.Millisecond, true}, // the discarded 400 ms did not reset the window
			{780 * time.Millisecond, true}, // 4 admitted within the first second, never 5 (CHAT-30)
			{1039 * time.Millisecond, false},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := rateGate{interval: tt.interval}
			for _, s := range tt.steps {
				if got := g.allow(base.Add(s.at)); got != s.want {
					t.Fatalf("allow(+%v) = %v, want %v", s.at, got, s.want)
				}
			}
		})
	}
}

// CHAT-21, CHAT-26: room signals skip the sender, DM signals reach only the recipient, from is server-assigned.
func TestTypingOverWebSocket(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")
	c, _ := dial(t, srv, "C")

	send(t, a, `{"type":"typing","from":"C"}`)
	for _, conn := range []*websocket.Conn{b, c} {
		if f := readTyping(t, conn); f.From != "A" || f.To != "" {
			t.Fatalf("typing = %+v, want from A without to", f)
		}
	}

	send(t, b, `{"type":"typing","to":"a"}`)
	if f := readTyping(t, a); f.From != "B" || f.To != "A" {
		t.Fatalf("dm typing = %+v, want B -> A", f)
	}

	send(t, c, "sync")
	for _, conn := range []*websocket.Conn{a, b, c} {
		if m := nextChat(t, conn, ""); m.Text != "sync" { // a stray typing frame would fail decode
			t.Fatalf("got %+v, want sync", m)
		}
	}
}

// CHAT-26: a second signal within the interval is dropped and the connection stays usable.
func TestTypingThrottledOverWebSocket(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

	send(t, a, `{"type":"typing"}`)
	send(t, a, `{"type":"typing"}`)
	send(t, a, "hola")
	if f := readTyping(t, b); f.From != "A" {
		t.Fatalf("typing = %+v, want from A", f)
	}
	if m := nextChat(t, b, ""); m.Text != "hola" { // a second typing frame would fail decode
		t.Fatalf("B got %+v, want hola", m)
	}
	if m := nextChat(t, a, ""); m.Text != "hola" {
		t.Fatalf("A got %+v, want hola", m)
	}
}

// CHAT-21: invalid typing frames are discarded without a reply and the connection stays open.
func TestInvalidTypingDiscarded(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

	send(t, a, `{"type":"typing","to":7}`)
	send(t, a, `{"type":"typing","to":"   "}`)
	send(t, a, "hola")
	for _, conn := range []*websocket.Conn{a, b} {
		if m := nextChat(t, conn, ""); m.Text != "hola" {
			t.Fatalf("first chat = %+v, want hola", m)
		}
	}
	// Invalid frames must not have consumed the throttle budget.
	send(t, a, `{"type":"typing"}`)
	if f := readTyping(t, b); f.From != "A" {
		t.Fatalf("typing = %+v, want from A", f)
	}
}

// CHAT-26: typing frames dropped by the throttle still extend the read deadline.
// A never reads (so never answers pings) and sends typing frames for longer than
// pongWait; all but the first per typingInterval are throttled.
func TestThrottledTypingExtendsReadDeadline(t *testing.T) {
	room := NewRoom("general")
	shortPings(room)
	srv, _ := startServer(t, room)

	bMsgs := startReader(t, dialRaw(t, srv, "B"))
	recvUntil(t, bMsgs, typeJoin, "B", "")
	a := dialRaw(t, srv, "A")

	interval := room.pongWait / 3 // 8 frames span about 2.7x pongWait
	for i := 0; i < 8; i++ {
		if err := a.WriteMessage(websocket.TextMessage, []byte(`{"type":"typing"}`)); err != nil {
			t.Fatalf("frame %d: write failed, connection dropped: %v", i, err)
		}
		time.Sleep(interval) // pacing only
	}
	if err := a.WriteMessage(websocket.TextMessage, []byte("alive")); err != nil {
		t.Fatal(err)
	}
	recvUntil(t, bMsgs, typeChat, "A", "alive")
}

// readMusic reads the next frame (skipping joins) and requires it to be a music frame.
func readMusic(t *testing.T, conn *websocket.Conn) musicFrame {
	t.Helper()
	return decodeMusic(t, nextNonJoin(t, conn))
}

// CHAT-21, CHAT-30: a control in a room with a player reaches everyone, sender included.
// In a room without one it is dropped and never becomes chat (the chat sent after it arrives alone).
func TestMusicOverWebSocket(t *testing.T) {
	srv, _ := startRooms(t, nil, NewMusicRoom("musica"), NewRoom("general"))
	a, _ := dialRoom(t, srv, "musica", "A")
	b, _ := dialRoom(t, srv, "musica", "B")

	send(t, a, `{"type":"music","action":"add","video":"dQw4w9WgXcQ"}`)
	for _, conn := range []*websocket.Conn{a, b} {
		if f := readMusic(t, conn); f.Video != "dQw4w9WgXcQ" || !f.Playing || f.By != "A" {
			t.Fatalf("music = %+v, want dQw4w9WgXcQ playing by A", f)
		}
	}

	c, _ := dialRoom(t, srv, "general", "C")
	d, _ := dialRoom(t, srv, "general", "D")
	send(t, c, `{"type":"music","action":"add","video":"dQw4w9WgXcQ"}`)
	send(t, c, `{"type":"music","action":"bogus"}`)
	send(t, c, "hola")
	for _, conn := range []*websocket.Conn{c, d} {
		if m := nextChat(t, conn, ""); m.Text != "hola" { // a music frame would fail decode first
			t.Fatalf("chat = %+v, want hola", m)
		}
	}
}

// CHAT-21, CHAT-30: an add_many is one control: one frame, and a second one right after is throttled.
func TestMusicAddManyOverWebSocket(t *testing.T) {
	srv, _ := startServer(t, NewMusicRoom("musica"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

	send(t, a, manyFrame([]string{v1, "aaaaaaaaaaa", "bbbbbbbbbbb"}))
	send(t, a, manyFrame([]string{"ccccccccccc", "ddddddddddd"})) // within 260 ms: no token left
	send(t, a, "hola")
	for _, conn := range []*websocket.Conn{a, b} {
		f := readMusic(t, conn)
		if f.Video != v1 || !f.Playing || f.By != "A" || len(f.Queue) != 2 {
			t.Fatalf("music = %+v, want %s playing by A with 2 queued", f, v1)
		}
		if m := nextChat(t, conn, ""); m.Text != "hola" { // a second music frame would fail decode first
			t.Fatalf("chat = %+v, want hola", m)
		}
	}
}

// CHAT-21 rule 2a: an invalid control is dropped, not relayed as text, and the connection stays open.
func TestInvalidMusicDiscarded(t *testing.T) {
	srv, _ := startServer(t, NewMusicRoom("musica"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

	send(t, a, `{"type":"music"}`)
	send(t, a, `{"type":"music","action":"add","video":"abc","text":"hola"}`)
	send(t, a, `{"type":"music","action":"seek","pos":1.5}`)
	send(t, a, "hola")
	for _, conn := range []*websocket.Conn{a, b} {
		if m := nextChat(t, conn, ""); m.Text != "hola" {
			t.Fatalf("first chat = %+v, want hola", m)
		}
	}
}

// CHAT-30: the newcomer's frames are welcome, users, history, music, then its join.
func TestMusicNewcomerOverWebSocket(t *testing.T) {
	srv, _ := startServer(t, NewMusicRoom("musica"))
	a, _ := dial(t, srv, "A")
	send(t, a, `{"type":"music","action":"add","video":"dQw4w9WgXcQ"}`)
	readMusic(t, a)

	conn := dialRaw(t, srv, "C")
	if m := readMsg(t, conn); m.Type != typeWelcome {
		t.Fatalf("frame 1 = %+v, want welcome", m)
	}
	decodeUsers(t, readRaw(t, conn))
	decodeHistory(t, readRaw(t, conn))
	if f := decodeMusic(t, readRaw(t, conn)); f.Video != "dQw4w9WgXcQ" || !f.Playing || f.By != "A" {
		t.Fatalf("frame 4 = %+v, want the loaded state by A", f)
	}
	if m := readMsg(t, conn); m.Type != typeJoin || m.From != "C" {
		t.Fatalf("frame 5 = %+v, want join C", m)
	}
}

// CHAT-30: at most 4 controls per second per connection are accepted; the connection stays open.
func TestMusicThrottledOverWebSocket(t *testing.T) {
	srv, _ := startServer(t, NewMusicRoom("musica"))
	a, _ := dial(t, srv, "A")

	send(t, a, `{"type":"music","action":"add","video":"dQw4w9WgXcQ"}`)
	for i := 0; i < 20; i++ {
		send(t, a, `{"type":"music","action":"play"}`)
	}
	send(t, a, "hola")
	frames := 0
	for {
		raw := nextNonJoin(t, a)
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &head); err != nil {
			t.Fatalf("invalid JSON %q: %v", raw, err)
		}
		if head.Type == typeChat {
			break
		}
		if head.Type != typeMusic {
			t.Fatalf("unexpected frame %s", raw)
		}
		frames++
	}
	// The 21 frames arrive within a few ms, so normally 1 is accepted; allow up to 5 so a slow
	// machine that spreads the sends over 1 s (4 per second plus the first) cannot flake.
	if frames < 1 || frames > 5 {
		t.Fatalf("music frames before the chat = %d, want 1..5", frames)
	}
	send(t, a, "otra")
	if m := nextChat(t, a, ""); m.Text != "otra" {
		t.Fatalf("chat = %+v, want otra", m)
	}
}

// CHAT-30: ended is the page's report, not a user control: it passes the throttle that drops
// a play sent right after a pause.
func TestMusicEndedNotThrottledOverWebSocket(t *testing.T) {
	srv, _ := startServer(t, NewMusicRoom("musica"))
	a, _ := dial(t, srv, "A")

	send(t, a, `{"type":"music","action":"add","video":"dQw4w9WgXcQ"}`)
	if f := readMusic(t, a); f.Seq != 1 {
		t.Fatalf("first add = %+v, want seq 1", f)
	}
	time.Sleep(300 * time.Millisecond) // past the 260 ms gate
	send(t, a, `{"type":"music","action":"add","video":"aaaaaaaaaaa"}`)
	if f := readMusic(t, a); f.Seq != 1 || len(f.Queue) != 1 {
		t.Fatalf("second add = %+v, want it queued", f)
	}
	time.Sleep(300 * time.Millisecond)

	send(t, a, `{"type":"music","action":"pause"}`)
	send(t, a, `{"type":"music","action":"play"}`)
	send(t, a, `{"type":"music","action":"ended","seq":1}`)
	for {
		f := readMusic(t, a)
		if f.Seq == 2 {
			if f.Video != "aaaaaaaaaaa" {
				t.Fatalf("frame = %+v, want video aaaaaaaaaaa", f)
			}
			return
		}
	}
}
