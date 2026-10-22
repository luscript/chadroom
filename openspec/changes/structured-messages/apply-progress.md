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

## Batch 2 — pending (4.5, 4.6)
