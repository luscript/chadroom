// Package auth owns identity: env config, signed cookies, the Require
// middleware, the per-IP rate limiter and (oidc.go) the Google OIDC login flow.
package auth

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// GoogleIssuer is the OIDC issuer LoadConfig sets; tests override Config.Issuer.
const GoogleIssuer = "https://accounts.google.com"

const (
	defaultRedirectURL = "http://localhost:8080/auth/callback"
	defaultDomain      = "crombie.dev"
	defaultSessionTTL  = 12 * time.Hour
	minSessionTTL      = 5 * time.Minute
	maxSessionTTL      = 168 * time.Hour
	minSecretLen       = 32
	callbackPath       = "/auth/callback"
)

// Config is the validated authentication configuration.
type Config struct {
	Issuer        string        // GoogleIssuer from LoadConfig; tests use a fake provider URL
	ClientID      string        // GOOGLE_CLIENT_ID (required)
	ClientSecret  string        // GOOGLE_CLIENT_SECRET (required)
	RedirectURL   string        // OIDC_REDIRECT_URL
	AllowedDomain string        // ALLOWED_EMAIL_DOMAIN, stored lowercase
	SessionSecret []byte        // SESSION_SECRET (required, >= 32 bytes)
	SessionTTL    time.Duration // SESSION_TTL, 5m <= ttl <= 168h
}

// LoadConfig reads and validates the environment. The returned error joins one
// problem per variable and names variables only, never their values.
func LoadConfig(getenv func(string) string) (Config, error) {
	cfg := Config{
		Issuer:        GoogleIssuer,
		ClientID:      strings.TrimSpace(getenv("GOOGLE_CLIENT_ID")),
		ClientSecret:  strings.TrimSpace(getenv("GOOGLE_CLIENT_SECRET")),
		RedirectURL:   strings.TrimSpace(getenv("OIDC_REDIRECT_URL")),
		AllowedDomain: strings.TrimSpace(getenv("ALLOWED_EMAIL_DOMAIN")),
		SessionSecret: []byte(getenv("SESSION_SECRET")),
		SessionTTL:    defaultSessionTTL,
	}
	if cfg.RedirectURL == "" {
		cfg.RedirectURL = defaultRedirectURL
	}
	if cfg.AllowedDomain == "" {
		cfg.AllowedDomain = defaultDomain
	}
	if raw := strings.TrimSpace(getenv("SESSION_TTL")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			d = 0 // out of range, so validate() reports SESSION_TTL without echoing the value
		}
		cfg.SessionTTL = d
	}
	err := cfg.validate()
	cfg.AllowedDomain = strings.ToLower(cfg.AllowedDomain)
	return cfg, err
}

// SecureCookies reports whether cookies get the Secure flag: exactly when the
// redirect URL is https. The request scheme is never consulted (SRV-05).
func (c Config) SecureCookies() bool {
	u, err := url.Parse(c.RedirectURL)
	return err == nil && u.Scheme == "https"
}

// validate checks every rule and joins the problems. New calls it again so a
// hand-built Config cannot skip it.
func (c Config) validate() error {
	var errs []error
	bad := func(name, why string) { errs = append(errs, fmt.Errorf("%s: %s", name, why)) }

	if c.ClientID == "" {
		bad("GOOGLE_CLIENT_ID", "required")
	}
	if c.ClientSecret == "" {
		bad("GOOGLE_CLIENT_SECRET", "required")
	}
	if len(c.SessionSecret) < minSecretLen {
		bad("SESSION_SECRET", fmt.Sprintf("required, at least %d bytes", minSecretLen))
	}
	if !validRedirectURL(c.RedirectURL) {
		bad("OIDC_REDIRECT_URL", "must be an absolute http or https URL with a host and path "+callbackPath)
	}
	if !validDomain(c.AllowedDomain) {
		bad("ALLOWED_EMAIL_DOMAIN", "must be a bare ASCII domain without '@' or spaces")
	}
	if c.SessionTTL < minSessionTTL || c.SessionTTL > maxSessionTTL {
		bad("SESSION_TTL", "must be between 5m and 168h")
	}
	return errors.Join(errs...)
}

func validRedirectURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return false
	}
	return u.Path == callbackPath && u.RawQuery == "" && !u.ForceQuery && u.Fragment == ""
}

func validDomain(d string) bool {
	if d == "" {
		return false
	}
	for i := 0; i < len(d); i++ {
		if b := d[i]; b <= ' ' || b >= 0x7f || b == '@' {
			return false
		}
	}
	return true
}
