package service

import "testing"

func TestNormalizeAgencyRole(t *testing.T) {
	cases := map[string]string{
		"":        AgencyRoleViewer,
		"viewer":  AgencyRoleViewer,
		"manager": AgencyRoleManager,
		"ADMIN":   AgencyRoleAdmin,
		"owner":   AgencyRoleOwner,
	}
	for in, want := range cases {
		got, err := NormalizeAgencyRole(in)
		if err != nil || got != want {
			t.Errorf("NormalizeAgencyRole(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := NormalizeAgencyRole("superuser"); err == nil {
		t.Error("expected invalid role error")
	}
}

func TestCanManageAgency(t *testing.T) {
	for _, role := range []string{AgencyRoleOwner, AgencyRoleAdmin, AgencyRolePlatformAdmin} {
		if !CanManageAgency(role) {
			t.Errorf("%s should manage", role)
		}
	}
	for _, role := range []string{AgencyRoleManager, AgencyRoleViewer} {
		if CanManageAgency(role) {
			t.Errorf("%s should not manage", role)
		}
	}
}

func TestAgencyRangeDays(t *testing.T) {
	cases := map[string]int{"": 7, "7d": 7, "30d": 30, "90d": 90}
	for in, want := range cases {
		days, normalized, ok := agencyRangeDays(in)
		if !ok || days != want || normalized == "" {
			t.Errorf("agencyRangeDays(%q) = (%d,%q,%v)", in, days, normalized, ok)
		}
	}
	if _, _, ok := agencyRangeDays("1y"); ok {
		t.Error("1y should be rejected")
	}
}
