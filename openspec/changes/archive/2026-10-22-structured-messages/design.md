# Design: Structured messages and nickname hardening

## Technical Approach

Keep the hub shape from `fix-channel-handling` (`Room.Run` sole owner of the registry, pumps
per connection, `select` on `cr.done` for every send to the hub) and move **all wire
formatting into `Run`**:

- `readPump` validates (binary / UTF-8 / empty) and sends `inbound{from: c, text}` to
  `broadcast`. It no longer reads or formats `c.nickname` for output.
- `Run` builds a `message{Type, From, Text, TS}`, marshals it **once** with `encoding/json`
  and hands the `[]byte` to the unchanged non-blocking `fanOut`.
- On `register`, `Run` picks the final nickname (case-folded `taken` set, ` (n)` suffix),
  writes it to `c.nickname`, adds `c` to the registry, **closes `c.registered`**, queues
  `welcome` on `c.send` only, then fans out `join` to everyone. `HandleWebSocket` waits on
  `<-c.registered` before starting the pumps, so the pumps read a final nickname
  (happens-before via channel close) and `welcome` is the first frame in `c.send`.
- A single `remove(c)` helper does `delete(clients) + delete(taken) + close(send)`, used by
  the three existing removal paths, so name release cannot drift from membership.
- `normalizeNickname` gains a strip step (Cc + `Bidi_Control`) before trim/truncate; the
  suffix algorithm reuses the same rune truncation helper.
- `index.html` replaces the `addRaw` regex with `JSON.parse` + `switch (msg.type)`.

Inbound frames stay plain text. Hub invariants I1–I7 of `fix-channel-handling` are kept; I8
(below) is added.

## Architecture Decisions

### Decision: Envelope type and encoding

**Choice**: one unexported struct, field order = JSON order, no `omitempty`:

    type message struct {
        Type string `json:"type"` // "chat" | "join" | "leave" | "welcome"
        From string `json:"from"`
        Text string `json:"text"` // "" for join/leave/welcome
        TS   int64  `json:"ts"`   // unix milliseconds
    }

Type constants `typeChat`, `typeJoin`, `typeLeave`, `typeWelcome`. `encode(m) []byte` wraps
`json.Marshal`; on error (unreachable for four strings + int64) it logs and returns `nil`,
and callers skip a `nil` payload.
**Alternatives considered**: `map[string]any` (no field order, allocs, typos at runtime);
`omitempty` on `text` (success criterion says every frame has all four fields; one shape is
simpler for the client); hand-built JSON with `strconv.Quote` (Go quoting ≠ JSON quoting —
injection risk); `json.Encoder` with `SetEscapeHTML(false)` (default HTML escaping of
`< > &` and U+2028/2029 is harmless and safer).
**Rationale**: stdlib only, deterministic output (`{"type":…,"from":…,"text":…,"ts":…}`),
trivial to decode in tests. `json.Marshal` turns any invalid UTF-8 into U+FFFD, but inputs
are already valid (CHAT-09).

### Decision: `ts` from `time.Now().UnixMilli()`, no injectable clock

**Choice**: `ts` is `int64` unix ms, taken with `time.Now()` inside `Run` once per event, so
every recipient gets the same value. Tests assert `before.UnixMilli() <= ts <= after.UnixMilli()`.
**Alternatives considered**: unexported `now func() time.Time` field on `Room` overridden in
tests (fits the timeout-field pattern, but no test needs an exact value — YAGNI); seconds
(too coarse to order a burst); RFC 3339 string (more bytes, client parses anyway).
**Rationale**: ms is what `new Date(ts)` takes directly and is exact in a JS `Number` until
year 287 396. A window assertion is as strong as an injected clock for these tests.

### Decision: Marshal once per event; `fanOut` keeps `[]byte`

**Choice**: `Run` calls `cr.encode(...)` once and passes the bytes to `fanOut(msg []byte)`
(unchanged) or, for `welcome`, to `deliver(c, msg) bool` — the per-client non-blocking send
that `fanOut` now loops over:

    func (cr *Room) deliver(c *Client, msg []byte) bool {
        select {
        case c.send <- msg:
            return true
        default:
            cr.remove(c) // slow client: silent drop, as today
            return false
        }
    }

**Alternatives considered**: `chan message` per client with marshaling in `writePump`
(N marshals per event, and pumps would format wire output again); an `emit(type, from, text)`
helper that also fans out (hides the welcome/join split that ordering depends on).
**Rationale**: one encoded buffer shared read-only by all writers; `writePump` stays a dumb
byte writer. `deliver` is extracted because `welcome` needs the same drop semantics for a
single client — real duplication, not speculative.

### Decision: `broadcast` carries `inbound{from, text}`; empty messages dropped in `readPump`

**Choice**: `broadcast chan inbound` (still buffered `sendBufferSize`), with
`type inbound struct{ from *Client; text string }`. `readPump` drops frames where
`strings.TrimSpace(string(msg)) == ""` with `continue` (deadline already extended, connection
stays open). Non-empty text is forwarded **unmodified** (not trimmed). In `Run`, a
`broadcast` whose `from` is **not in the registry is discarded**.
**Alternatives considered**: drop empty messages in the hub (costs a queue slot and a hub
iteration for garbage; validation already lives in `readPump` next to the binary/UTF-8
checks); trimming the text before broadcast (changes user content — not asked).
**Rationale**: the hub-membership check is new and required: after a slow-client drop the
client's name is released and may be reassigned, so relaying a late message from the stale
`*Client` would display it under a name another user now owns. Membership is the only
authority for "who is `from`".

### Decision: Registration handshake — `registered` channel closed by `Run`

**Choice**: `Client` gains `registered chan struct{}` (unbuffered, made by whoever builds the
client). `Run`'s register case, in one step:

    name := cr.assignNickname(c.nickname)   // c.nickname holds the normalized request
    c.nickname = name
    cr.clients[c] = struct{}{}
    cr.taken[foldKey(name)] = struct{}{}
    close(c.registered)
    ts := time.Now().UnixMilli()
    if cr.deliver(c, cr.encode(message{typeWelcome, name, "", ts})) {
        cr.fanOut(cr.encode(message{typeJoin, name, "", ts}))
    }

`HandleWebSocket`:

    select {
    case cr.register <- client:
    case <-cr.done:
        conn.Close() // room stopped: nothing was registered, send never used
        return
    }
    <-client.registered // guaranteed: Run closes it in the same case that accepted register
    go client.writePump(cr)
    go client.readPump(cr)

**Alternatives considered**: per-registration reply `chan string` buffered 1 (equally
non-blocking, but carries data that already lives on the client and needs a wrapper struct
on `register`); waiting with `select { <-registered | <-done }` (once `register` is accepted
the close is unconditional; adding `done` only introduces a random branch that skips pumps
for a registered client); keeping the name only in `map[*Client]string` (then `logError` in
the pumps has no name, or needs another copy).
**Rationale**: `close` cannot block, so `Run` still never blocks on a channel it touches
(CHAT-03). `register` is sent once per client, so `registered` is closed at most once (same
argument as I1). Close → receive gives the happens-before edge from `Run`'s write of
`c.nickname` to every pump read, which the unbuffered `register` send alone does not (the
write happens after the receive). After registration `c.nickname` is never written again →
pumps' reads are race-free.

### Decision: Case-insensitive `taken` set keyed by simple case folding (stdlib)

**Choice**: `taken map[string]struct{}` owned by `Run`, keyed by `foldKey(name)`: each rune
mapped to the smallest rune of its `unicode.SimpleFold` orbit. Released in `remove(c)` (all
three paths: unregister, slow drop, shutdown loop).
**Alternatives considered**: `strings.ToLower` (not `EqualFold` semantics: `ς`/`σ`, `ſ`/`s`,
`K` (Kelvin)/`k` would be distinct); `golang.org/x/text/cases.Fold` (present in `go.mod` only
as an indirect gin dependency — would become a direct dependency, and full folding makes
`ß` == `ss`, which `EqualFold` does not); linear scan with `strings.EqualFold` over the
registry (O(n) per join, no index for the suffix loop).
**Rationale**: `strings.EqualFold` compares rune-by-rune over `SimpleFold` orbits, so the
orbit minimum is a canonical key: `foldKey(a) == foldKey(b) ⇔ strings.EqualFold(a, b)`.
Proposal decision honoured exactly, no new dependency, O(1) lookup.

### Decision: Suffix algorithm

**Choice**:

    func (cr *Room) assignNickname(base string) string {
        if _, ok := cr.taken[foldKey(base)]; !ok {
            return base
        }
        for n := 2; ; n++ {
            suffix := " (" + strconv.Itoa(n) + ")"
            cand := truncateRunes(base, maxNicknameRunes-utf8.RuneCountInString(suffix)) + suffix
            if _, ok := cr.taken[foldKey(cand)]; !ok {
                return cand
            }
        }
    }

`truncateRunes(s, n)` keeps the first `n` runes and right-trims whitespace (today's
`TrimSpace(string([]rune(s)[:n]))`), shared with `normalizeNickname`.
**Alternatives considered**: reject with an error frame (user decided suffix); random/counter
suffix (not "lowest free n"); per-base counters (stale after leaves).
**Rationale**: terminates in at most `len(taken)+1` iterations (pigeonhole). Base is never
empty after truncation: it is non-empty, left-trimmed, and the budget is ≥ 27 runes for
any realistic `n`. Typed casing is preserved (`BOB` → `BOB (2)`). `Anonymous` is an ordinary
base → `Anonymous (2)`. A user may literally request `bob (2)`; the `taken` check covers that,
and the next suffix is chosen. Truncation can split a grapheme (ZWJ emoji, flag pair) leaving
a partial cluster — accepted cosmetic edge case (proposal risk table).

### Decision: `normalizeNickname` order of operations

**Choice**:
1. `strings.ToValidUTF8(s, "�")`
2. `strings.Map` dropping runes where `unicode.IsControl(r)` (Cc: U+0000–U+001F, U+007F,
   U+0080–U+009F — includes `\t \n \r`) or `unicode.Is(unicode.Bidi_Control, r)`
   (U+061C, U+200E, U+200F, U+202A–U+202E, U+2066–U+2069). ZWJ U+200D is Cf, not
   Bidi_Control → kept.
3. `strings.TrimSpace`
4. `truncateRunes(s, maxNicknameRunes)`
5. `""` → `"Anonymous"`

**Alternatives considered**: explicit rune ranges literal (drifts from Unicode; the stdlib
table is exact); trim before strip (`"‮ bob"` would keep the leading space); replacing
controls with a space (proposal says strip).
**Rationale**: strip first so trim sees what will be displayed; truncate after trim so the 32
runes are visible ones. Note `unicode.Bidi_Control` adds **U+061C** (Arabic Letter Mark) to
the proposal's list — see Open Questions.

### Decision: Frontend parses by type; identity from `welcome`; exact-match "own"

**Choice**: `socket.onmessage` → `handle(e.data)`: `JSON.parse` in `try` (non-JSON is ignored,
not rendered), then `switch (msg.type)`:
- `welcome` → `nickname = msg.from`, update `#me` text/title.
- `chat` → `addChat(msg.from, msg.text, msg.ts)`.
- `join` / `leave` → `addSystem(\`${msg.from} se unió 🎉\`)` / `\`${msg.from} salió 👋\``
  (Spanish, matching the UI; still `textContent`).
- default → ignore (forward compatible with `chat-features`).
`addChat` uses `own = author === nickname` and renders `new Date(ts)` (fallback `new Date()`
when `ts` is not a finite number). Delete `addRaw` and its regex. Layout/CSS unchanged.
**Alternatives considered**: case-insensitive "own" comparison in JS (`toLocaleLowerCase` ≠
Go simple folding; unnecessary because the server guarantees no other connected user folds
equal to my assigned name, and it echoes my exact assigned string); keeping the typed nick
until `welcome` (wrong when suffixed).
**Rationale**: the server is the single source of identity; exact match on the assigned name
is both correct and simplest. `welcome` arrives before any other frame, so `nickname` is set
before the first `chat` is rendered.

## Data Flow

    browser ──GET /ws?nickname=x──→ HandleWebSocket
        normalizeNickname(x) → c{nickname: req, send, registered}
        select{register<-c | <-done}  ──→  Run: assignNickname, clients+taken,
        <-c.registered                        close(registered), deliver(welcome), fanOut(join)
        go writePump / go readPump
    readPump: text frame → binary? 1003 · invalid UTF-8? 1007 · blank? continue
              → select{broadcast<-inbound{c,text} | <-done}
    Run: from ∉ clients? drop · else fanOut(encode(chat{from.nickname,text,now}))
    remove(c) = delete(clients,c); delete(taken,foldKey(c.nickname)); close(c.send)
        used by: unregister (then fanOut(leave)), fanOut/deliver drop (silent), ctx.Done (silent)

### Register → welcome → join

```mermaid
sequenceDiagram
    participant B as Browser (new)
    participant H as HandleWebSocket
    participant R as Room.Run
    participant WP as writePump
    participant O as Other clients
    B->>H: GET /ws?nickname=bob
    H->>H: upgrade, normalizeNickname
    H->>R: select register<-c | <-done
    alt done closed
        H->>H: conn.Close(); return
    else accepted
        R->>R: name = assignNickname("bob") → "bob (2)"; c.nickname = name
        R->>R: clients[c], taken[foldKey(name)]
        R-->>H: close(c.registered)
        R->>R: deliver(c, welcome) — 1st item in c.send
        R->>O: fanOut(join "bob (2)") — includes c (2nd item)
        H->>H: <-c.registered returns
        H->>WP: go writePump, go readPump
        WP->>B: {"type":"welcome","from":"bob (2)",...}
        WP->>B: {"type":"join","from":"bob (2)",...}
    end
```

### Chat broadcast

```mermaid
sequenceDiagram
    participant A as Browser A
    participant RP as readPump(A)
    participant R as Room.Run
    participant WP as writePump(each)
    A->>RP: text "hola"
    RP->>RP: SetReadDeadline; type/UTF-8 checks; TrimSpace=="" → continue
    RP->>R: select broadcast<-inbound{A,"hola"} | <-done
    R->>R: A ∈ clients? else drop
    R->>R: b = encode(chat, A.nickname, "hola", now) — once
    R->>WP: fanOut(b): non-blocking send; full → remove (silent)
    WP->>A: {"type":"chat","from":"A","text":"hola","ts":…}
```

### Why `welcome` is first and the handshake is safe

- **I8 — welcome first**: only `Run` sends on `c.send`. In the register case `c` enters the
  registry and receives `welcome` before any other case can run, so `welcome` precedes the
  `join` in the same case and every later frame. `send` is FIFO; pumps starting later does
  not change the order. If `deliver(welcome)` fails (only with a 0-capacity test buffer) the
  client is removed silently and no `join` is announced.
- **I9 — no block on registration**: `Run` only closes `registered` (never blocks).
  `HandleWebSocket` blocks on `<-registered` only after `register` was accepted, and the
  close happens in that same case. On shutdown after registration, pumps still start, drain
  `send` (welcome/join) and see it closed (I5).
- **I10 — name release mirrors membership**: `taken` is mutated only in the register case and
  in `remove`, both inside `Run`; `remove` runs at most once per client (I1).

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/chat/message.go` | Create | `message` struct, type constants, `inbound`, `encode` |
| `internal/chat/nickname.go` | Create | `normalizeNickname` (moved + strip step), `truncateRunes`, `foldKey`, `(*Room).assignNickname` |
| `internal/chat/room.go` | Modify | `broadcast chan inbound`, `taken` set, register case per handshake, `deliver`, `remove`, chat envelope with membership check, leave envelope; `HandleWebSocket` waits on `registered` |
| `internal/chat/client.go` | Modify | `registered chan struct{}` field; `readPump` drops blank frames and sends `inbound{c, text}` |
| `internal/chat/room_test.go` | Modify | envelope helpers, migrated assertions, welcome-first, collision/suffix/fold/normalize tables, non-member drop |
| `internal/chat/client_test.go` | Modify | envelope helpers, migrated assertions, concurrent-nickname race test, empty-message test, case-insensitive release test |
| `cmd/chatroom/main_test.go` | Modify | `TestRunGracefulShutdown` asserts the first frame is a `welcome` envelope |
| `cmd/chatroom/index.html` | Modify | JSON `switch` by type, identity from `welcome`, `ts` rendering, remove `addRaw` regex |
| `README.md` | Modify | `/ws` row describes the JSON envelope (currently documents `<nickname>: <texto>`) |

Splitting `message.go`/`nickname.go` keeps `room.go` focused on the hub; no new package.

## Interfaces / Contracts

Public API unchanged: `NewRoom(allowedOrigins []string) *Room`, `(*Room) Run(ctx)`,
`(*Room) HandleWebSocket(*gin.Context)`.

Wire (server → client, text frame, one object per frame):

```json
{"type":"chat","from":"bob (2)","text":"hola","ts":1792684800123}
{"type":"join","from":"bob (2)","text":"","ts":1792684800001}
{"type":"leave","from":"bob (2)","text":"","ts":1792684900000}
{"type":"welcome","from":"bob (2)","text":"","ts":1792684800001}
```

Client → server: unchanged plain text frame (≤ 4096 bytes, valid UTF-8).

```go
const (
	typeChat    = "chat"
	typeJoin    = "join"
	typeLeave   = "leave"
	typeWelcome = "welcome"
)

type message struct {
	Type string `json:"type"`
	From string `json:"from"`
	Text string `json:"text"`
	TS   int64  `json:"ts"`
}

type inbound struct {
	from *Client
	text string
}

type Room struct {
	clients    map[*Client]struct{}
	taken      map[string]struct{} // foldKey(nickname) of every registered client
	broadcast  chan inbound        // buffered sendBufferSize
	register   chan *Client
	unregister chan *Client
	done       chan struct{}
	// allowedOrigins, upgrader, timeouts: unchanged
}

type Client struct {
	conn       *websocket.Conn
	send       chan []byte
	registered chan struct{} // closed by Run once nickname is final
	nickname   string        // requested on entry to register; final after registered is closed
}

func encode(m message) []byte
func (cr *Room) deliver(c *Client, msg []byte) bool
func (cr *Room) fanOut(msg []byte)          // loops deliver
func (cr *Room) remove(c *Client)           // delete clients/taken, close(send)
func (cr *Room) assignNickname(base string) string
func normalizeNickname(s string) string
func truncateRunes(s string, n int) string
func foldKey(s string) string
```

## Testing Strategy

All under `go test -race ./...`, stdlib only, channel + deadline waits (no sleep assertions).

**Helpers** (package `chat`): `decode(t, []byte) message` (fails on invalid JSON or unknown
`type`); hub `expect(t, c, typ, from, text string)` ignoring `ts`; integration
`readMsg(t, conn) message`, `readUntil(t, conn, typ, from, text)`, `recvUntil` over
`<-chan message`; `newTestClient` also makes `registered`; `broadcast(t, cr, from *Client, text)`.

**Mechanical migration** (do first, no behavior additions in the same step):
`"X joined! 🎉"` → `(join, X, "")`; `"X left. 👋"` → `(leave, X, "")`; `"X: t"` →
`(chat, X, t)`; every registration now yields a leading `(welcome, X, "")` for the newcomer.
Knock-on adjustments: slow-client tests that size buffers by "two join notices" need +1 slot
for `welcome` (2 → 3); `TestDroppedSlowClientConnectionIsClosed` builds `Client` with
`registered` and a 2-slot buffer (welcome + own join); `TestSaturationNoDeadlock` pre-fills
`broadcast` with `inbound{from: listener}` and uses the registered listener as sender so
`fanOut` is still exercised; `TestNicknameNormalization` (integration) asserts the first
frame is `welcome` with the expected `from`; `expectLeaveWithoutBroadcast` checks
`Type == chat && From == nick`.

**New tests**

| Layer | Test |
|-------|------|
| Unit | `TestFoldKeyMatchesEqualFold`: pairs (`Bob/bOB`, `σ/ς/Σ`, `s/ſ`, `k/K(U+212A)`, `ß/ss` ≠, `é/É`) — `foldKey(a)==foldKey(b)` iff `strings.EqualFold` |
| Unit | `TestNormalizeNickname` extended: `\n`, `\t`, U+0000, U+007F, U+0085, each bidi control incl. U+202E and U+061C stripped; `"‮ bob"` → `bob`; ZWJ family emoji kept; control-only → `Anonymous` |
| Unit (hub) | `TestAssignNickname` table: free; `Bob` taken → `bob (2)`; `(2)` taken → `(3)`; 32-rune base → 28 runes + ` (2)`; `n=10` → 27 + ` (10)`; truncation leaving trailing space trimmed; `Anonymous` → `Anonymous (2)`; literal `bob (2)` taken by another; every result ≤ 32 runes |
| Unit (hub) | `TestWelcomeFirst`: register; first item on `send` is `welcome` with assigned name, then own `join`; others get only `join` |
| Unit (hub) | `TestNameReleasedOnRemove`: after `bob` leaves via unregister, and separately via slow drop, a later `bob` gets `bob` (shutdown releases too, but the room is gone) |
| Unit (hub) | `TestBroadcastFromNonMemberDropped`: after slow drop, `inbound{from: slow}` produces nothing (next `sync` is the next frame) |
| Unit (hub) | `ts` within `[before, after]` ms and identical across recipients |
| Integration | `TestConcurrentSameNickname`: 20 goroutines dial `bob` concurrently; welcome names pairwise distinct by `foldKey`, all ≤ 32 runes (`-race` catches unsynchronized `nickname` access) |
| Integration | `TestCaseInsensitiveCollision`: `Bob`, `bob`, `BOB` → `Bob`, `bob (2)`, `BOB (3)`; `Bob` leaves; new `bob` → `bob` |
| Integration | `TestEmptyMessagesDropped`: A sends `""`, `"   "`, `"\n\t"`, then `x`; B's next `chat` is `x`; A still connected |
| Integration | `TestForgedNoticeIsChat`: A sends `Bob joined! 🎉` and `\nBob: hi` → B receives `chat` from `A` with that exact text |
| Integration | First frame after dial is `welcome` (covered by the updated `dial` helper, which reads `welcome` and returns the assigned name) |

Frontend has no automated tests: manual check in two tabs (same nick → `(2)`, "vos" only on
own bubbles, join/leave lines, times shown, an unknown `type` sent via devtools is ignored).

## Migration / Rollout

Breaking wire change, but server and page ship in one binary and nothing is persisted. Open
tabs from the old version would render raw JSON until reloaded (they parse with the old
regex); acceptable — no external clients exist. Deploy = replace binary. Rollback = revert the
change's commits; clients reload. Specs (`openspec/specs/chat/spec.md`) are updated by the
specs phase; refresh `openspec/project-map.yaml` after apply (api_surface description).

## Open Questions

All resolved during spec ↔ design reconciliation (2026-10-22):

- [x] **U+061C (Arabic Letter Mark)**: stripped via `unicode.Bidi_Control`; spec CHAT-09 lists it.
- [x] **Own `join` echo**: kept — spec CHAT-02 sends `join` to the newcomer too, after its `welcome`.
- [x] **`text` on non-chat types**: always `""`, no `omitempty` — spec CHAT-10.
- [x] **Other invisible characters in nicknames** (U+200B, U+FEFF, U+2028/2029, other Cf): out
      of scope; revisit with homoglyphs / `frontend-hardening`.
- [x] **README**: updated in this change (task 3.3).
- [x] **Trailing whitespace before suffix**: spec CHAT-12 adopts the design's trim of the
      truncated base (scenario "Truncation lands on a space").
