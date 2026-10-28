# Apply Progress: global-music

## Batch 1 (crombie-worker) — SUCCESS

Completed: 1.1, 1.2, 1.3, 1.4, 1.5, 2.1, 2.2

Files changed:
- cmd/chatroom/app.js — `musicJoined`/`playerActive`/`music.seen`, park/unpark `#music`, tick while active, no pause on pane switch, `setMusicMuted`/`renderMusicMute` (persisted `chadroom.musicMuted`), `renderMusicHeader`, `pauseLocal` removed
- cmd/chatroom/index.html — `#music-now-playing`, `#music-global-mute` in header `.actions`
- cmd/chatroom/app.css — `.music.parked`, `.music-now-playing`, height rules exclude `.parked`
- cmd/chatroom/main_test.go — `TestIndexGlobalMusicMarkup`
- README.md — note on global music and 🎵 mute

Deviations: `#music-player.hidden = !music.frame` moved before `renderMusic`'s early return (keeps it right while parked). README mentions audio stops when leaving `musica`.

Checks: gofmt clean, `node --check` OK, `go test -race ./...` PASS. Manual browser checklist not run.

## Batch 2 (crombie-worker) — verify fixes — SUCCESS

F1–F8 applied (app.js, app.css, main_test.go): `seen` set in renderMusic, no off-pane retry while blocked, comments, `music.shown` removed, "Abrí #musica para escuchar", encodeURIComponent on video id, single-column parked strip, extra test assertions. gofmt/node --check/go test -race PASS.
