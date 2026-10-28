# Design: Global music playback

## Technical Approach

Frontend only (`app.js`, `index.html`, `app.css`, `main_test.go`, README). Today one gate, `musicShown()`,
controls two separate things: whether the pane is visible, and whether the player runs. This change
splits them:

- `musicShown()` (unchanged): the musica pane is on screen. It controls the pane UI, frame
  *creation* (with the `seen` relaxation below) and the now-playing indicator.
- `playerActive()` (new): `!!music.frame && musicJoined()`. It controls the tick, `reconcile`, and
  so `maybeReportEnded`.

When the user leaves the pane while a frame exists, `#music` is **parked** instead of hidden. It
stays rendered at full size but moves off-screen and is made `inert`. The iframe is never moved to
another parent (reparenting an iframe reloads it) and never gets `display:none`. Two header
elements, a global mute toggle and a now-playing text, are rendered from `renderMusic()` on every
call, before its early return.

## Architecture Decisions

### Decision: Park the whole `#music` section off-screen; never reparent the iframe

**Choice**: When `!musicShown() && playerActive()`, set `#music.hidden = false`, add class
`parked` and set `inert = true`. CSS takes `.music.parked` out of flow (`position: fixed;
left: -10000px; top: 0; width: 356px; pointer-events: none`) and hides every child except
`.music-player` (`.music.parked > :not(.music-player) { display: none }`). The iframe keeps its
356×200 size.
**Alternatives considered**: (a) Move `#music-player` out of `#music` into a permanent dock and
place it over the pane with CSS. That needs a new layout for the wide-screen grid, and the test
pins the strip structure. (b) Move the frame between the pane and a dock on show and hide. Moving
an iframe in the DOM reloads it, so playback would restart. (c) A 1px or `visibility:hidden`/
`opacity:0` frame. Chrome throttles hidden cross-origin frames, and YouTube embeds want at least
200×200.
**Rationale**: No DOM moves, no change to the markup the test pins (`#music` still starts
`hidden` and still sits between `#room-tabs` and `.rule`). `inert` keeps the off-screen controls
out of tab order and out of the accessibility tree. `position:fixed` adds no scrollbars.

### Decision: Frame creation allowed off-pane once the pane was seen (`music.seen`)

**Choice**: New flag `music.seen`. `syncShown()` sets it whenever the pane is shown, and
`resetMusic()` clears it. The `createPlayer` gate changes to
`musicShown() || (music.seen && musicJoined())`.
**Alternatives considered**: Keep `musicShown()` only. Then if a user opens an empty #musica,
switches to #general, and a friend adds a track, they get silence while the indicator says ♪.
**Rationale**: This matches the proposal's rule ("joined + pane shown at least once"). The privacy
rule still holds: there is no YouTube contact before the user has opened #musica. The page already
has sticky user activation and the frame has `allow="autoplay"`. If a browser still blocks
autoplay, the existing `blocked` path applies and the header says "Audio bloqueado: abrí #musica".

### Decision: Returning to the pane does a plain `reconcile()`, with no forced seek

**Choice**: `syncShown()` no longer pauses on leave and no longer forces `reconcile({seek:true})`
on return.
**Alternatives considered**: Keep the forced seek. That gives an audible skip on every return,
even when the player is in sync.
**Rationale**: The tick now keeps the player in sync off-pane, and drift > `MUSIC_DRIFT_MS`
(including drift from background-tab clamping) is still corrected by `reconcile()`. `pauseLocal()`
has no callers left and is deleted.

### Decision: One `music.muted`, two buttons, persisted

**Choice**: `MUSIC_MUTED_KEY = 'chadroom.musicMuted'` sits next to `MUTED_KEY`. `music.muted` is
read at init (`=== '1'`, inside try/catch). A single `setMusicMuted(v)` does four things: sets
the state, calls `renderMusicMute()`, sends `ytCommand('mute'|'unMute')`, and persists the value
(best effort). Both `#music-mute` (🔊/🔇) and `#music-global-mute` (🎵/🔇) call it.
`renderMusicMute()` sets `aria-pressed` and the emoji on both buttons and runs once at load. The
existing handshake re-apply (`ytCommand(music.muted ? 'mute' : 'unMute')`) is unchanged.
`resetMusic()` does not touch `muted`.
**Alternatives considered**: A separate global-mute flag that combines with the pane flag. That
gives two sources of truth for one audio output.
**Rationale**: Same pattern as 🔔 (`soundMuted` / `renderMute` / `chadroom.muted`). Each button
keeps a static `aria-label` and expresses state through `aria-pressed`.

### Decision: Header widgets render from `renderMusic()`, before its early return

**Choice**: New `renderMusicHeader()`, called first in `renderMusic()`:
- `#music-global-mute.hidden = !musicJoined()`
- `#music-now-playing.hidden = !(musicJoined() && !musicShown() && music.srv)`
- Text: if `music.blocked && !music.errored`, then `Audio bloqueado: abrí #musica`. Otherwise
  `(s.playing ? '♪ ' : '⏸ ') + (music.title || 'youtu.be/' + s.video)`. The text is set with
  `textContent`, and only when it changed.

**Rationale**: `renderMusic` already runs on every frame, status change, activate, widget message
and tick, so the indicator stays current without a new loop.

## Gate changes (function by function)

| Function | Before | After |
|---|---|---|
| `musicShown()` | joined && active pane && no DM | unchanged (pane visibility only) |
| `musicJoined()` / `playerActive()` | — | new: `r && r.joined` / `!!music.frame && musicJoined()` |
| `renderMusic()` | `#music.hidden = !shown`; when hidden, clears the tick and returns | First calls `renderMusicHeader()` and sets `#music-player.hidden = !music.frame`. `parked = !shown && playerActive()`, then `#music.hidden = !shown && !parked`, `toggle('parked', parked)`, `inert = parked`. The tick runs while `shown \|\| playerActive()` and is cleared otherwise. Returns early when `!shown` (pane controls are not updated off-screen). |
| `musicTick()` | ran only while the pane was shown | unchanged body; now also runs while parked |
| `createPlayer()` | `!musicShown()` → no-op | no-op unless `musicShown() \|\| (music.seen && musicJoined())` |
| `reconcile()` | `!music.ready \|\| !musicShown()` → return | `!music.ready \|\| !playerActive()` → return |
| `maybeReportEnded()` | no gate; ran from the tick (pane only) and from widget messages | unchanged; now also reported off-pane through the tick |
| `syncShown()` | leaving: `pauseLocal()`; returning: `createPlayer` + forced seek | `music.shown = musicShown(); if (!music.shown) return; music.seen = true; createPlayer(); reconcile();` |
| `pauseLocal()` | used by `syncShown` | deleted |
| `resetMusic()` | resets state, frame, `shown` | also `music.seen = false`. Its `renderMusic()` call unparks `#music` and hides both header widgets. |
| `setStatus()` / `activate()` | call `renderMusic` / `syncShown` | unchanged (they now also refresh the header) |
| `#music-mute` click | flips local state | `setMusicMuted(!music.muted)` |

## Data Flow

Room switch while playing (#musica → #general):

    tab click ─→ activate(general)
                   ├─ renderMusic(): shown=false, playerActive=true
                   │     #music: hidden=false, .parked, inert  (frame keeps playing off-screen)
                   │     header: 🎵 visible, "♪ <title>" visible; tick keeps running
                   └─ syncShown(): shown=false → return (no pause)
    server music frame (musica socket) ─→ onMusicFrame ─→ renderMusic + reconcile (load/seek/play/pause)
    tick 500ms ─→ renderMusic (header text) ─→ reconcile (drift/retries) ─→ maybeReportEnded
    tab click #musica ─→ activate ─→ renderMusic: unpark, full pane ─→ syncShown: reconcile()
    "Dejar sala" on #musica ─→ teardown ─→ resetMusic: frame removed, unparked, header hidden

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `cmd/chatroom/app.js` | Modify | Add `MUSIC_MUTED_KEY`, `musicJoined`, `playerActive`, `music.seen`, `setMusicMuted`, `renderMusicMute`, `renderMusicHeader`. Change the gates as in the table. Delete `pauseLocal`. Update the comments at the top of the music section and above `createPlayer` and `syncShown` to describe the new behavior. |
| `cmd/chatroom/index.html` | Modify | In `.actions`, before `#mute-btn`: `<span id="music-now-playing" class="music-now-playing" hidden></span>` and `<button id="music-global-mute" type="button" class="btn btn-ghost tool tool-mute" aria-pressed="false" aria-label="Silenciar música" title="Silenciar música" hidden>🎵</button>`. Nothing else moves. |
| `cmd/chatroom/app.css` | Modify | Add the `.music.parked` rules, plus `.music-now-playing { max-width: 180px; font-size: 12px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis }` with `display:none` at `max-width:480px` (the toggle stays). Change the three height rules from `#music:not([hidden])` to `#music:not([hidden], .parked)`, so a parked strip does not stretch `.app` to 900px. The `.music:has(.music-player:not([hidden]))` grid rules stay as they are: they still match while parked, which is harmless off-screen. |
| `cmd/chatroom/main_test.go` | Modify | New `TestIndexGlobalMusicMarkup` (see below). `TestIndexHasMusicPlayer` is unchanged and must still pass. |
| `README.md` | Modify | Short note: music continues in other rooms while you stay joined to #musica; 🎵 mutes it locally (remembered). |

## Tests (`main_test.go`)

`TestIndexGlobalMusicMarkup`:
- `<button id="music-global-mute"` start tag has `aria-pressed="false"`, the `hidden` attribute and
  class `tool-mute`.
- `id="music-now-playing"` start tag has `hidden` (reuse the `startTag` pattern).
- Order: `class="actions"` < `id="music-now-playing"` < `id="music-global-mute"` < `id="mute-btn"`
  < `id="leave-btn"`.
- `app.js` contains `'chadroom.musicMuted'`.

These existing tests are unchanged and must stay green: `TestIndexHasMusicPlayer` (ids, `#music`
starts hidden, position, no `<iframe>`/YouTube URL), `TestIndexHasNoInlineCode`, CSP tests,
`TestIndexNotificationMarkup`.

## Manual browser checklist (Chrome + Firefox)

1. Join #musica, add a track, press play, switch to #general: audio continues with no gap. The
   header shows 🎵 and `♪ <title>`, no strip or empty space appears, and `.app` keeps its normal
   height.
2. From a second client, pause, seek and skip: the first client follows while in #general, and
   the indicator switches to ⏸ / the new title.
3. Open a DM tab: same behavior as #general.
4. Return to #musica: no audible skip, the indicator hides, and the video and controls look the
   same as before.
5. 🎵 → 🔇 in #general mutes, and the in-pane button shows 🔇 too (and the other way round).
   Reload, rejoin #musica: still muted. Unmuting resumes at the room's position.
6. Let a track end while in #general: the queue advances (the `ended` report is sent off-pane).
7. Open #musica with no track, go to #general, then have another client add one: playback starts
   (or the header shows "Audio bloqueado: abrí #musica", and Escuchar in #musica fixes it).
8. Click "Dejar sala" on #musica, and separately click "Salir": audio stops and both header
   widgets hide.
9. With #general shown, press Tab through the header: no focus lands on the off-screen strip
   (`inert`).
10. Background the browser tab for 2+ minutes, then return: playback is at most about 2 s off
    and gets corrected.
11. Mobile width (≤480px): the header fits, the indicator is hidden and 🎵 is visible.
12. DevTools console: no CSP violations.
