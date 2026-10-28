# Delta for Chat

## MODIFIED Requirements

### Requirement: CHAT-31 Chat page shared player

The chat page MUST show the shared player controls only while the active pane belongs to
`musica` (the room or one of its direct-message conversations). Shared controls (add,
play/pause, skip, seek, remove) MUST NOT be offered anywhere else. The player MUST let the user paste a YouTube
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
frame (nor contact YouTube) before the user has shown the `musica` pane at least once since
joining it. Showing it while a video is current creates the frame and starts playback without
any further control; after that, while the user stays joined, the frame MAY be created while
another pane is shown when a track becomes current. If the
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

Once created, the frame MUST keep following the room's state (CHAT-34) while the user stays
joined to `musica`, even when the shown pane belongs to another room or conversation: switching
panes MUST NOT pause local playback nor send a control, and the page MUST keep sending `ended`
as above. Leaving `musica` (or the chat) MUST stop local playback and remove the frame without
sending a control. If the video cannot be embedded or played, the page MUST show a short
message.

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

#### Scenario: Switching rooms keeps playing

- GIVEN browsers `X` and `Y` are playing a track in `musica`
- WHEN `X` switches to `general` for 30 s and then back to `musica`
- THEN `X` keeps hearing the track the whole time, in sync with `Y`, and no control is sent

#### Scenario: Leaving musica stops the music

- GIVEN `X` is joined to `general` and `musica` and hears a track while looking at `general`
- WHEN `X` leaves `musica`
- THEN the music stops for `X` only and keeps playing for everyone else

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

## ADDED Requirements

### Requirement: CHAT-34 Chat page global music mute and now playing

While the user is joined to `musica`, the chat header MUST offer, from every room and private
conversation, a toggle (🎵 on / 🔇 muted, with `aria-pressed`) that mutes the shared music for
this browser only. It MUST NOT send any control and MUST NOT affect other users. While muted the
page MUST keep following the room's state (CHAT-31), so unmuting resumes at the room's current
position. The toggle and the in-player local mute (CHAT-31) MUST reflect one single state. The
choice MUST be remembered in the browser (`localStorage`, best effort) and restored on the next
visit. It is independent of the notification sound toggle (CHAT-32). The toggle MUST NOT be
shown while the user is not joined to `musica`.

While the user is joined to `musica`, a track is current and the shown pane does not belong to
`musica`, the chat header MUST show a read-only "now playing" text: `♪ ` followed by the track
title when playing, or `⏸ ` followed by it when paused. When the title is unknown it MUST use
`youtu.be/<id>`. When the browser blocked the audio (CHAT-31) it MUST read
"Audio bloqueado: abrí #musica", and when the user has not shown the `musica` pane since joining
it (so nothing plays, CHAT-31) it MUST read "Abrí #musica para escuchar". It MUST be set as text
(never as markup), MUST offer no control, and MUST be hidden otherwise. On narrow screens
(≤ 480 px wide) the page MAY hide this text; the toggle stays.

#### Scenario: Mute from another room

- GIVEN Ana is joined to `general` and `musica`, looking at `general`, and hears a track
- WHEN she presses 🎵
- THEN she stops hearing it, the button shows 🔇 with `aria-pressed="true"`, nobody else is
  affected and no frame is sent

#### Scenario: Unmute resumes in sync

- GIVEN Ana muted the music 1 minute ago while the track kept playing for the room
- WHEN she presses 🔇
- THEN she hears the track at about the room's current position

#### Scenario: Mute is remembered

- GIVEN Ana muted the music and reloads the page
- WHEN she joins `musica` and a track plays
- THEN the music is muted and the toggle shows 🔇

#### Scenario: Now playing outside musica

- GIVEN a track titled "Song" is playing and Ana is joined to `musica`, looking at `juegos`
- THEN the header shows `♪ Song`; when someone pauses it in `musica` it shows `⏸ Song`

#### Scenario: Hidden in musica and when not joined

- GIVEN Ana looks at the `musica` pane, or is not joined to `musica`
- THEN the header shows no now-playing text; and when not joined it shows no music toggle

#### Scenario: Shared controls stay in musica

- GIVEN Ana looks at `general` while a track plays
- THEN the page offers no way to pause, seek, skip, add or remove tracks outside `musica`
