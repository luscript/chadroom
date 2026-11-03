package auth

import (
	"context"
	"crypto/rand"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// Identity is the authenticated user. Name is already the display name.
type Identity struct{ Email, Name string }

type identityKey struct{}

// IdentityFrom returns the identity Require stored in the request context.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityKey{}).(Identity)
	return id, ok
}

// Mode picks how Require answers a missing or invalid session.
type Mode int

const (
	Page Mode = iota // failure: 302 /auth/login?next=<request path>
	API              // failure: 401 {"error":"unauthorized"}
)

// session is the payload of the session cookie. It carries no OAuth tokens.
type session struct {
	V     int    `json:"v"`
	Sid   string `json:"sid"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Exp   int64  `json:"exp"` // unix seconds
}

// Sessions mints and verifies session cookies and keeps the revocation set.
// Revocation is in memory: it is lost on restart (accepted, SRV-14).
type Sessions struct {
	sealer sealer
	ttl    time.Duration
	secure bool
	now    func() time.Time

	mu      sync.Mutex // not the chat hub: a plain mutex is fine here
	revoked map[string]time.Time
}

// NewSessions derives the session key from secret. secure selects the cookie
// name and flag (see Config.SecureCookies).
func NewSessions(secret []byte, ttl time.Duration, secure bool) *Sessions {
	return &Sessions{
		sealer:  newSealer(secret, purposeSession),
		ttl:     ttl,
		secure:  secure,
		now:     time.Now,
		revoked: make(map[string]time.Time),
	}
}

// Cookie mints a session cookie for id with a fresh random session id.
func (s *Sessions) Cookie(id Identity) (*http.Cookie, error) {
	value, err := s.sealer.seal(session{
		V:     cookieVersion,
		Sid:   rand.Text(),
		Email: id.Email,
		Name:  id.Name,
		Exp:   s.now().Add(s.ttl).Unix(),
	})
	if err != nil {
		return nil, err
	}
	return newCookie(sessionCookieName(s.secure), value, "/", int(s.ttl/time.Second), s.secure), nil
}

// clearCookie is the deletion cookie (same name, path and flags), for logout.
func (s *Sessions) clearCookie() *http.Cookie {
	return newCookie(sessionCookieName(s.secure), "", "/", -1, s.secure)
}

// verify returns the session carried by the request's cookie, or false when it
// is missing, malformed, has a bad MAC, wrong version, is expired or revoked.
func (s *Sessions) verify(r *http.Request) (session, bool) {
	c, err := r.Cookie(sessionCookieName(s.secure))
	if err != nil {
		return session{}, false
	}
	var sess session
	if s.sealer.open(c.Value, &sess) != nil || sess.V != cookieVersion || sess.Email == "" || sess.Sid == "" {
		return session{}, false
	}
	now := s.now()
	if !now.Before(time.Unix(sess.Exp, 0)) {
		return session{}, false
	}
	s.mu.Lock()
	_, gone := s.revoked[sess.Sid]
	s.mu.Unlock()
	return sess, !gone
}

// revoke blocks sid until exp (when the cookie would have expired anyway) and
// drops entries that are already past their expiry.
func (s *Sessions) revoke(sid string, exp time.Time) {
	now := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.revoked {
		if !now.Before(e) {
			delete(s.revoked, k)
		}
	}
	if now.Before(exp) {
		s.revoked[sid] = exp
	}
}

// Require rejects requests without a valid session and otherwise stores the
// Identity in the request context. Identity comes only from the cookie.
func (s *Sessions) Require(mode Mode) gin.HandlerFunc {
	return func(c *gin.Context) {
		sess, ok := s.verify(c.Request)
		if !ok {
			c.Header("Cache-Control", "no-store")
			if mode == Page {
				// Only the path travels in next; /auth/login validates it again.
				c.Redirect(http.StatusFound, "/auth/login?next="+url.QueryEscape(c.Request.URL.Path))
			} else {
				c.Data(http.StatusUnauthorized, "application/json; charset=utf-8", []byte(`{"error":"unauthorized"}`))
			}
			c.Abort()
			return
		}
		ctx := context.WithValue(c.Request.Context(), identityKey{}, Identity{Email: sess.Email, Name: sess.Name})
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
