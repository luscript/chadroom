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
	maxBioRunes           = 160
)

// member is an account present in a room: the identity shared by all its connections (CHAT-04).
// Owned by Room.Run. It never holds the account key (that lives only in Room.members' keys and Client.account).
type member struct {
	nickname string               // assigned once, on the first connection; fixed while the member lives
	avatar   string               // allowed URL or "" (CHAT-28, CHAT-38)
	bio      string               // cleaned or "" (CHAT-37)
	conns    map[*Client]struct{} // registered connections; empty only after the last one is removed
}

// Room manages clients and messages.
// Only Run touches clients and closes client.send, so membership in the map
// means "send is still open" and a channel can never be closed twice.
type Room struct {
	id         string               // immutable; used for lookup and logs, never sent on the wire
	clients    map[*Client]struct{} // every registered connection
	members    map[string]*member   // account key -> member
	taken      map[string]*member   // foldKey(nickname) -> member
	history    []message            // last historySize chat messages, oldest first
	dropped    []*member            // members whose last connection a fan-out dropped, not yet announced
	broadcast  chan inbound         // every inbound kind (chat, direct, typing and profile), so a client's frames stay ordered
	register   chan *Client
	unregister chan *Client
	done       chan struct{} // closed by Run when it exits
	player     *player       // nil: this room has no shared player (CHAT-30); set only by NewMusicRoom

	// validAvatar is set by NewServer before the room is served and read-only after; Run never
	// reads it. nil: every avatar is dropped (CHAT-28).
	validAvatar func(string) bool

	// Run-only (CHAT-35). One id sequence per room, shared by chat and dm frames.
	nextID     int64               // last id assigned; 0 before the first message
	targets    [replyTargets]quote // ring of quotable room messages; a zero ID marks an empty slot
	targetNext int                 // slot the next target overwrites

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
		members:        make(map[string]*member),
		taken:          make(map[string]*member),
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

// allowedAvatar reports whether s is a non-empty avatar this room may show (CHAT-28).
// Read-only: safe from any goroutine once the room is served.
func (cr *Room) allowedAvatar(s string) bool {
	return s != "" && cr.validAvatar != nil && cr.validAvatar(s)
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

// attach registers connection c under its account's member, creating the member (and assigning
// its nickname) on the account's first connection in the room. Reports whether it did. Run-only.
func (cr *Room) attach(c *Client) (first bool) {
	m := cr.members[c.account]
	if m == nil {
		name := cr.assignNickname(c.nickname)
		m = &member{nickname: name, avatar: c.reqAvatar, bio: c.reqBio, conns: make(map[*Client]struct{})}
		cr.members[c.account] = m
		cr.taken[foldKey(name)] = m
		first = true
	}
	m.conns[c] = struct{}{}
	c.member, c.nickname = m, m.nickname
	cr.clients[c] = struct{}{}
	return first
}

// admit registers c and sends it welcome, users, music and history privately. Only the first
// connection of an account is then announced with a join; a later one inherits the member's
// nickname and profile. A newcomer dropped on the way is never announced, so nobody needs a
// leave for it.
func (cr *Room) admit(c *Client) {
	first := cr.attach(c)
	close(c.registered) // publishes the final nickname to the pumps
	m := c.member
	now := time.Now()
	ts := now.UnixMilli()
	private := [][]byte{
		encode(message{Type: typeWelcome, From: m.nickname, Avatar: m.avatar, Bio: m.bio, TS: ts}),
		encode(usersFrame{Type: typeUsers, Users: cr.userNames(),
			Avatars: cr.memberValues(func(m *member) string { return m.avatar }),
			Bios:    cr.memberValues(func(m *member) string { return m.bio }), TS: ts}),
	}
	if cr.player != nil && cr.player.video != "" {
		private = append(private, encode(cr.player.frame(now))) // CHAT-30: state (current track, seq and queue) before history; no video yet means nothing to send
	}
	private = append(private, encode(historyFrame{typeHistory, cr.history, ts})) // always the last private frame
	for _, f := range private {
		if f == nil || !cr.deliver(c, f) {
			return // first: member removed, name freed, never announced. later: only c is gone
		}
	}
	if !first {
		return
	}
	if join := encode(message{Type: typeJoin, From: m.nickname, Avatar: m.avatar, Bio: m.bio, TS: ts}); join != nil {
		cr.publish(join)
	}
}

// userNames returns the members' nicknames sorted by foldKey (unique among
// members, so the order is total). Never nil.
func (cr *Room) userNames() []string {
	names := make([]string, 0, len(cr.members))
	for _, m := range cr.members {
		names = append(names, m.nickname)
	}
	slices.SortFunc(names, func(a, b string) int { return strings.Compare(foldKey(a), foldKey(b)) })
	return names
}

// memberValues maps members' nicknames to get(member), only for members whose value is
// non-empty. nil when nobody has one, so the users frame omits the field (CHAT-28, CHAT-37). Run-only.
func (cr *Room) memberValues(get func(*member) string) map[string]string {
	var out map[string]string
	for _, m := range cr.members {
		if v := get(m); v != "" {
			if out == nil {
				out = make(map[string]string)
			}
			out[m.nickname] = v
		}
	}
	return out
}

// remember appends m to the history window, evicting the oldest past historySize. Run-only.
func (cr *Room) remember(m message) {
	if len(cr.history) == historySize {
		copy(cr.history, cr.history[1:])
		cr.history = cr.history[:historySize-1]
	}
	cr.history = append(cr.history, m)
}

// leave removes c and, when it was its member's last connection, announces it. Messages c queued before disconnecting
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
	if len(c.member.conns) > 0 {
		return // the member is still present through another connection
	}
	if leave := encode(message{Type: typeLeave, From: c.member.nickname, TS: time.Now().UnixMilli()}); leave != nil {
		cr.publish(leave)
	}
}

// relay stores and fans a chat message out, discarding it if its sender is no longer a member.
func (cr *Room) relay(in inbound) {
	if _, ok := cr.clients[in.from]; !ok {
		return // sender was removed; its name may belong to someone else now
	}
	// The target is resolved before the new message joins the ring, so a reply_to equal
	// to the message's own id can't match. The id is used only once encode succeeds.
	m := message{Type: typeChat, From: in.from.member.nickname, Text: in.text, Avatar: in.from.member.avatar, TS: time.Now().UnixMilli(),
		ID: cr.nextID + 1, Reply: cr.replyTarget(in.replyTo)}
	if chat := encode(m); chat != nil {
		cr.nextID = m.ID
		cr.remember(m)
		cr.keepTarget(quote{ID: m.ID, From: m.From, Text: quoteText(m.Text)})
		cr.publish(chat)
	}
}

// keepTarget adds q to the reply ring, overwriting the oldest entry. Run-only.
func (cr *Room) keepTarget(q quote) {
	cr.targets[cr.targetNext] = q
	cr.targetNext = (cr.targetNext + 1) % replyTargets
}

// replyTarget returns a copy of the quote for room message id, or nil when id is 0,
// unknown, evicted or not a room message (CHAT-35). Run-only.
func (cr *Room) replyTarget(id int64) *quote {
	if id < 1 {
		return nil
	}
	for i := range cr.targets {
		if cr.targets[i].ID == id {
			q := cr.targets[i]
			return &q
		}
	}
	return nil
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
	case inboundProfile:
		cr.profile(in)
	default:
		cr.relay(in)
	}
}

// direct delivers a DM to every connection of its recipient and echoes it to every connection
// of the sender. It never stores or logs it.
func (cr *Room) direct(in inbound) {
	from, to := in.from.member, cr.memberByKey(foldKey(in.to))
	if to == from {
		cr.dmError(in.from, in.to, reasonSelf) // by member, not name: any tab of the account
		return
	}
	if to == nil {
		cr.dmError(in.from, in.to, reasonOffline)
		return
	}
	// reply_to is forwarded without lookup: nothing about a DM is kept (CHAT-24).
	d := dmFrame{Type: typeDM, From: from.nickname, To: to.nickname, Text: in.text, TS: time.Now().UnixMilli(),
		ID: cr.nextID + 1, ReplyTo: in.replyTo}
	if f := encode(d); f != nil {
		cr.nextID = d.ID
		// The members differ, so their connection sets do not overlap: no duplicate frames.
		cs := make([]*Client, 0, len(to.conns)+len(from.conns))
		for c := range to.conns {
			cs = append(cs, c)
		}
		for c := range from.conns {
			cs = append(cs, c)
		}
		cr.deliverPrivate(f, cs...)
	}
}

// typing relays an ephemeral typing signal: to the room except the sender's own connections, or
// to every connection of one recipient. Best effort; never stored, logged, answered or echoed. Run-only.
func (cr *Room) typing(in inbound) {
	ts := time.Now().UnixMilli()
	from := in.from.member
	if in.to == "" {
		f := encode(typingFrame{typeTyping, from.nickname, ts})
		if f == nil {
			return
		}
		for c := range cr.clients {
			if c.member != from {
				cr.offer(c, f)
			}
		}
		return
	}
	to := cr.memberByKey(foldKey(in.to))
	if to == nil || to == from {
		return // offline or self: silent
	}
	if f := encode(typingDMFrame{typeTyping, from.nickname, to.nickname, ts}); f != nil {
		for c := range to.conns {
			cr.offer(c, f)
		}
	}
}

// music applies a validated control to the shared player and sends the new state to the
// whole room, sender included. Must-have delivery; never stored or logged. Run-only.
func (cr *Room) music(in inbound) {
	if cr.player == nil {
		return // CHAT-21 rule 2a: no shared player in this room
	}
	now := time.Now()
	if !cr.player.apply(in.music, in.from.member.nickname, now) {
		return // not applicable in the current state (CHAT-30)
	}
	if f := encode(cr.player.frame(now)); f != nil {
		cr.publish(f)
	}
}

// profile replaces the sender's member profile and announces it to the room, every
// connection of the sender included (CHAT-38). Must-have delivery; never stored. Run-only.
func (cr *Room) profile(in inbound) {
	m := in.from.member
	f := profileFrame{Type: typeProfile, From: m.nickname, TS: time.Now().UnixMilli(), Avatar: in.avatar, Bio: in.bio}
	if b := encode(f); b != nil { // encode first: state changes only if the update can be announced
		m.avatar, m.bio = in.avatar, in.bio
		cr.publish(b)
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

// memberByKey returns the member whose foldKey(nickname) is key, or nil. Run-only.
func (cr *Room) memberByKey(key string) *member {
	return cr.taken[key]
}

// deliverPrivate queues msg for each of cs and announces any dropped as slow. Run-only.
func (cr *Room) deliverPrivate(msg []byte, cs ...*Client) {
	for _, c := range cs {
		// An earlier drop in this call may have removed c; its send is closed and a write would panic.
		if _, ok := cr.clients[c]; ok && !cr.deliver(c, msg) && len(c.member.conns) == 0 {
			cr.dropped = append(cr.dropped, c.member)
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

// remove drops connection c, closes send and, when c was its member's last connection, removes
// the member and frees its nickname. Run-only; membership guarantees send is still open.
func (cr *Room) remove(c *Client) {
	delete(cr.clients, c)
	m := c.member
	delete(m.conns, c)
	if len(m.conns) == 0 {
		delete(cr.members, c.account)
		delete(cr.taken, foldKey(m.nickname))
	}
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

// fanOut delivers msg to every connection; slow ones are removed, and a member whose last
// connection that was is queued in dropped (once: it reaches zero connections only once).
func (cr *Room) fanOut(msg []byte) {
	for c := range cr.clients {
		if !cr.deliver(c, msg) && len(c.member.conns) == 0 {
			cr.dropped = append(cr.dropped, c.member)
		}
	}
}

// publish fans msg out and then announces the clients it dropped. Run-only.
func (cr *Room) publish(msg []byte) {
	cr.fanOut(msg)
	cr.announceDropped()
}

// announceDropped announces a leave for every member dropped as slow, including
// members dropped by those announcements, then clears the list. Run-only.
func (cr *Room) announceDropped() {
	for i := 0; i < len(cr.dropped); i++ { // fanOut below may append
		m := cr.dropped[i]
		if leave := encode(message{Type: typeLeave, From: m.nickname, TS: time.Now().UnixMilli()}); leave != nil {
			cr.fanOut(leave)
		}
	}
	clear(cr.dropped)
	cr.dropped = cr.dropped[:0]
}

// serveConn registers a client for conn and starts its pumps. The caller has
// already upgraded the connection and validated the room.
func (cr *Room) serveConn(conn *websocket.Conn, account, nickname, avatar, bio string) {
	client := &Client{conn: conn, send: make(chan []byte, sendBufferSize), registered: make(chan struct{}),
		account: account, nickname: nickname, reqAvatar: avatar, reqBio: bio}
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
