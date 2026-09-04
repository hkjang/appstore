package httpapi

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// Every catalog field except the description is a single-line label on the app
// card. The request body alone allows two megabytes, so without a bound here a
// mistaken paste is stored whole and then rendered on every shelf that lists
// the app. The limits match the maxLength the submission form already applies.
const (
	maxIconRunes       = 16
	maxGradientRunes   = 200
	maxServiceURLRunes = 2048
	maxLabelRunes      = 60
	maxVersionRunes    = 40
	maxTeamRunes       = 120
)

func ValidateAppInput(input *model.AppInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.Summary = strings.TrimSpace(input.Summary)
	input.Description = strings.TrimSpace(input.Description)
	input.Icon = strings.TrimSpace(input.Icon)
	input.Gradient = strings.TrimSpace(input.Gradient)
	input.ServiceURL = strings.TrimSpace(input.ServiceURL)
	input.Language = strings.TrimSpace(input.Language)
	input.Framework = strings.TrimSpace(input.Framework)
	input.Team = strings.TrimSpace(input.Team)
	input.Version = strings.TrimSpace(input.Version)
	input.Visibility = strings.ToLower(strings.TrimSpace(input.Visibility))
	if input.Visibility == "" {
		input.Visibility = "public"
	}
	details := map[string]any{}
	if len([]rune(input.Name)) < 2 || len([]rune(input.Name)) > 120 {
		details["name"] = "2~120자로 입력하세요."
	}
	if !slugPattern.MatchString(input.Slug) || len(input.Slug) > 100 {
		details["slug"] = "영문 소문자, 숫자, 하이픈으로 100자 이내로 입력하세요."
	}
	if len([]rune(input.Summary)) < 2 || len([]rune(input.Summary)) > 240 {
		details["summary"] = "2~240자로 입력하세요."
	}
	if len([]rune(input.Description)) < 2 || len([]rune(input.Description)) > 20000 {
		details["description"] = "2~20,000자로 입력하세요."
	} else if strings.ContainsRune(input.Description, 0) {
		// PostgreSQL text columns cannot hold a NUL byte, so a JSON escape
		// for it would fail on INSERT and surface as an opaque 500.
		details["description"] = "제어 문자는 사용할 수 없습니다."
	}
	if !validHTTPURL(input.ServiceURL) {
		details["serviceUrl"] = "유효한 HTTP(S) 서비스 URL을 입력하세요."
	}
	if _, err := uuid.Parse(input.CategoryID); err != nil {
		details["categoryId"] = "유효한 카테고리를 선택하세요."
	}
	if input.Visibility != "public" && input.Visibility != "private" {
		details["visibility"] = "public 또는 private만 사용할 수 있습니다."
	}
	if len(input.Tags) > 30 {
		details["tags"] = "태그는 최대 30개입니다."
	}
	if len(input.Screenshots) > 12 {
		details["screenshots"] = "스크린샷은 최대 12개입니다."
	}
	for _, field := range []struct {
		key     string
		value   string
		maximum int
	}{
		{"name", input.Name, 120},
		{"summary", input.Summary, 240},
		{"icon", input.Icon, maxIconRunes},
		{"gradient", input.Gradient, maxGradientRunes},
		{"serviceUrl", input.ServiceURL, maxServiceURLRunes},
		{"language", input.Language, maxLabelRunes},
		{"framework", input.Framework, maxLabelRunes},
		{"team", input.Team, maxTeamRunes},
		{"version", input.Version, maxVersionRunes},
	} {
		if _, reported := details[field.key]; reported {
			continue
		}
		if len([]rune(field.value)) > field.maximum {
			details[field.key] = fmt.Sprintf("%d자 이내로 입력하세요.", field.maximum)
		} else if !singleLine(field.value) {
			details[field.key] = "줄바꿈이나 제어 문자는 사용할 수 없습니다."
		}
	}
	if len(details) > 0 {
		return Validation("입력값을 확인해 주세요.", details)
	}
	input.Tags = cleanStrings(input.Tags, 60)
	input.Screenshots = cleanStrings(input.Screenshots, 2048)
	return nil
}

func validHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Host != "" && parsed.User == nil && parsed.Fragment == "" && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

// singleLine reports whether a value fits a one-line catalog field. A tab or
// newline survives storage and then breaks the app card, the audit log line and
// the CSV export, and a NUL byte cannot be stored in a PostgreSQL text column
// at all.
func singleLine(value string) bool {
	return !strings.ContainsFunc(value, unicode.IsControl)
}

func cleanStrings(values []string, maxLength int) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len([]rune(value)) > maxLength || seen[value] || !singleLine(value) {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
