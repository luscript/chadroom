# Exploration: online-users-and-history

Synthesized from 2 crombie-explore agents (Go hub; web client + specs). Silent-drop
behavior verified at source (`internal/chat/room.go:150-160`).

## Key findings

- **Hub ownership**: `Room.Run` is the single goroutine owning `clients`, `taken` and every
  `close(c.send)`. A user-list snapshot and a history ring can be plain Run-owned `Room`
  fields — no locks. Keeping them on `Room` (not package-level) keeps the later
  multi-room change easy.
- **Register sequence** (`room.go:78-88`): assign nickname → add to `clients`/`taken` →
  `close(c.registered)` → deliver `welcome` privately → `fanOut(join)` only if welcome was
  delivered. Writer is a single FIFO `writePump`, so frame order == deliver order.
- **Envelope** (`message.go`): `message{type, from, text, ts}`, no omitempty, types
  chat/join/leave/welcome. Tests' `decode` enforces exactly 4 keys; CHAT-10 fixes the
  field set. New data (user list, history) needs new frame types/structs, not new fields
  on every frame.
- **Chat frames** are built in `relay` with a server ts; they can be stored encoded and
  replayed byte-identical.
- **Silent drops**: `deliver` on a full buffer calls `remove(c)` with **no leave notice**
  (CHAT-02). Shutdown is also silent. A client list maintained by join/leave deltas would
  keep **ghosts** for slow-dropped users.
- **Buffer**: `sendBufferSize = 256`. Sending N history frames individually at join risks
  dropping the newcomer and spams `aria-live`; a single batched frame avoids both.
- **Client** (`app.js` `addRaw` ~L133): one type switch, unknown types ignored (backward
  safe), but it drops any frame whose `from` is not a string. Rendering is createElement +
  textContent; images go through `isAllowedImage`. Own message iff `from === myName`.
- **Reconnect** keeps the DOM (CHAT-16) and only `join()` clears `#messages`. Replaying
  history on every welcome would duplicate messages unless the client replaces the list.
- **Layout**: no sidebar; header `.actions` (status, `#me`, Salir) is the natural slot.
  `.popover` already exists (emoji/GIF panels). Single breakpoint at 560px.
- **CSP / no-inline tests**: no `style=`, no inline JS/handlers, no new asset paths or hosts.
  No JS test harness — client behavior is manual/Go-string-asserted.
- **Specs**: `### Requirement: CHAT-nn`, RFC 2119, GIVEN/WHEN/THEN. Highest IDs CHAT-16,
  SRV-05. Affected: CHAT-02, CHAT-10, CHAT-11, CHAT-14, CHAT-16 (+ Purpose paragraph).

## Relevant files

| Path | Why |
|------|-----|
| `internal/chat/room.go` | Run loop, register/leave/relay, deliver/fanOut, silent drop |
| `internal/chat/message.go` | Envelope + encode; new frame types go here |
| `internal/chat/room_test.go` | Hub tests: `startRoom`, `newTestClient`, `expect`, `syncRoom` (hard-codes welcome→join→chat) |
| `internal/chat/client_test.go` | Socket tests: `dial` (expects welcome first), `decode` (4-key schema), `nextChat` |
| `cmd/chatroom/app.js` | `addRaw`, `addChat`, `join`, welcome/reconnect |
| `cmd/chatroom/index.html`, `app.css` | Header `.actions`, `.popover`, `.tag` |
| `cmd/chatroom/main_test.go` | Exact CSP, no-inline, embedded asset tests |
| `openspec/specs/chat/spec.md` | CHAT-01..16 |

## Risks

1. Ghost users in the list after silent slow-client drops.
2. Duplicate messages after reconnect if history is replayed into a kept DOM (CHAT-16 delta).
3. Test helpers (`syncRoom`, `dial`, `decode`, `TestWelcomeFirst`) assume an exact frame
   sequence/shape — they break if new frames are always sent.
4. Buffer overflow / screen-reader spam if history is N separate frames.
5. History is in memory only (lost on restart); bounded by N × 4 KiB.

## Gaps

- `openspec/config.yaml` context block is stale ("Testing: none yet").
- No automated JS tests; client behavior verified manually.
