# Proposal: Global music playback

## Intent

Music from `#musica` keeps playing, in sync, while the user looks at any other room or private
conversation, as long as they stay joined to `#musica`. A global local mute lets each user
silence it from anywhere, and a small "now playing" indicator shows what is on. Shared controls
(play/pause, seek, skip, add, remove) stay only in `#musica`, so nobody disturbs the room from
another channel.

## Decisions

Orchestrator decisions (auto mode, 2026-10-28; confirmed with the user):

- **When it plays**: while joined to `musica`, after its pane has been shown at least once
  (frame creation still happens on first showing — keeps the "no YouTube before opening musica"
  privacy rule and gives autoplay a user gesture). Switching rooms/DMs no longer pauses.
  Leaving `musica` (Dejar sala / Salir) stops playback and removes the frame, as today.
- **Sync keeps running off-pane**: the tick/reconcile loop and `ended` reporting run whenever
  the player exists, not only when the pane is shown. The frame is kept rendered but
  visually off-screen when the pane is not shown (never `display:none`).
- **Global music mute**: new header toggle `#music-global-mute` (🎵 on / 🔇 muted,
  `aria-pressed`), separate from 🔔 (notification sound). It is local only (no control sent),
  persisted in `localStorage` key `chadroom.musicMuted` (best effort), and shares state with the
  existing in-pane `#music-mute` (one `music.muted`). Muting keeps the player synced, so
  unmuting resumes at the room's position. Shown only while joined to `musica`.
- **Now playing indicator**: read-only header text `#music-now-playing`, shown while joined to
  `musica`, a track is current and the musica pane is **not** shown. Text: `♪ <title>` when
  playing, `⏸ <title>` when paused (title from the widget, fallback `youtu.be/<id>`),
  ellipsized. If the browser blocked audio, it reads "Audio bloqueado: abrí #musica". No controls.
- **Server / protocol**: unchanged.

## Scope

In: `cmd/chatroom/app.js`, `index.html`, `app.css`, `main_test.go` (markup test), spec CHAT-31
(MODIFIED) and CHAT-34 (ADDED), README note.
Out: shared controls outside `musica`, playing music without joining `musica`, server or
WebSocket changes, per-room volume.

## Affected Areas

| Area | Change |
|------|--------|
| `cmd/chatroom/app.js` | split pane gating (`musicShown`) from player gating (player exists + joined); no pause on switch; off-screen frame; persisted global mute; now-playing render |
| `cmd/chatroom/index.html` + `app.css` | header mute button + indicator; off-screen frame host styles |
| `cmd/chatroom/main_test.go` | assert new header markup |

## Rollback

Frontend-only; revert the commit. No data or protocol migration.

## Risks

- Browsers may throttle an off-screen iframe — mitigated by keeping it rendered (1px/off-screen, not `display:none`); needs a manual check.
- Background-tab timer clamping (pre-existing) may slow drift correction.
