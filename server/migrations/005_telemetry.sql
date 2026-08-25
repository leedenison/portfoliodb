-- Telemetry: run-scoped event rows read by Grafana through a SELECT-only role.
-- See docs/spec/telemetry.md for the column lists and outcome vocabularies, and
-- docs/adr/0053-telemetry-is-run-scoped-event-rows.md for why the Redis counters
-- were replaced by rows.
--
-- One rule governs every table here:
--
--   One row is one completed unit of work, and carries exactly one outcome drawn
--   from a closed, mutually exclusive vocabulary.
--
-- The vocabulary being closed is what makes a chart interpretable without knowing
-- how the code branches, and is why every outcome column is a CHECK over a spelled
-- out list rather than free text. Where a row has two outcome columns it is because
-- the unit of work has two sequential stages, not because two grains share a row.
-- Grains relate by foreign key and are never added together.
--
-- A separate schema rather than more tables in public: this is a separate scope,
-- read by a different role, retained on a different clock, and dropped as a unit if
-- it ever stops earning its keep.
--
-- Plain tables, not hypertables. The volume does not warrant Timescale and foreign
-- keys into a hypertable are an avoidable complication.
CREATE SCHEMA telemetry;

-- One activation of one subsystem, of which an ingestion job and a worker cycle are
-- two kinds. Every event row hangs off a run, which is what makes "what happened
-- during that import" answerable at all -- the question the counters could not
-- answer, because they were global running totals.
--
-- The row is written twice: created before its children, since they reference it,
-- and stamped with ended_at and an outcome when the work finishes. A run whose
-- process died never receives the second write, which is what 'incomplete' is for.
-- It is an explicit member of the vocabulary rather than a null so that a killed
-- container reads as a fact, and so that a null outcome can mean genuinely running
-- now. Under the dev stack's pinned memswap_limit that is a diagnosis rather than an
-- edge case.
--
-- telemetry_incomplete is unrelated to the 'incomplete' outcome: it says a telemetry
-- write failed, so this run's counts understate. The work may have succeeded while
-- its telemetry was lost, and a panel should mark such a run rather than trust it.
--
-- job_id and user_id are deliberately not foreign keys, for the reason tx_groups.job_id
-- is not one: a run must outlive the work it describes. Telemetry is retained for 360
-- days against whatever prunes jobs and users, and a cross-schema ON DELETE CASCADE
-- would let deleting a job silently destroy the diagnostics explaining it. A job's
-- status is likewise denormalised onto outcome rather than joined from ingestion_jobs,
-- because a run's outcome is a historical fact once stamped and because it keeps every
-- panel reading one table regardless of run kind.
--
-- An RPC request is not a kind of run. No handler does substantial synchronous work,
-- so nothing would populate one, while the SPA's polling would swamp this table.
CREATE TABLE telemetry.run (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  kind       TEXT NOT NULL CHECK (kind IN ('tx_import', 'user_archive_import',
                                           'system_archive_import',
                                           'grouping_cycle', 'transfer_match_cycle',
                                           'corporate_event_cycle', 'price_fetch_cycle',
                                           'inflation_cycle')),
  -- ingestion_jobs.id when the run is a job; null for a cycle.
  job_id     UUID,
  -- Null for cycles, which run on nobody's behalf.
  user_id    UUID,
  broker     TEXT,
  source     TEXT,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  ended_at   TIMESTAMPTZ,
  -- Null while in flight.
  outcome    TEXT CHECK (outcome IN ('success', 'failed', 'incomplete')),
  telemetry_incomplete BOOLEAN NOT NULL DEFAULT FALSE
);

-- started_at carries both the retention delete and the time axis of every drifting
-- panel, so it is indexed alone; the composite serves a panel bucketing one kind.
CREATE INDEX idx_telemetry_run_started_at ON telemetry.run (started_at);
CREATE INDEX idx_telemetry_run_kind_started_at ON telemetry.run (kind, started_at);

-- One distinct (source, instrument_description, identifier hints) triple within a
-- run -- the thing the ingestion resolver's cacheKeyWithHints names. Not one
-- transaction: many transactions share a key and resolve once, and tx_count records
-- that fan-out so a failure affecting 300 rows can be told from one affecting 1.
--
-- A cache hit is not an outcome here, because it is not a resolution. Counting one
-- would make the population transactions rather than descriptions, which is the
-- confusion of grains this schema exists to end.
--
-- Like run, the row is created and later stamped: its children reference it, so it
-- must exist before its own outcome is known. The columns above extraction_outcome
-- are the inputs, written on creation; the four below are the results, written when
-- the key resolves. That is why they are nullable, and a null in them means the same
-- thing a null run outcome does.
--
-- had_identifier_hints, security_type_hint and instrument_kind are here so a spike
-- can be attributed rather than merely noticed.
CREATE TABLE telemetry.resolution_key (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id      UUID NOT NULL REFERENCES telemetry.run (id) ON DELETE CASCADE,
  source      TEXT NOT NULL,
  description TEXT NOT NULL,
  -- Transactions sharing this key.
  tx_count    INT NOT NULL,
  had_identifier_hints BOOLEAN NOT NULL,
  security_type_hint   TEXT,
  instrument_kind      TEXT,
  -- Stage 1: what the candidate stage did for this key.
  --
  -- The not_attempted_* members are the skips, and each names a different thing to
  -- act on. db_hit is the stage costing nothing because the database already
  -- answered. identity_complete is the gate working: the source named where the
  -- instrument trades, so there was nothing to complete. conflicting_hints is a key
  -- already broken, whose stage 2 says so too -- recorded here as well so it can be
  -- taken out of the denominator of "how often did we attempt". run_kind is an
  -- archive import, which is never offered completion at all. type_filter is
  -- routing, and no_plugins is a missing installation.
  --
  -- Telling them apart is the point. Lumped together they say only that a paid call
  -- did not happen, and the interesting question is which of those numbers should
  -- move.
  candidate_outcome TEXT CHECK (candidate_outcome IN ('fields_proposed', 'nothing_proposed',
                                                      'not_attempted_db_hit',
                                                      'not_attempted_identity_complete',
                                                      'not_attempted_conflicting_hints',
                                                      'not_attempted_run_kind',
                                                      'not_attempted_type_filter',
                                                      'not_attempted_no_plugins')),
  -- Stage 2. The two db_* members are distinct lookups -- by stored (source,
  -- description) and by supplied identifier hints -- and conflating them hides which
  -- path is carrying an import. The three fallback members mirror the messages the
  -- resolver already records against a row.
  -- proposal_unconfirmed is a result found and not trusted: no source stated an
  -- identifier, so a proposal was queried as the only key there was, and nothing
  -- the source did state agreed with what it resolved to. Distinct from
  -- broker_description_only, which is nobody answering at all -- both end at the
  -- same kind of instrument and they are not the same finding.
  outcome     TEXT CHECK (outcome IN ('db_source_description', 'db_identifier_hints',
                                      'identified', 'broker_description_only',
                                      'extraction_failed', 'plugin_timeout',
                                      'plugin_unavailable', 'conflicting_hints',
                                      'proposal_unconfirmed')),
  -- Which probe found two ways of naming this key's instrument disagreeing, null
  -- when none did. Not an outcome: resolution continues and succeeds -- for
  -- figi_vs_ticker, using MIC_TICKER -- so a mismatch is not a terminal state,
  -- and modelling it as one would make outcome non-exhaustive.
  --
  -- A name rather than the boolean this was. Two different findings sharing one
  -- flag cannot be told apart afterwards, so neither can be counted.
  mismatch_detected TEXT CHECK (mismatch_detected IN ('figi_vs_ticker')),
  -- What the resolved instrument contradicted about the hints it was given,
  -- as the same summary the resolver logs ("Currency: USD->THB, Exchange:
  -- XNAS->XBKK"), or NULL when it contradicted nothing. Free text rather than a
  -- flag because which field disagreed is the whole content of the signal: a
  -- currency that differs is a different listing of one security, and an
  -- exchange that differs may be a different company. Hints do not currently
  -- gate resolution, so this is the only record that the answer stored was one
  -- the caller had already contradicted.
  hint_diffs        TEXT,
  -- Null when unresolved.
  instrument_id     UUID
);

CREATE INDEX idx_telemetry_resolution_key_run ON telemetry.resolution_key (run_id);

-- One ResolveWithPlugins call. A single resolution key produces several: one primary,
-- two more when the mismatch check runs, and one per level of underlying recursion.
-- That is a fact recorded in purpose and depth rather than a discrepancy between two
-- totals, which is what asking whether counters summed used to reduce it to.
--
-- A plugin filtered out by acceptable kind or security type produces no
-- identifier_plugin_call row, because no call was made. When that filter removes every
-- plugin the attempt records no_eligible_plugins.
CREATE TABLE telemetry.identification_attempt (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  resolution_key_id UUID NOT NULL REFERENCES telemetry.resolution_key (id) ON DELETE CASCADE,
  purpose           TEXT NOT NULL CHECK (purpose IN ('primary', 'mismatch_check', 'underlying')),
  -- Recursion depth; 0 for the first call.
  depth             INT NOT NULL,
  -- proposal_unconfirmed is a plugin having identified something the resolver
  -- declined to keep: the whole identity was a proposal and nothing the source
  -- stated agreed with it. The call rows beneath the attempt still say the
  -- plugins answered, which is why this is not not_identified.
  --
  -- underlying_line_unknown is a derivative resolved to a contract whose
  -- underlying names no line the strike could be quoted in. The identity was
  -- found and is not in doubt; what is missing is the currency the deliverable
  -- is in, so it is neither not_identified nor proposal_unconfirmed. See
  -- docs/adr/0074-an-options-underlying-is-the-line-its-strike-is-quoted-in.md.
  outcome           TEXT NOT NULL CHECK (outcome IN ('db_short_circuit', 'no_eligible_plugins',
                                                     'identified', 'not_identified',
                                                     'plugin_timeout', 'plugin_error',
                                                     'proposal_unconfirmed',
                                                     'underlying_line_unknown')),
  security_type_hint   TEXT,
  asset_class          TEXT,
  had_identifier_hints BOOLEAN NOT NULL
);

CREATE INDEX idx_telemetry_identification_attempt_key
  ON telemetry.identification_attempt (resolution_key_id);

-- One plugin invocation within an attempt.
--
-- won, superseded, discarded_inconsistent and discarded_uncorroborated are all
-- successes, and are decided by the orchestrator after every plugin has returned:
-- superseded lost to a better hint match despite higher precedence,
-- discarded_inconsistent was dropped as contradicting the winner, and
-- discarded_uncorroborated was dropped because nothing named the security the two
-- results share. A plugin cannot know any of them, which is why it returns its
-- transport outcome and the orchestrator composes the row. retries and duration_ms are the
-- orchestrator's too: the retry loop and the clock belong to it.
CREATE TABLE telemetry.identifier_plugin_call (
  id                        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  identification_attempt_id UUID NOT NULL
                            REFERENCES telemetry.identification_attempt (id) ON DELETE CASCADE,
  plugin_id                 TEXT NOT NULL,
  outcome                   TEXT NOT NULL CHECK (outcome IN ('won', 'superseded',
                                                             'discarded_inconsistent',
                                                             'discarded_uncorroborated',
                                                             'not_identified', 'rate_limited',
                                                             'timeout', 'error',
                                                             'skipped_expired')),
  retries                   INT NOT NULL,
  duration_ms               INT NOT NULL
);

CREATE INDEX idx_telemetry_identifier_plugin_call_attempt
  ON telemetry.identifier_plugin_call (identification_attempt_id);

-- One identifier an identifier plugin call returned, or was strictly filtered on.
--
-- The whole triple is recorded because a ticker under two domains names two
-- listings, so a claim that dropped the domain would assert something the call
-- did not.
--
-- A filtered row is graded with a returned one. A provider answering "no
-- identifier found" when its filter matches nothing has asserted that the
-- filtered value denotes the security it described, so the association holds
-- whether or not the value came back in the payload -- which matters because the
-- OpenFIGI plugin deliberately declines to echo a matched ISIN or CUSIP. A filter
-- the provider silently relaxes is a hint and asserts nothing, so only a strict
-- one is recorded this way.
--
-- The rows under one call_id are what one plugin said in one answer, and it is
-- that grouping rather than the plugin's identity that decides whether an
-- association between two of them may be acted on: identifiers arriving together
-- are a claim somebody made, and the same identifiers gathered from separate
-- calls are a set the resolver assembled.
--
-- Recording it here closes an inversion. candidate_field carries the value, the
-- field and the confidence for every field a candidate plugin proposed -- full
-- detail on claims that are not evidence and can never merge -- while
-- identifier_plugin_call carried a plugin id and an outcome and no identifiers at
-- all, for the results that do.
CREATE TABLE telemetry.identifier_claim (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  call_id         UUID NOT NULL
                  REFERENCES telemetry.identifier_plugin_call (id) ON DELETE CASCADE,
  identifier_type TEXT NOT NULL,
  domain          TEXT,
  value           TEXT NOT NULL,
  role            TEXT NOT NULL CHECK (role IN ('returned', 'filtered'))
);

CREATE INDEX idx_telemetry_identifier_claim_call
  ON telemetry.identifier_claim (call_id);

-- One plugin invocation over a batch. This hangs off the run rather than off a
-- resolution key: one ProposeBatch call covers many descriptions at once, so it has no
-- single parent key. Identifier plugins are called once per plugin per attempt and do
-- nest. The asymmetry is forced by the code and must not be flattened away -- it is
-- what made the counters this replaces impossible to add up.
--
-- Candidate plugins run in precedence order and each sees only the items its
-- predecessors failed on, so batch_size is a different population per plugin and rates
-- are not comparable between them. Identifier plugins run in parallel and every
-- eligible plugin is called, so those rates are comparable.
--
-- Tokens are columns rather than running totals, which is what makes the cost of one
-- import answerable. They are null, not zero, for a plugin that costs no tokens.
CREATE TABLE telemetry.candidate_plugin_call (
  id        UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id    UUID NOT NULL REFERENCES telemetry.run (id) ON DELETE CASCADE,
  plugin_id TEXT NOT NULL,
  -- Where this plugin sat in the chain, higher first. This is what makes the
  -- population differences above readable: without it the order the batches
  -- narrowed in cannot be recovered from the rows, and batch_size is only a
  -- guess at it.
  --
  -- The plugin's configured precedence rather than a loop index, because a
  -- plugin whose filtered batch is empty writes no row at all. A gap in the
  -- sequence therefore means that plugin was skipped, which is how a
  -- filtered-out identifier plugin already reads.
  precedence INT NOT NULL,
  -- Items passed to this plugin, after the type filter.
  batch_size       INT NOT NULL,
  -- Items it completed at least one field for.
  items_completed INT NOT NULL,
  -- Fields it proposed across them, which is not items_completed: one item can be
  -- given a ticker, a venue and a currency at once, and the cost of a call divided
  -- by items answers a different question from the cost divided by fields.
  fields_proposed INT NOT NULL,
  outcome   TEXT NOT NULL CHECK (outcome IN ('hints_returned', 'no_hints', 'error',
                                             'rate_limited', 'quota_exceeded',
                                             'model_not_found')),
  prompt_tokens     BIGINT,
  completion_tokens BIGINT,
  total_tokens      BIGINT,
  duration_ms       INT NOT NULL
);

CREATE INDEX idx_telemetry_candidate_plugin_call_run
  ON telemetry.candidate_plugin_call (run_id);

-- One field a candidate plugin proposed for one resolution key, and what became of
-- it.
--
-- This is the only row in the schema that names two parents, and it has to. The call
-- covers many keys at once, so a proposed value cannot be joined to the instrument
-- that was eventually resolved from the call alone; the key knows the instrument but
-- not which call or which plugin offered the value. Whether completion helps is
-- exactly that join, and no counter at any granularity can stand in for it.
--
-- value is stored because the question is not only how often a field was confirmed
-- but which values keep being wrong. A ticker the model reliably confabulates is a
-- prompt problem, and it is invisible in a rate.
--
-- confidence is what the plugin said about its own answer, and is recorded so it can
-- be bucketed against outcome. Nothing gates on it: an LLM's self-report is
-- uncalibrated, and turning it into a threshold before this table has shown whether
-- it correlates with correctness would be inventing a number. This table is the
-- evidence that would justify one. Null for a plugin that reports none.
--
-- The outcome vocabulary is about what the resolution was able to say, not about
-- whether the guess was any good:
--
--   confirmed     the instrument that resolved carries this field and agrees
--   contradicted  it carries this field and differs
--   untested      it says nothing about this field, so nothing was learned
--   unused        no instrument resolved, or resolution never consulted the
--                 proposal -- a key that short-circuited on the database reached
--                 no plugin that could have used it
--
-- untested and unused are kept apart deliberately. Both mean the guess was not
-- checked, and they have different fixes: untested is a provider that does not
-- return the field, unused is a proposal that was paid for and never needed.
CREATE TABLE telemetry.candidate_field (
  id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  resolution_key_id UUID NOT NULL REFERENCES telemetry.resolution_key (id) ON DELETE CASCADE,
  call_id           UUID NOT NULL REFERENCES telemetry.candidate_plugin_call (id) ON DELETE CASCADE,
  field             TEXT NOT NULL CHECK (field IN ('ticker', 'exchange', 'currency', 'key')),
  value             TEXT NOT NULL,
  confidence        DOUBLE PRECISION CHECK (confidence IS NULL OR (confidence >= 0 AND confidence <= 1)),
  outcome           TEXT NOT NULL CHECK (outcome IN ('confirmed', 'contradicted',
                                                     'untested', 'unused'))
);

CREATE INDEX idx_telemetry_candidate_field_key
  ON telemetry.candidate_field (resolution_key_id);
CREATE INDEX idx_telemetry_candidate_field_call
  ON telemetry.candidate_field (call_id);
-- Serves the question the table exists for: accuracy by field, and confidence
-- bucketed against outcome within one.
CREATE INDEX idx_telemetry_candidate_field_field_outcome
  ON telemetry.candidate_field (field, outcome);

-- One instrument a price fetch cycle set out to fill, which is one entry in the gap
-- list PriceGaps and FXGaps produce between them.
--
-- Not one price row and not one provider call: a single instrument's outstanding
-- history is put to several plugins over several ranges. days_outstanding is the size
-- of that ask, and is the denominator every rate here wants -- without it a cycle that
-- got slower cannot be told from one that was asked for more, which is the first
-- question about a cycle whose duration moved.
--
-- Like run and resolution_key the row is created before its children and stamped when
-- the instrument is done with, so a null outcome means the same thing it means there:
-- not stamped. Rows are written for the whole gap list up front, so a cycle that died
-- part way leaves the instruments it never reached unstamped, and where it stopped is
-- readable rather than lost.
--
-- asset_class, currency and exchange are the three fields the orchestrator filters
-- plugins on. They are recorded rather than looked up through v_instrument_label
-- because they are the inputs to a decision this row explains: an instrument whose
-- asset class is corrected later would otherwise rewrite the reason a plugin was
-- skipped a year ago.
CREATE TABLE telemetry.price_gap (
  id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  run_id        UUID NOT NULL REFERENCES telemetry.run (id) ON DELETE CASCADE,
  -- The currency line the gap is on, which is the unit the fetcher works on, and
  -- the security above it, which is what a panel groups by. Neither is a foreign
  -- key, for the reason run.job_id is not one: telemetry outlives the work it
  -- describes, and a deleted instrument must not take the diagnostics explaining
  -- it. v_instrument_label is where a panel turns the security into a name.
  --
  -- The security is nullable and the line is not, because a gap always names a
  -- line and the security is reached by looking that line up. A cycle that
  -- cannot find the line records the gap with no security, which is the
  -- listing_missing outcome below and the one row where the two differ.
  listing_id    UUID NOT NULL,
  instrument_id UUID,
  -- Price gaps and FX gaps are fetched by one loop over a concatenated list, and are
  -- indistinguishable from there on. The flag is what keeps them apart, and they are
  -- not the same size of problem: a missing rate breaks valuation for every instrument
  -- denominated in that currency, not for one.
  is_fx         BOOLEAN NOT NULL,
  -- The inputs to the eligibility decision this row explains. The asset class is
  -- the security's; the currency and the exchange are the line's own, the
  -- exchange being its whole venue set comma-joined because that is what the
  -- comparison now runs against -- a plugin carrying any one of them accepts the
  -- line.
  asset_class   TEXT,
  currency      TEXT,
  exchange      TEXT,
  -- Days outstanding when the cycle picked the gap up, summed over its ranges. This is
  -- what shrinks cycle over cycle while coverage recording is working, and what stops
  -- shrinking when it is not.
  days_outstanding INT NOT NULL,
  -- settled_empty is a success and must not be read as a failure: every outstanding
  -- range was closed by coverage without bars, because the instrument did not trade
  -- then or no plugin reaches that far back. The gap is settled and will not be asked
  -- about again, which is the whole purpose of recording empty coverage.
  --
  -- A settled_empty gap with no price_plugin_call rows under it is the one shape worth
  -- hunting: every plugin had already covered every range, so the gap recurs in the
  -- gap list every cycle forever while no plugin will ever fill it.
  outcome       TEXT CHECK (outcome IN ('filled', 'settled_empty', 'no_eligible_plugin',
                                        'all_plugins_failed', 'listing_missing'))
);

CREATE INDEX idx_telemetry_price_gap_run ON telemetry.price_gap (run_id);
-- The panel asking whether one instrument ever prices reads this way, and it is the
-- question a hole in a portfolio valuation turns into.
CREATE INDEX idx_telemetry_price_gap_instrument ON telemetry.price_gap (instrument_id);

-- One outstanding range put to one plugin, which is one FetchPrices call in every case
-- but history_limit.
--
-- The range rather than the plugin is the grain because one plugin is asked separately
-- for each range a gap leaves outstanding, and those calls can end differently: a head
-- the plugin cannot reach, a middle it answers, a tail that times out. Collapsing them
-- to one row per (gap, plugin) would force one outcome onto three answers, which is the
-- rule at the top of this file broken.
--
-- Plugins are tried in precedence order until one covers the gap, so a gap normally has
-- rows from fewer plugins than are configured. precedence is what makes that readable,
-- for the reason it is recorded on candidate_plugin_call: a plugin filtered out by
-- asset class, exchange or currency, blocked for this instrument, or holding no
-- identifier it supports, writes no row at all, so a gap in the sequence means skipped.
--
-- There is deliberately no row for a range a plugin had already covered. Recording the
-- absence of work would make the table grow with the catalogue rather than with what
-- was done; the same (instrument, plugin, range) recurring across cycles is the better
-- signal that coverage recording has stopped working, and it is positive evidence.
CREATE TABLE telemetry.price_plugin_call (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  price_gap_id UUID NOT NULL REFERENCES telemetry.price_gap (id) ON DELETE CASCADE,
  plugin_id    TEXT NOT NULL,
  precedence   INT NOT NULL,
  -- Half-open [range_from, range_before), as the orchestrator's ranges are. The
  -- span in days is not stored: it is the subtraction of these two, and a column
  -- holding it could disagree with the range it came from.
  range_from   DATE NOT NULL,
  range_before DATE NOT NULL,
  -- Bars the plugin returned, 0 for every outcome but bars_returned. A plugin that
  -- answered with an empty series records no_data instead, so this is never the way to
  -- tell an empty answer from a failed one.
  bars         INT NOT NULL,
  -- no_data is an answer, not a failure to answer: the range is settled for this plugin
  -- and never asked again. history_limit is the one member that made no call -- the
  -- plugin's configured reach does not extend that far back, so the range is settled
  -- without asking. upsert_failed is our database rather than the provider's API, and
  -- is separate for the reason transport failure is separate from not_identified: the
  -- two need different fixes.
  outcome      TEXT NOT NULL CHECK (outcome IN ('bars_returned', 'no_data', 'history_limit',
                                                'permanent_block', 'timeout', 'error',
                                                'upsert_failed')),
  -- Null for history_limit, which called nothing and so has no clock, for the reason a
  -- plugin costing no tokens writes null rather than zero.
  duration_ms  INT
);

CREATE INDEX idx_telemetry_price_plugin_call_gap
  ON telemetry.price_plugin_call (price_gap_id);

-- Views.
--
-- One per table, each flattening its parents in and never fanning out into its
-- children. A view spanning two sibling grains duplicates the parent's rows and makes
-- counting it silently wrong, so v_resolution_key must stay one row per key however
-- many attempts it produced.
--
-- Judgements are computed columns here; selection belongs to the panel. A panel reads
-- "where is_import and reached_plugins" rather than repeating a list of excluded
-- outcomes, so a definition is stated once in reviewed SQL rather than retyped into
-- each dashboard.
--
-- A parent's column keeps a prefixed name where the child has one of its own -- both a
-- run and a resolution key have a source, and both a key and an attempt have an
-- outcome -- so that a panel naming a column cannot be reading the wrong grain's.

-- is_import is the run kinds that are an import of transactions or an archive, as
-- against a worker cycle.
CREATE VIEW telemetry.v_run AS
SELECT
  r.id,
  r.kind,
  r.job_id,
  r.user_id,
  r.broker,
  r.source,
  r.started_at,
  r.ended_at,
  r.outcome,
  r.telemetry_incomplete,
  r.kind IN ('tx_import', 'user_archive_import', 'system_archive_import') AS is_import,
  -- Null while the run is in flight, and null for a run stamped incomplete: the
  -- sweep knows the process died but not when, so there is no end to measure to.
  (EXTRACT(EPOCH FROM (r.ended_at - r.started_at)) * 1000)::BIGINT AS duration_ms,
  -- How big was this run. Scalar subqueries, one per child table, because a join
  -- to two sibling grains would multiply both counts -- the failure the one view
  -- per table rule exists to prevent. An aggregate is not that failure: it
  -- duplicates no row, and v_run stays one row per run. Anything reaching into a
  -- second child must be written this way, never as a second JOIN.
  (SELECT count(*) FROM telemetry.resolution_key k
     WHERE k.run_id = r.id) AS key_count,
  -- Transactions that needed resolution, not rows in the imported file: a row
  -- naming no instrument never becomes a key. This is the denominator every rate
  -- over resolution keys wants, and the closest thing to an import's size that
  -- the schema records -- ingestion_jobs is out of the reading role's reach by
  -- design, so the file's own row count is not available here.
  (SELECT coalesce(sum(k.tx_count), 0) FROM telemetry.resolution_key k
     WHERE k.run_id = r.id) AS key_tx_count,
  (SELECT count(*) FROM telemetry.candidate_plugin_call c
     WHERE c.run_id = r.id) AS candidate_call_count,
  -- How big a price fetch cycle was. Instruments rather than days, because the
  -- instrument is what the run dashboard drills into and what a hole in a
  -- valuation is traced back to; v_price_gap.days_outstanding carries the other
  -- measure at the grain that owns it. Zero for every other run kind, as
  -- key_count is for this one.
  (SELECT count(*) FROM telemetry.price_gap g
     WHERE g.run_id = r.id) AS gap_count
FROM telemetry.run r;

CREATE VIEW telemetry.v_resolution_key AS
SELECT
  k.id,
  k.run_id,
  k.source,
  k.description,
  k.tx_count,
  k.had_identifier_hints,
  k.security_type_hint,
  k.instrument_kind,
  k.candidate_outcome,
  k.outcome,
  k.mismatch_detected,
  k.instrument_id,
  -- The key ended holding a real identifier, from wherever it came.
  --
  -- broker_description_only is deliberately outside it: nothing identified the
  -- instrument and the row's own contents are all it was built from, which is a
  -- failure for a transaction import however ordinary it looks. It is also the
  -- expected outcome for an archive run, which is why a panel charting the
  -- complement of this column splits by run kind rather than blending the two --
  -- an archive sits at its own high level and drifts against itself.
  --
  -- instrument_id IS NOT NULL is not the same test and must not be substituted:
  -- an archive key ensured from a supplied identifier has an instrument and
  -- identified nothing. A key whose outcome was never stamped is not resolved
  -- either, hence the explicit null handling rather than a bare IN, which would
  -- yield null and drop the row out of both this column and its negation.
  k.outcome IS NOT NULL AND k.outcome IN ('db_source_description',
                                          'db_identifier_hints',
                                          'identified') AS resolved,
  -- Whether identification was reached at all. Four of the five paths that stamp
  -- a key return before ResolveWithPlugins is called, so no attempt is the normal
  -- case rather than a fault, and telling "never asked" from "asked and failed"
  -- is the first question about a key that did not resolve. EXISTS rather than a
  -- join: this view stays one row per key.
  EXISTS (SELECT 1 FROM telemetry.identification_attempt a
            WHERE a.resolution_key_id = k.id) AS had_attempt,
  r.kind       AS run_kind,
  r.job_id     AS run_job_id,
  r.user_id    AS run_user_id,
  r.broker     AS run_broker,
  r.source     AS run_source,
  r.started_at AS run_started_at,
  r.outcome    AS run_outcome,
  r.telemetry_incomplete AS run_telemetry_incomplete,
  r.kind IN ('tx_import', 'user_archive_import', 'system_archive_import') AS is_import
FROM telemetry.resolution_key k
JOIN telemetry.run r ON r.id = k.run_id;

-- reached_plugins is the denominator for identification failure rate. Using all
-- attempts instead makes the rate fall as the instrument table fills, because more
-- resolutions short-circuit in the database, which reads as improving identification
-- when nothing has changed. A failure-rate panel filters purpose = 'primary' as well,
-- or an import carrying more dual-hint descriptions inflates the denominator with
-- mismatch-check attempts.
CREATE VIEW telemetry.v_identification_attempt AS
SELECT
  a.id,
  a.resolution_key_id,
  a.purpose,
  a.depth,
  a.outcome,
  a.security_type_hint,
  a.asset_class,
  a.had_identifier_hints,
  a.outcome NOT IN ('db_short_circuit', 'no_eligible_plugins') AS reached_plugins,
  k.source      AS key_source,
  k.description AS key_description,
  k.tx_count    AS key_tx_count,
  k.outcome     AS key_outcome,
  k.outcome IS NOT NULL AND k.outcome IN ('db_source_description',
                                          'db_identifier_hints',
                                          'identified') AS key_resolved,
  k.mismatch_detected AS key_mismatch_detected,
  r.id         AS run_id,
  r.kind       AS run_kind,
  r.job_id     AS run_job_id,
  r.user_id    AS run_user_id,
  r.broker     AS run_broker,
  r.source     AS run_source,
  r.started_at AS run_started_at,
  r.outcome    AS run_outcome,
  r.telemetry_incomplete AS run_telemetry_incomplete,
  r.kind IN ('tx_import', 'user_archive_import', 'system_archive_import') AS is_import
FROM telemetry.identification_attempt a
JOIN telemetry.resolution_key k ON k.id = a.resolution_key_id
JOIN telemetry.run r ON r.id = k.run_id;

CREATE VIEW telemetry.v_identifier_plugin_call AS
SELECT
  c.id,
  c.identification_attempt_id,
  c.plugin_id,
  c.outcome,
  c.retries,
  c.duration_ms,
  -- The call did not complete. Distinct from not_identified, which is the call
  -- completing and the answer being no, and from the three orchestrator-decided
  -- successes above it. The API breaking and the API not knowing are different
  -- events with different fixes, and a panel counting plugin failures means this
  -- one. skipped_expired is outside it: nothing was called.
  c.outcome IN ('rate_limited', 'timeout', 'error') AS transport_failed,
  a.purpose      AS attempt_purpose,
  a.depth        AS attempt_depth,
  a.outcome      AS attempt_outcome,
  a.asset_class  AS attempt_asset_class,
  a.security_type_hint AS attempt_security_type_hint,
  a.had_identifier_hints AS attempt_had_identifier_hints,
  a.outcome NOT IN ('db_short_circuit', 'no_eligible_plugins') AS reached_plugins,
  k.id          AS resolution_key_id,
  k.source      AS key_source,
  k.description AS key_description,
  k.tx_count    AS key_tx_count,
  k.outcome     AS key_outcome,
  k.outcome IS NOT NULL AND k.outcome IN ('db_source_description',
                                          'db_identifier_hints',
                                          'identified') AS key_resolved,
  r.id         AS run_id,
  r.kind       AS run_kind,
  r.job_id     AS run_job_id,
  r.user_id    AS run_user_id,
  r.broker     AS run_broker,
  r.source     AS run_source,
  r.started_at AS run_started_at,
  r.outcome    AS run_outcome,
  r.telemetry_incomplete AS run_telemetry_incomplete,
  r.kind IN ('tx_import', 'user_archive_import', 'system_archive_import') AS is_import
FROM telemetry.identifier_plugin_call c
JOIN telemetry.identification_attempt a ON a.id = c.identification_attempt_id
JOIN telemetry.resolution_key k ON k.id = a.resolution_key_id
JOIN telemetry.run r ON r.id = k.run_id;

-- One row per claim, with its call and that call's parents flattened in. It
-- carries no judgement column of its own: whether an association may be acted on
-- is a question about two rows sharing a call_id rather than about one row, so a
-- panel groups rather than filters.
CREATE VIEW telemetry.v_identifier_claim AS
SELECT
  cl.id,
  cl.call_id,
  cl.identifier_type,
  cl.domain,
  cl.value,
  cl.role,
  c.plugin_id,
  c.outcome     AS call_outcome,
  c.identification_attempt_id,
  a.purpose      AS attempt_purpose,
  a.depth        AS attempt_depth,
  a.outcome      AS attempt_outcome,
  a.asset_class  AS attempt_asset_class,
  a.outcome NOT IN ('db_short_circuit', 'no_eligible_plugins') AS reached_plugins,
  k.id          AS resolution_key_id,
  k.source      AS key_source,
  k.description AS key_description,
  k.tx_count    AS key_tx_count,
  k.outcome     AS key_outcome,
  k.outcome IS NOT NULL AND k.outcome IN ('db_source_description',
                                          'db_identifier_hints',
                                          'identified') AS key_resolved,
  r.id         AS run_id,
  r.kind       AS run_kind,
  r.job_id     AS run_job_id,
  r.user_id    AS run_user_id,
  r.broker     AS run_broker,
  r.source     AS run_source,
  r.started_at AS run_started_at,
  r.outcome    AS run_outcome,
  r.telemetry_incomplete AS run_telemetry_incomplete,
  r.kind IN ('tx_import', 'user_archive_import', 'system_archive_import') AS is_import
FROM telemetry.identifier_claim cl
JOIN telemetry.identifier_plugin_call c ON c.id = cl.call_id
JOIN telemetry.identification_attempt a ON a.id = c.identification_attempt_id
JOIN telemetry.resolution_key k ON k.id = a.resolution_key_id
JOIN telemetry.run r ON r.id = k.run_id;

CREATE VIEW telemetry.v_candidate_plugin_call AS
SELECT
  c.id,
  c.run_id,
  c.plugin_id,
  c.precedence,
  c.batch_size,
  c.items_completed,
  c.fields_proposed,
  c.outcome,
  -- The call did not produce an answer. no_hints is deliberately outside it: an
  -- empty answer is an answer, and counting it as a fault would make a plugin
  -- that correctly recognised it knew nothing look broken.
  c.outcome IN ('error', 'rate_limited', 'quota_exceeded',
                'model_not_found') AS call_failed,
  c.prompt_tokens,
  c.completion_tokens,
  c.total_tokens,
  c.duration_ms,
  r.kind       AS run_kind,
  r.job_id     AS run_job_id,
  r.user_id    AS run_user_id,
  r.broker     AS run_broker,
  r.source     AS run_source,
  r.started_at AS run_started_at,
  r.outcome    AS run_outcome,
  r.telemetry_incomplete AS run_telemetry_incomplete,
  r.kind IN ('tx_import', 'user_archive_import', 'system_archive_import') AS is_import
FROM telemetry.candidate_plugin_call c
JOIN telemetry.run r ON r.id = c.run_id;

-- Every proposed field with the key it was proposed for and the call it came from,
-- flattened so that accuracy by field, cost per resolution and confidence against
-- outcome are each one query rather than a three-way join written by hand.
--
-- The key's own outcome travels with the field, because "the venue was confirmed"
-- and "the key resolved at all" are different questions and a panel almost always
-- wants both: a field confirmed on a key that ended broker-description-only did not
-- help anybody.
CREATE VIEW telemetry.v_candidate_field AS
SELECT
  f.id,
  f.field,
  f.value,
  f.confidence,
  f.outcome,
  -- The guess was checked against something. The complement is not "wrong": it is
  -- untested and unused together, which is the share of what was paid for that
  -- nothing could evaluate.
  f.outcome IN ('confirmed', 'contradicted') AS was_tested,
  c.id         AS call_id,
  c.plugin_id,
  c.precedence,
  c.total_tokens AS call_total_tokens,
  c.batch_size   AS call_batch_size,
  k.id         AS resolution_key_id,
  k.source,
  k.description,
  k.tx_count,
  k.had_identifier_hints,
  k.security_type_hint,
  k.candidate_outcome,
  k.outcome    AS key_outcome,
  k.instrument_id,
  r.id         AS run_id,
  r.kind       AS run_kind,
  r.job_id     AS run_job_id,
  r.user_id    AS run_user_id,
  r.broker     AS run_broker,
  r.source     AS run_source,
  r.started_at AS run_started_at,
  r.outcome    AS run_outcome,
  r.telemetry_incomplete AS run_telemetry_incomplete,
  r.kind IN ('tx_import', 'user_archive_import', 'system_archive_import') AS is_import
FROM telemetry.candidate_field f
JOIN telemetry.candidate_plugin_call c ON c.id = f.call_id
JOIN telemetry.resolution_key k ON k.id = f.resolution_key_id
JOIN telemetry.run r ON r.id = k.run_id;

CREATE VIEW telemetry.v_price_gap AS
SELECT
  g.id,
  g.run_id,
  g.listing_id,
  g.instrument_id,
  g.is_fx,
  g.asset_class,
  g.currency,
  g.exchange,
  g.days_outstanding,
  g.outcome,
  -- The gap is dealt with and will not be back. settled_empty is deliberately
  -- inside it: a range no plugin can fill is settled once that is recorded, and
  -- counting it as a failure would make an untraded week look like an outage.
  --
  -- The complement is the column a panel wants -- a gap that recurs every cycle,
  -- costing discovery work and leaving a hole in valuation. A null outcome is not
  -- settled either, hence the explicit null handling rather than a bare IN, which
  -- would yield null and drop the row out of both this column and its negation.
  g.outcome IS NOT NULL AND g.outcome IN ('filled', 'settled_empty') AS settled,
  -- Whether any plugin was asked at all. Filters, fetch blocks, missing identifiers
  -- and existing coverage each return before FetchPrices is called, so no call is an
  -- ordinary case rather than a fault, and telling "never asked" from "asked and
  -- failed" is the first question about a gap that did not fill. EXISTS rather than a
  -- join: this view stays one row per gap.
  EXISTS (SELECT 1 FROM telemetry.price_plugin_call c
            WHERE c.price_gap_id = g.id) AS had_call,
  r.kind       AS run_kind,
  r.started_at AS run_started_at,
  r.outcome    AS run_outcome,
  r.telemetry_incomplete AS run_telemetry_incomplete,
  -- Constant false while price_fetch_cycle is the only kind that fills gaps. Carried
  -- because every view here carries it, so a panel written against one grain does not
  -- have to remember which grains are the exception.
  r.kind IN ('tx_import', 'user_archive_import', 'system_archive_import') AS is_import
FROM telemetry.price_gap g
JOIN telemetry.run r ON r.id = g.run_id;

CREATE VIEW telemetry.v_price_plugin_call AS
SELECT
  c.id,
  c.price_gap_id,
  c.plugin_id,
  c.precedence,
  c.range_from,
  c.range_before,
  -- Derived rather than stored, so it cannot disagree with the range above it.
  -- DATE minus DATE is a whole number of days in postgres, which is the
  -- resolution a fetch range has.
  (c.range_before - c.range_from) AS days,
  c.bars,
  c.outcome,
  c.duration_ms,
  -- The call did not complete. no_data is deliberately outside it, for the reason
  -- not_identified is outside the identifier plugin's transport_failed: the provider
  -- having nothing and the provider being unreachable are different events with
  -- different fixes. history_limit is outside it because nothing was called, and
  -- permanent_block because it is a durable fact about the pair rather than an
  -- incident -- it creates a fetch block, so it happens once and never again.
  c.outcome IN ('timeout', 'error') AS transport_failed,
  -- Ours rather than theirs. Separate from transport_failed so a panel watching
  -- provider health is not moved by our own write failing, and so that a burst of
  -- these reads as what it is.
  c.outcome = 'upsert_failed' AS write_failed,
  g.instrument_id AS gap_instrument_id,
  g.is_fx         AS gap_is_fx,
  g.asset_class   AS gap_asset_class,
  g.currency      AS gap_currency,
  g.exchange      AS gap_exchange,
  g.days_outstanding AS gap_days_outstanding,
  g.outcome       AS gap_outcome,
  g.outcome IS NOT NULL AND g.outcome IN ('filled', 'settled_empty') AS gap_settled,
  r.id         AS run_id,
  r.kind       AS run_kind,
  r.started_at AS run_started_at,
  r.outcome    AS run_outcome,
  r.telemetry_incomplete AS run_telemetry_incomplete,
  r.kind IN ('tx_import', 'user_archive_import', 'system_archive_import') AS is_import
FROM telemetry.price_plugin_call c
JOIN telemetry.price_gap g ON g.id = c.price_gap_id
JOIN telemetry.run r ON r.id = g.run_id;

-- The one thing here that reads outside the telemetry schema.
--
-- A resolution key records instrument_id and nothing readable, so no panel can
-- say which instrument a description landed on -- which is the question asked
-- after every manual import, because "APPLE INC COM" resolving to the wrong
-- listing looks identical to it resolving to the right one in a bare UUID.
--
-- Recording a label at write time was rejected: the readable instrument is two
-- frames below the write site and deliberately discarded, and three of the five
-- paths that stamp a key never hold one at all, so the column would be null
-- exactly where identification was most interesting.
--
-- So the lookup lives here instead. A view runs with its owner's privileges, not
-- its caller's, so granting SELECT on this view lets the reading role resolve a
-- UUID to a name while still holding no USAGE on public and no privilege on
-- instruments. That is the point of the indirection: one narrow window, reviewed
-- in this file, rather than a grant on the application schema.
--
-- It is a live lookup and not a recorded fact. An instrument renamed today
-- changes what a panel says about a run from last year. That is the right
-- trade for a label -- the recorded facts are the outcome columns, and they do
-- not move -- but it is why nothing here is joined into the views above.
--
-- instruments.name is already the readable form: a trigger keeps it at the
-- preferred identifier, ticker first, falling back through OCC and the broker
-- description to the id itself. See 001_initial.sql.
--
-- The asset class and nothing else beside the label. A currency and a venue are
-- facts about one of the security's lines rather than about the security, so
-- there is nothing here to join them from; a panel wanting either reads the
-- recorded columns on the row it is explaining, which is where the grain is
-- right.
CREATE VIEW telemetry.v_instrument_label AS
SELECT
  i.id,
  i.name AS label,
  i.asset_class
FROM instruments i;

-- The reading role.
--
-- telemetry_reader is a NOLOGIN group role holding the privileges, not the login the
-- dashboard uses. The login role and its password come from compose environment at
-- container init and are granted membership here, which is what keeps a password out
-- of a file in the repository. Creating it is guarded because a role is a
-- cluster-wide object while a migration is per-database.
--
-- SELECT and nothing else, on the views and the tables alike. The default privileges
-- cover a table added by a later migration, so a new grain cannot go unreadable
-- without anyone noticing.
DO $$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'telemetry_reader') THEN
    CREATE ROLE telemetry_reader NOLOGIN;
  END IF;
END
$$;

GRANT USAGE ON SCHEMA telemetry TO telemetry_reader;
GRANT SELECT ON ALL TABLES IN SCHEMA telemetry TO telemetry_reader;
ALTER DEFAULT PRIVILEGES IN SCHEMA telemetry GRANT SELECT ON TABLES TO telemetry_reader;
