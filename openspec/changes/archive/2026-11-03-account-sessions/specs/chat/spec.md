# Delta for Chat

Base: `openspec/specs/chat/spec.md` (CHAT-01…CHAT-42). New requirements start at CHAT-43.

Terminology introduced by this change:

- **Account**: the authenticated identity a connection belongs to, given by the server-side
  account key (the session email, CHAT-41). The account key MUST NOT be sent to any client and
  MUST NOT be logged.
- **Member**: an account present in a room, i.e. with one or more registered connections in that
  room. Each room keeps its own members (CHAT-19).

## MODIFIED Requirements

### Requirement: CHAT-02 Join and leave notices

The system MUST broadcast a `join` envelope to all registered clients, including the
newcomer, when a connection registers **and its account had no other registered connection in
the room** (the account becomes a member), and a `leave` envelope when the **last** registered
connection of a member disconnects or is dropped as a slow client (CHAT-05); `from` is the
member's assigned nickname. Registering or removing any other connection of a member MUST NOT
broadcast `join` or `leave`. For the newcomer, its `join` MUST arrive after its `welcome`,
`users` and `history` frames (CHAT-11, CHAT-17, CHAT-18). A leave notice MUST be broadcast at
most once per member presence, and never to a connection of the account that left. Room
shutdown (CHAT-08) is silent: no leave notice is broadcast for it. A newcomer dropped before its
`join` was broadcast gets no leave notice either.

(Existing scenarios unchanged; each named client is a different account.)

#### Scenario: Second connection of an account is not announced

- GIVEN `A` and account `Ana` (one connection, `Ana1`) are connected
- WHEN `Ana` opens a second connection `Ana2` in the same room
- THEN no client receives a `join` for it
- AND `Ana2` receives its `welcome`, `users` and `history`

#### Scenario: Leave only on the last connection

- GIVEN `A` and account `Ana` with connections `Ana1` and `Ana2` are connected
- WHEN `Ana1` closes
- THEN no client receives a `leave`
- AND WHEN `Ana2` then closes
- THEN `A` receives `type` = `leave`, `from` = `Ana` exactly once

#### Scenario: Slow drop of one connection of a member

- GIVEN `A` and account `Ana` with connections `Ana1` and a slow `Ana2`
- WHEN `Ana2` is dropped for a full outbound buffer
- THEN no client receives a `leave`
- AND `Ana1` stays registered and keeps receiving messages

### Requirement: CHAT-04 Client identity is the account

The system MUST identify room members by account, not by nickname nor by connection. Every
registered connection of the same account in a room MUST share one member: the same assigned
nickname and profile (CHAT-28, CHAT-37). Different accounts requesting the same nickname MUST
all be admitted and coexist without affecting each other, each under a distinct assigned
nickname (CHAT-12). Every connection of a member MUST receive the frames broadcast to the room
(CHAT-01). Connections stay independent for transport: each has its own outbound buffer,
liveness and slow-client drop (CHAT-05, CHAT-07).

#### Scenario: Two accounts request the same nickname

- GIVEN two different accounts with no name, assigned `Anonymous` and `Anonymous (2)`
- WHEN a third client sends `hi`
- THEN both of them receive the message

#### Scenario: One duplicate leaves

- GIVEN two different accounts assigned `Anonymous` and `Anonymous (2)`
- WHEN `Anonymous` disconnects
- THEN `Anonymous (2)` remains registered, keeps its nickname and keeps receiving messages

#### Scenario: Two connections of one account share the nickname

- GIVEN account `Ana` is connected to `general` as `Ana`
- WHEN the same account connects to `general` again
- THEN the new connection's `welcome` has `from` = `Ana`
- AND both connections receive every room `chat`

### Requirement: CHAT-11 Welcome message

When a connection registers, the server MUST send that connection — and only that connection —
a `welcome` envelope whose `from` is the nickname of its member (which MAY differ from the
requested one, see CHAT-09 and CHAT-12). When the account is already a member of the room, the
`welcome` MUST carry the member's current nickname, `avatar` and `bio`, not values requested by
the new connection. The `welcome` envelope MUST be the first data frame the connection
receives, followed by its `users` (CHAT-17) and `history` (CHAT-18) frames.

(Existing scenarios unchanged.)

#### Scenario: Later connection inherits the profile

- GIVEN account `Ana` is connected with avatar `U` and bio `hola`
- WHEN the same account connects again with `?avatar=` empty and no `bio`
- THEN its `welcome` has `from` = `Ana`, `avatar` = `U`, `bio` = `hola`
- AND no `profile` frame is broadcast

### Requirement: CHAT-12 Unique nicknames

Nicknames of room members MUST be unique under case-insensitive comparison using Unicode case
folding (so `Bob`, `bob` and `BOB` are the same name). A nickname is assigned when an account
becomes a member of the room (its first registered connection) and kept by all its connections
while it stays a member. The casing requested MUST be preserved in the assigned nickname. When
the normalized requested nickname (CHAT-09) is already taken **by a different member**, the
server MUST assign `<base> (<n>)`, where `n` is the lowest integer ≥ 2 that yields a free name,
and `<base>` is the normalized nickname truncated so the result is at most 32 runes, with
trailing whitespace of the truncated base removed. A nickname MUST become available again as
soon as its member's last connection is removed from the room, by any removal path.

(Existing scenarios unchanged; each named client is a different account.)

#### Scenario: Same account is never suffixed

- GIVEN account `Bob` is connected
- WHEN the same account connects again requesting `Bob`
- THEN the new connection is assigned `Bob`, not `Bob (2)`

#### Scenario: Name kept while one connection remains

- GIVEN account `Bob` with two connections, and a different account requesting `Bob`
  connects and is assigned `Bob (2)`
- WHEN one of `Bob`'s connections closes and a third account requests `Bob`
- THEN the third account is assigned `Bob (3)`

### Requirement: CHAT-17 Online users list

(Requirement text unchanged, except:)

`users` MUST be sorted case-insensitively and contain each **member** exactly once, however
many connections it has. `avatars` and `bios` map member nicknames to the member's profile.

#### Scenario: One entry per account

- GIVEN `A` and account `Ana` with two connections are connected
- WHEN a new client connects
- THEN its `users` frame lists `A` and `Ana` once each

### Requirement: CHAT-22 Direct message delivery

(Requirement text unchanged, except the delivery sentence, which becomes:)

If the recipient is a member of a different account, the server MUST send exactly one `dm`
frame to **each registered connection of the recipient** and the same frame to **each
registered connection of the sender** (the originating connection included), and to no other
client. All copies are identical (same `ts` and `id`). A copy that cannot be queued drops only
that connection as a slow client and does not affect the other copies.

#### Scenario: DM reaches every connection of both accounts

- GIVEN `A` with connections `A1`, `A2`, `B` with connections `B1`, `B2`, and `C`
- WHEN `A1` sends `{"type":"dm","to":"B","text":"hola"}`
- THEN `A1`, `A2`, `B1` and `B2` each receive exactly one identical `dm` frame
- AND `C` receives no frame

### Requirement: CHAT-23 Direct message errors

(Requirement text unchanged, except the `reason` values:)

`self` means the recipient is the sender's own account (any of its connections); `offline` means
no member of the room has that nickname.

#### Scenario: DM to yourself from another tab

- GIVEN account `Ana` with two connections
- WHEN one of them sends `{"type":"dm","to":"ana","text":"hi"}`
- THEN only that connection receives `dm_error` with `reason` = `self`

### Requirement: CHAT-26 Typing signal delivery

(Requirement text unchanged, except:)

- a **room typing signal** goes to every registered connection of the room **except the
  connections of the sender's account**;
- a **direct typing signal** goes to every registered connection of the recipient member, and
  is discarded when the recipient is the sender's own account.

The one-signal-per-second limit stays per connection.

#### Scenario: Own other tab gets no typing

- GIVEN `A` and account `Ana` with connections `Ana1`, `Ana2`
- WHEN `Ana1` sends a room typing signal
- THEN `A` receives `typing` with `from` = `Ana`
- AND neither `Ana1` nor `Ana2` receives it

### Requirement: CHAT-30 Shared music player

(Requirement text unchanged, except the newcomer snapshot sentence, which becomes:)

A connection that registers while a video is current MUST receive one `music` frame with the
current state right after its `users` frame and before its `history` frame (order: `welcome`,
`users`, `music`, `history`), whether or not its account was already a member. `history` is
therefore always the last of a connection's private join frames. The player halts when the last
**connection** leaves the room (unchanged: a member with a connection left keeps it playing).

#### Scenario: Snapshot comes before history

- GIVEN a video is current in `musica`
- WHEN a client joins `musica`
- THEN it receives `welcome`, `users`, `music`, `history` in that order

#### Scenario: Later connection gets the snapshot

- GIVEN account `Ana` is in `musica` while a video plays
- WHEN `Ana` connects again
- THEN the new connection receives a `music` frame before its `history`

### Requirement: CHAT-38 Profile update

(Requirement text unchanged, except:)

A valid profile update from any registered connection replaces the **member's** profile, shared
by all its connections. The `profile` frame is sent to every connection of the room, including
all of the sender's connections. The one-update-per-second limit stays per connection.

#### Scenario: Profile change reaches the other tab

- GIVEN `A` and account `Ana` with connections `Ana1`, `Ana2`
- WHEN `Ana1` sends a valid profile update with bio `nueva`
- THEN `A`, `Ana1` and `Ana2` receive `type` = `profile`, `from` = `Ana`, `bio` = `nueva`
- AND a later connection of `Ana` gets `bio` = `nueva` in its `welcome`

### Requirement: CHAT-41 Nickname derived from the session

(Requirement text unchanged, except the sentence about uniqueness, which becomes:)

The derived value is the "requested nickname" for normalization (CHAT-09) and uniqueness
(CHAT-12). The session email is the connection's account key (CHAT-04): connections with the
same session email are the same member; two people with the same name are different members and
are suffixed. The `nickname` query parameter MUST be ignored. A connection without an account key
MUST NOT be admitted.

The scenario "Same account in two tabs" is replaced by:

#### Scenario: Same account in two tabs

- GIVEN `Ana Pérez` is connected to `general`
- WHEN the same account connects to `general` again
- THEN the new connection is assigned `Ana Pérez` and both stay connected
- AND no `join` is broadcast for it

#### Scenario: Different accounts with the same name

- GIVEN `ana.perez@crombie.dev` named `Ana Pérez` is connected to `general`
- WHEN `ana.p@crombie.dev`, also named `Ana Pérez`, connects to `general`
- THEN it is assigned `Ana Pérez (2)`

## ADDED Requirements

### Requirement: CHAT-43 Chat page with several connections of one account

The chat page MUST work when its account already has other connections in the room:

- it MUST NOT depend on receiving its own `join` to finish joining; in particular, in the shared
  player room (CHAT-31) it MUST apply the `player` snapshot and follow later player frames even
  though no own `join` arrives;
- `dm` frames whose `from` is its own nickname, and `profile` frames for its own nickname, MUST be
  handled as its own (DM shown in the conversation with `to`, own profile and editor updated),
  whichever connection originated them;
- on connecting, it MUST adopt the profile in its `welcome` as its own.

#### Scenario: Second window in the player room follows playback

- GIVEN account `Ana` has a window in `musica` where a video is playing
- WHEN `Ana` opens a second window and it joins `musica`
- THEN the second window shows the current video and position
- AND a later pause from any member is applied in both windows

#### Scenario: DM sent from the other window

- GIVEN account `Ana` has windows `W1` and `W2`, and `B` is online
- WHEN `Ana` sends a DM to `B` from `W1`
- THEN `W2` shows it in its conversation with `B`, as sent by `Ana`, without an unread mark
