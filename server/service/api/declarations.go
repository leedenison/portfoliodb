package api

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/shopspring/decimal"
	"google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ListHoldingDeclarations lists all declarations for the authenticated user.
func (s *Server) ListHoldingDeclarations(ctx context.Context, req *apiv1.ListHoldingDeclarationsRequest) (*apiv1.ListHoldingDeclarationsResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	rows, err := s.db.ListHoldingDeclarations(ctx, u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// Batch-load instruments to avoid N+1 queries.
	instIDs := make([]string, 0, len(rows))
	for _, r := range rows {
		instIDs = append(instIDs, r.InstrumentID)
	}
	instByID := make(map[string]*apiv1.Instrument)
	if len(instIDs) > 0 {
		instRows, err := s.db.ListInstrumentsByIDs(ctx, instIDs)
		if err == nil {
			for _, ir := range instRows {
				instByID[ir.ID] = instrumentRowToProto(ir)
			}
		}
	}
	decls := make([]*apiv1.HoldingDeclaration, len(rows))
	for i, r := range rows {
		decls[i] = declarationToProto(r)
		if inst := instByID[r.InstrumentID]; inst != nil {
			decls[i].Instrument = inst
		}
	}
	return &apiv1.ListHoldingDeclarationsResponse{Declarations: decls}, nil
}

// declarationScale is the rounding scale the split-adjusted columns declare, and so
// the size of one unit in the last place of a converted quantity. See
// docs/adr/0026-exact-decimals-bounded-by-closure.md.
const declarationScale = -12

// declarationToProto converts a stored declaration to its wire form, checking it
// against the computed holding when the read supplied one. The instrument is left to
// the caller: only the list populates it.
func declarationToProto(r *db.HoldingDeclarationRow) *apiv1.HoldingDeclaration {
	out := &apiv1.HoldingDeclaration{
		Id:              r.ID,
		Broker:          r.Broker,
		Account:         r.Account,
		InstrumentId:    r.InstrumentID,
		DeclaredQty:     r.DeclaredQty,
		AsOfDate:        timeToDate(r.AsOfDate),
		ShareCountBasis: timeToDate(r.ShareCountBasis),
		Kind:            r.Kind,
	}
	if r.Verified == nil {
		return out
	}
	declared, err := decimal.NewFromString(r.DeclaredQty)
	if err != nil {
		// A declared_qty that will not parse is a stored value the write path
		// validated, so this cannot happen from the API. Report the declaration
		// without a verdict rather than failing the whole list for it.
		return out
	}

	// The tolerance is a bound, not a fudge factor. Each share count basis that
	// contributes a conversion by something other than 1/1 can round once, at the
	// declared scale of the split-adjusted columns -- so that many units in the last
	// place is the most the two sides can differ by while still agreeing. When no
	// split falls in the window, which is the common case, no basis converts
	// inexactly and the comparison is exact.
	tolerance := decimal.New(int64(r.Verified.InexactBases), declarationScale)
	delta := r.Verified.ComputedQty.Sub(declared)

	out.ComputedQty = decStr(r.Verified.ComputedQty)
	out.Delta = decStr(delta)
	out.Tolerance = decStr(tolerance)
	out.PostingCount = r.Verified.PostingCount
	// A pad is made true by construction, so its own check can only ever pass and
	// reporting it as a result would suggest it had found something out.
	out.Matched = r.Kind == apiv1.DeclarationKind_DECLARATION_KIND_PAD ||
		delta.Abs().LessThanOrEqual(tolerance)
	return out
}

// padDeclaration returns the declaration that seeds a holding's opening balance --
// the earliest of them -- or nil when the holding has none. The pad is derived from
// the set rather than flagged on a row, so adding an earlier declaration or deleting
// the current pad simply changes which one this returns.
func padDeclaration(decls []*db.HoldingDeclarationRow) *db.HoldingDeclarationRow {
	var pad *db.HoldingDeclarationRow
	for _, d := range decls {
		if pad == nil || d.AsOfDate.Before(pad.AsOfDate) {
			pad = d
		}
	}
	return pad
}

// holdingDeclarations filters a user's declarations to one holding, dropping the
// declaration with the given id. Callers pass the id of the row they are about to
// replace or delete, so what comes back is the rest of the holding as it will be
// once the write lands. Pass "" to keep all of them.
func holdingDeclarations(all []*db.HoldingDeclarationRow, broker, account, instrumentID, excludeID string) []*db.HoldingDeclarationRow {
	out := make([]*db.HoldingDeclarationRow, 0, len(all))
	for _, d := range all {
		if d.Broker == broker && d.Account == account && d.InstrumentID == instrumentID && d.ID != excludeID {
			out = append(out, d)
		}
	}
	return out
}

// holdingPad is the pad a write settles on: the transaction to store, and the
// as_of_date of the declaration it came from. Declaration dates are unique within a
// holding, so that date is what tells a caller whether the row it just wrote is the
// pad -- which the pending row's absent id cannot.
type holdingPad struct {
	init *db.InitializeTx // nil when the holding has no declarations left
	asOf time.Time
}

// padAfterWrite computes the pad for a holding as it will stand once the pending
// write lands. pending is the declaration being created or updated, or nil for a
// delete; excludeID drops the row being replaced or removed. The pad is nil only
// when the holding will have no declarations left, which is the one case where it is
// removed rather than rewritten.
func (s *Server) padAfterWrite(ctx context.Context, userID, broker, account, instrumentID, excludeID string, pending *db.HoldingDeclarationRow, startDate time.Time) (holdingPad, error) {
	all, err := s.db.ListHoldingDeclarations(ctx, userID)
	if err != nil {
		return holdingPad{}, err
	}
	decls := holdingDeclarations(all, broker, account, instrumentID, excludeID)
	if pending != nil {
		decls = append(decls, pending)
	}
	pad := padDeclaration(decls)
	if pad == nil {
		return holdingPad{}, nil
	}
	init, err := s.computeInitializeValues(ctx, userID, broker, account, instrumentID, pad.DeclaredQty, pad.AsOfDate, pad.ShareCountBasis, startDate)
	if err != nil {
		return holdingPad{}, err
	}
	return holdingPad{init: &init, asOf: pad.AsOfDate}, nil
}

// kindOf reports whether the declaration at asOfDate is its holding's pad.
func (p holdingPad) kindOf(asOfDate time.Time) apiv1.DeclarationKind {
	if p.init != nil && asOfDate.Equal(p.asOf) {
		return apiv1.DeclarationKind_DECLARATION_KIND_PAD
	}
	return apiv1.DeclarationKind_DECLARATION_KIND_ASSERT
}

// shareCountBasis reads the declared denomination from a request, falling back to
// as_of_date. That is the as-traded assumption -- a quantity read off a record of
// some date is in the share count current then -- and it matches the default the
// holding_declarations trigger applies. See docs/spec/bitemporality.md.
func shareCountBasis(d *date.Date, asOfDate time.Time) time.Time {
	if d == nil {
		return asOfDate
	}
	return dateToTime(d)
}

// CreateHoldingDeclaration creates a declaration and computes the INITIALIZE tx.
func (s *Server) CreateHoldingDeclaration(ctx context.Context, req *apiv1.CreateHoldingDeclarationRequest) (*apiv1.CreateHoldingDeclarationResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	asOfDate := dateToTime(req.GetAsOfDate())
	basis := shareCountBasis(req.GetShareCountBasis(), asOfDate)
	if _, err := strconv.ParseFloat(req.GetDeclaredQty(), 64); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid declared_qty: %v", err)
	}

	// Precondition: at least one real tx must exist
	startDate, err := s.db.GetPortfolioStartDate(ctx, u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if startDate == nil {
		return nil, status.Error(codes.FailedPrecondition, "no real transactions exist; upload transactions before declaring opening balances")
	}
	// Precondition: as_of_date >= portfolio start date
	startDay := startDate.Truncate(24 * time.Hour)
	if asOfDate.Before(startDay) {
		return nil, status.Errorf(codes.InvalidArgument, "as_of_date must be on or after the portfolio start date (%s)", startDay.Format("2006-01-02"))
	}

	// The new declaration joins whatever the holding already has, and the earliest of
	// the set is the pad -- so a declaration earlier than the current pad takes over
	// from it, and a later one is an assertion that generates nothing.
	pending := &db.HoldingDeclarationRow{DeclaredQty: req.GetDeclaredQty(), AsOfDate: asOfDate, ShareCountBasis: basis}
	pad, err := s.padAfterWrite(ctx, u.ID, req.GetBroker(), req.GetAccount(), req.GetInstrumentId(), "", pending, *startDate)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "compute INITIALIZE tx: %v", err)
	}

	row, err := s.db.CreateDeclarationWithInitializeTx(ctx, u.ID, req.GetBroker(), req.GetAccount(), req.GetInstrumentId(), req.GetDeclaredQty(), asOfDate, basis, *pad.init)
	if err != nil {
		if errors.Is(err, db.ErrDuplicate) {
			return nil, status.Error(codes.AlreadyExists, "a declaration for this holding already exists at that date")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	row.Kind = pad.kindOf(row.AsOfDate)

	return &apiv1.CreateHoldingDeclarationResponse{Declaration: declarationToProto(row)}, nil
}

// UpdateHoldingDeclaration updates a declaration and recomputes the INITIALIZE tx.
func (s *Server) UpdateHoldingDeclaration(ctx context.Context, req *apiv1.UpdateHoldingDeclarationRequest) (*apiv1.UpdateHoldingDeclarationResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	asOfDate := dateToTime(req.GetAsOfDate())
	if _, err := strconv.ParseFloat(req.GetDeclaredQty(), 64); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid declared_qty: %v", err)
	}

	// Verify ownership
	existing, err := s.db.GetHoldingDeclaration(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "declaration not found")
	}
	if existing.UserID != u.ID {
		return nil, status.Error(codes.NotFound, "declaration not found")
	}

	startDate, err := s.db.GetPortfolioStartDate(ctx, u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if startDate == nil {
		return nil, status.Error(codes.FailedPrecondition, "no real transactions exist")
	}
	startDay := startDate.Truncate(24 * time.Hour)
	if asOfDate.Before(startDay) {
		return nil, status.Errorf(codes.InvalidArgument, "as_of_date must be on or after the portfolio start date (%s)", startDay.Format("2006-01-02"))
	}

	// An update that says nothing about denomination keeps the one the declaration
	// already has, rather than re-deriving it from a moved as_of_date: restating the
	// units of a quantity the user did not touch would silently change what they said.
	basis := shareCountBasis(req.GetShareCountBasis(), existing.ShareCountBasis)

	// Moving as_of_date can hand the pad to a sibling or take it from one, so the pad
	// is recomputed from the holding as it will stand, not from this row alone.
	pending := &db.HoldingDeclarationRow{DeclaredQty: req.GetDeclaredQty(), AsOfDate: asOfDate, ShareCountBasis: basis}
	pad, err := s.padAfterWrite(ctx, u.ID, existing.Broker, existing.Account, existing.InstrumentID, existing.ID, pending, *startDate)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "compute INITIALIZE tx: %v", err)
	}

	row, err := s.db.UpdateDeclarationWithInitializeTx(ctx, req.GetId(), req.GetDeclaredQty(), asOfDate, basis, u.ID, existing.Broker, existing.Account, existing.InstrumentID, *pad.init)
	if err != nil {
		if errors.Is(err, db.ErrDuplicate) {
			return nil, status.Error(codes.AlreadyExists, "a declaration for this holding already exists at that date")
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	row.Kind = pad.kindOf(row.AsOfDate)

	return &apiv1.UpdateHoldingDeclarationResponse{Declaration: declarationToProto(row)}, nil
}

// DeleteHoldingDeclaration deletes a declaration and its INITIALIZE tx.
func (s *Server) DeleteHoldingDeclaration(ctx context.Context, req *apiv1.DeleteHoldingDeclarationRequest) (*apiv1.DeleteHoldingDeclarationResponse, error) {
	u, authErr := auth.RequireUser(ctx)
	if authErr != nil {
		return nil, authErr
	}
	if req.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	existing, err := s.db.GetHoldingDeclaration(ctx, req.GetId())
	if err != nil {
		return nil, status.Error(codes.NotFound, "declaration not found")
	}
	if existing.UserID != u.ID {
		return nil, status.Error(codes.NotFound, "declaration not found")
	}

	// Deleting an assertion leaves the pad alone; deleting the pad promotes the
	// next-earliest declaration to take its place. Only the last declaration for a
	// holding takes the pad with it, and a portfolio with no transactions left has no
	// start date to anchor one to.
	var pad holdingPad
	startDate, err := s.db.GetPortfolioStartDate(ctx, u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if startDate != nil {
		pad, err = s.padAfterWrite(ctx, u.ID, existing.Broker, existing.Account, existing.InstrumentID, existing.ID, nil, *startDate)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "compute INITIALIZE tx: %v", err)
		}
	}

	if err := s.db.DeleteDeclarationWithInitializeTx(ctx, req.GetId(), u.ID, existing.Broker, existing.Account, existing.InstrumentID, pad.init); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.DeleteHoldingDeclarationResponse{}, nil
}

// computeInitializeValues computes the pad that makes declaredQty true at asOfDate.
// basis is the declaration's denomination: the running balance is converted into it
// and the pad is written in it, so the subtraction has both sides in one share count.
func (s *Server) computeInitializeValues(ctx context.Context, userID, broker, account, instrumentID, declaredQtyStr string, asOfDate, basis time.Time, startDate time.Time) (db.InitializeTx, error) {
	// declared_qty is a decimal string on the wire and a NUMERIC column, so it
	// never has to become a float. The subtraction below is exact, which is what
	// makes the resulting pad reconcile to the declaration rather than to within
	// a rounding of it.
	declaredQty, err := decimal.NewFromString(declaredQtyStr)
	if err != nil {
		return db.InitializeTx{}, fmt.Errorf("parse declared_qty: %w", err)
	}
	startDay := startDate.Truncate(24 * time.Hour)
	dayAfterAsOf := asOfDate.AddDate(0, 0, 1)
	runningBalance, err := s.db.ComputeRunningBalance(ctx, userID, broker, account, instrumentID, startDay, dayAfterAsOf, basis)
	if err != nil {
		return db.InitializeTx{}, fmt.Errorf("compute running balance: %w", err)
	}
	initQty := declaredQty.Sub(runningBalance)
	var assetClass string
	inst, err := s.db.GetInstrument(ctx, instrumentID)
	if err == nil && inst != nil && inst.AssetClass != nil {
		assetClass = *inst.AssetClass
	}
	return db.InitializeTx{
		TxType:          txTypeForAssetClass(assetClass, initQty),
		Timestamp:       startDay,
		Quantity:        initQty,
		ShareCountBasis: basis,
	}, nil
}
