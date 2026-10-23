package chat

import (
	"context"
	"slices"
	"strings"
	"time"

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
	id         string // immutable; used for lookup and logs, never sent on the wire
	clients    map[*Client]struct{}
	taken      map[string]struct{} // foldKey(nickname) of every registered client
	history    []message           // last historySize chat messages, oldest first
	dropped    []*Client           // slow clients removed by a fan-out, not yet announced
	broadcast  chan inbound        // every inbound kind (chat, direct and typing), so a client's frames stay ordered
	register   chan *Client
	unregister chan *Client
	done       chan struct{} // closed by Run when it exits
	player     *player       // nil: this room has no shared player (CHAT-30); set only by NewMusicRoom

	// Unexported so tests can shorten them right after NewRoom.
	writeWait      time.Duration
	pongWait       time.Duration
	pingPeriod     time.Duration
	maxMessageSize int64
}

// NewRoom initializes the chatroom identified by id.
func NewRoom(id string) *Room {
	return &Room{
		id:             id,
		clients:        make(map[*Client]struct{}),
		taken:          make(map[string]struct{}),
		history:        make([]message, 0, historySize),
		broadcast:      make(chan inbound, sendBufferSize),
		register:       make(chan *Client),
		unregister:     make(chan *Client),
		done:           make(chan struct{}),
		writeWait:      defaultWriteWait,
		pongWait:       defaultPongWait,
		pingPeriod:     defaultPingPeriod,
		maxMessageSize: defaultMaxMessageSize,
	}
}

// NewMusicRoom is NewRoom plus a shared player (CHAT-30).
func NewMusicRoom(id string) *Room {
	cr := NewRoom(id)
	cr.player = &player{}
	return cr
}

// Run handles client registration and broadcasting until ctx is cancelled.
// It never sends on its own register/unregister/broadcast channels (it would
// deadlock itself); events go straight through deliver/publish.
func (cr *Room) Run(ctx context.Context) {
	defer close(cr.done)
	for {
		select {
		case c := <-cr.register:
			cr.admit(c)
		case c := <-cr.unregister:
			cr.leave(c)
		case in := <-cr.broadcast:
			cr.dispatch(in)
		case <-ctx.Done():
			for c := range cr.clients {
				cr.remove(c)
			}
			return
		}
	}
}

// admit registers c and sends it welcome, users and history privately before
// announcing its join. A newcomer dropped on the way is never announced, so
// nobody needs a leave for it.
func (cr *Room) admit(c *Client) {
	name := cr.assignNickname(c.nickname)
	c.nickname = name
	cr.clients[c] = struct{}{}
	cr.taken[foldKey(name)] = struct{}{}
	close(c.registered) // publishes the final nickname to the pumps
	now := time.Now()
	ts := now.UnixMilli()
	private := [][]byte{
		encode(message{Type: typeWelcome, From: name, Avatar: c.avatar, TS: ts}),
		encode(usersFrame{Type: typeUsers, Users: cr.userNames(), Avatars: cr.avatars(), TS: ts}),
		encode(historyFrame{typeHistory, cr.history, ts}),
	}
	if cr.player != nil && cr.player.video != "" {
		private = append(private, encode(cr.player.frame(now))) // CHAT-30: state right after history
	}
	for _, f := range private {
		if f == nil || !cr.deliver(c, f) {
			return
		}
	}
	if join := encode(message{Type: typeJoin, From: name, Avatar: c.avatar, TS: ts}); join != nil {
		cr.publish(join)
	}
}

// userNames returns the connected nicknames sorted by foldKey (unique among
// members, so the order is total). Never nil.
func (cr *Room) userNames() []string {
	names := make([]string, 0, len(cr.clients))
	for c := range cr.clients {
		names = append(names, c.nickname)
	}
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(foldKey(a), foldKey(b)) })
	return names
}

// avatars maps members' nicknames to their avatars, only for members that have one.
// nil when nobody has one, so the users frame omits the field (CHAT-28). Run-only.
func (cr *Room) avatars() map[string]string {
	var m map[string]string
	for c := range cr.clients {
		if c.avatar != "" {
			if m == nil {
				m = make(map[string]string)
			}
			m[c.nickname] = c.avatar
		}
	}
	return m
}

// remember appends m to the history window, evicting the oldest past historySize. Run-only.
func (cr *Room) remember(m message) {
	if len(cr.history) == historySize {
		copy(cr.history, cr.history[1:])
		cr.history = cr.history[:historySize-1]
	}
	cr.history = append(cr.history, m)
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
		return // the drain's publish dropped c and already announced its leave
	}
	cr.remove(c)
	if leave := encode(message{Type: typeLeave, From: c.nickname, TS: time.Now().UnixMilli()}); leave != nil {
		cr.publish(leave)
	}
}

// relay stores and fans a chat message out, discarding it if its sender is no longer a member.
func (cr *Room) relay(in inbound) {
	if _, ok := cr.clients[in.from]; !ok {
		return // sender was removed; its name may belong to someone else now
	}
	m := message{Type: typeChat, From: in.from.nickname, Text: in.text, Avatar: in.from.avatar, TS: time.Now().UnixMilli()}
	if chat := encode(m); chat != nil {
		cr.remember(m)
		cr.publish(chat)
	}
}

// dispatch routes a classified frame from a current member. Run-only.
func (cr *Room) dispatch(in inbound) {
	if _, ok := cr.clients[in.from]; !ok {
		return // removed; its name may belong to someone else now
	}
	switch in.kind {
	case inboundDM:
		cr.direct(in)
	case inboundBadDM:
		cr.dmError(in.from, "", reasonInvalid)
	case inboundTyping:
		cr.typing(in)
	case inboundMusic:
		cr.music(in)
	default:
		cr.relay(in)
	}
}

// direct delivers a DM to its recipient and echoes it to the sender. It never stores or logs it.
func (cr *Room) direct(in inbound) {
	key := foldKey(in.to)
	if key == foldKey(in.from.nickname) {
		cr.dmError(in.from, in.to, reasonSelf)
		return
	}
	to := cr.memberByKey(key)
	if to == nil {
		cr.dmError(in.from, in.to, reasonOffline)
		return
	}
	if f := encode(dmFrame{typeDM, in.from.nickname, to.nickname, in.text, time.Now().UnixMilli()}); f != nil {
		cr.deliverPrivate(f, to, in.from)
	}
}

// typing relays an ephemeral typing signal: to the room except the sender, or to one
// recipient. Best effort; never stored, logged, answered or echoed. Run-only.
func (cr *Room) typing(in inbound) {
	ts := time.Now().UnixMilli()
	if in.to == "" {
		f := encode(typingFrame{typeTyping, in.from.nickname, ts})
		if f == nil {
			return
		}
		for c := range cr.clients {
			if c != in.from {
				cr.offer(c, f)
			}
		}
		return
	}
	key := foldKey(in.to)
	if key == foldKey(in.from.nickname) {
		return // self: silent (no dm_error)
	}
	to := cr.memberByKey(key)
	if to == nil {
		return // offline: silent
	}
	if f := encode(typingDMFrame{typeTyping, in.from.nickname, to.nickname, ts}); f != nil {
		cr.offer(to, f)
	}
}

// music applies a validated control to the shared player and sends the new state to the
// whole room, sender included. Must-have delivery; never stored or logged. Run-only.
func (cr *Room) music(in inbound) {
	if cr.player == nil {
		return // CHAT-21 rule 2a: no shared player in this room
	}
	now := time.Now()
	if !cr.player.apply(in.music, in.from.nickname, now) {
		return // needs a loaded video
	}
	if f := encode(cr.player.frame(now)); f != nil {
		cr.publish(f)
	}
}

// offer queues msg for member c if its buffer has room and silently skips it otherwise.
// Unlike deliver it never removes c: a skipped typing frame is not a slow client. Run-only.
func (cr *Room) offer(c *Client, msg []byte) {
	select {
	case c.send <- msg:
	default:
	}
}

func (cr *Room) dmError(c *Client, to, reason string) {
	if f := encode(dmErrorFrame{typeDMError, to, reason, time.Now().UnixMilli()}); f != nil {
		cr.deliverPrivate(f, c)
	}
}

// memberByKey returns the member whose foldKey(nickname) is key, or nil. Rooms are
// small, so a scan is fine; taken short-circuits the offline case.
func (cr *Room) memberByKey(key string) *Client {
	if _, ok := cr.taken[key]; !ok {
		return nil
	}
	for c := range cr.clients {
		if foldKey(c.nickname) == key {
			return c
		}
	}
	return nil
}

// deliverPrivate queues msg for each of cs and announces any dropped as slow. Run-only.
func (cr *Room) deliverPrivate(msg []byte, cs ...*Client) {
	for _, c := range cs {
		// An earlier drop in this call may have removed c; its send is closed and a write would panic.
		if _, ok := cr.clients[c]; ok && !cr.deliver(c, msg) {
			cr.dropped = append(cr.dropped, c)
		}
	}
	cr.announceDropped()
}

// drainBroadcast dispatches everything already queued on cr.broadcast without blocking.
func (cr *Room) drainBroadcast() {
	for {
		select {
		case in := <-cr.broadcast:
			cr.dispatch(in)
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
	if len(cr.clients) == 0 && cr.player != nil {
		cr.player.halt(time.Now()) // CHAT-30: nobody left to hear it
	}
}

// deliver queues msg for c without blocking; a client whose buffer is full is
// removed. Announcing that is the caller's job (see publish).
func (cr *Room) deliver(c *Client, msg []byte) bool {
	select {
	case c.send <- msg:
		return true
	default:
		cr.remove(c)
		return false
	}
}

// fanOut delivers msg to every member; slow ones are removed and queued in dropped.
func (cr *Room) fanOut(msg []byte) {
	for c := range cr.clients {
		if !cr.deliver(c, msg) {
			cr.dropped = append(cr.dropped, c)
		}
	}
}

// publish fans msg out and then announces the clients it dropped. Run-only.
func (cr *Room) publish(msg []byte) {
	cr.fanOut(msg)
	cr.announceDropped()
}

// announceDropped announces a leave for every client dropped as slow, including
// clients dropped by those announcements, then clears the list. Run-only.
func (cr *Room) announceDropped() {
	for i := 0; i < len(cr.dropped); i++ { // fanOut below may append
		c := cr.dropped[i]
		if leave := encode(message{Type: typeLeave, From: c.nickname, TS: time.Now().UnixMilli()}); leave != nil {
			cr.fanOut(leave)
		}
	}
	clear(cr.dropped)
	cr.dropped = cr.dropped[:0]
}

// serveConn registers a client for conn and starts its pumps. The caller has
// already upgraded the connection and validated the room.
func (cr *Room) serveConn(conn *websocket.Conn, nickname, avatar string) {
	client := &Client{conn: conn, send: make(chan []byte, sendBufferSize), registered: make(chan struct{}), nickname: nickname, avatar: avatar}
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
