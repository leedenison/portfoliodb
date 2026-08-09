package archive_test

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/leedenison/portfoliodb/server/archive"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
)

func exportedAt() *timestamppb.Timestamp {
	return timestamppb.New(time.Date(2026, 7, 30, 0, 0, 0, 0, time.UTC))
}

// systemFixture is the document server/archive/testdata/system.json holds. It
// exercises one of each level: an envelope, a group with coverage, and rows
// with a present zero, an absent optional and a declared share count basis.
func systemFixture() *archivev1.SystemArchive {
	return &archivev1.SystemArchive{
		Envelope: &archivev1.Envelope{
			ExportedAt:     exportedAt(),
			SourceInstance: "portfoliodb.example.com",
		},
		Instruments: &archivev1.InstrumentPart{
			Instruments: []*archivev1.Instrument{{
				AssetClass:  typev1.AssetClass_STOCK,
				Currency:    "USD",
				ExchangeMic: proto.String("XNAS"),
				Identifiers: []*archivev1.Identifier{{
					Type:      typev1.IdentifierType_MIC_TICKER,
					Value:     "AAPL",
					Domain:    "XNAS",
					Canonical: true,
				}},
			}},
		},
		Prices: &archivev1.PricePart{
			Groups: []*archivev1.PriceGroup{{
				Instrument: &archivev1.InstrumentRef{
					Type:   typev1.IdentifierType_MIC_TICKER,
					Value:  "AAPL",
					Domain: "XNAS",
				},
				AssetClass: typev1.AssetClass_STOCK,
				Currency:   "USD",
				Coverage:   []*archivev1.DateInterval{{From: "2024-01-01", Before: "2024-01-17"}},
				Rows: []*archivev1.PriceRow{{
					PriceDate: "2024-01-15",
					Close:     "185.9",
					Volume:    proto.Int64(48088700),
				}, {
					// A back-adjusted bar, which is the only kind that states a
					// basis. Its presence beside an as-traded bar in one group is
					// what the field being on the row rather than the group buys.
					PriceDate:       "2024-01-16",
					ShareCountBasis: proto.String("2024-06-10"),
					Close:           "18.59",
				}},
			}},
		},
		InflationIndices: &archivev1.InflationPart{
			Groups: []*archivev1.InflationGroup{{
				Currency: "GBP",
				Rows: []*archivev1.InflationRow{{
					Month:      "2024-01-01",
					IndexValue: "131.5",
					BaseYear:   2015,
				}},
			}},
		},
		UnhandledEvents: &archivev1.UnhandledEventPart{
			Groups: []*archivev1.UnhandledEventGroup{{
				Instrument: &archivev1.InstrumentRef{
					Type:   typev1.IdentifierType_MIC_TICKER,
					Value:  "AAPL",
					Domain: "XNAS",
				},
				Events: []*archivev1.UnhandledEvent{{
					EventType: "REVERSE_SPLIT",
					ExDate:    proto.String("2025-04-11"),
					Detail:    "1:10 reverse split affects 3 options",
					Resolved:  true,
				}},
			}},
		},
		FetchBlocks: &archivev1.FetchBlockPart{
			Groups: []*archivev1.FetchBlockGroup{{
				Instrument: &archivev1.InstrumentRef{
					Type:   typev1.IdentifierType_MIC_TICKER,
					Value:  "AAPL",
					Domain: "XNAS",
				},
				Blocks: []*archivev1.FetchBlock{{
					Category:       typev1.PluginCategory_PRICE,
					PluginId:       "eodhd",
					Reason:         "404 from provider",
					FirstBlockedAt: exportedAt(),
				}},
			}},
		},
		PluginConfig: &archivev1.PluginConfigPart{
			Configs: []*archivev1.PluginConfig{{
				PluginId:   "eodhd",
				Category:   typev1.PluginCategory_PRICE,
				Enabled:    true,
				Precedence: 20,
				ConfigJson: proto.String(`{"eodhd_api_key":"secret"}`),
			}},
		},
	}
}

func userFixture() *archivev1.UserArchive {
	return &archivev1.UserArchive{
		Envelope: &archivev1.Envelope{
			ExportedAt:     exportedAt(),
			SourceInstance: "portfoliodb.example.com",
		},
		Preferences: &archivev1.PreferencePart{
			DisplayCurrency: proto.String("GBP"),
			IgnoredAssetClasses: &archivev1.IgnoredAssetClasses{
				Rules: []*archivev1.IgnoredAssetClassRule{{
					Broker:     typev1.Broker_IBKR,
					Account:    "U123",
					AssetClass: typev1.AssetClass_OPTION,
				}},
			},
		},
		Txs: &archivev1.TxPart{
			Windows: []*archivev1.TxWindow{{
				Broker:       typev1.Broker_IBKR,
				PeriodFrom:   timestamppb.New(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
				PeriodBefore: timestamppb.New(time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)),
				Source:       "IBKR:web:ibkr-ofx",
				Groups: []*archivev1.TxGroup{{
					Postings: []*archivev1.Posting{{
						Timestamp:             timestamppb.New(time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)),
						Account:               "U123",
						AccountType:           typev1.AccountType_ACCOUNT_TYPE_USER,
						Type:                  typev1.TxType_BUYSTOCK,
						InstrumentDescription: "APPLE INC",
						Quantity:              "10",
						UnitPrice:             proto.String("185.9"),
					}},
				}},
			}},
		},
	}
}

// The version and the kind come from the build, so an export cannot produce a
// document that misdescribes itself even if the caller never touches them.
func TestNewEnvelope_StampsVersionAndKind(t *testing.T) {
	e := archive.NewEnvelope("portfoliodb.example.com", archivev1.ArchiveKind_SYSTEM)
	if e.GetFormatVersion() != archive.FormatVersion {
		t.Errorf("format_version = %d, want %d", e.GetFormatVersion(), archive.FormatVersion)
	}
	if e.GetKind() != archivev1.ArchiveKind_SYSTEM {
		t.Errorf("kind = %s, want SYSTEM", e.GetKind())
	}
	if e.GetSourceInstance() != "portfoliodb.example.com" {
		t.Errorf("source_instance = %q", e.GetSourceInstance())
	}
	if e.GetExportedAt() == nil {
		t.Error("expected exported_at to be stamped")
	}
}

// The keys are snake_case and the enums are names, because that is what makes a
// file written by one version readable by another. A regression here is a
// wholesale format change, so it is asserted on the bytes rather than through a
// round trip, which would pass either way.
func TestMarshalSystem_WritesProtoNamesAndEnumNames(t *testing.T) {
	b, err := archive.MarshalSystem(systemFixture())
	if err != nil {
		t.Fatalf("MarshalSystem: %v", err)
	}
	s := string(b)
	for _, want := range []string{
		`"format_version":1`,
		`"exported_at":"2026-07-30T00:00:00Z"`,
		`"kind":"SYSTEM"`,
		`"asset_class":"STOCK"`,
		`"type":"MIC_TICKER"`,
		`"price_date":"2024-01-15"`,
		`"volume":"48088700"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %s in %s", want, s)
		}
	}
	for _, unwanted := range []string{"formatVersion", "assetClass", "ASSET_CLASS_STOCK", `"asset_class":1`} {
		if strings.Contains(s, unwanted) {
			t.Errorf("unexpected %s in %s", unwanted, s)
		}
	}
}

// An absent optional and a zero-valued one are different things, and the format
// is only useful if it keeps them apart: an option expiring worthless has a
// declared price of zero, and a posting with no price cannot be converted at all.
func TestMarshalUser_AbsentIsNotZero(t *testing.T) {
	withZero := userFixture()
	withZero.Txs.Windows[0].Groups[0].Postings[0].UnitPrice = proto.String("0")
	b, err := archive.MarshalUser(withZero)
	if err != nil {
		t.Fatalf("MarshalUser: %v", err)
	}
	if !strings.Contains(string(b), `"unit_price":"0"`) {
		t.Errorf("a declared zero price must be written: %s", b)
	}

	absent := userFixture()
	absent.Txs.Windows[0].Groups[0].Postings[0].UnitPrice = nil
	b, err = archive.MarshalUser(absent)
	if err != nil {
		t.Fatalf("MarshalUser: %v", err)
	}
	if strings.Contains(string(b), "unit_price") {
		t.Errorf("an absent price must not be written: %s", b)
	}
}

func TestRoundTrip_System(t *testing.T) {
	want := systemFixture()
	b, err := archive.MarshalSystem(want)
	if err != nil {
		t.Fatalf("MarshalSystem: %v", err)
	}
	got, err := archive.UnmarshalSystem(b)
	if err != nil {
		t.Fatalf("UnmarshalSystem: %v", err)
	}
	// Marshal stamps these, so the fixture does not set them.
	want.Envelope.FormatVersion = archive.FormatVersion
	want.Envelope.Kind = archivev1.ArchiveKind_SYSTEM
	if !proto.Equal(want, got) {
		t.Errorf("round trip changed the document:\n want %v\n got  %v", want, got)
	}
}

func TestRoundTrip_User(t *testing.T) {
	want := userFixture()
	b, err := archive.MarshalUser(want)
	if err != nil {
		t.Fatalf("MarshalUser: %v", err)
	}
	got, err := archive.UnmarshalUser(b)
	if err != nil {
		t.Fatalf("UnmarshalUser: %v", err)
	}
	want.Envelope.FormatVersion = archive.FormatVersion
	want.Envelope.Kind = archivev1.ArchiveKind_USER
	if !proto.Equal(want, got) {
		t.Errorf("round trip changed the document:\n want %v\n got  %v", want, got)
	}
}

// Marshal stamps the envelope, so a caller cannot produce a document that
// misdescribes its own version or kind.
func TestMarshal_StampsEnvelopeOverCaller(t *testing.T) {
	a := systemFixture()
	a.Envelope.FormatVersion = 99
	a.Envelope.Kind = archivev1.ArchiveKind_USER
	b, err := archive.MarshalSystem(a)
	if err != nil {
		t.Fatalf("MarshalSystem: %v", err)
	}
	got, err := archive.UnmarshalSystem(b)
	if err != nil {
		t.Fatalf("UnmarshalSystem: %v", err)
	}
	if got.Envelope.FormatVersion != archive.FormatVersion {
		t.Errorf("format_version = %d, want %d", got.Envelope.FormatVersion, archive.FormatVersion)
	}
	if got.Envelope.Kind != archivev1.ArchiveKind_SYSTEM {
		t.Errorf("kind = %v, want SYSTEM", got.Envelope.Kind)
	}
	// The caller's own message is not mutated.
	if a.Envelope.FormatVersion != 99 {
		t.Errorf("marshal mutated the caller's message")
	}
}

// A newer archive is refused by version rather than by a confusing parse error,
// which is the only thing format_version is for.
func TestUnmarshal_RefusesNewerFormatVersion(t *testing.T) {
	b := []byte(`{"envelope":{"format_version":2,"exported_at":"2026-07-30T00:00:00Z","kind":"SYSTEM"}}`)
	_, err := archive.UnmarshalSystem(b)
	var ve *archive.VersionError
	if !errors.As(err, &ve) {
		t.Fatalf("expected a VersionError, got %v", err)
	}
	if ve.Got != 2 || ve.Supports != archive.FormatVersion {
		t.Errorf("got %+v", ve)
	}
}

// An older archive stays readable; the version gate is one-sided.
func TestUnmarshal_AcceptsOwnFormatVersion(t *testing.T) {
	b, err := archive.MarshalSystem(systemFixture())
	if err != nil {
		t.Fatalf("MarshalSystem: %v", err)
	}
	if _, err := archive.UnmarshalSystem(b); err != nil {
		t.Fatalf("UnmarshalSystem: %v", err)
	}
}

func TestUnmarshal_RefusesWrongKind(t *testing.T) {
	b, err := archive.MarshalUser(userFixture())
	if err != nil {
		t.Fatalf("MarshalUser: %v", err)
	}
	_, err = archive.UnmarshalSystem(b)
	var ke *archive.KindError
	if !errors.As(err, &ke) {
		t.Fatalf("expected a KindError, got %v", err)
	}
	if ke.Got != archivev1.ArchiveKind_USER {
		t.Errorf("got %+v", ke)
	}
}

// An additive field from a later writer does not make the file unreadable. This
// is what makes the version gate safe to be one-sided.
func TestUnmarshal_IgnoresUnknownFields(t *testing.T) {
	b, err := archive.MarshalSystem(systemFixture())
	if err != nil {
		t.Fatalf("MarshalSystem: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	doc["portfolios"] = []any{map[string]any{"name": "a part this build has never heard of"}}
	doc["envelope"].(map[string]any)["written_by"] = "a later portfoliodb"
	b, err = json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if _, err := archive.UnmarshalSystem(b); err != nil {
		t.Fatalf("UnmarshalSystem with unknown fields: %v", err)
	}
}

// A value outside the vocabulary is refused whatever the unknown-field policy,
// because it is not representable anywhere in PortfolioDB and not merely here.
func TestUnmarshal_RefusesUnknownEnumValue(t *testing.T) {
	b := []byte(`{"envelope":{"format_version":1,"exported_at":"2026-07-30T00:00:00Z","kind":"USER"},` +
		`"txs":{"windows":[{"broker":"REVOLUT","period_from":"2024-01-01T00:00:00Z",` +
		`"period_before":"2024-02-01T00:00:00Z","source":"x:y:z"}]}}`)
	if _, err := archive.UnmarshalUser(b); err == nil {
		t.Fatal("expected an unknown broker to be refused")
	}
}

func TestUnmarshal_RefusesInvalidDocument(t *testing.T) {
	// before must be after from, and the interval is half-open.
	b := []byte(`{"envelope":{"format_version":1,"exported_at":"2026-07-30T00:00:00Z","kind":"SYSTEM"},` +
		`"prices":{"groups":[{"instrument":{"type":"ISIN","value":"US0378331005"},` +
		`"coverage":[{"from":"2024-02-01","before":"2024-01-01"}]}]}}`)
	if _, err := archive.UnmarshalSystem(b); err == nil {
		t.Fatal("expected an inverted coverage interval to be refused")
	}
}

// Two exports of the same data are byte-identical. protojson deliberately jitters
// its whitespace, so this is a property of the codec rather than of the library.
func TestMarshal_IsDeterministic(t *testing.T) {
	a, err := archive.MarshalSystem(systemFixture())
	if err != nil {
		t.Fatalf("MarshalSystem: %v", err)
	}
	for range 20 {
		b, err := archive.MarshalSystem(systemFixture())
		if err != nil {
			t.Fatalf("MarshalSystem: %v", err)
		}
		if string(a) != string(b) {
			t.Fatalf("two marshals of the same document differ:\n%s\n%s", a, b)
		}
	}
}

// The golden files are what the TypeScript codec is checked against, so that a
// document written by one runtime is provably the same bytes as the other's.
// Refresh with: go test ./server/archive -update
func TestGoldenFiles(t *testing.T) {
	for _, tc := range []struct {
		path string
		got  func() ([]byte, error)
	}{
		{"testdata/system.json", func() ([]byte, error) { return archive.MarshalSystem(systemFixture()) }},
		{"testdata/user.json", func() ([]byte, error) { return archive.MarshalUser(userFixture()) }},
	} {
		b, err := tc.got()
		if err != nil {
			t.Fatalf("marshal %s: %v", tc.path, err)
		}
		if *update {
			if err := os.WriteFile(tc.path, append(b, '\n'), 0o644); err != nil {
				t.Fatalf("write %s: %v", tc.path, err)
			}
			continue
		}
		want, err := os.ReadFile(tc.path)
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		if strings.TrimRight(string(want), "\n") != string(b) {
			t.Errorf("%s is stale:\n want %s\n got  %s", tc.path, want, b)
		}
	}
}

// A part present but empty means the export included it and there was nothing;
// a part absent means it was not included at all. The whole export menu rests on
// those being distinguishable, so the codec has to keep them apart.
func TestRoundTrip_PresentButEmptyPartIsNotAbsent(t *testing.T) {
	b, err := archive.MarshalSystem(&archivev1.SystemArchive{
		Envelope: archive.NewEnvelope("portfoliodb.example.com", archivev1.ArchiveKind_SYSTEM),
		Prices:   &archivev1.PricePart{},
	})
	if err != nil {
		t.Fatalf("MarshalSystem: %v", err)
	}
	if !strings.Contains(string(b), `"prices":{}`) {
		t.Fatalf("an empty part must still be written: %s", b)
	}
	if strings.Contains(string(b), `"instruments"`) {
		t.Fatalf("an unselected part must not be written: %s", b)
	}
	got, err := archive.UnmarshalSystem(b)
	if err != nil {
		t.Fatalf("UnmarshalSystem: %v", err)
	}
	if got.GetPrices() == nil {
		t.Error("a present but empty price part came back absent")
	}
	if got.GetInstruments() != nil {
		t.Error("an absent instrument part came back present")
	}
}
