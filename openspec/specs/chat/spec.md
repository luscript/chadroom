# Chat Specification

## Purpose

Behavior of the chat room hub and of each client connection: registration, message
broadcast, disconnection, connection limits and liveness. The wire protocol is unchanged:
plain text WebSocket frames; chat messages are delivered as `"<nickname>: <text>"`; join and
leave notices as `"<nickname> joined! 🎉"` / `"<nickname> left. 👋"`.

## Requirements

### Requirement: CHAT-01 Broadcast delivery

The system MUST deliver every chat message received from a registered client to all
registered clients, including the sender, formatted as `"<nickname>: <text>"`.

#### Scenario: Message reaches every client

- GIVEN clients A and B are connected to the room
- WHEN A sends the text frame `hola`
- THEN A and B each receive `A: hola`

### Requirement: CHAT-02 Join and leave notices

The system MUST broadcast a join notice when a client registers and a leave notice when a
registered client disconnects. A leave notice MUST be broadcast at most once per client.
Server-initiated removals — slow-client drop (CHAT-05) and room shutdown (CHAT-08) — are
silent: no leave notice is broadcast for them.

#### Scenario: Join notice

- GIVEN client A is connected
- WHEN client B connects with nickname `B`
- THEN A and B receive `B joined! 🎉`

#### Scenario: Leave notice exactly once

- GIVEN clients A and B are connected
- WHEN B closes its connection
- THEN A receives `B left. 👋` exactly once

### Requirement: CHAT-03 Hub never blocks on itself

The hub MUST NOT send to any channel that it alone consumes. Joins, leaves and broadcasts
MUST continue to be processed while the broadcast queue is saturated.

#### Scenario: Saturated broadcast queue during joins

- GIVEN the broadcast queue is at capacity
- WHEN new clients connect and existing clients disconnect concurrently
- THEN every registration and unregistration completes
- AND the hub keeps delivering messages (no deadlock)

### Requirement: CHAT-04 Client identity is independent of nickname

The system MUST identify clients by connection, not by nickname. Clients sharing a nickname
MUST coexist without affecting each other.

#### Scenario: Two clients with the same nickname

- GIVEN two clients connected with nickname `Anonymous`
- WHEN a third client sends `hi`
- THEN both `Anonymous` clients receive the message

#### Scenario: One duplicate leaves

- GIVEN two clients connected with nickname `Anonymous`
- WHEN the first one disconnects
- THEN the second one remains registered and keeps receiving messages

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
connection. The oversized message MUST NOT be broadcast.

#### Scenario: Oversized frame

- GIVEN clients A and B are connected
- WHEN A sends a frame larger than 4096 bytes
- THEN A's connection is closed
- AND B does not receive A's message
- AND B receives `A left. 👋`

#### Scenario: Message at the limit

- GIVEN client A is connected
- WHEN A sends a frame of exactly 4096 bytes
- THEN the message is broadcast normally

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
binary, 1007 for invalid UTF-8) and MUST NOT be broadcast. Nicknames MUST be normalized
before use: invalid UTF-8 sequences replaced, surrounding whitespace trimmed, truncated to
32 runes, and empty results replaced by `Anonymous`. (Charset rules and impersonation
protection remain out of scope — backlog #10.)

#### Scenario: Binary frame rejected

- GIVEN clients A and B are connected
- WHEN A sends a binary frame
- THEN A's connection is closed
- AND B does not receive the payload

#### Scenario: Invalid UTF-8 text rejected

- GIVEN clients A and B are connected
- WHEN A sends a text frame containing byte 0xff
- THEN A's connection is closed
- AND B does not receive the payload

#### Scenario: Nickname normalization

- GIVEN a client connects with `?nickname=%ff` or a nickname longer than 32 runes
- WHEN it joins
- THEN the join notice uses a valid UTF-8 nickname of at most 32 runes

#### Scenario: Default nickname

- GIVEN a client connects without a nickname (or only whitespace)
- WHEN it joins
- THEN the join notice is `Anonymous joined! 🎉`
