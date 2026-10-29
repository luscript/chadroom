# Tasks: message-replies

Design: `design.md`. Specs: `specs/chat/spec.md` (CHAT-10/18/21/22 modified, CHAT-35/36 added).

## 1. Backend protocol (internal/chat) — group A

- [x] 1.1 `message.go`: add `quote{ID,From,Text}`; `message.ID`/`message.Reply` (omitempty, after `avatar`); `dmFrame.ID`/`dmFrame.ReplyTo`; `inbound.replyTo`; `replyTo(env)` helper used only on valid chat/dm envelopes (CHAT-21); `quoteText` (100 runes, trailing whitespace trimmed)
- [x] 1.2 `room.go`: `nextID`, `targets [200]quote`, `targetNext`; `relay` resolves reply before assigning id, advances id/remember/keepTarget only after encode succeeds; `direct` sets `ID` and forwards `ReplyTo` without lookup (CHAT-24, CHAT-35)

## 2. Backend tests — group A

- [x] 2.1 `room_test.go`: update `decode`/`decodeDM` exact-shape helpers; hub unit tests per design Test Plan (ids shared by chat/dm, reply resolved, truncation, unknown/evicted/DM target ignored, no nesting, join/leave/welcome unchanged, history keeps id/reply)
- [x] 2.2 `client_test.go`: `TestParseInbound` rows for valid/malformed `reply_to` and ignored inbound `id`/`reply`
- [x] 2.3 `server_test.go`: integration tests per design (room reply end-to-end, DM `reply_to` forwarded, large reply frame delivered)

## 3. Frontend (cmd/chatroom) — group B

- [x] 3.1 `index.html`: `#reply-preview` (author, text, cancel) before `.composer-bar`
- [x] 3.2 `app.js`: per-pane `msgs` map + `forgetIds`; `addChat(…, id, quote)` with quote button / missing quote and one reply button; validation helpers (`validId`, `parseReply`); DM quote lookup; `startReply`/`cancelReply`/`renderReplyPreview`; `sendText` adds `reply_to`; Escape order; `activate` cancels; `jumpTo` scroll + highlight; "🖼 GIF" for image quotes (CHAT-36)
- [x] 3.3 `app.css`: quote, reply button (hover + focus-within), highlight (reduced-motion guard), preview bar — existing tokens only
- [x] 3.4 `main_test.go`: `TestIndexHasReplyPreview` (and no inline code still passes)

## 4. Docs — group A

- [x] 4.1 `README.md`: document `id`, `reply`, `reply_to` in the protocol section

## 5. Verification

- [x] 5.1 `gofmt` clean and `go test -race ./...` green
- [ ] 5.2 Manual two-browser checklist (`design.md`) — user
