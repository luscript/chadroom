# Archive report: music-queue

Archived 2026-10-28. Verdict PASS (see `verify-report.md`); shipped in PRs #11 and #12.

## Spec merge

- `specs/chat/spec.md`: CHAT-21 "Fields other than…" sentence now lists `id` and `seq` for music
  controls; CHAT-30 and CHAT-31 replaced whole (shared queue: add/skip/remove/ended, queue cap).
- `specs/server/spec.md`: SRV-05 "Page works under the policy" scenario covers the queue flow.
  CSP unchanged.
- No removals.

## Delivered

Shared queue in `musica`: links added while something plays are queued; skip, remove by id and
auto-advance on `ended` are server-authoritative in `Room.Run`. Follow-up fix (#12) completed the
YouTube handshake so the queue advances and shows the track title.

## Pending

- T802 manual two-browser checklist (`design.md`) — deferred to the user.
