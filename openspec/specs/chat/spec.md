# Chat Specification

## Purpose

Behavior of each chat room hub and of each client connection: registration, message
broadcast, direct messages, presence, history, disconnection, connection limits and liveness.
The server runs a fixed set of rooms (SRV-06). Each room is an independent hub, and every
requirement in this spec applies **within one room** (CHAT-19). A client connection belongs
to exactly one room, and a user is in several rooms by opening one connection per room
(CHAT-20). Wire protocol: inbound frames (client → server) are UTF-8 text, either plain text
containing only a room message or a JSON envelope for a room message or a direct message
(CHAT-21); outbound frames (server → client) are JSON objects built only by the server: event
envelopes `{"type","from","text","ts"}` (CHAT-10), the `users` (CHAT-17) and `history`
(CHAT-18) snapshot frames, and the `dm` and `dm_error` frames (CHAT-22, CHAT-23). Frames
carry no room field; the connection identifies the room. Nicknames are normalized (CHAT-09)
and unique case-insensitively within a room (CHAT-12).

## Requirements

### Requirement: CHAT-01 Broadcast delivery

The system MUST deliver every non-empty chat message (CHAT-13) received from a registered
client to all registered clients, including the sender, as a `chat` envelope (CHAT-10)
whose `from` is the sender's assigned nickname and whose `text` is the received text.

#### Scenario: Message reaches every client

- GIVEN clients `A` and `B` are connected to the room
- WHEN `A` sends the text frame `hola`
- THEN `A` and `B` each receive `type` = `chat`, `from` = `A`, `text` = `hola`

### Requirement: CHAT-02 Join and leave notices

The system MUST broadcast a `join` envelope to all registered clients, including the
newcomer, when a client registers, and a `leave` envelope when a registered client
disconnects or is dropped as a slow client (CHAT-05); `from` is the affected client's
assigned nickname. For the newcomer, its `join` MUST arrive after its `welcome`, `users` and
`history` frames (CHAT-11, CHAT-17, CHAT-18). A leave notice MUST be broadcast at most once
per client, and never to the client that left. Room shutdown (CHAT-08) is silent: no leave
notice is broadcast for it. A newcomer dropped before its `join` was broadcast gets no leave
notice either.

#### Scenario: Join notice

- GIVEN client `A` is connected
- WHEN client `B` connects with nickname `B`
- THEN `A` and `B` receive `type` = `join`, `from` = `B`
- AND `B` receives it after its `welcome`, `users` and `history`

#### Scenario: Leave notice exactly once

- GIVEN clients `A` and `B` are connected
- WHEN `B` closes its connection
- THEN `A` receives `type` = `leave`, `from` = `B` exactly once

#### Scenario: Slow-client drop is announced once

- GIVEN clients `A` and a slow client `S` are connected
- WHEN `S` is dropped for a full outbound buffer and its connection then closes
- THEN `A` receives `type` = `leave`, `from` = `S` exactly once

#### Scenario: Cascading drops

- GIVEN clients `A`, and slow clients `S1` and `S2` whose buffers are full
- WHEN a message is broadcast and both are dropped
- THEN `A` receives one `leave` for `S1` and one for `S2`
- AND the hub keeps processing events

### Requirement: CHAT-03 Hub never blocks on itself

The hub MUST NOT send to any channel that it alone consumes. Joins, leaves and broadcasts
MUST continue to be processed while the broadcast queue is saturated.

#### Scenario: Saturated broadcast queue during joins

- GIVEN the broadcast queue is at capacity
- WHEN new clients connect and existing clients disconnect concurrently
- THEN every registration and unregistration completes
- AND the hub keeps delivering messages (no deadlock)

### Requirement: CHAT-04 Client identity is independent of nickname

The system MUST identify clients by connection, not by nickname. Because requested
nicknames may collide, clients requesting the same nickname MUST all be admitted and
coexist without affecting each other, each under a distinct assigned nickname (CHAT-12).

#### Scenario: Two clients request the same nickname

- GIVEN two clients connected without a nickname, assigned `Anonymous` and `Anonymous (2)`
- WHEN a third client sends `hi`
- THEN both of them receive the message

#### Scenario: One duplicate leaves

- GIVEN clients assigned `Anonymous` and `Anonymous (2)`
- WHEN `Anonymous` disconnects
- THEN `Anonymous (2)` remains registered, keeps its nickname and keeps receiving messages

### Requirement: CHAT-05 Clean client teardown

Every client removal path (client disconnect, slow-client drop, read error, server shutdown)
MUST remove the client from the registry exactly once and MUST terminate both per-connection
goroutines. The system MUST NOT close a client's outbound queue more than once nor send on a
closed queue.

#### Scenario: No goroutine leak after disconnect

- GIVEN N clients connect and then disconnect
- WHEN all disconnections have been processed
- THEN no per-client goroutine remains running

#### Scenario: Slow client is dropped, others unaffected

- GIVEN client S never reads and its outbound buffer is full
- WHEN another message is broadcast
- THEN S is removed and its connection is closed
- AND the remaining clients receive the message, followed by `leave` for S (CHAT-02)
- AND S's later disconnection does not produce a second removal, a second leave or a panic

### Requirement: CHAT-06 Message size limit

The system MUST reject inbound frames larger than 4096 bytes by closing the offending
connection. The oversized message MUST NOT be broadcast. The limit applies to inbound
frames only; outbound envelopes MAY exceed 4096 bytes.

#### Scenario: Oversized frame

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends a frame larger than 4096 bytes
- THEN `A`'s connection is closed
- AND `B` does not receive `A`'s message
- AND `B` receives `type` = `leave`, `from` = `A`

#### Scenario: Message at the limit

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends a frame of exactly 4096 bytes made of characters that need JSON escaping
  (e.g. `"`)
- THEN `B` receives a `chat` envelope whose decoded `text` equals the sent frame

### Requirement: CHAT-07 Liveness (ping/pong)

The system MUST send periodic ping frames to each client and MUST close connections that do
not answer with a pong (or any frame) within the pong wait period; every received frame,
data or pong, MUST extend the read deadline. Each outbound write MUST
have a deadline so a stalled peer cannot block its writer indefinitely.

#### Scenario: Unresponsive peer is disconnected

- GIVEN a client that stops responding to pings
- WHEN the pong wait period elapses
- THEN its connection is closed and it is removed from the room

#### Scenario: Responsive idle peer stays connected

- GIVEN a client that sends no messages but answers pings
- WHEN several ping periods elapse
- THEN the client remains connected

### Requirement: CHAT-08 Room shutdown

When its context is cancelled, the room MUST stop processing, close every client connection
and MUST NOT leave goroutines blocked trying to register, unregister or broadcast.

#### Scenario: Shutdown with connected clients

- GIVEN clients A and B are connected
- WHEN the room context is cancelled
- THEN both connections are closed by the server
- AND the hub goroutine returns

#### Scenario: Connect during shutdown

- GIVEN the room context has been cancelled
- WHEN a new WebSocket connection arrives
- THEN the handler returns without blocking and the connection is closed

### Requirement: CHAT-09 Only valid UTF-8 text is relayed

The system MUST only accept text frames containing valid UTF-8 from clients. A binary frame
or a text frame with invalid UTF-8 MUST close the sender's connection (close code 1003 for
binary, 1007 for invalid UTF-8) and MUST NOT be broadcast. Requested nicknames MUST be
normalized before use, in this order:

1. invalid UTF-8 sequences replaced;
2. C0 controls (U+0000–U+001F), DEL (U+007F), C1 controls (U+0080–U+009F) and bidi controls
   (U+061C, U+200E, U+200F, U+202A–U+202E, U+2066–U+2069) removed; other characters, including
   ZWJ (U+200D), kept;
3. surrounding whitespace trimmed;
4. truncated to 32 runes;
5. an empty result replaced by `Anonymous`.

The result is then made unique (CHAT-12).

#### Scenario: Binary frame rejected

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends a binary frame
- THEN `A`'s connection is closed
- AND `B` does not receive the payload

#### Scenario: Invalid UTF-8 text rejected

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends a text frame containing byte 0xff
- THEN `A`'s connection is closed
- AND `B` does not receive the payload

#### Scenario: Nickname normalization

- GIVEN a client connects with `?nickname=%ff` or a nickname longer than 32 runes
- WHEN it joins
- THEN its `welcome` and `join` carry a valid UTF-8 nickname of at most 32 runes

#### Scenario: Control and bidi characters stripped

- GIVEN a client connects with a nickname from this table
- WHEN it joins
- THEN its assigned nickname is the expected value

| Requested (escaped)           | Assigned       |
|-------------------------------|----------------|
| `Bob\nAdmin`                  | `BobAdmin`     |
| `\tAlice\r`                   | `Alice`        |
| `Eve‮evil`               | `Eveevil`      |
| `⁦Mallory⁩`         | `Mallory`      |
| `Zoe\u0085`                   | `Zoe`          |
| `Ali\u061Cce`                 | `Alice`        |
| `👩‍💻`                  | `👩‍💻` (unchanged) |
| `\n‮\t`                  | `Anonymous`    |

#### Scenario: Stripping happens before trimming

- GIVEN a client connects with `‮  Bob  ‬`
- WHEN it joins
- THEN its assigned nickname is `Bob`

#### Scenario: Default nickname

- GIVEN no other client is connected
- WHEN a client connects without a nickname (or only whitespace)
- THEN its `welcome` and `join` carry `from` = `Anonymous`

### Requirement: CHAT-10 Outbound JSON envelope

Every event frame the server sends to a client — `chat`, `join`, `leave` and `welcome` —
MUST be a text frame containing a single JSON object with exactly these fields:

| Field  | JSON type | Value |
|--------|-----------|-------|
| `type` | string    | one of `chat`, `join`, `leave`, `welcome` |
| `from` | string    | the server-assigned nickname of the client the event is about |
| `text` | string    | the message text for `chat`; the empty string for every other type |
| `ts`   | number    | server time when the event was produced, as integer Unix milliseconds |

The only other data frames are the `users` (CHAT-17) and `history` (CHAT-18) snapshots and
the `dm` (CHAT-22) and `dm_error` (CHAT-23) frames, whose shapes are defined there; each
entry of `history.messages` is a `chat` envelope with exactly the fields above.

The envelope MUST be produced only by the server. `from` MUST always be the nickname the
server assigned to the originating connection and MUST NOT be taken from any client
payload. Message text MUST be carried only in `text` and MUST NOT alter `type` or `from`,
whatever characters it contains.

#### Scenario: Chat envelope

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends the text frame `hola`
- THEN `A` and `B` each receive a frame that decodes to `type` = `chat`, `from` = `A`,
  `text` = `hola`
- AND its `ts` is an integer between the Unix-millisecond times taken just before the send
  and just after the receipt

#### Scenario: Forged system notice is just chat

- GIVEN clients `A` and `Bob` are connected
- WHEN `A` sends the text frame `Bob joined! 🎉`
- THEN every client receives `type` = `chat`, `from` = `A`, `text` = `Bob joined! 🎉`
- AND no `join` envelope is produced

#### Scenario: Forged sender is just chat

- GIVEN clients `A` and `Bob` are connected
- WHEN `A` sends the text frame `\nBob: hi` (a leading newline)
- THEN every client receives `type` = `chat`, `from` = `A`, `text` = `\nBob: hi`

#### Scenario: Characters that need JSON escaping

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends `say "hi" \ <b>` followed by a tab and a newline
- THEN `B` receives a valid JSON frame whose decoded `text` equals the sent text exactly

### Requirement: CHAT-11 Welcome message

When a client registers, the server MUST send that client — and only that client — a
`welcome` envelope whose `from` is the nickname the server assigned to it (which MAY differ
from the requested one, see CHAT-09 and CHAT-12). The `welcome` envelope MUST be the first
data frame the client receives, followed by its `users` (CHAT-17) and `history` (CHAT-18)
frames.

#### Scenario: Welcome is first and private

- GIVEN client `A` is connected
- WHEN client `B` connects with `?nickname=B`
- THEN the first data frame `B` receives is `type` = `welcome`, `from` = `B`
- AND `A` never receives a `welcome` envelope

#### Scenario: Welcome carries the adjusted nickname

- GIVEN a client named `Bob` is connected
- WHEN a client connects with `?nickname=bob`
- THEN its first data frame is `type` = `welcome`, `from` = `bob (2)`

### Requirement: CHAT-12 Unique nicknames

Nicknames of connected clients MUST be unique under case-insensitive comparison using
Unicode case folding (so `Bob`, `bob` and `BOB` are the same name). The casing the client
requested MUST be preserved in the assigned nickname. When the normalized requested
nickname (CHAT-09) is already taken, the server MUST assign `<base> (<n>)`, where `n` is the
lowest integer ≥ 2 that yields a free name, and `<base>` is the normalized nickname
truncated so the result is at most 32 runes, with trailing whitespace of the truncated base
removed. A nickname MUST become available again as soon
as its owner is removed from the room, by any removal path.

#### Scenario: Case-insensitive collision

- GIVEN a client named `Bob` is connected
- WHEN clients request `bob` and then `BOB`
- THEN they are assigned `bob (2)` and `BOB (3)` respectively

#### Scenario: Lowest free suffix is reused

- GIVEN clients `Bob`, `Bob (2)` and `Bob (3)` are connected
- WHEN `Bob (2)` disconnects and a new client requests `Bob`
- THEN the new client is assigned `Bob (2)`

#### Scenario: Name is freed when its owner leaves

- GIVEN a client named `Bob` is connected
- WHEN it disconnects and a new client then requests `bob`
- THEN the new client is assigned `bob`

#### Scenario: Suffix respects the length limit

- GIVEN a client is connected with a 32-rune nickname `N` whose 28th rune is not whitespace
- WHEN another client requests the same 32-rune nickname
- THEN the assigned nickname is the first 28 runes of `N` followed by ` (2)`
- AND it is exactly 32 runes long

#### Scenario: Truncation lands on a space

- GIVEN a client is connected with the 32-rune nickname `aaaaaaaaaaaaaaaaaaaaaaaaaaa bbbb`
  (27 `a`, a space, 4 `b`)
- WHEN another client requests the same nickname
- THEN it is assigned `aaaaaaaaaaaaaaaaaaaaaaaaaaa (2)` (31 runes)

#### Scenario: Requested name equal to an assigned suffixed name

- GIVEN clients `Bob` and `Bob (2)` are connected
- WHEN a client requests `bob (2)`
- THEN it is assigned `bob (2) (2)`

#### Scenario: Default nickname collides

- GIVEN a client connected without a nickname (assigned `Anonymous`)
- WHEN another client connects without a nickname
- THEN it is assigned `Anonymous (2)`

### Requirement: CHAT-13 Empty messages are ignored

The server MUST silently discard inbound text frames that are empty or consist only of
whitespace, and room or direct messages whose `text` (after CHAT-21 classification) is empty
or consists only of whitespace. Discarding MUST NOT close the connection and MUST NOT produce
any frame (not even a `dm_error`). Non-empty messages MUST be relayed with their text
unchanged (no trimming).

#### Scenario: Whitespace-only message

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends an empty frame, then a frame of spaces, tabs and newlines, then `hi`
- THEN `B` receives exactly one `chat` envelope, with `text` = `hi`
- AND `A`'s connection remains open

#### Scenario: Whitespace-only envelope text

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends `{"type":"chat","text":"  "}`, then `{"type":"dm","to":"B","text":"\n"}`,
  then `hi`
- THEN `A` and `B` each receive exactly one frame, the `chat` envelope `hi`

#### Scenario: Surrounding whitespace is preserved

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends `  hi  `
- THEN `B` receives `text` = `  hi  `

### Requirement: CHAT-14 Chat page interprets envelopes

The chat page served at `GET /` MUST interpret each received frame by its `type`: render
`chat` as a message from `from` with the time taken from `ts`; render `join` and `leave` as
system notices about `from` and update the online list (CHAT-17); take its own identity from
`welcome`; handle `users` (CHAT-17), `history` (CHAT-18), `dm` and `dm_error` (CHAT-25). It
MUST mark a message as its own only when `from` equals the nickname received in `welcome`.
It MUST ignore frames that are not valid JSON, whose `type` it does not know, or whose fields
do not have the expected JSON types, without breaking later frames. It MUST render all
server-supplied strings as text, never as HTML.

#### Scenario: Own messages after renaming

- GIVEN a page connected as `bob` while `Bob` is already connected
- WHEN it receives `welcome` with `from` = `bob (2)`
- THEN only `chat` envelopes with `from` = `bob (2)` are marked as its own
- AND messages from `Bob` are shown as someone else's

#### Scenario: Forged notice renders as chat

- GIVEN the page is connected
- WHEN it receives `type` = `chat`, `from` = `A`, `text` = `Bob joined! 🎉`
- THEN it renders a chat message from `A`, not a system notice

#### Scenario: Unknown type

- GIVEN the page is connected
- WHEN it receives `{"type":"typing","from":"A","text":"","ts":1}` and then a `chat` envelope
- THEN the first frame renders nothing
- AND the `chat` envelope renders normally

### Requirement: CHAT-15 Inline images only from allowlisted hosts

The chat page MUST render a `chat` message as an inline image only when its whole `text` is
an image URL whose scheme is `https:`, whose host is exactly one of the hosts on the image
allowlist, and whose path ends in `.gif`, `.webp`, `.png`, `.jpg` or `.jpeg`
(case-insensitive; a query string is allowed). Every other message — including image URLs
on any other host, `http:` URLs and look-alike hosts (`media.giphy.com.evil.example`,
`evilgiphy.com`) — MUST be rendered as plain text, and the page MUST NOT request it.

The initial allowlist is `media.giphy.com`, `media0.giphy.com` … `media4.giphy.com`,
`i.giphy.com`, `media.tenor.com` and `i.imgur.com`. The page and the CSP `img-src`
directive (SRV-05) MUST use the same list.

The GIF panel MUST enable its send button only for URLs that pass the same check.

#### Scenario: Allowlisted GIF renders inline

- GIVEN the page is connected
- WHEN it receives a `chat` envelope with `text` = `https://media.giphy.com/media/abc/giphy.gif`
- THEN it renders an image loaded from that URL

#### Scenario: Unknown host renders as text

- GIVEN the page is connected
- WHEN it receives a `chat` envelope with `text` = `https://evil.example/x.gif`
- THEN it renders the URL as plain text
- AND the browser makes no request to `evil.example`

#### Scenario: Look-alike host

- GIVEN the page is connected
- WHEN it receives `chat` envelopes with `text` = `https://media.giphy.com.evil.example/x.gif`
  and `https://evilgiphy.com/x.gif`
- THEN both render as plain text

#### Scenario: URL with surrounding text

- GIVEN the page is connected
- WHEN it receives a `chat` envelope with `text` = `mirá https://media.giphy.com/media/abc/giphy.gif`
- THEN it renders the whole text as plain text

#### Scenario: Not-allowlisted URL in the GIF panel

- GIVEN the GIF panel is open
- WHEN the user types `https://evil.example/x.gif`
- THEN the panel's send button stays disabled

### Requirement: CHAT-16 Page reconnects automatically

The page MUST consider itself connected (`online`) only after it receives `welcome`
(CHAT-11), not when the socket opens. When its WebSocket closes without the user having left,
the page MUST show the `reconnecting` state and retry automatically, requesting the nickname
the user originally typed. Retry delays MUST grow exponentially with random jitter, MUST NOT
exceed 30 seconds, and MUST go back to the initial delay after a `welcome` is received. The
page MUST keep retrying for as long as the user stays in the chat view; it has no terminal
`offline` state. It MAY retry sooner when the browser reports the network is back or the tab
becomes visible (cutting a pending wait short or replacing a retry socket still connecting),
but MUST NOT have more than one socket open at a time. The page MUST show at most
one system notice when the connection is lost and one when it is re-established, per outage;
the "re-established" notice MUST be shown after the replayed history (CHAT-18), so it is not
erased by it. While not `online` the send button and the GIF panel's send button MUST stay
disabled. After the user clicks "Salir" the page MUST NOT reconnect, and any pending retry
MUST be cancelled. On reconnection the messages on screen MUST be replaced by the server's
history (CHAT-18) and the online list by the new `users` snapshot (CHAT-17), so no message is
shown twice and no departed user stays listed.

On reconnection the page's identity MUST come from the new `welcome` (CHAT-14); it MAY differ
from the previous one (e.g. `Lucía (2)` while the server still holds the old connection).

#### Scenario: Socket opens but is closed before welcome

- GIVEN a page in the `reconnecting` state
- WHEN a retry's socket opens and the server closes it before sending `welcome`
- THEN the page stays `reconnecting`, shows no "reconnected" notice
- AND the next retry waits longer than the previous one

#### Scenario: Server restart

- GIVEN two pages are connected as `A` and `B`
- WHEN the server is stopped and started again
- THEN both pages show the reconnecting state and one "connection lost" notice
- AND within 30 seconds of the server being back both are connected again without a reload
- AND each shows one "reconnected" notice
- AND `A` can send a message that `B` receives

#### Scenario: Short outage without duplicates

- GIVEN a page connected as `A` showing messages `uno` and `dos`
- WHEN its connection drops, `B` sends `tres` meanwhile, and the page reconnects
- THEN it shows `uno`, `dos` and `tres` exactly once each, followed by the "reconnected"
  notice

#### Scenario: Backoff while the server is down

- GIVEN a connected page
- WHEN the server stays down for 2 minutes
- THEN the page keeps retrying with growing delays, never waiting more than 30 seconds
  between attempts
- AND it shows no additional system notices while retrying

#### Scenario: Leaving cancels reconnection

- GIVEN a page in the reconnecting state
- WHEN the user clicks "Salir"
- THEN no further connection attempts are made

#### Scenario: Leaving while connected

- GIVEN a connected page
- WHEN the user clicks "Salir"
- THEN the connection closes and the page does not reconnect

#### Scenario: Renamed after reconnecting

- GIVEN a page that joined as `Lucía` lost its connection
- WHEN it reconnects and receives `welcome` with `from` = `Lucía (2)`
- THEN the header shows `Lucía (2)`
- AND only `chat` envelopes from `Lucía (2)` are marked as its own

### Requirement: CHAT-17 Online users list

When a client registers, the server MUST send that client — and only that client — one
`users` frame right after its `welcome` (CHAT-11). The frame is a JSON object with exactly
these fields:

| Field   | JSON type        | Value |
|---------|------------------|-------|
| `type`  | string           | `users` |
| `users` | array of strings | assigned nicknames of every registered client, including the newcomer |
| `ts`    | number           | server time, integer Unix milliseconds |

`users` MUST be sorted case-insensitively and contain each registered client exactly once.
After the snapshot, the list is kept current by `join` and `leave` envelopes (CHAT-02).

The chat page MUST show the number of connected users (`N en línea`) and, on request, the
list of their nicknames, marking its own nickname with the same "vos" tag used for its own messages. It MUST replace its list with
each `users` frame it receives, add a name on `join` and remove it on `leave`. Nicknames MUST
be rendered as text.

#### Scenario: Snapshot on join

- GIVEN clients `bob` and `Ana` are connected
- WHEN client `Carla` connects
- THEN the second data frame `Carla` receives is `type` = `users`, `users` = `["Ana", "bob", "Carla"]`
- AND `Ana` and `bob` receive no `users` frame

#### Scenario: First client sees only itself

- GIVEN the room is empty
- WHEN client `A` connects
- THEN `A` receives `type` = `users`, `users` = `["A"]`

#### Scenario: List follows joins and leaves

- GIVEN a page connected as `A` showing `1 en línea`
- WHEN it receives `join` for `B` and then `leave` for `B`
- THEN it shows `2 en línea` with `A` (tagged "vos") and `B`, and then `1 en línea` with only `A`

#### Scenario: Dropped slow client leaves the list

- GIVEN clients `A`, `B` and a slow client `S` are connected
- WHEN `S` is dropped for a full outbound buffer
- THEN `A` and `B` receive `type` = `leave`, `from` = `S`
- AND `S` no longer appears in a later client's `users` snapshot

### Requirement: CHAT-18 Recent message history

The server MUST keep, in memory, the last 50 `chat` envelopes it relayed (CHAT-01), oldest
first; when a 51st is relayed the oldest MUST be discarded. `join`, `leave`, `welcome`,
`users`, `dm` and `dm_error` frames MUST NOT be kept (CHAT-24). History is lost when the
server restarts.

When a client registers, the server MUST send that client — and only that client — one
`history` frame right after its `users` frame and before its own `join`. The frame is a JSON
object with exactly these fields:

| Field      | JSON type        | Value |
|------------|------------------|-------|
| `type`     | string           | `history` |
| `messages` | array of objects | the kept `chat` envelopes (CHAT-10), oldest first, unchanged (same `from`, `text`, `ts`) |
| `ts`       | number           | server time, integer Unix milliseconds |

`messages` MAY be empty. If any of the newcomer's `welcome`, `users` or `history` frames
cannot be queued, the newcomer is dropped and no `join` is broadcast for it.

The chat page MUST, on each `history` frame, replace the room messages on screen with the
frame's messages, rendered like live `chat` envelopes (CHAT-14, CHAT-15), including marking
its own messages by the nickname from the latest `welcome`. Direct conversations (CHAT-25)
MUST NOT be cleared by a `history` frame.

#### Scenario: Newcomer receives recent messages

- GIVEN client `A` is connected and has sent `uno` and then `dos`
- WHEN client `B` connects
- THEN `B` receives `welcome`, `users`, then `history` with `messages` = the `chat`
  envelopes `uno` and `dos` from `A`, in that order, then its own `join`

#### Scenario: Empty history

- GIVEN no chat message has been relayed since the server started
- WHEN client `A` connects
- THEN `A` receives `type` = `history`, `messages` = `[]`

#### Scenario: Only the last 50 messages are kept

- GIVEN 51 chat messages `m1` … `m51` have been relayed
- WHEN a client connects
- THEN its `history` contains `m2` … `m51` in order

#### Scenario: Notices and DMs are not kept

- GIVEN client `A` connected, sent `hola` and a DM to `B`, and client `B` then left
- WHEN client `C` connects
- THEN its `history` contains only the `chat` envelope `hola`

#### Scenario: Replayed message keeps its original data

- GIVEN `A` sent `hola` at time `t` and then disconnected
- WHEN `B` connects
- THEN `B`'s `history` contains `type` = `chat`, `from` = `A`, `text` = `hola`, `ts` = `t`

### Requirement: CHAT-19 Room isolation

Every room MUST keep its own registered clients, nickname set, online users list and message
history, and MUST NOT share any of them with another room. In CHAT-01 to CHAT-18, "the room",
"registered clients", "connected" and "history" refer only to the room the client's
connection belongs to. In particular:

- a `chat`, `join` or `leave` envelope MUST be delivered only to the clients of the room where
  it happened;
- a `users` frame MUST list only the clients of that room;
- a `history` frame MUST contain only messages sent in that room, up to the last 50 (CHAT-18);
- nickname uniqueness (CHAT-12) MUST be enforced per room. The same nickname MAY be assigned
  in different rooms, and the same requested nickname MAY be assigned different suffixes in
  different rooms.

A connection MUST NOT change rooms; to be in another room the client opens another
connection.

#### Scenario: Messages stay in their room

- GIVEN `Ana` is connected to `general` and `Bob` is connected to `juegos`
- WHEN `Ana` sends `hola`
- THEN `Ana` receives `type` = `chat`, `text` = `hola`
- AND `Bob` receives no frame

#### Scenario: Users list per room

- GIVEN `Ana` and `Bob` are connected to `general` and `Carla` is connected to `juegos`
- WHEN `Dani` connects to `juegos`
- THEN the `users` frame `Dani` receives is `["Carla", "Dani"]`

#### Scenario: History per room

- GIVEN `general` has history `[a, b]` and `juegos` has history `[c]`
- WHEN a client connects to `juegos`
- THEN its `history` frame contains only `c`

#### Scenario: Same nickname in two rooms

- GIVEN `Ana` is connected to `general`
- WHEN another client connects to `juegos` requesting nickname `Ana`
- THEN its `welcome` has `from` = `Ana` (no suffix)

#### Scenario: Join and leave are room-local

- GIVEN `Ana` is connected to `general` and `juegos` (two connections), and `Bob` only to `general`
- WHEN `Ana` closes her `juegos` connection
- THEN no `leave` envelope is sent to `Bob`
- AND `Ana`'s `general` connection stays open

### Requirement: CHAT-20 Chat page joins several rooms

After the user enters a nickname, the chat page MUST fetch the room list (SRV-07), connect to
the default room (the first room in the list) and make it the active room. The page MUST:

- show every room of the list by name, marking which rooms are joined, which one is active,
  and, for each joined but inactive room, the number of unread `chat` messages received since
  it was last active (shown only when > 0);
- when the user selects a room that is not joined, open one connection to `/ws` for that room
  with the same requested nickname, and make it active;
- when the user selects a joined room, make it active and reset its unread count to 0;
- keep, per joined room: its connection, its assigned nickname (from that connection's
  `welcome`), its message list, its online users list and its connection status. Frames
  received on a connection MUST only affect that room's state;
- show the active room's name, connection status, online count and users popover (CHAT-17),
  and the active room's message list only. The "vos" tag MUST use the nickname assigned in
  that room;
- send the composed message only on the active room's connection;
- let the user leave the active room ("Dejar sala") when more than one room is joined:
  close only its connection, discard its state, and activate another joined room. Leaving
  MUST NOT be offered while only one room is joined;
- on "Salir", close every connection and return to the nickname screen.

Room names MUST be rendered as text. Room ids received from `/rooms` that are not
lowercase ASCII letters, digits or `-` (1–32 chars) MUST be ignored. Automatic reconnection
(CHAT-16), including its history re-sync and "Reconectado" notice, MUST apply independently
to each joined room's connection; a room left by the user MUST NOT reconnect. History
replayed on (re)connect MUST NOT count as unread. If the room list cannot be fetched (after
one retry), the page MUST stay on the nickname screen and show an error message instead of
entering the chat.

#### Scenario: Enter the chat

- GIVEN the server rooms are `general`, `juegos`, `musica`
- WHEN the user enters nickname `Ana`
- THEN the page shows the three rooms, with `general` joined and active
- AND the header shows `general`

#### Scenario: Join a second room

- GIVEN `Ana` is in `general` (active)
- WHEN she selects `juegos`
- THEN a second connection to `juegos` is opened with nickname `Ana`
- AND `juegos` becomes active, showing its own history and users
- AND the `general` connection stays open

#### Scenario: Unread count

- GIVEN `Ana` is in `general` and `juegos`, with `juegos` active
- WHEN two `chat` messages arrive in `general`
- THEN `general` shows an unread count of 2
- AND when she selects `general` the count disappears and both messages are shown

#### Scenario: Different nickname per room

- GIVEN `Ana` is assigned `Ana (2)` in `juegos` and `Ana` in `general`
- WHEN `juegos` is active
- THEN her own messages there are tagged "vos" and the header shows `Ana (2)`

#### Scenario: Leave one room

- GIVEN `Ana` is in `general` and `juegos`, with `juegos` active
- WHEN she leaves `juegos`
- THEN only the `juegos` connection is closed
- AND `general` becomes active and still receives messages

#### Scenario: Cannot leave the last room

- GIVEN `Ana` is only in `general`
- THEN "Dejar sala" is disabled and only "Salir" leaves the chat

#### Scenario: Room list unavailable

- GIVEN `GET /rooms` fails twice
- WHEN the user submits a nickname
- THEN the page stays on the nickname screen and shows "No pudimos cargar las salas. Probá de nuevo."
- AND no WebSocket is opened

#### Scenario: Reconnect one room

- GIVEN `Ana` is in `general` and `juegos`
- WHEN only the `juegos` connection drops
- THEN only `juegos` shows the disconnected status and reconnects
- AND after reconnecting, `juegos`'s message list is replaced by its history, followed by
  "Reconectado", while `general` is unchanged

### Requirement: CHAT-21 Inbound envelope

After the size (CHAT-06) and binary / UTF-8 (CHAT-09) checks, the server MUST classify each
inbound text frame as follows:

1. If the frame decodes as a JSON object whose `type` is the string `dm`, it is a **direct
   message attempt** (CHAT-22). It is valid only if `to` is a JSON string that is non-empty
   after nickname normalization steps 1–4 of CHAT-09 (no `Anonymous` default) and `text` is
   a JSON string. An invalid attempt MUST produce a `dm_error` with reason `invalid`
   (CHAT-23).
2. Otherwise, if the frame decodes as a JSON object whose `type` is the string `chat` and
   whose `text` is a JSON string, it is a **room message** with that `text`.
3. Otherwise (not JSON, not an object, missing or unknown `type`, or a `chat` object whose
   `text` is not a string) it is a **room message** whose text is the whole frame, unchanged.

Fields other than `type`, `to` and `text` MUST be ignored. Room messages are relayed per
CHAT-01. The blank-text rule (CHAT-13) applies to the resulting `text` of both kinds.

#### Scenario: Plain text stays room chat

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends the text frame `hola`
- THEN `A` and `B` receive `type` = `chat`, `from` = `A`, `text` = `hola`

#### Scenario: Chat envelope

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends `{"type":"chat","text":"hola"}`
- THEN `A` and `B` receive `type` = `chat`, `from` = `A`, `text` = `hola`

#### Scenario: JSON that is not an envelope is plain text

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends, one at a time, `{"type":"typing"}`, `[1,2]`, `{"text":"x"}` and
  `{"type":"chat","text":5}`
- THEN `B` receives four `chat` envelopes whose `text` equals each sent frame exactly

#### Scenario: Envelope cannot forge the sender

- GIVEN clients `A` and `Bob` are connected
- WHEN `A` sends `{"type":"chat","from":"Bob","text":"hi"}`
- THEN every client receives `type` = `chat`, `from` = `A`, `text` = `hi`

#### Scenario: Invalid direct message

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends, one at a time, `{"type":"dm","text":"x"}`, `{"type":"dm","to":"   ","text":"x"}`,
  `{"type":"dm","to":7,"text":"x"}` and `{"type":"dm","to":"B","text":null}`
- THEN `A` receives four `dm_error` frames with `reason` = `invalid` and `to` = `""`
- AND `B` receives no frame
- AND `A`'s connection remains open

### Requirement: CHAT-22 Direct message delivery

For a valid direct message attempt from a registered client (the sender), the server MUST
look up the recipient by comparing the normalized `to` case-insensitively (as in CHAT-12)
with the **assigned** nicknames of the room's registered clients. If the recipient is a
different registered client, the server MUST send exactly one `dm` frame to the recipient and
the same frame to the sender, and to no other client. The frame is a JSON object with exactly
these fields:

| Field  | JSON type | Value |
|--------|-----------|-------|
| `type` | string    | `dm` |
| `from` | string    | the sender's assigned nickname (never taken from the payload) |
| `to`   | string    | the recipient's assigned nickname |
| `text` | string    | the received `text`, unchanged |
| `ts`   | number    | server time when the DM was routed, integer Unix milliseconds; identical in both copies |

Delivery MUST follow the same non-blocking rules as every other outbound frame (CHAT-03): a
copy that cannot be queued drops that client as a slow client (CHAT-05) and does not affect
the other copy. A direct message only reaches clients of the sender's room (CHAT-19).

#### Scenario: DM reaches recipient and is echoed

- GIVEN clients `A`, `B` and `C` are connected
- WHEN `A` sends `{"type":"dm","to":"B","text":"secreto"}`
- THEN `B` and `A` each receive `type` = `dm`, `from` = `A`, `to` = `B`, `text` = `secreto`
  with the same `ts`
- AND `C` receives no frame

#### Scenario: Recipient matched case-insensitively

- GIVEN clients `A` and `Bob` are connected
- WHEN `A` sends `{"type":"dm","to":"  bob ","text":"hi"}`
- THEN `Bob` receives `type` = `dm`, `to` = `Bob`

#### Scenario: Suffixed nickname is a distinct recipient

- GIVEN clients `A`, `Ana` and `Ana (2)` are connected
- WHEN `A` sends `{"type":"dm","to":"Ana (2)","text":"hi"}`
- THEN only `Ana (2)` and `A` receive the `dm`

#### Scenario: No cross-room delivery

- GIVEN `A` and `B` are connected to `general` and another `B` is connected to `juegos`
- WHEN `A` sends `{"type":"dm","to":"B","text":"hi"}` in `general`
- THEN only the `B` in `general` and `A` receive the `dm`

#### Scenario: Envelope cannot forge the DM sender

- GIVEN clients `A`, `B` and `C` are connected
- WHEN `A` sends `{"type":"dm","from":"C","to":"B","text":"hi"}`
- THEN `B` receives `from` = `A`

### Requirement: CHAT-23 Direct message errors

When a direct message cannot be delivered, the server MUST send the sender — and only the
sender — one `dm_error` frame, and MUST NOT send any frame to any other client. The frame is
a JSON object with exactly these fields:

| Field    | JSON type | Value |
|----------|-----------|-------|
| `type`   | string    | `dm_error` |
| `to`     | string    | the normalized requested recipient; `""` for `invalid` |
| `reason` | string    | `invalid` (CHAT-21), `self` (the recipient is the sender), or `offline` (no registered client in the room has that nickname) |
| `ts`     | number    | server time, integer Unix milliseconds |

A `dm_error` MUST NOT close the connection.

#### Scenario: Recipient not in the room

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends `{"type":"dm","to":"Zoe","text":"hi"}`
- THEN `A` receives `type` = `dm_error`, `to` = `Zoe`, `reason` = `offline`
- AND `B` receives no frame

#### Scenario: Recipient left

- GIVEN clients `A` and `B` are connected and `B` disconnects
- WHEN `A` sends `{"type":"dm","to":"B","text":"hi"}` after receiving `B`'s `leave`
- THEN `A` receives `dm_error` with `reason` = `offline`

#### Scenario: DM to yourself

- GIVEN client `Ana` is connected
- WHEN `Ana` sends `{"type":"dm","to":"ANA","text":"hi"}`
- THEN `Ana` receives `dm_error` with `to` = `ANA`, `reason` = `self`

### Requirement: CHAT-24 Direct message privacy

A direct message MUST be visible only to its sender and recipient. The server MUST NOT add
a direct message to the room history (CHAT-18), MUST NOT include it in any `chat`, `users`
or `history` frame, MUST NOT write its `text` or recipient to the logs, and MUST NOT keep its
content after routing it: direct messages are never stored or replayed by the server, and a
client that connects later (including one that takes the same nickname) receives no earlier
direct message.

#### Scenario: History has no trace of a DM

- GIVEN `A` sent the room message `hola` and then a DM `secreto` to `B`
- WHEN client `C` connects
- THEN `C`'s `history` contains only the `chat` envelope `hola`

#### Scenario: Reused nickname inherits nothing

- GIVEN `A` and `B` exchanged DMs and then `B` disconnected
- WHEN a new client connects as `B`
- THEN it receives no `dm` frame until someone sends it a new direct message

### Requirement: CHAT-25 Chat page direct messages

The chat page MUST let the user send direct messages to other members of a joined room:

- each nickname in that room's online list, except its own, MUST offer a "Mensaje privado"
  action that opens (or activates) a direct conversation with that nickname, shown as a tab
  labeled `@` + nickname in that room's tab group, with its own message pane and unread
  count;
- an incoming `dm` (sent by another user to this page's nickname) MUST be shown in the
  conversation with its `from`, opening one if needed and counting as unread when it is not
  the active pane; an echoed `dm` (whose `from` equals the latest `welcome` nickname) MUST be
  shown as its own message in the conversation with its `to`;
- the composer MUST send `{"type":"dm","to":<peer>,"text":…}` while a direct conversation is
  active and `{"type":"chat","text":…}` while a room is active;
- a `dm_error` MUST be shown as a system notice in the conversation with its `to` if one
  exists, otherwise in the active pane when it belongs to that room, or else in the room's
  own pane (never in another room's pane);
- when the peer's `leave` arrives, or the page's connection to that room is lost, the
  conversation MUST become closed: a notice is shown, its composer is disabled and its
  transcript stays on screen. Picking the peer again in the online list, or an incoming `dm`
  from that nickname, MUST reopen it after a notice that a new conversation starts (the
  nickname may now belong to someone else);
- leaving a room MUST remove that room's direct conversations;
- direct messages MUST be rendered like room messages (CHAT-14, CHAT-15). Nicknames and
  texts MUST be rendered only as text and MUST NOT be used in element ids, class names or
  selectors.

The transcript of a direct conversation is kept only in the page's memory.

#### Scenario: Open a conversation from the list

- GIVEN a page connected to `general` as `A`, with `B` online
- WHEN the user picks "Mensaje privado" on `B` and sends `hola`
- THEN a `@B` tab is active in `general`'s group
- AND the page sends `{"type":"dm","to":"B","text":"hola"}`
- AND, on the echoed `dm`, `hola` is shown in `@B` as its own message

#### Scenario: Incoming DM while in the room

- GIVEN a page connected to `general` as `B` with the `general` room tab active
- WHEN it receives a `dm` with `from` = `A`, `to` = `B`, `text` = `hi`
- THEN a `@A` tab appears with an unread count of 1
- AND the `general` pane shows nothing new

#### Scenario: Peer leaves

- GIVEN the page has an open `@B` conversation in `general`
- WHEN it receives `leave` for `B` in `general`
- THEN `@B` shows a disconnect notice, keeps its messages and its composer is disabled

#### Scenario: Leaving the room closes its DMs

- GIVEN the page has `@B` open in `general`
- WHEN the user leaves `general`
- THEN the `@B` tab is removed

#### Scenario: Own nickname has no DM action

- GIVEN a page connected as `A` with `B` online
- WHEN the user opens the online list
- THEN `B` offers "Mensaje privado" and `A` (tagged "vos") does not
