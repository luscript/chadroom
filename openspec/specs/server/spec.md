# Server Specification

## Purpose

HTTP surface of the chatroom: the fixed room set and its listing, the WebSocket endpoint's
room selection and origin policy, the GIF search endpoint backed by Giphy, serving the web
client (the page and its two embedded assets, `/app.js` and `/app.css`) under a strict
Content-Security-Policy, and process lifecycle (startup errors and graceful shutdown).

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

The system MUST serve the chat page at `GET /` and its static assets at `GET /app.js`,
`GET /app.css`, `GET /favicon.svg` and `GET /favicon-unread.svg`, all embedded in the binary,
so that a browser opening the server's root URL can load the page and connect to `/ws` without
any `ALLOWED_ORIGINS` configuration and from any host name that reaches the server (e.g.
`localhost`, `127.0.0.1`, a LAN IP). Each asset MUST be served with its exact media type
(`text/javascript; charset=utf-8`, `text/css; charset=utf-8`, `image/svg+xml`). The page and
every asset MUST be sent with `Cache-Control: no-cache`, so a redeployed binary never pairs a
new page with stale assets.
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

#### Scenario: Favicons

- GIVEN the server is running
- WHEN a client requests `GET /favicon.svg` and `GET /favicon-unread.svg`
- THEN each response is 200 with `image/svg+xml`, `X-Content-Type-Options: nosniff` and
  `Cache-Control: no-cache`
- AND the chat page declares `/favicon.svg` as its icon

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
- MUST allow embedded frames only from `https://www.youtube-nocookie.com` (`frame-src`), for the
  shared player (CHAT-31), and MUST NOT allow any YouTube host in `script-src`;
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
- AND the server runs with `GIPHY_API_KEY` set
- WHEN the user joins, sends messages, opens the emoji and GIF panels, searches for a GIF,
  sends one, opens `musica`, presses "Escuchar", puts a YouTube link, pauses, seeks and leaves
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

The room `musica` MUST have a shared player (CHAT-30); the rooms `general` and `juegos` MUST
NOT.

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

### Requirement: SRV-08 GIF search

The server MUST read the Giphy API key from the `GIPHY_API_KEY` environment variable at
startup. The key MUST NOT be sent to the browser and MUST NOT appear in any response body,
response header or log line.

`GET /gifs` MUST behave as follows:

1. If no key is configured, the route MUST NOT exist: the server MUST respond with its generic
   HTTP 404 and MUST NOT contact Giphy.
2. The `q` query parameter is trimmed of surrounding whitespace. If the trimmed `q` is longer
   than 50 characters (Unicode code points), the server MUST respond with HTTP 400 and MUST
   NOT contact Giphy. The body is `{"error":"query_too_long"}`.
3. If the trimmed `q` is empty or absent, the server MUST request Giphy **trending** GIFs.
   Otherwise it MUST request a Giphy **search** for `q`. The server MUST use fixed values for
   the result limit (24) and content rating (`pg-13`), and the client MUST NOT be able to set
   any other upstream parameter.
4. The outbound request MUST NOT follow redirects (a redirect counts as a non-200 status) and MUST time out after at most 5 seconds, and the upstream body MUST be
   read up to at most 1 MiB. If the upstream request fails, times out, returns a non-200
   status or returns a body that cannot be decoded, the server MUST respond with HTTP 502 and
   the body `{"error":"unavailable"}`.
5. On success the server MUST respond with HTTP 200 and the body `{"gifs":[…]}`, where each
   item is `{"url":"…","preview":"…"}` and no other fields appear. `url` is the GIF to send in
   chat and `preview` is a smaller thumbnail (it falls back to `url` when absent).
6. Every relayed URL MUST be `https`, MUST have a Giphy host (`*.giphy.com`) that is also on the
   image allowlist (CHAT-15), MUST
   have no userinfo and no port, and MUST have a path ending in `.gif`. Its query and
   fragment MUST be removed. An item whose `url` fails these rules MUST be omitted, and an
   item whose `preview` fails them MUST use its `url` as `preview`.
7. Every response from `/gifs` with a key configured (200, 400 and 502) MUST send
   `Content-Type: application/json; charset=utf-8`, `Cache-Control: no-cache` and
   `X-Content-Type-Options: nosniff`.

The server SHOULD cache successful results in memory per lowercased trimmed query (trending
counted as one entry) for up to 10 minutes, with at most 100 entries, so that repeated
requests do not consume the shared key's quota.

The page MUST show a GIF search box and a results grid in the GIF panel. With an empty box it
MUST show trending GIFs. Sending a result MUST send its `url` as the message text through the
current conversation (the room or the open DM). The panel MUST show the text
"Powered by GIPHY". When `GET /gifs` answers 404 the page MUST hide the search box and grid
and keep the paste-a-URL field. Result images MUST be built with DOM APIs only (no HTML
strings) and only for URLs that pass the image allowlist check (CHAT-15).

#### Scenario: Trending with no query

- GIVEN the server runs with a Giphy key
- WHEN a client requests `GET /gifs`
- THEN the server requests Giphy trending with the key, limit 24 and rating `pg-13`
- AND the response is 200 with `{"gifs":[{"url":…,"preview":…}, …]}`

#### Scenario: Search

- GIVEN the server runs with a Giphy key
- WHEN a client requests `GET /gifs?q=%20gatos%20`
- THEN the server requests a Giphy search for `gatos` with limit 24 and rating `pg-13`
- AND the response is 200 with the trimmed result list

#### Scenario: No key configured

- GIVEN `GIPHY_API_KEY` is unset
- WHEN a client requests `GET /gifs?q=gatos`
- THEN the response is 404 and Giphy is not contacted

#### Scenario: Query too long

- GIVEN the server runs with a Giphy key
- WHEN a client requests `GET /gifs` with a `q` of 51 characters
- THEN the response is 400 and Giphy is not contacted

#### Scenario: Client cannot inject upstream parameters

- GIVEN the server runs with a Giphy key
- WHEN a client requests `GET /gifs?q=a&limit=500&rating=r&api_key=x`
- THEN the upstream request uses limit 24, rating `pg-13` and the server's own key

#### Scenario: Upstream failure

- GIVEN the server runs with a Giphy key
- WHEN Giphy answers 500, or does not answer within the timeout
- THEN the response is 502 with `{"error":"unavailable"}`
- AND neither the response nor the server log contains the key

#### Scenario: Untrusted URLs are filtered and canonicalized

- GIVEN Giphy returns one item on `https://media1.giphy.com/media/abc/200.gif?cid=x&rid=y`,
  one on `http://media.giphy.com/…`, one on `https://evil.example/x.gif` and one whose path
  ends in `.mp4`
- WHEN a client requests `GET /gifs`
- THEN the response contains only `https://media1.giphy.com/media/abc/200.gif`

#### Scenario: Repeated query served from cache

- GIVEN the server runs with a Giphy key and `GET /gifs?q=Gatos` succeeded
- WHEN a client requests `GET /gifs?q=gatos` within 10 minutes
- THEN the response is 200 with the same results and Giphy is not contacted again

#### Scenario: Pick a GIF in the page

- GIVEN the user is in a room (or a DM tab) and the server has a Giphy key
- WHEN the user opens the GIF panel, types `gatos` and clicks a result
- THEN the result's `url` is sent as a message in that conversation and renders inline
- AND the panel closes

#### Scenario: Page without a key

- GIVEN `GIPHY_API_KEY` is unset
- WHEN the user opens the GIF panel
- THEN the search box and grid are hidden and the paste-a-URL field still works
