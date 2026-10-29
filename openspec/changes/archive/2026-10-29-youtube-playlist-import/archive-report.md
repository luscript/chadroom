# Archive Report: youtube-playlist-import

**Archived**: 2026-10-29
**Merged**: PR #18 (`029c17d`)
**Verdict at verify**: PASS (0 CRITICAL, 0 HIGH); manual check with a real key OK (user).

## What shipped

- `GET /playlist?list=ID` (`cmd/chatroom/playlist.go`): YouTube Data API v3 expansion,
  key in `X-Goog-Api-Key`, no redirects, 5 s total, 1 MiB cap, playable-only filter, 10 min cache.
- WS music control `add_many` (≤50 ids, atomic, one frame); queue cap 30 → 50 (Go + JS, mirror tests).
- Page: playlist links (`/playlist?list=`, `watch?v=X&list=Y` with X first; mixes add only X),
  fixed feedback texts, input placeholder mentions playlists.

## Specs merged into `openspec/specs/`

| Spec | Change |
|------|--------|
| `chat` CHAT-21 | MODIFIED: `videos` field allowed in music controls |
| `chat` CHAT-30 | MODIFIED: queue cap 50, `add_many` row and rules, discard rules, gate note, Queue cap scenario; 4 scenarios added |
| `chat` CHAT-31 | MODIFIED: link-forms paragraph (playlist links, import flow, texts, cap 50); 5 scenarios added |
| `server` SRV-09 | ADDED: YouTube playlist expansion (Purpose updated) |
| `server` SRV-05 | unchanged (same-origin `/playlist` covered by `connect-src 'self'`) |

## Other updates

- `openspec/project-map.yaml` refreshed (playlist.go, `/playlist`, `add_many`, cap 50, mirror tests, 14 archived changes); staleness fresh at `029c17d`.
- `CLAUDE.md`: "Ninguno en curso."

## Open follow-ups (backlog / verify-report)

- Rate limiting + negative cache for `/playlist` (quota burn, MEDIUM accepted risk).
- UX: typing during an import clears "Importando playlist…"; success clears text typed meanwhile (MEDIUM).
- Guard test for `PLAYLIST_TEXT` strings in app.js (MEDIUM); LOW naming/comment nits.
- `youtu.be/X?list=Y` share links import the playlist.
