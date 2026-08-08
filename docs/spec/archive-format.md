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
  portfolios, preferences, plugin configuration, fetch-block reasons, the
  corporate events an admin was asked to judge and the calls made on them, and
  hand-recovered prices no provider still serves. Nothing can reconstruct these.
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

- The **system archive** carries shared data and no user data: instruments and
  their identifiers, prices and coverage, corporate events and coverage,
  inflation indices, fetch blocks, unhandled corporate events and plugin
  configuration.
- The **user archive** carries one user's own data and no system data:
  transactions and their grouping, holding declarations, and preferences.

They have different owners, different authorisation and different lifecycles.
Shared reference data is curated by an admin and refetchable in principle; user
data is authoritative and recoverable from nowhere. Bundling them would put one
user's transactions in an artefact an admin exports, and force an ordinary user
to hold reference data they have no business owning.

Restoring a user archive into an instance with no instruments loaded leaves its
postings to resolve from scratch. That is working as intended: the normal
identifier resolution path handles it and the result is correct, merely
expensive. **Restoring the system archive first is a recommendation, not a
constraint** -- avoiding that cost is what the archive buys, not a mechanism it
depends on.

Full reasoning: `docs/adr/0033-system-and-user-archives-are-separate.md`.

## The three levels

A file has three levels, and each states its own scope in full.

- **File.** The `envelope`, and nothing else. It carries `format_version`,
  `exported_at`, the source instance and which of the two archives this is.
- **Group.** The entity's aggregate root: the instrument for prices and
  corporate events, the transaction window and group for transactions, the
  statement for holding declarations. Coverage, asset class and currency live
  here.
- **Row.** Only what varies per row. A price row is a date, a bar, and the share
  count basis the bar is denominated in when that is not its own date.

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
              "kind": "SYSTEM"}}
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
- `kind` is `SYSTEM` or `USER`. The document's message
  type says the same thing, but protojson records no type name, so the envelope
  has to carry it -- without it, an importer cannot refuse a user archive handed
  to the system page.

## The system archive

`SystemArchive` is one protojson object: the envelope, then one optional section
per entity. A section present but empty means the export included it and there
was nothing; a section absent means it was not included at all. Sections are
written in restore order -- instruments first, because every other part refers
to them.

```json
{"envelope": {"format_version": 1, "exported_at": "2026-07-30T00:00:00Z",
              "source_instance": "portfoliodb.example.com",
              "kind": "SYSTEM"},
 "instruments": {"instruments": [...]},
 "prices": {"groups": [...]},
 "corporate_events": {"groups": [...]},
 "inflation_indices": {"groups": [...]},
 "fetch_blocks": {"groups": [...]},
 "unhandled_events": {"groups": [...]},
 "plugin_config": {"configs": [...]}}
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

A system archive carries **every** instrument named by at least one canonical
identifier: currencies and FX pairs as well as securities, and instruments
identification has not yet given an asset class to. An unclassified instrument is
one a price import created before identification reached it, which makes it
precisely the row a rebuild could not reconstruct from anything else.

Importing an instrument the instance already has -- which every rebuild does, as
the currency and FX rows are reference data created before any file is read --
**fills gaps only**. Identifiers the file states and the instrument lacks are
added, columns still empty are filled, and a value already stored always wins.
An import therefore cannot rewrite what the importing instance already knew.

**Not carried:** the server UUID, which means nothing in another instance; the
`exchange` column, which is derived from `exchange_mic` and the identifiers; the
nested underlying subtree and the joined exchange reference data, both of which
exist so the SPA need not fetch them separately and neither of which belongs in
a file.

### Prices

`prices.groups[]` is one group per instrument.

| Level | Field | Notes |
| --- | --- | --- |
| group | `instrument` | identifier triple |
| group | `asset_class` | hint for identifier plugin routing on an unknown instrument |
| group | `currency` | ISO 4217; validation hint |
| group | `coverage[]` | half-open intervals |
| row | `price_date` | |
| row | `share_count_basis` | optional; absent means the row's own `price_date`, which is as-traded |
| row | `open`, `high`, `low`, `adjusted_close`, `volume` | optional |
| row | `close` | |

```json
{"instrument": {"type": "MIC_TICKER", "value": "AAPL", "domain": "XNAS"},
 "asset_class": "STOCK", "currency": "USD",
 "coverage": [{"from": "2022-01-01", "before": "2025-07-07"}],
 "rows": [{"price_date": "2024-01-15", "close": "185.9", "volume": "48088700"}]}
```

`share_count_basis` is on the row because `eod_prices` stores it there: one
instrument can hold a back-adjusted stretch beside an as-traded one, and both
travel in the one group. A row that omits it is denominated in the share count
current on its own `price_date`, which is what an ordinary export writes.

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

`share_count_basis` on the row is new. The price CSV could state it on import
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

### Inflation indices

`inflation_indices.groups[]` is one group per currency.

| Level | Field | Notes |
| --- | --- | --- |
| group | `currency` | ISO 4217 |
| row | `month` | the month described, written as its first day |
| row | `index_value` | decimal; relative to `base_year` July = 100 |
| row | `base_year` | the year in which July = 100 |

```json
{"currency": "GBP",
 "rows": [{"month": "2024-03-01", "index_value": "133.8", "base_year": 2015}]}
```

**A group carries no `coverage[]`**, and it is the only group in the format that
does not. A series is dense: an index is published for every month until the
series ends, so the rows say what is held and a gap is missing data rather than a
month a provider was asked about and had nothing to report. `inflation_indices`
stores no coverage either, and a file must not claim more than the table it came
from can answer for.

`base_year` is on the row rather than on the group because a rebasing changes it
partway through a series and both halves travel in the one group. Reading a
rebased value against the wrong base is not an error but a wrong number, which
is the same reason `share_count_basis` sits on a price row.

Inflation indices are expensive to reacquire rather than irreplaceable, with one
wrinkle: a revision replaces its predecessor in place and leaves no record, so a
refetch returns the current revision and not what this file holds. See
`docs/spec/bitemporality.md`.

**Not carried:** `data_provider`, because an import records every row against the
`import` sentinel, so provenance cannot survive a round trip; and
`last_fetched_at`, which comes from the envelope's `exported_at`.

### Fetch blocks

`fetch_blocks.groups[]` is one group per instrument, carrying that instrument's
blocks across both fetchers and every plugin.

| Level | Field | Notes |
| --- | --- | --- |
| group | `instrument` | identifier triple |
| block | `category` | `PRICE` or `CORPORATE_EVENT`: which fetcher is blocked |
| block | `plugin_id` | as the plugin registry spells it |
| block | `reason` | free text, as the plugin reported it |
| block | `first_blocked_at` | optional; knowledge time |

```json
{"instrument": {"type": "MIC_TICKER", "value": "AAPL", "domain": "XNAS"},
 "blocks": [{"category": "PRICE", "plugin_id": "eodhd",
             "reason": "404 from provider", "first_blocked_at": "2026-03-04T09:12:00Z"}]}
```

One part covers both `price_fetch_blocks` and `corporate_event_fetch_blocks`.
They are the same statement about two fetchers, the reason someone wrote down
reads the same way in both, and splitting them would put two rows in the export
menu where an admin thinks of one. `category` is the plugin category the block
belongs to, from the same vocabulary plugin configuration uses; only `PRICE` and
`CORPORATE_EVENT` have a fetch-block table, and a file naming another category is
describing a table that does not exist.

`reason` is free text and not a vocabulary. It is what the plugin's permanent
error carried, it is what the column holds, and it is read by a person deciding
whether to unblock.

`first_blocked_at` is knowledge time and the column never overwrites it, so an
import keeps the earlier of the stored and the supplied value. Absent falls back
to the envelope's `exported_at`.

A block naming a plugin the importing instance does not register is a rejected
row rather than a failed part: nothing would ever consult it.

**Not carried:** the instrument's server UUID, which every archive replaces with
an identifier.

### Unhandled corporate events

`unhandled_events.groups[]` is one group per instrument.

| Level | Field | Notes |
| --- | --- | --- |
| group | `instrument` | identifier triple |
| event | `event_type` | `REVERSE_SPLIT`, `NON_WHOLE_SPLIT`, `SPECIAL_CASH_DIVIDEND`, ... |
| event | `ex_date` | optional; absent for the rare event with no date |
| event | `detail` | the sentence shown in the review queue |
| event | `data_json` | optional; the detector's own context, as JSON text |
| event | `resolved` | whether an admin has already made the call |
| event | `detected_at` | optional; knowledge time |

```json
{"instrument": {"type": "MIC_TICKER", "value": "XYZ", "domain": "XNAS"},
 "events": [{"event_type": "REVERSE_SPLIT", "ex_date": "2025-04-11",
             "detail": "1:10 reverse split affects 3 options", "resolved": true}]}
```

**The whole row is carried, resolved and unresolved alike**, and not just the
`resolved` flag that is the irreplaceable part of it. These rows exist only
because a corporate-event fetch detected something it could not apply, and a
restore writes events from the file instead of fetching them, so nothing
re-creates the row a bare resolution would need to attach to. Carrying the row
restores both halves of what an admin had: the judgements already made, and the
queue still waiting for one.

`resolved` is why the part exists. It is the only trace that a person looked at a
reverse split or a merger and decided, and a rebuild without it re-surfaces every
event for review.

Import inserts an event only where no row with the same instrument, `event_type`,
`ex_date` and `resolved` state is already stored, so importing the same file
twice does not double the queue. It does not un-resolve anything: a stored
resolved row and an incoming unresolved one are different rows, which is the same
thing that happens today when a refetch re-detects an event already judged.

`detail` and `data_json` are advisory. They were written for a person reading the
queue, and both may name ids belonging to the instance that wrote the file.
`data_json` is carried as the JSON text the column holds rather than as a
structured value: its shape belongs to whichever detector wrote it, and protojson
would carry its numbers as doubles. The column is `JSONB`, which stores a parsed
value, so what an export writes is the database's spelling -- key order and
spacing are its own. The value round trips; the bytes do not.

**Not carried:** the row `id`, a server UUID.

### Plugin configuration

`plugin_config.configs[]` is a flat list. A config row has no aggregate root
above it -- `category` is a column on the row, not an entity the rows hang off --
so grouping by category would invent a level the data does not have.

| Level | Field | Notes |
| --- | --- | --- |
| row | `plugin_id` | as the plugin registry spells it |
| row | `category` | `IDENTIFIER`, `DESCRIPTION`, `PRICE`, `INFLATION`, `CORPORATE_EVENT` |
| row | `enabled` | |
| row | `precedence` | higher is preferred; unique within a category |
| row | `config_json` | optional; the plugin's own settings, as JSON text |
| row | `max_history_days` | optional; absent means unlimited |

```json
{"plugin_id": "eodhd", "category": "PRICE", "enabled": true,
 "precedence": 20, "config_json": "{\"eodhd_api_key\":\"...\"}",
 "max_history_days": 3650}
```

**A document carrying this part is a secret.** `config_json` holds live API keys,
in full and unredacted, because a rebuild that needs an admin to re-enter every
provider credential by hand is a rebuild that has not restored. That is why the
export menu leaves this part unticked: including it changes where the file can
safely be kept, and that has to be a decision somebody made rather than a default
they inherited.

`precedence` is an ordering somebody chose between providers that disagree, so
nothing can guess it back, and it is unique per category. Import applies a
category's rows as a set: the stored precedences in that category are moved out
of the way first, the file's rows are applied with the precedences they state,
and any plugin the file does not name keeps its row and is given a free
precedence below them.

A row naming a plugin this build does not register is a rejected row rather than
a failed part. A config row nothing will ever read is not worth storing, and the
mismatch is worth saying out loud.

`category` names are the archive's spelling, not the column's: `plugin_config.category`
holds the lowercase `"corporate_event"`. It is the one controlled vocabulary
where the file and the column differ, and the mapping lives in one place on the
server.

**Not carried:** nothing. The row is small and every column of it is a decision.

## The user archive

`UserArchive` has the same shape as the system archive and the same rules about
present-but-empty versus absent. Sections are written in restore order:
preferences first, because which asset classes are ignored changes what a later
transaction import keeps; declarations last, because a checked declaration is
compared against what the transactions add up to.

```json
{"envelope": {"format_version": 1, "exported_at": "2026-07-30T00:00:00Z",
              "source_instance": "portfoliodb.example.com",
              "kind": "USER"},
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

## Producing and consuming a document

One endpoint each way, and one page: `/admin/archive`. Neither the export nor the
import is an affordance on whatever page happens to own an entity, so a rebuild
is one action rather than a sequence of pages visited in the right order.

**The export takes a menu of parts.** A part left out is absent from the
document. A part included is present even when it holds nothing, which records
that the export asked and there was nothing -- the distinction the wrapper
messages exist for. Restore order is a property of the format rather than of the
request, so the parts are written in it whatever order they were asked for in.

The envelope is written once, before any part. `exported_at` is knowledge time,
and knowledge time that differs between one file's own parts is not knowledge
time.

**The import takes a whole document and applies it asynchronously.** It returns
a job; the parts are applied server-side in restore order, so an import finishes
whether or not the client is still there to watch. Importing one kind of data
means supplying a document carrying only that part, rather than a separate
endpoint per part.

A part that fails does not stop the ones after it. The parts are not
hard-dependent -- prices and corporate events resolve any instrument they cannot
find, so they apply against an instance whose instrument part failed. Restoring
instruments first is what avoids paying for that resolution, which is the same
recommendation-not-constraint that holds between the two archives.

The job reports a result per part: how much was applied, and which rows were
rejected. A rejected row does not fail its part. A part fails only when a write
does not land, so "completed, 12 rows rejected" is a result the format can state
and a page can show.

## Restore order

Within a document, the sections are written in the order they are applied, and a
reader walks them in that order:

- **System:** instruments, then prices, then corporate events, then inflation
  indices, fetch blocks, unhandled corporate events and plugin configuration.
  Prices, events, fetch blocks and unhandled events reference instruments.
  Inflation indices and plugin configuration reference nothing, and plugin
  configuration is written last because it is what makes a document a secret.
- **User:** preferences, then transactions, then declarations. Preferences first
  because ignored asset classes change what a transaction import keeps;
  declarations last because a checked declaration is compared against what the
  transactions add up to.

Between documents, restoring the system archive before the user archive is a
recommendation and not a constraint. A user archive restored into an instance
with no instruments loaded resolves its postings through the normal identifier
path: correct, merely expensive. That is not an error and must not be reported
as one.

Order in the file is a convenience, not a contract. A reader applies the parts in
its own order, so a hand-written document whose keys appear in some other order
restores identically.

## Encoding and versioning

An archive is one protojson document in a `.json` file, served and uploaded as
`application/json`. The options that decide what it looks like on disk are fixed
in one place per language -- `server/archive/codec.go` and
`client/lib/archive/codec.ts` -- rather than at each call site, because a caller
that forgot one of them would silently write a different format.

| | Go | TypeScript |
| --- | --- | --- |
| snake_case keys | `UseProtoNames: true` | `useProtoFieldName: true` |
| enums by name | `UseEnumNumbers: false` | `enumAsInteger: false` |
| absent stays absent | `EmitUnpopulated: false` | `alwaysEmitImplicit: false` |
| unknown fields ignored | `DiscardUnknown: true` | `ignoreUnknownFields: true` |

Three consequences worth stating:

- Those write options affect **writing only**. Both runtimes accept either
  casing on read, so a hand-written file using `priceDate` parses as readily as
  one using `price_date`.
- Go's protojson injects unstable whitespace deliberately, so that callers
  cannot depend on its exact bytes. The codec normalises its output, which makes
  two exports of the same data byte-identical and therefore diffable. Both
  runtimes emit fields in field-number order, so a document written by the
  server and one written by the browser agree key for key -- there is a test
  that holds them to it.
- A 64-bit integer is written as a quoted string: `"volume": "48088700"`. That
  is protojson's rule for 64-bit types, not a decision of this format.

### format_version

`format_version` is bumped only by a change an older reader cannot survive: a
field removed, renamed or retyped, or a semantic change to one that stays.
Adding a field does not bump it.

A reader refuses a document whose `format_version` is higher than its own, and
accepts any lower one. That check runs **before** the document is parsed,
because the parse is what a mismatch would break -- without it the user gets a
confusing parse error where they should get a sentence telling them the file was
written by a later PortfolioDB.

Its job is not migration. Nothing here lets an old reader read a genuinely
breaking new file; it makes the refusal legible. If forward compatibility is
ever wanted, that is a different mechanism and an ADR of its own.

### What the unknown-field policy does and does not cover

Ignoring unknown fields is what lets an additive change land without making
existing files unreadable, and it is safe only because `format_version` catches
the changes that are not additive.

It does **not** extend to unrecognised enum values. A file naming a broker
outside the vocabulary is refused, because that value is not representable
anywhere in PortfolioDB and not merely in this file. Accepting it would leave a
zero, which reads as a different value rather than as nothing.

Note also that **protojson cannot preserve unknown fields**. Unlike binary
protobuf, which keeps them and re-emits them, neither runtime can round-trip a
field it does not understand. Reading a newer archive and writing it back is
therefore never lossless, at any setting, and tools must not rewrite an archive
they only partly understand.

## Writing an archive by hand

protojson is plain JSON, so nothing needs a protobuf runtime to produce an
archive. The price recovery scripts in `local/scripts/` write one with
`json.dump`.

The minimum valid system document is an envelope and one part:

```json
{"envelope": {"format_version": 1,
              "exported_at": "2026-07-30T00:00:00Z",
              "source_instance": "recover-prices.py",
              "kind": "SYSTEM"},
 "prices": {"groups": [
   {"instrument": {"type": "MIC_TICKER", "value": "AAPL", "domain": "XNAS"},
    "asset_class": "STOCK", "currency": "USD",
    "coverage": [{"from": "2010-01-04", "before": "2024-06-11"}],
    "rows": [{"price_date": "2010-01-04", "close": "30.57"}]}]}}
```

Points a hand-written file usually gets wrong:

- Decimals are strings. `"close": 30.57` is a JSON number and is rejected.
- `before` is exclusive. A span ending on the last day of 2024 has
  `"before": "2025-01-01"`.
- Omit a field rather than writing an empty string or a zero for it.
- Set `share_count_basis` on a price row only for a back-adjusted bar. Setting it
  where the bar is as-traded makes the price wrong by the split factor.
