# Delta for Chat

## MODIFIED Requirements

### Requirement: CHAT-21 Inbound envelope

(Requirement text unchanged, except the "Fields other than…" sentence, which becomes:)

Fields other than `type`, `to` and `text` (and `action`, `video`, `pos`, `id` and `seq` in a
music control) MUST be ignored (`text` is also ignored in a typing signal and a music control).

(Scenarios unchanged.)

### Requirement: CHAT-30 Shared music player

(Replaces the whole requirement.)

A room MAY have a **shared player** (only `musica` does; SRV-06). In a room with a shared
player, the server MUST keep the **music state**, owned by the room:

| Field      | Meaning |
|------------|---------|
| `video`    | the current YouTube video id, or none |
| `playing`  | whether playback is running |
| position   | milliseconds into the video; while `playing` it advances with server time |
| `by`       | nickname shown as responsible for the current state (see below) |
| `seq`      | integer that increases by 1 every time a track becomes current; 0 while none has |
| `queue`    | ordered list of at most **30** items `{id, video, by}` waiting after the current track |
| `finished` | whether the current track ended with nothing queued after it |

Each queue item gets an `id`: a positive integer assigned by the server, unique within the room
for the life of the process. Initially no video is current, the queue is empty and `seq` is 0.

A track **becomes current** when its video is set as `video` with position 0, `playing` = true,
`finished` = false, `seq` incremented, and `by` = the nickname that added it.

A **music control** (CHAT-21) is a JSON object `{"type":"music","action":A,…}` and is **valid**
only when:

| `action` | Other fields | Effect on the state |
|----------|--------------|---------------------|
| `add`    | `video`: a string matching `^[A-Za-z0-9_-]{11}$` | if no video is current, or `finished` is true and the queue is empty: the video becomes current. Otherwise it is appended to the queue (requires fewer than 30 queued items) |
| `remove` | `id`: an integer ≥ 1 | the queue item with that `id` is removed (requires it to exist) |
| `skip`   | — | the first queue item is removed and becomes current (requires a non-empty queue) |
| `ended`  | `seq`: an integer ≥ 1 | requires `seq` equal to the state's `seq` and `finished` false. If the queue is non-empty, as `skip`. Otherwise `finished` = true and `playing` = false at the current position |
| `play`   | — | `playing` = true from the current position (requires a current video) |
| `pause`  | — | `playing` = false at the current position (requires a current video) |
| `seek`   | `pos`: an integer, 0 ≤ `pos` ≤ 86 400 000 | position = `pos`, `playing` unchanged (requires a current video) |

`play` and `seek` on a `finished` track set `finished` to false (the track is replayed). For
`play`, `pause` and `seek`, `by` becomes the sender's nickname. For `remove`, and for `add` that
only appends, `by` is unchanged. Every accepted control produces a `music` frame.

Any other control MUST be discarded without any reply. This includes an unknown or missing
`action` (including the former `load`), wrong field types, an invalid `video`, `pos`, `id` or
`seq` out of range or not an integer, an unmet requirement above, or an `ended` whose `seq` is
not the current one. Several clients reporting the same track as ended therefore advance the
queue only once.

Per connection, the server MUST accept at most 4 `ended` controls per second and, separately,
at most 4 other music controls per second. It MUST discard the excess without any reply.

After each accepted control the server MUST send every registered client of the room (including
the sender) a `music` frame, a JSON object with exactly these fields:

| Field     | JSON type | Value |
|-----------|-----------|-------|
| `type`    | string    | `music` |
| `video`   | string    | the current video id |
| `playing` | boolean   | the state's `playing` |
| `pos`     | number    | the position, in integer milliseconds, at the moment the frame is produced |
| `by`      | string    | the state's `by` |
| `seq`     | number    | the state's `seq` |
| `queue`   | array     | the queue in order, each element an object with exactly `id` (number), `video` (string) and `by` (string, who added it); `[]` when empty |
| `ts`      | number    | server time, integer Unix milliseconds |

A client that registers while a video is current MUST receive one `music` frame with the current
state right after its `history` frame (CHAT-11 order: `welcome`, `users`, `history`, `music`).
No `music` frame is sent while no video is current. `music` frames MUST NOT be kept in history
(CHAT-18), and a client that is too slow to receive one is handled like any other must-have
frame (CHAT-03).

When the last client leaves a room while `playing` is true, the server MUST set `playing` to
false at the current position. The queue is kept. In a room without a shared player no `music`
frame is ever sent.

#### Scenario: First add plays

- GIVEN clients `A` and `B` are in `musica` and no video is current
- WHEN `A` sends `{"type":"music","action":"add","video":"dQw4w9WgXcQ"}`
- THEN `A` and `B` receive `type` = `music`, `video` = `dQw4w9WgXcQ`, `playing` = true,
  `by` = `A`, `seq` = 1, `queue` = `[]`, and `pos` between 0 and the elapsed time

#### Scenario: Add while playing queues

- GIVEN `dQw4w9WgXcQ` is current and playing in `musica`
- WHEN `B` sends `add` with `video` = `9bZkp7q19f0`
- THEN every client receives `video` = `dQw4w9WgXcQ`, `playing` = true, `by` unchanged, the same
  `seq`, and `queue` = `[{"id":1,"video":"9bZkp7q19f0","by":"B"}]`

#### Scenario: Skip advances

- GIVEN a current track with `seq` = 1 and queue `[{id 1, 9bZkp7q19f0, by B}]`
- WHEN `A` sends `skip`
- THEN every client receives `video` = `9bZkp7q19f0`, `playing` = true, `pos` about 0,
  `by` = `B`, `seq` = 2, `queue` = `[]`

#### Scenario: Ended advances once

- GIVEN a current track with `seq` = 3 and one queued item
- WHEN `A`, `B` and `C` each send `{"type":"music","action":"ended","seq":3}`
- THEN exactly one `music` frame is sent, with the queued item as current and `seq` = 4
- AND the other two controls are discarded

#### Scenario: Last track ends

- GIVEN a current track with `seq` = 2 and an empty queue
- WHEN `A` sends `ended` with `seq` = 2
- THEN every client receives the same `video`, `playing` = false, `seq` = 2, `queue` = `[]`
- AND a later `add` from `B` with `9bZkp7q19f0` makes it current with `seq` = 3

#### Scenario: Remove by id

- GIVEN queue `[{id 4, …}, {id 5, …}]`
- WHEN `A` sends `remove` with `id` = 4, then `remove` with `id` = 4 again
- THEN one `music` frame is sent, with `queue` = `[{id 5, …}]`, and the second control is
  discarded

#### Scenario: Queue cap

- GIVEN 30 items are queued
- WHEN `A` sends `add`
- THEN no client receives any frame

#### Scenario: Pause and seek keep the position

- GIVEN a video current and playing in `musica`
- WHEN `B` sends `pause`, then `seek` with `pos` = 60000
- THEN every client receives `playing` = false, `by` = `B`, and then `playing` = false,
  `pos` = 60000

#### Scenario: Newcomer gets the state

- GIVEN a video playing in `musica` for about 5 s with two queued items
- WHEN `C` joins `musica`
- THEN its fourth data frame is `type` = `music` with that `video`, `playing` = true, `pos`
  of about 5000, and both queued items

#### Scenario: Invalid controls are discarded

- GIVEN clients `A` and `B` in `musica`, no video current
- WHEN `A` sends `play`, `seek` with `pos` = 5, `skip`, `ended` with `seq` = 1, `remove` with
  `id` = 1, `load` with a valid `video`, `add` with `video` = `abc`, `add` with
  `video` = `https://youtu.be/dQw4w9WgXcQ`, `seek` with `pos` = -1 or 1.5, `remove` with
  `id` = 0 or `"1"`, and `{"type":"music"}`
- THEN no client receives any frame

#### Scenario: Flood is limited

- GIVEN a video current in `musica`
- WHEN `A` sends 20 `play` controls within 100 ms
- THEN clients receive at most 4 `music` frames caused by them

#### Scenario: Ended is not throttled by user controls

- GIVEN a current track with `seq` = 1, one queued item, and `A` is the only client
- WHEN `A` sends `pause`, `play` and immediately `ended` with `seq` = 1
- THEN the `ended` is accepted and the queued item becomes current

#### Scenario: Empty room pauses

- GIVEN a video playing in `musica` with one queued item, and `A` is the only client
- WHEN `A` leaves and `B` joins 10 s later
- THEN `B` receives `playing` = false with `pos` about the position at the time `A` left, and
  the queued item

### Requirement: CHAT-31 Chat page shared player

(Replaces the whole requirement.)

The chat page MUST show the shared player only while the active pane belongs to `musica` (the
room or one of its direct-message conversations). The player MUST let the user paste a YouTube
or YouTube Music link and add it for everyone, play/pause for everyone, skip for everyone, and
seek for everyone. It MUST show the current position, who is responsible for the current state
(`by`, as text), and the queue in order. Each queued item shows its video id and who added it
(as text), plus a control that removes it for everyone. The player MUST offer local
volume/mute (not shared).

The page MUST accept links of the forms `https://www.youtube.com/watch?v=ID`,
`https://youtube.com/watch?v=ID`, `https://m.youtube.com/watch?v=ID`,
`https://music.youtube.com/watch?v=ID`, `https://youtu.be/ID`,
`https://www.youtube.com/shorts/ID`, `https://youtube.com/shorts/ID` and
`https://m.youtube.com/shorts/ID` (extra query parameters allowed), where `ID` matches
`^[A-Za-z0-9_-]{11}$`, and MUST send only the id (`add`). Anything else MUST show an error and
send nothing. When the last `music` frame shows 30 queued items, the page MUST show
"La cola está llena" and send nothing. The skip control MUST be disabled while the queue is
empty.

The video MUST play in an embedded `https://www.youtube-nocookie.com/embed/ID` frame with
YouTube's own controls disabled, controlled only through cross-document messages; the page MUST
NOT load any YouTube script. Messages from the frame MUST be accepted only when they come from
the `https://www.youtube-nocookie.com` origin and from that frame. The page MUST NOT create the
frame (nor contact YouTube) before the `musica` pane is shown for the first time while a video
is current. Showing it creates the frame and starts playback without any further control. If the
browser blocks playback, the page MUST show the text
"Tu navegador bloqueó el audio: apretá Escuchar." with an **"Escuchar"** control that retries
playback.

On each `music` frame the page MUST follow the state. It loads the video if `seq` changed,
plays or pauses, and seeks when its local position differs from the expected one by more than
2 seconds (expected = `pos` plus the time since the frame arrived while `playing`). When the
embedded player reports that the loaded track ended, and that track's `seq` is the state's
`seq`, the page MUST send `ended` with that `seq`, once per `seq`. It MUST NOT restart a track
that ended locally while it waits for the next frame. No other local player event is sent as a
control.

Leaving `musica` MUST pause local playback without sending a control. Returning MUST re-sync to
the room's current state before resuming: the current track, `playing`, and the expected
position, regardless of where local playback was paused. If the video cannot be embedded or
played, the page MUST show a short message.

#### Scenario: Everyone hears the same

- GIVEN two browsers with the `musica` pane open and nothing current
- WHEN one pastes `https://music.youtube.com/watch?v=dQw4w9WgXcQ&si=x` and presses "Poner"
- THEN both play that video from the start without pressing anything else, and both show the
  first user as responsible

#### Scenario: Paste while playing queues

- GIVEN both browsers are playing a track
- WHEN one pastes another valid link and presses "Poner"
- THEN the track keeps playing in both, and both show the new item in the queue with its author

#### Scenario: Auto-advance

- GIVEN both browsers are playing the last seconds of a track with one queued item
- WHEN the track ends
- THEN both start the queued item within a few seconds, and the queue is empty in both

#### Scenario: Skip and remove

- GIVEN two queued items
- WHEN one browser removes the second and then presses skip
- THEN both play the first item and show an empty queue with the skip control disabled

#### Scenario: Global pause

- GIVEN both browsers are playing
- WHEN one presses pause
- THEN both pause within a second, at about the same position

#### Scenario: Late joiner syncs

- GIVEN a video has been playing for 1 minute
- WHEN a third browser opens `musica`
- THEN it starts at about 1:00 without pressing "Escuchar" (or shows the blocked warning)

#### Scenario: Returning re-syncs

- GIVEN browsers `X` and `Y` are playing a track in `musica`
- WHEN `X` switches to `general` for 30 s and then back to `musica`
- THEN `X` plays at about the same position as `Y` (about 30 s later than where `X` left), not
  from where `X` paused

#### Scenario: Blocked autoplay

- GIVEN a browser that refuses to start audio without a click on the player
- WHEN the user opens `musica` while a video is playing
- THEN the page shows "Tu navegador bloqueó el audio: apretá Escuchar." and the "Escuchar"
  control, and pressing it starts playback in sync

#### Scenario: Invalid link

- GIVEN the player
- WHEN the user pastes `https://evil.example/watch?v=dQw4w9WgXcQ` and presses "Poner"
- THEN the page shows an error and sends nothing

#### Scenario: No YouTube outside musica

- GIVEN a video is playing in `musica`
- WHEN a user joins and stays in `general` without opening `musica`
- THEN the page makes no request to any YouTube host
