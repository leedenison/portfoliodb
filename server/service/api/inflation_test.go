package api

import (
	"errors"
	"testing"
	"time"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	"github.com/shopspring/decimal"
	"go.uber.org/mock/gomock"
	"google.golang.org/genproto/googleapis/type/date"
	"google.golang.org/grpc/codes"
)

// A month is a date rather than an instant on the wire, and the index keeps its
// full precision: it is a divisor in every real-terms figure, so a value rounded
// here would move them all.
func TestListInflationIndices(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	month := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	mockDB.EXPECT().
		ListInflationIndices(gomock.Any(), "GBP", gomock.Any(), gomock.Any(), 30, "").
		Return([]db.InflationIndex{{
			Currency: "GBP", Month: month,
			IndexValue: decimal.RequireFromString("134.567"), BaseYear: 2015, DataProvider: "ons",
		}}, "next", 1, nil)

	resp, err := srv.ListInflationIndices(adminCtx("admin-1", "sub|admin"), &apiv1.ListInflationIndicesRequest{
		Currency:   "GBP",
		DateFrom:   &date.Date{Year: 2025, Month: 1, Day: 1},
		DateBefore: &date.Date{Year: 2025, Month: 4, Day: 1},
	})
	if err != nil {
		t.Fatalf("ListInflationIndices: %v", err)
	}
	if resp.GetTotalCount() != 1 || resp.GetNextPageToken() != "next" {
		t.Errorf("page: got total=%d token=%q, want 1 and \"next\"", resp.GetTotalCount(), resp.GetNextPageToken())
	}
	if len(resp.GetIndices()) != 1 {
		t.Fatalf("indices: got %d, want 1", len(resp.GetIndices()))
	}
	i := resp.GetIndices()[0]
	if i.GetCurrency() != "GBP" || i.GetMonth() != "2025-03-01" || i.GetIndexValue() != "134.567" ||
		i.GetBaseYear() != 2015 || i.GetDataProvider() != "ons" {
		t.Errorf("index: got %+v", i)
	}
}

// An unset page size is a default, and one larger than the cap is the cap: a page
// is bounded by what the server will serve rather than by what a caller asks for.
func TestListInflationIndices_PageSizeBounds(t *testing.T) {
	tests := []struct {
		name     string
		asked    int32
		expected int
	}{
		{"unset takes the default", 0, 30},
		{"negative takes the default", -1, 30},
		{"under the cap is honoured", 10, 10},
		{"over the cap is capped", 500, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, mockDB := newAPIServerWithMock(t)
			mockDB.EXPECT().
				ListInflationIndices(gomock.Any(), "", gomock.Any(), gomock.Any(), tt.expected, "").
				Return(nil, "", 0, nil)

			_, err := srv.ListInflationIndices(adminCtx("admin-1", "sub|admin"), &apiv1.ListInflationIndicesRequest{PageSize: tt.asked})
			if err != nil {
				t.Fatalf("ListInflationIndices: %v", err)
			}
		})
	}
}

func TestListInflationIndices_StoreError(t *testing.T) {
	srv, mockDB := newAPIServerWithMock(t)
	mockDB.EXPECT().
		ListInflationIndices(gomock.Any(), "", gomock.Any(), gomock.Any(), 30, "").
		Return(nil, "", 0, errors.New("boom"))

	_, err := srv.ListInflationIndices(adminCtx("admin-1", "sub|admin"), &apiv1.ListInflationIndicesRequest{})
	testutil.RequireGRPCCode(t, err, codes.Internal)
}

func TestListInflationIndices_AdminOnly(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	_, err := srv.ListInflationIndices(authCtx("user-1", "sub|1"), &apiv1.ListInflationIndicesRequest{})
	testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
}
