package store

import (
	"errors"
	"strings"
	"testing"

	"github.com/hkjang/appstore/internal/model"
)

func baseSettings() model.SystemSettings {
	return model.SystemSettings{SiteName: "AppStore", Theme: "system", PageSize: 24}
}

func TestValidateSystemSettingsTrimsBannerCopy(t *testing.T) {
	settings := baseSettings()
	settings.HeroTitle = "  팀의 좋은 앱을\n한곳에서 발견하세요.  "
	settings.HeroEyebrow = " 새로운 소식 "
	got, err := validateSystemSettings(settings)
	if err != nil {
		t.Fatalf("validateSystemSettings: %v", err)
	}
	// The line break inside the title is the editor's layout choice and has to
	// survive; only the surrounding whitespace goes.
	if got.HeroTitle != "팀의 좋은 앱을\n한곳에서 발견하세요." {
		t.Fatalf("HeroTitle = %q", got.HeroTitle)
	}
	if got.HeroEyebrow != "새로운 소식" {
		t.Fatalf("HeroEyebrow = %q", got.HeroEyebrow)
	}
}

func TestValidateSystemSettingsKeepsEmptyBannerCopy(t *testing.T) {
	// Empty means "show the shipped default", so it must not be rejected.
	got, err := validateSystemSettings(baseSettings())
	if err != nil {
		t.Fatalf("validateSystemSettings: %v", err)
	}
	if got.HomeCopy != (model.HomeCopy{}) {
		t.Fatalf("HomeCopy = %+v, want zero", got.HomeCopy)
	}
}

func TestValidateSystemSettingsRejectsOverlongBannerCopy(t *testing.T) {
	settings := baseSettings()
	// Counted in runes: 41 Korean characters is over the 40 rune label cap even
	// though it is far more than 40 bytes either way.
	settings.HeroPrimaryLabel = strings.Repeat("가", 41)
	if _, err := validateSystemSettings(settings); !errors.Is(err, ErrInvalid) {
		t.Fatalf("error = %v, want ErrInvalid", err)
	}
	within := baseSettings()
	within.HeroPrimaryLabel = strings.Repeat("가", 40)
	if _, err := validateSystemSettings(within); err != nil {
		t.Fatalf("40 runes rejected: %v", err)
	}
}

func TestAdminAppOverridesRejectRanksOutsideTheScale(t *testing.T) {
	for _, rank := range []int{0, -1, MaxFeaturedRank + 1} {
		value := rank
		_, err := AdminAppOverrides{Status: model.AppStatusDraft, Featured: true, FeaturedRank: &value}.normalize()
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("rank %d: error = %v, want ErrInvalid", rank, err)
		}
	}
	// No rank at all is the normal case: the app simply sorts by recency.
	if _, err := (AdminAppOverrides{Status: model.AppStatusDraft, Featured: true}).normalize(); err != nil {
		t.Fatalf("unranked featured app rejected: %v", err)
	}
}
