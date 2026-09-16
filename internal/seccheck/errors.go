// Package seccheck is a read-only client for the SecCheck security review
// service. It knows nothing about AppStore's database or registration flow.
package seccheck

import (
	"errors"
	"fmt"
)

type Reason string

const (
	ReasonInvalidConfig   Reason = "invalid_config"
	ReasonInvalidReviewID Reason = "invalid_review_id"
	ReasonUnauthorized    Reason = "unauthorized"
	ReasonForbidden       Reason = "forbidden"
	ReasonNotFound        Reason = "not_found"
	ReasonRateLimited     Reason = "rate_limited"
	ReasonRedirect        Reason = "redirect"
	ReasonUnavailable     Reason = "unavailable"
	ReasonTimeout         Reason = "timeout"
	ReasonTooLarge        Reason = "response_too_large"
	ReasonInvalidResponse Reason = "invalid_response"
	ReasonBindingMismatch Reason = "binding_mismatch"
	ReasonServiceMismatch Reason = "service_mismatch"
	ReasonNotApproved     Reason = "not_approved"
	ReasonIncomplete      Reason = "incomplete"
)

// messages are what an owner or an administrator reads, so each one says what
// to do next rather than which HTTP status came back.
var messages = map[Reason]string{
	ReasonInvalidConfig:   "SecCheck 연동 설정이 올바르지 않습니다. 관리자에게 문의하세요.",
	ReasonInvalidReviewID: "심의 ID 형식이 올바르지 않습니다. SecCheck 심의 주소의 ID를 그대로 입력하세요.",
	ReasonUnauthorized:    "SecCheck가 API Key를 거부했습니다. 관리자에게 문의하세요.",
	ReasonForbidden:       "등록된 SecCheck API Key에 심의 조회 권한이 없습니다. 관리자에게 문의하세요.",
	ReasonNotFound:        "SecCheck에서 해당 심의를 찾을 수 없습니다. 심의 ID를 확인하세요.",
	ReasonRateLimited:     "SecCheck 요청이 제한되었습니다. 잠시 후 다시 시도하세요.",
	ReasonRedirect:        "SecCheck 주소가 다른 곳으로 이동시킵니다. 관리자에게 문의하세요.",
	ReasonUnavailable:     "SecCheck에 연결하지 못했습니다. 잠시 후 다시 시도하세요.",
	ReasonTimeout:         "SecCheck 응답이 제한 시간을 넘었습니다. 잠시 후 다시 시도하세요.",
	ReasonTooLarge:        "SecCheck 응답이 너무 큽니다. 관리자에게 문의하세요.",
	ReasonInvalidResponse: "SecCheck 응답을 해석하지 못했습니다. 관리자에게 문의하세요.",
	ReasonBindingMismatch: "심의 설명에 이 앱의 연동 정보가 없습니다. 앱 화면의 연동 정보를 심의 설명에 그대로 붙여 넣으세요.",
	ReasonServiceMismatch: "심의의 서비스명이 앱 이름과 다릅니다. 같은 이름으로 심의를 등록하세요.",
	ReasonNotApproved:     "아직 최종 승인된 심의가 아닙니다. SecCheck에서 승인이 끝난 뒤 다시 확인하세요.",
	ReasonIncomplete:      "심의에 아직 처리되지 않은 항목이 남아 있습니다.",
}

type Error struct {
	Reason Reason
	// Detail is for the server log, never for the reader.
	Detail string
}

func (e *Error) Error() string {
	if message, ok := messages[e.Reason]; ok {
		return message
	}
	return "SecCheck 결과를 확인하지 못했습니다."
}

func (e *Error) LogValue() string { return fmt.Sprintf("%s: %s", e.Reason, e.Detail) }

func failure(reason Reason) error { return &Error{Reason: reason} }

func detailed(reason Reason, detail string) error { return &Error{Reason: reason, Detail: detail} }

// Code reports the reason behind an error, or an empty reason when it did not
// come from this package.
func Code(err error) Reason {
	var failure *Error
	if errors.As(err, &failure) {
		return failure.Reason
	}
	return ""
}
