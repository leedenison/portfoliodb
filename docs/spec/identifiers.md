# Instruments

An **instrument** is a security: what an identifier denotes, independent of how it was identified. It holds `id`, asset class, name, CIK and SIC code. **Asset class** is a controlled vocabulary and a tree; its values and what each means are in [The asset class vocabulary](#the-asset-class-vocabulary) below, and null where nothing has said. Instruments with asset class `OPTION` or `FUTURE` must reference an underlying listing.

## Listings

A **listing** is one currency a security trades in. Currency and exchange are facts about a listing rather than about the security, and a security holds one listing per currency: two venues quoting it in one currency are one listing, because they differ by a spread rather than by an FX rate and holdings on them are fungible. A listing's venues are derived from its listing-grain identifiers into `listing_venues`, which is what lets a stated venue pick a line.

Uniqueness is on the **currency family** rather than the raw code. GBX and GBP are one currency under a different unit prefix, so a provider quoting the London line in pence and another quoting it in pounds name one listing; the listing stores the code it is actually quoted in. The family governs that uniqueness and nothing else, and never rewrites a currency code anywhere.

**A line carries a currency or does not exist.** A security nobody has named a line for holds none, and that -- rather than a row standing in for it -- is how "which lines this security has is unknown" is said. A line comes into existence when a provider or a listing-grain identifier asserts one, never to hold a caller's silence, so a resolution that stated no currency mints nothing and names no line. A holding on no line reports unpriced. Cash and FX instruments have a listing degenerately -- a cash instrument's own currency, and an FX pair's quote currency, which is USD under adr/0006-fx-as-synthetic-instruments.md.

**A line has a lifetime and the security does not.** A listing carries the half-open interval it was tradeable in: a delisting closes one, and a redenomination closes one and merges into the line taking over from it. GBX to GBP is neither, the two being one currency family, and a venue migration closes nothing. The security's window is the hull of its lines'. Nothing closes a line today -- an archive stating the bounds is their only writer.

See adr/0068-a-listing-is-a-currency-of-a-security.md, adr/0075-a-name-that-could-not-be-placed-names-no-line.md and adr/0076-a-listing-has-a-lifecycle-and-a-security-does-not.md.

**Instrument tags**: Instruments support **tags** (tag type / tag value). Datasource-specific metadata such as market sector, security type, or similar fields returned by identification or price plugins (e.g. OpenFIGI’s marketSector, securityType) will be stored as tags on the instrument.

## Instrument Identifiers

Instrument **identifiers** are unique identifiers for instruments and consist of three parts: `identifier_type` (required), `domain` (nullable) and `value` (required).  Each triplet is unique within the system.

Most identifiers (eg. ISIN, CUSIP) consist of an identifier_type and an opaque value.  Ticker identifiers include a domain: MIC_TICKER uses an ISO 10383 MIC code as domain, OPENFIGI_TICKER uses a Bloomberg/OpenFIGI exchange code as domain.

### Identifier type properties

Each `identifier_type` carries five properties that the rules below key off.

**Scope** says who is the authority for the value. *Global* identifiers (ISIN, CUSIP, SEDOL, CINS, WERTPAPIER, OCC, OPRA, FUT_OPT, the OpenFIGI types, MIC_TICKER, OPENFIGI_TICKER, CURRENCY, FX_PAIR) are issued by a registry and validated by identifier plugins. *Broker-scoped* identifiers are issued by one broker and mean nothing without it, so the domain names the broker and only that broker can validate the value. *Description* scope (BROKER_DESCRIPTION) is the broker's own text for a security, with the ingestion source as domain.

**Reassignment** says whether the issuer may give one value to a different instrument over time. It is a property this system declares of a type, on the best evidence available and recorded with that evidence, rather than a fact anyone can prove -- so it is revisable when a counterexample appears. The line that matters is whether reassignment is an exception or a practice, because a rare wrong link is correctable while refusing to link at all leaves the security master permanently fragmented. A FIGI is retired and never reassigned; an ISIN, CUSIP, SEDOL, CINS or WERTPAPIER is reassigned only by exception, and all of these may mediate a transitive association. A ticker is reused constantly and across venues, a contract symbol is handed down the strike ladder by most forward splits, and a broker description is not injective at all -- none of these may mediate one. See adr/0061-transitivity-needs-a-non-reassigned-identifier.md.

**Grain** says what the values name: the security, or one listing of it -- which is to say one currency it trades in. MIC_TICKER, OPENFIGI_TICKER, SEDOL and OPENFIGI_COMPOSITE name a listing; every other type names the security. Two listing-grain values are two listings, which is why a symbol appearing under two venue domains is not two results agreeing; two security-grain values are two names for one thing. A SEDOL is assigned per market and per line, and a composite FIGI names a security within a market, which is a currency line. A contract symbol is security-grain: a contract is its own security, cleared in one place, however many venues its underlying trades on.

Grain decides where a row is stored: security-grain identifiers on `instrument_identifiers`, listing-grain on `instrument_listing_identifiers`, each with its own overlap constraint. The two are never queried polymorphically.

**Domain** says what a type's domain does, because grain does not imply one. A ticker needs a domain to say which listing it names, and the domain *scopes* the value: two values under two named domains are about two listings, and the value names no line at all until the domain is there. A SEDOL and a composite FIGI carry no domain, being globally unique without one, as an ISIN is at the level above. A security-grain type's domain -- the source that wrote a description, the broker that issued a contract number -- names something *beside* the value instead, and the value is neither more nor less complete for carrying it. Only a listing-grain type has a domain that scopes, a security-grain type having no line for one to pick out.

**Lines** says how many of a security's currency lines a value of the type reaches, which is not grain restated. A listing-grain value reaches *one* line by definition, and so does a security-grain value whose security has exactly one: a currency or an FX pair is the cash or FX instrument entire, and a contract symbol names a contract cleared in one place. An ISIN, CUSIP, CINS, WERTPAPIER or share class FIGI reaches *many* -- every line the security trades in -- which is to say it named the security and left which line open. A broker description reaches *none*: it is not injective, so it named no security to have lines of. The last two are separate because a stated currency closes the gap the first leaves and cannot close the second. This is the property the candidate stage reads. See adr/0058-candidate-plugins-complete-a-partial-identity.md and adr/0068-a-listing-is-a-currency-of-a-security.md.

**A listing-grain row names a security and a line, and the line may be null.** A result may supply a ticker, a SEDOL or a composite FIGI without supplying a currency, and then nothing says which line it names. The row records that: it carries the security always and the line when something said which, under the same composite `MATCH SIMPLE` foreign key a posting carries, so a name filed on a line of some other security is unrepresentable. Such a name still identifies its security -- a lookup by it resolves, and the derived instrument name may fall back to it -- and it is claimed by a line when the security comes to have exactly one for it to mean. It is never resolved by picking one of several. The same holds for provider-scoped listing-grain rows. See adr/0075-a-name-that-could-not-be-placed-names-no-line.md.

The type property is necessary and never sufficient: a **user-owned** association mediates nothing whatever its type, because identifier rows are owner-scoped while instruments are not, so a chain drawn through one would merge instance-global rows on the strength of one unauthenticated file. A broker contract identifier is the case to watch -- its issuer may well never reassign it, and it still mediates nothing until the promotion sweep makes it system-owned (see **Ownership of user-supplied mappings** below).

### Exchange code normalization

MIC_TICKER domains are always stored as **operating MICs** (ISO 10383 mic_type = 'O'). When a segment MIC (mic_type = 'S') is supplied -- whether from a data provider, CSV import, or API call -- it is silently normalized to the corresponding operating MIC via the `exchanges` table before storage. For example, XNGS (NASDAQ/NGS Global Select Market, a segment) is normalized to XNAS (NASDAQ, the operating MIC).

Consistency checks between identifier plugins and between import hints and resolved instruments compare exchanges at the operating MIC level. Two plugins returning different segment MICs for the same operating exchange are considered consistent. See adr/0003-mic-operating-mic-normalization.md. They compare currencies on the **family** for the same reason: GBX is GBP under a different unit prefix, so a source stating pence and a plugin resolving pounds have named one line. Every currency comparison uses the family -- a rule about what makes two lines cannot hold on one path and not another -- and that includes the one deciding which plugin is offered a line: a price plugin declaring GBP is offered the London line whether it is quoted in pounds or in pence.

**Composite exchange codes name a market, not a venue -- and a market is a listing.** A provider may report a listing under a composite -- OpenFIGI's `US`, EODHD's `US` -- which covers NASDAQ, NYSE and the OTC markets together and does not say which of them the listing is on. It does not need to: those venues share a currency, so the composite names the currency line exactly and leaves only the venue unknown. A composite is not an ISO 10383 concept and is not stored as one: no MIC is recorded against the listing and the provider's code travels as a provider-specific identifier (`EODHD_EXCH_CODE`, or the `OPENFIGI_TICKER` domain). The same holds for the handful of venue codes that cover more than one operating MIC.

A composite is held as the **ISO 3166 country** whose venues it spans, not as the list of venues the provider assigns to it. Providers disagree about the list and agree about the country: OpenFIGI spells its US composite as thirteen operating MICs and EODHD spells its as three, so comparing member lists would let EODHD's narrower one reject a legitimate BATS listing. Of the 168 composites OpenFIGI publishes, 165 are exactly one country's venues; the rest -- a pan-European MTF book, and one code covering both Munich and Douala -- are dropped, because an absent constraint is harmless where a wrong one rejects the right answer.

The rule is that a venue is recorded only when a provider named one. Picking a member of the group instead writes a venue nobody stated, stores it as canonical, and makes a correct venue from another plugin read as a contradiction of it -- so a listing may legitimately have no venue while still recording everything the provider said about where it trades. That costs nothing valuation needs, because the currency is what identifies the listing and the composite supplied it.

Composites still narrow resolution, in two ways. Where a provider returns results across many venues, one reported under a composite covering an exchange the caller named is preferred over a listing elsewhere in the world, and ranked below a result naming that exchange outright.

And a composite **constrains what other plugins may contribute**, where no currency has already settled it. A plugin that named a market rather than a venue leaves the exchange empty, which is not the same as having no opinion: where neither result stated a currency, another plugin's result on a venue in a different country contradicts it and is excluded from the merge, identifiers included. Without that, an empty exchange reads as silence and a London listing of the same ticker can be merged into a security the first plugin placed in the United States, carrying that listing's ISIN with it. Where both stated a currency the currency has already said whether the two named one line, and the country does not overrule it.

### Provider-specific identifiers

Some identifiers are specific to a particular data provider and are not part of the canonical identifier vocabulary. Each row includes a `provider` column (e.g. "massive", "eodhd", "openfigi") and a free-form `identifier_type` specific to that provider.

They are split by grain on the same axis the canonical ones are, into `provider_instrument_identifiers` and `provider_listing_identifiers`. The grain of a provider type is declared in a table of its own rather than read off the canonical vocabulary, because a provider type is a free-form string a plugin invents; an undeclared one names the security, which attaches it to a row that certainly exists rather than to a listing something had to pick. That is the same question asked of a canonical type, applied to the other table: a grain nobody declared is not a listing, and both defaults follow from the one rule. All three types below name a listing.

Examples of provider-specific identifiers:
- **SEGMENT_MIC_TICKER** (provider: massive) -- the segment-level MIC and ticker that Polygon.io's API requires for price and corporate event lookups
- **EODHD_EXCH_CODE** (provider: eodhd) -- EODHD's proprietary exchange code (e.g. "US", "LSE") used to build `ticker.code` symbols for API calls
- **FIGI** (provider: openfigi) -- the venue-specific FIGI (formerly OPENFIGI_GLOBAL), which is tied to a specific trading venue

Provider identifiers are populated by identifier plugins during resolution and stored alongside canonical identifiers. When a price or corporate event plugin needs to fetch data, the orchestrator loads provider-specific identifiers for the plugin's provider ID -- at both grains, since it is asking what the security and every line of it can be keyed on -- and merges them into the identifier list. Plugins prefer their provider-specific identifiers when available and fall back to canonical identifiers.

If a provider-specific identifier is not available (e.g. the instrument was imported without running through the provider's identifier plugin), the provider plugin falls back to canonical identifiers. If those are also insufficient for the provider's API, the fetch fails gracefully and the orchestrator tries the next plugin in precedence order.

Externally understood identifiers (eg. type = `"ISIN"`, `"CUSIP"`, `"MIC_TICKER"`, `"OPENFIGI_TICKER"`, etc) are **canonical** (ie. canonical: true).  Instruments can also be identified by a broker description (eg. type equals a source string like `"IBKR:<client>:statement"`) which is a non-canonical identifier (ie. canonical: false).  This flag is stored in the database and used (e.g. for export) to distinguish broker-description-only instruments without inferring from identifier_type. Whether a canonical name identifies a security at all -- at security grain, on one of its listings, or among the listing-grain names nobody could place -- is one question with one answer, so the API carries it as a derived `identified` field rather than leaving each client to read the three lists and combine them. Broker descriptions are stored as identifiers: `identifier_type` = source (the ingestion request’s source), `value` = full instrument description.

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

If a client supplies one or more external identifiers with a transaction the system resolves the instrument from them, and the transaction is associated with the resolved instrument.  **No (source, NULL, description) identifier is stored in this case** (the client's identifiers are authoritative; see adr/0004-instrument-resolution-and-merge.md). A later upload with the same source and description but without those identifiers will go through candidate proposal and resolution and may resolve to a different instrument.

The candidate stage is not skipped merely because identifiers were supplied. It runs when the identity they state is **incomplete**, which means the source did not say which currency line the instrument traded on: an ISIN or CUSIP alone is not complete, and neither is a bare ticker.  A line is a security and a currency, so a source may state the two halves apart: a **trading currency** completes an identifier that had already reached the security, which is why an upload stating an ISIN and a trading currency -- the common broker case -- does not reach this stage at all.  It completes nothing else.  Beside a bare ticker it names the line of no particular security, tickers being reused across venues, and beside a broker description it names the line of whichever security the text turns out to mean; both still reach the stage.  The currency read is the one the source stated the security is quoted in, never what the record settled in.  A SEDOL, a MIC_TICKER carrying its MIC and a composite code are complete for the same reason: each names a line.  So are the two that name an instrument having only one line -- a currency or FX pair, and a contract symbol (OCC, OPRA, FUT_OPT).  A share class FIGI is not: it is the class the lines belong to, and reaches every one of them as an ISIN reaches every line of the security.  Which types are complete is declared as the **Lines** property of the identifier type rather than listed at the gate, so a type added to the vocabulary is answered the day it is added.  Only a broker upload is offered completion; an archive states one identifier per posting out of an identity already resolved elsewhere, which is a pointer to an instrument rather than a partial description of it.  An archive posting that names no identifier still reaches the candidate plugins on its description.  See adr/0058-candidate-plugins-complete-a-partial-identity.md.

Completion is considered only after both database lookups have missed, so a key the database already answers -- by its description or by the identifiers it states -- is never paid for.

If the identifiers a client supplies resolve to more than one instrument, what happens depends on where the contradiction is (see adr/0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md).

If **one file** states claims at one vintage that cannot all hold, the artefact is faulty: no reading of the validity intervals reconciles claims that share a vintage. The converter refuses the file before upload, and ingestion checks again rather than trusting the converter. The upload is rejected.

If **the file and the database** disagree, this is an identity failure and not a transaction failure. The transactions were never in doubt; only which instrument they belong to is. The instrument degrades to broker-description-only, exactly as an identifier plugin timeout already causes, the contradiction is recorded for an admin, and the upload is accepted. Rejecting it would strand the user behind an admin for a corporate action neither knew about.

A **transaction fault** -- an unparseable amount, an unbalanced group -- rejects the whole upload. Accepting the sound rows and dropping the rest leaves holdings in a state nobody can characterise, where rejecting the upload leaves them describable as valid up to the last accepted import.

A broker's claim about its own contract identifier is a special case. Chaining through it to link two global identifiers produces a claim that lies wholly in the identifier plugins' domain, and a broker file is user-mediated: unauthenticated, single-shot and impossible to re-interrogate. Such a derived claim is therefore a **hypothesis** for an identifier plugin to settle rather than something to act on, recorded on the same surface as a contradiction. See adr/0062-a-user-mediated-claim-is-a-lead-not-a-write.md.

If the client supplies only a broker, source and description with optional hints, the system will attempt to extract candidate identifiers from the description.  This is done via the "description" plugins at `server/plugins/<datasource>/description` (see below).  If the extraction succeeds then resolution continues using the extracted identifiers.  **A (source, NULL, description) identifier must be stored in this case as an authoritative mapping from the broker description.**

Extraction failure is treated as an identity lookup failure.  **A (source, NULL, description) identifier must be stored in this case as a mapping from the broker description.**

### Resolve Identifiers

When a client supplies external identifier hints for a transaction, or when a candidate plugin has successfully extracted one or more identifier hints from a transaction description, the identifier plugins will attempt to look up canonical instrument metadata and canonical identifiers for the instrument.
 
Resolution order: (1) DB lookup by (source, NULL (domain), instrument_description) or by existing identifiers; (2) within the current batch, use a cache so the same (source, description) is resolved once; (3) only if still unresolved, call enabled plugins (passing broker, source, instrument_description, currency hint, and identifier hints). See adr/0004-instrument-resolution-and-merge.md.

When a client supplies identifier hints and those identifiers name no instrument, the description is looked up too. Where it names an instrument holding no canonical identifier -- one that is nothing but a broker description -- resolution binds to that instrument rather than creating a second one beside it, and what identifies it is written on to it: the null columns are filled and the identifiers inserted. A description naming an instrument that has already been identified does not bind, since a broker description may not associate two identities (see adr/0061-transitivity-needs-a-non-reassigned-identifier.md). No (source, description) identifier is minted either way; the one the binding names is already stored. See adr/0067-an-instrument-with-no-identity-is-completed-in-place.md.

If no plugin resolves a given (source, instrument_description), the system ensures an instrument exists with at least that source identifier and attaches it to the transaction. Plugin failures (e.g. timeout, unavailable) are handled the same way: persist the transaction with the broker-description-only instrument and record an identification error; do not fail the whole job. Identification reporting (e.g. GetJob, UI) must distinguish between description extraction failure (no candidate plugin returned identifiers), identifier resolution failure (extraction succeeded or client supplied identifiers but no identifier plugin resolved), and plugin failure (e.g. timeout, unavailable).

**Instrument merge**: A merge acts on an **identity claim** -- an assertion that two identifiers denote one instrument -- and never on anything else. The claim must be corroborated by an authority: one identifier plugin result naming both identifiers -- by returning both, or by returning one while strictly filtering on the other -- or a chain through a third identifier whose association is system-owned, whose type does not routinely reassign its values, and whose two halves have overlapping validity intervals. A value the result was **strictly filtered on** counts as named: a provider that answers "no identifier found" when its filter matches nothing has asserted that the filtered value denotes the security it described, so OpenFIGI mapping a stated ISIN and answering with a FIGI corroborates that pair even though it deliberately does not echo the ISIN back. A filter the provider silently relaxes is a hint and confirms nothing. A set of identifiers assembled by the resolver from several identifier plugin results is not a claim anybody made, and does not merge; two results agreeing about a currency and a venue have not said they are the same security, and treating that as identity is how two share classes on one venue become one instrument. Remaining fields are still checked for consistency, but only identity claims merge. See adr/0060-an-identity-claim-is-admitted-by-the-authority-for-its-scope.md and adr/0061-transitivity-needs-a-non-reassigned-identifier.md.

Where the claim is corroborated and links two previously distinct instruments (e.g. same ISIN), the system must merge them: choose a survivor, update all transaction references, move identifiers to the survivor, delete the merged-away instrument.

**A merge unions the listing sets by currency family.** The survivor is given a line in every family the loser holds, and everything hanging off the loser's line moves on to it: its postings, declarations, names, prices, coverage, fetch blocks and dividends. Because currency is the key rather than an attribute, a collision *is* a merge -- there is no case where two survivors have to coexist and nothing says which wins -- and the survivor's row wins where two rows describe one thing. Coverage is the exception: two spans of one line are not duplicates but one answer over their union, and they are merged as a fetch merges them rather than deduplicated. A posting or declaration that named no line still names none. See adr/0071-listings-merge-by-currency-and-an-unknown-one-splits.md. When **multiple** identifiers returned for the same logical security resolve to **more than one** existing instrument (e.g. instrument A has ISIN 1, instrument B has CUSIP 1, and an identifier plugin returns both identifiers for one security), the system **detects** this and **merges** those instruments eagerly during resolution. After merge, a single canonical instrument remains; the survivor is chosen as follows: the instrument with more identifiers wins; if tied, the one with older `created_at` (further tie-breaker may be non-deterministic). All updates (transaction references, identifier moves, deletion of the merged-away instrument) happen in one database transaction; implementations may batch the updates within that transaction for scale.

Merge runs **eagerly** when such a conflict is detected during the resolution step (ingestion path); see adr/0004-instrument-resolution-and-merge.md. When an identifier plugin returns identifiers that match an **existing** instrument, the **identifier is the source of truth**: attach the transaction to that instrument and do not overwrite its canonical fields with the identifier plugin's output. That protects a value a system authority wrote. An instrument named only through a user-mediated channel holds no such value, and its metadata is discarded and replaced rather than defended (see **Two classes of instrument** below). An identifier the instrument does not yet hold is written onto it when it is authoritatively corroborated with a name the instrument already holds, under the same rule the merge uses -- one rule, asked at two call sites.

**A merge that cannot complete does not proceed.** If the survivor and the loser both hold one identifier triple over overlapping intervals, the two claims cannot both hold and nothing in the data says which is right: either two instruments were validly but wrongly identified, or a corporate event nobody knows about would have closed one of the intervals. The merge stops, both instruments remain, and the collision is recorded for an admin. It must not be resolved by dropping the colliding name, which destroys the only evidence that a contradiction was seen.

Corroboration is asked of stored claims rather than decided once at ingestion. A merge unjustified today can become justified when a provider starts returning a field it did not, or when an admin enables an identifier plugin or pays for a richer tier, so the periodic re-identification below asks the question again.

**Metadata is security-level or listing-level, and they attach to different rows.** Only an identifier plugin is authoritative for the metadata of a system-authoritative instrument, and metadata never triggers a merge. Security-level metadata -- name, asset class, CIK, SIC code -- is a fact about the security and propagates across every identifier corroborated as denoting it. Listing-level metadata -- currency and venue -- is a fact about one listing, and is written on to the listing the result named. This used to need a rule forbidding it to propagate through a security-grain identifier, because there was one row and a currency learned from the London line would be asserted of the New York one. There is now a row per line, so the constraint is structural rather than a rule: a result that names a line supplies that line's currency and venue, and a result that names only the security supplies neither.

**Consistency between two identifier plugin results is decided by the currency.** A line is a currency of a security, so two results stating currencies of one family have described one line however many venues they name between them and the lower-precedence one is merged; two stating different families have described two lines, and the lower-precedence result is excluded from the merge, identifiers included. Where either result stated no currency the venue stands in for it, and a result on a venue outside the venue or the market the other named is excluded on the same terms. A venue never decides on its own account: two venues quoting one currency are one listing, and the domain of a listing-grain identifier is the venue the result already named rather than a second discriminator.

Identifier values are then compared on the **subject** two results share -- the type and, where it has one, the domain. A difference in the value under one subject excludes the lower-precedence result, unless the winner also named that value elsewhere in its own answer. An identifier naming no domain names no particular listing, and is not compared against one that does. See adr/0068-a-listing-is-a-currency-of-a-security.md and adr/0077-a-venue-set-is-what-we-know-not-what-exists.md.

**And a result that contradicts nothing is still not admitted until something names the security both results describe.** The lower-precedence result is merged only where each of them named one identifier of **security grain** -- by returning it, or by strictly filtering on it -- with the same value. Where nothing does, it is excluded and contributes nothing: not its identifiers, of either grain, and not the fields the winner left blank. Agreement about the line is not evidence about the security, because the identity a resolution starts from is routinely ambiguous: a file names a symbol and a currency and no venue, and one symbol is quoted in one currency in more than one place, so two results agreeing about the currency have restated the query rather than shown they picked the same security. A ticker, a SEDOL and a composite FIGI name a line and do not corroborate; a broker description names a security but is not injective, two securities being able to wear one, and does not corroborate either. The refusal is recorded as `discarded_uncorroborated`, distinct from the `discarded_inconsistent` above: nothing contradicted the result, and nothing tied it to the winner. See adr/0078-merge-admission-needs-a-security-both-results-named.md.

**Duplicate (source, instrument_description) in same batch with different plugin results**: Resolve each (source, instrument_description) once per batch and cache the result. All transactions in the batch with that key receive the same instrument_id. No per-transaction plugin call for the same key—ensures consistency and avoids extra plugin cost.

**Plugin is unavailable or times out (e.g. external API down)**: Create or find the broker-description-only instrument, set instrument_id, persist the transaction, and record an identification error/warning (e.g. plugin timeout) for GetJob and UI. Do not fail the whole job. Optional: retry the plugin once with backoff before falling back.

**No fixed "complete" set of identifiers**: What identifiers exist for an instrument depends on enabled plugins and instrument type (e.g. some instruments have no CUSIP). The system does not treat "only one standard identifier known" as a hard error for that reason. Merge-on-conflict (above) handles the case where the same security was previously stored under two different instruments (e.g. one had only ISIN, another only CUSIP) by merging them once one identifier plugin result names both.

PortfolioDB should periodically attempt to identify instruments in case datasources have been updated. Admin users can manually force a refresh for a given instrument or set of instruments.

Re-identification and merge change shared reference data retroactively: which instrument a historical transaction rolls up to can differ from what it was last month, and holdings computed then may not reproduce. See [bitemporality.md](bitemporality.md#retroactive-restatement).

**An identifier is valid over an interval.** Each row carries a half-open `[valid_from, valid_before)` in market time saying when the name was correct for the instrument, and the (identifier_type, domain, value) triple denotes one instrument at a time rather than for all time. So an option holds both the OCC symbol it traded under before a split and the one minted for it, and a broker file exported either side resolves to the same contract. See adr/0055-identifier-validity-is-an-interval.md.

Resolution does not yet use the interval to choose between holders. A lookup by value takes the name in force and falls back to the most recently closed one, so it still answers "which instrument holds this identifier now" rather than "which instrument held it on the transaction's date"; asking the second question is [0122](../issues/0122-resolve-identity-as-of-a-date.md). A merge deletes the loser -- its canonical fields, and the splits that cascade from it, are gone, and nothing records what was believed before, though the loser's names travel to the survivor with their intervals intact, unless one of them collides, which stops the merge rather than dropping the name (above). What hangs off its lines travels with them (see the union above), so the identity judgement is what a merge loses rather than the history. See adr/0004-instrument-resolution-and-merge.md.

### Two classes of instrument

**Every identifier and every piece of metadata on an instrument has been confirmed by a source authoritative for that instrument's class.** A **system-authoritative** instrument holds at least one identifier that arrived through a channel carrying system authority -- an identifier plugin, an admin upload, reference data. A **user-authoritative** instrument holds only identifiers and metadata that arrived through a user-mediated channel: a broker description today, and a broker contract identifier once one is carried. A broker-description-only instrument is the case of this the system has always had, stated as a class rather than as a shape.

The axis is the channel, not the scope of the identifier. A broker is the only possible authority for its own contract numbers, and what costs those values their standing is that the only route they take into this system is a file a user handed us. Given a direct feed from the broker they would carry system authority exactly as a plugin's answer does. Scope is therefore the current proxy for the class, and the owner column below is what replaces it: an authority wrote the row when the owner is null.

**Metadata follows the identifiers.** Everything on a user-authoritative instrument arrived through a user-mediated channel, by definition, so the class of the instrument is the provenance of its metadata and nothing has to be recorded per column to know it. When such an instrument becomes system-authoritative -- completed in place, or absorbed into one that already was -- its own metadata is discarded and replaced by what the authority supplied, rather than merged with it or kept on the strength of having been stored first.

**A user-mediated claim does not merge a user-authoritative instrument into a system-authoritative one.** It writes an owned identifier row on the system-authoritative instrument, and the user-authoritative one stays where it is for everyone still resolving through it. That is why user-authoritative instruments are shared rather than owned: their metadata is provisional, and a user-owned association mediates nothing, so nothing moves one user's postings on another user's word until the promotion sweep has made the mapping system-owned.

See adr/0079-an-instrument-carries-the-authority-of-the-channel-that-named-it.md.

### Ownership of user-supplied mappings

A broker file reaches the system through a user. It is unauthenticated, so nothing attests it came from the account it names; it is a single artefact, so a stale export, a misgrab or a hand edit is invisible; and it cannot be re-interrogated. Instruments are instance-global, so a merge driven by one upload rewrites reference data for every user. A broker is still the only possible authority for its own contract identifiers and descriptions, so the trust is forced -- what bounds it is ownership.

`instrument_identifiers` carries an **owner**. Null means system-owned, which is what an identifier plugin writes. A broker-scoped identifier or a broker-description association arriving in a regular user's upload is written owned by that user and resolves for that user alone: lookups are owner-scoped with a system fallback.

**Promotion.** A periodic sweep counts the users holding each user-owned mapping. Where the count reaches a threshold the admin configures and no user holds a conflicting mapping, the mapping is promoted to system-owned and the user rows it came from are deleted. Where users conflict, the mapping is listed for an admin to resolve; resolving it deletes the user rows agreeing with the winner and leaves the losing rows in place, so a user whose file said otherwise keeps working and the disagreement resurfaces rather than being decided by deletion.

The threshold validates the **channel**, not the claim: every user reads the same mapping out of the same broker security master, so agreement between them says nothing about whether the broker is right, only that the file was not doctored, stale or from the wrong account. A small number is therefore the whole of the evidence available. The threshold must be allowed to be one and an admin must be able to promote by hand, or a single-user instance never promotes anything.

**Uploads by an admin.** An archive uploaded by an admin is authoritative at every level, including its instrument data. A broker transaction file uploaded by an admin is treated exactly as a regular user's. The distinction is the artefact and the act rather than the person: an archive is this system's own format, produced by an export that validated what it wrote, and uploading one is deliberate curation, where a broker statement is an unvetted third-party file uploaded as a matter of routine.

**Archives need no rule of their own.** A user archive has no instrument part, so a regular user cannot state instrument data in one, and importing a system archive is admin-only. The separation already puts that boundary in the message shape (see [archive-format.md](archive-format.md)). Ownership therefore governs what arrives through **transaction uploads** -- a broker file, or the postings of a user archive, both of which state identifier hints and neither of which carries instrument data.

See adr/0062-a-user-mediated-claim-is-a-lead-not-a-write.md and adr/0063-identity-claims-are-owned-until-users-corroborate-them.md.

### User override

A user may believe the system has mis-identified an instrument. It should be possible for a user to override the identity for their portfolio. They do this by ensuring that the client provides an external identifier hint for the transactions they want to override.  This will then be looked up directly rather than using identifiers extracted from the description.  Admin users can correct shared instrument identities in the admin UI.

### Security type and transaction handling

The **security type hint** is the asset class the source stated on the posting
(`asset_class_hint`; see tx-types.md). It is passed to description and identifier
plugins for routing only: a plugin is offered a row when the class the source
stated and a class the plugin declares could describe one security. A plugin
covering shares declares STOCK and is offered the EQUITY a statement line says;
a cash plugin declares CASH, which no security class lies under or over, so only
a row whose source stated cash can reach one.

For routing the stated class is floored at SECURITY: a posting that stated
nothing, or stated only the root, routes as a security of unstated class, so an
unidentifiable security cannot silently resolve to its trading currency. The
floor is a routing decision and not a claim -- what the source actually stated is
what is stored and what a contradiction is measured against.

#### Type layers

- **Transaction type** (declared/resolved): what kind of event the posting is a
  leg of; says nothing about asset class. See tx-types.md.
- **Security type hint** (routing): the stated `asset_class_hint`, one value of
  the asset class vocabulary below. Empty when the source made no claim, which
  is weaker than any value in it.
- **Asset class** (canonical): the same vocabulary, set by identifier plugins
  and stored on instruments.

#### Comparing two classes

Nothing compares two asset classes for equality. Every comparison is one of two
questions, and which one it is depends on what turns on the answer.

**Does this contradict?** Permissive, and symmetric. Two claims contradict when
no reading admits them both -- a stated EQUITY and a resolved ETF have not
disagreed, and a stated STOCK and a resolved ETF have. Silence contradicts
nothing, and neither does a claim of the root, which rules nothing out. This is
what refuses a transaction at ingest and what a reported hint difference means.

**Does this corroborate?** Strict, and asymmetric. A resolution corroborates a
stated class when the claim ruled something out and the answer falls inside it:
EQUITY is corroborated by ETF, which had to land in one of three and did, while
STOCK is not corroborated by EQUITY, which never reached the question.
Corroboration is a stronger thing than the absence of a contradiction, because a
result that says almost nothing contradicts almost nothing; it is what decides
whether a guessed identifier was actually tested.

#### The asset class vocabulary

The values form a tree, as the transaction types do. A leaf is the specificity
the system acts on; an internal node is what a less specific source says, and
both are legal values -- stated on a posting and stored on an instrument alike.
The node exists so that a source is not made to pick: an OFX file has no ETF
type, so an ETF trade arrives as a stock trade, and EQUITY is how that file says
what it knows rather than asserting something it does not. A plugin that
classified a security only as far as a derivative says DERIVATIVE for the same
reason.

| Value | Parent | Means |
|---|---|---|
| `UNKNOWN` | -- | money or a security, nothing narrower |
| `CASH` | `UNKNOWN` | money, in one currency |
| `SECURITY` | `UNKNOWN` | a security of unstated class |
| `EQUITY` | `SECURITY` | a shareholding, direct or pooled |
| `STOCK` | `EQUITY` | a direct holding in a company |
| `ETF` | `EQUITY` | an exchange-traded fund |
| `MUTUAL_FUND` | `EQUITY` | a fund not traded on an exchange |
| `FIXED_INCOME` | `SECURITY` | a debt instrument |
| `DERIVATIVE` | `SECURITY` | a contract whose strike is quoted in a line of something else |
| `OPTION` | `DERIVATIVE` | requires an underlying listing, a strike, an expiry and a right |
| `FUTURE` | `DERIVATIVE` | requires an underlying listing |
| `FX` | `SECURITY` | a synthetic currency pair; see adr/0006-fx-as-synthetic-instruments.md |

`EQUITY` here is a value of this vocabulary and is unrelated to the
`ACCOUNT_TYPE_EQUITY` of [postings.md](postings.md#account-types), which names
the non-asset side of a posting.

`UNKNOWN` is the root and is distinct from the field being unset: a source
stating it says it does not know, where a source stating nothing has not been
asked. It is never a routing hint -- a posting that made no claim routes as a
security, so that an unidentifiable security cannot resolve to its trading
currency.

The requirements the leaves carry do not travel up: an instrument classed
`DERIVATIVE` has not been resolved to an `OPTION` or a `FUTURE` and so carries
no strike and needs no underlying line. `FX` sits under `SECURITY` rather than
beside `CASH` because a pair is an instrument that holds a price, where a `CASH`
instrument is the money itself.

The hierarchy is written twice, in `server/assetclass` (Go) and
`client/lib/asset-class.ts` (TypeScript). Both are checked against the golden
fixture `server/assetclass/testdata/tree.json`, and each language asserts every
value in its generated enum appears exactly once in its parent map, so the two
spellings cannot drift. The `chk_asset_class_vocabulary` CHECK in the schema is
a third statement of the values, held to the proto by a test that reads it back
out of the catalogue.

### Candidate Plugins

When what the source stated leaves the identity incomplete (see above), the system uses **candidate plugins** to propose the identifiers it did not state. Candidate plugins live at `server/plugins/<datasource>/candidate` (e.g. `server/plugins/openai/candidate`). They are given the broker description, the identifiers the source did state, and the currency and security-type hints (e.g. from a broker statement or confirmation) and return zero or more identifier hints (type, domain, value) that are then passed to the identifier resolution step. If a candidate plugin successfully extracts one or more identifiers, resolution continues using those extracted identifiers and a (source, NULL, description) identifier is stored as the authoritative mapping from the broker description. If extraction fails (no plugin returns identifiers, or the description is unparseable), the system treats it as an identity lookup failure: a (source, NULL, description) identifier is still stored and the instrument is created or found as broker-description-only.

The OpenAI candidate plugin asks for a schema-checked object rather than parsing JSON out of a chat reply: `response_format` names a strict JSON schema, every field is required and nullable, and a reply that does not match is a failure rather than something to parse around. It sends only the fields the source supplied, so the model is never shown a blank to fill, and it returns a per-field confidence. **Confidence is recorded and never gated on**: a model's self-report is uncalibrated, and turning it into a threshold before anything has measured whether it correlates with correctness would be inventing a number. What decides whether a proposal is used is the resolution.

A proposal is validated in code rather than in prose. An OCC symbol that will not parse is dropped rather than offered, a field the source already supplied is dropped however firmly the model was asked not to return it, and a venue travels as the domain of the ticker it qualifies -- an exchange with no symbol to qualify names nothing that can be looked up.

Candidate plugins have **precedence** (integer, required, unique across candidate plugins; stored in the database with plugin config). They are executed **in series** by precedence order. The **first** plugin that returns one or more identifier hints is used; no later plugin is called for that transaction. If no plugin returns identifiers, extraction has failed. Candidate plugins receive the broker, source, instrument description, the identifiers the source stated, and optional client hints (e.g. currency), so they can complete what is missing rather than guess it from the description alone. They have no database access, so nothing they propose has been checked against reference data; the resolver filters proposals against the identifier vocabulary and the exchange table before passing them on. Like identifier plugins, they are compiled in and enabled at runtime; configuration (e.g. API keys, options) is stored in the database and only admins can view or edit it. The shared interface and types for extracted identifiers live with the identifier resolution code (e.g. under `server/identifier`). Only after candidate plugins run (and only when they return identifiers) does the resolver call identifier plugins to look up canonical instrument metadata and canonical identifiers.

### Identifier Plugins

Plugins implement a single interface that accepts everything known about the instrument, e.g. `Identify(ctx, config, broker, source, instrument_description, identity) → (Result, error)`, where `identity` holds what a source **stated**, what a plugin **proposed**, and the currency and security-type hints, where `Result` carries the instrument, the identifiers and the plugin's telemetry for the call (see [telemetry.md](telemetry.md)). Exchange information is carried on identifier hints (MIC_TICKER domain = MIC, OPENFIGI_TICKER domain = exchange code). The hints struct carries currency and security type hint; only API-confirmed data is stored on the instrument. For options and futures, the plugin may also return data for the underlying instrument; the caller is responsible for ensuring the underlying exists and is linked to the derivative. The resolver passes each plugin’s config JSON from the database into `Identify`; plugins may use it for API keys and options (only admins can view or edit plugin config). Implementations live under `server/plugins/<datasource>/identifier` (e.g. `server/plugins/local/identifier`, `server/plugins/ibkr/identifier`). The shared interface and canonical types (Instrument, Identifier) live in `server/identifier`. Plugins are compiled in and enabled at runtime; configuration is stored in the database.

**A proposed identifier is not evidence.** A candidate plugin may propose an identifier no source stated -- a ticker for a row that carried only a CUSIP, a venue for a bare ticker. Resolution keeps the two apart. A proposal never displaces what a source stated: it never raises the conflicting-identifier-hints validation error, and is never written back as an identifier, so it cannot draw a second instrument into a merge. Where a source stated an identifier, that is what is queried and looked up and a proposal alongside it only narrows and ranks; where a source stated nothing, a proposal is the only key resolution has and is queried as such. An identifier plugin may narrow or rank with one -- preferring the listing on a proposed venue over one elsewhere in the world is the point of passing it -- but must not return it.

**An invented identifier round-trips before it is trusted.** Where a source stated no identifier at all, the proposal is the only key resolution has and is queried as such, so a provider answering about it proves the value names some security and not that it names this one. A result reached that way is kept only if it agrees with something nobody guessed -- the security type the transaction stated, or an identifier the source stated. Agreeing with nothing is the absence of a test rather than a near miss, and the result is dropped in favour of a broker-description-only instrument: a blank is visible, repairable and does not propagate, where a wrong `(source, description)` binding is canonical and never re-examined. The currency the transaction states does the most work here: OpenFIGI's mapping call filters on the currency it is given and answers `No identifier found` when the security has no listing in it, so a plugin recording that currency is recording something the provider confirmed rather than echoing a hint back. A guessed ticker naming the wrong company therefore survives only if that company also trades in the currency the source stated. A source that stated an identifier is not subject to this: the proposal there only ranked among listings the stated key produced. See adr/0059-an-invented-identifier-round-trips.md.

Winner selection has three tiers in order: agreeing with something a source stated, then agreeing with something a plugin proposed, then precedence alone. A proposal can break a tie among plugins that all answered; it can never outrank a statement, and a contradicted proposal costs a result its place in the middle tier and nothing more. The middle tier also refuses a result that contradicts what a source stated, so a guess cannot promote a result that argues with the file it came from. See adr/0057-a-proposed-identifier-is-not-evidence.md.

**An identifier plugin declares what it claims.** Alongside its precedence and config, it declares which identifier types it returns, which of them it returns **together**, and which it **strictly filters** on -- a plugin that never returns ISINs may still be the only thing able to confirm one. The declaration is an analysis surface, not a gate: nothing enforces that it returns what it said, and a provider changing its response shape drifts the two apart silently, so a merge is never decided on a declaration. What it answers is what the enabled set makes possible -- whether anything could have corroborated a given association, whether such a plugin's silence about a field is informative or merely its normal output, and which shapes of erroneous merge a configuration can produce before any data arrives. Enforcement looks instead at what a call actually returned or was filtered on: results reach the merge site **partitioned by the result that produced them** rather than flattened, because what makes an association a claim is that the identifiers arrived together. Which plugin they came from does not enter the merge decision -- every identifier plugin is equally authoritative for a global identifier. See adr/0065-a-plugin-declares-what-it-claims-a-call-records-what-it-claimed.md.

Candidate plugins carry no such declaration. Their output is already recorded per field (see [telemetry.md](telemetry.md)) and none of it can merge, since a proposal is not evidence (see **Candidate Plugins** above and adr/0057-a-proposed-identifier-is-not-evidence.md).

**Plugin config** (in DB): plugin id, enabled flag, **precedence** (integer, required, unique across plugins; used to resolve conflicts), and optional config JSONB. Scope is global for the initial milestone. No two plugins may share the same precedence.

**Resolution flow**: If the DB or in-batch cache already has an instrument for (source, instrument_description), do not call plugins. Otherwise call **all enabled plugins in parallel**, then merge results: instrument metadata (name, asset class, etc.) from the highest-precedence plugin that succeeded, with any field it left **empty** filled from the highest-precedence plugin that was admitted to the merge and supplied one (a value already present is never replaced; an exchange is filled only from within the country the winner named, if it named a market rather than a venue; and the asset class is never filled -- it decides which invariants the row must satisfy, and the fields those invariants need come from the winner); **identifiers merged** from all successful plugins—for each identifier **subject** (the type and, where it has one, its domain, normalized to the operating MIC), the value from the highest-precedence plugin that returned that subject is used (non-overlapping subjects are combined; same-subject conflicts resolved by precedence). The subject and not the type alone: a ticker under two domains names one line at two venues, and keying on the type would keep whichever arrived first and drop the other, taking its venue out of the line's venue set with it. The resolver **must always** ensure the (source, instrument_description) identifier is stored on the instrument (whether from plugin results or as the only identifier when no plugin resolves). That way future uploads with the same (source, description) resolve by DB lookup and do not call plugins again. Then find or create an instrument (with at least the broker identifier) and set the transaction’s instrument_id. If no plugin resolves (e.g. returns a “not identified” sentinel), the service still ensures a broker-description-only instrument exists and attaches it. Record identification errors/warnings (e.g. plugin timeout, broker-description-only) for GetJob and UI.

Plugins can own database migrations (e.g. reference tables). Plugin migrations live in the plugin directory (eg. `server/plugins/<datasource>/identifier/migrations`). Example: the local reference-data plugin uses a Postgres reference table.

## Troubleshooting: identification not running

**Identification runs only during ingestion.** Instrument resolution (candidate plugins → identifier plugins) is performed by the ingestion worker when transactions are submitted via the **Ingestion gRPC API** (UpsertTxs or CreateTx). If transactions were loaded by another route (e.g. direct SQL into `txs`, or a script that does not use the ingestion API), no jobs are created and the worker never runs, so no identification and no telemetry run to read it in.

**Identifier plugins run only when there are identifiers.** The flow is: DB lookup by (source, description) or by the identifiers the source stated → if miss and the stated identity is incomplete, run **candidate plugins** to propose the rest → then run **identifier plugins** with what the source stated and what was proposed. If the source stated nothing and the candidate plugin returns **no proposals** (e.g. OpenAI API error or invalid model), the server creates a broker-description-only instrument, records the identification error as "description extraction failed", and **never** calls identifier plugins. A source that stated an identifier still reaches them, with or without a proposal. So no identification attempt is opened, no `identifier_plugin_call` rows are written, and no OpenFIGI calls are made.

**Diagnosis steps:**

1. **Confirm transactions were ingested via the API**  
   If there are no ingestion jobs, or no recent jobs for your upload, identification did not run:
   ```sql
   SELECT id, user_id, broker, source, status, created_at FROM ingestion_jobs ORDER BY created_at DESC LIMIT 20;
   ```

2. **Check identification errors**  
   If jobs exist and completed, look at stored identification errors. Message `description extraction failed` means no candidate plugin returned hints (e.g. OpenAI failing); other messages indicate identifier plugin timeouts or "broker description only":
   ```sql
   SELECT j.id, j.status, e.row_index, e.instrument_description, e.message
   FROM ingestion_jobs j
   JOIN identification_errors e ON e.job_id = j.id
   ORDER BY j.created_at DESC, e.row_index
   LIMIT 50;
   ```

3. **Server logs**  
   With `LOG_LEVEL=debug`, the server logs:
   - `candidate plugin returned error` — a candidate plugin (e.g. OpenAI) failed; the `err` field shows the cause.
   - `description extraction: no plugin returned hints` — no candidate plugin returned any hints.
   - `instrument resolution: description extraction failed, using broker description only` — we are creating broker-description-only instruments and not calling identifier plugins.

4. **OpenAI candidate plugin**  
   Ensure `candidate plugin config` uses a **valid model** (e.g. `gpt-4o-mini`, `gpt-4o`). An invalid model (e.g. `gpt-5.2`) causes the API to return an error; the plugin then returns no hints and identifier plugins are never called.