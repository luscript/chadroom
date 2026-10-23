# Exploration: typing-indicator

Synthesized by the orchestrator from 2 crombie-explore agents (backend, frontend), 2026-10-23.
No conflicts between the two reports.

## Backend (internal/chat)

- `parseInbound` (`message.go:85-112`) decodes into `map[string]json.RawMessage` with exact
  keys; `type`=`dm` → `inboundDM`/`inboundBadDM`, `type`=`chat` + string `text` → chat,
  everything else (incl. `{"type":"typing"}`) → plain-text room chat with the whole frame.
  A new `case typeTyping` goes in the `switch typ` before the fallthrough.
- `inboundKind` is a `uint8` iota, zero value = chat; appending new kinds keeps test literals valid.
- `Room.Run` → `dispatch` (`room.go:157-170`): membership check, then `direct` / `dmError` /
  `relay`. Only `relay` calls `remember` (history). `direct` uses `memberByKey` + `deliverPrivate`.
- Delivery is non-blocking: `deliver` drops (removes) a client whose 256-slot `send` buffer is
  full; `fanOut`/`publish` then `announceDropped` sends `leave`. No skip-sender helper exists.
- No rate limiting anywhere. All inbound kinds share `Room.broadcast` (cap 256), so a typing
  flood would delay chat and can slow-drop recipients.
- `readPump` (`client.go:68-108`): read limit 4096, binary → 1003, bad UTF-8 → 1007, any frame
  refreshes the read deadline (CHAT-07). It is a per-client goroutine: per-client throttle
  state can live there without locks.
- Specs: highest IDs CHAT-25 / SRV-07. **CHAT-21 explicitly lists `{"type":"typing"}` as plain
  chat** (scenario "JSON that is not an envelope is plain text") — must be MODIFIED.
- Tests: hub unit helpers in `room_test.go` (`startRoom`, `newTestClient`, `broadcast`, `dm`,
  `syncRoom`, `next`, `expect`, `expectEmpty`, `exactFields`); integration helpers in
  `client_test.go` (`startServer`, `dialRoom`, `send`, `readMsg`, `nextChat` skips non-chat).
  `TestParseInbound` (`client_test.go:748`) is a table test to extend. `drainTypes` does not exist.

## Frontend (cmd/chatroom)

- Single IIFE in `app.js`, no JS tests. One socket per room; DM tabs ride the room socket.
- Send path `sendText` (`app.js:615`) guards on `room.status==='online'`, `ws.readyState===OPEN`,
  `activeConv.open`; emits `{type:'chat',text}` or `{type:'dm',to,text}`. Has size-check and
  `addSystem` side effects → typing must not go through it, but must reuse the same guards.
- Only draft listener: `draft.addEventListener('input', updateSendState)` (`app.js:605`).
  Emoji insertion (`draft.value += ch`) does not fire `input`. One shared `#draft` for all panes.
- Incoming dispatch `addRaw` (`app.js:388-456`): early returns for `users`/`history`/`dm_error`,
  then `switch(msg.type)`; unknown types ignored. Own echo: `msg.from === room.myName`.
- Tab model: `rooms`, `active`, `conversations` (`convKey = room.id + '\n' + nick`),
  `activeConv`, `current()`, `ownerRoom()`; `activate()` is the single place the visible pane
  changes.
- Lifecycle resets: `joinRoom`, `teardown`, `scheduleReconnect` (+ `closeConversations`),
  `closeConversation`, `leave` frame.
- Layout: `#scroller` then `#send-form` in `index.html` (~L67-69). `.messages` lists are
  `aria-live=polite`; indicator must NOT go inside them. CSS tokens in `:root`, kebab-case,
  muted text pattern `.msg-system`; second "chadroom" skin at `app.css:203+` may need an override.
- Only timer pattern: `retryTimer`. No debounce helper.
- `main_test.go`: CSP exact string, no inline code regex, required-id checks
  (`TestIndexHasRoomTabs` pattern). No CSP change needed.

## Risks

- Typing floods without rate limiting → server-side per-client throttle needed (client throttle
  is not a trust boundary).
- Typing fan-out through `deliver` could slow-drop clients and emit `leave` for an ephemeral event.
- Typing must never reach `remember` or logs.
- Hub unit tests using strict `next()` ordering would see new frames only if tests send typing.
