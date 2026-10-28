# Delta for Server

## MODIFIED Requirements

### Requirement: SRV-02 Serve the web client

(Requirement text unchanged except the first and the media-type sentences, which become:)

The system MUST serve the chat page at `GET /` and its static assets at `GET /app.js`,
`GET /app.css`, `GET /favicon.svg` and `GET /favicon-unread.svg`, all embedded in the binary,
so that a browser opening the server's root URL can load the page and connect to `/ws` without
any `ALLOWED_ORIGINS` configuration and from any host name that reaches the server (e.g.
`localhost`, `127.0.0.1`, a LAN IP). Each asset MUST be served with its exact media type
(`text/javascript; charset=utf-8`, `text/css; charset=utf-8`, `image/svg+xml`). The page and
every asset MUST be sent with `Cache-Control: no-cache`, so a redeployed binary never pairs a
new page with stale assets.

(Scenarios unchanged, plus:)

#### Scenario: Favicons

- GIVEN the server is running
- WHEN a client requests `GET /favicon.svg` and `GET /favicon-unread.svg`
- THEN each response is 200 with `image/svg+xml`, `X-Content-Type-Options: nosniff` and
  `Cache-Control: no-cache`
- AND the chat page declares `/favicon.svg` as its icon
