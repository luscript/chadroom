# Chat Specification

## Purpose

Behavior of the chat room hub and of each client connection: registration, message
broadcast, disconnection, connection limits and liveness. Wire protocol: inbound frames
(client → server) are plain UTF-8 text containing only the message; outbound frames
(server → client) are JSON envelopes `{"type","from","text","ts"}` built only by the server
(CHAT-10). Nicknames are normalized (CHAT-09) and unique case-insensitively (CHAT-12).

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
disconnects; `from` is the affected client's assigned nickname. For the newcomer, its
`join` MUST arrive after its `welcome` (CHAT-11). A leave notice MUST be broadcast at most
once per client. Server-initiated removals — slow-client drop (CHAT-05) and room shutdown
(CHAT-08) — are silent: no leave notice is broadcast for them.

#### Scenario: Join notice

- GIVEN client `A` is connected
- WHEN client `B` connects with nickname `B`
- THEN `A` and `B` receive `type` = `join`, `from` = `B`
- AND `B` receives it after its `welcome`

#### Scenario: Leave notice exactly once

- GIVEN clients `A` and `B` are connected
- WHEN `B` closes its connection
- THEN `A` receives `type` = `leave`, `from` = `B` exactly once

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
- AND the remaining clients receive the message
- AND S's later disconnection does not produce a second removal or a panic

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

Every data frame the server sends to a client MUST be a text frame containing a single JSON
object with exactly these fields:

| Field  | JSON type | Value |
|--------|-----------|-------|
| `type` | string    | one of `chat`, `join`, `leave`, `welcome` |
| `from` | string    | the server-assigned nickname of the client the event is about |
| `text` | string    | the message text for `chat`; the empty string for every other type |
| `ts`   | number    | server time when the event was produced, as integer Unix milliseconds |

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
data frame the client receives.

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
whitespace. Discarding MUST NOT close the connection and MUST NOT produce any frame.
Non-empty messages MUST be relayed with their text unchanged (no trimming).

#### Scenario: Whitespace-only message

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends an empty frame, then a frame of spaces, tabs and newlines, then `hi`
- THEN `B` receives exactly one `chat` envelope, with `text` = `hi`
- AND `A`'s connection remains open

#### Scenario: Surrounding whitespace is preserved

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends `  hi  `
- THEN `B` receives `text` = `  hi  `

### Requirement: CHAT-14 Chat page interprets envelopes

The chat page served at `GET /` MUST interpret each received frame by its `type`: render
`chat` as a message from `from` with the time taken from `ts`; render `join` and `leave` as
system notices about `from`; take its own identity from `welcome`. It MUST mark a message as
its own only when `from` equals the nickname received in `welcome`. It MUST ignore frames
that are not valid JSON or whose `type` it does not know, without breaking later frames. It
MUST render all server-supplied strings as text, never as HTML.

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
one system notice when the connection is lost and one when it is re-established, per outage.
While not `online` the send button and the GIF panel's send button MUST stay disabled. After the user clicks "Salir" the page
MUST NOT reconnect, and any pending retry MUST be cancelled. Messages already on screen MUST
be kept across reconnects.

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
