# Proposal: Room music sync

## Intent

A shared YouTube player in the `musica` room: everyone in the room hears the same track at the
same point, and anyone can load a track, play, pause or seek for everybody. One track at a time.
This is the `music-player` backlog group, re-scoped by the user (2026-10-23).

## Decisions

User decisions: synced playback with global controls; **one track at a time** (no queue);
control the embed via **`postMessage` (no `iframe_api` script, CSP `script-src` unchanged)**;
source is YouTube / YouTube Music links; anyone in the room controls it; state lost on restart.

Orchestrator decisions (auto mode):

- **Only the `musica` room has a player.** It is a per-room capability set when the server builds
  its rooms (rooms stay generic; `musica` is configured in `cmd/chatroom`). In other rooms music
  frames are dropped silently and no music frame is ever sent.
- **Server-authoritative state, owned by `Room.Run`** (hub pattern, no locks):
  `video` (11-char YouTube id or none), `playing`, and the position as
  `pos` milliseconds at server time `at`. Current position = `pos + (now - at)` while playing.
- **Inbound** `{"type":"music","action":…}`:
  - `load` + `video`: replaces the track, position 0, playing.
  - `play`, `pause`: resume / pause at the current position.
  - `seek` + `pos` (integer ms, 0 ≤ pos ≤ 24 h): jump; keeps playing/paused.
  The server never handles URLs: the page extracts the id from `youtube.com/watch?v=`,
  `youtu.be/`, `music.youtube.com/watch?v=` and `youtube.com/shorts/` links, and the server
  validates `^[A-Za-z0-9_-]{11}$`. A `music` frame that is invalid, or arrives in a room without a
  player, is **dropped** (never relayed as chat). Actions are rate-limited per connection
  (gate like typing, ~4 per second; excess dropped).
- **Outbound** `{"type":"music","video":"…","playing":bool,"pos":ms,"by":"<nick>","ts":…}` where
  `pos` is the position **at send time** (clients treat it as "now" on receipt, so server/client
  clocks don't matter). Sent to every client on each accepted action (must-have delivery), and to
  a newcomer right after `history` when a track is loaded. `by` is who made the last change.
  Never stored in history.
- **Room empties while playing → paused** at the current position.
- **End of track:** the server does not know durations. Clients show "terminó" locally; anyone
  can replay (seek 0 / play) or load another.
- **Page.**
  - A strip under the room tabs, only while `musica` is active: link input + "Poner",
    play/pause, a seek bar, current time, local volume/mute, "puesto por <nick>".
  - YouTube iframe `https://www.youtube-nocookie.com/embed/<id>?enablejsapi=1&controls=0&disablekb=1&origin=<page origin>`
    (no YouTube controls, so nobody desyncs by clicking inside it). Built with DOM APIs only.
  - Control via `postMessage` to the iframe's `contentWindow`, target origin
    `https://www.youtube-nocookie.com`. Incoming messages accepted only if `event.origin` is that
    origin **and** `event.source` is the iframe; parsed defensively.
  - Drift correction: if the local position differs from the expected one by more than 2 s,
    seek locally.
  - Autoplay: when there is something playing and the user hasn't interacted with the player yet,
    show an **"Escuchar"** button (one gesture per page load).
  - Leaving `musica` pauses locally (room state untouched); coming back re-syncs.
- **CSP:** add `frame-src https://www.youtube-nocookie.com`. The iframe gets
  `referrerpolicy="strict-origin-when-cross-origin"` only if YouTube requires a referrer
  (design to confirm); the page policy stays `no-referrer`.

## Affected Areas

| Area | Files | Change |
|------|-------|--------|
| Chat hub | `internal/chat/{message,room,client,server}.go` | music frames, Run-owned state, gate, per-room capability |
| Server wiring | `cmd/chatroom/main.go` | enable the player on `musica`; CSP `frame-src` |
| Page | `cmd/chatroom/index.html`, `app.js`, `app.css` | player strip, iframe control, sync |
| Tests | `internal/chat/*_test.go`, `cmd/chatroom/main_test.go` | state machine, replay, gating, CSP, DOM |
| Docs / specs | `README.md`, `CLAUDE.md`, `openspec/specs/{chat,server}/spec.md` | protocol, CSP |

## Out of scope

- Queue/playlists, voting, moderators/permissions, persistence across restarts, other rooms,
  titles/thumbnails from YouTube's API (would need a key and server fetches), Spotify,
  the local-file library (`feat/music-player`, kept unmerged).

## Risks

- Undocumented `postMessage` protocol: if YouTube changes it, sync breaks (chat unaffected).
- Some videos refuse embedding (101/150) → shown as an error in the strip.
- Viewers' IPs reach YouTube (only for users who press "Escuchar" / have the strip active).
- Anyone can hijack the music (accepted: friends-only chat).

## Rollback

Revert the change's commit(s). No persisted data.
