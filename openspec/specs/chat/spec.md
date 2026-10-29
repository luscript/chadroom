# Chat Specification

## Purpose

Behavior of each chat room hub and of each client connection: registration, message
broadcast, direct messages, typing indicators, presence, history, disconnection, connection
limits and liveness. The server runs a fixed set of rooms (SRV-06). Each room is an
independent hub, and every requirement in this spec applies **within one room** (CHAT-19). A
client connection belongs to exactly one room, and a user is in several rooms by opening one
connection per room (CHAT-20). Wire protocol: inbound frames (client → server) are UTF-8 text,
either plain text containing only a room message or a JSON envelope for a room message, a
direct message, a typing signal or a music control (CHAT-21, CHAT-30); outbound frames (server → client) are JSON
objects built only by the server: event envelopes `{"type","from","text","ts"}` with an optional `avatar` (CHAT-10, CHAT-28), the
`users` (CHAT-17) and `history` (CHAT-18) snapshot frames, the `dm` and `dm_error` frames
(CHAT-22, CHAT-23) the `typing` frame (CHAT-26) and the `music` state frame (CHAT-30). Frames carry no room field; the
connection identifies the room. Nicknames are normalized (CHAT-09) and unique
case-insensitively within a room (CHAT-12).

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

| Field    | JSON type | Value |
|----------|-----------|-------|
| `type`   | string    | one of `chat`, `join`, `leave`, `welcome` |
| `from`   | string    | the server-assigned nickname of the client the event is about |
| `text`   | string    | the message text for `chat`; the empty string for every other type |
| `ts`     | number    | server time when the event was produced, as integer Unix milliseconds |
| `avatar` | string    | **optional**: present only on `chat`, `join` and `welcome`, and only when the client the event is about has a profile picture (CHAT-28); its value is that picture's URL |

`avatar` MUST NOT appear on `leave` envelopes, and MUST NOT appear with an empty value.

The only other data frames are the `users` (CHAT-17) and `history` (CHAT-18) snapshots, the
`dm` (CHAT-22) and `dm_error` (CHAT-23) frames, the `typing` frame (CHAT-26) and the `music`
frame (CHAT-30), whose shapes are defined there; each entry of `history.messages` is a `chat`
envelope with exactly the fields above.

The envelope MUST be produced only by the server. `from` and `avatar` MUST always be the
nickname and profile picture the server assigned to the originating connection and MUST NOT be
taken from any message payload. Message text MUST be carried only in `text` and MUST NOT alter
`type`, `from` or `avatar`, whatever characters it contains.

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

#### Scenario: Chat envelope carries the sender's avatar

- GIVEN client `A` connected with a valid avatar `U` and client `B` connected without one
- WHEN `A` sends `hola` and then `B` sends `chau`
- THEN every client receives `type` = `chat`, `from` = `A`, `text` = `hola`, `avatar` = `U`
- AND then `type` = `chat`, `from` = `B`, `text` = `chau` with no `avatar` field

#### Scenario: Avatar cannot be forged in a message

- GIVEN client `A` is connected without an avatar
- WHEN `A` sends the inbound envelope `{"type":"chat","text":"hi","avatar":"https://i.imgur.com/x.png"}`
- THEN the relayed `chat` envelope has no `avatar` field

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
`users` frame right after its `welcome` (CHAT-11). The frame is a JSON object with these
fields:

| Field     | JSON type        | Value |
|-----------|------------------|-------|
| `type`    | string           | `users` |
| `users`   | array of strings | assigned nicknames of every registered client, including the newcomer |
| `avatars` | object           | **optional**: maps the nickname of each registered client that has a profile picture (CHAT-28) to its URL; omitted when no registered client has one |
| `ts`      | number           | server time, integer Unix milliseconds |

`users` MUST be sorted case-insensitively and contain each registered client exactly once.
Every key of `avatars` MUST appear in `users`. After the snapshot, the list is kept current by
`join` and `leave` envelopes (CHAT-02).

The chat page MUST show the number of connected users (`N en línea`) and, on request, the
list of their nicknames, marking its own nickname with the same "vos" tag used for its own
messages. It MUST replace its list with each `users` frame it receives, add a name on `join`
and remove it on `leave`. Nicknames MUST be rendered as text.

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

#### Scenario: Snapshot includes avatars

- GIVEN client `Ana` is connected with a valid avatar `U` and client `bob` without one
- WHEN client `Carla` connects without an avatar
- THEN `Carla` receives `type` = `users`, `users` = `["Ana", "bob", "Carla"]`,
  `avatars` = `{"Ana": U}`

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
2. Otherwise, if the frame decodes as a JSON object whose `type` is the string `typing`, it is
   a **typing signal** (CHAT-26): a **room typing signal** when `to` is absent, a **direct
   typing signal** when `to` is a JSON string that is non-empty after nickname normalization
   steps 1–4 of CHAT-09, and **invalid** otherwise. An invalid typing signal MUST be discarded
   without any reply. The blank-text rule (CHAT-13) does not apply to typing signals.
2a. Otherwise, if the frame decodes as a JSON object whose `type` is the string `music`, it is a
   **music control** (CHAT-30). A music control MUST NEVER be treated as a room message: if it is
   invalid, or the room has no shared player, it MUST be discarded without any reply.
3. Otherwise, if the frame decodes as a JSON object whose `type` is the string `chat` and
   whose `text` is a JSON string, it is a **room message** with that `text`.
4. Otherwise (not JSON, not an object, missing or unknown `type`, or a `chat` object whose
   `text` is not a string) it is a **room message** whose text is the whole frame, unchanged.

Fields other than `type`, `to` and `text` (and `action`, `video`, `videos`, `pos`, `id` and
`seq` in a music control) MUST be ignored (`text` is also ignored in a typing signal and a music control). Room messages are relayed per CHAT-01. The blank-text rule (CHAT-13) applies to the
resulting `text` of room messages and direct messages.

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
- WHEN `A` sends, one at a time, `{"type":"poke"}`, `[1,2]`, `{"text":"x"}` and
  `{"type":"chat","text":5}`
- THEN `B` receives four `chat` envelopes whose `text` equals each sent frame exactly

#### Scenario: Typing envelope is not chat

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends `{"type":"typing"}`
- THEN `B` receives a `typing` frame and no `chat` envelope

#### Scenario: Invalid typing signal is discarded

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends, one at a time with more than one second between them,
  `{"type":"typing","to":7}` and `{"type":"typing","to":"   "}`
- THEN neither `A` nor `B` receives any frame
- AND `A`'s connection remains open

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

#### Scenario: Music control is never chat

- GIVEN clients `A` and `B` are connected to a room **without** a shared player
- WHEN `A` sends `{"type":"music","action":"play"}` and `{"type":"music","action":"bogus"}`
- THEN neither `A` nor `B` receives any frame

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

### Requirement: CHAT-26 Typing signal delivery

For each valid typing signal (CHAT-21) from a registered client, the server MUST:

- for a **room typing signal**, send `{"type":"typing","from":<sender>,"ts":<server time>}` to
  every registered client of the room **except the sender**;
- for a **direct typing signal**, send `{"type":"typing","from":<sender>,"to":<recipient>,
  "ts":<server time>}` **only to the recipient**, found as in CHAT-22 (case-insensitive match
  within the room) with `to` set to the recipient's assigned nickname. When the recipient is
  the sender or is not in the room, the signal MUST be discarded without any reply (no
  `dm_error`).

`from` MUST be the sender's assigned nickname, never a value from the payload; `ts` uses the
CHAT-10 format. Typing signals MUST NOT be stored in room history (CHAT-18), MUST NOT be
logged, and MUST NOT be echoed to the sender.

Delivery is **best-effort**: when a recipient's outbound buffer is full, the typing frame MUST
be skipped for that recipient, and the recipient MUST NOT be disconnected nor announced as
leaving because of it.

The server MUST forward at most **one typing signal per connection per second**; additional
typing signals received within one second of the last forwarded one MUST be discarded
silently, and the connection stays open. Discarded signals do not reset the one-second
window. Typing signals still count as received frames for liveness (CHAT-07).

#### Scenario: Room typing goes to everyone else

- GIVEN clients `A`, `B` and `C` are connected
- WHEN `A` sends `{"type":"typing"}`
- THEN `B` and `C` each receive a frame with exactly the fields `type` = `typing`, `from` = `A`
  and `ts`
- AND `A` receives no frame

#### Scenario: Sender cannot be forged

- GIVEN clients `A` and `Bob` are connected
- WHEN `A` sends `{"type":"typing","from":"Bob"}`
- THEN `Bob` receives `type` = `typing`, `from` = `A`

#### Scenario: Direct typing goes to the recipient only

- GIVEN clients `A`, `B` and `C` are connected
- WHEN `A` sends `{"type":"typing","to":"b"}`
- THEN `B` receives a frame with exactly the fields `type` = `typing`, `from` = `A`,
  `to` = `B` and `ts`
- AND `A` and `C` receive no frame

#### Scenario: Direct typing to an absent user or to oneself

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends `{"type":"typing","to":"Zed"}` and, more than one second later,
  `{"type":"typing","to":"A"}`
- THEN no client receives any frame (no `dm_error`)

#### Scenario: Typing is not history

- GIVEN client `A` has sent `{"type":"typing"}` and `hola` in the room
- WHEN client `C` joins
- THEN `C`'s history contains `hola` and no typing entry

#### Scenario: Throttled

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends `{"type":"typing"}` twice within one second
- THEN `B` receives exactly one `typing` frame
- AND `A`'s connection remains open

#### Scenario: Full buffer does not disconnect

- GIVEN client `B`'s outbound buffer is full
- WHEN `A` sends a room typing signal
- THEN `B` is not removed from the room and no `leave` for `B` is sent

### Requirement: CHAT-27 Chat page typing indicator

The chat page MUST show who is typing in the visible pane and signal its own typing:

- while the draft is non-empty after trimming and the active pane can send (CHAT-25: room
  online, or direct conversation open), each draft edit (typing, pasting or inserting an emoji)
  MUST send `{"type":"typing"}` for a room pane or `{"type":"typing","to":<peer>}` for a direct
  conversation, at most once every **3 seconds** per pane. Sending a message, or switching to
  another pane, MUST allow the next edit to signal immediately;
- a received room `typing` frame MUST mark its `from` as typing in that room's pane; a
  received direct `typing` frame (with `to`) MUST mark its `from` as typing in the direct
  conversation with that nickname, only if that conversation exists and is open. Frames whose
  `from` is the page's own nickname MUST be ignored;
- a typist MUST stop being shown **5 seconds** after its latest `typing` frame, or as soon as
  a `chat` (room pane) or `dm` (direct pane) from it arrives, or on its `leave`, and all
  typists of a room and its conversations MUST be cleared when the connection to the room is
  lost or the room is left;
- a single line between the message list and the composer MUST show the visible pane's
  typists: `<A> está escribiendo…`, `<A> y <B> están escribiendo…`, or
  `Varias personas están escribiendo…` for three or more, and be empty otherwise. The line MUST
  keep its height when empty, MUST NOT be an ARIA live region, and nicknames MUST be rendered
  only as text (never in element ids, class names or selectors).

#### Scenario: Someone types in the room

- GIVEN a page showing `general` as `B`
- WHEN it receives `{"type":"typing","from":"A","ts":…}` on `general`
- THEN the indicator reads `A está escribiendo…`
- AND about 5 seconds later, with no further frame, it is empty

#### Scenario: Message clears the typist

- GIVEN the indicator shows `A está escribiendo…` in `general`
- WHEN a `chat` from `A` arrives on `general`
- THEN the indicator is empty

#### Scenario: Only the visible pane

- GIVEN a page showing `@A` in `general` as `B`
- WHEN it receives a room `typing` from `C` on `general`
- THEN the indicator is empty
- AND activating the `general` tab within 5 seconds shows `C está escribiendo…`

#### Scenario: Direct typing

- GIVEN a page with an open `@A` conversation in `general`, showing it
- WHEN it receives `{"type":"typing","from":"A","to":"B","ts":…}`
- THEN the indicator reads `A está escribiendo…`

#### Scenario: Own typing is throttled

- GIVEN a page connected to `general` with the room pane active
- WHEN the user types five characters within one second
- THEN the page sends exactly one `{"type":"typing"}`

#### Scenario: Several typists

- GIVEN the room pane shows typists `A` and `B`
- WHEN a room `typing` from `C` arrives
- THEN the indicator reads `Varias personas están escribiendo…`

### Requirement: CHAT-28 Profile picture

A client MAY request a profile picture with the `avatar` query parameter of `GET /ws`. The
server MUST accept it only when it is an **allowed avatar URL**:

- at most 512 bytes;
- an absolute URL whose scheme is `https`;
- whose host is exactly one of the hosts on the image allowlist (CHAT-15), with no port and no
  user information;
- with no fragment;
- whose path ends in `.gif`, `.webp`, `.png`, `.jpg` or `.jpeg` (case-insensitive; a query
  string is allowed).

An absent, empty or not allowed value MUST be ignored: the client is registered normally,
without a profile picture, and the connection MUST NOT be rejected because of it. The accepted
URL MUST be relayed exactly as received.

The profile picture belongs to the connection: it MUST be fixed from registration until the
connection ends and MUST NOT be changed by any inbound frame. The server MUST include it as
`avatar` in that client's `welcome`, `join` and `chat` envelopes (CHAT-10) and in `users`
snapshots (CHAT-17). A `chat` envelope kept in history (CHAT-18) MUST keep the `avatar` it
was relayed with.

#### Scenario: Valid avatar is announced

- GIVEN client `A` is connected
- WHEN client `B` connects with `?nickname=B&avatar=https://i.imgur.com/abc.png`
- THEN `B`'s `welcome` has `avatar` = `https://i.imgur.com/abc.png`
- AND `A` receives `type` = `join`, `from` = `B`, `avatar` = `https://i.imgur.com/abc.png`

#### Scenario: Invalid avatar is ignored

- GIVEN the server is running
- WHEN a client connects with an `avatar` of `http://i.imgur.com/a.png`,
  `https://evil.example/a.png`, `https://i.imgur.com.evil.example/a.png`,
  `https://i.imgur.com:8443/a.png`, `https://u@i.imgur.com/a.png`,
  `https://i.imgur.com/a.png#x`, `https://i.imgur.com/a.svg`, `javascript:alert(1)` or a
  513-byte allowlisted URL
- THEN the upgrade succeeds
- AND its `welcome`, the `join` others receive and its `chat` envelopes carry no `avatar`

#### Scenario: History keeps the avatar

- GIVEN client `A` with avatar `U` sends `hola` and disconnects
- WHEN client `C` connects
- THEN the `history` entry for `hola` has `from` = `A` and `avatar` = `U`

### Requirement: CHAT-29 Chat page profile pictures

The join form MUST offer an optional profile-picture URL field. The page MUST check it with
the same rule as inline images (CHAT-15) before joining; if it is not empty and fails the
check, the page MUST show an error and MUST NOT join. A valid value MUST be sent as `avatar`
on every room connection and reconnection. The page MAY remember the last value in browser
storage.

The page MUST show a user's profile picture, when known, in place of the initial-letter avatar
next to that user's messages (rooms, history and direct messages) and next to the user's name
in the online list, and MUST show its own picture in the profile area. The page MUST learn
pictures from `welcome`, `users`, `join` and `chat` frames, MUST forget a user's picture on
`leave`, and MUST use for a direct-message peer the picture from the room's online list.

Before rendering any picture URL received from the server, the page MUST check it with the
CHAT-15 rule and MUST NOT request it if the check fails. Pictures MUST be loaded only by
setting an image's source (never as markup or CSS), with no referrer, and MUST be decorative
(empty alternative text). If a picture fails to load, the page MUST show the initial-letter
avatar instead.

#### Scenario: Avatar next to messages and in the list

- GIVEN the page is connected and `Ana` (avatar `U`) is online
- WHEN `Ana` sends `hola`
- THEN the message shows an image loaded from `U` instead of the letter `A`
- AND the online list shows the same image before `Ana`

#### Scenario: Invalid field blocks joining

- GIVEN the join form
- WHEN the user types a nickname and the picture URL `https://evil.example/a.png` and submits
- THEN the page shows an error and opens no connection

#### Scenario: Broken picture falls back

- GIVEN `Ana`'s picture URL returns 404
- WHEN her message is rendered
- THEN the initial-letter avatar `A` is shown

#### Scenario: Server value failing the page check is not requested

- GIVEN the page receives a `chat` envelope with `avatar` = `https://evil.example/a.png`
- WHEN it renders the message
- THEN it shows the initial-letter avatar and requests nothing from `evil.example`

### Requirement: CHAT-30 Shared music player

A room MAY have a **shared player** (only `musica` does; SRV-06). In a room with a shared
player, the server MUST keep the **music state**, owned by the room:

| Field      | Meaning |
|------------|---------|
| `video`    | the current YouTube video id, or none |
| `playing`  | whether playback is running |
| position   | milliseconds into the video; while `playing` it advances with server time |
| `by`       | nickname shown as responsible for the current state (see below) |
| `seq`      | integer that increases by 1 every time a track becomes current; 0 while none has |
| `queue`    | ordered list of at most **50** items `{id, video, by}` waiting after the current track |
| `finished` | whether the current track ended with nothing queued after it |

Each queue item gets an `id`: a positive integer assigned by the server, unique within the room
for the life of the process. Initially no video is current, the queue is empty and `seq` is 0.

A track **becomes current** when its video is set as `video` with position 0, `playing` = true,
`finished` = false, `seq` incremented, and `by` = the nickname that added it.

A **music control** (CHAT-21) is a JSON object `{"type":"music","action":A,…}` and is **valid**
only when:

| `action`   | Other fields | Effect on the state |
|------------|--------------|---------------------|
| `add`      | `video`: a string matching `^[A-Za-z0-9_-]{11}$` | if no video is current, or `finished` is true and the queue is empty: the video becomes current. Otherwise it is appended to the queue (requires fewer than 50 queued items) |
| `add_many` | `videos`: a JSON array of 1 to 50 elements, each a string matching `^[A-Za-z0-9_-]{11}$` | processed in order as one control: if no video is current, or `finished` is true and the queue is empty, the first element becomes current; the remaining elements are appended to the queue while it has fewer than 50 items, and the rest are dropped. Requires at least one element to become current or be queued |
| `remove` | `id`: an integer ≥ 1 | the queue item with that `id` is removed (requires it to exist) |
| `skip`   | — | the first queue item is removed and becomes current (requires a non-empty queue) |
| `ended`  | `seq`: an integer ≥ 1 | requires `seq` equal to the state's `seq` and `finished` false. If the queue is non-empty, as `skip`. Otherwise `finished` = true and `playing` = false at the current position |
| `play`   | — | `playing` = true from the current position (requires a current video) |
| `pause`  | — | `playing` = false at the current position (requires a current video) |
| `seek`   | `pos`: an integer, 0 ≤ `pos` ≤ 86 400 000 | position = `pos`, `playing` unchanged (requires a current video) |

`play` and `seek` on a `finished` track set `finished` to false (the track is replayed). For
`play`, `pause` and `seek`, `by` becomes the sender's nickname. For `remove`, and for `add` or
`add_many` that only appends, `by` is unchanged. When an `add_many` makes its first element
current, `by` is the sender's nickname. Every queued item gets its own `id`, in order. Every
accepted control produces a `music` frame; an accepted `add_many` produces exactly one, reflecting
the whole batch.

Any other control MUST be discarded without any reply. This includes an unknown or missing
`action` (including the former `load`), wrong field types, an invalid `video`, `pos`, `id` or
`seq` out of range or not an integer, an unmet requirement above, or an `ended` whose `seq` is
not the current one. Several clients reporting the same track as ended therefore advance the
queue only once. Discarded controls also include an `add_many` whose `videos` is missing, not
an array, empty, longer than 50, or contains any element that is not a string matching the video
id pattern (the whole control is discarded; no element is applied).

Per connection, the server MUST accept at most 4 `ended` controls per second and, separately,
at most 4 other music controls per second (an `add_many` counts as one). It MUST discard the
excess without any reply.

After each accepted control the server MUST send every registered client of the room (including
the sender) a `music` frame, a JSON object with exactly these fields:

| Field     | JSON type | Value |
|-----------|-----------|-------|
| `type`    | string    | `music` |
| `video`   | string    | the current video id |
| `playing` | boolean   | the state's `playing` |
| `pos`     | number    | the position, in integer milliseconds, at the moment the frame is produced |
| `by`      | string    | the state's `by` |
| `seq`     | number    | the state's `seq` |
| `queue`   | array     | the queue in order, each element an object with exactly `id` (number), `video` (string) and `by` (string, who added it); `[]` when empty |
| `ts`      | number    | server time, integer Unix milliseconds |

A client that registers while a video is current MUST receive one `music` frame with the current
state right after its `history` frame (CHAT-11 order: `welcome`, `users`, `history`, `music`).
No `music` frame is sent while no video is current. `music` frames MUST NOT be kept in history
(CHAT-18), and a client that is too slow to receive one is handled like any other must-have
frame (CHAT-03).

When the last client leaves a room while `playing` is true, the server MUST set `playing` to
false at the current position. The queue is kept. In a room without a shared player no `music`
frame is ever sent.

#### Scenario: First add plays

- GIVEN clients `A` and `B` are in `musica` and no video is current
- WHEN `A` sends `{"type":"music","action":"add","video":"dQw4w9WgXcQ"}`
- THEN `A` and `B` receive `type` = `music`, `video` = `dQw4w9WgXcQ`, `playing` = true,
  `by` = `A`, `seq` = 1, `queue` = `[]`, and `pos` between 0 and the elapsed time

#### Scenario: Add while playing queues

- GIVEN `dQw4w9WgXcQ` is current and playing in `musica`
- WHEN `B` sends `add` with `video` = `9bZkp7q19f0`
- THEN every client receives `video` = `dQw4w9WgXcQ`, `playing` = true, `by` unchanged, the same
  `seq`, and `queue` = `[{"id":1,"video":"9bZkp7q19f0","by":"B"}]`

#### Scenario: Skip advances

- GIVEN a current track with `seq` = 1 and queue `[{id 1, 9bZkp7q19f0, by B}]`
- WHEN `A` sends `skip`
- THEN every client receives `video` = `9bZkp7q19f0`, `playing` = true, `pos` about 0,
  `by` = `B`, `seq` = 2, `queue` = `[]`

#### Scenario: Ended advances once

- GIVEN a current track with `seq` = 3 and one queued item
- WHEN `A`, `B` and `C` each send `{"type":"music","action":"ended","seq":3}`
- THEN exactly one `music` frame is sent, with the queued item as current and `seq` = 4
- AND the other two controls are discarded

#### Scenario: Last track ends

- GIVEN a current track with `seq` = 2 and an empty queue
- WHEN `A` sends `ended` with `seq` = 2
- THEN every client receives the same `video`, `playing` = false, `seq` = 2, `queue` = `[]`
- AND a later `add` from `B` with `9bZkp7q19f0` makes it current with `seq` = 3

#### Scenario: Remove by id

- GIVEN queue `[{id 4, …}, {id 5, …}]`
- WHEN `A` sends `remove` with `id` = 4, then `remove` with `id` = 4 again
- THEN one `music` frame is sent, with `queue` = `[{id 5, …}]`, and the second control is
  discarded

#### Scenario: Queue cap

- GIVEN 50 items are queued
- WHEN `A` sends `add`, or `add_many` with any valid `videos`
- THEN no client receives any frame

#### Scenario: Pause and seek keep the position

- GIVEN a video current and playing in `musica`
- WHEN `B` sends `pause`, then `seek` with `pos` = 60000
- THEN every client receives `playing` = false, `by` = `B`, and then `playing` = false,
  `pos` = 60000

#### Scenario: Newcomer gets the state

- GIVEN a video playing in `musica` for about 5 s with two queued items
- WHEN `C` joins `musica`
- THEN its fourth data frame is `type` = `music` with that `video`, `playing` = true, `pos`
  of about 5000, and both queued items

#### Scenario: Invalid controls are discarded

- GIVEN clients `A` and `B` in `musica`, no video current
- WHEN `A` sends `play`, `seek` with `pos` = 5, `skip`, `ended` with `seq` = 1, `remove` with
  `id` = 1, `load` with a valid `video`, `add` with `video` = `abc`, `add` with
  `video` = `https://youtu.be/dQw4w9WgXcQ`, `seek` with `pos` = -1 or 1.5, `remove` with
  `id` = 0 or `"1"`, and `{"type":"music"}`
- THEN no client receives any frame

#### Scenario: Flood is limited

- GIVEN a video current in `musica`
- WHEN `A` sends 20 `play` controls within 100 ms
- THEN clients receive at most 4 `music` frames caused by them

#### Scenario: Ended is not throttled by user controls

- GIVEN a current track with `seq` = 1, one queued item, and `A` is the only client
- WHEN `A` sends `pause`, `play` and immediately `ended` with `seq` = 1
- THEN the `ended` is accepted and the queued item becomes current

#### Scenario: Empty room pauses

- GIVEN a video playing in `musica` with one queued item, and `A` is the only client
- WHEN `A` leaves and `B` joins 10 s later
- THEN `B` receives `playing` = false with `pos` about the position at the time `A` left, and
  the queued item

#### Scenario: Bulk add on an idle player

- GIVEN clients `A` and `B` are in `musica` and no video is current
- WHEN `A` sends `{"type":"music","action":"add_many","videos":["dQw4w9WgXcQ","9bZkp7q19f0","kJQP7kiw5Fk"]}`
- THEN `A` and `B` receive exactly one `music` frame with `video` = `dQw4w9WgXcQ`,
  `playing` = true, `by` = `A`, `seq` = 1 and `queue` =
  `[{"id":1,"video":"9bZkp7q19f0","by":"A"},{"id":2,"video":"kJQP7kiw5Fk","by":"A"}]`

#### Scenario: Bulk add while playing fills free slots only

- GIVEN a video is current and 48 items are queued
- WHEN `B` sends `add_many` with 5 valid videos
- THEN every client receives one `music` frame with the same current video and `seq`, and 50
  queued items, the last two being the first two videos sent, added by `B`

#### Scenario: Bulk add after the last track ended

- GIVEN the current track has `finished` = true and the queue is empty, with `seq` = 2
- WHEN `A` sends `add_many` with 2 valid videos
- THEN every client receives the first video as current with `seq` = 3, `playing` = true, and
  the second one queued

#### Scenario: Invalid bulk add is discarded whole

- GIVEN clients `A` and `B` in `musica`, no video current
- WHEN `A` sends `add_many` with `videos` = `[]`, with 51 valid ids, with
  `["dQw4w9WgXcQ","abc"]`, with `["dQw4w9WgXcQ",null]`, with `"dQw4w9WgXcQ"`, or without
  `videos`
- THEN no client receives any frame

### Requirement: CHAT-31 Chat page shared player

The chat page MUST show the shared player controls only while the active pane belongs to
`musica` (the room or one of its direct-message conversations). Shared controls (add,
play/pause, skip, seek, remove) MUST NOT be offered anywhere else. The player MUST let the user paste a YouTube
or YouTube Music link and add it for everyone, play/pause for everyone, skip for everyone, and
seek for everyone. It MUST show the current position, who is responsible for the current state
(`by`, as text), and the queue in order. Each queued item shows its video id and who added it
(as text), plus a control that removes it for everyone. The player MUST offer local
volume/mute (not shared).

The page MUST accept links of the forms `https://www.youtube.com/watch?v=ID`,
`https://youtube.com/watch?v=ID`, `https://m.youtube.com/watch?v=ID`,
`https://music.youtube.com/watch?v=ID`, `https://youtu.be/ID`,
`https://www.youtube.com/shorts/ID`, `https://youtube.com/shorts/ID` and
`https://m.youtube.com/shorts/ID` (extra query parameters allowed), where `ID` matches
`^[A-Za-z0-9_-]{11}$`, and MUST send only the id (`add`).

The page MUST also accept **playlist links**: `https://H/playlist?list=LIST` and
`https://H/watch?v=ID&list=LIST`, where `H` is `www.youtube.com`, `youtube.com`,
`m.youtube.com` or `music.youtube.com`, `LIST` matches `^[A-Za-z0-9_-]{10,64}$` (extra query
parameters allowed). For a playlist link the page MUST request `GET /playlist?list=LIST`
(SRV-09) and send the returned ids with one `add_many`:

- For a `watch?v=ID&list=LIST` link, `ID` goes first and is removed from the rest of the list.
  If `LIST` starts with `RD` (an automatic mix), the page MUST NOT request the playlist, MUST
  send `ID` only and show "Las mezclas automáticas de YouTube no se pueden importar. Puse solo
  el video." If the request fails for any reason, the page MUST send `ID` only (as `add` or as a
  one-element `add_many`) and show the matching error text below followed by " Puse solo el
  video."
- The page MUST send at most 50 ids and at most the free queue slots (50 minus the queued
  items in the last `music` frame). When it has to drop ids it MUST show "Se agregaron N de M."
  (N sent, M received, counting `ID` for a `watch?v=ID&list=LIST` link). When nothing fits it MUST show "La cola está llena" and send nothing.
- While the request is pending the page MUST show "Importando playlist…", MUST disable the add
  control, and MUST NOT start another import. If the user leaves `musica` before the answer,
  the page MUST send nothing.
- Errors (send nothing): when `/playlist` answers 404 without a JSON `error` (import disabled),
  "Importar playlists no está disponible."; `not_found`, "No encontré esa playlist (¿es
  privada?)."; an empty `videos`, "Esa playlist no tiene videos reproducibles."; anything else,
  "No se pudo leer la playlist." The page MUST show only these fixed texts, never text from the
  response, and MUST use only returned ids that match the video id pattern.

Anything else MUST show an error and send nothing. When the last `music` frame shows 50 queued
items, the page MUST show "La cola está llena" and send nothing. The skip control MUST be
disabled while the queue is empty.

The video MUST play in an embedded `https://www.youtube-nocookie.com/embed/ID` frame with
YouTube's own controls disabled, controlled only through cross-document messages; the page MUST
NOT load any YouTube script. Messages from the frame MUST be accepted only when they come from
the `https://www.youtube-nocookie.com` origin and from that frame. The page MUST NOT create the
frame (nor contact YouTube) before the user has shown the `musica` pane at least once since
joining it. Showing it while a video is current creates the frame and starts playback without
any further control; after that, while the user stays joined, the frame MAY be created while
another pane is shown when a track becomes current. If the
browser blocks playback, the page MUST show the text
"Tu navegador bloqueó el audio: apretá Escuchar." with an **"Escuchar"** control that retries
playback.

On each `music` frame the page MUST follow the state. It loads the video if `seq` changed,
plays or pauses, and seeks when its local position differs from the expected one by more than
2 seconds (expected = `pos` plus the time since the frame arrived while `playing`). When the
embedded player reports that the loaded track ended, and that track's `seq` is the state's
`seq`, the page MUST send `ended` with that `seq`, once per `seq`. It MUST NOT restart a track
that ended locally while it waits for the next frame. No other local player event is sent as a
control.

Once created, the frame MUST keep following the room's state (CHAT-34) while the user stays
joined to `musica`, even when the shown pane belongs to another room or conversation: switching
panes MUST NOT pause local playback nor send a control, and the page MUST keep sending `ended`
as above. Leaving `musica` (or the chat) MUST stop local playback and remove the frame without
sending a control. If the video cannot be embedded or played, the page MUST show a short
message.

#### Scenario: Everyone hears the same

- GIVEN two browsers with the `musica` pane open and nothing current
- WHEN one pastes `https://music.youtube.com/watch?v=dQw4w9WgXcQ&si=x` and presses "Poner"
- THEN both play that video from the start without pressing anything else, and both show the
  first user as responsible

#### Scenario: Paste while playing queues

- GIVEN both browsers are playing a track
- WHEN one pastes another valid link and presses "Poner"
- THEN the track keeps playing in both, and both show the new item in the queue with its author

#### Scenario: Auto-advance

- GIVEN both browsers are playing the last seconds of a track with one queued item
- WHEN the track ends
- THEN both start the queued item within a few seconds, and the queue is empty in both

#### Scenario: Skip and remove

- GIVEN two queued items
- WHEN one browser removes the second and then presses skip
- THEN both play the first item and show an empty queue with the skip control disabled

#### Scenario: Global pause

- GIVEN both browsers are playing
- WHEN one presses pause
- THEN both pause within a second, at about the same position

#### Scenario: Late joiner syncs

- GIVEN a video has been playing for 1 minute
- WHEN a third browser opens `musica`
- THEN it starts at about 1:00 without pressing "Escuchar" (or shows the blocked warning)

#### Scenario: Switching rooms keeps playing

- GIVEN browsers `X` and `Y` are playing a track in `musica`
- WHEN `X` switches to `general` for 30 s and then back to `musica`
- THEN `X` keeps hearing the track the whole time, in sync with `Y`, and no control is sent

#### Scenario: Leaving musica stops the music

- GIVEN `X` is joined to `general` and `musica` and hears a track while looking at `general`
- WHEN `X` leaves `musica`
- THEN the music stops for `X` only and keeps playing for everyone else

#### Scenario: Blocked autoplay

- GIVEN a browser that refuses to start audio without a click on the player
- WHEN the user opens `musica` while a video is playing
- THEN the page shows "Tu navegador bloqueó el audio: apretá Escuchar." and the "Escuchar"
  control, and pressing it starts playback in sync

#### Scenario: Invalid link

- GIVEN the player
- WHEN the user pastes `https://evil.example/watch?v=dQw4w9WgXcQ` and presses "Poner"
- THEN the page shows an error and sends nothing

#### Scenario: No YouTube outside musica

- GIVEN a video is playing in `musica`
- WHEN a user joins and stays in `general` without opening `musica`
- THEN the page makes no request to any YouTube host

#### Scenario: Paste a YouTube Music playlist

- GIVEN two browsers with the `musica` pane open, nothing current, and the server has a
  YouTube key
- WHEN one pastes `https://music.youtube.com/playlist?list=PLlb3q7unEBfNPa0aR-cOQB33HDAwyDLpF`
  and presses "Poner"
- THEN both play the first playable video of the playlist and show up to 49 more in the queue,
  added by the first user

#### Scenario: Song inside a playlist

- GIVEN the player and a server with a YouTube key
- WHEN the user pastes `https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=PL…&index=3`
- THEN `dQw4w9WgXcQ` is sent first, followed by the rest of the playlist without repeating it

#### Scenario: Mix link adds only the song

- GIVEN the player
- WHEN the user pastes `https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=RDdQw4w9WgXcQ`
- THEN the page sends only `dQw4w9WgXcQ`, shows the mix text, and does not request `/playlist`

#### Scenario: Import disabled

- GIVEN the server runs without a YouTube key
- WHEN the user pastes a `/playlist?list=` link and presses "Poner"
- THEN the page shows "Importar playlists no está disponible." and sends nothing

#### Scenario: Partial import

- GIVEN a video is current and 45 items are queued
- WHEN the user imports a playlist that returns 20 videos
- THEN the page sends `add_many` with the first 5 and shows "Se agregaron 5 de 20."

### Requirement: CHAT-32 Chat page notification sound

A **notifying message** is a `chat` frame (CHAT-10) or a `dm` frame (CHAT-22) received live
whose `from` is not the user's own nickname in that room. Messages delivered in a `history`
frame, the user's own echoed messages, `join`/`leave`, `typing`, `music`, `dm_error` frames
and system notices MUST NOT be notifying messages.

The page is **unattended** when its tab is hidden or its window does not have the focus (the
user is in another tab, window or application; `document.hidden` alone misses a visible but
unfocused window).

When a notifying message arrives and the page is unattended, or the message belongs to a pane
(room or private conversation) other than the one currently shown, the page MUST play a short
sound (at most 200 ms), synthesized in the page (no audio file, no change to the
Content-Security-Policy). The page MUST play at most one sound per second; extra notifying
messages inside that window are silent. If the browser does not allow audio, the page MUST
fail silently and keep working.

The chat header MUST offer a single global toggle (🔔 on / 🔕 muted, with `aria-pressed`)
that mutes the sound for every room and private conversation. The choice MUST be remembered
in the browser (`localStorage`, best effort) and restored on the next visit. Muting MUST NOT
affect the tab indicator (CHAT-33) nor the unread badges.

#### Scenario: Message in another room

- GIVEN Ana has joined `general` and `juegos`, is looking at `general`, sound not muted
- WHEN Bob sends a message in `juegos`
- THEN Ana's page plays the notification sound once

#### Scenario: Message in the pane being read

- GIVEN Ana's tab is visible and shows `general`
- WHEN Bob sends a message in `general`
- THEN no sound plays

#### Scenario: Hidden tab

- GIVEN Ana's tab is hidden while showing `general`
- WHEN Bob sends a message in `general` or a private message to Ana
- THEN the notification sound plays

#### Scenario: Visible window without focus

- GIVEN Ana's browser shows `general` but she is typing in another application
- WHEN Bob, from another computer, sends a message in `general`
- THEN the notification sound plays and the title reads `(1) ★chadroom★`
- AND when Ana clicks back into the browser window, the title and favicon reset

#### Scenario: Own messages and history are silent

- GIVEN Ana's tab is hidden
- WHEN Ana's own message is echoed, or she rejoins and receives the room history
- THEN no sound plays

#### Scenario: Other events are silent

- GIVEN Ana's tab is hidden
- WHEN someone joins, leaves, types or changes the music
- THEN no sound plays

#### Scenario: Burst

- GIVEN Ana's tab is hidden
- WHEN five messages from others arrive within one second
- THEN the sound plays once

#### Scenario: Mute is global and remembered

- GIVEN Ana presses 🔔 and it turns into 🔕
- WHEN messages arrive in any room or private conversation while the tab is hidden
- THEN no sound plays
- AND after reloading the page and joining again, the toggle still shows 🔕

### Requirement: CHAT-33 Chat page tab indicator

While the page is unattended, every notifying message (CHAT-32), muted or not and including those
in the pane being shown, MUST increment an unread counter. While the counter is above zero the
document title MUST be `(N) ` followed by the page's original title, and the favicon MUST be
the unread variant (`/favicon-unread.svg`). When the page is attended again (visible and focused), or
the user leaves the chat, the counter MUST reset to zero and the title and favicon MUST return to the
original (`/favicon.svg`). An attended page MUST NOT change title or favicon.

#### Scenario: Unread while away

- GIVEN Ana's tab is hidden
- WHEN Bob sends three messages across her rooms and conversations
- THEN the tab title reads `(3) ★chadroom★` and the favicon shows the unread variant

#### Scenario: Coming back resets

- GIVEN Ana's tab shows `(3) ★chadroom★`
- WHEN she switches back to the tab
- THEN the title is `★chadroom★` and the favicon is the normal one

#### Scenario: Muted still indicates

- GIVEN Ana muted the sound and her tab is hidden
- WHEN Bob sends a message
- THEN no sound plays AND the title reads `(1) ★chadroom★`

### Requirement: CHAT-34 Chat page global music mute and now playing

While the user is joined to `musica`, the chat header MUST offer, from every room and private
conversation, a toggle (🎵 on / 🔇 muted, with `aria-pressed`) that mutes the shared music for
this browser only. It MUST NOT send any control and MUST NOT affect other users. While muted the
page MUST keep following the room's state (CHAT-31), so unmuting resumes at the room's current
position. The toggle and the in-player local mute (CHAT-31) MUST reflect one single state. The
choice MUST be remembered in the browser (`localStorage`, best effort) and restored on the next
visit. It is independent of the notification sound toggle (CHAT-32). The toggle MUST NOT be
shown while the user is not joined to `musica`.

While the user is joined to `musica`, a track is current and the shown pane does not belong to
`musica`, the chat header MUST show a read-only "now playing" text: `♪ ` followed by the track
title when playing, or `⏸ ` followed by it when paused. When the title is unknown it MUST use
`youtu.be/<id>`. When the browser blocked the audio (CHAT-31) it MUST read
"Audio bloqueado: abrí #musica", and when the user has not shown the `musica` pane since joining
it (so nothing plays, CHAT-31) it MUST read "Abrí #musica para escuchar". It MUST be set as text
(never as markup), MUST offer no control, and MUST be hidden otherwise. On narrow screens
(≤ 480 px wide) the page MAY hide this text; the toggle stays.

#### Scenario: Mute from another room

- GIVEN Ana is joined to `general` and `musica`, looking at `general`, and hears a track
- WHEN she presses 🎵
- THEN she stops hearing it, the button shows 🔇 with `aria-pressed="true"`, nobody else is
  affected and no frame is sent

#### Scenario: Unmute resumes in sync

- GIVEN Ana muted the music 1 minute ago while the track kept playing for the room
- WHEN she presses 🔇
- THEN she hears the track at about the room's current position

#### Scenario: Mute is remembered

- GIVEN Ana muted the music and reloads the page
- WHEN she joins `musica` and a track plays
- THEN the music is muted and the toggle shows 🔇

#### Scenario: Now playing outside musica

- GIVEN a track titled "Song" is playing and Ana is joined to `musica`, looking at `juegos`
- THEN the header shows `♪ Song`; when someone pauses it in `musica` it shows `⏸ Song`

#### Scenario: Hidden in musica and when not joined

- GIVEN Ana looks at the `musica` pane, or is not joined to `musica`
- THEN the header shows no now-playing text; and when not joined it shows no music toggle

#### Scenario: Shared controls stay in musica

- GIVEN Ana looks at `general` while a track plays
- THEN the page offers no way to pause, seek, skip, add or remove tracks outside `musica`
