# Proposal: Online users list and message history

## Intent

Today a user who opens the chat sees an empty screen and has no way to know who else is
connected until someone joins, leaves or writes. This change adds the two `chat-features`
items that most improve the experience for a friends-only chat: an **online users list**
and the **last N messages on join**. Backlog group `chat-features` (first half; multiple
rooms and typing indicator stay in the backlog — rooms is the planned next change).

## Decisions (orchestrator, automatic mode — 2026-10-22)

- **N = 50** chat messages of history, in memory only (lost on restart). Only `chat`
  messages are stored — join/leave notices are not replayed.
- **New frame types, not new fields**: the existing 4-field envelope (CHAT-10) stays
  unchanged for `chat`/`join`/`leave`/`welcome`. Two new frame types carry their own shape:
  - `users`: `{"type":"users","users":["Ana","Bob"],"ts":…}` — full snapshot, sorted.
  - `history`: `{"type":"history","messages":[{chat envelope}, …],"ts":…}` — one batched
    frame, oldest first. One frame (not N) avoids filling the 256-slot send buffer on join
    and avoids N screen-reader announcements.
- **Join sequence**: `welcome` → `users` → `history` → `join` fan-out. Both new frames are
  always sent (even empty), so the client logic is unconditional.
- **The list is kept current with deltas**: after the snapshot, the client adds/removes
  names on `join`/`leave`.
- **No ghosts**: a client dropped as slow (today silent, CHAT-02) is now announced to the
  remaining clients with a `leave`. Shutdown stays silent (everyone is disconnected anyway).
- **Reconnect re-syncs instead of keeping the DOM**: on `history` the page replaces the
  message list with the server's history, and on `users` it replaces the list. This removes
  duplicates after a reconnect and also shows messages sent during the outage. CHAT-16's
  "messages already on screen MUST be kept" is amended accordingly.
- **UI**: a "N en línea" button in the header `.actions` that toggles a popover (reusing
  `.popover`) with the names; the own name is tagged "vos". Works the same on mobile; no
  sidebar.
- **Rooms-ready, not rooms**: history and the user list are `Room` fields owned by `Run`,
  so the next change can scope them per room without moving state.

## Scope

### In Scope
- Hub: Run-owned history ring (50 `chat` messages), `users` snapshot and `history` frame on
  join, `leave` for slow-client drops.
- Envelope: encoding for the two new frame types; `chat`/`join`/`leave`/`welcome` unchanged.
- Page: handle `users` and `history`; online list popover; re-sync on reconnect; own
  messages in history marked by name as today.
- Tests (`go test -race ./...`), spec deltas (CHAT-02, -10, -11, -14, -16 modified; new
  requirements for the user list and history).

### Out of Scope
- Multiple rooms (next change), typing indicator (backlog).
- Persisting history across restarts; configurable N.
- Replaying join/leave notices; per-user "last seen".
- Any rate limiting / max-clients cap (`rate-limiting` backlog).

## Approach

1. `message.go`: two new outbound structs (`users`, `history`) with their own encoders;
   history entries reuse the `message` struct so each entry is a standard chat envelope.
2. `room.go`: `history []message` ring appended in `relay`; register case delivers
   welcome → users → history, stopping at the first failed deliver (no join fan-out),
   then fans out join. Slow-client drops during a fan-out are collected and announced with
   `leave` after it, using the same path as a normal leave.
3. `app.js`: `addRaw` accepts the two new types (they have no `from`); `users` replaces the
   list, `join`/`leave` patch it, `history` replaces `#messages` and renders via `addChat`.
4. `index.html`/`app.css`: online button + popover, class-based styling only (CSP).

Exact structs, drop-announcement mechanics, client reset ordering vs the "Reconectado"
notice and test helper changes go in `design.md`.

## Constraints

- Keep hub invariants: only `Run` touches registry/history and closes `send`; `Run` never
  sends on its own channels (CHAT-03); no locks.
- No new dependencies. CSP and no-inline rules (SRV-05) unchanged: no new hosts, assets,
  inline code or `style=`.
- Nicknames and history text rendered with `textContent` only; history images go through
  the existing allowlist (CHAT-15).

## Affected Areas

| Area | Files | Change |
|------|-------|--------|
| Hub | `internal/chat/room.go` | history ring, join sequence, leave on slow drop |
| Envelope | `internal/chat/message.go` | `users` and `history` frame encoding |
| Frontend | `cmd/chatroom/app.js`, `index.html`, `app.css` | new frame handling, online popover, reconnect re-sync |
| Tests | `internal/chat/*_test.go`, `cmd/chatroom/main_test.go` | helpers consume the new join frames; new tests |
| Specs | `openspec/specs/chat/spec.md` | modified CHAT-02/10/11/14/16, new CHAT-17/18 |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Test helpers (`syncRoom`, `dial`, `decode`) assume welcome is followed by join | High | Update helpers first to consume `users` + `history`; then add new tests |
| Announcing slow drops cascades (a leave fan-out drops another slow client) | Low | Collect drops and announce iteratively until none are pending; covered by test |
| History frame is large (50 × up to 4 KiB, escaped) | Low | Bounded (~1–2 MB worst case); no outbound limit exists; acceptable for friends |
| Replayed message from a name now used by someone else | Low | Accepted: names are unique only while connected (documented) |
| Client behavior has no automated JS tests | Medium | Manual checklist in verify; Go string assertions where useful |

## Rollback Plan

Single binary (server + embedded page), no persisted data: revert the change's commits and
redeploy; open tabs reload into the old protocol (old clients already ignore unknown types).

## Dependencies

- Builds on `structured-messages` and `frontend-hardening` (both archived, in `main`).
- Branch `feat/online-users-and-history` from `main`.

## Success Criteria

- [ ] A joining client receives `welcome`, then `users` (all connected names, sorted, itself included), then `history` (≤ 50 last chat messages, oldest first), then its own `join`.
- [ ] Other clients keep an accurate list via `join`/`leave`, including when a slow client is dropped.
- [ ] Join/leave are not stored in history; the 51st message evicts the oldest.
- [ ] After a reconnect the page shows the server history without duplicates and an accurate list.
- [ ] The page shows "N en línea" and the names in a popover, own name tagged "vos".
- [ ] `go vet ./...` and `go test -race ./...` pass; CSP/no-inline tests unchanged and passing.
