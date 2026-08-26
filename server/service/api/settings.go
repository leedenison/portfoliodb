package api

import (
	"context"
	"strconv"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/auth"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Instance settings. Admin-only at both ends: these describe the deployment
// rather than the caller, and a regular user has nothing here of their own to
// read. What a user configures about themselves lives on the user row and is
// reached through SetDisplayCurrency.

// settingsWritable is the keys a client may set, and the check that a value is
// usable for each.
//
// A vocabulary rather than a free-form store. The migration seeds every key this
// build reads, so an unrecognised one is a typo that would sit in the table
// answering nothing, and a value the reader will reject later is better refused
// now while somebody is looking at it.
var settingsWritable = map[string]func(string) error{
	db.SettingPromotionThreshold: func(v string) error {
		n, err := strconv.Atoi(v)
		if err != nil {
			return status.Error(codes.InvalidArgument, "promotion_threshold must be a number")
		}
		if n < 1 {
			return status.Error(codes.InvalidArgument, "promotion_threshold must be at least 1")
		}
		return nil
	},
}

// ListSettings returns every instance setting.
func (s *Server) ListSettings(ctx context.Context, _ *apiv1.ListSettingsRequest) (*apiv1.ListSettingsResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	rows, err := s.db.ListSettings(ctx)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	out := make([]*apiv1.Setting, len(rows))
	for i, r := range rows {
		out[i] = &apiv1.Setting{Key: r.Key, Value: r.Value}
	}
	return &apiv1.ListSettingsResponse{Settings: out}, nil
}

// SetSetting writes one instance setting.
func (s *Server) SetSetting(ctx context.Context, req *apiv1.SetSettingRequest) (*apiv1.SetSettingResponse, error) {
	if _, authErr := auth.RequireAdmin(ctx); authErr != nil {
		return nil, authErr
	}
	key, value := req.GetKey(), req.GetValue()
	valid, ok := settingsWritable[key]
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument, "unknown setting %q", key)
	}
	if err := valid(value); err != nil {
		return nil, err
	}
	if err := s.db.SetSetting(ctx, key, value); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &apiv1.SetSettingResponse{Setting: &apiv1.Setting{Key: key, Value: value}}, nil
}
