# Proposal: Music queue

## Intent

Turn the single-track `musica` player into a shared **queue**: anyone can add, remove or skip
tracks, and the room advances to the next track when one ends. Also drop the mandatory
"Escuchar" click: the player starts by itself when the user opens the `musica` tab. "Escuchar"
stays only as a fallback, with a warning, when the browser blocks audio.

Builds on `room-music-sync` (archived 2026-10-23). Exploration: `exploration.md`.

## Decisions

User decisions (2026-10-23):

- **Anyone** can add, remove and skip (same trust model as the current controls).
- **Pasting a link while something is playing appends it** to the queue. A **skip** button exists.
- **No reordering.**
- **Auto-start on opening the `musica` tab.** When the browser blocks it, show a warning and the
  "Escuchar" button.
- **End of the last track:** the player stays on "Terminó" (↺ Repetir available). The next pasted
  link plays immediately instead of waiting in the queue.
- **Queue cap: 30** items (not counting the current track). When the queue is full the page shows
  "La cola está llena".
- **Skip is disabled while the queue is empty.**

Orchestrator decisions:

- **State stays server-authoritative and Run-owned** (hub pattern, no locks). `player` gains:
  - `queue []item`, where each item is `{id, video, by}`. `id` is a server-assigned, increasing
    integer.
  - `seq`, an integer that grows every time a track becomes current.
  - `finished`, a flag set when the last track ends.
- **Inbound actions** (`{"type":"music","action":…}`):
  - `add` + `video`: if there is no current track, or the current track is `finished` and the queue
    is empty, the new track becomes current (pos 0, playing). Otherwise it is appended. It is
    discarded if the queue already holds 30 items.
  - `remove` + `id`: deletes that queue item. An unknown id is discarded.
  - `skip`: the queue head becomes current (pos 0, playing). It is discarded when the queue is empty.
  - `ended` + `seq`: if `seq` matches the current track, advance (same as skip). With an empty
    queue, mark the track `finished` and pause it. A stale or duplicate `seq` is discarded. This is
    how the reports from several clients get deduplicated (video ids can repeat in a queue, so the
    video id cannot be used for this).
  - `play`, `pause`, `seek`: unchanged.
  - **`load` is removed.** `add` replaces it, and the page is embedded in the same binary.
- **`ended` has its own rate gate.** It does not share the 260 ms gate with the user controls, so a
  lone listener's report right after a click is not dropped and the queue does not stall.
- **Outbound frame** gains `seq` and `queue` (`[{id, video, by}]`), on top of
  `type, video, playing, pos, by, ts`. It is still sent on every accepted change and as the join
  snapshot. There are no new frame types.
- **Page:**
  - "Poner" sends `add`. The page checks locally whether the queue is full, because the server
    discards without replying.
  - The queue is a list under the player. Each item shows the video id, "agregado por <nick>", and a
    remove button. It is built with DOM APIs only (createElement and textContent, no innerHTML).
  - A ⏭ skip button sits next to play/pause and is disabled while the queue is empty.
  - When the player reports playerState 0 (ended), the page sends `ended` with the `seq` of the
    track it has loaded (once per seq). `reconcile` must no longer restart an ended track that the
    server still reports as playing.
  - Opening the `musica` pane counts as consent and builds the iframe. The iframe already has
    `allow="autoplay"`, so no CSP change is needed. When playback is blocked, `#music-consent` shows
    "Tu navegador bloqueó el audio: apretá Escuchar." next to the button.
- **Re-sync on return (bug fix, user report 2026-10-23):** leaving the `musica` tab pauses locally
  (unchanged). On coming back, the player currently resumes from where *this user* left it instead
  of the room's position. Returning to the pane MUST seek to the server's current position and
  play/pause state (`pos + elapsed`, plus any track/queue change that happened meanwhile) before
  resuming. The design must find why the existing drift correction doesn't fire on return, for
  example stale local time, the settle window, or the paused state skipping the drift check.
- **Privacy note changes:** opening `musica` now contacts youtube-nocookie.com, so viewers' IPs
  reach YouTube. CHAT-31 / SRV-05 and the README are updated to say so. The user accepted this for
  a friends-only chat.

## Affected Areas

| Area | Files | Change |
|------|-------|--------|
| Chat hub | `internal/chat/{music,message,room,client}.go` | queue state, add/remove/skip/ended, seq dedupe, ended gate, frame fields |
| Page | `cmd/chatroom/{index.html,app.js,app.css}` | queue list, skip button, ended report, auto-start, blocked warning |
| Tests | `internal/chat/*_test.go`, `cmd/chatroom/main_test.go` | queue state machine, dedupe, cap, WebSocket flows, markup |
| Docs / specs | `README.md`, `openspec/specs/{chat,server}/spec.md` | protocol (CHAT-21/30/31), privacy note (SRV-05/06) |

## Out of scope

- Track titles or thumbnails. They would need a server-side oEmbed fetch or a YouTube API key;
  this is a candidate follow-up.
- Reordering, voting, per-user permissions, persisting the queue across restarts, playlists or
  importing YouTube playlists.
- Server-side validation of `ended` timing. Anyone can already skip, so a forged `ended` is just
  a skip.

## Risks

- **Auto-start may be blocked** by Safari/iOS, or when the user lands in `musica` without a click
  (for example after a reconnect). The `blocked` → "Escuchar" fallback covers this.
- **Breaking frame shape:** CHAT-30 says the frame has "exactly these fields", and the tests assert
  it. Specs, `exactFields` tests and the page validator all change together.
- **Interaction with reconcile:** the restart-on-ended path in `reconcile` would fight
  auto-advance. The design must define how the page waits for the server's next frame.
- **Frame size grows with the queue.** The 30-item cap keeps it around 2 KB.
- Undocumented `postMessage` protocol (inherited risk).

## Rollback

Revert the change commits. No persisted data or schema is involved; the state lives in memory.
