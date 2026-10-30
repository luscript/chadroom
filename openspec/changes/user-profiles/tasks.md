# Tasks: user-profiles

Design: `design.md`. Specs: `specs/chat/spec.md` (CHAT-10/17/21/28/29 modified, CHAT-37…40 added).

Groups A (backend + README) and B (frontend + `main_test.go`) touch disjoint files and run in
parallel. Inside group B every task touches `app.js`, so it runs sequentially in one worker.

## 1. Backend protocol (internal/chat) — group A

- [x] 1.1 `nickname.go`: extract `cleanText(s, maxRunes)`; `cleanNickname` delegates; `maxBioRunes = 160`
- [x] 1.2 `message.go`: `typeProfile`; `message.Bio` (omitempty, after `Avatar`); `usersFrame.Bios`; `profileFrame{type,from,ts,avatar?,bio?}`; `inboundProfile` kind with `avatar`/`bio`; `parseInbound` case (absent or string only, anything else drops the frame; bio cleaned) (CHAT-21 2b)
- [x] 1.3 `room.go` / `server.go`: validator moved to `Room.validAvatar` (set by `NewServer`), `allowedAvatar` helper; `?bio=` read and cleaned; `serveConn(…, bio)`; bio in `welcome`/`join`; `memberValues` → `avatars`/`bios` in `users`; `dispatch` case + `Room.profile` (mutate in `Run`, `publish` profileFrame) (CHAT-28/37/38)
- [x] 1.4 `client.go`: `Client.bio`; in `readPump` after parse: avatar check, then `profileInterval = 1s` rate gate (CHAT-38)

## 2. Backend tests — group A

- [x] 2.1 `room_test.go`: hub unit tests per design Test Plan (bio in welcome/join/users, profile broadcast exact shape, clear, history keeps old avatar, no stale bio on nickname reuse)
- [x] 2.2 `client_test.go`: `TestParseInbound` rows for profile frames (valid, absent keys, non-string → dropped, not chat); `cleanText` tests
- [x] 2.3 `server_test.go`: integration tests per design (`?bio=` cleaned/truncated/blank, profile end-to-end, invalid avatar discards, rate limit, forged `from` ignored); `TestForgedInboundAvatarIgnored` stays green

## 3. Docs — group A

- [x] 3.1 `README.md`: `bio` param, `bio`/`bios` fields, `profile` frame in/out, avatar now editable, rate limit; frontend usage (Mi perfil, popup); remove join-form avatar mention

## 4. Frontend (cmd/chatroom) — group B (sequential, same files)

- [x] 4.1 State & frames (`app.js`): `myProfile` replaces `requestedAvatar`; `connect()` appends `avatar`/`bio`; remove join-form avatar field (`index.html`) + `localStorage` code, `removeItem('chadroom.avatar')` at startup; `room.bios`; `setMemberProfile`; `profile` frame in `addRaw`; `resendProfile` on `welcome`; logout resets profile; `renderProfile` shows own bio (CHAT-29/39)
- [x] 4.2 Editor & GIF pick mode: `#profile-btn` (header) + scrapbook card open `#profile-panel` (`panels.profile`, `showPanel`); preview, "Elegir GIF" (pick mode, `#gif-paste` hidden), "Quitar foto", bio textarea 160 + counter, "Guardar" (send on all open sockets, 1.1 s lock); Escape order (CHAT-39)
- [x] 4.3 User popup & triggers: `#user-popup` (body level, fixed, centered, `role="dialog"`); `button.user-trigger` in `addChat` (name + avatar) and `renderUsers` rows; "Mensaje privado" → `openConversation` / "Editar perfil" for self; live update; close rules; focus handling; textContent only (CHAT-40)
- [x] 4.4 `app.css`: editor, pick mode, popup, triggers — existing tokens only, mobile-safe
- [x] 4.5 `main_test.go`: `TestIndexHasNoAvatarField` (replaces `TestIndexHasAvatarField`), `TestIndexHasProfileEditor`, `TestIndexHasUserPopup`; no-inline-code test still passes

## 5. Verification

- [x] 5.1 `gofmt` clean and `go test -race ./...` green
- [ ] 5.2 Manual two-browser checklist (`design.md`) — user
