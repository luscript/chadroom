# Verify report: room-music-sync

Date: 2026-10-23. Sources: crombie-reviewer (static), crombie-security (static), crombie-worker
(gates). Specs: CHAT-10, CHAT-21, SRV-05, SRV-06 (modified); CHAT-30, CHAT-31 (added).

## Verdict: PASS (after fix round 1)

Round 1 was **FAIL** (two HIGH, frontend sync). Fix round 1 resolved them; the orchestrator
reviewed the fixed code (`musicTick`, handshake timeout, `createPlayer` guard, `sendMusic`) and
the worker re-ran every gate green.

## Gates

| Gate | Round 1 | After fix round 1 |
|------|---------|-------------------|
| `gofmt -l .` | clean | clean |
| `go vet ./...` | clean | clean |
| `go test -race ./...` | pass | pass |
| `-race -count=5` music/parse/gate/CSP tests | pass | pass (`Music\|Gate`) |
| `node --check app.js` | ok | ok |
| Coverage | cmd/chatroom 92.4%, internal/chat 97.3% | — |

## Findings

| ID | Sev | Source | Finding | Resolution |
|----|-----|--------|---------|------------|
| H1 | HIGH | review | `musicTick` didn't reconcile → stalled player never retried/healed | Fixed |
| H2 | HIGH | review | Handshake timeout was a dead end (no way to retry) | Fixed: frame removed, "Escuchar" shown again |
| M1 | MEDIUM | review + security L3 | Iframe created while viewing another room (hidden) | Fixed: only when `musicShown()` |
| M2 | MEDIUM | review | Sends within 300 ms silently dropped (seek, Poner) | Fixed: trailing seek, "Esperá un segundo…" |
| M3 | MEDIUM | review | Drift seek on a cued video could start audio while paused | Fixed: `pauseVideo` after seek |
| M4 | MEDIUM | review | Early local end restarted the track from 0 | Fixed: seek to expected before play |
| M5 | MEDIUM | review | Embed errors showed "Escuchar" again (misleading) | Fixed: `music.errored` |
| M6 | MEDIUM | review | `type=url` native bubble hid the page's error | Fixed: `novalidate` |
| L1 | LOW | review | 250 ms gate admits 5 in a closed 1 s window | Fixed: 260 ms |
| L2 | LOW | review | Seek bar disabled until "Escuchar" (no duration) | Accepted (design); backlog |
| L3 | LOW | review | Frame during buffering may cause a drift seek (jitter) | Accepted; backlog |
| S1 | LOW | security | Iframe has no `sandbox` | Backlog: try `allow-scripts allow-same-origin` with a manual browser check |
| S2 | LOW | security | Origin + IP reach YouTube after "Escuchar" | Accepted (required by embed; consent line) |
| S4 | LOW | security | Anyone can change the music (griefing) | Accepted (user decision) |
| S5 | LOW | security | N connections × 4 controls/s fan-out | Backlog (`rate-limiting`) |
| S6 | LOW | security | No `Permissions-Policy` header | Backlog (`ops-hardening`) |

## Spec coverage

CHAT-21 rule 2a and CHAT-30 scenarios are covered by hub, parse and WebSocket tests (incl.
empty-room pause and last-slow-drop pause); SRV-05/06 by the CSP literal, `TestMusicOnlyInMusica`
and `TestNoYouTubeScript`; CHAT-31 markup/constants by `TestIndexHasMusicPlayer` and
`TestMusicRoomMirroredInJS`. CHAT-31 behavior (sync, consent, errors) relies on the manual
two-browser checklist in `design.md` — pending; run steps 1 and 7 first (referrer / error 153).
