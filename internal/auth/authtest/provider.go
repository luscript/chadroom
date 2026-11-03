// Package authtest is a fake OpenID Connect provider for tests. It uses only
// the standard library and must not import internal/auth.
package authtest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

const keyID = "authtest-key"

var (
	keyOnce sync.Once
	rsaKey  *rsa.PrivateKey
)

// sharedKey generates the RSA-2048 key once per test binary (it is slow).
func sharedKey() *rsa.PrivateKey {
	keyOnce.Do(func() {
		k, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		rsaKey = k
	})
	return rsaKey
}

type grant struct {
	nonce, challenge, redirectURI string
}

// Provider is a fake OIDC provider on a loopback httptest.Server. Its URL is
// the issuer. The authorize endpoint approves every request immediately.
type Provider struct {
	// URL is the issuer and base URL.
	URL string

	// Claims overrides (or, with a nil value, deletes) claims of the next ID
	// tokens. Set it before starting the flow. Defaults: email ana@crombie.dev,
	// email_verified true, name "Ana Pérez", hd crombie.dev.
	Claims map[string]any

	// Now is the clock for iat/exp; defaults to time.Now.
	Now func() time.Time

	// TokenFail makes the token endpoint answer 400 with a body that echoes the
	// code, to test that callers never log response bodies.
	TokenFail bool

	clientID, clientSecret string
	srv                    *httptest.Server

	mu          sync.Mutex
	grants      map[string]grant
	tokenCalls  int
	lastAuthQry url.Values
}

// NewProvider starts the fake provider and closes it when the test ends.
func NewProvider(t testing.TB, clientID, clientSecret string) *Provider {
	t.Helper()
	p := &Provider{
		Now:          time.Now,
		clientID:     clientID,
		clientSecret: clientSecret,
		grants:       make(map[string]grant),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", p.discovery)
	mux.HandleFunc("/jwks", p.jwks)
	mux.HandleFunc("/authorize", p.authorize)
	mux.HandleFunc("/token", p.token)
	p.srv = httptest.NewServer(mux)
	p.URL = p.srv.URL
	t.Cleanup(p.srv.Close)
	return p
}

// TokenCalls is how many requests reached the token endpoint.
func (p *Provider) TokenCalls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tokenCalls
}

// LastAuthorizeQuery is the query of the latest authorize request (state,
// nonce, code_challenge, hd, ...).
func (p *Provider) LastAuthorizeQuery() url.Values {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastAuthQry
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (p *Provider) discovery(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                p.URL,
		"authorization_endpoint":                p.URL + "/authorize",
		"token_endpoint":                        p.URL + "/token",
		"jwks_uri":                              p.URL + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (p *Provider) jwks(w http.ResponseWriter, r *http.Request) {
	pub := sharedKey().PublicKey
	writeJSON(w, http.StatusOK, map[string]any{"keys": []map[string]string{{
		"kty": "RSA",
		"kid": keyID,
		"alg": "RS256",
		"use": "sig",
		"n":   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}})
}

func (p *Provider) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	redirect, err := url.Parse(q.Get("redirect_uri"))
	if q.Get("client_id") != p.clientID || q.Get("response_type") != "code" ||
		q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" ||
		err != nil || redirect.Scheme == "" || redirect.Host == "" {
		http.Error(w, "invalid_request", http.StatusBadRequest)
		return
	}
	code := rand.Text()
	p.mu.Lock()
	p.lastAuthQry = q
	p.grants[code] = grant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge"), redirectURI: q.Get("redirect_uri")}
	p.mu.Unlock()

	rq := redirect.Query()
	rq.Set("code", code)
	if s := q.Get("state"); s != "" {
		rq.Set("state", s)
	}
	redirect.RawQuery = rq.Encode()
	http.Redirect(w, r, redirect.String(), http.StatusFound)
}

// clientAuthOK accepts the client credentials via HTTP Basic (form-encoded, per
// RFC 6749 2.3.1) or in the POST body.
func (p *Provider) clientAuthOK(r *http.Request) bool {
	id, secret := r.PostFormValue("client_id"), r.PostFormValue("client_secret")
	if u, pw, ok := r.BasicAuth(); ok {
		id, _ = url.QueryUnescape(u)
		secret, _ = url.QueryUnescape(pw)
	}
	return subtle.ConstantTimeCompare([]byte(id), []byte(p.clientID)) == 1 &&
		subtle.ConstantTimeCompare([]byte(secret), []byte(p.clientSecret)) == 1
}

func (p *Provider) token(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	p.tokenCalls++
	p.mu.Unlock()

	if r.Method != http.MethodPost || r.ParseForm() != nil {
		http.Error(w, "invalid_request", http.StatusBadRequest)
		return
	}
	if !p.clientAuthOK(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}
	code := r.PostFormValue("code")
	if p.TokenFail {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant", "error_description": "bad code " + code})
		return
	}

	p.mu.Lock()
	g, known := p.grants[code]
	delete(p.grants, code) // single use, even when the rest of the request is wrong
	p.mu.Unlock()

	sum := sha256.Sum256([]byte(r.PostFormValue("code_verifier")))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if r.PostFormValue("grant_type") != "authorization_code" || !known ||
		r.PostFormValue("redirect_uri") != g.redirectURI ||
		subtle.ConstantTimeCompare([]byte(challenge), []byte(g.challenge)) != 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid_grant"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": "fake-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     p.idToken(g.nonce),
	})
}

// idToken builds an RS256 JWT with the default claims plus p.Claims overrides.
func (p *Provider) idToken(nonce string) string {
	now := p.Now()
	claims := map[string]any{
		"iss":            p.URL,
		"aud":            p.clientID,
		"sub":            "authtest-subject",
		"iat":            now.Unix(),
		"exp":            now.Add(5 * time.Minute).Unix(),
		"nonce":          nonce,
		"email":          "ana@crombie.dev",
		"email_verified": true,
		"name":           "Ana Pérez",
		"hd":             "crombie.dev",
	}
	for k, v := range p.Claims {
		if v == nil {
			delete(claims, k)
		} else {
			claims[k] = v
		}
	}
	enc := base64.RawURLEncoding
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": keyID})
	body, _ := json.Marshal(claims)
	signing := enc.EncodeToString(header) + "." + enc.EncodeToString(body)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, sharedKey(), crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return signing + "." + enc.EncodeToString(sig)
}
