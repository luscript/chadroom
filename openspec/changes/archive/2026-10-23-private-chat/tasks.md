# Tasks: Private chat (direct messages)

Source: `proposal.md` (incl. spec-phase amendment: no server-side DM store),
`specs/chat/spec.md` (CHAT-21 to CHAT-25; modified Purpose, CHAT-10, 13, 14, 18), `design.md`.
Run checks with `/snap/go/current/bin/go` (`gofmt -l`, `go vet ./...`, `go test -race ./...`).

Parallel groups (disjoint files): **Group B** (backend: `internal/chat/*`, `README.md`) and
**Group F** (frontend: `cmd/chatroom/app.js`, `app.css`). `index.html` and `main_test.go` do
not change. Group F has no automated tests; the manual checklist (design.md) runs in verify.

## Phase 1 — Infrastructure (Group B: wire types and parsing)

- [x] 1.1 `internal/chat/nickname.go`: extract `cleanNickname` (CHAT-09 steps 1–4);
  `normalizeNickname` = `cleanNickname` + `Anonymous` fallback. Existing tests stay green.
- [x] 1.2 `internal/chat/message.go`: constants `typeDM`, `typeDMError`, `reasonInvalid/Self/Offline`;
  structs `dmFrame{type,from,to,text,ts}`, `dmErrorFrame{type,to,reason,ts}`; `inboundKind`
  (`inboundChat` = zero value, `inboundDM`, `inboundBadDM`); `inbound.kind`/`inbound.to`;
  `jsonString` (rejects non-`"` raw values, assigns `err` before returning); `parseInbound`
  decoding into `map[string]json.RawMessage` (exact keys; invalid DM wins over blank text).
- [x] 1.3 `internal/chat/client.go`: `readPump` calls `parseInbound` after the binary/UTF-8
  checks (`!ok` → `continue`), sets `in.from = c`; drop the `strings` import.

## Phase 2 — Implementation (Group B: hub routing)

- [x] 2.1 `internal/chat/room.go`: `Run` and `drainBroadcast` call `dispatch` (membership
  check, then `direct` / `dmError(invalid)` / `relay`); `direct` (self → `self`,
  `memberByKey` miss → `offline`, else one `dmFrame` with one `ts` via `deliverPrivate(f, to,
  from)`); `dmError`; `memberByKey` (`taken` fast path, then scan `clients`); split `publish`
  into `fanOut` + `announceDropped`; `deliverPrivate` queues each copy independently and
  announces slow drops. Nothing in the DM path calls `remember` or logs text/recipient.
  Comment on `broadcast` that it carries every inbound kind.
- [x] 2.2 `README.md`: inbound protocol (plain text, `chat`/`dm` envelopes), `dm`/`dm_error`
  frames and reasons, privacy note (room-scoped, never stored or replayed, unauthenticated
  nicknames).

## Phase 3 — Implementation (Group F: page)

- [x] 3.1 `cmd/chatroom/app.js` state and lifecycle: `conversations` Map (`convKey = room.id +
  '\n' + exact nickname`), `activeConv`, `convSeq` ids (`dm-tab-N`/`dm-pane-N`, never the
  nickname); `current()`, `ownerRoom()`; `createConversation`, `renderConvTab` (badge 99+,
  `.closed`, aria-label), `openConversation`, `reopenConversation` (divider notice),
  `closeConversation(s)`, `removeConversations`. DM tab inserted after the room's last tab.
- [x] 3.2 `app.js` wiring: `activate(room, conv = null)` (selection/hidden across rooms and
  conversations, `#room-name` `@peer · #room`, unread reset); room tab click guard;
  `teardown` → `removeConversations`; `scheduleReconnect` → `closeConversations` with the
  connection-lost notice; `renderUsers` adds "Mensaje privado" button (`.btn.btn-ghost.users-dm`)
  for everyone but `myName`; `addChat`/`addSystem`/`append` accept a room or a conversation
  (own via `ownerRoom(pane).myName`, scroll/`img.onload` against `current()`); `bumpUnread`;
  room `chat` unread when `room !== current()`.
- [x] 3.3 `app.js` frames and composer: `addRaw` `leave` closes the peer's open conversation;
  new `dm` case (peer = `to` if own echo, `from` if addressed to me, else ignore; get/create,
  reopen if closed, render, unread); new `dm_error` case before the `from` guard (target by
  `to`, else `current()`; Spanish notices per reason; `offline` closes it); `updateSendState`
  (closed conversation disables send, placeholders per design); `sendText` sends
  `{"type":"chat"}`/`{"type":"dm"}` envelopes, byte check on the encoded frame, no local render.
  `renderHistory` unchanged (DM panes never cleared). Nicknames only via `textContent`,
  `aria-label`, `placeholder`.
- [x] 3.4 `cmd/chatroom/app.css`: `.room-tab.dm-tab .room-tab-name` ellipsis (16ch),
  `.room-tab.dm-tab.closed` muted, `.users-list .users-dm`; existing tokens only.

## Phase 4 — Testing (Group B)

- [x] 4.1 `internal/chat/client_test.go`: `TestParseInbound` table (all cases in design.md test
  plan, incl. `{"TYPE":"dm",…}` → chat, `"to":null`, invalid-over-blank, 40-rune `to` → 32).
- [x] 4.2 `internal/chat/room_test.go` helpers `dm`, `exactFields`, `expectDM`, `expectDMError`;
  tests `TestDMDeliveredAndEchoed`, `TestDMSuffixedRecipient`, `TestDMErrors`,
  `TestDMNotInHistory`, `TestReusedNicknameInheritsNothing`, `TestDMFromNonMemberDropped`,
  `TestDMBeforeLeaveOnDisconnect`, `TestDMSlowRecipientDropped`, `TestDMNotLogged`.
- [x] 4.3 `internal/chat/client_test.go` WebSocket tests: helpers `readDM`, `readDMError`;
  `TestDMOverWebSocket`, `TestChatEnvelopeAndForgery`, `TestDMWhitespaceDiscarded`,
  `TestInvalidDMKeepsConnection`, `TestOversizedDMClosesSender`.
- [x] 4.4 `internal/chat/server_test.go`: `TestDMRoomScoped` (CHAT-19/22, incl. `offline` for a
  nick present only in another room).
- [x] 4.5 Gates: `gofmt -l` empty, `go vet ./...`, `go test -race ./...` all green.
