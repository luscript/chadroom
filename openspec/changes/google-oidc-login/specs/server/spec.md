# Delta for Server

Base: `openspec/specs/server/spec.md` (SRV-01..09). New requirements start at SRV-10.

## MODIFIED Requirements

### Requirement: SRV-02 Serve the web client

(Requirement text unchanged, except the following, which is added after the first paragraph.)

`GET /` and `GET /app.js` MUST require a valid session (SRV-15). `GET /app.css`,
`GET /favicon.svg`, `GET /favicon-unread.svg` and the auth pages (SRV-16) MUST stay public, since
the auth pages use them. Scenarios that open the page or load `/app.js` assume a valid session
cookie.

#### Scenario: Page without a session

- GIVEN a browser without a session cookie
- WHEN it requests `GET /`
- THEN the response is 302 to `/auth/login?next=%2F`
- AND the chat page is not sent

#### Scenario: Public stylesheet

- GIVEN a client without a session cookie
- WHEN it requests `GET /app.css` or `GET /favicon.svg`
- THEN the response is 200 with the usual media type and headers

### Requirement: SRV-06 Room selection on the WebSocket endpoint

(Requirement text unchanged, except the following.)

The `nickname` query parameter is no longer read: the nickname comes from the session
(CHAT-41). The session check (SRV-15) MUST happen before room selection and before the origin
check; a request without a valid session MUST receive 401 regardless of `room` or `Origin`.
Existing scenarios assume a valid session cookie; their `nickname=` parameters are ignored.

#### Scenario: Unknown room without a session

- GIVEN a client without a session cookie
- WHEN it requests `/ws?room=secret`
- THEN the response is 401 (not 404) and no WebSocket is established

## ADDED Requirements

### Requirement: SRV-10 Authentication configuration

At startup the server MUST read:

| Variable | Default | Rule |
|----------|---------|------|
| `GOOGLE_CLIENT_ID` | — | required, non-empty after trimming |
| `GOOGLE_CLIENT_SECRET` | — | required, non-empty after trimming |
| `OIDC_REDIRECT_URL` | `http://localhost:8080/auth/callback` | absolute URL with a host and path `/auth/callback`; `https`, or `http` only for a loopback host (`localhost`, `127.0.0.0/8`, `::1`) |
| `ALLOWED_EMAIL_DOMAIN` | `crombie.dev` | a bare ASCII domain (no `@`), compared case-insensitively |
| `SESSION_SECRET` | — | required, at least 32 bytes |
| `SESSION_TTL` | `12h` | Go duration, between 5 minutes and 7 days |

If any rule fails the process MUST log an error naming the variable (never its value) and exit
with a non-zero status before listening. The server MUST NOT run without authentication and MUST
NOT generate a session secret on its own. Cookies are `Secure` exactly when `OIDC_REDIRECT_URL`
uses `https`.

#### Scenario: Missing session secret

- GIVEN `SESSION_SECRET` is unset
- WHEN the server starts
- THEN it logs an error naming `SESSION_SECRET`
- AND exits with a non-zero status without listening

#### Scenario: Short session secret

- GIVEN `SESSION_SECRET` is 16 bytes long
- WHEN the server starts
- THEN it exits with a non-zero status and the log does not contain the secret

#### Scenario: Plain http outside loopback rejected

- GIVEN `OIDC_REDIRECT_URL=http://chat.example.com/auth/callback`
- WHEN the server starts
- THEN it exits with a non-zero status naming `OIDC_REDIRECT_URL`

#### Scenario: Defaults

- GIVEN only `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` and a 32-byte `SESSION_SECRET` are set
- WHEN the server starts
- THEN the redirect URL is `http://localhost:8080/auth/callback`, the domain is `crombie.dev`,
  the session lifetime is 12 hours and cookies are not `Secure`

### Requirement: SRV-11 Login flow

`GET /auth/login` MUST start an OpenID Connect authorization-code flow against the issuer
`https://accounts.google.com`, requesting scopes `openid email profile`, with:

- a fresh random `state` and `nonce` (each at least 128 bits from `crypto/rand`);
- PKCE with method `S256` and a fresh random verifier;
- the `hd` parameter set to the allowed domain (a hint only, never trusted);
- `state`, `nonce`, the verifier and the post-login path stored in a signed flow cookie
  (`HttpOnly`, `SameSite=Lax`, `Secure` per SRV-10, scoped to `/auth`, lifetime ≤ 10 minutes).

The post-login path comes from the optional `next` query parameter. It MUST be used only if it
is a relative path of this server: it starts with a single `/`, the second character is
neither `/` nor `\`, it contains no `\`, spaces or control characters, it has no scheme, host or
user info, it is not under `/auth`, and it is at most 512 bytes; otherwise `/` MUST be used. It
MUST be re-validated before the post-login redirect.

When the request carries `switch=1`, the authorization request MUST also set
`prompt=select_account`, so the user can choose another Google account (used by the auth pages).

`GET /auth/callback` MUST:

1. reject (400) when the flow cookie is missing, invalid or expired, when the `state` parameter
   does not match the cookie (constant-time comparison), when the provider returned an `error`
   parameter, or when `code` is missing;
2. exchange the code with the PKCE verifier; failure, or a token response without an
   `id_token`, → 400;
3. verify the ID token with the provider's keys (signature, issuer, audience = client id,
   expiry) and check that its `nonce` equals the cookie's nonce; any failure → 400;
4. apply the authorization rules (SRV-12); failure → 403;
5. on success: delete the flow cookie, set the session cookie (SRV-13) and redirect (302) to the
   stored post-login path.

The flow cookie MUST be deleted on every callback outcome. Access and refresh tokens MUST be
discarded and never stored. Error responses MUST be generic and MUST NOT reveal which check
failed.

#### Scenario: Successful login

- GIVEN a provider that authenticates `ana@crombie.dev` with `email_verified=true` and
  `hd=crombie.dev`
- WHEN the browser completes `/auth/login` → provider → `/auth/callback`
- THEN the response is 302 to `/` with a session cookie
- AND a following `GET /` returns the chat page

#### Scenario: State mismatch

- GIVEN a valid flow cookie
- WHEN `/auth/callback` is called with a different `state`
- THEN the response is 400, no session cookie is set and the code is not exchanged

#### Scenario: Missing flow cookie

- GIVEN no flow cookie
- WHEN `/auth/callback?state=x&code=y` is called
- THEN the response is 400

#### Scenario: Nonce mismatch

- GIVEN the provider returns an ID token whose `nonce` differs from the flow cookie's
- WHEN the callback completes the exchange
- THEN the response is 400 and no session cookie is set

#### Scenario: PKCE is used

- GIVEN `/auth/login` was requested
- THEN the authorization redirect carries `code_challenge_method=S256` and a `code_challenge`
- AND the token request on callback carries the matching `code_verifier`

#### Scenario: Open redirect refused

- GIVEN a user requests `/auth/login?next=//evil.example/x` (or `https://evil.example`,
  `/\evil.example`)
- WHEN the login completes successfully
- THEN the redirect goes to `/`

### Requirement: SRV-12 Domain authorization

The server MUST authorize a verified ID token only if all of these hold:

- the `email_verified` claim is the boolean `true`;
- the `email` claim contains `@`, and the part after the **last** `@` equals
  `ALLOWED_EMAIL_DOMAIN` under ASCII case-insensitive comparison (whole-string equality, not a
  suffix check);
- the `hd` claim equals `ALLOWED_EMAIL_DOMAIN` under the same comparison.

Otherwise the server MUST respond 403 with the access-denied page (SRV-16), MUST NOT set a session
cookie and MUST NOT reveal which rule failed.

#### Scenario: Allowed domain

- GIVEN claims `email=ana@crombie.dev`, `email_verified=true`, `hd=crombie.dev`
- THEN the login succeeds

#### Scenario: Look-alike domains rejected

- GIVEN `email` is `eve@evilcrombie.dev`, `eve@crombie.dev.evil.com` or `eve@gmail.com`
  (with `hd` set to the same domain or absent)
- THEN the response is 403

#### Scenario: Unverified email rejected

- GIVEN `email=ana@crombie.dev`, `email_verified=false`, `hd=crombie.dev`
- THEN the response is 403

#### Scenario: hd mismatch rejected

- GIVEN `email=ana@crombie.dev`, `email_verified=true` and `hd` absent or `other.com`
- THEN the response is 403

### Requirement: SRV-13 Session cookie

A successful login MUST set a session cookie that carries a random session id, the email, the
display name and an absolute expiry (`now + SESSION_TTL`), authenticated with HMAC-SHA256 under
`SESSION_SECRET`. The cookie MUST be `HttpOnly`, `SameSite=Lax`, `Path=/`, `Secure` per SRV-10,
and expire with the session. A cookie MUST be rejected when its encoding is invalid, its MAC
does not verify (constant-time comparison), it has expired, or its session id was revoked
(SRV-14). The session MUST NOT contain OAuth tokens. The flow cookie and the session cookie MUST
use distinct MAC contexts, so neither is accepted as the other.

#### Scenario: Tampered cookie

- GIVEN a valid session cookie with one byte of its payload or MAC changed
- WHEN it is sent to `GET /rooms`
- THEN the response is 401

#### Scenario: Expired cookie

- GIVEN a session cookie whose expiry is in the past
- WHEN it is sent to `GET /`
- THEN the response is 302 to `/auth/login?next=%2F`

#### Scenario: Flow cookie is not a session

- GIVEN the value of a valid flow cookie
- WHEN it is sent as the session cookie
- THEN it is rejected

### Requirement: SRV-14 Logout

`POST /auth/logout` MUST revoke the request's session id (in memory, until the session would
have expired), MUST clear the session cookie (`Max-Age` ≤ 0, same name, path and flags) and MUST
respond 204. It MUST respond 204 also when there is no valid session. Requests with an `Origin`
header that is `null`, unparsable, or whose host differs from the request host MUST receive 403
and change nothing. Only `POST` is accepted (no CSRF token: `SameSite=Lax` plus the Origin check). Revocation is
lost on restart (sessions remain stateless otherwise).

#### Scenario: Logout invalidates the session

- GIVEN a valid session cookie `C`
- WHEN the client posts `/auth/logout` with `C`
- THEN the response is 204 and clears the cookie
- AND a later request to `/rooms` with `C` receives 401

#### Scenario: Cross-site logout refused

- GIVEN a valid session cookie
- WHEN `/auth/logout` is posted with `Origin: https://evil.example`
- THEN the response is 403 and the session stays valid

### Requirement: SRV-15 Protected routes

Every route except `/auth/*`, `/app.css`, `/favicon.svg` and `/favicon-unread.svg` MUST require a
valid session (SRV-13). Without one:

- `GET /` MUST respond 302 to `/auth/login?next=<escaped request path>` (path only, query dropped);
- `GET /app.js`, `GET /rooms`, `GET /gifs`, `GET /playlist` and `GET /ws` MUST respond 401 with
  `{"error":"unauthorized"}` and `Cache-Control: no-store`; `/ws` MUST NOT be upgraded and MUST
  NOT reveal whether the room exists.

Session data MUST be read only from the session cookie, never from the query string or other
headers. The authenticated identity (email, name) MUST be made available to handlers through
the request context.

#### Scenario: WebSocket without a session

- GIVEN no session cookie
- WHEN a client upgrades `/ws?room=general`
- THEN the response is 401 and no WebSocket is established

#### Scenario: APIs without a session

- GIVEN no session cookie
- WHEN a client requests `/rooms`, `/gifs` (with a Giphy key configured) or `/playlist`
  (with a YouTube key configured)
- THEN each response is 401 and the upstream API is not contacted

#### Scenario: WebSocket with a session

- GIVEN a valid session cookie and `Origin: http://localhost:8080`
- WHEN the client upgrades `/ws?room=juegos`
- THEN the upgrade succeeds

### Requirement: SRV-16 Auth pages

The server MUST serve two embedded static pages, without inline script or style and with the
same security headers as `GET /` (SRV-05):

- an **access denied** page, sent with status 403 when authorization fails (SRV-12), saying only
  that the account is not allowed and offering a link to `/auth/login?switch=1`;
- a **signed out** page at `GET /auth/signed-out` (200), offering a link to `/auth/login?switch=1`.

Pages for 400 callback errors MAY reuse the access-denied wording. These pages MUST NOT contain
the email, name or any claim.

#### Scenario: Denied page

- GIVEN a login with `eve@gmail.com`
- THEN the 403 response is the access-denied page with `Content-Security-Policy` and no inline
  script

### Requirement: SRV-17 Login rate limit

`GET /auth/login` and `GET /auth/callback` MUST be rate limited per client IP (remote address;
proxy headers are not trusted, SRV-05; IPv6 clients are keyed by their `/64` prefix). A client exceeding the limit (default: 20 requests per
minute, shared by both routes) MUST receive 429 and MUST NOT get a redirect to the provider or a
token exchange. Limiter state MUST be bounded in memory (entries for idle IPs are dropped).

#### Scenario: Burst of logins

- GIVEN a client that already made 20 login requests in the current minute
- WHEN it requests `/auth/login` again
- THEN the response is 429

### Requirement: SRV-18 Secrets stay out of logs and responses

The client secret, `SESSION_SECRET`, cookie values, authorization codes, `state`, `nonce`, PKCE
verifiers and tokens MUST NOT appear in any log line or response body. Errors from the token
exchange or verification MUST be logged, if at all, as a fixed message without the underlying
error text. The access log MUST keep omitting query strings. Panic recovery MUST NOT dump request
headers (gin's default recovery masks only `Authorization` and would log the `Cookie` header), so
the server MUST use its own recovery middleware that logs only the panic value and route.

#### Scenario: Failed exchange is not logged verbatim

- GIVEN the token endpoint responds 400 with a body containing the code
- WHEN the callback fails
- THEN no log line contains the code or the response body
