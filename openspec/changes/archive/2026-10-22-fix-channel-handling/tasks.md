# Tasks: Fix channel handling, connection robustness and origin check

> Spec ↔ design reconciled, no divergences. Coverage: CHAT-01..05 → Run/fanOut/registry
> decisions; CHAT-06/07 → pump decisions; CHAT-08 → shutdown propagation; SRV-01 → origin
> check + parseAllowedOrigins; SRV-02 → go:embed + newRouter; SRV-03/04 → main flow.
> Slow-client drop and shutdown are silent (spec CHAT-02, design Open Question resolved).

## Batch plan (file independence)

| Batch | Worker | Tasks | Files |
|-------|--------|-------|-------|
| 1 | W1 | 1.1–1.6, 3.1 | internal/chat/room.go, internal/chat/client.go, internal/chat/room_test.go |
| 2 (parallel) | W2 | 3.2 | internal/chat/client_test.go |
| 2 (parallel) | W3 | 2.1–2.4, 3.3 | index.html → cmd/chatroom/index.html, cmd/chatroom/main.go, cmd/chatroom/main_test.go |
| — | orchestrator | 4.1 | openspec/project-map.yaml |

## Phase 1: Hub and pumps (`internal/chat`)

- [x] 1.1 `room.go`: replace `sync.Map` with `clients map[*Client]struct{}`; add `done chan struct{}`, `allowedOrigins map[string]struct{}`, per-room `upgrader`, and unexported `writeWait, pongWait, pingPeriod time.Duration; maxMessageSize int64` fields; add constants `defaultWriteWait=10s`, `defaultPongWait=60s`, `defaultPingPeriod=defaultPongWait*9/10`, `defaultMaxMessageSize=4096`, `sendBufferSize=256`; remove the package-level `upgrader`.
- [x] 1.2 `room.go`: `NewRoom(allowedOrigins []string) *Room` initializing all fields (broadcast buffered 256, register/unregister unbuffered).
- [x] 1.3 `room.go`: `Run(ctx)` + `fanOut(msg)` with exactly three removal paths (unregister-if-present + leave notice; silent slow-client drop; silent shutdown of all clients), `defer close(cr.done)`. Run never sends on its own channels.
- [x] 1.4 `room.go`: `checkOrigin(r)` — empty Origin allow; `null` reject (even if allowlisted); unparsable/empty host reject; `strings.EqualFold(u.Host, r.Host)` allow; exact allowlist match allow; else reject.
- [x] 1.5 `room.go`: `HandleWebSocket` — upgrade with `cr.upgrader`; nickname default `Anonymous`; `select { register <- c | <-done: conn.Close(); return }`; start `writePump`/`readPump` only after successful register.
- [x] 1.6 `client.go`: `readPump(cr)` (SetReadLimit, read deadline, pong handler extending deadline, `select` sends to broadcast/unregister with `<-cr.done`, `defer conn.Close()`); `writePump(cr)` (ping ticker at pingPeriod, SetWriteDeadline per write, close frame when `send` closed, `defer` ticker.Stop + conn.Close()).

## Phase 2: Composition root (`cmd/chatroom`)

- [x] 2.1 `git mv index.html cmd/chatroom/index.html` (content unchanged).
- [x] 2.2 `main.go`: `//go:embed index.html` → `indexHTML []byte`; `newRouter(room *chat.Room) *gin.Engine` with `GET /` (200, `text/html; charset=utf-8`) and `GET /ws`.
- [x] 2.3 `main.go`: `parseAllowedOrigins(s string) []string` — split on `,`, trim, drop empties, drop `null` with a log warning.
- [x] 2.4 `main.go`: `signal.NotifyContext(SIGINT, SIGTERM)`; `chat.NewRoom(parseAllowedOrigins(os.Getenv("ALLOWED_ORIGINS")))`; `go room.Run(ctx)` + roomDone; `http.Server{Addr: ":8080", Handler, ReadHeaderTimeout: 10s}`; ListenAndServe into errCh; non-`ErrServerClosed` error → log + exit 1; on signal: wait roomDone, `Shutdown(5s)`, exit 0.

## Phase 3: Tests (`go test -race ./...`, no sleep-based assertions)

- [x] 3.1 `internal/chat/room_test.go`: hub unit tests with nil-conn clients — join notice; duplicate nicknames independent (CHAT-04); unregister closes `send` once + leave once (CHAT-02/05); unregister after drop is no-op; slow client dropped, others still receive (CHAT-05); saturation with concurrent register/unregister completes (CHAT-03); ctx cancel closes all `send` and `done` (CHAT-08); `checkOrigin` table test (SRV-01).
- [x] 3.2 `internal/chat/client_test.go`: httptest + gin WebSocket integration — two clients exchange `"nick: text"` (CHAT-01); cross-origin → 403, `null` → 403 (SRV-01); oversized frame closes only sender, others get leave, exactly-at-limit passes (CHAT-06); unresponsive peer dropped with short pongWait (CHAT-07); responsive idle peer stays (CHAT-07); ctx cancel closes clients (CHAT-08); connect after shutdown returns (CHAT-08); goroutine count returns to baseline (CHAT-05).
- [x] 3.3 `cmd/chatroom/main_test.go`: `GET /` 200 + text/html + page body (SRV-02); same-origin upgrade with no allowlist succeeds (SRV-02); `parseAllowedOrigins` table test (SRV-01).

## Phase 4: Housekeeping

- [x] 4.1 `openspec/project-map.yaml`: web-client path → `cmd/chatroom/index.html`, served at `GET /`; add test paths; re-stamp header.

## Phase 5: Verify round 1 corrections (see verify-report.md)

| Batch | Worker | Tasks | Files |
|-------|--------|-------|-------|
| 3 (parallel) | W4 | 5.1–5.4 | internal/chat/room.go, client.go, room_test.go, client_test.go |
| 3 (parallel) | W5 | 5.5–5.7 | cmd/chatroom/main.go, main_test.go |

- [x] 5.1 `internal/chat`: readPump rejects binary frames (close 1003) and invalid UTF-8 text (close 1007) without broadcasting; nickname normalization in HandleWebSocket (ToValidUTF8, TrimSpace, 32 runes, empty → Anonymous) (CHAT-09).
- [x] 5.2 `client.go`: extend read deadline after every successful read (CHAT-07); log abnormal disconnects (read limit, timeout, unexpected close) with nickname; close frame via `FormatCloseMessage(CloseNormalClosure, "")`; log ping failures consistently; `_ =` on best-effort deadline/close errors with a short comment; doc comment on `Client`.
- [x] 5.3 `room_test.go`: slow-client tests trigger the drop on a broadcast (buffer sized so join notices fit); saturation test pre-fills `broadcast` before `Run` and guards sends with timeouts; assert `NewRoom(nil).maxMessageSize == 4096`.
- [x] 5.4 `client_test.go`: tests for CHAT-09 (binary, invalid UTF-8, nickname `%ff`/long, default Anonymous); CHAT-05 slow client conn is closed at conn level; ping tests with readers started immediately and pongWait ≈ 500ms–1s; goroutine-leak test counts only readPump/writePump frames via `runtime.Stack`; `errors.As` instead of type assertion.
- [x] 5.5 `main.go`: `run(ctx, ln net.Listener, allowed string) int` (main does signal ctx + `net.Listen(":8080")`, logs listen error and exits 1 — SRV-04; logs "listening" after bind); `http.Server` with `ReadHeaderTimeout`, `IdleTimeout: 60s`, `MaxHeaderBytes: 8<<10`, served via `srv.Serve(ln)` (SRV-05).
- [x] 5.6 `main.go` newRouter: `SetTrustedProxies(nil)`; `GET /` sets `X-Content-Type-Options: nosniff` and `X-Frame-Options: DENY` (SRV-05).
- [x] 5.7 `main_test.go`: SRV-03 test (run on 127.0.0.1:0, dial WS, cancel ctx → client read errors, run returns 0 within 6s); SRV-04 test (pre-bound port → startup path returns non-zero); SRV-05 headers test; parseAllowedOrigins as subtests with `slices.Equal`.
