# Delta for Chat

## MODIFIED Purpose

Behavior of each chat room hub and of each client connection: registration, message
broadcast, presence, history, disconnection, connection limits and liveness. The server runs
a fixed set of rooms (SRV-06). Each room is an independent hub, and every requirement in this
spec applies **within one room** (CHAT-19). A client connection belongs to exactly one room,
and a user is in several rooms by opening one connection per room (CHAT-20). Wire protocol:
inbound frames (client → server) are plain UTF-8 text containing only the message; outbound
frames (server → client) are JSON objects built only by the server: event envelopes
`{"type","from","text","ts"}` (CHAT-10) plus the `users` (CHAT-17) and `history` (CHAT-18)
snapshot frames. Frames carry no room field; the connection identifies the room. Nicknames
are normalized (CHAT-09) and unique case-insensitively within a room (CHAT-12).

## ADDED Requirements

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
