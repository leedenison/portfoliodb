package api

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/testutil"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
)

// The five plugin categories are one shape repeated: list reads ListPluginConfigs
// for its own category and maps the rows out, update writes UpdatePluginConfig for
// its own category and maps the row back. Only price was tested, and the other four
// were the copies -- which is how they came to have no coverage at all.
//
// A table rather than five sets of near-identical tests, because the thing worth
// asserting is that they are the same shape, and because a sixth category should be
// one line here rather than another copy. What is particular to a category is the
// constant it passes, which is exactly what these check.
//
// pluginView is what the five response types have in common, so one set of
// assertions can read all of them.
type pluginView struct {
	id         string
	enabled    bool
	precedence int32
	configJSON string
	display    string
}

// pluginOpts is the optional half of an update request. Every field is a pointer
// on the wire because leaving one out means "do not change it", which is not the
// same as setting it to zero, and it is the half a copied method can drop without
// failing to compile.
type pluginOpts struct {
	enabledV    *bool
	precedenceV *int32
	configV     *string
}

func (o *pluginOpts) enabled() *bool {
	if o == nil {
		return nil
	}
	return o.enabledV
}

func (o *pluginOpts) precedence() *int32 {
	if o == nil {
		return nil
	}
	return o.precedenceV
}

func (o *pluginOpts) configJSON() *string {
	if o == nil {
		return nil
	}
	return o.configV
}

type pluginCategory struct {
	name     string
	category string
	list     func(*Server, context.Context) ([]pluginView, error)
	update   func(*Server, context.Context, string, *pluginOpts) (pluginView, error)
}

var pluginCategories = []pluginCategory{
	{
		name: "identifier", category: db.PluginCategoryIdentifier,
		list: func(s *Server, ctx context.Context) ([]pluginView, error) {
			r, err := s.ListIdentifierPlugins(ctx, &apiv1.ListIdentifierPluginsRequest{})
			if err != nil {
				return nil, err
			}
			out := make([]pluginView, 0, len(r.GetPlugins()))
			for _, p := range r.GetPlugins() {
				out = append(out, pluginView{p.GetPluginId(), p.GetEnabled(), p.GetPrecedence(), p.GetConfigJson(), p.GetDisplayName()})
			}
			return out, nil
		},
		update: func(s *Server, ctx context.Context, id string, o *pluginOpts) (pluginView, error) {
			r, err := s.UpdateIdentifierPlugin(ctx, &apiv1.UpdateIdentifierPluginRequest{PluginId: id, Enabled: o.enabled(), Precedence: o.precedence(), ConfigJson: o.configJSON()})
			if err != nil {
				return pluginView{}, err
			}
			p := r.GetPlugin()
			return pluginView{p.GetPluginId(), p.GetEnabled(), p.GetPrecedence(), p.GetConfigJson(), p.GetDisplayName()}, nil
		},
	},
	{
		name: "description", category: db.PluginCategoryCandidate,
		list: func(s *Server, ctx context.Context) ([]pluginView, error) {
			r, err := s.ListCandidatePlugins(ctx, &apiv1.ListCandidatePluginsRequest{})
			if err != nil {
				return nil, err
			}
			out := make([]pluginView, 0, len(r.GetPlugins()))
			for _, p := range r.GetPlugins() {
				out = append(out, pluginView{p.GetPluginId(), p.GetEnabled(), p.GetPrecedence(), p.GetConfigJson(), p.GetDisplayName()})
			}
			return out, nil
		},
		update: func(s *Server, ctx context.Context, id string, o *pluginOpts) (pluginView, error) {
			r, err := s.UpdateCandidatePlugin(ctx, &apiv1.UpdateCandidatePluginRequest{PluginId: id, Enabled: o.enabled(), Precedence: o.precedence(), ConfigJson: o.configJSON()})
			if err != nil {
				return pluginView{}, err
			}
			p := r.GetPlugin()
			return pluginView{p.GetPluginId(), p.GetEnabled(), p.GetPrecedence(), p.GetConfigJson(), p.GetDisplayName()}, nil
		},
	},
	{
		name: "price", category: db.PluginCategoryPrice,
		list: func(s *Server, ctx context.Context) ([]pluginView, error) {
			r, err := s.ListPricePlugins(ctx, &apiv1.ListPricePluginsRequest{})
			if err != nil {
				return nil, err
			}
			out := make([]pluginView, 0, len(r.GetPlugins()))
			for _, p := range r.GetPlugins() {
				out = append(out, pluginView{p.GetPluginId(), p.GetEnabled(), p.GetPrecedence(), p.GetConfigJson(), p.GetDisplayName()})
			}
			return out, nil
		},
		update: func(s *Server, ctx context.Context, id string, o *pluginOpts) (pluginView, error) {
			r, err := s.UpdatePricePlugin(ctx, &apiv1.UpdatePricePluginRequest{PluginId: id, Enabled: o.enabled(), Precedence: o.precedence(), ConfigJson: o.configJSON()})
			if err != nil {
				return pluginView{}, err
			}
			p := r.GetPlugin()
			return pluginView{p.GetPluginId(), p.GetEnabled(), p.GetPrecedence(), p.GetConfigJson(), p.GetDisplayName()}, nil
		},
	},
	{
		name: "inflation", category: db.PluginCategoryInflation,
		list: func(s *Server, ctx context.Context) ([]pluginView, error) {
			r, err := s.ListInflationPlugins(ctx, &apiv1.ListInflationPluginsRequest{})
			if err != nil {
				return nil, err
			}
			out := make([]pluginView, 0, len(r.GetPlugins()))
			for _, p := range r.GetPlugins() {
				out = append(out, pluginView{p.GetPluginId(), p.GetEnabled(), p.GetPrecedence(), p.GetConfigJson(), p.GetDisplayName()})
			}
			return out, nil
		},
		update: func(s *Server, ctx context.Context, id string, o *pluginOpts) (pluginView, error) {
			r, err := s.UpdateInflationPlugin(ctx, &apiv1.UpdateInflationPluginRequest{PluginId: id, Enabled: o.enabled(), Precedence: o.precedence(), ConfigJson: o.configJSON()})
			if err != nil {
				return pluginView{}, err
			}
			p := r.GetPlugin()
			return pluginView{p.GetPluginId(), p.GetEnabled(), p.GetPrecedence(), p.GetConfigJson(), p.GetDisplayName()}, nil
		},
	},
	{
		name: "corporate_event", category: db.PluginCategoryCorporateEvent,
		list: func(s *Server, ctx context.Context) ([]pluginView, error) {
			r, err := s.ListCorporateEventPlugins(ctx, &apiv1.ListCorporateEventPluginsRequest{})
			if err != nil {
				return nil, err
			}
			out := make([]pluginView, 0, len(r.GetPlugins()))
			for _, p := range r.GetPlugins() {
				out = append(out, pluginView{p.GetPluginId(), p.GetEnabled(), p.GetPrecedence(), p.GetConfigJson(), p.GetDisplayName()})
			}
			return out, nil
		},
		update: func(s *Server, ctx context.Context, id string, o *pluginOpts) (pluginView, error) {
			r, err := s.UpdateCorporateEventPlugin(ctx, &apiv1.UpdateCorporateEventPluginRequest{PluginId: id, Enabled: o.enabled(), Precedence: o.precedence(), ConfigJson: o.configJSON()})
			if err != nil {
				return pluginView{}, err
			}
			p := r.GetPlugin()
			return pluginView{p.GetPluginId(), p.GetEnabled(), p.GetPrecedence(), p.GetConfigJson(), p.GetDisplayName()}, nil
		},
	},
}

// Each list reads its own category and nobody else's. The expectation names the
// category, so a method that passed a neighbour's constant -- the mistake a set of
// copies invites -- fails on the unexpected call.
func TestListPlugins_ReadsItsOwnCategory(t *testing.T) {
	for _, c := range pluginCategories {
		t.Run(c.name, func(t *testing.T) {
			srv, mockDB := newAPIServerWithMock(t)
			mockDB.EXPECT().ListPluginConfigs(gomock.Any(), c.category).Return([]db.PluginConfigRowFull{
				{PluginID: "one", Enabled: true, Precedence: 10, Config: []byte(`{"key":"val"}`)},
			}, nil)

			got, err := c.list(srv, adminCtx("admin-1", "sub|admin"))
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			want := []pluginView{{id: "one", enabled: true, precedence: 10, configJSON: `{"key":"val"}`, display: "one"}}
			if len(got) != 1 || got[0] != want[0] {
				t.Errorf("plugins: got %+v, want %+v", got, want)
			}
		})
	}
}

// An empty category returns no plugins rather than failing, which is the state an
// instance with a category disabled is in.
func TestListPlugins_EmptyCategory(t *testing.T) {
	for _, c := range pluginCategories {
		t.Run(c.name, func(t *testing.T) {
			srv, mockDB := newAPIServerWithMock(t)
			mockDB.EXPECT().ListPluginConfigs(gomock.Any(), c.category).Return(nil, nil)

			got, err := c.list(srv, adminCtx("admin-1", "sub|admin"))
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("plugins: got %+v, want none", got)
			}
		})
	}
}

// A store error is Internal rather than a partial response.
func TestListPlugins_StoreError(t *testing.T) {
	for _, c := range pluginCategories {
		t.Run(c.name, func(t *testing.T) {
			srv, mockDB := newAPIServerWithMock(t)
			mockDB.EXPECT().ListPluginConfigs(gomock.Any(), c.category).Return(nil, errors.New("boom"))

			_, err := c.list(srv, adminCtx("admin-1", "sub|admin"))
			testutil.RequireGRPCCode(t, err, codes.Internal)
		})
	}
}

// Update writes to its own category too, and hands back the row the store returned
// rather than the request it was given.
func TestUpdatePlugin_WritesItsOwnCategory(t *testing.T) {
	for _, c := range pluginCategories {
		t.Run(c.name, func(t *testing.T) {
			srv, mockDB := newAPIServerWithMock(t)
			mockDB.EXPECT().
				UpdatePluginConfig(gomock.Any(), c.category, "one", nil, nil, nil, gomock.Any()).
				Return(&db.PluginConfigRowFull{PluginID: "one", Enabled: false, Precedence: 20, Config: []byte(`{"k":1}`)}, nil)

			got, err := c.update(srv, adminCtx("admin-1", "sub|admin"), "one", nil)
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			want := pluginView{id: "one", enabled: false, precedence: 20, configJSON: `{"k":1}`, display: "one"}
			if got != want {
				t.Errorf("plugin: got %+v, want %+v", got, want)
			}
		})
	}
}

// A plugin the category does not have is NotFound, not Internal: the store says so
// with ErrNoRows and every category has to translate it.
func TestUpdatePlugin_NotFound(t *testing.T) {
	for _, c := range pluginCategories {
		t.Run(c.name, func(t *testing.T) {
			srv, mockDB := newAPIServerWithMock(t)
			mockDB.EXPECT().
				UpdatePluginConfig(gomock.Any(), c.category, "missing", gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
				Return(nil, sql.ErrNoRows)

			_, err := c.update(srv, adminCtx("admin-1", "sub|admin"), "missing", nil)
			testutil.RequireGRPCCode(t, err, codes.NotFound)
		})
	}
}

// A signed-in user who is not an admin is refused. Distinct from the unauthenticated
// case in server_test.go, which the service descriptor drives: this is the other half
// of the guard, and it is the half a category copied without RequireAdmin would fail.
func TestPlugins_AdminOnly(t *testing.T) {
	for _, c := range pluginCategories {
		t.Run(c.name+"/list", func(t *testing.T) {
			srv, _ := newAPIServerWithMock(t)
			_, err := c.list(srv, authCtx("user-1", "sub|1"))
			testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
		})
		t.Run(c.name+"/update", func(t *testing.T) {
			srv, _ := newAPIServerWithMock(t)
			_, err := c.update(srv, authCtx("user-1", "sub|1"), "one", nil)
			testutil.RequireGRPCCode(t, err, codes.PermissionDenied)
		})
	}
}

// An empty plugin_id is rejected before the store is asked anything.
func TestUpdatePlugin_MissingPluginID(t *testing.T) {
	for _, c := range pluginCategories {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := newAPIServerWithMock(t)
			_, err := c.update(srv, adminCtx("admin-1", "sub|admin"), "", nil)
			testutil.RequireGRPCCode(t, err, codes.InvalidArgument)
		})
	}
}

// Each optional field reaches the store as the value the request carried, and a
// field left out reaches it as nil.
//
// The expectation names all three rather than accepting anything, because these are
// three pointers marshalled by hand in five copies of the same method, and a copy
// that dropped one would still compile and still return a plausible response.
func TestUpdatePlugin_CarriesTheOptionalFields(t *testing.T) {
	for _, c := range pluginCategories {
		t.Run(c.name, func(t *testing.T) {
			srv, mockDB := newAPIServerWithMock(t)
			enabled, precedence, config := true, int32(42), `{"key":"val"}`
			wantEnabled, wantPrecedence := true, 42
			mockDB.EXPECT().
				UpdatePluginConfig(gomock.Any(), c.category, "one", &wantEnabled, &wantPrecedence, []byte(config), gomock.Any()).
				Return(&db.PluginConfigRowFull{PluginID: "one", Enabled: true, Precedence: 42, Config: []byte(config)}, nil)

			got, err := c.update(srv, adminCtx("admin-1", "sub|admin"), "one",
				&pluginOpts{enabledV: &enabled, precedenceV: &precedence, configV: &config})
			if err != nil {
				t.Fatalf("update: %v", err)
			}
			want := pluginView{id: "one", enabled: true, precedence: 42, configJSON: config, display: "one"}
			if got != want {
				t.Errorf("plugin: got %+v, want %+v", got, want)
			}
		})
	}
}
