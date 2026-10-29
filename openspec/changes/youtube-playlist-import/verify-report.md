# Verify Report: youtube-playlist-import

**Date**: 2026-10-29
**Verdict**: **PASS** (0 CRITICAL, 0 HIGH; all gates green)

## Gates (crombie-worker)

| Check | Result |
|-------|--------|
| `gofmt -l .` | PASS (no output) |
| `go vet ./...` | PASS |
| `go build ./...` | PASS |
| `go test -race -count=1 ./...` | PASS (`cmd/chatroom`, `internal/chat`) |
| `go test -race -count=3` on new tests (Playlist, AddMany, MusicLimits, ParseInbound, PlayerApply, MusicQueueCap) | PASS, no flakiness |
| `node --check cmd/chatroom/app.js` | PASS |
| Smoke `/playlist` without key → 404 + "disabled" log | SKIPPED (:8080 busy); covered by `TestPlaylistDisabledWithoutKey` |

## Findings

| # | Sev | Source | Location | Finding | Disposition |
|---|-----|--------|----------|---------|-------------|
| 1 | MEDIUM | security | `playlist.go` handle/fetch | Unauthenticated `/playlist`: random ids burn quota (404s not cached, no singleflight). Availability of import only. | Accepted risk (backlog: rate limiting). Cheap mitigation: short negative cache for `not_found`. |
| 2 | MEDIUM | review | `app.js` CHAT-31 import flow | Page scenarios (texts, race token, truncation, X-first, 404 mapping) have no automated test; only constants are mirror-tested. | Manual checklist (design.md 1–7). Cheap guard: Go test asserting `PLAYLIST_TEXT` strings appear verbatim in app.js. |
| 3 | MEDIUM | review | `app.js:1796`, `:1761` | Typing during an import erases "Importando playlist…"; success clears whatever was typed meanwhile. | UX bug, small fix. |
| 4 | LOW | security | `playlist.go` | No cap on concurrent upstream calls (bounded by 5 s × 1 MiB each). | Backlog with #1. |
| 5 | LOW | security | ops | Restrict key to YouTube Data API v3 (+ egress IP). | Documented in README. |
| 6 | LOW | security | `playlist.go:196-218` | Body not drained on non-200 → no keep-alive reuse. | Optional. |
| 7 | LOW | review | `app.js:1786` | Misleading comment about mix ids on `/playlist` links. | Reword. |
| 8 | LOW | review | `app.js:1734` | Any 404 → "disabled"; spec says 404 without JSON error. | Use `!body`. |
| 9 | LOW | review | `app.js:1748` | Empty `videos` with a watch link sends X silently (consistent with design). | Note only. |
| 10 | LOW | review | `playlist.go:31`, `playlist_test.go` | Terse names (`reasonNotAccess`, `tid`, `okv`). | Rename. |
| 11 | LOW | review | `playlist.go:94-101` | Client disconnect logs a 502 to nobody (log noise; same as gifs.go). | Note only. |

## Spec coverage

- CHAT-21 / CHAT-30 deltas: every scenario covered by Go tests (`TestPlayerApply` add_many subtests,
  `TestMusicAddManyBroadcast`, `TestMusicAddManyFull`, `TestParseInbound` add_many rows,
  `TestMusicAddManyOverWebSocket`, `TestMusicLimitsMirroredInJS`).
- SRV-09: every scenario covered (`playlist_test.go`: params/injection, invalid list, filters,
  empty, not_found, upstream errors, redirect, timeout, key never leaks, cache, bound, cap,
  concurrency, disabled route; `TestPlaylistIDMirroredInJS`).
- CHAT-31 delta: implemented and statically verified (texts match the spec character for
  character); not automated — manual checklist pending with a real `YOUTUBE_API_KEY`.

## Security summary

Key only in `X-Goog-Api-Key` header; redirects blocked; sanitized errors; no SSRF (fixed host,
whitelisted list id, re-validated ids); 1 MiB cap with truncation → 502; `add_many` strictly
validated, whole control discarded on any bad element; page renders only fixed texts via
`textContent`; CSP unchanged.

## Pending before archive

- Manual checklist (design.md) with a real key, incl. confirming the `X-Goog-Api-Key` header works.
- Merge spec deltas into `openspec/specs/` and refresh `project-map.yaml` at archive.
