# Exploration: message-replies

WhatsApp-style replies in room chat and direct messages: the reply bubble shows the quoted
message (author + text) above the reply; clicking the quote scrolls to the original. Desktop
only, one reply button per message, quoted GIFs/images shown as text.

Sources: two crombie-explore agents (backend `internal/chat`, frontend `cmd/chatroom`).

## Backend (internal/chat)

- Messages have **no id** today. `message{type,from,text,ts,avatar}` is shared by
  welcome/join/leave/chat/history (`message.go:31-37`).
- Room chat is built, stamped, stored and published in `Room.relay` (`room.go:173-182`);
  `remember()` keeps a 50-entry history ring (`room.go:145-152`). This is the single place to
  assign an id and resolve a reply target. Only `Room.Run` touches this state → a `nextID`
  counter and a lookup ring are plain Room fields, no locks.
- DMs are routed in `Room.direct` (`room.go:204-218`), one `dmFrame{type,from,to,text,ts}` to
  the recipient + echo. **CHAT-24 forbids keeping DM content after routing**, so the server
  cannot resolve a DM reply target without amending CHAT-24.
- `parseInbound` (`message.go:121-165`) reads exact keys from a raw map; unknown fields are
  ignored, so `reply_to` (parsed with `jsonInt`) is backward compatible. A malformed `reply_to`
  should be ignored, not invalidate the message.
- Limits: inbound frame 4096 bytes; no outbound cap; `truncateRunes(s, n)` exists
  (`nickname.go:34-39`) for the quote snapshot.
- Tests: `room_test.go` `decode()` requires exactly 4 fields → must be updated when chat frames
  gain `id`. `TestParseInbound` table in `client_test.go:761+`. httptest helpers in
  `server_test.go`.

## Frontend (cmd/chatroom)

- One render path: `addChat(pane, author, text, ts, avatarUrl)` (`app.js:786`), used by live
  chat (617), live dm (630) and `renderHistory` (736). Panes (room / conversation) own an
  `<ol>`; nothing is pruned, so a per-pane `Map(id → li)` is enough to find the original.
  Reset it in `renderHistory` (`replaceChildren`, 732) and `joinRoom` (280).
- Sending: `sendText` (`app.js:885`) builds `{type:'chat',text}` / `{type:'dm',to,text}` and
  checks the 4096-byte limit on the encoded frame. GIF paths (972, 1032) also use it.
  Own messages render from the server echo (no optimistic render) → own messages get ids too.
- Escape: one global keydown listener calls `closePanels()` (936).
- Composer: `form#send-form.composer > div.composer-bar` (`index.html:106`).
- Rendering is `textContent` only; CSP `default-src 'none'; script-src 'self'; style-src 'self'`
  → no inline code/style; classes only. A quoted GIF must stay text (`isAllowedImage` test).
- CSS tokens in `:root`, kebab-case classes, no `.msg:hover` yet; legacy overrides at
  `app.css:252-267`. Hover-only controls must also show on `:focus-within`.
- No JS tests; `main_test.go` does substring checks on embedded `app.js`/`index.html`.

## Risks

- DM replies vs CHAT-24 (no DM storage).
- Exact-field-set specs (CHAT-01/10/11/18, CHAT-22) must be amended; `omitempty` keeps
  join/leave/welcome byte-identical.
- History frame grows with embedded quotes → small truncation, flat snapshot (no nested quotes).
- Reply target evicted / unknown → define silent behavior (deliver without quote).
- `lastAuthor` grouping hides heads → every `li` needs its own id and button.

## Gaps

- Mobile/touch not in scope (desktop only, per the user).
