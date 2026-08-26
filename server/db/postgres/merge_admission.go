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

// refKey identifies an identifier by the triple that names it, so a value looked
// up once is not looked up again. Domains are normalized to operating MICs
// before a key is taken, as the lookup itself is.
func refKey(ref db.InstrumentRef) string {
	return ref.Type + "\x00" + ref.Domain + "\x00" + ref.Value
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
func (p *Postgres) mergeGroup(ctx context.Context, anchor uuid.UUID, claims []db.IdentityClaim, held map[string]endpoint) ([]uuid.UUID, error) {
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
		ends := make([]endpoint, 0, len(c.Identifiers))
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
					return nil, err
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
			ends = append(ends, e)
		}
		// One claim, every pair of instruments it reached. The claim names them
		// together, so it corroborates each pair equally.
		for i := range ends {
			for j := i + 1; j < len(ends); j++ {
				if ends[i].instrument != ends[j].instrument && mayMerge(ends[i], ends[j]) {
					link(ends[i], ends[j])
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
	return group, nil
}

// mayMerge reports whether a claim naming these two endpoints corroborates
// merging the instruments that hold them.
//
// Every condition is asked of both endpoints, because the chain runs through
// both stored rows and is no stronger than its weaker end. A type that reassigns
// its values as a matter of course carries nothing: a claim about today's EA
// says nothing about the security that held the ticker in 2019, and the same
// goes for a contract symbol a split has handed down the strike ladder.
// Non-overlapping intervals say the two rows were never correct at one time, so
// no claim made at any single moment can reach across them.
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
func mayMerge(a, b endpoint) bool {
	return identifier.MayMediate(a.typ) && identifier.MayMediate(b.typ) && overlaps(a, b)
}

// overlaps reports whether two stored names were both correct at some one time.
// Half-open intervals, and a nil bound is unbounded on that side. See
// docs/adr/0018-half-open-date-intervals.md.
func overlaps(a, b endpoint) bool {
	return (a.before == nil || b.from == nil || b.from.Before(*a.before)) &&
		(b.before == nil || a.from == nil || a.from.Before(*b.before))
}
