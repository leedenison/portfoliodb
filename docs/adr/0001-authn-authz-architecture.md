# Authentication and authorization architecture

PortfolioDB authenticates browser SPA users with Google Sign-In but uses the
Google ID token only as a one-time bootstrap credential: the backend verifies it
once, then issues its own random, opaque session id stored server-side in Redis
and carried by an HttpOnly cookie. We chose an opaque server-side session over a
long-lived JWT so sessions can be revoked immediately (logout deletes the record)
and no sensitive token is exposed to JavaScript; the SPA therefore needs no
refresh-token handling.

Authorization is enforced in the Go backend per-RPC via gRPC interceptors, not at
the Envoy edge. We deliberately do **not** use Envoy JWT/Auth filters or external
authz (ext_authz), and the backend never trusts identity headers injected by
Envoy. Keeping all auth decisions in one place inside the trust boundary avoids a
split-brain policy across two systems and a class of header-spoofing bugs.

Auth is Google-only (single provider). Multi-provider auth was a non-goal for the
initial milestones; adding one later is a localized change to the bootstrap
verification step.
