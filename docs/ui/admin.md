# Admin UI

Admin-only pages are available to users with the **admin** role. Admin role is assigned when the user’s Google subject matches `ADMIN_AUTH_SUB` (see [auth.md](../auth.md) and [README](../../README.md)).

## Access

- **Header link**: When signed in as an admin, an **Admin** link appears in the top-right header (next to the user email and Log out). It is hidden for non-admin users.
- **URL**: The admin area lives under `/admin`. Direct navigation to `/admin` or any `/admin/*` route without the admin role shows an “Access denied” message.

## Admin layout

- **Overview** (`/admin`): Short description and links to admin tools.
- **Sidebar**: All admin pages share a layout with a left sidebar that links to:
  - Overview
  - ID token

## ID token page (`/admin/id-token`)

- **Purpose**: Fetch a Google ID token for use in scripts (e.g. calling the Auth API or passing credentials to other tools).
- **How to use**: Click “Sign in with Google” to obtain a fresh ID token. The token is shown in a read-only text area so you can copy it. The token is short-lived; request a new one when needed.
- **Typical use**: Copy the token and use it in scripts that call the backend (e.g. `Auth` to get a session, or use the token where an ID token is required).
