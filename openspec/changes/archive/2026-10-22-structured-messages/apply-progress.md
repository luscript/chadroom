# Apply progress: structured-messages

## Batch 1 — 2026-10-22

| Worker | Tasks | Status | Verification |
|--------|-------|--------|--------------|
| W1 | 1.1, 1.2, 2.1–2.4, 4.1–4.4 | SUCCESS | `gofmt -l`, `go vet ./...`, `go test -race -count=5 ./internal/chat/...` clean |
| W2 | 3.1–3.3 | SUCCESS | `node --check` on page script; build checked by orchestrator after W1 |

Files: `internal/chat/{message,nickname,room,client,room_test,client_test}.go`,
`cmd/chatroom/index.html`, `README.md`.

Orchestrator check: `go vet ./...` + `go build ./...` clean; `go test -race ./internal/...` ok;
`cmd/chatroom` `TestRunGracefulShutdown` fails as expected (first frame is now `welcome`) → task 4.6.

Deviations from design (accepted):
- `Run` register case encodes `welcome` and `join` up front and skips delivery if either is nil.
- Test helpers: `dial` returns `(conn, assignedName)` after reading `welcome`; `startReader(t, …)`
  yields decoded `message`s; `syncRoom` (room_test.go) broadcasts a marker so slow-drop tests
  don't race the join fan-out.
- `TestSameNicknameIndependent` now expects the second `bob` as `bob (2)`.
- Page: `#me` header tag shows the requested name until `welcome` arrives; non-finite `ts`
  falls back to the local clock; frames without a string `from` are ignored.

## Batch 2 — 2026-10-22

| Worker | Tasks | Status | Verification |
|--------|-------|--------|--------------|
| W3 | 4.5 | SUCCESS | `go test -race -count=3 ./internal/chat/...` clean; no production bugs found |
| W4 | 4.6 | SUCCESS | `go test -race -count=3 ./cmd/...` clean |
| orchestrator | 5.1 | done | project map re-stamped at efcdb26 (chat module, web client, `/ws` api_surface) |

Orchestrator check: `gofmt -l .` empty, `go vet ./...` clean, `go test -race -count=2 ./...` ok.

## Pending

- 4.7 done: automated checks plus manual two-tab browser check by the developer (2026-10-22).

## Verify round 2 corrections — 2026-10-22

| Executor | Tasks | Status |
|----------|-------|--------|
| crombie-worker | 6.1–6.5 | PARTIAL — added `relay`/`drainBroadcast` helpers but did not wire them into `Run`; its regression test passed without the fix (Run idle → broadcast always taken first) |
| orchestrator | 6.1, 6.2 completion | Extracted `leave(c)` and wired `relay`; rewrote the regression test to drive `leave` directly (fails without fix); found and fixed a double close (drain can slow-drop `c`) with `TestLeaveAfterDrainDroppedClient` (fails without fix) |

Checks: `gofmt -l .` empty (real binary), `go vet ./...` clean, `go test -race -count=20 ./...` ok.

