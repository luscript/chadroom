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
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"chatroom/internal/auth"
	"chatroom/internal/auth/authtest"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// openGuards lets every request through: for router tests that are not about authentication.
// Production code has no such path; newRouter refuses incomplete guards.
func openGuards() guards {
	noop := func(*gin.Context) {}
	chatOnly := func(chat, _ gin.HandlerFunc) gin.HandlerFunc { return chat }
	return guards{page: chatOnly, api: noop, register: func(*gin.RouterGroup) {}}
}

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

// authRouter is the full router with a real auth handler and gif / playlist proxies whose upstream
// is unreachable (a 401 must come first, so it is never called).
func authRouter(t *testing.T) (*httptest.Server, auth.Config) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	h, cfg := testAuthHandler(t)
	chatSrv := newChatServer(nil, chatIdentity)
	ctx, cancel := context.WithCancel(context.Background())
	go chatSrv.Run(ctx)
	const dead = "http://127.0.0.1:1"
	ts := httptest.NewServer(newRouter(chatSrv, newGifSearch(dead, "k"), newPlaylistImport(dead, "k"), authGuards(h)))
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
	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws?room=secret",
		http.Header{"Origin": []string{ts.URL}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ws dial err=%v resp=%v, want 401", err, resp)
	}
}

func TestAuthPublicRoutes(t *testing.T) {
	ts, _ := authRouter(t)
	for _, path := range []string{"/app.css", "/favicon.svg", "/favicon-unread.svg"} {
		resp, err := noRedirectClient.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 without a session", path, resp.StatusCode)
		}
	}
	resp, err := noRedirectClient.Get(ts.URL + "/auth/signed-out")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /auth/signed-out = %d, want 404", resp.StatusCode)
	}
	resp, err = noRedirectClient.Get(ts.URL + "/auth/login")
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
		"denied": {http.StatusForbidden, deniedHTML},
		"login":  {http.StatusOK, loginHTML},
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

func TestChatIdentity(t *testing.T) {
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
		g.GET("/", sessions.Require(), func(c *gin.Context) { out = c.Request })
		g.ServeHTTP(httptest.NewRecorder(), r)
		return out
	}
	for _, tc := range []struct {
		name        string
		id          *auth.Identity
		wantAccount string
		wantNick    string
	}{
		{"name", &auth.Identity{Email: "a@crombie.dev", Name: " Ana Pérez "}, "a@crombie.dev", "Ana Pérez"},
		{"blank name", &auth.Identity{Email: "a.b@crombie.dev", Name: "  "}, "a.b@crombie.dev", "a.b"},
		{"no name", &auth.Identity{Email: "x@y@crombie.dev"}, "x@y@crombie.dev", "x@y"},
		{"no identity", nil, "", ""},
	} {
		account, nick := chatIdentity(cookieReq(tc.id))
		if account != tc.wantAccount || nick != tc.wantNick {
			t.Errorf("%s: chatIdentity = (%q, %q), want (%q, %q)", tc.name, account, nick, tc.wantAccount, tc.wantNick)
		}
	}
}

// wsDial opens /ws with the session cookie and consumes welcome, users and history, returning the
// connection and the welcome's from.
func wsDial(t *testing.T, ts *httptest.Server, cookie *http.Cookie) (*websocket.Conn, string) {
	t.Helper()
	hdr := http.Header{"Origin": []string{ts.URL}}
	hdr.Add("Cookie", cookie.String())
	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws", hdr)
	if err != nil {
		t.Fatalf("dial: %v (resp %v)", err, resp)
	}
	t.Cleanup(func() { conn.Close() })
	var from string
	for i, want := range []string{"welcome", "users", "history"} {
		f := wsFrame(t, conn)
		if f.Type != want {
			t.Fatalf("frame %d type = %q, want %q", i, f.Type, want)
		}
		if i == 0 {
			from = f.From
		}
	}
	return conn, from
}

type wsFrameFields struct{ Type, From string }

func wsFrame(t *testing.T, conn *websocket.Conn) wsFrameFields {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var f wsFrameFields
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("frame %q: %v", data, err)
	}
	return f
}

// CHAT-41: two tabs with the same session are one member: same name, no second join.
func TestSameAccountInTwoTabs(t *testing.T) {
	ts, cfg := authRouter(t)
	ana := sessionCookie(t, cfg, auth.Identity{Email: "ana@crombie.dev", Name: "Ana Pérez"})
	first, from1 := wsDial(t, ts, ana)
	if f := wsFrame(t, first); f.Type != "join" || f.From != "Ana Pérez" {
		t.Fatalf("first tab own frame = %+v, want its own join", f)
	}
	_, from2 := wsDial(t, ts, ana)
	if from1 != "Ana Pérez" || from2 != "Ana Pérez" {
		t.Fatalf("welcomes = %q, %q, want both Ana Pérez", from1, from2)
	}
	// A later member's join is the marker: if the second tab had announced itself, it would arrive first.
	carl := sessionCookie(t, cfg, auth.Identity{Email: "carl@crombie.dev", Name: "Carl"})
	wsDial(t, ts, carl)
	if f := wsFrame(t, first); f.Type != "join" || f.From != "Carl" {
		t.Fatalf("first tab next frame = %+v, want join of Carl", f)
	}
}

// CHAT-41: different accounts with the same name are different members.
func TestDifferentAccountsSameName(t *testing.T) {
	ts, cfg := authRouter(t)
	a := sessionCookie(t, cfg, auth.Identity{Email: "ana.perez@crombie.dev", Name: "Ana Pérez"})
	b := sessionCookie(t, cfg, auth.Identity{Email: "ana.p@crombie.dev", Name: "Ana Pérez"})
	_, from1 := wsDial(t, ts, a)
	_, from2 := wsDial(t, ts, b)
	if from1 != "Ana Pérez" || from2 != "Ana Pérez (2)" {
		t.Fatalf("welcomes = %q, %q, want Ana Pérez and Ana Pérez (2)", from1, from2)
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
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !bytes.Equal(page, loginHTML) {
		t.Fatalf("GET / = %d, want 200 with the login page", resp.StatusCode)
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

// newRouter must refuse guards with a missing piece instead of serving unauthenticated.
func TestNewRouterPanicsWithoutGuards(t *testing.T) {
	gin.SetMode(gin.TestMode)
	noop := func(*gin.Context) {}
	pg := func(chat, _ gin.HandlerFunc) gin.HandlerFunc { return chat }
	reg := func(*gin.RouterGroup) {}
	for name, g := range map[string]guards{
		"zero":        {},
		"no page":     {api: noop, register: reg},
		"no api":      {page: pg, register: reg},
		"no register": {page: pg, api: noop},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: newRouter did not panic", name)
				}
			}()
			newRouter(newChatServer(nil, queryIdentity), nil, nil, g)
		}()
	}
}

// SRV-06: the session check comes before the origin check, so a foreign origin without a cookie is a 401.
func TestWebSocketForeignOriginNoCookie401(t *testing.T) {
	ts, _ := authRouter(t)
	_, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws",
		http.Header{"Origin": []string{"https://evil.example"}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("ws dial err=%v resp=%v, want 401", err, resp)
	}
}

// CHAT-41: a name that is empty once cleaned (bidi / control characters only) falls back to the email.
func TestIdentityNicknameUnusableName(t *testing.T) {
	ts, cfg := authRouter(t)
	c := sessionCookie(t, cfg, auth.Identity{Email: "bob.smith@crombie.dev", Name: "\u202e\x00"})
	if got := wsWelcomeFrom(t, ts, "", c); got != "bob.smith" {
		t.Errorf("welcome from = %q, want bob.smith", got)
	}
}

// Guards on the embedded assets: the login hand-off and the logout flow (CHAT-16, CHAT-42).
func TestAppJSAuthFlow(t *testing.T) {
	js := string(appJS)
	if !strings.Contains(js, "location.assign('/')") {
		t.Error("app.js does not send the user back to the login page at /")
	}
	for _, path := range []string{"'/rooms'", "'/gifs?q='", "'/playlist?list='"} {
		at := strings.Index(js, "fetch("+path)
		if at < 0 {
			t.Errorf("app.js has no fetch(%s", path)
			continue
		}
		window := js[at:min(at+400, len(js))]
		if !strings.Contains(window, "res.status === 401") || !strings.Contains(window, "toLogin()") {
			t.Errorf("fetch(%s has no 401 branch calling toLogin()", path)
		}
	}
	at := strings.Index(js, "fetch('/auth/logout'")
	if at < 0 || !strings.Contains(js[at:min(at+200, len(js))], "method: 'POST'") {
		t.Fatal("app.js does not POST /auth/logout")
	}
	// The login page is reached only after the server confirmed the logout.
	rest := js[at:]
	ok := strings.Index(rest, "if (!res.ok) throw")
	replace := strings.Index(rest, "location.replace('/')")
	if ok < 0 || replace < 0 || ok > replace {
		t.Error("app.js must check res.ok before location.replace('/')")
	}
	if strings.Count(js, "location.replace('/')") != 1 {
		t.Error("location.replace('/') must appear only in the logout handler")
	}
	if strings.Contains(js, "signed-out") || strings.Contains(js, "'/auth/login'") {
		t.Error("app.js still references /auth/signed-out or navigates to /auth/login directly")
	}
}

// The start view is a loading / retry state (no nickname form) and the header has the logout button.
func TestIndexHasSessionMarkup(t *testing.T) {
	html := string(indexHTML)
	if !strings.Contains(html, `id="start-view"`) {
		t.Error(`index.html missing id="start-view"`)
	}
	if tag := startTagOf(t, html, "leave-btn"); !strings.Contains(tag, "<button") {
		t.Errorf("leave-btn tag = %q", tag)
	}
	if !regexp.MustCompile(`(?s)id="leave-btn"[^>]*>\s*Cerrar sesión\s*</button>`).MatchString(html) {
		t.Error(`#leave-btn does not read "Cerrar sesión"`)
	}
	if strings.Contains(html, `id="nickname"`) || strings.Contains(html, `name="nickname"`) {
		t.Error("index.html still has a nickname input")
	}
}

// Only the denied page offers the account chooser; the login page has just the primary link.
func TestAuthPagesLinkToAccountChooser(t *testing.T) {
	if !strings.Contains(string(deniedHTML), `href="/auth/login?switch=1"`) {
		t.Error("denied page does not link to /auth/login?switch=1")
	}
	if !strings.Contains(string(loginHTML), `href="/auth/login"`) || strings.Contains(string(loginHTML), "switch=1") {
		t.Error("login page must link to /auth/login only, without switch=1")
	}
}

// SRV-02, CHAT-20: "/" is the chat page with a session and the login page (200, no redirect) without one.
func TestRootLoginPageOrChat(t *testing.T) {
	const wantCSP = "default-src 'none'; style-src 'self'; img-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	ts, cfg := authRouter(t)
	get := func(c *http.Cookie) (*http.Response, string) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
		if c != nil {
			req.AddCookie(c)
		}
		resp, err := noRedirectClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	good := sessionCookie(t, cfg, auth.Identity{Email: "ana@crombie.dev", Name: "Ana"})
	tampered := *good
	tampered.Value = tampered.Value[:len(tampered.Value)-2] + "xx"
	expired := sessionCookie(t, auth.Config{SessionSecret: cfg.SessionSecret, SessionTTL: -time.Minute}, auth.Identity{Email: "ana@crombie.dev"})

	for name, c := range map[string]*http.Cookie{"no cookie": nil, "tampered": &tampered, "expired": expired} {
		t.Run(name, func(t *testing.T) {
			resp, body := get(c)
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET / = %d, want 200", resp.StatusCode)
			}
			for k, want := range map[string]string{
				"Content-Security-Policy": wantCSP,
				"Cache-Control":           "no-store",
				"Vary":                    "Cookie",
				"X-Content-Type-Options":  "nosniff",
				"X-Frame-Options":         "DENY",
				"Referrer-Policy":         "no-referrer",
			} {
				if got := resp.Header.Get(k); got != want {
					t.Errorf("%s = %q, want %q", k, got, want)
				}
			}
			for _, want := range []string{"Iniciar sesión con Google", `href="/auth/login"`} {
				if !strings.Contains(body, want) {
					t.Errorf("login page misses %q", want)
				}
			}
			if strings.Contains(body, "switch=1") {
				t.Error("login page links to switch=1")
			}
			if strings.Contains(body, `id="start-view"`) || strings.Contains(body, "/app.js") {
				t.Error("login page carries the chat markup")
			}
		})
	}

	resp, body := get(good)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `id="start-view"`) || strings.Contains(body, "Iniciar sesión con Google") {
		t.Fatalf("GET / with a session = %d, want the chat page", resp.StatusCode)
	}
	if got := resp.Header.Get("Vary"); got != "Cookie" {
		t.Errorf("chat page Vary = %q, want Cookie", got)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src") {
		t.Error("chat page lost its CSP")
	}

	resp2, err := noRedirectClient.Get(ts.URL + "/app.js")
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("GET /app.js without a session = %d, want 401", resp2.StatusCode)
	}
}
