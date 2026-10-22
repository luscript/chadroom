# Exploration: fix-channel-handling

> Source: direct review of the full codebase (4 files, ~180 LOC) by the orchestrator,
> commit d7072e4. No sub-agent fan-out — the whole repo fit in a single review pass.

## Current Architecture

- `cmd/chatroom/main.go` — creates `chat.NewRoom()`, runs `go chatRoom.Run()`, gin router with
  `GET /ws`, `r.Run(":8080")` (error ignored, no shutdown).
- `internal/chat/room.go` — `Room{clients *sync.Map, broadcast chan []byte (256), register,
  unregister chan *Client (unbuffered)}`. `Run()` loops forever over a `select`.
  `HandleWebSocket` upgrades (CheckOrigin always true), creates `Client`, sends to `register`,
  starts `write()` and `read()` goroutines.
- `internal/chat/client.go` — `Client{conn, send chan []byte (256), nickname}`.
  `write()` ranges over `send`; `read()` loops `ReadMessage` and sends to `broadcast`; on error
  sends `unregister` and closes conn.

## Findings (in scope for this change)

| # | Severity | Location | Finding |
|---|----------|----------|---------|
| 1 | CRITICAL | room.go:43,46 | `Run()` sends to `broadcast`, which only `Run()` consumes. When the 256 buffer is full, the hub deadlocks itself; every `read()` and `HandleWebSocket` then blocks forever. |
| 2 | HIGH | room.go:44-46, client.go:29 | `unregister` never closes `client.send` → `write()` goroutine blocks on `range` forever (one leaked goroutine per disconnect). |
| 3 | HIGH | room.go:42,45,54 | Clients keyed by nickname: duplicate nicknames (default "Anonymous") overwrite each other; a disconnect deletes the *other* client; a slow client already dropped at :53 later unregisters a new same-named client and "left" is announced twice. |
| 4 | MEDIUM | room.go:21 | `sync.Map` is unnecessary: in the hub pattern only `Run()` touches the registry. Mixing paradigms obscures ownership. |
| 5 | HIGH | client.go | No ping/pong, no read/write deadlines → dead peers never detected; a stalled TCP write blocks `write()` indefinitely. |
| 6 | HIGH | client.go | No `SetReadLimit` → any client can send arbitrarily large frames that are rebroadcast to everyone (memory DoS). |
| 7 | MEDIUM | client.go:38 | Readers block on `broadcast` with no escape; tied to #1 (hub that can stall). |
| 8 | MEDIUM | main.go, room.go:38 | No graceful shutdown: `Run()` cannot stop, `r.Run` error ignored, no signal handling. Note `http.Server.Shutdown` does NOT close hijacked (WebSocket) connections. |
| 9 | HIGH (security) | room.go:16 | `CheckOrigin` returns true → Cross-Site WebSocket Hijacking. |

Positive: frontend renders with `textContent` (no XSS). gorilla `Conn.Close` is safe to call
concurrently with other methods, so double `conn.Close()` in read/write is harmless.

## Backlog (OUT of scope — documented for future changes)

| # | Proposed change name | Finding | Suggested approach |
|---|----------------------|---------|--------------------|
| 10 | `structured-messages` | Impersonation: nickname unvalidated, message built as `nick + ": " + text` (a user can send `"\nBob: hi"` or pick nick "Admin"). | JSON envelope `{type, from, text, ts}`; validate nickname (length, charset); optionally reject duplicate nicknames. |
| 11 | `rate-limiting` | No per-client rate limit; one client can flood the room. | Token bucket per client in the read pump (`golang.org/x/time/rate` or a stdlib ticker). |
| 13 | `frontend-hardening` | `ws://localhost:8080` hardcoded; breaks on deploy/HTTPS. | Build URL from `location.host`, `wss:` when `location.protocol === "https:"`. |
| 14 | ✅ moved into `fix-channel-handling` | Server does not serve `index.html`; opened as `file://` (Origin `null`). | `r.StaticFile("/", "index.html")` (or `http.FileServer`). Interacts with #9 origin allowlist. |
| 15 | `frontend-hardening` | Cancelled `prompt` → nickname `"null"`. | Default/retry when `prompt` returns null/empty. |
| 16 | `frontend-hardening` | No reconnect; `ws.send` throws if socket not OPEN. | Check `readyState`, exponential-backoff reconnect. |
| 17 | `deps-cleanup` | gin and gorilla marked `// indirect` though imported directly. | `go mod tidy`. |
| 18 | `deps-cleanup` | gin used for a single route, pulls ~30 transitive deps. | Replace with `net/http` `ServeMux` (Go 1.22+ method patterns). |
| 20 | — (housekeeping) | `.claude.json` / `.claude/` untracked. | Review for secrets before committing. |
| — | `chat-features` | Feature ideas: online user list, multiple rooms, last-N history on join, typing indicator. | Each depends on `structured-messages` first. |
