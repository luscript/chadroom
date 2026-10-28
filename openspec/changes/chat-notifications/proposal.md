# Proposal: Chat notifications

## Intent

Let people notice new messages when they are not looking: a short sound for messages from
other people, a global way to mute it, and an unread indicator on the browser tab (favicon +
title counter).

## Decisions

Orchestrator decisions (auto mode, 2026-10-28; confirmed with the user):

- **What notifies**: only `chat` (room) and `dm` frames from **another person**. Never own
  messages (echo), `history`, join/leave, typing, music, system notices or `dm_error`.
- **When the sound plays**: the tab is hidden (`document.hidden`) **or** the message lands in a
  pane (room or DM conversation) that is not the current one. A message in the current pane of
  a visible tab is silent.
- **Sound**: synthesized with the Web Audio API (short "blip", ≤ 200 ms, low volume). No audio
  file, no `media-src`. The `AudioContext` is created lazily and resumed on a user gesture
  (join, mute toggle) to satisfy autoplay policies; if it cannot run, notifications fail
  silently. Bursts are coalesced: at most one sound per ~1 s.
- **Mute**: one global toggle button 🔔/🔕 in the chat header (`aria-pressed`), applying to every
  room and DM. Persisted in `localStorage` key `chadroom.muted` (best effort, like the avatar).
  It mutes **only the sound**; the tab indicator keeps working.
- **Tab indicator**: while the tab is hidden, each notifying message (same filter as the sound,
  regardless of mute, including the current pane) increments a counter; the title becomes
  `(N) ★chadroom★` and the favicon swaps from `/favicon.svg` to `/favicon-unread.svg` (same
  icon with a dot). Becoming visible again — or leaving the chat — restores both and resets N.
  Pane unread badges keep their current behavior (unchanged).
- **Server**: serves the two embedded SVGs with `image/svg+xml` and the usual static headers;
  the page gets `<link rel="icon">`. CSP unchanged (`img-src 'self'` already covers it).

## Scope

In: app.js, index.html, app.css, two new SVG files, main.go routes/embeds, main_test.go.
Out: browser Notification API / permissions, per-room mute, volume control, sound choice,
changes to the WebSocket protocol or `internal/chat`.

## Affected Areas

| Area | Change |
|------|--------|
| `cmd/chatroom/app.js` | notify hook in `chat`/`dm`, Web Audio blip, mute state, title/favicon counter |
| `cmd/chatroom/index.html` + `app.css` | icon link, mute button and its pressed style |
| `cmd/chatroom/main.go` + tests | embed and serve `/favicon.svg`, `/favicon-unread.svg` |

## Risks

- Autoplay: first message before any gesture could be silent — mitigated by resuming on join.
- Safari may not repaint a swapped favicon; the title counter still works.
