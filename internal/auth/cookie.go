package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"
)

const (
	// Key-derivation labels: one key per cookie purpose, so a flow cookie can
	// never verify as a session cookie (and vice versa) by construction.
	purposeSession = "go-chatroom/session/v1"
	purposeFlow    = "go-chatroom/oidc-flow/v1"

	cookieVersion  = 1    // the "v" field of every payload
	maxCookieValue = 4096 // longer values are rejected before any parsing
	flowTTL        = 10 * time.Minute
	flowPath       = "/auth"
)

var errBadCookie = errors.New("auth: invalid cookie")

var b64 = base64.RawURLEncoding

// sealer signs and verifies cookie values:
//
//	b64url(json(payload)) "." b64url(HMAC-SHA256(key, b64url(json(payload))))
//
// The payload is signed, not encrypted. It is used for the session cookie and
// the flow cookie, each with its own key.
type sealer struct{ key []byte }

// newSealer derives the purpose key from the secret.
func newSealer(secret []byte, purpose string) sealer {
	m := hmac.New(sha256.New, secret)
	m.Write([]byte(purpose))
	return sealer{key: m.Sum(nil)}
}

func (s sealer) mac(payload string) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(payload))
	return m.Sum(nil)
}

func (s sealer) seal(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	payload := b64.EncodeToString(raw)
	value := payload + "." + b64.EncodeToString(s.mac(payload))
	if len(value) > maxCookieValue {
		return "", errors.New("auth: cookie value too large")
	}
	return value, nil
}

// open checks size, shape and MAC (constant time) before it parses any JSON.
// Callers still check the payload's version and expiry.
func (s sealer) open(value string, v any) error {
	if len(value) > maxCookieValue || strings.Count(value, ".") != 1 {
		return errBadCookie
	}
	payload, macPart, _ := strings.Cut(value, ".")
	got, err := b64.DecodeString(macPart)
	if err != nil || !hmac.Equal(got, s.mac(payload)) {
		return errBadCookie
	}
	raw, err := b64.DecodeString(payload)
	if err != nil || json.Unmarshal(raw, v) != nil {
		return errBadCookie
	}
	return nil
}

// Cookie names: __Host-/__Secure- prefixes only make sense (and are only
// accepted by browsers) over https. The flow cookie cannot use __Host- because
// its Path is /auth.
func sessionCookieName(secure bool) string {
	if secure {
		return "__Host-chat_session"
	}
	return "chat_session"
}

func flowCookieName(secure bool) string {
	if secure {
		return "__Secure-chat_oidc"
	}
	return "chat_oidc"
}

// newCookie builds a cookie with the shared attributes: HttpOnly, SameSite=Lax,
// no Domain. maxAge < 0 deletes it (same name, path and flags).
func newCookie(name, value, path string, maxAge int, secure bool) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// newFlowCookie wraps a sealed flow value (Path=/auth, 10 minutes).
func newFlowCookie(value string, secure bool) *http.Cookie {
	return newCookie(flowCookieName(secure), value, flowPath, int(flowTTL/time.Second), secure)
}

// clearFlowCookie deletes the flow cookie; the callback sends it on every outcome.
func clearFlowCookie(secure bool) *http.Cookie {
	return newCookie(flowCookieName(secure), "", flowPath, -1, secure)
}
