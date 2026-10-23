# Tasks: Profile picture

Specs: `specs/chat/spec.md` (CHAT-10, CHAT-17 modified; CHAT-28, CHAT-29 added).
Design: `design.md`. Batches: A1 and A2 run in parallel (disjoint files).

## 1. Hub and server wiring (batch A1 — Go)

- [x] 1.1 `internal/chat/message.go`: `message.Avatar` (`json:"avatar,omitempty"`, last field) and
      `usersFrame.Avatars map[string]string` (`json:"avatars,omitempty"`); update doc comments.
- [x] 1.2 `internal/chat/client.go`: immutable `Client.avatar` field (set before register).
- [x] 1.3 `internal/chat/server.go`: `NewServer(allowedOrigins, validAvatar func(string) bool, rooms...)`
      (nil = avatars off); `HandleWebSocket` reads `?avatar`, keeps it only if the validator accepts
      it, passes it to `serveConn`; never rejects the connection for it.
- [x] 1.4 `internal/chat/room.go`: `serveConn(conn, nickname, avatar)`; keyed literals; `Avatar` on
      welcome/join/chat (history keeps it); Run-only `avatars()` helper for the users frame;
      leave/dm/dm_error/typing unchanged.
- [x] 1.5 `cmd/chatroom/main.go`: `maxAvatarBytes = 512`, `avatarPath`, `validAvatar` (CHAT-28 rules,
      URL not rewritten); `newChatServer` passes it.
- [x] 1.6 Tests: compile fixes (`nil` NewServer args, keyed `message` literals); hub tests
      `TestAvatarOnWelcomeAndJoin`, `TestUsersFrameAvatars`, `TestChatAndHistoryKeepAvatar`,
      `TestAvatarNotOnLeaveDMTyping`, `TestReusedNicknameNoStaleAvatar`; integration
      `TestAvatarQueryParam`, `TestAvatarDisabledWithoutValidator` (helpers `startAvatarRooms`,
      `dialAvatar`); main `TestValidAvatar`, `TestWebSocketAvatarWiring`, `TestIndexHasAvatarField` (ids
      `avatar-url` type=url maxlength=512 after `nick`, `profile-avatar` — markup contract from
      design; A1 owns every main_test.go edit), plus a forged-avatar case
      (inbound `{"type":"chat","text":"hi","avatar":"…"}` relays without `avatar`).

## 2. Page (batch A2 — frontend)

- [x] 2.1 `cmd/chatroom/index.html`: optional `#avatar-url` (type=url, maxlength 512, label
      "Foto de perfil (opcional)", hint) after `#nick`; `#profile-avatar` in `.profile-art`. No inline code.
- [x] 2.2 `cmd/chatroom/app.js`: `MAX_AVATAR_BYTES`, `requestedAvatar` (validated with
      `isAllowedImage` + byte check; inline error in `#join-error`; localStorage `chadroom.avatar`
      best-effort in try/catch); `&avatar=` on every connect/reconnect when set; per-room
      `room.avatars` Map from welcome/users/join/leave (join without avatar deletes); `letterAvatar`,
      `avatarEl(name, url)` (re-check `isAllowedImage`, `referrerPolicy=no-referrer`, `alt=""`,
      lazy, error → letter); `addChat` uses the frame avatar (room/history) or `room.avatars` (DM);
      `renderUsers` 20px avatar; `renderProfile` fills `#profile-avatar` (error → ♡).
- [x] 2.3 `cmd/chatroom/app.css`: `.field-hint`, `img.avatar`, `.users-list .avatar`, `.profile-avatar`
      (both skins).

## 3. Docs (batch B — after A)

- [x] 3.1 `README.md`: `?avatar=` on `/ws`; `avatar` / `avatars` in the protocol table.

## 4. Verification

- [x] 4.1 `gofmt -l .`, `go vet ./...`, `go test -race ./...` green.
- [ ] 4.2 Manual UI checklist (design.md) — user-verified.
