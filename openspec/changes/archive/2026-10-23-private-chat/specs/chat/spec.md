# Delta for Chat

## MODIFIED Purpose

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

## ADDED Requirements

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

## MODIFIED Requirements

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
