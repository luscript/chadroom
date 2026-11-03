# Server Specification

## Purpose

HTTP surface of the chatroom: the fixed room set and its listing, the WebSocket endpoint's
room selection and origin policy, the GIF search endpoint backed by Giphy, serving the web
client (the page and its two embedded assets, `/app.js` and `/app.css`) under a strict
Content-Security-Policy, authentication with Google OpenID Connect restricted to the company
domain (login, session cookie, logout, protected routes, auth pages), and process lifecycle
(startup errors and graceful shutdown).

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

`GET /` MUST serve the chat page only with a valid session (SRV-15); without one it MUST serve
the login page instead (SRV-16), at the same URL and without a redirect. `GET /app.js` MUST require
a valid session. `GET /app.css`, `GET /favicon.svg`, `GET /favicon-unread.svg` MUST stay public,
since the auth pages use them. Because `GET /` varies with the cookie, it MUST send
`Vary: Cookie`. Scenarios that open the chat page or load `/app.js` assume a valid session cookie.

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

#### Scenario: Page without a session

- GIVEN a browser without a session cookie
- WHEN it requests `GET /`
- THEN the response is 200 with the login page (SRV-16)
- AND the chat page and `/app.js` are not sent

#### Scenario: Public stylesheet

- GIVEN a client without a session cookie
- WHEN it requests `GET /app.css` or `GET /favicon.svg`
- THEN the response is 200 with the usual media type and headers

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
  sends one, opens `musica`, puts a YouTube link (and presses "Escuchar" only if the blocked
  warning appears), queues a second link, removes it, queues another, skips, pauses, seeks and
  leaves
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

The `nickname` query parameter is not read: the nickname comes from the session (CHAT-41). The
session check (SRV-15) MUST happen before room selection and before the origin check; a request
without a valid session MUST receive 401 regardless of `room` or `Origin`. The scenarios below
assume a valid session cookie unless they say otherwise.

Room selection MUST happen before the upgrade. The origin policy (SRV-01) MUST apply equally
to every room. When both checks fail, the server MUST respond with 404 or 403 and MUST NOT
upgrade.

The room `musica` MUST have a shared player (CHAT-30); the rooms `general` and `juegos` MUST
NOT.

#### Scenario: Join a named room

- GIVEN the server is running
- WHEN a client upgrades `/ws?room=juegos`
- THEN the upgrade succeeds and the client is registered in `juegos`

#### Scenario: Default room

- GIVEN the server is running
- WHEN a client upgrades `/ws`
- THEN the upgrade succeeds and the client is registered in `general`

#### Scenario: Unknown room rejected

- GIVEN the server is running
- WHEN a client requests `/ws?room=secret`, or `/ws?room=Juegos`
- THEN the response is 404 and no WebSocket is established

#### Scenario: Foreign origin rejected for any room

- GIVEN `ALLOWED_ORIGINS` is unset
- WHEN a client upgrades `/ws?room=juegos` with `Origin: https://evil.example`
- THEN the response is 403 and no WebSocket is established

#### Scenario: Unknown room without a session

- GIVEN a client without a session cookie
- WHEN it requests `/ws?room=secret`
- THEN the response is 401 (not 404) and no WebSocket is established

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

### Requirement: SRV-09 YouTube playlist expansion

The server MUST read the YouTube Data API key from the `YOUTUBE_API_KEY` environment variable at
startup (surrounding whitespace trimmed). The key MUST NOT be sent to the browser and MUST NOT
appear in any response body, response header or log line.

`GET /playlist` MUST behave as follows:

1. If no key is configured, the route MUST NOT exist: the server MUST respond with its generic
   HTTP 404 and MUST NOT contact YouTube. The server SHOULD log once at startup that playlist
   import is disabled.
2. The `list` query parameter MUST match `^[A-Za-z0-9_-]{10,64}$`. Otherwise the server MUST
   respond with HTTP 400 and the body `{"error":"invalid_list"}` and MUST NOT contact YouTube.
3. The server MUST request the first page (at most 50 items) of the playlist's items from the
   YouTube Data API v3 at a fixed host, and then the status of the remaining videos with one
   more request. The client MUST NOT be able to set any other upstream parameter.
4. The returned ids are, in playlist order, the items' video ids that match
   `^[A-Za-z0-9_-]{11}$`, without duplicates, excluding videos that are private, deleted, not
   embeddable or not fully processed (any video whose status is not returned counts as
   unavailable).
5. If YouTube reports that the playlist does not exist or is not accessible, the server MUST
   respond with HTTP 404 and the body `{"error":"not_found"}`.
6. The outbound requests MUST NOT follow redirects (a redirect counts as a failure), the whole
   expansion (both requests) MUST time out after at most 5 seconds, and each upstream body MUST
   be read up to at most 1 MiB.
   If an upstream request fails, times out, returns any other non-200 status (including quota
   errors) or returns a body that cannot be decoded, the server MUST respond with HTTP 502 and
   the body `{"error":"unavailable"}`.
7. On success the server MUST respond with HTTP 200 and the body `{"videos":[…]}` (at most 50
   strings; `[]` when no video is playable), with no other fields.
8. Every response from `/playlist` with a key configured (200, 400, 404 and 502) MUST send
   `Content-Type: application/json; charset=utf-8`, `Cache-Control: no-cache` and
   `X-Content-Type-Options: nosniff`.

The server SHOULD cache non-empty successful results in memory per `list` for up to 10 minutes,
with at most 100 entries, so that repeated imports do not consume the shared key's quota.

The upstream calls MUST NOT run in a room's goroutine (CHAT-03): the page sends the result with
`add_many` (CHAT-30, CHAT-31).

#### Scenario: Playlist is expanded

- GIVEN the server runs with a YouTube key
- WHEN a client requests `GET /playlist?list=PLlb3q7unEBfNPa0aR-cOQB33HDAwyDLpF`
- THEN the server requests that playlist's items (at most 50) and their videos' status with its
  own key
- AND the response is 200 with `{"videos":[…]}` in playlist order

#### Scenario: Unplayable videos are filtered

- GIVEN YouTube returns items for a public video, a private video, a deleted video, a video
  that is not embeddable, the public video again, and an item whose id is `../x`
- WHEN a client requests `GET /playlist?list=PLxxxxxxxxxx`
- THEN the response contains only the public video, once

#### Scenario: No key configured

- GIVEN `YOUTUBE_API_KEY` is unset
- WHEN a client requests `GET /playlist?list=PLxxxxxxxxxx`
- THEN the response is 404 and YouTube is not contacted

#### Scenario: Invalid list id

- GIVEN the server runs with a YouTube key
- WHEN a client requests `GET /playlist?list=abc`, `GET /playlist?list=PL%2F..%2Fxxxxxxxx`, or
  `GET /playlist` without `list`
- THEN the response is 400 with `{"error":"invalid_list"}` and YouTube is not contacted

#### Scenario: Client cannot inject upstream parameters

- GIVEN the server runs with a YouTube key
- WHEN a client requests `GET /playlist?list=PLxxxxxxxxxx&maxResults=500&key=x&part=snippet`
- THEN the upstream requests use the server's own fixed parameters and key

#### Scenario: Playlist not found

- GIVEN the server runs with a YouTube key
- WHEN YouTube reports that the playlist does not exist or is private
- THEN the response is 404 with `{"error":"not_found"}`

#### Scenario: Upstream failure

- GIVEN the server runs with a YouTube key
- WHEN YouTube answers 500, answers 403 for an exceeded quota, redirects, or does not answer
  within the timeout
- THEN the response is 502 with `{"error":"unavailable"}`
- AND neither the response nor the server log contains the key

#### Scenario: Repeated import is cached

- GIVEN the server runs with a YouTube key and a playlist was expanded with a non-empty result
- WHEN a client requests the same `list` within 10 minutes
- THEN the response is the same and YouTube is not contacted

### Requirement: SRV-10 Authentication configuration

At startup the server MUST read:

| Variable | Default | Rule |
|----------|---------|------|
| `GOOGLE_CLIENT_ID` | — | required, non-empty after trimming |
| `GOOGLE_CLIENT_SECRET` | — | required, non-empty after trimming |
| `OIDC_REDIRECT_URL` | `http://localhost:8080/auth/callback` | absolute URL with a host and path `/auth/callback`; `https`, or `http` only for a loopback host (`localhost`, `127.0.0.0/8`, `::1`) |
| `ALLOWED_EMAIL_DOMAIN` | `crombie.dev` | a bare ASCII domain (no `@`), compared case-insensitively |
| `SESSION_SECRET` | — | required, at least 32 bytes |
| `SESSION_TTL` | `12h` | Go duration, between 5 minutes and 7 days |

If any rule fails the process MUST log an error naming the variable (never its value) and exit
with a non-zero status before listening. The server MUST NOT run without authentication and MUST
NOT generate a session secret on its own. Cookies are `Secure` exactly when `OIDC_REDIRECT_URL`
uses `https`.

#### Scenario: Missing session secret

- GIVEN `SESSION_SECRET` is unset
- WHEN the server starts
- THEN it logs an error naming `SESSION_SECRET`
- AND exits with a non-zero status without listening

#### Scenario: Short session secret

- GIVEN `SESSION_SECRET` is 16 bytes long
- WHEN the server starts
- THEN it exits with a non-zero status and the log does not contain the secret

#### Scenario: Plain http outside loopback rejected

- GIVEN `OIDC_REDIRECT_URL=http://chat.example.com/auth/callback`
- WHEN the server starts
- THEN it exits with a non-zero status naming `OIDC_REDIRECT_URL`

#### Scenario: Defaults

- GIVEN only `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` and a 32-byte `SESSION_SECRET` are set
- WHEN the server starts
- THEN the redirect URL is `http://localhost:8080/auth/callback`, the domain is `crombie.dev`,
  the session lifetime is 12 hours and cookies are not `Secure`

### Requirement: SRV-11 Login flow

`GET /auth/login` MUST start an OpenID Connect authorization-code flow against the issuer
`https://accounts.google.com`, requesting scopes `openid email profile`, with:

- a fresh random `state` and `nonce` (each at least 128 bits from `crypto/rand`);
- PKCE with method `S256` and a fresh random verifier;
- the `hd` parameter set to the allowed domain (a hint only, never trusted);
- `state`, `nonce`, the verifier and the post-login path stored in a signed flow cookie
  (`HttpOnly`, `SameSite=Lax`, `Secure` per SRV-10, scoped to `/auth`, lifetime ≤ 10 minutes).

The post-login path comes from the optional `next` query parameter. It MUST be used only if it
is a relative path of this server: it starts with a single `/`, the second character is
neither `/` nor `\`, it contains no `\`, spaces or control characters, it has no scheme, host or
user info, it is not under `/auth`, and it is at most 512 bytes; otherwise `/` MUST be used. It
MUST be re-validated before the post-login redirect.

When the request carries `switch=1`, the authorization request MUST also set
`prompt=select_account`, so the user can choose another Google account (used by the auth pages).

`GET /auth/callback` MUST:

1. reject (400) when the flow cookie is missing, invalid or expired, when the `state` parameter
   does not match the cookie (constant-time comparison), when the provider returned an `error`
   parameter, or when `code` is missing;
2. exchange the code with the PKCE verifier; failure, or a token response without an
   `id_token`, → 400;
3. verify the ID token with the provider's keys (signature, issuer, audience = client id,
   expiry) and check that its `nonce` equals the cookie's nonce; any failure → 400;
4. apply the authorization rules (SRV-12); failure → 403;
5. on success: delete the flow cookie, set the session cookie (SRV-13) and redirect (302) to the
   stored post-login path.

The flow cookie MUST be deleted on every callback outcome. Access and refresh tokens MUST be
discarded and never stored. Error responses MUST be generic and MUST NOT reveal which check
failed.

#### Scenario: Successful login

- GIVEN a provider that authenticates `ana@crombie.dev` with `email_verified=true` and
  `hd=crombie.dev`
- WHEN the browser completes `/auth/login` → provider → `/auth/callback`
- THEN the response is 302 to `/` with a session cookie
- AND a following `GET /` returns the chat page

#### Scenario: State mismatch

- GIVEN a valid flow cookie
- WHEN `/auth/callback` is called with a different `state`
- THEN the response is 400, no session cookie is set and the code is not exchanged

#### Scenario: Missing flow cookie

- GIVEN no flow cookie
- WHEN `/auth/callback?state=x&code=y` is called
- THEN the response is 400

#### Scenario: Nonce mismatch

- GIVEN the provider returns an ID token whose `nonce` differs from the flow cookie's
- WHEN the callback completes the exchange
- THEN the response is 400 and no session cookie is set

#### Scenario: PKCE is used

- GIVEN `/auth/login` was requested
- THEN the authorization redirect carries `code_challenge_method=S256` and a `code_challenge`
- AND the token request on callback carries the matching `code_verifier`

#### Scenario: Open redirect refused

- GIVEN a user requests `/auth/login?next=//evil.example/x` (or `https://evil.example`,
  `/\evil.example`)
- WHEN the login completes successfully
- THEN the redirect goes to `/`

### Requirement: SRV-12 Domain authorization

The server MUST authorize a verified ID token only if all of these hold:

- the `email_verified` claim is the boolean `true`;
- the `email` claim contains `@`, and the part after the **last** `@` equals
  `ALLOWED_EMAIL_DOMAIN` under ASCII case-insensitive comparison (whole-string equality, not a
  suffix check);
- the `hd` claim equals `ALLOWED_EMAIL_DOMAIN` under the same comparison.

Otherwise the server MUST respond 403 with the access-denied page (SRV-16), MUST NOT set a session
cookie and MUST NOT reveal which rule failed.

#### Scenario: Allowed domain

- GIVEN claims `email=ana@crombie.dev`, `email_verified=true`, `hd=crombie.dev`
- THEN the login succeeds

#### Scenario: Look-alike domains rejected

- GIVEN `email` is `eve@evilcrombie.dev`, `eve@crombie.dev.evil.com` or `eve@gmail.com`
  (with `hd` set to the same domain or absent)
- THEN the response is 403

#### Scenario: Unverified email rejected

- GIVEN `email=ana@crombie.dev`, `email_verified=false`, `hd=crombie.dev`
- THEN the response is 403

#### Scenario: hd mismatch rejected

- GIVEN `email=ana@crombie.dev`, `email_verified=true` and `hd` absent or `other.com`
- THEN the response is 403

### Requirement: SRV-13 Session cookie

A successful login MUST set a session cookie that carries a random session id, the email, the
display name and an absolute expiry (`now + SESSION_TTL`), authenticated with HMAC-SHA256 under
`SESSION_SECRET`. The cookie MUST be `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` per SRV-10,
and expire with the session. A cookie MUST be rejected when its encoding is invalid, its MAC
does not verify (constant-time comparison), it has expired, or its session id was revoked
(SRV-14). The session MUST NOT contain OAuth tokens. The flow cookie and the session cookie MUST
use distinct MAC contexts, so neither is accepted as the other.

#### Scenario: Tampered cookie

- GIVEN a valid session cookie with one byte of its payload or MAC changed
- WHEN it is sent to `GET /rooms`
- THEN the response is 401

#### Scenario: Expired cookie

- GIVEN a session cookie whose expiry is in the past
- WHEN it is sent to `GET /`
- THEN the response is the login page, not the chat page

#### Scenario: Flow cookie is not a session

- GIVEN the value of a valid flow cookie
- WHEN it is sent as the session cookie
- THEN it is rejected

### Requirement: SRV-14 Logout

`POST /auth/logout` MUST revoke the request's session id (in memory, until the session would
have expired), MUST clear the session cookie (`Max-Age` ≤ 0, same name, path and flags) and MUST
respond 204. It MUST respond 204 also when there is no valid session. Requests with an `Origin`
header that is `null`, unparsable, or whose host differs from the request host MUST receive 403
and change nothing. Only `POST` is accepted (no CSRF token: `SameSite=Lax` plus the Origin check). Revocation is
lost on restart (sessions remain stateless otherwise).

#### Scenario: Logout invalidates the session

- GIVEN a valid session cookie `C`
- WHEN the client posts `/auth/logout` with `C`
- THEN the response is 204 and clears the cookie
- AND a later request to `/rooms` with `C` receives 401

#### Scenario: Cross-site logout refused

- GIVEN a valid session cookie
- WHEN `/auth/logout` is posted with `Origin: https://evil.example`
- THEN the response is 403 and the session stays valid

### Requirement: SRV-15 Protected routes

Every route except `/auth/*`, `/app.css`, `/favicon.svg` and `/favicon-unread.svg` MUST require a
valid session (SRV-13). Without one:

- `GET /` MUST respond 200 with the login page (SRV-16);
- `GET /app.js`, `GET /rooms`, `GET /gifs`, `GET /playlist` and `GET /ws` MUST respond 401 with
  `{"error":"unauthorized"}` and `Cache-Control: no-store`; `/ws` MUST NOT be upgraded and MUST
  NOT reveal whether the room exists.

Session data MUST be read only from the session cookie, never from the query string or other
headers. The authenticated identity (email, name) MUST be made available to handlers through
the request context.

#### Scenario: WebSocket without a session

- GIVEN no session cookie
- WHEN a client upgrades `/ws?room=general`
- THEN the response is 401 and no WebSocket is established

#### Scenario: APIs without a session

- GIVEN no session cookie
- WHEN a client requests `/rooms`, `/gifs` (with a Giphy key configured) or `/playlist`
  (with a YouTube key configured)
- THEN each response is 401 and the upstream API is not contacted

#### Scenario: WebSocket with a session

- GIVEN a valid session cookie and `Origin: http://localhost:8080`
- WHEN the client upgrades `/ws?room=juegos`
- THEN the upgrade succeeds

### Requirement: SRV-16 Auth pages

The server MUST serve two embedded static pages, without inline script or style, with the same
security headers as the chat page (SRV-05) and `Cache-Control: no-store`:

- a **login** page, served by `GET /` without a valid session (SRV-02). It reuses the look of the
  former nickname screen (brand, headline, card), with — instead of the nickname input — a
  primary "Iniciar sesión con Google" link to `/auth/login` (account switching is offered only on
  the access-denied page, where a wrong single Google session would otherwise loop);
- an **access denied** page, sent with status 403 when authorization fails (SRV-12), saying only
  that the account is not allowed and offering a link to `/auth/login?switch=1`.

There is no separate signed-out page: `GET /auth/signed-out` MUST NOT exist (404).

Pages for 400 callback errors MAY reuse the access-denied wording. These pages MUST NOT contain
the email, name or any claim.

#### Scenario: Login page

- GIVEN a browser without a session cookie
- WHEN it opens `/`
- THEN it sees "Iniciar sesión con Google" and is not redirected to Google until it clicks it

#### Scenario: Denied page

- GIVEN a login with `eve@gmail.com`
- THEN the 403 response is the access-denied page with `Content-Security-Policy` and no inline
  script

### Requirement: SRV-17 Login rate limit

`GET /auth/login` and `GET /auth/callback` MUST be rate limited per client IP (remote address;
proxy headers are not trusted, SRV-05; IPv6 clients are keyed by their `/64` prefix). A client exceeding the limit (default: 20 requests per
minute, shared by both routes) MUST receive 429 and MUST NOT get a redirect to the provider or a
token exchange. Limiter state MUST be bounded in memory (entries for idle IPs are dropped).

#### Scenario: Burst of logins

- GIVEN a client that already made 20 login requests in the current minute
- WHEN it requests `/auth/login` again
- THEN the response is 429

### Requirement: SRV-18 Secrets stay out of logs and responses

The client secret, `SESSION_SECRET`, cookie values, authorization codes, `state`, `nonce`, PKCE
verifiers and tokens MUST NOT appear in any log line or response body. Errors from the token
exchange or verification MUST be logged, if at all, as a fixed message without the underlying
error text. The access log MUST keep omitting query strings. Panic recovery MUST NOT dump request
headers (gin's default recovery masks only `Authorization` and would log the `Cookie` header), so
the server MUST use its own recovery middleware that logs only the panic value and route.

#### Scenario: Failed exchange is not logged verbatim

- GIVEN the token endpoint responds 400 with a body containing the code
- WHEN the callback fails
- THEN no log line contains the code or the response body
