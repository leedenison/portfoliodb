# Archive format

The archive is the single import and export format. One schema, defined in
`proto/archive/v1/` and serialised as protojson, replaces the standard
transaction CSV, the price CSV, the instrument JSON and the corporate event
JSON.

Broker-specific files -- the Fidelity CSV, the IBKR OFX, the Schwab CSV -- are
not affected. They are the broker's own output and PortfolioDB does not define
them; converters read them into archive messages directly. Only the formats
PortfolioDB itself defines are replaced.

## What an archive is

An archive exists to rebuild an instance on a new server without losing data and
without re-running expensive external operations, identifier lookup above all.
It is neither a general interchange format nor a full database backup, and the
difference decides what it carries.

It carries two kinds of thing:

- **Irreplaceable data.** Transactions and their grouping, holding declarations,
  portfolios, preferences, plugin configuration, fetch-block reasons, resolved
  corporate events, and hand-recovered prices no provider still serves. Nothing
  can reconstruct these.
- **Data that is expensive to reacquire.** Instruments and their identifiers,
  provider identifiers, prices with their coverage, corporate events with their
  coverage, and inflation indices. These are refetchable in principle, at the
  cost of the paid, rate-limited lookups the archive exists to avoid.

It does not carry anything the importing instance recomputes: split-adjusted
quantities and prices, posting weights, synthetic opening postings, lots and
realised gains, identification and validation errors, ingestion jobs, or
computed holdings and valuations. Exporting derived state would invite a round
trip to change a number -- to carry a rounding, or to mix share counts -- which
`docs/spec/bitemporality.md` forbids.

Full reasoning: `docs/adr/0032-archive-preserves-inputs-not-derived-state.md`.

## The two archives

Export and import split by data ownership rather than bundling everything into
one file.

- The **admin archive** carries shared data and no user data: instruments and
  their identifiers, prices and coverage, corporate events and coverage,
  inflation indices, fetch blocks, unhandled event resolutions and plugin
  configuration.
- The **user archive** carries one user's own data and no admin data:
  transactions and their grouping, holding declarations, and preferences.

They have different owners, different authorisation and different lifecycles.
Shared reference data is curated by an admin and refetchable in principle; user
data is authoritative and recoverable from nowhere. Bundling them would put one
user's transactions in an artefact an admin exports, and force an ordinary user
to hold reference data they have no business owning.

Restoring a user archive into an instance with no instruments loaded leaves its
postings to resolve from scratch. That is working as intended: the normal
identifier resolution path handles it and the result is correct, merely
expensive. **Restoring the admin archive first is a recommendation, not a
constraint** -- avoiding that cost is what the archive buys, not a mechanism it
depends on.

Full reasoning: `docs/adr/0033-admin-and-user-archives-are-separate.md`.

## The three levels

A file has three levels, and each states its own scope in full.

- **File.** The `envelope`, and nothing else. It carries `format_version`,
  `exported_at`, the source instance and which of the two archives this is.
- **Group.** The entity's aggregate root: the instrument for prices and
  corporate events, the transaction window and group for transactions, the
  statement for holding declarations. Coverage, `share_count_basis`, asset class
  and currency live here.
- **Row.** Only what varies per row. A price row is a date and a bar.

A field belongs at the file level only if it **cannot** differ between two rows
of a valid file. Being constant in practice is not enough. The flat formats got
this wrong in both directions: `prices-recovered.csv` carries ninety coverage
declarations and every one of them is instrument-scoped, so the file-wide slot
the format was designed around goes unused -- coverage diverges exactly when
instruments have different lifetimes, which is the case it exists to record.
Meanwhile `ExportPriceRow.exported_at` stamps one value onto every row, because
the stream has nowhere else to put it.

**There is no inheritance or override between levels.** Repetition is cheap in a
machine format and compresses away; precedence rules are expensive, because
every reader has to implement them identically and a disagreement between two
readers is silent. The price CSV needs four rules to rebuild nesting from
flatness -- at most one global declaration, a specific one overrides rather than
adds, several specifics all apply, a partial identifier is an error -- plus two
cases the export must always write out in full. The cost of getting one of them
wrong is not an error but a wrong number: a missing share count basis reads as
as-traded, and back-adjusted prices are then adjusted a second time.

Full reasoning: `docs/adr/0035-archive-nests-by-aggregate-root.md`.

## Shared conventions

These hold everywhere in the format, and are stated here rather than repeated
per part.

**Dates** are `"YYYY-MM-DD"` strings. **Instants** are RFC 3339, as protojson
writes a `google.protobuf.Timestamp`: `"2026-07-30T00:00:00Z"`.

**Date intervals** are half-open `[from, before)` and always name the exclusive
bound `before`. To cover through 31 December 2024, write
`{"from": "2024-01-01", "before": "2025-01-01"}`. See
`docs/adr/0018-half-open-date-intervals.md`.

**Decimals** -- quantities, prices, money, split factors -- are canonical
decimal strings, never JSON numbers: `"185.9"`, not `185.9`. A `double` cannot
carry an exact decimal, and a value that does not reimport identically is a bug.
Trailing zeroes are the only thing not preserved: `"1.50"` and `"1.5"` are the
same price. See `docs/adr/0027-decimal-values-cross-the-wire-as-strings.md`.

**Absent is not zero.** A field is written only when it has a value, and a field
that is absent means the file does not state it. An absent `unit_price` is not a
price of zero -- an option expiring worthless has a declared price of zero and
its group balances, while a posting with no price cannot be converted at all.
Readers must not substitute a zero for an absent value.

**Instruments are named by identifier, never by id.** A server UUID means
nothing in another instance and is never written. One instrument reference is a
triple:

```json
{"type": "MIC_TICKER", "value": "AAPL", "domain": "XNAS"}
```

`domain` is the MIC for `MIC_TICKER`, the exchange code for `OPENFIGI_TICKER`,
and the source for `BROKER_DESCRIPTION`; it is empty for everything else, and
absent and empty mean the same thing. Where a file states an instrument's whole
identifier set rather than referring to it, each entry carries `canonical` as
well, which is false only for broker-description identifiers.

**Coverage** is a list of intervals on the group, saying which dates were
authoritatively answered for. An interval holding no rows is meaningful, and is
the only way a file can record that a provider was asked about those dates and
had nothing; dropping it leaves valuation treating the days between reported
bars as unpriced. See `docs/adr/0023-price-coverage-is-stored-not-inferred.md`.

**Vocabularies** -- asset classes, identifier types, transaction types, account
types, brokers -- are written by name, and the names are the same strings the
database stores: `"STOCK"`, `"ISIN"`, `"BUYSTOCK"`. They come from
`proto/type/v1/type.proto`, which does not remove or rename a value. See
`docs/adr/0038-controlled-vocabularies-are-shared.md`.

## The envelope

Every archive is one protojson object whose first member is the envelope:

```json
{"envelope": {"format_version": 1,
              "exported_at": "2026-07-30T00:00:00Z",
              "source_instance": "portfoliodb.example.com",
              "kind": "ARCHIVE_KIND_ADMIN"}}
```

- `format_version` is bumped only by a change an older reader cannot survive: a
  field removed, renamed or retyped, or a semantic change to one that stays.
  Adding a field does not bump it.
- `exported_at` is knowledge time: when the data in the file was current.
  Imported price rows and coverage record it as `last_fetched_at`, and corporate
  events with no `first_known_at` of their own fall back to it. It says nothing
  about which share count a value is denominated in; that is
  `share_count_basis`, and it is a different question entirely.
- `source_instance` is an opaque label for whatever produced the file, for a
  reader's benefit only. Nothing keys off it.
- `kind` is `ARCHIVE_KIND_ADMIN` or `ARCHIVE_KIND_USER`. The document's message
  type says the same thing, but protojson records no type name, so the envelope
  has to carry it -- without it, an importer cannot refuse a user archive handed
  to the admin page.
