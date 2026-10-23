# Delta for Chat

## MODIFIED Purpose

(Purpose text unchanged, except the outbound envelope sentence, which becomes:) outbound frames
(server → client) are JSON objects built only by the server: event envelopes
`{"type","from","text","ts"}` with an optional `avatar` (CHAT-10, CHAT-28), the `users`
(CHAT-17) and `history` (CHAT-18) snapshot frames, the `dm` and `dm_error` frames (CHAT-22,
CHAT-23) and the `typing` frame (CHAT-26).

## MODIFIED Requirements

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

The only other data frames are the `users` (CHAT-17) and `history` (CHAT-18) snapshots and
the `dm` (CHAT-22) and `dm_error` (CHAT-23) frames, whose shapes are defined there; each
entry of `history.messages` is a `chat` envelope with exactly the fields above.

The envelope MUST be produced only by the server. `from` and `avatar` MUST always be the
nickname and profile picture the server assigned to the originating connection and MUST NOT be
taken from any message payload. Message text MUST be carried only in `text` and MUST NOT alter
`type`, `from` or `avatar`, whatever characters it contains.

(Existing scenarios unchanged. Added:)

#### Scenario: Chat envelope carries the sender's avatar

- GIVEN client `A` connected with a valid avatar `U` and client `B` connected without one
- WHEN `A` sends `hola` and then `B` sends `chau`
- THEN every client receives `type` = `chat`, `from` = `A`, `text` = `hola`, `avatar` = `U`
- AND then `type` = `chat`, `from` = `B`, `text` = `chau` with no `avatar` field

#### Scenario: Avatar cannot be forged in a message

- GIVEN client `A` is connected without an avatar
- WHEN `A` sends the inbound envelope `{"type":"chat","text":"hi","avatar":"https://i.imgur.com/x.png"}`
- THEN the relayed `chat` envelope has no `avatar` field

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

(Existing scenarios unchanged; "exactly these fields" checks in them now exclude `avatars`
when no client has a profile picture. Added:)

#### Scenario: Snapshot includes avatars

- GIVEN client `Ana` is connected with a valid avatar `U` and client `bob` without one
- WHEN client `Carla` connects without an avatar
- THEN `Carla` receives `type` = `users`, `users` = `["Ana", "bob", "Carla"]`,
  `avatars` = `{"Ana": U}`

## ADDED Requirements

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
