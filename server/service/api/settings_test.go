package api

import (
	"testing"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestListSettings_AdminOnly(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	if _, err := srv.ListSettings(authCtx("user-1", "sub|1"), &apiv1.ListSettingsRequest{}); status.Code(err) != codes.PermissionDenied {
		t.Errorf("a regular user got %v, want PermissionDenied", err)
	}
}

func TestSetSetting_AdminOnly(t *testing.T) {
	srv, _ := newAPIServerWithMock(t)
	req := &apiv1.SetSettingRequest{Key: db.SettingPromotionThreshold, Value: "2"}
	if _, err := srv.SetSetting(authCtx("user-1", "sub|1"), req); status.Code(err) != codes.PermissionDenied {
		t.Errorf("a regular user got %v, want PermissionDenied", err)
	}
}

func TestSetSetting_Stores(t *testing.T) {
	srv, mdb := newAPIServerWithMock(t)
	mdb.EXPECT().SetSetting(gomock.Any(), db.SettingPromotionThreshold, "2").Return(nil)

	resp, err := srv.SetSetting(adminCtx("admin-1", "sub|a"), &apiv1.SetSettingRequest{
		Key: db.SettingPromotionThreshold, Value: "2",
	})
	if err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if resp.GetSetting().GetValue() != "2" {
		t.Errorf("stored value = %q, want 2", resp.GetSetting().GetValue())
	}
}

// A key the server does not read, and a value the reader would reject, are both
// refused here rather than written. A row nothing reads answers nothing, and a
// threshold the sweep will refuse is better refused while an admin is looking at
// it than at three in the morning inside a cycle.
func TestSetSetting_RefusesWhatNothingCouldUse(t *testing.T) {
	for _, tc := range []struct{ name, key, value string }{
		{"an unknown key", "not_a_setting", "1"},
		{"a threshold below one", db.SettingPromotionThreshold, "0"},
		{"a threshold that is not a number", db.SettingPromotionThreshold, "many"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newAPIServerWithMock(t)
			_, err := srv.SetSetting(adminCtx("admin-1", "sub|a"), &apiv1.SetSettingRequest{Key: tc.key, Value: tc.value})
			if status.Code(err) != codes.InvalidArgument {
				t.Errorf("got %v, want InvalidArgument", err)
			}
		})
	}
}
