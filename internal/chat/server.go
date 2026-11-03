package chat

import (
	"context"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

// IdentityFunc returns the private account key and the raw display name for a WebSocket
// request. The chat package treats account as opaque (never sent, never logged) and
// normalizes the nickname (cleanText, "Anonymous", CHAT-12 suffixing). An empty account
// means the connection is not admitted (CHAT-41).
type IdentityFunc func(r *http.Request) (account, nickname string)

// Server routes WebSocket connections to a fixed set of rooms. It is never
// modified after NewServer, so concurrent handlers read it without locks.
type Server struct {
	rooms          map[string]*Room
	defaultRoom    *Room // first room passed to NewServer; used when ?room is missing or empty
	allowedOrigins map[string]struct{}
	identity       IdentityFunc
	upgrader       websocket.Upgrader
}

// NewServer builds a Server over rooms. allowedOrigins lists extra origins
// (e.g. "https://example.com") accepted besides same-host requests.
// validAvatar reports whether an avatar URL (?avatar= or a profile frame) may be shown to
// others; nil disables avatars (CHAT-28). The chat package treats avatars as opaque strings.
// NewServer sets it on each room, so a room must belong to one Server and must not be
// running yet.
// identity supplies the account key and requested display name of each connection;
// HandleWebSocket never reads ?nickname= itself.
// It panics if identity is nil, rooms is empty or an id is empty or repeated (programmer error).
func NewServer(allowedOrigins []string, validAvatar func(string) bool, identity IdentityFunc, rooms ...*Room) *Server {
	if identity == nil {
		panic("chat: NewServer needs an identity resolver")
	}
	if len(rooms) == 0 {
		panic("chat: NewServer needs at least one room")
	}
	s := &Server{
		rooms:          make(map[string]*Room, len(rooms)),
		defaultRoom:    rooms[0],
		identity:       identity,
		allowedOrigins: make(map[string]struct{}, len(allowedOrigins)),
	}
	for _, r := range rooms {
		if r.id == "" {
			panic("chat: room id must not be empty")
		}
		if _, dup := s.rooms[r.id]; dup {
			panic("chat: duplicate room id " + r.id)
		}
		s.rooms[r.id] = r
		r.validAvatar = validAvatar
	}
	for _, o := range allowedOrigins {
		s.allowedOrigins[o] = struct{}{}
	}
	s.upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     s.checkOrigin,
	}
	return s
}

// Run runs every room until ctx is cancelled and returns once all have stopped.
// Call it once.
func (s *Server) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, r := range s.rooms {
		wg.Go(func() { r.Run(ctx) })
	}
	wg.Wait()
}

// HandleWebSocket resolves the room first (unknown id: 404, nothing upgraded), then the
// identity (no account: 401), then upgrades the connection, which applies the origin check (403).
func (s *Server) HandleWebSocket(c *gin.Context) {
	room := s.defaultRoom
	if id := c.Query("room"); id != "" {
		r, ok := s.rooms[id] // exact, case-sensitive match against the fixed set
		if !ok {
			c.AbortWithStatus(http.StatusNotFound) // no body and no log: the id is attacker-controlled
			return
		}
		room = r
	}
	account, nickname := s.identity(c.Request)
	if account == "" {
		c.AbortWithStatus(http.StatusUnauthorized) // no body and no log: nothing identifies the caller
		return
	}
	conn, err := s.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		log.Println("Upgrade error:", err)
		return
	}
	avatar := c.Query("avatar")
	if !room.allowedAvatar(avatar) {
		avatar = "" // invalid or disabled: join without a picture, never reject (CHAT-28)
	}
	bio := cleanText(c.Query("bio"), maxBioRunes) // CHAT-37: blank or absent means no bio, never reject
	room.serveConn(conn, account, normalizeNickname(nickname), avatar, bio)
}

// checkOrigin accepts same-host origins, requests without Origin (non-browser
// clients) and explicitly allowed origins. "null" is always rejected.
func (s *Server) checkOrigin(r *http.Request) bool {
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
	_, ok := s.allowedOrigins[origin]
	return ok
}
