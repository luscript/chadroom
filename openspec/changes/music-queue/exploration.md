# Exploration: music-queue

Goal: shared playback queue for the `musica` player (anyone adds/removes/skips, no reordering,
paste while playing appends, auto-advance on end) and auto-start on opening the `musica` tab,
with an "Escuchar" fallback + warning when the browser blocks audio.

Sources: two crombie-explore agents (server: `internal/chat/`; frontend: `cmd/chatroom/`).
No conflicting findings between them.

## Server (internal/chat)

- State: `player{video, playing, pos ms, at monotonic, by}` on `Room.player` (only `NewMusicRoom`).
  `position(now)` = `pos + elapsed` while playing. Mutated only in `Room.music` (Run goroutine):
  a queue slice fits there with no locks (hub pattern preserved).
- Wire in: `{type:'music', action: load|play|pause|seek, video?, pos?}`, parsed exhaustively in
  `parseMusic` (`message.go`); unknown actions are discarded without a reply.
  Wire out: `musicFrame{type, video, playing, pos, by, ts}`. Spec and tests pin "exactly these
  fields" (`exactFields`).
- Rate gate: one `rateGate{260 ms}` per connection for ALL music frames (`client.go:126`).
- Join snapshot: `admit` sends a music frame only if `video != ""`. `remove` → `halt` when the room
  empties. `video` can never be cleared today.
- No end-of-track notion: no duration on the server; `pos` grows past the end (accepted gap in the
  `room-music-sync` design).
- Limits: inbound frames are capped at 4096 B; there is no queue cap anywhere.

## Frontend (cmd/chatroom)

- Consent: `music.consent` is set only by the `#music-listen` click. `createPlayer()` requires
  consent + `music.srv` + `musicShown()` (`app.js:1174`). Hooks: `activate()` (312–313),
  `teardown()` → `resetMusic()` (331).
- The iframe already has `allow="autoplay; encrypted-media"`, and the CSP `frame-src` already allows
  youtube-nocookie, so auto-start needs no header change.
- `blocked` comes from `onAutoplayBlocked`, from two failed playVideo tries (~6–9 s), or from a failed
  handshake. `#music-consent` is shown when `!consent || blocked`, and the Escuchar handler already
  works as the fallback.
- playerState 0 is stored but never reported to the server. `musicEnded()` is time-based and drives
  "Terminó" / ↺ Repetir. `reconcile` restarts an ended player when the server says playing, which
  would fight auto-advance.
- `sendMusic` keeps a 300 ms client gap. `onMusicFrame` validates the exact 6-field shape.
- DOM: no innerHTML anywhere; createElement + textContent + addEventListener (strict CSP).
  Layout: `.music-player` is `grid-row: 1 / span 6` (`app.css:225`); the pinkroom skin fixes the
  height at 800px (`app.css:251`).

## Specs / tests touched

- `openspec/specs/chat/spec.md`: CHAT-30 (state, controls, exact frame, snapshot), CHAT-21 (allowed
  fields), CHAT-31 (paste = replace; consent before contacting YouTube); CHAT-11 order text.
- `openspec/specs/server/spec.md`: SRV-06 (and the SRV-05 privacy scenario around Escuchar).
- Go tests: `room_test.go` music helpers (`startMusicRoom`, `musicCtl`, `decodeMusic`, `expectMusic`),
  `client_test.go` (WebSocket music tests, `TestParseInbound`), `main_test.go`
  (`TestIndexHasMusicPlayer` pins `music-listen` "Escuchar" + comment at 658, `TestNoYouTubeScript`).
  There are no JS tests.

## Risks

1. Auto-start reverses the CHAT-31 privacy decision ("no YouTube contact before Escuchar"). This
   needs a spec delta + README note.
2. Dedupe of `ended` by video id fails for repeated ids → needs a server-issued track sequence that
   clients echo back.
3. The shared 260 ms gate can drop a lone listener's `ended` → the queue stalls.
4. Any client can forge `ended`/skip (no auth, by design: anyone can skip anyway).
5. Queue end: `video` can't be cleared today; the empty-queue state must be defined.
6. A queue cap is required (each frame is fanned out to every client buffer).
7. A user landing in `musica` without a gesture (reconnect) → blocked after a few seconds → warning.
