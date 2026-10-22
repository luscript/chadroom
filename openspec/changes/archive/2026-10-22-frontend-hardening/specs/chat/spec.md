# Delta for Chat

## ADDED Requirements

### Requirement: CHAT-15 Inline images only from allowlisted hosts

The chat page MUST render a `chat` message as an inline image only when its whole `text` is
an image URL whose scheme is `https:`, whose host is exactly one of the hosts on the image
allowlist, and whose path ends in `.gif`, `.webp`, `.png`, `.jpg` or `.jpeg`
(case-insensitive; a query string is allowed). Every other message — including image URLs
on any other host, `http:` URLs and look-alike hosts (`media.giphy.com.evil.example`,
`evilgiphy.com`) — MUST be rendered as plain text, and the page MUST NOT request it.

The initial allowlist is `media.giphy.com`, `media0.giphy.com` … `media4.giphy.com`,
`i.giphy.com`, `media.tenor.com` and `i.imgur.com`. The page and the CSP `img-src`
directive (SRV-05) MUST use the same list.

The GIF panel MUST enable its send button only for URLs that pass the same check.

#### Scenario: Allowlisted GIF renders inline

- GIVEN the page is connected
- WHEN it receives a `chat` envelope with `text` = `https://media.giphy.com/media/abc/giphy.gif`
- THEN it renders an image loaded from that URL

#### Scenario: Unknown host renders as text

- GIVEN the page is connected
- WHEN it receives a `chat` envelope with `text` = `https://evil.example/x.gif`
- THEN it renders the URL as plain text
- AND the browser makes no request to `evil.example`

#### Scenario: Look-alike host

- GIVEN the page is connected
- WHEN it receives `chat` envelopes with `text` = `https://media.giphy.com.evil.example/x.gif`
  and `https://evilgiphy.com/x.gif`
- THEN both render as plain text

#### Scenario: URL with surrounding text

- GIVEN the page is connected
- WHEN it receives a `chat` envelope with `text` = `mirá https://media.giphy.com/media/abc/giphy.gif`
- THEN it renders the whole text as plain text

#### Scenario: Not-allowlisted URL in the GIF panel

- GIVEN the GIF panel is open
- WHEN the user types `https://evil.example/x.gif`
- THEN the panel's send button stays disabled

### Requirement: CHAT-16 Page reconnects automatically

The page MUST consider itself connected (`online`) only after it receives `welcome`
(CHAT-11), not when the socket opens. When its WebSocket closes without the user having left,
the page MUST show the `reconnecting` state and retry automatically, requesting the nickname
the user originally typed. Retry delays MUST grow exponentially with random jitter, MUST NOT
exceed 30 seconds, and MUST go back to the initial delay after a `welcome` is received. The
page MUST keep retrying for as long as the user stays in the chat view; it has no terminal
`offline` state. It MAY retry sooner when the browser reports the network is back or the tab
becomes visible (cutting a pending wait short or replacing a retry socket still connecting),
but MUST NOT have more than one socket open at a time. The page MUST show at most
one system notice when the connection is lost and one when it is re-established, per outage.
While not `online` the send button and the GIF panel's send button MUST stay disabled. After the user clicks "Salir" the page
MUST NOT reconnect, and any pending retry MUST be cancelled. Messages already on screen MUST
be kept across reconnects.

On reconnection the page's identity MUST come from the new `welcome` (CHAT-14); it MAY differ
from the previous one (e.g. `Lucía (2)` while the server still holds the old connection).

#### Scenario: Socket opens but is closed before welcome

- GIVEN a page in the `reconnecting` state
- WHEN a retry's socket opens and the server closes it before sending `welcome`
- THEN the page stays `reconnecting`, shows no "reconnected" notice
- AND the next retry waits longer than the previous one

#### Scenario: Server restart

- GIVEN two pages are connected as `A` and `B`
- WHEN the server is stopped and started again
- THEN both pages show the reconnecting state and one "connection lost" notice
- AND within 30 seconds of the server being back both are connected again without a reload
- AND each shows one "reconnected" notice
- AND `A` can send a message that `B` receives

#### Scenario: Backoff while the server is down

- GIVEN a connected page
- WHEN the server stays down for 2 minutes
- THEN the page keeps retrying with growing delays, never waiting more than 30 seconds
  between attempts
- AND it shows no additional system notices while retrying

#### Scenario: Leaving cancels reconnection

- GIVEN a page in the reconnecting state
- WHEN the user clicks "Salir"
- THEN no further connection attempts are made

#### Scenario: Leaving while connected

- GIVEN a connected page
- WHEN the user clicks "Salir"
- THEN the connection closes and the page does not reconnect

#### Scenario: Renamed after reconnecting

- GIVEN a page that joined as `Lucía` lost its connection
- WHEN it reconnects and receives `welcome` with `from` = `Lucía (2)`
- THEN the header shows `Lucía (2)`
- AND only `chat` envelopes from `Lucía (2)` are marked as its own
