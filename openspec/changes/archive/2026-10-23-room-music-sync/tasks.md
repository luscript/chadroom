# Tasks: Room music sync

Specs: `specs/chat/spec.md` (CHAT-10, CHAT-21 modified; CHAT-30, CHAT-31 added),
`specs/server/spec.md` (SRV-05, SRV-06 modified). Design: `design.md`.
Batches A1 and A2 run in parallel (disjoint files).

## 1. Hub and server (batch A1 — Go + docs)

- [x] 1.1 `internal/chat/music.go` (new): constants, `videoID` check, `musicCmd`, `player`
      (`position`, `apply`, `halt`, `frame`, all taking `now`).
- [x] 1.2 `internal/chat/message.go`: `typeMusic`, `musicFrame` (exact fields
      `type,video,playing,pos,by,ts`), `inboundMusic`, `typeMusic` case in `parseInbound`
      (invalid → `ok=false`, never chat), `jsonMillis` (integers only, 0..86 400 000).
- [x] 1.3 `internal/chat/client.go`: `typingGate` → `rateGate{interval,last}`; typing 1 s,
      music 250 ms in `readPump`.
- [x] 1.4 `internal/chat/room.go`: `Room.player`, `NewMusicRoom(id)`; dispatch → `music(in)` →
      `publish`; snapshot after history in `admit` when a video is loaded; `halt` in `remove` when
      the last client leaves.
- [x] 1.5 `cmd/chatroom/main.go`: `musicRoom = "musica"`, `newChatServer` uses `NewMusicRoom` for
      it; `buildCSP` adds `frame-src https://www.youtube-nocookie.com`.
- [x] 1.6 Tests (design "Testing"): hub tests per action/invalid case, replay, empty-room pause
      (incl. slow drop), room without player, gate; `TestParseInbound` rows, `TestRateGate`, WS
      music tests; main_test: CSP literal, `TestMusicRoomMirroredInJS`, `TestMusicOnlyInMusica`,
      `TestIndexHasMusicPlayer`, `TestNoYouTubeScript` (A1 owns every main_test.go edit; markup
      contract from design).
- [x] 1.7 `README.md` (Spanish): `music` protocol, `musica` player, CSP `frame-src`, privacy note.
      `CLAUDE.md`: `music.go` in the structure; rule `musicRoom` (main.go) ↔ `MUSIC_ROOM` (app.js).

## 2. Page (batch A2 — frontend)

- [x] 2.1 `cmd/chatroom/index.html`: `#music` strip between `</nav>` and `.rule` (design markup,
      voseo labels). No inline code.
- [x] 2.2 `cmd/chatroom/app.js`: music block per design (`parseYouTubeId` with all CHAT-31 forms incl.
      `youtube.com/shorts` and `m.youtube.com/shorts`, `sendMusic`, `onMusicFrame`, `expectedMs`,
      `renderMusic`, `createPlayer` only after "Escuchar", `ytCommand`, `onYTMessage` with origin AND
      source checks, `reconcile` (2 s drift, settle window), `pauseLocal`, `resetMusic`,
      `showMusicError`); hooks in `addRaw`, `activate`, `setStatus`, `teardown`. Label
      "último cambio: <by>" via `textContent`.
- [x] 2.3 `cmd/chatroom/app.css`: `.music*` rules, both skins, mobile; iframe ≥ 200×200.

## 3. Verification

- [x] 3.1 `gofmt -l .`, `go vet ./...`, `go test -race ./...` green.
- [ ] 3.2 Manual UI checklist (design.md, two browsers) — user-verified; steps 1 and 7 first
      (referrer / error 153, error codes on localhost).
