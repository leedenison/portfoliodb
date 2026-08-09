package api

import (
	"context"
	"testing"

	"github.com/shopspring/decimal"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/archive"
	dbpkg "github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
)

func systemArchive(mutate func(*archivev1.SystemArchive)) *apiv1.ImportSystemArchiveRequest {
	a := &archivev1.SystemArchive{
		Envelope: archive.NewEnvelope("portfoliodb.example.com", archivev1.ArchiveKind_SYSTEM),
	}
	if mutate != nil {
		mutate(a)
	}
	return &apiv1.ImportSystemArchiveRequest{Archive: a, Filename: "system-archive.json"}
}

func TestImportSystemArchive_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	req := systemArchive(func(a *archivev1.SystemArchive) {
		a.Instruments = &archivev1.InstrumentPart{}
	})
	_, err := srv.ImportSystemArchive(authCtx("user-1", "sub|1"), req)
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

func TestImportSystemArchive_NoParts_ReturnsError(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.ImportSystemArchive(adminCtx("user-1", "sub|1"), systemArchive(nil))
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

func TestImportSystemArchive_NewerFormatVersion_Refused(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	req := systemArchive(func(a *archivev1.SystemArchive) {
		a.Instruments = &archivev1.InstrumentPart{}
		a.Envelope.FormatVersion = archive.FormatVersion + 1
	})
	// The request is well formed and this server is the thing that is out of
	// date, so this is a precondition rather than a bad argument.
	_, err := srv.ImportSystemArchive(adminCtx("user-1", "sub|1"), req)
	testutil.RequireGRPCCode(t, err, codes.FailedPrecondition)
}

func TestImportSystemArchive_UserArchive_Refused(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	req := systemArchive(func(a *archivev1.SystemArchive) {
		a.Instruments = &archivev1.InstrumentPart{}
		a.Envelope.Kind = archivev1.ArchiveKind_USER
	})
	_, err := srv.ImportSystemArchive(adminCtx("user-1", "sub|1"), req)
	testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
}

// The job records the parts the document carried, in restore order, so a caller
// polling immediately sees what the import will apply.
func TestImportSystemArchive_QueuesJobWithPresentParts(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	req := systemArchive(func(a *archivev1.SystemArchive) {
		a.CorporateEvents = &archivev1.CorporateEventPart{}
		a.Instruments = &archivev1.InstrumentPart{}
	})
	var enqueuedType string
	srv.enqueueJob = func(_, jobType string) error {
		enqueuedType = jobType
		return nil
	}
	var got dbpkg.CreateJobParams
	mockDB.EXPECT().CreateJob(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params dbpkg.CreateJobParams) (string, error) {
			got = params
			return "job-1", nil
		})
	resp, err := srv.ImportSystemArchive(adminCtx("user-1", "sub|1"), req)
	if err != nil {
		t.Fatalf("ImportSystemArchive: %v", err)
	}
	if resp.GetJobId() != "job-1" {
		t.Fatalf("job_id = %q", resp.GetJobId())
	}
	if got.JobType != dbpkg.JobTypeSystemArchive || got.Filename != "system-archive.json" {
		t.Fatalf("job = %q %q", got.JobType, got.Filename)
	}
	if enqueuedType != dbpkg.JobTypeSystemArchive {
		t.Fatalf("enqueued as %q", enqueuedType)
	}
	want := []archivev1.ArchivePart{archivev1.ArchivePart_INSTRUMENTS, archivev1.ArchivePart_CORPORATE_EVENTS}
	if len(got.Parts) != len(want) {
		t.Fatalf("parts = %v, want %v", got.Parts, want)
	}
	for i := range want {
		if got.Parts[i] != want[i] {
			t.Fatalf("parts = %v, want %v", got.Parts, want)
		}
	}
	if len(got.Payload) == 0 {
		t.Fatal("job carries no payload")
	}
}

// A part present but empty says the export included it and there was nothing.
// That is a different statement from a part never included, so it is applied.
func TestImportSystemArchive_EmptyPartIsStillApplied(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	req := systemArchive(func(a *archivev1.SystemArchive) {
		a.Prices = &archivev1.PricePart{}
	})
	srv.enqueueJob = func(_, _ string) error { return nil }
	var got dbpkg.CreateJobParams
	mockDB.EXPECT().CreateJob(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, params dbpkg.CreateJobParams) (string, error) {
			got = params
			return "job-2", nil
		})
	if _, err := srv.ImportSystemArchive(adminCtx("user-1", "sub|1"), req); err != nil {
		t.Fatalf("ImportSystemArchive: %v", err)
	}
	if len(got.Parts) != 1 || got.Parts[0] != archivev1.ArchivePart_PRICES {
		t.Fatalf("parts = %v, want [PRICES]", got.Parts)
	}
}

// exportArchiveStreamMock captures a whole system archive export.
type exportArchiveStreamMock struct {
	ctx  context.Context
	sent []*apiv1.ExportSystemArchiveResponse
}

func (e *exportArchiveStreamMock) Context() context.Context    { return e.ctx }
func (e *exportArchiveStreamMock) RecvMsg(m interface{}) error { return nil }
func (e *exportArchiveStreamMock) Send(m *apiv1.ExportSystemArchiveResponse) error {
	e.sent = append(e.sent, m)
	return nil
}
func (e *exportArchiveStreamMock) SendHeader(m metadata.MD) error { return nil }
func (e *exportArchiveStreamMock) SetHeader(m metadata.MD) error  { return nil }
func (e *exportArchiveStreamMock) SetTrailer(m metadata.MD)       {}
func (e *exportArchiveStreamMock) SendMsg(m interface{}) error {
	if r, ok := m.(*apiv1.ExportSystemArchiveResponse); ok {
		e.sent = append(e.sent, r)
	}
	return nil
}

// instruments returns the instruments the export streamed.
func (e *exportArchiveStreamMock) instruments() []*archivev1.Instrument {
	var out []*archivev1.Instrument
	for _, m := range e.sent {
		if v := m.GetInstrument(); v != nil {
			out = append(out, v)
		}
	}
	return out
}

// groups returns the price groups the export streamed.
func (e *exportArchiveStreamMock) groups() []*archivev1.PriceGroup {
	var out []*archivev1.PriceGroup
	for _, m := range e.sent {
		if v := m.GetPriceGroup(); v != nil {
			out = append(out, v)
		}
	}
	return out
}

// eventGroups returns the corporate event groups the export streamed.
func (e *exportArchiveStreamMock) eventGroups() []*archivev1.CorporateEventGroup {
	var out []*archivev1.CorporateEventGroup
	for _, m := range e.sent {
		if v := m.GetCorporateEventGroup(); v != nil {
			out = append(out, v)
		}
	}
	return out
}

// inflationGroups returns the inflation groups the export streamed.
func (e *exportArchiveStreamMock) inflationGroups() []*archivev1.InflationGroup {
	var out []*archivev1.InflationGroup
	for _, m := range e.sent {
		if v := m.GetInflationGroup(); v != nil {
			out = append(out, v)
		}
	}
	return out
}

// shape describes the stream as a sequence of item kinds, which is what the
// client's reassembly reads.
func (e *exportArchiveStreamMock) shape() []string {
	var out []string
	for _, m := range e.sent {
		switch {
		case m.GetEnvelope() != nil:
			out = append(out, "envelope")
		case m.GetPartBegin() != nil:
			out = append(out, "begin:"+m.GetPartBegin().GetPart().String())
		case m.GetInstrument() != nil:
			out = append(out, "instrument")
		case m.GetPriceGroup() != nil:
			out = append(out, "price_group")
		case m.GetCorporateEventGroup() != nil:
			out = append(out, "corporate_event_group")
		case m.GetInflationGroup() != nil:
			out = append(out, "inflation_group")
		}
	}
	return out
}

func TestExportSystemArchive_NonAdmin_PermissionDenied(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	stream := &exportArchiveStreamMock{ctx: authCtx("user-1", "sub|1")}
	err := srv.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_INSTRUMENTS},
	}, stream)
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}

// A selected part that holds nothing still announces itself, because a part
// present and empty is a different statement from a part never selected.
func TestExportSystemArchive_EmptyPartStillAnnouncesItself(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListInstrumentsForExport(gomock.Any(), "", []string(nil)).Return(nil, nil)
	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_INSTRUMENTS},
	}, stream); err != nil {
		t.Fatalf("ExportSystemArchive: %v", err)
	}
	want := []string{"envelope", "begin:INSTRUMENTS"}
	if got := stream.shape(); !equalStrings(got, want) {
		t.Fatalf("stream = %v, want %v", got, want)
	}
}

// The parts come out in restore order whatever order they were asked for in,
// and each part's items follow its own marker.
func TestExportSystemArchive_PartsInRestoreOrder(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListInstrumentsForExport(gomock.Any(), "", []string(nil)).
		Return([]*dbpkg.InstrumentRow{{ID: "id-1", ContractMultiplier: decimal.NewFromInt(1)}}, nil)
	mockDB.EXPECT().ListCorporateEventCoverageForExport(gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().ListStockSplitsForExport(gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().ListCashDividendsForExport(gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().ListInflationIndicesForExport(gomock.Any()).Return(nil, nil)

	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	// Asked for out of order, and with a duplicate.
	if err := srv.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{
		Parts: []archivev1.ArchivePart{
			archivev1.ArchivePart_INFLATION_INDICES,
			archivev1.ArchivePart_CORPORATE_EVENTS,
			archivev1.ArchivePart_INSTRUMENTS,
			archivev1.ArchivePart_CORPORATE_EVENTS,
		},
	}, stream); err != nil {
		t.Fatalf("ExportSystemArchive: %v", err)
	}
	want := []string{"envelope", "begin:INSTRUMENTS", "instrument", "begin:CORPORATE_EVENTS", "begin:INFLATION_INDICES"}
	if got := stream.shape(); !equalStrings(got, want) {
		t.Fatalf("stream = %v, want %v", got, want)
	}
}

// A part that was not asked for is absent from the stream entirely -- no marker,
// no items -- which is what makes it absent from the document.
func TestExportSystemArchive_UnselectedPartIsAbsent(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListPriceCoverageForExport(gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().ListPricesForExport(gomock.Any()).Return(nil, nil)
	// Neither instruments nor corporate events are read at all.
	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_PRICES},
	}, stream); err != nil {
		t.Fatalf("ExportSystemArchive: %v", err)
	}
	want := []string{"envelope", "begin:PRICES"}
	if got := stream.shape(); !equalStrings(got, want) {
		t.Fatalf("stream = %v, want %v", got, want)
	}
}

// One clock reading for the whole document: knowledge time that differs between
// a file's own parts is not knowledge time.
func TestExportSystemArchive_SendsExactlyOneEnvelopeFirst(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListInstrumentsForExport(gomock.Any(), "", []string(nil)).Return(nil, nil)
	mockDB.EXPECT().ListPriceCoverageForExport(gomock.Any()).Return(nil, nil)
	mockDB.EXPECT().ListPricesForExport(gomock.Any()).Return(nil, nil)
	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_INSTRUMENTS, archivev1.ArchivePart_PRICES},
	}, stream); err != nil {
		t.Fatalf("ExportSystemArchive: %v", err)
	}
	envelopes := 0
	for _, m := range stream.sent {
		if m.GetEnvelope() != nil {
			envelopes++
		}
	}
	if envelopes != 1 {
		t.Fatalf("got %d envelopes, want exactly 1", envelopes)
	}
	env := stream.sent[0].GetEnvelope()
	if env == nil {
		t.Fatal("the envelope is not the first message")
	}
	if env.GetKind() != archivev1.ArchiveKind_SYSTEM || env.GetFormatVersion() != archive.FormatVersion {
		t.Fatalf("envelope = %v v%d", env.GetKind(), env.GetFormatVersion())
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
