# Verify Report: music-queue

**Verdict: PASS**
**Date**: 2026-10-23

FAIL triggers on any CRITICAL/HIGH finding or any failing gate. None present. One MEDIUM
(code review) was fixed before this verdict. Manual two-browser checklist (T802) is deferred to
the user — automated verification cannot run it.

## Gates (crombie-worker, Bash)

| Gate | Result |
|------|--------|
| `gofmt -l .` | clean (no output) |
| `go vet ./...` | clean |
| `go build ./...` | ok |
| `go test -race ./...` | PASS — `chatroom/cmd/chatroom` ok, `chatroom/internal/chat` ok (re-run clean after the fix batch) |

## Code review (crombie-reviewer, static)

- CRITICAL: none. HIGH: none.
- **MEDIUM (FIXED):** page-side track identity broke after a server restart/reconnect — `seq`
  restarts at 1, the page thought it was the already-loaded track, never reloaded the video, and
  `maybeReportEnded` stayed blocked (queue could stall on that client). Fixed in T901: the
  no-snapshot join branch now resets `loadedSeq`/`endedSeq`/`duration`, and `onMusicFrame` treats a
  regressed or same-seq/different-video frame as a reset so `reconcile` issues a fresh load.
- LOW (addressed): keyed `musicFrame` literal to protect the spec's 8-field order (T902); stale
  comments in room.go / room_test.go / app.js (T903); added an "ended while paused advances" hub
  test (T904).
- LOW (accepted, not changed): `musicEnded()` naming overlap; ↺ sends two controls by design;
  `by` nickname can be reassigned after a leave (cosmetic, same as chat history).
- Residual (accepted, documented by the fix worker): a restart that yields a *higher* seq, or the
  same seq with the same video, is still undetectable by the page. Not reachable in practice
  (restart resets seq low) — noted for the manual checklist.

## Security audit (crombie-security, static, OWASP)

- CRITICAL/HIGH/MEDIUM: none.
- LOW (all within the accepted friends-chat trust model): anyone can remove/skip/drain the queue;
  `by` nickname reuse is cosmetic; the two per-connection gates allow ~7.7 music frames/s total
  (bounded, not abusive — a forged/stale `ended` is rejected before broadcast).
- Verified: queue cap 30 enforced server-side; no slice-bounds/nil-deref panics; integer bounds on
  id/seq/pos; `nextID` monotonic (no reuse/forgery); frame queue is copied (no aliasing into client
  buffers); CSP unchanged and strict (no YouTube script, postMessage origin+source checked); no
  iframe string reaches the DOM; no secret/PII in music frames.

## Deferred to user

- **T802** — manual two-browser checklist (design.md, 11 steps). Priority: step 6 (re-sync on
  return) and steps 3/4/8 (auto-advance, ↺ replay, repeated-id reload), plus a restart test for the
  T901 fix. Automated Go tests cover the server and markup; browser behavior needs a human.
