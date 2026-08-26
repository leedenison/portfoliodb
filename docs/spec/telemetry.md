# Telemetry

## 1. Events

Telemetry is event rows in a `telemetry` schema in the application database, read
by a Grafana container through a SELECT-only role. See
[adr/0053](../adr/0053-telemetry-is-run-scoped-event-rows.md) for why, and for what
replaced the Redis counters.

One rule governs every table below:

> One row is one completed unit of work, and carries exactly one outcome drawn from
> a closed, mutually exclusive vocabulary.

Where a row has two outcome columns it is because the unit of work has two sequential
stages, not because two grains share a row. Counts of one grain are never derived by
adding counts of another; grains relate by foreign key.

### Shape

```
run
├── resolution_key                     one distinct (source, description, hints)
│     └── identification_attempt       one ResolveWithPlugins call
│           └── identifier_plugin_call one plugin invocation
├── candidate_plugin_call              one plugin invocation over a batch
└── price_gap                          one listing a cycle set out to fill
      └── price_plugin_call            one outstanding range put to one plugin

candidate_field                        one proposed field, naming both
                                       resolution_key and candidate_plugin_call
```

`candidate_plugin_call` hangs off the run rather than off a resolution key: one
`ProposeBatch` call covers many descriptions at once, so it has no single parent key.
Identifier plugins are called once per plugin per attempt and do nest. This asymmetry
is forced by the code and must not be flattened away -- it is what made the counters
it replaces impossible to add up.

`candidate_field` is drawn outside the tree because it is the one row with two
parents, which the tree cannot show. It is what closes the asymmetry above: the call
knows the cost and the key knows the instrument, and only a row naming both can say
whether the field that was paid for turned out to be right.

### run

One activation of one subsystem. Created before its children and stamped when it ends.

| column | notes |
| --- | --- |
| `id` | referenced by every event row |
| `kind` | `tx_import`, `user_archive_import`, `system_archive_import`, `grouping_cycle`, `transfer_match_cycle`, `corporate_event_cycle`, `price_fetch_cycle`, `inflation_cycle`, `promotion_cycle` |
| `job_id` | `ingestion_jobs.id` when the run is a job; null for a cycle |
| `user_id`, `broker`, `source` | null for cycles |
| `started_at`, `ended_at` | |
| `outcome` | `success`, `failed`, `incomplete`; null while in flight |
| `telemetry_incomplete` | a telemetry write failed, so this run's counts understate |

`incomplete` means the run died. It is stamped by a sweep at service startup over runs
with no terminal outcome, which is what lets a null outcome mean genuinely running now.
The sweep leaves `ended_at` null, because the run ended when its process died and
nothing recorded when that was; a run stamped `incomplete` therefore has no duration.
`telemetry_incomplete` is unrelated to it: the work may have succeeded while its
telemetry was lost, and a panel should mark such a run rather than trust its counts.

The three import kinds are the three ingestion job types. The five cycle kinds are the
five triggered workers, and a cycle that finds no work still opens a run and stamps it
`success`: it ran, and a run table with holes in it is harder to read than one with
quiet rows.

### resolution_key

One distinct `(source, instrument_description, identifier hints)` triple within a run --
the thing `cacheKeyWithHints` names. **Not** one transaction: many transactions share a
key and resolve once. A cache hit is not an outcome, because it is not a resolution;
`tx_count` records the fan-out instead, so a failure affecting 300 rows can be told from
one affecting 1.

| column | notes |
| --- | --- |
| `run_id`, `source`, `description` | |
| `tx_count` | transactions sharing this key |
| `had_identifier_hints`, `security_type_hint` | lets a spike be attributed rather than merely noticed |
| `candidate_outcome` | stage 1, below |
| `outcome` | stage 2, below |
| `mismatch_detected` | which probe found two namings disagreeing, below |
| `instrument_id` | null when unresolved |

`candidate_outcome`: `fields_proposed`, `nothing_proposed`, `not_attempted_db_hit`,
`not_attempted_identity_complete`, `not_attempted_conflicting_hints`,
`not_attempted_run_kind`, `not_attempted_type_filter`, `not_attempted_no_plugins`.
The `not_attempted_*` members are the skips, and each names a different thing to act
on: `db_hit` is the stage costing nothing because the database already answered,
`identity_complete` is the gate working because the source named where the instrument
trades, `conflicting_hints` is a key already broken, `run_kind` is an archive import
which is never offered completion at all, `type_filter` is routing and `no_plugins` is
a missing installation. Lumped together they say only that a paid call did not happen,
and the interesting question is which of those numbers should move.

`outcome`: `db_source_description`, `db_identifier_hints`, `identified`,
`broker_description_only`, `extraction_failed`, `plugin_timeout`, `plugin_unavailable`,
`conflicting_hints`, `proposal_unconfirmed`. `proposal_unconfirmed` is a result found
and not trusted: no source stated an identifier, so a proposal was queried as the only
key there was, and nothing the source did state agreed with what it resolved to. It
ends at the same kind of instrument as `broker_description_only` and is not the same
finding -- "an answer was found and not trusted" is not "nobody answered". The two `db_*` members are distinct lookups -- by stored
`(source, description)` and by supplied identifier hints -- and conflating them hides
which path is carrying an import. The three fallback members mirror the messages the
resolver already records against a row.

`mismatch_detected` names the probe that found two ways of naming this key's instrument
disagreeing, and is null when none did. Its one member is `figi_vs_ticker`, the proposed
MIC_TICKER and OPENFIGI_SHARE_CLASS resolving to different instruments. It is not an
outcome, because resolution continues and succeeds -- for `figi_vs_ticker`, using
MIC_TICKER -- so a mismatch is not a terminal state and modelling it as one would make
the outcome column non-exhaustive. It is a name rather than the boolean it was, because
two different findings sharing one flag cannot be told apart afterwards, so neither can
be counted.

The price and corporate event parts of an archive resolve instruments through the same
resolver, but from an identifier and no broker description. They still write a key,
because an identification attempt reaches its run through one. The identifier names it
-- `description` is `TYPE:DOMAIN:VALUE` and `source` is empty, an archive being no
broker's export -- and `tx_count` carries the archive groups sharing it, the fan-out
this grain records whatever the things sharing it are called. `candidate_outcome` is
`not_attempted_run_kind`, an archive part never being offered completion, and an instrument ensured from the supplied identifier alone is
`broker_description_only`: no plugin resolved it and the row's own contents are what
the instrument was built from, which is that member's shape.

### conflicting_hint

One identifier a source stated and the instrument the database says it names, for a key
whose stated identifiers named more than one instrument between them.

| column | notes |
| --- | --- |
| `resolution_key_id` | the key whose names disagreed |
| `identifier_type`, `domain`, `value` | the whole triple the source stated |
| `instrument_id` | what the database says it names |

The key's own outcome already says `conflicting_hints`. This is what it was: the file
named an ISIN this instance holds on one security and a CUSIP it holds on another, and
nothing in the data says which is right, because whichever arrived first is the one
stored. Rows are written only for such a key, so the table is empty on an instance where
no file has ever disagreed with the security master.

One row per identifier rather than one per conflict, because how many instruments the
hints reached is not fixed: two is the ordinary case and three is possible. A panel groups
by `resolution_key_id` to see the whole disagreement and counts distinct keys to see how
often it happens.

The instrument is not a foreign key, for the reason `run.job_id` is not one: a merge
deleting one of these instruments must not take the record of the disagreement with it.

This is an identity failure and not a transaction failure. The upload is accepted and the
posting resolves to a broker-description-only instrument -- the same degradation an
identifier plugin timeout produces -- because blocking it would strand the user behind an
admin over a corporate action neither knew about. See
adr/0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md.

### candidate_field

One field a candidate plugin proposed for one resolution key, and what became of it.
The only row in the schema naming two parents, and it has to: the call covers many
keys, so a proposed value cannot be joined to the instrument that was eventually
resolved from the call alone, and the key does not know which call or which plugin
offered it. Whether completion helps is exactly that join, and no counter at any
granularity stands in for it.

| column | meaning |
| --- | --- |
| `resolution_key_id` | the key it was proposed for |
| `call_id` | the call it came from |
| `field` | `ticker`, `exchange`, `currency` or `key` |
| `value` | what was proposed |
| `confidence` | the plugin's own estimate, null when it reports none |
| `outcome` | below |

`outcome`: `confirmed` when the instrument that resolved carries the field and agrees,
`contradicted` when it carries it and differs, `untested` when it says nothing about
it, and `unused` when no instrument resolved or resolution never consulted the
proposal -- a key that short-circuited on the database reached no plugin that could
have used it. `untested` and `unused` are kept apart because their fixes differ: one is
a provider that does not return the field, the other is a proposal nothing needed.

The verdict is about the identifier as a whole, venue included, because that is what
was proposed: confirming a symbol while the venue disagrees is not a confirmation of
what was said.

`value` is stored because the question is not only how often a field was confirmed but
which values keep being wrong -- a ticker a model reliably confabulates is a prompt
problem, and it is invisible in a rate. **`confidence` is recorded and gated on by
nothing.** A model's self-report is uncalibrated, and turning it into a threshold
before this table has shown whether it correlates with correctness would be inventing a
number; this table is the evidence that would justify one.

### identification_attempt

One `ResolveWithPlugins` call. A single resolution key produces several: one `primary`,
two more when the mismatch check runs, and one per level of underlying recursion.

| column | notes |
| --- | --- |
| `resolution_key_id` | |
| `purpose` | `primary`, `mismatch_check`, `underlying` |
| `depth` | recursion depth; 0 for the first call |
| `outcome` | `db_short_circuit`, `no_eligible_plugins`, `identified`, `not_identified`, `plugin_timeout`, `plugin_error`, `proposal_unconfirmed`, `underlying_line_unknown` |
| `security_type_hint`, `asset_class`, `had_identifier_hints` | |

A plugin filtered out by acceptable kind or security type produces no
`identifier_plugin_call` row, because no call was made. When that filter removes every
plugin the attempt records `no_eligible_plugins`.

`asset_class` is the class the attempt landed on, against `security_type_hint`, which is
the class it was asked about. An attempt that identified nothing has no class to record.

A resolution that fails outright -- a database read that errored rather than a plugin
that found nothing -- writes no attempt row. `outcome` is not nullable, and there is no
member for a unit of work that did not complete; the run's own outcome is where that
failure shows.

`underlying_line_unknown` is a derivative resolved to a contract whose underlying names
no line the strike could be quoted in. The identity was found and is not in doubt, so it
is neither `not_identified` nor `proposal_unconfirmed`; what is missing is the currency
the deliverable is in. It ends at a broker-description-only instrument, as
`proposal_unconfirmed` does. See
adr/0074-an-options-underlying-is-the-line-its-strike-is-quoted-in.md.

### identifier_plugin_call

One plugin invocation within an attempt.

| column | notes |
| --- | --- |
| `identification_attempt_id`, `plugin_id` | |
| `outcome` | `won`, `superseded`, `discarded_inconsistent`, `not_identified`, `rate_limited`, `timeout`, `error`, `skipped_expired` |
| `retries`, `duration_ms` | |
| `mismatch_subject` | what the two results argued about: `Currency`, `Venue`, or `Identifier:<type>` |
| `mismatch_winner`, `mismatch_other` | what each said, an identifier as its whole triple |
| `mismatch_winner_plugin` | which result it lost to |

The first three are all successes and are decided by the orchestrator after every plugin
has returned: `superseded` lost to a better hint match despite higher precedence, and
`discarded_inconsistent` was dropped as contradicting the winner. A plugin cannot know
either, which is why the plugin returns its transport outcome and the orchestrator
composes the row.

The four mismatch columns are what `discarded_inconsistent` was dropped over, and are on
this row rather than in a table of their own because they share its grain exactly: the
check stops at the first thing the two results argued about, so one call has at most one.
They are null for every other outcome, as `duration_ms` is null on a price call that made
none, and a CHECK holds them to that -- a mismatch on a winning call would be a column
meaning a different thing per row.

`mismatch_subject` is free text rather than a vocabulary because an identifier subject
spells its own type into it, so the values are open by construction. Whose answer won
decides nothing about a merge -- every identifier plugin is equally authoritative for a
global identifier -- but a reader asking why two providers disagree needs to know which
two, which is what `mismatch_winner_plugin` is for.

Recording them closes the same inversion `identifier_claim` closed. The outcome said a
plugin contradicted the winner and nothing said what about, so the disagreement was
rediscovered and re-logged on every upload of the same file while nothing accumulated.
See adr/0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md and
adr/0080-a-contradiction-is-logged-not-queued.md.

### identifier_claim

One identifier an identifier plugin call returned, or was strictly filtered on.

| column | notes |
| --- | --- |
| `call_id` | the `identifier_plugin_call` it belongs to |
| `identifier_type`, `domain`, `value` | the whole triple, because a ticker under two domains names two listings |
| `listing_currency` | which line the resolution landed on, absent when it landed on the unknown one |
| `role` | `returned`, or `filtered` where the call constrained the provider to this value and a non-empty response therefore asserts it |

A filtered row is graded with a returned one. A provider answering "no identifier found"
when its filter matches nothing has asserted that the filtered value denotes the security
it described, so the association holds whether or not the value came back in the payload
-- which matters because the OpenFIGI plugin deliberately declines to echo a matched ISIN
or CUSIP. A filter the provider silently relaxes is a hint and asserts nothing, so only a
strict one is recorded this way.

The rows under one `call_id` are what one identifier plugin said in one answer, and it
is that grouping rather than the plugin's identity that decides whether an association between
two of them may be acted on: identifiers arriving together are a claim somebody made,
and the same identifiers gathered from separate calls are a set the resolver assembled.
See adr/0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md.

Recording it here closes an inversion. `candidate_field` carries the value, the field
and the confidence for every field a candidate plugin proposed -- full detail on claims
that are not evidence and can never merge -- while `identifier_plugin_call` carried a
plugin id and an outcome and no identifiers at all, for the results that do. So "which
plugin corroborated this association" was unanswerable for the one class of claim where
it matters.

### candidate_plugin_call

One plugin invocation over a batch.

| column | notes |
| --- | --- |
| `run_id`, `plugin_id` | |
| `precedence` | where this plugin sat in the chain, higher first |
| `batch_size` | items passed to this plugin, after the type filter |
| `items_completed` | items it filled at least one field for |
| `fields_proposed` | fields it filled across them |
| `outcome` | `hints_returned`, `no_hints`, `error`, `rate_limited`, `quota_exceeded`, `model_not_found` |
| `prompt_tokens`, `completion_tokens`, `total_tokens` | null for plugins with no token cost |
| `duration_ms` | |

Candidate plugins run in precedence order and each sees only the items its
predecessors failed on, so `batch_size` is a different population per plugin and rates
are not comparable between them. Identifier plugins run in parallel and every eligible
plugin is called, so those rates are comparable. Tokens are columns rather than running
totals, which is what makes the cost of one import answerable.

`precedence` is what makes that narrowing readable: without it the order the batches
shrank in cannot be recovered from the rows, and `batch_size` descending is only a guess
at it, arbitrary the moment two plugins are handed equal batches. It is the plugin's
configured precedence rather than an ordinal of the rows written, because a plugin whose
filtered batch is empty writes no row at all -- so a gap in the sequence means that
plugin was skipped, which is how a filtered-out identifier plugin already reads.

### price_gap

One listing a price fetch cycle set out to fill, which is one entry in the gap list
`PriceGaps` and `FXGaps` produce between them. Created before its children and stamped
when the listing is done with.

| column | notes |
| --- | --- |
| `run_id` | |
| `listing_id` | the line the gap is on, which is the unit the fetcher works on |
| `instrument_id` | the security above it, which is what a panel groups by; not a foreign key, for the reason `run.job_id` is not one |
| `is_fx` | the gap came from `FXGaps` rather than `PriceGaps` |
| `asset_class`, `currency`, `exchange` | the three fields plugin filtering reads. The asset class is the security's; the currency and the venue set are the line's own, the venues comma-joined because a plugin carrying any one of them accepts the line |
| `days_outstanding` | days the gap covered when the cycle picked it up, summed over its ranges |
| `outcome` | `filled`, `settled_empty`, `no_eligible_plugin`, `all_plugins_failed`, `listing_missing`; null while in flight |

Not one price row and not one provider call: a single instrument's outstanding history is
put to several plugins over several ranges. `days_outstanding` is the size of that ask and
the denominator every rate here wants -- without it a cycle that got slower cannot be told
from one that was asked for more, which is the first question about a cycle whose duration
moved. It is also what shrinks cycle over cycle while coverage recording is working, and
what stops shrinking when it is not.

Rows are written for the whole gap list up front, so a cycle that died part way leaves the
instruments it never reached unstamped and where it stopped stays readable. A null outcome
therefore means what a null run outcome means.

`settled_empty` is a success. Every outstanding range was closed by coverage without bars,
because the instrument did not trade then or no plugin reaches that far back, and the gap
will not be asked about again -- which is the whole purpose of recording empty coverage. A
`settled_empty` gap with no `price_plugin_call` rows beneath it is the shape worth hunting:
every plugin had already covered every range, so the gap recurs in the gap list every cycle
while no plugin will ever fill it.

`asset_class`, `currency` and `exchange` are recorded rather than read live through
`v_instrument_label`, because they are the inputs to the decision this row explains. An
instrument whose asset class is corrected later would otherwise rewrite the reason a plugin
was skipped a year ago.

### price_plugin_call

One outstanding range put to one plugin, which is one `FetchPrices` call in every case but
`history_limit`.

| column | notes |
| --- | --- |
| `price_gap_id`, `plugin_id` | |
| `precedence` | where this plugin sat in the order, higher first |
| `range_from`, `range_before` | half-open, as the orchestrator's ranges are |
| `bars` | rows the plugin returned; 0 for every outcome but `bars_returned` |
| `outcome` | `bars_returned`, `no_data`, `history_limit`, `permanent_block`, `timeout`, `error`, `upsert_failed` |
| `duration_ms` | null for `history_limit`, which called nothing |

The range rather than the plugin is the grain because one plugin is asked separately for
each range a gap leaves outstanding, and those calls can end differently -- a head the
plugin cannot reach, a middle it answers, a tail that times out. One row per (gap, plugin)
would force one outcome onto three answers.

Plugins are tried in order until one covers the gap, so a gap normally has rows from fewer
plugins than are configured. `precedence` makes that readable for the reason it does on
`candidate_plugin_call`: a plugin filtered out by asset class, exchange or currency,
blocked for this instrument, or holding no identifier it supports writes no row at all, so
a gap in the sequence means skipped.

There is deliberately no row for a range a plugin had already covered. Recording the
absence of work would make the table grow with the catalogue rather than with what was
done, and the same (instrument, plugin, range) recurring across cycles is the better signal
that coverage recording has stopped working -- it is positive evidence rather than an
absence.

The span in days is not a column. `v_price_plugin_call` derives it by subtracting the two
dates, so it cannot disagree with the range it came from. `days_outstanding` on the gap is
a different matter and is stored: it sums ranges the gap no longer holds by the time
anything is written.

`no_data` is an answer and not a failure to answer: the range is settled for that plugin
and never asked again. `upsert_failed` is our database rather than the provider's API, and
is a separate member for the reason transport failure is separate from `not_identified`.

### merge

One decision about whether two identifiers denote one security, and what was done about
it.

| column | notes |
| --- | --- |
| `run_id` | the run, directly: a merge is also taken by the corporate event cycle, which has no resolution key to hang off |
| `outcome` | `merged`, `refused_uncorroborated`, `refused_unmediated`, `refused_unsettled`, `refused_disjoint`, `refused_collision` |
| `a_type`, `a_domain`, `a_value` | one endpoint, as a whole triple |
| `b_type`, `b_domain`, `b_value` | the other |
| `a_instrument_id`, `b_instrument_id` | what the two endpoints resolved to when the decision was taken |
| `collision_type`, `collision_domain`, `collision_value` | the triple both instruments hold, for `refused_collision` alone |

The pair is the grain, not the resolution: a set of identifiers landing on three
instruments asks the question twice and can answer it differently each time, so one row
per resolution would force one outcome onto two answers. A resolution whose identifiers
all name the security already holding them asks nothing and writes nothing, so the table
grows with contested identity rather than with traffic.

The endpoints are whole triples rather than instrument ids because a triple outlives the
merge and an instrument does not -- the loser is deleted, so an id alone leaves a reader a
year later with nothing to look up. The ids are recorded beside them for a panel grouping
by security, on the terms `price_gap` records its instrument: not foreign keys, because
telemetry outlives the work it describes. A merged pair need not contain the survivor,
which is picked over the whole group rather than per pair.

The five refusals are separate members because they need different fixes.
`refused_uncorroborated` is the resolver having assembled a set nobody asserted, and wants
a plugin that returns both names; it is what a plugin set whose identifier vocabularies do
not overlap produces all day. `refused_unmediated` is a type that reassigns its values
routinely and is working as intended. `refused_unsettled` is a chain that would have run
through a row one user owns: the instruments are instance-global where the row is not, so
acting on it would settle the association for everybody on the strength of one
unauthenticated file. It wants another user holding the same mapping, which the promotion
sweep turns into a fact, or a plugin confirming it. `refused_disjoint` is two names that
were never correct at one time, which may be a vintage recorded wrongly.
`refused_collision` is the one that is neither noise nor design: both instruments hold one
triple over overlapping intervals, so two claims cannot both hold and nothing in the data
says which is right. See adr/0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md and
adr/0063-identity-claims-are-owned-until-users-corroborate-them.md.

Nothing recorded a merge before this table. The decision is taken inside the database
layer, which has no run to hang a row off and no logger, so a merge that happened, a merge
that was refused, and a name silently dropped on the way through were indistinguishable
from outside. The collision case was the worst of the three: the merge dropped the
colliding name and deleted the instrument that held it, leaving nothing to look at
afterwards.

There is deliberately no mediator column. adr/0061's chain runs through the two stored
rows rather than through a third value, so every decision recorded here names two
endpoints and a column for a third would be null on every row. Transitivity is visible as
several rows under one run.

### unhandled_corporate_event

One corporate event a run could not apply: a reverse or non-whole split, an
extraordinary dividend on an option, a dividend in a currency no line of the security
is quoted in, a futures adjustment.

| column | notes |
| --- | --- |
| `run_id` | the run, directly: the corporate event cycle has no resolution key to hang off |
| `instrument_id` | not a foreign key, so a merge deleting the instrument leaves the record |
| `event_type` | `REVERSE_SPLIT`, `NON_WHOLE_SPLIT`, `SPECIAL_CASH_DIVIDEND`, `UNATTRIBUTABLE_DIVIDEND`, ... |
| `ex_date` | optional; absent for the rare event with no date |
| `detail` | the sentence a person reads |
| `data` | the event's own terms, free-form per `event_type` |

The unit of work is one event one run could not handle, so an event that fails every
cycle writes a row each time. That is the signal rather than noise, exactly as a price
gap coming back every cycle is: how long it has been failing is what the repeated rows
say. Within a run it is deduped -- a split is examined for the underlying and again per
option, and recording it twice would say one finding twice.

This was an operational table with a `resolved` flag an admin set. Both moved here in
0141: an operator's decision is state where this schema records events, and a row here
is read and acted on elsewhere rather than worked. The admin surface reads it, which is
the one read of telemetry a serving path makes -- a read of what happened rather than a
dependency, since nothing the system does turns on the answer. See
adr/0080-a-contradiction-is-logged-not-queued.md.

Two kinds of dividend are recorded here and nowhere else, because the fetch refuses
them a home in `cash_dividends` and this schema is neither archived nor retained
forever. That is a known gap:
[0173](../issues/0173-an-unfiled-dividend-has-nowhere-durable-to-live.md).

### Views

One view per table, each flattening its parents in and never fanning out into its
children -- a view spanning two sibling grains duplicates the parent's rows and makes
counting it silently wrong.

Judgements are computed columns on the view; selection belongs to the panel. A judgement
a panel needs and no view carries is a gap in the views, not a licence to inline an
outcome list into a dashboard. They are:

| column | on | meaning |
| --- | --- | --- |
| `is_import` | every view | the run kind is one of the three import kinds |
| `reached_plugins` | attempt, identifier call | the attempt outcome is neither `db_short_circuit` nor `no_eligible_plugins` |
| `resolved` (`key_resolved` on children) | key, attempt, identifier call | the key ended holding a real identifier |
| `had_attempt` | key | the key produced at least one identification attempt |
| `transport_failed` | identifier call | the call did not complete |
| `call_failed` | description call | the call produced no answer |
| `settled` (`gap_settled` on the call) | price gap, price call | the gap is dealt with and will not recur |
| `had_call` | price gap | the gap reached at least one `FetchPrices` call |
| `transport_failed` | price call | the call did not complete |
| `write_failed` | price call | our own upsert failed, not the provider |
| `is_refusal` | merge | the two identifiers were not merged |
| `is_contradiction` | merge | the refusal was a collision, which is the one a person has to look at |

`reached_plugins` is the denominator for identification failure rate. Using all attempts
instead makes the rate fall as the instrument table fills, because more resolutions
short-circuit in the database, which reads as improving identification when nothing has
changed. A failure-rate panel filters `purpose = 'primary'` as well, or an import
carrying more dual-hint descriptions inflates the denominator with mismatch-check
attempts.

`resolved` covers `db_source_description`, `db_identifier_hints` and `identified`, and
deliberately excludes `broker_description_only`: nothing identified that instrument and
the row's own contents are all it was built from, which is a failure for a transaction
import however ordinary it looks. It is the expected outcome for an archive run, so a
panel charting the complement splits by run kind rather than blending the two. A null
outcome counts as not resolved rather than as null, or an unstamped key would drop out of
both the column and its negation. `instrument_id IS NOT NULL` is not the same test and is
not a substitute: an archive key ensured from a supplied identifier has an instrument and
identified nothing.

`had_attempt` separates a key that never asked from one that asked and was told nothing.
Four of the five paths that stamp a key return before `ResolveWithPlugins` is called, so
no attempt is ordinary rather than a fault.

`transport_failed` and `call_failed` both draw the line at the call completing.
`not_identified`, `no_hints` and a price call's `no_data` stay outside them: an empty
answer is an answer, and counting it as a fault makes a plugin that correctly knew nothing
read as a plugin that is down. A price call's `history_limit` is outside it because nothing
was called, and `permanent_block` because it is a durable fact about the (instrument,
plugin) pair rather than an incident -- it creates a fetch block, so it happens once and
never again. `write_failed` is separate so a panel watching provider health is not moved by
our own write failing.

`settled` and `had_call` are the price-gap parallels of `resolved` and `had_attempt`, and
the reasoning is the same. `settled` includes `settled_empty`, because a range no plugin can
fill is dealt with once that is recorded and counting it as a failure would make an untraded
week look like an outage; the complement is the column a panel wants, being the gaps that
come back every cycle. `had_call` separates a gap that never asked from one that asked and
was told nothing -- filters, fetch blocks, missing identifiers and existing coverage each
return before `FetchPrices`, so no call is ordinary rather than a fault.

`v_run` also carries `key_count`, `key_tx_count`, `description_call_count`, `gap_count`,
`merge_count` and `unhandled_event_count`. These are
scalar subqueries, one per child table, and they are the only sanctioned way for a view
to reach into a second child. The rule above forbids a view *fanning out* into two
sibling grains, because that repeats the parent once per child and makes counting it
wrong; an aggregate repeats nothing and `v_run` stays one row per run. A panel wanting
per-run child counts uses these and never a join.

`key_tx_count` is transactions that needed resolution, not rows in the imported file: a
row naming no instrument never becomes a key. It is the closest thing to an import's size
the schema holds, and it is the denominator every rate over resolution keys wants. A
transaction count on `run` itself is deliberately not recorded -- it would mean postings
for one import kind, heterogeneous rows processed for another, nothing for the third and
null for every cycle, which is a column meaning a different thing per row and the grain
confusion this schema exists to end. The imported file's own row count is not available
at all: `run.job_id` is not a foreign key and `ingestion_jobs` is outside the reading
role, both by design.

`gap_count` is how big a price fetch cycle was, counted in instruments rather than days
because the instrument is what a hole in a valuation is traced back to;
`v_price_gap.days_outstanding` carries the other measure at the grain that owns it. It is
zero for every other run kind, as `key_count` is for a cycle.

`merge_count` is zero for almost every run, and that is what makes it worth carrying: a
merge is only asked about where one resolution's identifiers reached more than one
instrument, so a non-zero count is itself the signal rather than a volume to normalise.

`unhandled_event_count` is zero for every run kind but the corporate event cycle and an
archive import, as `gap_count` is for the price fetch cycle.

### Naming an instrument

A resolution key records `listing_id` and nothing readable, which leaves no panel able
to say which listing a description landed on -- the question asked after every manual
import, since a description resolving to the wrong currency line looks identical to it
resolving to the right one in a bare UUID.

Recording a label when the key is stamped was rejected: the readable instrument is two
frames below the write site and deliberately discarded, and three of the five paths that
stamp a key never hold one, so the column would be null exactly where identification was
most interesting.

So `telemetry.v_instrument_label` is a lookup instead. A view runs with its owner's
privileges rather than its caller's, so granting SELECT on it lets the reading role turn
an id into a name while still holding no privilege on `instruments` and no USAGE on
`public`. That indirection is the point: one narrow window, reviewed in the migration,
rather than a grant on the application schema. It is the only place the telemetry schema
reads outside itself.

It is a live lookup and not a recorded fact -- an instrument renamed today changes what a
panel says about a run from last year -- which is why it is a separate view and is joined
by a panel rather than folded into the views above.

### Writing

Writes are synchronous, through a connection pool separate from the application's, and
confined to `server/db` as all SQL is. They never join the work's transaction: a failed
import rolls back, and telemetry riding along would erase the diagnostics for the run
most worth inspecting. A write that fails sets `telemetry_incomplete` on the run and is
otherwise ignored -- telemetry never fails the work. No write returns an error, so a
caller threads ids through without testing them; a write whose parent id is missing is
skipped, which costs one failure its own subtree and nothing else.

`run` and `resolution_key` are each written twice -- created with what is known when the
work starts, stamped with its outcome when the work ends. For a run that is what leaves
one whose process died unstamped. For a resolution key it is forced by the foreign key:
its identification attempts reference it, so it has to exist before its own outcome is
known. The other three grains are written once, when their unit of work completes, which
for an identifier plugin call is the moment the orchestrator decides `won` against
`superseded` against `discarded_inconsistent`.

Plugins do not write. `Identify` and `ProposeBatch` return a telemetry value alongside
their result and the orchestrator composes the row, so no plugin depends on the
telemetry backend or needs a database to unit test.

### Retention

Plain tables, not hypertables: the volume does not warrant Timescale, and foreign keys
into a hypertable are an avoidable complication. Children cascade from `run`, so
retention is a delete over `run.started_at`, at **360 days**. Traffic is low and a
problem may go unnoticed for a long time, so the window is set to outlast the gap
between a regression landing and someone looking for it.

The delete is exposed as an admin RPC and nothing in the service schedules it. The
service runs no cron of its own, and a retention window measured in a year is not worth
one: an operator's scheduler calls the RPC, and the count it answers with is what that
scheduler logs. This is why the RPC is synchronous and returns what it deleted, unlike
the `Trigger` RPCs, which poke a worker and answer immediately.

The schema, its tables, its views and the SELECT-only grants live in
`server/migrations/005_telemetry.sql`, separate from the application schema because it is
a separate scope rather than a later change to the same one.

The reading role's password is not in the migration, which rules out the migration
creating the login the dashboard uses. So the migration creates `telemetry_reader`, a
NOLOGIN group role holding SELECT and nothing else, and the login role is created by
`docker/postgres/init/10-telemetry-reader.sh` from compose environment and granted
membership of it. The privileges are then reviewed in the repository and the password
never is.

That script runs from the Postgres entrypoint, on an empty data directory, before the
service has connected and so before the migration has run. It therefore creates
`telemetry_reader` itself; the migration guards its own `CREATE ROLE` on `pg_roles` and
adds only the grants. The apparent duplication is what the ordering costs. The test and
e2e stacks mount no init script and take the role from the migration alone, which is why
the privilege test needs no login to run against.

## 2. Dashboards

Grafana runs as a container in the dev stack, bound to loopback, with its datasource and
its dashboards provisioned from files under `docker/grafana`. Dashboards are read only in
the browser: an edit saved there would live in the container volume and drift from the
repository unnoticed.

There are two, over the same tables. **Telemetry: one run** is scoped by a run picker,
for reading a single import during manual testing; **Telemetry: drift** buckets over
time, for noticing that something changed. The drift dashboard's run table links each row
through to the run dashboard, which is what makes the pair a workflow rather than two
pictures.

The **Broker** picker is import scoped and applies to the resolution and description
panels alone. The run panels ignore it because a price fetch cycle dying matters as much
as an import doing so, and the price panels because a cycle runs on nobody's behalf and
names no broker to filter on.

Panels select from the views and carry no definitions of their own -- `where is_import
and reached_plugins`, never a repeated list of excluded outcomes. A judgement a panel
needs and the views do not carry is a gap in the views.

Two consequences of the grains bind every panel. A view spanning two sibling child grains
duplicates the parent, so a panel wanting per-run child counts reads `v_run`'s rollups and
never a join. And a child view has no timestamp of its own, so a panel bucketing one over
time uses `run_started_at`, the run's start; `started_at` exists only on `v_run`.

The price panels answer the question a cycle's duration cannot. Both dashboards carry a
**Price fetching** section: on the drift dashboard the gap outcome mix, the unsettled rate
split on `is_fx`, time spent per plugin and transport failures; on the run dashboard the
gap outcomes, the days of history the cycle was handed, and a table of the gaps that did
not settle joined to `v_instrument_label` so a hole in a valuation names an instrument
rather than a UUID. Time per plugin is summed rather than averaged, because attributing a
cycle's duration is what it is for.

The dashboards are SQL in files no compiler reads, so a renamed column would break them
silently until somebody opened a browser. Every panel query and every template variable
query is therefore planned against the real schema by a test in the database layer, which
also refuses a panel that reads a table instead of a view.

A template variable holds its SQL as a bare string, not the `{rawSql, format}` object a
panel target holds. Grafana hands the string to the datasource and builds `rawSql` from it
itself, so an object there arrives at postgres as an object, the query fails and the picker
offers nothing while every panel around it still draws. The same test refuses the object
form, because the symptom looks like an empty dashboard rather than a broken one.

## 3. Logger

### Overview

- **Output**: Standard out (stdout).
- **Level**: Controlled by an environment variable (e.g. `LOG_LEVEL`). The default is currently **debug** (see adr/0009-telemetry-and-logging.md).
- **Behaviour**: When OpenFIGI is invoked (mapping or search), log at debug (or info) that we're calling the API (and optionally the input, e.g. ticker or query). On success, log success; on error, log the error (message and optionally status code / response body summary).

### LOG_LEVEL

- **Env var**: `LOG_LEVEL` (or `PORTFOLIODB_LOG_LEVEL` if we want to namespace).
- **Values**: At least `debug`, `info`, `warn`, `error`. Default: `debug`.
- **Implementation**: Use a standard Go logger that supports levels (e.g. `log/slog` from the standard library). Only emit a log line if its level is >= the configured level.

### OpenFIGI log points

- **On invoke**: When calling OpenFIGI mapping or search, log once per call with:
  - Operation: mapping or search.
  - Input: e.g. job ID/value for mapping, query (and optional exchCode) for search.
  - Level: debug (or info).
- **On success**: Log that the call succeeded and optionally result count (e.g. "mapping returned 1 result"). Level: debug.
- **On error**: Log that the call failed, with:
  - Error message (and for HTTP errors: status code, and optionally a short summary of the response body).
  - Level: error (or warn for rate-limit if we want to distinguish).

### Placement

- **Logger**: Initialized at server startup from `LOG_LEVEL`, stored in a package or passed where needed (e.g. to the OpenFIGI client or plugin).
- **Call sites**: In `server/plugins/openfigi/identifier/openfigi.go` (and optionally in `plugin.go` for "Identify started" / "Identify succeeded" / "Identify failed" if we want a single place for "OpenFIGI invoked"). Prefer one place (e.g. `Mapping` and `Search` in `openfigi.go`) so all HTTP-level outcomes are logged there.

### Dependencies

- Use the standard library `log/slog` (Go 1.21+); no third-party logging library (see adr/0009-telemetry-and-logging.md).
