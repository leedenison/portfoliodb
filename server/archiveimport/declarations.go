package archiveimport

import (
	"context"
	"fmt"
	"time"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/identifier"
	"github.com/leedenison/portfoliodb/server/service/identification"
	"github.com/shopspring/decimal"
)

// declarationDateLayout is the date form the archive states.
const declarationDateLayout = "2006-01-02"

// DeclarationPart restores a user's holding declarations and reports through
// rep. It returns how many declarations were written.
//
// Import is an upsert on (user, broker, account, instrument, as_of_date). The
// unique key means a re-import collides on every unchanged row, and the
// AlreadyExists the declaration API answers with -- right for a user filling in
// a form -- would fail the restore this exists for. A row whose quantity has
// changed restates the declaration; a row identical to what is stored changes
// nothing.
//
// Absence is not deletion, and this is the one place the archive deliberately
// differs from the transaction part. A declaration missing from the file is left
// alone: a file assembled from one statement covers one account and one date,
// and treating everything outside it as retracted would delete the user's other
// checkpoints. Deleting one stays an explicit action.
//
// The pads are not written here. Which declaration pads a holding depends on the
// others in it, so the unit of recalculation is the holding rather than the
// declaration, and the caller settles every one of them from the set as it
// stands once the whole part has landed. See
// docs/adr/0030-declarations-are-padded-then-asserted.md.
func DeclarationPart(ctx context.Context, database db.DB, userID string, part *archivev1.DeclarationPart, rep *PartReporter) (int32, error) {
	statements := part.GetStatements()
	// The part's unit is a declaration rather than a statement, which is what a
	// reader watching the job expects to see move.
	total := 0
	for _, st := range statements {
		total += len(st.GetDeclarations())
	}
	rep.Total(ctx, total)
	if total == 0 {
		return 0, nil
	}

	// A declaration is padded from and checked against the transactions, so a
	// portfolio with none has nothing to anchor one to. That is the precondition
	// the create RPC states, and it fails the part rather than every row in it:
	// the file is fine and the instance is not ready for it.
	startDate, err := database.GetPortfolioStartDate(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("get portfolio start date: %w", err)
	}
	if startDate == nil {
		return 0, fmt.Errorf("no transactions exist; import transactions before declarations")
	}
	startDay := startDate.Truncate(24 * time.Hour)

	var written int32
	// Row indices run across the whole part rather than restarting per
	// statement, so a problem points at a declaration in the document.
	row := 0
	for _, st := range statements {
		broker := db.BrokerToStr(st.GetBroker())
		asOfDate, dateErr := time.Parse(declarationDateLayout, st.GetAsOfDate())
		for _, d := range st.GetDeclarations() {
			idx := row
			row++
			rep.Advance(ctx, 1)

			if broker == "" {
				rep.Errf(idx, "broker", fmt.Sprintf("unknown broker %q", st.GetBroker()))
				continue
			}
			if dateErr != nil {
				rep.Errf(idx, "as_of_date", fmt.Sprintf("%q is not a date: %v", st.GetAsOfDate(), dateErr))
				continue
			}
			// Rejected here rather than left to the recalculation, which deletes
			// the declarations the start date has moved past. Writing it would
			// report success and then take it away again with nothing to show
			// the user for it.
			if asOfDate.Before(startDay) {
				rep.Errf(idx, "as_of_date", fmt.Sprintf("%s is before the portfolio start date (%s)",
					st.GetAsOfDate(), startDay.Format(declarationDateLayout)))
				continue
			}
			qty, err := decimal.NewFromString(d.GetDeclaredQty())
			if err != nil {
				rep.Errf(idx, "declared_qty", fmt.Sprintf("%q is not a decimal: %v", d.GetDeclaredQty(), err))
				continue
			}
			basis := asOfDate
			if d.ShareCountBasis != nil {
				basis, err = time.Parse(declarationDateLayout, d.GetShareCountBasis())
				if err != nil {
					rep.Errf(idx, "share_count_basis", fmt.Sprintf("%q is not a date: %v", d.GetShareCountBasis(), err))
					continue
				}
			}
			instrumentID, err := findDeclaredInstrument(ctx, database, d.GetInstrument(), idx, rep)
			if err != nil {
				return written, fmt.Errorf("resolve instrument: %w", err)
			}
			if instrumentID == "" {
				continue
			}
			// The line the declaration is on. The file states no currency of its
			// own yet -- naming the listing in the archive is 0151 -- so this is
			// the last rung of the ladder, the security's sole line, and no line
			// where it has several.
			listingID, err := database.SoleListing(ctx, instrumentID)
			if err != nil {
				return written, fmt.Errorf("resolve listing: %w", err)
			}

			holding := db.Holding{
				UserID:       userID,
				Broker:       broker,
				Account:      st.GetAccount(),
				InstrumentID: instrumentID,
				ListingID:    listingID,
			}
			if err := database.UpsertHoldingDeclaration(ctx, holding, qty.String(), asOfDate, basis); err != nil {
				return written, fmt.Errorf("upsert holding declaration: %w", err)
			}
			written++
		}
	}
	return written, nil
}

// findDeclaredInstrument maps a declaration's instrument reference to an
// instrument this instance already has, reporting a miss against the row.
//
// It resolves against the database alone and never creates an instrument, which
// is what makes failing loudly possible at all: the transaction path cannot
// fail, because an unresolved posting lands on a placeholder instrument built
// from its description. A declaration has no such fallback to offer. It is a
// statement about a holding the system cannot name -- there is nothing for it to
// pad and nothing to check it against -- so the row is rejected and the rest of
// the file lands.
//
// A row failing here is a defect rather than a user error: the instrument was
// picked in the UI when the declaration was made, and the export writes whichever
// identifier the listing join ranks highest. ResolveByHintsDBOnly rather than a
// bare lookup is what keeps the two ends in step -- it normalises OCC to the
// compact form the column stores, and falls back to (type, value) where the file
// states no domain.
//
// It returns "" when the row has nothing to attach itself to, and an error only
// where the lookup itself failed.
func findDeclaredInstrument(ctx context.Context, database db.DB, ref *archivev1.InstrumentRef, idx int, rep *PartReporter) (string, error) {
	if ref.GetValue() == "" {
		rep.Errf(idx, "instrument", "declaration names no instrument")
		return "", nil
	}
	hint := identifier.Identifier{
		Type:   ref.GetType().String(),
		Domain: ref.GetDomain(),
		Value:  ref.GetValue(),
	}
	ids, err := identification.ResolveIDsByHintsDBOnly(ctx, database, []identifier.Identifier{hint})
	if err != nil {
		return "", err
	}
	if len(ids) == 0 {
		rep.Errf(idx, "instrument", fmt.Sprintf("%s %s is not an instrument this instance has", ref.GetType(), ref.GetValue()))
		return "", nil
	}
	return ids[0], nil
}
