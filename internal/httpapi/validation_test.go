package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

func TestValidateAppInput(t *testing.T) {
	input := model.AppInput{
		Name: " Catalog ", Slug: "My-App", Summary: "설명", Description: "상세 설명",
		ServiceURL: "https://apps.internal/catalog", CategoryID: uuid.NewString(), Visibility: "",
	}
	if err := ValidateAppInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.Slug != "my-app" || input.Visibility != "public" || input.Name != "Catalog" {
		t.Fatalf("normalized input = %#v", input)
	}
}

func TestValidateAppInputRejectsRepositoryInsteadOfServiceURL(t *testing.T) {
	input := model.AppInput{Name: "A", Slug: "bad slug", Summary: "", Description: "", ServiceURL: "git@github.com:test/repo", CategoryID: "nope"}
	if err := ValidateAppInput(&input); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateAppInputBoundsTheSingleLineFields(t *testing.T) {
	// The request body allows two megabytes, so an unbounded label field lands
	// in the database whole and then renders on every shelf that lists the app.
	for _, testCase := range []struct {
		field  string
		mutate func(*model.AppInput)
	}{
		{"icon", func(i *model.AppInput) { i.Icon = strings.Repeat("🚀", maxIconRunes+1) }},
		{"gradient", func(i *model.AppInput) { i.Gradient = strings.Repeat("g", maxGradientRunes+1) }},
		{"language", func(i *model.AppInput) { i.Language = strings.Repeat("가", maxLabelRunes+1) }},
		{"framework", func(i *model.AppInput) { i.Framework = strings.Repeat("f", maxLabelRunes+1) }},
		{"team", func(i *model.AppInput) { i.Team = strings.Repeat("t", maxTeamRunes+1) }},
		{"version", func(i *model.AppInput) { i.Version = strings.Repeat("9", maxVersionRunes+1) }},
		{"serviceUrl", func(i *model.AppInput) {
			i.ServiceURL = "https://apps.internal/" + strings.Repeat("p", maxServiceURLRunes)
		}},
		{"name", func(i *model.AppInput) { i.Name = "Cata\nlog" }},
		{"team", func(i *model.AppInput) { i.Team = "플랫폼\t실" }},
		{"description", func(i *model.AppInput) { i.Description = "상세\x00설명" }},
	} {
		input := validAppInput()
		testCase.mutate(&input)
		err := ValidateAppInput(&input)
		apiError, ok := err.(*APIError)
		if !ok {
			t.Fatalf("%s: error = %v, want *APIError", testCase.field, err)
		}
		if _, reported := apiError.Details[testCase.field]; !reported {
			t.Fatalf("%s: details = %v, want the field reported", testCase.field, apiError.Details)
		}
	}
}

func TestValidateAppInputDropsUnusableTagsAndScreenshots(t *testing.T) {
	input := validAppInput()
	input.Tags = []string{" 검색 ", "with\nnewline", "검색", ""}
	input.Screenshots = []string{"https://apps.internal/a.png", "https://apps.internal/\x00.png"}
	if err := ValidateAppInput(&input); err != nil {
		t.Fatal(err)
	}
	if len(input.Tags) != 1 || input.Tags[0] != "검색" {
		t.Fatalf("tags = %#v", input.Tags)
	}
	if len(input.Screenshots) != 1 || input.Screenshots[0] != "https://apps.internal/a.png" {
		t.Fatalf("screenshots = %#v", input.Screenshots)
	}
}

func validAppInput() model.AppInput {
	return model.AppInput{
		Name: "Catalog", Slug: "catalog", Summary: "설명", Description: "상세 설명",
		ServiceURL: "https://apps.internal/catalog", CategoryID: uuid.NewString(),
	}
}

func TestValidateReviewReasonKeepsTheTypedTextAndBoundsIt(t *testing.T) {
	// The reviewer types into a textarea, so a line break belongs to the reason
	// while the surrounding whitespace does not.
	reason, err := ValidateReviewReason("  아이콘을 교체해 주세요.\n서비스 URL도 사내 주소로.  ")
	if err != nil {
		t.Fatal(err)
	}
	if reason != "아이콘을 교체해 주세요.\n서비스 URL도 사내 주소로." {
		t.Fatalf("reason = %q", reason)
	}
	// An empty reason is only rejected by the workflow policy, in the store.
	if reason, err := ValidateReviewReason("   "); err != nil || reason != "" {
		t.Fatalf("reason = %q, err = %v", reason, err)
	}
}

func TestValidateReviewReasonRejectsUnstorableText(t *testing.T) {
	for name, value := range map[string]string{
		"too long":       strings.Repeat("사", maxReasonRunes+1),
		"NUL byte":       "사유\x00",
		"other controls": "사유\x07",
	} {
		_, err := ValidateReviewReason(value)
		apiError, ok := err.(*APIError)
		if !ok {
			t.Fatalf("%s: error = %v, want *APIError", name, err)
		}
		if _, reported := apiError.Details["reason"]; !reported || apiError.Status != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status = %d details = %v", name, apiError.Status, apiError.Details)
		}
	}
	if _, err := ValidateReviewReason(strings.Repeat("사", maxReasonRunes)); err != nil {
		t.Fatalf("the limit itself must stay valid: %v", err)
	}
}

func TestValidateCategoryInputKeepsTheSeededTaxonomyEditable(t *testing.T) {
	// The shipped categories carry underscore slugs and those are live
	// /categories/{slug} URLs, so an edit of one must stay valid.
	input := store.CategoryInput{Slug: " Enterprise_Ops ", Name: " 엔터프라이즈 & 운영 ", Icon: "🏢", Active: true}
	if err := ValidateCategoryInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.Slug != "enterprise_ops" || input.Name != "엔터프라이즈 & 운영" {
		t.Fatalf("normalized input = %#v", input)
	}
}

func TestValidateCategoryInputBoundsWhatTheBrowsePageRenders(t *testing.T) {
	for _, testCase := range []struct {
		field  string
		mutate func(*store.CategoryInput)
	}{
		{"slug", func(i *store.CategoryInput) { i.Slug = "카테고리" }},
		{"slug", func(i *store.CategoryInput) { i.Slug = "ops/../admin" }},
		{"slug", func(i *store.CategoryInput) { i.Slug = strings.Repeat("a", 101) }},
		{"slug", func(i *store.CategoryInput) { i.Slug = "" }},
		{"name", func(i *store.CategoryInput) { i.Name = strings.Repeat("운", maxCategoryNameRunes+1) }},
		{"name", func(i *store.CategoryInput) { i.Name = "운\n영" }},
		{"name", func(i *store.CategoryInput) { i.Name = "" }},
		{"icon", func(i *store.CategoryInput) { i.Icon = strings.Repeat("🏢", maxIconRunes+1) }},
		{"icon", func(i *store.CategoryInput) { i.Icon = "🏢\x00" }},
		{"description", func(i *store.CategoryInput) {
			i.Description = strings.Repeat("설", maxCategoryDescriptionRunes+1)
		}},
		{"description", func(i *store.CategoryInput) { i.Description = "설명\x00" }},
		{"position", func(i *store.CategoryInput) { i.Position = maxCategoryPosition + 1 }},
		{"position", func(i *store.CategoryInput) { i.Position = -1 }},
	} {
		input := validCategoryInput()
		testCase.mutate(&input)
		err := ValidateCategoryInput(&input)
		apiError, ok := err.(*APIError)
		if !ok {
			t.Fatalf("%s: error = %v, want *APIError", testCase.field, err)
		}
		if _, reported := apiError.Details[testCase.field]; !reported || apiError.Status != http.StatusUnprocessableEntity {
			t.Fatalf("%s: status = %d details = %v", testCase.field, apiError.Status, apiError.Details)
		}
	}
	// The limits themselves stay valid, and the description is a textarea so the
	// line breaks the administrator typed belong to it.
	input := validCategoryInput()
	input.Name = strings.Repeat("운", maxCategoryNameRunes)
	input.Description = "운영 도구\n모음"
	input.Position = maxCategoryPosition
	if err := ValidateCategoryInput(&input); err != nil {
		t.Fatalf("the limits themselves must stay valid: %v", err)
	}
}

func validCategoryInput() store.CategoryInput {
	return store.CategoryInput{Slug: "enterprise-ops", Name: "운영", Icon: "🏢", Active: true}
}

func TestNormalizedSortLeavesTheDefaultToTheStore(t *testing.T) {
	// An unset or unknown sort must not become "updated" here: only the store
	// knows that a featured-only list defaults to the editorial order instead.
	for _, value := range []string{"", "  ", "bogus"} {
		if got := normalizedSort(value); got != "" {
			t.Fatalf("normalizedSort(%q) = %q, want empty", value, got)
		}
	}
	for _, value := range []string{"featured", "Featured", " published "} {
		want := strings.ToLower(strings.TrimSpace(value))
		if got := normalizedSort(value); got != want {
			t.Fatalf("normalizedSort(%q) = %q, want %q", value, got, want)
		}
	}
}
