# Archive Report: music-queue

**Archived**: 2026-10-23
**Commit**: 0bc2fd9 feat(music): shared playback queue with auto-advance
**Branch**: feat/room-music-sync (PR prepared, not yet merged)

## Outcome

Shared playback queue for the `musica` room: add/remove/skip, auto-advance on end, auto-start on
opening the pane, and the return re-sync fix. Delivered through the full SDD pipeline
(explore → propose → spec → design → tasks → apply → verify) plus two post-verify bug fixes.

## Spec deltas merged into `openspec/specs/`

- `specs/chat/spec.md`: CHAT-21 (inbound fields gain `id`, `seq`); CHAT-30 replaced (queue state,
  add/remove/skip/ended, 8-field frame with `seq`+`queue`, seq dedupe, separate `ended` gate, cap
  30); CHAT-31 replaced (queue UI, skip, auto-start + blocked fallback, re-sync on return,
  duration-independent end reporting).
- `specs/server/spec.md`: SRV-05 "Page works under the policy" scenario updated (queue walk, no
  mandatory "Escuchar"). SRV-06 unchanged.

## Verification

- Gates green: `gofmt`, `go vet`, `go build`, `go test -race ./...`.
- Static code review + OWASP security audit: no CRITICAL/HIGH. One MEDIUM (re-sync after server
  restart) found and fixed (T901). Security LOWs all within the accepted friends-chat trust model.

## Post-verify fixes

- **T901–T904**: server-restart seq-reset re-sync, keyed `musicFrame` literal, stale comments, an
  "ended while paused" hub test.
- **T905** (from the user's manual test — a track repeated instead of advancing): end-of-track
  detection no longer depends on a known duration (widget state 0, duration+grace, or a
  `currentTime` plateau), and a finished track is latched per `seq` so the keep-alive never replays
  it.

## Deferred / follow-ups (to backlog)

- **T802 — manual two-browser checklist** still pending (auto-advance, ↺ replay, re-sync, repeated
  ids). The T905 fix in particular needs browser confirmation.
- **T905 plateau trade-off**: a long network stall with an unknown duration could false-trigger the
  end detection (skip a track). Mitigated by the 2 s threshold and the near-end check; tighten only
  if it shows up in practice.
- Track titles/thumbnails (oEmbed) — out of scope, candidate follow-up.
- HawkScan DAST not run (no `HAWK_API_KEY`, server not up); static security audit covered this
  change.

## Token usage

See `token-tracker.md` (status: complete). Sub-agent total ≈ 683 K; orchestrator in/out ≈ 252 K
(cache excluded). Best-effort figures.
