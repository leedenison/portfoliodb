-- Enable TimescaleDB for time-series price data.
CREATE EXTENSION IF NOT EXISTS timescaledb;
-- btree_gist lets an exclusion constraint mix equality on scalar columns with
-- overlap on a range, which is what instrument_identifiers needs below.
CREATE EXTENSION IF NOT EXISTS btree_gist;

-- M01 datamodel: holdings only. No instrument identification, prices or corporate events.
-- Holdings are calculated from transactions at query time, not materialized.

-- Users own portfolios. auth_sub stores Google ID token sub; name and email from token at Auth.
CREATE TABLE users (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  auth_sub   TEXT NOT NULL UNIQUE,
  name       TEXT NOT NULL,
  email      TEXT NOT NULL,
  role       TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
  display_currency TEXT NOT NULL DEFAULT 'USD',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_users_auth_sub ON users (auth_sub);

-- Portfolios are user-owned containers for transactions.
CREATE TABLE portfolios (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  name       TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_portfolios_user_id ON portfolios (user_id);

-- Portfolio filters: a portfolio is a view over txs matching any of its filters (OR). filter_value is text (broker name, account string, or instrument UUID).
CREATE TABLE portfolio_filters (
  portfolio_id  UUID NOT NULL REFERENCES portfolios (id) ON DELETE CASCADE,
  filter_type   TEXT NOT NULL CHECK (filter_type IN ('broker', 'account', 'instrument')),
  filter_value  TEXT NOT NULL,
  PRIMARY KEY (portfolio_id, filter_type, filter_value)
);

CREATE INDEX idx_portfolio_filters_portfolio ON portfolio_filters (portfolio_id);

-- A tx_group is one economic event; the txs referencing it are its postings.
-- timestamp is the event date; the postings carry the amounts and accounts.
-- job_id is the ingestion job that created the group, and is NULL for
-- system-generated groups such as the one backing an INITIALIZE posting.
-- It is deliberately not a foreign key: a group must outlive its job, and if job
-- rows are ever pruned by age the id still distinguishes one creation from another
-- and still groups everything written by the same upload.
-- The postings of a group are required to sum to zero in every commodity they weigh
-- in, enforced by check_tx_group_balance() at COMMIT.
-- See docs/spec/postings.md.
CREATE TABLE tx_groups (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  timestamp  TIMESTAMPTZ NOT NULL,
  job_id     UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_tx_groups_user_time ON tx_groups (user_id, timestamp);
CREATE INDEX idx_tx_groups_job_id ON tx_groups (job_id);

-- Transactions. Each row is a posting: a signed amount of one commodity in one account
-- at one point in time. See docs/spec/postings.md. The sections below follow the column
-- order of the table.
--
-- Ingestion and identity.
-- No natural key (broker statements often supply date only). Bulk idempotency is by
-- replace-by-period (user_id, broker, period); single-tx ingestion is append-only.
-- See docs/adr/0002-transaction-ingestion-model.md.
--
-- What moved: instrument_description, instrument_id, broker_tx_type,
-- resolved_tx_type, asset_class_hint.
-- instrument_id is added by an ALTER further down, because instruments is created after
-- this table and the foreign key needs it to exist. The column itself is declared here,
-- where it belongs.
-- broker_tx_type is the set of candidate event types the source declared, at
-- whatever specificity it managed; resolved_tx_type is the single value grouping
-- narrowed it to, and is what every consumer reads. Declared and derived are
-- separate columns because an archive carries only the declaration and an import
-- re-derives the resolution. Values are the TxType names from
-- proto/type/v1/type.proto; AMBIGUOUS is legal only resolved, never declared.
-- The antichain and weight-neutrality constraints on the declared set are
-- server-enforced. See docs/adr/0044-tx-type-is-declared-and-resolved.md and
-- docs/adr/0046-declared-ambiguity-is-bounded-by-weight-neutrality.md.
-- asset_class_hint is the class the source stated for routing, NULL when it made
-- no claim; the canonical class lives on the instrument.
--
-- The amounts as the source wrote them: quantity, unit_price, trading_currency,
-- settlement_currency, settlement_amount. Transcribed and exact; see Numeric types
-- below.
-- settlement_amount is the cash total the source stated for the row, in
-- settlement_currency and unsigned. It is transcribed rather than worked out from
-- other columns, which is what makes it independent of quantity * unit_price and so
-- what lets grouping identify a cash leg on one figure and check the identification
-- against the other. NULL on a posting whose own quantity is already money, since
-- carrying the same total twice would leave two figures to disagree. It contributes
-- nothing to weight: a TRADE_ASSET leg still weighs at quantity * unit_price, so the
-- gap between the two figures stays visible as the group's SOURCE_ROUNDING residual.
-- See docs/adr/0024-group-balance-is-checked-on-weight.md.
--
-- What the source called this row lives in tx_correlations below, not in columns
-- here: a reference number and a counterparty pointer are two series of the same
-- kind of thing, and a column per series would grow the posting one nullable field
-- per broker.
--
-- What kind of leg it is: account_type, synthetic_purpose.
-- account_type classifies the account this row lands in. USER is an ordinary broker
-- account posting; the others are the non-asset side of an event that is one-sided in
-- the source data and keep the broker and account of the event they belong to, so a
-- residual stays attributable to the account that produced it. Holdings and the other
-- quantity aggregations read USER only. See docs/adr/0022-typed-per-account-cash-flow-boundary.md.
--
-- synthetic_purpose says whether the server made the row, and why. NULL is a posting
-- a source stated, which carries evidence and survives every regroup; anything else
-- is derived from the postings around it and is recreated whenever those move.
-- 'RESIDUAL' is what a group's legs did not balance to, routed to an explicit
-- counterparty. 'BOUNDARY' is the other side of a posting whose own type names where
-- its money came from or went to, income for a dividend and expense for a charge.
-- 'INITIALIZE' is a synthetic opening balance, which differs from the other two in
-- being derived from a declaration rather than from the group it sits in.
--
-- It is what tells derived from stated, and account_type is not: a routed residual
-- and a leg a converter read out of a record can land in the same account type, so
-- the account-type list stopped separating them.
--
-- The event and its balance: group_id, weight, weight_commodity.
-- group_id is the economic event this row is a posting of. Every posting belongs to
-- exactly one group -- a lone posting is a group of one -- so that the balance
-- invariant has no rows it cannot reach. Deleting a group deletes its postings, which
-- makes the group the unit of deletion for replace-by-period.
-- weight is what this posting contributes to its group's balance and weight_commodity
-- names what that contribution is denominated in: 'cur:<code>' for money,
-- 'inst:<uuid>' for a security, 'desc:<text>' when the instrument never resolved. A
-- group balances when SUM(weight) is zero for each weight_commodity. The weight rules
-- live in server/service/ingestion/balance.go and are evaluated once at ingest; the
-- result is stored rather than re-derived here, because instrument state moves under a
-- posting afterwards -- a merge rewrites instrument_id and contract_multiplier records
-- what a corporate action left behind -- so a re-derived weight could disagree with the
-- one the group was balanced on. Computed from the raw quantity and unit_price, never
-- from the split-adjusted pair, which carries a rounding an exact check would reject.
-- The merge in server/db/postgres/instruments.go is the only thing that maintains
-- these after ingest, and it rewrites weight_commodity alongside instrument_id.
-- See docs/adr/0029-posting-weight-is-stored.md and
-- docs/adr/0024-group-balance-is-checked-on-weight.md.
--
-- Share counts: share_count_basis, split_adjusted_quantity, split_adjusted_unit_price.
-- share_count_basis is the date at which the share count the raw quantity and
-- unit_price are denominated in was current. It defaults to trade_date::date --
-- the as-traded assumption, that a broker log line accounts only for events
-- prior to the trade, which is why it hangs off the date the trade happened
-- rather than the date it was ordered. A source that restates historical rows (a broker's live
-- web UI showing post-split quantities) declares its own on the upload.
-- See docs/spec/bitemporality.md.
-- split_adjusted_quantity / split_adjusted_unit_price hold the values that result
-- from applying every stock split with ex_date > share_count_basis for the tx's
-- instrument. They equal the raw quantity/unit_price when no later split exists.
-- They are recomputed idempotently from the raw columns whenever splits change
-- (see RecomputeTxSplitAdjustments).
--
-- Numeric types.
-- quantity and unit_price are transcribed decimals and are exact, so they are
-- bare NUMERIC. The split-adjusted pair is not: the cumulative split factor is a
-- rational and a reverse /3 has no finite decimal form, so the pair declares a
-- rounding scale of 12 -- more places than any broker quotes fractional shares or
-- prices to. The rounding is confined to this derived cache; the raw columns it
-- is computed from stay exact and it is recomputable from them at any time. An
-- exact check (a group balance, a checked declaration) reads the raw columns.
-- See docs/adr/0026-exact-decimals-bounded-by-closure.md and
-- docs/adr/0028-cumulative-split-factor-is-an-exact-rational.md.
CREATE TABLE txs (
  id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id                   UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,

  broker                    TEXT NOT NULL,
  account                   TEXT NOT NULL,

  -- When the transaction was ordered, and the date a posting is filed under:
  -- windows, listings and the grouping rules that bucket on a day all use this
  -- one. It is the date a trade and the charges levied on it agree about, which
  -- neither of the dates a broker reports per row is: a trade settles days after
  -- a charge clears. See
  -- docs/adr/0051-a-posting-carries-an-order-date-and-a-trade-date.md.
  order_date                TIMESTAMPTZ NOT NULL,
  -- When the transaction took effect: a trade executed, a charge was levied,
  -- money moved. Equal to order_date for a source that reports one date and no
  -- other, which is a statement that the two coincide rather than that one is
  -- unknown.
  trade_date                TIMESTAMPTZ NOT NULL,

  instrument_description    TEXT NOT NULL,
  instrument_id             UUID,
  -- The currency line of that security the posting is on, and NULL where nothing
  -- said which. A posting names the security always and the line within it when
  -- it is known, so partial knowledge is the null rather than a sentinel row.
  -- The pair is held together by a foreign key below.
  --
  -- What a posting balances in is the security -- weight_commodity is
  -- 'inst:<uuid>' -- because the line is not available for every posting and a
  -- group's legs have to be weighed at one grain. The line decides which price
  -- series values the holding, which is a valuation question rather than a
  -- balance one. See docs/adr/0072-a-posting-names-a-security-and-a-line.md.
  listing_id                UUID,
  broker_tx_type            TEXT[] NOT NULL
                              CHECK (cardinality(broker_tx_type) > 0
                                     AND broker_tx_type <@ ARRAY['TRADE', 'TRADE_ASSET', 'TRADE_CASH',
                                                                 'INCOME', 'DIVIDEND', 'INTEREST',
                                                                 'RETURN_OF_CAPITAL', 'EXPENSE',
                                                                 'TRANSACTION_COST', 'HOLDING_COST',
                                                                 'FINANCING_COST', 'TRANSFER',
                                                                 'TRANSFER_INTERNAL', 'TRANSFER_EXTERNAL']),
  resolved_tx_type          TEXT NOT NULL
                              CHECK (resolved_tx_type IN ('TRADE', 'TRADE_ASSET', 'TRADE_CASH',
                                                          'INCOME', 'DIVIDEND', 'INTEREST',
                                                          'RETURN_OF_CAPITAL', 'EXPENSE',
                                                          'TRANSACTION_COST', 'HOLDING_COST',
                                                          'FINANCING_COST', 'TRANSFER',
                                                          'TRANSFER_INTERNAL', 'TRANSFER_EXTERNAL',
                                                          'AMBIGUOUS')),
  asset_class_hint          TEXT,

  quantity                  NUMERIC NOT NULL,
  unit_price                NUMERIC,
  trading_currency          TEXT,
  settlement_currency       TEXT,
  settlement_amount         NUMERIC CHECK (settlement_amount IS NULL OR settlement_amount >= 0),

  account_type              TEXT NOT NULL DEFAULT 'USER'
                              CHECK (account_type IN ('USER', 'EQUITY', 'INCOME', 'EXPENSE',
                                                      'IMBALANCE', 'TRANSFER_CLEARING',
                                                      'SOURCE_ROUNDING')),
  synthetic_purpose         TEXT CHECK (synthetic_purpose IS NULL
                                        OR synthetic_purpose IN ('INITIALIZE', 'RESIDUAL',
                                                                 'BOUNDARY')),

  group_id                  UUID NOT NULL REFERENCES tx_groups (id) ON DELETE CASCADE,
  weight                    NUMERIC NOT NULL,
  weight_commodity          TEXT NOT NULL,

  share_count_basis         DATE NOT NULL,
  split_adjusted_quantity   NUMERIC(38, 12) NOT NULL,
  split_adjusted_unit_price NUMERIC(38, 12),

  created_at                TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_txs_user_broker_time ON txs (user_id, broker, order_date);
CREATE INDEX idx_txs_group_id ON txs (group_id);

-- Why a posting might belong with another one, as its source stated it: an
-- identifier the source issued, what may be compared about it, and over what set
-- of postings. This is the evidence the transaction partition is derived from,
-- in place of the partition being stated outright. See
-- docs/adr/0048-correlations-declare-their-own-semantics.md.
--
-- A child table rather than columns on txs, because a posting carries however
-- many correlations its source supplies -- a reference number and a counterparty
-- pointer are two different series -- and a column per series would grow the
-- posting one nullable field per broker.
--
-- ordinality preserves the order the source stated them in, so a posting written
-- and read back is the posting that was written. Nothing compares on it.
--
-- token is the identifier verbatim and ordinal is the number it carries, where
-- the converter knew how to take one. They are separate because proximity is
-- load-bearing and cannot be recovered from the token: an IBKR FITID reads
-- 20251015U10000018371888432, and an edit distance over such strings would make
-- 1000000 and 0999999 four edits apart while 1000001 and 2000001 are one.
--
-- scope says what the identifier is comparable over and matches what may be done
-- with it; both are the vocabularies in proto/type/v1/type.proto, spelled the
-- same here, in the proto and in an archive file.
--
-- job_id is the ingestion job that supplied the correlation, and is what a
-- FILE-scoped one is comparable within: a file has no identity of its own once
-- its postings are rows. It is deliberately not a foreign key, for the reason
-- tx_groups.job_id is not -- a posting must outlive its job, and a pruned job row
-- still leaves an id that distinguishes one upload from another.
CREATE TABLE tx_correlations (
  tx_id        UUID NOT NULL REFERENCES txs (id) ON DELETE CASCADE,
  ordinality   INT NOT NULL,

  label        TEXT NOT NULL,
  token        TEXT NOT NULL,
  ordinal      BIGINT,
  scope        TEXT NOT NULL CHECK (scope IN ('FILE', 'ACCOUNT', 'BROKER')),
  matches      TEXT[] NOT NULL
                 CHECK (cardinality(matches) > 0
                        AND matches <@ ARRAY['EXACT', 'ORDINAL', 'ACCOUNT', 'ATTACHES']),
  ordinal_span BIGINT,

  job_id       UUID,

  PRIMARY KEY (tx_id, ordinality)
);

-- The lookup a grouping pass makes: everything correlated with this token in
-- this series. Postings are reached from here rather than the other way round,
-- since a pass starts from an identifier and asks who else holds it.
CREATE INDEX idx_tx_correlations_token ON tx_correlations (label, token);

-- Async ingestion jobs. status and validation_errors surfaced via front-end API.
-- job_type distinguishes tx uploads from archive imports; broker/source are
-- tx-specific.
-- payload stores the serialized protobuf request and is cleared after processing.
CREATE TABLE ingestion_jobs (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  job_type     TEXT NOT NULL DEFAULT 'tx' CHECK (job_type IN ('tx', 'system_archive', 'user_archive')),
  broker       TEXT,
  source       TEXT,
  filename     TEXT,
  period_from   TIMESTAMPTZ,
  period_before TIMESTAMPTZ,
  status       TEXT NOT NULL CHECK (status IN ('PENDING', 'RUNNING', 'SUCCESS', 'FAILED')),
  total_count      INT NOT NULL DEFAULT 0,
  processed_count  INT NOT NULL DEFAULT 0,
  payload      BYTEA,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ingestion_jobs_user ON ingestion_jobs (user_id);
CREATE INDEX idx_ingestion_jobs_status ON ingestion_jobs (status);

-- Per-part results for an archive import. One row per part the document
-- carried, created with the job so a page can render a row per part before the
-- worker has started and after a reload. part spells the ArchivePart enum name,
-- so the proto value, the column and the archive section are one string.
--
-- Restore order is a property of the archive format rather than of a row, so
-- there is no ordinal column: readers order by the enum.
CREATE TABLE ingestion_job_parts (
  job_id          UUID NOT NULL REFERENCES ingestion_jobs (id) ON DELETE CASCADE,
  part            TEXT NOT NULL CHECK (part IN ('INSTRUMENTS', 'PRICES', 'CORPORATE_EVENTS',
                                                'INFLATION_INDICES', 'FETCH_BLOCKS',
                                                'UNHANDLED_EVENTS', 'PLUGIN_CONFIG',
                                                'PREFERENCES', 'TXS', 'DECLARATIONS')),
  status          TEXT NOT NULL CHECK (status IN ('PENDING', 'RUNNING', 'SUCCESS', 'FAILED')),
  total_count     INT NOT NULL DEFAULT 0,
  processed_count INT NOT NULL DEFAULT 0,
  message         TEXT,
  PRIMARY KEY (job_id, part)
);

-- Validation errors for async ingestion. row_index, field, message per API.
-- part attributes an error to one part of an archive; NULL for a tx job
-- and for a failure that happened before any part was reached.
CREATE TABLE validation_errors (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_id     UUID NOT NULL REFERENCES ingestion_jobs (id) ON DELETE CASCADE,
  part       TEXT,
  row_index  INT NOT NULL,
  field      TEXT NOT NULL,
  message    TEXT NOT NULL
);

CREATE INDEX idx_validation_errors_job_id ON validation_errors (job_id);

-- ISO 10383 MIC (Market Identifier Code) reference data.
-- operating_mic FK is DEFERRABLE because operating MICs self-reference (operating_mic = mic)
-- and segment MICs reference their parent; all rows are inserted in a single transaction.
CREATE TABLE exchanges (
  mic           TEXT PRIMARY KEY,
  country       TEXT NOT NULL,
  country_code  TEXT NOT NULL,
  operating_mic TEXT NOT NULL REFERENCES exchanges(mic) DEFERRABLE INITIALLY DEFERRED,
  mic_type      TEXT NOT NULL CHECK (mic_type IN ('O', 'S')),
  name          TEXT NOT NULL,
  acronym       TEXT,
  city          TEXT
);

-- Canonical instruments (security master).
-- asset_class: controlled vocabulary. OPTION and FUTURE require
-- underlying_listing_id.
-- name: denormalized display name, computed by trigger from identifier priority:
--   MIC_TICKER > OPENFIGI_TICKER > OCC > BROKER_DESCRIPTION > CURRENCY > FX_PAIR > (existing name) > id::text.
-- exchange: denormalized short exchange label, computed by trigger:
--   exchanges.acronym (via exchange_mic) > OPENFIGI_TICKER domain > ''.
CREATE TABLE instruments (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  asset_class  TEXT CHECK (asset_class IS NULL OR asset_class IN ('STOCK','ETF','FIXED_INCOME','MUTUAL_FUND','OPTION','FUTURE','CASH','FX','UNKNOWN')),
  exchange_mic TEXT REFERENCES exchanges(mic),
  currency     TEXT,
  name         TEXT,
  exchange     TEXT NOT NULL DEFAULT '',
  -- The line an OPTION or FUTURE delivers. A contract's strike is a price and a
  -- price is in a currency, so the deliverable is one currency line of the
  -- underlying security rather than the security itself: naming the security
  -- leaves the strike's currency unstated. It is required to be a line with a
  -- currency -- an underlying whose currency is unknown gives no way to read the
  -- strike -- which the write path enforces, there being no cross-table CHECK to
  -- state it in. See
  -- docs/adr/0074-an-options-underlying-is-the-line-its-strike-is-quoted-in.md.
  --
  -- The foreign key waits until instrument_listings exists, below.
  underlying_listing_id UUID,
  cik          TEXT,
  sic_code     TEXT,
  -- Denormalized from the OCC identifier for options. NULL for non-options.
  strike       NUMERIC,
  expiry       DATE,
  put_call     TEXT CHECK (put_call IS NULL OR put_call IN ('C', 'P')),
  -- Deliverable multiplier: 1 = standard (100 shares/contract). Non-standard
  -- splits (e.g. 3:2) may set this to 1.5 meaning 150 shares/contract.
  contract_multiplier NUMERIC NOT NULL DEFAULT 1,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT chk_underlying_required CHECK (
    (asset_class IN ('OPTION','FUTURE') AND underlying_listing_id IS NOT NULL)
    OR (asset_class IS NULL OR asset_class NOT IN ('OPTION','FUTURE'))
  ),
  CONSTRAINT chk_option_fields CHECK (
    (asset_class = 'OPTION' AND strike IS NOT NULL AND expiry IS NOT NULL AND put_call IS NOT NULL)
    OR asset_class IS DISTINCT FROM 'OPTION'
  )
);

CREATE INDEX idx_instruments_underlying_listing_id ON instruments (underlying_listing_id);

-- Identifiers for an instrument. (identifier_type, domain, value) names the
-- instrument over the half-open [valid_from, valid_before) interval the name was
-- correct for it; two instruments may hold one value over disjoint intervals.
-- identifier_type: proto IdentifierType name (ISIN, CUSIP, TICKER, OPENFIGI_GLOBAL, OPENFIGI_SHARE_CLASS, OPENFIGI_COMPOSITE, BROKER_DESCRIPTION, etc.).
-- domain: optional; for BROKER_DESCRIPTION = source (e.g. 'Fidelity:web:fidelity-csv'); for TICKER = exchange code when present.
-- canonical = false only for BROKER_DESCRIPTION identifiers; canonical = true for standard identifiers.
-- Surrogate PK so domain can be NULL (PostgreSQL PK columns are NOT NULL).
--
-- valid_from is the point in market time the name became correct for the
-- instrument: the vintage of the source that supplied it, or the ex_date of the
-- split that minted it. A NULL valid_before means it is the name the instrument
-- wears now, and a NULL valid_from means the name predates everything we know.
-- The bounds are what decide whether an option's OCC symbol still needs
-- restating, in place of a single stamp on the instrument. See
-- docs/adr/0055-identifier-validity-is-an-interval.md,
-- docs/adr/0018-half-open-date-intervals.md and docs/spec/bitemporality.md.
CREATE TABLE instrument_identifiers (
  id              UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
  instrument_id   UUID NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  identifier_type TEXT NOT NULL,
  domain          TEXT,
  value           TEXT NOT NULL,
  canonical       BOOLEAN NOT NULL DEFAULT true,
  valid_from      DATE,
  valid_before    DATE,
  CONSTRAINT chk_instrument_identifiers_interval CHECK (
    valid_from IS NULL OR valid_before IS NULL OR valid_from < valid_before
  ),
  -- One name denotes one instrument at a time. This replaces the global unique
  -- index on (identifier_type, domain, value), which cannot survive retained
  -- history: a forward split halves every strike, so one option's new OCC symbol
  -- is character-for-character another's old one whenever the strike ladder
  -- overlaps itself.
  --
  -- The COALESCE is load-bearing. A GIST 'WITH =' on a NULL never conflicts,
  -- which is the opposite of what the partial unique indexes on a NULL domain
  -- achieved.
  --
  -- Being global, it also covers per-instrument uniqueness for overlapping rows
  -- while still allowing one instrument to hold a value over disjoint intervals.
  CONSTRAINT excl_instrument_identifiers_overlap EXCLUDE USING gist (
    identifier_type WITH =,
    COALESCE(domain, '') WITH =,
    value WITH =,
    daterange(valid_from, valid_before) WITH &&
  )
);

-- Domain-agnostic lookup by (identifier_type, value). Deliberately narrow: an
-- intermediate COALESCE(domain,'') column would block the prefix match on value
-- for the queries that actually reach this index (FX pair, ticker, ISIN lookups).
CREATE INDEX idx_instrument_identifiers_lookup ON instrument_identifiers (identifier_type, value);
-- Domain-aware lookup, previously served by the partial unique indexes the
-- exclusion constraint replaced. GIST answers equality far worse than btree, so
-- the constraint's index is not a substitute for this one.
CREATE INDEX idx_instrument_identifiers_lookup_domain ON instrument_identifiers (identifier_type, domain, value);

-- currency_family collapses a minor-unit currency onto the unit it is a prefix
-- of. GBX is pence sterling: the same currency as GBP under a different prefix,
-- so a provider quoting the London line in pence and another quoting it in
-- pounds name one listing, not two.
--
-- It governs the listing uniqueness index below and nothing else. It never
-- rewrites a stored code -- not a CURRENCY identifier, not an FX_PAIR, not
-- trading_currency, not a price -- because GBX and GBP are separately seeded
-- CASH instruments, GBX/USD and GBP/USD separately seeded FX instruments, and
-- valuation compares a currency to the display currency directly. Normalising
-- codes would collapse instruments that are deliberately distinct.
--
-- The body is a literal rather than a lookup because an index expression must be
-- IMMUTABLE. server/currency.MinorUnits is the declaration this restates, and
-- TestCurrencyFamily_matchesGoTable holds the two in lockstep.
-- See docs/adr/0068-a-listing-is-a-currency-of-a-security.md.
CREATE FUNCTION currency_family(code TEXT) RETURNS TEXT
    LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT CASE code WHEN 'GBX' THEN 'GBP' ELSE code END
$$;

-- A listing is one currency a security trades in. Currency and exchange are
-- facts about a listing rather than about the security above it: a security
-- listed in GBP and in USD may carry one ISIN, and one instruments row cannot
-- hold both, so a holding in one line becomes indistinguishable from a holding
-- in the other and the portfolio is wrong by an FX rate.
--
-- Venue is an attribute of a listing rather than part of its identity. Two
-- venues quoting one security in one currency differ by a spread, so holdings on
-- them are fungible and distinguishing them would be spurious; two currencies
-- differ by an FX rate, and only that makes two holdings non-fungible. A
-- listing's venues live in listing_venues below.
--
-- valid_from and valid_before are the half-open interval the line was tradeable
-- in: a delisting closes one, and the security above has no interval of its own.
-- Descriptive for now -- nothing filters on them and nothing closes a listing
-- yet. See docs/spec/bitemporality.md and
-- docs/adr/0068-a-listing-is-a-currency-of-a-security.md.
CREATE TABLE instrument_listings (
  id            UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
  instrument_id UUID NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  -- The code the line is actually quoted in, not its family: GBX stays GBX so
  -- price magnitudes and the existing scaling logic are untouched. A line
  -- carries a currency or does not exist: a security nobody has named a line for
  -- holds none, and the listing-grain names it holds name no line. See
  -- docs/adr/0075-a-name-that-could-not-be-placed-names-no-line.md.
  currency      TEXT NOT NULL,
  valid_from    DATE,
  valid_before  DATE,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT chk_instrument_listings_interval CHECK (
    valid_from IS NULL OR valid_before IS NULL OR valid_from < valid_before
  ),
  -- Redundant on its own -- id is already the primary key -- and declared so
  -- that (instrument_id, id) can be a foreign key target. A table naming both a
  -- security and one of its lines references this pair, which is what makes the
  -- two disagreeing unrepresentable rather than merely unintended. See
  -- docs/adr/0072-a-posting-names-a-security-and-a-line.md.
  --
  -- Its index leads on instrument_id, so it also serves the plain lookup by
  -- security that neither partial index below can: the planner cannot use a
  -- partial index without matching its predicate, and the read path selects a
  -- security's listings without knowing which kind it wants.
  CONSTRAINT uq_instrument_listings_of_instrument UNIQUE (instrument_id, id)
);

-- One listing per security per currency family. The family and not the code, so
-- a provider quoting the London line in pence and another quoting it in pounds
-- name one line rather than forking it in two.
CREATE UNIQUE INDEX uq_instrument_listings_currency ON instrument_listings (instrument_id, currency_family(currency));

-- The line a derivative delivers. The column is declared with the rest of
-- instruments; only the foreign key waits until here, because
-- instrument_listings references instruments and so is created after it.
--
-- No ON DELETE, for the reason txs_listing_id_fkey gives: a merge repoints its
-- loser's derivatives before deleting the loser, and one that forgot to should
-- fail rather than quietly cascade. That matches what underlying_id did.
ALTER TABLE instruments ADD CONSTRAINT instruments_underlying_listing_id_fkey
  FOREIGN KEY (underlying_listing_id) REFERENCES instrument_listings (id);

-- The venues a listing is admitted to. A set rather than a column, because a
-- venue migration is a change to this set rather than an event, and because two
-- venues quoting one line are one listing. Derived from the listing's
-- listing-grain identifiers by trigger, in the pattern recompute_instrument_name
-- follows, which keeps a real foreign key to exchanges while making divergence
-- between the two unrepresentable. Declared here and populated when there are
-- listing-grain identifiers to derive it from.
CREATE TABLE listing_venues (
  listing_id UUID NOT NULL REFERENCES instrument_listings (id) ON DELETE CASCADE,
  mic        TEXT NOT NULL REFERENCES exchanges (mic),
  PRIMARY KEY (listing_id, mic)
);

CREATE INDEX idx_listing_venues_mic ON listing_venues (mic);

-- Identifiers that name one listing of a security rather than the security
-- itself: a ticker, a SEDOL, a composite FIGI. identifier.NamesAListing decides
-- which of the two tables a row lands in, and nothing reads them
-- polymorphically -- a caller knows the grain of the type it is asking about
-- before it asks.
--
-- The split is what gives a listing fact somewhere to live. Currency and
-- exchange are facts about a listing, and while every identifier hung off the
-- security a currency learned from the London line was asserted of the New York
-- one; the rules that suppressed that consequence are removed rather than ported.
-- See docs/adr/0068-a-listing-is-a-currency-of-a-security.md.
--
-- Every column below means what the matching column on instrument_identifiers
-- means, including the half-open [valid_from, valid_before) interval and what a
-- NULL bound says. Only the thing named differs.
--
-- The security and the line are carried as a pair, as a posting carries them: a
-- result may supply a ticker or a SEDOL without supplying a currency, and then
-- nothing says which line the name is on. NULL is that state, and it is the only
-- one -- there is no listing standing in for it. See
-- docs/adr/0075-a-name-that-could-not-be-placed-names-no-line.md.
CREATE TABLE instrument_listing_identifiers (
  id              UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
  instrument_id   UUID NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  listing_id      UUID,
  identifier_type TEXT NOT NULL,
  domain          TEXT,
  value           TEXT NOT NULL,
  canonical       BOOLEAN NOT NULL DEFAULT true,
  valid_from      DATE,
  valid_before    DATE,
  CONSTRAINT chk_instrument_listing_identifiers_interval CHECK (
    valid_from IS NULL OR valid_before IS NULL OR valid_from < valid_before
  ),
  -- One name denotes one listing at a time, instance-wide, exactly as
  -- excl_instrument_identifiers_overlap does one instrument. Global rather than
  -- per-listing for the same reason: a triple held by two listings at once is
  -- the ambiguity the constraint exists to refuse, and it is no less ambiguous
  -- for the two listings belonging to one security.
  --
  -- The COALESCE is load-bearing there and here: a GIST 'WITH =' on a NULL never
  -- conflicts, so an absent domain has to be spelled as a value.
  CONSTRAINT excl_instrument_listing_identifiers_overlap EXCLUDE USING gist (
    identifier_type WITH =,
    COALESCE(domain, '') WITH =,
    value WITH =,
    daterange(valid_from, valid_before) WITH &&
  ),
  -- The line and the security it belongs to, referenced as a pair, so a name
  -- cannot be filed on a line of some other security. MATCH SIMPLE skips the
  -- check when the line is null, which is what makes the unplaced name
  -- representable; instrument_id is NOT NULL, so there is no second case for a
  -- CHECK to close.
  --
  -- ON DELETE CASCADE because a line's names go with the line, which is what the
  -- reference to instrument_listings did before the pair. A merge moves them
  -- first; this is the backstop, not the mechanism. An unplaced name matches no
  -- listing and so survives one being deleted, which is right -- it never named
  -- that line.
  FOREIGN KEY (instrument_id, listing_id)
    REFERENCES instrument_listings (instrument_id, id) MATCH SIMPLE ON DELETE CASCADE
);

-- The same pair of lookup indexes instrument_identifiers carries, and for the
-- same reasons: the domain-agnostic one is kept narrow so a prefix match on
-- value still reaches it, and GIST answers equality far worse than btree, so the
-- exclusion constraint's index is no substitute for the domain-aware one.
CREATE INDEX idx_instrument_listing_identifiers_lookup ON instrument_listing_identifiers (identifier_type, value);
CREATE INDEX idx_instrument_listing_identifiers_lookup_domain ON instrument_listing_identifiers (identifier_type, domain, value);
CREATE INDEX idx_instrument_listing_identifiers_listing ON instrument_listing_identifiers (listing_id);
-- The read that asks what a security is named at listing grain, which is the
-- name trigger and every export: it leads with the security because a name on no
-- line has no listing to lead with.
CREATE INDEX idx_instrument_listing_identifiers_instrument ON instrument_listing_identifiers (instrument_id);

-- Provider-specific instrument identifiers. Mirrors instrument_identifiers but
-- scoped to a data provider. Identifier types are free-form strings specific to
-- the provider (e.g. "SEGMENT_MIC_TICKER", "EODHD_EXCH_CODE", "FIGI").
CREATE TABLE provider_instrument_identifiers (
  id              UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
  instrument_id   UUID NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  provider        TEXT NOT NULL,
  identifier_type TEXT NOT NULL,
  domain          TEXT,
  value           TEXT NOT NULL
);

-- Per-instrument per-provider uniqueness. Both read paths -- loadProviderIdentifiers
-- and FindProviderIdentifiers -- lead with instrument_id and are served by these
-- indexes, so no separate lookup index is needed.
CREATE UNIQUE INDEX idx_prov_instr_ident_unique_null_domain ON provider_instrument_identifiers (instrument_id, provider, identifier_type, value) WHERE domain IS NULL;
CREATE UNIQUE INDEX idx_prov_instr_ident_unique_non_null_domain ON provider_instrument_identifiers (instrument_id, provider, identifier_type, domain, value) WHERE domain IS NOT NULL;

-- Provider identifiers that name a listing, split from the table above on the
-- same axis and by the same rule -- identifier.ProviderNamesAListing, which
-- declares grain for the provider types that exist. All three of them name a
-- listing today, so the table above holds nothing at present; it stays because a
-- provider type nobody has classified reads as security-grain, which attaches it
-- to a row that certainly exists rather than to a listing something had to pick.
-- The security and the line are a pair here for the reason they are a pair on
-- the canonical table above, and the foreign key is shaped the same way.
CREATE TABLE provider_listing_identifiers (
  id              UUID NOT NULL DEFAULT gen_random_uuid() PRIMARY KEY,
  instrument_id   UUID NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  listing_id      UUID,
  provider        TEXT NOT NULL,
  identifier_type TEXT NOT NULL,
  domain          TEXT,
  value           TEXT NOT NULL,
  FOREIGN KEY (instrument_id, listing_id)
    REFERENCES instrument_listings (instrument_id, id) MATCH SIMPLE ON DELETE CASCADE
);

-- Uniqueness per line where the name is on one, and per security where it is on
-- none: two partial indexes rather than one over a nullable column, which would
-- call two unplaced copies of one name distinct. Same shape and same reason as
-- uq_holding_declarations_on_listing and its twin.
--
-- The domain is spelled with COALESCE rather than split into a second pair of
-- indexes, and it is load-bearing for the same reason it is on the exclusion
-- constraint above: an absent domain has to be spelled as a value to compare
-- equal. No path stores an empty domain -- an empty string is written as NULL --
-- so the two cannot collide.
CREATE UNIQUE INDEX idx_prov_listing_ident_unique_on_listing
  ON provider_listing_identifiers (listing_id, provider, identifier_type, COALESCE(domain, ''), value)
  WHERE listing_id IS NOT NULL;
CREATE UNIQUE INDEX idx_prov_listing_ident_unique_no_listing
  ON provider_listing_identifiers (instrument_id, provider, identifier_type, COALESCE(domain, ''), value)
  WHERE listing_id IS NULL;

-- Trigger: recompute instruments.name and instruments.exchange whenever an
-- identifier at either grain, a listing, or the instrument itself changes.
-- Fires AFTER so that all rows are visible.
--
-- It sits below the listing tables because it reads them. MIC_TICKER ranks first
-- in the name priority and is listing-grain, so a version reading only
-- instrument_identifiers would name every equity by its broker description.
--
-- Only names still in force are considered. An instrument that has worn two OCC
-- symbols holds both, and without the filter the priority order would fall
-- through to the lexicographically smaller value rather than the current one.
--
-- No listing is primary. Naming one would reintroduce the conflation between a
-- default listing and an unknown one that the level exists to remove, so the
-- security's name is a label its listings share rather than a claim about which
-- of them matters. Where a user has to tell two listings apart, the currency is
-- what tells them apart. See
-- docs/adr/0069-a-listing-is-named-by-a-security-identifier-and-a-currency.md.
CREATE OR REPLACE FUNCTION recompute_instrument_name() RETURNS TRIGGER AS $$
DECLARE
  instr_id UUID;
BEGIN
  CASE TG_TABLE_NAME
    WHEN 'instrument_identifiers' THEN
      instr_id := COALESCE(NEW.instrument_id, OLD.instrument_id);
    WHEN 'instrument_listing_identifiers' THEN
      -- A listing-grain row names its security outright, whether or not anything
      -- placed it on one of that security's lines. Reaching the security through
      -- the listing would drop the unplaced ones, which name the security no less
      -- for naming no line.
      instr_id := COALESCE(NEW.instrument_id, OLD.instrument_id);
    WHEN 'instrument_listings' THEN
      instr_id := COALESCE(NEW.instrument_id, OLD.instrument_id);
    ELSE
      instr_id := NEW.id;
  END CASE;

  IF instr_id IS NULL THEN
    RETURN NULL;
  END IF;

  UPDATE instruments SET
    name = COALESCE(
      -- Each branch selects only the types of its own grain, so the two are
      -- read separately and joined here rather than searched together. The
      -- currency comes from the listing a listing-grain name belongs to and is
      -- null for every security-grain one, which costs nothing: type priority
      -- sorts first and the branches contribute disjoint types, so the currency
      -- only ever tie-breaks among a security's listings.
      --
      -- Nulls last within a type is "prefer a name that is on a line": a name
      -- nobody could place is a last resort rather than a first choice.
      (SELECT n.value FROM (
         SELECT ii.identifier_type, ii.domain, ii.value, NULL::text AS currency
         FROM instrument_identifiers ii
         WHERE ii.instrument_id = instr_id
           AND ii.valid_before IS NULL
           AND ii.identifier_type IN ('OCC','BROKER_DESCRIPTION','CURRENCY','FX_PAIR')
         UNION ALL
         SELECT li.identifier_type, li.domain, li.value, l.currency
         FROM instrument_listing_identifiers li
         LEFT JOIN instrument_listings l ON l.id = li.listing_id
         WHERE li.instrument_id = instr_id
           AND li.valid_before IS NULL
           AND li.identifier_type IN ('MIC_TICKER','OPENFIGI_TICKER')
       ) n
       ORDER BY CASE n.identifier_type
         WHEN 'MIC_TICKER' THEN 0 WHEN 'OPENFIGI_TICKER' THEN 1
         WHEN 'OCC' THEN 2 WHEN 'BROKER_DESCRIPTION' THEN 3
         WHEN 'CURRENCY' THEN 4 WHEN 'FX_PAIR' THEN 5
       END, n.currency IS NULL, n.currency, n.domain, n.value LIMIT 1),
      NULLIF(instruments.name, ''),
      instr_id::text
    ),
    exchange = COALESCE(
      (SELECT e.acronym FROM exchanges e WHERE e.mic = instruments.exchange_mic),
      (SELECT li.domain FROM instrument_listing_identifiers li
       LEFT JOIN instrument_listings l ON l.id = li.listing_id
       WHERE li.instrument_id = instr_id AND li.identifier_type = 'OPENFIGI_TICKER'
         AND li.valid_before IS NULL
         AND li.domain IS NOT NULL AND li.domain <> ''
       ORDER BY l.currency IS NULL, l.currency, li.domain LIMIT 1),
      ''
    )
  WHERE id = instr_id;

  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- Recompute on identifier changes, at either grain.
CREATE TRIGGER trg_recompute_instrument_name_on_ident
  AFTER INSERT OR UPDATE OR DELETE ON instrument_identifiers
  FOR EACH ROW EXECUTE FUNCTION recompute_instrument_name();

CREATE TRIGGER trg_recompute_instrument_name_on_listing_ident
  AFTER INSERT OR UPDATE OR DELETE ON instrument_listing_identifiers
  FOR EACH ROW EXECUTE FUNCTION recompute_instrument_name();

-- Recompute on instrument creation or exchange_mic change.
-- Column-specific UPDATE OF avoids infinite loop (trigger only writes name/exchange).
CREATE TRIGGER trg_recompute_instrument_name_on_inst
  AFTER INSERT OR UPDATE OF exchange_mic ON instruments
  FOR EACH ROW EXECUTE FUNCTION recompute_instrument_name();

-- A listing is minted with no identifiers and its currency never changes
-- afterwards -- a redenomination closes the line and opens another -- so nothing
-- about a listing row can change which name a security takes. What can is a name
-- moving on to a line, which is an update on the trigger above.

-- Trigger: derive a listing's venues from its listing-grain identifiers.
--
-- A set rather than a column, because a venue migration is a change to this set
-- rather than an event, and because two venues quoting one line are one listing.
-- Deriving it keeps a real foreign key to exchanges while making divergence
-- between the identifiers and the venue set unrepresentable.
--
-- MIC_TICKER alone, because it is the only listing-grain type whose domain is a
-- MIC: an OPENFIGI_TICKER's domain is a composite exchange code, which names a
-- market rather than a venue and is deliberately not stored as one, and a SEDOL
-- and a composite FIGI carry no domain at all. SEGMENT_MIC_TICKER is a provider
-- identifier and is excluded too -- its segment MIC normalises to the operating
-- MIC the canonical row already carries, so it would add nothing and could only
-- disagree.
--
-- The join to exchanges is both the foreign key and the filter. A MIC the
-- reference table does not carry is dropped rather than failing the identifier
-- write, which is the same judgement the resolver makes when a candidate
-- proposes an unknown exchange: keep the ticker, lose the venue.
--
-- Recomputed whole rather than patched, in the pattern recompute_instrument_name
-- follows: a closed interval or a deleted row removes a venue as surely as an
-- insert adds one, and one statement pair says so for all three.
CREATE OR REPLACE FUNCTION recompute_listing_venues() RETURNS TRIGGER AS $$
DECLARE
  lstg_id UUID;
BEGIN
  lstg_id := COALESCE(NEW.listing_id, OLD.listing_id);
  -- A name nobody could place is on no line and so contributes no venue: a venue
  -- is a fact about a line, and this one names none.
  IF lstg_id IS NULL THEN
    RETURN NULL;
  END IF;
  -- The listing may have gone with the row, in which case its venues went too.
  IF NOT EXISTS (SELECT 1 FROM instrument_listings WHERE id = lstg_id) THEN
    RETURN NULL;
  END IF;

  DELETE FROM listing_venues WHERE listing_id = lstg_id;

  INSERT INTO listing_venues (listing_id, mic)
  SELECT DISTINCT lstg_id, li.domain
  FROM instrument_listing_identifiers li
  JOIN exchanges e ON e.mic = li.domain
  WHERE li.listing_id = lstg_id
    AND li.identifier_type = 'MIC_TICKER'
    AND li.canonical
    AND li.valid_before IS NULL
    AND li.domain IS NOT NULL AND li.domain <> '';

  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_recompute_listing_venues
  AFTER INSERT OR UPDATE OR DELETE ON instrument_listing_identifiers
  FOR EACH ROW EXECUTE FUNCTION recompute_listing_venues();


-- Plugin config: which plugins are enabled, precedence (unique per category), plugin-specific config.
-- category: 'identifier', 'candidate', 'price'.
-- Precedence constraints are DEFERRABLE so that two plugins' precedences can be swapped
-- within a single transaction without hitting a uniqueness violation mid-swap.
-- max_history_days is only used by price plugins; NULL = unlimited lookback.
CREATE TABLE plugin_config (
  plugin_id        TEXT NOT NULL,
  category         TEXT NOT NULL CHECK (category IN ('identifier', 'candidate', 'price', 'inflation', 'corporate_event')),
  enabled          BOOLEAN NOT NULL DEFAULT true,
  precedence       INT NOT NULL,
  config           JSONB,
  max_history_days INT,
  PRIMARY KEY (plugin_id, category),
  UNIQUE (category, precedence) DEFERRABLE INITIALLY IMMEDIATE
);

-- Blocked (listing, plugin) pairs that should not be retried.
--
-- The listing rather than the security, matching the unit that was actually
-- fetched: a provider carrying the USD line of a security and not its GBP one
-- refuses the one and answers the other, and a block keyed on the security would
-- either lose a line the provider carries or stop asking about every line of one
-- it does not.
--
-- first_blocked_at is when the pair was first blocked and is never overwritten;
-- re-blocking updates only the reason. See docs/spec/bitemporality.md.
CREATE TABLE price_fetch_blocks (
  listing_id      UUID NOT NULL REFERENCES instrument_listings(id) ON DELETE CASCADE,
  plugin_id       TEXT NOT NULL,
  reason          TEXT NOT NULL,
  first_blocked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (listing_id, plugin_id)
);

-- Monthly inflation index values per currency. Index values are relative to a
-- base year where July 1st = 100. Different providers/currencies may use
-- different base years (e.g. ONS CPIH uses 2015=100).
CREATE TABLE inflation_indices (
  currency      TEXT        NOT NULL,              -- ISO 4217 (e.g. 'GBP')
  month         DATE        NOT NULL,              -- 1st of month UTC
  index_value   NUMERIC     NOT NULL,              -- relative to base_year July=100
  base_year     INT         NOT NULL,              -- year where July = 100
  data_provider TEXT        NOT NULL,              -- plugin ID
  -- Staleness only: overwritten on every refresh. Index values are not
  -- versioned by design: a revision replaces its predecessor in place and
  -- leaves no record. See docs/spec/bitemporality.md.
  last_fetched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (currency, month)
);

-- Identification errors for a job (e.g. plugin timeout, broker description only).
CREATE TABLE identification_errors (
  id                    UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  job_id                UUID NOT NULL REFERENCES ingestion_jobs (id) ON DELETE CASCADE,
  row_index             INT NOT NULL,
  instrument_description TEXT NOT NULL,
  message               TEXT NOT NULL
);

CREATE INDEX idx_identification_errors_job_id ON identification_errors (job_id);

-- Link txs to instruments. Every tx has an instrument (plugin-resolved or broker description only).
-- The column is declared with the rest of txs; only the foreign key waits until here,
-- because instruments is created after txs.
ALTER TABLE txs ADD CONSTRAINT txs_instrument_id_fkey
  FOREIGN KEY (instrument_id) REFERENCES instruments (id);

CREATE INDEX idx_txs_instrument_id ON txs (instrument_id);

-- The line and the security it belongs to, referenced as a pair, so a posting
-- cannot name a line of some other security. MATCH SIMPLE -- the default, spelled
-- out because it is the whole reason the constraint is shaped this way -- skips
-- the check when either column is null, which is what lets a posting name a
-- security whose line is not known.
--
-- No ON DELETE. A merge repoints its loser's postings before deleting the loser,
-- and a merge that forgot to should fail rather than quietly cascade.
ALTER TABLE txs ADD CONSTRAINT txs_listing_id_fkey
  FOREIGN KEY (instrument_id, listing_id)
  REFERENCES instrument_listings (instrument_id, id) MATCH SIMPLE;

-- MATCH SIMPLE also skips the check when only instrument_id is null, so without
-- this a posting could name a line and no security at all.
ALTER TABLE txs ADD CONSTRAINT chk_txs_listing_needs_instrument
  CHECK (instrument_id IS NOT NULL OR listing_id IS NULL);

CREATE INDEX idx_txs_listing_id ON txs (listing_id);

-- Residual postings -- the IMBALANCE, TRANSFER_CLEARING and SOURCE_ROUNDING legs
-- routed to balance a group its source data left one-sided -- are a small minority of
-- txs, and the report that aggregates them reads every one across all users. A partial
-- index over just those rows answers it without carrying the USER postings that
-- dominate the table. The key is order_date because the report filters on a time window
-- and nothing else on the residual subset; grouping cannot be index-ordered anyway,
-- because two GROUP BY keys come from the joined instruments table.
CREATE INDEX idx_txs_residual_postings
  ON txs (order_date)
  WHERE account_type IN ('IMBALANCE', 'TRANSFER_CLEARING', 'SOURCE_ROUNDING');

-- At most one INITIALIZE posting per holding per account type. account_type is part of
-- the key because the pad's counterparty is an equal-and-opposite posting of the same
-- instrument in the same broker account, and is synthetic for the same reason the pad
-- is; without it the two collide.
--
-- The line is part of what makes a holding, so it is part of the key: a security
-- quoted in two currencies is two holdings and takes two pads. Two partial
-- indexes because the line is nullable and a key over a nullable column would
-- call two pads of the same unplaced holding distinct -- the same shape as
-- uq_holding_declarations_on_listing and uq_holding_declarations_no_listing, and
-- for the same reason.
CREATE UNIQUE INDEX idx_txs_initialize_unique_on_listing
  ON txs (user_id, broker, account, instrument_id, listing_id, account_type)
  WHERE synthetic_purpose = 'INITIALIZE' AND listing_id IS NOT NULL;
CREATE UNIQUE INDEX idx_txs_initialize_unique_no_listing
  ON txs (user_id, broker, account, instrument_id, account_type)
  WHERE synthetic_purpose = 'INITIALIZE' AND listing_id IS NULL;

-- The two sides of a transfer, paired after the fact.
--
-- Each side of a journal is balanced by a TRANSFER_CLEARING counterparty and the two
-- are deliberately not paired at ingest, because brokers report them in separate
-- statements and sometimes in separate imports. This is the pairing. It records which
-- group -- and so which account -- holds the other side, because that identity is what
-- the portfolio membership test in docs/adr/0022-typed-per-account-cash-flow-boundary.md
-- consumes; that a match merely exists would not answer it.
--
-- A link rather than a status column on the posting, which could say that a side was
-- matched but not what it matched with. And not a synthetic third group closing both
-- sides out, which would fabricate an economic event that never happened and have to be
-- unpicked on rematch. Both alternatives are also unreachable: check_tx_group_balance()
-- rejects mutating or deleting a single leg of a group.
--
-- Derived and disposable. A re-upload replaces one side's groups
-- (docs/adr/0002-transaction-ingestion-model.md), the cascade takes the link with them,
-- the surviving side reappears as unmatched and the matcher runs again. It is never
-- authoritative and is always cheap to rebuild.
--
-- Directed. from_group_id is the side the value left: its TRANSFER_CLEARING residual is
-- positive, because the group's own leg is negative and the clearing leg holds what is
-- owed out. to_group_id is where it arrived. The direction costs nothing to record --
-- it is the sign of the residual -- and a link that did not carry it would read the
-- same for a transfer in either direction.
--
-- Keyed per (group, commodity) rather than per group, which is what the two unique
-- indexes say. Balancing emits one residual per commodity, so an unpaired security transfer group
-- can have a security side and a cash side in flight independently and each pairs with
-- a different group. instrument_id moves with an instrument merge, alongside
-- txs.instrument_id.
--
-- The line each side sat on is recorded and is not part of the key. A residual is
-- weighed per security-grain commodity and carries a line only where every leg it
-- balances shares one, so the security is the only grain every side has -- the same
-- argument weight_commodity is decided by. Keying on the line would also make the
-- one case these columns exist for unrepresentable: a broker converting a holding
-- between two currency lines of one security is quantity-preserving and so is a
-- transfer, and its two sides are on different lines by definition. See
-- docs/adr/0072-a-posting-names-a-security-and-a-line.md.
--
-- Neither the quantity nor the ingestion job is stored. The two sides are equal and
-- opposite by construction, so a stored amount could only disagree with them, and a
-- match is not made by an ingestion job.
--
-- user_id must be the owner of both groups, and both groups must have the same owner.
-- Neither is checked here: a CHECK cannot reach another table and a trigger to enforce
-- it would run on every insert to catch a case no caller can produce. The matcher
-- partitions its candidates by user before proposing anything, so a cross-user pair
-- cannot be built. The column is a denormalisation for query scoping -- deleting a
-- user already cascades through tx_groups -- and the invariant is the caller's.
CREATE TABLE transfer_matches (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id       UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  from_group_id UUID NOT NULL REFERENCES tx_groups (id) ON DELETE CASCADE,
  to_group_id   UUID NOT NULL REFERENCES tx_groups (id) ON DELETE CASCADE,
  instrument_id UUID NOT NULL REFERENCES instruments (id),
  -- The currency line each side's residual was on, and NULL where it named none.
  -- A holding is per line, so a link between two holdings says which two, and a
  -- departure and an arrival on different lines is a currency conversion rather
  -- than a mismatch.
  from_listing_id UUID,
  to_listing_id   UUID,
  -- How the pair was found. POINTER is the source naming the other account outright;
  -- REFERENCE is the proximity of the two sides' broker references; MANUAL is a person
  -- saying so. There is no amount-and-window-alone value: a pair with no evidence
  -- beyond an equal and opposite amount is left unmatched rather than guessed at.
  method        TEXT NOT NULL CHECK (method IN ('POINTER', 'REFERENCE', 'MANUAL')),
  matched_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (from_group_id <> to_group_id),
  -- Each line and the security it belongs to, referenced as a pair, so a match
  -- cannot name a line of some other security. MATCH SIMPLE skips the check when
  -- either column is null, which is what lets a side name a security whose line is
  -- not known; instrument_id is NOT NULL here, so unlike txs there is no second
  -- case for a CHECK to close.
  --
  -- No ON DELETE, for the reason txs_listing_id_fkey gives: a merge repoints its
  -- loser's rows before deleting the loser, and one that forgot to should fail
  -- rather than quietly cascade.
  FOREIGN KEY (instrument_id, from_listing_id)
    REFERENCES instrument_listings (instrument_id, id) MATCH SIMPLE,
  FOREIGN KEY (instrument_id, to_listing_id)
    REFERENCES instrument_listings (instrument_id, id) MATCH SIMPLE
);

-- No index on either listing column. The only predicate on them is the merge's,
-- which rides an UPDATE ... WHERE instrument_id that this table does not index
-- either: a link exists per unpaired transfer rather than per posting, so the
-- table is small enough that a scan is the plan anyway.

-- One match per side per commodity, in both directions. These are the uniqueness
-- constraint and the lookup index at once: what holds the other side of a group is
-- (from_group_id = g OR to_group_id = g), and neither side can be matched twice.
CREATE UNIQUE INDEX idx_transfer_matches_from ON transfer_matches (from_group_id, instrument_id);
CREATE UNIQUE INDEX idx_transfer_matches_to   ON transfer_matches (to_group_id, instrument_id);

-- Portfolio filter matching view: returns (portfolio_id, tx_id) pairs for txs
-- matching the portfolio's filters. Semantics: AND between categories (broker,
-- account, instrument), OR within each category. Categories with no filters are
-- unconstrained. Portfolios with zero filters match zero transactions.
CREATE VIEW portfolio_matched_txs AS
SELECT p.id AS portfolio_id, t.id AS tx_id
FROM portfolios p
JOIN txs t ON t.user_id = p.user_id
WHERE
  EXISTS (SELECT 1 FROM portfolio_filters WHERE portfolio_id = p.id)
  AND (NOT EXISTS (SELECT 1 FROM portfolio_filters WHERE portfolio_id = p.id AND filter_type = 'broker')
       OR t.broker IN (SELECT filter_value FROM portfolio_filters WHERE portfolio_id = p.id AND filter_type = 'broker'))
  AND (NOT EXISTS (SELECT 1 FROM portfolio_filters WHERE portfolio_id = p.id AND filter_type = 'account')
       OR t.account IN (SELECT filter_value FROM portfolio_filters WHERE portfolio_id = p.id AND filter_type = 'account'))
  AND (NOT EXISTS (SELECT 1 FROM portfolio_filters WHERE portfolio_id = p.id AND filter_type = 'instrument')
       OR (t.instrument_id IS NOT NULL
           AND t.instrument_id::text IN (SELECT filter_value FROM portfolio_filters WHERE portfolio_id = p.id AND filter_type = 'instrument')));

-- The TRANSFER_CLEARING postings a portfolio values: one side of a matched pair
-- whose other side matches the same portfolio's filters.
--
-- An in-flight balance is never a position, so holdings exclude it always. Valuation
-- is a different question. The two sides of a transfer are dated by their own
-- statements, so dropping both clearing legs makes the transferred holding vanish for
-- the days in between -- a dip in portfolio value and a fake return blip. Holding
-- value in transit is what a clearing account is for. An unmatched leg is never here:
-- including it would assert that money in transit is coming back to an account we
-- hold, which is the thing we do not know. See docs/spec/postings.md and
-- docs/adr/0022-typed-per-account-cash-flow-boundary.md.
--
-- The counterpart is tested through its own clearing leg rather than through its
-- group, because that leg carries the counterpart account and the commodity the match
-- is keyed on. That makes the test symmetric -- whichever of the two legs is being
-- tested asks the same question of the other -- so a pair is admitted whole or not at
-- all. A test against the counterpart's USER leg would not be, since an instrument
-- filter can admit one and not the other.
--
-- No date bound. A match is a fact about the pair rather than about a valuation
-- window, and value still in transit when a window closes is value held. Bounding it
-- would reinstate the dip for exactly the days the pairing exists to cover.
--
-- The CASE picks the counterpart group: a plain equality on idx_txs_group_id, which
-- CHECK (from_group_id <> to_group_id) makes unambiguous.
--
-- The counterpart's membership is an EXISTS rather than a fourth join, which reads
-- the same -- portfolio_matched_txs holds at most one row per (portfolio, tx) -- and
-- plans very differently. As a join the planner ordered it last and matched it with a
-- join filter, materialising the filter view and discarding 43,160 rows to keep 40,
-- which cost 90ms of a 490ms portfolio valuation. As a semi-join the equality reaches
-- the scan and the cost returns to the noise floor. Every row estimate in here is 1,
-- so the shape has to make the pushdown unavoidable rather than merely available.
CREATE VIEW portfolio_in_flight_txs AS
SELECT m.portfolio_id, t.id AS tx_id
FROM txs t
JOIN portfolio_matched_txs m ON m.tx_id = t.id
JOIN transfer_matches tm
  ON tm.instrument_id = t.instrument_id
 AND (tm.from_group_id = t.group_id OR tm.to_group_id = t.group_id)
JOIN txs c
  ON c.group_id = CASE WHEN tm.from_group_id = t.group_id
                       THEN tm.to_group_id ELSE tm.from_group_id END
 AND c.account_type = 'TRANSFER_CLEARING'
 AND c.instrument_id = tm.instrument_id
WHERE t.account_type = 'TRANSFER_CLEARING'
  AND EXISTS (SELECT 1 FROM portfolio_matched_txs cm
              WHERE cm.tx_id = c.id AND cm.portfolio_id = m.portfolio_id);

-- EOD price cache. Stores end-of-day OHLCV data per listing per date.
-- A price is quoted in a currency, so the bar belongs to the listing rather than
-- to the security above it: two currency lines of one security differ by an FX
-- rate, and a cache keyed on the security would hold whichever line the plugin
-- happened to fetch. A listing whose currency is unknown is never priceable and
-- so never appears here -- a price with no stated currency asserts nothing. See
-- docs/adr/0068-a-listing-is-a-currency-of-a-security.md.
-- Every row is a bar a provider actually reported: non-trading days (weekends,
-- holidays) simply have no row. Valuation carries the last close forward over
-- them at read time, bounded by price_coverage, so the filled series is derived
-- from (bars, coverage) rather than stored alongside them.
-- share_count_basis is the date at which the share count the raw OHLCV is
-- denominated in was current. It is declared by whoever supplied the row -- as
-- price_date for an as-traded series, or the fetch date for a provider that
-- back-adjusts -- and is never inferred from last_fetched_at. It defaults to
-- price_date, the as-traded assumption. See docs/spec/bitemporality.md.
-- The split_adjusted_* columns hold OHLCV after applying every stock split with
-- ex_date > share_count_basis for the security this listing belongs to. A split
-- is an action on the security and every one of its lines splits together. They equal the raw values when
-- no later split exists. close (NOT NULL) implies split_adjusted_close (NOT NULL);
-- the others are NULL iff their raw counterpart is NULL. Volume is adjusted in
-- the opposite direction (more shares trade in adjusted-share terms).
-- The raw OHLC are transcribed decimals and are exact, so they are bare NUMERIC.
-- The split_adjusted_* pair is not: the cumulative split factor is a rational and
-- a reverse /3 has no finite decimal form, so they declare a rounding scale of 12,
-- matching txs.split_adjusted_*. The rounding is confined to this derived cache.
-- See docs/adr/0028-cumulative-split-factor-is-an-exact-rational.md.
-- adjusted_close is preserved as-supplied by the data provider on the provider's
-- own basis (typically including dividend adjustment as well as splits). It is
-- never an input to valuation and exists to cross-check split_adjusted_close,
-- which is the value PortfolioDB derives itself.
CREATE TABLE eod_prices (
  listing_id             UUID        NOT NULL REFERENCES instrument_listings (id) ON DELETE CASCADE,
  price_date             DATE        NOT NULL,
  open                   NUMERIC,
  split_adjusted_open    NUMERIC(38, 12),
  high                   NUMERIC,
  split_adjusted_high    NUMERIC(38, 12),
  low                    NUMERIC,
  split_adjusted_low     NUMERIC(38, 12),
  close                  NUMERIC     NOT NULL,
  split_adjusted_close   NUMERIC(38, 12) NOT NULL,
  adjusted_close         NUMERIC,
  volume                 BIGINT,
  split_adjusted_volume  BIGINT,
  data_provider          TEXT        NOT NULL,
  share_count_basis      DATE        NOT NULL,
  -- Staleness only: when this row was last fetched. It carries no meaning about
  -- the prices themselves. See docs/spec/bitemporality.md.
  last_fetched_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (listing_id, price_date)
);

SELECT create_hypertable('eod_prices', 'price_date');

-- Coverage tracking for prices. A missing eod_prices row does not say whether
-- the date was never fetched or was fetched and had no price (pre-IPO,
-- delisted, suspended, or beyond a plugin's history limit). This table records,
-- per (listing, plugin), the date intervals the plugin has answered
-- authoritatively -- including answers that returned no bars at all, which are
-- coverage just as much as a full series is.
-- The grain is the listing, matching the bars it bounds: a provider that carries
-- the USD line of a security and not its GBP one has answered for one and not
-- the other, and a span keyed on the security could not say so.
-- The interval is half-open [covered_from, covered_before). Adjacent or
-- overlapping intervals for the same (listing, plugin) are merged on insert.
-- Every eod_prices row lies within some span here for its listing; the
-- converse does not hold, and a span with no rows in it is exactly the "asked,
-- nothing there" answer that row presence alone cannot express.
CREATE TABLE price_coverage (
  listing_id     UUID        NOT NULL REFERENCES instrument_listings (id) ON DELETE CASCADE,
  plugin_id      TEXT        NOT NULL,
  covered_from   DATE        NOT NULL,
  covered_before DATE        NOT NULL CHECK (covered_before > covered_from),
  -- Staleness only: when this span was last confirmed. A merged span keeps the
  -- oldest constituent value, since a union is only as freshly confirmed as its
  -- stalest part.
  last_fetched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (listing_id, plugin_id, covered_from)
);

CREATE INDEX idx_price_coverage_listing ON price_coverage (listing_id);

-- Coverage merged across plugins. Every caller asks the same question of this
-- table -- which date spans has anyone answered for -- and none of them cares
-- who answered: the valuation carry-forward needs a bound, the gap scan needs
-- what is already answered for, and the export needs what to write down. So the
-- per-plugin rows are unioned into the fewest non-overlapping spans covering
-- the same dates, once, here.
--
-- range_agg over a half-open daterange merges adjacent spans as well as
-- overlapping ones, which matters to a carry-forward partitioned by span: two
-- plugins covering [Jan, Feb) and [Feb, Mar) must come out as one span, or the
-- spurious boundary restarts the fill and the first day of February reads as
-- unpriced.
--
-- last_fetched_at is deliberately not carried. A merged span has no single
-- constituent to take it from, and no caller needs it yet. When one does, the
-- staleness rule stated on the table above -- a union is only as freshly
-- confirmed as its stalest part -- belongs here rather than at the call site.
--
-- A view, not a repeated subquery, so the merge has one definition. Postgres
-- inlines it, so a caller's listing_id predicate still reaches the aggregate:
-- listing_id is the grouping key.
CREATE VIEW merged_price_coverage AS
SELECT listing_id,
       lower(span) AS covered_from,
       upper(span) AS covered_before
FROM (
  SELECT listing_id,
         unnest(range_agg(daterange(covered_from, covered_before))) AS span
  FROM price_coverage
  GROUP BY listing_id
) s;

-- The one line a security-grain identifier names, for the callers that reach a
-- line through one. FX_PAIR is security-grain and a seeded pair has exactly one
-- line, which is what valuation and the price cache use this for.
--
-- A security with exactly one currency-bearing listing has one answer. A security
-- with two has none, and picking one would name a currency nobody stated. Holdings
-- do not come through here: a posting names its own line, and one that named none
-- reports unpriced rather than being resolved to a line by its security. See
-- docs/adr/0072-a-posting-names-a-security-and-a-line.md.
--
-- A security nobody has named a line for holds none and has no row here either.
CREATE VIEW instrument_priced_listing AS
SELECT instrument_id,
       (array_agg(id))[1]       AS listing_id,
       (array_agg(currency))[1] AS currency
FROM instrument_listings
GROUP BY instrument_id
HAVING count(*) = 1;

-- Stock splits per instrument. ex_date is the effective/execution date. The
-- split factor is split_to / split_from (e.g. 2:1 split = split_from=1,
-- split_to=2, factor=2; 1:2 reverse split = split_from=2, split_to=1, factor=0.5).
-- data_provider is the plugin id ("massive", "eodhd", ...), "import" for CSV/JSON
-- imports, or "broker:<broker>" for events sourced from a broker SPLIT tx.
CREATE TABLE stock_splits (
  instrument_id  UUID        NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  ex_date        DATE        NOT NULL,
  split_from     NUMERIC     NOT NULL CHECK (split_from > 0),
  split_to       NUMERIC     NOT NULL CHECK (split_to   > 0),
  data_provider  TEXT        NOT NULL,
  -- When we first learned of this split. It only ever moves backwards: revising
  -- the ratio leaves it alone, and an import supplying an earlier stamp restores
  -- that one. It is preserved across corporate-event export and import; option
  -- adjustment keys off ex_date rather than this column, because what matters
  -- there is when the split took effect, not when we heard about it.
  first_known_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (instrument_id, ex_date)
);

CREATE INDEX idx_stock_splits_instrument ON stock_splits (instrument_id);

-- Cash dividends per listing. ex_date is the ex-dividend date. amount is per
-- share. frequency is provider-supplied and may be NULL.
--
-- A dividend is paid in a currency, so it is a fact about one currency line
-- rather than about the security above it. Keyed on the security it collides on
-- its primary key the first time one ex-date pays in two currencies, and the
-- second payment is lost. See
-- docs/adr/0068-a-listing-is-a-currency-of-a-security.md.
--
-- currency is the code the amount is quoted in, not the line's own: a provider
-- quoting the London line's dividend in pence files against a line stored as
-- GBP, and deriving the unit from the listing would read that amount as pounds.
-- It agrees with the listing's currency family by construction, the write path
-- selecting the line from the stated currency rather than the other way round.
-- A currency matching no line of the security names no line at all, and such a
-- dividend is queued for review rather than filed here; see
-- docs/adr/0073-a-dividend-names-a-line-it-does-not-mint.md.
CREATE TABLE cash_dividends (
  listing_id       UUID        NOT NULL REFERENCES instrument_listings (id) ON DELETE CASCADE,
  ex_date          DATE        NOT NULL,
  pay_date         DATE,
  record_date      DATE,
  declaration_date DATE,
  amount           NUMERIC     NOT NULL CHECK (amount >= 0),
  currency         TEXT        NOT NULL,
  frequency        TEXT,
  type             TEXT        NOT NULL DEFAULT 'CD',
  data_provider    TEXT        NOT NULL,
  -- When we first learned of this dividend. Only ever moves backwards, as for
  -- stock_splits.first_known_at.
  first_known_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (listing_id, ex_date)
);

CREATE INDEX idx_cash_dividends_listing ON cash_dividends (listing_id);

-- Coverage tracking for corporate events. Events are sparse, so absence of an
-- event for a (instrument, date) does not tell us whether the data was fetched.
-- This table records, per (instrument, plugin), the date intervals for which
-- the plugin has been queried successfully (including queries that returned
-- empty results -- those are still authoritative coverage).
-- The interval is half-open [covered_from, covered_before). Adjacent or
-- overlapping intervals for the same (instrument, plugin) are merged on insert.
CREATE TABLE corporate_event_coverage (
  instrument_id  UUID        NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  plugin_id      TEXT        NOT NULL,
  covered_from   DATE        NOT NULL,
  covered_before DATE        NOT NULL CHECK (covered_before > covered_from),
  -- Staleness only: when this span was last confirmed. A merged span keeps the
  -- oldest constituent value, since a union is only as freshly confirmed as its
  -- stalest part.
  last_fetched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (instrument_id, plugin_id, covered_from)
);

CREATE INDEX idx_corporate_event_coverage_instrument ON corporate_event_coverage (instrument_id);

-- Coverage merged across plugins, as merged_price_coverage is for prices. See
-- the comment there for why the merge lives in a view.
CREATE VIEW merged_corporate_event_coverage AS
SELECT instrument_id,
       lower(span) AS covered_from,
       upper(span) AS covered_before
FROM (
  SELECT instrument_id,
         unnest(range_agg(daterange(covered_from, covered_before))) AS span
  FROM corporate_event_coverage
  GROUP BY instrument_id
) s;

-- Blocked (instrument, plugin) pairs for corporate-event fetches. Mirrors
-- price_fetch_blocks: an entry here means the plugin returned a permanent
-- error (404, 403, ...) for this instrument and should not be retried.
-- first_blocked_at is never overwritten; re-blocking updates only the reason.
CREATE TABLE corporate_event_fetch_blocks (
  instrument_id    UUID        NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  plugin_id        TEXT        NOT NULL,
  reason           TEXT        NOT NULL,
  first_blocked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (instrument_id, plugin_id)
);

-- Corporate events that cannot be automatically processed (reverse splits,
-- non-whole splits, mergers, extraordinary dividends on options, dividends in a
-- currency no line of the security is quoted in, futures adjustments). Surfaced
-- to admin users for manual review.
CREATE TABLE unhandled_corporate_events (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  instrument_id UUID        NOT NULL REFERENCES instruments (id) ON DELETE CASCADE,
  event_type    TEXT        NOT NULL,
  ex_date       DATE,
  detail        TEXT        NOT NULL,
  data          JSONB,
  resolved      BOOLEAN     NOT NULL DEFAULT false,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_unhandled_ce_unresolved
  ON unhandled_corporate_events (resolved) WHERE NOT resolved;

-- Prevent duplicate unresolved events for the same (instrument, type, date).
-- NULL ex_dates are treated as distinct by PostgreSQL unique indexes, which is
-- acceptable since events without an ex_date are rare edge cases.
CREATE UNIQUE INDEX idx_unhandled_ce_dedup
  ON unhandled_corporate_events (instrument_id, event_type, ex_date)
  WHERE NOT resolved;

-- Default the split_adjusted_* columns on txs to the raw counterparts whenever
-- they are not explicitly set. This keeps every existing INSERT/UPSERT path
-- working without modification: callers continue to set quantity / unit_price
-- and the trigger seeds split_adjusted_quantity / split_adjusted_unit_price to
-- the same value. The recompute pass (RecomputeTxSplitAdjustments) later
-- overwrites the adjusted columns when stock_splits exist for the instrument.
-- On UPDATE, the trigger only resets adjusted columns when the raw column
-- actually changes AND the adjusted column was not part of the same UPDATE,
-- so explicit recompute updates are preserved.
CREATE OR REPLACE FUNCTION default_split_adjusted_tx() RETURNS TRIGGER AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    -- As-traded is the default denomination: a row with no declared basis is
    -- assumed to be expressed in the share count current when the trade
    -- happened, which is trade_date rather than the date it was ordered.
    IF NEW.share_count_basis IS NULL THEN
      NEW.share_count_basis := NEW.trade_date::date;
    END IF;
    IF NEW.split_adjusted_quantity IS NULL THEN
      NEW.split_adjusted_quantity := NEW.quantity;
    END IF;
    IF NEW.split_adjusted_unit_price IS NULL THEN
      NEW.split_adjusted_unit_price := NEW.unit_price;
    END IF;
  ELSIF TG_OP = 'UPDATE' THEN
    IF NEW.quantity IS DISTINCT FROM OLD.quantity
       AND NEW.split_adjusted_quantity IS NOT DISTINCT FROM OLD.split_adjusted_quantity THEN
      NEW.split_adjusted_quantity := NEW.quantity;
    END IF;
    IF NEW.unit_price IS DISTINCT FROM OLD.unit_price
       AND NEW.split_adjusted_unit_price IS NOT DISTINCT FROM OLD.split_adjusted_unit_price THEN
      NEW.split_adjusted_unit_price := NEW.unit_price;
    END IF;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_default_split_adjusted_tx
  BEFORE INSERT OR UPDATE ON txs
  FOR EACH ROW EXECUTE FUNCTION default_split_adjusted_tx();

-- The postings of a tx group must sum to zero in every commodity they weigh in.
--
-- Enforcing this here rather than in the application makes it unbypassable: no code
-- path, no bad import and no manual psql session can leave an unbalanced group
-- behind. It is affordable because ingestion routes every non-zero residual to an
-- explicit counterparty, so balance is satisfiable by construction and turning the
-- check on cannot reject otherwise-valid data.
--
-- It reads the stored weight rather than re-deriving one. The weight rules live in
-- server/service/ingestion/balance.go, and a second copy in PL/pgSQL would both drift
-- from the Go one and re-derive at COMMIT from instrument state that has moved under
-- the posting since it was written -- which could reject a group that was balanced
-- when it was written, on an update that has nothing to do with it. See
-- docs/adr/0029-posting-weight-is-stored.md.
--
-- TG_OP branching rather than a CASE over NEW/OLD, because referencing NEW in a
-- DELETE raises whether or not the branch is taken. A wholly deleted group matches no
-- rows and passes vacuously, which is what lets replace-by-period delete groups and
-- let the cascade take their postings.
CREATE OR REPLACE FUNCTION check_tx_group_balance() RETURNS TRIGGER AS $$
DECLARE
  gids UUID[] := ARRAY[]::UUID[];
  gid  UUID;
  bad  RECORD;
BEGIN
  IF TG_OP <> 'INSERT' THEN
    gids := gids || OLD.group_id;
  END IF;
  IF TG_OP <> 'DELETE' THEN
    gids := gids || NEW.group_id;
  END IF;
  FOREACH gid IN ARRAY gids LOOP
    SELECT weight_commodity, SUM(weight) AS residual INTO bad
    FROM txs
    WHERE group_id = gid
    GROUP BY weight_commodity
    HAVING SUM(weight) <> 0
    LIMIT 1;
    IF FOUND THEN
      RAISE EXCEPTION 'tx group % does not balance: % left over in %',
        gid, bad.residual, bad.weight_commodity
        USING ERRCODE = 'check_violation';
    END IF;
  END LOOP;
  RETURN NULL;
END;
$$ LANGUAGE plpgsql;

-- Deferred to COMMIT so the legs of a group can be inserted in any order within one
-- transaction: a group is only ever balanced once all of it is written. There is
-- precedent for DEFERRABLE in this schema -- exchanges.operating_mic and the
-- plugin_config precedence uniqueness both use it.
CREATE CONSTRAINT TRIGGER trg_tx_group_balance
  AFTER INSERT OR DELETE ON txs
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW EXECUTE FUNCTION check_tx_group_balance();

-- Split into a second trigger because WHEN cannot reference OLD on an INSERT. The
-- guard is what keeps the split-adjustment recompute cheap: with no instrument filter
-- it rewrites split_adjusted_* for every posting in the table and leaves weight alone,
-- so without this every such row would queue a deferred check.
CREATE CONSTRAINT TRIGGER trg_tx_group_balance_update
  AFTER UPDATE ON txs
  DEFERRABLE INITIALLY DEFERRED
  FOR EACH ROW
  WHEN (OLD.weight IS DISTINCT FROM NEW.weight
     OR OLD.weight_commodity IS DISTINCT FROM NEW.weight_commodity
     OR OLD.group_id IS DISTINCT FROM NEW.group_id)
  EXECUTE FUNCTION check_tx_group_balance();

-- Same defaulting trigger for eod_prices. close (NOT NULL) implies
-- split_adjusted_close (NOT NULL); the OHLV columns are nullable on both
-- sides so the adjusted column is left NULL when the raw side is NULL.
CREATE OR REPLACE FUNCTION default_split_adjusted_eod_price() RETURNS TRIGGER AS $$
BEGIN
  IF TG_OP = 'INSERT' THEN
    -- As-traded is the default denomination: a bar with no declared basis is
    -- assumed to be expressed in the share count current on its own date.
    IF NEW.share_count_basis IS NULL THEN
      NEW.share_count_basis := NEW.price_date;
    END IF;
    IF NEW.split_adjusted_open IS NULL THEN
      NEW.split_adjusted_open := NEW.open;
    END IF;
    IF NEW.split_adjusted_high IS NULL THEN
      NEW.split_adjusted_high := NEW.high;
    END IF;
    IF NEW.split_adjusted_low IS NULL THEN
      NEW.split_adjusted_low := NEW.low;
    END IF;
    IF NEW.split_adjusted_close IS NULL THEN
      NEW.split_adjusted_close := NEW.close;
    END IF;
    IF NEW.split_adjusted_volume IS NULL THEN
      NEW.split_adjusted_volume := NEW.volume;
    END IF;
  ELSIF TG_OP = 'UPDATE' THEN
    IF NEW.open IS DISTINCT FROM OLD.open
       AND NEW.split_adjusted_open IS NOT DISTINCT FROM OLD.split_adjusted_open THEN
      NEW.split_adjusted_open := NEW.open;
    END IF;
    IF NEW.high IS DISTINCT FROM OLD.high
       AND NEW.split_adjusted_high IS NOT DISTINCT FROM OLD.split_adjusted_high THEN
      NEW.split_adjusted_high := NEW.high;
    END IF;
    IF NEW.low IS DISTINCT FROM OLD.low
       AND NEW.split_adjusted_low IS NOT DISTINCT FROM OLD.split_adjusted_low THEN
      NEW.split_adjusted_low := NEW.low;
    END IF;
    IF NEW.close IS DISTINCT FROM OLD.close
       AND NEW.split_adjusted_close IS NOT DISTINCT FROM OLD.split_adjusted_close THEN
      NEW.split_adjusted_close := NEW.close;
    END IF;
    IF NEW.volume IS DISTINCT FROM OLD.volume
       AND NEW.split_adjusted_volume IS NOT DISTINCT FROM OLD.split_adjusted_volume THEN
      NEW.split_adjusted_volume := NEW.volume;
    END IF;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_default_split_adjusted_eod_price
  BEFORE INSERT OR UPDATE ON eod_prices
  FOR EACH ROW EXECUTE FUNCTION default_split_adjusted_eod_price();

-- mul is the product aggregate Postgres does not ship. Multiplication over
-- numeric is exact, so a cumulative split factor built from it is exact.
-- numeric_mul is strict and the initcond is non-null, so a NULL input is skipped
-- rather than poisoning the product, and zero input rows return 1.
CREATE AGGREGATE mul(numeric) (SFUNC = numeric_mul, STYPE = numeric, INITCOND = '1');

-- split_factor_at returns the cumulative split adjustment factor for the
-- given instrument as of the given reference date, as an exact rational: the
-- numerator is the product of split_to and the denominator the product of
-- split_from, over every stock split with ex_date strictly greater than
-- reference_date AND less than or equal to the current date.
--
-- The quotient is returned unevaluated so that callers multiply first and divide
-- once -- quantity * num / den rather than quantity * (num / den) -- which is the
-- minimal-error ordering and keeps the exact part exact for as long as possible.
-- See docs/adr/0028-cumulative-split-factor-is-an-exact-rational.md.
--
-- For derivative instruments (options, futures) that name an underlying line,
-- the function also includes splits on the security that line belongs to. This
-- avoids the need to duplicate split rows onto each derivative. A split is an
-- action on the security and every line splits with it, so the lookup climbs
-- from the line to the security above it.
--
-- The CURRENT_DATE clause matters: corporate event plugins return splits
-- the moment they are announced, often weeks before the ex_date arrives,
-- and the corporate event fetcher's lookahead window pulls them into
-- stock_splits early. Without this clause, an announced-but-not-yet-effective
-- split would immediately scale every prior price/tx for the instrument as
-- if the split had already happened, even though the user still owns
-- pre-split shares trading at pre-split prices. With this clause, future-
-- dated splits sit inert in stock_splits until their ex_date passes, at
-- which point the next recompute pass picks them up. The corporate event
-- daily scheduler (see docs/spec/corporate-events.md) is responsible for
-- triggering that recompute when ex_dates cross.
--
-- Returns 1/1 when no effective future splits are known, so a row with no
-- later splits is unchanged by adjustment.
CREATE OR REPLACE FUNCTION split_factor_at(
  p_instrument_id UUID,
  p_reference DATE,
  OUT num NUMERIC,
  OUT den NUMERIC
) LANGUAGE sql STABLE AS $$
  SELECT mul(s.split_to), mul(s.split_from)
  FROM stock_splits s
  WHERE s.instrument_id IN (
      p_instrument_id,
      (SELECT ul.instrument_id
         FROM instruments i
         JOIN instrument_listings ul ON ul.id = i.underlying_listing_id
        WHERE i.id = p_instrument_id)
    )
    AND s.ex_date > p_reference
    AND s.ex_date <= CURRENT_DATE;
$$;

-- holding_qty_in_basis returns a holding's quantity over a half-open [from, before)
-- window, converted into a single share count basis, together with the two numbers
-- a caller needs to bound the rounding in that conversion.
--
-- A holding is per currency line, so p_listing_id is part of what names it, and a
-- null one names the holding no posting could place rather than every line at
-- once. The split factor stays a question about the security: a split is an
-- action on the shares, not on one of the lines they are quoted in.
--
-- Postings are grouped by their own share_count_basis and summed before conversion,
-- so the division happens once per distinct basis rather than once per posting. That
-- is what makes the bound stateable: each group converts by an exact rational
-- (num_b * den_d) / (den_b * num_d) -- the row's own factor to today divided by the
-- target's -- so the whole result is exact unless a group's factor is not 1/1, and
-- inexact_bases counts the groups that can carry a rounding. A caller comparing
-- against a declared quantity allows inexact_bases units in the last place of the
-- split-adjusted rounding scale, and nothing when the count is zero.
--
-- Only USER postings are read. The EQUITY counterparty of a pad shares its broker
-- account and instrument, so including it would net a declared opening balance back
-- to zero. p_include_synthetic is false when computing a pad, which must not feed
-- back into its own recomputation, and true when checking a declaration against the
-- holding as the user sees it. Either bound may be NULL for an open one.
-- See docs/spec/bitemporality.md and docs/adr/0028-cumulative-split-factor-is-an-exact-rational.md.
CREATE OR REPLACE FUNCTION holding_qty_in_basis(
  p_user_id UUID,
  p_broker TEXT,
  p_account TEXT,
  p_instrument_id UUID,
  p_listing_id UUID,
  p_from TIMESTAMPTZ,
  p_before TIMESTAMPTZ,
  p_basis DATE,
  p_include_synthetic BOOLEAN,
  OUT qty NUMERIC,
  OUT posting_count INT,
  OUT inexact_bases INT
) LANGUAGE sql STABLE AS $$
  WITH per_basis AS (
    SELECT t.share_count_basis AS basis, SUM(t.quantity) AS q, COUNT(*)::int AS n
    FROM txs t
    WHERE t.user_id = p_user_id
      AND t.broker = p_broker
      AND t.account = p_account
      AND t.instrument_id = p_instrument_id
      -- IS NOT DISTINCT FROM rather than =, because the line is nullable at both
      -- ends and a holding on no line is a holding: a null asked for selects the
      -- postings nothing placed, not none of them.
      AND t.listing_id IS NOT DISTINCT FROM p_listing_id
      AND t.account_type = 'USER'
      AND (p_include_synthetic OR t.synthetic_purpose IS NULL)
      AND (p_from IS NULL OR t.order_date >= p_from)
      AND (p_before IS NULL OR t.order_date < p_before)
    GROUP BY t.share_count_basis
  )
  SELECT COALESCE(SUM(p.q * fb.num * fd.den / (fb.den * fd.num)), 0),
         COALESCE(SUM(p.n), 0)::int,
         COUNT(*) FILTER (WHERE fb.num * fd.den <> fb.den * fd.num)::int
  FROM per_basis p,
       LATERAL split_factor_at(p_instrument_id, p.basis) fb,
       LATERAL split_factor_at(p_instrument_id, p_basis) fd;
$$;

-- Holding declarations: user-provided statement of known holding quantity at a date.
-- Holdings are computed aggregates identified by (broker, account, instrument_id,
-- listing_id).
--
-- share_count_basis is the date at which the share count declared_qty is denominated
-- in was current. It defaults to as_of_date: a user reading a quantity off a
-- statement of that date means the share count current then. A user reading today's
-- holdings screen means today's, and says so. Without the declaration stating which,
-- it and the postings it is reconciled against can be in different units, and a
-- correct portfolio disagrees with itself by the split factor.
-- See docs/spec/bitemporality.md.
--
-- The unique key carries as_of_date, so a holding may hold several declarations at
-- different dates. The earliest is the pad: it generates the INITIALIZE transaction
-- that makes the declared quantity true, and it is true by construction, so it can
-- never catch an error. The later ones are assertions -- statements the computed
-- holding is checked against, which is where the safety comes from. The
-- discriminator is not stored: deriving it from MIN(as_of_date) leaves nothing to
-- drift out of step with the rows.
-- See docs/spec/fixed-point.md and docs/adr/0011-synthetic-initialize-transactions.md.
CREATE TABLE holding_declarations (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id           UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  broker            TEXT NOT NULL,
  account           TEXT NOT NULL,
  instrument_id     UUID NOT NULL REFERENCES instruments(id),
  -- The currency line the declared quantity is a quantity of, and NULL where
  -- nothing said which. A declaration is a statement about a holding, and a
  -- holding is per line, so the two carry the same pair and the same null: two
  -- lines of one security are two holdings an FX rate apart, and a declaration
  -- that could not say which line is on no line rather than on a sentinel one.
  -- See docs/adr/0072-a-posting-names-a-security-and-a-line.md.
  listing_id        UUID,
  declared_qty      NUMERIC NOT NULL,
  as_of_date        DATE NOT NULL,
  share_count_basis DATE NOT NULL,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
  -- The line and the security it belongs to, referenced as a pair, so a
  -- declaration cannot name a line of some other security. MATCH SIMPLE skips
  -- the check when either column is null, which is what lets a declaration name
  -- a security whose line is not known; instrument_id is NOT NULL here, so
  -- unlike txs there is no second case for a CHECK to close.
  --
  -- No ON DELETE, for the reason txs_listing_id_fkey gives: a merge repoints its
  -- loser's rows before deleting the loser, and one that forgot to should fail
  -- rather than quietly cascade.
  FOREIGN KEY (instrument_id, listing_id)
    REFERENCES instrument_listings (instrument_id, id) MATCH SIMPLE
);

-- One declaration per holding per date. Two partial indexes rather than one key
-- over a nullable column, which would call two declarations of the same
-- unknown-line holding distinct and let a re-import write a second row where it
-- meant to restate the first. Same shape and same reason as
-- uq_instrument_listings_currency and uq_instrument_listings_unknown: no
-- NULLS NOT DISTINCT, so no PostgreSQL 15 dependency.
--
-- A write picks its ON CONFLICT target from whether the line is named, which is
-- the one thing the caller always knows.
CREATE UNIQUE INDEX uq_holding_declarations_on_listing
  ON holding_declarations (user_id, broker, account, instrument_id, listing_id, as_of_date)
  WHERE listing_id IS NOT NULL;
CREATE UNIQUE INDEX uq_holding_declarations_no_listing
  ON holding_declarations (user_id, broker, account, instrument_id, as_of_date)
  WHERE listing_id IS NULL;

CREATE INDEX idx_holding_declarations_listing_id ON holding_declarations (listing_id);

-- The default lives here rather than in the application so that every path into the
-- table -- the API, an integration test, a psql session -- lands on the same
-- denomination. It is an insert-time decision: once a declaration states its basis,
-- moving as_of_date does not restate it.
CREATE OR REPLACE FUNCTION default_declaration_share_count_basis() RETURNS TRIGGER AS $$
BEGIN
  IF NEW.share_count_basis IS NULL THEN
    NEW.share_count_basis := NEW.as_of_date;
  END IF;
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trg_default_declaration_share_count_basis
  BEFORE INSERT ON holding_declarations
  FOR EACH ROW EXECUTE FUNCTION default_declaration_share_count_basis();

-- qty_is_zero is a null-safe test for a closed position, applied to a sum of
-- split_adjusted_quantity. Raw quantities cannot be summed at all: each is
-- denominated in its own row's share_count_basis, so a buy recorded before a
-- split and a sell recorded after it are in different units and adding them
-- scales the total by part of the split factor. The split-adjusted column is
-- already converted to today's share count, so the sum is in one denomination.
--
-- What that costs is exactness. The column declares a rounding scale of 12, so a
-- row whose conversion did not land on a representable decimal rounds once, and a
-- genuinely closed position sums to something near zero rather than to zero.
-- inexact_postings is how many contributing rows may carry such a rounding, and
-- one unit in the last place each is the most the sum can differ from a true
-- zero. Callers count the rows whose adjusted quantity differs from their raw
-- one: a row that converted by 1/1 cannot have rounded, and counting the rest
-- overstates by the ones that converted exactly, which is the safe direction for
-- a bound. Passing zero is an exact test.
--
-- The NULL branch is what the valuation day grid depends on: a holding has no
-- position on a date before its instrument's first tx, and that reads as closed
-- rather than as unknown.
--
-- See docs/spec/bitemporality.md, adr/0026-exact-decimals-bounded-by-closure.md
-- and adr/0028-cumulative-split-factor-is-an-exact-rational.md.
CREATE FUNCTION qty_is_zero(q numeric, inexact_postings int) RETURNS boolean
    LANGUAGE sql IMMUTABLE PARALLEL SAFE AS $$
    SELECT q IS NULL OR abs(q) <= COALESCE(inexact_postings, 0) * 1e-12
$$;
