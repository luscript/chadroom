# Server Specification

## Purpose

HTTP surface of the chatroom: the WebSocket endpoint's origin policy, serving the web
client, and process lifecycle (startup errors and graceful shutdown).

## Requirements

### Requirement: SRV-01 Origin check on WebSocket upgrade

The system MUST reject WebSocket upgrade requests on `GET /ws` whose `Origin` header is
present and is neither the same origin as the request host nor listed in the configured
allowlist. Rejected requests MUST receive HTTP 403 and MUST NOT be upgraded. The `null`
origin MUST NOT be accepted by default. Requests without an `Origin` header (non-browser
clients) MAY be upgraded, since CSWSH requires a browser, which always sends `Origin`.

The allowlist SHALL be read from the `ALLOWED_ORIGINS` environment variable as a
comma-separated list of exact origins (`scheme://host[:port]`); surrounding whitespace and
empty entries SHALL be ignored.

#### Scenario: Same-origin browser client

- GIVEN the server runs at `localhost:8080`
- WHEN a client upgrades `/ws` with `Origin: http://localhost:8080`
- THEN the upgrade succeeds

#### Scenario: Foreign origin rejected

- GIVEN `ALLOWED_ORIGINS` is unset
- WHEN a client upgrades `/ws` with `Origin: https://evil.example`
- THEN the response is 403 and no WebSocket is established

#### Scenario: Allowlisted origin

- GIVEN `ALLOWED_ORIGINS="http://localhost:3000, https://chat.example.com"`
- WHEN a client upgrades `/ws` with `Origin: http://localhost:3000`
- THEN the upgrade succeeds

#### Scenario: Null origin rejected by default

- GIVEN `ALLOWED_ORIGINS` is unset
- WHEN a client upgrades `/ws` with `Origin: null`
- THEN the response is 403

#### Scenario: Non-browser client without Origin

- GIVEN any configuration
- WHEN a client upgrades `/ws` without an `Origin` header
- THEN the upgrade succeeds

### Requirement: SRV-02 Serve the web client

The system MUST serve the chat page (`index.html`) at `GET /`, so that a browser opening
the server's root URL can connect to `/ws` without any `ALLOWED_ORIGINS` configuration.

#### Scenario: Open the chat page

- GIVEN the server is running at `localhost:8080`
- WHEN a browser requests `GET http://localhost:8080/`
- THEN it receives 200 with the chat page (`text/html`)
- AND the page's WebSocket connection to `/ws` succeeds

### Requirement: SRV-03 Graceful shutdown

On SIGINT or SIGTERM the server MUST stop accepting new connections, close all active
WebSocket clients and exit within a bounded shutdown timeout.

#### Scenario: SIGTERM with connected clients

- GIVEN the server has connected WebSocket clients
- WHEN the process receives SIGTERM
- THEN all WebSocket connections are closed
- AND the process exits with status 0 within the shutdown timeout

### Requirement: SRV-04 Startup errors are reported

If the HTTP server fails to start or stops with an error other than a requested shutdown,
the process MUST log the error and exit with a non-zero status.

#### Scenario: Port already in use

- GIVEN port 8080 is already bound by another process
- WHEN the server starts
- THEN it logs the listen error
- AND exits with a non-zero status

### Requirement: SRV-05 HTTP hardening

The HTTP server MUST set `ReadHeaderTimeout`, `IdleTimeout` and `MaxHeaderBytes`, MUST NOT
trust proxy forwarding headers by default, and `GET /` MUST send
`X-Content-Type-Options: nosniff` and `X-Frame-Options: DENY`.

#### Scenario: Security headers on the page

- GIVEN the server is running
- WHEN a client requests `GET /`
- THEN the response includes `X-Content-Type-Options: nosniff` and `X-Frame-Options: DENY`
