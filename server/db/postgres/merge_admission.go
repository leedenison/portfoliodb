package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
)

// Merge admission: which of the instruments an identifier set landed on may be
// merged into one.
//
// A merge acts on the claim that two identifiers denote one security, never on
// the identifier set the caller assembled. Two identifiers reaching one caller
// from two answers are a set nobody asserted, and merging on it is how two share
// classes on one venue become one instrument. See
// docs/adr/0060-an-identity-claim-is-admitted-by-the-authority-of-its-source.md.
//
// What a claim asserts is that its identifiers denote one security *now*. What
// it takes to act on that is a chain: the stored row tying one of them to the
// instrument holding it, the claim, and the stored row at the other end. So the
// claim alone does not admit a merge -- each of the two stored associations has
// to be one a chain may be drawn through, which is a question about the
// identifier type and about the interval the row was written with. See
// docs/adr/0061-transitivity-needs-a-non-reassigned-identifier.md.

// endpoint is one end of a candidate merge: an instrument, reached through one
// stored identifier row, and what that row says about when the name was correct.
//
// The type and the interval are the row's rather than the caller's. A caller
// naming a value states what it believes today; whether that belief reaches the
// instrument holding the value is what the stored row decides.
type endpoint struct {
	instrument uuid.UUID
	typ        string
	from       *time.Time
	before     *time.Time
}

// findHolder returns the instrument holding one identifier, and the interval the
// row that holds it was written with.
//
// The type says which table to ask, as FindInstrumentByIdentifier's dispatch
// does, and the ordering is that function's: the name in force wins and the most
// recently closed one is the fallback, so a value two instruments held over
// disjoint intervals answers with its current holder. FindInstrumentByIdentifier
// delegates here rather than restating the query, so the two cannot come to
// disagree about which row a value resolves to.
func findHolder(ctx context.Context, q queryable, ref db.InstrumentRef) (endpoint, bool, error) {
	table := "instrument_identifiers"
	if identifier.NamesAListing(ref.Type) {
		table = "instrument_listing_identifiers"
	}
	where, args := "domain IS NULL", []any{ref.Type, ref.Value}
	if ref.Domain != "" {
		where, args = "domain = $3", []any{ref.Type, ref.Value, ref.Domain}
	}
	var (
		id           uuid.UUID
		from, before sql.NullTime
	)
	err := q.QueryRowContext(ctx, fmt.Sprintf(`
		SELECT instrument_id, valid_from, valid_before
		FROM %s
		WHERE identifier_type = $1 AND value = $2 AND %s
		ORDER BY valid_before IS NULL DESC, valid_before DESC
		LIMIT 1
	`, table, where), args...).Scan(&id, &from, &before)
	if err == sql.ErrNoRows {
		return endpoint{}, false, nil
	}
	if err != nil {
		return endpoint{}, false, fmt.Errorf("find identifier holder: %w", err)
	}
	e := endpoint{instrument: id, typ: ref.Type}
	if from.Valid {
		e.from = &from.Time
	}
	if before.Valid {
		e.before = &before.Time
	}
	return e, true, nil
}

// mergeEnd pairs the identifier a caller named with the stored row it reached.
// The decision is taken on the endpoint and reported on the ref: a triple
// outlives the merge where an instrument does not, so what is recorded has to be
// the name rather than the row.
type mergeEnd struct {
	ref db.InstrumentRef
	ep  endpoint
}

// mergeDecision is one answer to whether two identifiers denote one security.
// The pair is the unit, not the resolution: identifiers landing on three
// instruments ask the question twice and can answer it differently each time.
type mergeDecision struct {
	outcome string
	a, b    mergeEnd
}

// key identifies a decision by the two names and what was decided, so that two
// claims reaching the same verdict about one pair record it once. Two claims
// reaching different verdicts are two findings and both are kept.
//
// The two names are ordered here and not in the row: which way round a claim
// listed them says nothing, so it must not make one finding into two, while the
// row keeps the order it was built with because an uncorroborated refusal is
// reported against the anchor and that is worth reading off the row.
func (d mergeDecision) key() string {
	a, b := refKey(d.a.ref), refKey(d.b.ref)
	if a > b {
		a, b = b, a
	}
	return d.outcome + "\x00" + a + "\x00" + b
}

// refKey identifies an identifier by the triple that names it, so a value looked
// up once is not looked up again. Domains are normalized to operating MICs
// before a key is taken, as the lookup itself is.
func refKey(ref db.InstrumentRef) string {
	return ref.Type + "\x00" + ref.Domain + "\x00" + ref.Value
}

// collidingIdentifier returns a triple both instruments hold over overlapping
// intervals, if there is one.
//
// That is a merge that cannot complete. The two claims cannot both hold and
// nothing in the data says which is right: either two instruments were validly
// but wrongly identified, or a corporate action nobody knows about would have
// closed one of the intervals. Carrying the loser's name across would fail the
// exclusion constraint, and dropping it -- which is what the merge used to do --
// destroys the only evidence a contradiction was seen. So the merge stops and the
// collision is recorded. See
// docs/adr/0064-a-claim-that-cannot-hold-is-flagged-not-resolved.md.
//
// Both grains are asked, in the order a name is looked up in. Only the first is
// returned: one is enough to stop the merge, and a merge that stops has not
// begun to move anything, so there is no second name to report having lost.
//
// Note that the exclusion constraints on both tables are global and say nothing
// about the instrument, so two instruments holding one triple over overlapping
// intervals is a state they already forbid and this finds nothing today. It
// becomes reachable when the owner enters the constraint, which is 0142.
func collidingIdentifier(ctx context.Context, q queryable, a, b uuid.UUID) (db.InstrumentRef, bool, error) {
	for _, table := range []string{"instrument_identifiers", "instrument_listing_identifiers"} {
		var (
			typ    string
			domain sql.NullString
			value  string
		)
		err := q.QueryRowContext(ctx, fmt.Sprintf(`
			SELECT x.identifier_type, x.domain, x.value
			FROM %[1]s x
			JOIN %[1]s y
			  ON y.identifier_type = x.identifier_type
			 AND COALESCE(y.domain, '') = COALESCE(x.domain, '')
			 AND y.value = x.value
			 AND daterange(y.valid_from, y.valid_before) && daterange(x.valid_from, x.valid_before)
			WHERE x.instrument_id = $1 AND y.instrument_id = $2
			LIMIT 1
		`, table), a, b).Scan(&typ, &domain, &value)
		if err == sql.ErrNoRows {
			continue
		}
		if err != nil {
			return db.InstrumentRef{}, false, fmt.Errorf("find colliding identifier: %w", err)
		}
		return db.InstrumentRef{Type: typ, Domain: domain.String, Value: value}, true, nil
	}
	return db.InstrumentRef{}, false, nil
}

// mergeGroup returns the instruments that may be merged into anchor: anchor
// itself, whatever a single claim tied to it, and whatever a claim tied to one of
// those in turn. Everything else the identifiers landed on is left alone.
//
// Transitive because each admitted link is a corroborated association in its own
// right, so a claim tying A to B and another tying B to C says A and C are one
// as surely as one claim naming all three would. Held apart from the identifiers
// the caller supplied: the group is what claims corroborate, not what the lookup
// found.
//
// held is what the caller's own identifiers already resolved to. A claim may
// name values beyond those -- a value the result was strictly filtered on is
// never stored and so is never in the caller's set, and adr/0060 has it
// corroborating the association as loudly as a returned value does -- and those
// are looked up here. This runs only where the identifiers landed on more than
// one instrument, so the extra lookups stay off the path every ordinary
// resolution takes.
//
// The decisions it took are returned alongside the group. Every pair a claim
// named together is one, whether it was admitted or refused, because the refusal
// is as much the finding as the merge: counting how often each reason fires is
// what says whether the enabled plugin set can corroborate anything.
func (p *Postgres) mergeGroup(ctx context.Context, anchor uuid.UUID, claims []db.IdentityClaim, held map[string]endpoint) ([]uuid.UUID, []mergeDecision, error) {
	var decisions []mergeDecision
	seenDecision := make(map[string]bool)
	record := func(d mergeDecision) {
		if k := d.key(); !seenDecision[k] {
			seenDecision[k] = true
			decisions = append(decisions, d)
		}
	}
	linked := make(map[uuid.UUID]map[uuid.UUID]bool)
	link := func(a, b endpoint) {
		if linked[a.instrument] == nil {
			linked[a.instrument] = make(map[uuid.UUID]bool)
		}
		if linked[b.instrument] == nil {
			linked[b.instrument] = make(map[uuid.UUID]bool)
		}
		linked[a.instrument][b.instrument] = true
		linked[b.instrument][a.instrument] = true
	}
	for _, c := range claims {
		ends := make([]mergeEnd, 0, len(c.Identifiers))
		for _, ci := range c.Identifiers {
			ref := ci.Ref
			if ref.Type == "MIC_TICKER" && ref.Domain != "" {
				ref.Domain = p.normalizeToOperatingMIC(ctx, ref.Domain)
			}
			e, ok := held[refKey(ref)]
			if !ok {
				var err error
				e, ok, err = findHolder(ctx, p.q, ref)
				if err != nil {
					return nil, nil, err
				}
				held[refKey(ref)] = e
				if !ok {
					// Cached as the zero endpoint so a value named by several
					// claims is asked for once. uuid.Nil holds no identifier, so
					// it never links anything.
					continue
				}
			}
			if e.instrument == uuid.Nil {
				continue
			}
			ends = append(ends, mergeEnd{ref: ref, ep: e})
		}
		// One claim, every pair of instruments it reached. The claim names them
		// together, so it corroborates each pair equally.
		//
		// A pair already on one instrument is not a decision: nothing is being
		// joined, so there is nothing to admit or refuse and nothing to record.
		for i := range ends {
			for j := i + 1; j < len(ends); j++ {
				if ends[i].ep.instrument == ends[j].ep.instrument {
					continue
				}
				v := mergeVerdict(ends[i].ep, ends[j].ep)
				record(mergeDecision{outcome: v, a: ends[i], b: ends[j]})
				if v == db.TelemetryMerged {
					link(ends[i].ep, ends[j].ep)
				}
			}
		}
	}
	// The anchor's component, breadth-first so the survivor tie-break sees the
	// same set however the links were discovered.
	group := []uuid.UUID{anchor}
	inGroup := map[uuid.UUID]bool{anchor: true}
	for i := 0; i < len(group); i++ {
		for next := range linked[group[i]] {
			if !inGroup[next] {
				inGroup[next] = true
				group = append(group, next)
			}
		}
	}
	return group, decisions, nil
}

// uncorroborated is one refusal for each instrument the caller's names reached
// that no claim named alongside anything, reported against the anchor.
//
// An instrument some claim did name is skipped: mergeGroup has already said why
// that pair was refused, and a second row would count one refusal twice under
// two reasons. The anchor itself is skipped for the obvious reason, and so is
// anything the group swallowed.
func uncorroborated(distinctIDs, group []uuid.UUID, decided []mergeDecision, reachedBy map[uuid.UUID]db.InstrumentRef) []mergeDecision {
	if len(distinctIDs) < 2 {
		return nil
	}
	inGroup := make(map[uuid.UUID]bool, len(group))
	for _, id := range group {
		inGroup[id] = true
	}
	named := make(map[uuid.UUID]bool, len(decided)*2)
	for _, d := range decided {
		named[d.a.ep.instrument] = true
		named[d.b.ep.instrument] = true
	}
	anchor := distinctIDs[0]
	var out []mergeDecision
	for _, id := range distinctIDs[1:] {
		if inGroup[id] || named[id] {
			continue
		}
		out = append(out, mergeDecision{
			outcome: db.TelemetryMergeUncorroborated,
			a:       mergeEnd{ref: reachedBy[anchor], ep: endpoint{instrument: anchor}},
			b:       mergeEnd{ref: reachedBy[id], ep: endpoint{instrument: id}},
		})
	}
	return out
}

// recordMerges writes what was decided, once the writes those decisions
// authorised have committed.
//
// After the transaction rather than inside it, so that a merge rolled back is
// not reported as one that happened. A refusal in a rolled back transaction is
// lost with it, which is the right way round: the run carries the failure, and a
// refusal that never took effect explains nothing.
//
// collided repoints an admitted merge that could not complete. The pair was
// corroborated -- that judgement stands and is not what failed -- so what is
// recorded is the collision rather than the admission.
func (p *Postgres) recordMerges(ctx context.Context, runID string, decisions []mergeDecision, collided map[uuid.UUID]db.InstrumentRef) {
	if runID == "" || len(decisions) == 0 {
		return
	}
	tel := p.telemetry()
	for _, d := range decisions {
		outcome := d.outcome
		var collision *db.InstrumentRef
		if outcome == db.TelemetryMerged {
			for _, inst := range []uuid.UUID{d.a.ep.instrument, d.b.ep.instrument} {
				if c, ok := collided[inst]; ok {
					c := c
					outcome, collision = db.TelemetryMergeCollision, &c
					break
				}
			}
		}
		tel.WriteMerge(ctx, db.TelemetryMerge{
			RunID:       runID,
			Outcome:     outcome,
			A:           d.a.ref,
			B:           d.b.ref,
			AInstrument: d.a.ep.instrument.String(),
			BInstrument: d.b.ep.instrument.String(),
			Collision:   collision,
		})
	}
}

// mergeVerdict reports whether a claim naming these two endpoints corroborates
// merging the instruments that hold them, and where it does not, why.
//
// Every condition is asked of both endpoints, because the chain runs through
// both stored rows and is no stronger than its weaker end. A type that reassigns
// its values as a matter of course carries nothing: a claim about today's EA
// says nothing about the security that held the ticker in 2019, and the same
// goes for a contract symbol a split has handed down the strike ladder.
// Non-overlapping intervals say the two rows were never correct at one time, so
// no claim made at any single moment can reach across them.
//
// The reason is kept rather than reduced to a boolean because the two failures
// want different fixes -- a routinely reassigned type is working as intended,
// where two names that were never correct at one time may be a vintage recorded
// wrongly -- and a caller recording what it decided has to say which. Order is
// not arbitrary: a type that cannot mediate says nothing about the interval, so
// it is answered first.
//
// adr/0061's third condition -- that each association be one the system holds as
// settled rather than as a user's claim -- is satisfied by construction and so
// is not asked. There is no owner column yet, so every stored row was written by
// an identifier plugin or an admin's archive. 0142 adds the column, and this is
// where it is read; it is a question about the row rather than about the type,
// since the same value is a fact from one source and a claim from another.
//
// The authority the caller's own claim arrived with is a further input this does
// not take, for the reason db.IdentityClaim gives: every claim reaching here
// today carries system authority. What a claim carrying user authority may do
// instead is 0171 and 0172.
func mergeVerdict(a, b endpoint) string {
	switch {
	case !identifier.MayMediate(a.typ) || !identifier.MayMediate(b.typ):
		return db.TelemetryMergeUnmediated
	case !overlaps(a, b):
		return db.TelemetryMergeDisjoint
	default:
		return db.TelemetryMerged
	}
}

// overlaps reports whether two stored names were both correct at some one time.
// Half-open intervals, and a nil bound is unbounded on that side. See
// docs/adr/0018-half-open-date-intervals.md.
func overlaps(a, b endpoint) bool {
	return (a.before == nil || b.from == nil || b.from.Before(*a.before)) &&
		(b.before == nil || a.from == nil || a.from.Before(*b.before))
}
