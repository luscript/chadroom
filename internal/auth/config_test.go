package auth

import (
	"strings"
	"testing"
	"time"
)

const testSecret = "0123456789abcdef0123456789abcdef" // 32 bytes

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func baseEnv() map[string]string {
	return map[string]string{
		"GOOGLE_CLIENT_ID":     "id",
		"GOOGLE_CLIENT_SECRET": "client-secret-value",
		"SESSION_SECRET":       testSecret,
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	cfg, err := LoadConfig(env(baseEnv()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Issuer != GoogleIssuer || cfg.RedirectURL != "http://localhost:8080/auth/callback" ||
		cfg.AllowedDomain != "crombie.dev" || cfg.SessionTTL != 12*time.Hour || cfg.SecureCookies() {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name    string
		set     map[string]string
		wantErr []string // variables the error must name
	}{
		{"all missing", map[string]string{"GOOGLE_CLIENT_ID": "", "GOOGLE_CLIENT_SECRET": "", "SESSION_SECRET": ""},
			[]string{"GOOGLE_CLIENT_ID", "GOOGLE_CLIENT_SECRET", "SESSION_SECRET"}},
		{"blank id", map[string]string{"GOOGLE_CLIENT_ID": "  "}, []string{"GOOGLE_CLIENT_ID"}},
		{"31 byte secret", map[string]string{"SESSION_SECRET": testSecret[:31]}, []string{"SESSION_SECRET"}},
		{"32 byte secret ok", map[string]string{"SESSION_SECRET": testSecret}, nil},
		{"ttl garbage", map[string]string{"SESSION_TTL": "soon"}, []string{"SESSION_TTL"}},
		{"ttl too short", map[string]string{"SESSION_TTL": "4m59s"}, []string{"SESSION_TTL"}},
		{"ttl too long", map[string]string{"SESSION_TTL": "168h1s"}, []string{"SESSION_TTL"}},
		{"ttl min ok", map[string]string{"SESSION_TTL": "5m"}, nil},
		{"ttl max ok", map[string]string{"SESSION_TTL": "168h"}, nil},
		{"redirect wrong path", map[string]string{"OIDC_REDIRECT_URL": "https://x.dev/cb"}, []string{"OIDC_REDIRECT_URL"}},
		{"redirect scheme", map[string]string{"OIDC_REDIRECT_URL": "ftp://x.dev/auth/callback"}, []string{"OIDC_REDIRECT_URL"}},
		{"redirect no host", map[string]string{"OIDC_REDIRECT_URL": "https:///auth/callback"}, []string{"OIDC_REDIRECT_URL"}},
		{"redirect relative", map[string]string{"OIDC_REDIRECT_URL": "not-a-url-secret"}, []string{"OIDC_REDIRECT_URL"}},
		{"redirect query", map[string]string{"OIDC_REDIRECT_URL": "https://x.dev/auth/callback?a=b"}, []string{"OIDC_REDIRECT_URL"}},
		{"redirect fragment", map[string]string{"OIDC_REDIRECT_URL": "https://x.dev/auth/callback#f"}, []string{"OIDC_REDIRECT_URL"}},
		{"redirect https ok", map[string]string{"OIDC_REDIRECT_URL": "https://chat.crombie.dev/auth/callback"}, nil},
		{"domain with at", map[string]string{"ALLOWED_EMAIL_DOMAIN": "@crombie.dev"}, []string{"ALLOWED_EMAIL_DOMAIN"}},
		{"domain non-ascii", map[string]string{"ALLOWED_EMAIL_DOMAIN": "crombié.dev"}, []string{"ALLOWED_EMAIL_DOMAIN"}},
		{"domain inner space", map[string]string{"ALLOWED_EMAIL_DOMAIN": "a b.dev"}, []string{"ALLOWED_EMAIL_DOMAIN"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := baseEnv()
			for k, v := range tt.set {
				m[k] = v
			}
			_, err := LoadConfig(env(m))
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected error")
			}
			for _, name := range tt.wantErr {
				if !strings.Contains(err.Error(), name) {
					t.Errorf("error %q does not name %s", err, name)
				}
			}
			// Values must never leak into the error.
			for _, secret := range []string{"client-secret-value", m["SESSION_SECRET"], m["OIDC_REDIRECT_URL"], m["SESSION_TTL"]} {
				if secret != "" && strings.Contains(err.Error(), secret) {
					t.Errorf("error leaks a value: %q", err)
				}
			}
		})
	}
}

func TestLoadConfigNormalizes(t *testing.T) {
	m := baseEnv()
	m["ALLOWED_EMAIL_DOMAIN"] = " CroMbie.DEV "
	m["OIDC_REDIRECT_URL"] = "https://chat.crombie.dev/auth/callback"
	cfg, err := LoadConfig(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.AllowedDomain != "crombie.dev" {
		t.Errorf("domain = %q", cfg.AllowedDomain)
	}
	if !cfg.SecureCookies() {
		t.Error("https redirect must give Secure cookies")
	}
}
