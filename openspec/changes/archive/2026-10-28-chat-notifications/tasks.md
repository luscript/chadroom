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
- [x] T302 Manual two-browser checklist (design.md) — deferred to the user if not runnable — user confirmed sound (other computer, unfocused window, first message after a quiet period)

## Fix round 2 (user report: no sound for messages from another computer)

- [x] T911 `app.js`: "unattended" = `document.hidden || !document.hasFocus()`; use it in `notify` for the sound and the tab counter; reset the indicator on window `focus` (and keep visibilitychange → visible+focused); spec CHAT-32/CHAT-33 updated

## Fix round 3 (user report: first sound after a quiet period is lost)

- [x] T912 `playBlip`: if the context is not `running`, resume and play once the resume resolves (instead of dropping); start the tone after a ~120 ms silent lead-in so an idle audio sink (PipeWire/Pulse suspend-on-idle, Bluetooth) can wake without swallowing it
