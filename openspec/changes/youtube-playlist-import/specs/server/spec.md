# Delta for Server

## ADDED Requirements

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

## MODIFIED Requirements

### Requirement: SRV-05 HTTP hardening

(Requirement text and Content-Security-Policy unchanged: `/playlist` is same-origin and is
covered by `connect-src 'self'`; the browser never contacts the YouTube Data API.)
