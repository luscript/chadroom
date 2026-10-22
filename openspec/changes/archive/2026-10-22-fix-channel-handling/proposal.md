# Proposal: Fix channel handling, connection robustness and origin check

## Intent

The chat hub can deadlock itself under load (the `Run` goroutine sends to a channel only it
consumes), leaks one goroutine per disconnect, and corrupts its client registry when two users
share a nickname. Connections have no liveness detection, no size limits and no shutdown path,
and any website can open a WebSocket to the server (CSWSH). This change makes the hub
correct and bounded under concurrency, and secure by default on origin. Findings #1–#9 in
`exploration.md`.

## Scope

### In Scope
- Hub never sends to its own channels; join/leave/broadcast go through a local fan-out helper (#1, #7).
- Registry is a plain `map[*Client]struct{}` owned exclusively by `Run` (#3, #4).
- Every removal path (unregister, slow-client drop, shutdown) removes the client **once** and
  closes `send` **once**, so `write` always terminates (#2).
- Per-connection read limit, read/write deadlines and ping/pong keepalive (#5, #6).
- Graceful shutdown: `Run(ctx)` stops on context cancel and disconnects all clients; `main`
  uses `http.Server` + `signal.NotifyContext`, handles `ListenAndServe` errors (#8).
- Origin check: same-origin by default, plus an explicit allowlist from configuration (#9).
- Serve `index.html` from the Go server at `GET /` so the page and the WebSocket share an origin
  and pass the same-origin check (#14, pulled in from the backlog by user decision).
- Tests with `go test -race`: hub unit tests and WebSocket integration tests (`httptest`).

### Out of Scope
- Everything in the **Backlog** section of `exploration.md` (#10 structured messages / nickname
  validation, #11 rate limiting, #13 and #15–#16 frontend hardening, #17–#18 dependency cleanup /
  gin removal, feature ideas). #14 was moved INTO scope. Each is documented there with a suggested future change name.

## Approach

Classic hub pattern (as in gorilla's chat example), made context-aware:

- `Room.Run(ctx)` is the single owner of the registry. A private `fanOut(msg)` delivers with a
  non-blocking send per client; if a client's buffer is full it is removed (delete + close).
  `unregister` only acts if the client is still registered, which makes close idempotent by
  ownership instead of by flags or `recover`.
- `HandleWebSocket` and `readPump` send to `register` / `unregister` / `broadcast` inside a
  `select` with `<-ctx.Done()` (room's done channel), so no goroutine blocks after shutdown.
- `readPump`: `SetReadLimit`, `SetReadDeadline(pongWait)`, pong handler extends the deadline.
  `writePump`: ping ticker (`pingPeriod < pongWait`), `SetWriteDeadline` per write, sends a
  close frame when `send` is closed. Timeouts are package constants (YAGNI: no config layer).
- Origin: `CheckOrigin` accepts same-origin requests (or no `Origin` header, i.e. non-browser
  clients) plus origins listed in `ALLOWED_ORIGINS` (comma-separated env var). Everything else
  is rejected with 403 before upgrade.
- Static page: `GET /` serves `index.html` from the Go server. How the file is located (relative
  path vs `go:embed`, which cannot reference parent directories) is a design decision.
- `main`: `signal.NotifyContext(SIGINT, SIGTERM)`, `http.Server{Handler: gin router}`,
  `Shutdown` with timeout; cancel the room context so hijacked WS connections are closed too.

## Constraints

- No new runtime dependencies (stdlib + existing gin/gorilla only).
- Keep the wire protocol unchanged (plain text frames, `"nick: text"`, join/leave notices) so
  `index.html` keeps working.
- Keep package layout (`cmd/chatroom`, `internal/chat`); no new packages.
- Must pass `go test -race ./...` and `go vet ./...`.

## Affected Areas

| Area / Module | Impact | Description |
|---------------|--------|-------------|
| `internal/chat` (hub) | Modified | Registry ownership, fan-out, idempotent removal, context-aware Run, origin check |
| `internal/chat` (client pumps) | Modified | Read limit, deadlines, ping/pong, non-blocking sends to hub |
| `cmd/chatroom` | Modified | `http.Server`, signal handling, graceful shutdown, error handling, `GET /` serves index.html |
| `internal/chat` tests | New | Hub unit tests + WebSocket integration tests under `-race` |

## Risks

| Risk | Likelihood | Mitigation |
|------|------------|------------|
| Opening `index.html` as `file://` stops working (`Origin: null` is rejected). | High | Page is now served by Go at `GET /` (same origin). `null` is never auto-allowed (sandboxed iframes send it). |
| `index.html` still hardcodes `ws://localhost:8080`; opening the page as `http://127.0.0.1:8080` yields a cross-origin upgrade and is rejected. | Med | Document "open http://localhost:8080"; proper fix (build URL from `location.host`) stays in backlog #13. |
| Read limit drops clients that send legitimate long messages. | Low | 4 KiB limit is generous for chat text; constant is easy to change. |
| Ping/pong timing kills idle-but-alive connections behind proxies with short idle timeouts. | Low | Ping every ~54s with 60s pong wait (standard values). |
| Concurrency tests are flaky. | Med | Synchronize via channels/timeouts, no `time.Sleep`-based assertions; run with `-race -count` in verify. |

## Rollback Plan

All changes are confined to `internal/chat` and `cmd/chatroom/main.go` in a single change set.
Revert the commit(s) (`git revert <sha>`) to restore the previous behavior; no data, schema or
protocol migration is involved.

## Dependencies

- None external. Requires Go toolchain (installed at `/snap/bin/go`).

## Success Criteria

- [ ] Hub cannot deadlock: a test saturating `broadcast` while clients join/leave completes under `-race`.
- [ ] No goroutine leak: after N clients connect and disconnect, `write`/`read` goroutines all exit (verified in test).
- [ ] Two clients with the same nickname both receive messages; one leaving does not affect the other.
- [ ] Messages larger than the read limit close the offending connection only.
- [ ] Unresponsive peers are disconnected after the pong timeout.
- [ ] Cross-origin WebSocket upgrade not in the allowlist is rejected (403); same-origin and allowlisted origins succeed.
- [ ] `GET /` returns the chat page and its WebSocket connects without any `ALLOWED_ORIGINS` configuration.
- [ ] SIGINT/SIGTERM shuts the server down cleanly, closing all WebSocket clients.
- [ ] `go vet ./...` and `go test -race ./...` pass.
