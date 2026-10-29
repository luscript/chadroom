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

const testList = "PLxxxxxxxxxx"

type vidEntry struct {
	id, upload, privacy string
	embeddable          bool
}

func okv(id string) vidEntry { return vidEntry{id, "processed", "public", true} }

// tid returns a distinct valid 11-char video id.
func tid(n int) string { return fmt.Sprintf("vid%08d", n) }

func itemsJSON(ids ...string) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprintf(`{"contentDetails":{"videoId":%q}}`, id)
	}
	return `{"items":[` + strings.Join(parts, ",") + `]}`
}

func videosJSON(entries ...vidEntry) string {
	parts := make([]string, len(entries))
	for i, e := range entries {
		parts[i] = fmt.Sprintf(`{"id":%q,"status":{"uploadStatus":%q,"privacyStatus":%q,"embeddable":%t}}`,
			e.id, e.upload, e.privacy, e.embeddable)
	}
	return `{"items":[` + strings.Join(parts, ",") + `]}`
}

// youtubeFake routes on the upstream path to canned bodies.
func youtubeFake(items, videos string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlistItems":
			io.WriteString(w, items)
		case "/videos":
			io.WriteString(w, videos)
		default:
			http.NotFound(w, r)
		}
	}
}

func status(code int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(code)
		io.WriteString(w, body)
	}
}

func newPlaylistTestServer(t *testing.T, p *playlistImport) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(newRouter(newChatServer(nil), nil, p))
	t.Cleanup(ts.Close)
	return ts
}

func setupPlaylist(t *testing.T, h http.HandlerFunc) (*upstream, *playlistImport, *httptest.Server) {
	t.Helper()
	up := fakeUpstream(t, h)
	p := newPlaylistImport(up.URL, testKey)
	return up, p, newPlaylistTestServer(t, p)
}

func getPlaylist(t *testing.T, ts *httptest.Server, query string) (int, string, http.Header) {
	t.Helper()
	resp, err := http.Get(ts.URL + "/playlist" + query)
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

func videosBody(ids ...string) string {
	return `{"videos":["` + strings.Join(ids, `","`) + `"]}`
}

func TestPlaylistDisabledWithoutKey(t *testing.T) {
	ts := newTestServer(t)
	if code, _, _ := getPlaylist(t, ts, "?list="+testList); code != http.StatusNotFound {
		t.Errorf("GET /playlist = %d, want 404", code)
	}
}

func TestPlaylistRequestParams(t *testing.T) {
	a, b := tid(1), tid(2)
	up, _, ts := setupPlaylist(t, youtubeFake(itemsJSON(a, b), videosJSON(okv(a), okv(b))))
	code, body, hdr := getPlaylist(t, ts, "?list="+testList+"&maxResults=500&key=x&part=snippet&id=zzz&fields=foo")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if want := videosBody(a, b); body != want {
		t.Errorf("body = %s, want %s", body, want)
	}
	requireJSONHeaders(t, hdr)
	reqs := up.requests()
	if len(reqs) != 2 {
		t.Fatalf("upstream calls = %d, want 2", len(reqs))
	}
	want := []struct {
		path  string
		query url.Values
	}{
		{"/playlistItems", url.Values{
			"part": {"contentDetails"}, "playlistId": {testList}, "maxResults": {"50"},
			"fields": {"items/contentDetails/videoId"},
		}},
		{"/videos", url.Values{
			"part": {"status"}, "id": {a + "," + b},
			"fields": {"items(id,status(uploadStatus,privacyStatus,embeddable))"},
		}},
	}
	for i, w := range want {
		r := reqs[i]
		if r.URL.Path != w.path {
			t.Errorf("call %d path = %q, want %q", i, r.URL.Path, w.path)
		}
		if got := r.URL.Query(); got.Encode() != w.query.Encode() {
			t.Errorf("call %d query = %v, want %v", i, got, w.query)
		}
		if r.URL.Query().Has("key") || r.URL.Query().Has("api_key") {
			t.Errorf("call %d carries the key in the URL", i)
		}
		if got := r.Header.Get("X-Goog-Api-Key"); got != testKey {
			t.Errorf("call %d X-Goog-Api-Key = %q, want %q", i, got, testKey)
		}
	}
}

func TestPlaylistInvalidList(t *testing.T) {
	up, _, ts := setupPlaylist(t, youtubeFake(itemsJSON(), videosJSON()))
	for _, q := range []string{
		"", "?list=", "?list=abc123456", "?list=" + strings.Repeat("a", 65), "?list=PL%20xxxxxxxxx",
		"?list=PL%0Axxxxxxxxx", "?list=..", "?list=PL%2F..%2Fxxxxxxxx", "?list=PL,abcdefghij",
	} {
		code, body, hdr := getPlaylist(t, ts, q)
		requireJSONHeaders(t, hdr)
		if code != http.StatusBadRequest {
			t.Errorf("%q: status = %d, want 400", q, code)
		}
		if want := `{"error":"invalid_list"}`; body != want {
			t.Errorf("%q: body = %s, want %s", q, body, want)
		}
	}
	if n := len(up.requests()); n != 0 {
		t.Errorf("upstream called %d times for invalid lists, want 0", n)
	}
	// Boundaries are accepted.
	for _, l := range []string{strings.Repeat("a", 10), strings.Repeat("A", 64), "PL_-" + strings.Repeat("0", 7)} {
		if code, _, _ := getPlaylist(t, ts, "?list="+l); code != http.StatusOK {
			t.Errorf("list %q: status = %d, want 200", l, code)
		}
	}
}

func TestPlaylistFilters(t *testing.T) {
	a, b, c, d, e, f, g, z := tid(1), tid(2), tid(3), tid(4), tid(5), tid(6), tid(7), tid(26)
	items := itemsJSON(a, b, c, d, e, f, a, "bad!", "../x", g)
	vids := videosJSON(
		okv(a),
		vidEntry{b, "processed", "private", true},
		vidEntry{c, "processed", "unlisted", true},
		vidEntry{d, "processed", "public", false},
		vidEntry{e, "uploaded", "public", true},
		okv(g),
		okv(z), // never requested
	)
	up, _, ts := setupPlaylist(t, youtubeFake(items, vids))
	code, body, hdr := getPlaylist(t, ts, "?list="+testList)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if want := videosBody(a, c, g); body != want {
		t.Errorf("body = %s, want %s", body, want)
	}
	requireJSONHeaders(t, hdr)
	reqs := up.requests()
	if len(reqs) != 2 {
		t.Fatalf("upstream calls = %d, want 2", len(reqs))
	}
	if got, want := reqs[1].URL.Query().Get("id"), strings.Join([]string{a, b, c, d, e, f, g}, ","); got != want {
		t.Errorf("videos id = %q, want %q", got, want)
	}
}

func TestPlaylistEmpty(t *testing.T) {
	for name, items := range map[string]string{
		"no items":      itemsJSON(),
		"only bad ids":  itemsJSON("bad!", "../x", ""),
		"items missing": `{}`,
		"null items":    `{"items":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			up, _, ts := setupPlaylist(t, youtubeFake(items, videosJSON()))
			for i := 1; i <= 2; i++ {
				code, body, hdr := getPlaylist(t, ts, "?list="+testList)
				if code != http.StatusOK {
					t.Fatalf("status = %d", code)
				}
				if want := `{"videos":[]}`; body != want {
					t.Errorf("body = %s, want %s", body, want)
				}
				requireJSONHeaders(t, hdr)
				if n := len(up.requests()); n != i { // one playlistItems call each time, no videos call
					t.Errorf("after GET %d: upstream calls = %d, want %d", i, n, i)
				}
			}
			for _, r := range up.requests() {
				if r.URL.Path == "/videos" {
					t.Error("videos called without valid ids")
				}
			}
		})
	}
	t.Run("all filtered out", func(t *testing.T) {
		a := tid(1)
		_, _, ts := setupPlaylist(t, youtubeFake(itemsJSON(a), videosJSON(vidEntry{a, "processed", "private", true})))
		if _, body, _ := getPlaylist(t, ts, "?list="+testList); body != `{"videos":[]}` {
			t.Errorf("body = %s", body)
		}
	})
}

func TestPlaylistNotFound(t *testing.T) {
	tests := map[string]http.HandlerFunc{
		"404": status(404, `{"error":{"code":404,"errors":[{"reason":"playlistNotFound"}]}}`),
		"403 playlistItemsNotAccessible": status(403,
			`{"error":{"code":403,"errors":[{"domain":"youtube.playlistItem","reason":"playlistItemsNotAccessible"}]}}`),
	}
	for name, h := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, ts := setupPlaylist(t, h)
			code, body, hdr := getPlaylist(t, ts, "?list="+testList)
			requireJSONHeaders(t, hdr)
			if code != http.StatusNotFound {
				t.Errorf("status = %d, want 404", code)
			}
			if want := `{"error":"not_found"}`; body != want {
				t.Errorf("body = %s, want %s", body, want)
			}
		})
	}
}

func TestPlaylistUpstreamError(t *testing.T) {
	a := tid(1)
	good := itemsJSON(a)
	tests := []struct {
		name string
		h    http.HandlerFunc
	}{
		{"403 quotaExceeded", status(403, `{"error":{"errors":[{"reason":"quotaExceeded"}]}}`)},
		{"403 no body", status(403, "")},
		{"400 keyInvalid", status(400, `{"error":{"errors":[{"reason":"keyInvalid"}]}}`)},
		{"500", status(500, "")},
		{"malformed json", youtubeFake("not json", "")},
		{"playlistItems over 1 MiB", youtubeFake(`{"items":[],"pad":"`+strings.Repeat("a", maxYouTubeBody)+`"}`, "")},
		{"videos 403", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/videos" {
				status(403, `{"error":{"errors":[{"reason":"quotaExceeded"}]}}`)(w, r)
				return
			}
			io.WriteString(w, good)
		}},
		{"videos 500", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/videos" {
				w.WriteHeader(500)
				return
			}
			io.WriteString(w, good)
		}},
		{"videos not json", youtubeFake(good, "nope")},
		{"videos over 1 MiB", youtubeFake(good, `{"items":[],"pad":"`+strings.Repeat("a", maxYouTubeBody)+`"}`)},
		{"videos 404 is not not_found", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/videos" {
				w.WriteHeader(404)
				return
			}
			io.WriteString(w, good)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, p, ts := setupPlaylist(t, tt.h)
			code, body, hdr := getPlaylist(t, ts, "?list="+testList)
			requireJSONHeaders(t, hdr)
			if code != http.StatusBadGateway {
				t.Errorf("status = %d, want 502", code)
			}
			if want := `{"error":"unavailable"}`; body != want {
				t.Errorf("body = %s, want %s", body, want)
			}
			if _, ok := p.cached(testList); ok {
				t.Error("error was cached")
			}
		})
	}
}

func TestPlaylistRedirectNotFollowed(t *testing.T) {
	other := fakeUpstream(t, okJSON(itemsJSON(tid(1))))
	up, _, ts := setupPlaylist(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/x", http.StatusFound)
	})
	code, body, hdr := getPlaylist(t, ts, "?list="+testList)
	requireJSONHeaders(t, hdr)
	if code != http.StatusBadGateway || body != `{"error":"unavailable"}` {
		t.Errorf("got %d %s, want 502 unavailable", code, body)
	}
	if n := len(other.requests()); n != 0 {
		t.Errorf("redirect target contacted %d times, want 0", n)
	}
	if n := len(up.requests()); n != 1 {
		t.Errorf("upstream calls = %d, want 1", n)
	}
}

func TestPlaylistTimeout(t *testing.T) {
	a := tid(1)
	release := make(chan struct{})
	_, p, ts := setupPlaylist(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/playlistItems" {
			io.WriteString(w, itemsJSON(a))
			return
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	t.Cleanup(func() { close(release) })
	p.timeout = 50 * time.Millisecond
	start := time.Now()
	code, body, _ := getPlaylist(t, ts, "?list="+testList)
	if code != http.StatusBadGateway || body != `{"error":"unavailable"}` {
		t.Errorf("got %d %s, want 502 unavailable", code, body)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v, want about the 50ms deadline", d)
	}
}

func TestPlaylistKeyNeverLeaks(t *testing.T) {
	const secret = "SECRET"
	var buf bytes.Buffer
	var logMu sync.Mutex
	log.SetOutput(lockedWriter{&logMu, &buf})
	t.Cleanup(func() { log.SetOutput(os.Stderr) })

	dead := httptest.NewServer(http.NotFoundHandler())
	deadURL := dead.URL
	dead.Close()

	a := tid(1)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	cases := map[string]struct {
		h       http.HandlerFunc // nil: base is the closed server
		timeout time.Duration
		status  int
	}{
		"transport error": {nil, 0, http.StatusBadGateway},
		"403":             {status(403, `{"error":{"errors":[{"reason":"quotaExceeded","message":"`+secret+`"}]}}`), 0, http.StatusBadGateway},
		"timeout": {func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-release:
			}
		}, 50 * time.Millisecond, http.StatusBadGateway},
		"redirect": {func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://127.0.0.1:1/"+secret, http.StatusFound)
		}, 0, http.StatusBadGateway},
		"success": {youtubeFake(itemsJSON(a), videosJSON(okv(a))), 0, http.StatusOK},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var p *playlistImport
			if tc.h == nil {
				p = newPlaylistImport(deadURL, secret)
			} else {
				p = newPlaylistImport(fakeUpstream(t, tc.h).URL, secret)
			}
			if tc.timeout > 0 {
				p.timeout = tc.timeout
			}
			ts := newPlaylistTestServer(t, p)
			code, body, hdr := getPlaylist(t, ts, "?list="+testList)
			if code != tc.status {
				t.Errorf("status = %d, want %d", code, tc.status)
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

func TestPlaylistCache(t *testing.T) {
	t.Run("hit, case, expiry", func(t *testing.T) {
		a := tid(1)
		up, p, ts := setupPlaylist(t, youtubeFake(itemsJSON(a), videosJSON(okv(a))))
		now := time.Unix(1000, 0)
		var nowMu sync.Mutex
		p.now = func() time.Time { nowMu.Lock(); defer nowMu.Unlock(); return now }

		_, b1, _ := getPlaylist(t, ts, "?list="+testList)
		_, b2, _ := getPlaylist(t, ts, "?list="+testList)
		if n := len(up.requests()); n != 2 {
			t.Fatalf("upstream calls = %d, want 2 (one import)", n)
		}
		if b1 != b2 || b1 != videosBody(a) {
			t.Errorf("bodies differ or wrong: %s vs %s", b1, b2)
		}
		getPlaylist(t, ts, "?list="+strings.ToLower(testList))
		if n := len(up.requests()); n != 4 {
			t.Errorf("upstream calls after a case-different id = %d, want 4", n)
		}
		nowMu.Lock()
		now = now.Add(playlistCacheTTL + time.Second)
		nowMu.Unlock()
		getPlaylist(t, ts, "?list="+testList)
		if n := len(up.requests()); n != 6 {
			t.Errorf("upstream calls after TTL = %d, want 6", n)
		}
	})
	t.Run("errors are not cached", func(t *testing.T) {
		a := tid(1)
		var calls int
		var mu sync.Mutex
		up, _, ts := setupPlaylist(t, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			if n == 1 {
				w.WriteHeader(500)
				return
			}
			youtubeFake(itemsJSON(a), videosJSON(okv(a)))(w, r)
		})
		if code, _, _ := getPlaylist(t, ts, "?list="+testList); code != http.StatusBadGateway {
			t.Fatalf("first status = %d, want 502", code)
		}
		if code, _, _ := getPlaylist(t, ts, "?list="+testList); code != http.StatusOK {
			t.Fatalf("second status = %d, want 200", code)
		}
		if n := len(up.requests()); n != 3 {
			t.Errorf("upstream calls = %d, want 3", n)
		}
	})
}

func TestPlaylistCacheBound(t *testing.T) {
	p := newPlaylistImport("http://unused", testKey)
	now := time.Unix(1000, 0)
	p.now = func() time.Time { return now }

	for i := 0; i < playlistCacheMax; i++ {
		p.store(fmt.Sprint("k", i), nil)
	}
	p.store("extra", nil)
	if len(p.cache) != playlistCacheMax {
		t.Fatalf("len(cache) = %d, want %d", len(p.cache), playlistCacheMax)
	}
	if _, ok := p.cache["extra"]; !ok {
		t.Error("new entry missing")
	}

	// Expire some, keep others fresh: only the expired ones go.
	p = newPlaylistImport("http://unused", testKey)
	p.now = func() time.Time { return now }
	for i := 0; i < 10; i++ {
		p.store(fmt.Sprint("old", i), nil)
	}
	now = now.Add(playlistCacheTTL + time.Second)
	for i := 0; i < playlistCacheMax-10; i++ {
		p.store(fmt.Sprint("new", i), nil)
	}
	p.store("extra", nil)
	if len(p.cache) != playlistCacheMax-10+1 {
		t.Fatalf("len(cache) = %d, want %d", len(p.cache), playlistCacheMax-10+1)
	}
	for k := range p.cache {
		if strings.HasPrefix(k, "old") {
			t.Errorf("expired entry %s survived", k)
		}
	}
}

func TestPlaylistCap(t *testing.T) {
	ids := make([]string, 60)
	for i := range ids {
		ids[i] = tid(i)
	}
	up, _, ts := setupPlaylist(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlistItems":
			io.WriteString(w, itemsJSON(ids...))
		case "/videos": // every requested id is playable
			var es []vidEntry
			for _, id := range strings.Split(r.URL.Query().Get("id"), ",") {
				es = append(es, okv(id))
			}
			io.WriteString(w, videosJSON(es...))
		}
	})
	code, body, _ := getPlaylist(t, ts, "?list="+testList)
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	if want := videosBody(ids[:50]...); body != want {
		t.Errorf("body = %s, want the first 50 ids", body)
	}
	reqs := up.requests()
	if len(reqs) != 2 {
		t.Fatalf("upstream calls = %d, want 2", len(reqs))
	}
	if n := len(strings.Split(reqs[1].URL.Query().Get("id"), ",")); n != 50 {
		t.Errorf("videos id count = %d, want 50", n)
	}
}

func TestPlaylistConcurrent(t *testing.T) {
	a := tid(1)
	_, _, ts := setupPlaylist(t, youtubeFake(itemsJSON(a), videosJSON(okv(a))))
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(fmt.Sprintf("%s/playlist?list=PL%08d", ts.URL, i%5))
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
