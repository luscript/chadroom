# Design: Profile picture

## Technical Approach

The avatar is one more per-connection datum next to the nickname. It arrives as `?avatar=` on
`GET /ws`, is checked once in `Server.HandleWebSocket` (outside `Run`, before `register`) by a
validator that `package main` injects, and is stored on the `Client` as an immutable string
(`""` = no picture). `Run` only reads it when it builds `welcome`, `users`, `join` and `chat`
frames. History keeps it because `relay` stores the full `message`.

- **Server.** `NewServer` gains one `validAvatar func(string) bool` parameter (`nil` = avatars off).
  `main.go` passes `validAvatar`, a 10-line function next to `imageHosts`. The chat package never
  parses URLs. It treats the avatar as an opaque, already-checked string.
- **Wire.** `message` gains `Avatar string \`json:"avatar,omitempty"\`` and `usersFrame` gains
  `Avatars map[string]string \`json:"avatars,omitempty"\``. A frame from a user with no picture is
  byte-for-byte what it is today, so the existing strict shape helpers (`decode`, `frameFields`)
  keep passing unchanged and still prove that the field is absent.
- **Page.** It adds an optional URL field on the join form (checked with `isAllowedImage` plus a
  512-byte limit, and remembered in `localStorage`). `requestedAvatar` is re-sent on every room
  socket and reconnect. Each room keeps an `avatars` Map (nick→url) built from `users`, `join` and
  `leave`. One `avatarEl(name, url)` helper renders an `<img>` or the letter. `addChat` and
  `renderUsers` call it. The "MI PERFIL" card shows the room's own avatar.

No new dependency, no CSP change, no change to `imageHosts`/`IMAGE_HOSTS`, no new inbound frame,
no lock.

## Architecture Decisions

### Decision: Inject the validator as a `NewServer` parameter

**Choice**:

```go
// internal/chat/server.go
type Server struct {
	rooms          map[string]*Room
	defaultRoom    *Room
	allowedOrigins map[string]struct{}
	validAvatar    func(string) bool // nil: every ?avatar is dropped
	upgrader       websocket.Upgrader
}

// NewServer builds a Server over rooms. validAvatar reports whether a requested
// avatar URL may be shown to others; nil disables avatars.
func NewServer(allowedOrigins []string, validAvatar func(string) bool, rooms ...*Room) *Server
```

`HandleWebSocket` changes only its last line:

```go
avatar := c.Query("avatar")
if avatar != "" && (s.validAvatar == nil || !s.validAvatar(avatar)) {
	avatar = "" // invalid or disabled: join without a picture, never reject (like nickname normalization)
}
room.serveConn(conn, normalizeNickname(c.Query("nickname")), avatar)
```

Call sites: `main.go` `newChatServer` → `chat.NewServer(allowedOrigins, validAvatar, rooms...)`.
Tests → `nil` in `client_test.go` (`startRooms` :32, :425) and `server_test.go` (:44, :158, :209).
**Alternatives considered**: (a) An exported `Server.ValidAvatar` field set after construction. It
needs zero test edits, but it breaks the documented "never modified after NewServer" invariant
and makes an unset field a silent default. (b) A functional option (`WithAvatarValidator`). That
is a pattern the codebase doesn't use, for a single option. (c) Moving `imageHosts` into
`internal/chat`. The chat package would then own CSP data, which is the opposite of the
proposal's "URL-agnostic" rule.
**Rationale**: `allowedOrigins` already reaches the server the same way (policy from `main`
through the constructor). Positional parameters are explicit, and a forgotten argument fails at
compile time. The field is read-only after construction, so concurrent handlers stay lock-free.

### Decision: `validAvatar` lives in `main.go` and is not merged with `canonicalGIF`

**Choice**:

```go
// cmd/chatroom/main.go, right after imageHosts
const maxAvatarBytes = 512 // mirrored by MAX_AVATAR_BYTES in app.js

// avatarPath mirrors IMAGE_PATH in app.js.
var avatarPath = regexp.MustCompile(`(?i)\.(gif|webp|png|jpe?g)$`)

// validAvatar accepts the same URLs the page renders inline (isAllowedImage): https, an exact
// imageHosts host (so no port), no userinfo, no fragment, an image extension, at most
// maxAvatarBytes. The URL is relayed exactly as given; it is never rewritten.
func validAvatar(raw string) bool {
	if raw == "" || len(raw) > maxAvatarBytes || strings.Contains(raw, "#") ||
		strings.ContainsFunc(raw, unicode.IsSpace) {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Opaque == "" && u.User == nil &&
		slices.Contains(imageHosts, u.Host) && avatarPath.MatchString(u.EscapedPath())
}
```

Notes:
- `u.Host` exact match rejects `host:443`, `host:`, uppercase hosts and any backslash trick
  (`evil\@i.imgur.com` parses with userinfo and is rejected).
- `EscapedPath` is what the browser's `URL.pathname` sees, so `/a%2Epng` fails on both sides.
- The whitespace and `#` rules mirror `isAllowedImage`'s `/\s/` and `hash === ''`. Rejecting any
  `#` also covers an empty trailing fragment.
- Query strings are allowed, as `isAllowedImage` allows them.

**Alternatives considered**: (a) Extracting a shared `imageURL(raw) (*url.URL, bool)` core
for `canonicalGIF` and `validAvatar`. The overlap is one boolean line. The two differ on purpose:
Giphy-only plus `.gif` and *rewrites* (strips query) versus all hosts plus four extensions and
*pass-through*. A shared helper would need flags. (b) Canonicalizing the avatar (strip the query,
`u.String()`). The browser would then fetch a URL the user never typed, and Imgur/Tenor need no
cleanup.
**Rationale**: It is a pure function with a table test, it sits next to the data it depends on,
and it gives the same verdict as the page, so whatever the server relays the page renders.

### Decision: The avatar is an immutable `Client` field, set before `register`

**Choice**:

```go
// client.go
type Client struct {
	conn       *websocket.Conn
	send       chan []byte
	registered chan struct{}
	nickname   string
	avatar     string // validated URL or ""; set before register and never changed
}

// room.go
func (cr *Room) serveConn(conn *websocket.Conn, nickname, avatar string)
```

The send on `cr.register` happens-before `Run` reads `c.avatar`, and nothing writes it later.
The pumps never read it. `inbound` is unchanged: `relay` reads `in.from.avatar`, so the
zero-value `inbound{from, text}` literals still mean room chat.
**Alternatives considered**: Carrying the avatar on `inbound`. That duplicates state per frame,
and a client could in theory diverge between frames.
**Rationale**: It follows the nickname's path with one difference: the hub never rewrites it.

### Decision: Additive `omitempty` fields; positional literals become keyed

**Choice** (`message.go`):

```go
// message is the outbound envelope for chat/join/leave/welcome. type, from, text and ts are
// always present (text is "" except for chat); avatar only when the sender has one (welcome,
// join, chat and history entries; never leave).
type message struct {
	Type   string `json:"type"`
	From   string `json:"from"`
	Text   string `json:"text"`
	TS     int64  `json:"ts"`
	Avatar string `json:"avatar,omitempty"`
}

type usersFrame struct {
	Type    string            `json:"type"`
	Users   []string          `json:"users"`
	Avatars map[string]string `json:"avatars,omitempty"` // nick -> url, only members with one; omitted when none
	TS      int64             `json:"ts"`
}
```

`room.go`. Every `message{...}` / `usersFrame{...}` literal switches to keyed fields (adding a
field breaks positional ones at compile time anyway):

| Site | After |
|------|-------|
| `admit` welcome | `message{Type: typeWelcome, From: name, Avatar: c.avatar, TS: ts}` |
| `admit` users | `usersFrame{Type: typeUsers, Users: cr.userNames(), Avatars: cr.avatars(), TS: ts}` |
| `admit` join | `message{Type: typeJoin, From: name, Avatar: c.avatar, TS: ts}` |
| `leave`, `announceDropped` | `message{Type: typeLeave, From: c.nickname, TS: …}` (no avatar) |
| `relay` | `message{Type: typeChat, From: in.from.nickname, Text: in.text, Avatar: in.from.avatar, TS: …}` stored by `remember`, so history keeps it |

New Run-only helper next to `userNames` (which is unchanged):

```go
// avatars maps members' nicknames to their avatars, only for members that have one.
// nil when nobody has one, so the users frame omits the field.
func (cr *Room) avatars() map[string]string
```

`encoding/json` sorts map keys, so the frame is deterministic. `dmFrame`, `dmErrorFrame`,
`typingFrame` and `typingDMFrame` are untouched.
**Alternatives considered**: (a) An always-present `"avatar":""` and `"avatars":{}`. That changes
every frame and every exact-shape test, with no benefit to old clients. (b) A separate `avatars`
frame after `users`. That is one more private frame (`privateFrames` and slow-client tests
shift) and splits one snapshot into two.
**Rationale**: Clients that ignore the field keep working. Frames from users without a picture
stay unchanged, so the strict helpers keep guarding CHAT-17/18 for the common case.

### Decision: The page keeps a per-room `avatars` Map and one rendering helper

**Choice** (`app.js`):

```js
const MAX_AVATAR_BYTES = 512; // maxAvatarBytes in cmd/chatroom/main.go
const AVATAR_KEY = 'chadroom.avatar'; // localStorage; best effort
let requestedAvatar = ''; // validated join value, re-sent by every room's socket

function validAvatarInput(v)   // v === '' || (isAllowedImage(v) && new TextEncoder().encode(v).length <= MAX_AVATAR_BYTES)
function letterAvatar(name)    // existing div.avatar code moved out of addChat (aria-hidden, first letter)
function avatarEl(name, url)   // url string passing isAllowedImage → img.avatar (alt '', aria-hidden, referrerPolicy
                               //   'no-referrer', loading 'lazy', decoding 'async', src = url,
                               //   onerror → img.replaceWith(letterAvatar(name))); else letterAvatar(name)
function renderProfile()       // url = active ? active.avatars.get(active.myName) : undefined; updates #profile-avatar
```

| Place | Change |
|-------|--------|
| `createRoom` state | `avatars: new Map()` next to `users` |
| `joinRoom`, `teardown` | `room.avatars = new Map()` next to `room.users = new Set()` |
| `users` frame | after `room.users`: `room.avatars = new Map()`. If `msg.avatars` is a non-array object, keep the entries whose key is in `room.users` and whose value is a string passing `isAllowedImage`. Then, if active, `renderUsers(); renderProfile()` |
| `join` frame | `typeof msg.avatar === 'string' && isAllowedImage(msg.avatar)` ? `set` : `delete` (a reused nickname must not inherit a picture) |
| `leave` frame | `room.avatars.delete(msg.from)` |
| `chat` frame / `renderHistory` | `addChat(room, msg.from, msg.text, msg.ts, msg.avatar)`, so the frame's own avatar is used and history works after the author left |
| `dm` frame | `addChat(conv, msg.from, msg.text, msg.ts, room.avatars.get(msg.from))` (DMs only reach online members; own echoes resolve `myName`) |
| `addChat(pane, author, text, ts, avatar)` | `slot.append(avatarEl(author, avatar))` replaces the inline letter block |
| `renderUsers` | each `li` starts with `avatarEl(name, room.avatars.get(name))` |
| `activate` | calls `renderProfile()` |
| `connect` | appends `'&avatar=' + encodeURIComponent(requestedAvatar)` only when non-empty |
| join submit | `const avatar = avatarInput.value.trim()`. If `!validAvatarInput(avatar)` → `#join-error` "La foto tiene que ser un link https a una imagen (.gif, .png, .jpg o .webp) de Giphy, Tenor o Imgur.", `aria-invalid="true"`, focus the field, return (before the rooms wait). On success: `try { avatar ? localStorage.setItem(AVATAR_KEY, avatar) : localStorage.removeItem(AVATAR_KEY) } catch {}` and `enterChat(nick, avatar)` sets `requestedAvatar` |
| startup | `try { const v = localStorage.getItem(AVATAR_KEY); if (typeof v === 'string') avatarInput.value = v; } catch {}` |
| `#avatar-url` `input` | clears `aria-invalid` and hides `#join-error` |

`renderProfile` keeps a module-level `profileUrl` and only touches `#profile-avatar` when the URL
changes. It sets `hidden = !url` and assigns `src`. Its `onerror` sets `hidden = true`, so the
`♡` placeholder shows again, and a re-render with the same URL doesn't retry. Only `src`,
`textContent` and properties are used: no `innerHTML`, no CSS `url()`, no inline attributes.
**Alternatives considered**: (a) Using `room.avatars` for room chat too. History from authors who
already left would lose their picture. (b) Reading the profile from `welcome.avatar`. That is
equivalent (the `users` frame follows immediately), but the Map is the single source the list
and DMs already use. `welcome.avatar` stays for other clients. (c) Shrinking the avatar via a
modifier class in `addChat`. The list needs a smaller size, and a descendant rule does that
without new options.
**Rationale**: One helper covers both call sites (real duplication: letter in chat and in the
list). The `onerror` fallback matches the existing GIF bubble. Re-checking with `isAllowedImage`
keeps the page safe against a misbehaving server.

### Decision: Markup and CSS reuse `.field` and `.avatar`

**Choice** (`index.html`, a second `.field` after the `#nick` one):

```html
<div class="field">
  <label for="avatar-url">Foto de perfil (opcional)</label>
  <!-- maxlength mirrors maxAvatarBytes in cmd/chatroom/main.go -->
  <input id="avatar-url" class="input" type="url" maxlength="512" placeholder="https://i.imgur.com/….png" aria-describedby="avatar-hint">
  <p id="avatar-hint" class="field-hint text-muted">Pegá el link de una imagen de Giphy, Tenor o Imgur.</p>
</div>
```

The `.profile-art` card gets `<img id="profile-avatar" class="profile-avatar" alt="" hidden>` as its
first child (the card is already `aria-hidden`).

```css
.field-hint { margin: 0; font-size: 12px; }
img.avatar { object-fit: cover; }
.users-list .avatar { width: 20px; height: 20px; flex: none; font-size: 11px; }
.profile-avatar { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: cover; }
```

`.users-list .avatar` (0,2,0) beats the pinkroom `.avatar{height:38px…}` (0,1,0) wherever it sits.
`.profile-art` is already `position: relative` and its `✧` span comes later in the DOM, so it stays
on top of the picture. `[hidden]` (app.css:42) already hides the `<img>`.
**Alternatives considered**: An `<img>` inside the existing `div.avatar`. That gives a nested
circle plus a letter to toggle, where a single element is simpler to swap.
**Rationale**: It uses only existing tokens and adds nothing inline. `type="url"` gives mobile keyboards
the right layout, and `maxlength` counts UTF-16 units (≤ bytes), so the JS byte check is the real limit.

## Data Flow

```
Page (join)            HandleWebSocket (gin goroutine)        Room.Run                 Others
 │ validAvatarInput ok
 │ localStorage.set
 │ GET /ws?room=general&nickname=Lu&avatar=https%3A%2F%2Fi.imgur.com%2Fa.png
 │──────────────────────▶ resolve room (404?) → upgrade (403?)
 │                        avatar = c.Query("avatar")
 │                        validAvatar(avatar)? no → ""   (never rejects)
 │                        serveConn(conn, normalizeNickname(nick), avatar)
 │                          Client{…, avatar} ──register──▶ admit: assign nickname
 │◀── welcome {type,from,text,ts,avatar} ◀──────────────── │
 │◀── users {users,[avatars],ts} ◀──────────────────────── │ avatars(): nick→url, nil if none
 │◀── history {messages:[{…,avatar?}]} ◀────────────────── │
 │                                                          │── join {…,avatar} ──────▶ avatars.set
 │ send "hola" ──────────────────────────────────────────▶ relay: message{…, Avatar: from.avatar}
 │                                                          │   remember(m) + publish ─▶ addChat(…, msg.avatar)
 │ (socket drops) → reconnect: same URL, same avatar        │── leave {type,from,text,ts} ▶ avatars.delete
 │ DM from X ◀── dm {from,to,text,ts} (no avatar) ── page: room.avatars.get(X)
```

## Testing

Strict helpers (`decode`, `frameFields`, `decodeUsers`, `decodeHistory`) stay as they are. They now
also prove that the avatar field is absent for users without a picture. Frames that carry one are
checked with `exactFields`.

**`internal/chat/room_test.go`** (hub; avatars are opaque strings such as `"pic-a"`, set with
`c.avatar = …` after `newTestClient`):

| Test | Asserts |
|------|---------|
| `TestAvatarOnWelcomeAndJoin` | `a` with an avatar: its welcome has exactly `type,from,text,ts,avatar`, avatar `pic-a`. The join that `obs` sees has the same shape. `b` without one: welcome/join pass `decode` (4 fields) |
| `TestUsersFrameAvatars` | Newcomer `c` with `a` (pic) and `b` (none) online: exactly `type,users,avatars,ts`, and `avatars == {"a":"pic-a"}` (the newcomer's own entry is included if it has one). Nobody has one: `decodeUsers` passes (3 fields) |
| `TestChatAndHistoryKeepAvatar` | Chat from `a` carries `avatar`, chat from `b` doesn't. After `a` leaves, a late joiner's history entry for `a` still has `avatar: pic-a` (`exactFields` on the raw entry) |
| `TestAvatarNotOnLeaveDMTyping` | Sender with an avatar: leave passes `decode`; dm `exactFields(type,from,to,text,ts)`; dm_error and typing (room/DM) keep their current exact shapes |
| `TestReusedNicknameNoStaleAvatar` | `bob` (pic) leaves, a new `bob` without one joins: its users frame has no `avatars`, and its join passes `decode` |

**`internal/chat/server_test.go`** (integration; new helper `startAvatarRooms(t, valid func(string) bool, rooms...)`
that `startRooms` delegates to with `nil`, and `dialAvatar(t, srv, nick, avatar)` using `url.QueryEscape`):

| Test | Asserts |
|------|---------|
| `TestAvatarQueryParam` | Validator `func(s string) bool { return s == good }` where `good` contains `?`, `&` and `%`, so escaping round-trips. Good → welcome `avatar == good`. `"bad"` → connection accepted, welcome passes `decode`. Missing param → validator not called (recorded calls) |
| `TestAvatarDisabledWithoutValidator` | `startRooms` (nil) + `?avatar=good` → welcome without avatar |

**`cmd/chatroom/main_test.go`**:

| Test | Asserts |
|------|---------|
| `TestValidAvatar` | Accept: each `imageHosts` host with `.png`, `.JPG`, `.jpeg`, `.webp`, `.gif`, and with a query. Reject: `""`, `http://`, a relative `/a.png`, `i.imgur.com:443`, `i.imgur.com:`, `I.IMGUR.COM`, `user@i.imgur.com`, `evil.com\@i.imgur.com`, `evil.com`, `.mp4`, `.png#x`, `.png#`, a space, a tab, `/a%2Epng`, `javascript:`, opaque `https:i.imgur.com/a.png`, 512 bytes accepted and 513 rejected |
| `TestWebSocketAvatarWiring` | `newTestServer` + gorilla dial `/ws?nickname=A&avatar=<valid imgur>` → welcome `avatar` equals it. With `evil.com` → no `avatar` key (proves `main` injects `validAvatar`) |
| `TestIndexHasAvatarField` | `index.html` has `id="avatar-url"`, `type="url"`, `maxlength="512"` and `id="profile-avatar"`, and `#avatar-url` comes after `id="nick"` |
| existing | `TestIndexHasNoInlineCode`, CSP string, and `TestImageHostsMirroredInJS` are unchanged and green |

**Existing tests that change (compile fixes only)**: `NewServer(...)` gets `nil` as its second
argument at `client_test.go:32, :425` and `server_test.go:44, :158, :209`. `server_test.go:91, :95`
`message{typeChat, "B", "x", 0}` → keyed `message{Type: typeChat, From: "B", Text: "x"}`.

Gates: `/snap/go/current/bin/gofmt -l .`, `go vet ./...`, `go test -race ./...`.

## Manual UI checklist

1. On the join form, an empty photo field → joins as today, and the letter avatars are unchanged.
2. A valid Imgur/Giphy/Tenor URL → your messages, the online list and the "MI PERFIL" card show the
   picture, in both skins.
3. An invalid URL (`http://`, `evil.com`, `.mp4`) → inline error, no connection. Typing in the
   field clears the error.
4. Reload the page → the field is pre-filled. Clear it and join → the next reload is empty.
   Private window / storage blocked → still joins.
5. A second browser without a photo sees your picture on your messages, in history after you leave,
   in the online list and on your DMs. Your view shows its letter.
6. A URL on an allowed host that 404s → the letter shows in chat and the list, and the `♡` shows in the card.
7. Leave and join a second room → the picture is sent there too. Kill the server and restart it → it
   comes back after the reconnect.
8. Same nickname reused by someone without a picture → no stale picture in the list or on their join.
9. Devtools: no CSP violations, avatar requests carry no `Referer`, and the `/ws` URL includes
   `avatar=` only when one is set.
10. 480 px and ≤800 px: the list avatars stay 20 px and the card is hidden as before.

## File Changes

| File | Action | Description |
|------|--------|-------------|
| `internal/chat/server.go` | Modify | `validAvatar` field and `NewServer` parameter; `HandleWebSocket` reads/filters `?avatar` and passes it to `serveConn` |
| `internal/chat/client.go` | Modify | `Client.avatar` field + comment |
| `internal/chat/room.go` | Modify | `serveConn(conn, nickname, avatar)`; keyed literals with `Avatar` in welcome/join/relay; `avatars()` helper; users frame `Avatars` |
| `internal/chat/message.go` | Modify | `message.Avatar` (omitempty, last), `usersFrame.Avatars` (omitempty), updated doc comments |
| `internal/chat/room_test.go` | Modify | New hub tests above |
| `internal/chat/server_test.go` | Modify | `nil` call sites; keyed literals; `startAvatarRooms`, `dialAvatar`; integration tests |
| `internal/chat/client_test.go` | Modify | `startRooms` delegates to `startAvatarRooms(t, nil, …)`; `NewServer` call at :425 |
| `cmd/chatroom/main.go` | Modify | `maxAvatarBytes`, `avatarPath`, `validAvatar`; `newChatServer` passes it |
| `cmd/chatroom/main_test.go` | Modify | `TestValidAvatar`, `TestWebSocketAvatarWiring`, `TestIndexHasAvatarField` |
| `cmd/chatroom/index.html` | Modify | `#avatar-url` field with hint; `#profile-avatar` in `.profile-art` |
| `cmd/chatroom/app.js` | Modify | Constants, `requestedAvatar`, `room.avatars`, `validAvatarInput`, `letterAvatar`, `avatarEl`, `renderProfile`; hooks in join/connect/frames/`addChat`/`renderUsers`/`activate` |
| `cmd/chatroom/app.css` | Modify | `.field-hint`, `img.avatar`, `.users-list .avatar`, `.profile-avatar` |
| `README.md` | Modify | `?avatar=` on `/ws`; `avatar` / `avatars` fields in the protocol table |
| `openspec/specs/{chat,server}/spec.md` | Modify at archive | Avatar requirement; CHAT-17/18 shapes; `/ws` query param |

## Open Risks

- **Automatic third-party fetches.** Every viewer loads every avatar (IP and timing visible to
  Giphy/Tenor/Imgur). The hosts are the same ones already trusted for GIFs, with `no-referrer`.
  A proxy is out of scope.
- **Access logs.** gin's logger prints `/ws?…&avatar=…` (the nickname is already there). It's
  public data, accepted.
- **Mutable content.** An allowed URL can later serve a different image, or a huge or animated
  one. CSS fixes the rendered size, but not the bytes downloaded.
- **Frame growth.** History adds up to 512 B per entry (≤ ~25 KiB per room), and `users` adds
  ≤ 512 B per member. That's outbound only, so the 4096-byte read limit doesn't apply.
- **First `localStorage` use.** It's best effort, always wrapped in `try`. A stored value is
  re-validated on submit, so a tampered entry can't bypass the check.
- **No JS tests.** The Map upkeep, fallback and profile rendering rely on the manual checklist.
