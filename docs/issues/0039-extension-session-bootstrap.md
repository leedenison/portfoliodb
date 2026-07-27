---
status: open
title: Extension session bootstrap and API clients
milestone: M12
dependencies: [0037, 0038]
---

Content script on the PortfolioDB origin that calls GetSession and passes the
session id to the service worker, which stores it and sends it as
`Authorization: Bearer`. Re-bootstrap by prompting the user to open a PortfolioDB
tab when a call returns UNAUTHENTICATED.

Service worker clients for ListTxs, UpsertTxs and GetJob over the shared gRPC-Web
transport. Verify that the MV3 host_permissions CORS exemption covers the bearer
header; if it does not, add `authorization` to the Envoy `allow_headers`.
