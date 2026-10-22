# Proposal: Multiple rooms

## Intent

The chat has a single room ("Sala general"). Friends want separate conversations by topic
without leaving the main one. This change adds a **fixed, server-defined set of rooms**, lets
a user **be in several rooms at once**, and keeps the **online users list and last-50
history per room**. Backlog group `chat-features` (second half: multiple rooms; typing
indicator stays in the backlog).

## Decisions (orchestrator, automatic mode — 2026-10-22)

User-decided: fixed set of rooms; several rooms per user; users and history per room.

- **One WebSocket per joined room** (`GET /ws?room=<id>&nickname=<name>`), not a multiplexed
  socket. Each `Room` keeps its own `Run` goroutine, registry, history and nickname set, and
  each `Client` belongs to exactly one `Room`. The hub invariants stay as they are: only `Run`
  closes `send`, and there are no locks. A multiplexed socket was rejected because it needs an
  inbound JSON protocol, a `room` field on every frame, and a shared `send` channel that rooms
  could double-close (see `exploration.md`).
- **The wire protocol stays the same.** Frames get no `room` field because the socket
  identifies the room. The `welcome`/`users`/`history`/`chat`/`join`/`leave` shapes and their
  exact-field tests stay as they are.
- **Fixed rooms**: `general`, `juegos`, `musica`, in that order, as one Go slice in
  `cmd/chatroom/main.go`. Changing the set needs a code change. There is no env var (YAGNI).
  Room ids are lowercase ASCII and also serve as display names (`#general`).
- **The page gets the list from the server**: `GET /rooms` → `{"rooms":["general","juegos","musica"]}`.
  This avoids a third constant mirrored by hand in JS (as `IMAGE_HOSTS` already is).
- **Room validation**: the `room` query param MUST match a configured id exactly. An unknown
  room returns **404 before Upgrade**. If `room` is missing, the server uses the default room
  (`general`, the first in the list). This keeps old pages and the existing bare-`/ws` tests
  working.
- **Nicknames are unique per room.** One person may be `Bob` in one room and `Bob (2)` in
  another. Each socket's `welcome` tells the page the name assigned in that room, so it marks
  "vos" correctly per room. A cross-room identity is out of scope.
- **Joining rooms**: after entering a nickname the user joins `general`. A room list shows
  every room. Clicking a room that hasn't been joined joins it (opens its socket) and makes it
  active. Each joined room can be left (closes its socket), and the user can leave all of
  them. "Salir" still leaves everything and goes back to the nickname screen.
- **Per-room client state**: socket, reconnect backoff, assigned nickname, message list
  (one `<ol>` per room, only the active one visible), users set, `lastAuthor`, and an unread
  counter for inactive rooms. The header shows the active room's name, users button and
  status. The "Reconectado" notice is per room.
- **Origin check and upgrader are shared**: one upgrader/origin policy serves every room
  (SRV-01 unchanged), so it is not duplicated per `Room`. The exact code structure is decided
  in `design.md`.
- **Shutdown** cancels one context and waits for every room's `Run` before `srv.Shutdown`
  (SRV-03 extended to all rooms).

## Scope

### In Scope
- Server: fixed room set, room registry, `GET /rooms`, `room` query param with validation and
  a default, per-room `Run`, shutdown waits for all rooms.
- Hub: a room name/id, otherwise unchanged behavior within each room.
- Page: room list with joined/active/unread state, join/leave per room, one socket per
  joined room with independent reconnect, per-room transcript and users popover, header
  shows the active room.
- Tests (`go test -race ./...`): per-room isolation of history/users/nicknames, unknown room
  → 404, default room, `GET /rooms`, shutdown waits for all rooms. Spec deltas for chat
  and server.

### Out of Scope
- User-created/deleted rooms, private rooms, room config via env.
- A single multiplexed socket; a global (cross-room) nickname identity.
- Remembering joined rooms across page reloads.
- Capping live DOM per room, rate limiting, max clients (existing backlog items).
- Typing indicator (backlog).

## Approach

1. `internal/chat`: give `Room` an id. Move the upgrader/origin check and room lookup into a
   small server-level type that owns `map[id]*Room` and serves `GET /ws` (validate `room`
   before Upgrade, 404 if unknown, default if missing). `Room`'s registration/pumps stay
   unchanged.
2. `cmd/chatroom/main.go`: declare the room slice, build the registry, run every room, wait
   for all on shutdown, add `GET /rooms`.
3. `app.js`/`index.html`/`app.css`: per-room state object keyed by id; room list (tabs or
   sidebar, following the existing `role=tablist` pattern); socket/reconnect per room;
   dispatch routes frames to the room that owns the socket; unread badge; leave controls.
4. Tests and spec deltas.

Type/function names, the exact HTTP handler shape, the UI layout (sidebar vs tab strip, mobile)
and test helper changes go in `design.md`.

## Constraints

- Keep hub invariants: only `Run` touches registry/history and closes `send`; no locks; no
  shared mutable state between rooms (only the immutable room list).
- Secure by default: room id validated against the allowlist before Upgrade; origin policy
  (SRV-01) applies to every room; room names rendered with `textContent`.
- No new dependencies. CSP and no-inline rules (SRV-05) unchanged: no new hosts, no inline
  code or `style=`. Any new asset needs embedding + route + test (prefer none).
- Spanish (voseo) UI copy, existing CSS tokens and class conventions.

## Affected Areas

| Area | Files | Change |
|------|-------|--------|
| Hub / server handler | `internal/chat/room.go` (+ possibly a new small file) | room id, room registry + `/ws` handler with room validation, shared upgrader/origin check |
| Entrypoint | `cmd/chatroom/main.go` | fixed room set, run/wait all rooms, `GET /rooms` |
| Frontend | `cmd/chatroom/app.js`, `index.html`, `app.css` | room list, per-room sockets/state/panes, unread, join/leave |
| Tests | `internal/chat/*_test.go`, `cmd/chatroom/main_test.go` | isolation, validation, default room, `/rooms`, shutdown |
| Specs | `openspec/specs/chat/spec.md`, `openspec/specs/server/spec.md` | room-scoped wording of CHAT reqs; new rooms reqs; SRV-01/03 extended |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Room param used without validation (unbounded rooms, injection into logs/UI) | Medium | Exact match against the fixed set before Upgrade; 404 otherwise; test |
| Refactoring the handler off `Room` breaks ~40 white-box tests | Medium | Keep `NewRoom`/`Run`/channel API; only move the HTTP/upgrade part; update `startServer` helper once |
| Shutdown leaves a room's clients connected | Low | Wait on every room's done before `srv.Shutdown`; test with clients in 2 rooms |
| Client reconnect logic multiplied by N sockets (storm after outage) | Low | Bounded by 3 rooms; independent jittered backoff per socket |
| Different nickname suffix per room confuses users | Low | Accepted and documented; "vos" marking uses each room's `welcome` |
| No JS tests for the routing/unread logic | Medium | Manual checklist in verify + Go string assertions for new DOM ids |

## Rollback Plan

Single binary (server + embedded page), no persisted data: revert the change's commits and
redeploy. Old pages keep working against the new server meanwhile, because bare `/ws` maps to
`general`.

## Dependencies

- Builds on `online-users-and-history` (archived; branch `feat/online-users-and-history`,
  not yet merged to `main`). Branch `feat/multiple-rooms` from it.

## Success Criteria

- [ ] `GET /rooms` returns the fixed list in order; the page renders it.
- [ ] `/ws?room=juegos` joins `juegos`; `/ws` joins `general`; `/ws?room=nope` → 404, no upgrade.
- [ ] A message in `juegos` reaches only `juegos` members; `users` and `history` of each room contain only that room's members/messages.
- [ ] The same user can be in `general` and `juegos` at once, receive both, and see an unread count on the inactive one.
- [ ] Leaving one room sends `leave` only in that room and keeps the others connected; "Salir" leaves all.
- [ ] Reconnect re-syncs each joined room independently.
- [ ] Shutdown closes clients of all rooms before the server stops.
- [ ] `go vet ./...` and `go test -race ./...` pass; CSP/no-inline tests unchanged and passing.
