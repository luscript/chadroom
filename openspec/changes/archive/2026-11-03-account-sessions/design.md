# Design: account-sessions

## Technical Approach

The chat package learns a second, private value per connection: the **account key**. The
resolver injected into `chat.NewServer` returns it with the display name; production takes the
session email from `auth.IdentityFrom`. Inside each `Room`, a small **member** record (nickname,
profile, set of connections) is shared by every connection of one account. `clients` stays the
flat per-connection set, so `fanOut`, `send` ownership, the slow-client drop and the player halt
rule do not change. What changes is *who* gets announced, named and addressed: join, leave,
nickname uniqueness, `users`, DM, typing and profile all go through the member.

Everything stays inside `Room.Run` (hub pattern, no locks). The account key is a map key in
`Room.members` and a field on `Client`; it is never encoded, never logged.

One protocol-order change is needed to make the frontend work without an own `join` (see the
music snapshot decision): in `musica`, the `music` snapshot moves **before** `history`, so
`history` is always the last private frame.

## Architecture Decisions

### Decision: Resolver shape

**Choice**: `type IdentityFunc func(r *http.Request) (account, nickname string)` in
`internal/chat/server.go`, replacing `NicknameFunc`. `Server.nickname` becomes `Server.identity`.
`NewServer` still panics on nil (`"chat: NewServer needs an identity resolver"`). The chat package
treats `account` as opaque: no folding or trimming (production already lowercases the email at
login, `oidc.go:252`).

**Alternatives considered**: a `chat.Identity{Account, Nickname}` struct (clearer at call sites, but
collides in name with `auth.Identity` in `main.go` and adds a type for two strings); keeping
`NicknameFunc` and adding a second `AccountFunc` (two resolvers that can disagree, two nil checks).

**Rationale**: one function, one call, named results document the order. Every call site is a test
helper or `main.go`, so the churn is mechanical.

### Decision: Connection without an account key

**Choice**: `HandleWebSocket` resolves the identity after the room lookup and **before** the upgrade.
`account == ""` aborts with `401` and no body, no log. Order becomes: unknown room `404` → no account
`401` → origin `403` (upgrade).

**Alternatives considered**: fall back to a per-connection key (silently restores the old behavior
and hides a wiring bug); reject after upgrade with a close frame (more code, spends a socket).

**Rationale**: spec CHAT-41 says a connection without an account key MUST NOT be admitted.
In production this cannot happen behind `auth.Require`. If it ever does, the request fails without
being admitted.

### Decision: Production resolver

**Choice**: in `cmd/chatroom/main.go`:

```go
// chatIdentity is the chat identity of the authenticated user (CHAT-04, CHAT-41): the session email
// as private account key, and the display name. Both empty without an identity (unreachable behind Require).
func chatIdentity(r *http.Request) (account, nickname string)

// identityNickname: the display name, or the email local part when the account has none.
func identityNickname(id auth.Identity) string // body unchanged, now pure
```

`newChatServer(allowedOrigins []string, identity chat.IdentityFunc)`; `run` passes `chatIdentity`.

**Alternatives considered**: using `Sid` (changes on every login) or `sub` (not in the cookie; needs a
version bump). Both are out of scope per the proposal.

**Rationale**: email is verified, lowercased and stable within the allowed domain. Making
`identityNickname` pure keeps the CHAT-41 name logic and its table test.

### Decision: Test resolvers. Each connection is its own account unless the test says otherwise

**Choice**:
- `internal/chat/server_test.go`: `queryNickname` → `queryIdentity`. Nickname from `?nickname=`.
  Account from `?account=`, or, when absent, a fresh `"conn-<n>"` from a package-level
  `atomic.Int64`.
- `cmd/chatroom/main_test.go`: same `queryIdentity` (unique account per connection; cmd multi-account
  tests use real session cookies instead).
- Hub unit tests (`room_test.go`): `newTestClient(nick, buf)` gives each client a unique account
  (same atomic counter idea). New `newAccountClient(account, nick string, buf int)` for
  multi-connection tests. `client_test.go`'s one `&Client{...}` literal gets `account: "slow"`.
- New dial helper `dialAccount(t, srv, account, nick)` (wraps the existing dial with `&account=`).

**Alternatives considered**: account = nickname by default. Rejected: two empty nicknames would
share account `""` (rejected with 401), and every existing "two bobs → `bob (2)`" test would
turn into "same account" and fail.

**Rationale**: existing tests keep their meaning (every connection is a different person), so
they need no changes apart from the rename. Only the new tests opt into shared accounts.

### Decision: Data structures

**Choice**:

```go
// member is an account present in a room: the identity shared by all its connections (CHAT-04).
// Owned by Room.Run. It never holds the account key (that lives only in Room.members' keys and Client.account).
type member struct {
	nickname string               // assigned once, on the first connection; fixed while the member lives
	avatar   string               // allowed URL or "" (CHAT-28, CHAT-38)
	bio      string               // cleaned or "" (CHAT-37)
	conns    map[*Client]struct{} // registered connections; empty only after the last one is removed
}

type Room struct {
	clients map[*Client]struct{} // unchanged: every registered connection
	members map[string]*member   // NEW: account key -> member
	taken   map[string]*member   // CHANGED: foldKey(nickname) -> member (was a set)
	dropped []*member            // CHANGED: members whose LAST connection a fan-out dropped, not yet announced
	...
}

type Client struct {
	conn, send, registered      // unchanged
	account   string  // NEW: private account key; set before register, immutable; never sent or logged
	nickname  string  // requested until registered is closed, then member.nickname (pumps read it for logError only)
	reqAvatar string  // RENAMED from avatar: requested ?avatar=; seeds a NEW member only, never read after admit
	reqBio    string  // RENAMED from bio: same as reqAvatar
	member    *member // NEW: set by Run in attach; Run-only; NOT cleared on removal
}
```

**Alternatives considered**: storing the profile on every `Client` and copying it to all connections
on change (N copies, they can drift); a member id in the frames (exposes an identifier; out of scope);
`members` only, with `memberByKey` scanning (taken would still be needed for `assignNickname`, so
keeping `taken → *member` gives O(1) lookup for free).

**Rationale**: one source of truth per account. Renaming `avatar`/`bio` to `req*` makes the compiler
flag every place that read the per-connection profile (`relay`, `admit`, `memberValues`, `profile`),
so none can be missed. Keeping `c.member` after removal lets the leave path name a member that
is already gone from the maps.

### Decision: One registry entry point, `attach`, shared by admit and hub tests

**Choice**:

```go
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
```

The ~14 hub tests that seat clients by hand (`cr.clients[c] = …; cr.taken[…] = …`) call
`cr.attach(c)` instead.

**Alternatives considered**: a test-only `seat` helper that duplicates the registry writes (two
copies of the invariant that can drift).

**Rationale**: tests exercise the same registry code as production.

### Decision: Removal and leave announcement. Announce only when the member ends

**Choice**:

```go
// remove drops connection c, closes send and, when c was its member's last connection, removes the
// member and frees its nickname. Run-only; membership guarantees send is still open.
func (cr *Room) remove(c *Client) {
	delete(cr.clients, c)
	m := c.member
	delete(m.conns, c)
	if len(m.conns) == 0 {
		delete(cr.members, c.account)
		delete(cr.taken, foldKey(m.nickname))
	}
	close(c.send)
	if len(cr.clients) == 0 && cr.player != nil { cr.player.halt(time.Now()) } // unchanged
}
```

The leave rule, applied the same way on every path:
- `fanOut` / `deliverPrivate`: `if !cr.deliver(c, msg) && len(c.member.conns) == 0 { cr.dropped = append(cr.dropped, c.member) }`.
- `announceDropped`: one `leave` with `from = m.nickname` per entry (the loop still handles
  members dropped by the leave fan-out itself).
- `leave(c)` (readPump unregister): drain as today; if `c` is still registered, `remove(c)`, then
  publish `leave` **only if** `len(c.member.conns) == 0`.
- `admit` private delivery failure: `deliver` → `remove`; never announced (see admit).
- Run shutdown: `remove` for each client, silent (unchanged).

A member reaches zero connections exactly once, and a new connection after that creates a new
`member`. So each member is appended to `dropped` at most once, even when two of its connections
are dropped in the same fan-out. That gives the spec's "leave at most once per member presence"
without a separate "announced" flag.

**Alternatives considered**: `remove` returning `last bool` (all callers would need to thread it; the
`len(conns)==0` check after the call says the same thing); a per-member `announced` flag (extra state).

**Rationale**: the existing slow-client paths stay the same shape; only the "should this be
announced" test changes, from "a client was dropped" to "a member ended".

### Decision: Admit, first vs later connection

**Choice**:

```go
func (cr *Room) admit(c *Client) {
	first := cr.attach(c)
	close(c.registered)
	m := c.member
	// private: welcome{From: m.nickname, Avatar: m.avatar, Bio: m.bio}, users, [music], history
	for _, f := range private {
		if f == nil || !cr.deliver(c, f) {
			return // first: member removed, name freed, never announced. later: only c is gone.
		}
	}
	if first { cr.publish(join{From: m.nickname, Avatar: m.avatar, Bio: m.bio}) }
}
```

- First connection: unchanged behavior (seeded from `?avatar=`/`?bio=`, private frames, public join).
- Later connection: requested nickname, `reqAvatar` and `reqBio` are ignored. The welcome carries
  the member's current values. No `join`, no `profile`.
- Failed private delivery on a later connection removes only that connection. The member keeps at
  least one other connection, because nothing else runs between `attach` and the private
  deliveries (they target only `c`). So nothing is announced and the name stays taken.

**Alternatives considered**: a private `join` to the new connection only (would make the old frontend
work as-is, but spec CHAT-02 says no client receives a `join` for it).

**Rationale**: matches CHAT-02/11/12 exactly with one boolean.

### Decision: Lookups, DM, typing, profile

**Choice**:
- `userNames()`: nicknames of `cr.members` values, sorted by `foldKey` (one per account).
- `memberValues(get func(*member) string)`: over `cr.members`.
- `memberByKey(key) *member`: `return cr.taken[key]` (scan removed).
- `relay`: `From: in.from.member.nickname`, `Avatar: in.from.member.avatar`.
- `music`: `apply(in.music, in.from.member.nickname, now)`.
- `direct`:
  ```go
  from, to := in.from.member, cr.memberByKey(foldKey(in.to))
  if to == from { dmError(in.from, in.to, reasonSelf); return }  // only the originating connection
  if to == nil  { dmError(in.from, in.to, reasonOffline); return }
  // encode once; then
  cs := make([]*Client, 0, len(to.conns)+len(from.conns))
  for c := range to.conns   { cs = append(cs, c) }
  for c := range from.conns { cs = append(cs, c) }
  cr.deliverPrivate(f, cs...)
  ```
  `to` and `from` are different members, so their connection sets do not overlap (no duplicates).
  The slice is built before delivery, and `deliverPrivate` already skips a connection removed earlier
  in the same call.
- `typing` room-wide: `if c.member != in.from.member { offer(c, f) }`.
- `typing` DM: `to := memberByKey(key)`; `nil` or `to == in.from.member` → silent; else `offer` to
  every `c` in `to.conns`.
- `profile`: encode first, then `m.avatar, m.bio = in.avatar, in.bio` on `in.from.member`,
  then `publish` (reaches every connection, the sender's included).
- `dispatch`'s guard (`in.from` in `clients`) is unchanged: a removed connection's queued frames are
  discarded even if its member is still online, exactly as for a slow drop today.

**Alternatives considered**: echoing a DM only to the originating connection (the other window would
miss its own sent messages; spec CHAT-22 requires all).

**Rationale**: every rule reads "the member" where it used to read "the client". No new helpers
beyond what DM fan-out needs.

### Decision: Music snapshot moves before history; the frontend closes the snapshot slot on `history`

**Choice**:
- Server (`admit`): private order becomes `welcome`, `users`, [`music`], `history`.
- Frontend: the "no snapshot → reset" block moves from the own-`join` branch to the `history`
  branch:
  ```js
  if (msg.type === 'history') {
    if (Array.isArray(msg.messages)) renderHistory(room, msg.messages);
    // history is the last private frame: a snapshot, if any, came before it. None means the server
    // has no track (it may have restarted).
    if (room.id === MUSIC_ROOM && music.awaitSnapshot) closeSnapshotSlot();
    return;
  }
  ```
  `closeSnapshotSlot()` holds the existing reset body (`awaitSnapshot=false; srv=null; loadedSeq=…;
  renderQueue(); renderMusic(); reconcile();`). The `join` case no longer touches `music`. The
  welcome comment and the `awaitSnapshot` field comment change to say "before history".

**Alternatives considered**:
1. Keep the order and close the slot on "the first non-`music` frame after `history`". Not
   deterministic: in a quiet room nothing may follow `history`. A second window that reconnects
   after a server restart (the common deploy case with two tabs) would keep its stale track and
   play it.
2. Reset on `history` with the current order. The snapshot then arrives right after a reset with
   `loadedSeq = 0`, so every reconnect that has a track would reload the embed (an audible glitch).
3. Always send a `music` frame in `musica`, even without a video. This changes "no music frame while
   no video is current" and the frontend validation, which is a bigger protocol change.

**Rationale**: `history` is always sent, and private frames are queued in one Run step on one
socket, so "last private frame" is reliable for first and later connections alike. The frontend
does not care about the order (music frames add no chat lines, and `renderHistory` does not read
music state). Version skew is harmless: an old page on a new server still gets the snapshot before
its own join.

**Spec impact (must be reflected in the delta)**: CHAT-30's sentence "right after its `history`
frame (CHAT-11 order: `welcome`, `users`, `history`, `music`)" becomes "right after its `users`
frame and before `history` (order: `welcome`, `users`, `music`, `history`)". CHAT-11's "followed by
its `users` and `history`" stays true. The delta at `specs/chat/spec.md` currently lacks this
MODIFIED CHAT-30 entry.

### Decision: Frontend adopts the profile from `welcome`

**Choice**: in the `welcome` case, after `setMemberProfile(...)`:

```js
if (room.resendProfile) { room.resendProfile = false; sendProfile(room); } // a pending local edit wins; its echo is adopted
else adoptOwnProfile(room);                                                  // CHAT-43: the member's profile is ours
```

**Alternatives considered**: adopting only when the welcome carries a non-empty profile (a member
can really have an empty profile; you can't tell "inherited empty" from "nothing").

**Rationale**: a fresh second window starts with an empty `myProfile` (CHAT-39, page memory only).
Without adoption, its editor and its future `/ws` URLs would show and send an empty profile, and
the next save would wipe the shared one. For a first connection, the adoption just copies back what
the page sent, now cleaned, which is what `adoptOwnProfile` already does for `profile` echoes.
DM echo, own `profile` frames and `users` need no frontend change (already handled by
`msg.from === room.myName`).

## Data Flow

### First connection of an account (unchanged shape)

```
Browser W1        HandleWebSocket            Room.Run (admit)                 Room
   │ GET /ws ─────────▶│ room? identity(r)                                     
   │                   │ account="" → 401                                      
   │                   │ upgrade, serveConn(account, nick, avatar, bio)         
   │                   │──── register ───────────▶ attach: members[acc]==nil    
   │                   │                           assign name, new member, first=true
   │                   │                           close(registered)            
   │◀──────────────────────────── welcome, users, [music], history (private)   
   │                   │                           ok? publish(join Ana) ──────▶ all conns (W1 included)
   │                   │                           failed? remove: member gone, name freed, no join
```

### Later connection of the same account

```
Browser W2        HandleWebSocket            Room.Run (admit)                 Room
   │ GET /ws ─────────▶│ same account                                          
   │                   │──── register ───────────▶ attach: members[acc]=m (first=false)
   │                   │                           c.nickname=m.nickname; ?avatar/?bio ignored
   │◀──────────────── welcome{Ana, m.avatar, m.bio}, users(Ana once), [music], history
   │                   │                           no join, no profile          
   │ history: closeSnapshotSlot() if no music came; welcome: adoptOwnProfile()  
   │                   │                           failed? remove(c): m still has W1 → silent
```

### Leave on last connection (close and slow drop)

```
W1 closes:  readPump ─ unregister ─▶ leave(W1): drain; remove(W1); m.conns={W2} → no leave
W2 closes:  readPump ─ unregister ─▶ leave(W2): drain; remove(W2); m.conns={}  → members/taken freed
                                      publish(leave Ana) ──▶ every remaining conn (none of Ana's)

Slow drop during publish(x):
  fanOut ─ deliver(W2) full ─▶ remove(W2); m.conns={W1}      → not appended to dropped
  fanOut ─ deliver(W1) full ─▶ remove(W1); m.conns={}        → dropped=[m]   (exactly once)
  announceDropped ─▶ fanOut(leave Ana)   (may drop more members: same rule, same loop)
```

### DM fan-out

```
A1 ── dm{to:"b"} ─▶ broadcast ─▶ dispatch(A1 registered) ─▶ direct
   from = A1.member (A), to = taken[foldKey("b")] (B)
   to == from → dm_error self → A1 only
   to == nil  → dm_error offline → A1 only
   encode once (id = nextID+1)
   deliverPrivate(f, B1, B2, A1, A2)   one identical frame each; C gets nothing
     └ a full buffer drops only that conn; leave announced at the end only if it was B's/A's last
```

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/chat/server.go` | Modify | `NicknameFunc` → `IdentityFunc`; `Server.identity`; nil panic message; `HandleWebSocket` resolves identity before upgrade, `401` on empty account; `serveConn(conn, account, normalizeNickname(nick), avatar, bio)`. |
| `internal/chat/room.go` | Modify | `member` type; `members`, `taken map[string]*member`, `dropped []*member`; `attach`; `admit` (first/later, music before history); `remove` (member cleanup); `leave`, `fanOut`, `deliverPrivate`, `announceDropped` (last-connection rule); `userNames`, `memberValues`, `memberByKey`, `relay`, `music`, `direct`, `typing`, `profile` through `c.member`; `serveConn` sets `account`, `reqAvatar`, `reqBio`. |
| `internal/chat/client.go` | Modify | `Client`: add `account`, `member`; rename `avatar`/`bio` → `reqAvatar`/`reqBio`; comments on ownership. Pumps unchanged (`logError` still logs only nickname). |
| `internal/chat/nickname.go` | Modify | `assignNickname` reads `taken` as `map[string]*member` (lookup unchanged in shape); comment. |
| `cmd/chatroom/main.go` | Modify | `chatIdentity(r) (account, nickname)`; `identityNickname(id auth.Identity) string` pure; `newChatServer(…, identity chat.IdentityFunc)`; `run` wires `chatIdentity`. |
| `cmd/chatroom/app.js` | Modify | `history` closes the music snapshot slot (`closeSnapshotSlot`), removed from `join`; `welcome` adopts own profile unless a resend is pending; comments. |
| `internal/chat/room_test.go` | Modify | `newTestClient` unique account; `newAccountClient`; manual seating → `cr.attach`; `TestAssignNickname` seeds `taken` with `&member{}`; `avatarClient`/`bioClient` set `req*`; `TestMusicNewcomerSnapshot` order; new tests below. |
| `internal/chat/server_test.go` | Modify | `queryIdentity`; `dialAccount`; `TestNicknameResolver` → identity resolver; new tests. |
| `internal/chat/client_test.go` | Modify | `queryIdentity`; `&Client{…, account: "slow"}`; WS-level multi-tab test. |
| `cmd/chatroom/main_test.go`, `gifs_test.go`, `playlist_test.go` | Modify | `queryNickname` → `queryIdentity` (rename only). |
| `cmd/chatroom/auth_test.go` | Modify | `authRouter` uses `chatIdentity`; `TestIdentityNickname` → checks `chatIdentity` (account = email, nickname as before, no identity → `"",""`); new session-level tests; `:411` rename. |
| `openspec/changes/account-sessions/specs/chat/spec.md` | Modify (spec phase owner) | Add MODIFIED CHAT-30 order sentence (music before history). Not edited by this design. |

### Test plan

New, hub unit (`room_test.go`):
- `TestSameAccountSharesMember`: second conn welcome = same name, no `join` anywhere (marker frame), a third client's `users` lists the account once, both conns get room `chat`.
- `TestSameAccountNeverSuffixed` and `TestNameKeptWhileOneConnectionRemains` (CHAT-12 `Bob (3)` scenario).
- `TestLeaveOnlyOnLastConnection`: unregister one → no leave (marker); unregister last → exactly one leave.
- `TestSlowDropOfOneConnectionIsSilent`: the other connection keeps receiving; no leave.
- `TestBothConnectionsDroppedInOneFanOutLeaveOnce`: deduplication of `dropped`.
- `TestLaterConnectionFailedAdmitIsSilent`: buffer `privateFrames-1`; member and name stay; no leave.
- Extend the existing failed-first-admit test (`:842`): `len(cr.members) == 0` afterwards.
- `TestLaterConnectionInheritsProfile`: different `req*` ignored; welcome has the member profile; no `profile` frame.
- `TestDMFanOutToAllConnections`: A1, A2, B1, B2 get one identical frame (same id/ts); C gets nothing; no extra frame on A1.
- `TestDMToOwnAccountFromOtherTab`: `dm_error self` on the originator only.
- `TestDMSlowRecipientConnection`: one of B's conns dropped; B's other conn and A's conns still get it; no leave.
- `TestTypingExcludesOwnConnections` (room), `TestTypingDMReachesAllRecipientConnections`, `TestTypingDMToOwnAccountSilent`.
- `TestProfileSyncAcrossConnections`: all of the room's conns get `profile`; a later conn's welcome shows it.
- `TestPlayerKeepsPlayingWhileAConnectionRemains`: one of two conns leaves; no halt.
- `TestMusicNewcomerSnapshot` (changed): order `welcome, users, music, history`; also a later connection gets the snapshot.

New, chat integration (`server_test.go` / `client_test.go`):
- `TestIdentityResolver` (replaces `TestNicknameResolver`): `?nickname=` ignored.
- `TestEmptyAccountRejected`: `401`, no upgrade, room untouched.
- `TestTwoTabsOverWebSocket`: real close of one tab → the observer sees no leave (marker); close of the second → leave.

New, cmd (`auth_test.go`, real session cookies):
- CHAT-41 "Same account in two tabs": same cookie twice → both welcomes `Ana Pérez`; the first gets no `join` for the second.
- CHAT-41 "Different accounts with the same name": `ana.perez@` and `ana.p@` → `Ana Pérez (2)`.

Unchanged in meaning (rename only): every "two bobs" test (`TestNicknameUniquePerRoom`, `:344`, `:525`, `:654`, `server_test.go:139`). Each connection is still a separate account by default.

Frontend (no JS test harness): manual check with two windows of one account in `musica`. The second
window gets the snapshot and follows pause. Restart the server with two tabs open: the second tab
must clear its track when no snapshot comes. A DM sent from W1 shows in W2 with no unread mark. A
profile edit in W1 updates W2's editor. A fresh W2 does not wipe the profile.

## Risks and edge cases

- **Missed removal path**: all removals go through `remove`, and every announcement checks
  `len(c.member.conns) == 0`. Tests cover close, slow drop (one / both / last), failed admit
  (first / later) and shutdown.
- **Self-DM across tabs**: resolved by member identity (`to == from`), not by name, so `"ANA"` from
  W2 is still `self`. Only the originating connection gets the `dm_error`.
- **Recipient shares the sender's browser**: impossible in practice. One cookie means one account
  per browser profile, and the sender's and recipient's connection sets never overlap, so no
  duplicate frames.
- **Slow drop of one tab**: silent, while the other tab stays online. Frames queued by the dropped
  tab are discarded (same as today's slow drop).
- **History replay per connection**: each connection gets the room `history` on admit. DMs are not
  replayed (CHAT-24), so W2 opened later does not see earlier DMs. That is accepted (DM replay is out
  of scope).
- **Nickname frozen while online**: a renamed Google profile, or a new login with another name,
  applies only after all of the account's connections in that room close.
- **Profiles are per room**: a fresh window can be the first connection in one room (seeded empty)
  and a later connection in another (inherits). `myProfile` then ends up with whichever `welcome` is
  processed last. The next save sends to every room and they converge again. Accepted for a friends
  chat.
- **N tabs × per-connection rate gates**: an account gets N× the typing and profile budget. This is
  accepted per the proposal.
- **Logout does not close other tabs' sockets**: out of scope (backlog).
- **Privacy**: the account key lives only in `Client.account` and the `Room.members` keys. It is
  never encoded, never logged (`logError` logs the nickname; `HandleWebSocket` logs nothing about
  it), and never in panics (no `%v` of `Client`).
- **Old page on new server**: the snapshot now arrives before the own join, which the old JS
  already handles. Only a later connection running stale JS keeps the old limitation until reload.
