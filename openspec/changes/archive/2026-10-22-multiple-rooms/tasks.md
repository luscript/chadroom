# Tasks: Multiple rooms

Source: `proposal.md`, `specs/chat/spec.md` (CHAT-19, CHAT-20), `specs/server/spec.md`
(SRV-06, SRV-07, SRV-03), `design.md`. Run checks with `/snap/go/current/bin/go`
(`go vet ./...`, `go test -race ./...`).

Parallel groups (disjoint files): **Group B** (backend: `internal/chat/*`,
`cmd/chatroom/main.go`, `main_test.go`, `README.md`) and **Group F** (frontend:
`cmd/chatroom/index.html`, `app.js`, `app.css`). Group B's `TestIndexHasRoomTabs` asserts the
DOM ids fixed in design.md, so it passes only once Group F has finished; verify runs after both.

## Phase 1 — Infrastructure (Group B)

- [x] 1.1 `internal/chat/room.go`: add immutable `id`; `NewRoom(id string)`; remove
  `allowedOrigins`/`upgrader`/`checkOrigin`/`HandleWebSocket`; add
  `(*Room).serveConn(conn, nickname)` (old handler tail, unchanged logic); drop unused imports.
  `Run`, channels and hub methods unchanged.
- [x] 1.2 `internal/chat/server.go` (new): `Server{rooms, defaultRoom, allowedOrigins, upgrader}`,
  `NewServer(allowedOrigins []string, rooms ...*Room)` (panics on no rooms / empty id / duplicate
  id), `Run(ctx)` (WaitGroup over all rooms, returns when all stopped), `HandleWebSocket`
  (room lookup: missing/empty → default, unknown → 404 with no body and no log, BEFORE Upgrade;
  then Upgrade with origin check → `serveConn(conn, normalizeNickname(nick))`), `checkOrigin`
  moved verbatim.
- [x] 1.3 `internal/chat/client.go`: `logError(roomID, op, err)` logs the room id; pumps pass `cr.id`.

## Phase 2 — Implementation (Group B: server wiring)

- [x] 2.1 `cmd/chatroom/main.go`: `var roomIDs = []string{"general","juegos","musica"}`;
  `newChatServer(allowedOrigins)`; `serveRooms` (`setStaticHeaders` + `c.JSON` →
  `{"rooms":[...]}`); `newRouter(*chat.Server)` with `GET /rooms` and `/ws` →
  `chatSrv.HandleWebSocket`; `run` waits on `chatSrv.Run` (roomsDone) before `srv.Shutdown`.
- [x] 2.2 `README.md`: document `GET /rooms`, the `room` query param (default room, 404 on
  unknown), and per-room users/history/nicknames.

## Phase 3 — Implementation (Group F: page)

- [x] 3.1 `cmd/chatroom/index.html`: `nav.rooms` with `#room-tabs[role=tablist]` and
  `#room-leave-btn` ("Dejar sala", disabled) between header and `.rule`; `.subtitle` →
  `#room-name`; `#scroller[role=tabpanel]` without the static `<ol id="messages">`;
  `#join-error` (role=alert, hidden) in the join form. No inline code/styles (SRV-05).
- [x] 3.2 `cmd/chatroom/app.css`: `.rooms`, `.room-tabs` (one row, `overflow-x:auto`),
  `.room-tab` (+ `.joined`, `[aria-selected="true"]`, muted when not joined), `.room-unread`,
  `.room-leave`, `.join-error`, panes `.messages`; existing tokens only; mobile ≤560px.
- [x] 3.3 `cmd/chatroom/app.js` state: `rooms` Map + `active` + shared `requestedNick`;
  `loadRooms()` (fetch `/rooms`, validate ids `/^[a-z0-9-]{1,32}$/`, ≥1 room) and
  `createRoom(id)` (tab + `ol#pane-<id>.messages[aria-live=polite][hidden]`); move
  `ws/myName/status/lastAuthor/retryTimer/attempt/outage/users` into the room object.
- [x] 3.4 `app.js` connection: `connect(room)` (`/ws?room=…&nickname=…`, handlers close over
  room with `room.ws !== socket` guard), per-room `scheduleReconnect`/`reconnectNow`
  (`online`/`visibilitychange` → every joined room), `setStatus(room, s)` updates header only
  if active.
- [x] 3.5 `app.js` dispatch/rendering: `addRaw(room, data)`, `renderHistory(room, …)` (per-room
  "Reconectado"), `addChat/addSystem/append(room, …)`, `renderUsers()` for `active`, "vos" from
  `room.myName`; unread via `bumpUnread` only for live `chat` in inactive rooms from others
  (not history); badge `99+`; tab `aria-label` rebuilt on state change. `textContent` only.
- [x] 3.6 `app.js` flows: nickname submit awaits rooms (retry once, else show `#join-error`
  "No pudimos cargar las salas. Probá de nuevo." and stay); `joinRoom`, `activate`
  (header, pane, users, unread reset, leave-button state; focus not moved), tab click,
  "Dejar sala" (active room, disabled with one joined), `teardown`, "Salir" for all rooms.
  `sendText` / `updateSendState` use `active`.

## Phase 4 — Testing (Group B)

- [x] 4.1 Test helpers (`internal/chat/client_test.go`): `startRooms(t, allowed, rooms...)`,
  `startServer` as wrapper, `dialRoom`/`dialRoomRaw`; adapt origin and slow-client tests;
  every `NewRoom(nil)` → `NewRoom("general")` in `client_test.go` and `room_test.go`;
  `TestNewRoomDefaults` asserts id; move `TestCheckOrigin` to `server_test.go`.
- [x] 4.2 `internal/chat/server_test.go` (new): `TestNewServerPanics`, `TestUnknownRoomIs404`,
  `TestDefaultRoom`, `TestRoomsIsolated`, `TestNicknameUniquePerRoom`,
  `TestLeaveOneRoomKeepsOther`, `TestOriginCheckAppliesToEveryRoom`,
  `TestServerRunWaitsForAllRooms`, `TestCheckOrigin` (moved) — per design.md "New tests".
- [x] 4.3 `cmd/chatroom/main_test.go`: `newTestServer` uses `newChatServer` + `Run` +
  `newRouter(chatSrv)`; add `TestRoomsEndpoint`, `TestWebSocketRoomParam`,
  `TestRunShutdownClosesAllRooms`, `TestIndexHasRoomTabs`.
- [x] 4.4 `gofmt -l`, `go vet ./...` and `go test -race ./...` pass (Group B worker runs them;
  `TestIndexHasRoomTabs` may fail until Group F lands — report, don't modify frontend).
