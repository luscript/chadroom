# Delta for Chat

## MODIFIED Purpose

(Purpose text unchanged, except that the inbound list gains "a music control" (CHAT-21, CHAT-30)
and the outbound list gains "the `music` state frame (CHAT-30)".)

## MODIFIED Requirements

### Requirement: CHAT-10 Outbound JSON envelope

(Requirement text unchanged, except the "only other data frames" sentence, which becomes:)

The only other data frames are the `users` (CHAT-17) and `history` (CHAT-18) snapshots, the
`dm` (CHAT-22) and `dm_error` (CHAT-23) frames, the `typing` frame (CHAT-26) and the `music`
frame (CHAT-30), whose shapes are defined there; each entry of `history.messages` is a `chat`
envelope with exactly the fields above.

(Scenarios unchanged.)

### Requirement: CHAT-21 Inbound envelope

(Requirement text unchanged, except a new rule is inserted before rule 3, and the "Fields other
than…" sentence gains `action`, `video` and `pos` for music controls:)

2a. Otherwise, if the frame decodes as a JSON object whose `type` is the string `music`, it is a
   **music control** (CHAT-30). A music control MUST NEVER be treated as a room message: if it is
   invalid, or the room has no shared player, it MUST be discarded without any reply.

(Existing scenarios unchanged. Added:)

#### Scenario: Music control is never chat

- GIVEN clients `A` and `B` are connected to a room **without** a shared player
- WHEN `A` sends `{"type":"music","action":"play"}` and `{"type":"music","action":"bogus"}`
- THEN neither `A` nor `B` receives any frame

## ADDED Requirements

### Requirement: CHAT-30 Shared music player

A room MAY have a **shared player** (only `musica` does; SRV-06). In a room with a shared
player, the server MUST keep the **music state**, owned by the room:

| Field     | Meaning |
|-----------|---------|
| `video`   | the loaded YouTube video id, or none |
| `playing` | whether playback is running |
| position  | milliseconds into the video; while `playing` it advances with server time |
| `by`      | nickname of the client whose control produced the current state |

Initially no video is loaded. A **music control** (CHAT-21) is a JSON object
`{"type":"music","action":A,…}` and is **valid** only when:

| `action` | Other fields | Effect on the state |
|----------|--------------|---------------------|
| `load`   | `video`: a string matching `^[A-Za-z0-9_-]{11}$` | `video` = it, position 0, `playing` = true |
| `play`   | — | `playing` = true from the current position (requires a loaded video) |
| `pause`  | — | `playing` = false at the current position (requires a loaded video) |
| `seek`   | `pos`: an integer, 0 ≤ `pos` ≤ 86 400 000 | position = `pos`, `playing` unchanged (requires a loaded video) |

Any other control — unknown or missing `action`, wrong field types, an invalid `video`, `pos`
out of range or not an integer, or `play`/`pause`/`seek` with no video loaded — MUST be
discarded without any reply. Each connection MAY have at most 4 music controls accepted per
second; the server MUST discard the excess without any reply.

After each accepted control, `by` becomes the sender's nickname and the server MUST send every
registered client of the room (including the sender) a `music` frame, a JSON object with exactly
these fields:

| Field     | JSON type | Value |
|-----------|-----------|-------|
| `type`    | string    | `music` |
| `video`   | string    | the loaded video id |
| `playing` | boolean   | the state's `playing` |
| `pos`     | number    | the position, in integer milliseconds, at the moment the frame is produced |
| `by`      | string    | the state's `by` |
| `ts`      | number    | server time, integer Unix milliseconds |

A client that registers while a video is loaded MUST receive one `music` frame with the current
state right after its `history` frame (CHAT-11 order: `welcome`, `users`, `history`, `music`).
No `music` frame is sent while no video is loaded. `music` frames MUST NOT be kept in history
(CHAT-18), and a client that is too slow to receive one is handled like any other must-have
frame (CHAT-03).

When the last client leaves a room while `playing` is true, the server MUST set `playing` to
false at the current position. In a room without a shared player no `music` frame is ever sent.

#### Scenario: Load is broadcast

- GIVEN clients `A` and `B` are in `musica`
- WHEN `A` sends `{"type":"music","action":"load","video":"dQw4w9WgXcQ"}`
- THEN `A` and `B` receive `type` = `music`, `video` = `dQw4w9WgXcQ`, `playing` = true,
  `by` = `A`, and `pos` between 0 and the elapsed time

#### Scenario: Pause and seek keep the position

- GIVEN a video loaded and playing in `musica`
- WHEN `B` sends `pause`, then `seek` with `pos` = 60000
- THEN every client receives `playing` = false, `by` = `B`, and then `playing` = false,
  `pos` = 60000

#### Scenario: Newcomer gets the state

- GIVEN a video playing in `musica` for about 5 s
- WHEN `C` joins `musica`
- THEN its fourth data frame is `type` = `music` with that `video`, `playing` = true and `pos`
  of about 5000

#### Scenario: Invalid controls are discarded

- GIVEN clients `A` and `B` in `musica`, no video loaded
- WHEN `A` sends `play`, `seek` with `pos` = 5, `load` with `video` = `abc`, `load` with
  `video` = `https://youtu.be/dQw4w9WgXcQ`, `seek` with `pos` = -1 or 1.5, and
  `{"type":"music"}`
- THEN no client receives any frame

#### Scenario: Flood is limited

- GIVEN a video loaded in `musica`
- WHEN `A` sends 20 `play` controls within 100 ms
- THEN clients receive at most 4 `music` frames caused by them

#### Scenario: Empty room pauses

- GIVEN a video playing in `musica` and `A` is the only client
- WHEN `A` leaves and `B` joins 10 s later
- THEN `B` receives `playing` = false with `pos` about the position at the time `A` left

### Requirement: CHAT-31 Chat page shared player

The chat page MUST show the shared player only while the active pane belongs to `musica` (the
room or one of its direct-message conversations). The player MUST let the user paste a YouTube
or YouTube Music link and put it on for everyone, play/pause for everyone, and seek for everyone;
it MUST show the current position, who made the last change (`by`, as text), and offer local
volume/mute (not shared).

The page MUST accept links of the forms `https://www.youtube.com/watch?v=ID`,
`https://youtube.com/watch?v=ID`, `https://m.youtube.com/watch?v=ID`,
`https://music.youtube.com/watch?v=ID`, `https://youtu.be/ID`,
`https://www.youtube.com/shorts/ID`, `https://youtube.com/shorts/ID` and
`https://m.youtube.com/shorts/ID` (extra query parameters allowed), where `ID` matches
`^[A-Za-z0-9_-]{11}$`, and MUST send only the id (`load`). Anything else MUST show an error and
send nothing.

The video MUST play in an embedded `https://www.youtube-nocookie.com/embed/ID` frame with
YouTube's own controls disabled, controlled only through cross-document messages; the page MUST
NOT load any YouTube script. Messages from the frame MUST be accepted only when they come from
the `https://www.youtube-nocookie.com` origin and from that frame. The page MUST NOT create the
frame (nor contact YouTube) until the user presses an explicit **"Escuchar"** control.

On each `music` frame the page MUST follow the state: load the video if it changed, play or
pause, and seek when its local position differs from the expected one by more than 2 seconds
(expected = `pos` plus the time since the frame arrived while `playing`). Local player events
MUST NOT be sent back as controls. Leaving `musica` MUST pause local playback without sending a
control; returning MUST re-sync. If the video cannot be embedded or played, the page MUST show a
short message.

#### Scenario: Everyone hears the same

- GIVEN two browsers in `musica` that pressed "Escuchar"
- WHEN one pastes `https://music.youtube.com/watch?v=dQw4w9WgXcQ&si=x` and presses "Poner"
- THEN both play that video from the start, and both show the first user as the author of the
  last change

#### Scenario: Global pause

- GIVEN both browsers are playing
- WHEN one presses pause
- THEN both pause within a second, at about the same position

#### Scenario: Late joiner syncs

- GIVEN a video has been playing for 1 minute
- WHEN a third browser opens `musica` and presses "Escuchar"
- THEN it starts at about 1:00

#### Scenario: Invalid link

- GIVEN the player
- WHEN the user pastes `https://evil.example/watch?v=dQw4w9WgXcQ` and presses "Poner"
- THEN the page shows an error and sends nothing

#### Scenario: No YouTube before consent

- GIVEN a video is playing in `musica`
- WHEN a user opens `musica` and does not press "Escuchar"
- THEN the page makes no request to any YouTube host
