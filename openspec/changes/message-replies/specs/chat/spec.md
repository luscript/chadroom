# Delta for Chat

## MODIFIED Requirements

### Requirement: CHAT-10 Outbound JSON envelope

(Requirement text unchanged, except the field table, which gains two optional rows after
`avatar`, and the sentence below it.)

| Field    | JSON type | Value |
|----------|-----------|-------|
| `id`     | number    | **optional**: present only on `chat`; the message id assigned by the server (CHAT-35) |
| `reply`  | object    | **optional**: present only on `chat`, and only when the message is a resolved reply (CHAT-35); an object with exactly `id` (number), `from` (string) and `text` (string) |

`avatar` MUST NOT appear on `leave` envelopes, and MUST NOT appear with an empty value. `id`
and `reply` MUST NOT appear on `join`, `leave` or `welcome` envelopes. Field order is `type`,
`from`, `text`, `ts`, `avatar`, `id`, `reply` (absent fields skipped).

`id` and `reply` MUST be produced only by the server; an inbound `id` or `reply` field is
ignored (CHAT-21).

New scenario:

#### Scenario: Join envelopes stay unchanged

- GIVEN clients `A` and `B` in a room
- WHEN `C` joins
- THEN the `join` envelope has exactly `type`, `from`, `text`, `ts` (and `avatar` when `C`
  has one), with no `id` or `reply`

### Requirement: CHAT-18 Recent message history

(Requirement text unchanged, except the `messages` row, which becomes:)

| Field      | JSON type        | Value |
|------------|------------------|-------|
| `messages` | array of objects | the kept `chat` envelopes (CHAT-10), oldest first, unchanged (same `from`, `text`, `ts`, `avatar`, `id` and `reply`) |

New scenario:

#### Scenario: History keeps ids and quotes

- GIVEN `A` sent `hola` (id 1) and `B` replied `chau` to it
- WHEN `C` joins
- THEN `C`'s `history` has `{"type":"chat","from":"A","text":"hola",…,"id":1}` followed by
  `{"type":"chat","from":"B","text":"chau",…,"id":2,"reply":{"id":1,"from":"A","text":"hola"}}`

### Requirement: CHAT-21 Inbound envelope

(Requirement text unchanged, except the "Fields other than…" sentence, which becomes:)

Fields other than `type`, `to` and `text` (and `reply_to` in a room or direct message, and
`action`, `video`, `videos`, `pos`, `id` and `seq` in a music control) MUST be ignored (`text`
is also ignored in a typing signal and a music control). A `reply_to` that is not a JSON
integer ≥ 1 MUST be ignored as if absent; it never makes a message invalid nor changes how
the frame is classified.

New scenarios:

#### Scenario: reply_to is read from a chat envelope

- GIVEN `A` and `B` in a room and message id 3 is a room message
- WHEN `A` sends `{"type":"chat","text":"sí","reply_to":3}`
- THEN `B` receives a `chat` envelope with `text` = `sí` and a `reply` for id 3

#### Scenario: Malformed reply_to is ignored

- GIVEN `A` and `B` in a room
- WHEN `A` sends `{"type":"chat","text":"sí","reply_to":"3"}`, `…"reply_to":0`,
  `…"reply_to":1.5` or `…"reply_to":null`
- THEN `B` receives a `chat` envelope with `text` = `sí` and no `reply`

### Requirement: CHAT-22 Direct message delivery

(Requirement text unchanged, except the frame table, which gains two rows after `ts`:)

| Field      | JSON type | Value |
|------------|-----------|-------|
| `id`       | number    | the message id assigned by the server (CHAT-35); identical in both copies |
| `reply_to` | number    | **optional**: present only when the received direct message carried a valid `reply_to` (CHAT-21); its value, unchanged |

The server MUST NOT look up, validate or keep the target of a DM `reply_to` (CHAT-24).

New scenario:

#### Scenario: DM reply is forwarded as an id

- GIVEN `A` and `B` in a room
- WHEN `A` sends `{"type":"dm","to":"B","text":"ok","reply_to":7}`
- THEN `A` and `B` each receive one `dm` frame with `from` = `A`, `to` = `B`, `text` = `ok`,
  the same `ts` and `id`, and `reply_to` = 7

## ADDED Requirements

### Requirement: CHAT-35 Message ids and room replies

Each room MUST assign every `chat` envelope it relays and every `dm` frame it routes an `id`:
an integer starting at 1 and increasing by 1, unique within the room for the life of the
process (one sequence per room, shared by room and direct messages). Messages that are
discarded (blank, invalid, rate-limited, `dm_error`) MUST NOT consume an id.

The room MUST keep, in memory and owned by the room, the last **200** room messages it relayed
as **reply targets** `{id, from, text}`, where `text` is the message text truncated to its
first 100 runes (trailing whitespace removed). Direct messages MUST NOT be reply targets.

When a room message carries a valid `reply_to` (CHAT-21) equal to the `id` of a kept reply
target, the relayed `chat` envelope MUST carry `reply` = that target `{id, from, text}`. `from`
is the nickname at the time the target was relayed. The reply's own reply target stores only
its own text: quotes never nest. When `reply_to` does not match a kept target (unknown, older
than the last 200, a direct message id, or the message's own id), the message MUST be relayed
without `reply` and without any error frame.

#### Scenario: Reply to a room message

- GIVEN `A` and `B` in `general` and `A` sent `¿pizza?`, relayed with `id` = 5
- WHEN `B` sends `{"type":"chat","text":"dale","reply_to":5}`
- THEN `A` and `B` receive `{"type":"chat","from":"B","text":"dale","ts":…,"id":6,"reply":{"id":5,"from":"A","text":"¿pizza?"}}`

#### Scenario: Ids are shared by room and direct messages

- GIVEN a new room
- WHEN `A` sends a room message, then a DM to `B`, then another room message
- THEN their ids are 1, 2 and 3

#### Scenario: Long quoted text is truncated

- GIVEN `A` sent a 300-rune message with id 1
- WHEN `B` replies to id 1
- THEN the `reply.text` is the first 100 runes of `A`'s message

#### Scenario: Unknown target is ignored

- GIVEN 201 room messages were relayed after id 1, or id 9 was a direct message
- WHEN `B` sends a room message with `reply_to` = 1, 9 or 999
- THEN it is relayed with an `id` and no `reply`, and no other frame is sent

#### Scenario: Quotes do not nest

- GIVEN `B`'s message id 6 replies to id 5
- WHEN `A` replies to id 6
- THEN `reply` = `{"id":6,"from":"B","text":"dale"}` with no nested quote

### Requirement: CHAT-36 Chat page replies

Every room message and direct message rendered from a frame with a numeric `id` MUST offer
**one** reply control (shown while the pointer is over the message or the message has keyboard
focus; it MUST be reachable with the keyboard). System lines and messages without an `id` MUST
NOT offer it.

Activating the reply control MUST show, above the message input, a **reply preview** with the
text "Respondiendo a NICK", the quoted text and a cancel control, and MUST focus the input.
While the preview is shown, the next message or GIF sent from that pane MUST carry `reply_to`
= the target's `id`. The preview MUST be cleared after a successful send, by the cancel
control, by Escape (when no panel is open; an open emoji/GIF panel closes first), and when the
shown pane changes. Only one reply target exists at a time.

A message with a quote MUST show the quote above its text, inside the same bubble: the quoted
author and the quoted text. For a room message the quote comes from `reply`; for a direct
message the page MUST look up `reply_to` in that conversation's messages on the page, and when
it is not there it MUST show "Mensaje no disponible" as the quote. Quoted text that is an
allowed image URL (CHAT-15) MUST be shown as the text "🖼 GIF", never as an image. Long quoted
text MUST be cut visually to one or two lines. All quoted strings MUST be rendered as text
(never markup) and MUST NOT be used in element ids, classes or selectors.

Clicking a quote MUST scroll the original message into view and highlight it briefly, when the
original is on the page in that pane; otherwise it MUST do nothing. The page MUST validate
server data: `id` and `reply.id`/`reply_to` used only when they are integers ≥ 1, and `reply`
used only when `from` and `text` are strings.

#### Scenario: Reply in a room

- GIVEN `A` and `B` with `general` open, and `A` sent `¿pizza?`
- WHEN `B` presses the reply control on `¿pizza?`, types `dale` and sends
- THEN both pages show `dale` from `B` with the quote "A — ¿pizza?" above it

#### Scenario: Jump to the original

- GIVEN a reply quoting a message far above in the pane
- WHEN the user clicks the quote
- THEN the original scrolls into view and is highlighted briefly

#### Scenario: Late joiner sees the quote

- GIVEN `B` replied to `A` in `general`
- WHEN `C` joins `general`
- THEN `C`'s page shows the reply with its quote (from `history`)

#### Scenario: Reply in a direct message

- GIVEN `A` and `B` have a DM conversation where `B` sent `nos vemos?`
- WHEN `A` replies `sí` to it
- THEN both pages show `sí` with the quote "B — nos vemos?"

#### Scenario: DM original not on the page

- GIVEN `B` reloaded the page after `A` sent a DM
- WHEN `A` replies to that DM
- THEN `B`'s page shows the reply with the quote "Mensaje no disponible"

#### Scenario: Quoted GIF

- GIVEN `A` sent a GIF
- WHEN `B` replies to it
- THEN the quote shows "🖼 GIF" and no image

#### Scenario: Cancel a reply

- GIVEN the reply preview is shown
- WHEN the user presses Escape, the cancel control, or switches to another pane
- THEN the preview disappears and the next message is sent without `reply_to`
