package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"chatroom/internal/auth/authtest"

	"github.com/gin-gonic/gin"
)

const (
	rigClientID     = "test-client-id"
	rigClientSecret = "test-client-secret-value"
	deniedBody      = "DENIED-PAGE"
)

type rigClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *rigClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *rigClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// rig is a Handler wired to the fake provider behind a real HTTP server, so
// cookies and redirects behave as in a browser.
type rig struct {
	h     *Handler
	prov  *authtest.Provider
	srv   *httptest.Server
	clock *rigClock
}

func newRig(t *testing.T) *rig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	prov := authtest.NewProvider(t, rigClientID, rigClientSecret)
	var engine http.Handler
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { engine.ServeHTTP(w, r) }))
	t.Cleanup(srv.Close)

	h, err := New(context.Background(), Config{
		Issuer:        prov.URL,
		ClientID:      rigClientID,
		ClientSecret:  rigClientSecret,
		RedirectURL:   srv.URL + "/auth/callback",
		AllowedDomain: "crombie.dev",
		SessionSecret: []byte(testSecret),
		SessionTTL:    time.Hour,
	}, func(c *gin.Context) { c.String(http.StatusForbidden, deniedBody) })
	if err != nil {
		t.Fatal(err)
	}
	clock := &rigClock{t: time.Now()}
	h.now, h.sessions.now, prov.Now = clock.Now, clock.Now, clock.Now

	r := gin.New()
	h.Register(r.Group("/auth"))
	r.GET("/", h.Sessions().Require(), func(c *gin.Context) {
		id, _ := IdentityFrom(c.Request.Context())
		c.String(http.StatusOK, id.Name+"|"+id.Email)
	})
	r.GET("/hello", h.Sessions().Require(), func(c *gin.Context) { c.String(http.StatusOK, "hello") })
	r.GET("/api", h.Sessions().Require(), func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	engine = r
	return &rig{h: h, prov: prov, srv: srv, clock: clock}
}

func noRedirect() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (g *rig) do(t *testing.T, method, target string, cookies []*http.Cookie, hdr map[string]string) *http.Response {
	t.Helper()
	if strings.HasPrefix(target, "/") {
		target = g.srv.URL + target
	}
	req, err := http.NewRequest(method, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cookies {
		if c != nil {
			req.AddCookie(c)
		}
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func setCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// begin runs /auth/login and the provider's authorize step. It returns the
// callback URL (with code and state) and the flow cookie.
func (g *rig) begin(t *testing.T, next string) (*url.URL, *http.Cookie) {
	t.Helper()
	target := "/auth/login"
	if next != "" {
		target += "?next=" + url.QueryEscape(next)
	}
	resp := g.do(t, "GET", target, nil, nil)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	fc := setCookie(resp, "chat_oidc")
	if fc == nil {
		t.Fatal("no flow cookie")
	}
	resp = g.do(t, "GET", resp.Header.Get("Location"), nil, nil)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("authorize status = %d", resp.StatusCode)
	}
	cb, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return cb, fc
}

func (g *rig) callback(t *testing.T, cb *url.URL, fc *http.Cookie) *http.Response {
	t.Helper()
	return g.do(t, "GET", cb.String(), []*http.Cookie{fc}, nil)
}

func assertFlowCleared(t *testing.T, resp *http.Response) {
	t.Helper()
	c := setCookie(resp, "chat_oidc")
	if c == nil || c.MaxAge >= 0 || c.Value != "" || c.Path != "/auth" {
		t.Errorf("flow cookie not cleared: %+v", c)
	}
}

// signIn completes the flow and returns the session cookie.
func (g *rig) signIn(t *testing.T) *http.Cookie {
	t.Helper()
	cb, fc := g.begin(t, "")
	resp := g.callback(t, cb, fc)
	c := setCookie(resp, "chat_session")
	if resp.StatusCode != http.StatusFound || c == nil {
		t.Fatalf("sign in failed: %d", resp.StatusCode)
	}
	return c
}

func TestCallbackHappyPath(t *testing.T) {
	g := newRig(t)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar} // follows every redirect, like a browser
	resp, err := client.Get(g.srv.URL + "/auth/login")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := body(t, resp); got != "Ana Pérez|ana@crombie.dev" {
		t.Errorf("identity = %q", got)
	}
}

func TestCallbackHappyPathSteps(t *testing.T) {
	g := newRig(t)
	cb, fc := g.begin(t, "/hello")
	resp := g.callback(t, cb, fc)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/hello" {
		t.Fatalf("got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("missing no-store")
	}
	assertFlowCleared(t, resp)
	if setCookie(resp, "chat_session") == nil {
		t.Error("no session cookie")
	}
}

func TestEmailInDomain(t *testing.T) {
	tests := []struct {
		email string
		want  bool
	}{
		{"ana@crombie.dev", true},
		{"ANA@CROMBIE.DEV", true},
		{"a@b@crombie.dev", true},
		{"ana@evilcrombie.dev", false},
		{"ana@crombie.dev.evil.com", false},
		{"ana@sub.crombie.dev", false},
		{"ana@crombie.dev@evil.com", false},
		{"crombie.dev@evil.com", false},
		{"@crombie.dev", false},
		{"ana", false},
		{"", false},
		{"ana@crombie.dev ", false},
		{"ana@crombie.deK", false},
	}
	for _, tt := range tests {
		if got := emailInDomain(tt.email, "crombie.dev"); got != tt.want {
			t.Errorf("emailInDomain(%q) = %v, want %v", tt.email, got, tt.want)
		}
	}
}

func TestCallbackDenied(t *testing.T) {
	tests := []struct {
		name   string
		claims map[string]any
	}{
		{"evilcrombie.dev", map[string]any{"email": "eve@evilcrombie.dev"}},
		{"crombie.dev.evil.com", map[string]any{"email": "eve@crombie.dev.evil.com"}},
		{"gmail", map[string]any{"email": "eve@gmail.com", "hd": nil}},
		{"gmail with hd", map[string]any{"email": "eve@gmail.com"}},
		{"email_verified false", map[string]any{"email_verified": false}},
		{"email_verified missing", map[string]any{"email_verified": nil}},
		{"email_verified string", map[string]any{"email_verified": "true"}},
		{"hd missing", map[string]any{"hd": nil}},
		{"hd mismatch", map[string]any{"hd": "evil.com"}},
		{"email missing", map[string]any{"email": nil}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := newRig(t)
			g.prov.Claims = tt.claims
			cb, fc := g.begin(t, "")
			resp := g.callback(t, cb, fc)
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if got := body(t, resp); got != deniedBody {
				t.Errorf("body = %q", got)
			}
			if setCookie(resp, "chat_session") != nil {
				t.Error("session cookie set")
			}
			assertFlowCleared(t, resp)
		})
	}
}

func TestCallbackDeniedDefaultPage(t *testing.T) {
	g := newRig(t)
	g.h.denied = nil
	g.prov.Claims = map[string]any{"hd": nil}
	cb, fc := g.begin(t, "")
	resp := g.callback(t, cb, fc)
	if resp.StatusCode != http.StatusForbidden || body(t, resp) == "" {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

type badCase struct {
	name        string
	setup       func(g *rig)
	mutate      func(g *rig, q url.Values, fc *http.Cookie) *http.Cookie
	noTokenCall bool
}

func badCases() []badCase {
	keep := func(_ *rig, _ url.Values, fc *http.Cookie) *http.Cookie { return fc }
	return []badCase{
		{name: "state mismatch", noTokenCall: true, mutate: func(_ *rig, q url.Values, fc *http.Cookie) *http.Cookie {
			q.Set("state", "other")
			return fc
		}},
		{name: "state missing", noTokenCall: true, mutate: func(_ *rig, q url.Values, fc *http.Cookie) *http.Cookie {
			q.Del("state")
			return fc
		}},
		{name: "no flow cookie", noTokenCall: true, mutate: func(*rig, url.Values, *http.Cookie) *http.Cookie { return nil }},
		{name: "tampered flow cookie", noTokenCall: true, mutate: func(_ *rig, _ url.Values, fc *http.Cookie) *http.Cookie {
			c := *fc
			c.Value = flip(c.Value, 3)
			return &c
		}},
		{name: "flow cookie from another purpose", noTokenCall: true, mutate: func(g *rig, _ url.Values, _ *http.Cookie) *http.Cookie {
			v, _ := newSealer([]byte(testSecret), purposeSession).seal(flowState{V: cookieVersion, State: "x", Exp: g.clock.Now().Add(time.Hour).Unix()})
			return &http.Cookie{Name: "chat_oidc", Value: v}
		}},
		{name: "expired flow cookie", noTokenCall: true, mutate: func(g *rig, _ url.Values, fc *http.Cookie) *http.Cookie {
			g.clock.Advance(11 * time.Minute)
			return fc
		}},
		{name: "provider error", noTokenCall: true, mutate: func(_ *rig, q url.Values, fc *http.Cookie) *http.Cookie {
			q.Del("code")
			q.Set("error", "access_denied")
			return fc
		}},
		{name: "missing code", noTokenCall: true, mutate: func(_ *rig, q url.Values, fc *http.Cookie) *http.Cookie {
			q.Del("code")
			return fc
		}},
		{name: "bad code", mutate: func(_ *rig, q url.Values, fc *http.Cookie) *http.Cookie {
			q.Set("code", "nope")
			return fc
		}},
		{name: "nonce mismatch", setup: func(g *rig) { g.prov.Claims = map[string]any{"nonce": "evil"} }, mutate: keep},
		{name: "wrong audience", setup: func(g *rig) { g.prov.Claims = map[string]any{"aud": "someone-else"} }, mutate: keep},
		{name: "wrong issuer", setup: func(g *rig) { g.prov.Claims = map[string]any{"iss": "https://evil.example"} }, mutate: keep},
		{name: "expired id_token", setup: func(g *rig) {
			g.prov.Now = func() time.Time { return g.clock.Now().Add(-time.Hour) }
		}, mutate: keep},
		{name: "token endpoint failure", setup: func(g *rig) { g.prov.TokenFail = true }, mutate: keep},
	}
}

// runBad drives one failing callback and returns the response plus the code,
// state and flow cookie it used.
func runBad(t *testing.T, tc badCase) (g *rig, resp *http.Response, cb *url.URL, fc *http.Cookie) {
	g = newRig(t)
	if tc.setup != nil {
		tc.setup(g)
	}
	cb, fc = g.begin(t, "")
	q := cb.Query()
	sent := tc.mutate(g, q, fc)
	cb2 := *cb
	cb2.RawQuery = q.Encode()
	return g, g.callback(t, &cb2, sent), cb, fc
}

func TestCallbackBadRequest(t *testing.T) {
	for _, tc := range badCases() {
		t.Run(tc.name, func(t *testing.T) {
			g, resp, _, _ := runBad(t, tc)
			if resp.StatusCode != http.StatusBadRequest {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if got := body(t, resp); got != badRequestText {
				t.Errorf("body = %q", got)
			}
			if setCookie(resp, "chat_session") != nil {
				t.Error("session cookie set")
			}
			assertFlowCleared(t, resp)
			if tc.noTokenCall && g.prov.TokenCalls() != 0 {
				t.Errorf("token endpoint called %d times", g.prov.TokenCalls())
			}
		})
	}
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestCallbackLogsNoSecrets(t *testing.T) {
	var logs lockedBuf
	prev := log.Writer()
	log.SetOutput(&logs)
	t.Cleanup(func() { log.SetOutput(prev) })

	type run struct{ secrets []string }
	var runs run
	check := func(cb *url.URL, fc *http.Cookie) {
		runs.secrets = append(runs.secrets, cb.Query().Get("code"), cb.Query().Get("state"), fc.Value)
	}
	for _, tc := range badCases() {
		_, _, cb, fc := runBad(t, tc)
		check(cb, fc)
	}
	// Denials, including a look-alike domain: only the domain may appear.
	g := newRig(t)
	g.prov.Claims = map[string]any{"email": "mallory@evilcrombie.dev"}
	cb, fc := g.begin(t, "")
	g.callback(t, cb, fc)
	check(cb, fc)

	out := logs.String()
	if !strings.Contains(out, "auth: callback rejected: ") {
		t.Fatalf("no rejection logged: %q", out)
	}
	for _, s := range append(runs.secrets, rigClientSecret, "fake-access-token", "mallory", "bad code", "nope", testSecret) {
		if s != "" && strings.Contains(out, s) {
			t.Errorf("log contains %q", s)
		}
	}
	if !strings.Contains(out, "evilcrombie.dev") {
		t.Error("denied domain not logged")
	}
}

func TestSafeNext(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"/", "/"},
		{"/hello", "/hello"},
		{"/a/b?x=1&y=2", "/a/b?x=1&y=2"},
		{"", "/"},
		{"hello", "/"},
		{"//evil.com", "/"},
		{"/\\evil.com", "/"},
		{"/a\\b", "/"},
		{"https://evil.com", "/"},
		{"/a b", "/"},
		{"/a\tb", "/"},
		{"/a\nb", "/"},
		{"/a\x7fb", "/"},
		{"/auth", "/"},
		{"/auth/login", "/"},
		{"/auth/login?next=/", "/"},
		{"/auth?x=1", "/"},
		{"/%61uth/login", "/"},
		{"/authors", "/authors"},
		{"/" + strings.Repeat("a", 511), "/" + strings.Repeat("a", 511)},
		{"/" + strings.Repeat("a", 512), "/"},
		{"/%zz", "/"},
	}
	for _, tt := range tests {
		if got := safeNext(tt.in); got != tt.want {
			t.Errorf("safeNext(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestOpenRedirectNextIgnored(t *testing.T) {
	g := newRig(t)
	cb, fc := g.begin(t, "//evil.com")
	resp := g.callback(t, cb, fc)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/" {
		t.Errorf("got %d %q", resp.StatusCode, resp.Header.Get("Location"))
	}
}

// A validly signed flow cookie carrying a hostile next must still redirect to
// "/": the callback re-validates next instead of trusting the cookie.
func TestCallbackRedirectRevalidatesNext(t *testing.T) {
	g := newRig(t)
	cb, flowCookie := g.begin(t, "")
	var st flowState
	if err := g.h.flow.open(flowCookie.Value, &st); err != nil {
		t.Fatal(err)
	}
	st.Next = "//evil.com"
	v, err := g.h.flow.seal(st)
	if err != nil {
		t.Fatal(err)
	}
	forged := *flowCookie
	forged.Value = v
	resp := g.callback(t, cb, &forged)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/" {
		t.Errorf("got %d %q, want 302 /", resp.StatusCode, resp.Header.Get("Location"))
	}
}

func TestLoginRedirect(t *testing.T) {
	g := newRig(t)
	resp := g.do(t, "GET", "/auth/login?next=/hello", nil, nil)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	loc, err := url.Parse(resp.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(loc.String(), g.prov.URL+"/authorize?") {
		t.Fatalf("location = %q", loc)
	}
	q := loc.Query()
	want := map[string]string{
		"response_type":         "code",
		"client_id":             rigClientID,
		"redirect_uri":          g.srv.URL + "/auth/callback",
		"scope":                 "openid email profile",
		"code_challenge_method": "S256",
		"hd":                    "crombie.dev",
	}
	for k, v := range want {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	if len(q.Get("state")) < 26 || len(q.Get("nonce")) < 26 || len(q.Get("code_challenge")) != 43 {
		t.Errorf("weak or missing state/nonce/challenge: %v", q)
	}
	if q.Has("access_type") {
		t.Error("offline access requested")
	}

	fc := setCookie(resp, "chat_oidc")
	if fc == nil {
		t.Fatal("no flow cookie")
	}
	if !fc.HttpOnly || fc.SameSite != http.SameSiteLaxMode || fc.Path != "/auth" || fc.Domain != "" ||
		fc.MaxAge <= 0 || fc.MaxAge > 600 || fc.Secure {
		t.Errorf("flow cookie attributes: %+v", fc)
	}
	var st flowState
	if err := newSealer([]byte(testSecret), purposeFlow).open(fc.Value, &st); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(st.Verifier))
	if st.State != q.Get("state") || st.Nonce != q.Get("nonce") || st.Next != "/hello" ||
		base64.RawURLEncoding.EncodeToString(sum[:]) != q.Get("code_challenge") {
		t.Errorf("flow cookie does not match authorize request: %+v", st)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("missing no-store")
	}
	if q.Has("prompt") {
		t.Errorf("prompt = %q without switch=1", q.Get("prompt"))
	}
}

func TestLoginSwitchAccount(t *testing.T) {
	g := newRig(t)
	for _, tc := range []struct{ query, prompt string }{
		{"?switch=1", "select_account"},
		{"?switch=0", ""},
		{"?switch=true", ""},
		{"?switch=", ""},
		{"", ""},
	} {
		resp := g.do(t, "GET", "/auth/login"+tc.query, nil, nil)
		loc, err := url.Parse(resp.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		if got := loc.Query().Get("prompt"); got != tc.prompt {
			t.Errorf("query %q: prompt = %q, want %q", tc.query, got, tc.prompt)
		}
	}
}

func TestLoginFreshValuesEachTime(t *testing.T) {
	g := newRig(t)
	a := g.do(t, "GET", "/auth/login", nil, nil).Header.Get("Location")
	b := g.do(t, "GET", "/auth/login", nil, nil).Header.Get("Location")
	ua, _ := url.Parse(a)
	ub, _ := url.Parse(b)
	for _, k := range []string{"state", "nonce", "code_challenge"} {
		if ua.Query().Get(k) == ub.Query().Get(k) {
			t.Errorf("%s repeated", k)
		}
	}
}

func TestLoginRateLimited(t *testing.T) {
	g := newRig(t)
	for i := 1; i <= 21; i++ {
		resp := g.do(t, "GET", "/auth/login", nil, nil)
		want := http.StatusFound
		if i == 21 {
			want = http.StatusTooManyRequests
		}
		if resp.StatusCode != want {
			t.Fatalf("request %d: status = %d, want %d", i, resp.StatusCode, want)
		}
	}
}

func TestLogoutRevokes(t *testing.T) {
	g := newRig(t)
	sess := g.signIn(t)
	if resp := g.do(t, "GET", "/api", []*http.Cookie{sess}, nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("before logout: %d", resp.StatusCode)
	}
	resp := g.do(t, "POST", "/auth/logout", []*http.Cookie{sess}, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d", resp.StatusCode)
	}
	if c := setCookie(resp, "chat_session"); c == nil || c.MaxAge >= 0 || c.Path != "/" {
		t.Errorf("session cookie not cleared: %+v", c)
	}
	if resp.Header.Get("Cache-Control") != "no-store" {
		t.Error("missing no-store")
	}
	if resp := g.do(t, "GET", "/api", []*http.Cookie{sess}, nil); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("after logout: %d", resp.StatusCode)
	}
}

func TestLogoutOrigin(t *testing.T) {
	g := newRig(t)
	sess := g.signIn(t)
	valid := func() bool {
		return g.do(t, "GET", "/api", []*http.Cookie{sess}, nil).StatusCode == http.StatusOK
	}
	for _, origin := range []string{"https://evil.example", "null", "http://[::1", "evil", g.srv.URL + ".evil.com", ""} {
		hdr := map[string]string{"Origin": origin}
		if origin == "" {
			hdr = map[string]string{"Origin": " "} // present but empty
		}
		resp := g.do(t, "POST", "/auth/logout", []*http.Cookie{sess}, hdr)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("origin %q: status = %d", origin, resp.StatusCode)
		}
		if setCookie(resp, "chat_session") != nil {
			t.Errorf("origin %q: cookie touched", origin)
		}
		if !valid() {
			t.Fatalf("origin %q: session revoked", origin)
		}
	}
	resp := g.do(t, "POST", "/auth/logout", []*http.Cookie{sess}, map[string]string{"Origin": strings.ToUpper(g.srv.URL[:4]) + g.srv.URL[4:]})
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("same origin: status = %d", resp.StatusCode)
	}
	if valid() {
		t.Error("same-origin logout did not revoke")
	}
}

func TestLogoutWithoutOriginOrSession(t *testing.T) {
	g := newRig(t)
	resp := g.do(t, "POST", "/auth/logout", nil, nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if c := setCookie(resp, "chat_session"); c == nil || c.MaxAge >= 0 {
		t.Errorf("cookie not cleared: %+v", c)
	}
	if resp := g.do(t, "GET", "/auth/logout", nil, nil); resp.StatusCode == http.StatusNoContent {
		t.Error("GET logout accepted")
	}
}

func TestNewRejectsInvalidConfig(t *testing.T) {
	if _, err := New(context.Background(), Config{}, nil); err == nil {
		t.Error("expected validation error")
	}
}
