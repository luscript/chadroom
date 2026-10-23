package chat

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// joinRoom dials room and returns the connection with its users and history frames.
func joinRoom(t *testing.T, srv *httptest.Server, room, nick string) (*websocket.Conn, usersFrame, historyFrame) {
	t.Helper()
	conn := dialRoomRaw(t, srv, room, nick)
	if m := readMsg(t, conn); m.Type != typeWelcome {
		t.Fatalf("first frame = %+v, want welcome", m)
	}
	users := decodeUsers(t, readRaw(t, conn))
	history := decodeHistory(t, readRaw(t, conn))
	return conn, users, history
}

func TestNewServerPanics(t *testing.T) {
	tests := []struct {
		name  string
		rooms []*Room
	}{
		{"no rooms", nil},
		{"empty id", []*Room{NewRoom("")}},
		{"duplicate id", []*Room{NewRoom("a"), NewRoom("a")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			NewServer(nil, tt.rooms...)
		})
	}
}

// SRV-06: unknown room ids are rejected before the upgrade.
func TestUnknownRoomIs404(t *testing.T) {
	srv, _ := startRooms(t, nil, NewRoom("general"), NewRoom("juegos"))
	base := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?nickname=X&room="
	for _, id := range []string{"nope", "General", "general ", "general/x"} {
		conn, resp, err := websocket.DefaultDialer.Dial(base+url.QueryEscape(id), nil)
		if err == nil {
			conn.Close()
			t.Fatalf("room %q: expected dial to fail", id)
		}
		if resp == nil || resp.StatusCode != http.StatusNotFound {
			t.Fatalf("room %q: expected 404, got resp=%v err=%v", id, resp, err)
		}
	}
	_, users, _ := joinRoom(t, srv, "general", "A")
	expectUsers(t, users, "A") // nothing was registered by the rejected dials
}

// SRV-06: a missing or empty room param joins the first room.
func TestDefaultRoom(t *testing.T) {
	srv, _ := startRooms(t, nil, NewRoom("general"), NewRoom("juegos"))
	dial(t, srv, "A") // bare /ws
	joinRoom(t, srv, "", "B")
	_, users, _ := joinRoom(t, srv, "general", "C")
	expectUsers(t, users, "A", "B", "C")
}

// CHAT-19: rooms share no state.
func TestRoomsIsolated(t *testing.T) {
	srv, _ := startRooms(t, nil, NewRoom("general"), NewRoom("juegos"))
	a, _ := dialRoom(t, srv, "general", "A")
	send(t, a, "hola")
	readUntil(t, a, typeChat, "A", "hola")

	b, _ := dialRoom(t, srv, "juegos", "B")
	send(t, b, "x")
	if m := nextChat(t, b, ""); m.From != "B" || m.Text != "x" {
		t.Fatalf("first chat in juegos = %+v, want B x", m)
	}

	_, users, history := joinRoom(t, srv, "juegos", "C")
	expectUsers(t, users, "B", "C")
	expectHistory(t, history, message{typeChat, "B", "x", 0})

	_, users, history = joinRoom(t, srv, "general", "D")
	expectUsers(t, users, "A", "D")
	expectHistory(t, history, message{typeChat, "A", "hola", 0})
}

func TestNicknameUniquePerRoom(t *testing.T) {
	srv, _ := startRooms(t, nil, NewRoom("general"), NewRoom("juegos"))
	if _, name := dialRoom(t, srv, "general", "Bob"); name != "Bob" {
		t.Fatalf("general nickname = %q, want Bob", name)
	}
	if _, name := dialRoom(t, srv, "juegos", "Bob"); name != "Bob" {
		t.Fatalf("juegos nickname = %q, want Bob", name)
	}
	if _, name := dialRoom(t, srv, "juegos", "Bob"); name != "Bob (2)" {
		t.Fatalf("second juegos nickname = %q, want Bob (2)", name)
	}
}

func TestLeaveOneRoomKeepsOther(t *testing.T) {
	srv, _ := startRooms(t, nil, NewRoom("general"), NewRoom("juegos"))
	aGeneral, _ := dialRoom(t, srv, "general", "A")
	aJuegos, _ := dialRoom(t, srv, "juegos", "A")
	oGeneral, _ := dialRoom(t, srv, "general", "O")
	oJuegos, _ := dialRoom(t, srv, "juegos", "O")

	aGeneral.Close()
	readUntil(t, oGeneral, typeLeave, "A", "")

	send(t, aJuegos, "still here")
	for { // O's own join may still be queued; a leave must never show up
		m := readMsg(t, oJuegos)
		if m.Type == typeLeave {
			t.Fatalf("leave leaked into juegos: %+v", m)
		}
		if m.Type == typeChat {
			if m.From != "A" || m.Text != "still here" {
				t.Fatalf("juegos chat = %+v, want A still here", m)
			}
			break
		}
	}
	readUntil(t, aJuegos, typeChat, "A", "still here")
}

// SRV-01 applies to every room.
func TestOriginCheckAppliesToEveryRoom(t *testing.T) {
	srv, _ := startRooms(t, nil, NewRoom("general"), NewRoom("juegos"))
	conn, resp, err := dialOriginRoom(srv, "A", "juegos", "https://evil.example")
	if err == nil {
		conn.Close()
		t.Fatal("expected dial to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got resp=%v err=%v", resp, err)
	}
}

func dialOriginRoom(srv *httptest.Server, nick, room, origin string) (*websocket.Conn, *http.Response, error) {
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?nickname=" + nick + "&room=" + url.QueryEscape(room)
	return websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": []string{origin}})
}

// SRV-03: Run returns only after every room has stopped.
func TestServerRunWaitsForAllRooms(t *testing.T) {
	r1, r2 := NewRoom("one"), NewRoom("two")
	s := NewServer(nil, r1, r2)
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})
	go func() {
		s.Run(ctx)
		close(returned)
	}()
	engine := gin.New()
	engine.GET("/ws", s.HandleWebSocket)
	srv := httptest.NewServer(engine)
	defer srv.Close()
	c1, _ := dialRoom(t, srv, "one", "A")
	c2, _ := dialRoom(t, srv, "two", "B")

	cancel()
	select {
	case <-returned:
	case <-time.After(testDeadline):
		t.Fatal("Run did not return after cancel")
	}
	for _, r := range []*Room{r1, r2} {
		select {
		case <-r.done:
		default:
			t.Fatalf("Run returned before room %q stopped", r.id)
		}
	}
	expectReadError(t, c1)
	expectReadError(t, c2)
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
			s := NewServer(tt.allowed, NewRoom("general"))
			r := httptest.NewRequest("GET", "http://example.com/ws", nil)
			if tt.origin != "" {
				r.Header.Set("Origin", tt.origin)
			}
			if got := s.checkOrigin(r); got != tt.want {
				t.Fatalf("checkOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
			}
		})
	}
}

// CHAT-19, CHAT-22: a dm only reaches the sender's room; a nickname from another room is offline.
func TestDMRoomScoped(t *testing.T) {
	srv, _ := startRooms(t, nil, NewRoom("general"), NewRoom("juegos"))
	a, _ := dialRoom(t, srv, "general", "A")
	bGeneral, _ := dialRoom(t, srv, "general", "B")
	bJuegos, _ := dialRoom(t, srv, "juegos", "B")
	z, _ := dialRoom(t, srv, "juegos", "Zoe")

	send(t, a, `{"type":"dm","to":"B","text":"hi"}`)
	if f := readDM(t, bGeneral); f.From != "A" || f.To != "B" || f.Text != "hi" {
		t.Fatalf("dm = %+v", f)
	}
	readDM(t, a)

	send(t, a, `{"type":"dm","to":"Zoe","text":"hi"}`)
	if e := readDMError(t, a); e.To != "Zoe" || e.Reason != reasonOffline {
		t.Fatalf("dm_error = %+v, want Zoe offline", e)
	}

	send(t, z, "sync") // Zoe's room never saw a dm; B in juegos gets only this chat
	if m := nextChat(t, bJuegos, ""); m.From != "Zoe" || m.Text != "sync" {
		t.Fatalf("juegos B got %+v", m)
	}
}
