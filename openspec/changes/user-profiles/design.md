# Design: User profiles (picture + bio, editor and user popup)

## Technical Approach

A profile is two strings that belong to one connection: `avatar` (already exists) and `bio`
(new). Nothing is persisted anywhere.

- **Server (`internal/chat`)**: `Client` gets a `bio` field next to `avatar`. Both are set before
  register (from `?avatar=` / `?bio=`) and from then on are written **only by `Room.Run`**, through
  a new `inboundProfile` kind and a `Room.profile` handler. `parseInbound` gets a `profile`
  case that checks the envelope's shape and cleans the bio. `readPump` checks the avatar with
  the injected validator and then applies a 1 s profile `rateGate`, so `Run` only ever sees
  updates that are valid and within the rate limit. The validator moves from `Server` to `Room`,
  where `NewServer` sets it, so the same function checks avatars at connect time and in
  `readPump`. `welcome`/`join` gain `bio`, `users` gains `bios`, and a new `profile` frame is
  published to the whole room.
- **Page (`cmd/chatroom`)**: `myProfile = {avatar, bio}` replaces `requestedAvatar`. The join-form
  picture field and its `localStorage` key are removed. On start, the page deletes any old
  value stored under that key. Each room gets `bios: Map` next to `avatars`, and one helper,
  `setMemberProfile`, feeds both from `welcome`, `join` and `profile`. The profile editor is
  one more `.popover` in the composer bar. The GIF picker (also in the composer bar) gets a
  *pick mode*, and the two popovers swap places while a picture is being chosen. Avatars and
  names in messages, and rows in the online list, become `<button class="user-trigger">`
  elements that open one page-level user popup (`#user-popup`).

This follows the proposal and the spec delta (CHAT-10/17/21/28/29 amended, CHAT-37…40 added).
The one change is that avatar validation for profile frames runs in `readPump` rather than in
`Run`; see the decision below. The proposal had the validator "injected into each Room" without
saying where it runs.

## Architecture Decisions

### Decision: Profile is mutated only in `Room.Run` (hub pattern, no locks)

**Choice**: A new `inboundProfile` kind travels on `Room.broadcast` like every other inbound
frame. `dispatch` checks membership and then calls `cr.profile(in)`, which assigns
`in.from.avatar` and `in.from.bio` and publishes the frame.
**Alternatives considered**: (a) A `sync.Mutex` on `Client` and mutation from `readPump`. That
breaks the CLAUDE.md rule ("no agregar locks") and the "only Run touches clients" invariant.
(b) An atomic pointer to a profile struct. That is a second concurrency mechanism for two
strings.
**Rationale**: `c.avatar` is already read only inside `Run` (`admit`, `relay`, `avatars()`).
`c.bio` is used in exactly the same places. Writes in `Run` are therefore ordered with every
read, so the race detector has nothing to flag. Because profile frames share the broadcast
channel, one client's profile change and its next chat stay in order: a chat sent after a
change carries the new avatar.

### Decision: Validator lives on `Room` (set by `NewServer`), and profile avatars are checked in `readPump`

**Choice**:
- `Room` gains `validAvatar func(string) bool`. It is immutable once the room is served.
  `NewServer` sets it on each room it receives, and the `Server.validAvatar` field is removed.
- A single helper is used for both entry points:
  ```go
  // allowedAvatar reports whether s is a non-empty avatar this room may show (CHAT-28).
  // Read-only: safe from any goroutine once the room is served.
  func (cr *Room) allowedAvatar(s string) bool {
      return s != "" && cr.validAvatar != nil && cr.validAvatar(s)
  }
  ```
- `HandleWebSocket` uses `if !room.allowedAvatar(avatar) { avatar = "" }`, and `readPump` drops a
  profile frame whose non-empty `avatar` fails `cr.allowedAvatar`.

**Alternatives considered**: (a) Validate in `Room.profile` (inside `Run`). Hub unit tests
could then cover the drop path without a socket. But that runs `main`'s URL parser on the room's
only goroutine, and `Run` would read a field that `NewServer` writes. Tests that start
`Run` before `NewServer` would then race. (b) Keep the validator on `Server` and pass it to
`serveConn` and on to every `Client`. That adds a per-client copy of a server-wide constant. (c)
Pass the validator into `parseInbound`. That makes a pure parser depend on configuration and
changes every `TestParseInbound` row.
**Rationale**: Option (a)'s race can't happen with this choice. The field is written once,
inside `NewServer`, and is read only by `HandleWebSocket` and `readPump`. Both of those start
after `NewServer` returns: the HTTP server is started afterwards in `main` and in
`startAvatarRooms`. `Run` never reads it. Validation then sits in `readPump` with the other
pre-hub filters (UTF-8, typing and music gates), and `Run` stays cheap. Connect time and
profile updates call the same function, so "same rule as at connect" (CHAT-38) holds by
construction. A `nil` validator still means "every non-empty avatar is dropped", matching the
existing test convention (`startRooms` passes nil). `NewServer`'s doc comment gains: "sets each
room's avatar validator; a room must belong to one Server and must not be running yet."

### Decision: Profile envelope: both keys optional, absent means empty, present non-strings drop the frame

**Choice**: In `{"type":"profile", …}`, each of `avatar` and `bio` may be **absent** (taken as
`""`) or a **JSON string**. Anything else, including `null`, numbers, arrays and objects, makes
the whole frame invalid. An invalid profile frame is discarded silently and never falls back to
chat (CHAT-21 step 2b, the same treatment as music in 2a). Other keys (`from`, `text`, `ts`, …)
are ignored.
**Alternatives considered**: (a) Require both keys. That is stricter, but it adds a failure mode
(a client omits `bio` and its whole update vanishes) with no security benefit. It also contradicts
the spec scenario where `{"type":"profile"}` clears the profile. (b) Treat a present non-string
as empty. A malformed client would then silently wipe its own profile.
**Rationale**: An update replaces the whole profile, and the outbound `profile` frame omits empty
fields. So "absent = none" is the protocol's natural representation of an empty field in both
directions, and a client may echo an outbound frame back as is. Dropping wrong types means a
malformed frame never changes state. The page always sends both keys.

### Decision: One `cleanText(s, maxRunes)` for nicknames and bios

**Choice**: Rename the body of `cleanNickname` to `cleanText(s string, maxRunes int) string`
(`ToValidUTF8` → strip `IsControl` and `Bidi_Control` → `TrimSpace` → `truncateRunes`).
`cleanNickname(s)` becomes `cleanText(s, maxNicknameRunes)`. Add the constant
`maxBioRunes = 160` next to `maxNicknameRunes` in `room.go`. The bio is cleaned in
`parseInbound` for frames, and in `HandleWebSocket` for `?bio=`.
**Alternatives considered**: A separate `cleanBio` that maps newlines to spaces. The spec
(CHAT-37) says "clean it like a nickname", and newlines are removed. The page already collapses
whitespace before sending, so the server never sees a typed newline from the page.
**Rationale**: There are now two real call sites with the same rule. Both are pure functions,
so cleaning happens outside `Run`, and `truncateRunes` already handles cutting at a rune
boundary.

### Decision: Rate gate is 1 s per connection, applied after validation; the page throttles to match

**Choice**: `const profileInterval = time.Second` (in `client.go`, next to `typingInterval`).
`readPump` adds `profile := rateGate{interval: profileInterval}`. A frame is checked first
(parse, then avatar) and then passes through `profile.allow(time.Now())`. Over budget means
`continue`, with no reply. Only admitted frames move the window, as `rateGate` already does.
**Alternatives considered**: A per-nickname or per-room limit. That needs shared state, and the
page legitimately sends one frame per room socket, which a per-connection limit already
allows.
**Rationale**: Each profile frame fans out to the whole room, so spam costs O(room) per frame.
1/s bounds that and is invisible to a person. Invalid frames don't use up the budget, so a
buggy picker can't block the next valid save. The page disables "Guardar" for 1.1 s after a
save, so an honest page never has an update dropped, and its in-memory profile never drifts
from what the server holds.

### Decision: Field placement and `omitempty`

**Choice**:
- `message`: `Bio string json:"bio,omitempty"` goes **right after `Avatar`**, giving the spec order
  `type, from, text, ts, avatar, bio, id, reply`. It is set only in `admit` (welcome, join), so
  chat, history and leave are byte-for-byte unchanged.
- `usersFrame`: `Bios map[string]string json:"bios,omitempty"` right after `Avatars`. It is
  nil when nobody has a bio.
- New `profileFrame{Type, From, TS, Avatar omitempty, Bio omitempty}`.

**Rationale**: Every existing exact-shape test keeps passing as long as its clients have no bio.
`relay` never sets `Bio` (CHAT-10: `bio` never appears on chat). `history` keeps the avatar
value that was copied at relay time, so CHAT-28's "history keeps the old avatar" holds without
extra code.

### Decision: The profile editor is a composer popover; the GIF picker swaps with it in pick mode

**Choice**: `#profile-panel` is a `.popover` inside `.composer-bar`, registered in `panels` as
`profile` with the header button `#profile-btn`. "Elegir GIF" calls `startAvatarPick()`. That
function hides the editor (its pending values stay in JS variables and the textarea), opens
`#gif-panel` in the same spot with the paste-URL block (`#gif-paste`) hidden, and sets
`gifPick = true`. Clicking a GIF in pick mode calls `endAvatarPick(url)`, which sets the pending
picture and shows the editor again. Escape in pick mode calls `endAvatarPick(null)`, which goes
back to the editor without changes.
**Alternatives considered**: (a) Move the `#gif-panel` node into the editor while picking.
Absolute positioning then breaks in the new parent, and the node has to be moved back on every
exit path. (b) A second search box and grid inside the editor. That duplicates markup, the
`/gifs` state machine and the debounce. (c) A modal `<dialog>` editor. `showModal()` makes the
composer (where the picker lives) inert.
**Rationale**: This reuses the existing panel mechanics (`closePanels`, outside click, Escape)
and the existing picker code unchanged except for one click branch. It moves no DOM nodes and
needs no CSSOM writes, so CSP stays trivially satisfied. Both popovers are already sized for
mobile (`min(…, 100%)` of the composer bar).

### Decision: The user popup is a single fixed, centered, non-modal card at body level

**Choice**: `#user-popup` (`role="dialog"`, `aria-labelledby="user-popup-nick"`) is a direct child
of `<body>`, positioned with `position:fixed; left:50%; top:50%; transform:translate(-50%,-50%);
width:min(300px, calc(100vw - 32px))`. It is not anchored to the clicked element. The page state
is `popupUser = null | {room, nick, returnFocus}`.
**Alternatives considered**: (a) Anchor it with `getBoundingClientRect()` and CSSOM `top`/`left`.
That needs viewport clamping and scroll/resize handling, and on a 360 px phone it often ends up
centered anyway. (b) Put it inside `#scroller` or `.chat-main`. It would be clipped by
`overflow` containers and affected by ancestor transforms.
**Rationale**: A centered fixed card can't overflow on any screen and needs no geometry code. At
body level, no ancestor `transform`, `filter` or `overflow` can capture `position:fixed`. It
holds no inputs, so the on-screen keyboard never covers it.

### Decision: Click targets are real buttons; the avatar button is mouse-only

**Choice**: In `addChat`, when `showHead`, the avatar goes inside
`<button type="button" class="user-trigger" tabindex="-1">` and the author name becomes
`<button type="button" class="user-trigger author">`, with
`aria-label = 'Ver perfil de ' + author`. In `renderUsers`, the avatar and name of each row are
wrapped in one `<button class="user-trigger users-who">`. The existing "Mensaje privado" row
button stays.
**Rationale**: Buttons give keyboard activation and roles for free and need no `role`/`tabindex`
work on spans. The avatar and the name next to it do the same thing, so only the name is a tab
stop. The avatar is removed from the tab order rather than `aria-hidden`, because a focusable
aria-hidden element is an a11y error.

### Decision: A profile saved while a socket is still connecting is resent after its welcome

**Choice**: `saveProfile()` sends the frame on every joined room whose `ws.readyState === OPEN`.
For a room whose socket is `CONNECTING`, it sets `room.resendProfile = true`, and the `welcome`
handler sends the current `myProfile` once and clears the flag. A room waiting on a retry timer
(`ws === null`) needs nothing, because `connect()` reads `myProfile` when it builds the URL.
**Alternatives considered**: On every `welcome`, compare the echoed `avatar`/`bio` with
`myProfile` and resend if they differ. That is stateless, but server-side cleaning (for example
pasted bidi characters) would cause a needless frame on every reconnect.
**Rationale**: It closes the one gap in "sent on every room socket" (CHAT-39) with one boolean
per room.

## Data Flow

### Editing the profile

```
Page A (editor)                readPump(A, room R)                    Room.Run(R)                         Pages in R
 "Guardar"
 myProfile = {avatar, bio'}    // bio' = whitespace collapsed, trimmed
 for each joined room:
   OPEN       → ws.send({"type":"profile","avatar":…,"bio":…})
   CONNECTING → room.resendProfile = true
 ─────────────────────────────▶ parseInbound: case profile
                                 avatar/bio absent|string? else drop
                                 bio = cleanText(bio, 160)
                                avatar != "" && !cr.allowedAvatar → drop
                                profile.allow(now)? else drop
                                ── broadcast ─────────────────────────▶ dispatch: member? → profile(in)
                                                                          c.avatar, c.bio = in.avatar, in.bio
                                                                          publish(profileFrame{…}) ──────▶ addRaw 'profile'
                                                                                                           setMemberProfile(room, from, …)
                                                                                                           renderUsers / renderProfile
                                                                                                           refresh popup if it shows `from`
```

### Connect, reconnect and late joiners

```
connect(room): /ws?room=…&nickname=…[&avatar=…][&bio=…]      // from myProfile
HandleWebSocket: avatar = allowedAvatar ? avatar : ""; bio = cleanText(Query("bio"), 160)
serveConn(conn, nick, avatar, bio) → Client{avatar, bio} → register
admit: welcome{…, avatar, bio}  users{users, avatars, bios}  history (chat entries: avatar as relayed, no bio)
       join{…, avatar, bio} → everyone
```

### Opening the user popup

```
click .user-trigger (message head or online-list row)
  └─ openUserPopup(room, nick, returnFocus)
       room.users.has(nick)? no → nothing (CHAT-40)
       closePanels(); popupUser = {…}; renderUserPopup(); focus primary button
 profile frame for nick → renderUserPopup()     leave/users without nick, teardown, activate, logout → closeUserPopup(false)
 Escape → closeUserPopup(true): focus returnFocus if still connected and visible, else #draft
```

## Backend Changes (exact)

### `internal/chat/message.go`

```go
const (
    // … existing …
    typeProfile = "profile"
)

type message struct {
    Type   string `json:"type"`
    From   string `json:"from"`
    Text   string `json:"text"`
    TS     int64  `json:"ts"`
    Avatar string `json:"avatar,omitempty"`
    Bio    string `json:"bio,omitempty"`     // welcome/join only (CHAT-37); never chat or history
    ID     int64  `json:"id,omitempty"`
    Reply  *quote `json:"reply,omitempty"`
}

type usersFrame struct {
    Type    string            `json:"type"`
    Users   []string          `json:"users"`
    Avatars map[string]string `json:"avatars,omitempty"`
    Bios    map[string]string `json:"bios,omitempty"` // nick -> bio, only members with one (CHAT-17/37)
    TS      int64             `json:"ts"`
}

// profileFrame announces a member's new profile to the whole room (CHAT-38). Never stored.
type profileFrame struct {
    Type   string `json:"type"` // always "profile"
    From   string `json:"from"`
    TS     int64  `json:"ts"`
    Avatar string `json:"avatar,omitempty"`
    Bio    string `json:"bio,omitempty"`
}

const (
    // … existing kinds …
    inboundProfile // shape-checked profile update; avatar not yet validated (readPump does it)
)

type inbound struct {
    from    *Client
    kind    inboundKind
    to      string
    text    string
    replyTo int64
    music   musicCmd
    avatar  string // inboundProfile: raw requested avatar ("" clears); readPump validates it
    bio     string // inboundProfile: cleanText(bio, maxBioRunes) ("" clears)
}
```

New case in `parseInbound`, placed after `typeMusic`:
```go
case typeProfile:
    // CHAT-21 rule 2b: never falls back to chat. Absent fields mean "none".
    avatar, okA := optionalString(env, "avatar")
    bio, okB := optionalString(env, "bio")
    if !okA || !okB {
        return inbound{}, false
    }
    return inbound{kind: inboundProfile, avatar: avatar, bio: cleanText(bio, maxBioRunes)}, true
```
```go
// optionalString returns env[key] when it is absent ("", true) or a JSON string; any other
// JSON value (null included) is reported as invalid.
func optionalString(env map[string]json.RawMessage, key string) (string, bool) {
    raw, ok := env[key]
    if !ok {
        return "", true
    }
    return jsonString(raw)
}
```
The avatar is **not** trimmed or cleaned. It must pass the validator exactly as sent, just like
`?avatar=` (the validator already rejects whitespace, `#`, invalid UTF-8 and more than 512
bytes).

### `internal/chat/nickname.go`

`cleanText(s string, maxRunes int) string` holds the current `cleanNickname` body, and
`cleanNickname(s)` returns `cleanText(s, maxNicknameRunes)`. `truncateRunes` is unchanged.

### `internal/chat/client.go`

- `const profileInterval = time.Second // minimum time between two profile updates from one connection (CHAT-38)`.
- `Client`:
  ```go
  avatar string // allowed URL or ""; set before register, then written only by Room.Run (CHAT-28, CHAT-38)
  bio    string // cleanText(…, maxBioRunes) or ""; same ownership as avatar (CHAT-37)
  ```
- `readPump`: add `profile := rateGate{interval: profileInterval}` and, after the music block:
  ```go
  if in.kind == inboundProfile {
      if in.avatar != "" && !cr.allowedAvatar(in.avatar) {
          continue // CHAT-38: invalid picture discards the whole update, no reply
      }
      if !profile.allow(time.Now()) {
          continue // CHAT-38: over the per-connection budget
      }
  }
  ```

### `internal/chat/room.go`

- `const maxBioRunes = 160` next to `maxNicknameRunes`.
- `Room` gains `validAvatar func(string) bool // set by NewServer before serving; read-only after (never read by Run)`.
  `NewRoom` leaves it nil.
- `allowedAvatar` (see the decision above).
- `admit`: `Bio: c.bio` on welcome and join, and `Bios: cr.memberValues(func(c *Client) string { return c.bio })`
  in `users`.
- `avatars()` becomes `memberValues(get func(*Client) string) map[string]string`, the same loop
  with `get(c)`. It returns nil when every value is `""`. The `Avatars:` call site passes
  `func(c *Client) string { return c.avatar }`. This is the second identical use, so the extraction is
  justified.
- `dispatch`: `case inboundProfile: cr.profile(in)`.
- New handler:
  ```go
  // profile replaces the sender's whole profile and announces it to the room, sender
  // included (CHAT-38). Must-have delivery; never stored. Run-only.
  func (cr *Room) profile(in inbound) {
      in.from.avatar, in.from.bio = in.avatar, in.bio
      f := profileFrame{Type: typeProfile, From: in.from.nickname, TS: time.Now().UnixMilli(), Avatar: in.avatar, Bio: in.bio}
      if b := encode(f); b != nil {
          cr.publish(b)
      }
  }
  ```
  The update is always applied and published, even when it equals the current profile. The echo
  is how the page confirms the save, and the rate gate bounds the cost.
- `serveConn(conn, nickname, avatar, bio string)` sets `bio` in the `Client` literal.
- `relay`, `remember`, `leave` and `direct` are unchanged. `relay` copies `in.from.avatar`, which is
  the current value inside `Run`.

### `internal/chat/server.go`

- Remove the `validAvatar` field from `Server`. In `NewServer`, `r.validAvatar = validAvatar`
  inside the existing room loop, and update the doc comment.
- `HandleWebSocket`:
  ```go
  avatar := c.Query("avatar")
  if !room.allowedAvatar(avatar) {
      avatar = "" // invalid or disabled: join without a picture, never reject (CHAT-28)
  }
  bio := cleanText(c.Query("bio"), maxBioRunes) // CHAT-37: blank or absent means no bio, never reject
  room.serveConn(conn, normalizeNickname(c.Query("nickname")), avatar, bio)
  ```

### Race analysis (`go test -race`)

| Data | Writers | Readers | Ordering |
|------|---------|---------|----------|
| `Room.validAvatar` | `NewServer` (once) | `HandleWebSocket`, `readPump` | Written before the HTTP server starts and before any `serveConn`, so goroutine start gives happens-before. `Run` never reads it. |
| `Client.avatar`, `Client.bio` | `serveConn` (before `register <-`), `Room.profile` (in `Run`) | `admit`, `relay`, `memberValues` (in `Run`) | Channel send/receive on `register`, then single-goroutine ownership. `readPump`/`writePump` never touch them. |
| `rateGate profile` | `readPump` | `readPump` | Local to one goroutine. |

No new shared state. No locks.

## Frontend Changes (`cmd/chatroom`)

### `app.js` — state and constants

- Replace `AVATAR_KEY` with `const LEGACY_AVATAR_KEY = 'chadroom.avatar'; // removed on start (CHAT-29)`
  and run `try { localStorage.removeItem(LEGACY_AVATAR_KEY); } catch {}` once at startup, where
  the old `getItem` was.
- `const MAX_BIO = 160; // mirrors maxBioRunes in internal/chat/room.go (UTF-16 maxlength ≤ runes)`
- `const PROFILE_GAP_MS = 1100; // > profileInterval in internal/chat/client.go`
- `const BIO_PLACEHOLDER = '✿ vibecoder profesional ✿'; // MI PERFIL card without a bio`
- Replace `let requestedAvatar` with `let myProfile = { avatar: '', bio: '' }; // page memory only (CHAT-39)`.
- Editor state: `let editorAvatar = ''; // pending picture while the editor is open`,
  `let gifPick = false; // the GIF panel picks a picture instead of sending`,
  `let profileSaveLocked = false;`.
- Popup state: `let popupUser = null; // {room, nick, returnFocus}`.
- Room objects (`createRoom`) gain `bios: new Map()` and `resendProfile: false`. `joinRoom` and
  `teardown` reset both, next to `avatars`.

### `app.js` — join and connect

- Delete the `avatarInput` code: storage read, `input` listener, the `validAvatarInput` check in
  submit, and the `localStorage.setItem/removeItem`. `enterChat(nick)` takes one argument.
  Keep `validAvatarInput`, which the picker uses now.
- `connect(room)`: append
  `(myProfile.avatar ? '&avatar=' + encodeURIComponent(myProfile.avatar) : '') + (myProfile.bio ? '&bio=' + encodeURIComponent(myProfile.bio) : '')`.
- `#leave-btn` handler adds: `myProfile = { avatar: '', bio: '' }; editorAvatar = ''; $('profile-bio-input').value = ''; closeUserPopup(false);`.
  It already calls `closePanels()`.

### `app.js` — frames

- New helper used by `welcome`, `join` and `profile`. It replaces the duplicated avatar set/delete
  in those cases:
  ```js
  // A frame's avatar/bio replace what the page knows about nick; absent or invalid means none.
  function setMemberProfile(room, nick, avatar, bio) {
    if (typeof avatar === 'string' && isAllowedImage(avatar)) room.avatars.set(nick, avatar); else room.avatars.delete(nick);
    if (typeof bio === 'string' && bio.trim() !== '') room.bios.set(nick, bio); else room.bios.delete(nick);
  }
  ```
- `users`: rebuild `room.bios` like `room.avatars` (object check, `room.users.has(nick)`, string
  value). Then, if `popupUser?.room === room`, close the popup when the nick is gone and
  re-render it otherwise.
- `welcome`: `setMemberProfile(room, msg.from, msg.avatar, msg.bio)`. Then
  `if (room.resendProfile) { room.resendProfile = false; sendProfile(room); }`.
- `join`: `setMemberProfile(…)`. The existing comment about reused nicknames now covers bios too.
- `leave`: also `room.bios.delete(msg.from)`, and
  `if (popupUser?.room === room && popupUser.nick === msg.from) closeUserPopup(false)`.
- New case:
  ```js
  case 'profile':
    if (!room.users.has(msg.from)) break;
    setMemberProfile(room, msg.from, msg.avatar, msg.bio);
    if (room === active) { renderUsers(); renderProfile(); }
    if (popupUser?.room === room && popupUser.nick === msg.from) renderUserPopup();
    break;
  ```
  Already-rendered messages are left alone (CHAT-29). Update the comment above `addRaw` to
  mention `bio` and `profile`.

### `app.js` — profile editor

- `panels.profile = { panel: $('profile-panel'), btn: $('profile-btn') }`.
- Extract `showPanel(key)` (`closePanels()` + unhide + `aria-expanded=true`) from `togglePanel`.
  `togglePanel(key)`: closing goes through `closePanels()`. Opening `'profile'` calls
  `openProfileEditor()`. Opening `'gif'` calls `showPanel('gif'); openGifPanel();` as before.
- `closePanels()` also does `gifPick = false; $('gif-paste').hidden = false; relabelGifItems();`.
- `openProfileEditor()`: `closeUserPopup(false); editorAvatar = myProfile.avatar; bioInput.value = myProfile.bio; showPanel('profile'); renderEditor(); bioInput.focus();`.
  It is called by `#profile-btn` (via `togglePanel`), `#profile-card-edit` and `#user-popup-edit`.
- `renderEditor()`: `#profile-preview` → `replaceChildren(avatarEl(ownName(), editorAvatar))`,
  where `ownName()` is `active?.myName || requestedNick`. `#profile-clear.disabled = !editorAvatar`,
  `#profile-pick.disabled = gifSearchOff`, `#profile-save.disabled = profileSaveLocked`, and
  `#profile-bio-count` is `bioInput.value.length + '/' + MAX_BIO`.
- `startAvatarPick()`: if `gifSearchOff`, show "La búsqueda de GIF no está disponible." in
  `#profile-error` and return. Otherwise `showPanel('gif'); gifPick = true; $('gif-paste').hidden = true; relabelGifItems(); openGifPanel();`.
- `endAvatarPick(url)`: `if (url) editorAvatar = url; showPanel('profile'); renderEditor(); $('profile-pick').focus();`.
  `showPanel` → `closePanels` resets pick mode.
- `disableGifSearch()`: if `gifPick`, call `endAvatarPick(null)` and show the same error.
- `renderGifs` click: `if (gifPick) { if (validAvatarInput(g.url)) endAvatarPick(g.url); else showProfileError('Ese GIF no se puede usar como foto.'); } else if (sendText(g.url)) closePanels();`.
  Each item's `aria-label` comes from `gifItemLabel()`, which returns `gifPick ? 'Usar como foto' : 'Enviar GIF'`,
  and `relabelGifItems()` re-applies it to the buttons already rendered.
- `#profile-clear`: `editorAvatar = ''; renderEditor();`.
- `bioInput` (`#profile-bio-input`): `input` → `renderEditor()`. `keydown` Enter (not composing)
  → `preventDefault()`, so no newlines are typed. Because the editor is inside `#send-form`, this
  also stops Enter from submitting the draft.
- `saveProfile()` (`#profile-save` click):
  ```js
  if (profileSaveLocked) return;
  const bio = bioInput.value.replace(/\s+/g, ' ').trim();
  myProfile = { avatar: editorAvatar, bio };
  for (const room of joinedRooms()) {
    if (room.ws?.readyState === WebSocket.OPEN) sendProfile(room);
    else if (room.ws?.readyState === WebSocket.CONNECTING) room.resendProfile = true;
  }
  profileSaveLocked = true;
  setTimeout(() => { profileSaveLocked = false; if (!panels.profile.panel.hidden) renderEditor(); }, PROFILE_GAP_MS);
  closePanels();
  $('profile-btn').focus();
  ```
  `sendProfile(room)` is `room.ws.send(JSON.stringify({ type: 'profile', avatar: myProfile.avatar, bio: myProfile.bio }))`.
  The worst case is about 1.5 KB, well under 4096 bytes, so no size check is needed. The UI
  changes only when the echoed `profile` frame arrives (CHAT-39).
- `renderProfile()`: first set `#profile-bio` text to `active?.bios.get(active.myName) || BIO_PLACEHOLDER`.
  The existing image early-return logic stays below that, so it no longer skips the bio.

### `app.js` — user popup

- `openUserPopup(room, nick, returnFocus)`: return unless `room.joined && room.users.has(nick)`.
  Then `closePanels(); popupUser = { room, nick, returnFocus }; renderUserPopup(); $('user-popup').hidden = false;`
  and focus `#user-popup-dm`, or `#user-popup-edit` for yourself.
- `renderUserPopup()`: `#user-popup-avatar` gets `replaceChildren(avatarEl(nick, room.avatars.get(nick)))`.
  `#user-popup-nick.textContent = nick`. `#user-popup-bio.textContent` is the bio or `'Sin descripción'`,
  and the class `empty` is toggled. `#user-popup-dm.hidden = self`, `#user-popup-edit.hidden = !self`
  (`self` = `nick === room.myName`), and `#user-popup-dm` gets `aria-label 'Mensaje privado a ' + nick`.
- `closeUserPopup(restoreFocus)`: if `!popupUser`, return. Hide the popup and clear `popupUser`.
  When `restoreFocus`, focus `returnFocus` if it `isConnected` and has an `offsetParent`, else
  focus `draft`.
- `#user-popup-dm` click: `const { room, nick } = popupUser; closeUserPopup(false); openConversation(room, nick);`.
  `openConversation` focuses the draft.
- `#user-popup-edit` click: `openProfileEditor()`, which closes the popup.
- `activate()` and `teardown()` (when `popupUser?.room === room`) call `closeUserPopup(false)`.
- **Escape priority** (the single existing listener):
  ```js
  if (popupUser) closeUserPopup(true);
  else if (gifPick && !panels.gif.panel.hidden) endAvatarPick(null);
  else if (Object.values(panels).some((p) => !p.panel.hidden)) closePanels();
  else if (replyTarget) cancelReply();
  ```
- **Outside click**: the existing handler's selector becomes `'.popover, .tool, #user-popup'`,
  so clicks inside the popup (for example "Editar perfil" opening the editor) don't immediately
  close panels. Add a second document click handler:
  `if (popupUser && !e.target.closest('#user-popup, .user-trigger')) closeUserPopup(false);`.
  The click that opens the popup lands on a `.user-trigger`, so it never closes its own popup.
- **Triggers**:
  - `addChat` (only when `showHead`):
    `const who = userTrigger(ownerRoom(pane), author)` for the avatar (append `avatarEl`, then
    `who.tabIndex = -1`), and a second `userTrigger` with class `author` and `textContent = author`
    replaces `span.author`.
  - `userTrigger(room, nick)` creates `<button type="button" class="user-trigger">` with
    `aria-label 'Ver perfil de ' + nick` and click
    `(e) => openUserPopup(room, nick, e.currentTarget)`.
  - `renderUsers`: each row's avatar and label go inside
    `userTrigger(room, name)` + class `users-who`. Its click opens with
    `returnFocus = panels.users.btn`, because the row disappears when `closePanels` runs. The
    "vos" tag and the per-row "Mensaje privado" button are unchanged.

Nicknames and bios reach the DOM only through `textContent` and `aria-label`, never `innerHTML`,
ids, classes or selectors.

### `index.html`

- Remove the `#avatar-url` field block (label, input, `#avatar-hint`) from the join form.
- In the header `.actions`, before `#mute-btn`:
  `<button id="profile-btn" type="button" class="btn btn-ghost tool tool-profile" aria-expanded="false" aria-controls="profile-panel">Mi perfil</button>`.
- In `.composer-bar`, before `#emoji-panel`:
  ```html
  <div id="profile-panel" class="popover profile-editor" role="dialog" aria-labelledby="profile-title" hidden>
    <div id="profile-title" class="profile-editor-title">Mi perfil</div>
    <div class="profile-editor-pic">
      <div id="profile-preview" class="profile-preview"></div>
      <button id="profile-pick" type="button" class="btn">Elegir GIF</button>
      <button id="profile-clear" type="button" class="btn btn-ghost">Quitar foto</button>
    </div>
    <label for="profile-bio-input">Descripción</label>
    <!-- maxlength mirrors maxBioRunes in internal/chat/room.go -->
    <textarea id="profile-bio-input" class="input" rows="3" maxlength="160" aria-describedby="profile-bio-count"></textarea>
    <div id="profile-bio-count" class="text-muted profile-count">0/160</div>
    <p id="profile-error" class="profile-error" role="alert" hidden></p>
    <button id="profile-save" type="button" class="btn btn-primary">Guardar</button>
  </div>
  ```
- In `#gif-panel`, wrap `.gif-hint` and `.gif-row` in `<div id="gif-paste">`.
- In the scrapbook card, the static `<p>` becomes `<p id="profile-bio" class="profile-bio">✿ vibecoder profesional ✿</p>`,
  followed by `<button id="profile-card-edit" type="button" class="btn btn-ghost tool">Editar perfil</button>`.
- Just before `<script src="/app.js">`:
  ```html
  <div id="user-popup" class="user-popup" role="dialog" aria-labelledby="user-popup-nick" hidden>
    <div id="user-popup-avatar" class="user-popup-avatar"></div>
    <b id="user-popup-nick" class="user-popup-nick"></b>
    <p id="user-popup-bio" class="user-popup-bio"></p>
    <button id="user-popup-dm" type="button" class="btn btn-primary">Mensaje privado</button>
    <button id="user-popup-edit" type="button" class="btn" hidden>Editar perfil</button>
  </div>
  ```

### `app.css`

Append a `/* Profiles (CHAT-39, CHAT-40) */` block at the end of the file, after the retro skin
and the replies block, using existing tokens only:
- `.user-trigger`: `border:0; background:none; padding:0; margin:0; font:inherit; color:inherit; text-align:inherit; cursor:pointer`,
  plus a `:focus-visible` outline using `--color-accent-600`.
- `.users-who`: `display:flex; align-items:center; gap:var(--space-2); flex:1; min-width:0`.
- `.profile-editor`: `width:min(320px,100%)`. `.profile-editor-pic`: flex row with gap.
  `.profile-preview .avatar`: 56 px square. `.profile-editor textarea`: `resize:none; width:100%`.
  `.profile-count`: `font-size:12px; text-align:right`. `.profile-error`: like `.join-error`.
- `.profile-bio`: `overflow-wrap:anywhere`.
- `.user-popup`: `position:fixed; left:50%; top:50%; transform:translate(-50%,-50%); z-index:20; width:min(300px, calc(100vw - 32px)); max-height:calc(100dvh - 32px); overflow-y:auto;`
  plus the `.popover` surface, radius and shadow. Flex column, centered.
  `.user-popup-avatar .avatar`: 72 px. `.user-popup-bio`: `overflow-wrap:anywhere; white-space:pre-wrap`.
  `.user-popup-bio.empty`: italic, `--color-neutral-500`.

No inline styles and no CSSOM writes. The global `[hidden]{display:none !important}` handles
visibility.

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/chat/message.go` | Modify | `typeProfile`; `Bio` on `message` (after `Avatar`); `Bios` on `usersFrame`; `profileFrame`; `inboundProfile` kind; `avatar`/`bio` on `inbound`; `profile` case in `parseInbound`; `optionalString` |
| `internal/chat/nickname.go` | Modify | `cleanText(s, maxRunes)`; `cleanNickname` delegates to it |
| `internal/chat/client.go` | Modify | `Client.bio`; ownership comments; `profileInterval`; avatar check and profile gate in `readPump` |
| `internal/chat/room.go` | Modify | `maxBioRunes`; `Room.validAvatar` + `allowedAvatar`; `memberValues` replaces `avatars()`; bio in welcome/join/users; `dispatch` case; `profile` handler; `serveConn(…, bio)` |
| `internal/chat/server.go` | Modify | Validator moved to rooms in `NewServer`; `?bio=` read and cleaned; `Server.validAvatar` removed |
| `internal/chat/room_test.go` | Modify | `decode` allows `bio` on welcome/join; `decodeUsers` allows `bios`; new hub tests |
| `internal/chat/client_test.go` | Modify | `TestParseInbound` profile rows; `TestCleanText` |
| `internal/chat/server_test.go` | Modify | `dialProfile` helper (nick, avatar, bio); end-to-end profile tests |
| `cmd/chatroom/app.js` | Modify | `myProfile`, legacy key cleanup, `bios`, `setMemberProfile`, `profile` frame, editor, GIF pick mode, user popup, triggers, Escape/outside click, logout reset |
| `cmd/chatroom/index.html` | Modify | Remove `#avatar-url`; add `#profile-btn`, `#profile-panel`, `#gif-paste`, `#profile-bio`, `#profile-card-edit`, `#user-popup` |
| `cmd/chatroom/app.css` | Modify | Profile editor, user popup and `.user-trigger` rules |
| `cmd/chatroom/main_test.go` | Modify | Replace `TestIndexHasAvatarField`; add `TestIndexHasProfileEditor` and `TestIndexHasUserPopup` |
| `README.md` | Modify | Protocol: `?bio=`, `bio`, `bios`, inbound/outbound `profile`, rate limit; join form no longer asks for a picture |

`music.go`, `cmd/chatroom/main.go` (it still passes `validAvatar` to `NewServer`), `gifs.go` and
`playlist.go` don't change. `imageHosts`/`IMAGE_HOSTS` don't change.

## Test Plan

Run `go test -race ./...` and `gofmt -l .` (with snap, use `/snap/go/current/bin/…`).

**Helpers (`room_test.go`)**
- `decode`: `allowed["bio"] = true` only for `typeJoin` and `typeWelcome`. A chat or leave frame
  with `bio` fails, which checks CHAT-10 in every existing test that decodes chat.
- `decodeUsers`: allow the optional `bios` key, which must be an object whose keys are all in `users`.
- `bioClient(nick, avatar, bio)` builds on `avatarClient`.

**Hub unit tests (`room_test.go`, `NewRoom` plus fake clients, inbound sent straight to `broadcast`)**
- `TestWelcomeAndJoinCarryBio`: welcome and join have `bio`. A client without a bio gets no `bio`
  key (`exactFields`).
- `TestUsersFrameBios`: only members with a bio appear. `bios` is omitted when nobody has one.
- `TestProfileUpdateBroadcast`: `A` and `B` get exactly
  `{"type":"profile","from":"A","ts":…,"avatar":…,"bio":"hola"}`. Check key order with a
  byte-prefix comparison up to `ts` and `exactFields`.
- `TestProfileClear`: an update with empty fields gives `exactFields(type, from, ts)`, and a later
  joiner's `users` has no `avatars`/`bios`.
- `TestProfileHistoryKeepsOldAvatar`: the CHAT-28 scenario (history keeps `U`, users map has `V`).
- `TestProfileChangesLaterChatAvatar`: after an update, `A`'s next chat carries the new avatar
  and no `bio`.
- `TestProfileFromRemovedClientIgnored`: nothing is published.
- `TestProfileNotInHistory`: a late joiner's history contains only chat entries.

**`client_test.go`**
- `TestParseInbound` new rows: both strings; `{"type":"profile"}` gives empty fields; only `bio`;
  `bio:5`, `bio:null`, `avatar:null`, `avatar:[]`, `avatar:{}` discarded (`ok == false`, never
  chat); a bio with `‮`, `\n`, a tab and 200 `x` is cleaned to 160 runes; forged `from` and
  `text` ignored. Existing rows are unaffected (`avatar`/`bio` stay zero).
- `TestCleanText`: existing nickname cases through `cleanText(…, 32)`, plus a 160-rune cut with
  multibyte runes and a trailing-space trim.

**`server_test.go` integration (httptest + gorilla, `startAvatarRooms` with an allow-list validator)**
- `TestBioQueryParam`: CHAT-37 scenarios (announced on join, cleaned and truncated, blank omitted).
- `TestReusedNicknameHasNoStaleBio`.
- `TestProfileUpdateOverWebSocket`: the "update is broadcast" scenario.
- `TestProfileInvalidAvatarDiscarded`: an invalid avatar, then a sentinel chat from `A`. `B`'s next
  frame is the chat, and a late joiner's `bios` still has `hola`.
- `TestProfileAvatarsDisabled`: with a nil validator, a non-empty avatar is dropped and
  `{"type":"profile","bio":"x"}` is accepted.
- `TestProfileRateLimited`: two back-to-back updates and a sentinel chat. `B` gets one `profile`
  and then the chat.
- `TestProfileInvalidIsNotChat`: `{"type":"profile","bio":5}` and then a sentinel. The sentinel is
  the next frame.
- `TestProfileForgedFromIgnored`.
- `TestProfileConcurrentWithJoins`: one client sends updates, about 1 s apart over 3 s, while others
  connect and disconnect. This exercises the `admit`/`profile` paths under `-race`.
- `TestForgedInboundAvatarIgnored` must stay green unchanged.

**`cmd/chatroom/main_test.go`**
- `TestIndexHasNoAvatarField` (replaces `TestIndexHasAvatarField`): `index.html` has no
  `id="avatar-url"` and still has `id="profile-avatar"`. `app.js` contains
  `removeItem(LEGACY_AVATAR_KEY)` and no `setItem(LEGACY_AVATAR_KEY`.
- `TestIndexHasProfileEditor`: ids `profile-btn`, `profile-panel`, `profile-preview`,
  `profile-pick`, `profile-clear`, `profile-bio-input`, `profile-bio-count`, `profile-save`,
  `gif-paste`, `profile-bio` and `profile-card-edit`. The `#profile-bio-input` start tag has
  `maxlength="160"`, and `#profile-panel` sits inside `class="composer-bar"`. `app.js` contains
  `type: 'profile'`, `const MAX_BIO = 160` and `&bio=`.
- `TestIndexHasUserPopup`: ids `user-popup`, `user-popup-avatar`, `user-popup-nick`,
  `user-popup-bio`, `user-popup-dm` and `user-popup-edit`, plus `role="dialog"`. `app.js` contains
  `'Sin descripción'` and `user-trigger`.
- `TestIndexHasNoInlineCode` already covers CSP.

**Manual two-browser checklist** (`go run ./cmd/chatroom` with `GIPHY_API_KEY`; browser A and
private window B)
1. The join form has only "Apodo". In DevTools, `localStorage['chadroom.avatar']` (set by hand
   beforehand) is gone after a reload.
2. A: "Mi perfil" → "Elegir GIF". The GIF panel has no paste box and the items say "Usar como
   foto". Pick one: the editor comes back with the preview, and no chat message was sent. Type a
   bio (the counter updates, Enter adds no newline) → "Guardar". In both pages the online list,
   the MI PERFIL card (A) and new messages show the GIF. Old messages keep the old picture.
3. A joins `#juegos` and `#musica`, then saves again. B, in each room, sees the change.
   "Guardar" is disabled for about 1 s.
4. B clicks A's name on a message, then A's row in the online list. The popup shows the picture,
   name and bio, and focus is on "Mensaje privado". Clicking it opens the DM tab and closes the
   popup. B's own entry shows "Editar perfil", which opens the editor.
5. With the popup open, A edits the bio: B's popup updates live. A leaves: B's popup closes, and
   clicking A's old messages opens nothing.
6. Escape order: popup → (pick mode back to editor) → panel → reply preview. Escape on the popup
   returns focus to the trigger. An outside click closes the popup without moving focus.
7. A's bio is `<img src=x onerror=alert(1)>`: shown literally, and the Network tab shows no
   request to `x`.
8. Stop the server while A has a profile, then restart it. A reconnects with `avatar` and `bio`
   in the `/ws` URL (Network tab) and B sees them again.
9. A clicks "Salir" and joins again. No `avatar`/`bio` in the URL, and the editor is empty.
10. At a 360 px width: "Mi perfil" is visible in the header, the editor and GIF picker fit above
    the composer, and the popup is centered and fully visible.
11. Without `GIPHY_API_KEY`: "Elegir GIF" is disabled or shows "La búsqueda de GIF no está
    disponible." and never opens an empty panel.

## Risks & Trade-offs

- **Nickname reuse**: Clicking an old message from a previous "Ana" opens the profile of
  whoever is "Ana" now. Nicknames are the only identity (same as DMs). Accepted for a friends
  chat. CHAT-40 only promises the current online member.
- **Bio in access logs**: `gin.Default()`'s logger writes the full query string, so `bio` is
  logged on every connect, like `nickname` and `avatar` today. It isn't secret, but it is
  user-written text. Accepted. Backlog: a logger that strips `/ws` query strings.
- **Silent drops**: A rate-limited or invalid update gets no reply. The page throttles at 1.1 s
  and only sends validated GIF URLs, so an honest page never hits this. A hand-crafted client
  just sees no echo.
- **Shared GIF panel state**: Pick mode adds one boolean that every exit path must reset.
  `closePanels` is the single reset point, and both `showPanel` and the outside-click handler go
  through it.
- **`renderUsers` rebuild on each `profile` frame**: Focus is lost if the online list is open
  and focused at that moment. The same already happens on join and leave, and the rate gate
  bounds how often it can happen.
- **Counting mismatch**: `maxlength="160"` counts UTF-16 units and the server counts runes.
  The page is never more permissive than the server, so emoji-heavy bios are cut on the page,
  never by the server.
- **Header crowding on phones**: One more header button. It is checked in manual step 10. If it
  doesn't fit, the fallback is a shorter label via CSS (no markup change).
- **Popup isn't anchored**: A centered card is less "contextual" than one anchored to the
  click. It was chosen for robustness on mobile and zero geometry code.

## Security Notes

- **XSS**: Bio and nickname are cleaned on the server (control and bidi characters) and
  rendered only with `textContent` or `aria-label`. There is no `innerHTML`. CSP stays strict:
  the new markup is static, and there are no inline handlers or style attributes.
- **SSRF / IP leak**: Profile avatars pass the same `validAvatar` as `?avatar=`
  (https, `imageHosts`, ≤ 512 bytes, image extension). The page also checks `isAllowedImage`
  before loading any received URL (CHAT-29), and `validAvatarInput` before sending a picked
  one.
- **Forgery**: `from` is always the connection's assigned nickname. Inbound `from`, `ts` and
  `text` on a profile frame are ignored. `avatar`/`bio` on chat, dm and typing frames are never
  read (`TestForgedInboundAvatarIgnored`, plus `decode` rejecting `bio` on chat).
- **Abuse bounds**: 1 update/s/connection, a 160-rune bio, a 4096-byte inbound frame limit and
  `publish`'s slow-client removal. Profiles exist only in memory and disappear with the
  connection (CHAT-37).
- **Secrets**: No new logging. The Giphy key path (`gifs.go`) is untouched.
