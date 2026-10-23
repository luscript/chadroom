# Archive report: room-music-sync

Archived 2026-10-23. Verdict PASS after fix round 1 (see `verify-report.md`).

## Spec merge

- `specs/chat/spec.md`: Purpose (music control / `music` frame); CHAT-10 "only other data
  frames" sentence; CHAT-21 rule 2a + fields sentence + scenario; CHAT-30 and CHAT-31 added.
- `specs/server/spec.md`: SRV-05 `frame-src` rule and last scenario; SRV-06 `musica`-only player.
- No removals.

## Delivered

Shared YouTube / YouTube Music player in `musica`: server-authoritative state in `Room.Run`
(`load`/`play`/`pause`/`seek`, ≤4 controls/s per connection, snapshot to newcomers, pause when
the room empties); page controls the `youtube-nocookie` embed via `postMessage` only (no YouTube
script), created only after "Escuchar"; CSP `frame-src https://www.youtube-nocookie.com`.

Supersedes the local-file attempt (`music-player`, branch `feat/music-player`, not merged).

## Pending

- Manual two-browser checklist (`design.md`) — user to verify.
- Follow-ups in `backlog.md` → `room-music-sync-polish`.
