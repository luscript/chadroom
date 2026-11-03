# Exploration: user-profiles

Synthesis of two `crombie-explore` scopes (backend `internal/chat`, frontend `cmd/chatroom`).

## User decisions (2026-10-30)

- Profile is **ephemeral**: JS memory only; wiped on logout or closing the tab, resent on
  automatic reconnect. No server persistence, no browser storage.
- Avatar is picked from the existing **Giphy GIF picker** (no URL pasting).
- Edits are visible to others **instantly**.
- Bio: plain text, up to 160 characters, same control/bidi filtering as nicknames.
- Clicking a user in the **online list and in messages** opens a popup with avatar, nickname,
  bio and a **"Mensaje privado"** button.

## Backend findings

- Avatar validation is injected: `Server.validAvatar func(string) bool` (`server.go:87-91`),
  real validator `validAvatar` in `cmd/chatroom/main.go:56-67` (https, host in `imageHosts`,
  ≤512 bytes, image extension). Invalid → silently dropped at connect.
- `Client.avatar` is set before register and documented as immutable (`client.go:34-40`). It is
  read only inside `Room.Run` (`admit`, `relay`, `avatars()`), so mutating it inside `Run` is
  race-free (hub pattern, no locks).
- Avatar flows into `welcome`/`join` (`admit`), `chat` (`relay`, snapshot stored in history) and
  `users` (`avatars` map). Never on `leave`, `dm`, `dm_error`, `typing`.
- `parseInbound` (`message.go:139-183`) accepts `dm`, `typing`, `music`, `chat`; anything else is
  treated as chat text → a `profile` type needs an explicit case.
- Every inbound kind goes through `Room.broadcast` → `dispatch` in `Run`
  (`room.go:76-93,215-232`). New `inboundProfile` kind + `Room.profile` handler fits naturally;
  `publish` gives must-have delivery.
- Rate gates (`rateGate`) exist in `readPump` for typing (1 s) and music; none for profile.
- `cleanNickname` (`nickname.go:10-39`): `ToValidUTF8`, strip `IsControl` + `Bidi_Control`,
  `TrimSpace`, `truncateRunes(32)` — generalizable for bio.
- Tests: hub unit helpers (`startRoom`, `newTestClient`, `avatarClient`, `exactFields`…) and
  httptest integration helpers (`startAvatarRooms`, `dialAvatar`, `readMsg`…).
  `TestForgedInboundAvatarIgnored` must stay green.

## Frontend findings

- Avatar today: join-form field `#avatar-url` (`index.html:29-34`), saved in `localStorage`
  key `chadroom.avatar` (`app.js:351-354,393-395`), kept in `requestedAvatar` and appended to
  every WS URL (reconnect included, `app.js:411`). Conflicts with the ephemeral requirement.
- GIF picker click handler is hard-wired to `sendText(g.url)` (`app.js:1149`); panel lives in
  the composer (`#send-form`) with a paste-URL box. Needs a pick-mode/callback.
- DOM built with `createElement`/`textContent` only (no `innerHTML`). `addChat` builds the
  avatar slot and `span.author` as non-interactive (`app.js:862-883`); `renderUsers`
  (`app.js:762-788`) has a per-row "Mensaje privado" button → `openConversation(room, peer)`.
- Per-room state: `room.users` (Set), `room.avatars` (Map), reset in `joinRoom`, `teardown`,
  `users`, `leave`. Each room has its own socket.
- Popovers: `.popover` panels in the `panels` map with `togglePanel`/`closePanels`; global
  Escape and outside-click (`.popover, .tool`) close them. No dialog/focus management.
- "MI PERFIL" scrapbook card (`renderProfile`, static "vibecoder profesional" text) is hidden
  under 800px → mobile needs another entry point (header).
- Logout (`#leave-btn`) does not clear `requestedNick`/`requestedAvatar`.
- CSP: no inline scripts/handlers/styles; images only from `imageHosts`.

## Risks

- Profile spam fans out to the whole room → rate gate.
- Validator lives in `main`; `Room` needs it injected (nil = drop avatar, test convention).
- Nickname reuse must not inherit a previous bio (reset like avatars on join/leave).
- Old message bubbles keep the avatar they were sent with (same as server history).
- Popup outside-click race, Escape priority, mobile overflow.
