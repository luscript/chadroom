# Apply progress: chat-notifications

## Batch 1 (2 parallel workers)

- Worker A — T101, T102, T103: `favicon.svg`, `favicon-unread.svg`, `main.go` (embeds + routes),
  `main_test.go` (`TestStaticAssets` rows, `TestIndexNotificationMarkup`).
- Worker B — T201–T204: `index.html` (icon link, `#mute-btn`), `app.css` (`.tool-mute`),
  `app.js` (notifications section; hooks in `chat`/`dm`, join, visibilitychange, leave).

Worker A's markup test failed only while B was in flight; B reported `go test ./cmd/...` green
after landing. Deviations from design: none reported.

Pending: T301 (gates), T302 (manual checklist).

## Fix round (1 worker)

- T901, T902 in `app.js` and `main_test.go`. gofmt/vet/`go test -race ./...`/`node --check` green.

Pending: T302 (manual checklist, user).

## Fix round 2 (user report)

- T911: "unattended" = hidden or window unfocused (Linux rarely reports occlusion, so a message from another computer was silent while the user was in another app). Indicator resets on window focus. Gates green.

## Fix round 3 (user report)

- T912: first sound after a quiet period was lost. A suspended context is resumed and the tone scheduled once it resolves (no longer dropped); 120 ms silent lead-in lets an idle audio sink wake. Gates green.
