package auth

import (
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	loginLimit   = 20 // requests per IP per window, shared by /auth/login and /auth/callback
	loginWindow  = time.Minute
	maxTrackedIP = 4096
)

type windowCount struct {
	start time.Time
	n     int
}

// rateLimiter is a fixed-window per-key counter with bounded memory. The mutex
// is fine here: this is not the chat hub.
type rateLimiter struct {
	limit  int
	window time.Duration
	max    int
	now    func() time.Time

	mu        sync.Mutex
	hits      map[string]*windowCount
	lastSweep time.Time
}

func newRateLimiter(limit int, window time.Duration, max int) *rateLimiter {
	return &rateLimiter{limit: limit, window: window, max: max, now: time.Now, hits: make(map[string]*windowCount)}
}

// allow counts one request for key. It fails closed: when the table is full of
// live windows, unknown keys are refused.
func (l *rateLimiter) allow(key string) bool {
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()

	if now.Sub(l.lastSweep) >= l.window {
		l.sweep(now)
	}
	w, ok := l.hits[key]
	if !ok {
		if len(l.hits) >= l.max {
			l.sweep(now)
			if len(l.hits) >= l.max {
				return false
			}
		}
		l.hits[key] = &windowCount{start: now, n: 1}
		return true
	}
	if now.Sub(w.start) >= l.window {
		w.start, w.n = now, 1
		return true
	}
	w.n++
	return w.n <= l.limit
}

// sweep drops entries whose window has ended (idle IPs).
func (l *rateLimiter) sweep(now time.Time) {
	l.lastSweep = now
	for k, w := range l.hits {
		if now.Sub(w.start) >= l.window {
			delete(l.hits, k)
		}
	}
}

// middleware keys on the remote address: proxy headers are not trusted
// (SRV-05), so X-Forwarded-For is deliberately ignored whatever the engine's
// trusted-proxy setting is.
func (l *rateLimiter) middleware() gin.HandlerFunc {
	retryAfter := strconv.Itoa(int(l.window / time.Second))
	return func(c *gin.Context) {
		ip, _, err := net.SplitHostPort(c.Request.RemoteAddr)
		if err != nil {
			ip = c.Request.RemoteAddr
		}
		if !l.allow(ip) {
			c.Header("Retry-After", retryAfter)
			c.Header("Cache-Control", "no-store")
			c.String(http.StatusTooManyRequests, "Demasiados intentos. Probá de nuevo en un minuto.")
			c.Abort()
			return
		}
		c.Next()
	}
}
