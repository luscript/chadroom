package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRateLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	l := newRateLimiter(loginLimit, loginWindow, 2)
	now := t0
	l.now = func() time.Time { return now }
	r := gin.New()
	r.GET("/auth/login", l.middleware())
	r.GET("/auth/callback", l.middleware())

	do := func(path, addr string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = addr
		req.Header.Set("X-Forwarded-For", "9.9.9.9") // must be ignored
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	for i := 0; i < 20; i++ { // shared by both routes
		path := "/auth/login"
		if i%2 == 1 {
			path = "/auth/callback"
		}
		if w := do(path, "192.0.2.1:1000"); w.Code != 200 {
			t.Fatalf("request %d: %d", i+1, w.Code)
		}
	}
	w := do("/auth/login", "192.0.2.1:2000") // same IP, other port
	if w.Code != 429 || w.Header().Get("Retry-After") != "60" {
		t.Fatalf("21st: %d %v", w.Code, w.Header())
	}
	if w := do("/auth/login", "192.0.2.2:1000"); w.Code != 200 {
		t.Fatalf("other IP: %d", w.Code)
	}
	now = now.Add(loginWindow)
	if w := do("/auth/login", "192.0.2.1:1000"); w.Code != 200 {
		t.Fatalf("next window: %d", w.Code)
	}

	// Bounded map: max 2 live keys, a 3rd unknown IP is refused until windows end.
	if w := do("/auth/login", "192.0.2.3:1"); w.Code != 200 {
		t.Fatalf("second key: %d", w.Code)
	}
	if w := do("/auth/login", "192.0.2.4:1"); w.Code != 429 {
		t.Fatalf("full table: %d", w.Code)
	}
	if w := do("/auth/login", "192.0.2.1:1"); w.Code != 200 {
		t.Fatalf("known IP while full: %d", w.Code)
	}
	now = now.Add(loginWindow)
	if w := do("/auth/login", "192.0.2.4:1"); w.Code != 200 {
		t.Fatalf("after idle entries dropped: %d", w.Code)
	}
	if len(l.hits) != 1 {
		t.Fatalf("hits = %d, idle entries not dropped", len(l.hits))
	}
}

func TestRateLimitIPv6Prefix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	l := newRateLimiter(2, loginWindow, 100)
	l.now = func() time.Time { return t0 }
	r := gin.New()
	r.GET("/auth/login", l.middleware())
	do := func(addr string) int {
		req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
		req.RemoteAddr = addr
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	// Same /64: the budget is shared.
	if do("[2001:db8:1:2::1]:1") != 200 || do("[2001:db8:1:2:ffff::9]:2") != 200 {
		t.Fatal("first two requests in the /64 should pass")
	}
	if c := do("[2001:db8:1:2:aaaa::5]:3"); c != 429 {
		t.Fatalf("third address in the same /64: %d", c)
	}
	// Different /64: own budget.
	if c := do("[2001:db8:1:3::1]:1"); c != 200 {
		t.Fatalf("other /64: %d", c)
	}
	// IPv4 stays per-address.
	if do("192.0.2.1:1") != 200 || do("192.0.2.1:2") != 200 || do("192.0.2.1:3") != 429 {
		t.Fatal("IPv4 budget should be per address")
	}
	if c := do("192.0.2.2:1"); c != 200 {
		t.Fatalf("other IPv4: %d", c)
	}
	// IPv4-mapped IPv6 stays per-address too.
	if do("[::ffff:192.0.2.9]:1") != 200 || do("[::ffff:192.0.2.9]:2") != 200 || do("[::ffff:192.0.2.9]:3") != 429 {
		t.Fatal("IPv4-mapped budget should be per address")
	}
	if c := do("[::ffff:192.0.2.10]:1"); c != 200 {
		t.Fatalf("other mapped address: %d", c)
	}
}

func TestRateLimitFullTableSweepsAtMostOncePerSecond(t *testing.T) {
	l := newRateLimiter(5, loginWindow, 1)
	now := t0
	l.now = func() time.Time { return now }
	if !l.allow("a") {
		t.Fatal("first key")
	}
	now = now.Add(loginWindow) // "a" is now expired but the table looks full
	l.lastSweep = now          // a sweep just happened, so the expired entry stays
	if l.allow("b") {
		t.Fatal("sweep within 1s of the last one should not run")
	}
	now = now.Add(sweepMinInterval)
	if !l.allow("b") {
		t.Fatal("after 1s the sweep should free the expired entry")
	}
}
