package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

var t0 = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func newTestSessions(secure bool) (*Sessions, *time.Time) {
	now := t0
	s := NewSessions([]byte(testSecret), time.Hour, secure)
	s.now = func() time.Time { return now }
	return s, &now
}

func mint(t *testing.T, s *Sessions) *http.Cookie {
	t.Helper()
	c, err := s.Cookie(Identity{Email: "ana@crombie.dev", Name: "Ana Pérez"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func reqWith(c *http.Cookie) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	if c != nil {
		r.AddCookie(c)
	}
	return r
}

func flip(s string, i int) string {
	b := []byte(s)
	if b[i] == 'A' {
		b[i] = 'B'
	} else {
		b[i] = 'A'
	}
	return string(b)
}

func TestSessionVerifyRoundTrip(t *testing.T) {
	s, _ := newTestSessions(false)
	sess, ok := s.verify(reqWith(mint(t, s)))
	if !ok || sess.Email != "ana@crombie.dev" || sess.Name != "Ana Pérez" || sess.Sid == "" {
		t.Fatalf("verify = %+v, %v", sess, ok)
	}
}

func TestSessionSidsAreUnique(t *testing.T) {
	s, _ := newTestSessions(false)
	a, _ := s.verify(reqWith(mint(t, s)))
	b, _ := s.verify(reqWith(mint(t, s)))
	if a.Sid == b.Sid {
		t.Fatal("sid repeated")
	}
}

func TestSessionVerifyRejects(t *testing.T) {
	s, _ := newTestSessions(false)
	good := mint(t, s)
	payload, mac, _ := strings.Cut(good.Value, ".")

	flowSealer := newSealer([]byte(testSecret), purposeFlow)
	flowValue, err := flowSealer.seal(session{V: 1, Sid: "x", Email: "ana@crombie.dev", Exp: t0.Add(time.Hour).Unix()})
	if err != nil {
		t.Fatal(err)
	}
	otherKey := NewSessions([]byte("another-secret-another-secret-12"), time.Hour, false)
	otherKey.now = s.now

	seal := func(p session) string {
		v, err := s.sealer.seal(p)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	valid := session{V: 1, Sid: "sid", Email: "a@crombie.dev", Exp: t0.Add(time.Hour).Unix()}
	wrongV, noEmail, noSid := valid, valid, valid
	wrongV.V = 2
	noEmail.Email = ""
	noSid.Sid = ""

	tests := map[string]string{
		"empty":           "",
		"no dot":          payload,
		"two dots":        good.Value + ".x",
		"payload byte":    flip(payload, 5) + "." + mac,
		"mac byte":        payload + "." + flip(mac, 3),
		"mac not base64":  payload + ".!!!",
		"flow cookie":     flowValue,
		"other secret":    mint(t, otherKey).Value,
		"oversized":       good.Value + strings.Repeat("A", maxCookieValue),
		"wrong version":   seal(wrongV),
		"missing email":   seal(noEmail),
		"missing sid":     seal(noSid),
		"signed non-json": b64.EncodeToString([]byte("nope")) + "." + b64.EncodeToString(s.sealer.mac(b64.EncodeToString([]byte("nope")))),
	}
	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			c := &http.Cookie{Name: sessionCookieName(false), Value: value}
			if _, ok := s.verify(reqWith(c)); ok {
				t.Fatal("accepted")
			}
		})
	}
	if _, ok := s.verify(reqWith(nil)); ok {
		t.Fatal("accepted without cookie")
	}
	if _, ok := s.verify(reqWith(&http.Cookie{Name: "other", Value: good.Value})); ok {
		t.Fatal("accepted under another cookie name")
	}
}

func TestSessionExpired(t *testing.T) {
	s, now := newTestSessions(false)
	c := mint(t, s)
	*now = t0.Add(time.Hour - time.Second)
	if _, ok := s.verify(reqWith(c)); !ok {
		t.Fatal("rejected before expiry")
	}
	*now = t0.Add(time.Hour)
	if _, ok := s.verify(reqWith(c)); ok {
		t.Fatal("accepted at expiry")
	}
}

func TestSessionRevokeAndPrune(t *testing.T) {
	s, now := newTestSessions(false)
	c := mint(t, s)
	sess, _ := s.verify(reqWith(c))
	s.revoke(sess.Sid, time.Unix(sess.Exp, 0))
	if _, ok := s.verify(reqWith(c)); ok {
		t.Fatal("revoked session accepted")
	}
	if len(s.revoked) != 1 {
		t.Fatalf("revoked = %d", len(s.revoked))
	}
	*now = t0.Add(2 * time.Hour)
	s.revoke("other", t0.Add(3*time.Hour)) // any call prunes expired entries
	if _, still := s.revoked[sess.Sid]; still || len(s.revoked) != 1 {
		t.Fatalf("not pruned: %v", s.revoked)
	}
	s.revoke("already-expired", t0) // never stored
	if len(s.revoked) != 1 {
		t.Fatal("expired revocation stored")
	}
}

func TestCookieAttributes(t *testing.T) {
	secure, _ := newTestSessions(true)
	c := mint(t, secure)
	if c.Name != "__Host-chat_session" || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode ||
		c.Path != "/" || c.Domain != "" || c.MaxAge != 3600 {
		t.Fatalf("secure session cookie: %+v", c)
	}
	plain, _ := newTestSessions(false)
	if c := mint(t, plain); c.Name != "chat_session" || c.Secure {
		t.Fatalf("plain session cookie: %+v", c)
	}
	if c := secure.clearCookie(); c.MaxAge >= 0 || c.Name != "__Host-chat_session" || c.Path != "/" || !c.Secure || !c.HttpOnly {
		t.Fatalf("clear cookie: %+v", c)
	}
	f := newFlowCookie("v", true)
	if f.Name != "__Secure-chat_oidc" || f.Path != "/auth" || !f.Secure || !f.HttpOnly ||
		f.SameSite != http.SameSiteLaxMode || f.MaxAge != 600 {
		t.Fatalf("flow cookie: %+v", f)
	}
	if f := newFlowCookie("v", false); f.Name != "chat_oidc" || f.Secure {
		t.Fatalf("plain flow cookie: %+v", f)
	}
	if f := clearFlowCookie(true); f.MaxAge >= 0 || f.Path != "/auth" || f.Name != "__Secure-chat_oidc" {
		t.Fatalf("clear flow cookie: %+v", f)
	}
}

func TestCookieTooLarge(t *testing.T) {
	s, _ := newTestSessions(false)
	if _, err := s.Cookie(Identity{Email: "a@crombie.dev", Name: strings.Repeat("x", 4000)}); err == nil {
		t.Fatal("oversized cookie minted")
	}
}

func TestRequire(t *testing.T) {
	s, now := newTestSessions(false)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	var seen Identity
	r.GET("/", s.Require(), func(c *gin.Context) {
		seen, _ = IdentityFrom(c.Request.Context())
		c.String(200, "page")
	})
	r.GET("/rooms", s.Require(), func(c *gin.Context) { c.String(200, "api") })

	do := func(path string, c *http.Cookie) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, func() *http.Request {
			q := httptest.NewRequest("GET", path, nil)
			if c != nil {
				q.AddCookie(c)
			}
			return q
		}())
		return w
	}

	w := do("/", nil)
	if w.Code != 401 || strings.Contains(w.Body.String(), "page") {
		t.Fatalf("no session: %d %v %q", w.Code, w.Header(), w.Body)
	}
	w = do("/rooms", nil)
	if w.Code != 401 || w.Body.String() != `{"error":"unauthorized"}` || w.Header().Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("api: %d %v %q", w.Code, w.Header(), w.Body)
	}

	c := mint(t, s)
	if id, ok := s.Identify(requestWith(c)); !ok || id != (Identity{Email: "ana@crombie.dev", Name: "Ana Pérez"}) {
		t.Fatalf("Identify = %+v %v", id, ok)
	}
	if _, ok := s.Identify(httptest.NewRequest("GET", "/", nil)); ok {
		t.Fatal("Identify without a cookie")
	}
	if w := do("/", c); w.Code != 200 || seen != (Identity{Email: "ana@crombie.dev", Name: "Ana Pérez"}) {
		t.Fatalf("valid page: %d %+v", w.Code, seen)
	}
	if w := do("/rooms", c); w.Code != 200 {
		t.Fatalf("valid api: %d", w.Code)
	}
	tampered := *c
	tampered.Value = flip(c.Value, 2)
	if w := do("/rooms", &tampered); w.Code != 401 {
		t.Fatalf("tampered: %d", w.Code)
	}
	*now = t0.Add(2 * time.Hour)
	if w := do("/", c); w.Code != 401 {
		t.Fatalf("expired: %d", w.Code)
	}
	if _, ok := s.Identify(requestWith(c)); ok {
		t.Fatal("Identify accepted an expired session")
	}
	if _, ok := IdentityFrom(httptest.NewRequest("GET", "/", nil).Context()); ok {
		t.Fatal("identity without Require")
	}
}

func requestWith(c *http.Cookie) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(c)
	return r
}
