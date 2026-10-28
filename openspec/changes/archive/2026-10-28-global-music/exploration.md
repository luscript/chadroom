# Exploration: global-music

Synthesized by the orchestrator from 1 crombie-explore agent (scope: music client across room switches).

## Key findings

- The player is gated end-to-end by `musicShown()` (`app.js:1125`: musica joined AND active AND no DM conversation shown).
  On switching away: `renderMusic()` hides `#music` and clears the 500 ms tick; `syncShown()` sends `pauseVideo` once;
  `reconcile()` and `createPlayer()` no-op. The iframe is **not** removed on a switch, only on leave (`resetMusic`, via `teardown`),
  handshake failure or reset. Server `music` frames keep arriving (the musica socket stays open) and update `music.srv`.
- Gates to reconsider: `musicShown()` (1125), `renderMusic` early return + tick start (1345-1352), `createPlayer` (1398),
  `reconcile` (1537), `syncShown` pause/re-seek (1616-1623), `maybeReportEnded` (only via tick), `setStatus` → `renderMusic` (475).
- The iframe lives in `#music-player` inside `#music`; `[hidden]` is `display:none !important` (app.css:42), which can make
  browsers throttle/stop an embedded player. A global player must stay rendered (offscreen), not `display:none`.
- A local music mute already exists in the pane: `#music-mute` (🔊/🔇) toggling `music.muted` + `ytCommand('mute'|'unMute')`,
  re-applied on handshake (1476). Not persisted. `resetMusic` does not reset `music.muted`.
- The 🔔 `#mute-btn` pattern (CHAT-32): `soundMuted` loaded from `localStorage` `chadroom.muted`, `renderMute()` sets
  `aria-pressed` + emoji, `.tool-mute` / `.tool-mute[aria-pressed="true"]` styles.
- Track title: `music.title` from widget `infoDelivery`/`initialDelivery` (1493-1498); fallback `youtu.be/<id>`; rendered in
  `#music-title` / `#music-now` inside `#music` (only when shown).
- Header: `.chat-header > .actions` holds `#status`, users popover, `#me`, `#mute-btn`, `#leave-btn` (index.html:51-67).

## Constraints (tests)

- `TestIndexHasMusicPlayer` pins `#music-*` ids (incl. `#music-mute`, `#music-player`), `#music` starts hidden, no YouTube URL in HTML.
- `TestNoYouTubeScript`, `TestIndexHasNoInlineCode`, CSP tests, `TestIndexNotificationMarkup` (🔔 markup).

## Spec impact

CHAT-31 currently mandates "show only while active pane belongs to musica" and "Leaving musica MUST pause local playback",
scenario "Returning re-syncs". Needs MODIFIED CHAT-31; new UI goes in ADDED CHAT-34 (last id is CHAT-33).

## Risks

- Hidden-iframe throttling (needs manual browser check).
- Autoplay policy if the frame is created without a gesture → keep frame creation on first showing of the musica pane.
- Background-tab timer clamping affects tick (pre-existing).
