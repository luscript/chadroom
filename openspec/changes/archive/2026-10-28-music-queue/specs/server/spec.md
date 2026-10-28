# Delta for Server

## MODIFIED Requirements

### Requirement: SRV-05 HTTP hardening

(Requirement text unchanged. The Content-Security-Policy is unchanged. Scenarios unchanged
except the last one, which becomes:)

#### Scenario: Page works under the policy

- GIVEN the page is opened in a current Chrome or Firefox with the developer console open
- AND the server runs with `GIPHY_API_KEY` set
- WHEN the user joins, sends messages, opens the emoji and GIF panels, searches for a GIF,
  sends one, opens `musica`, puts a YouTube link (and presses "Escuchar" only if the blocked
  warning appears), queues a second link, removes it, queues another, skips, pauses, seeks and
  leaves
- THEN everything renders styled and works
- AND the console reports no Content-Security-Policy violations

(SRV-06 is unchanged: `musica` still has the shared player, now with a queue per CHAT-30.)
