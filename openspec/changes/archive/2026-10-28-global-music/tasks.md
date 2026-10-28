# Tasks: global-music

## 1. Implementation

- [x] 1.1 `app.js`: add `musicJoined()`, `playerActive()`, `music.seen`; change gates per design table (`renderMusic` park/unpark + tick while `shown || playerActive()`, `createPlayer`, `reconcile`, `syncShown` without pause/forced seek, `resetMusic` clears `seen`); delete `pauseLocal`; update comments.
- [x] 1.2 `app.js`: `MUSIC_MUTED_KEY = 'chadroom.musicMuted'`, load at init, `setMusicMuted()` + `renderMusicMute()` shared by `#music-mute` and `#music-global-mute`.
- [x] 1.3 `app.js`: `renderMusicHeader()` (global mute visibility, now-playing text `♪`/`⏸`/blocked, textContent only), called first in `renderMusic()`.
- [x] 1.4 `index.html`: `#music-now-playing` and `#music-global-mute` in `.actions` before `#mute-btn`.
- [x] 1.5 `app.css`: `.music.parked` rules, `.music-now-playing` styles (hidden ≤480px), height rules to `#music:not([hidden], .parked)`.

## 2. Testing and docs

- [x] 2.1 `main_test.go`: `TestIndexGlobalMusicMarkup` per design; existing tests stay green; `gofmt` + `go test -race ./...`.
- [x] 2.2 `README.md`: short note on music continuing in other rooms and the 🎵 local mute.
