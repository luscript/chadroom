# Exploration: Private chat (direct messages)

Two crombie-explore agents (backend `internal/chat` + `cmd/chatroom/main.go`; frontend
`app.js`/`index.html`/`app.css`), synthesized by the orchestrator. No conflicts between reports.

## Key findings

- **No server-wide user registry.** `Server` is an immutable map of rooms. Each `Room` is a
  single-goroutine hub (`Run`) that owns `clients`, `taken` (nickname set), and `history`. There
  are no mutexes. A person is "in several rooms" by opening one socket per room.
- **Nicknames are unique per room only** (`foldKey`, ` (n)` suffixes, CHAT-12/19). The same
  person may be `Ana` in `general` and `Ana (2)` in `juegos`, and two different people may both be
  `Ana` in different rooms. A global "DM by nickname" is therefore ambiguous.
- **The wire protocol is asymmetric.** Inbound frames are raw UTF-8 text (the whole frame is the
  message; 4096-byte cap, UTF-8 check, blank frames ignored in `readPump`). Outbound frames are
  server-built JSON: `message{type,from,text,ts}` for `chat|join|leave|welcome`, plus `users` and
  `history`. Inbound frames cannot express a recipient today.
- **Delivery must go through the owning `Run`.** Only `Run` may touch `clients` and close
  `send` (CHAT-05). Writing to another room's `client.send` from another goroutine risks a
  send-on-closed-channel panic.
- **Room history (50, chat only) is replayed to every newcomer.** DMs must never be stored
  there.
- **Frontend**: a vanilla-JS IIFE, a `rooms` Map with one socket per joined room, and the
  `addRaw` switch on `msg.type` (unknown types are ignored per CHAT-14, so old pages degrade
  safely). Rendering is XSS-safe (only `textContent`, strict CSP). The users popover
  (`renderUsers`) holds static `<li>` items today and is the natural DM entry point. The tab strip
  and `#scroller` panes can host DM conversations. The unread badge logic is reusable.
- **The room object conflates conversation, room, and socket** (`Dejar sala`, `updateLeaveBtn`,
  and the join/leave count all assume tabs are rooms). DM conversations need their own structure.

## Relevant files

| Path | Why |
|------|-----|
| `internal/chat/client.go` | `readPump`: inbound validation; where DM frames are parsed |
| `internal/chat/room.go` | Hub: `admit`, `relay`, `deliver`, `remove`, history; where DM routing/storage lives |
| `internal/chat/message.go` | Type constants, positional `message` struct, `encode()` |
| `internal/chat/nickname.go` | `normalizeNickname`, `foldKey` for recipient lookup |
| `internal/chat/server.go` | `Server`, `HandleWebSocket`, origin check |
| `cmd/chatroom/app.js` | `rooms` Map, `addRaw`, `renderUsers`, `sendText`, `activate`, unread |
| `cmd/chatroom/index.html`, `app.css` | Users popover, tab strip, panes, composer |
| `internal/chat/room_test.go`, `client_test.go` | Hub helpers (`startRoom`, `expect*`), httptest + dial helpers |
| `openspec/specs/chat/spec.md` | CHAT-01, 09, 10, 12, 13, 14, 17, 18, 19, 20 and the Purpose paragraph |
| `openspec/backlog.md` | No auth item; rate-limiting, nickname-polish (look-alikes), ops-hardening |

## Patterns

- Single-owner goroutine per room, channels instead of locks, non-blocking `deliver` with
  slow-drop.
- Private frames to a single client already exist (`welcome`, `users`, `history` in `admit`).
- Table tests and white-box hub tests with fake clients; integration tests over httptest with
  WebSocket dial helpers. There are no JS tests, so UI verification is manual.

## Risks

- **Impersonation / privacy**: nicknames are unauthenticated, and look-alike names survive
  normalization (backlog M1). Any DM state stored per nickname can be inherited by whoever takes
  that name after a disconnect.
- **Inbound protocol change**: the "plain text only" contract (spec Purpose, CHAT-09/10) must
  change without misrouting room messages.
- **Unbounded memory**: there is no rate limit or connection cap; DM storage must be capped.
- **No frontend tests**: XSS safety relies on `textContent`. Nicknames must never be used in
  element ids or selectors.

## Gaps

- `internal/chat/server_test.go` and `cmd/chatroom/main_test.go` were only skimmed. Check them in
  the design phase.
