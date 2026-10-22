# Delta for Server

## MODIFIED Purpose

HTTP surface of the chatroom: the fixed room set and its listing, the WebSocket endpoint's
room selection and origin policy, serving the web client (the page and its two embedded
assets, `/app.js` and `/app.css`) under a strict Content-Security-Policy, and process
lifecycle (startup errors and graceful shutdown).

## ADDED Requirements

### Requirement: SRV-06 Room selection on the WebSocket endpoint

The server MUST run a fixed, ordered set of rooms defined in the server code: `general`,
`juegos`, `musica`. The first room is the **default room**. Room ids are lowercase ASCII and
MUST NOT change at runtime.

`GET /ws` MUST select the room from the `room` query parameter:

- if `room` is absent or empty, the connection MUST join the default room;
- if `room` exactly matches (case-sensitive) a room id, the connection MUST join that room;
- otherwise the server MUST respond with HTTP 404, MUST NOT upgrade the connection, and MUST
  NOT create a room.

Room selection MUST happen before the upgrade. The origin policy (SRV-01) MUST apply equally
to every room. When both checks fail, the server MUST respond with 404 or 403 and MUST NOT
upgrade.

#### Scenario: Join a named room

- GIVEN the server is running
- WHEN a client upgrades `/ws?room=juegos&nickname=Ana`
- THEN the upgrade succeeds and the client is registered in `juegos`

#### Scenario: Default room

- GIVEN the server is running
- WHEN a client upgrades `/ws?nickname=Ana`
- THEN the upgrade succeeds and the client is registered in `general`

#### Scenario: Unknown room rejected

- GIVEN the server is running
- WHEN a client requests `/ws?room=secret&nickname=Ana`, or `/ws?room=Juegos`
- THEN the response is 404 and no WebSocket is established

#### Scenario: Foreign origin rejected for any room

- GIVEN `ALLOWED_ORIGINS` is unset
- WHEN a client upgrades `/ws?room=juegos` with `Origin: https://evil.example`
- THEN the response is 403 and no WebSocket is established

### Requirement: SRV-07 Room list

`GET /rooms` MUST return HTTP 200 with `Content-Type: application/json; charset=utf-8`,
`Cache-Control: no-cache` and `X-Content-Type-Options: nosniff`, and the body
`{"rooms":["general","juegos","musica"]}`: every room id, in the configured order, with the
default room first. The response MUST NOT include room members or any other state.

#### Scenario: List rooms

- GIVEN the server is running
- WHEN a client requests `GET /rooms`
- THEN the response is 200 with JSON `{"rooms":["general","juegos","musica"]}`

## MODIFIED Requirements

### Requirement: SRV-03 Graceful shutdown

On SIGINT or SIGTERM the server MUST stop accepting new connections, close all active
WebSocket clients **of every room** and exit within a bounded shutdown timeout. The HTTP
server MUST be shut down only after every room has stopped (CHAT-08).

#### Scenario: SIGTERM with connected clients

- GIVEN the server has connected WebSocket clients
- WHEN the process receives SIGTERM
- THEN all WebSocket connections are closed
- AND the process exits with status 0 within the shutdown timeout

#### Scenario: SIGTERM with clients in several rooms

- GIVEN clients connected to `general` and to `juegos`
- WHEN the process receives SIGTERM
- THEN the connections in both rooms are closed before the HTTP server stops
- AND the process exits with status 0 within the shutdown timeout
