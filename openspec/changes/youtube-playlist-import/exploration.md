# Exploration: youtube-playlist-import

Goal: pasting a YouTube / YouTube Music playlist link (`/playlist?list=ID`) in `musica`
appends up to 50 of its videos to the shared queue. Today it fails with
"Ese link no es de YouTube." — playlist support was never implemented (no commit, branch,
spec or test); it was explicitly out of scope in `room-music-sync` / `music-queue` and is in
`openspec/backlog.md:71`.

Sources: 2 crombie-explore agents (backend, frontend), verified against HEAD 636b090
(project map is stale: generated at 698a80b).

## Key findings

- **Protocol is single-video.** `musicCmd{action, video, pos, id, seq}`; `parseMusic`
  (`internal/chat/message.go:169`) validates `add.video` against `^[A-Za-z0-9_-]{11}$`;
  invalid controls are discarded silently (CHAT-21 rule 2a). `player.apply` `add`
  (`internal/chat/music.go:79`) starts the video if idle/finished, else appends; refuses at
  `maxMusicQueue = 30` (no frame).
- **Hub must not block.** Only `Room.Run` owns player state (`room.go:251`); an upstream
  HTTP call cannot run there. Expansion belongs in an HTTP handler (same shape as `/gifs`).
- **`gifs.go` is the template** for an upstream proxy with a secret key: env var trimmed in
  `main()`, route registered only when the key is set (else 404), `http.Client` 5s timeout,
  no redirects, `io.LimitReader` 1 MiB, transport error never wrapped (`*url.Error` carries
  the key), 502 `{"error":"unavailable"}`, TTL cache with mutex, all upstream params fixed
  server-side, upstream ids re-validated. Tests (`gifs_test.go`) use an httptest fake
  upstream with injected `baseURL`/`now`, and cover key leaks, redirects, timeouts, cache.
- **Client cap mirrors server cap.** `MUSIC_QUEUE_MAX = 30` (`app.js:1092`); `cleanQueue`
  drops the WHOLE music frame if `queue.length > 30` → clients would silently freeze if only
  the Go cap is raised. No mirror test exists for it (only comment).
- **No server→client feedback for music.** Add errors are silent drops; only the client's
  own precheck shows "La cola está llena". `dm_error{to,reason}` is the precedent for a
  typed error frame; `MUSIC_ERRORS` the precedent for fixed-text mapping by code (never
  render server strings).
- **Frontend add flow:** submit handler `app.js:1689` → `parseYouTubeId` `app.js:1138`
  (https only, no port/userinfo; `/watch?v=`, `youtu.be/`, `/shorts/`; `watch?v=X&list=Y`
  adds X and ignores `list`) → `sendMusic` `app.js:1157` (300 ms client gap; server gate
  260 ms). Queue renders `youtu.be/<id> · agregado por <by>` via `textContent`; no titles,
  no per-item fetches, so 50 items is cheap to render.
- **CSP** `connect-src 'self'` → client cannot call the YouTube API; same-origin proxy fits.
- **Specs touched:** CHAT-30 (queue cap 30, `add` row, "Queue cap" scenario, gates),
  CHAT-31 (accepted link forms, "cola llena"), CHAT-21 2a; new SRV requirement modeled on
  SRV-08. No current spec or Go test asserts playlist rejection.

## Candidate approaches

A. **(recommended)** `GET /playlist?list=ID` → `{"videos":[ids]}` (≤50, first page of
   `playlistItems.list`), then the client sends ONE new WS action (e.g. `add_many`
   `{videos:[...]}`); the server re-validates every id and applies atomically in
   `player.apply`, producing one frame. Keeps the hub pattern.
B. WS action `add_playlist{list}` expanded server-side in a goroutine that re-enters the hub
   via a synthesized inbound. More invasive (async result path, nickname/room may change
   meanwhile). Not recommended.
C. Client sends 50 `add`s: ~13 s at the 260 ms gate, hits the cap midway. Rejected.

## Risks

- Key leakage via `*url.Error` / logs (key goes in the query string) — reuse gifs.go rules
  and add a key-never-leaks test.
- Bulk gate bypass: one 260 ms token buys up to 50 tracks; `/playlist` has no rate limit
  (neither does `/gifs`; cache protects quota).
- Cap mismatch 50 vs 30; client/server constants must move together (add a mirror test).
- Private/deleted/non-embeddable videos stall playback (track advances only on `ended`) —
  filter server-side if feasible (`part=status` / `contentDetails`).
- Auto-generated mixes (`list=RD…`) are not readable by `playlistItems.list`.
- Changing `watch?v=X&list=Y` semantics would alter existing behavior.

## Open questions (for the user)

1. Queue cap: raise to 50 and/or fill only free slots with "N of M" feedback?
2. `watch?v=X&list=Y`: keep single video (recommended) or import the list?
3. API key availability (`YOUTUBE_API_KEY`), feature disabled without it.
4. Partial vs all-or-nothing on overflow; feedback frame for errors.
5. Filter unavailable/private videos server-side?
