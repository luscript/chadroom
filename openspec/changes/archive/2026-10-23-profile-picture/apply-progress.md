# Apply progress: profile-picture

## Batch 1 (2026-10-23) — parallel

| Worker | Tasks | Status |
|--------|-------|--------|
| crombie-worker (batch 1) Go | 1.1–1.6, 3.1 | SUCCESS |
| crombie-worker (batch 1) frontend | 2.1–2.3 | SUCCESS |

### Files changed

- `internal/chat/{message,client,server,room}.go` — avatar on Client, `?avatar` filtered by the
  injected validator, `avatar` on welcome/join/chat (history keeps it), `avatars` in users frame.
- `internal/chat/{room,server,client}_test.go` — hub and integration avatar tests, forged-avatar test.
- `cmd/chatroom/main.go` — `maxAvatarBytes`, `avatarPath`, `validAvatar`, wired into `NewServer`.
- `cmd/chatroom/main_test.go` — `TestValidAvatar`, `TestWebSocketAvatarWiring`, `TestIndexHasAvatarField`.
- `cmd/chatroom/{index.html,app.js,app.css}` — join field + hint, localStorage `chadroom.avatar`,
  per-room `room.avatars`, `letterAvatar`/`avatarEl`, avatars in messages/online list/profile.
- `README.md` — `?avatar=`, `avatar`/`avatars` fields (Spanish).

### Gates (worker-run)

`gofmt -l .` clean, `go vet ./...` clean, `go test -race ./...` pass; `node --check app.js` ok.

### Deviations from design

- `startAvatarRooms(t, valid, allowed, rooms...)` keeps the `allowed` argument.
- Forged-avatar check is an integration test (`TestForgedInboundAvatarIgnored`).
- Page also updates `room.avatars` on `welcome`; `addChat` takes an optional 5th `avatarUrl` arg;
  `#profile-avatar` error listener registered once at load.

### Pending

- 4.1 independent gate run (verify), 4.2 manual UI checklist (user).
