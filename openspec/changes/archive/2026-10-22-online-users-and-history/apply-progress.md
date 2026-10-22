# Apply Progress: online-users-and-history

## Batch 1 (parallel, 2 workers) — 2026-10-22

### Worker A — Go hub (tasks 1.1, 2.1–2.5, 3.1–3.5) — SUCCESS
Files: `internal/chat/message.go`, `internal/chat/room.go`, `internal/chat/room_test.go`,
`internal/chat/client_test.go`.
Checks: `gofmt -l internal` clean; `go vet ./...` clean; `go test -race -count=5 ./internal/...`
and `go test -race -count=3 ./...` pass (112 PASS lines incl. subtests, 0 FAIL).
Deviations (test code only, production code per design):
1. `expectWelcome` also asserts welcome/users/history share one ts.
2. New `expectEmpty(t, c)` helper replaces inline empty-buffer selects.
3. `TestNewcomerDroppedDuringJoinIsSilent`: sync broadcast from obs happens before
   `expectClosed(n)` — the design's order let `expectClosed` drain the buffer while Run was
   still delivering, so the history frame fit and the test always failed.

### Worker B — page + docs (tasks 4.1–4.4, 5.1–5.3) — SUCCESS
Files: `cmd/chatroom/index.html`, `cmd/chatroom/app.css`, `cmd/chatroom/app.js`,
`cmd/chatroom/main_test.go`, `README.md`.
Checks: `go vet ./cmd/...`, `go test -race ./cmd/...`, `node --check cmd/chatroom/app.js` pass.
Deviations: none. Note: `renderUsers` isn't re-run on `welcome` (harmless — `users` always
follows `welcome`).

## Pending
- Manual browser checklist (no JS harness): reconnect shows history w/o duplicates and
  "Reconectado" last; count/list follow join/leave/slow drop; "vos" on own name; popover
  Escape/outside click; ≤560px header layout.

## Verify round 1 fixes — 2026-10-22 — SUCCESS
Files: `internal/chat/room_test.go`, `internal/chat/client_test.go`, `cmd/chatroom/app.js`.
- V1 (R-M1): later `users` snapshots assert dropped clients are absent.
- V2 (R-M2): `TestJoinSequence` asserts replayed history keeps the original `ts`.
- V3 (R-M3): `privateFrames = 3` const; slow-buffer sizes expressed in terms of it (same values).
- V4 (R-L2): `renderHistory` clears `aria-busy` in `finally`.
Checks: gofmt clean, `go vet ./...` clean, `go test -race -count=3 ./...` pass, `node --check` ok.
