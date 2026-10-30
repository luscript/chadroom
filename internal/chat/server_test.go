package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
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
			NewServer(nil, nil, tt.rooms...)
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
	expectHistory(t, history, message{Type: typeChat, From: "B", Text: "x"})

	_, users, history = joinRoom(t, srv, "general", "D")
	expectUsers(t, users, "A", "D")
	expectHistory(t, history, message{Type: typeChat, From: "A", Text: "hola"})
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
	s := NewServer(nil, nil, r1, r2)
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
			s := NewServer(tt.allowed, nil, NewRoom("general"))
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

// dialAvatar connects with a same-host Origin and the given ?avatar, returning without reading.
func dialAvatar(t *testing.T, srv *httptest.Server, nick, avatar string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?nickname=" + nick + "&avatar=" + url.QueryEscape(avatar)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": []string{srv.URL}})
	if err != nil {
		t.Fatalf("dial %s with avatar: %v", nick, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// CHAT-28: ?avatar is checked by the injected validator; an invalid one never rejects the connection.
func TestAvatarQueryParam(t *testing.T) {
	const good = "https://i.imgur.com/a.png?x=1&y=%41"
	var mu sync.Mutex
	var calls []string
	valid := func(s string) bool {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, s)
		return s == good
	}
	srv, _ := startAvatarRooms(t, valid, nil, NewRoom("general"))

	conn := dialAvatar(t, srv, "A", good)
	raw := readRaw(t, conn)
	exactFields(t, raw, "type", "from", "text", "ts", "avatar")
	var w message
	if err := json.Unmarshal(raw, &w); err != nil || w.Avatar != good {
		t.Fatalf("welcome avatar = %q (err %v), want %q", w.Avatar, err, good)
	}

	conn = dialAvatar(t, srv, "B", "bad")
	decode(t, readRaw(t, conn)) // welcome without avatar: exactly four fields

	mu.Lock()
	calls = nil
	mu.Unlock()
	readMsg(t, dialRaw(t, srv, "C")) // no param at all; welcome only (users lists A's avatar)
	conn = dialAvatar(t, srv, "D", "")
	decode(t, readRaw(t, conn))
	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 0 {
		t.Fatalf("validator called for a missing or empty avatar: %q", calls)
	}
}

// CHAT-28: a valid avatar is announced in the join frame other members receive.
func TestAvatarAnnouncedInJoin(t *testing.T) {
	const good = "https://i.imgur.com/a.png"
	srv, _ := startAvatarRooms(t, func(s string) bool { return s == good }, nil, NewRoom("general"))
	a := dialRaw(t, srv, "A") // no avatar
	dialAvatar(t, srv, "B", good)
	for {
		raw := readRaw(t, a)
		var m message
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("invalid JSON %q: %v", raw, err)
		}
		if m.Type == "join" && m.From == "B" {
			exactFields(t, raw, "type", "from", "text", "ts", "avatar")
			if m.Avatar != good {
				t.Fatalf("join avatar = %q, want %q", m.Avatar, good)
			}
			return
		}
	}
}

// CHAT-28: without a validator every ?avatar is dropped.
func TestAvatarDisabledWithoutValidator(t *testing.T) {
	srv, _ := startRooms(t, nil, NewRoom("general"))
	conn := dialAvatar(t, srv, "A", "https://i.imgur.com/a.png")
	decode(t, readRaw(t, conn))
}

// CHAT-28: the avatar is fixed at connection time; an avatar inside an inbound frame is ignored.
func TestForgedInboundAvatarIgnored(t *testing.T) {
	srv, _ := startRooms(t, nil, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	send(t, a, `{"type":"chat","text":"hi","avatar":"https://i.imgur.com/x.png"}`)
	m := nextChat(t, a, "")
	if m.Text != "hi" || m.Avatar != "" {
		t.Fatalf("chat = %+v, want text hi without avatar", m)
	}
}

// CHAT-35: a reply reaches everyone with its quote, and a later joiner gets it in history.
func TestReplyIntegration(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")

	send(t, a, "¿pizza?")
	first := nextChat(t, a, "")
	nextChat(t, b, "")
	send(t, b, `{"type":"chat","text":"dale","reply_to":`+strconv.FormatInt(first.ID, 10)+`}`)
	want := quote{ID: first.ID, From: "A", Text: "¿pizza?"}
	for _, c := range []*websocket.Conn{a, b} {
		if m := nextChat(t, c, ""); m.Text != "dale" || m.Reply == nil || *m.Reply != want {
			t.Fatalf("reply = %+v (%+v), want quote %+v", m, m.Reply, want)
		}
	}

	_, _, hist := joinRoom(t, srv, "general", "C")
	if len(hist.Messages) != 2 || hist.Messages[1].Reply == nil || *hist.Messages[1].Reply != want {
		t.Fatalf("history = %+v", hist.Messages)
	}
}

// CHAT-22, CHAT-35: a dm reply carries the same id and reply_to in both copies; a third client sees nothing.
func TestDMReplyIntegration(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")
	b, _ := dial(t, srv, "B")
	c, _ := dial(t, srv, "C")

	send(t, a, `{"type":"dm","to":"B","text":"hola"}`)
	orig := readDM(t, b)
	readDM(t, a)
	send(t, b, `{"type":"dm","to":"A","text":"que tal","reply_to":`+strconv.FormatInt(orig.ID, 10)+`}`)
	toA, toB := readDM(t, a), readDM(t, b)
	if toA.ID != toB.ID || toA.ID <= orig.ID || toA.ReplyTo != orig.ID || toB.ReplyTo != orig.ID {
		t.Fatalf("copies = %+v / %+v, orig id %d", toA, toB, orig.ID)
	}

	send(t, c, "sync")
	if m := nextChat(t, c, ""); m.Text != "sync" {
		t.Fatalf("c got %+v, want only its own chat", m)
	}
}

// CHAT-21, CHAT-35: an inbound frame near the 4096-byte limit plus reply_to is accepted,
// and the outbound reply, which grows past 4096 bytes, arrives intact.
func TestReplyFrameLargerThanLimit(t *testing.T) {
	srv, _ := startServer(t, NewRoom("general"))
	a, _ := dial(t, srv, "A")

	origin := strings.Repeat("ñ", 100)
	send(t, a, origin)
	nextChat(t, a, "")
	text := strings.Repeat("x", 3950)
	frame := `{"type":"chat","text":"` + text + `","reply_to":1}`
	if len(frame) >= 4096 {
		t.Fatalf("inbound frame is %d bytes, want < 4096", len(frame))
	}
	send(t, a, frame)
	raw := readRaw(t, a)
	if len(raw) <= 4096 {
		t.Fatalf("outbound frame is %d bytes, want > 4096", len(raw))
	}
	m := decode(t, raw)
	if m.Text != text || m.Reply == nil || m.Reply.Text != origin {
		t.Fatalf("reply = %+v", m)
	}
}

// dialProfile connects with a same-host Origin and the given ?avatar and ?bio (each
// omitted when empty), returning without reading.
func dialProfile(t *testing.T, srv *httptest.Server, nick, avatar, bio string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?nickname=" + url.QueryEscape(nick)
	if avatar != "" {
		wsURL += "&avatar=" + url.QueryEscape(avatar)
	}
	if bio != "" {
		wsURL += "&bio=" + url.QueryEscape(bio)
	}
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": []string{srv.URL}})
	if err != nil {
		t.Fatalf("dial %s with profile: %v", nick, err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

// joinProfile is dialProfile plus the welcome, users and history frames, which it
// returns raw so profile fields can be inspected (decode would reject a missing text).
func joinProfile(t *testing.T, srv *httptest.Server, nick, avatar, bio string) (conn *websocket.Conn, welcome, users []byte) {
	t.Helper()
	conn = dialProfile(t, srv, nick, avatar, bio)
	welcome = readRaw(t, conn)
	users = readRaw(t, conn)
	decodeHistory(t, readRaw(t, conn))
	return conn, welcome, users
}

// frameType returns the "type" of a raw frame.
func frameType(t *testing.T, raw []byte) string {
	t.Helper()
	var f struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("invalid JSON %q: %v", raw, err)
	}
	return f.Type
}

// readType skips frames until one of type typ arrives and returns it raw.
func readType(t *testing.T, conn *websocket.Conn, typ string) []byte {
	t.Helper()
	for {
		if raw := readRaw(t, conn); frameType(t, raw) == typ {
			return raw
		}
	}
}

// readChatNoProfile reads until a chat frame, failing if a profile frame comes first.
func readChatNoProfile(t *testing.T, conn *websocket.Conn) message {
	t.Helper()
	for {
		raw := readRaw(t, conn)
		switch frameType(t, raw) {
		case typeProfile:
			t.Fatalf("unexpected profile frame %s", raw)
		case typeChat:
			return decode(t, raw)
		}
	}
}

const (
	profileGood  = "https://i.imgur.com/a.png"
	profileGood2 = "https://i.imgur.com/b.png"
)

// profileServer starts one room whose validator allows only profileGood and profileGood2.
func profileServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv, _ := startAvatarRooms(t, func(s string) bool { return s == profileGood || s == profileGood2 }, nil, NewRoom("general"))
	return srv
}

// CHAT-37: ?bio is announced on welcome and join, cleaned and cut; a blank one means no bio.
func TestBioQueryParam(t *testing.T) {
	srv := profileServer(t)
	a, _, _ := joinProfile(t, srv, "A", "", "")

	_, welcome, _ := joinProfile(t, srv, "B", "", "  hi\n‮ there  ")
	f := exactFields(t, welcome, "type", "from", "text", "ts", "bio")
	if string(f["bio"]) != `"hi there"` {
		t.Fatalf("welcome bio = %s, want cleaned", f["bio"])
	}
	readType(t, a, typeJoin) // A's own join
	join := readType(t, a, typeJoin)
	if string(exactFields(t, join, "type", "from", "text", "ts", "bio")["bio"]) != `"hi there"` {
		t.Fatalf("join = %s", join)
	}

	long := strings.Repeat("ñ", 200)
	_, welcome, _ = joinProfile(t, srv, "C", "", long)
	var w message
	if err := json.Unmarshal(welcome, &w); err != nil || w.Bio != strings.Repeat("ñ", maxBioRunes) {
		t.Fatalf("welcome bio = %d runes (err %v), want %d", len([]rune(w.Bio)), err, maxBioRunes)
	}

	_, welcome, _ = joinProfile(t, srv, "D", "", "   ")
	decode(t, welcome) // blank bio: exactly four fields
}

// CHAT-37: a nickname reused after its owner left inherits no bio.
func TestReusedNicknameHasNoStaleBio(t *testing.T) {
	srv := profileServer(t)
	obs, _, _ := joinProfile(t, srv, "Obs", "", "")
	first, _, _ := joinProfile(t, srv, "Ana", "", "soy ana")
	readType(t, obs, typeJoin) // obs' own join
	readType(t, obs, typeJoin) // Ana's join
	first.Close()
	readUntil(t, obs, typeLeave, "Ana", "")

	_, welcome, users := joinProfile(t, srv, "Ana", "", "")
	decode(t, welcome)
	exactFields(t, users, "type", "users", "ts")
}

// CHAT-38: a valid update reaches every member, sender included, and later joiners see it in users.
func TestProfileUpdateOverWebSocket(t *testing.T) {
	srv := profileServer(t)
	a, _, _ := joinProfile(t, srv, "A", "", "")
	b, _, _ := joinProfile(t, srv, "B", "", "")

	send(t, a, `{"type":"profile","avatar":"`+profileGood+`","bio":"hola"}`)
	for _, c := range []*websocket.Conn{a, b} {
		raw := readType(t, c, typeProfile)
		f := exactFields(t, raw, "type", "from", "ts", "avatar", "bio")
		if string(f["from"]) != `"A"` || string(f["avatar"]) != `"`+profileGood+`"` || string(f["bio"]) != `"hola"` {
			t.Fatalf("profile = %s", raw)
		}
	}

	_, _, users := joinProfile(t, srv, "C", "", "")
	f := exactFields(t, users, "type", "users", "avatars", "bios", "ts")
	if got := stringMap(t, f["avatars"]); got["A"] != profileGood {
		t.Fatalf("avatars = %v", got)
	}
	if got := stringMap(t, f["bios"]); got["A"] != "hola" {
		t.Fatalf("bios = %v", got)
	}
}

// CHAT-38: an invalid avatar discards the whole update, with no reply and the profile unchanged.
func TestProfileInvalidAvatarDiscarded(t *testing.T) {
	srv := profileServer(t)
	a, _, _ := joinProfile(t, srv, "A", "", "hola")
	b, _, _ := joinProfile(t, srv, "B", "", "")

	send(t, a, `{"type":"profile","avatar":"https://evil.example/x.png","bio":"pwned"}`)
	send(t, a, "sentinel")
	if m := readChatNoProfile(t, b); m.Text != "sentinel" {
		t.Fatalf("B got %+v, want the sentinel chat", m)
	}
	if m := readChatNoProfile(t, a); m.Text != "sentinel" {
		t.Fatalf("A got %+v, want the sentinel chat", m)
	}

	_, _, users := joinProfile(t, srv, "C", "", "")
	f := exactFields(t, users, "type", "users", "bios", "ts")
	if got := stringMap(t, f["bios"]); got["A"] != "hola" {
		t.Fatalf("bios = %v, want A=hola unchanged", got)
	}
}

// CHAT-28, CHAT-38: without a validator a non-empty avatar is dropped, but a bio-only update works.
func TestProfileAvatarsDisabled(t *testing.T) {
	srv, _ := startRooms(t, nil, NewRoom("general"))
	a, _, _ := joinProfile(t, srv, "A", "", "")
	b, _, _ := joinProfile(t, srv, "B", "", "")

	send(t, a, `{"type":"profile","avatar":"`+profileGood+`","bio":"dropped"}`)
	send(t, a, `{"type":"profile","bio":"x"}`) // the dropped frame did not use the budget
	raw := readType(t, b, typeProfile)
	exactFields(t, raw, "type", "from", "ts", "bio")
	if !strings.HasSuffix(string(raw), `"bio":"x"}`) {
		t.Fatalf("profile = %s, want the bio-only update", raw)
	}
}

// CHAT-38: at most one update per second per connection; the excess is dropped silently.
func TestProfileRateLimited(t *testing.T) {
	srv := profileServer(t)
	a, _, _ := joinProfile(t, srv, "A", "", "")
	b, _, _ := joinProfile(t, srv, "B", "", "")

	send(t, a, `{"type":"profile","bio":"uno"}`)
	send(t, a, `{"type":"profile","bio":"dos"}`)
	send(t, a, "sentinel")
	raw := readType(t, b, typeProfile)
	if !strings.HasSuffix(string(raw), `"bio":"uno"}`) {
		t.Fatalf("profile = %s, want the first update", raw)
	}
	if m := readChatNoProfile(t, b); m.Text != "sentinel" {
		t.Fatalf("B got %+v, want the sentinel chat", m)
	}
}

// CHAT-21 rule 2b: an invalid profile frame is discarded and never becomes chat.
func TestProfileInvalidIsNotChat(t *testing.T) {
	srv := profileServer(t)
	a, _, _ := joinProfile(t, srv, "A", "", "")
	b, _, _ := joinProfile(t, srv, "B", "", "")

	send(t, a, `{"type":"profile","bio":5}`)
	send(t, a, "sentinel")
	if m := readChatNoProfile(t, b); m.Text != "sentinel" {
		t.Fatalf("B got %+v, want the sentinel as first chat", m)
	}
}

// CHAT-38: from is the connection's own nickname whatever the frame says.
func TestProfileForgedFromIgnored(t *testing.T) {
	srv := profileServer(t)
	a, _, _ := joinProfile(t, srv, "A", "", "")
	b, _, _ := joinProfile(t, srv, "B", "", "")

	send(t, a, `{"type":"profile","from":"B","text":"x","bio":"mine"}`)
	raw := readType(t, b, typeProfile)
	f := exactFields(t, raw, "type", "from", "ts", "bio")
	if string(f["from"]) != `"A"` {
		t.Fatalf("profile = %s, want from A", raw)
	}
}

// CHAT-38: updates racing with joins and leaves are safe under -race, and the
// observer sees the joins and leaves around its own updates.
func TestProfileConcurrentWithJoins(t *testing.T) {
	const minEvents = 3
	srv := profileServer(t)
	a, _, _ := joinProfile(t, srv, "A", "", "")

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws?nickname=J"+strconv.Itoa(i)+"&bio=x", http.Header{"Origin": []string{srv.URL}})
			if err != nil {
				t.Errorf("dial J%d: %v", i, err)
				return
			}
			conn.SetReadDeadline(time.Now().Add(testDeadline))
			_, _, err = conn.ReadMessage() // welcome: the client is registered
			conn.Close()
			if err != nil {
				t.Errorf("read welcome of J%d: %v", i, err)
				return
			}
			time.Sleep(50 * time.Millisecond) // keeps A's send buffer from filling with join/leave frames
		}
	})

	joins, leaves := 0, 0
	// observe reads frames from A until one satisfies stop, counting other members' joins and leaves.
	observe := func(stop func(raw []byte) bool) {
		t.Helper()
		for {
			raw := readRaw(t, a)
			if stop(raw) {
				return
			}
			var m message
			if err := json.Unmarshal(raw, &m); err != nil || m.From == "A" {
				continue
			}
			switch m.Type {
			case typeJoin:
				joins++
			case typeLeave:
				leaves++
			}
		}
	}
	for i := range 3 {
		bio := "bio" + strconv.Itoa(i)
		send(t, a, `{"type":"profile","avatar":"`+profileGood+`","bio":"`+bio+`"}`)
		observe(func(raw []byte) bool {
			if frameType(t, raw) != typeProfile {
				return false
			}
			if !strings.HasSuffix(string(raw), `"bio":"`+bio+`"}`) {
				t.Fatalf("profile = %s, want bio %s", raw, bio)
			}
			return true
		})
		time.Sleep(profileInterval + 100*time.Millisecond)
	}
	close(done)
	wg.Wait()
	observe(func([]byte) bool { return joins >= minEvents && leaves >= minEvents })
}

// CHAT-37: a bio inside a chat, dm or typing frame is ignored: nothing carries it.
func TestForgedInboundBioIgnored(t *testing.T) {
	srv := profileServer(t)
	a, _, _ := joinProfile(t, srv, "A", "", "")
	b, _, _ := joinProfile(t, srv, "B", "", "")

	send(t, a, `{"type":"typing","bio":"x"}`)
	send(t, a, `{"type":"chat","text":"hi","bio":"x"}`)
	send(t, a, `{"type":"dm","to":"B","text":"psst","bio":"x"}`)
	for _, c := range []*websocket.Conn{a, b} {
		for _, want := range []string{"hi", "psst"} {
			raw := readType(t, c, map[string]string{"hi": typeChat, "psst": typeDM}[want])
			var m message
			if err := json.Unmarshal(raw, &m); err != nil || m.Text != want || m.Bio != "" {
				t.Fatalf("frame = %s (err %v), want text %s without bio", raw, err, want)
			}
		}
	}

	_, welcome, users := joinProfile(t, srv, "C", "", "")
	decode(t, welcome)
	exactFields(t, users, "type", "users", "ts")
}

// CHAT-37: control characters and bidi marks are stripped, then the bio is cut to 160 runes.
func TestBioCleanedAndTruncatedOverQuery(t *testing.T) {
	srv := profileServer(t)
	_, welcome, _ := joinProfile(t, srv, "A", "", "  a\u202eb\nc"+strings.Repeat("x", 200))
	var w message
	if err := json.Unmarshal(welcome, &w); err != nil {
		t.Fatal(err)
	}
	if want := "abc" + strings.Repeat("x", 157); w.Bio != want {
		t.Fatalf("bio = %q, want %q", w.Bio, want)
	}
}

// CHAT-38: {"type":"profile"} with no fields clears avatar and bio for later joiners.
func TestProfileClearOverWebSocket(t *testing.T) {
	srv := profileServer(t)
	a, _, _ := joinProfile(t, srv, "A", profileGood, "hola")

	send(t, a, `{"type":"profile"}`)
	raw := readType(t, a, typeProfile)
	exactFields(t, raw, "type", "from", "ts")

	_, _, users := joinProfile(t, srv, "C", "", "")
	exactFields(t, users, "type", "users", "ts")
}
