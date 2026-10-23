# Proposal: Typing indicator

## Intent

Show "Ana está escribiendo…" to the other people looking at the same conversation (a room or a
DM tab) while someone is typing. Last pending item of the `chat-features` backlog group.

## Decisions

Orchestrator decisions (auto mode, 2026-10-23; user chose "your recommendation"):

- **Ephemeral signal, start-only.** The page sends `{"type":"typing"}` (room) or
  `{"type":"typing","to":"<nick>"}` (DM) while the draft is non-empty, **at most once every
  3 s** per target. There is **no "stop" frame**: receivers hide a typist **5 s** after that
  typist's last `typing` frame, or immediately when a `chat`/`dm` from them arrives, or on their
  `leave`. One frame type keeps the protocol and the hub small (YAGNI).
- **Server throttle (trust boundary).** `readPump` accepts at most **one `typing` frame per
  second per connection** and silently discards the rest before they reach the hub. State is
  local to the per-client goroutine, so no locks. This protects `Room.broadcast` without
  waiting for the `rate-limiting` change.
- **Outbound frames.** Room: `{"type":"typing","from","ts"}` to **every member except the
  sender**. DM: `{"type":"typing","from","to","ts"}` to **the recipient only** (no echo).
  `from` is always the server-assigned nickname.
- **Silent failures.** A typing frame to an offline user, to oneself, or with an invalid `to`
  is dropped with **no** `dm_error` (it is not a message the user asked to send).
- **Best-effort delivery.** If a recipient's send buffer is full, that typing frame is skipped
  for that recipient — it does **not** drop the client nor announce `leave`. Slow clients keep
  being dropped by real messages as today.
- **Never stored, never logged.** Typing is not in room history and is never written to logs.
- **CHAT-21 changes**: `{"type":"typing"}` stops being plain chat text. Old pages never send it;
  the page and server ship in the same binary, so there is no mixed-version window.
- **UI**: one line `#typing` between the message list and the composer, with a reserved height
  (no layout jump), showing typists of the **visible pane** only: "Ana está escribiendo…",
  "Ana y Bob están escribiendo…", "Varias personas están escribiendo…" (3+). It is **not** an
  aria-live region (a screen reader would announce it every few seconds). Nicknames rendered
  with `textContent` only.

## Affected Areas

| Area | Files | Change |
|------|-------|--------|
| Wire protocol / parsing | `internal/chat/message.go` | `typeTyping`, `typingFrame`, inbound kinds |
| Connection | `internal/chat/client.go` | per-connection typing throttle in `readPump` |
| Hub | `internal/chat/room.go` | dispatch typing: room fan-out except sender, DM to recipient, best-effort |
| Page | `cmd/chatroom/app.js`, `index.html`, `app.css` | send throttle, receive/expire, indicator line |
| Tests | `internal/chat/*_test.go`, `cmd/chatroom/main_test.go` | hub, parse, integration, id check |
| Docs / specs | `README.md`, `openspec/specs/chat/spec.md` | protocol table; CHAT-21 modified, CHAT-26/27 added |

## Out of scope

- General rate limiting (backlog `rate-limiting`).
- Typing replay after reconnect, cross-room typing, "stop typing" frames.

## Rollback

Revert the change's commit(s). No persisted data or schema; the page and server are one binary.
