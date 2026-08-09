package archiveimport

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.uber.org/mock/gomock"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

func inflationPart(groups ...*archivev1.InflationGroup) *archivev1.InflationPart {
	return &archivev1.InflationPart{Groups: groups}
}

func inflationGroup(currency string, rows ...*archivev1.InflationRow) *archivev1.InflationGroup {
	return &archivev1.InflationGroup{Currency: currency, Rows: rows}
}

func TestInflationPart_Success(t *testing.T) {
	database, rep := newPartTest(t)
	asOf := time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC)
	database.EXPECT().
		UpsertInflationIndices(gomock.Any(), gomock.Any(), &asOf).
		DoAndReturn(func(_ context.Context, rows []db.InflationIndex, _ *time.Time) error {
			if len(rows) != 2 {
				t.Errorf("expected 2 rows, got %d", len(rows))
			}
			if rows[0].Currency != "GBP" || rows[0].BaseYear != 2015 {
				t.Errorf("row = %+v", rows[0])
			}
			if !rows[0].Month.Equal(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)) {
				t.Errorf("month = %v", rows[0].Month)
			}
			if rows[0].IndexValue.String() != "131.5" {
				t.Errorf("index_value = %s", rows[0].IndexValue)
			}
			return nil
		})

	part := inflationPart(inflationGroup("GBP",
		&archivev1.InflationRow{Month: "2024-01-01", IndexValue: "131.5", BaseYear: 2015},
		&archivev1.InflationRow{Month: "2024-02-01", IndexValue: "132.1", BaseYear: 2015},
	))
	written, err := InflationPart(context.Background(), database, part, &asOf, rep)
	if err != nil {
		t.Fatalf("InflationPart: %v", err)
	}
	if written != 2 {
		t.Fatalf("written = %d, want 2", written)
	}
	if rep.ErrCount() != 0 {
		t.Fatalf("unexpected problems: %v", rep.Errors())
	}
}

// Every row is stamped with the envelope's knowledge time rather than with now.
// An imported value is only as fresh as the file it came from, and stamping it
// now would tell the fetcher a stale series had just been confirmed.
func TestInflationPart_StampsTheEnvelopeKnowledgeTime(t *testing.T) {
	database, rep := newPartTest(t)
	asOf := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	var got *time.Time
	database.EXPECT().
		UpsertInflationIndices(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ []db.InflationIndex, fetchedAt *time.Time) error {
			got = fetchedAt
			return nil
		})

	part := inflationPart(inflationGroup("GBP",
		&archivev1.InflationRow{Month: "2024-01-01", IndexValue: "131.5", BaseYear: 2015}))
	if _, err := InflationPart(context.Background(), database, part, &asOf, rep); err != nil {
		t.Fatalf("InflationPart: %v", err)
	}
	if got == nil || !got.Equal(asOf) {
		t.Fatalf("fetchedAt = %v, want %v", got, asOf)
	}
}

// Provenance cannot survive a round trip, so every imported row is recorded
// against the import sentinel rather than against whichever plugin first
// fetched it.
func TestInflationPart_RecordsTheImportSentinel(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().
		UpsertInflationIndices(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, rows []db.InflationIndex, _ *time.Time) error {
			if rows[0].DataProvider != db.InflationProviderImport {
				t.Errorf("data_provider = %q", rows[0].DataProvider)
			}
			return nil
		})

	part := inflationPart(inflationGroup("GBP",
		&archivev1.InflationRow{Month: "2024-01-01", IndexValue: "131.5", BaseYear: 2015}))
	if _, err := InflationPart(context.Background(), database, part, nil, rep); err != nil {
		t.Fatalf("InflationPart: %v", err)
	}
}

// A row the file describes badly is rejected on its own. The rest of the
// currency still lands, which is what makes "completed, 1 row rejected" a
// result the page can state.
func TestInflationPart_BadRowDoesNotCostTheGroup(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().
		UpsertInflationIndices(gomock.Any(), gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, rows []db.InflationIndex, _ *time.Time) error {
			if len(rows) != 1 {
				t.Errorf("expected the good row only, got %d", len(rows))
			}
			return nil
		})

	part := inflationPart(inflationGroup("GBP",
		&archivev1.InflationRow{Month: "not-a-date", IndexValue: "131.5", BaseYear: 2015},
		&archivev1.InflationRow{Month: "2024-02-01", IndexValue: "132.1", BaseYear: 2015},
	))
	written, err := InflationPart(context.Background(), database, part, nil, rep)
	if err != nil {
		t.Fatalf("InflationPart: %v", err)
	}
	if written != 1 {
		t.Fatalf("written = %d, want 1", written)
	}
	if rep.ErrCount() != 1 {
		t.Fatalf("problems = %d, want 1", rep.ErrCount())
	}
	if rep.Errors()[0].GetField() != "month" {
		t.Fatalf("field = %q", rep.Errors()[0].GetField())
	}
}

// A group with no currency names no series, so there is nothing to write it to.
func TestInflationPart_GroupWithNoCurrencyIsRejected(t *testing.T) {
	database, rep := newPartTest(t)
	part := inflationPart(inflationGroup("",
		&archivev1.InflationRow{Month: "2024-01-01", IndexValue: "131.5", BaseYear: 2015}))
	written, err := InflationPart(context.Background(), database, part, nil, rep)
	if err != nil {
		t.Fatalf("InflationPart: %v", err)
	}
	if written != 0 {
		t.Fatalf("written = %d, want 0", written)
	}
	if rep.ErrCount() != 1 || rep.Errors()[0].GetField() != "currency" {
		t.Fatalf("problems = %v", rep.Errors())
	}
}

// A write that does not land fails the part. A rejected row is a row; a failed
// write is the part not having happened.
func TestInflationPart_WriteFailureFailsThePart(t *testing.T) {
	database, rep := newPartTest(t)
	database.EXPECT().
		UpsertInflationIndices(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(errors.New("boom"))

	part := inflationPart(inflationGroup("GBP",
		&archivev1.InflationRow{Month: "2024-01-01", IndexValue: "131.5", BaseYear: 2015}))
	if _, err := InflationPart(context.Background(), database, part, nil, rep); err == nil {
		t.Fatal("expected the part to fail")
	}
}

// A part that was selected and holds nothing is not an error: it says the export
// asked and there was nothing.
func TestInflationPart_EmptyPart(t *testing.T) {
	database, rep := newPartTest(t)
	written, err := InflationPart(context.Background(), database, inflationPart(), nil, rep)
	if err != nil || written != 0 {
		t.Fatalf("InflationPart = %d, %v", written, err)
	}
}
