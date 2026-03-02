package api

import (
	"context"
	"time"

	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Server implements ApiService.
type Server struct {
	apiv1.UnimplementedApiServiceServer
	db db.DB
}

// NewServer returns a new API server.
func NewServer(database db.DB) *Server {
	return &Server{db: database}
}

// ListPortfolios returns portfolios owned by the authenticated user.
func (s *Server) ListPortfolios(ctx context.Context, req *apiv1.ListPortfoliosRequest) (*apiv1.ListPortfoliosResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}
	portfolios, nextToken, err := s.db.ListPortfolios(ctx, u.ID, pageSize, req.GetPageToken())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.ListPortfoliosResponse{Portfolios: portfolios, NextPageToken: nextToken}, nil
}

// GetPortfolio returns a portfolio by ID if owned by the user.
func (s *Server) GetPortfolio(ctx context.Context, req *apiv1.GetPortfolioRequest) (*apiv1.GetPortfolioResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if req.GetPortfolioId() == "" {
		return nil, status.Error(codes.InvalidArgument, "portfolio_id required")
	}
	ok, err := s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "portfolio not found")
	}
	portfolio, _, err := s.db.GetPortfolio(ctx, req.GetPortfolioId())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if portfolio == nil {
		return nil, status.Error(codes.NotFound, "portfolio not found")
	}
	return &apiv1.GetPortfolioResponse{Portfolio: portfolio}, nil
}

// CreatePortfolio creates a portfolio for the authenticated user.
func (s *Server) CreatePortfolio(ctx context.Context, req *apiv1.CreatePortfolioRequest) (*apiv1.CreatePortfolioResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name required")
	}
	portfolio, err := s.db.CreatePortfolio(ctx, u.ID, req.GetName())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.CreatePortfolioResponse{Portfolio: portfolio}, nil
}

// UpdatePortfolio updates a portfolio name if owned by the user.
func (s *Server) UpdatePortfolio(ctx context.Context, req *apiv1.UpdatePortfolioRequest) (*apiv1.UpdatePortfolioResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if req.GetPortfolioId() == "" {
		return nil, status.Error(codes.InvalidArgument, "portfolio_id required")
	}
	ok, err := s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "portfolio not found")
	}
	portfolio, err := s.db.UpdatePortfolio(ctx, req.GetPortfolioId(), req.GetName())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if portfolio == nil {
		return nil, status.Error(codes.NotFound, "portfolio not found")
	}
	return &apiv1.UpdatePortfolioResponse{Portfolio: portfolio}, nil
}

// DeletePortfolio deletes a portfolio if owned by the user.
func (s *Server) DeletePortfolio(ctx context.Context, req *apiv1.DeletePortfolioRequest) (*apiv1.DeletePortfolioResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if req.GetPortfolioId() == "" {
		return nil, status.Error(codes.InvalidArgument, "portfolio_id required")
	}
	ok, err := s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "portfolio not found")
	}
	if err := s.db.DeletePortfolio(ctx, req.GetPortfolioId()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.DeletePortfolioResponse{}, nil
}

// GetPortfolioFilters returns the filter rows for a portfolio (broker/account/instrument). Portfolio must be owned by user.
func (s *Server) GetPortfolioFilters(ctx context.Context, req *apiv1.GetPortfolioFiltersRequest) (*apiv1.GetPortfolioFiltersResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if req.GetPortfolioId() == "" {
		return nil, status.Error(codes.InvalidArgument, "portfolio_id required")
	}
	ok, err := s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "portfolio not found")
	}
	filters, err := s.db.ListPortfolioFilters(ctx, req.GetPortfolioId())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	protos := make([]*apiv1.PortfolioFilterProto, 0, len(filters))
	for _, f := range filters {
		protos = append(protos, &apiv1.PortfolioFilterProto{FilterType: f.FilterType, FilterValue: f.FilterValue})
	}
	return &apiv1.GetPortfolioFiltersResponse{Filters: protos}, nil
}

// SetPortfolioFilters replaces all filters for a portfolio. Portfolio must be owned by user.
func (s *Server) SetPortfolioFilters(ctx context.Context, req *apiv1.SetPortfolioFiltersRequest) (*apiv1.SetPortfolioFiltersResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if req.GetPortfolioId() == "" {
		return nil, status.Error(codes.InvalidArgument, "portfolio_id required")
	}
	ok, err := s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if !ok {
		return nil, status.Error(codes.NotFound, "portfolio not found")
	}
	filters := make([]db.PortfolioFilter, 0, len(req.GetFilters()))
	for _, f := range req.GetFilters() {
		if f.GetFilterType() != "" {
			filters = append(filters, db.PortfolioFilter{FilterType: f.GetFilterType(), FilterValue: f.GetFilterValue()})
		}
	}
	if err := s.db.SetPortfolioFilters(ctx, req.GetPortfolioId(), filters); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.SetPortfolioFiltersResponse{}, nil
}

// ListTxs lists transactions: by portfolio view (if portfolio_id set) or all user transactions. Filtering is via portfolios only.
func (s *Server) ListTxs(ctx context.Context, req *apiv1.ListTxsRequest) (*apiv1.ListTxsResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = 50
	}
	if pageSize > 100 {
		pageSize = 100
	}
	var txs []*apiv1.PortfolioTx
	var nextToken string
	var err error
	if req.GetPortfolioId() != "" {
		ok, err := s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if !ok {
			return nil, status.Error(codes.NotFound, "portfolio not found")
		}
		txs, nextToken, err = s.db.ListTxsByPortfolio(ctx, req.GetPortfolioId(), req.PeriodFrom, req.PeriodTo, pageSize, req.GetPageToken())
	} else {
		txs, nextToken, err = s.db.ListTxs(ctx, u.ID, nil, "", req.PeriodFrom, req.PeriodTo, pageSize, req.GetPageToken())
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// Enrich with Instrument when instrument_id is set
	underlyingIDs := make(map[string]struct{})
	for _, pt := range txs {
		if pt.GetTx().GetInstrumentId() != "" {
			inst, err := s.db.GetInstrument(ctx, pt.Tx.InstrumentId)
			if err == nil && inst != nil {
				pt.Instrument = instrumentRowToProto(inst)
				if inst.UnderlyingID != "" {
					underlyingIDs[inst.UnderlyingID] = struct{}{}
				}
			}
		}
	}
	// Single batch query for all underlyings
	if len(underlyingIDs) > 0 {
		ids := make([]string, 0, len(underlyingIDs))
		for id := range underlyingIDs {
			ids = append(ids, id)
		}
		underlyingRows, err := s.db.ListInstrumentsByIDs(ctx, ids)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		underlyingByID := make(map[string]*db.InstrumentRow)
		for _, r := range underlyingRows {
			underlyingByID[r.ID] = r
		}
		for _, pt := range txs {
			if pt.Instrument != nil && pt.Instrument.UnderlyingId != "" {
				if u := underlyingByID[pt.Instrument.UnderlyingId]; u != nil {
					pt.Instrument.Underlying = instrumentRowToProto(u)
				}
			}
		}
	}
	return &apiv1.ListTxsResponse{Txs: txs, NextPageToken: nextToken}, nil
}

// GetHoldings returns holdings: by portfolio view (if portfolio_id set) or all user holdings. Filtering is via portfolios only.
func (s *Server) GetHoldings(ctx context.Context, req *apiv1.GetHoldingsRequest) (*apiv1.GetHoldingsResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	var holdings []*apiv1.Holding
	var asOf *timestamppb.Timestamp
	var err error
	if req.GetPortfolioId() != "" {
		ok, err := s.db.PortfolioBelongsToUser(ctx, req.GetPortfolioId(), u.ID)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		if !ok {
			return nil, status.Error(codes.NotFound, "portfolio not found")
		}
		holdings, asOf, err = s.db.ComputeHoldingsForPortfolio(ctx, req.GetPortfolioId(), req.AsOf)
	} else {
		holdings, asOf, err = s.db.ComputeHoldings(ctx, u.ID, nil, "", req.AsOf)
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	// Enrich with Instrument when instrument_id is set
	underlyingIDs := make(map[string]struct{})
	for _, h := range holdings {
		if h.GetInstrumentId() != "" {
			inst, err := s.db.GetInstrument(ctx, h.InstrumentId)
			if err == nil && inst != nil {
				h.Instrument = instrumentRowToProto(inst)
				if inst.UnderlyingID != "" {
					underlyingIDs[inst.UnderlyingID] = struct{}{}
				}
			}
		}
	}
	// Single batch query for all underlyings
	if len(underlyingIDs) > 0 {
		ids := make([]string, 0, len(underlyingIDs))
		for id := range underlyingIDs {
			ids = append(ids, id)
		}
		underlyingRows, err := s.db.ListInstrumentsByIDs(ctx, ids)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		underlyingByID := make(map[string]*db.InstrumentRow)
		for _, r := range underlyingRows {
			underlyingByID[r.ID] = r
		}
		for _, h := range holdings {
			if h.Instrument != nil && h.Instrument.UnderlyingId != "" {
				if u := underlyingByID[h.Instrument.UnderlyingId]; u != nil {
					h.Instrument.Underlying = instrumentRowToProto(u)
				}
			}
		}
	}
	return &apiv1.GetHoldingsResponse{Holdings: holdings, AsOf: asOf}, nil
}

// ExportInstruments streams instruments that have at least one canonical identifier. Optional exchange filter. Admin only.
func (s *Server) ExportInstruments(req *apiv1.ExportInstrumentsRequest, stream apiv1.ApiService_ExportInstrumentsServer) error {
	ctx := stream.Context()
	if auth.FromContext(ctx) == nil || auth.FromContext(ctx).ID == "" {
		return status.Error(codes.Unauthenticated, "missing user")
	}
	if auth.FromContext(ctx).Role != "admin" {
		return status.Error(codes.PermissionDenied, "admin role required")
	}
	rows, err := s.db.ListInstrumentsForExport(ctx, req.GetExchange())
	if err != nil {
		return status.Error(codes.Internal, err.Error())
	}
	for _, row := range rows {
		protoInst := instrumentRowToProto(row)
		// For derivatives, include nested underlying with canonical identifiers (no reliance on internal ID for import).
		if row.UnderlyingID != "" {
			underlying, err := s.db.GetInstrument(ctx, row.UnderlyingID)
			if err == nil && underlying != nil {
				protoInst.Underlying = instrumentRowToProto(underlying)
			}
		}
		if err := stream.Send(protoInst); err != nil {
			return err
		}
	}
	return nil
}

// ImportInstruments ensures the given instruments exist (find-or-create by identifiers). Two-pass: underlyings first, then derivatives. Admin only.
func (s *Server) ImportInstruments(ctx context.Context, req *apiv1.ImportInstrumentsRequest) (*apiv1.ImportInstrumentsResponse, error) {
	if auth.FromContext(ctx) == nil || auth.FromContext(ctx).ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if auth.FromContext(ctx).Role != "admin" {
		return nil, status.Error(codes.PermissionDenied, "admin role required")
	}
	var errs []*apiv1.ImportInstrumentError
	seenKeys := make(map[string]struct{})
	var ensuredCount int32
	instruments := req.GetInstruments()

	// Pass 1: ensure all non-derivatives (and ensure nested underlyings from derivatives).
	for i, inst := range instruments {
		ac := inst.GetAssetClass()
		isDerivative := ac == "OPTION" || ac == "FUTURE"
		if isDerivative {
			continue
		}
		if len(inst.GetIdentifiers()) == 0 {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "at least one identifier required"})
			continue
		}
		dup := false
		for _, idf := range inst.GetIdentifiers() {
			key := idf.GetType() + "\x00" + idf.GetValue()
			if _, ok := seenKeys[key]; ok {
				errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "duplicate (type, value) in payload"})
				dup = true
				break
			}
			seenKeys[key] = struct{}{}
		}
		if dup {
			continue
		}
		idns := make([]db.IdentifierInput, 0, len(inst.GetIdentifiers()))
		for _, idf := range inst.GetIdentifiers() {
			idns = append(idns, db.IdentifierInput{Type: idf.GetType(), Value: idf.GetValue(), Canonical: idf.GetCanonical()})
		}
		_, err := s.db.EnsureInstrument(ctx, inst.GetAssetClass(), inst.GetExchange(), inst.GetCurrency(), inst.GetName(), idns, "", protoValidFrom(inst.GetValidFrom()), protoValidTo(inst.GetValidTo()))
		if err != nil {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: err.Error()})
			continue
		}
		ensuredCount++
	}

	// For derivatives we need to ensure nested underlyings first (in pass 1 we skipped derivatives; ensure their underlyings here if not already in payload).
	underlyingIDByIndex := make(map[int32]string) // index -> resolved underlying_id for derivatives
	for i, inst := range instruments {
		ac := inst.GetAssetClass()
		if ac != "OPTION" && ac != "FUTURE" {
			continue
		}
		if inst.GetUnderlying() == nil || len(inst.GetUnderlying().GetIdentifiers()) == 0 {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "OPTION/FUTURE requires nested underlying with at least one identifier"})
			continue
		}
		u := inst.GetUnderlying()
		uIdns := make([]db.IdentifierInput, 0, len(u.GetIdentifiers()))
		for _, idf := range u.GetIdentifiers() {
			uIdns = append(uIdns, db.IdentifierInput{Type: idf.GetType(), Value: idf.GetValue(), Canonical: idf.GetCanonical()})
		}
		underlyingID, err := s.db.EnsureInstrument(ctx, u.GetAssetClass(), u.GetExchange(), u.GetCurrency(), u.GetName(), uIdns, "", protoValidFrom(u.GetValidFrom()), protoValidTo(u.GetValidTo()))
		if err != nil {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "underlying: " + err.Error()})
			continue
		}
		underlyingIDByIndex[int32(i)] = underlyingID
	}

	// Pass 2: ensure derivatives (underlyings already ensured).
	for i, inst := range instruments {
		ac := inst.GetAssetClass()
		if ac != "OPTION" && ac != "FUTURE" {
			continue
		}
		underlyingID, ok := underlyingIDByIndex[int32(i)]
		if !ok {
			continue // already had an error in pass 1
		}
		if len(inst.GetIdentifiers()) == 0 {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "at least one identifier required"})
			continue
		}
		dup := false
		for _, idf := range inst.GetIdentifiers() {
			key := idf.GetType() + "\x00" + idf.GetValue()
			if _, ok := seenKeys[key]; ok {
				errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: "duplicate (type, value) in payload"})
				dup = true
				break
			}
			seenKeys[key] = struct{}{}
		}
		if dup {
			continue
		}
		idns := make([]db.IdentifierInput, 0, len(inst.GetIdentifiers()))
		for _, idf := range inst.GetIdentifiers() {
			idns = append(idns, db.IdentifierInput{Type: idf.GetType(), Value: idf.GetValue(), Canonical: idf.GetCanonical()})
		}
		_, err := s.db.EnsureInstrument(ctx, inst.GetAssetClass(), inst.GetExchange(), inst.GetCurrency(), inst.GetName(), idns, underlyingID, protoValidFrom(inst.GetValidFrom()), protoValidTo(inst.GetValidTo()))
		if err != nil {
			errs = append(errs, &apiv1.ImportInstrumentError{Index: int32(i), Message: err.Error()})
			continue
		}
		ensuredCount++
	}
	return &apiv1.ImportInstrumentsResponse{EnsuredCount: ensuredCount, Errors: errs}, nil
}

// GetJob returns ingestion job status and validation errors; job must belong to user.
func (s *Server) GetJob(ctx context.Context, req *apiv1.GetJobRequest) (*apiv1.GetJobResponse, error) {
	u := auth.FromContext(ctx)
	if u == nil || u.ID == "" {
		return nil, status.Error(codes.Unauthenticated, "missing user")
	}
	if req.GetJobId() == "" {
		return nil, status.Error(codes.InvalidArgument, "job_id required")
	}
	statusVal, errs, idErrs, jobUserID, err := s.db.GetJob(ctx, req.GetJobId())
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if jobUserID == "" {
		return nil, status.Error(codes.NotFound, "job not found")
	}
	if jobUserID != u.ID {
		return nil, status.Error(codes.NotFound, "job not found")
	}
	idErrProtos := make([]*apiv1.IdentificationError, 0, len(idErrs))
	for _, e := range idErrs {
		idErrProtos = append(idErrProtos, &apiv1.IdentificationError{
			RowIndex:               e.RowIndex,
			InstrumentDescription: e.InstrumentDescription,
			Message:                e.Message,
		})
	}
	return &apiv1.GetJobResponse{Status: statusVal, ValidationErrors: errs, IdentificationErrors: idErrProtos}, nil
}

func instrumentRowToProto(row *db.InstrumentRow) *apiv1.Instrument {
	if row == nil {
		return nil
	}
	identifiers := make([]*apiv1.InstrumentIdentifier, 0, len(row.Identifiers))
	for _, idn := range row.Identifiers {
		identifiers = append(identifiers, &apiv1.InstrumentIdentifier{Type: idn.Type, Value: idn.Value, Canonical: idn.Canonical})
	}
	out := &apiv1.Instrument{
		Id:           row.ID,
		AssetClass:   row.AssetClass,
		Exchange:     row.Exchange,
		Currency:     row.Currency,
		Name:         row.Name,
		Identifiers:  identifiers,
		UnderlyingId: row.UnderlyingID,
	}
	if row.ValidFrom != nil {
		out.ValidFrom = timestamppb.New(*row.ValidFrom)
	}
	if row.ValidTo != nil {
		out.ValidTo = timestamppb.New(*row.ValidTo)
	}
	return out
}

// protoValidFrom converts optional proto timestamp to *time.Time for DB.
func protoValidFrom(ts *timestamppb.Timestamp) *time.Time {
	if ts == nil || !ts.IsValid() {
		return nil
	}
	t := ts.AsTime()
	return &t
}

// protoValidTo converts optional proto timestamp to *time.Time for DB.
func protoValidTo(ts *timestamppb.Timestamp) *time.Time {
	return protoValidFrom(ts)
}
