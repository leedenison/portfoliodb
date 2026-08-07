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

## The admin archive

`AdminArchive` is one protojson object: the envelope, then one optional section
per entity. A section present but empty means the export included it and there
was nothing; a section absent means it was not included at all. Sections are
written in restore order -- instruments first, because every other part refers
to them.

```json
{"envelope": {"format_version": 1, "exported_at": "2026-07-30T00:00:00Z",
              "source_instance": "portfoliodb.example.com",
              "kind": "ARCHIVE_KIND_ADMIN"},
 "instruments": {"instruments": [...]},
 "prices": {"groups": [...]},
 "corporate_events": {"groups": [...]}}
```

### Instruments

`instruments.instruments[]` is a flat list. A derivative names its underlying by
identifier rather than by position, so a shared underlying appears once and the
order the list is written in carries no meaning.

| Field | Notes |
| --- | --- |
| `asset_class` | |
| `name` | optional; advisory, see below |
| `currency` | ISO 4217 |
| `exchange_mic` | optional; ISO 10383 |
| `identifiers[]` | at least one; `{type, value, domain, canonical}` |
| `provider_identifiers[]` | `{provider, identifier_type, value, domain}`; `identifier_type` is the provider's own vocabulary, not `IdentifierType` |
| `underlying` | optional; an identifier triple naming an instrument in the same part |
| `cik`, `sic_code` | optional |
| `valid_from`, `valid_before` | optional; half-open, either bound open-ended |
| `strike`, `expiry`, `put_call`, `contract_multiplier` | optional; options only |
| `identity_as_of` | optional; the point in market time the identity reflects |

`name` is advisory because the importing instance recomputes it from the
identifiers. It survives only where no ticker-like identifier exists to derive
one from, which is exactly the case where a plugin-supplied name would otherwise
be lost.

`provider_identifiers` are the recorded output of the paid, rate-limited lookups
the archive exists to avoid repeating. An instrument restored with them is
indistinguishable from a resolved one, and no plugin is called for it.

**Not carried:** the server UUID, which means nothing in another instance; the
`exchange` column, which is derived from `exchange_mic` and the identifiers; the
nested underlying subtree and the joined exchange reference data, both of which
exist so the SPA need not fetch them separately and neither of which belongs in
a file.

### Prices

`prices.groups[]` is one group per instrument, or one group per
`share_count_basis` where an instrument's rows are not all denominated in one
share count -- which is how a single file carries a back-adjusted series
alongside an as-traded one.

| Level | Field | Notes |
| --- | --- | --- |
| group | `instrument` | identifier triple |
| group | `asset_class` | hint for identifier plugin routing on an unknown instrument |
| group | `currency` | ISO 4217; validation hint |
| group | `share_count_basis` | optional; absent means as-traded |
| group | `coverage[]` | half-open intervals |
| row | `price_date` | |
| row | `open`, `high`, `low`, `adjusted_close`, `volume` | optional |
| row | `close` | |

```json
{"instrument": {"type": "MIC_TICKER", "value": "AAPL", "domain": "XNAS"},
 "asset_class": "STOCK", "currency": "USD",
 "coverage": [{"from": "2022-01-01", "before": "2025-07-07"}],
 "rows": [{"price_date": "2024-01-15", "close": "185.9", "volume": "48088700"}]}
```

That group is what the price CSV spelled as a `# coverage=` comment line plus a
row, and it is why the comment syntax goes. Coverage is a field of the group it
applies to, so a file needs no global declaration, no rule for a specific
declaration overriding a global one, no rule for several specifics applying at
once, and no error case for a half-written identifier. An instrument that was
covered but has no rows is simply a group with an empty `rows`.

**Not carried:** the split-adjusted close, which the importing instance derives;
`last_fetched_at`, which comes from the envelope's `exported_at`; and the
originating data provider, because an import records every row and every span
against the `import` sentinel, so provenance cannot survive a round trip.

`share_count_basis` on the group is new. The price CSV could state it on import
but not on export, so a back-adjusted series exported and reimported came back
as as-traded and was adjusted a second time.

### Corporate events

`corporate_events.groups[]` is one group per instrument, carrying coverage and a
list of events, each of which is a `split` or a `dividend`.

| Level | Field | Notes |
| --- | --- | --- |
| group | `instrument`, `asset_class` | |
| group | `coverage[]` | merged across plugins; see below |
| split | `ex_date`, `split_from`, `split_to` | factor is `split_to / split_from` |
| split | `first_known_at` | optional; knowledge time |
| dividend | `ex_date`, `amount`, `currency`, `type` | `type` is `CD` or `SC` |
| dividend | `pay_date`, `record_date`, `declaration_date`, `frequency`, `first_known_at` | optional |

Events are sparse, so the absence of an event says nothing about whether a range
was queried. Only coverage says that, which is why a group with no events is
still worth writing.

`first_known_at` gates retroactive adjustment of options on the underlying: a
round trip that dropped it would re-adjust symbols that were already correct. It
falls back to the envelope's `exported_at` and then to storage time, and a
stored value only ever moves backwards.

Coverage is stored per instrument and plugin, but an import records every span
against the `import` sentinel, so the file carries spans merged across plugins.
The per-plugin distinction cannot survive a round trip and is not written.

## The user archive

`UserArchive` has the same shape as the admin archive and the same rules about
present-but-empty versus absent. Sections are written in restore order:
preferences first, because which asset classes are ignored changes what a later
transaction import keeps; declarations last, because a checked declaration is
compared against what the transactions add up to.

```json
{"envelope": {"format_version": 1, "exported_at": "2026-07-30T00:00:00Z",
              "source_instance": "portfoliodb.example.com",
              "kind": "ARCHIVE_KIND_USER"},
 "preferences": {"display_currency": "GBP", "ignored_asset_classes": {"rules": []}},
 "txs": {"windows": [...]},
 "declarations": {"statements": [...]}}
```

A user archive does not name its user. Restoring one into a different account is
therefore a feature rather than an accident.

### Preferences

`display_currency` is ISO 4217. `ignored_asset_classes` is wrapped in a message
rather than being a bare list, so that "the user has no rules" -- an empty
`rules`, which import applies -- stays distinguishable from "the file does not
state them", which import ignores. Each rule is `{broker, account, asset_class}`,
where an empty `account` means every account for that broker.

### Transactions

`txs.windows[]` is one window per (broker, period). A window is a replacement
scope: import replaces the period rather than appending to it, because
transactions have no natural key -- broker statements often supply a date and
nothing else. See `docs/adr/0002-transaction-ingestion-model.md`.

The window has to state its own period rather than have one inferred from the
postings it holds, because **a window holding no groups is a valid instruction to
clear that period**, and an inferred window could never say that.

| Level | Field | Notes |
| --- | --- | --- |
| window | `broker` | |
| window | `period_from`, `period_before` | half-open, instants |
| window | `source` | `"<broker>:<client>:<source>"`; the domain of the fallback description identifier |
| window | `share_count_basis` | optional; absent means as-traded |
| group | `postings[]` | at least one |
| posting | `timestamp`, `instrument_description`, `type`, `quantity` | |
| posting | `account`, `account_type` | `account_type` absent reads as `ACCOUNT_TYPE_USER` |
| posting | `identifier_hints[]` | zero or more identifier triples |
| posting | `unit_price`, `trading_currency`, `settlement_currency` | optional |
| posting | `broker_ref`, `counterparty_account` | optional |

Grouping is structural: a group is a list of postings, not a shared key. It is
the converter's output and nothing can rebuild it -- the server does not pair
rows or infer a missing leg -- so an archive that dropped it would lose the
balance invariant, residual attribution and the association between a fee and
its trade, permanently. See
`docs/adr/0021-converters-own-transaction-grouping.md`.

That structure is what replaces `group_ref`. The CSV needed an opaque key
because a flat file cannot nest; it was scoped to one upload and never stored,
which is exactly what a nested list expresses directly.

A posting may carry several `identifier_hints`, which the CSV could not express
-- it had one `symbol_type`/`symbol` pair per row. The paired
`exchange_type`/`exchange` columns are gone with it: `identifier_type` already
says whether a domain is a MIC or an OpenFIGI exchange code, so `exchange_type`
was restating it, and the validation that the two were present or absent
together goes too.

A group whose postings do not sum to zero is accepted rather than rejected; the
server routes the residual to an `ACCOUNT_TYPE_IMBALANCE`,
`ACCOUNT_TYPE_TRANSFER_CLEARING` or `ACCOUNT_TYPE_SOURCE_ROUNDING` posting. A
group exported with its routed residual already sums to zero, so nothing is
routed a second time.

**Not carried:** the server UUIDs; `tx_groups.id`, `job_id` and `created_at`,
which the importing instance generates; `tx_groups.timestamp`, which is the
timestamp of the first posting that names the group; the split-adjusted quantity
and price and the posting weights, all recomputed; and the synthetic opening
postings with their counterparties, which follow from the declarations they were
derived from.

### Holding declarations

`declarations.statements[]` is one statement per (account, date): the aggregate
root a set of declarations comes from. `broker` and `account` are in the file
rather than in the request, because an archive has to be self-describing.

| Level | Field | Notes |
| --- | --- | --- |
| statement | `broker`, `account`, `as_of_date` | |
| declaration | `instrument` | identifier triple |
| declaration | `declared_qty` | signed decimal |
| declaration | `share_count_basis` | optional; absent means the statement's `as_of_date` |

**Absence is not deletion**, and this is the one place the archive deliberately
differs from the transaction part. A declaration missing from an imported file is
left alone: a file assembled from one statement covers one account and one date,
and treating everything outside it as retracted would delete the user's other
checkpoints. Import is an upsert on (broker, account, instrument, `as_of_date`).

The file carries no pad-or-assert discriminator. Which one a declaration is
follows from the declaration dates for its holding -- the earliest pads, the rest
are checked -- so a stored copy could only ever disagree with them. See
`docs/adr/0030-declarations-are-padded-then-asserted.md`.

## Restore order

Within a document, the sections are written in the order they are applied, and a
reader walks them in that order:

- **Admin:** instruments, then prices, then corporate events. Prices and events
  reference instruments.
- **User:** preferences, then transactions, then declarations. Preferences first
  because ignored asset classes change what a transaction import keeps;
  declarations last because a checked declaration is compared against what the
  transactions add up to.

Between documents, restoring the admin archive before the user archive is a
recommendation and not a constraint. A user archive restored into an instance
with no instruments loaded resolves its postings through the normal identifier
path: correct, merely expensive. That is not an error and must not be reported
as one.

Order in the file is a convenience, not a contract. A reader applies the parts in
its own order, so a hand-written document whose keys appear in some other order
restores identically.
