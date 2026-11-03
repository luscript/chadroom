package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/gin-gonic/gin"
	"golang.org/x/oauth2"
)

const (
	maxNextLen      = 512
	maxDisplayName  = 64 // runes; the chat package normalizes further
	clientTimeout   = 10 * time.Second
	badRequestText  = "Solicitud de inicio de sesión inválida o vencida. Volvé a entrar desde el inicio."
	forbiddenText   = "Acceso denegado."
	logoutBadOrigin = "Solicitud no permitida."
)

// oauthErrorCode is the only shape of provider-supplied text that may be logged.
var oauthErrorCode = regexp.MustCompile(`^[a-z_]{1,64}$`)

// flowState is the payload of the flow cookie.
type flowState struct {
	V        int    `json:"v"`
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
	Next     string `json:"next"`
	Exp      int64  `json:"exp"` // unix seconds
}

// Handler runs the Google OIDC login flow and logout. Build it with New.
type Handler struct {
	cfg      Config
	sessions *Sessions
	oauth    oauth2.Config
	verifier *oidc.IDTokenVerifier
	flow     sealer
	limiter  *rateLimiter
	client   *http.Client
	denied   gin.HandlerFunc
	secure   bool
	now      func() time.Time
}

// New validates cfg, runs OIDC discovery against cfg.Issuer and builds the
// Sessions. denied renders the 403 page: the status is already set to 403 when
// it runs (nil: plain-text 403).
func New(ctx context.Context, cfg Config, denied gin.HandlerFunc) (*Handler, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.AllowedDomain = strings.ToLower(cfg.AllowedDomain)
	client := &http.Client{Timeout: clientTimeout}
	// The JWKS fetches reuse this context later, so it must outlive startup.
	dctx := oidc.ClientContext(context.WithoutCancel(ctx), client)
	provider, err := oidc.NewProvider(dctx, cfg.Issuer)
	if err != nil {
		return nil, fmt.Errorf("auth: oidc discovery: %w", err)
	}
	endpoint := provider.Endpoint()
	endpoint.AuthStyle = oauth2.AuthStyleInHeader // one token request, no auto-detect retry

	secure := cfg.SecureCookies()
	h := &Handler{
		cfg:      cfg,
		sessions: NewSessions(cfg.SessionSecret, cfg.SessionTTL, secure),
		oauth: oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			Endpoint:     endpoint,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		flow:    newSealer(cfg.SessionSecret, purposeFlow),
		limiter: newRateLimiter(loginLimit, loginWindow, maxTrackedIP),
		client:  client,
		denied:  denied,
		secure:  secure,
		now:     time.Now,
	}
	h.verifier = provider.Verifier(&oidc.Config{
		ClientID: cfg.ClientID,
		Now:      func() time.Time { return h.now() }, // late-bound so tests can move the clock
	})
	return h, nil
}

// Sessions is the session store the router uses for Require.
func (h *Handler) Sessions() *Sessions { return h.sessions }

// Register mounts the auth routes on g (the "/auth" group).
func (h *Handler) Register(g *gin.RouterGroup) {
	limit := h.limiter.middleware()
	g.GET("/login", limit, h.login)
	g.GET("/callback", limit, h.callback)
	g.POST("/logout", h.logout)
}

func (h *Handler) login(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	state, nonce, verifier := rand.Text(), rand.Text(), oauth2.GenerateVerifier() // >= 128 bits each
	value, err := h.flow.seal(flowState{
		V:        cookieVersion,
		State:    state,
		Nonce:    nonce,
		Verifier: verifier,
		Next:     safeNext(c.Query("next")),
		Exp:      h.now().Add(flowTTL).Unix(),
	})
	if err != nil {
		log.Print("auth: login failed: flow cookie")
		c.String(http.StatusInternalServerError, "Error interno.")
		return
	}
	http.SetCookie(c.Writer, newFlowCookie(value, h.secure))
	// hd is only a hint for Google's account picker; the callback re-checks it.
	authURL := h.oauth.AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier),
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("hd", h.cfg.AllowedDomain))
	c.Redirect(http.StatusFound, authURL)
}

// reject answers any failure before authorization: one generic 400, no session.
func (h *Handler) reject(c *gin.Context, reason string) {
	log.Print("auth: callback rejected: " + reason)
	c.String(http.StatusBadRequest, badRequestText)
}

// deny answers an authorization failure with the 403 page.
func (h *Handler) deny(c *gin.Context, reason string) {
	log.Print("auth: callback rejected: " + reason)
	c.Status(http.StatusForbidden)
	if h.denied != nil {
		h.denied(c)
	} else {
		c.String(http.StatusForbidden, forbiddenText)
	}
	c.Abort()
}

func (h *Handler) callback(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	http.SetCookie(c.Writer, clearFlowCookie(h.secure)) // every outcome

	fc, err := c.Request.Cookie(flowCookieName(h.secure))
	var fs flowState
	if err != nil || h.flow.open(fc.Value, &fs) != nil || fs.V != cookieVersion || !h.now().Before(time.Unix(fs.Exp, 0)) {
		h.reject(c, "flow")
		return
	}
	q := c.Request.URL.Query()
	if got := q.Get("state"); got == "" || fs.State == "" || subtle.ConstantTimeCompare([]byte(got), []byte(fs.State)) != 1 {
		h.reject(c, "state")
		return
	}
	if e := q.Get("error"); e != "" {
		if oauthErrorCode.MatchString(e) {
			h.reject(c, "provider_error code="+e)
		} else {
			h.reject(c, "provider_error")
		}
		return
	}
	code := q.Get("code")
	if code == "" {
		h.reject(c, "code")
		return
	}

	ctx := context.WithValue(c.Request.Context(), oauth2.HTTPClient, h.client)
	token, err := h.oauth.Exchange(ctx, code, oauth2.VerifierOption(fs.Verifier))
	if err != nil {
		// err.Error() may embed the response body: log fixed fields only.
		var re *oauth2.RetrieveError
		if errors.As(err, &re) && re.Response != nil {
			reason := "exchange status=" + strconv.Itoa(re.Response.StatusCode)
			if oauthErrorCode.MatchString(re.ErrorCode) {
				reason += " code=" + re.ErrorCode
			}
			h.reject(c, reason)
		} else {
			h.reject(c, "exchange transport")
		}
		return
	}
	rawID, _ := token.Extra("id_token").(string) // the access/refresh tokens are dropped here
	if rawID == "" {
		h.reject(c, "no_id_token")
		return
	}
	idToken, err := h.verifier.Verify(ctx, rawID)
	if err != nil {
		h.reject(c, "verify")
		return
	}
	if subtle.ConstantTimeCompare([]byte(idToken.Nonce), []byte(fs.Nonce)) != 1 || fs.Nonce == "" {
		h.reject(c, "nonce")
		return
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified any    `json:"email_verified"`
		Name          string `json:"name"`
		HD            string `json:"hd"`
	}
	if idToken.Claims(&claims) != nil {
		h.reject(c, "claims")
		return
	}

	// Authorization (SRV-12), only after full cryptographic validation.
	if verified, _ := claims.EmailVerified.(bool); !verified {
		h.deny(c, "unverified_email")
		return
	}
	if !emailInDomain(claims.Email, h.cfg.AllowedDomain) {
		domain := ""
		if i := strings.LastIndexByte(claims.Email, '@'); i >= 0 {
			domain = claims.Email[i+1:]
			if len(domain) > 64 {
				domain = domain[:64]
			}
		}
		h.deny(c, "domain="+strconv.Quote(domain))
		return
	}
	if !asciiEqualFold(claims.HD, h.cfg.AllowedDomain) {
		h.deny(c, "hd")
		return
	}

	cookie, err := h.sessions.Cookie(Identity{Email: strings.ToLower(claims.Email), Name: displayName(claims.Name)})
	if err != nil {
		log.Print("auth: callback failed: session")
		c.String(http.StatusInternalServerError, "Error interno.")
		return
	}
	http.SetCookie(c.Writer, cookie)
	c.Redirect(http.StatusFound, safeNext(fs.Next))
}

// displayName trims the name claim and caps it at maxDisplayName runes. An
// empty result is left for the chat layer's nickname fallback.
func displayName(name string) string {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > maxDisplayName {
		name = string([]rune(name)[:maxDisplayName])
	}
	return name
}

func (h *Handler) logout(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !sameOrigin(c.Request) {
		c.String(http.StatusForbidden, logoutBadOrigin)
		return
	}
	if sess, ok := h.sessions.verify(c.Request); ok {
		h.sessions.revoke(sess.Sid, time.Unix(sess.Exp, 0))
	}
	http.SetCookie(c.Writer, h.sessions.clearCookie())
	c.Status(http.StatusNoContent)
}

// sameOrigin is true when there is no Origin header or its host is the
// request's host. "null" and unparsable values are refused.
func sameOrigin(r *http.Request) bool {
	vals := r.Header.Values("Origin")
	if len(vals) == 0 {
		return true
	}
	if len(vals) > 1 || vals[0] == "null" {
		return false
	}
	u, err := url.Parse(vals[0])
	return err == nil && u.Host != "" && asciiEqualFold(u.Host, r.Host)
}

// safeNext returns s only when it is a same-site relative path that cannot
// loop through /auth; otherwise "/".
func safeNext(s string) string {
	if len(s) < 1 || len(s) > maxNextLen || s[0] != '/' {
		return "/"
	}
	if len(s) > 1 && (s[1] == '/' || s[1] == '\\') {
		return "/"
	}
	for i := 0; i < len(s); i++ {
		if b := s[i]; b <= ' ' || b == 0x7f || b == '\\' {
			return "/"
		}
	}
	u, err := url.Parse(s)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil {
		return "/"
	}
	if u.Path == "/auth" || strings.HasPrefix(u.Path, "/auth/") {
		return "/"
	}
	return s
}

// emailInDomain requires a non-empty local part and whole-string equality of
// the part after the last '@' with domain (ASCII case-insensitive).
func emailInDomain(email, domain string) bool {
	i := strings.LastIndexByte(email, '@')
	return i > 0 && asciiEqualFold(email[i+1:], domain)
}

// asciiEqualFold folds only A-Z, so Unicode look-alikes never match.
func asciiEqualFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		x, y := a[i], b[i]
		if 'A' <= x && x <= 'Z' {
			x += 'a' - 'A'
		}
		if 'A' <= y && y <= 'Z' {
			y += 'a' - 'A'
		}
		if x != y {
			return false
		}
	}
	return true
}
