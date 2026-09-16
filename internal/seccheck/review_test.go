package seccheck

import (
	"strings"
	"testing"
	"time"
)

func counts(unreviewed, unverified, stale int) CompletionBlockers {
	return CompletionBlockers{UnreviewedItems: &unreviewed, UnverifiedChanges: &unverified, StaleVerdicts: &stale}
}

const binding = BindingStart + "\napp_id=aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa\nchallenge=bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb\n" + BindingEnd

func approvedReview(description string) *Review {
	approved := time.Now().Add(-time.Hour)
	return &Review{
		ID: "cccccccc-cccc-4ccc-8ccc-cccccccccccc", Number: "SEC-2026-0001",
		ServiceName: "Agent Hub", Description: description, Status: "APPROVED",
		FinalResult: "APPROVED", ApprovedAt: &approved, CompletionBlockers: counts(0, 0, 0),
	}
}

func expected() Expected { return Expected{BindingText: binding, ServiceName: "Agent Hub"} }

func TestVerifyAcceptsAnApprovedReviewCarryingTheBinding(t *testing.T) {
	if err := Verify(approvedReview("보안 심의 요청입니다.\n"+binding+"\n감사합니다."), expected()); err != nil {
		t.Fatalf("a bound approved review was refused: %v", err)
	}
	if err := Verify(approvedReview(binding), expected()); err != nil {
		t.Fatalf("a description that is only the binding was refused: %v", err)
	}
	windows := approvedReview(strings.ReplaceAll("머리말\n"+binding, "\n", "\r\n"))
	if err := Verify(windows, expected()); err != nil {
		t.Fatalf("Windows line endings were refused: %v", err)
	}
}

func TestVerifyRefusesAReviewThatDoesNotCoverThisApp(t *testing.T) {
	cases := []struct {
		name        string
		description string
		reason      Reason
	}{
		{"no binding at all", "보안 심의 요청입니다.", ReasonBindingMismatch},
		{"another app's challenge", strings.Replace(binding, "challenge=bbbbbbbb", "challenge=dddddddd", 1), ReasonBindingMismatch},
		{"the block buried inside a line", "prefix " + binding, ReasonBindingMismatch},
		{"a second block pasted in", binding + "\n" + binding, ReasonBindingMismatch},
		{"only the opening marker", BindingStart + "\napp_id=aaaa", ReasonBindingMismatch},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := Code(Verify(approvedReview(test.description), expected())); got != test.reason {
				t.Fatalf("reason = %q, want %q", got, test.reason)
			}
		})
	}
}

func TestVerifyRefusesEveryNonApproval(t *testing.T) {
	approved := time.Now().Add(-time.Hour)
	cases := []struct {
		name   string
		mutate func(*Review)
		reason Reason
	}{
		{"still being reviewed", func(r *Review) { r.Status = "REVIEWING"; r.FinalResult = "" }, ReasonNotApproved},
		{"waiting for approval", func(r *Review) { r.Status = "APPROVAL_PENDING"; r.FinalResult = "" }, ReasonNotApproved},
		{"approved conditionally", func(r *Review) { r.FinalResult = "CONDITIONAL" }, ReasonNotApproved},
		{"rejected", func(r *Review) { r.Status = "REJECTED"; r.FinalResult = "REJECTED" }, ReasonNotApproved},
		{"closed after approval", func(r *Review) { r.Status = "CLOSED" }, ReasonNotApproved},
		{"a different service", func(r *Review) { r.ServiceName = "Flow Studio" }, ReasonServiceMismatch},
		{"unreviewed items left", func(r *Review) { r.CompletionBlockers = counts(2, 0, 0) }, ReasonIncomplete},
		{"unverified change requests", func(r *Review) { r.CompletionBlockers = counts(0, 1, 0) }, ReasonIncomplete},
		{"verdicts gone stale", func(r *Review) { r.CompletionBlockers = counts(0, 0, 3) }, ReasonIncomplete},
		{"no approval time", func(r *Review) { r.ApprovedAt = nil }, ReasonInvalidResponse},
		{"approved in the future", func(r *Review) {
			later := approved.Add(48 * time.Hour)
			r.ApprovedAt = &later
		}, ReasonInvalidResponse},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			review := approvedReview(binding)
			test.mutate(review)
			if got := Code(Verify(review, expected())); got != test.reason {
				t.Fatalf("reason = %q, want %q", got, test.reason)
			}
		})
	}
}

func TestVerifyRefusesAnEmptyExpectation(t *testing.T) {
	if got := Code(Verify(approvedReview(binding), Expected{})); got != ReasonBindingMismatch {
		t.Fatalf("an empty expectation returned %q", got)
	}
	if got := Code(Verify(nil, expected())); got != ReasonInvalidResponse {
		t.Fatalf("a missing review returned %q", got)
	}
}

func TestParseReviewReadsWhatSecCheckSends(t *testing.T) {
	body := []byte(`{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","review_number":"SEC-2026-0001",
		"service_name":"Agent Hub","description":"설명","status":"APPROVED","final_result":"APPROVED",
		"approved_at":"2026-09-10T02:00:00Z","completion_blockers":{"unreviewed_items":0,"unverified_changes":0,"stale_verdicts":0},
		"progress":{"total":42,"answered":42},"risk_score":0}`)
	review, err := parseReview(body)
	if err != nil {
		t.Fatalf("a real SecCheck payload failed to parse: %v", err)
	}
	if review.Number != "SEC-2026-0001" || review.ServiceName != "Agent Hub" || review.ApprovedAt == nil {
		t.Fatalf("parsed review = %#v", review)
	}
}

func TestParseReviewRefusesIncompletePayloads(t *testing.T) {
	cases := map[string]string{
		"not an object":            `[]`,
		"no identifier":            `{"review_number":"SEC-1","service_name":"A","status":"APPROVED","final_result":"","completion_blockers":{"unreviewed_items":0,"unverified_changes":0,"stale_verdicts":0}}`,
		"unknown status":           `{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","review_number":"SEC-1","service_name":"A","status":"SHIPPED","final_result":"","completion_blockers":{"unreviewed_items":0,"unverified_changes":0,"stale_verdicts":0}}`,
		"unknown result":           `{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","review_number":"SEC-1","service_name":"A","status":"APPROVED","final_result":"PASSED","completion_blockers":{"unreviewed_items":0,"unverified_changes":0,"stale_verdicts":0}}`,
		"no completion blockers":   `{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","review_number":"SEC-1","service_name":"A","status":"APPROVED","final_result":"APPROVED"}`,
		"one blocker unreported":   `{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","review_number":"SEC-1","service_name":"A","status":"APPROVED","final_result":"APPROVED","completion_blockers":{"unreviewed_items":0,"stale_verdicts":0}}`,
		"empty review number":      `{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","review_number":" ","service_name":"A","status":"APPROVED","final_result":"APPROVED","completion_blockers":{"unreviewed_items":0,"unverified_changes":0,"stale_verdicts":0}}`,
		"identifier is not a uuid": `{"id":"1","review_number":"SEC-1","service_name":"A","status":"APPROVED","final_result":"APPROVED","completion_blockers":{"unreviewed_items":0,"unverified_changes":0,"stale_verdicts":0}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := parseReview([]byte(body)); Code(err) != ReasonInvalidResponse {
				t.Fatalf("error = %v, want an invalid response", err)
			}
		})
	}
}

func TestParseIdentityRequiresAnActiveAccount(t *testing.T) {
	identity, err := parseIdentity([]byte(`{"user":{"id":"u1","username":"auditor","display_name":"감사","active":true,"roles":["AUDITOR"]}}`))
	if err != nil || identity.Roles[0] != "AUDITOR" {
		t.Fatalf("identity = %#v err=%v", identity, err)
	}
	if _, err := parseIdentity([]byte(`{"user":{"id":"u1","username":"auditor","display_name":"감사","active":false,"roles":["AUDITOR"]}}`)); Code(err) != ReasonUnauthorized {
		t.Fatalf("a disabled account was accepted: %v", err)
	}
	if _, err := parseIdentity([]byte(`{}`)); Code(err) != ReasonInvalidResponse {
		t.Fatalf("an empty payload was accepted: %v", err)
	}
}
