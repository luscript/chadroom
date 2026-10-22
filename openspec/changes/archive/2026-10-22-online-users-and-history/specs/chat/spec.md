# Delta for Chat

## MODIFIED Purpose

Behavior of the chat room hub and of each client connection: registration, message
broadcast, presence, history, disconnection, connection limits and liveness. Wire protocol:
inbound frames (client → server) are plain UTF-8 text containing only the message; outbound
frames (server → client) are JSON objects built only by the server: event envelopes
`{"type","from","text","ts"}` (CHAT-10) plus the `users` (CHAT-17) and `history` (CHAT-18)
snapshot frames. Nicknames are normalized (CHAT-09) and unique case-insensitively (CHAT-12).

## ADDED Requirements

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
first; when a 51st is relayed the oldest MUST be discarded. `join`, `leave`, `welcome` and
`users` frames MUST NOT be kept. History is lost when the server restarts.

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

The chat page MUST, on each `history` frame, replace the messages on screen with the
frame's messages, rendered like live `chat` envelopes (CHAT-14, CHAT-15), including marking
its own messages by the nickname from the latest `welcome`.

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

#### Scenario: Notices are not kept

- GIVEN client `A` connected, sent `hola`, and client `B` connected and left
- WHEN client `C` connects
- THEN its `history` contains only the `chat` envelope `hola`

#### Scenario: Replayed message keeps its original data

- GIVEN `A` sent `hola` at time `t` and then disconnected
- WHEN `B` connects
- THEN `B`'s `history` contains `type` = `chat`, `from` = `A`, `text` = `hola`, `ts` = `t`

## MODIFIED Requirements

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

### Requirement: CHAT-10 Outbound JSON envelope

Every event frame the server sends to a client — `chat`, `join`, `leave` and `welcome` —
MUST be a text frame containing a single JSON object with exactly these fields:

| Field  | JSON type | Value |
|--------|-----------|-------|
| `type` | string    | one of `chat`, `join`, `leave`, `welcome` |
| `from` | string    | the server-assigned nickname of the client the event is about |
| `text` | string    | the message text for `chat`; the empty string for every other type |
| `ts`   | number    | server time when the event was produced, as integer Unix milliseconds |

The only other data frames are the `users` (CHAT-17) and `history` (CHAT-18) snapshots,
whose shapes are defined there; each entry of `history.messages` is a `chat` envelope with
exactly the fields above.

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

### Requirement: CHAT-14 Chat page interprets envelopes

The chat page served at `GET /` MUST interpret each received frame by its `type`: render
`chat` as a message from `from` with the time taken from `ts`; render `join` and `leave` as
system notices about `from` and update the online list (CHAT-17); take its own identity from
`welcome`; handle `users` (CHAT-17) and `history` (CHAT-18). It MUST mark a message as its
own only when `from` equals the nickname received in `welcome`. It MUST ignore frames that
are not valid JSON, whose `type` it does not know, or whose fields do not have the expected
JSON types, without breaking later frames. It MUST render all server-supplied strings as
text, never as HTML.

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
