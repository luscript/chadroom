# Exploration: room-music-sync

Synthesized by the orchestrator, 2026-10-23, from the `music-player` exploration (backend and
frontend crombie-explore agents, same session) plus a targeted check of the hub hooks. Source:
backlog group `music-player`, re-scoped by the user to a **shared, synced YouTube player** in
`musica` (one track at a time, global controls, no third-party script).

The local-file `music-player` attempt (commit `ad2013b`, branch `feat/music-player`) is NOT the
base of this change; this branch starts from `feat/profile-picture`.

## Key findings

- **Hub hooks.** `parseInbound` (`internal/chat/message.go:105`) classifies JSON frames by exact
  `type` (`chat`, `dm`, `typing`); any other JSON falls back to plain chat text, so a `music`
  type MUST be handled explicitly (and invalid music frames dropped, not relayed as chat).
  `Room.dispatch` routes by `inboundKind` (`room.go:182` for typing). `admit` (`room.go:85`)
  sends welcome → users → history privately, then publishes join; a music state snapshot fits
  after history. `typingGate` (`client.go:16-20`, used in `readPump`) is the per-connection
  rate-limit pattern.
- **Rooms are generic.** `NewRoom(id)` (`room.go:43`); `newChatServer` (`cmd/chatroom/main.go:111`)
  builds one per `roomIDs` entry. The room id is never on the wire; "only in `musica`" must be a
  per-room capability set at construction.
- **Delivery primitives.** `deliver`/`fanOut` drop slow clients (must-have frames); `offer` is
  best-effort (typing). A music state change is must-have (state would diverge otherwise).
- **CSP** has `default-src 'none'`, no `frame-src`; page sends `Referrer-Policy: no-referrer`.
  `TestIndexSecurityHeaders` asserts the exact CSP string.
- **Frontend.** `activate(room, conv)` is the single room-switch hook; persistent UI must not be in
  the panels registry; the strip slot under `nav.rooms` (between `#room-tabs` and `.rule`) works
  on mobile. Browsers block audible autoplay until a user gesture.

## YouTube constraints (to confirm in design)

- No embeddable YouTube Music player; `music.youtube.com/watch?v=ID` uses ordinary YouTube video
  ids, playable via `https://www.youtube-nocookie.com/embed/ID`.
- Control without the `iframe_api` script: `enablejsapi=1` + `origin=` param, then
  `postMessage` commands (`playVideo`, `pauseVideo`, `seekTo`, `loadVideoById`, `setVolume`,
  `mute`/`unMute`) and `{"event":"listening"}` to receive `infoDelivery`/`onStateChange`
  messages. Undocumented protocol — may change.
- YouTube embeds may refuse to play without an HTTP referrer (player error 153); the page-level
  `no-referrer` policy would need a per-iframe `referrerpolicy` override.
- Some videos have embedding disabled (errors 101/150).

## Risks

- Third-party privacy: viewers' IPs (and the page origin, if a referrer is sent) reach YouTube.
- Undocumented `postMessage` protocol; messages from the iframe are untrusted input.
- Control-frame flooding; state replay to newcomers; room emptying while playing.
- Clock skew between server and clients.
