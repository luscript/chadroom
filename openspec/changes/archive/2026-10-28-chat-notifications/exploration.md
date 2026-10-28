# Exploration: chat-notifications

Source: 1 crombie-explore agent (frontend + static serving), 2026-10-28.

## Key findings

- `addRaw(room, data)` (app.js:476) is the single inbound dispatcher. `history`, `music`,
  `users`, `dm_error` return early, so history never reaches the `chat`/`dm` cases.
- `chat` case (app.js:526-532): own = `msg.from === room.myName`; already calls
  `bumpUnread(room)` when `room !== current()` and not own. `dm` case (app.js:533-544): `own`
  computed; `bumpUnread(conv)` when `conv !== current() && !own`. These are the hook points.
- Visible pane: `active` / `activeConv`, `current = () => activeConv || active` (app.js:36-40);
  `activate(room, conv)` (290) is the single switch hook.
- One `visibilitychange` listener (app.js:443) runs `reconnectAll` on visible.
- localStorage key convention `chadroom.<name>` (`AVATAR_KEY = 'chadroom.avatar'`), best effort.
- Header actions in index.html:50-65 (`#users-btn`, `#me`, `#leave-btn`); `#music-mute`
  (index.html:82) is a precedent for an emoji toggle with `aria-pressed`. `.tool` styles key on
  `aria-expanded`, so a pressed-state rule is needed. `<title>★chadroom★</title>`, no icon link.
- main.go: `//go:embed` per asset + `serveAsset(ct, bytes)` routes; CSP `img-src 'self' …`
  already allows same-origin favicons. Web Audio needs no CSP directive.
- main_test.go: `TestStaticAssets` (105) is a table — add the two SVG rows. No test enumerates
  all routes/embedded files.

## Risks

- Autoplay policy: `AudioContext` must be created/resumed on a user gesture (join submit, mute
  click) or the first sound is silent.
- Title counter needs its own counter: `bumpUnread` ignores the current pane.
- Reset title/favicon on visible and on leave.
- Favicon swap: two distinct URLs (Safari unreliable — acceptable).
