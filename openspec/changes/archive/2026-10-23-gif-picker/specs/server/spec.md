# Delta for Server

## MODIFIED Purpose

HTTP surface of the chatroom: the fixed room set and its listing, the WebSocket endpoint's
room selection and origin policy, the GIF search endpoint backed by Giphy, serving the web
client (the page and its two embedded assets, `/app.js` and `/app.css`) under a strict
Content-Security-Policy, and process lifecycle (startup errors and graceful shutdown).

## MODIFIED Requirements

### Requirement: SRV-05 HTTP hardening

(Requirement text unchanged; only the last scenario changes.)

#### Scenario: Page works under the policy

- GIVEN the page is opened in a current Chrome or Firefox with the developer console open
- AND the server runs with `GIPHY_API_KEY` set
- WHEN the user joins, sends messages, opens the emoji and GIF panels, searches for a GIF,
  sends one and leaves
- THEN everything renders styled and works
- AND the console reports no Content-Security-Policy violations

## ADDED Requirements

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
