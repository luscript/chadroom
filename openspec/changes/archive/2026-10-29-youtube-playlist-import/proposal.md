# Proposal: YouTube playlist import

## Intent

In `#musica`, pasting a YouTube or YouTube Music playlist link (for example
`https://music.youtube.com/playlist?list=PL…`) appends up to 50 of its playable videos to the
shared queue, instead of failing with "Ese link no es de YouTube.". Single-video links keep
working as today.

## Decisions

Orchestrator decisions (auto mode, 2026-10-29; confirmed with the user):

- **Server-side expansion over HTTP**: new `GET /playlist?list=ID` in `cmd/chatroom/playlist.go`,
  modeled on `/gifs` (SRV-08): key from `YOUTUBE_API_KEY` (trimmed), route registered only when
  the key is set (otherwise 404), fixed upstream `https://www.googleapis.com/youtube/v3`, 5 s
  timeout, no redirects, 1 MiB body cap, transport error never wrapped (the key is in the query
  string), 502 `{"error":"unavailable"}` on upstream failure, TTL cache keyed by list id
  (10 min, ≤100 entries, empty results not cached). Response `{"videos":["<11-char id>", …]}`
  (≤50, playlist order). Unknown/private playlist → 404 `{"error":"not_found"}`; malformed id →
  400 `{"error":"invalid_list"}`.
- **List id**: accepted only if it matches `^[A-Za-z0-9_-]{10,64}$` (client and server). The
  client controls no other upstream parameter.
- **First page only**: one `playlistItems.list` call (`maxResults=50`); longer playlists import
  their first 50 items.
- **Filter unplayable videos**: drop items that are private or deleted (`playlistItems`
  `status.privacyStatus` not `public`/`unlisted`), then one `videos.list` call
  (`part=status`) for the remaining ids and drop those not `embeddable` or not `processed`.
  Ids from upstream are re-validated against `^[A-Za-z0-9_-]{11}$`; duplicates dropped.
  Quota: 2 units per uncached import (default 10 000/day).
- **New WS control `add_many`** `{type:"music", action:"add_many", videos:[…]}`: 1..50 ids,
  each validated like `add`; an invalid frame is discarded silently (CHAT-21 2a). Applied
  atomically in `player.apply`: if nothing is current (or the last track finished with an
  empty queue) the first id becomes current, the rest are queued; ids that do not fit in the
  queue are dropped; one music frame for the whole batch; nothing fits → no frame. Costs one
  music rate-gate token, like `add`. The upstream call never runs in `Room.Run`.
- **Queue cap 30 → 50**: `maxMusicQueue` (Go) and `MUSIC_QUEUE_MAX` (app.js) move together; a
  new test fails if they differ (closes the drift risk found in exploration).
- **Fill free slots + feedback (client-side)**: the client truncates the list to the free
  slots it knows about before sending and shows "Se agregaron N de M." when it had to cut
  (or "La cola está llena." when there is no room, sending nothing). No new server→client
  frame: the server cap is authoritative and races with other users are benign.
- **Link forms** (client parser):
  - `https://{www.,m.,music.,}youtube.com/playlist?list=ID` → import the playlist.
  - `…/watch?v=X&list=ID` → import the playlist with X first (X removed from the rest if
    present). If the list is an auto-generated mix (`RD…`, not readable by the API) or the
    import fails/is disabled, fall back to adding X alone (today's behavior) and say so.
  - Every other existing single-video form unchanged.
- **UI feedback**: while importing, `#music-load` is disabled and the existing `#music-error`
  region shows "Importando playlist…"; fixed Spanish texts mapped from status codes (never
  render server strings): disabled (404 without JSON body) → "Importar playlists no está
  disponible."; `not_found` → "No encontré esa playlist (¿es privada?)."; empty after filter →
  "Esa playlist no tiene videos reproducibles."; other → "No se pudo leer la playlist.".
- **Without `YOUTUBE_API_KEY`**: server logs that playlist import is disabled; everything else
  works as today.

## Scope

In: `cmd/chatroom/playlist.go` + `playlist_test.go` (new), `cmd/chatroom/main.go` (config,
wiring, route), `internal/chat/music.go`, `internal/chat/message.go` (+ tests),
`cmd/chatroom/app.js`, `cmd/chatroom/main_test.go` (queue-cap mirror test), specs CHAT-21,
CHAT-30, CHAT-31 (MODIFIED) and a new SRV requirement (ADDED), `README.md`.
Out: paging beyond 50 items, mixes (`RD…`), titles/thumbnails in the queue, per-user queue
quotas, rate limiting `/playlist` (tracked in backlog), region-restriction filtering.

## Affected Areas

| Area | Change |
|------|--------|
| `cmd/chatroom/playlist.go` (new) | `/playlist` proxy to YouTube Data API: validation, 2 upstream calls, filtering, cache, key hygiene |
| `cmd/chatroom/main.go` | `YOUTUBE_API_KEY` config, optional route registration |
| `internal/chat` (`music.go`, `message.go`) | `add_many` action, cap 50 |
| `cmd/chatroom/app.js` | playlist link parsing, fetch `/playlist`, truncation + feedback, `MUSIC_QUEUE_MAX` 50 |
| `openspec/specs`, `README.md` | CHAT-21/30/31 deltas, new SRV requirement, env var docs |

## Security

- The key lives only in the server env; never sent to the browser, logged, or echoed
  (same rules and tests as Giphy). Recommend restricting it in Google Cloud Console to
  "YouTube Data API v3" only.
- No SSRF: fixed upstream host, query built with `url.Values`, list id whitelisted.
- Upstream data is untrusted: only 11-char ids are used, re-validated by the client
  (`/playlist` response) and again by the server (`add_many`).
- Quota exhaustion: `/playlist` is unauthenticated like `/gifs`; the cache limits repeated
  imports of the same list. Rate limiting stays in the backlog (pragmatic scope).

## Rollback

Revert the commit; unset `YOUTUBE_API_KEY` to disable without a deploy. No data migration.

## Risks

- Queue cap change touches existing tests that assert 30.
- A video can still fail to play (region restrictions); the existing player error handling
  applies.
- A single import can fill the queue for everyone (accepted for a friends chat).
