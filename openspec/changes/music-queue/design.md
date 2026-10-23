# Design: Music queue

## Technical Approach

The `musica` player becomes a shared queue. The state stays server-authoritative and owned by
`Room.Run`, as before.

- **Hub.** `player` (`internal/chat/music.go`) gains `seq`, `queue`, `nextID` and `finished`.
  `apply` is restructured around one helper, `start(item, now)`, which implements "becomes
  current" from CHAT-30. `add` replaces `load`. `remove`, `skip` and `ended` are new. `parseMusic`
  validates `id` and `seq` as JSON integers ≥ 1. `musicFrame` gains `seq` and `queue`, in the exact
  8-field order. `readPump` gets a second `rateGate`, used only for `ended`. `admit`, `remove`
  (the empty-room `halt`) and `Room.music` keep their wiring unchanged.
- **Page.** Opening the `musica` pane counts as consent: the `consent` flag and its gate go away,
  and `createPlayer` is gated only by "pane shown + a current video". Track identity moves from
  the video id to `seq`. `music.loaded` becomes `music.loadedSeq`, because ids can repeat in a
  queue. The page reports `ended` once per `seq`. `reconcile` stops restarting a track that ended
  locally. Returning to the pane forces a seek to the room position (bug fix, see below). The
  queue is rendered with `createElement` + `textContent` only.

The design adds no dependencies and no new frame types. The CSP is unchanged (`frame-src`
already allows youtube-nocookie, and the iframe already has `allow="autoplay"`).

## Root cause: returning to `musica` resumes from the local position (CHAT-31 "Returning re-syncs")

Leaving calls `pauseLocal()` (`app.js:313`, `pauseVideo`). While the pane is hidden, `onMusicFrame`
keeps `music.srv` current, but `reconcile` returns at line 1287 (`!musicShown()`). On return,
`activate` calls a plain `reconcile()` with no frame (`app.js:313`). There are three defects:

1. **No path forces a seek on return.** Only the frame path seeks on its own
   (`app.js:1314-1315`, `frame.seek`). It runs only for a server frame, and `frame.seek` is set
   only when the frame's `pos` differs from the previous expectation (`app.js:1079`). A return is
   not a frame. Even if a frame arrived, the room advanced exactly as expected, so `seek` would be
   false.
2. **The real widget may not report `playerState`** (comment at `app.js:1283-1285`, and commit
   407b769). With `music.reported === false`, `reconcile` takes the branch at `app.js:1325-1331`,
   posts a bare `playVideo` and **returns**. The drift check (`app.js:1350-1355`) is never reached.
   It also requires `music.state !== -1`, which is false in that case. Local playback resumes at
   the paused position and is never corrected. This is the reported symptom.
3. **Even when states are reported**, the branch at `app.js:1333-1345` sees state 2 (paused), posts
   a bare `playVideo` (resuming at the stale spot) and sets `settleUntil = now + 3000`
   (`app.js:1344`). The drift check in the same pass fails `now >= music.settleUntil`, so the
   correction waits at least 3 s. It also depends on a fresh `currentTime` push: `music.time` and
   `timeAt` (`app.js:1254-1255`) froze at the pause, and `localMs()` (`app.js:1096`) extrapolates at
   most 1 s. The return also wrongly counts as a play try (`playTries++`).

**Fix.** Treat "the pane became visible" as an explicit re-sync, and stop calling it a
steady-state reconcile:

```js
// music.shown: whether the pane was visible at the last activate (new field, false initially)
function syncShown() {                      // replaces the musicShown() line in activate (app.js:313)
  const was = music.shown;
  music.shown = musicShown();
  if (!music.shown) { if (was) pauseLocal(); return; }
  createPlayer();                           // no-op without a video or with a frame already
  if (was) { reconcile(); return; }
  music.settleUntil = 0;
  music.playTries = 0;
  reconcile({ seek: true });                // seekTo(expected) first, then play/pause, whatever was reported
}
```

`reconcile({seek:true})` goes through the frame path. When `seq` changed while the pane was
hidden, it goes through the load path instead (`loadVideoById` at `startSeconds()` = the room
position). Both are independent of `reported`/`state`. Every `seekTo` the page posts now goes
through one helper, `seekLocal()`, which also sets `music.time = target` and `music.timeAt = now`.
After that, a stale pre-pause `currentTime` cannot trigger a second, wrong drift seek, and the 3 s
settle window protects the target. Guarding with `was` means repeated `activate` calls while the
pane stays visible (DM switches inside the room are already "hidden") don't cause audible seeks.
The "Escuchar" handler uses the same forced path.

## Architecture Decisions

### Decision: Queue state is four fields on `player`, and "becomes current" is one helper

**Choice** (`music.go`):

```go
const (
	musicInterval = 260 * time.Millisecond // unchanged; also the ended gate's interval (separate gate)
	maxMusicPos   = 24 * 60 * 60 * 1000
	maxMusicQueue = 30 // queued items, not counting the current track (CHAT-30)

	musicAdd, musicRemove, musicSkip, musicEnded = "add", "remove", "skip", "ended"
	musicPlay, musicPause, musicSeek             = "play", "pause", "seek" // musicLoad is deleted
)

type musicCmd struct {
	action string
	video  string // add
	pos    int64  // seek, ms
	id     int64  // remove
	seq    int64  // ended
}

// queueItem is one queued track, as stored and as sent (CHAT-30).
type queueItem struct {
	ID    int64  `json:"id"`
	Video string `json:"video"`
	By    string `json:"by"` // who added it
}

type player struct {
	video    string // "" until the first add
	playing  bool
	pos      int64
	at       time.Time
	by       string
	seq      int64       // +1 each time a track becomes current; 0 until the first
	queue    []queueItem // ≤ maxMusicQueue, in order
	nextID   int64       // last id handed out; ids start at 1 and are never reused
	finished bool        // the current track ended with nothing queued
}

// start makes it the current track (CHAT-30 "becomes current").
func (p *player) start(it queueItem, now time.Time) {
	p.video, p.by = it.Video, it.By
	p.pos, p.at, p.playing, p.finished = 0, now, true, false
	p.seq++
}
```

`apply(c, by, now) bool` (false = discarded, state untouched):

| action | requires | effect | `by` |
|--------|----------|--------|------|
| `add` | — | if `video == ""` or (`finished` and `len(queue) == 0`): `start({video, by: sender})`. Otherwise requires `len(queue) < 30`: `nextID++`, append `{nextID, video, sender}` | becoming current: the sender (who added it). Append: unchanged |
| `remove` | an item with `ID == c.id` | `queue = slices.Delete(queue, i, i+1)`. `pos`/`at` untouched | unchanged |
| `skip` | `len(queue) > 0` | `next := queue[0]`; `queue = slices.Delete(queue, 0, 1)`; `start(next)`. Also while paused or `finished` (start sets playing and clears finished) | `next.By` |
| `ended` | `c.seq == p.seq` and `!finished` (`seq` ≥ 1, so this also implies a current video) | queue non-empty: as `skip`. Empty: `pos = position(now)`, `at = now`, `playing = false`, `finished = true`. Accepted while paused too (someone paused right at the end) | as skip / unchanged |
| `play` | `video != ""` | `pos = position(now)`, `at = now`, `playing = true`, `finished = false` | sender |
| `pause` | `video != ""` | `pos = position(now)`, `at = now`, `playing = false` (`finished` unchanged) | sender |
| `seek` | `video != ""` | `pos = c.pos`, `at = now`, `finished = false`, `playing` unchanged | sender |

Notes:

- `seq` starts at 0, and the first `start` makes it 1, so frames always carry `seq` ≥ 1.
- `ended` with a stale or duplicate `seq` fails the `c.seq == p.seq` check. When several clients
  report the same end, the first one advances `seq` and the rest are discarded (scenario "Ended
  advances once"). After the last track ends, `finished` blocks further reports for the same `seq`.
- "`add` while `finished` with a non-empty queue" is unreachable: nothing appends while
  `finished`, because an add while finished with an empty queue becomes current. The rule is kept
  as the spec's literal fallthrough (append). It needs no test.
- `halt` is unchanged. It keeps the queue, `seq` and `finished`.
- `play` on a `finished` track resumes "from the current position", which is the end. That is why
  the page's ↺ sends `seek 0` and then `play` (see the page decisions). The spec table is followed
  literally.

**Alternatives considered**: (a) A separate `queue` type with methods. That's an abstraction for
an append/delete-at-index slice that `slices` already covers. (b) Clearing `video` when the last
track ends. The spec keeps the video and the "Terminó" state, and the join snapshot logic
(`video != ""`) stays untouched. (c) A dedupe key of video id plus position. Ids repeat, so the
server-issued `seq` is the only sound key.
**Rationale**: The state stays a Run-owned value with pure methods that take `now`, as before, so
the whole state machine stays table-testable in `TestPlayerApply`. A single `start` keeps the
three "becomes current" paths (add, skip, ended) identical by construction.

### Decision: One `queueItem` type for state and wire, and the frame copies it

**Choice** (`message.go`):

```go
type musicFrame struct {
	Type    string      `json:"type"`
	Video   string      `json:"video"`
	Playing bool        `json:"playing"`
	Pos     int64       `json:"pos"`
	By      string      `json:"by"`
	Seq     int64       `json:"seq"`
	Queue   []queueItem `json:"queue"` // never nil: [] when empty
	TS      int64       `json:"ts"`
}
```

`frame(now)` builds `Queue: append([]queueItem{}, p.queue...)`. That is always non-nil (so it
encodes as `[]`, never `null`) and is a copy, so a test that holds a frame never aliases the
state. The field order matches CHAT-30 exactly: type, video, playing, pos, by, seq, queue, ts. The
largest frame is about 30 × 60 B, roughly 2 KB.

**Alternatives considered**: A separate unexported `item` plus a wire struct with a conversion
loop. Two identical structs for one shape.
**Rationale**: The JSON tags are the only difference, and they don't hurt the in-memory use.

### Decision: Parse `id`/`seq` with the existing integer decoder, renamed `jsonInt`

**Choice**: `parseMusic` cases:

```
add    : video string && videoID.MatchString(video)
remove : id,  ok := jsonInt(env["id"]);  ok && id >= 1
skip   : always (extra keys ignored)
ended  : seq, ok := jsonInt(env["seq"]); ok && seq >= 1
play, pause, seek : unchanged
default (incl. "load") : invalid → inbound{}, false
```

`jsonMillis` becomes `jsonInt` (same body; the comment says "a JSON integer"), since it now decodes
ids and sequence numbers too. It already rejects `null`, strings, `1.5`, `1e3` and values beyond
int64, which matches "not an integer". There is no upper bound beyond int64: an id or seq that
doesn't exist simply fails the hub's requirement.
**Alternatives considered**: A second decoder. Same logic.
**Rationale**: Validation stays in the pure parse, so an invalid control never costs a gate slot
or a hub round-trip.

### Decision: A second `rateGate` for `ended` in `readPump`

**Choice** (`client.go`):

```go
music := rateGate{interval: musicInterval}
ended := rateGate{interval: musicInterval} // CHAT-30: its own budget, so a click can't starve an end report
…
if in.kind == inboundMusic {
	g := &music
	if in.music.action == musicEnded {
		g = &ended
	}
	if !g.allow(time.Now()) {
		continue
	}
}
```

The existing gate and its 260 ms constant are untouched, and so is `TestRateGate`.
**Alternatives considered**: (a) Exempting `ended` from rate limiting. Any client can forge it,
so it must stay bounded (4/s). (b) A token bucket. YAGNI; the page sends `ended` at most once per
`seq` and per replay.
**Rationale**: The struct already exists for exactly this. Two gates is two values, not a framework.

### Decision: Track identity on the page is `seq`, and the frame validator checks all 8 fields

**Choice** (`onMusicFrame`). The frame is dropped unless all of these hold:

```
YT_ID.test(video) && typeof playing === 'boolean' && Number.isFinite(pos) && pos >= 0 &&
typeof by === 'string' && Number.isInteger(seq) && seq >= 1 &&
Array.isArray(queue) && queue.length <= MUSIC_QUEUE_MAX (30) &&
every item: non-null, non-array object, Number.isInteger(id) && id >= 1,
            typeof video === 'string' && YT_ID.test(video), typeof by === 'string'
```

Valid items are copied into fresh `{id, video, by}` objects:
`music.srv = {video, playing, pos, by, seq, queue, at}`. Per-track resets (duration, the held
seek) key on `!srv || srv.seq !== msg.seq`. The `seek` flag (`app.js:1079`) applies only when the
`seq` is the same. `music.loaded` (a video id) becomes `music.loadedSeq` (0 = nothing handed to the
player). `reconcile`'s load condition becomes `s.seq !== music.loadedSeq`, and the duration guard
(`app.js:1259`) becomes `music.loadedSeq === music.srv.seq`. `startListening`'s failure path and
`resetMusic` reset `loadedSeq = 0`.

**Alternatives considered**: Keeping the video id as identity. Skip to a repeated id would never
reload, and an `ended` would be reported against the wrong track.
**Rationale**: `seq` is exactly "which track is current", and it's the token the server dedupes on.

### Decision: Report `ended` once per `seq`, guard stale reports, and never restart an ended track

**Choice**. New state: `music.endedSeq` (last seq reported, 0 initially) and
`music.staleEndUntil`. New constant: `MUSIC_END_GRACE_MS = 2000`.

```js
function maybeReportEnded() {
  const s = music.srv;
  if (!s || music.loadedSeq !== s.seq || music.endedSeq === s.seq) return;
  const localEnd = music.state === 0;
  const timeEnd = s.playing && music.duration > 0 && expectedMs() >= music.duration * 1000 + MUSIC_END_GRACE_MS;
  if (!localEnd && !timeEnd) return;
  if (sendEnded(s.seq)) music.endedSeq = s.seq;
}
```

- **Stale state 0.** Every `loadVideoById`/`cueVideoById`/`seekTo` the page posts sets
  `staleEndUntil = now + MUSIC_STALE_MS`. `setYTState(0)` is ignored before that time, for
  `onStateChange` and `infoDelivery` alike. Today `ignoreInfoUntil` covers only `infoDelivery`, and
  only after loads. So an "ended" from the previous video, or one that predates our own seek (for
  example the ↺ replay), can never be reported against the current `seq`.
- **Time fallback.** The real widget may never report `playerState` (see the root cause above),
  and then state 0 never arrives. If every client's widget behaved that way, the queue would
  stall. The fallback uses the room clock (`expectedMs`) against the duration the player reported,
  plus a 2 s grace, so a client that does report state 0 normally wins. It is still "the loaded
  track ended" in CHAT-31's sense, and duplicates are discarded by the server.
- **Replay.** `endedSeq` is cleared when a frame for the same `seq` turns `playing` from false to
  true (↺ or ▶ after "Terminó"), so a replayed track can be reported again. This is the one case
  where "once per seq" means once per play-through. Without it, a replayed track with items queued
  behind it would stall.
- `sendEnded(seq)` writes `{type:'music', action:'ended', seq}` directly when the `musica` socket
  is open. It **bypasses** `sendMusic`'s 300 ms gap and doesn't touch `lastSend`, mirroring the
  server's separate gate. If the socket isn't open it returns false and the next call retries.
- Callers: at the end of `onYTMessage` (after the state update) and in `musicTick`. Neither depends
  on `musicShown()`. A hidden pane is paused, so it doesn't end.

`reconcile` changes, so an ended track waits for the next frame:

| Where | Today | New |
|-------|-------|-----|
| frame path (`1317-1319`) | state 0 && !seek → `seekTo` + `playVideo` (restart) | `playVideo` only when `!ended && !errored && (frame.seek \|\| state !== 0)`. The restart line is removed |
| play retry (`1333`, `1342`) | state 0 → `seekTo` + `playVideo` | the condition gains `music.state !== 0`, and the `seekTo` on state 0 is removed |
| drift (`1350`) | `!ended && state !== -1 …` | also `music.state !== 0` |

A server seek, or ↺, still moves an ended player (it goes through `frame.seek`).

**Alternatives considered**: (a) Reporting only on state 0. A stall risk with the widget we
actually run against. (b) Reporting on every frame while ended. That breaks "once per seq" for no
gain, since the server dedupes anyway. (c) Ending on the server by duration. The server has no
duration (it would need oEmbed or an API key), which is out of scope.
**Rationale**: It is deterministic, bounded (at most one report per `seq` per play-through, plus
the server gate), and safe against the widget's known stale reports.

### Decision: Auto-start = pane shown. `consent` is removed, and "Escuchar" is the blocked fallback only

**Choice**: Delete `music.consent`. `createPlayer` requires `!music.frame && music.srv &&
musicShown()`, which is exactly CHAT-31's "not before the pane is shown while a video is current".
If the pane opens with no video, the frame is built by `onMusicFrame` on the first frame (it
already calls `createPlayer`). `#music-consent` starts `hidden` in the markup. `renderMusic` sets
`hidden = !music.blocked || music.errored`. The span text becomes
`Tu navegador bloqueó el audio: apretá Escuchar.` (static; no JS text swap needed). The "Escuchar"
handler clears `blocked` and `playTries`, then either calls `createPlayer()` (no frame yet, the
handshake-failure retry) or `reconcile({ seek: true })` (re-sync, then play inside the click's
user activation). `blocked` still comes from `onAutoplayBlocked`, two failed play tries, or a
failed handshake.

**Alternatives considered**: Keeping a `consent` flag that flips in `activate`. It's a second name
for `musicShown()` that has to be kept in sync.
**Rationale**: Less state. The privacy boundary (no YouTube outside the `musica` pane) is
enforced by the same `musicShown()` guard that already protects it.

### Decision: Queue UI, skip button, local cap check, and ↺

**Choice** (`index.html`): `#music-skip` sits right after `#music-toggle`:
`<button id="music-skip" type="button" class="btn" aria-label="Saltar al siguiente" disabled>⏭</button>`.
After `#music-now`: `<ol id="music-queue" class="music-queue" aria-label="Cola" hidden></ol>`.

- `renderQueue()` is called from `onMusicFrame` and `resetMusic` only, **not** from the 500 ms
  tick, so the remove buttons keep focus. It runs `replaceChildren()`, then for each item builds
  `li.music-queue-item` > `span` (`textContent = 'youtu.be/' + id + ' · agregado por ' + by`) +
  `button.btn.btn-ghost` (`type=button`, `textContent='✕'`, `aria-label='Quitar de la cola'`,
  `dataset.id = String(item.id)`). `hidden = queue.length === 0`.
- There is one delegated `click` listener on `#music-queue`:
  `e.target.closest('button[data-id]')` → `sendMusic('remove', { id: Number(b.dataset.id) })`.
- `renderMusic` sets `#music-skip.disabled = !s || !online || s.queue.length === 0` and disables
  the queue buttons while offline (`querySelectorAll`). Skip click → `sendMusic('skip')`.
- Poner: after `parseYouTubeId`, `if (music.srv && music.srv.queue.length >= MUSIC_QUEUE_MAX)` →
  `setMusicError('La cola está llena')` and nothing is sent. Then `sendMusic('add', { video: id })`.
- Toggle: ↺ shows only when `musicEnded() && !s.playing` (finished or paused at the end). A click
  sends `seek {pos:0}`, and if accepted, `setTimeout(() => sendMusic('play'), MUSIC_SEND_GAP_MS + 20)`.
  While `playing` and locally ended (waiting for the advance), it shows ⏸ as usual.
- The now line stays `youtu.be/<id> · último cambio: <by>`.

CSS (`app.css`): `.music-queue { list-style:none; margin:0; padding:0; max-height:7.5em;
overflow-y:auto; font-size:13px; color:var(--color-neutral-400) }`, `.music-queue-item { display:flex;
align-items:center; gap:var(--space-2); }` (the span gets `flex:1; overflow-wrap:anywhere`). The
wide-screen player becomes `grid-row: 1 / span 7`: there are now 6 left-column rows (form,
controls, now, queue, error, consent), and the span must cover them all. In the pinkroom skin,
`.app:has(#music:not([hidden]))` goes from `800px` to `900px` to absorb the capped queue (about
4–5 rows visible). The ≤800px rule (`height:auto`) is unchanged.

**Alternatives considered**: (a) A per-button listener that is recreated on each render. This
works, but delegation keeps it to one listener. (b) Rendering the queue in the tick. It steals
focus every 500 ms. (c) A separate "cola llena" element. `#music-error` already exists for exactly
this.
**Rationale**: Strict CSP (no inline handlers, no `innerHTML`), and nick/id text from the wire
only ever reaches `textContent`.

## Data Flow

```
Page A                 readPump A                          Room.Run (musica)                       Pages A, B
 │ Poner (queue < 30)
 │ {"type":"music","action":"add","video":V2}
 │ ───────────────▶ parseMusic ok → music gate (260 ms)
 │                  broadcast ───────────────────────▶ apply(add): video set → append {id 1, V2, A}
 │                                                     publish frame ─────────────────────────▶ {…,"seq":1,"queue":[{"id":1,"video":V2,"by":"A"}],…}
 │                                                                                             renderQueue(); reconcile()
 ·· track V1 ends ··
Page B: widget state 0 (past staleEndUntil), loadedSeq 1 == srv.seq 1, endedSeq ≠ 1
 │ {"type":"music","action":"ended","seq":1} ─▶ ended gate (separate)
 │                                              broadcast ─▶ apply(ended,1): seq ok → start(V2): seq 2, by A
 │                                                           publish ──────────────────────────▶ {video V2, playing, pos 0, by A, seq 2, queue []}
Page A: also sends ended seq 1 ────────────────────────────▶ apply: 1 ≠ 2 → discarded, no frame
                                                                                               reconcile: seq 2 ≠ loadedSeq 1 → loadVideoById [V2, 0]
```

Return to the pane:

```
X in general: pauseLocal → widget paused at t0;   frames keep srv current (reconcile returns: hidden)
X back in musica: activate → syncShown: was=false → reconcile({seek:true})
   same seq   → seekLocal(expected) ; playVideo  (or pauseVideo)  ; settle 3 s
   seq moved  → loadVideoById [srv.video, expected]               ; settle 3 s
```

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/chat/music.go` | Modify | Action constants (`load` deleted; add/remove/skip/ended), `maxMusicQueue`, `musicCmd.id/seq`, `queueItem`, new `player` fields, `start`, the rewritten `apply` (table above), `frame` with `seq` + copied `queue` |
| `internal/chat/message.go` | Modify | `musicFrame` gains `Seq` and `Queue` (8 fields, in order); `parseMusic` cases add/remove/skip/ended; `jsonMillis` → `jsonInt` |
| `internal/chat/client.go` | Modify | A second `rateGate` (`ended`) in `readPump`, selected by `in.music.action` |
| `internal/chat/room.go` | Modify | Comments only (`music()`, `admit` snapshot "video current"). The logic is unchanged |
| `internal/chat/room_test.go` | Modify | Helpers and hub tests (below) |
| `internal/chat/client_test.go` | Modify | `TestParseInbound` rows, WebSocket tests (below) |
| `cmd/chatroom/main_test.go` | Modify | `load` → `add`, `readMusicFrame` + `seq`, markup assertions (below) |
| `cmd/chatroom/index.html` | Modify | `#music-skip`, `#music-queue`, `#music-consent` starts `hidden` with the new blocked text |
| `cmd/chatroom/app.js` | Modify | Header comment (no Escuchar gate); remove `consent`; `loaded` → `loadedSeq`; `shown`, `endedSeq`, `staleEndUntil`; `MUSIC_QUEUE_MAX`, `MUSIC_END_GRACE_MS`; the `onMusicFrame` validator; `syncShown` (replaces `app.js:313`); `seekLocal`; `maybeReportEnded`/`sendEnded`; `reconcile` edits; `renderQueue`; skip/queue/Poner/toggle/Escuchar handlers; `setYTState` stale-0 guard |
| `cmd/chatroom/app.css` | Modify | `.music-queue*`; `grid-row: 1 / span 7`; pinkroom `height: 900px` |
| `README.md` | Modify | The protocol (add/remove/skip/ended, the 8-field frame, the queue cap, ended gate), auto-start, and the privacy note (opening `musica` contacts youtube-nocookie.com) |
| `CLAUDE.md` | Modify | "En curso: music-queue" during work; `music.go` description mentions the queue |
| `openspec/specs/{chat,server}/spec.md` | Modify at archive | Merge the deltas (CHAT-21/30/31, SRV-05) |

## Testing

**Helpers (`room_test.go`)**. `decodeMusic` uses
`exactFields(t, b, "type","video","playing","pos","by","seq","queue","ts")`, requires `pos`, `ts`
and `seq` to be integers, requires `queue` to start with `[` (never `null`), and runs
`exactFields(…, "id","video","by")` on each element (the `id` must be an integer).
`expectMusic(t, c, video, playing, by)` keeps its signature; callers check `.Seq`/`.Queue` on the
result. `startMusicRoom`/`musicCtl` are unchanged. `loadCmd` → `addCmd(video)`, plus `skipCmd`,
`removeCmd(id)` and `endedCmd(seq)`.

| Test | Asserts |
|------|---------|
| `TestPlayerApply` (rewritten table, fixed `now`) | The first add → seq 1, pos 0, playing, by = adder. Add while current → appended with id 1, 2…, by/pos/at unchanged. Add with 30 queued → false. Remove an existing/missing id. Skip → head becomes current, seq+1, by = item.By, also from paused and from finished; skip with an empty queue → false. Ended with a matching seq + queue → advance; with an empty queue → finished, paused at `position(now)`, by unchanged; ended stale/duplicate/while finished → false. Play/pause/seek with no video → false. Seek/play clear `finished`, pause doesn't. Add while finished with an empty queue → current, seq+1. `halt` keeps queue/seq/finished. Ids are never reused after a remove |
| `TestPlayerFrameQueue` | `frame` with an empty queue marshals `"queue":[]`. The field order is the spec's. The frame's queue is a copy (mutating the state doesn't change it) |
| `TestMusicAddBroadcast` (was Load) | A and B get seq 1 and `queue` `[]` (scenario "First add plays") |
| `TestMusicAddQueues`, `TestMusicSkipAdvances` | Scenarios "Add while playing queues" and "Skip advances" (exact `queue` contents) |
| `TestMusicEndedOnce` | A, B and C send `ended 3`-style for the current seq → one frame with seq+1; `syncRoom` + `expectEmpty` afterwards |
| `TestMusicLastTrackEnds` | Ended with an empty queue → same video, `playing:false`; a later add → current with seq+1 |
| `TestMusicRemoveById`, `TestMusicQueueCap` | One frame, then the second remove is discarded. With 30 queued, an add → `expectEmpty` |
| `TestMusicNeedsVideo` (extended) | play, seek, skip, `ended 1` and `remove 1` with nothing current → no frames |
| `TestMusicNewcomerSnapshot`, `TestMusicEmptyRoomPauses` | The snapshot includes `queue` (2 items). After the room empties, the queue is kept |
| existing others | They stay, with `load` → `add` |

**Parse / WebSocket (`client_test.go`)**. `TestParseInbound`: the `load` rows become `add` rows,
and `action:"load"` with a valid video becomes an invalid row. New rows: remove `id` 1 / 2^62 valid;
`id` 0, -1, `"1"`, 1.5, 1e3, null, missing, 2^63 → invalid; skip valid (extra keys ignored); ended
`seq` 1 valid; `seq` 0, `"1"`, 1.5, missing → invalid. `readMusic` inherits the new
`decodeMusic`. WebSocket tests: `load` → `add` throughout. New
`TestMusicEndedNotThrottledOverWebSocket`: A alone; add V1, sleep 300 ms, add V2 (queued), sleep
300 ms, then pause, play and ended `seq 1` back to back → a frame with `seq` 2 and video V2
arrives (scenario "Ended is not throttled"). `TestMusicThrottledOverWebSocket` is unchanged (it
still proves the 260 ms gate for user controls).

**Wiring and markup (`main_test.go`)**. `TestMusicOnlyInMusica` and `TestMusicSyncAcrossSessions`
use `add`, and `readMusicFrame`'s struct gains `Seq *int64` (asserted non-nil, = 1).
`TestIndexHasMusicPlayer`: the id list gains `music-skip` and `music-queue`; `music-listen` is
still "Escuchar"; `#music-consent` must start `hidden`; html contains
`Tu navegador bloqueó el audio: apretá Escuchar.`; and the comment at line 658 becomes "the frame
is built by app.js only, once the musica pane is shown". The no-`<iframe` and no-YouTube-URL
assertions stay. `TestNoYouTubeScript` and the CSP test are unchanged.

Gates: `/snap/go/current/bin/gofmt -l .`, `go vet ./...`, `go test -race ./...`.

**Manual checklist** (A = Chrome, B = Firefox, devtools open):

1. B stays in `general`: no request to any YouTube host. A opens `musica` (nothing current), pastes
   a `music.youtube.com/…&si=` link → Poner → it plays without Escuchar. B opens `musica` → it starts
   at about the same position (or shows the blocked text + Escuchar, which syncs).
2. While playing, B adds two links → both see the queue with "agregado por B", and ⏭ is enabled.
   A removes the second, then skips → both play the first, the queue is empty and ⏭ is disabled.
3. Add one item, seek to about 10 s before the end → both advance within a few seconds. The WS
   panel shows at most one `ended` per client and a single seq+1 frame.
4. Let the last track end → "Terminó", ↺ in both. ↺ replays from 0:00 in both. Let it end again
   with an item queued → it advances (the replay ended report works).
5. After "Terminó", paste a link → it plays immediately (not queued).
6. **Re-sync bug**: X and Y playing; X goes to `general` for 30 s and comes back → X lands at Y's
   position (±2 s) right away, not where X left. Repeat with a skip made by Y while X was away → X
   loads the new track at the room position.
7. Queue 30 items (script in the console, spaced 300 ms) → Poner shows "La cola está llena" and
   the WS panel shows no send.
8. A repeated video id queued twice: skip to the second copy → it restarts from 0 (the seq change
   reloads it).
9. Reload `musica` with a hard refresh and no click (Chrome without engagement / Safari) → blocked
   text + Escuchar; pressing it starts in sync.
10. 480 px and ≥1000 px, both skins: the video is ≥200 px, the queue scrolls after about 4 rows,
    nothing overlaps; pinkroom at 900 px.
11. The console shows no CSP violations (the SRV-05 scenario walk).

## Open Risks

- **Undocumented widget behavior.** `playerState` may never be reported. The time fallback and the
  forced re-sync cover end detection and return. If `duration` is never reported either, only
  clients whose widget reports state 0 can advance the queue.
- **Early cut.** The first client to report an end wins. A client up to 2 s ahead (inside the
  drift tolerance) cuts the tail for the others by that much. This is accepted.
- **Autoplay blocked** on Safari/iOS or after a reconnect without a gesture. Detection relies on
  `onAutoplayBlocked`, or on two play tries when states are reported. With a widget that reports
  nothing and no `onAutoplayBlocked`, the warning may not appear. The iframe has
  `pointer-events: none`, so "Escuchar" is the only recovery.
- **"Once per seq" vs replay.** The page re-arms `ended` for the same `seq` after a
  paused→playing transition (↺/▶ after "Terminó"). Strictly, that's once per play-through, not
  once per seq. The archive step should reword CHAT-31 accordingly.
- **↺ is two controls** (`seek 0` and then `play` 320 ms later). That follows CHAT-30 literally
  (`play` on a finished track resumes at the end). If someone else acts in between, the `play`
  still lands harmlessly.
- **Forged `ended`/`skip`.** Same trust model as skip, and bounded by the gates.
- **Privacy.** Opening `musica` with a video current contacts youtube-nocookie.com without a click
  (the viewer's IP and the page origin). The user accepted this; the README and spec are updated.
- **No JS tests.** Queue rendering, end reporting and re-sync rely on the manual checklist.
