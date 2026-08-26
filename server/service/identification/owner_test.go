package identification

import (
	"context"
	"testing"

	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/db/mock"
	"github.com/leedenison/portfoliodb/server/identifier"
	"go.uber.org/mock/gomock"
)

// One resolution writes facts and claims together, and which is which is decided
// by where each name came from rather than by the call it arrived in. The
// plugin's names are the instance's; the broker's own text for the security
// reached us through whoever uploaded the file and is theirs until other users
// hold it too. See
// docs/adr/0063-identity-claims-are-owned-until-users-corroborate-them.md.
func TestResolveWithPlugins_OnlyTheBrokerDescriptionIsTheUploadersClaim(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "Apple Inc.", Listing: identifier.Listing{Currency: "USD"}},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	})

	const owner = "user-1"
	const source = "IBKR:test:statement"
	const desc = "APPLE INC COM"

	// Every lookup is scoped to the uploader, so a name they already hold
	// answers before the instance's does.
	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), owner, "ISIN", "", "US0378331005").
		Return("", "", nil, nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), owner, "ISIN", "US0378331005").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)

	var written []db.IdentifierInput
	database.EXPECT().EnsureInstrument(
		gomock.Any(), owner, "STOCK", "USD", "Apple Inc.", "", "", gomock.Any(), gomock.Any(), "", nil, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _ *db.OptionFields, _ string) (string, string, error) {
			written = idns
			return "inst-1", "listing-1", nil
		})

	_, err := ResolveWithPlugins(context.Background(), database, registry, owner,
		"IBKR", source, desc,
		identifier.Identity{Stated: []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}}},
		true, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}

	want := map[string]string{"ISIN": "", "BROKER_DESCRIPTION": owner}
	for _, idn := range written {
		w, ok := want[idn.Ref.Type]
		if !ok {
			t.Errorf("unexpected identifier %s", idn.Ref.Type)
			continue
		}
		if idn.Owner != w {
			t.Errorf("%s owner = %q, want %q", idn.Ref.Type, idn.Owner, w)
		}
		delete(want, idn.Ref.Type)
	}
	for typ := range want {
		t.Errorf("%s was not written", typ)
	}
}

// A caller with no user to speak for writes nothing owned and sees the
// instance's facts alone. That is the price fetcher and the archive import, and
// it is what keeps reference data resolving facts rather than claims.
func TestResolveWithPlugins_ACallerWithNoUserWritesNoClaim(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	database := mock.NewMockDB(ctrl)

	registry := identifier.NewRegistry()
	registry.Register("test", &fakePlugin{
		inst: &identifier.Instrument{AssetClass: "STOCK", Name: "Apple Inc.", Listing: identifier.Listing{Currency: "USD"}},
		ids:  []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}},
	})

	database.EXPECT().
		FindInstrumentWithMetaByIdentifier(gomock.Any(), "", "ISIN", "", "US0378331005").
		Return("", "", nil, nil)
	database.EXPECT().
		FindInstrumentByTypeAndValue(gomock.Any(), "", "ISIN", "US0378331005").
		Return("", nil)
	database.EXPECT().
		ListEnabledPluginConfigs(gomock.Any(), db.PluginCategoryIdentifier).
		Return([]db.PluginConfigRow{{PluginID: "test", Precedence: 10}}, nil)

	var written []db.IdentifierInput
	database.EXPECT().EnsureInstrument(
		gomock.Any(), "", "STOCK", "USD", "Apple Inc.", "", "", gomock.Any(), gomock.Any(), "", nil, gomock.Any()).
		DoAndReturn(func(_ context.Context, _, _, _, _, _, _ string, idns []db.IdentifierInput, _ []db.IdentityClaim, _ string, _ *db.OptionFields, _ string) (string, string, error) {
			written = idns
			return "inst-1", "listing-1", nil
		})

	_, err := ResolveWithPlugins(context.Background(), database, registry, "",
		"IBKR", "IBKR:test:statement", "APPLE INC COM",
		identifier.Identity{Stated: []identifier.Identifier{{Type: "ISIN", Value: "US0378331005"}}},
		true, nil, Attempt{}, nil, 0, nil)
	if err != nil {
		t.Fatalf("ResolveWithPlugins: %v", err)
	}
	for _, idn := range written {
		if idn.Owner != "" {
			t.Errorf("%s was written owned by %q, want nobody", idn.Ref.Type, idn.Owner)
		}
	}
}
