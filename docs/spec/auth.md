# Auth, Session Bootstrap, and Authorization

## Goals

- Support browser-based login from a statically served SPA using Google Sign-In.
- Use Google ID token only as a bootstrap credential (one-time exchange).
- After bootstrap, rely on an application-managed session carried by an HttpOnly cookie.
- Enforce authorization (authz) in the backend per-RPC using gRPC interceptors.
- Restrict account creation (CreateUser) to users whose verified email matches an allowlist pattern.

## Non-goals

- Do not use Envoy JWT/Auth filters or external authz (ext_authz) at the edge.
- No refresh token handling on the SPA.
- No multi-provider auth (Google-only).

## High-level Architecture

```
SPA (browser) --gRPC-Web--> Envoy (gRPC-Web + CORS + cookie pass-through) --> gRPC backend
                                         |
                                         +-- serves static SPA assets
```

1. SPA obtains a Google ID token via Google Sign-In.
2. SPA calls backend **AuthService.Auth** with the ID token to bootstrap a session.
3. Backend verifies the ID token and creates an application session.
4. Backend sets an HttpOnly session cookie on the response.
5. Subsequent SPA gRPC-Web calls include the cookie (credentials included) and are authenticated server-side.

## Session Model

### Cookie-based session (opaque)

- Backend issues a random, opaque `session_id` stored server-side (Redis).
- **Cookie name:** `portfoliodb_session` (configurable).

**Cookie flags:**

- **HttpOnly:** `true`
- **Secure:** `true` (required in production)
- **SameSite:** Lax by default (see CORS/Origin section); may need `None` if cross-site.
- **Path:** `/`
- **Max-Age:** configurable, default 30d

**Session storage fields:**

- `session_id` (random 32+ bytes, base64url)
- `user_id` (internal UUID)
- `email` (string)
- `created_at`, `expires_at`, `last_seen_at`
- `google_sub` (string; stable Google user id)
- optional: `user_agent_hash`, `ip_prefix` (for basic binding; optional)

### Session renewal

- Sliding session is extended for 72 hrs on any request.

### Logout

- Logout deletes server-side session record and clears cookie.

## Google ID Token Verification

### Inputs

- The SPA passes `google_id_token` (JWT string) to **Auth**.
- Backend must validate:
  - Signature using Google public keys (JWKs).
  - Standard claims:
    - `iss` is one of Google issuers.
    - `aud` matches configured `GOOGLE_OAUTH_CLIENT_ID`.
    - `exp` in future; `iat` reasonable.
    - `email` exists and `email_verified == true`.
  - Extract `sub` (Google subject identifier) for stable identity binding.

### Configuration

- **GOOGLE_OAUTH_CLIENT_ID:** required.
- **GOOGLE_JWKS_CACHE_TTL:** default 1h.
- **GOOGLE_TOKEN_CLOCK_SKEW:** default 2m.

### Failure behavior

- Invalid token ⇒ **Unauthenticated**.
- Unverified email ⇒ **PermissionDenied**.
- Wrong audience/issuer ⇒ **Unauthenticated**.

## RPC API (Protobuf)

Create a new service (or extend an existing auth service):

### AuthService

**Bootstrap session**

- **RPC:** `Auth(AuthRequest) returns (AuthResponse)`
- **Request:** `string google_id_token`
- **Response:**
  - `User user` (internal user info, may be newly provisioned or existing)
  - `bool user_exists` (whether internal user already existed)
- **Side effects:**
  - Sets `Set-Cookie: portfoliodb_session=...` on the HTTP response (via gRPC-Web compatible headers).
  - Creates or updates internal user record (see “User provisioning”).

**Logout**

- **RPC:** `Logout(google.protobuf.Empty) returns (google.protobuf.Empty)`
- **Side effects:**
  - Deletes session server-side
  - Clears cookie

**Notes:** gRPC does not natively define cookie setting. In Go, implement cookie set/clear using gRPC metadata mapped to HTTP headers through the gRPC-Web / Envoy stack. (Implementation detail: add set-cookie in response headers / trailers as appropriate for gRPC-Web compatibility.)

## User Provisioning

On successful ID token verification:

1. Lookup internal user by `google_sub` first; if found, use it.
2. Otherwise lookup by email (case-insensitive). If found, bind `google_sub` to that user.
3. Otherwise create a new, normal active user.
4. Store: `user_id` (UUID), `email`, `google_sub`, `created_at`.

## Authorization Model

### Identity

- Authenticated identity is derived from a valid session cookie.
- Backend attaches `user_id` (and `email`) to request context for handlers.
- Admin roles are assigned through database configuration or via the ADMIN_AUTH_SUB environment variable.

### Interceptors

Implement two interceptors:

**Authentication interceptor**

- For each incoming RPC:
  - Extract `portfoliodb_session` cookie from incoming request metadata.
  - Validate session exists and not expired.
  - Attach identity to context: `{user_id, email, google_sub}`.
- If missing/invalid session:
  - For public RPCs (BootstrapSession, health checks): allow.
  - Otherwise: return **Unauthenticated**.

**Authorization interceptor**

- Enforce method-level policy:
  - Default policy: authenticated required.
  - Exceptions list: AuthService/Auth, etc.
  - **AuthService/Auth** policy: authenticated **AND** email allowlisted.

*Implementation note:* This can be implemented as one interceptor that performs both authn and authz with a per-method policy map.

### Allowlist rules

- Configurable allowlist as a list of patterns. Support at least one of:
  - Regex patterns, or
  - Glob-like patterns (e.g. `*@example.com`, `lee+*@gmail.com`)
- Matching is performed against the verified email from the authenticated session identity.
- If not matched: return **PermissionDenied** with a generic message.

### Configuration

- **ACCOUNT_CREATE_EMAIL_ALLOWLIST:** list (comma-separated or repeated env var) of patterns.
- **ACCOUNT_CREATE_ALLOWLIST_MODE:** `regex` \| `glob` (default glob).
- **ACCOUNT_CREATE_ALLOWLIST_CASE_SENSITIVE:** default false.

### Example patterns

- `*@mycompany.com`
- `lee.denison@*`
- `*@example.org`

## Envoy / gRPC-Web Requirements (non-auth)

Envoy must:

- Support gRPC-Web translation.
- Forward cookies from browser to backend and allow Set-Cookie back to browser.
- Provide CORS to allow SPA origin to call backend with credentials.

### CORS + credentials

- SPA must send requests with credentials included (implementation-specific on the client).
- Envoy must return:
  - `Access-Control-Allow-Origin: <exact SPA origin>` (not `*` when using credentials)
  - `Access-Control-Allow-Credentials: true`
  - Allowed headers must include gRPC-Web required headers (e.g. x-grpc-web, content-type, etc.)
  - Allow set-cookie exposure as needed (note: cookies are handled by the browser; no need to expose set-cookie to JS)

## Error Codes

| Code | When |
|------|------|
| **Unauthenticated** | Missing/invalid session, invalid ID token. |
| **PermissionDenied** | Email not allowlisted for Auth, email not verified. |
| **InvalidArgument** | Missing google_id_token in request, malformed input. |
| **Internal** | Session store failures, unexpected token verification failure. |

## Testing Requirements

**Unit tests:**

- ID token verification wrapper (mocked JWKs / verifier)
- Allowlist matcher for glob/regex
- Interceptor policy map behavior

**Integration tests (docker-compose):**

- Auth sets cookie
- Subsequent RPC with cookie succeeds
- Subsequent RPC without cookie fails with Unauthenticated
- Auth succeeds only for allowlisted emails
- Logout clears session and blocks further calls

## Security Notes (explicit requirements)

- Session cookies must be **HttpOnly**.
- **Secure** must be enabled in production; allow disabling only in local dev mode.
- Do not store Google ID tokens after bootstrap; do not treat them as long-lived session credentials.
- Reject Google identities with `email_verified == false`.
- All auth decisions must be made in backend (do not trust headers injected by Envoy).
