# Apply Progress: music-queue

## Batch 1 (2026-10-23) — parallel, disjoint file sets

| Worker | Tasks | Files | Status |
|--------|-------|-------|--------|
| server logic | T101–T105, T201–T204 | internal/chat/{music,message,client,room}.go | SUCCESS — `go build ./...` passes; `go vet` on tests fails only on not-yet-updated `musicLoad` refs (expected, batch 2) |
| frontend | T401–T406, T501 | cmd/chatroom/{index.html,app.js,app.css} | SUCCESS — `node --check app.js` passes; cmd tests fail only on old `load` action (expected, batch 2) |
| docs | T701, T702 | README.md, CLAUDE.md | SUCCESS |

### Deviations (accepted)
- Server: added private `advance(now)` helper shared by skip/ended; `ended` also guards `p.video != ""` (belt-and-suspenders vs direct seq-0 apply).
- Frontend: `seekLocal()` also clears a lingering local state 0 (avoids false ended after ↺ seek); frame validator also requires finite `ts`; `renderQueue()` also runs on the no-snapshot join path and in `resetMusic` (which also resets shown/endedSeq/staleEndUntil).

### Pending
- Batch 2: T301–T303 (server tests), T601 (page tests).
- Verify: T801 (gofmt/vet/test -race), T802 (manual two-browser checklist — deferred to user).

## Batch 2 (2026-10-23) — parallel, disjoint test files

| Worker | Tasks | Files | Status |
|--------|-------|-------|--------|
| server tests | T301–T303 | internal/chat/{room,client}_test.go | SUCCESS — `go test -race -count=3 ./internal/chat/` green; gofmt/vet clean; no batch-1 logic bugs found |
| page tests | T601 | cmd/chatroom/main_test.go | SUCCESS — `go test -race ./cmd/...` green; markup already matched |

### Deviations (accepted)
- `TestMusicEndedOnce` uses a broadcast marker instead of `syncRoom` (a music room sends a snapshot frame to newcomers, breaking syncRoom's frame-order expectation); proves the same dedupe.
- `TestNewMusicRoom` switched to `reflect.DeepEqual` (player no longer `==`-comparable due to the queue slice).

### Remaining
- Verify: T801 (full gofmt/vet/test -race), T802 (manual two-browser checklist — deferred to user).

## Fix batch (2026-10-23) — post-verify

| Worker | Tasks | Files | Status |
|--------|-------|-------|--------|
| seq-reset fix + cleanups | T901–T904 | cmd/chatroom/app.js, internal/chat/{music,room}.go, internal/chat/room_test.go | SUCCESS — gofmt/vet/build/test -race + node --check all pass |

- T901: page resets loadedSeq/endedSeq/duration on no-snapshot join and on regressed/same-seq-different-video frames (server-restart bug).
- T902: keyed musicFrame literal (protects spec 8-field order). T903: stale comments. T904: "ended while paused advances" test.

## Verify (2026-10-23)
- Verdict PASS (verify-report.md). Gates green; security clean; code review MEDIUM fixed; LOWs addressed/accepted.
- T801 done. T802 (manual two-browser checklist) DEFERRED to user.

## Bug-fix batch T905 (2026-10-23) — post manual-test

User manual-tested and found: a track ending REPEATED instead of advancing to the next queue item.
- Root cause (confirmed): the nocookie embed often leaves `music.duration` at 0 (duration delivered inside the 1s `ignoreInfoUntil` window is dropped and never resent). With duration 0, `musicEnded()` is always false, the time-based `ended` report never fires, and reconcile's keep-alive `playVideo` restarts the finished video from 0. Server was correct all along.
- Fix (cmd/chatroom/app.js only): per-seq `finishedSeq` latch via `trackDone()`; `checkLocalEnd()` detects end via widget state 0, duration+grace, OR a `currentTime` plateau (MUSIC_PLATEAU_MS) that does NOT need a known duration; `maybeReportEnded` resends every 3s while the server still plays the same seq; all reconcile replay paths suppressed once done; latch clears only on a new seq / server seek / paused→playing / seq regression / resetMusic / manual ↺.
- Gates: gofmt/build/test -race/node --check all pass.
- Documented risk: the plateau net can false-positive on a long network stall when duration is unknown (could skip a track / mark finished). Mitigated by the 2s threshold and near-end check when duration is known.
- STILL NEEDS user browser re-test (checklist steps 3 and 4).
