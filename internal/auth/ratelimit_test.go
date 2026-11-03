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
