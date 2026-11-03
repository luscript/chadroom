# Exploration: account-sessions

Synthesized from two crombie-explore agents (backend hub; session identity + frontend), 2026-11-03.

## Key findings

- **Identity is per connection today.** `Client` has `conn, send, registered, nickname, avatar, bio`
  (`internal/chat/client.go:37-44`); `Room` keeps `clients map[*Client]struct{}` and
  `taken map[foldKey]struct{}` (`room.go`). Every lookup (DM, typing, `users`, profile) is by nickname text.
- **Chat gets only a display name.** `NicknameFunc func(*http.Request) string`
  (`internal/chat/server.go:17`), called once: `room.serveConn(conn, normalizeNickname(s.nickname(r)), avatar, bio)`
  (`server.go:104`). Production wires `identityNickname` (`cmd/chatroom/main.go:142-155`):
  Name → email local part → email.
- **Session payload** is `{V, Sid, Email, Name, Exp}` (`internal/auth/session.go:25-31`); no `sub`.
  Email is lowercased once at login (`oidc.go:252`), login requires `email_verified` and the allowed
  domain. `Sid` is random per login → not a stable account key. **Email is the account key.**
- **Suffixing** happens in `admit` → `assignNickname` (`room.go:109-113`, `nickname.go:52-77`);
  freed in `remove` (`room.go:378-385`). `memberByKey` is a linear scan (`room.go:339-351`).
- **Join/leave**: `admit` sends private `welcome`, `users`, `history` (+ `player` in `musica`), then
  publishes `join`; `leave` / slow-drop (`deliver` → `remove` → `announceDropped`) publish `leave`
  per client (`room.go:109-135,176-188,399-425`). Run shutdown is silent.
- **DMs** (`direct`, `room.go:250-268`): one recipient via `memberByKey`, `deliverPrivate(f, to, from)`
  → exactly two clients. Self-check by `foldKey`.
- **Typing** (`room.go:272-297`): room-wide skips only `c != in.from` → with accounts it would reach
  the sender's other tabs.
- **Profile** (`room.go:316-322`): updates one `Client`, broadcasts a `profile` frame. Avatar/bio of
  a new connection come from `?avatar=` / `?bio=` query params.
- **Music** keys nothing on clients; `by` is a display string. Player halts when `len(clients)==0`
  (`room.go:382`) — stays correct if `clients` remains per connection.
- **Frontend** (`cmd/chatroom/app.js`) keys everything by nickname: `room.users` Set, avatar/bio
  Maps, DM tabs `convKey(room, nick)`; own name from `welcome` (`room.myName = msg.from`).
  - One entry in `users` per person: works unchanged.
  - DM echo into window B: works (`own = from===myName`, peer = `msg.to`).
  - `profile` from window A into B: works (`adoptOwnProfile`).
  - **Breaks**: in `musica`, `awaitSnapshot` is cleared only by an own `join` (`app.js:~704`); a
    second window would never see one.
  - **Breaks**: a new window reconnects with an empty `myProfile` in the query → would overwrite the
    shared profile unless the server ignores query values for an already-online account.
- **Privacy**: no email reaches the browser today. The account key must never go on the wire or in logs.

## Relevant files

- `internal/chat/room.go` — registry, admit/leave/remove, fan-out, DM, typing, profile.
- `internal/chat/client.go` — Client struct, per-connection rate gates.
- `internal/chat/server.go` — `NicknameFunc`, `HandleWebSocket`, `serveConn` call site.
- `internal/chat/nickname.go` — `foldKey`, `assignNickname`.
- `cmd/chatroom/main.go` — `identityNickname`, `newChatServer` wiring.
- `cmd/chatroom/app.js` — frame handling, `awaitSnapshot`, `myProfile`.
- `internal/chat/{room,server,client}_test.go`, `cmd/chatroom/{main,auth,gifs,playlist}_test.go` —
  `newTestClient`, `queryNickname` resolver, `sessionCookie` helper.
- `openspec/specs/chat/spec.md` — CHAT-02, 04, 12, 17, 22, 23, 26, 38, 41 assume one connection per user.

## Patterns

- Hub pattern: only `Room.Run` touches the registry and closes `send`; no locks.
- Tests: `go test -race`, `httptest` + gin `/ws`; `privateFrames = 3` (welcome, users, history).
- Session tests mint real cookies with `sessionCookie(t, cfg, auth.Identity{Email, Name})`.

## Risks

- Slow-drop / leave of one connection must not announce `leave` while other connections of the
  account remain (false offline, DM tabs closed, profile deleted on peers).
- Nickname (`taken`) must be freed only when the account's last connection goes.
- A failed `admit` of a first connection must not leave an empty account entry.
- DM fan-out must not duplicate frames to the originating tab nor double-deliver.
- Changing the resolver signature touches every test call site of `NewServer` / `newChatServer`.
- Profile rate gate is per connection: N tabs → N updates/s (acceptable for a friends chat).

## Gaps / open questions (resolved in the proposal)

- Key: email vs `sub` → email.
- Second connection's `?avatar` / `?bio` → ignored when the account is already online.
- Typing to own other tabs → no.
- Logout closing all the account's sockets → out of scope (separate backlog item).
