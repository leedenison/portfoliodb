# Browser extension transaction import

The browser extension parses the broker CSV and calls UpsertTxs itself rather than
handing the raw file to the web UI's upload modal. Routing through the modal was
the cheaper-looking option, but the modal refuses to upload when any row fails to
parse and derives the replace period from the minimum and maximum of the parsed
rows -- and the extension needs the opposite of both (upload despite dropped rows,
and replace the window that was requested). Reusing the modal would have meant
changing shared UI semantics for every manual uploader to suit one automated
client, and would have made the extension depend on a page being open and
responsive. The converters themselves are still shared, so parsing logic is not
duplicated.

The extension authenticates with the existing opaque session carried as
`Authorization: Bearer <session_id>` rather than the session cookie. A service
worker request to the PortfolioDB origin is cross-site, so the SameSite cookie is
not attached and the extension cannot ride the SPA's session directly; the
interceptor already accepts a bearer token as an alternative to the cookie. The id
is obtained by a content script on the PortfolioDB origin, which does run in a
context where the cookie applies, calling GetSession and reading the id from the
response body. We chose this over granting the extension the `cookies` permission
to read the HttpOnly cookie directly: the bootstrap is a little more code, but the
extension asks for no permission that lets it read cookies for any other site.

The date window sent to UpsertTxs starts *before* the last known transaction, by a
configurable lookback, rather than resuming the day after it. A broker row that is
Pending at export time is dated by its order date and re-dated to its later
completion date once it settles; resuming after the last known transaction would
leave the earlier copy outside the replace window, where it survives the delete and
becomes a duplicate. The overlap exists to prevent that duplication, and is free
because ingestion is idempotent by replacement (see
adr/0002-transaction-ingestion-model.md). The consequence is that the lookback is
not an arbitrary safety margin: it must exceed the broker's longest order-to-
completion lag, or duplicates reappear.
