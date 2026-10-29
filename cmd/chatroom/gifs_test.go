package main

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

const testKey = "K"

type upstream struct {
	*httptest.Server
	mu   sync.Mutex
	reqs []*http.Request
}

func (u *upstream) requests() []*http.Request {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]*http.Request(nil), u.reqs...)
}

// fakeUpstream starts a fake upstream that records every request before calling h.
func fakeUpstream(t *testing.T, h http.HandlerFunc) *upstream {
	t.Helper()
	u := &upstream{}
	u.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.reqs = append(u.reqs, r)
		u.mu.Unlock()
		h(w, r)
	}))
	t.Cleanup(u.Close)
	return u
}

func newGifTestServer(t *testing.T, g *gifSearch) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(newRouter(newChatServer(nil), g, nil))
	t.Cleanup(ts.Close)
	return ts
}

func giphyJSON(items ...[2]string) string {
	var parts []string
	for _, it := range items {
		parts = append(parts, fmt.Sprintf(
			`{"images":{"fixed_height":{"url":%q},"fixed_width_small":{"url":%q}}}`, it[0], it[1]))
	}
	return `{"data":[` + strings.Join(parts, ",") + `]}`
}

func okJSON(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }
}

func requireJSONHeaders(t *testing.T, hdr http.Header) {
	t.Helper()
	for k, want := range map[string]string{
		"Content-Type":           "application/json; charset=utf-8",
		"Cache-Control":          "no-cache",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := hdr.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func getGifs(t *testing.T, ts *httptest.Server, query string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Get(ts.URL + "/gifs" + query)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b), resp.Header
}

// setup wires a fake upstream, a gifSearch pointed at it and a test server.
func setup(t *testing.T, h http.HandlerFunc) (*upstream, *gifSearch, *httptest.Server) {
	t.Helper()
	up := fakeUpstream(t, h)
	g := newGifSearch(up.URL, testKey)
	return up, g, newGifTestServer(t, g)
}

const (
	gifA  = "https://media2.giphy.com/media/x/200.gif"
	gifAP = "https://media2.giphy.com/media/x/100.gif"
)

func TestGifsDisabledWithoutKey(t *testing.T) {
	ts := newTestServer(t)
	for _, q := range []string{"", "?q=x"} {
		if code, _, _ := getGifs(t, ts, q); code != http.StatusNotFound {
			t.Errorf("GET /gifs%s = %d, want 404", q, code)
		}
	}
}

func TestGifsTrending(t *testing.T) {
	for _, q := range []string{"", "?q=", "?q=%20%20%20"} {
		t.Run(q, func(t *testing.T) {
			up, _, ts := setup(t, okJSON(giphyJSON([2]string{gifA, gifAP})))
			code, body, hdr := getGifs(t, ts, q)
			if code != http.StatusOK {
				t.Fatalf("status = %d", code)
			}
			if want := `{"gifs":[{"url":"` + gifA + `","preview":"` + gifAP + `"}]}`; body != want {
				t.Errorf("body = %s, want %s", body, want)
			}
			requireJSONHeaders(t, hdr)
			reqs := up.requests()
			if len(reqs) != 1 {
				t.Fatalf("upstream calls = %d, want 1", len(reqs))
			}
			r := reqs[0]
			if r.URL.Path != "/v1/gifs/trending" {
				t.Errorf("path = %q", r.URL.Path)
			}
			got := r.URL.Query()
			if got.Get("api_key") != testKey || got.Get("limit") != "24" || got.Get("rating") != "pg-13" {
				t.Errorf("query = %v", got)
			}
			if got.Has("q") || got.Has("lang") {
				t.Errorf("trending query has q or lang: %v", got)
			}
		})
	}
}

func TestGifsSearchParams(t *testing.T) {
	up, _, ts := setup(t, okJSON(giphyJSON()))
	if code, _, _ := getGifs(t, ts, "?q=%20Cat%20&limit=99&rating=r&api_key=x"); code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	reqs := up.requests()
	if len(reqs) != 1 {
		t.Fatalf("upstream calls = %d, want 1", len(reqs))
	}
	if reqs[0].URL.Path != "/v1/gifs/search" {
		t.Errorf("path = %q", reqs[0].URL.Path)
	}
	want := url.Values{"q": {"cat"}, "lang": {"es"}, "limit": {"24"}, "rating": {"pg-13"}, "api_key": {testKey}}
	if got := reqs[0].URL.Query(); got.Encode() != want.Encode() {
		t.Errorf("query = %v, want %v", got, want)
	}
}

func TestGifsQueryTooLong(t *testing.T) {
	up, _, ts := setup(t, okJSON(giphyJSON()))
	if code, _, _ := getGifs(t, ts, "?q="+url.QueryEscape(strings.Repeat("ñ", 50))); code != http.StatusOK {
		t.Errorf("50 runes: status = %d, want 200", code)
	}
	before := len(up.requests())
	code, body, hdr := getGifs(t, ts, "?q="+url.QueryEscape(strings.Repeat("ñ", 51)))
	requireJSONHeaders(t, hdr)
	if code != http.StatusBadRequest {
		t.Errorf("51 runes: status = %d, want 400", code)
	}
	if want := `{"error":"query_too_long"}`; body != want {
		t.Errorf("body = %s, want %s", body, want)
	}
	if n := len(up.requests()); n != before {
		t.Errorf("upstream called for a too-long query (%d -> %d)", before, n)
	}
}

func TestGifsUpstreamError(t *testing.T) {
	tests := []struct {
		name string
		h    http.HandlerFunc
	}{
		{"status 500", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }},
		{"not json", okJSON("not json")},
		{"over 1 MiB", func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"data":[],"pad":"`+strings.Repeat("a", maxGiphyBody)+`"}`)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, ts := setup(t, tt.h)
			code, body, hdr := getGifs(t, ts, "")
			requireJSONHeaders(t, hdr)
			if code != http.StatusBadGateway {
				t.Errorf("status = %d, want 502", code)
			}
			if want := `{"error":"unavailable"}`; body != want {
				t.Errorf("body = %s, want %s", body, want)
			}
		})
	}
}

func TestGifsUpstreamRedirectNotFollowed(t *testing.T) {
	other := fakeUpstream(t, okJSON(giphyJSON([2]string{gifA, gifAP})))
	_, _, ts := setup(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/x", http.StatusFound)
	})
	if code, _, _ := getGifs(t, ts, ""); code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", code)
	}
	if n := len(other.requests()); n != 0 {
		t.Errorf("redirect target contacted %d times, want 0", n)
	}
}

func TestGifsUpstreamTimeout(t *testing.T) {
	release := make(chan struct{})
	_, g, ts := setup(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	t.Cleanup(func() { close(release) })
	g.client = &http.Client{Timeout: 50 * time.Millisecond}
	if code, _, _ := getGifs(t, ts, ""); code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", code)
	}
}

func TestGifsKeyNeverLeaks(t *testing.T) {
	const secret = "SECRET"
	var buf bytes.Buffer
	var logMu sync.Mutex
	log.SetOutput(lockedWriter{&logMu, &buf})
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	cases := map[string]struct {
		base   string
		h      http.HandlerFunc
		status int
	}{
		"transport error": {deadURL, nil, http.StatusBadGateway},
		"status 500":      {"", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }, http.StatusBadGateway},
		"key in item url": {"", okJSON(giphyJSON([2]string{gifA + "?api_key=" + secret, gifAP + "?api_key=" + secret})), http.StatusOK},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var g *gifSearch
			if tc.h == nil {
				g = newGifSearch(tc.base, secret)
			} else {
				up := fakeUpstream(t, tc.h)
				g = newGifSearch(up.URL, secret)
			}
			ts := newGifTestServer(t, g)
			code, body, hdr := getGifs(t, ts, "?q=x")
			if code != tc.status {
				t.Errorf("status = %d, want %d", code, tc.status)
			}
			if tc.status == http.StatusOK {
				if want := `{"gifs":[{"url":"` + gifA + `","preview":"` + gifAP + `"}]}`; body != want {
					t.Errorf("body = %s, want %s", body, want)
				}
			}
			if strings.Contains(body, secret) {
				t.Errorf("response body contains the key: %s", body)
			}
			for k, v := range hdr {
				if strings.Contains(strings.Join(v, ","), secret) {
					t.Errorf("header %s contains the key", k)
				}
			}
		})
	}
	logMu.Lock()
	defer logMu.Unlock()
	if buf.Len() == 0 {
		t.Fatal("log is empty: the leak scan proved nothing")
	}
	if strings.Contains(buf.String(), secret) {
		t.Errorf("log contains the key: %s", buf.String())
	}
}

type lockedWriter struct {
	mu *sync.Mutex
	w  io.Writer
}

func (l lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

func TestGifsFiltersAndCanonicalizes(t *testing.T) {
	h := okJSON(giphyJSON(
		[2]string{"https://media2.giphy.com/media/x/200.gif?cid=1&rid=2#f", "https://media2.giphy.com/media/x/100.gif?cid=1"},
		[2]string{"https://media1.giphy.com/media/y/200.gif", "http://media1.giphy.com/media/y/100.gif"},
		[2]string{"http://media.giphy.com/a.gif", gifAP},
		[2]string{"https://evil.com/a.gif", gifAP},
		[2]string{"https://media.giphy.com/a.mp4", gifAP},
		[2]string{"https://user@media.giphy.com/a.gif", gifAP},
		[2]string{"https://media.giphy.com:443/a.gif", gifAP},
	))
	_, _, ts := setup(t, h)
	_, body, _ := getGifs(t, ts, "")
	want := `{"gifs":[` +
		`{"url":"https://media2.giphy.com/media/x/200.gif","preview":"https://media2.giphy.com/media/x/100.gif"},` +
		`{"url":"https://media1.giphy.com/media/y/200.gif","preview":"https://media1.giphy.com/media/y/200.gif"}]}`
	if body != want {
		t.Errorf("body = %s\nwant   %s", body, want)
	}

	_, _, ts = setup(t, okJSON(giphyJSON([2]string{"http://media.giphy.com/a.gif", gifAP})))
	if _, body, _ := getGifs(t, ts, ""); body != `{"gifs":[]}` {
		t.Errorf("all bad: body = %s, want {\"gifs\":[]}", body)
	}
}

func TestGifsCap(t *testing.T) {
	items := make([][2]string, 30)
	for i := range items {
		items[i] = [2]string{fmt.Sprintf("https://media.giphy.com/%d.gif", i), gifAP}
	}
	_, _, ts := setup(t, okJSON(giphyJSON(items...)))
	_, body, _ := getGifs(t, ts, "")
	if n := strings.Count(body, `"url"`); n != gifLimit {
		t.Errorf("relayed %d items, want %d", n, gifLimit)
	}
}

func TestCanonicalGIF(t *testing.T) {
	tests := []struct {
		in   string
		want string // "" = rejected
	}{
		{"https://media2.giphy.com/media/x/200.gif?cid=1&rid=2#f", "https://media2.giphy.com/media/x/200.gif"},
		{"https://media.tenor.com/x.gif", ""},
		{"https://i.imgur.com/x.gif", ""},
		{"HTTPS://media.giphy.com/a.gif", "https://media.giphy.com/a.gif"},
		{"https://media.giphy.com/a.GIF", "https://media.giphy.com/a.GIF"},
		{"https://media.giphy.com/a.gif?", "https://media.giphy.com/a.gif"},
		{"http://media.giphy.com/a.gif", ""},
		{"https://evil.com/a.gif", ""},
		{"https://media.giphy.com/a.mp4", ""},
		{"https://media.giphy.com/a.gif/b", ""},
		{"https://user@media.giphy.com/a.gif", ""},
		{"https://media.giphy.com:443/a.gif", ""},
		{"https://media.giphy.com:/a.gif", ""},
		{"https://MEDIA.giphy.com/a.gif", ""},
		{"https:media.giphy.com/a.gif", ""},
		{"//media.giphy.com/a.gif", ""},
		{"", ""},
		{"://bad", ""},
	}
	for _, tt := range tests {
		got, ok := canonicalGIF(tt.in)
		if ok != (tt.want != "") || got != tt.want {
			t.Errorf("canonicalGIF(%q) = %q, %v; want %q", tt.in, got, ok, tt.want)
		}
	}
}

func TestGifsCache(t *testing.T) {
	t.Run("hit and expiry", func(t *testing.T) {
		up, g, ts := setup(t, okJSON(giphyJSON([2]string{gifA, gifAP})))
		now := time.Unix(1000, 0)
		var nowMu sync.Mutex
		g.now = func() time.Time { nowMu.Lock(); defer nowMu.Unlock(); return now }

		_, b1, _ := getGifs(t, ts, "?q=Cat")
		_, b2, _ := getGifs(t, ts, "?q=cat")
		if n := len(up.requests()); n != 1 {
			t.Fatalf("upstream calls = %d, want 1", n)
		}
		if b1 != b2 {
			t.Errorf("bodies differ: %s vs %s", b1, b2)
		}
		nowMu.Lock()
		now = now.Add(gifCacheTTL + time.Second)
		nowMu.Unlock()
		getGifs(t, ts, "?q=cat")
		if n := len(up.requests()); n != 2 {
			t.Errorf("upstream calls after TTL = %d, want 2", n)
		}
	})
	t.Run("empty results are not cached", func(t *testing.T) {
		up, _, ts := setup(t, okJSON(giphyJSON([2]string{"http://media.giphy.com/a.gif", gifAP})))
		for i := 0; i < 2; i++ {
			if _, body, _ := getGifs(t, ts, ""); body != `{"gifs":[]}` {
				t.Fatalf("body = %s", body)
			}
		}
		if n := len(up.requests()); n != 2 {
			t.Errorf("upstream calls = %d, want 2", n)
		}
	})
	t.Run("errors are not cached", func(t *testing.T) {
		var calls int
		var mu sync.Mutex
		up, _, ts := setup(t, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			if n == 1 {
				w.WriteHeader(500)
				return
			}
			io.WriteString(w, giphyJSON())
		})
		if code, _, _ := getGifs(t, ts, ""); code != http.StatusBadGateway {
			t.Fatalf("first status = %d, want 502", code)
		}
		if code, _, _ := getGifs(t, ts, ""); code != http.StatusOK {
			t.Fatalf("second status = %d, want 200", code)
		}
		if n := len(up.requests()); n != 2 {
			t.Errorf("upstream calls = %d, want 2", n)
		}
	})
}

func TestGifsCacheBound(t *testing.T) {
	g := newGifSearch("http://unused", testKey)
	now := time.Unix(1000, 0)
	g.now = func() time.Time { return now }

	for i := 0; i < gifCacheMax; i++ {
		g.store(fmt.Sprint("k", i), nil)
	}
	g.store("extra", nil)
	if len(g.cache) != gifCacheMax {
		t.Fatalf("len(cache) = %d, want %d", len(g.cache), gifCacheMax)
	}
	if _, ok := g.cache["extra"]; !ok {
		t.Error("new entry missing")
	}

	// Expire some, keep others fresh: only the expired ones go.
	g = newGifSearch("http://unused", testKey)
	g.now = func() time.Time { return now }
	for i := 0; i < 10; i++ {
		g.store(fmt.Sprint("old", i), nil)
	}
	now = now.Add(gifCacheTTL + time.Second)
	for i := 0; i < gifCacheMax-10; i++ {
		g.store(fmt.Sprint("new", i), nil)
	}
	g.store("extra", nil)
	if len(g.cache) != gifCacheMax-10+1 {
		t.Fatalf("len(cache) = %d, want %d", len(g.cache), gifCacheMax-10+1)
	}
	for k := range g.cache {
		if strings.HasPrefix(k, "old") {
			t.Errorf("expired entry %s survived", k)
		}
	}
}

func TestGifsConcurrent(t *testing.T) {
	_, _, ts := setup(t, okJSON(giphyJSON([2]string{gifA, gifAP})))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(fmt.Sprintf("%s/gifs?q=k%d", ts.URL, i%5))
			if err != nil {
				t.Error(err)
				return
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d", resp.StatusCode)
			}
		}()
	}
	wg.Wait()
}
