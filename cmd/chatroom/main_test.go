package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"chatroom/internal/chat"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	room := chat.NewRoom(nil)
	ctx, cancel := context.WithCancel(context.Background())
	go room.Run(ctx)
	ts := httptest.NewServer(newRouter(room))
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
	if !strings.Contains(string(body), "Chatroom") {
		t.Fatal("body missing \"Chatroom\"")
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
	go func() { codeCh <- run(ctx, ln, "") }()

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
	go func() { codeCh <- serve(context.Background(), ln.Addr().String(), "") }()
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
