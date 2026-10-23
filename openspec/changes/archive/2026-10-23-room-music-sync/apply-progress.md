# Apply progress: room-music-sync

## Batch 1 (2026-10-23) — parallel

| Worker | Tasks | Status |
|--------|-------|--------|
| crombie-worker (batch 1) Go + docs | 1.1–1.7 | SUCCESS |
| crombie-worker (batch 1) frontend | 2.1–2.3 | SUCCESS |

### Files changed

- `internal/chat/music.go` (new) — `player` state with pure `position/apply/halt/frame(now)`.
- `internal/chat/message.go` — `typeMusic`, `musicFrame`, `parseMusic`, `jsonMillis`; invalid music → dropped.
- `internal/chat/client.go` — `rateGate{interval,last}` (typing 1 s, music 250 ms).
- `internal/chat/room.go` — `Room.player`, `NewMusicRoom`, `music(in)` → `publish`, snapshot in `admit`, halt in `remove`.
- `cmd/chatroom/main.go` — `musicRoom`, `NewMusicRoom("musica")`, CSP `frame-src https://www.youtube-nocookie.com`.
- Tests: `internal/chat/{room,client}_test.go`, `cmd/chatroom/main_test.go`.
- `cmd/chatroom/{index.html,app.js,app.css}` — `#music` strip, music block (parser, handshake, reconcile, consent), styles.
- `README.md`, `CLAUDE.md`.

### Gates (worker-run)

`gofmt -l .` clean, `go vet ./...` clean, `go test -race ./...` pass (+ `-count=3` on music tests);
`node --check app.js` ok; `parseYouTubeId` spot-checked in node (11 links).

### Deviations from design

- `TestIndexHasMusicPlayer` bans `<iframe` and YouTube URLs in index.html (not the word "YouTube",
  which the design's own labels use). `TestNoYouTubeScript` bans more script URL shapes.
- Empty-room pause test checks pause by identical `pos` across two newcomers (no flaky upper bound).
- Frontend: separate `YT_EMBED` literal; `.btn-accent` added; pink-skin tweaks after the override
  block; play retried twice then `blocked`; drift seek only when state ≠ -1; Repetir sends `seek 0` only.
- Label "último cambio: <by>" (replaces "puesto por").

### Pending

- 3.1 independent gate run (verify), 3.2 manual UI checklist (user, two browsers).
