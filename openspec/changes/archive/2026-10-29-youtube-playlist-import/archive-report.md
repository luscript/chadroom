# Archive report: youtube-playlist-import

Archived 2026-10-29. Verdict PASS (see `verify-report.md`); shipped in PR #18.

## Spec merge

- `specs/chat/spec.md`: CHAT-21 "Fields other than…" sentence now lists `videos`. CHAT-30: queue
  cap 30 → 50, new `add_many` control (one frame per batch, counts as one control for the rate
  limit, invalid batches discarded whole), "Queue cap" scenario updated, 4 new scenarios.
  CHAT-31: playlist links (`/playlist?list=` and `watch?v=…&list=…`, mixes `RD…` add only the
  song), import messages and limits, 5 new scenarios.
- `specs/server/spec.md`: new SRV-09 `GET /playlist` (YouTube Data API v3, key in
  `X-Goog-Api-Key`, TTL cache). SRV-05 unchanged (`/playlist` is same-origin).
- No removals.

## Pending

- Manual two-browser checklist (`design.md`) — deferred to the user.
