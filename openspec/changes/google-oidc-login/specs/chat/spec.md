# Delta for Chat

Base: `openspec/specs/chat/spec.md` plus the not-yet-archived `message-replies` (CHAT-35/36) and
`user-profiles` (CHAT-37..40) deltas. New requirements start at CHAT-41.

## MODIFIED Requirements

### Requirement: CHAT-16 Page reconnects automatically

(Requirement text unchanged, except:)

- "requesting the nickname the user originally typed" is replaced by "with the same session
  cookie; the nickname is derived again by the server (CHAT-41)".
- "After the user clicks "Salir"" becomes "After the user clicks "Cerrar sesión"".
- Added: after **3 consecutive** failed reconnect attempts of a room (closed before `welcome`),
  the page MUST check the session by requesting `GET /rooms`; if it answers 401 the page MUST stop
  every retry and navigate to `/` (the login page). Any other answer keeps retrying as before.

#### Scenario: Session expired while reconnecting

- GIVEN the user is in the chat and the session cookie has expired
- WHEN the room connection drops and 3 reconnect attempts fail
- THEN the page requests `/rooms`, receives 401 and navigates to `/`

### Requirement: CHAT-20 Chat page joins several rooms

(Requirement text unchanged, except:)

- "After the user enters a nickname" becomes "When the page loads".
- "open one connection to `/ws` for that room with the same requested nickname" becomes
  "open one connection to `/ws` for that room".
- "on "Salir", close every connection and return to the nickname screen" becomes
  "on "Cerrar sesión", follow CHAT-42".
- "the page MUST stay on the nickname screen and show an error message instead of entering the
  chat" becomes "the page MUST show an error message instead of entering the chat; a 401 MUST
  navigate to `/`" (the server then serves the login page).

There is no nickname screen and no nickname input.

## ADDED Requirements

### Requirement: CHAT-41 Nickname derived from the session

The nickname requested for a WebSocket connection MUST be derived by the server from the
authenticated session (SRV-15), never from client input:

1. the session's display name (`name` claim), if it is non-empty after normalization;
2. otherwise the local part of the session email (before the last `@`).

The derived value is the "requested nickname" for normalization (CHAT-09) and uniqueness
(CHAT-12): two connections of the same account in one room, or two people with the same name,
coexist under suffixed nicknames. The `nickname` query parameter MUST be ignored. Avatar and bio
(CHAT-28, CHAT-37) keep their existing rules.

#### Scenario: Name from the session

- GIVEN a session for `ana.perez@crombie.dev` with name `Ana Pérez`
- WHEN it connects to `/ws?room=general&nickname=Boss`
- THEN its `welcome` `from` is `Ana Pérez`

#### Scenario: Fallback to the email local part

- GIVEN a session for `ana.perez@crombie.dev` with an empty name
- WHEN it connects
- THEN its nickname is `ana.perez`

#### Scenario: Same account in two tabs

- GIVEN `Ana Pérez` is connected to `general`
- WHEN the same session connects to `general` again
- THEN the new connection is assigned `Ana Pérez (2)` and both stay connected

### Requirement: CHAT-42 Chat page session handling

The page MUST NOT ask for a nickname: on load it fetches the room list and enters the chat
(CHAT-20). The header's "Salir" button is replaced by "Cerrar sesión", which MUST:

1. send `POST /auth/logout` (same origin);
2. leave the page on success (which closes every connection and pending retry);
3. on a successful response, navigate to `/` (which then shows the login page); otherwise re-enable the button and
   show a system notice ("No se pudo cerrar sesión. Probá de nuevo.") — the page MUST NOT claim the
   user is signed out when the server did not confirm it.

A 401 response from `/rooms`, `/gifs` or `/playlist` MUST navigate to `/` instead of
showing a generic error. The page MUST NOT read, store or send any session data itself (the
cookie is `HttpOnly`).

#### Scenario: Sign out

- GIVEN the user is in the chat
- WHEN they click "Cerrar sesión"
- THEN every room connection closes, `/auth/logout` is posted and the browser shows the
  login page

#### Scenario: Expired session on GIF search

- GIVEN the session has expired
- WHEN the user searches GIFs and `/gifs` responds 401
- THEN the page navigates to `/` and the login page is shown
