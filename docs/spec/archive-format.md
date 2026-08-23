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

- **Irreplaceable data.** Transactions, holding declarations, preferences,
  plugin configuration, fetch-block reasons, the corporate events an admin was
  asked to judge and the calls made on them, and hand-recovered prices no
  provider still serves. Nothing can reconstruct these.
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

Transaction grouping has joined that list. The server derives the partition from
the evidence a posting carries rather than being told it
(`docs/adr/0043-grouping-does-not-travel-in-the-archive.md`), so the file carries
postings and the importing instance groups them. The evidence itself -- a
posting's `correlations` -- is irreplaceable data and stays, since a rebuild that
had only the answer could never derive it again.

Portfolio definitions are the one piece of irreplaceable data the archive does
not carry, and their absence is a deliberate omission rather than a claim that
they are recomputable. A definition is a set of filters whose shape is not
settled -- tag-based definitions and portfolios shared between users would both
change what one is -- and writing the current shape into the format would fix it
before the question is answered. A user rebuilding an instance enters their
portfolios again, which is a handful of filter rows against a format that would
otherwise have to be migrated.

Full reasoning: `docs/adr/0032-archive-preserves-inputs-not-derived-state.md`.

## The two archives

Export and import split by data ownership rather than bundling everything into
one file.

- The **system archive** carries shared data and no user data: instruments and
  their identifiers, prices and coverage, corporate events and coverage,
  inflation indices, fetch blocks, unhandled corporate events and plugin
  configuration.
- The **user archive** carries one user's own data and no system data:
  transactions, holding declarations, and preferences.

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

That split also settles who may state instrument identity. Only a system archive
carries instruments and only an admin may import one, so its identifiers are written
on the file's authority; a user archive has no instrument part, and its postings state
identifier hints that resolve exactly as a broker upload's do. Neither needs a rule of
its own -- the boundary is in the message shape. See
[identifiers.md](identifiers.md) for what makes an identity claim authoritative.

Full reasoning: `docs/adr/0033-system-and-user-archives-are-separate.md`.

## The three levels

A file has three levels, and each states its own scope in full.

- **File.** The `envelope`, and nothing else. It carries `format_version`,
  `exported_at`, the source instance and which of the two archives this is.
- **Group.** The entity's aggregate root: the **listing** for prices, the
  instrument for corporate events, the window for transactions, the statement
  for holding declarations. Coverage and asset class live here. A listing is
  named by an identifier plus a currency, since an identifier alone no longer
  picks one out -- see
  adr/0069-a-listing-is-named-by-a-security-identifier-and-a-currency.md. Both
  halves sit on the `instrument` reference itself, so a reference either names a
  security or names one of its lines and there is nowhere for the two to
  disagree. Where a group spans both grains, the currency sits on the row that
  varies by it rather than on the group, under the same rule as every other
  field.
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
types, brokers -- are written by name: `"STOCK"`, `"ISIN"`, `"TRADE_ASSET"`. They
come from `proto/type/v1/type.proto`, which does not remove or rename a value.
See `docs/adr/0038-controlled-vocabularies-are-shared.md`.

The names are the strings the database stores, with one exception. An account
type keeps its prefix -- `"ACCOUNT_TYPE_EQUITY"` in a file, `EQUITY` in the
column -- because protojson writes an enum by its own name and `AccountType`
cannot be unprefixed the way `AssetClass` was: enum values share package scope
and `TxType` already defines `INCOME` and `TRANSFER`. The mapping lives in one
place on the server, as the plugin category's does.

## The envelope

Every archive is one protojson object whose first member is the envelope:

```json
{"envelope": {"format_version": 2,
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
{"envelope": {"format_version": 2, "exported_at": "2026-07-30T00:00:00Z",
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
| `listings[]` | `{currency, valid_from, valid_before, identifiers[], provider_identifiers[]}`. `currency` is ISO 4217 and required -- a line is a currency, so a listing without one is not a line. May be empty: a security nobody has named a line for has none |
| `identifiers[]` | the security-grain ones; `{type, value, domain, canonical, valid_from, valid_before}`. May be empty for a security identified only through its lines -- an equity known by nothing but its ticker |
| `unplaced_identifiers[]` | listing-grain names the file places on no line: a ticker or a SEDOL from a result that stated no currency. Same shape as `identifiers[]`. They name the security and no line of it, which is neither of the two claims above, so grain alone cannot say where a name belongs and the file says (adr/0075) |
| `provider_identifiers[]`, `unplaced_provider_identifiers[]` | the security-grain ones and the unplaced listing-grain ones; `{provider, identifier_type, value, domain}`, where `identifier_type` is the provider's own vocabulary rather than `IdentifierType`. Every type that exists today names a line, so in practice these travel on the listing |
| `underlying` | optional; a reference naming a line of an instrument in the same part -- identifier plus `currency`, since a contract's strike is a price and a price is in a currency, so what it delivers is one currency line (adr/0074). A reference stating no currency names no line and is rejected |
| `cik`, `sic_code` | optional |
| `strike`, `expiry`, `put_call`, `contract_multiplier` | optional; options only |

An identifier's `valid_from` and `valid_before` are the half-open interval in
market time the name was correct for the instrument, and both are optional: an
absent `valid_before` means it is the name the instrument wears now. They travel
because nothing recomputes them, and because an option restated for a split holds
both the symbol it traded under before the ex_date and the one minted for it. A
file that dropped them would leave an already-restated option looking unrestated,
and would leave a file exported before the split naming a symbol the importing
instance has never heard of.

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

Which of the two an identifier belongs to follows from its type and is declared
rather than inferred from whether it carries a domain; see
`docs/spec/identifiers.md`. A security nests its listings rather than stating one
currency because currency, the tradability window and the listing-grain
identifiers are all facts about a line, and a field cannot sit above the level it
varies by.

A listing-grain name the exporting instance could not place on a line is the one
exception, and it rides on the security in a field of its own rather than on a
line invented to hold it. A file has to be able to say "this ticker names a line
of this security and nothing said which", which is what the instance itself
records; a listing with no currency would say something else, and a listing
picked to carry it would say something false.

Importing an instrument the instance already has -- which every rebuild does, as
the currency and FX rows are reference data created before any file is read --
**fills gaps only**. Identifiers the file states and the instrument lacks are
added, columns still empty are filled, and a value already stored always wins.
An import therefore cannot rewrite what the importing instance already knew.

**Not carried:** the server UUID, which means nothing in another instance; a
listing's venues, which are derived from its own identifiers; the nested
underlying subtree and the joined exchange reference data, both of which exist so
the SPA need not fetch them separately and neither of which belongs in a file.
Nor is there an instrument-level validity interval: a tradability window is a
fact about a listing, and the security's is the hull of its listings'.

### Prices

`prices.groups[]` is one group per listing.

| Level | Field | Notes |
| --- | --- | --- |
| group | `instrument` | identifier triple plus `currency`, naming one listing. The currency is required here and is not a hint: a price with no stated currency asserts nothing, and the line whose currency is unknown is not priceable |
| group | `asset_class` | hint for identifier plugin routing on an unknown instrument |
| group | `coverage[]` | half-open intervals |
| row | `price_date` | |
| row | `share_count_basis` | optional; absent means the row's own `price_date`, which is as-traded |
| row | `open`, `high`, `low`, `adjusted_close`, `volume` | optional |
| row | `close` | |

```json
{"instrument": {"type": "MIC_TICKER", "value": "AAPL", "domain": "XNAS", "currency": "USD"},
 "asset_class": "STOCK",
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
list of events, each of which is a `split` or a `dividend`. The group is the
security rather than the listing because coverage and splits are facts about the
security; a dividend is paid in a currency, so its own `currency` field names
the listing it belongs to. The group's `instrument` therefore carries no
currency, and one that does is refused rather than ignored.

That field selects among the lines the importing instance already holds and
mints none, so a dividend naming a currency the security is not quoted in is
queued for review rather than stored, and reported on the import. See
adr/0073-a-dividend-names-a-line-it-does-not-mint.md.

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

Coverage is stored per instrument and plugin -- the fetch is per security -- but an import records every span
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
| group | `instrument` | identifier triple, naming the security and carrying no `currency` |
| block | `category` | `PRICE` or `CORPORATE_EVENT`: which fetcher is blocked |
| block | `currency` | `PRICE` blocks only; which listing is blocked. A corporate event fetch is per security and states none |
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

`unhandled_events.groups[]` is one group per instrument: the events it carries
-- reverse splits, mergers, futures adjustments -- are actions on the security.

| Level | Field | Notes |
| --- | --- | --- |
| group | `instrument` | identifier triple, naming the security and carrying no `currency`: an unapplied event is ruled on for all of a security's lines at once |
| event | `event_type` | `REVERSE_SPLIT`, `NON_WHOLE_SPLIT`, `SPECIAL_CASH_DIVIDEND`, `UNATTRIBUTABLE_DIVIDEND`, ... |
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
preferences first, because a setting is what a later part is read against;
declarations last, because a checked declaration is compared against what the
transactions add up to.

```json
{"envelope": {"format_version": 2, "exported_at": "2026-07-30T00:00:00Z",
              "source_instance": "portfoliodb.example.com",
              "kind": "USER"},
 "preferences": {"display_currency": "GBP"},
 "txs": {"windows": [...]},
 "declarations": {"statements": [...]}}
```

A user archive does not name its user. Restoring one into a different account is
therefore a feature rather than an accident.

### Preferences

`display_currency` is ISO 4217. Absent means the file does not state it and the
importing instance's stored value is left alone. A currency the file spells
wrongly is a validation error against the setting rather than a failed part, and
leaves the stored value alone.

A restored `display_currency` triggers an FX price fetch, once for the whole
import, exactly as setting one through the UI does per change. Without it a
rebuilt instance has no rates for its display currency until the next scheduled
cycle.

### Transactions

`txs.windows[]` is one window per (broker, period). A window is a replacement
scope: import replaces the period rather than appending to it, because
transactions have no natural key -- broker statements often supply a date and
nothing else. See `docs/adr/0002-transaction-ingestion-model.md`.

The window has to state its own period rather than have one inferred from the
postings it holds, because **a window holding no postings is a valid instruction
to clear that period**, and an inferred window could never say that.

An export writes one window per broker. Asked for no period it runs from that
broker's first posting to the day after its last, so the window provably contains
everything it carries and no group straddles its edge.

**An export asked for a period adheres strictly to it.** Every window states the
period asked for, and a group straddling a bound contributes only its in-period
legs -- so the exported group does not balance, which is already legal: a group
whose postings do not sum to zero is accepted and the importer routes the
residual. A broker with no posting in the period gets no window, because an export
is a picture of what is stored and an empty window is an instruction to clear a
period, which is not what the export was asked to say.

Import is the same operation from the other side, and it too takes only the
postings inside the period, so a group straddling a bound keeps the legs the window
does not carry. See
`docs/adr/0039-replace-by-period-deletes-postings-not-groups.md`.

A window's `source` names the export -- `"FIDELITY:archive:export"` -- rather than
an ingestion job: a window carries one source and a broker's postings come from
several. Nothing is lost, because a posting's own source, where one was recorded,
is the `domain` of its `BROKER_DESCRIPTION` identifier, which is where it was
stored and where it travels.

| Level | Field | Notes |
| --- | --- | --- |
| window | `broker` | |
| window | `period_from`, `period_before` | half-open, instants |
| window | `source` | `"<broker>:<client>:<source>"`; the domain of the fallback description identifier |
| window | `postings[]` | may be empty, which clears the period |
| posting | `order_date`, `trade_date`, `instrument_description`, `type`, `quantity` | both dates required; a source stating one date writes it to both |
| posting | `account`, `account_type` | `account_type` absent reads as `ACCOUNT_TYPE_USER` |
| posting | `identifier_hints[]` | zero or more identifier triples, naming the security. Which line the posting is on is settled at ingest from `trading_currency`, so a hint carries no `currency` |
| posting | `unit_price`, `trading_currency`, `settlement_currency` | optional |
| posting | `share_count_basis` | optional; absent means the posting's own `trade_date` |
| posting | `correlations[]` | zero or more; why this posting might belong with another |

`share_count_basis` is on the posting rather than on the window for the same
reason it is on a price row rather than on its group: a window-wide value can
only say "one date for everything", which cannot express the ordinary case,
where every posting is as-traded and so carries a different basis from its
neighbours. Absent is what an ordinary export writes, and the importing instance
takes the posting's own date.

A posting's `identifier_hints` are **not** on that basis. An identifier moves
under a split where a quantity is merely denominated by one -- an OCC symbol
encodes a strike -- and a file names a contract under the symbol current when the
file was written, not under the one it wore on each posting's `trade_date`. So the
vintage of every hint in the part is the envelope's `exported_at`, one value for
the document, and it is what dates the names an identification writes from those
hints.

A broker upload has no envelope, so `UpsertTxsRequest.exported_at` is where it
states the same thing. Where the broker's file dates itself the converter reports
that -- an OFX statement's `DTSERVER`, which is the vintage of the `SECLIST` the
symbols are rendered from. Where it does not, the upload asks: the web upload
offers the last day the window covers, which is the earliest date the file can
honestly claim, and lets the user correct it; the extension states the moment it
drove the export. An upload that states nothing at all is taken to be its own
export and the server stamps its clock at receipt.

What no path does is infer the vintage from a posting's `trade_date`, which is
the error this rule exists to stop.

**Grouping does not travel.** Postings are flat under the window and the file says
nothing about which of them are legs of one event. The importing instance derives
that from the evidence they carry, as it would for a fresh upload of the same
records. See
`docs/adr/0043-grouping-does-not-travel-in-the-archive.md` and
`docs/adr/0041-server-owns-transaction-grouping.md`.

**The evidence is not.** A posting's `correlations` are what its source said about
why it might belong with another posting -- an identifier, what may be compared
about it, and over what set of postings -- and they are irreplaceable data rather
than a partition. Each states a `label`, a `token`, an optional `ordinal`, one
`scope` (`SCOPE_FILE`, `SCOPE_ACCOUNT` or `SCOPE_BROKER`), at least one `match`
(`MATCH_EXACT`, `MATCH_ORDINAL`, `MATCH_ACCOUNT`, `MATCH_ATTACHES`) and an optional
`ordinal_span`.
`docs/spec/postings.md` says what each means; the shape is settled in
`docs/adr/0048-correlations-declare-their-own-semantics.md`.

They travel because a rebuild from an archive has to be able to derive the same
groups the original data did, and the derivation reads evidence rather than being
told the answer. The `scope` travels as the source stated it, `SCOPE_FILE`
included: rewriting it here would throw away what the source said, so an importer
resolves it against its own job instead.

Only a posting transcribed from a source row carries any. A converter's derived
counter-leg and a routed residual transcribe nothing, so they correlate with
nothing, and a token that is present always names something the source itself
issued.

A posting may carry several `identifier_hints`, which the CSV could not express
-- it had one `symbol_type`/`symbol` pair per row. The paired
`exchange_type`/`exchange` columns are gone with it: `identifier_type` already
says whether a domain is a MIC or an OpenFIGI exchange code, so `exchange_type`
was restating it, and the validation that the two were present or absent
together goes too.

A window's postings need not sum to zero, and are accepted rather than rejected:
the importing instance partitions them and routes what each group it draws fails
to balance to, as an `ACCOUNT_TYPE_IMBALANCE`, `ACCOUNT_TYPE_TRANSFER_CLEARING` or
`ACCOUNT_TYPE_SOURCE_ROUNDING` posting.

Those postings are not exported, and neither are the boundary legs beside them. A
residual is arithmetic on the legs of a group the file no longer names, so a
re-imported one would land in a group of its own and be balanced by a
counterparty of its own; it is derived again from the postings that are carried.

**Not carried:** the server UUIDs; `tx_groups.id`, `job_id` and `created_at`,
which the importing instance generates; `tx_groups.timestamp`, which is the
earliest `order_date` of the postings that name the group; the split-adjusted quantity
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
| declaration | `instrument` | identifier triple plus `currency`, naming the line the holding is on. The currency is optional and its absence is meaningful: a declaration on no line at all, which is the holding nothing could place and reports unpriced |
| declaration | `declared_qty` | signed decimal |
| declaration | `share_count_basis` | optional; absent means the statement's `as_of_date` |

A holding is per currency line, so one security may carry a declaration on each
of its lines at one date: two lines differ by an FX rate, and adding them would
report a number in no currency at all.

**Absence is not deletion**, and this is the one place the archive deliberately
differs from the transaction part. A declaration missing from an imported file is
left alone: a file assembled from one statement covers one account and one date,
and treating everything outside it as retracted would delete the user's other
checkpoints. Import is an upsert on (broker, account, instrument, listing,
`as_of_date`).

**The line is resolved and never minted.** A currency the importing instance does
not quote the security in names no line, so the row is rejected the way one that
identifies nothing is -- a user saying how much of something they hold has not
said the security is quoted in that currency, and minting a line from it would
let a typo in a file invent one. A reference stating no currency names no line,
and is carried through as it stands: settling it on import would turn "nothing
said which" into a claim.

The file carries no pad-or-assert discriminator. Which one a declaration is
follows from the declaration dates for its holding -- the earliest pads, the rest
are checked -- so a stored copy could only ever disagree with them. See
`docs/adr/0030-declarations-are-padded-then-asserted.md`.

**A declaration that fails to identify is rejected as a row.** This is the one
part whose instrument resolution is a database lookup and never a creation.
Prices and corporate events resolve an unknown instrument through the identifier
plugins because their rows are worth having either way, and a transaction that
identifies nothing still lands, on a placeholder built from its description --
the event happened. A declaration is a statement about a holding, so an
instrument the system cannot name leaves nothing for it to pad and nothing to
check it against. The row is rejected and the rest of the file lands, which is
what a rejected row does everywhere in this format. A row failing here is a
defect rather than a user error: the user picked the instrument in the UI, and
the export writes an identifier this instance holds.

**A declaration dated before the portfolio start date is rejected too.** Nothing
before the first transaction can be padded to or checked against, so the
recalculation deletes such declarations; writing one and removing it moments
later would report a success the user cannot see the result of. A user with no
transactions at all has no start date, and the whole part fails rather than every
row in it -- the file is fine and the instance is not ready for it. Restoring
transactions before declarations is what avoids both, and is why they are in that
order.

**The pads are settled once, after the part.** A declaration carries no pad --
the pad is derived -- and which declaration pads a holding depends on the others
in it, so the unit of recalculation is the holding rather than the row. The
import writes the declarations and the recalculation that follows every ingestion
settles every affected holding's opening balance from the set as it now stands.

## Producing and consuming a document

One endpoint each way per archive, and one page for each: the system archive at
`/admin/archive`, the user archive at `/archive`. Neither the export nor the
import is an affordance on whatever page happens to own an entity, so a rebuild
is one action rather than a sequence of pages visited in the right order.

**The export takes a menu of parts.** A part left out is absent from the
document. A part included is present even when it holds nothing, which records
that the export asked and there was nothing -- the distinction the wrapper
messages exist for. Restore order is a property of the format rather than of the
request, so the parts are written in it whatever order they were asked for in.

**The user export also takes an optional period**, half-open and open-ended on
either side when a bound is left off. It scopes the transaction part alone --
preferences and declarations are not dated by a period the way a posting is -- and
what it does to a window is above.

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

A part whose unit is a setting rather than a row counts settings: its total is
how many the file states, and a rejected setting carries a row index of `-1`,
because there is nothing to point at. Preferences is the only such part today.

## Restore order

Within a document, the sections are written in the order they are applied, and a
reader walks them in that order:

- **System:** instruments, then prices, then corporate events, then inflation
  indices, fetch blocks, unhandled corporate events and plugin configuration.
  Prices, events, fetch blocks and unhandled events reference instruments.
  Inflation indices and plugin configuration reference nothing, and plugin
  configuration is written last because it is what makes a document a secret.
- **User:** preferences, then transactions, then declarations. Preferences first
  because a setting is what a later part is read against; declarations last
  because a checked declaration is compared against what the transactions add up
  to.

`ArchivePart` numbers the parts in two blocks, the system parts and then the
user parts, because no part belongs to both documents. Within a block the values
run in restore order. An export request naming a part from the other block is
refused rather than ignored: a part quietly missing from a document the caller
asked for it in is a silent wrong answer.

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
{"envelope": {"format_version": 2,
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
