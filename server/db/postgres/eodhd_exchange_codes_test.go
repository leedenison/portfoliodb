package postgres

import (
	"context"
	"testing"
)

func TestLookupMICsByEODHDCode(t *testing.T) {
	db := testDBTx(t)
	ctx := context.Background()

	// The eodhd_exchange_codes table is populated by plugin migrations,
	// which run in TestMain via migrate.UpPlugin.
	mics, err := db.LookupMICsByEODHDCode(ctx, "US")
	if err != nil {
		t.Fatalf("LookupMICsByEODHDCode: %v", err)
	}
	if len(mics) == 0 {
		t.Fatal("expected MICs for code US")
	}
	// "US" maps to "XNAS, XNYS, OTCM"
	want := map[string]bool{"XNAS": true, "XNYS": true, "OTCM": true}
	for _, mic := range mics {
		if !want[mic] {
			t.Errorf("unexpected MIC %q for code US", mic)
		}
		delete(want, mic)
	}
	for mic := range want {
		t.Errorf("missing MIC %q for code US", mic)
	}
}

func TestLookupMICsByEODHDCode_NotFound(t *testing.T) {
	db := testDBTx(t)
	ctx := context.Background()

	_, err := db.LookupMICsByEODHDCode(ctx, "NONEXISTENT")
	if err == nil {
		t.Fatal("expected error for non-existent code")
	}
}

func TestLookupMICsByEODHDCode_NullMIC(t *testing.T) {
	db := testDBTx(t)
	ctx := context.Background()

	// GBOND has null operating_mic
	mics, err := db.LookupMICsByEODHDCode(ctx, "GBOND")
	if err != nil {
		t.Fatalf("LookupMICsByEODHDCode: %v", err)
	}
	if len(mics) != 0 {
		t.Errorf("expected nil mics for null operating_mic, got %v", mics)
	}
}
