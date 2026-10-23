# Tasks: Typing indicator

Source: `proposal.md`, `specs/chat/spec.md` (CHAT-21 modified, CHAT-26, CHAT-27), `design.md`.
Run checks with `/snap/go/current/bin/go` (`/snap/go/current/bin/gofmt -l .`, `go vet ./...`,
`go test -race ./...`).

Parallel groups (disjoint files): **Group B** (backend: `internal/chat/*`, `README.md`) and
**Group F** (page: `cmd/chatroom/index.html`, `app.js`, `app.css`, `main_test.go`).

## Phase 1 — Infrastructure (Group B: wire types, parsing, throttle)

- [x] 1.1 `internal/chat/message.go`: `typeTyping`, `typingFrame{type,from,ts}`,
  `typingDMFrame{type,from,to,ts}`, `inboundTyping` (appended to the kinds), `parseInbound`
  case between `dm` and `chat` (no `to` → room; `to` string cleaned non-empty → DM; otherwise
  `ok=false`; `text` ignored; no blank-text rule).
- [x] 1.2 `internal/chat/client.go`: `typingInterval = time.Second`, `typingGate{last}` with
  `allow(now)`; `readPump` applies it to `inboundTyping` after `parseInbound` (dropped frames
  do not move the window; still refresh liveness).

## Phase 2 — Implementation (Group B: hub + docs)

- [x] 2.1 `internal/chat/room.go`: `dispatch` case `inboundTyping` → `typing(in)`; room signal
  to every member except sender; DM signal: self / `memberByKey` miss → silent drop, else
  recipient only with assigned nickname in `to`; `offer(c, msg)` non-blocking skip on full
  buffer (never removes, never announces). No `remember`, no logging, no `dm_error`.
- [x] 2.2 `README.md`: inbound/outbound protocol tables (`typing` both ways) + short
  "Escribiendo…" paragraph (throttle 1/s server, 3 s page, 5 s expiry, never stored).

## Phase 3 — Testing (Group B)

- [x] 3.1 `internal/chat/client_test.go`: `TestParseInbound` rows (room, DM, invalid `to`
  values, `text`/`from` ignored); old unknown-type example → `{"type":"poke"}` (also in
  `TestChatEnvelopeAndForgery` or equivalent); `TestTypingGate` with fixed times; WS
  integration: room + DM delivery, throttle (2 back-to-back → 1 delivered, chat still flows),
  invalid typing discarded and connection open.
- [x] 3.2 `internal/chat/room_test.go`: helpers + hub tests: room excludes sender, sender not
  forgeable, DM recipient only (case-insensitive, `to` = assigned nickname), self/offline
  silent, non-member dropped, not in history, full buffer skipped without removal or `leave`.

## Phase 4 — Implementation (Group F: page)

- [x] 4.1 `cmd/chatroom/index.html`: `<div id="typing" class="typing"></div>` between
  `#scroller` and `#send-form`, no `aria-live`/`role`.
- [x] 4.2 `cmd/chatroom/app.css`: `.typing` fixed one-line height with ellipsis, base skin +
  chadroom override (per design).
- [x] 4.3 `cmd/chatroom/app.js`: per design — `TYPING_SEND_MS`, `TYPING_TTL_MS`,
  `composerRoom()` extracted from `sendText` guards, `notifyTyping` (draft `input`, emoji
  insert), reset after send / pane switch, `case 'typing'` in `addRaw`, per-pane `typing`
  Maps with timers, clears on chat/dm/leave/closeConversation/removeConversations/
  scheduleReconnect/teardown, `activate` re-render, `typingText` texts per CHAT-27.
  Nicknames via `textContent` only.
- [x] 4.4 `cmd/chatroom/main_test.go`: `TestIndexHasTypingLine` (id present, between scroller
  and send-form, no aria-live/role). Existing CSP/no-inline tests stay green.

## Phase 5 — Verify

- [x] 5.1 `gofmt -l`, `go vet ./...`, `go test -race ./...` all clean.
- [x] 5.2 Manual UI checklist from `design.md` (user, in browser).
