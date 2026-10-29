# Delta for Chat

## MODIFIED Requirements

### Requirement: CHAT-21 Inbound envelope

(Requirement text unchanged, except the "Fields other than…" sentence, which becomes:)

Fields other than `type`, `to` and `text` (and `action`, `video`, `videos`, `pos`, `id` and
`seq` in a music control) MUST be ignored (`text` is also ignored in a typing signal and a music
control).

(Scenarios unchanged.)

### Requirement: CHAT-30 Shared music player

(Requirement text unchanged, except the following.)

The `queue` row of the music state table becomes:

| Field      | Meaning |
|------------|---------|
| `queue`    | ordered list of at most **50** items `{id, video, by}` waiting after the current track |

The `add` row of the valid-control table becomes, and a new `add_many` row is added after it:

| `action`   | Other fields | Effect on the state |
|------------|--------------|---------------------|
| `add`      | `video`: a string matching `^[A-Za-z0-9_-]{11}$` | if no video is current, or `finished` is true and the queue is empty: the video becomes current. Otherwise it is appended to the queue (requires fewer than 50 queued items) |
| `add_many` | `videos`: a JSON array of 1 to 50 elements, each a string matching `^[A-Za-z0-9_-]{11}$` | processed in order as one control: if no video is current, or `finished` is true and the queue is empty, the first element becomes current; the remaining elements are appended to the queue while it has fewer than 50 items, and the rest are dropped. Requires at least one element to become current or be queued |

For `remove`, and for `add` or `add_many` that only appends, `by` is unchanged. When an
`add_many` makes its first element current, `by` is the sender's nickname. Every queued item
gets its own `id`, in order. An accepted `add_many` produces exactly one `music` frame,
reflecting the whole batch.

Discarded controls also include an `add_many` whose `videos` is missing, not an array, empty,
longer than 50, or contains any element that is not a string matching the video id pattern (the
whole control is discarded; no element is applied).

`add_many` counts as one control for the per-connection limit of 4 music controls per second.

The scenario "Queue cap" is replaced by:

#### Scenario: Queue cap

- GIVEN 50 items are queued
- WHEN `A` sends `add`, or `add_many` with any valid `videos`
- THEN no client receives any frame

New scenarios:

#### Scenario: Bulk add on an idle player

- GIVEN clients `A` and `B` are in `musica` and no video is current
- WHEN `A` sends `{"type":"music","action":"add_many","videos":["dQw4w9WgXcQ","9bZkp7q19f0","kJQP7kiw5Fk"]}`
- THEN `A` and `B` receive exactly one `music` frame with `video` = `dQw4w9WgXcQ`,
  `playing` = true, `by` = `A`, `seq` = 1 and `queue` =
  `[{"id":1,"video":"9bZkp7q19f0","by":"A"},{"id":2,"video":"kJQP7kiw5Fk","by":"A"}]`

#### Scenario: Bulk add while playing fills free slots only

- GIVEN a video is current and 48 items are queued
- WHEN `B` sends `add_many` with 5 valid videos
- THEN every client receives one `music` frame with the same current video and `seq`, and 50
  queued items, the last two being the first two videos sent, added by `B`

#### Scenario: Bulk add after the last track ended

- GIVEN the current track has `finished` = true and the queue is empty, with `seq` = 2
- WHEN `A` sends `add_many` with 2 valid videos
- THEN every client receives the first video as current with `seq` = 3, `playing` = true, and
  the second one queued

#### Scenario: Invalid bulk add is discarded whole

- GIVEN clients `A` and `B` in `musica`, no video current
- WHEN `A` sends `add_many` with `videos` = `[]`, with 51 valid ids, with
  `["dQw4w9WgXcQ","abc"]`, with `["dQw4w9WgXcQ",null]`, with `"dQw4w9WgXcQ"`, or without
  `videos`
- THEN no client receives any frame

### Requirement: CHAT-31 Chat page shared player

(Requirement text unchanged, except the second paragraph, which becomes:)

The page MUST accept links of the forms `https://www.youtube.com/watch?v=ID`,
`https://youtube.com/watch?v=ID`, `https://m.youtube.com/watch?v=ID`,
`https://music.youtube.com/watch?v=ID`, `https://youtu.be/ID`,
`https://www.youtube.com/shorts/ID`, `https://youtube.com/shorts/ID` and
`https://m.youtube.com/shorts/ID` (extra query parameters allowed), where `ID` matches
`^[A-Za-z0-9_-]{11}$`, and MUST send only the id (`add`).

The page MUST also accept **playlist links**: `https://H/playlist?list=LIST` and
`https://H/watch?v=ID&list=LIST`, where `H` is `www.youtube.com`, `youtube.com`,
`m.youtube.com` or `music.youtube.com`, `LIST` matches `^[A-Za-z0-9_-]{10,64}$` (extra query
parameters allowed). For a playlist link the page MUST request `GET /playlist?list=LIST`
(SRV-09) and send the returned ids with one `add_many`:

- For a `watch?v=ID&list=LIST` link, `ID` goes first and is removed from the rest of the list.
  If `LIST` starts with `RD` (an automatic mix), the page MUST NOT request the playlist, MUST
  send `ID` only and show "Las mezclas automáticas de YouTube no se pueden importar. Puse solo
  el video." If the request fails for any reason, the page MUST send `ID` only (as `add` or as a
  one-element `add_many`) and show the matching error text below followed by " Puse solo el
  video."
- The page MUST send at most 50 ids and at most the free queue slots (50 minus the queued
  items in the last `music` frame). When it has to drop ids it MUST show "Se agregaron N de M."
  (N sent, M received, counting `ID` for a `watch?v=ID&list=LIST` link). When nothing fits it MUST show "La cola está llena" and send nothing.
- While the request is pending the page MUST show "Importando playlist…", MUST disable the add
  control, and MUST NOT start another import. If the user leaves `musica` before the answer,
  the page MUST send nothing.
- Errors (send nothing): when `/playlist` answers 404 without a JSON `error` (import disabled),
  "Importar playlists no está disponible."; `not_found`, "No encontré esa playlist (¿es
  privada?)."; an empty `videos`, "Esa playlist no tiene videos reproducibles."; anything else,
  "No se pudo leer la playlist." The page MUST show only these fixed texts, never text from the
  response, and MUST use only returned ids that match the video id pattern.

Anything else MUST show an error and send nothing. When the last `music` frame shows 50 queued
items, the page MUST show "La cola está llena" and send nothing. The skip control MUST be
disabled while the queue is empty.

New scenarios:

#### Scenario: Paste a YouTube Music playlist

- GIVEN two browsers with the `musica` pane open, nothing current, and the server has a
  YouTube key
- WHEN one pastes `https://music.youtube.com/playlist?list=PLlb3q7unEBfNPa0aR-cOQB33HDAwyDLpF`
  and presses "Poner"
- THEN both play the first playable video of the playlist and show up to 49 more in the queue,
  added by the first user

#### Scenario: Song inside a playlist

- GIVEN the player and a server with a YouTube key
- WHEN the user pastes `https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=PL…&index=3`
- THEN `dQw4w9WgXcQ` is sent first, followed by the rest of the playlist without repeating it

#### Scenario: Mix link adds only the song

- GIVEN the player
- WHEN the user pastes `https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=RDdQw4w9WgXcQ`
- THEN the page sends only `dQw4w9WgXcQ`, shows the mix text, and does not request `/playlist`

#### Scenario: Import disabled

- GIVEN the server runs without a YouTube key
- WHEN the user pastes a `/playlist?list=` link and presses "Poner"
- THEN the page shows "Importar playlists no está disponible." and sends nothing

#### Scenario: Partial import

- GIVEN a video is current and 45 items are queued
- WHEN the user imports a playlist that returns 20 videos
- THEN the page sends `add_many` with the first 5 and shows "Se agregaron 5 de 20."
