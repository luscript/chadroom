# Delta for Server

## MODIFIED Requirements

### Requirement: SRV-05 HTTP hardening

(Requirement text unchanged, except one rule is added to the Content-Security-Policy list:)

- MUST allow embedded frames only from `https://www.youtube-nocookie.com` (`frame-src`), for the
  shared player (CHAT-31), and MUST NOT allow any YouTube host in `script-src`.

(Existing scenarios unchanged except the last one, which becomes:)

#### Scenario: Page works under the policy

- GIVEN the page is opened in a current Chrome or Firefox with the developer console open
- AND the server runs with `GIPHY_API_KEY` set
- WHEN the user joins, sends messages, opens the emoji and GIF panels, searches for a GIF,
  sends one, opens `musica`, presses "Escuchar", puts a YouTube link, pauses, seeks and leaves
- THEN everything renders styled and works
- AND the console reports no Content-Security-Policy violations

### Requirement: SRV-06 Room selection on the WebSocket endpoint

(Requirement text unchanged, plus:) The room `musica` MUST have a shared player (CHAT-30); the
rooms `general` and `juegos` MUST NOT.

(Scenarios unchanged.)
