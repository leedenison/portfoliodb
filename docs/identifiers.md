# Instruments

An **instrument** holds canonical data (id, asset class, exchange, currency, name, etc.) independent of how it was identified. **Asset class** is a controlled vocabulary: one of `STOCK`, `ETF`, `FIXED_INCOME`, `MUTUAL_FUND`, `OPTION`, `FUTURE`, `CASH`, or `UNKNOWN` (or null if unknown). Instruments with asset class `OPTION` or `FUTURE` must reference an underlying instrument. 

**Instrument tags**: Instruments support **tags** (tag type / tag value). Datasource-specific metadata such as market sector, security type, or similar fields returned by identification or price plugins (e.g. OpenFIGI’s marketSector, securityType) will be stored as tags on the instrument.

## Instrument Identifiers

Instrument **identifiers** are unique identifiers for instruments and consist of three parts: `identifier_type` (required), `domain` (nullable) and `value` (required).  Each triplet is unique within the system.

Most identifiers (eg. ISIN, CUSIP) consist of an identifier_type and an opaque value.  Ticker identifiers include a domain: MIC_TICKER uses an ISO 10383 MIC code as domain, OPENFIGI_TICKER uses a Bloomberg/OpenFIGI exchange code as domain.

### Exchange code normalization

MIC_TICKER domains are always stored as **operating MICs** (ISO 10383 mic_type = 'O'). When a segment MIC (mic_type = 'S') is supplied -- whether from a data provider, CSV import, or API call -- it is silently normalized to the corresponding operating MIC via the `exchanges` table before storage. For example, XNGS (NASDAQ/NGS Global Select Market, a segment) is normalized to XNAS (NASDAQ, the operating MIC).

This normalization ensures that the same instrument is always identified by the same MIC regardless of which provider or segment returned it. Different providers may disagree about which segment is "primary" for an instrument; normalizing to the operating MIC eliminates this ambiguity.

Consistency checks between identifier plugins and between import hints and resolved instruments compare exchanges at the operating MIC level. Two plugins returning different segment MICs for the same operating exchange are considered consistent.

### Provider-specific identifiers

Some identifiers are specific to a particular data provider and are not part of the canonical identifier vocabulary. These are stored in the `provider_instrument_identifiers` table, separate from canonical identifiers. Each row includes a `provider` column (e.g. "massive", "eodhd", "openfigi") and a free-form `identifier_type` specific to that provider.

Examples of provider-specific identifiers:
- **SEGMENT_MIC_TICKER** (provider: massive) -- the segment-level MIC and ticker that Polygon.io's API requires for price and corporate event lookups
- **EODHD_EXCH_CODE** (provider: eodhd) -- EODHD's proprietary exchange code (e.g. "US", "LSE") used to build `ticker.code` symbols for API calls
- **FIGI** (provider: openfigi) -- the venue-specific FIGI (formerly OPENFIGI_GLOBAL), which is tied to a specific trading venue

Provider identifiers are populated by identifier plugins during resolution and stored alongside canonical identifiers. When a price or corporate event plugin needs to fetch data, the orchestrator loads provider-specific identifiers for the plugin's provider ID and merges them into the identifier list. Plugins prefer their provider-specific identifiers when available and fall back to canonical identifiers.

If a provider-specific identifier is not available (e.g. the instrument was imported without running through the provider's identifier plugin), the provider plugin falls back to canonical identifiers. If those are also insufficient for the provider's API, the fetch fails gracefully and the orchestrator tries the next plugin in precedence order.

Externally understood identifiers (eg. type = `"ISIN"`, `"CUSIP"`, `"MIC_TICKER"`, `"OPENFIGI_TICKER"`, etc) are **canonical** (ie. canonical: true).  Instruments can also be identified by a broker description (eg. type equals a source string like `"IBKR:<client>:statement"`) which is a non-canonical identifier (ie. canonical: false).  This flag is stored in the database and used (e.g. for export) to distinguish broker-description-only instruments without inferring from identifier_type. Broker descriptions are stored as identifiers: `identifier_type` = source (the ingestion request’s source), `value` = full instrument description.

The triple **(identifier_type, domain, value) is unique** in the system; the server does not allow duplicates. The database should enforce this with a unique index on (identifier_type, domain, value) so that instruments can be looked up by any known identifier.

**The (source, instrument_description) identifier is always stored on the instrument** whenever that description is resolved (by plugin or as broker-description-only), so that future uploads with the same source and description can resolve via DB lookup without calling plugins again. 

A broker may supply **multiple descriptions for the same stock** (e.g. from a statement, a trade confirmation email, or a tax document). The client supplies a **source** per ingestion request (e.g. `"IBKR:<client>:statement"` or `"IBKR:<client>:confirmation"`). The server does not interpret source; it only requires that it be non-empty. Lookup by (source, instrument_description) is a single lookup on (identifier_type, NULL (domain), value). **Normalization** of broker descriptions (e.g. avoiding two descriptions for the same instrument when they refer to the same thing) is the **client’s responsibility**; the server stores values as received.

Broker-description-only instruments are first-class: they appear in holdings and the UI by that description. 

Broker descriptions and canonical identifiers are unique once processed. Two users with the same brokerage and description, or two broker descriptions resolving to the same ISIN, refer to the same instrument so updates are reflected globally.

## Identifying Instruments

PortfolioDB resolves instruments during asynchronous ingestion of transactions or during a periodic sweep of broker-descriptions only instruments.

### Data Supplied by the Client

Every valid transaction has a broker, a **source** (required; opaque, eg. `"IBKR:<client>:statement"`), and an instrument description; missing any is a **validation error**.  Clients must pass these when importing transactions. The client must provide a description even when they also supply external identifiers, so that the batch cache can always be keyed by (source, description).

Clients may also pass a `currency` hint along with each transaction.  This can be used to narrow instrument resolution (see plugins below).  The hint must never be stored as canonical information directly; it can only be used to narrow resolution with the authoritative data coming from the plugin resolution.

Clients may also pass known, external identifiers for a transaction (eg. `"ISIN"`, `"CUSIP"`, `"MIC_TICKER"`, `"OPENFIGI_TICKER"`, etc).  Exchange information is carried on the identifier itself: MIC_TICKER uses an ISO 10383 operating MIC code as domain, OPENFIGI_TICKER uses a Bloomberg exchange code as domain.  

### Extract Identifiers from Transaction

If a client supplies one or more external identifiers with a transaction then the identifier extraction step is skipped and the system resolves instruments from the supplied identifiers.  The transaction is associated with the resolved instrument.  **No (source, NULL, description) identifier is stored in this case since we are relying on the authority of the user.** A later upload with the same source and description but without those identifiers will go through description extraction and resolution and may resolve to a different instrument.

If the client supplies conflicting external identifiers which resolve to more than one instrument it is considered a validation error.

If the client supplies only a broker, source and description with optional hints, the system will attempt to extract candidate identifiers from the description.  This is done via the "description" plugins at `server/plugins/<datasource>/description` (see below).  If the extraction succeeds then resolution continues using the extracted identifiers.  **A (source, NULL, description) identifier must be stored in this case as an authoritative mapping from the broker description.**

Extraction failure is treated as an identity lookup failure.  **A (source, NULL, description) identifier must be stored in this case as a mapping from the broker description.**

### Resolve Identifiers

When a client supplies external identifier hints for a transaction, or when a description plugin has successfully extracted one or more identifier hints from a transaction description, the identifier plugins will attempt to look up canonical instrument metadata and canonical identifiers for the instrument.
 
Resolution order: (1) DB lookup by (source, NULL (domain), instrument_description) or by existing identifiers; (2) within the current batch, use a cache so the same (source, description) is resolved once; (3) only if still unresolved, call enabled plugins (passing broker, source, instrument_description, currency hint, and identifier hints). This avoids unnecessary or duplicate calls to expensive (e.g. quota-managed) plugins.

If no plugin resolves a given (source, instrument_description), the system ensures an instrument exists with at least that source identifier and attaches it to the transaction and adds to the status counter. Plugin failures (e.g. timeout, unavailable) are handled the same way: persist the transaction with the broker-description-only instrument and record an identification error; do not fail the whole job. Identification reporting (e.g. GetJob, UI) must distinguish between description extraction failure (no description plugin returned identifiers), identifier resolution failure (extraction succeeded or client supplied identifiers but no identifier plugin resolved), and plugin failure (e.g. timeout, unavailable).

**Instrument merge**: When a new identifier would link two previously distinct instruments (e.g. same ISIN), the system must merge them: choose a survivor, update all transaction references, move identifiers to the survivor, delete the merged-away instrument. When **multiple** identifiers returned for the same logical security resolve to **more than one** existing instrument (e.g. instrument A has ISIN 1, instrument B has CUSIP 1, and a plugin returns both identifiers for one security), the system **detects** this and **merges** those instruments eagerly during resolution. After merge, a single canonical instrument remains; the survivor is chosen as follows: the instrument with more identifiers wins; if tied, the one with older `created_at` (further tie-breaker may be non-deterministic). All updates (transaction references, identifier moves, deletion of the merged-away instrument) happen in one database transaction; implementations may batch the updates within that transaction for scale.

Merge runs **eagerly** when such a conflict is detected during the resolution step (ingestion path). The same merge logic can be reused by a future periodic job; the job’s scheduling and implementation are out of scope for the initial milestone. When a plugin returns identifiers that match an **existing** instrument, the **identifier is the source of truth**: attach the transaction to that instrument and do not overwrite its canonical fields with the plugin’s output.

**Duplicate (source, instrument_description) in same batch with different plugin results**: Resolve each (source, instrument_description) once per batch and cache the result. All transactions in the batch with that key receive the same instrument_id. No per-transaction plugin call for the same key—ensures consistency and avoids extra plugin cost.

**Plugin is unavailable or times out (e.g. external API down)**: Create or find the broker-description-only instrument, set instrument_id, persist the transaction, and record an identification error/warning (e.g. plugin timeout) for GetJob and UI. Do not fail the whole job. Optional: retry the plugin once with backoff before falling back.

**No fixed “complete” set of identifiers**: What identifiers exist for an instrument depends on enabled plugins and instrument type (e.g. some instruments have no CUSIP). The system does not treat “only one standard identifier known” as a hard error for that reason. Merge-on-conflict (above) handles the case where the same security was previously stored under two different instruments (e.g. one had only ISIN, another only CUSIP) by merging them when resolution later sees both.

PortfolioDB should periodically attempt to identify instruments in case datasources have been updated. Admin users can manually force a refresh for a given instrument or set of instruments.

### User override

A user may believe the system has mis-identified an instrument. It should be possible for a user to override the identity for their portfolio. They do this by ensuring that the client provides an external identifier hint for the transactions they want to override.  This will then be looked up directly rather than using identifiers extracted from the description.  Admin users can correct shared instrument identities in the admin UI.

### Security type and transaction handling

Transactions that are **not stored** (e.g. SPLIT) are determined by **TxType** and are dropped before resolution. No resolution or DB insert is performed for such transactions.

For transactions that are stored, the ingestion layer maps TxType to a **security type hint**. This hint is passed to description and identifier plugins for routing only (e.g. the cash plugin only accepts CASH). The hint vocabulary is the **same as asset class** (type alias).

#### Type layers

- **TxType** (broker/proto): Transaction classification (BUYSTOCK, SPLIT, INCOME, …). Some TxTypes are not stored (e.g. SPLIT).
- **Security type hint** (routing): Derived from TxType; vocabulary is the same as asset class: STOCK, ETF, FIXED_INCOME, MUTUAL_FUND, OPTION, FUTURE, CASH, UNKNOWN. TxType cannot distinguish stock from ETF, so stock-like TxTypes map to STOCK (never ETF).
- **Asset class** (canonical): STOCK, ETF, FIXED_INCOME, MUTUAL_FUND, OPTION, FUTURE, CASH, UNKNOWN. Set by identifier plugins and stored on instruments.

### Description Plugins

When the client supplies only broker, source and instrument description (no external identifier hints), the system uses **description plugins** to **extract** candidate identifiers from the raw broker description. Description plugins live at `server/plugins/<datasource>/description` (e.g. `server/plugins/ibkr/description`). They parse the free-text instrument description (e.g. from a broker statement or confirmation) and return zero or more identifier hints (type, domain, value) that are then passed to the identifier resolution step. If a description plugin successfully extracts one or more identifiers, resolution continues using those extracted identifiers and a (source, NULL, description) identifier is stored as the authoritative mapping from the broker description. If extraction fails (no plugin returns identifiers, or the description is unparseable), the system treats it as an identity lookup failure: a (source, NULL, description) identifier is still stored and the instrument is created or found as broker-description-only.

Description plugins have **precedence** (integer, required, unique across description plugins; stored in the database with plugin config). They are executed **in series** by precedence order. The **first** plugin that returns one or more identifier hints is used; no later plugin is called for that transaction. If no plugin returns identifiers, extraction has failed. Description plugins receive the broker, source, instrument description, and optional client hints (e.g. currency) so they can narrow or disambiguate extraction. Like identifier plugins, they are compiled in and enabled at runtime; configuration (e.g. API keys, options) is stored in the database and only admins can view or edit it. The shared interface and types for extracted identifiers live with the identifier resolution code (e.g. under `server/identifier`). Only after description plugins run (and only when they return identifiers) does the resolver call identifier plugins to look up canonical instrument metadata and canonical identifiers.

### Identifier Plugins

Plugins implement a single interface that accepts all hints, e.g. `Identify(ctx, config, broker, source, instrument_description, hints, identifier_hints) → (*Instrument, []Identifier, error)`. Exchange information is carried on identifier hints (MIC_TICKER domain = MIC, OPENFIGI_TICKER domain = exchange code). The hints struct carries currency and security type hint; only API-confirmed data is stored on the instrument. For options and futures, the plugin may also return data for the underlying instrument; the caller is responsible for ensuring the underlying exists and is linked to the derivative. The resolver passes each plugin’s config JSON from the database into `Identify`; plugins may use it for API keys and options (only admins can view or edit plugin config). Implementations live under `server/plugins/<datasource>/identifier` (e.g. `server/plugins/local/identifier`, `server/plugins/ibkr/identifier`). The shared interface and canonical types (Instrument, Identifier) live in `server/identifier`. Plugins are compiled in and enabled at runtime; configuration is stored in the database.

**Plugin config** (in DB): plugin id, enabled flag, **precedence** (integer, required, unique across plugins; used to resolve conflicts), and optional config JSONB. Scope is global for the initial milestone. No two plugins may share the same precedence.

**Resolution flow**: If the DB or in-batch cache already has an instrument for (source, instrument_description), do not call plugins. Otherwise call **all enabled plugins in parallel**, then merge results: instrument metadata (name, asset class, etc.) from the highest-precedence plugin that succeeded; **identifiers merged** from all successful plugins—for each identifier **type**, the value from the highest-precedence plugin that returned that type is used (non-overlapping types are combined; same-type conflicts resolved by precedence). The resolver **must always** ensure the (source, instrument_description) identifier is stored on the instrument (whether from plugin results or as the only identifier when no plugin resolves). That way future uploads with the same (source, description) resolve by DB lookup and do not call plugins again. Then find or create an instrument (with at least the broker identifier) and set the transaction’s instrument_id. If no plugin resolves (e.g. returns a “not identified” sentinel), the service still ensures a broker-description-only instrument exists and attaches it. Record identification errors/warnings (e.g. plugin timeout, broker-description-only) for GetJob and UI.

Plugins can own database migrations (e.g. reference tables). Plugin migrations live in the plugin directory (eg. `server/plugins/<datasource>/identifier/migrations`). Example: the local reference-data plugin uses a Postgres reference table.

## Troubleshooting: identification not running

**Identification runs only during ingestion.** Instrument resolution (description plugins → identifier plugins) is performed by the ingestion worker when transactions are submitted via the **Ingestion gRPC API** (UpsertTxs or CreateTx). If transactions were loaded by another route (e.g. direct SQL into `txs`, or a script that does not use the ingestion API), no jobs are created and the worker never runs, so no identification and no Redis counters.

**Identifier plugins run only when there are hints.** The flow is: DB lookup by (source, description) → if miss, run **description plugins** to get identifier hints → then run **identifier plugins** with those hints. If the description plugin returns **no hints** (e.g. OpenAI API error or invalid model), the server creates a broker-description-only instrument, records the identification error as "description extraction failed", and **never** calls identifier plugins. So no Redis counters (e.g. `instrument.identify.attempts`) and no OpenFIGI calls.

**Diagnosis steps:**

1. **Confirm transactions were ingested via the API**  
   If there are no ingestion jobs, or no recent jobs for your upload, identification did not run:
   ```sql
   SELECT id, user_id, broker, source, status, created_at FROM ingestion_jobs ORDER BY created_at DESC LIMIT 20;
   ```

2. **Check identification errors**  
   If jobs exist and completed, look at stored identification errors. Message `description extraction failed` means no description plugin returned hints (e.g. OpenAI failing); other messages indicate identifier plugin timeouts or "broker description only":
   ```sql
   SELECT j.id, j.status, e.row_index, e.instrument_description, e.message
   FROM ingestion_jobs j
   JOIN identification_errors e ON e.job_id = j.id
   ORDER BY j.created_at DESC, e.row_index
   LIMIT 50;
   ```

3. **Server logs**  
   With `LOG_LEVEL=debug`, the server logs:
   - `description plugin returned error` — a description plugin (e.g. OpenAI) failed; the `err` field shows the cause.
   - `description extraction: no plugin returned hints` — no description plugin returned any hints.
   - `instrument resolution: description extraction failed, using broker description only` — we are creating broker-description-only instruments and not calling identifier plugins.

4. **OpenAI description plugin**  
   Ensure `description_plugin_config.config` uses a **valid model** (e.g. `gpt-4o-mini`, `gpt-4o`). An invalid model (e.g. `gpt-5.2`) causes the API to return an error; the plugin then returns no hints and identifier plugins are never called.