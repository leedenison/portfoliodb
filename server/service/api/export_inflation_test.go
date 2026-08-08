package api

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"go.uber.org/mock/gomock"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	dbpkg "github.com/leedenison/portfoliodb/server/db"
)

func inflationRow(currency string, year, month, baseYear int, value string) dbpkg.InflationIndex {
	return dbpkg.InflationIndex{
		Currency:     currency,
		Month:        time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC),
		IndexValue:   decimal.RequireFromString(value),
		BaseYear:     baseYear,
		DataProvider: "ons",
	}
}

// One group per currency, and the months stay in the order the query returned
// them.
func TestExportSystemArchive_Inflation_GroupsByCurrency(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListInflationIndicesForExport(gomock.Any()).Return([]dbpkg.InflationIndex{
		inflationRow("GBP", 2024, 1, 2015, "131.5"),
		inflationRow("GBP", 2024, 2, 2015, "132.1"),
		inflationRow("USD", 2024, 1, 1984, "308.4"),
	}, nil)

	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_INFLATION_INDICES},
	}, stream); err != nil {
		t.Fatalf("ExportSystemArchive: %v", err)
	}

	groups := stream.inflationGroups()
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(groups))
	}
	if groups[0].GetCurrency() != "GBP" || len(groups[0].GetRows()) != 2 {
		t.Fatalf("first group = %s with %d rows", groups[0].GetCurrency(), len(groups[0].GetRows()))
	}
	if groups[1].GetCurrency() != "USD" || len(groups[1].GetRows()) != 1 {
		t.Fatalf("second group = %s with %d rows", groups[1].GetCurrency(), len(groups[1].GetRows()))
	}
	first := groups[0].GetRows()[0]
	if first.GetMonth() != "2024-01-01" {
		t.Fatalf("month = %q, want the first of the month", first.GetMonth())
	}
	if first.GetIndexValue() != "131.5" {
		t.Fatalf("index_value = %q, want the decimal string", first.GetIndexValue())
	}
	if first.GetBaseYear() != 2015 {
		t.Fatalf("base_year = %d", first.GetBaseYear())
	}
}

// base_year is on the row, so a series rebased partway through carries both
// bases in the one group. Read against the wrong base the value is not an error
// but a wrong number.
func TestExportSystemArchive_Inflation_RebasedSeriesKeepsBothBases(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListInflationIndicesForExport(gomock.Any()).Return([]dbpkg.InflationIndex{
		inflationRow("GBP", 2015, 6, 2005, "258.9"),
		inflationRow("GBP", 2024, 1, 2015, "131.5"),
	}, nil)

	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_INFLATION_INDICES},
	}, stream); err != nil {
		t.Fatalf("ExportSystemArchive: %v", err)
	}

	groups := stream.inflationGroups()
	if len(groups) != 1 {
		t.Fatalf("expected 1 group, got %d", len(groups))
	}
	rows := groups[0].GetRows()
	if len(rows) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(rows))
	}
	if rows[0].GetBaseYear() != 2005 || rows[1].GetBaseYear() != 2015 {
		t.Fatalf("base years = %d, %d", rows[0].GetBaseYear(), rows[1].GetBaseYear())
	}
}

// A currency that was exported and holds nothing is not a case the format can
// state: an empty part is the only way to say it, because a series with no rows
// is a series nobody has ever fetched.
func TestExportSystemArchive_Inflation_EmptyIsAnEmptyPart(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().ListInflationIndicesForExport(gomock.Any()).Return(nil, nil)

	stream := &exportArchiveStreamMock{ctx: adminCtx("user-1", "sub|1")}
	if err := srv.ExportSystemArchive(&apiv1.ExportSystemArchiveRequest{
		Parts: []archivev1.ArchivePart{archivev1.ArchivePart_INFLATION_INDICES},
	}, stream); err != nil {
		t.Fatalf("ExportSystemArchive: %v", err)
	}
	want := []string{"envelope", "begin:INFLATION_INDICES"}
	if got := stream.shape(); !equalStrings(got, want) {
		t.Fatalf("stream = %v, want %v", got, want)
	}
}
