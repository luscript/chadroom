# Delta for Chat

## MODIFIED Purpose

Behavior of each chat room hub and of each client connection: registration, message
broadcast, direct messages, typing indicators, presence, history, disconnection, connection
limits and liveness. The server runs a fixed set of rooms (SRV-06). Each room is an
independent hub, and every requirement in this spec applies **within one room** (CHAT-19). A
client connection belongs to exactly one room, and a user is in several rooms by opening one
connection per room (CHAT-20). Wire protocol: inbound frames (client → server) are UTF-8 text,
either plain text containing only a room message or a JSON envelope for a room message, a
direct message or a typing signal (CHAT-21); outbound frames (server → client) are JSON
objects built only by the server: event envelopes `{"type","from","text","ts"}` (CHAT-10), the
`users` (CHAT-17) and `history` (CHAT-18) snapshot frames, the `dm` and `dm_error` frames
(CHAT-22, CHAT-23) and the `typing` frame (CHAT-26). Frames carry no room field; the
connection identifies the room. Nicknames are normalized (CHAT-09) and unique
case-insensitively within a room (CHAT-12).

## MODIFIED Requirements

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
3. Otherwise, if the frame decodes as a JSON object whose `type` is the string `chat` and
   whose `text` is a JSON string, it is a **room message** with that `text`.
4. Otherwise (not JSON, not an object, missing or unknown `type`, or a `chat` object whose
   `text` is not a string) it is a **room message** whose text is the whole frame, unchanged.

Fields other than `type`, `to` and `text` MUST be ignored (`text` is also ignored in a typing
signal). Room messages are relayed per CHAT-01. The blank-text rule (CHAT-13) applies to the
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

## ADDED Requirements

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
