# Design: Chat notifications

## Technical Approach

Client-only. One `notify(paneIsCurrent)` call sits next to the existing `bumpUnread` calls in the
`chat` and `dm` cases of `addRaw` (app.js:526-544). Those cases are only reached by live frames,
and `history` returns early (app.js:494), so history never notifies. `notify` does two things:
(a) while unattended (tab hidden or window without focus), it increments a tab counter that drives the title and the favicon;
(b) when unattended or the pane is not the current one, it asks `playBlip()` for a sound.
`playBlip` checks mute and the 1 s coalescing window. The server embeds two SVG favicons and
serves them through the existing `serveAsset`. CSP, protocol and `internal/chat` stay unchanged.

## Architecture Decisions

### Decision: Hook at the `chat`/`dm` call sites, not in `addChat`
**Choice**: call `notify(...)` right after the existing `bumpUnread` lines, with the same
"not own" filter.
**Alternatives considered**: hooking `addChat` (also called by `renderHistory`, so a reconnect
would replay the sound for old messages); a generic event bus (YAGNI).
**Rationale**: the own/peer logic is already computed at these call sites (`msg.from !== room.myName`,
`own`). Reusing it puts every notification rule in one obvious place.

### Decision: Separate tab counter (`unreadCount`) from pane badges
**Choice**: a module-level `let unreadCount = 0` that counts only while unattended (hidden or unfocused), current pane included.
**Alternatives considered**: summing pane badges. `bumpUnread` skips the current pane, and badges
outlive visibility changes.
**Rationale**: the counter's semantics ("arrived while you were away") are different from the
badges, which stay unchanged per the proposal.

### Decision: Favicon swap between two same-origin SVG URLs
**Choice**: `/favicon.svg` and `/favicon-unread.svg`, embedded and served like `app.css`. JS
swaps the `href` of the single `<link rel="icon">`.
**Alternatives considered**: a `data:` URI or canvas-drawn icon, which would need `img-src data:`
(weakens CSP) and more code; a `.ico` (binary, and `img-src 'self'` covers SVG equally).
**Rationale**: no CSP change and no new code paths on the server. Safari may ignore the swap,
which is acceptable because the title still updates.

### Decision: Web Audio blip, context unlocked on gestures
**Choice**: create the `AudioContext` lazily in `unlockAudio()`, called from the join submit and
from the mute toggle (when unmuting). Every message produces a fresh oscillator plus a gain
envelope. Each Web Audio call is wrapped in try/catch so failures are silent.
**Alternatives considered**: an `<audio>` file, which needs `media-src` and an extra asset;
creating the context on page load, which starts suspended under autoplay policy.
**Rationale**: no CSP or asset impact. Entering the chat always goes through the join submit
(a user gesture), so the context is running before the first message arrives.

### Decision: Mute button mirrors `#music-mute`
**Choice**: `<button id="mute-btn" aria-pressed>`. The `aria-label` stays fixed ("Silenciar
avisos") and the glyph switches between 🔔 and 🔕. State is stored in `localStorage['chadroom.muted'] = '1'`
or the key is removed, best effort like `AVATAR_KEY`.
**Alternatives considered**: toggling the aria-label text instead of aria-pressed, which
announces the state twice; a per-room mute (out of scope).
**Rationale**: this is the same pattern as the existing music mute, so the codebase stays consistent.

## Data Flow

    WebSocket frame ─→ addRaw ─→ case chat/dm (not own)
                                   ├─ bumpUnread(pane)         (unchanged; pane ≠ current)
                                   └─ notify(pane === current())
                                         ├─ unattended → unreadCount++ → renderTabIndicator()
                                         │                            ├─ document.title = "(N) ★chadroom★"
                                         │                            └─ link[rel=icon].href = /favicon-unread.svg
                                         └─ unattended || !paneIsCurrent → playBlip() (skip if muted / <1 s)
    visibilitychange(visible) ─→ resetTabIndicator() + reconnectAll()
    #leave-btn click          ─→ resetTabIndicator()

## app.js

New constants (next to `AVATAR_KEY`, line 5):

    const MUTED_KEY = 'chadroom.muted'; // localStorage; best effort
    const BLIP_GAP_MS = 1000; // at most one notification sound per second
    const FAVICON = '/favicon.svg'; // served by cmd/chatroom/main.go
    const FAVICON_UNREAD = '/favicon-unread.svg';

New section `// — notifications —`, placed just before `// — messages —` (line 473). Function
declarations are hoisted, and the `let`s are initialized before any socket exists.

    const baseTitle = document.title;                      // captured at load
    const faviconLink = document.querySelector('link[rel="icon"]');
    let unreadCount = 0;   // notifying messages while unattended
    let soundMuted = false;
    try { soundMuted = localStorage.getItem(MUTED_KEY) === '1'; } catch { /* blocked: unmuted */ }
    let audioCtx = null;
    let lastBlipAt = -Infinity;
    renderMute();

    function notify(paneIsCurrent) {
      if (unattended()) { unreadCount++; renderTabIndicator(); }
      if (unattended() || !paneIsCurrent) playBlip();
    }

    function renderTabIndicator() {
      document.title = unreadCount ? '(' + unreadCount + ') ' + baseTitle : baseTitle;
      const href = unreadCount ? FAVICON_UNREAD : FAVICON;
      if (faviconLink && faviconLink.getAttribute('href') !== href) faviconLink.setAttribute('href', href);
    }
    function resetTabIndicator() { unreadCount = 0; renderTabIndicator(); }

    // Must run inside a user gesture (join submit, unmute click) so autoplay policy allows it.
    function unlockAudio() {
      try {
        if (!audioCtx && window.AudioContext) audioCtx = new AudioContext();
        if (audioCtx && audioCtx.state === 'suspended') audioCtx.resume().catch(() => {});
      } catch { audioCtx = null; }
    }

    function playBlip() {
      if (soundMuted || !audioCtx) return;
      const now = performance.now();
      if (now - lastBlipAt < BLIP_GAP_MS) return;          // coalesce bursts
      lastBlipAt = now;
      const scheduleTone = () => {                          // reads currentTime only once the clock is live
        try {
          const t = audioCtx.currentTime + BLIP_LEAD_S;     // 0.12 s silent lead-in: idle OS sinks drop the first ~100-300 ms
          // oscillator 880→1320 Hz, gain 0.0001 from currentTime, envelope relative to t, osc.stop(t + 0.16)
          // (tone ≤ 200 ms; lead-in silence doesn't count); osc.onended disconnects gain
        } catch { /* no sound: fail silently */ }
      };
      try {
        if (audioCtx.state === 'running') scheduleTone();
        else audioCtx.resume().then(scheduleTone, () => {}); // first sound after idle is no longer lost
      } catch { /* no sound: fail silently */ }
    }

    function renderMute() {
      $('mute-btn').setAttribute('aria-pressed', String(soundMuted));
      $('mute-btn').textContent = soundMuted ? '🔕' : '🔔';
    }
    $('mute-btn').addEventListener('click', () => {
      soundMuted = !soundMuted;
      renderMute();
      if (!soundMuted) unlockAudio();
      try {
        if (soundMuted) localStorage.setItem(MUTED_KEY, '1'); else localStorage.removeItem(MUTED_KEY);
      } catch { /* storage blocked: the choice just isn't remembered */ }
    });

Hooks into existing code:

| Where | Change |
|-------|--------|
| join submit (line 349) | add `unlockAudio();` right after `if (!nick \|\| joining) return;`, before any `await`, so it runs in the same gesture |
| `visibilitychange` (line 443) | `if (!unattended()) resetTabIndicator(); if (!document.hidden) reconnectAll();`; plus `window` `focus` -> `resetTabIndicator` |
| `#leave-btn` click (line 445) | add `resetTabIndicator();` |
| `case 'chat'` (line 530) | after the `bumpUnread` line: `if (msg.from !== room.myName) notify(room === current());` |
| `case 'dm'` (line 542) | after the `bumpUnread` line: `if (!own) notify(conv === current());` |

Leaving the chat does not close the `AudioContext`, so it is reused if the user rejoins. Mute
is not reset on leave.

## index.html

- In `<head>`, after the stylesheet link: `<link rel="icon" type="image/svg+xml" href="/favicon.svg">`
- In `.chat-header .actions`, between `#me` and `#leave-btn`:
  `<button id="mute-btn" type="button" class="btn btn-ghost tool tool-mute" aria-pressed="false" aria-label="Silenciar avisos" title="Silenciar avisos">🔔</button>`

No inline code; `TestIndexHasNoInlineCode` keeps passing.

## app.css

Append at the end of the file so these rules come after the retro skin `.btn` rules (lines 245+):

    .tool-mute{height:32px;padding:0 8px;font-size:15px;line-height:1}
    .tool-mute[aria-pressed="true"]{border-style:inset;background:var(--color-accent-900);box-shadow:none}

`.tool` already sets `flex:none`. At ≤800px the header hides `#me` (`.chat-header .tag`), so
the bell sits next to "Salir" and needs no extra mobile rule. The emoji glyph carries the state;
the inset border is the visual pressed cue, the same as `.btn:active`.

## SVG files

`cmd/chatroom/favicon.svg`: a pink retro chat bubble with a star, in skin colors (`#f4d5e7` fill,
`#b71967` accent). No scripts, no external references, no `href`.

    <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><path d="M5 4h22a3 3 0 0 1 3 3v14a3 3 0 0 1-3 3H13l-7 6v-6H5a3 3 0 0 1-3-3V7a3 3 0 0 1 3-3z" fill="#f4d5e7" stroke="#b71967" stroke-width="2" stroke-linejoin="round"/><path d="M16 7l1.8 4.6 4.9.2-3.8 3.1 1.3 4.8L16 17l-4.1 2.7 1.3-4.8-3.8-3.1 4.9-.2z" fill="#b71967"/></svg>

`cmd/chatroom/favicon-unread.svg`: the same two paths plus a dot at the top-right corner (the
white ring keeps it readable on the pink bubble):

    <circle cx="25" cy="7" r="6" fill="#ff2d6f" stroke="#fff" stroke-width="2"/>

## main.go

    //go:embed favicon.svg
    var faviconSVG []byte

    //go:embed favicon-unread.svg
    var faviconUnreadSVG []byte

In `newRouter`, after `/app.css`:

    r.GET("/favicon.svg", serveAsset("image/svg+xml", faviconSVG))
    r.GET("/favicon-unread.svg", serveAsset("image/svg+xml", faviconUnreadSVG))

`serveAsset` already sets `nosniff` and `no-cache`. The SVGs contain no script, so opening them
directly is inert (a test guards this).

## main_test.go

- `TestStaticAssets`: add rows `{"/favicon.svg", "image/svg+xml", faviconSVG}` and
  `{"/favicon-unread.svg", "image/svg+xml", faviconUnreadSVG}`.
- New `TestIndexNotificationMarkup`:
  - `indexHTML` contains `<link rel="icon" type="image/svg+xml" href="/favicon.svg">`.
  - regexp `<button id="mute-btn"[^>]*aria-pressed="false"` matches `indexHTML`.
  - `appJS` contains `'/favicon.svg'`, `'/favicon-unread.svg'` and `'chadroom.muted'`. This is
    the coherence check between routes and JS, the same idea as the mirrored image hosts test.
  - For both SVGs: the lowercased body contains none of `<script`, `href`, ` on` (event
    attributes) or `<foreignobject`.

Run `gofmt` and `go test -race ./...`.

## Manual test checklist (browser A = Ana, browser B = Beto, both on `general`)

1. A joins and B joins. B sends in `general` while A has the tab visible on `general`: no sound, title unchanged.
2. A switches to `juegos`, B sends in `general`: A hears one blip and the `general` badge bumps.
3. B sends 5 messages quickly: A hears one blip per second at most.
4. A sends a message: A hears no sound for their own echo.
5. A hides the tab (switches to another tab), B sends 3 messages: A hears blips, the title shows `(3) ★chadroom★` and the favicon shows the dot.
6. A comes back to the tab: the title goes back to `★chadroom★` and the favicon loses the dot.
7. B opens a DM to A while A is on `general`: blip plus DM badge. With the DM open and visible: silent.
8. A clicks 🔔: it becomes 🔕 and looks pressed. Repeat steps 2 and 5: no sound, but the title and favicon still update.
9. A reloads the page: the button stays 🔕 (localStorage). Clicking it again gives 🔔 and sound works without a reload.
10b. Browser visible but focus in another app: B writes -> A hears the sound and sees (N); clicking back on A's window resets title and favicon.
10. With the tab hidden and the counter above zero, A clicks Salir (keyboard focus) or leaves and rejoins: the title and favicon are reset.
11. B leaves or joins `general`, starts typing, or loads music in `musica`: A gets no sound.
12. A reconnects (network off then on): replayed history makes no sound.
13. DevTools console shows no CSP violations, and `/favicon.svg` is served as `image/svg+xml`.
14. On a mobile width (≤560px), the bell is visible and tappable next to "Salir".

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `cmd/chatroom/favicon.svg` | Create | Base tab icon (pink bubble + star) |
| `cmd/chatroom/favicon-unread.svg` | Create | Same icon with an unread dot |
| `cmd/chatroom/app.js` | Modify | Constants, notifications section, five hook lines |
| `cmd/chatroom/index.html` | Modify | `<link rel="icon">`, `#mute-btn` in header actions |
| `cmd/chatroom/app.css` | Modify | `.tool-mute` sizing and pressed style |
| `cmd/chatroom/main.go` | Modify | Two `//go:embed` vars and two routes |
| `cmd/chatroom/main_test.go` | Modify | `TestStaticAssets` rows, `TestIndexNotificationMarkup` |
