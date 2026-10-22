# Server Specification

## Purpose

HTTP surface of the chatroom: the fixed room set and its listing, the WebSocket endpoint's
room selection and origin policy, serving the web client (the page and its two embedded
assets, `/app.js` and `/app.css`) under a strict Content-Security-Policy, and process
lifecycle (startup errors and graceful shutdown).

## Requirements

### Requirement: SRV-01 Origin check on WebSocket upgrade

The system MUST reject WebSocket upgrade requests on `GET /ws` whose `Origin` header is
present and is neither the same origin as the request host nor listed in the configured
allowlist. Rejected requests MUST receive HTTP 403 and MUST NOT be upgraded. The `null`
origin MUST NOT be accepted by default. Requests without an `Origin` header (non-browser
clients) MAY be upgraded, since CSWSH requires a browser, which always sends `Origin`.

The allowlist SHALL be read from the `ALLOWED_ORIGINS` environment variable as a
comma-separated list of exact origins (`scheme://host[:port]`); surrounding whitespace and
empty entries SHALL be ignored.

#### Scenario: Same-origin browser client

- GIVEN the server runs at `localhost:8080`
- WHEN a client upgrades `/ws` with `Origin: http://localhost:8080`
- THEN the upgrade succeeds

#### Scenario: Foreign origin rejected

- GIVEN `ALLOWED_ORIGINS` is unset
- WHEN a client upgrades `/ws` with `Origin: https://evil.example`
- THEN the response is 403 and no WebSocket is established

#### Scenario: Allowlisted origin

- GIVEN `ALLOWED_ORIGINS="http://localhost:3000, https://chat.example.com"`
- WHEN a client upgrades `/ws` with `Origin: http://localhost:3000`
- THEN the upgrade succeeds

#### Scenario: Null origin rejected by default

- GIVEN `ALLOWED_ORIGINS` is unset
- WHEN a client upgrades `/ws` with `Origin: null`
- THEN the response is 403

#### Scenario: Non-browser client without Origin

- GIVEN any configuration
- WHEN a client upgrades `/ws` without an `Origin` header
- THEN the upgrade succeeds

### Requirement: SRV-02 Serve the web client

The system MUST serve the chat page at `GET /` and its static assets at `GET /app.js` and
`GET /app.css`, all embedded in the binary, so that a browser opening the server's root URL
can load the page and connect to `/ws` without any `ALLOWED_ORIGINS` configuration and from
any host name that reaches the server (e.g. `localhost`, `127.0.0.1`, a LAN IP). Each asset
MUST be served with its exact media type (`text/javascript; charset=utf-8`,
`text/css; charset=utf-8`). The page and both assets MUST be sent with
`Cache-Control: no-cache`, so a redeployed binary never pairs a new page with stale assets.
No other path MUST expose embedded files (no directory listing).

#### Scenario: Open the chat page

- GIVEN the server is running at `localhost:8080`
- WHEN a browser requests `GET http://localhost:8080/`
- THEN it receives 200 with the chat page (`text/html`)
- AND the page loads `/app.js` and `/app.css` from the same origin
- AND the page's WebSocket connection to `/ws` succeeds

#### Scenario: Open the page by IP

- GIVEN the server is running on port 8080 with no `ALLOWED_ORIGINS`
- WHEN a browser opens `http://127.0.0.1:8080/`
- THEN the page's WebSocket connection to `ws://127.0.0.1:8080/ws` succeeds

#### Scenario: Static assets

- GIVEN the server is running
- WHEN a client requests `GET /app.js` and `GET /app.css`
- THEN each response is 200 with `text/javascript; charset=utf-8` and
  `text/css; charset=utf-8` respectively
- AND each response, like `GET /`, includes `Cache-Control: no-cache`

#### Scenario: Unknown path

- GIVEN the server is running
- WHEN a client requests `GET /index.html` or `GET /static/`
- THEN the response is 404

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

### Requirement: SRV-04 Startup errors are reported

If the HTTP server fails to start or stops with an error other than a requested shutdown,
the process MUST log the error and exit with a non-zero status.

#### Scenario: Port already in use

- GIVEN port 8080 is already bound by another process
- WHEN the server starts
- THEN it logs the listen error
- AND exits with a non-zero status

### Requirement: SRV-05 HTTP hardening

The HTTP server MUST set `ReadHeaderTimeout`, `IdleTimeout` and `MaxHeaderBytes` and MUST
NOT trust proxy forwarding headers by default. Every response for the page and its assets
MUST send `X-Content-Type-Options: nosniff`. `GET /` MUST also send
`X-Frame-Options: DENY`, `Referrer-Policy: no-referrer` and a `Content-Security-Policy` that:

- MUST NOT contain `'unsafe-inline'` or `'unsafe-eval'` in any directive;
- MUST allow scripts only from the page's own origin (`script-src 'self'`);
- MUST allow images only from the page's own origin and from the hosts on the image allowlist
  (CHAT-15), over `https:`;
- MUST allow network connections only to the page's own origin (`connect-src 'self'`);
- MUST forbid framing (`frame-ancestors 'none'`) and `<base>` changes (`base-uri 'none'`);
- MUST fall back to denying anything not explicitly allowed (`default-src 'none'`).

The page MUST work under that policy with no CSP violations reported by the browser.

#### Scenario: Security headers on the page

- GIVEN the server is running
- WHEN a client requests `GET /`
- THEN the response includes `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY` and
  `Referrer-Policy: no-referrer`
- AND it includes a `Content-Security-Policy` meeting every rule above

#### Scenario: Security headers on assets

- GIVEN the server is running
- WHEN a client requests `GET /app.js` or `GET /app.css`
- THEN the response includes `X-Content-Type-Options: nosniff`

#### Scenario: No inline code in the page

- GIVEN the embedded chat page
- WHEN its HTML is inspected
- THEN it contains no inline `<script>` content, no inline `<style>` element, no `style`
  attribute and no `on*` event-handler attribute

#### Scenario: Page works under the policy

- GIVEN the page is opened in a current Chrome or Firefox with the developer console open
- WHEN the user joins, sends messages, opens the emoji and GIF panels and leaves
- THEN everything renders styled and works
- AND the console reports no Content-Security-Policy violations

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
