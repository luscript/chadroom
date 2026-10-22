# Apply Progress: fix-channel-handling

## Batch 1 — crombie-worker (W1) — SUCCESS
Tasks: 1.1, 1.2, 1.3, 1.4, 1.5, 1.6, 3.1
Files: internal/chat/room.go (modified), internal/chat/client.go (modified), internal/chat/room_test.go (created)
Verification: `go build ./internal/... && go vet ./internal/... && go test -race -count=3 ./internal/...` → ok
Deviations: none. checkOrigin table test adds cases (case-insensitive host, exact-match allowlist, no host).
Issues: package main does not compile until batch 2 (expected: NewRoom/Run signature change).

## Batch 2 — 2 crombie-workers in parallel — SUCCESS
### W2 — Tasks: 3.2
Files: internal/chat/client_test.go (created)
Verification: `go vet ./internal/... && go test -race -count=5 ./internal/...` → ok (also -count=20 once)
Notes: goroutine-leak test uses +2 tolerance and a 50ms setup sleep before baseline (documented in code). No implementation bugs found.
### W3 — Tasks: 2.1, 2.2, 2.3, 2.4, 3.3
Files: index.html → cmd/chatroom/index.html (moved), cmd/chatroom/main.go (modified), cmd/chatroom/main_test.go (created)
Verification: `go build ./... && go vet ./cmd/... && go test -race -count=3 ./cmd/...` rc=0; gofmt clean.
Deviations: plain `mv` instead of `git mv` (index.html was untracked). main split into `run() int` for exit codes.
Issues: worker saw no test output line despite rc=0 → re-confirm in verify.

## Batch 3 (verify round 1 corrections) — 2 crombie-workers in parallel — SUCCESS
### W4 — Tasks: 5.1, 5.2, 5.3, 5.4
Files: internal/chat/room.go, client.go, room_test.go, client_test.go (modified)
Verification: `go vet ./internal/... && go test -race -count=10 ./internal/...` → ok (29.9s); gofmt clean.
Notes: normalizeNickname + maxNicknameRunes=32; closeWith via WriteControl (1003/1007); logError with %q nickname (log-injection safe); CHAT-05 conn-level test uses a test-only /slow handler with 1-slot buffer. Two tests keep a sleep to let time pass (assertions are receives).
### W5 — Tasks: 5.5, 5.6, 5.7
Files: cmd/chatroom/main.go, main_test.go (modified)
Verification: `go build ./... && go vet ./cmd/... && go test -race -count=5 ./cmd/...` → ok; gofmt clean.
Deviation: run() derives its own cancelable ctx and cancels it before waiting on the room, so a Serve error with a live parent ctx cannot hang on roomDone.
