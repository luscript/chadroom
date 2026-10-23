# Exploration: profile-picture

Synthesized by the orchestrator from two crombie-explore agents (backend, frontend), 2026-10-23.
Source backlog group: `profile-picture`.

## Key findings

- **Only per-client datum today is the nickname.** `Client{conn, send, registered, nickname}`
  (`internal/chat/client.go`). The nickname arrives as `?nickname=` on `GET /ws`, is normalized
  in `HandleWebSocket` (`server.go`) and passed to `room.serveConn`. `Room.admit` assigns the
  final name, delivers `welcome`, `users`, `history`, then publishes `join`.
- **Wire shapes.** `message{type,from,text,ts}` (no omitempty) for chat/join/leave/welcome;
  `usersFrame{users []string}`; `historyFrame{messages []message}`; `dmFrame{from,to,text}`.
  `relay()` stores `message` values in the 50-entry history. Tests assert exact field sets
  (`frameFields`/`exactFields`), so a new field ripples into tests.
- **Inbound parsing** (`parseInbound`): JSON with `type` chat/dm/typing, anything else falls back
  to plain chat text. A new inbound type would leak into the room as text unless handled.
- **Image allowlist.** `imageHosts` (`cmd/chatroom/main.go`) feeds CSP `img-src` and mirrors
  `IMAGE_HOSTS` (`app.js`). The chat package has no URL knowledge. `canonicalGIF` (`gifs.go`) is
  Giphy/.gif-only, so not reusable as-is but a good template. `isAllowedImage` (`app.js`) is
  reusable client-side: https, exact host on `IMAGE_HOSTS`, no port/credentials/fragment,
  path ending `.gif|.webp|.png|.jpe?g`.
- **Frontend.** `addChat(pane, author, text, ts)` already renders a 30px `div.avatar` (first
  letter) in `div.avatar-slot`, only when the author changes; same path for rooms, DMs and
  history. `renderUsers()` builds `li > span(name) + tag/button` from `room.users: Set<string>`.
  `aside.scrapbook` "MI PERFIL" has `#profile-nick` and a `.profile-art` placeholder (hidden at
  <=800px). Join form has one field (`#nick`). No localStorage, no innerHTML.

## Relevant files

- `internal/chat/{client,server,room,message}.go` — per-client field, query param, frames.
- `internal/chat/{room,server}_test.go` — exact-shape assertions, helpers.
- `cmd/chatroom/main.go` — `imageHosts`, `newChatServer`, CSP.
- `cmd/chatroom/{index.html,app.js,app.css}` — join form, `addChat`, `renderUsers`, scrapbook.
- `openspec/specs/chat/spec.md` (CHAT-01..27), `openspec/specs/server/spec.md` (SRV-01..08).

## Risks

- Avatar URLs are attacker-controlled and make every viewer's browser fetch them (IP leak /
  tracking pixel), automatically rather than click-driven. Hosts are the same ones already
  trusted for GIFs; keep `referrerPolicy=no-referrer`; validate server- and client-side.
- Adding fields to `message`/`usersFrame` changes CHAT-17/18 shapes; prefer additive,
  omitempty fields.
- Identity is the nickname per room; avatars are per connection and lost when the session ends.
- gin access log records the `/ws` query (nickname already appears there).

## Gaps

- Mid-session avatar change would need a new inbound frame and a broadcast; not explored further.
