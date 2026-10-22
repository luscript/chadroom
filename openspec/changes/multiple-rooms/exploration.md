# Exploration: multiple-rooms

Synthesized by the orchestrator from 2 crombie-explore agents (backend, frontend), 2026-10-22.
User requirements: **fixed server-defined set of rooms**, **a user can be in several rooms at
once**, **online users and last-50 history kept per room**.

## Key findings

- **Server is single-room by construction.** `chat.Room` is at once the hub goroutine (`Run`),
  the upgrader/origin checker and the HTTP handler (`HandleWebSocket`). `main.go` builds one
  `NewRoom(allowedOrigins)`, runs it, and routes `GET /ws` to it. History (50 `chat` msgs),
  the client registry and the `taken` nickname set are all `Run`-owned `Room` fields, so
  "per-room state" = "one `Room` instance per room" with no state moves.
- **Wire protocol.** Inbound: every non-blank text frame IS the chat text (no JSON), ≤ 4096
  bytes. Outbound: JSON `message {type,from,text,ts}` (exactly 4 fields, asserted by tests),
  `users {type,users,ts}`, `history {type,messages,ts}`. Join sequence: welcome → users →
  history → join fan-out. No frame carries a room field.
- **Nickname** comes from `/ws?nickname=`, is normalized and made unique case-insensitively
  within one `Room` (`taken`). With one `Room` per room, uniqueness becomes per room.
- **Client page is single-room and single-socket**: global `ws`, `users` Set, `lastAuthor`,
  one `ol#messages`, header subtitle hardcoded "Sala general". Rendering is XSS-safe
  (`textContent` only); strict CSP forbids inline code. Reconnect = equal-jitter backoff;
  a `history` frame replaces the transcript, then "Reconectado" is shown if there was an outage.

## Transport options (backend agent)

| Option | Change size | Concurrency risk | Notes |
|--------|-------------|------------------|-------|
| (a) one WebSocket per room, `/ws?room=X&nickname=Y` | small: registry of `Room`s + room param validation | **none** — a `Client` stays in exactly one `Room`, close-once invariant on `send` intact | N sockets per user (bounded by the fixed set); nickname may get a different suffix per room; frames unchanged |
| (b) one multiplexed socket | large: inbound JSON command protocol, `room` on every outbound frame, `Client`/membership split | **high** — a room's `remove` closing the shared `send` → double close / send-on-closed panic; slow-client policy across rooms | breaks ~all exact-shape tests and the frontend at once |

**Orchestrator choice: (a).** It satisfies all three requirements with no protocol break and
no change to the hub's single-goroutine invariants.

## Relevant files

- `internal/chat/room.go` — hub, admit/leave/relay, history, users, upgrader, origin check, `HandleWebSocket`.
- `internal/chat/client.go` — `Client`, read/write pumps (take `*Room`).
- `internal/chat/message.go` — frame structs, `historySize = 50`.
- `internal/chat/nickname.go` — normalize + per-`Room` uniqueness.
- `cmd/chatroom/main.go` — single-room wiring, `/ws` route, graceful shutdown (cancel → wait room done → `srv.Shutdown`), CSP, embedded assets.
- `cmd/chatroom/main_test.go` — dials bare `/ws` (no room) → keep a default room for compatibility; string tests over `index.html`/`app.js` (ids, IMAGE_HOSTS mirror).
- `internal/chat/room_test.go`, `client_test.go` — ~40 white-box hub tests via `NewRoom(nil)` + channels; e2e via httptest `startServer`.
- `cmd/chatroom/app.js` (396 lines), `index.html`, `app.css` — all client logic/markup/styles.
- `openspec/specs/chat/spec.md` (CHAT-01..18), `openspec/specs/server/spec.md` (SRV-01..05).

## Patterns

- Hub: only `Run` touches registry/history and closes `client.send`; no locks; channels only.
- Tests: `go test -race ./...`; white-box helpers (`startRoom`, `newTestClient`, `expectWelcome`, `decode`).
- Frontend: IIFE, `textContent`/`createElement`, panels registry (popover open/close),
  `role=tablist` precedent in GIF categories, CSS tokens in `:root`, kebab-case classes,
  Spanish (voseo) copy.

## Risks

- Room name is new untrusted input → must be validated against the fixed set **before** Upgrade.
- Upgrader/origin config lives on `Room`; N rooms would duplicate it → likely extract a small
  server-level handler/registry (design decision).
- Shutdown must wait for **all** room `Run` goroutines.
- Frontend: per-room state (messages, users, lastAuthor, unread, socket, backoff); the
  "Reconectado" notice must be per socket/room; frames without `from` (`users`, `history`)
  are dispatched before the `from` guard.
- Same person may be `Bob` in one room and `Bob (2)` in another (per-room uniqueness).
- Live DOM per room grows unbounded (pre-existing, multiplied by rooms).
- No JS tests; client behavior verified manually + Go string assertions.

## Gaps / open questions (resolved in proposal)

- Which rooms and where defined; how the page learns the list.
- Behavior of bare `/ws` (no room).
- Auto-join vs explicit join; meaning of "Salir".
