package httpapi

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func ValidateAppInput(input *model.AppInput) error {
	input.Name = strings.TrimSpace(input.Name)
	input.Slug = strings.ToLower(strings.TrimSpace(input.Slug))
	input.Summary = strings.TrimSpace(input.Summary)
	input.Description = strings.TrimSpace(input.Description)
	input.ServiceURL = strings.TrimSpace(input.ServiceURL)
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

func cleanStrings(values []string, maxLength int) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || len([]rune(value)) > maxLength || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}
