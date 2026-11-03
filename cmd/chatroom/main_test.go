package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"chatroom/internal/auth"
	"chatroom/internal/auth/authtest"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// queryNickname takes the nickname from ?nickname=, as the tests without a session do.
func queryNickname(r *http.Request) string { return r.URL.Query().Get("nickname") }

const (
	testClientID     = "test-client"
	testClientSecret = "test-client-secret"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

// testAuthConfig points the auth config at a fake provider.
func testAuthConfig(t *testing.T) auth.Config {
	t.Helper()
	p := authtest.NewProvider(t, testClientID, testClientSecret)
	return auth.Config{
		Issuer:        p.URL,
		ClientID:      testClientID,
		ClientSecret:  testClientSecret,
		RedirectURL:   "http://localhost:8080/auth/callback",
		AllowedDomain: "crombie.dev",
		SessionSecret: testSecret,
		SessionTTL:    time.Hour,
	}
}

// testAuthHandler builds a real auth handler against the fake provider.
func testAuthHandler(t *testing.T) (*auth.Handler, auth.Config) {
	t.Helper()
	cfg := testAuthConfig(t)
	h, err := auth.New(context.Background(), cfg, serveAuthPage(http.StatusForbidden, deniedHTML))
	if err != nil {
		t.Fatal(err)
	}
	return h, cfg
}

// sessionCookie mints a session cookie valid for cfg's handler.
func sessionCookie(t *testing.T, cfg auth.Config, id auth.Identity) *http.Cookie {
	t.Helper()
	c, err := auth.NewSessions(cfg.SessionSecret, cfg.SessionTTL, cfg.SecureCookies()).Cookie(id)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	chatSrv := newChatServer(nil, queryNickname)
	ctx, cancel := context.WithCancel(context.Background())
	go chatSrv.Run(ctx)
	ts := httptest.NewServer(newRouter(chatSrv, nil, nil, nil))
	t.Cleanup(func() {
		cancel()
		ts.Close()
	})
	return ts
}

func TestServeIndex(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "chadroom") {
		t.Fatal("body missing \"chadroom\"")
	}
}

func TestWebSocketSameOriginNoAllowlist(t *testing.T) {
	ts := newTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	hdr := http.Header{"Origin": []string{ts.URL}}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("dial: %v (resp %v)", err, resp)
	}
	conn.Close()
}

func TestIndexSecurityHeaders(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q", got)
	}
	if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q", got)
	}
	const wantCSP = "default-src 'none'; script-src 'self'; " +
		"style-src 'self'; " +
		"img-src 'self' https://media.giphy.com https://media0.giphy.com https://media1.giphy.com " +
		"https://media2.giphy.com https://media3.giphy.com https://media4.giphy.com " +
		"https://i.giphy.com https://media.tenor.com https://i.imgur.com; " +
		"connect-src 'self'; frame-src https://www.youtube-nocookie.com; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	csp := resp.Header.Get("Content-Security-Policy")
	if csp != wantCSP {
		t.Errorf("Content-Security-Policy = %q, want %q", csp, wantCSP)
	}
	if !strings.Contains(csp, "script-src 'self';") {
		t.Errorf("script-src must stay 'self' only: %q", csp)
	}
	for _, bad := range []string{"unsafe-inline", "unsafe-eval"} {
		if strings.Contains(csp, bad) {
			t.Errorf("Content-Security-Policy contains %q", bad)
		}
	}
}

func TestStaticAssets(t *testing.T) {
	ts := newTestServer(t)
	tests := []struct {
		path string
		ct   string
		body []byte
	}{
		{"/", "text/html; charset=utf-8", indexHTML},
		{"/app.js", "text/javascript; charset=utf-8", appJS},
		{"/app.css", "text/css; charset=utf-8", appCSS},
		{"/favicon.svg", "image/svg+xml", faviconSVG},
		{"/favicon-unread.svg", "image/svg+xml", faviconUnreadSVG},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp, err := http.Get(ts.URL + tt.path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if got := resp.Header.Get("Content-Type"); got != tt.ct {
				t.Errorf("Content-Type = %q, want %q", got, tt.ct)
			}
			if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q", got)
			}
			if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
				t.Errorf("Cache-Control = %q", got)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != string(tt.body) {
				t.Error("body differs from the embedded file")
			}
		})
	}

	for _, path := range []string{"/index.html", "/static/"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestIndexHasNoInlineCode(t *testing.T) {
	html := string(indexHTML)
	if regexp.MustCompile(`(?i)<style`).MatchString(html) {
		t.Error("index.html contains a <style> element")
	}
	if regexp.MustCompile(`(?i)\sstyle\s*=`).MatchString(html) {
		t.Error("index.html contains a style attribute")
	}
	for _, tag := range regexp.MustCompile(`(?i)<script\b[^>]*>`).FindAllString(html, -1) {
		if !strings.Contains(tag, "src=") {
			t.Errorf("inline script tag: %s", tag)
		}
	}
	if m := regexp.MustCompile(`(?i)\son[a-z]+\s*=`).FindString(html); m != "" {
		t.Errorf("inline event handler attribute: %q", m)
	}
	for _, want := range []string{`src="/app.js"`, `href="/app.css"`} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %s", want)
		}
	}
}

func TestIndexHasReplyPreview(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{`id="reply-preview"`, `id="reply-preview-author"`, `id="reply-preview-text"`, `id="reply-cancel"`} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %s", want)
		}
	}
	if strings.Index(html, `id="reply-preview"`) > strings.Index(html, `class="composer-bar"`) {
		t.Error("reply preview must come before the composer bar")
	}
	js := string(appJS)
	for _, want := range []string{"reply_to", "'Mensaje no disponible'", "'🖼 GIF'"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js missing %s", want)
		}
	}
	if !strings.Contains(string(appCSS), ".msg:focus-within .msg-reply") {
		t.Error("app.css missing the focus-within reveal of the reply button")
	}
}

func TestIndexHasOnlineList(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		`id="users-btn"`, `id="users-count"`, `id="users-panel"`, `id="users-list"`,
		`aria-controls="users-panel"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %s", want)
		}
	}
}

func TestIndexHasRoomTabs(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		`id="room-tabs"`, `role="tablist"`, `id="room-leave-btn"`, `id="room-name"`,
		`id="start-view"`, `id="start-error"`, `id="start-retry"`, `role="tabpanel"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %s", want)
		}
	}
	if strings.Contains(html, `id="messages"`) {
		t.Error(`index.html still has the static id="messages" list`)
	}
}

func TestIndexHasTypingLine(t *testing.T) {
	html := string(indexHTML)
	scroller := strings.Index(html, `id="scroller"`)
	typing := strings.Index(html, `id="typing"`)
	form := strings.Index(html, `id="send-form"`)
	if scroller < 0 || typing < 0 || form < 0 || scroller >= typing || typing >= form {
		t.Fatalf(`id="typing" must sit between #scroller and #send-form (scroller=%d typing=%d form=%d)`, scroller, typing, form)
	}
	// The line updates every few seconds while anyone types: it must not be a live region.
	start := strings.LastIndex(html[:typing], "<")
	tag := html[start : typing+strings.Index(html[typing:], ">")]
	for _, banned := range []string{"aria-live", "role="} {
		if strings.Contains(tag, banned) {
			t.Errorf("#typing start tag must not have %s: %s", banned, tag)
		}
	}
}

func TestRoomsEndpoint(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/rooms")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for k, want := range map[string]string{
		"Content-Type":           "application/json; charset=utf-8",
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "no-cache",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"rooms":["general","juegos","musica"]}`; string(body) != want {
		t.Fatalf("body = %s, want %s", body, want)
	}
}

func TestWebSocketRoomParam(t *testing.T) {
	ts := newTestServer(t)
	hdr := http.Header{"Origin": []string{ts.URL}}
	base := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?room="

	conn, resp, err := websocket.DefaultDialer.Dial(base+"juegos", hdr)
	if err != nil {
		t.Fatalf("dial juegos: %v (resp %v)", err, resp)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"type":"welcome"`) {
		t.Fatalf("first frame = %s, want welcome", data)
	}

	conn2, resp, err := websocket.DefaultDialer.Dial(base+"nope", hdr)
	if err == nil {
		conn2.Close()
		t.Fatal("expected dial to unknown room to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got resp=%v err=%v", resp, err)
	}
}

func TestRunShutdownClosesAllRooms(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	codeCh := make(chan int, 1)
	acfg := testAuthConfig(t)
	go func() { codeCh <- run(ctx, ln, config{auth: acfg}) }()

	addr := ln.Addr().String()
	hdr := http.Header{"Origin": []string{"http://" + addr}}
	hdr.Add("Cookie", sessionCookie(t, acfg, auth.Identity{Email: "ana@crombie.dev", Name: "Ana"}).String())
	var conns []*websocket.Conn
	for _, room := range []string{"general", "musica"} {
		conn, resp, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws?room="+room, hdr)
		if err != nil {
			t.Fatalf("dial %s: %v (resp %v)", room, err, resp)
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, _, err := conn.ReadMessage(); err != nil { // welcome: registered
			t.Fatalf("read welcome in %s: %v", room, err)
		}
		conns = append(conns, conn)
	}

	cancel()
	for _, conn := range conns {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					t.Fatal("expected client read error after shutdown, got timeout")
				}
				break
			}
		}
	}
	select {
	case code := <-codeCh:
		if code != 0 {
			t.Fatalf("run returned %d, want 0", code)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("run did not return in time")
	}
}

func TestRunGracefulShutdown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	codeCh := make(chan int, 1)
	acfg := testAuthConfig(t)
	go func() { codeCh <- run(ctx, ln, config{auth: acfg}) }()

	addr := ln.Addr().String()
	hdr := http.Header{"Origin": []string{"http://" + addr}}
	hdr.Add("Cookie", sessionCookie(t, acfg, auth.Identity{Email: "ana@crombie.dev", Name: "Ana"}).String())
	conn, resp, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws", hdr)
	if err != nil {
		t.Fatalf("dial: %v (resp %v)", err, resp)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read welcome: %v", err)
	}
	var first struct {
		Type string `json:"type"`
		From string `json:"from"`
		Text string `json:"text"`
		TS   int64  `json:"ts"`
	}
	if err := json.Unmarshal(data, &first); err != nil {
		t.Fatalf("decode first frame %q: %v", data, err)
	}
	if first.Type != "welcome" {
		t.Fatalf("first frame type = %q, want welcome", first.Type)
	}

	cancel()
	// Drain frames already queued (e.g. join) until the connection errors.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("expected client read error after shutdown, got timeout")
			}
			break
		}
	}
	select {
	case code := <-codeCh:
		if code != 0 {
			t.Fatalf("run returned %d, want 0", code)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("run did not return in time")
	}
}

func TestServeListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	codeCh := make(chan int, 1)
	go func() { codeCh <- serve(context.Background(), ln.Addr().String(), config{}) }()
	select {
	case code := <-codeCh:
		if code != 1 {
			t.Fatalf("serve returned %d, want 1", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return in time")
	}
}

func TestParseAllowedOrigins(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", []string{}},
		{"trims blanks", " http://a.com , ,https://b.com ", []string{"http://a.com", "https://b.com"}},
		{"drops null", "null,http://a.com", []string{"http://a.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseAllowedOrigins(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("parseAllowedOrigins(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// The JS allowlist must list exactly the hosts the CSP allows in img-src.
func TestImageHostsMirroredInJS(t *testing.T) {
	block := regexp.MustCompile(`(?s)const IMAGE_HOSTS = new Set\(\[(.*?)\]\)`).FindSubmatch(appJS)
	if block == nil {
		t.Fatal("IMAGE_HOSTS not found in app.js")
	}
	var got []string
	for _, m := range regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(block[1], -1) {
		got = append(got, string(m[1]))
	}
	want := slices.Clone(imageHosts)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("IMAGE_HOSTS = %v, want %v (imageHosts)", got, want)
	}
}

func TestIndexHasGifPicker(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		`id="gif-search-box"`, `id="gif-search"`, `id="gif-grid"`, "Powered by GIPHY",
		`id="gif-url"`, `id="gif-send"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %s", want)
		}
	}
	box := strings.Index(html, `id="gif-search-box"`)
	paste := strings.Index(html, `id="gif-url"`)
	if box < 0 || paste < 0 || box >= paste {
		t.Errorf(`id="gif-search-box" must come before id="gif-url" (box=%d url=%d)`, box, paste)
	}
}

// CHAT-28: validAvatar gives the same verdict as the page's isAllowedImage.
func TestValidAvatar(t *testing.T) {
	var accept []string
	for _, h := range imageHosts {
		for _, ext := range []string{".png", ".JPG", ".jpeg", ".webp", ".gif"} {
			accept = append(accept, "https://"+h+"/a/b"+ext)
		}
		accept = append(accept, "https://"+h+"/a.png?x=1&y=2")
	}
	pad := func(n int) string { // an imgur URL of exactly n bytes
		const base = "https://i.imgur.com/"
		return base + strings.Repeat("a", n-len(base)-len(".png")) + ".png"
	}
	accept = append(accept, pad(maxAvatarBytes))
	for _, u := range accept {
		if !validAvatar(u) {
			t.Errorf("validAvatar(%q) = false, want true", u)
		}
	}

	reject := []string{
		"", "http://i.imgur.com/a.png", "/a.png", "https://i.imgur.com:443/a.png", "https://i.imgur.com:/a.png",
		"https://I.IMGUR.COM/a.png", "https://user@i.imgur.com/a.png", `https://evil.com\@i.imgur.com/a.png`,
		"https://evil.com/a.png", "https://i.imgur.com/a.mp4", "https://i.imgur.com/a.png#x",
		"https://i.imgur.com/a.png#", "https://i.imgur.com/a b.png", "https://i.imgur.com/a\t.png",
		"https://i.imgur.com/a%2Epng", "javascript:alert(1)", "https:i.imgur.com/a.png",
		"https://i.imgur.com/\xff.png", "https://i.imgur.com.evil.example/a.png",
		pad(maxAvatarBytes + 1),
	}
	for _, u := range reject {
		if validAvatar(u) {
			t.Errorf("validAvatar(%q) = true, want false", u)
		}
	}
}

// avatarWelcome dials /ws with ?avatar and returns the raw welcome frame's fields.
func avatarWelcome(t *testing.T, ts *httptest.Server, avatar string) map[string]json.RawMessage {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?nickname=A&avatar=" + url.QueryEscape(avatar)
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": []string{ts.URL}})
	if err != nil {
		t.Fatalf("dial: %v (resp %v)", err, resp)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatalf("welcome %q: %v", data, err)
	}
	return fields
}

// CHAT-28: newChatServer injects validAvatar into the chat server.
func TestWebSocketAvatarWiring(t *testing.T) {
	ts := newTestServer(t)
	const good = "https://i.imgur.com/a.png"
	var got string
	if err := json.Unmarshal(avatarWelcome(t, ts, good)["avatar"], &got); err != nil || got != good {
		t.Fatalf("welcome avatar = %q (err %v), want %q", got, err, good)
	}
	if _, ok := avatarWelcome(t, ts, "https://evil.com/a.png")["avatar"]; ok {
		t.Fatal("welcome carries an avatar for a host outside imageHosts")
	}
}

// The join form no longer asks for a picture (CHAT-29): it is edited in the profile editor, and the
// old localStorage entry is removed on start. The profile card keeps its image.
func TestIndexHasNoAvatarField(t *testing.T) {
	html := string(indexHTML)
	if strings.Contains(html, `id="avatar-url"`) {
		t.Error(`index.html still has id="avatar-url"`)
	}
	if !strings.Contains(html, `id="profile-avatar"`) {
		t.Error(`index.html missing id="profile-avatar"`)
	}
	js := string(appJS)
	if !strings.Contains(js, "removeItem(LEGACY_AVATAR_KEY)") {
		t.Error("app.js must remove the legacy avatar key on start")
	}
	if strings.Contains(js, "setItem(LEGACY_AVATAR_KEY") {
		t.Error("app.js must not store the avatar in localStorage")
	}
}

// startTagOf returns the start tag of the element with the given id.
func startTagOf(t *testing.T, html, id string) string {
	t.Helper()
	at := strings.Index(html, `id="`+id+`"`)
	if at < 0 {
		t.Fatalf(`index.html missing id="%s"`, id)
	}
	start := strings.LastIndex(html[:at], "<")
	return html[start : at+strings.Index(html[at:], ">")]
}

// CHAT-39: the profile editor lives in the composer bar; the bio limit mirrors maxBioRunes.
func TestIndexHasProfileEditor(t *testing.T) {
	html := string(indexHTML)
	for _, id := range []string{"profile-btn", "profile-panel", "profile-preview", "profile-pick", "profile-clear",
		"profile-bio-input", "profile-bio-count", "profile-save", "gif-paste", "profile-bio", "profile-card-edit"} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf(`index.html missing id="%s"`, id)
		}
	}
	if tag := startTagOf(t, html, "profile-bio-input"); !strings.Contains(tag, `maxlength="160"`) {
		t.Errorf("#profile-bio-input start tag missing maxlength=160: %s", tag)
	}
	bar := strings.Index(html, `class="composer-bar"`)
	panel := strings.Index(html, `id="profile-panel"`)
	if bar < 0 || panel < bar {
		t.Errorf("#profile-panel must sit inside .composer-bar (bar=%d panel=%d)", bar, panel)
	}
	js := string(appJS)
	for _, want := range []string{"type: 'profile'", "const MAX_BIO = 160", "&bio="} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js missing %q", want)
		}
	}
}

// CHAT-40: one page-level user popup, opened from buttons with class user-trigger.
func TestIndexHasUserPopup(t *testing.T) {
	html := string(indexHTML)
	for _, id := range []string{"user-popup", "user-popup-avatar", "user-popup-nick", "user-popup-bio", "user-popup-dm", "user-popup-edit"} {
		if !strings.Contains(html, `id="`+id+`"`) {
			t.Errorf(`index.html missing id="%s"`, id)
		}
	}
	if tag := startTagOf(t, html, "user-popup"); !strings.Contains(tag, `role="dialog"`) {
		t.Errorf("#user-popup start tag missing role=dialog: %s", tag)
	}
	js := string(appJS)
	for _, want := range []string{"'Sin descripción'", "user-trigger"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js missing %q", want)
		}
	}
}

// SRV-06: the player room is named once in Go and once in the page, and they must agree.
func TestMusicRoomMirroredInJS(t *testing.T) {
	if !slices.Contains(roomIDs, musicRoom) {
		t.Fatalf("roomIDs %v does not contain musicRoom %q", roomIDs, musicRoom)
	}
	m := regexp.MustCompile(`const MUSIC_ROOM = '([^']*)'`).FindSubmatch(appJS)
	if m == nil {
		t.Fatal("MUSIC_ROOM not found in app.js")
	}
	if string(m[1]) != musicRoom {
		t.Errorf("MUSIC_ROOM = %q, want %q (musicRoom)", m[1], musicRoom)
	}
}

// dialRoomConn connects to room with a same-host Origin and consumes welcome, users and history.
func dialRoomConn(t *testing.T, ts *httptest.Server, room, nick string) *websocket.Conn {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?nickname=" + nick + "&room=" + url.QueryEscape(room)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, http.Header{"Origin": []string{ts.URL}})
	if err != nil {
		t.Fatalf("dial %s in %s: %v", nick, room, err)
	}
	t.Cleanup(func() { conn.Close() })
	for _, want := range []string{"welcome", "users", "history"} {
		if got := readFrameType(t, conn); got != want {
			t.Fatalf("frame = %q, want %q", got, want)
		}
	}
	return conn
}

// readFrameType reads one frame and returns its type, skipping join notices.
func readFrameType(t *testing.T, conn *websocket.Conn) string {
	t.Helper()
	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var head struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &head); err != nil {
			t.Fatalf("invalid JSON %q: %v", data, err)
		}
		if head.Type != "join" {
			return head.Type
		}
	}
}

// SRV-06: only the musica room has a shared player; a control in any other room is dropped, never chat.
func TestMusicOnlyInMusica(t *testing.T) {
	ts := newTestServer(t)
	const load = `{"type":"music","action":"add","video":"dQw4w9WgXcQ"}`

	m := dialRoomConn(t, ts, "musica", "A")
	if err := m.WriteMessage(websocket.TextMessage, []byte(load)); err != nil {
		t.Fatal(err)
	}
	if got := readFrameType(t, m); got != "music" {
		t.Fatalf("musica answered %q, want a music frame", got)
	}

	g := dialRoomConn(t, ts, "general", "A")
	for _, frame := range []string{load, "hola"} {
		if err := g.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	if got := readFrameType(t, g); got != "chat" { // the chat is the first and only reply
		t.Fatalf("general answered %q first, want only the chat", got)
	}
}

// CHAT-31: the player strip sits between the room tabs and the rule, hidden, with every control.
func TestIndexHasMusicPlayer(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		`id="music-url"`, `id="music-load"`, `id="music-toggle"`, `id="music-seek"`,
		`id="music-time"`, `id="music-volume"`, `id="music-mute"`, `id="music-now"`,
		`id="music-error"`, `id="music-listen"`, `id="music-player"`,
		`id="music-skip"`, `id="music-queue"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %s", want)
		}
	}
	startTag := func(id string) string {
		i := strings.Index(html, `id="`+id+`"`)
		if i < 0 {
			t.Fatalf("index.html missing id=%q", id)
		}
		return html[strings.LastIndex(html[:i], "<") : i+strings.Index(html[i:], ">")]
	}
	if !regexp.MustCompile(`\shidden[\s>]`).MatchString(startTag("music") + ">") {
		t.Errorf("#music must start hidden: %s", startTag("music"))
	}
	if !regexp.MustCompile(`(?s)id="music-load"[^>]*>\s*Poner\s*<`).MatchString(html) {
		t.Error(`#music-load must be the "Poner" button`)
	}
	if !regexp.MustCompile(`(?s)id="music-listen"[^>]*>\s*Escuchar\s*<`).MatchString(html) {
		t.Error(`#music-listen must be the "Escuchar" button`)
	}
	if !regexp.MustCompile(`\shidden[\s>]`).MatchString(startTag("music-consent") + ">") {
		t.Errorf("#music-consent must start hidden: %s", startTag("music-consent"))
	}
	if !strings.Contains(html, "Tu navegador bloqueó el audio: apretá Escuchar.") {
		t.Error("index.html missing the blocked-audio warning text")
	}
	tabs, music, rule := strings.Index(html, `id="room-tabs"`), strings.Index(html, `id="music"`), strings.Index(html, `class="rule"`)
	if tabs < 0 || music < 0 || rule < 0 || tabs >= music || music >= rule {
		t.Fatalf(`id="music" must sit between id="room-tabs" and class="rule" (tabs=%d music=%d rule=%d)`, tabs, music, rule)
	}
	// The page must not contact YouTube on its own (CHAT-31): the frame is built by app.js only, once the musica pane is shown.
	if strings.Contains(strings.ToLower(html), "<iframe") {
		t.Error("index.html contains an <iframe>")
	}
	if m := regexp.MustCompile(`(?i)(https?:)?//[^"'\s]*youtu`).FindString(html); m != "" {
		t.Errorf("index.html references a YouTube URL: %q", m)
	}
}

// CHAT-31: no YouTube script is ever loaded; the iframe is driven with postMessage.
func TestNoYouTubeScript(t *testing.T) {
	js := string(appJS)
	for _, banned := range []string{"iframe_api", "createElement('script')", `createElement("script")`, "youtube.com/s/", "youtube.com/iframe"} {
		if strings.Contains(js, banned) {
			t.Errorf("app.js contains %q", banned)
		}
	}
	if m := regexp.MustCompile(`(?i)https://(www\.|m\.|music\.)?youtube\.com/[^'"\x60\s]*\.js`).FindString(js); m != "" {
		t.Errorf("app.js references a YouTube script URL: %q", m)
	}
	if !strings.Contains(js, "youtube-nocookie.com/embed/") {
		t.Error("app.js does not build the youtube-nocookie.com/embed/ URL")
	}
}

// readMusicFrame reads frames until a music frame arrives and decodes it.
func readMusicFrame(t *testing.T, conn *websocket.Conn) (frame struct {
	Type    string `json:"type"`
	Video   string `json:"video"`
	Playing bool   `json:"playing"`
	Pos     *int64 `json:"pos"`
	By      string `json:"by"`
	Seq     *int64 `json:"seq"`
}) {
	t.Helper()
	for {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, data, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("waiting for a music frame: %v", err)
		}
		if err := json.Unmarshal(data, &frame); err != nil {
			t.Fatalf("invalid JSON %q: %v", data, err)
		}
		if frame.Type == "music" {
			return frame
		}
	}
}

// CHAT-30: an add and a pause from one session reach the other, through the production wiring.
func TestMusicSyncAcrossSessions(t *testing.T) {
	ts := newTestServer(t)
	a := dialRoomConn(t, ts, "musica", "A")
	b := dialRoomConn(t, ts, "musica", "B")

	send := func(frame string) {
		t.Helper()
		if err := a.WriteMessage(websocket.TextMessage, []byte(frame)); err != nil {
			t.Fatal(err)
		}
	}
	send(`{"type":"music","action":"add","video":"dQw4w9WgXcQ"}`)
	time.Sleep(300 * time.Millisecond) // past the per-connection music gate
	send(`{"type":"music","action":"pause"}`)

	for _, c := range []struct {
		name    string
		playing bool
	}{{"add", true}, {"pause", false}} {
		f := readMusicFrame(t, b)
		if f.Video != "dQw4w9WgXcQ" || f.Playing != c.playing || f.Pos == nil || f.Seq == nil || *f.Seq != 1 || f.By != "A" {
			t.Fatalf("B after %s: %+v, want video dQw4w9WgXcQ playing=%v pos and seq 1 set by A", c.name, f, c.playing)
		}
	}
}

func TestIndexGlobalMusicMarkup(t *testing.T) {
	html := string(indexHTML)
	startTag := func(id string) string {
		i := strings.Index(html, `id="`+id+`"`)
		if i < 0 {
			t.Fatalf("index.html missing id=%q", id)
		}
		return html[strings.LastIndex(html[:i], "<") : i+strings.Index(html[i:], ">")+1]
	}
	hidden := regexp.MustCompile(`\shidden[\s>]`)
	mute := startTag("music-global-mute")
	if !strings.HasPrefix(mute, "<button") || !strings.Contains(mute, `aria-pressed="false"`) ||
		!hidden.MatchString(mute) || !strings.Contains(mute, "tool-mute") {
		t.Errorf("#music-global-mute must be a hidden button with aria-pressed=\"false\" and class tool-mute: %s", mute)
	}
	if now := startTag("music-now-playing"); !hidden.MatchString(now) {
		t.Errorf("#music-now-playing must start hidden: %s", now)
	}
	prev := -1
	for _, marker := range []string{`class="actions"`, `id="music-now-playing"`, `id="music-global-mute"`, `id="mute-btn"`, `id="leave-btn"`} {
		i := strings.Index(html, marker)
		if i <= prev {
			t.Errorf("%s is missing or out of order in the header", marker)
		}
		prev = i
	}
	if !strings.Contains(string(appJS), "'chadroom.musicMuted'") {
		t.Error("app.js does not contain 'chadroom.musicMuted'")
	}
	for _, want := range []string{".music.parked", ":not([hidden], .parked)"} {
		if !strings.Contains(string(appCSS), want) {
			t.Errorf("app.css does not contain %q", want)
		}
	}
	if strings.Contains(string(appJS), "pauseLocal") {
		t.Error("app.js still contains pauseLocal")
	}
}

func TestIndexNotificationMarkup(t *testing.T) {
	if !strings.Contains(string(indexHTML), `<link rel="icon" type="image/svg+xml" href="/favicon.svg">`) {
		t.Error("index.html does not declare /favicon.svg as the icon")
	}
	if !regexp.MustCompile(`<button id="mute-btn"[^>]*aria-pressed="false"`).Match(indexHTML) {
		t.Error("index.html has no #mute-btn with aria-pressed=\"false\"")
	}
	js := string(appJS)
	for _, want := range []string{"'/favicon.svg'", "'/favicon-unread.svg'", "'chadroom.muted'", "notify(room === current())", "notify(conv === current())", "resetTabIndicator()"} {
		if !strings.Contains(js, want) {
			t.Errorf("app.js does not contain %s", want)
		}
	}
	for name, svg := range map[string][]byte{"favicon.svg": faviconSVG, "favicon-unread.svg": faviconUnreadSVG} {
		t.Run(name, func(t *testing.T) {
			low := strings.ToLower(string(svg))
			for _, bad := range []string{"<script", "href", "<foreignobject"} {
				if strings.Contains(low, bad) {
					t.Errorf("%s contains %q", name, bad)
				}
			}
			if regexp.MustCompile(`\son\w+\s*=`).MatchString(low) {
				t.Errorf("%s contains an event handler attribute", name)
			}
		})
	}
}

// SRV-09: the list id rule is written once in Go and once in the page, and they must agree.
func TestPlaylistIDMirroredInJS(t *testing.T) {
	want := "const YT_LIST_ID = /" + playlistID.String() + "/;"
	if !strings.Contains(string(appJS), want) {
		t.Errorf("app.js does not contain %q", want)
	}
}

// The request log never carries the query string: /ws puts the nickname, avatar and bio there.
func TestRequestLogOmitsQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	r := newRouterLogging(newChatServer(nil, queryNickname), nil, nil, nil, &buf)
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ws?nickname=x&bio=secreto", nil))
	out := buf.String()
	if !strings.Contains(out, `"/ws"`) {
		t.Fatalf("log = %q, want the /ws path", out)
	}
	if strings.Contains(out, "secreto") || strings.Contains(out, "nickname") {
		t.Fatalf("log leaks the query string: %q", out)
	}
}

// authRouter is the full router with a real auth handler and gif / playlist proxies whose upstream
// is unreachable (a 401 must come first, so it is never called).
func authRouter(t *testing.T) (*httptest.Server, auth.Config) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h, cfg := testAuthHandler(t)
	chatSrv := newChatServer(nil, identityNickname)
	ctx, cancel := context.WithCancel(context.Background())
	go chatSrv.Run(ctx)
	const dead = "http://127.0.0.1:1"
	ts := httptest.NewServer(newRouter(chatSrv, newGifSearch(dead, "k"), newPlaylistImport(dead, "k"), h))
	t.Cleanup(func() {
		cancel()
		ts.Close()
	})
	return ts, cfg
}

// noRedirectClient returns the first response instead of following redirects.
var noRedirectClient = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func TestAuthRoutes(t *testing.T) {
	ts, _ := authRouter(t)

	resp, err := noRedirectClient.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/auth/login?next=%2F" {
		t.Fatalf("GET / = %d Location %q, want 302 /auth/login?next=%%2F", resp.StatusCode, resp.Header.Get("Location"))
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("GET / Cache-Control = %q, want no-store", cc)
	}

	for _, path := range []string{"/app.js", "/rooms", "/gifs?q=cat", "/playlist?list=PL1234567890abcdef", "/ws", "/ws?room=secret"} {
		resp, err := noRedirectClient.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("GET %s = %d, want 401", path, resp.StatusCode)
		}
		if !strings.Contains(string(body), `"unauthorized"`) || !strings.Contains(resp.Header.Get("Content-Type"), "json") {
			t.Errorf("GET %s body %q type %q, want the JSON unauthorized error", path, body, resp.Header.Get("Content-Type"))
		}
		if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
			t.Errorf("GET %s Cache-Control = %q, want no-store", path, cc)
		}
	}

	// A WebSocket dial fails with the 401, not an upgrade or a room 404.
	_, resp, err = websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws?room=secret",
		http.Header{"Origin": []string{ts.URL}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ws dial err=%v resp=%v, want 401", err, resp)
	}
}

func TestAuthPublicRoutes(t *testing.T) {
	ts, _ := authRouter(t)
	for _, path := range []string{"/app.css", "/favicon.svg", "/favicon-unread.svg", "/auth/signed-out"} {
		resp, err := noRedirectClient.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 without a session", path, resp.StatusCode)
		}
	}
	resp, err := noRedirectClient.Get(ts.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "/authorize") {
		t.Fatalf("GET /auth/login = %d Location %q, want 302 to the provider", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestAuthRoutesWithSession(t *testing.T) {
	ts, cfg := authRouter(t)
	cookie := sessionCookie(t, cfg, auth.Identity{Email: "ana@crombie.dev", Name: "Ana"})
	for _, path := range []string{"/", "/app.js", "/rooms"} {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		req.AddCookie(cookie)
		resp, err := noRedirectClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s with a session = %d, want 200", path, resp.StatusCode)
		}
	}
	// A cookie signed with another secret is refused.
	other := auth.Config{SessionSecret: []byte("another-secret-another-secret-12345"), SessionTTL: time.Hour}
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/rooms", nil)
	req.AddCookie(sessionCookie(t, other, auth.Identity{Email: "ana@crombie.dev"}))
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("forged cookie = %d, want 401", resp.StatusCode)
	}
}

func TestAuthPageSecurityHeaders(t *testing.T) {
	const wantCSP = "default-src 'none'; style-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	for name, tc := range map[string]struct {
		status int
		body   []byte
	}{
		"denied":     {http.StatusForbidden, deniedHTML},
		"signed-out": {http.StatusOK, signedOutHTML},
	} {
		t.Run(name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
			serveAuthPage(tc.status, tc.body)(c)
			if rec.Code != tc.status {
				t.Errorf("status = %d, want %d", rec.Code, tc.status)
			}
			for k, want := range map[string]string{
				"Content-Security-Policy": wantCSP,
				"X-Content-Type-Options":  "nosniff",
				"X-Frame-Options":         "DENY",
				"Referrer-Policy":         "no-referrer",
				"Cache-Control":           "no-store",
			} {
				if got := rec.Header().Get(k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
			html := strings.ToLower(string(tc.body))
			if strings.Contains(html, "<script") || strings.Contains(html, "style=") || strings.Contains(html, "<style") ||
				regexp.MustCompile(`\son[a-z]+=`).MatchString(html) || strings.Contains(html, "javascript:") {
				t.Error("auth page has inline code or style")
			}
			if !strings.Contains(html, "/auth/login") {
				t.Error("auth page does not link to /auth/login")
			}
		})
	}
}

// wsWelcomeFrom dials /ws with the cookie and returns the welcome frame's from.
func wsWelcomeFrom(t *testing.T, ts *httptest.Server, query string, cookie *http.Cookie) string {
	t.Helper()
	hdr := http.Header{"Origin": []string{ts.URL}}
	hdr.Add("Cookie", cookie.String())
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws"+query, hdr)
	if err != nil {
		t.Fatalf("dial: %v (resp %v)", err, resp)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var f struct{ Type, From string }
	if err := json.Unmarshal(data, &f); err != nil || f.Type != "welcome" {
		t.Fatalf("first frame %q (err %v), want welcome", data, err)
	}
	return f.From
}

func TestWebSocketNicknameFromSession(t *testing.T) {
	ts, cfg := authRouter(t)
	ana := sessionCookie(t, cfg, auth.Identity{Email: "ana@crombie.dev", Name: "Ana Pérez"})
	if got := wsWelcomeFrom(t, ts, "?nickname=Mallory", ana); got != "Ana Pérez" {
		t.Errorf("welcome from = %q, want the session name %q", got, "Ana Pérez")
	}
	noName := sessionCookie(t, cfg, auth.Identity{Email: "bob.smith@crombie.dev"})
	if got := wsWelcomeFrom(t, ts, "?nickname=Mallory&room=juegos", noName); got != "bob.smith" {
		t.Errorf("welcome from = %q, want the email local part", got)
	}
}

func TestIdentityNickname(t *testing.T) {
	cookieReq := func(id *auth.Identity) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		if id == nil {
			return r
		}
		// Only Require stores an identity, so go through it.
		var out *http.Request
		sessions := auth.NewSessions(testSecret, time.Hour, false)
		c, _ := sessions.Cookie(*id)
		r.AddCookie(c)
		g := gin.New()
		g.GET("/", sessions.Require(auth.API), func(c *gin.Context) { out = c.Request })
		g.ServeHTTP(httptest.NewRecorder(), r)
		return out
	}
	for _, tc := range []struct {
		name string
		id   *auth.Identity
		want string
	}{
		{"name", &auth.Identity{Email: "a@crombie.dev", Name: " Ana Pérez "}, "Ana Pérez"},
		{"blank name", &auth.Identity{Email: "a.b@crombie.dev", Name: "  "}, "a.b"},
		{"no name", &auth.Identity{Email: "x@y@crombie.dev"}, "x@y"},
		{"no identity", nil, ""},
	} {
		if got := identityNickname(cookieReq(tc.id)); got != tc.want {
			t.Errorf("%s: identityNickname = %q, want %q", tc.name, got, tc.want)
		}
	}
}

// gin.Recovery would dump the Cookie header on a panic; recoverPanics must not (SRV-18).
func TestRecoverPanicsNoCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var buf bytes.Buffer
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
	var access bytes.Buffer
	r := gin.New()
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{Output: &access, Formatter: logFormatter}), recoverPanics)
	r.GET("/boom", func(*gin.Context) { panic("kaboom") })
	req := httptest.NewRequest(http.MethodGet, "/boom?code=querysecret", nil)
	req.Header.Set("Cookie", "chat_session=SECRETCOOKIEVALUE")
	req.Header.Set("Authorization", "Bearer SECRETTOKEN")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	out := buf.String()
	if !strings.Contains(out, "kaboom") || !strings.Contains(out, "/boom") {
		t.Errorf("log = %q, want the panic value and path", out)
	}
	for _, bad := range []string{"SECRETCOOKIEVALUE", "SECRETTOKEN", "Cookie", "querysecret"} {
		if strings.Contains(out, bad) {
			t.Errorf("panic log leaks %q: %q", bad, out)
		}
	}
}

// Production wiring: run() always protects the routes (SRV-15) and names the session user (SRV-11).
func TestRunRequiresSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	acfg := testAuthConfig(t)
	codeCh := make(chan int, 1)
	go func() { codeCh <- run(ctx, ln, config{auth: acfg}) }()
	base := "http://" + ln.Addr().String()

	resp, err := noRedirectClient.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/auth/login?next=%2F" {
		t.Fatalf("GET / = %d Location %q, want 302 /auth/login?next=%%2F", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, err = noRedirectClient.Get(base + "/ws")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /ws = %d, want 401", resp.StatusCode)
	}

	ts := &httptest.Server{URL: base}
	cookie := sessionCookie(t, acfg, auth.Identity{Email: "ana@crombie.dev", Name: "Ana Pérez"})
	if got := wsWelcomeFrom(t, ts, "?nickname=Mallory", cookie); got != "Ana Pérez" {
		t.Errorf("welcome from = %q, want %q", got, "Ana Pérez")
	}
	cancel()
	select {
	case code := <-codeCh:
		if code != 0 {
			t.Fatalf("run returned %d, want 0", code)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("run did not return in time")
	}
}

func TestRunFailsWithoutAuthConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	codeCh := make(chan int, 1)
	go func() { codeCh <- run(context.Background(), ln, config{}) }()
	select {
	case code := <-codeCh:
		if code != 1 {
			t.Fatalf("run returned %d, want 1", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not return in time")
	}
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		c.Close()
		t.Error("listener still open after run failed")
	}
}
