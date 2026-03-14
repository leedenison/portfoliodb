# Information Architecture

The informantion architecture describes key concepts for users (and admin users), how they relate to each other, the relative importance they carry for the user and gives the example of how the information architecture should impact global navigation.  However, these concepts should be used to inform the way user interfaces are presented to users across the application.

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
     - **Trade Notifications** are the single transaction uploads which are likely to have been automated.  We keep them separate from the uploads since the user is likely to conceive of automated uploads as distinct from manual uploads.

 ## Key Admin User Concepts

 * **Reference Data** covers much of the shared data on the system used by all users (eg. instrument data).  Admin users are concerned with the health of the data and the ability to export / import reference data for the purposes of backup and reducing lookup overhead.
    - **Instruments** are the shared instrument and instrument identifier data.  The admin user is primarily concerned with the health of the data and whether there are large numbers of unidentified instruments - or if there are a large number of users overriding the identity of a given instrument.  
    - **Prices** are the shared price data for instruments.  Admin users are primarily interested in the periods of time for which we have price data for a given instrument.  They are also interested in retrieval failures and whether there are periods of time for which we have been unable to fetch price data for a given instrument.
 * **Plugins** cover the integration with external services for providing instrument identity, price data and corporate events.  Plugins should be organised by type and each plugin should be presented separately.
    - **Configuration** is the configuration for each plugin including the enabled switch and precedence.
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
| Right | User email | Display only | Active |
| Right | **Admin** | Link (`/admin`) | Active (admin role only, hidden otherwise) |
| Right | **Log out** | Action | Active |

### Portfolio Selector

The portfolio selector is a chip displayed at the left of the top navigation bar showing the name of the currently selected portfolio.  Clicking it opens a modal dialog (similar to Google Cloud's project picker) which serves as the complete portfolio management surface:

 * The modal lists all portfolios owned by the user, with "All Holdings" pinned at the top.
 * **Shared Portfolios** appear in the list alongside owned portfolios, visually distinguished (eg. with a shared icon or label).
 * Selecting a portfolio closes the modal and updates the global context.  All portfolio-scoped pages immediately reflect the new selection.
 * The modal also provides controls to **create**, **rename** and **delete** portfolios.  "All Holdings" cannot be renamed or deleted.
 * If a user has many portfolios, a search/filter field at the top of the modal allows quick lookup.

The selected portfolio defaults to "All Holdings" on sign-in.  The selection is preserved across page navigations within the same session.

### Left Sidebar

The left sidebar contains the primary working views.  All views except Uploads are scoped to the selected portfolio.

| Item | Destination | Status |
|------|-------------|--------|
| **Holdings** | `/holdings` | Active |
| **Transactions** | `/transactions` | Disabled |
| **Uploads** | `/uploads` | Disabled |
| **Performance** | `/performance` | Disabled |
| **Analysis** | `/analysis` | Disabled |

### Navigation Behaviour

 * **Holdings** is the default destination after sign-in.  It shows the holdings for the selected portfolio.
 * **Transactions** shows the transaction history filtered to the selected portfolio -- only transactions for instruments, accounts and brokers that match the portfolio's criteria are displayed.  When the selected portfolio is "All Holdings", all transactions are shown.  Disabled until implemented.
 * **Uploads** shows all uploads regardless of the selected portfolio, since a single upload can contain transactions spanning multiple portfolios.  However, when a portfolio other than "All Holdings" is selected, rows containing transactions relevant to that portfolio should be visually highlighted.  Disabled until implemented.
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
| | **Overview** | `/admin` | Active |
| **Reference Data** | Instruments | `/admin/instruments` | Active |
| **Reference Data** | Prices | `/admin/prices` | Disabled |
| **Plugins** | *(listed by type)* | `/admin/plugins` | Disabled |
| **Diagnostics** | Logs | `/admin/logs` | Disabled |
| **Diagnostics** | Telemetry | `/admin/telemetry` | Disabled |
| **Diagnostics** | Tools | `/admin/tools` | Active |

### Navigation Behaviour

 * **Overview** is the admin landing page and should provide a dashboard-style summary with quick links to the most important admin functions.
 * **Reference Data** groups Instruments and Prices.  These are the data stewardship pages the admin visits most frequently.  Instruments is active since it is already implemented; Prices is disabled until implemented.
 * **Plugins** should list each plugin type (identity, price-fetcher, corporate events) and within each, the individual plugins with their configuration and telemetry.  The sidebar item expands or navigates to a plugin index page.  Disabled until implemented.
 * **Diagnostics** groups Logs, Telemetry and Tools.  Tools is active (ID Token page is implemented); Logs and Telemetry are disabled until implemented.
 * Section headers (Reference Data, Plugins, Diagnostics) are non-clickable labels that organise the sidebar visually.

### Mobile Considerations

 * The admin sidebar should collapse to an off-canvas drawer triggered by a menu button on small screens.
 * The sidebar should overlay the content rather than pushing it, to preserve the content area width on narrow devices.