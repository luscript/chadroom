# Tasks: music-queue

Order: server state machine → parse + gate → tests → page (re-sync fix first) → CSS → docs.
Gates after each code group: `/snap/go/current/bin/gofmt -l .`, `go vet ./...`, `go test -race ./...`.

## 1. Server — state machine (`internal/chat/music.go`)

- [x] T101 Add `queueItem{ID int64, Video string, By string}` with JSON tags `id,video,by`; add
      `const maxMusicQueue = 30`. Delete the `load` action; add `add`, `remove`, `skip`, `ended`
      action constants.
- [x] T102 Add `player` fields: `seq int64`, `queue []queueItem`, `nextID int64`, `finished bool`.
      Add `musicCmd.id` and `musicCmd.seq` (int64).
- [x] T103 Add `start(item queueItem, now)` helper: set `video`, `pos` 0, `at` now, `playing` true,
      `finished` false, `seq++`, `by` = item.By.
- [x] T104 Rewrite `apply` per design CHAT-30 table: `add` (current when nothing current OR finished
      && empty queue, else append with `nextID++`, cap 30 → false; append leaves by/pos/at), `remove`
      (delete by id with `slices.Delete`; missing → false), `skip` (head via `start`; empty → false;
      works while paused/finished), `ended` (require `seq==p.seq` && !finished; non-empty → advance;
      empty → `finished=true`, `playing=false` at `position(now)`, by unchanged; else false),
      `play`/`seek` clear `finished` and set `by`, `pause` keeps `finished`. play/pause/seek need a
      current video.
- [x] T105 Update `frame(now)` to emit `seq` and a copied `queue` (`append([]queueItem{}, p.queue...)`
      so it is `[]` not `null`). Confirm `halt` keeps queue/seq/finished.

## 2. Server — protocol + gate (`message.go`, `client.go`)

- [x] T201 `musicFrame`: add `Seq` and `Queue` with JSON tags; field order must be
      `type, video, playing, pos, by, seq, queue, ts`. Rename `jsonMillis` → `jsonInt`.
- [x] T202 `parseMusic`: cases for `add` (valid 11-char video), `remove` (`id` int ≥ 1),
      `skip` (no fields), `ended` (`seq` int ≥ 1); `load` is now invalid; reuse `jsonInt` for id/seq.
- [x] T203 `client.go` `readPump`: add a second `rateGate{musicInterval}` used only when
      `in.music.action == "ended"`; the existing 260 ms gate still covers all other music controls.
- [x] T204 `room.go`: comment-only updates in `music()` and the `admit` snapshot (snapshot condition
      `video != ""` is unchanged).

## 3. Server tests (`room_test.go`, `client_test.go`)

- [x] T301 `room_test.go` helpers: `decodeMusic` asserts the 8 exact fields, integer `pos/ts/seq`,
      `queue` starts with `[` (never `null`), and `exactFields(id,video,by)` per element.
      `loadCmd`→`addCmd(video)`; add `skipCmd`, `removeCmd(id)`, `endedCmd(seq)`.
- [x] T302 `room_test.go` hub tests: rewrite `TestPlayerApply` (table per design T104 cases);
      add `TestPlayerFrameQueue`, `TestMusicAddBroadcast`, `TestMusicAddQueues`,
      `TestMusicSkipAdvances`, `TestMusicEndedOnce`, `TestMusicLastTrackEnds`, `TestMusicRemoveById`,
      `TestMusicQueueCap`; extend `TestMusicNeedsVideo`, `TestMusicNewcomerSnapshot`,
      `TestMusicEmptyRoomPauses`; `load`→`add` in the rest.
- [x] T303 `client_test.go`: `TestParseInbound` rows (add valid; `load` invalid; remove id valid/
      bounds/invalid; skip valid; ended seq valid/invalid per design); `load`→`add` in WebSocket
      tests; add `TestMusicEndedNotThrottledOverWebSocket`; leave `TestMusicThrottledOverWebSocket`.

## 4. Page — markup + re-sync fix first (`index.html`, `app.js`)

- [x] T401 `index.html`: add `#music-skip` (⏭) button next to toggle; add `<ol id="music-queue">`;
      `#music-consent` starts `hidden` with text "Tu navegador bloqueó el audio: apretá Escuchar.".
- [x] T402 `app.js` re-sync fix: replace the `activate` call at ~313 with `syncShown()` keeping a
      `music.shown` flag; on hidden→shown only, reset `settleUntil`/`playTries` and call
      `reconcile({seek:true})`. Add `seekLocal()` updating `music.time`/`timeAt` on every seek.
      Remove the state-0 restart lines; play-retry and drift checks skip state 0. "Escuchar" uses the
      same forced re-sync.
- [x] T403 `app.js` auto-start: remove the `consent` flag; `createPlayer` requires a current video +
      `musicShown()`; `#music-consent` shows only when `blocked`; update the header comment.
- [x] T404 `app.js` frame handling: `onMusicFrame` validator for the 8 fields incl. each queue item
      (id int ≥ 1, video `YT_ID`, by string) and cap 30; `loaded`→`loadedSeq`; add `endedSeq`,
      `staleEndUntil`, `MUSIC_QUEUE_MAX`, `MUSIC_END_GRACE_MS`.
- [x] T405 `app.js` ended reporting: `maybeReportEnded()` (fires when loadedSeq==srv.seq, endedSeq
      differs, and widget state 0 OR time fallback playing && expectedMs ≥ duration+2s); `sendEnded`
      bypasses the 300 ms gap; `setYTState` ignores state 0 for `MUSIC_STALE_MS` after any load/cue/seek.
- [x] T406 `app.js` queue UI: `renderQueue()` builds `<ol>` items with createElement+textContent
      (re-render only on frames, one delegated remove handler); `#music-skip` disabled when queue
      empty; Poner sends `add` and checks the cap locally ("La cola está llena"); ↺ sends `seek 0`
      then `play` after 320 ms, shown only when ended && paused.

## 5. Page — styles (`app.css`)

- [x] T501 `.music-queue*` styles (height capped ~7.5em, scroll); `.music-player` `grid-row: 1 / span 7`;
      pinkroom height 800px → 900px.

## 6. Page tests (`main_test.go`)

- [x] T601 `TestMusicOnlyInMusica`/`TestMusicSyncAcrossSessions` use `add`; `readMusicFrame` struct
      gains `Seq *int64` (non-nil, = 1). `TestIndexHasMusicPlayer`: id list gains `music-skip`,
      `music-queue`; `music-listen` still "Escuchar"; `#music-consent` starts `hidden`; html contains
      the blocked text; comment at line 658 updated; no-`<iframe`/no-YouTube-URL stay.
      `TestNoYouTubeScript` and the CSP test unchanged.

## 7. Docs (`README.md`, `CLAUDE.md`)

- [x] T701 `README.md`: protocol (add/remove/skip/ended, 8-field frame, queue cap, ended gate),
      auto-start, and the privacy note (opening `musica` contacts youtube-nocookie.com).
- [x] T702 `CLAUDE.md`: "En curso: music-queue"; `music.go` description mentions the queue.

## 8. Verify

- [x] T801 `gofmt -l .` clean, `go vet ./...` clean, `go test -race ./...` green.
- [ ] T802 Manual two-browser checklist (design.md, 11 steps; step 6 = re-sync bug). Deferred to the
      user if no two-browser setup is available — record in verify-report.
