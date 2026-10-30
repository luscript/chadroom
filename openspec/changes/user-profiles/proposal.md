# Proposal: user-profiles

## Intent

Give each connected user an ephemeral profile — profile picture + short description (bio) —
that they can open and edit while chatting, and let everyone click a user (online list or a
message) to see a small popup with that user's picture, nickname and bio, plus a button to
start a private conversation.

## Scope

**In scope**
- Bio: plain text, at most 160 runes, cleaned like nicknames (invalid UTF-8 replaced, control
  and bidi-control characters stripped, trimmed). Empty = no bio.
- `GET /ws` gains an optional `bio` query parameter (parallel to `avatar`), so the profile is
  restored automatically on reconnect and late joiners learn it from `join`/`users`.
- `welcome` and `join` carry `bio` when set; `users` gains a `bios` map (only users with a bio).
  `chat`, history, `leave`, `dm`, `typing` never carry `bio`.
- New inbound frame `{"type":"profile","avatar":"…","bio":"…"}` replaces the sender's whole
  profile while connected. Avatar validated with the same rule as at connect; an invalid
  non-empty avatar drops the whole frame. Empty avatar / bio clear them.
- New outbound frame `profile` (`from`, `ts`, optional `avatar`, optional `bio`) published to
  everyone in the room, sender included. Rate-limited per connection.
- CHAT-28 amended: the picture is no longer fixed for the connection; it changes only via a
  valid `profile` frame. History entries keep the avatar they were relayed with.
- Frontend:
  - Remove the join-form avatar URL field and its `localStorage` key (and remove any value
    already stored under `chadroom.avatar`).
  - "Mi perfil" button in the header (visible on desktop and mobile) and the existing
    "MI PERFIL" card open an edit panel: picture preview, "Elegir GIF" (opens the GIF picker in
    avatar mode, no URL box), "Quitar foto", bio textarea with a counter, "Guardar".
  - Profile kept only in JS memory, sent on every room socket (URL on connect/reconnect,
    `profile` frame on edit); cleared on logout.
  - Clicking a user's picture or name in a message, or a row of the online list, opens a
    user popup: picture, nickname, bio (or "Sin descripción"), "Mensaje privado" (hidden for
    yourself; for yourself, "Editar perfil" instead).
  - The "MI PERFIL" card shows the own bio instead of the static text.

**Out of scope**
- Persistence of any kind (server or browser), accounts, uploaded files, non-Giphy pickers.
- Rewriting avatars of already-rendered messages or history entries.
- Popups for users not in the current room's online list (e.g. history authors who left).
- Multi-line bios, links/formatting in bios.

## Approach

Hub pattern unchanged: `Client` gets `bio`; `avatar`/`bio` are mutated only inside
`Room.Run` by a new `inboundProfile` dispatch case. The avatar validator is injected into each
`Room` by `NewServer` (nil = every avatar dropped, existing test convention). A shared
`cleanText(s, maxRunes)` serves nicknames and bios. `readPump` gets a profile `rateGate`.

Frontend keeps `myProfile = {avatar, bio}` next to `requestedNick`; `connect()` appends both
params; edits send a `profile` frame on every joined room socket and the server echo updates
the UI. Per-room `room.bios` mirrors `room.avatars`. The GIF picker gets a pick callback mode.

## Affected Areas

| Area | Change |
|------|--------|
| `internal/chat/message.go`, `nickname.go` | `bio` fields, `bios` map, `profile` type/frame, `parseInbound` case, `cleanText` |
| `internal/chat/room.go`, `client.go`, `server.go` | `Room.profile` handler, injected validator, `?bio=`, profile rate gate |
| `internal/chat/*_test.go` | unit + integration tests |
| `cmd/chatroom/main.go`, `main_test.go` | wire validator; markup checks |
| `cmd/chatroom/app.js`, `index.html`, `app.css` | profile editor, GIF avatar mode, user popup, state/reset |
| `README.md`, `openspec/specs/chat/spec.md` | protocol docs; CHAT-10/11/17/21/28/29 amended, CHAT-37/38 added |

## Risks & mitigations

- **Profile spam** → per-connection rate gate (1 frame/s, excess dropped silently) and
  ≤160-rune bio.
- **Stored-XSS via bio** → server strips control chars; page renders with `textContent` only.
- **Avatar SSRF/IP leak** → same allowlist validation server-side and the CHAT-15 check
  page-side before loading.
- **Nickname reuse inherits a bio** → page forgets bios on `leave`/`join`, as with avatars.
- **Exact-shape tests** → new fields `omitempty`; frames without profile data unchanged.
