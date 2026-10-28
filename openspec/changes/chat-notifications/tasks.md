# Tasks: chat-notifications

Specs: CHAT-32, CHAT-33, SRV-02 (delta). Design: `design.md`.

## Batch 1 — server & assets (worker A)

- [x] T101 Create `cmd/chatroom/favicon.svg` and `cmd/chatroom/favicon-unread.svg` per design (no script/href/handlers)
- [x] T102 `main.go`: `//go:embed` vars `faviconSVG`, `faviconUnreadSVG`; routes `GET /favicon.svg`, `GET /favicon-unread.svg` via `serveAsset("image/svg+xml", …)`
- [x] T103 `main_test.go`: two `TestStaticAssets` rows; `TestIndexNotificationMarkup` (icon link, `#mute-btn` with `aria-pressed`, app.js references favicon paths + `chadroom.muted`, SVGs free of `<script`/`href`/`on…`/`<foreignObject`)

## Batch 1 — page (worker B)

- [x] T201 `index.html`: `<link rel="icon" type="image/svg+xml" href="/favicon.svg">`; `#mute-btn` (🔔, `aria-pressed="false"`, fixed `aria-label`) in header actions
- [x] T202 `app.css`: `.tool-mute` + pressed style at the end of the file
- [x] T203 `app.js`: constants, notifications section (`unlockAudio`, `playBlip` with ≤1 s gap, mute init/toggle persisted in `chadroom.muted`, `notify(paneIsCurrent)`, tab counter + title/favicon set/reset)
- [x] T204 `app.js` hooks: `chat`/`dm` cases (not own, not history), join submit and mute click → `unlockAudio`, `visibilitychange` visible → reset, leave → reset

## Fix round (from verify)

- [x] T901 `playBlip`: skip (and resume) unless the context is `running`; stamp the gap only after scheduling; disconnect gain on end
- [x] T902 `TestIndexNotificationMarkup`: event-attribute regexp, `t.Run` per SVG, assert hook calls

## Batch 2 — verify

- [x] T301 `gofmt -l .`, `go vet ./...`, `go test -race ./...` green
- [ ] T302 Manual two-browser checklist (design.md) — deferred to the user if not runnable
