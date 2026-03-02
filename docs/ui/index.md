## Unauthenticated home screen

Shows a welcome message.

## Users home screen

This page shows a summary of portfolios that a user has created. It allows them to create a new portfolio, as well as rename and delete existing portfolios. Each portfolio name is a link to that portfolio’s holdings page.

## Portfolio holdings page

- **Purpose**: View holdings for a single portfolio (aggregate quantity per instrument, by broker).
- **How to reach it**: From the users home screen, click a portfolio name to go to `/portfolios/{id}`.
- **Contents**: Page title “Holdings – {portfolio name}”, a “Back to portfolios” link to home, and a table of holdings with columns: Instrument (description), Quantity, Broker. The “as of” date is shown when returned by the API.
- **Auth**: Same as the users home: the page requires an authenticated user; otherwise it shows a loading or unauthenticated state (sign-in prompt).
- **Edge cases**: Invalid or missing portfolio id (e.g. not found or no access) shows an error message instead of the holdings table.

## Admin

Admin-only pages (e.g. ID token for scripts) are available to users with the admin role. See [admin.md](admin.md) for access, layout, and the ID token page.