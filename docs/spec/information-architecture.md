# Information Architecture

The informantion architecture describes key concepts for users (and admin users), how they relate to each other, the relative importance they carry for the user and gives the example of how the information architecture should impact global navigation.  However, these concepts should be used to inform the way user interfaces are presented to users across the application.  For the reasoning behind concept visibility and the navigation split, see adr/0012-ui-concept-visibility.md.

## Key User Concepts

 * **Portfolios** are subsets of a users holdings filtered according some criteria (eg. asset class, account, broker, a chosen list of instruments, etc).  They allow analysis to be performed on specific subsets (eg. performance against a benchmark of all non-pension assets).  The unfiltered 'All Holdings' list is treated as the default portfolio that every user has.  It is always presented as a portfolio that cannot be deleted.
    - **Shared Portfolios** are a union of two portfolios, usually owned by different users.  When a user decides to share a portfolio with another user they are granting read only access to the portfolio.  The other party then selects a portfolio to contribute to the share which reciprocates the access.  Users can share their 'All Holdings' default portfolio.
    - **Performance** is the umbrella term used to cover all kinds of metrics used to examine historic, or project future, performance of a portfolio.  Performance will normally be expressed in the context of a particular portfolio.
    - **Analysis** is the umbrella term user to cover all kinds of breakdowns of portfolios (eg. by market sector, by asset class, by georgraphic location, etc).  Analysis will normally be expressed in the context of a particular portfolio.
 * **Transaction History** is the history of all transactions affecting holdings that a user has added to the system.  Conceptually this covers both the transactions that are stored in the system, as well as the bulk and single transaction uploads (jobs) and any errors that were associated with those uploads.
     - **Epoch Start Date** is the date at which the transaction history begins.  It is defined as the date of the earliest transaction that exists in the system for the user.  This concept is not prominent for the user and should only be presented to the user when absolutely necessary.
     - **Initialization Transactions** are transactions which are automatically added to the transaction history to reconcile the current state of the users holdings due to missing history prior to the epoch start date.  This concept is not prominent for the user and should only be presented to the user when absolutely necessary.
     - **Holdings Checkpoint** is a point in time when the user specifies absolute values held for all instruments.  Users can provide a checkpoint by reading off the current values in their accounts without needing to do any arithmetic.  The system can calculate the required initialization transactions from the checkpoint.  This concept is not prominent for the user and should only be presented to the user when absolutely necessary.
     - **Uploads** are the jobs related to the uploads of transaction data that the user has provided in the past.  Users are most interested in whether the uploads succeeded and any errors associated with failures.
        + **Errors** are the errors associated with processing a transaction.  Identification errors and validation errors are presented to the user - ideally with links directly to UI that allows corrective action, or with a helpful, actionable error message. 
     - **Trade Notifications** are the single transaction uploads which are likely to have been automated.  They are kept separate from the (manual) uploads.

 * **Your Archive** is the single file a user's own data is exported to and restored from: their preferences, and in time their transactions and holding declarations.  It is its own concept rather than an attribute of any one page, because it spans all three.  It carries none of the shared reference data the instance is built from -- that is The Archive below, which an admin keeps -- so the two never mix and neither reaches the other's page.  A user cares about getting their data out, and about knowing what an import actually applied.  Restoring an archive into an instance whose instruments are not loaded is supported and correct; it is merely slower, and must not be presented as an error.

 ## Key Admin User Concepts

 * **The Archive** is the single file the shared data of an instance is exported to and rebuilt from.  It is its own concept rather than an attribute of any one page, because it spans Reference Data, Plugins and Diagnostics: instruments, prices and corporate events sit under Reference Data, plugin configuration under Plugins, and fetch blocks and unhandled corporate events under Diagnostics.  An admin user cares about producing a file that is complete enough to rebuild from, and about knowing what an import actually applied.  The archive carries no user data at all, and a user's own archive is a separate file with a separate page.
 * **Reference Data** covers much of the shared data on the system used by all users (eg. instrument data).  Admin users are concerned with the health of the data.
    - **Instruments** are the shared instrument and instrument identifier data.  The admin user is primarily concerned with the health of the data and whether there are large numbers of unidentified instruments - or if there are a large number of users overriding the identity of a given instrument.  
    - **Prices** are the shared price data for instruments.  Admin users are primarily interested in the periods of time for which we have price data for a given instrument.  They are also interested in retrieval failures and whether there are periods of time for which we have been unable to fetch price data for a given instrument.
 * **Plugins** cover the integration with external services for providing instrument identity, price data and corporate events.  Plugins should be organised by type and each plugin should be presented separately.
    - **Configuration** is the configuration for each plugin including the enabled switch and precedence.  Precedence is an ordering someone chose rather than something that can be reconstructed, so it belongs in the archive; carrying it also carries live API keys, which is why including it is a deliberate choice rather than the default.
    - **Telemetry** covers counters for paths specific to a particular plugin.
 * **Diagnostics** cover the information and tools which allow the admin user to maintain the system.
    - **Logs** provide a history of notable events (eg. errors, restarts, etc) that have occurred on the system.
    - **Telemetry** provides counters for notable events which can be viewed in aggregate (eg. uploads (successes and failures), 5xx errors, etc).  These can be presented as a history over time as well as a current snapshot.
    - **Tools** provide debugging and diagnostic tools (eg. ID Token creation for use with scripts).

## User Navigation

Navigation is split into a top navigation bar and a left sidebar.  The top bar contains the portfolio selector and account-level actions.  The left sidebar contains the primary working views, all scoped to the selected portfolio.

### Top Navigation Bar

| Position | Item | Type | Status |
|----------|------|------|--------|
| Left | **Portfolio selector** | Chip / modal picker | Active |
| Right | **User menu** | Dropdown (see below) | Active |

### Portfolio Selector

The portfolio selector is a chip displayed at the left of the top navigation bar showing the name of the currently selected portfolio.  Clicking it opens a modal dialog (similar to Google Cloud's project picker) which serves as the complete portfolio management surface:

 * The modal lists all portfolios owned by the user, with "All Holdings" pinned at the top.
 * **Shared Portfolios** appear in the list alongside owned portfolios, visually distinguished (eg. with a shared icon or label).
 * Selecting a portfolio closes the modal and updates the global context.  All portfolio-scoped pages immediately reflect the new selection.
 * The modal also provides controls to **create**, **rename** and **delete** portfolios.  "All Holdings" cannot be renamed or deleted.
 * If a user has many portfolios, a search/filter field at the top of the modal allows quick lookup.

The selected portfolio defaults to "All Holdings" on sign-in.  The selection is preserved across page navigations within the same session.

### User Menu

The user menu is a dropdown anchored to the user's email address on the right side of the top navigation bar.  Clicking the email opens the dropdown; clicking outside or pressing Escape closes it.

| Item | Type | Visibility |
|------|------|------------|
| User email | Display only | Always |
| **Uploads** | Link (`/uploads`) | Always |
| **Archive** | Link (`/archive`) | Always |
| **Settings** | Link (`/settings`) | Always |
| **Admin** | Link (`/admin`) | Admin role only |
| **Log out** | Action | Always |

 * **Uploads** shows all uploads regardless of the selected portfolio.  However, when a portfolio other than "All Holdings" is selected, rows containing transactions relevant to that portfolio should be visually highlighted.  Note: the upload flow itself is a modal launched from the Holdings page, not a separate route; the `/uploads` page is the upload history list.
 * **Archive** is where a user produces and consumes their own archive.  Export offers a menu of what to include: a part left out is absent from the file, and a part included but holding nothing is written empty, which records that the export asked and there was nothing.  Import takes a whole file, runs on the server and reports a result per part, so it finishes whether or not the page stays open.  The page states plainly that importing ignored asset classes replaces the rules the user has and removes the transactions those rules cover.  It is a separate affordance from the transaction upload modal, which converts a broker's own file: different input, different failure modes, different frequency.
 * **Settings** is where a user sets their display currency and the asset classes to ignore on import.
 * **Admin** navigates to the admin area.  Only visible to users with the admin role.

### Left Sidebar

The left sidebar contains the primary working views.  All views are scoped to the selected portfolio.

| Item | Destination | Status |
|------|-------------|--------|
| **Holdings** | `/holdings` | Active |
| **Transactions** | `/transactions` | Disabled |
| **Performance** | `/performance` | Disabled |
| **Analysis** | `/analysis` | Disabled |

### Navigation Behaviour

 * **Holdings** is the default destination after sign-in.  It shows the holdings for the selected portfolio.
 * **Transactions** shows the transaction history filtered to the selected portfolio -- only transactions for instruments, accounts and brokers that match the portfolio's criteria are displayed.  When the selected portfolio is "All Holdings", all transactions are shown.  Disabled until implemented.
 * **Performance** shows TWR, MWR and other performance metrics for the selected portfolio.  Disabled until implemented.
 * **Analysis** shows breakdowns (by sector, asset class, geography, etc) for the selected portfolio.  Disabled until implemented.

### Mobile Considerations

 * The left sidebar should collapse to an off-canvas drawer triggered by a hamburger menu button in the top navigation bar.
 * The portfolio selector chip should remain visible in the top bar at all times so that the user can always see and change the active portfolio context.
 * The portfolio selector modal should render as a full-screen sheet on small viewports.

## Admin Navigation

Admin pages live under `/admin` and use a dedicated layout with a left sidebar.  The sidebar should be persistent and visible on all admin pages.  The top navigation bar from the user area remains visible so that admin users can easily return to their portfolios.

### Admin Sidebar

| Section | Item | Destination | Status |
|---------|------|-------------|--------|
| | **Dashboard** | `/admin` | Active |
| | **Archive** | `/admin/archive` | Active |
| **Reference Data** | Instruments | `/admin/instruments` | Active |
| **Reference Data** | Prices | `/admin/prices` | Active |
| **Reference Data** | Corporate Events | `/admin/corporate-events` | Active |
| **Reference Data** | Inflation | `/admin/inflation` | Active |
| **Plugins** | Identifier | `/admin/plugins/identifier` | Active |
| **Plugins** | Description | `/admin/plugins/description` | Active |
| **Plugins** | Price | `/admin/plugins/price` | Active |
| **Plugins** | Inflation | `/admin/plugins/inflation` | Active |
| **Diagnostics** | Imbalance | `/admin/imbalance` | Active |
| **Diagnostics** | Logs | `/admin/logs` | Disabled |
| **Diagnostics** | Telemetry | `/admin/telemetry` | Active |
| **Diagnostics** | Workers | `/admin/workers` | Active |
| **Diagnostics** | Authentication | `/admin/tools` | Active |

### Navigation Behaviour

 * **Dashboard** is the admin landing page and should provide a dashboard-style summary with quick links to the most important admin functions, including the Archive.
 * **Archive** (`/admin/archive`) is the one place the system archive is produced and consumed.  It sits at the top level rather than inside a section because the data it carries spans all three of them.  It offers a menu of what to include; a part left out is absent from the file, and a part included but holding nothing is written empty, which records that the export asked and there was nothing.  Plugin config is the one part not ticked by default: it carries live API keys, which makes the file a secret and changes where it can safely be kept, so including it is a deliberate choice rather than one inherited from a default.  An import is applied on the server, so it finishes whether or not the page stays open, and it reports a result per part: how much was applied, and which rows were rejected.  No user data is reachable from this page.
 * **Reference Data** groups Instruments, Prices, Corporate Events and Inflation.  These are the data stewardship pages the admin visits most frequently.  They show what the instance holds and its health; producing or restoring a file is done from the Archive rather than from any of them, so there is one way to do it rather than several.
 * **Plugins** lists individual plugin types: Identifier (`/admin/plugins/identifier`), Description (`/admin/plugins/description`), Price (`/admin/plugins/price`) and Inflation (`/admin/plugins/inflation`).  Each plugin page shows configuration and telemetry for that plugin type.  All are active.
 * **Diagnostics** groups Imbalance, Logs, Telemetry, Workers and Authentication.  Telemetry (`/admin/telemetry`), Workers (`/admin/workers`) and Authentication (`/admin/tools`) are active; Logs is disabled until implemented.
 * **Imbalance** (`/admin/imbalance`) reports the residual and transfer-clearing balances left in non-asset accounts, aggregated across all users and grouped by broker, account, commodity and the event that left them.  It is an admin surface because it measures how lossy each broker converter is rather than what is in any one portfolio, and it carries no user identity.  The per-user view of the same data belongs with user alerts.  Its transfers view lists the transfers whose second side has not arrived; a matched pair is settled and is excluded.
 * Section headers (Reference Data, Plugins, Diagnostics) are non-clickable labels that organise the sidebar visually.

### Mobile Considerations

 * The admin sidebar should collapse to an off-canvas drawer triggered by a menu button on small screens.
 * The sidebar should overlay the content rather than pushing it, to preserve the content area width on narrow devices.