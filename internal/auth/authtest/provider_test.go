package authtest

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const verifier = "a-pkce-verifier-with-enough-length-0123456789"

func challengeOf(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// authorize drives /authorize without following the redirect and returns the code.
func authorize(t *testing.T, p *Provider, nonce, challenge string) string {
	t.Helper()
	q := url.Values{
		"client_id": {"cid"}, "response_type": {"code"}, "redirect_uri": {"http://app.test/auth/callback"},
		"state": {"st"}, "nonce": {nonce}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Get(p.URL + "/authorize?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	loc, err := url.Parse(res.Header.Get("Location"))
	if res.StatusCode != 302 || err != nil || loc.Host != "app.test" || loc.Query().Get("state") != "st" {
		t.Fatalf("authorize: %d %q", res.StatusCode, res.Header.Get("Location"))
	}
	return loc.Query().Get("code")
}

func exchange(t *testing.T, p *Provider, code, ver string, basic bool, secret string) (int, map[string]any) {
	t.Helper()
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "code_verifier": {ver},
		"redirect_uri": {"http://app.test/auth/callback"}}
	req, _ := http.NewRequest("POST", p.URL+"/token", nil)
	if basic {
		req.SetBasicAuth("cid", secret)
	} else {
		form.Set("client_id", "cid")
		form.Set("client_secret", secret)
	}
	req.Body = io.NopCloser(strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(res.Body).Decode(&out)
	return res.StatusCode, out
}

func decodeJWT(t *testing.T, tok string) map[string]any {
	t.Helper()
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", tok)
	}
	enc := base64.RawURLEncoding
	raw, _ := enc.DecodeString(parts[1])
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

func TestProviderFlow(t *testing.T) {
	p := NewProvider(t, "cid", "csecret")

	// JWKS verifies the token signature.
	var jwks struct{ Keys []struct{ N, E string } }
	res, err := http.Get(p.URL + "/jwks")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewDecoder(res.Body).Decode(&jwks); err != nil || len(jwks.Keys) != 1 {
		t.Fatalf("jwks: %v", err)
	}
	res.Body.Close()
	nb, _ := base64.RawURLEncoding.DecodeString(jwks.Keys[0].N)
	eb, _ := base64.RawURLEncoding.DecodeString(jwks.Keys[0].E)
	pub := &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: int(new(big.Int).SetBytes(eb).Int64())}

	code := authorize(t, p, "n1", challengeOf(verifier))
	status, body := exchange(t, p, code, verifier, true, "csecret")
	if status != 200 {
		t.Fatalf("exchange: %d %v", status, body)
	}
	idt, _ := body["id_token"].(string)
	parts := strings.Split(idt, ".")
	sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, sum[:], sig); err != nil {
		t.Fatalf("signature: %v", err)
	}
	claims := decodeJWT(t, idt)
	want := map[string]any{"iss": p.URL, "aud": "cid", "nonce": "n1", "email": "ana@crombie.dev",
		"email_verified": true, "name": "Ana Pérez", "hd": "crombie.dev"}
	for k, v := range want {
		if claims[k] != v {
			t.Errorf("claim %s = %v, want %v", k, claims[k], v)
		}
	}
	if status, _ := exchange(t, p, code, verifier, true, "csecret"); status != 400 {
		t.Errorf("code reuse: %d", status)
	}
	if p.LastAuthorizeQuery().Get("nonce") != "n1" || p.TokenCalls() != 2 {
		t.Errorf("recording: %v calls=%d", p.LastAuthorizeQuery(), p.TokenCalls())
	}
}

func TestProviderRejects(t *testing.T) {
	p := NewProvider(t, "cid", "csecret")

	code := authorize(t, p, "n", challengeOf(verifier))
	if status, _ := exchange(t, p, code, "wrong-verifier", false, "csecret"); status != 400 {
		t.Errorf("wrong verifier: %d", status)
	}
	if status, _ := exchange(t, p, code, verifier, false, "csecret"); status != 400 {
		t.Errorf("code must be spent after a failed attempt: %d", status)
	}
	code = authorize(t, p, "n", challengeOf(verifier))
	if status, _ := exchange(t, p, code, verifier, false, "nope"); status != 401 {
		t.Errorf("wrong secret: %d", status)
	}
	if status, _ := exchange(t, p, code, verifier, false, "csecret"); status != 200 {
		t.Errorf("post credentials: %d", status)
	}
	if status, _ := exchange(t, p, "unknown", verifier, true, "csecret"); status != 400 {
		t.Errorf("unknown code: %d", status)
	}
	p.TokenFail = true
	code = authorize(t, p, "n", challengeOf(verifier))
	if status, body := exchange(t, p, code, verifier, true, "csecret"); status != 400 || !strings.Contains(body["error_description"].(string), code) {
		t.Errorf("TokenFail: %d %v", status, body)
	}
}

func TestProviderClaimOverrides(t *testing.T) {
	p := NewProvider(t, "cid", "csecret")
	p.Claims = map[string]any{"email": "eve@gmail.com", "hd": nil, "nonce": "x"}
	code := authorize(t, p, "n", challengeOf(verifier))
	_, body := exchange(t, p, code, verifier, true, "csecret")
	claims := decodeJWT(t, body["id_token"].(string))
	if claims["email"] != "eve@gmail.com" || claims["nonce"] != "x" {
		t.Errorf("override: %v", claims)
	}
	if _, has := claims["hd"]; has {
		t.Error("nil override must delete the claim")
	}
	if claims["email_verified"] != true {
		t.Errorf("defaults lost: %v", claims)
	}
}

func TestAuthorizeValidates(t *testing.T) {
	p := NewProvider(t, "cid", "csecret")
	for name, q := range map[string]string{
		"wrong client": "client_id=other&response_type=code&redirect_uri=http://a.test/cb&code_challenge=x&code_challenge_method=S256",
		"no pkce":      "client_id=cid&response_type=code&redirect_uri=http://a.test/cb",
		"plain pkce":   "client_id=cid&response_type=code&redirect_uri=http://a.test/cb&code_challenge=x&code_challenge_method=plain",
	} {
		res, err := http.Get(p.URL + "/authorize?" + q)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Errorf("%s: %d", name, res.StatusCode)
		}
	}
}
