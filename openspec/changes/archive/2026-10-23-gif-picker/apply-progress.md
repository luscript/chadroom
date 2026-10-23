# Apply Progress: gif-picker

## Batch 1 (2026-10-23) — two parallel workers

| Group | Tasks | Status |
|-------|-------|--------|
| A (server) | 1.1, 1.2, 2.1, 2.5, 3.1, 3.2, 3.3 | SUCCESS |
| B (page)   | 2.2, 2.3, 2.4 | SUCCESS |

### Files changed
- Created: `cmd/chatroom/gifs.go`, `cmd/chatroom/gifs_test.go`
- Modified: `cmd/chatroom/main.go`, `cmd/chatroom/main_test.go`, `cmd/chatroom/index.html`,
  `cmd/chatroom/app.js`, `cmd/chatroom/app.css`, `README.md`

### Gates (worker A)
`gofmt -l .` empty; `go vet ./...` clean; `go test -race ./...` green (both packages),
including `TestIndexHasGifPicker` against worker B's `index.html`. `node --check app.js` OK (worker B).

### Deviations
- Page: the error text has no trailing period ("No se pudieron cargar los GIF"), matching the
  proposal. Added an `Array.isArray(body.gifs)` guard, so a bad body shows the error state.
- Page: the panel closes only if `sendText` returns true.
- Server: `canonicalGIF` accepts an `HTTPS://` scheme and a `.GIF` suffix, and rejects
  uppercase hosts (exact `imageHosts` match), per the design.

### Issues
None. Browser checklist (design §Testing) not yet run.

## Batch 2 (2026-10-23) — verify-round fixes, one worker

Fixes F1–F8 from `verify-report.md`: no redirects on the Giphy client, Giphy-only hosts in
`canonicalGIF`, empty results not cached, `strconv.Itoa`, stronger `TestGifsKeyNeverLeaks`,
`requireJSONHeaders` on 200/400/502, `gifPending` in-flight guard, IME Enter guard.
Files: `gifs.go`, `gifs_test.go`, `app.js`. Gates green (gofmt, vet, `go test -race`, `node --check`).
Spec delta SRV-08 clauses 4 and 6 updated to match (no redirects; Giphy hosts only).
