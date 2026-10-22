# Delta for Server

> On archive, also update the `## Purpose` paragraph of `openspec/specs/server/spec.md`:
> "serving the web client" now covers the page and its two static assets (`/app.js`,
> `/app.css`) and the page's Content-Security-Policy.

## MODIFIED Requirements

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

### Requirement: SRV-05 HTTP hardening

The HTTP server MUST set `ReadHeaderTimeout`, `IdleTimeout` and `MaxHeaderBytes` and MUST
NOT trust proxy forwarding headers by default. Every response for the page and its assets
MUST send `X-Content-Type-Options: nosniff`. `GET /` MUST also send
`X-Frame-Options: DENY` and a `Content-Security-Policy` that:

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
- THEN the response includes `X-Content-Type-Options: nosniff` and `X-Frame-Options: DENY`
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
