# Design: Fix channel handling, connection robustness and origin check

## Technical Approach

Keep the existing hub shape (`Room.Run` goroutine + per-client `read`/`write` pumps) and make
ownership explicit, following gorilla's chat example:

- **`Run(ctx)` is the only goroutine that touches the registry and the only one that closes
  `client.send`.** It only ever *receives* from `register` / `unregister` / `broadcast`; it
  delivers via a private `fanOut(msg)` that does a non-blocking send per client. This fixes
  #1, #2, #3, #4 and #7 by construction.
- Every goroutine that *sends* to the hub (`HandleWebSocket`, `readPump`) does so inside a
  `select` that also watches `cr.done`, a channel `Run` closes when it returns. Nothing can block
  on the hub after shutdown (#7, #8).
- Pumps get the standard gorilla keepalive: read limit, read deadline extended by pongs, ping
  ticker, write deadline per write, close frame when `send` is closed (#5, #6).
- `CheckOrigin` is a `Room` method: same-origin, or no `Origin` header, or exact match against an
  allowlist passed to `NewRoom` (#9). `internal/chat` does not read env; `main` does.
- `main` becomes the composition root: `signal.NotifyContext`, `http.Server` with
  `ReadHeaderTimeout`, `ListenAndServe` error handling, `Shutdown` with timeout, room context
  cancelled on signal so hijacked WebSocket conns are closed by the hub. `GET /` serves an
  embedded `index.html` (#14).

Wire protocol is unchanged: plain text frames, `"<nick>: <text>"`, `"<nick> joined! 🎉"`,
`"<nick> left. 👋"`.

## Architecture Decisions

### Decision: Registry ownership — plain map owned by `Run`, keyed by `*Client`

**Choice**: `clients map[*Client]struct{}` read and written only inside `Run`. Join notice is
delivered with `fanOut` after insertion (the joining client also receives it, as today).
**Alternatives considered**: keep `sync.Map` (hides that there is a single owner and keeps the
nickname-key bug); `sync.Mutex` + map (works, but the code is already channel/hub-shaped and
would mix two paradigms); key by nickname with duplicate rejection (out of scope — backlog #10).
**Rationale**: Pointer keys make duplicate nicknames independent (#3). Single-goroutine
ownership means no locks and no data race by construction; `go test -race` verifies it.

### Decision: `send` is closed only by `Run`, in the same step that deletes the client

**Choice**: There are exactly three removal paths, all inside `Run`, all doing
`delete(cr.clients, c); close(c.send)`:
1. `unregister` — **only if `c` is still in the map** (otherwise no-op), then `fanOut("<nick> left. 👋")`.
2. Slow client in `fanOut` — buffer full → delete + close, **silently** (no leave notice).
3. Shutdown (`ctx.Done()`) — delete + close for every client, no notices; then `Run` returns
   and its `defer close(cr.done)` fires.
**Alternatives considered**: `closed bool` flag / `sync.Once` on Client (redundant state that can
drift from the map); `recover()` around `close` (hides bugs); closing `send` from `readPump`
(two closers → double close).
**Rationale**: Membership in the map *is* the "still open" flag. A client enters the map once
(`register` is sent once per client) and leaves it once, so `close(send)` happens at most once.
Silent drop for slow clients avoids `fanOut` recursing into itself (a leave notice could fill
more buffers and cascade drops); it matches gorilla's example. This is a visible behavior
decision — see Open Questions.

### Decision: Who closes the connection

**Choice**: Both pumps `defer c.conn.Close()`. `writePump` is the only goroutine that writes data
frames, pings and the final close frame; `readPump` is the only reader. Control frames written by
gorilla internally (pong reply, 1009 on read limit) use `WriteControl`, which is safe
concurrently with `WriteMessage`.
**Alternatives considered**: a single closer coordinated with another channel/WaitGroup (extra
state for no gain).
**Rationale**: gorilla's `Conn.Close` is safe to call concurrently and more than once (the second
call just returns an error, ignored). Each pump closing the conn is what makes the *other* pump
wake up: `writePump` closing unblocks `ReadMessage`; `readPump` closing makes the next write fail.

### Decision: Shutdown propagation

**Choice**: `Run(ctx context.Context)`; on `ctx.Done()` it closes every `send` and returns,
closing `cr.done`. Senders use:

    select { case cr.register <- c: ... ; case <-cr.done: conn.Close(); return }
    select { case cr.broadcast <- msg: ; case <-cr.done: return }
    select { case cr.unregister <- c: ; case <-cr.done: }

`main` cancels the room context (same `signal.NotifyContext` ctx) and calls
`srv.Shutdown(5s)` for regular HTTP connections. `broadcast` stays buffered (256);
`register`/`unregister` stay unbuffered. No channel other than `done` and `send` is ever closed.
**Alternatives considered**: storing `ctx` in `Room` and selecting on `ctx.Done()` in senders
(works, but then a sender could still block between "ctx cancelled" and "Run stopped
receiving"; `done` is signalled only after cleanup has finished, which is what the proof below
relies on); closing `broadcast` (multiple senders → send-on-closed panic).
**Rationale**: `http.Server.Shutdown` does not track hijacked conns (#8); the hub is the only
thing that knows them, so it must close them. Pumps are started only *after* a successful
`register`, so every running pump belongs to a client that `Run` will close on shutdown.

### Decision: Timeouts and limits — constants copied into unexported `Room` fields

**Choice**: Package constants `defaultWriteWait = 10s`, `defaultPongWait = 60s`,
`defaultPingPeriod = defaultPongWait*9/10` (54s), `defaultMaxMessageSize = 4096`,
`sendBufferSize = 256`. `NewRoom` copies the first four into unexported fields
`writeWait, pongWait, pingPeriod time.Duration; maxMessageSize int64`. Pumps read them from the
room. Internal tests (package `chat`) overwrite the fields right after `NewRoom`, before `Run`
or the test server start.
**Alternatives considered**: unexported package `var`s overridden by tests — rejected: pumps from
a previous test may still be running when `t.Cleanup` restores the value, which `-race` reports
as a data race; exported options / functional options — a config layer nobody needs (YAGNI).
**Rationale**: Fields are written once before any goroutine reads them → race-free, per-room
isolation between tests, zero public API.

### Decision: Origin check implementation

**Choice**: `NewRoom(allowedOrigins []string)` stores a `map[string]struct{}` and builds a
per-room `websocket.Upgrader{ReadBufferSize: 1024, WriteBufferSize: 1024, CheckOrigin: cr.checkOrigin}`
(the package-level `upgrader` var is removed). `checkOrigin(r)`:
1. `origin := r.Header.Get("Origin")`; empty → allow (non-browser client; CSWSH needs a browser,
   and browsers always send `Origin` on WebSocket handshakes).
2. `origin == "null"` → reject, even if listed (sandboxed iframes / `file://`).
3. `url.Parse(origin)`; error or empty host → reject.
4. `strings.EqualFold(u.Host, r.Host)` → allow (same-origin, same rule as gorilla's default).
5. exact string match in the allowlist → allow. Otherwise reject.
When it returns false, gorilla's `Upgrade` responds **403** before upgrading — no extra code.
`main` parses `ALLOWED_ORIGINS` with `parseAllowedOrigins(string) []string`: split on `,`,
`strings.TrimSpace`, drop empties, drop `null` with a log warning. Entries must be exact
`scheme://host[:port]` (no trailing slash, no path).
**Alternatives considered**: `NewRoom` reading the env itself (couples `internal/chat` to process
config and makes tests mutate env); wildcard/suffix matching (easy to get wrong, not required).
**Rationale**: Secure by default (empty allowlist = same-origin only), env access confined to the
composition root, and `checkOrigin` is unit-testable with `httptest.NewRequest`.

### Decision: Serving `index.html` — move to `cmd/chatroom/` and `go:embed`

**Choice**: `git mv index.html cmd/chatroom/index.html`; in `main.go`
`//go:embed index.html` into `var indexHTML []byte`; route
`GET /` → `c.Data(http.StatusOK, "text/html; charset=utf-8", indexHTML)`.
**Alternatives considered**: `r.StaticFile("/", "index.html")` relative to CWD (simplest code, but
404s when the binary runs from any other directory, e.g. `cd cmd/chatroom && go run .` or a
deployed binary); embed from a new root-level package (`go:embed` cannot use `..`) — adds a
package, violating the layout constraint; embedding in `internal/chat` (mixes UI asset with the
hub).
**Rationale**: One self-contained binary, CWD-independent, testable with `httptest`, no new
package. `main` is the composition root, so the page belongs next to the router. The file's
content is not changed (frontend fixes are backlog #13/#15/#16).

### Decision: Router construction extracted for testing

**Choice**: `func newRouter(room *chat.Room) *gin.Engine` in `main.go` registers `GET /` and
`GET /ws`. `main` wires it into `http.Server{Addr: ":8080", Handler: newRouter(room), ReadHeaderTimeout: 10 * time.Second}`.
**Alternatives considered**: testing `main()` end-to-end (needs ports/signals); no tests for
`cmd/chatroom` (leaves the "GET / page connects with no config" criterion unverified).
**Rationale**: Smallest seam that makes `GET /` + same-origin upgrade testable. `ReadHeaderTimeout`
is the secure default against slowloris; `ReadTimeout`/`WriteTimeout` are deliberately not set
(gorilla clears deadlines after hijack, and the pumps manage their own).

## Data Flow

    browser ──GET /──────────────→ gin ──→ embedded index.html
    browser ──GET /ws (Origin)───→ gin ──→ Room.HandleWebSocket
                                             │ checkOrigin → 403 | Upgrade
                                             │ select{register<-c | <-done}
                                             ▼
             ┌──────────── Room.Run (sole owner of clients map, closes send) ───────────┐
             │ register   → add, fanOut(join)                                          │
             │ unregister → if present: delete, close(send), fanOut(leave)             │
             │ broadcast  → fanOut(msg)  (full buffer → delete, close(send), silent)   │
             │ ctx.Done   → delete+close(send) for all; return; defer close(done)      │
             └────────────────────────────────────────────────────────────────────────┘
                 ▲ broadcast / unregister (select with done)       │ c.send (buffered 256)
                 │                                                 ▼
             readPump (only reader; SetReadLimit,          writePump (only writer; ping
             read deadline + pong handler) ◄── conn ──►    ticker, write deadline, close frame)

### Client lifecycle (sequence)

```mermaid
sequenceDiagram
    participant B as Browser
    participant H as HandleWebSocket
    participant R as Room.Run
    participant RP as readPump
    participant WP as writePump

    B->>H: GET /ws?nickname=x (Origin)
    H->>H: checkOrigin (false → 403, stop)
    H->>R: select register<-c | <-done
    alt done closed (shutdown before register)
        H->>H: conn.Close(); return  (send never used)
    else registered
        R->>R: clients[c]; fanOut("x joined! 🎉")
        H->>WP: go writePump
        H->>RP: go readPump
    end
    loop messages
        B->>RP: text frame (<= maxMessageSize)
        RP->>R: select broadcast<-"x: text" | <-done
        R->>WP: fanOut: non-blocking c.send<-msg
        WP->>B: WriteMessage (deadline writeWait); ping every pingPeriod
    end
```

Disconnect paths (who closes what):

| # | Trigger | First event | `close(send)` by | `conn.Close()` by | Leave notice |
|---|---------|-------------|------------------|-------------------|--------------|
| 1 | Peer closes / network error | `ReadMessage` error in readPump | Run (unregister, client present) | readPump (defer), then writePump (defer, after `send` closed) | yes |
| 2 | Pong timeout | read deadline expires → `ReadMessage` error | Run (unregister) | readPump, writePump | yes |
| 3 | Oversized message | `ErrReadLimit` (gorilla sends close 1009) | Run (unregister) | readPump, writePump | yes |
| 4 | Write error / write deadline / ping fails | writePump returns | Run (unregister, after readPump wakes on closed conn) | writePump, then readPump | yes |
| 5 | Slow client (send buffer full) | `fanOut` default branch | Run (fanOut: delete+close) | writePump (drains, close frame), readPump; its later `unregister` is a no-op | no |
| 6 | Server shutdown | `ctx.Done()` in Run | Run (cleanup loop), then `close(done)` | writePump (close frame), readPump; its `unregister` select takes `<-done` | no |

```mermaid
sequenceDiagram
    participant R as Room.Run
    participant RP as readPump
    participant WP as writePump
    Note over RP: paths 1-3: ReadMessage error
    RP->>R: select unregister<-c | <-done
    R->>R: if c in map: delete, close(c.send), fanOut(leave)
    RP->>RP: conn.Close()
    WP->>WP: send closed → close frame, conn.Close()
    Note over R: path 5: fanOut finds c.send full
    R->>R: delete, close(c.send)
    WP->>WP: drain, close frame, conn.Close()
    RP->>R: unregister<-c → not in map → no-op
    Note over R: path 6: ctx cancelled
    R->>R: for c: delete, close(c.send); return; close(done)
    WP->>WP: close frame, conn.Close()
    RP->>RP: unregister select picks <-done; conn.Close()
```

### Why there is no double close and no send on a closed channel

- **I1 — single closer**: `close(c.send)` appears only in `Run`, always paired with
  `delete(clients, c)` and only when `c` is in the map. `c` is inserted once (one `register` per
  client), so it can be deleted — and its `send` closed — at most once.
- **I2 — no send on closed `send`**: only `Run` sends on `c.send` (`fanOut`), and only to clients
  currently in the map. A closed `send` belongs to a client no longer in the map. Same goroutine →
  no interleaving.
- **I3 — no hub self-deadlock**: `Run` never sends on `register`/`unregister`/`broadcast`; notices
  go through `fanOut`, whose sends are non-blocking.
- **I4 — no sender blocks forever**: every send to the hub selects on `done`; `done` is closed by
  `defer` when `Run` returns. `done` and `send` are the only channels ever closed, each by one owner.
- **I5 — writePump always terminates**: pumps start only after `register` was accepted, so the
  client is in the map. It leaves the map only via a path that closes `send` (unregister, drop,
  shutdown). If `readPump`'s `unregister` is not accepted, it is because `done` is closed, which
  happens only after the shutdown loop closed every registered `send`. Otherwise writePump exits
  earlier on a write error.
- **I6 — readPump always terminates**: read deadline (`pongWait`) bounds silence; any conn close
  by writePump unblocks `ReadMessage`.
- **I7 — conn double close is harmless**: gorilla `Conn.Close` is concurrency-safe; the second call
  returns an ignored error.

## Interfaces / Contracts

Public API of `internal/chat` (only `NewRoom` and `Run` change):

```go
// NewRoom creates a room. allowedOrigins are exact "scheme://host[:port]" origins accepted in
// addition to same-origin requests. nil/empty means same-origin only.
func NewRoom(allowedOrigins []string) *Room

// Run owns the client registry until ctx is cancelled, then disconnects every client and
// returns. Call exactly once, before serving HandleWebSocket.
func (cr *Room) Run(ctx context.Context)

// HandleWebSocket — signature unchanged.
func (cr *Room) HandleWebSocket(c *gin.Context)
```

Unexported shape (for implementers; receiver name `cr` kept from current code):

```go
type Room struct {
    clients        map[*Client]struct{}
    broadcast      chan []byte        // buffered 256
    register       chan *Client       // unbuffered
    unregister     chan *Client       // unbuffered
    done           chan struct{}      // closed by Run on return
    allowedOrigins map[string]struct{}
    upgrader       websocket.Upgrader // CheckOrigin: cr.checkOrigin
    writeWait, pongWait, pingPeriod time.Duration
    maxMessageSize int64
}

type Client struct {
    conn     *websocket.Conn // never touched by Run/fanOut → nil in hub unit tests
    send     chan []byte
    nickname string
}

func (cr *Room) fanOut(msg []byte)
func (cr *Room) checkOrigin(r *http.Request) bool
func (c *Client) readPump(cr *Room)  // renamed from read
func (c *Client) writePump(cr *Room) // renamed from write; needs cr for writeWait/pingPeriod
```

`cmd/chatroom` (package `main`, unexported): `newRouter(room *chat.Room) *gin.Engine`,
`parseAllowedOrigins(s string) []string`, `//go:embed index.html var indexHTML []byte`.

`main` flow: `ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)` →
`room := chat.NewRoom(parseAllowedOrigins(os.Getenv("ALLOWED_ORIGINS")))` →
`go func(){ room.Run(ctx); close(roomDone) }()` → `go srv.ListenAndServe()` into a 1-buffered
`errCh` → `select` on `ctx.Done()` / `errCh` (non-`http.ErrServerClosed` error: log, `stop()`,
exit 1) → `stop()`, wait `roomDone`, `srv.Shutdown(context.WithTimeout(5s))` and log its error.
Close frames on shutdown are best-effort; TCP is closed at process exit regardless.

## Testing Strategy

All tests: stdlib `testing`, `go test -race ./...`. No `time.Sleep` assertions — wait on channels
with a helper that fails after a deadline (e.g. 2s). Tests touching Room fields do not call
`t.Parallel()` on shared rooms (each test creates its own room anyway).

| Layer | What to Test | Approach |
|-------|-------------|----------|
| Unit (hub) `internal/chat/room_test.go` | join notice delivered; duplicate nicknames independent; unregister closes `send` exactly once and emits leave; double unregister / unregister after drop is a no-op (no panic, no extra notice); slow client dropped and `send` closed while others still receive; saturation (many goroutines pushing `broadcast` + concurrent register/unregister) completes before deadline; ctx cancel closes every `send` and `done`, and later sends via the select pattern return | `NewRoom(nil)`, `go cr.Run(ctx)`; `&Client{send: make(chan []byte, n), nickname: "a"}` with nil `conn`; send directly on `cr.register`/`cr.unregister`/`cr.broadcast` (same package); small `n` (e.g. 1) to force drops |
| Unit (origin) `internal/chat/room_test.go` | no Origin → allow; same host → allow; other host → reject; `null` → reject even if allowlisted; allowlisted exact → allow; malformed → reject | table test calling `cr.checkOrigin(httptest.NewRequest(...))` |
| Integration `internal/chat/client_test.go` | two clients exchange `"nick: text"`; cross-origin upgrade → 403; oversized frame closes only the sender (`maxMessageSize = 16`); unresponsive peer dropped (`pongWait = 200ms`, `pingPeriod = 100ms`, client never reads) and others see leave; ctx cancel → client `ReadMessage` returns error and `done` is closed; goroutine count returns to baseline after N connect/disconnect | `gin.SetMode(gin.TestMode)`, `httptest.NewServer` on a gin engine with `GET /ws`; `websocket.DefaultDialer.Dial` with explicit `Origin` header; leak check polls `runtime.NumGoroutine()` until ≤ baseline or deadline |
| Integration `cmd/chatroom/main_test.go` | `GET /` returns 200, `text/html`, page body; WebSocket upgrade with `Origin` = server URL succeeds with no allowlist; `parseAllowedOrigins` trims, drops empties and `null` | `httptest.NewServer(newRouter(room))`, room run with cancellable ctx in `t.Cleanup`; table test for parser |

`testing/synctest` considered and not used: integration tests need real sockets, which are not
durably blocking inside a bubble; one consistent channel+deadline style is simpler.

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/chat/room.go` | Modify | `map[*Client]struct{}` registry, `done` channel, `Run(ctx)` with `fanOut` and three removal paths, `NewRoom(allowedOrigins)`, per-room upgrader + `checkOrigin`, timeout constants copied into fields, `HandleWebSocket` registers via `select` with `done` and starts pumps after registration |
| `internal/chat/client.go` | Modify | `readPump` (read limit, read deadline, pong handler, `select` sends to `broadcast`/`unregister`), `writePump` (ping ticker, write deadline per write, close frame on closed `send`); both `defer conn.Close()` |
| `internal/chat/room_test.go` | Create | Hub unit tests (nil-conn clients) + `checkOrigin` table tests |
| `internal/chat/client_test.go` | Create | `httptest` WebSocket integration tests: messaging, 403, oversize, pong timeout, shutdown, goroutine leak |
| `cmd/chatroom/main.go` | Modify | `go:embed` page, `newRouter`, `parseAllowedOrigins`, `signal.NotifyContext`, `http.Server{ReadHeaderTimeout}`, `ListenAndServe` error handling, room cancel + `Shutdown(5s)` |
| `cmd/chatroom/main_test.go` | Create | `GET /` served, same-origin upgrade works with no config, `parseAllowedOrigins` table test |
| `index.html` → `cmd/chatroom/index.html` | Move | `git mv` so it can be embedded (`go:embed` cannot reference `..`); content unchanged |
| `openspec/project-map.yaml` | Modify | `web-client` path → `cmd/chatroom/index.html`, "served by Go at GET /"; add test paths (refresh after apply) |

## Migration / Rollout

No migration required. Operational notes: open the app at `http://localhost:8080` (the page still
hardcodes `ws://localhost:8080`, backlog #13); opening it via `file://` or `127.0.0.1` will be
rejected by the origin check unless that origin is added to `ALLOWED_ORIGINS`. Rollback =
`git revert` (includes moving `index.html` back).

## Open Questions

- [ ] Slow-client drop is **silent** (no `"left"` notice), as is shutdown. If the specs require a
      leave notice on drop, implement it by collecting dropped clients during `fanOut` and
      announcing them in one follow-up `fanOut` (bounded, non-recursive) — non-blocking either way.
      Not blocking for implementation; specs should state the expected behavior.
