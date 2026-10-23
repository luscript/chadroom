# Proposal: Profile picture

## Intent

Let each user pick a profile picture when joining. It is shown next to their messages (rooms,
DMs, history) and in the online list. This is the `profile-picture` backlog group.

## Decisions

Orchestrator decisions (auto mode, 2026-10-23):

- **Source: an image URL on the existing allowlist.** Same rules as inline images
  (`isAllowedImage`): https, exact host in `imageHosts`/`IMAGE_HOSTS` (Giphy, Tenor, Imgur),
  no userinfo, port or fragment, path ending in `.gif|.webp|.png|.jpg|.jpeg`, at most 512 bytes.
  **No file upload** (would need storage, size/type checks and rate limiting) and **no new
  hosts** (CSP `img-src` and the Go/JS host lists stay unchanged).
- **Transport: `?avatar=<url>` on `GET /ws`**, next to `nickname`. The page sends it on every
  room socket and every reconnect. It is fixed for the life of the connection (YAGNI: no
  mid-session change, no new inbound frame type). To change it the user leaves and joins again.
- **Server-side validation.** The server re-validates the URL. An invalid or oversized value is
  **silently dropped** (the user joins without a picture), mirroring nickname normalization; the
  connection is never rejected for it. The validator is owned by `package main` (where
  `imageHosts` lives) and handed to the chat package, which stays URL-agnostic.
- **Wire format (additive, `omitempty`).**
  - `welcome`, `join` and `chat` frames (including history entries) gain `"avatar":"<url>"`
    when the sender has one. History keeps the avatar the message was sent with (fixed per
    connection anyway).
  - `users` frame gains `"avatars":{"<nick>":"<url>"}` with entries only for users that have one.
  - `leave`, `dm`, `dm_error`, `typing` are unchanged; the page resolves DM avatars from the
    room's online map (DMs only go to online users).
  Clients that ignore the new fields keep working.
- **Page.**
  - Join form gets an optional "Foto de perfil (URL)" field, checked with `isAllowedImage`
    before connecting (inline error if invalid). The last value is remembered in `localStorage`
    (best-effort; failures ignored). It is a convenience, not a security boundary.
  - The letter avatar in `addChat` becomes an `<img>` when the author has a valid avatar
    (re-checked with `isAllowedImage`), with `referrerPolicy=no-referrer`, `alt=""`, and a
    fallback to the letter on load error (listener, not an inline attribute).
  - The online list shows a small avatar before each name. The "MI PERFIL" aside shows the
    user's own picture in `.profile-art`.
  - Rendering only via `img.src` / `textContent`; never `innerHTML` or CSS `url()`.

## Affected Areas

| Area | Files | Change |
|------|-------|--------|
| Chat hub | `internal/chat/{client,server,room,message}.go` | avatar on Client, query param, frames |
| Server wiring | `cmd/chatroom/main.go` | avatar validator over `imageHosts`, passed to the chat server |
| Page | `cmd/chatroom/index.html`, `app.js`, `app.css` | join field, message/online-list/profile avatars |
| Tests | `internal/chat/*_test.go`, `cmd/chatroom/main_test.go` | frames, validation, DOM presence |
| Docs / specs | `README.md`, `openspec/specs/{chat,server}/spec.md` | protocol fields, new requirements |

## Out of scope

- Uploads, a server-side image proxy (viewer IPs still reach the image host, as with GIFs),
  new image hosts, changing the picture mid-session, avatars on DM tabs, persistence beyond the
  browser's `localStorage`.

## Risks

- Avatars are fetched automatically by every viewer (IP leak to Giphy/Tenor/Imgur), whereas
  inline GIFs are already fetched automatically too; same trusted hosts, `no-referrer` kept.
- Exact-shape tests for CHAT-17/18 must be updated.

## Rollback

Revert the change's commit(s). No persisted server data; the `localStorage` key is harmless if
left behind.
