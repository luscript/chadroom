# Delta for Chat

Base: `openspec/specs/chat/spec.md` plus the not-yet-archived `message-replies` delta
(CHAT-35/36). New requirements start at CHAT-37.

## MODIFIED Requirements

### Requirement: CHAT-10 Outbound JSON envelope

(Requirement text unchanged, except the field table, which gains one optional row after
`avatar`, and the sentences below it.)

| Field | JSON type | Value |
|-------|-----------|-------|
| `bio` | string    | **optional**: present only on `join` and `welcome`, and only when the client the event is about has a bio (CHAT-37); its value is that bio |

`bio` MUST NOT appear on `chat` (including `history` entries), `leave`, `dm`, `dm_error` or
`typing` frames, and MUST NOT appear with an empty value. Field order is `type`, `from`,
`text`, `ts`, `avatar`, `bio`, `id`, `reply` (absent fields skipped). The `profile` frame
(CHAT-38) is added to the list of other data frames.

`bio`, like `avatar`, MUST always be the value the server holds for the originating
connection and MUST NOT be taken from a `chat`, `dm` or `typing` payload.

### Requirement: CHAT-17 Online users list

(Requirement text unchanged, except the field table, which gains one optional row after
`avatars`:)

| Field  | JSON type | Value |
|--------|-----------|-------|
| `bios` | object    | **optional**: maps the nickname of each registered client that has a bio (CHAT-37) to it; omitted when no registered client has one |

Every key of `bios` MUST appear in `users`. The snapshot MUST reflect the current profiles,
including changes made with `profile` frames (CHAT-38).

### Requirement: CHAT-21 Inbound envelope

(Requirement text unchanged, except a new step after 2a and the "Fields other than…"
sentence.)

2b. Otherwise, if the frame decodes as a JSON object whose `type` is the string `profile`, it
   is a **profile update** (CHAT-38). It is valid only if `avatar` and `bio` are each absent
   or a JSON string. A profile update MUST NEVER be treated as a room message: if it is
   invalid it MUST be discarded without any reply.

Fields other than `type`, `to` and `text` (and `action`, `video`, `videos`, `pos`, `id` and
`seq` in a music control, `reply_to` in a room or direct message, and `avatar` and `bio` in a
profile update) MUST be ignored.

#### Scenario: Profile frame is not chat

- GIVEN clients `A` and `B` are connected
- WHEN `A` sends `{"type":"profile","bio":5}`
- THEN no client receives any frame

### Requirement: CHAT-28 Profile picture

(Requirement text unchanged, except the paragraph "The profile picture belongs to the
connection…", which becomes:)

The profile picture belongs to the connection. It MUST be set at registration from the
`avatar` query parameter and MUST change only through a valid profile update (CHAT-38); no
other inbound frame may change it. The server MUST include the current picture as `avatar` in
that client's `welcome`, `join`, `chat` and `profile` envelopes and in `users` snapshots
(CHAT-17). A `chat` envelope kept in history (CHAT-18) MUST keep the `avatar` it was relayed
with, even if the picture changes later.

#### Scenario: History keeps the old avatar after a change

- GIVEN client `A` with avatar `U` sends `hola`
- AND then `A` sends `{"type":"profile","avatar":"V"}` with `V` an allowed URL
- WHEN client `C` connects
- THEN the `history` entry for `hola` has `avatar` = `U`
- AND `C`'s `users` frame maps `A` to `V`

### Requirement: CHAT-29 Chat page profile pictures

(Second and third paragraphs unchanged. The first paragraph becomes:)

The join form MUST NOT offer a profile-picture field. The page MUST keep its own profile
(picture and bio, CHAT-39) only in page memory and MUST send its picture as `avatar` on every
room connection and reconnection. The page MUST NOT store the profile in browser storage, and
on start it MUST delete any value left by earlier versions under the key `chadroom.avatar`.

The page MUST also learn pictures from `profile` frames (CHAT-38). Messages already shown keep
the picture they were rendered with.

(Scenario "Invalid field blocks joining" is removed.)

## ADDED Requirements

### Requirement: CHAT-37 Profile bio

A client MAY send a bio with the `bio` query parameter of `GET /ws`. The server MUST clean it
like a nickname but with its own limit: replace invalid UTF-8 with U+FFFD, remove control and
bidi-control characters (including newlines and tabs), trim surrounding whitespace and keep at
most the first **160** runes. An absent or (after cleaning) empty value means no bio; it MUST
NOT cause the connection to be rejected.

The bio belongs to the connection and changes only through a valid profile update (CHAT-38).
The server MUST include it as `bio` in that client's `welcome`, `join` and `profile`
envelopes and in `users` snapshots (`bios`), and MUST NOT store it anywhere else nor keep it
after the connection ends.

#### Scenario: Bio is announced on join

- GIVEN client `A` is connected
- WHEN client `B` connects with `?nickname=B&bio=hola%20mundo`
- THEN `B`'s `welcome` has `bio` = `hola mundo`
- AND `A` receives `type` = `join`, `from` = `B`, `bio` = `hola mundo`

#### Scenario: Bio is cleaned and truncated

- GIVEN the server is running
- WHEN a client connects with a `bio` of `"  a"` + U+202E + `"b\nc"` followed by 200 `x` (interior spaces are kept; only the ends are trimmed)
- THEN its `welcome` `bio` is `abc` followed by 157 `x` (160 runes)

#### Scenario: Blank bio is omitted

- GIVEN the server is running
- WHEN a client connects with `bio` = `"   "`
- THEN its `welcome` and the `join` others receive have no `bio` field

#### Scenario: Reused nickname has no stale bio

- GIVEN `A` connected with bio `hola` and disconnected
- WHEN a new client connects as `A` with no bio
- THEN its `welcome`, `join` and the `users` frame others get have no bio for `A`

### Requirement: CHAT-38 Profile update

A valid profile update (CHAT-21 step 2b) from a registered client MUST replace that client's
whole profile:

- `avatar`: absent or empty clears the picture; otherwise it MUST be an allowed avatar URL
  (CHAT-28). If it is not, the whole update MUST be discarded without any reply and the
  profile MUST NOT change.
- `bio`: absent or empty after cleaning clears the bio; otherwise it is cleaned per CHAT-37.

After applying it, the server MUST send to every client of the room, the sender included, one
frame:

| Field    | JSON type | Value |
|----------|-----------|-------|
| `type`   | string    | `profile` |
| `from`   | string    | the sender's assigned nickname |
| `ts`     | number    | server time, integer Unix milliseconds |
| `avatar` | string    | **optional**: the new picture; omitted when there is none |
| `bio`    | string    | **optional**: the new bio; omitted when there is none |

Field order is `type`, `from`, `ts`, `avatar`, `bio`. The update MUST NOT be stored in history
and MUST NOT change any `chat` already relayed. It affects only the room of the connection
that sent it.

The server MUST accept at most one profile update per connection per second; extra updates
MUST be discarded without any reply.

#### Scenario: Update is broadcast

- GIVEN clients `A` and `B` are connected, `A` without a profile
- WHEN `A` sends `{"type":"profile","avatar":"https://i.imgur.com/a.png","bio":"hola"}`
- THEN `A` and `B` receive exactly
  `{"type":"profile","from":"A","ts":…,"avatar":"https://i.imgur.com/a.png","bio":"hola"}`

#### Scenario: Clearing the profile

- GIVEN `A` has avatar `U` and bio `hola`
- WHEN `A` sends `{"type":"profile"}`
- THEN every client receives `{"type":"profile","from":"A","ts":…}`
- AND a client connecting afterwards gets no `A` key in `avatars` or `bios`

#### Scenario: Invalid avatar discards the update

- GIVEN `A` has bio `hola`
- WHEN `A` sends `{"type":"profile","avatar":"https://evil.example/a.png","bio":"chau"}`
- THEN no client receives any frame
- AND `A`'s bio is still `hola`

#### Scenario: Updates are rate limited

- GIVEN `A` and `B` are connected
- WHEN `A` sends two valid profile updates back to back
- THEN `B` receives exactly one `profile` frame

#### Scenario: Forged sender is ignored

- GIVEN `A` and `B` are connected
- WHEN `A` sends `{"type":"profile","from":"B","bio":"x"}`
- THEN the `profile` frame everyone receives has `from` = `A`

### Requirement: CHAT-39 Chat page profile editor

The page MUST offer a "Mi perfil" control reachable on every screen size while in the chat.
It MUST open a profile editor showing the current picture (or the initial-letter avatar), a
button to choose a picture, a button to remove it, a bio field limited to 160 characters with
a visible counter, and a save button.

Choosing a picture MUST open the GIF picker (SRV-08) in a picture mode where selecting a GIF
sets it as the pending picture instead of sending a message; in that mode the page MUST NOT
offer pasting a URL. Saving MUST send `{"type":"profile","avatar":…,"bio":…}` on every room
the page has joined and keep the values as its profile for later connections and
reconnections (sent as `avatar` and `bio` query parameters). The page MUST update what it
shows from the `profile` frames the server echoes.

The profile MUST live only in page memory: leaving the chat MUST clear it, and closing or
reloading the tab loses it. The "MI PERFIL" card MUST show the own bio when set.

#### Scenario: Pick a GIF as picture

- GIVEN the page is in the chat and the editor is open
- WHEN the user clicks "Elegir GIF", picks a GIF and clicks "Guardar"
- THEN every joined room socket receives a `profile` frame with that GIF's URL as `avatar`
- AND no `chat` message is sent

#### Scenario: Profile survives a reconnect

- GIVEN the user saved bio `hola`
- WHEN the connection drops and the page reconnects
- THEN the new `GET /ws` request has `bio=hola`

#### Scenario: Leaving clears the profile

- GIVEN the user saved a picture and bio
- WHEN the user leaves the chat and joins again
- THEN the new connection has no `avatar` or `bio` parameter and the editor is empty

### Requirement: CHAT-40 Chat page user popup

Clicking (or activating with the keyboard) a user's picture or name next to a room message, a
direct message, or a row of the online list MUST open a popup for that user showing their
picture (or initial-letter avatar), nickname, bio (or the text "Sin descripción") and, when
the user is not the page's own nickname, a "Mensaje privado" button that opens the direct
conversation with them (CHAT-25) and closes the popup. For the own nickname the popup MUST show
an "Editar perfil" button that opens the editor instead.

The popup MUST show the user's current profile from the room's online list and MUST update if
a `profile` frame for that user arrives while it is open. It MUST NOT open for a nickname that
is not in the current room's online list. It MUST close on Escape, on a click outside it, and
when the user leaves the room. All user data MUST be rendered as text; the picture is loaded
under the CHAT-29 rules.

#### Scenario: Popup from a message

- GIVEN `Ana` (bio `hola`) is online and sent a message
- WHEN the user clicks `Ana`'s name on that message
- THEN a popup shows `Ana`, her picture and `hola`, and a "Mensaje privado" button

#### Scenario: Start a DM from the popup

- GIVEN the popup for `Ana` is open
- WHEN the user clicks "Mensaje privado"
- THEN the direct conversation with `Ana` is active and the popup is closed

#### Scenario: Bio markup is not interpreted

- GIVEN `Ana`'s bio is `<img src=x onerror=alert(1)>`
- WHEN her popup opens
- THEN the bio is shown literally and no image request is made

#### Scenario: User who left

- GIVEN `Ana` sent a message and then left
- WHEN the user clicks her name on that message
- THEN no popup opens
