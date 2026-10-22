package chat

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

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
	taken          map[string]struct{} // foldKey(nickname) of every registered client
	broadcast      chan inbound
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
		taken:          make(map[string]struct{}),
		broadcast:      make(chan inbound, sendBufferSize),
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
// deadlock itself); events go straight through deliver/fanOut.
func (cr *Room) Run(ctx context.Context) {
	defer close(cr.done)
	for {
		select {
		case c := <-cr.register:
			name := cr.assignNickname(c.nickname)
			c.nickname = name
			cr.clients[c] = struct{}{}
			cr.taken[foldKey(name)] = struct{}{}
			close(c.registered) // publishes the final nickname to the pumps
			ts := time.Now().UnixMilli()
			welcome, join := encode(message{typeWelcome, name, "", ts}), encode(message{typeJoin, name, "", ts})
			if welcome != nil && join != nil && cr.deliver(c, welcome) {
				cr.fanOut(join)
			}
		case c := <-cr.unregister:
			cr.leave(c)
		case in := <-cr.broadcast:
			cr.relay(in)
		case <-ctx.Done():
			for c := range cr.clients {
				cr.remove(c)
			}
			return
		}
	}
}

// leave removes c and announces it. Messages c queued before disconnecting
// are relayed first: select picks ready cases at random, so without the drain
// a client's last message could be dropped (no longer a member) or arrive
// after its leave notice.
func (cr *Room) leave(c *Client) {
	if _, ok := cr.clients[c]; !ok {
		return // already dropped (slow client) or unregistered
	}
	cr.drainBroadcast()
	if _, ok := cr.clients[c]; !ok {
		return // the drain's fan-out dropped c as a slow client (silent, CHAT-02)
	}
	cr.remove(c)
	if leave := encode(message{typeLeave, c.nickname, "", time.Now().UnixMilli()}); leave != nil {
		cr.fanOut(leave)
	}
}

// relay fans a chat message out, discarding it if its sender is no longer a member.
func (cr *Room) relay(in inbound) {
	if _, ok := cr.clients[in.from]; !ok {
		return // sender was removed; its name may belong to someone else now
	}
	if chat := encode(message{typeChat, in.from.nickname, in.text, time.Now().UnixMilli()}); chat != nil {
		cr.fanOut(chat)
	}
}

// drainBroadcast relays everything already queued on cr.broadcast without blocking.
func (cr *Room) drainBroadcast() {
	for {
		select {
		case in := <-cr.broadcast:
			cr.relay(in)
		default:
			return
		}
	}
}

// remove drops c from the registry, releases its nickname and closes send.
// Run-only; membership guarantees send is still open.
func (cr *Room) remove(c *Client) {
	delete(cr.clients, c)
	delete(cr.taken, foldKey(c.nickname))
	close(c.send)
}

// deliver queues msg for c without blocking; a client whose buffer is full is
// dropped silently.
func (cr *Room) deliver(c *Client, msg []byte) bool {
	select {
	case c.send <- msg:
		return true
	default:
		cr.remove(c)
		return false
	}
}

// fanOut delivers msg to every client without blocking.
func (cr *Room) fanOut(msg []byte) {
	for c := range cr.clients {
		cr.deliver(c, msg)
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

	client := &Client{conn: conn, send: make(chan []byte, sendBufferSize), registered: make(chan struct{}), nickname: nickname}
	select {
	case cr.register <- client:
	case <-cr.done:
		conn.Close()
		return
	}

	<-client.registered // Run closes it in the same case that accepted register

	go client.writePump(cr)
	go client.readPump(cr)
}
