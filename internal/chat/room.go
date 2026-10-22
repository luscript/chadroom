package chat

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

const (
	defaultWriteWait      = 10 * time.Second
	defaultPongWait       = 60 * time.Second
	defaultPingPeriod     = defaultPongWait * 9 / 10
	defaultMaxMessageSize = 4096
	sendBufferSize        = 256
	maxNicknameRunes      = 32
)

// Room manages clients and messages.
// Only Run touches clients and closes client.send, so membership in the map
// means "send is still open" and a channel can never be closed twice.
type Room struct {
	clients        map[*Client]struct{}
	broadcast      chan []byte
	register       chan *Client
	unregister     chan *Client
	done           chan struct{} // closed by Run when it exits
	allowedOrigins map[string]struct{}
	upgrader       websocket.Upgrader

	// Unexported so tests can shorten them right after NewRoom.
	writeWait      time.Duration
	pongWait       time.Duration
	pingPeriod     time.Duration
	maxMessageSize int64
}

// NewRoom initializes the chatroom. allowedOrigins lists extra origins
// (e.g. "https://example.com") accepted besides same-host requests.
func NewRoom(allowedOrigins []string) *Room {
	cr := &Room{
		clients:        make(map[*Client]struct{}),
		broadcast:      make(chan []byte, sendBufferSize),
		register:       make(chan *Client),
		unregister:     make(chan *Client),
		done:           make(chan struct{}),
		allowedOrigins: make(map[string]struct{}, len(allowedOrigins)),
		writeWait:      defaultWriteWait,
		pongWait:       defaultPongWait,
		pingPeriod:     defaultPingPeriod,
		maxMessageSize: defaultMaxMessageSize,
	}
	for _, o := range allowedOrigins {
		cr.allowedOrigins[o] = struct{}{}
	}
	cr.upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     cr.checkOrigin,
	}
	return cr
}

// Run handles client registration and broadcasting until ctx is cancelled.
// It never sends on its own register/unregister/broadcast channels (it would
// deadlock itself); notices go straight through fanOut.
func (cr *Room) Run(ctx context.Context) {
	defer close(cr.done)
	for {
		select {
		case client := <-cr.register:
			cr.clients[client] = struct{}{}
			cr.fanOut([]byte(client.nickname + " joined! 🎉"))
		case client := <-cr.unregister:
			if _, ok := cr.clients[client]; !ok {
				continue // already dropped (slow client) or unregistered
			}
			delete(cr.clients, client)
			close(client.send)
			cr.fanOut([]byte(client.nickname + " left. 👋"))
		case message := <-cr.broadcast:
			cr.fanOut(message)
		case <-ctx.Done():
			for client := range cr.clients {
				delete(cr.clients, client)
				close(client.send)
			}
			return
		}
	}
}

// fanOut delivers msg to every client without blocking; clients whose buffer
// is full are dropped silently.
func (cr *Room) fanOut(msg []byte) {
	for client := range cr.clients {
		select {
		case client.send <- msg:
		default:
			delete(cr.clients, client)
			close(client.send)
		}
	}
}

// checkOrigin accepts same-host origins, requests without Origin (non-browser
// clients) and explicitly allowed origins. "null" is always rejected.
func (cr *Room) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if origin == "null" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		return false
	}
	if strings.EqualFold(u.Host, r.Host) {
		return true
	}
	_, ok := cr.allowedOrigins[origin]
	return ok
}

// HandleWebSocket upgrades HTTP to WebSocket
func (cr *Room) HandleWebSocket(c *gin.Context) {
	conn, err := cr.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}

	nickname := normalizeNickname(c.Query("nickname"))

	client := &Client{conn: conn, send: make(chan []byte, sendBufferSize), nickname: nickname}
	select {
	case cr.register <- client:
	case <-cr.done:
		conn.Close()
		return
	}

	go client.writePump(cr)
	go client.readPump(cr)
}

// normalizeNickname makes an untrusted nickname safe to embed in text frames:
// valid UTF-8, trimmed, at most maxNicknameRunes runes, "Anonymous" if empty.
func normalizeNickname(s string) string {
	s = strings.TrimSpace(strings.ToValidUTF8(s, "\uFFFD"))
	if utf8.RuneCountInString(s) > maxNicknameRunes {
		s = strings.TrimSpace(string([]rune(s)[:maxNicknameRunes]))
	}
	if s == "" {
		return "Anonymous"
	}
	return s
}
