# Design: Room music sync

## Technical Approach

The `musica` room gets a shared YouTube player. The server holds a small piece of state and the
page follows it. The server never touches YouTube, and the page talks to YouTube only after the
user presses "Escuchar".

- **Hub.** A room built with `chat.NewMusicRoom(id)` has a non-nil `player` (`video`, `playing`,
  `pos` ms anchored at a monotonic `at`, `by`). Every other room has `player == nil`.
  `parseInbound` classifies `{"type":"music",…}` frames. It validates them completely, so an
  invalid one is dropped in `readPump` and never falls back to chat. `readPump` rate-limits
  them per connection, and `Room.dispatch` applies them to the player. Each accepted control
  goes out as one `music` frame through `publish` (must-have delivery). The newcomer replay
  lives in `admit`, and the pause when the room empties lives in `remove`, the single removal
  path. Everything runs on `Room.Run`, with no locks.
- **Wiring.** `cmd/chatroom` builds `musica` with `NewMusicRoom` and adds
  `frame-src https://www.youtube-nocookie.com` to the CSP. `script-src` is unchanged.
- **Page.** A strip below the room tabs is visible only while the active pane belongs to `musica`.
  Controls send `music` actions. Server frames are the only thing that changes the shared UI
  (play button, seek bar, "puesto por"). After "Escuchar", a `youtube-nocookie` iframe is built
  with DOM APIs and driven by `postMessage`, using the same wire protocol as YouTube's own widget
  script (we just don't load that script). A `reconcile()` loop makes the local player match the
  server state. Player events never produce outbound actions.

The design follows the proposal and the change's spec deltas (CHAT-21/30/31, SRV-05/06), with one
spec tweak suggested (Shorts hosts, see Open Risks). It adds no dependencies.

## YouTube embed: verified vs assumed

| # | Fact | Status | Source |
|---|------|--------|--------|
| Y1 | Commands are JSON **strings** posted to the iframe's `contentWindow`: `{"event":"command","func":F,"args":[…],"id":N,"channel":"widget"}`. The target origin is the iframe `src` origin. | **Verified** (current widget script) | `www.youtube.com/s/player/…/www-widgetapi.js`, function `bb`/`sendMessage` |
| Y2 | Handshake: the widget posts `{"event":"listening","id":N,"channel":"widget"}` every **250 ms**, starting (again) on the iframe `load` event, until `initialDelivery` (or `alreadyInitialized`) arrives. Commands are **queued** until the first message from the player arrives, then flushed. | **Verified** (widget script) | same, `cb`, `yt_embedsWidgetPollIntervalMs \|\| 250` |
| Y3 | Inbound events: `initialDelivery` and `infoDelivery` carry `info` objects merged into `playerInfo` (`currentTime`, `playerState`, `duration`, `volume`, `muted`, `videoData`, `currentTimeLastUpdated_`, `playbackRate`). `apiInfoDelivery` is metadata. Any other event name (e.g. `onStateChange`, `onError`, `onReady`, `onAutoplayBlocked`) is dispatched generically with `info` as data. | **Verified** for the names the widget handles. The `onX` names come from the iframe and are **assumed** to match the public API event names. | widget script `switch(h.event)` |
| Y4 | `onStateChange`/`onError`/`onAutoplayBlocked` are only sent after a `{"event":"command","func":"addEventListener","args":["onError"]}` per event. `infoDelivery` arrives without subscribing. | **Verified** that the widget sends `addEventListener` once per event name (`ab`). Push of `infoDelivery` without a subscription is **assumed** (the widget's `getCurrentTime` relies on it). | widget script |
| Y5 | The widget extrapolates `currentTime` while `playerState === 1` by `min(elapsed, 1 s) × playbackRate`. | **Verified** | widget script `getCurrentTime` |
| Y6 | The widget appends `origin=<protocol//host>` and `widgetid=<id>` to the embed URL. It does **not** check `event.source`, only `event.origin` against the host. | **Verified** | widget script |
| Y7 | `playerState`: -1 unstarted, 0 ended, 1 playing, 2 paused, 3 buffering, 5 cued. `onError`: 2 invalid param, 5 HTML5 error, 100 not found/private, 101 and 150 embedding disabled, **153 missing HTTP Referer** or equivalent client identity. | **Verified** | IFrame API reference |
| Y8 | API clients "must provide identification through the HTTP Referer". YouTube recommends `strict-origin-when-cross-origin`. | **Verified** | Required Minimum Functionality (RMF) |
| Y9 | The iframe `referrerpolicy` attribute governs "fetching the frame's resource". It overrides the document's `no-referrer` for that request. | **Verified** (MDN). Coverage of later navigations inside the frame is unspecified there, and we don't need it. | MDN `<iframe>` |
| Y10 | Embedded player viewport ≥ **200×200 px**. No overlays in front of any part of it. Scripted playback must not start until the player is visible. | **Verified** | RMF |
| Y11 | Top frames can delegate sound autoplay to cross-origin iframes (`allow="autoplay"`). Sound autoplay needs a prior user interaction with the domain. | **Verified** | Chrome autoplay policy |
| Y12 | `youtube-nocookie.com` embeds accept the same parameters and postMessage protocol as `youtube.com`, and don't redirect to another host. | **Assumed** (widely used, not documented) | — |
| Y13 | `localhost` and plain-http origins pass the Referer check (`strict-origin-when-cross-origin` sends `http://localhost:8080/` to an https host, since it isn't a downgrade). | **Assumed**. Check in manual step 1. | — |

## Architecture Decisions

### Decision: The player is a room capability chosen at construction (`NewMusicRoom`)

**Choice**:

```go
// room.go
type Room struct {
	// … existing fields …
	player *player // nil: this room has no shared player (CHAT-30); set only by NewMusicRoom
}

// NewMusicRoom is NewRoom plus a shared player (CHAT-30).
func NewMusicRoom(id string) *Room { cr := NewRoom(id); cr.player = &player{}; return cr }
```

```go
// cmd/chatroom/main.go
const musicRoom = "musica" // has the shared player; mirrored by MUSIC_ROOM in app.js

func newChatServer(allowedOrigins []string) *chat.Server {
	// … if id == musicRoom { rooms[i] = chat.NewMusicRoom(id) } else { rooms[i] = chat.NewRoom(id) }
}
```

**Alternatives considered**: (a) `NewRoom(id, opts ...RoomOption)`: an options framework for one
flag. (b) An exported `EnablePlayer()` setter. It mutates after construction and needs a "call
before Run" rule. (c) The chat package checking `id == "musica"`. That breaks "rooms are generic,
the id is never on the wire", and the test rooms are named `general`. (d) Advertising the
capability through `/rooms`. That changes an API shape the page doesn't need, because it already
knows `musica`.
**Rationale**: This is the smallest change that matches the existing constructor pattern (fully
initialized struct, nothing set later). None of the ~40 `NewRoom("…")` test call sites change.
A nil pointer reads as "no capability", and `cr.player == nil` is a single guard.

### Decision: `player` is a Run-owned value with pure methods that take `now`

**Choice** (new file `internal/chat/music.go`):

```go
const (
	musicInterval = 250 * time.Millisecond // ≤ 4 accepted controls per second per connection (CHAT-30)
	maxMusicPos   = 24 * 60 * 60 * 1000     // ms
	musicLoad, musicPlay, musicPause, musicSeek = "load", "play", "pause", "seek"
)

var videoID = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`) // Go's $ is end-of-text: no trailing \n

// musicCmd is a validated music control (CHAT-30).
type musicCmd struct {
	action string // one of the four constants
	video  string // load only
	pos    int64  // seek only, ms
}

// player is a room's shared playback state. Owned by Room.Run: no locks.
type player struct {
	video   string    // "" until the first load
	playing bool
	pos     int64     // ms into the video at `at`
	at      time.Time // monotonic reading when pos was taken
	by      string    // nickname behind the current state
}

func (p *player) position(now time.Time) int64        // playing ? pos + now.Sub(at).Milliseconds() : pos
func (p *player) apply(c musicCmd, by string, now time.Time) bool // false: needs a video and none is loaded
func (p *player) halt(now time.Time)                 // playing → pos = position(now), at = now, playing = false; by unchanged
func (p *player) frame(now time.Time) musicFrame
```

`apply` semantics, matching the CHAT-30 table. Every accepted control re-anchors `pos = …,
at = now` and sets `by`:

| action | requires video | new state |
|--------|----------------|-----------|
| load   | no  | `video = c.video`, `pos = 0`, `playing = true` |
| play   | yes | `pos = position(now)`, `playing = true` (also when already playing: accepted and broadcast, per the spec's flood scenario) |
| pause  | yes | `pos = position(now)`, `playing = false` |
| seek   | yes | `pos = c.pos`, `playing` unchanged |

**Alternatives considered**: (a) Fields directly on `Room`. They would mix with registry state and
the logic couldn't be tested in isolation. (b) A `now func()` seam on `Room`. Passing `now` into
pure methods gives deterministic tests of the position math (`TestPlayerApply`) without any
injectable clock. Hub tests use bounds. (c) Dropping no-op play/pause. That contradicts CHAT-30
("after each accepted control … MUST send").
**Rationale**: The state machine is about 40 lines that are testable on their own, and the hub
only wires it. `time.Time` from `time.Now()` carries the monotonic clock, so wall-clock jumps
don't move the position.

### Decision: Parse completely in `parseInbound`. Invalid music is dropped before the hub.

**Choice**: `inbound` gains `music musicCmd` and a new kind `inboundMusic` (appended after
`inboundTyping`, so the zero value is still chat). A new `case typeMusic:` in `parseInbound`:

```
action := jsonString(env["action"])
load : video := jsonString(env["video"]); ok iff string && videoID.MatchString(video)
play, pause : always ok (other keys ignored)
seek : pos, ok := jsonMillis(env["pos"]); ok iff 0 ≤ pos ≤ maxMusicPos
default (missing/unknown/non-string action) : invalid
→ valid:   return inbound{kind: inboundMusic, music: musicCmd{…}}, true
→ invalid: return inbound{}, false          // never reaches the plain-text fallback
```

`jsonMillis(raw)` requires `raw[0]` to be `-` or a digit (so `null`, which `json.Unmarshal` would
silently decode as 0, as well as strings, booleans and objects are rejected) and then decodes
into `int64`. `1.5`, `1e3` and values beyond int64 fail to decode, so only JSON integers pass.
Exact-key matching stays: `{"type":"MUSIC"}` is still plain chat text, as for every other
type (CHAT-21).

**Alternatives considered**: Classifying in `parseInbound` and validating in the hub. Invalid
frames would then cost a hub round-trip and a gate slot. The parse stays pure and
table-testable, like the dm rules.
**Rationale**: CHAT-21 rule 2a says "never treated as a room message". Returning `ok=false` from
the `typeMusic` case makes that true by construction. "No player in this room" is the only check
left for the hub, because only the hub knows the room.

### Decision: Generalize `typingGate` into `rateGate` with an interval

**Choice** (`client.go`):

```go
// rateGate admits at most one event per interval. It belongs to one readPump goroutine (no lock);
// a discarded event does not move the window.
type rateGate struct {
	interval time.Duration
	last     time.Time
}
func (g *rateGate) allow(now time.Time) bool
```

`readPump` has `typing := rateGate{interval: typingInterval}` and
`music := rateGate{interval: musicInterval}`. After `parseInbound`:
`if in.kind == inboundMusic && !music.allow(time.Now()) { continue }`. The gate applies in every
room, which is harmless. `TestTypingGate` becomes `TestRateGate` (the same steps for typing, plus a
250 ms table).
**Alternatives considered**: (a) A second copy, `musicGate`. That duplicates 8 lines with
different constants. (b) A token bucket allowing a burst of 4. A 250 ms minimum spacing already
satisfies "at most 4 per second", and the page spaces its own sends 300 ms apart.
**Rationale**: There are now two users of the same pattern, which is the point where extracting it
is justified. The change is a field, not a framework.

### Decision: Dispatch, replay and empty-room pause live in Run's existing paths

**Choice** (`room.go`):

- `dispatch`: `case inboundMusic: cr.music(in)`.
- `music(in)`: `if cr.player == nil { return }`. `now := time.Now()`.
  `if !cr.player.apply(in.music, in.from.nickname, now) { return }`. Then
  `if f := encode(cr.player.frame(now)); f != nil { cr.publish(f) }`. Not stored. Not logged.
- `admit`: take `now := time.Now()` and `ts := now.UnixMilli()`. When
  `cr.player != nil && cr.player.video != ""`, append `encode(cr.player.frame(now))` to `private`
  after history. The existing loop delivers it and still aborts on a failed delivery, so the
  order is welcome → users → history → music → (published) join.
- `remove`: after `close(c.send)`, `if len(cr.clients) == 0 && cr.player != nil { cr.player.halt(time.Now()) }`.
  `remove` is the single removal path (leave, slow drop, shutdown), so a last client dropped as
  slow also pauses the room. `halt` sends nothing, because nobody is left.

Outbound frame (`message.go`, `typeMusic = "music"`):

```go
// musicFrame is a room's shared player state (CHAT-30). pos is computed when the frame is built,
// so receivers treat it as "now" and server/client clock skew doesn't matter. Never stored.
type musicFrame struct {
	Type    string `json:"type"` // always "music"
	Video   string `json:"video"`
	Playing bool   `json:"playing"`
	Pos     int64  `json:"pos"` // ms
	By      string `json:"by"`
	TS      int64  `json:"ts"`
}
```

**Alternatives considered**: (a) `offer` (best effort, like typing). A lost state frame leaves
that client diverged until the next change, and CHAT-30 says must-have. (b) Excluding the sender
from the broadcast. The sender needs the authoritative `pos`, and its UI only changes from server
frames (see the frontend decision). (c) Pausing in `leave` only. That would miss slow drops.
**Rationale**: This reuses `publish`, `deliver` and `remove` exactly as chat does, so the
slow-client semantics (CHAT-03) apply unchanged.

### Decision: The page never contacts YouTube before "Escuchar"

**Choice**: "Escuchar" is the only thing that creates the iframe (CHAT-31). It sets
`music.consent = true` for the page's life. If a video is loaded, the iframe is created at once.
Otherwise it's created when the first `music` frame arrives. "Poner", play/pause and seek work
without consent, because they only send WebSocket actions. The strip shows the state as text
(`youtu.be/<id>` and "puesto por").
**Alternatives considered**: (a) Creating the iframe on entering `musica`. Every visitor's IP
would reach YouTube without a choice, and it violates the spec. (b) Letting "Poner" count as
consent. It's convenient, but the spec names one explicit control. A single, clearly labelled
consent point is easier to reason about.
**Rationale**: It meets the privacy requirement and RMF Y10 (the player is visible before any
scripted play), and the click is the user activation that sound autoplay needs (Y11).

### Decision: Iframe attributes and URL

**Choice** (built with `createElement`, with every attribute set **before** it's appended, so the
first request already carries the policy):

```
src  = https://www.youtube-nocookie.com/embed/<id>?enablejsapi=1&controls=0&disablekb=1
       &playsinline=1&rel=0&iv_load_policy=3&widgetid=1&origin=<encodeURIComponent(location.origin)>
referrerPolicy = 'strict-origin-when-cross-origin'   // Y8/Y9: avoids error 153; sends only the origin
allow          = 'autoplay; encrypted-media'          // Y11; encrypted-media for protected (music) videos
title          = 'Reproductor de YouTube'
width 356, height 200 (CSS: min 200×200, Y10)       // no sandbox, no allowfullscreen
```

The page keeps `Referrer-Policy: no-referrer`. Only this one request sends the origin (never the
path or the query).
**Alternatives considered**: (a) Leaving the page policy for the iframe too. That's error 153 (Y7).
(b) Changing the page policy. It would leak the referrer to Giphy/Imgur image requests as well.
(c) `sandbox="allow-scripts allow-same-origin allow-popups allow-presentation"`. The frame is
cross-origin, so it already can't touch our DOM or socket. The remaining gain (blocking
top-navigation, which browsers already gate behind user activation) isn't worth the risk of
breaking an undocumented embed. (d) Hiding the iframe for audio only. RMF requires a ≥200×200
visible viewport and no overlays (Y10).
**Rationale**: This is the smallest exposure that works, and each attribute traces to a
verified source.

### Decision: Mirror YouTube's widget handshake and validate every inbound message

**Choice** (`app.js`). Handshake:

1. On the iframe `load` event, post `{"event":"listening","id":1,"channel":"widget"}` every
   `YT_LISTEN_MS = 250` (Y2). After `YT_LISTEN_TRIES = 40` (10 s) with no answer, stop and show
   "No se pudo cargar el reproductor."
2. The first valid inbound message (normally `initialDelivery`) sets `music.ready = true` and
   clears the timer. Then send `addEventListener` for `onStateChange`, `onError` and
   `onAutoplayBlocked` (Y4), apply the local volume/mute, and call `reconcile()`.
3. `ytCommand(func, args = [])` posts
   `JSON.stringify({event:'command', func, args, id:1, channel:'widget'})` to `YT_ORIGIN`. It's a
   no-op unless `ready`, and `reconcile()` re-derives every command after ready, so there's no
   queue.

`onYTMessage(e)` (one global `message` listener) drops the message unless
`music.frame && e.source === music.frame.contentWindow && e.origin === YT_ORIGIN &&
typeof e.data === 'string' && e.data.length <= 65536`. It `JSON.parse`s inside `try`, and the
result must be a non-array object with a string `event`. It reads only these fields:

| event | field read | check |
|-------|-----------|-------|
| `initialDelivery`, `infoDelivery` | `info.currentTime` | finite number ≥ 0 → `music.time`, `music.timeAt = performance.now()` |
| | `info.playerState` | one of -1,0,1,2,3,5 → `music.state` |
| | `info.duration` | finite number > 0 → `music.duration` |
| `onStateChange` | `info` | same set as `playerState` |
| `onError` | `info` | integer → `showMusicError(code)` |
| `onAutoplayBlocked` | — | `music.blocked = true` |

No string from the iframe ever reaches the DOM. Errors map codes to fixed Spanish text:
101/150 → "Este video no se puede reproducir fuera de YouTube.", 100 → "El video no existe o es
privado.", 153 → "YouTube rechazó el reproductor (falta el referer).", others → "No se pudo
reproducir el video.".
**Alternatives considered**: (a) Loading `iframe_api`. The user rejected it, and it would widen
`script-src`. (b) Trusting origin alone, as the widget does (Y6). Another YouTube frame
(e.g. a future embed) could then drive our state.
**Rationale**: Reusing the exact shapes the official widget sends today gives the best chance
of compatibility. The double check plus defensive parsing treats the frame as untrusted input.

### Decision: Sync is a single idempotent `reconcile()`, and local events are never sent

**Choice**. Server state on the page is
`music.srv = {video, playing, pos, by, at: performance.now()}`, and
`expectedMs() = srv.pos + (srv.playing ? performance.now() - srv.at : 0)`. The local position is
`music.time*1000`, plus `min(now - timeAt, 1000)` while `state === 1` (the widget's rule, Y5).
The track has ended when `duration > 0 && expectedMs() >= duration*1000`.

`reconcile()` runs only when `ready && musicShown()`:

1. `srv === null` → if playing locally, `pauseVideo`. Done.
2. `srv.video !== music.loaded` → `loadVideoById` (when playing) or `cueVideoById` (when paused)
   with `[video, expectedMs()/1000]`. Set `loaded = video`, reset `duration`/error, and set
   `settleUntil = now + 3000`. Done.
3. `srv.playing && !ended && state ∉ {1,3}` and `now ≥ settleUntil` → `playVideo`, and set
   `settleUntil = now + 3000`. Two consecutive tries without reaching state 1 set
   `music.blocked = true`, which shows the "Escuchar" button again.
4. `!srv.playing && state ∈ {1,3}` → `pauseVideo`.
5. `!ended && now ≥ settleUntil && |localMs - expectedMs()| > MUSIC_DRIFT_MS (2000)` →
   `seekTo [expectedMs()/1000, true]` and set `settleUntil = now + 3000`.

Callers: every valid `music` frame (sets `settleUntil = 0` first, so a global action applies at
once), `ready`, each `infoDelivery`/`onStateChange`, and `activate` into `musica`.

**Invariant**: `sendMusic` is called only from strip button, form and seek-bar handlers. Nothing in
the iframe path ever sends. The play button, seek bar, time and "puesto por" render from
`music.srv` and `expectedMs()`, never from the local player. Only `duration` comes from the
player. A user who clicks inside the iframe and pauses gets resumed at the next reconcile.

**Alternatives considered**: (a) Optimistic local apply on click. Two clients clicking at once
would diverge until the echo arrived, and the server answers within one round-trip anyway.
(b) Applying only on server frames (no periodic check). Drift from buffering would never heal.
(c) Adjusting `playbackRate` for smooth catch-up. That's more protocol surface for a 2 s
tolerance, so it's YAGNI.
**Rationale**: One function derives every command from (server state, local state). That makes it
safe to call from anywhere, and the settle window prevents seek/play loops while the player is
buffering.

### Decision: Page lifecycle hooks reuse `activate`, `addRaw`, `teardown` and the reconnect flow

**Choice**:

| Hook | Change |
|------|--------|
| `addRaw`, before the `from` guard | `if (msg.type === 'music') { if (room.id === MUSIC_ROOM) onMusicFrame(msg); return; }`. Valid only if `YT_ID.test(video)`, `playing` is boolean, `pos` is a finite number ≥ 0 and `by` is a string; otherwise ignored |
| `addRaw` `welcome` | in `musica`: `music.awaitSnapshot = true` |
| `onMusicFrame` | `awaitSnapshot = false`; store `srv`; create the iframe if `consent` and there's none; `renderMusic()`; `reconcile()` |
| `addRaw` `join` with `msg.from === room.myName` | in `musica` with `awaitSnapshot` still true: no track on the server (e.g. it restarted), so `srv = null`; `renderMusic()`; `reconcile()`. The own join is the first frame after the snapshot slot (CHAT-11 order) |
| `activate(room, conv)` | ends with `renderMusic()`, which shows `#music` iff `active.id === MUSIC_ROOM`, starts or stops a 500 ms `musicTick` (time and seek bar), and calls `pauseLocal()` (`pauseVideo` if ready) when hidden or `reconcile()` when shown |
| `scheduleReconnect` | nothing new. The local player keeps its extrapolated position, and strip buttons are disabled while `room.status !== 'online'` (in `renderMusic`, called from `setStatus` for `musica`) |
| `teardown(room)` for `musica` | `resetMusic()`: remove the iframe, clear the timers, `srv = null`, `ready = false`, `loaded = ''`. `consent` stays (one gesture per page load) |

**Alternatives considered**: (a) Having the server always send a `music` frame to newcomers
(`video:""` when empty), which would remove the self-join trick. That contradicts CHAT-30 ("no
`music` frame is sent while no video is loaded"). (b) Destroying the iframe when leaving `musica`.
Re-sync on return would then cost a full reload. A paused, hidden iframe is cheap.
**Rationale**: Each hook already exists for the same purpose (room switch, frame routing, room
reset). No new lifecycle is invented.

### Decision: Strip markup and controls (voseo)

**Choice** (`index.html`, between `</nav>` and `<div class="rule">`; no inline code; no `<iframe>` in
the HTML):

```html
<section id="music" class="music" aria-label="Música compartida" hidden>
  <form id="music-form" class="music-load" autocomplete="off">
    <input id="music-url" class="input" type="url" inputmode="url" maxlength="2048"
      placeholder="Pegá un link de YouTube o YouTube Music" aria-label="Link de YouTube">
    <button id="music-load" type="submit" class="btn">Poner</button>
  </form>
  <div class="music-controls">
    <button id="music-toggle" type="button" class="btn" aria-label="Reproducir para todos" disabled>▶</button>
    <input id="music-seek" type="range" min="0" max="0" step="1" value="0" aria-label="Posición" disabled>
    <span id="music-time" class="music-time">0:00</span>
    <button id="music-mute" type="button" class="btn btn-ghost" aria-label="Silenciar" aria-pressed="false">🔊</button>
    <input id="music-volume" type="range" min="0" max="100" value="100" aria-label="Volumen">
  </div>
  <div id="music-now" class="music-now">No suena nada. Pegá un link y tocá Poner.</div>
  <div id="music-error" class="music-error" role="alert" hidden></div>
  <div id="music-consent" class="music-consent">
    <button id="music-listen" type="button" class="btn btn-accent">Escuchar</button>
    <span>Carga el reproductor de YouTube (youtube-nocookie.com) en esta página.</span>
  </div>
  <div id="music-player" class="music-player" hidden></div>
</section>
```

- **Poner**: `submit` → `preventDefault`, then `parseYouTubeId(value)`. `null` shows "Ese link no
  es de YouTube." in `#music-error`. Otherwise `sendMusic('load', {video})` and clear the input.
- **Toggle**: disabled without `srv` or when offline. The label is ⏸ "Pausar para todos" when
  `srv.playing && !ended`, otherwise ▶ "Reproducir para todos" (or "↺ Repetir" when ended, which
  sends `seek` with `pos: 0`).
- **Seek bar**: `max = floor(duration)`. It's disabled until the player has reported a duration.
  `input` sets `seeking = true` (the tick stops overwriting it). `change` sends
  `seek {pos: Math.round(value*1000)}` and clears `seeking`.
- **Time**: `m:ss` (or `h:mm:ss`) of `min(expectedMs, duration)`, plus ` / m:ss` when the
  duration is known. Shows "Terminó" when ended.
- **Now line**: `youtu.be/<id> · último cambio: <by>`, set with `textContent`.
- **Volume/mute**: local only (`setVolume [v]`, `mute`/`unMute`). Not persisted.
- `sendMusic(action, extra)`: returns false unless the `musica` socket is open and at least
  `MUSIC_SEND_GAP_MS = 300` have passed since the last send (this stays under the server's
  250 ms gate).
- `#music-consent` is shown while `!consent || blocked`. A click sets `consent`, clears `blocked`,
  creates the iframe if there's a video, or posts `playVideo` if ready.

`parseYouTubeId(text)`: `new URL(text.trim())` in `try`. It requires `protocol === 'https:'`, an
empty `port`, `username` and `password`, and then matches (with the hostname lowercased by `URL`):

| hostname | path | id from |
|----------|------|---------|
| `www.youtube.com`, `youtube.com`, `m.youtube.com`, `music.youtube.com` | exactly `/watch` | `searchParams.get('v')` |
| `youtu.be` | `^/([A-Za-z0-9_-]{11})$` | group 1 |
| `www.youtube.com`, `youtube.com`, `m.youtube.com` | `^/shorts/([A-Za-z0-9_-]{11})/?$` | group 1 |

Then `YT_ID.test(id)`. Extra query parameters (`si`, `t`, `list`) are ignored, and `t` is not honoured.

CSS (`app.css`, using only existing tokens): `.music` (flex column, gap), `.music-load`,
`.music-controls` (flex, `#music-seek { flex: 1 }`), `.music-time` (tabular-nums),
`.music-now`, `.music-error` (the same color as `.join-error`), `.music-consent`, and
`.music-player iframe { display:block; width:min(100%,356px); min-width:200px; height:200px; border:0 }`.
`[hidden]` already wins.

**Alternatives considered**: Putting the strip in the panels registry. It's persistent UI, not a
popover, and the exploration ruled that out.
**Rationale**: It reuses `.input`/`.btn` and is mobile-safe below the tabs. Every control is a real
button or input with an accessible name.

## Data Flow

Load and global control:

```
Page A (musica)            readPump A            Room.Run (musica)                 Page A, Page B
 │ Poner → parseYouTubeId
 │ ws.send {"type":"music","action":"load","video":"dQw4w9WgXcQ"}
 │ ─────────────────────▶ parseInbound → inboundMusic
 │                        music gate (250 ms) ─ excess → drop
 │                        broadcast ────────▶ dispatch → music(in)
 │                                            player == nil? → drop
 │                                            apply(load, "A", now) → video, pos 0, playing
 │                                            publish(frame(now)) ──────────────▶ {"type":"music","video":…,"playing":true,"pos":0,"by":"A","ts":…}
 │                                                                              onMusicFrame → srv{…, at: performance.now()}
 │                                                                              renderMusic(); reconcile() → loadVideoById [id, 0]
```

Newcomer, consent, handshake:

```
Page C            Room.Run                          Page C                        youtube-nocookie iframe
 │ connect ──▶ admit: welcome, users, history,
 │             music{pos = position(now)} ─────▶ awaitSnapshot=false, srv=…
 │             publish join(C) ────────────────▶ (own join: snapshot slot closed)
 │                                               strip shows "youtu.be/… · último cambio: A" (no YouTube request yet)
 │ click "Escuchar" ───────────────────────────▶ consent=true; createElement('iframe') ──▶ GET /embed/<id>?…  (Referer: origin only)
 │                                               iframe load → every 250 ms ───────────▶ {"event":"listening","id":1,"channel":"widget"}
 │                                               ◀──────────────────────────────────── {"event":"initialDelivery","info":{…}}
 │                                               ready=true; addEventListener ×3 ──────▶ {"event":"command","func":"addEventListener",…}
 │                                               reconcile: loaded≠video → loadVideoById [id, expectedMs/1000]
 │                                               ◀──────────────────────────────────── infoDelivery {currentTime, playerState, duration}
 │                                               reconcile: |local − expected| > 2 s ? seekTo
```

Room empties, then a newcomer arrives:

```
A leaves ──▶ leave → remove(A): clients empty → player.halt(now): pos = position(now), playing=false
B joins  ──▶ admit: … history, music{playing:false, pos: <pos at A's leave>, by:"A"}
```

Reconnect after a server restart (no track):

```
socket closes → scheduleReconnect (local player keeps going, strip buttons disabled)
welcome → awaitSnapshot = true ; users ; history ; (no music) ; join(me) with awaitSnapshot → srv = null → pauseVideo, strip "No suena nada"
```

## Testing

Hub unit tests (`room_test.go`). `startRoom` becomes a thin wrapper over a new
`runRoom(t, cr *Room)`. New helpers: `startMusicRoom(t)` = `runRoom(t, NewMusicRoom("musica"))`,
`musicCtl(t, cr, from, musicCmd)` (puts `inbound{from, kind: inboundMusic, music}` on broadcast),
and `expectMusic(t, c, video, playing, by) musicFrame`, which uses
`exactFields(t, b, "type","video","playing","pos","by","ts")`.

| Test | Asserts |
|------|---------|
| `TestPlayerApply` | Table on `player` with fixed `now` values: load resets pos and plays. Play and pause anchor `position(now)`. Seek keeps `playing`. Play while playing re-anchors. play/pause/seek with no video → false and the state is unchanged. `halt` freezes the position and keeps `by`. `position` while paused is constant |
| `TestNewRoomDefaults` (+) / `TestNewMusicRoom` | `NewRoom` → `player == nil`. `NewMusicRoom` → empty player, the same defaults otherwise |
| `TestMusicLoadBroadcast` | A and B (the sender included) get the exact shape, `by` = A, `playing`, `0 ≤ pos ≤ elapsed` |
| `TestMusicPauseSeekPlay` | pause → `playing:false`. seek 60000 → `pos == 60000` exactly (paused). play → `playing:true`, `pos ≥ 60000`. Seek while playing stays playing. `by` follows the sender |
| `TestMusicNeedsVideo` | play/pause/seek with no video → `expectEmpty` for everyone (after `syncRoom`) |
| `TestMusicNewcomerSnapshot` | The 4th frame is `music` with `pos` within [elapsed-before-join, elapsed-after], then join. With no video: no `music` frame (the 4th frame is the join) |
| `TestMusicEmptyRoomPauses` | The only client loads, waits 50 ms, unregisters (then `syncRoom`), sleeps 100 ms. A newcomer gets `playing:false` with `pos` in [50 ms, time-until-unregister] |
| `TestMusicLastSlowDropPauses` | The last client removed by `deliver` (full buffer) → paused (seen by the next newcomer) |
| `TestMusicRoomWithoutPlayer` | `NewRoom`: a music control → nothing. A following chat is still relayed. History is unchanged |
| `TestMusicNotInHistory`, `TestMusicFromNonMemberDropped` | Not stored. A non-member's control is ignored |
| `TestMusicSlowClientDropped` | A full-buffer member is removed and announced as leave (must-have, CHAT-03) |

Parse and gate (`client_test.go`):

| Test | Asserts |
|------|---------|
| `TestParseInbound` (+ rows) | Valid load/play/pause/seek (`pos` 0 and 86400000). Each invalid case gives `ok=false`: no action, unknown action, non-string action, load without video, `video` of 10/12 chars, a URL, `"abc\n"`-style trailing newline, non-string video, seek with `pos` missing/`null`/`"5"`/`-1`/`1.5`/`1e3`/86400001/2^63. `{"type":"music"}`. A music frame with a `text` key still isn't chat. `{"type":"MUSIC",…}` → chat fallback (exact keys) |
| `TestRateGate` | The existing typing steps with `rateGate{interval: typingInterval}`, plus a 250 ms table (boundary, discarded events don't move the window) |
| `TestMusicOverWebSocket` | Two clients in a `NewMusicRoom`: load → both get the frame. In a `NewRoom` room, music then chat → only the chat arrives (ordering proves the drop) |
| `TestMusicNewcomerOverWebSocket` | Frame order welcome, users, history, music, join |
| `TestMusicThrottledOverWebSocket` | Load, then 20 plays back to back, then a chat → at most 4 music frames before the chat (1 in practice). The connection stays open |

Wiring and page (`main_test.go`):

| Test | Asserts |
|------|---------|
| `TestIndexSecurityHeaders` | `wantCSP` gains `frame-src https://www.youtube-nocookie.com;` after `connect-src 'self';` and `script-src 'self'` is unchanged. `Referrer-Policy` is still `no-referrer` |
| `TestMusicRoomMirroredInJS` | `slices.Contains(roomIDs, musicRoom)` and app.js contains `const MUSIC_ROOM = 'musica'` (regexp, like `TestImageHostsMirroredInJS`) |
| `TestMusicOnlyInMusica` | Over `newTestServer` with a gorilla dial: load in `musica` → a music frame. Load and then chat in `general` → only the chat |
| `TestIndexHasMusicPlayer` | `id="music"` with `hidden`, `music-url`, `music-load` (Poner), `music-toggle`, `music-seek`, `music-time`, `music-volume`, `music-mute`, `music-now`, `music-error`, `music-listen` with text `Escuchar`, `music-player`. `id="music"` sits between `id="room-tabs"` and `class="rule"`. **No** `<iframe` and no `youtube` in index.html |
| `TestNoYouTubeScript` | app.js contains no `iframe_api`, no `createElement('script')` and no `youtube.com/` script URL. It does contain `youtube-nocookie.com/embed/` |
| existing | `TestIndexHasNoInlineCode` and the others stay green unchanged |

Gates: `/snap/go/current/bin/gofmt -l .`, `go vet ./...`, `go test -race ./...`.

Manual checklist (two browsers, A = Chrome and B = Firefox, both in `musica`, devtools open):

1. B opens `musica` and doesn't press Escuchar. The Network panel shows no request to any
   YouTube host. A presses Escuchar and pastes a `music.youtube.com/watch?v=…&si=…` link, then
   Poner. A plays from 0, with no error 153 (also on `localhost`, Y13). The embed request's
   `Referer` is the origin only.
2. B presses Escuchar and starts within about 2 s of A. Both show "último cambio: A".
3. A pauses, and both pause within 1 s at about the same time. B seeks to 2:00, and both jump. B plays.
4. Clicking inside A's video pauses it locally, and it resumes within about 3 s. A's mute/volume
   don't affect B.
5. A switches to `general`: A's audio pauses and B's continues. A returns and re-syncs.
6. A third tab joins mid-song, presses Escuchar and starts at about the right position.
7. Invalid links (`https://evil.example/watch?v=…`, `youtube.com/watch` with no v, and a playlist URL
   only) show the error and send nothing (WS frames panel). An embed-disabled video shows
   "Este video no se puede reproducir fuera de YouTube."
8. All clients close the tab while playing. Rejoin: the state is paused at the old position.
9. Kill and restart the server while playing. After reconnect the strip says "No suena nada" and the player pauses.
10. Seek-bar spam and play spam: the connection stays open and the state stays consistent.
11. 480 px viewport, both skins: the player is ≥ 200 px tall, nothing overlays it, and the strip wraps.
12. The console shows no CSP violations (SRV-05 scenario).

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/chat/music.go` | Create | `musicInterval`, `maxMusicPos`, action consts, `videoID`, `musicCmd`, `player` (+ `position`, `apply`, `halt`, `frame`) |
| `internal/chat/message.go` | Modify | `typeMusic`, `musicFrame`, `inboundMusic`, `inbound.music`, the `typeMusic` case in `parseInbound`, `jsonMillis` |
| `internal/chat/room.go` | Modify | `Room.player`, `NewMusicRoom`, `dispatch` case, `music(in)`, the snapshot in `admit` (with `now`), `halt` in `remove` |
| `internal/chat/client.go` | Modify | `typingGate` → `rateGate{interval,last}`; a music gate in `readPump` |
| `internal/chat/room_test.go` | Modify | `runRoom`/`startMusicRoom`/`musicCtl`/`expectMusic` and the hub tests above; `TestNewRoomDefaults` (+) |
| `internal/chat/client_test.go` | Modify | `TestParseInbound` rows, `TestRateGate`, WS music tests |
| `cmd/chatroom/main.go` | Modify | `musicRoom` const, `newChatServer` uses `NewMusicRoom`, `buildCSP` adds `frame-src https://www.youtube-nocookie.com` |
| `cmd/chatroom/main_test.go` | Modify | CSP literal, `TestMusicRoomMirroredInJS`, `TestMusicOnlyInMusica`, `TestIndexHasMusicPlayer`, `TestNoYouTubeScript` |
| `cmd/chatroom/index.html` | Modify | `#music` strip between `</nav>` and `.rule` |
| `cmd/chatroom/app.js` | Modify | Constants (`MUSIC_ROOM`, `YT_ORIGIN`, `YT_ID`, timings); a `music` state block (`parseYouTubeId`, `sendMusic`, `onMusicFrame`, `expectedMs`, `renderMusic`, `musicTick`, `createPlayer`, `ytCommand`, `onYTMessage`, `reconcile`, `pauseLocal`, `resetMusic`, `showMusicError`, handlers); hooks in `addRaw`, `activate`, `setStatus`, `teardown` |
| `cmd/chatroom/app.css` | Modify | `.music*` rules, plus a pinkroom-skin tweak only if needed |
| `README.md` | Modify | The `music` inbound/outbound protocol, the `musica` player, CSP `frame-src`, a privacy note |
| `CLAUDE.md` | Modify | Rule: the player room is `musicRoom` (main.go) **and** `MUSIC_ROOM` (app.js), and a test fails if they differ; `music.go` in the structure list |
| `openspec/specs/{chat,server}/spec.md` | Modify at archive | Merge the deltas (CHAT-10/21/30/31, SRV-05/06) |

## Open Risks

- **Undocumented postMessage protocol (Y1–Y4, Y12).** It was verified against today's widget
  script, but YouTube can change it. If it does, the handshake times out and the strip shows "No
  se pudo cargar el reproductor". Chat is unaffected.
- **Safari/iOS autoplay.** A `playVideo` posted into a cross-origin frame may be blocked even after
  "Escuchar". The fallback is the `blocked` → "Escuchar" again path. Only Chrome and Firefox are in
  the checklist.
- **Spec gap: Shorts hosts.** CHAT-31 lists only `https://www.youtube.com/shorts/ID`, but mobile
  share links are `https://youtube.com/shorts/ID?si=…`. The design accepts `youtube.com` and
  `m.youtube.com` for Shorts as well. The spec phase should add those two forms, or the parser
  narrows to the spec.
- **Label wording.** `by` is whoever made the last change (load, play, pause or seek), so the strip reads "último cambio: <by>" (orchestrator decision, replaces "puesto por").
- **No server-side end of track.** `pos` keeps growing past the end while `playing`. Clients clamp
  to the duration and show "Terminó". Anyone can restart it with Repetir (seek 0).
- **Privacy.** After "Escuchar", the viewer's IP and the page origin (no path) reach Google.
  Nothing is sent before that.
- **Anyone can control.** Accepted for a friends chat. Flooding is bounded at 4 per second per
  connection.
- **No JS tests.** The sync, handshake and parser rely on the manual checklist. The Go tests cover
  the protocol and the static guarantees (CSP, no script, DOM presence).
