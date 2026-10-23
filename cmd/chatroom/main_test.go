package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	chatSrv := newChatServer(nil)
	ctx, cancel := context.WithCancel(context.Background())
	go chatSrv.Run(ctx)
	ts := httptest.NewServer(newRouter(chatSrv, nil))
	t.Cleanup(func() {
		cancel()
		ts.Close()
	})
	return ts
}

func TestServeIndex(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "chadroom") {
		t.Fatal("body missing \"chadroom\"")
	}
}

func TestWebSocketSameOriginNoAllowlist(t *testing.T) {
	ts := newTestServer(t)
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws"
	hdr := http.Header{"Origin": []string{ts.URL}}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("dial: %v (resp %v)", err, resp)
	}
	conn.Close()
}

func TestIndexSecurityHeaders(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q", got)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q", got)
	}
	if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q", got)
	}
	const wantCSP = "default-src 'none'; script-src 'self'; " +
		"style-src 'self'; " +
		"img-src 'self' https://media.giphy.com https://media0.giphy.com https://media1.giphy.com " +
		"https://media2.giphy.com https://media3.giphy.com https://media4.giphy.com " +
		"https://i.giphy.com https://media.tenor.com https://i.imgur.com; " +
		"connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"
	csp := resp.Header.Get("Content-Security-Policy")
	if csp != wantCSP {
		t.Errorf("Content-Security-Policy = %q, want %q", csp, wantCSP)
	}
	for _, bad := range []string{"unsafe-inline", "unsafe-eval"} {
		if strings.Contains(csp, bad) {
			t.Errorf("Content-Security-Policy contains %q", bad)
		}
	}
}

func TestStaticAssets(t *testing.T) {
	ts := newTestServer(t)
	tests := []struct {
		path string
		ct   string
		body []byte
	}{
		{"/", "text/html; charset=utf-8", indexHTML},
		{"/app.js", "text/javascript; charset=utf-8", appJS},
		{"/app.css", "text/css; charset=utf-8", appCSS},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			resp, err := http.Get(ts.URL + tt.path)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d", resp.StatusCode)
			}
			if got := resp.Header.Get("Content-Type"); got != tt.ct {
				t.Errorf("Content-Type = %q, want %q", got, tt.ct)
			}
			if got := resp.Header.Get("X-Content-Type-Options"); got != "nosniff" {
				t.Errorf("X-Content-Type-Options = %q", got)
			}
			if got := resp.Header.Get("Cache-Control"); got != "no-cache" {
				t.Errorf("Cache-Control = %q", got)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != string(tt.body) {
				t.Error("body differs from the embedded file")
			}
		})
	}

	for _, path := range []string{"/index.html", "/static/"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s status = %d, want 404", path, resp.StatusCode)
		}
	}
}

func TestIndexHasNoInlineCode(t *testing.T) {
	html := string(indexHTML)
	if regexp.MustCompile(`(?i)<style`).MatchString(html) {
		t.Error("index.html contains a <style> element")
	}
	if regexp.MustCompile(`(?i)\sstyle\s*=`).MatchString(html) {
		t.Error("index.html contains a style attribute")
	}
	for _, tag := range regexp.MustCompile(`(?i)<script\b[^>]*>`).FindAllString(html, -1) {
		if !strings.Contains(tag, "src=") {
			t.Errorf("inline script tag: %s", tag)
		}
	}
	if m := regexp.MustCompile(`(?i)\son[a-z]+\s*=`).FindString(html); m != "" {
		t.Errorf("inline event handler attribute: %q", m)
	}
	for _, want := range []string{`src="/app.js"`, `href="/app.css"`} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %s", want)
		}
	}
}

func TestIndexHasOnlineList(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		`id="users-btn"`, `id="users-count"`, `id="users-panel"`, `id="users-list"`,
		`aria-controls="users-panel"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %s", want)
		}
	}
}

func TestIndexHasRoomTabs(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		`id="room-tabs"`, `role="tablist"`, `id="room-leave-btn"`, `id="room-name"`,
		`id="join-error"`, `role="tabpanel"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %s", want)
		}
	}
	if strings.Contains(html, `id="messages"`) {
		t.Error(`index.html still has the static id="messages" list`)
	}
}

func TestIndexHasTypingLine(t *testing.T) {
	html := string(indexHTML)
	scroller := strings.Index(html, `id="scroller"`)
	typing := strings.Index(html, `id="typing"`)
	form := strings.Index(html, `id="send-form"`)
	if scroller < 0 || typing < 0 || form < 0 || scroller >= typing || typing >= form {
		t.Fatalf(`id="typing" must sit between #scroller and #send-form (scroller=%d typing=%d form=%d)`, scroller, typing, form)
	}
	// The line updates every few seconds while anyone types: it must not be a live region.
	start := strings.LastIndex(html[:typing], "<")
	tag := html[start : typing+strings.Index(html[typing:], ">")]
	for _, banned := range []string{"aria-live", "role="} {
		if strings.Contains(tag, banned) {
			t.Errorf("#typing start tag must not have %s: %s", banned, tag)
		}
	}
}

func TestRoomsEndpoint(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/rooms")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	for k, want := range map[string]string{
		"Content-Type":           "application/json; charset=utf-8",
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "no-cache",
	} {
		if got := resp.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"rooms":["general","juegos","musica"]}`; string(body) != want {
		t.Fatalf("body = %s, want %s", body, want)
	}
}

func TestWebSocketRoomParam(t *testing.T) {
	ts := newTestServer(t)
	hdr := http.Header{"Origin": []string{ts.URL}}
	base := "ws" + strings.TrimPrefix(ts.URL, "http") + "/ws?room="

	conn, resp, err := websocket.DefaultDialer.Dial(base+"juegos", hdr)
	if err != nil {
		t.Fatalf("dial juegos: %v (resp %v)", err, resp)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"type":"welcome"`) {
		t.Fatalf("first frame = %s, want welcome", data)
	}

	conn2, resp, err := websocket.DefaultDialer.Dial(base+"nope", hdr)
	if err == nil {
		conn2.Close()
		t.Fatal("expected dial to unknown room to fail")
	}
	if resp == nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404, got resp=%v err=%v", resp, err)
	}
}

func TestRunShutdownClosesAllRooms(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	codeCh := make(chan int, 1)
	go func() { codeCh <- run(ctx, ln, config{}) }()

	addr := ln.Addr().String()
	hdr := http.Header{"Origin": []string{"http://" + addr}}
	var conns []*websocket.Conn
	for _, room := range []string{"general", "musica"} {
		conn, resp, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws?room="+room, hdr)
		if err != nil {
			t.Fatalf("dial %s: %v (resp %v)", room, err, resp)
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if _, _, err := conn.ReadMessage(); err != nil { // welcome: registered
			t.Fatalf("read welcome in %s: %v", room, err)
		}
		conns = append(conns, conn)
	}

	cancel()
	for _, conn := range conns {
		conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					t.Fatal("expected client read error after shutdown, got timeout")
				}
				break
			}
		}
	}
	select {
	case code := <-codeCh:
		if code != 0 {
			t.Fatalf("run returned %d, want 0", code)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("run did not return in time")
	}
}

func TestRunGracefulShutdown(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	codeCh := make(chan int, 1)
	go func() { codeCh <- run(ctx, ln, config{}) }()

	addr := ln.Addr().String()
	hdr := http.Header{"Origin": []string{"http://" + addr}}
	conn, resp, err := websocket.DefaultDialer.Dial("ws://"+addr+"/ws", hdr)
	if err != nil {
		t.Fatalf("dial: %v (resp %v)", err, resp)
	}
	defer conn.Close()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read welcome: %v", err)
	}
	var first struct {
		Type string `json:"type"`
		From string `json:"from"`
		Text string `json:"text"`
		TS   int64  `json:"ts"`
	}
	if err := json.Unmarshal(data, &first); err != nil {
		t.Fatalf("decode first frame %q: %v", data, err)
	}
	if first.Type != "welcome" {
		t.Fatalf("first frame type = %q, want welcome", first.Type)
	}

	cancel()
	// Drain frames already queued (e.g. join) until the connection errors.
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				t.Fatal("expected client read error after shutdown, got timeout")
			}
			break
		}
	}
	select {
	case code := <-codeCh:
		if code != 0 {
			t.Fatalf("run returned %d, want 0", code)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("run did not return in time")
	}
}

func TestServeListenError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	codeCh := make(chan int, 1)
	go func() { codeCh <- serve(context.Background(), ln.Addr().String(), config{}) }()
	select {
	case code := <-codeCh:
		if code != 1 {
			t.Fatalf("serve returned %d, want 1", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return in time")
	}
}

func TestParseAllowedOrigins(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", []string{}},
		{"trims blanks", " http://a.com , ,https://b.com ", []string{"http://a.com", "https://b.com"}},
		{"drops null", "null,http://a.com", []string{"http://a.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseAllowedOrigins(tt.in); !slices.Equal(got, tt.want) {
				t.Errorf("parseAllowedOrigins(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// The JS allowlist must list exactly the hosts the CSP allows in img-src.
func TestImageHostsMirroredInJS(t *testing.T) {
	block := regexp.MustCompile(`(?s)const IMAGE_HOSTS = new Set\(\[(.*?)\]\)`).FindSubmatch(appJS)
	if block == nil {
		t.Fatal("IMAGE_HOSTS not found in app.js")
	}
	var got []string
	for _, m := range regexp.MustCompile(`'([^']+)'`).FindAllSubmatch(block[1], -1) {
		got = append(got, string(m[1]))
	}
	want := slices.Clone(imageHosts)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("IMAGE_HOSTS = %v, want %v (imageHosts)", got, want)
	}
}

func TestIndexHasGifPicker(t *testing.T) {
	html := string(indexHTML)
	for _, want := range []string{
		`id="gif-search-box"`, `id="gif-search"`, `id="gif-grid"`, "Powered by GIPHY",
		`id="gif-url"`, `id="gif-send"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("index.html missing %s", want)
		}
	}
	box := strings.Index(html, `id="gif-search-box"`)
	paste := strings.Index(html, `id="gif-url"`)
	if box < 0 || paste < 0 || box >= paste {
		t.Errorf(`id="gif-search-box" must come before id="gif-url" (box=%d url=%d)`, box, paste)
	}
}
