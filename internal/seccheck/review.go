package seccheck

import (
	"encoding/json"
	"strings"
	"time"
)

// The binding block is what an owner copies into the SecCheck review
// description. Its markers are fixed so a review can carry exactly one.
const (
	BindingStart = "[APPSTORE-SECURITY-CHECK:v1]"
	BindingEnd   = "[/APPSTORE-SECURITY-CHECK]"
)

// CompletionBlockers are SecCheck's own counts of what still stands in the way
// of a finished review. An approved review with any of them above zero is not
// one AppStore accepts.
type CompletionBlockers struct {
	UnreviewedItems   *int `json:"unreviewed_items"`
	UnverifiedChanges *int `json:"unverified_changes"`
	StaleVerdicts     *int `json:"stale_verdicts"`
}

type Review struct {
	ID                 string
	Number             string
	ServiceName        string
	Description        string
	Status             string
	FinalResult        string
	ApprovedAt         *time.Time
	CompletionBlockers CompletionBlockers
}

type Identity struct {
	ID          string
	Username    string
	DisplayName string
	Roles       []string
}

// Expected is what the review has to say about itself for AppStore to treat it
// as this app's approval.
type Expected struct {
	BindingText string
	ServiceName string
}

type reviewPayload struct {
	ID                 string             `json:"id"`
	Number             string             `json:"review_number"`
	ServiceName        string             `json:"service_name"`
	Description        string             `json:"description"`
	Status             string             `json:"status"`
	FinalResult        string             `json:"final_result"`
	ApprovedAt         *time.Time         `json:"approved_at"`
	CompletionBlockers CompletionBlockers `json:"completion_blockers"`
}

func parseReview(body []byte) (*Review, error) {
	var payload reviewPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, detailed(ReasonInvalidResponse, err.Error())
	}
	review := &Review{
		ID: payload.ID, Number: payload.Number, ServiceName: payload.ServiceName,
		Description: payload.Description, Status: payload.Status, FinalResult: payload.FinalResult,
		ApprovedAt: payload.ApprovedAt, CompletionBlockers: payload.CompletionBlockers,
	}
	if !ValidReviewID(review.ID) || strings.TrimSpace(review.Number) == "" ||
		strings.TrimSpace(review.ServiceName) == "" || !validStatus(review.Status) || !validResult(review.FinalResult) {
		return nil, detailed(ReasonInvalidResponse, "review fields are missing or unknown")
	}
	blockers := review.CompletionBlockers
	if blockers.UnreviewedItems == nil || blockers.UnverifiedChanges == nil || blockers.StaleVerdicts == nil {
		return nil, detailed(ReasonInvalidResponse, "completion blockers are missing")
	}
	return review, nil
}

func validStatus(status string) bool {
	switch status {
	case "DRAFT", "CHANGE_REQUESTED", "SUBMITTED", "RESUBMITTED", "REVIEWING",
		"APPROVAL_PENDING", "APPROVED", "REJECTED", "CANCELLED", "CLOSED":
		return true
	}
	return false
}

func validResult(result string) bool {
	switch result {
	case "", "APPROVED", "CONDITIONAL", "REJECTED":
		return true
	}
	return false
}

// Verify decides whether a review approves this exact app. The binding is
// checked before the verdict, so an unrelated approved review is reported as
// the mismatch it is rather than as an app merely waiting for approval.
func Verify(review *Review, expected Expected) error {
	if review == nil || !ValidReviewID(review.ID) || !validStatus(review.Status) || !validResult(review.FinalResult) {
		return detailed(ReasonInvalidResponse, "review cannot be verified")
	}
	if !bindingMatches(review.Description, expected.BindingText) {
		return failure(ReasonBindingMismatch)
	}
	if expected.ServiceName == "" || review.ServiceName != expected.ServiceName {
		return failure(ReasonServiceMismatch)
	}
	blockers := review.CompletionBlockers
	if blockers.UnreviewedItems == nil || blockers.UnverifiedChanges == nil || blockers.StaleVerdicts == nil {
		return detailed(ReasonInvalidResponse, "completion blockers are missing")
	}
	// CONDITIONAL and REJECTED are decisions, not approvals: only a review that
	// SecCheck itself calls APPROVED on both axes counts.
	if review.Status != "APPROVED" || review.FinalResult != "APPROVED" {
		return failure(ReasonNotApproved)
	}
	if review.ApprovedAt == nil || review.ApprovedAt.IsZero() || review.ApprovedAt.After(time.Now().Add(5*time.Minute)) {
		return detailed(ReasonInvalidResponse, "approval timestamp is missing or in the future")
	}
	if *blockers.UnreviewedItems != 0 || *blockers.UnverifiedChanges != 0 || *blockers.StaleVerdicts != 0 {
		return failure(ReasonIncomplete)
	}
	return nil
}

// bindingMatches looks for the expected block as whole lines of the
// description. Anything looser — a substring, a second block, a trimmed
// comparison — would let one approved review cover more than it was read for.
func bindingMatches(description, expected string) bool {
	description = strings.ReplaceAll(description, "\r\n", "\n")
	expected = strings.ReplaceAll(expected, "\r\n", "\n")
	if strings.ContainsAny(expected, "\r\x00") ||
		!strings.HasPrefix(expected, BindingStart+"\n") || !strings.HasSuffix(expected, "\n"+BindingEnd) {
		return false
	}
	for _, text := range []string{description, expected} {
		if strings.Count(text, "[APPSTORE-SECURITY-CHECK") != 1 || strings.Count(text, BindingEnd) != 1 {
			return false
		}
	}
	if strings.Count(description, expected) != 1 {
		return false
	}
	start := strings.Index(description, expected)
	end := start + len(expected)
	return (start == 0 || description[start-1] == '\n') && (end == len(description) || description[end] == '\n')
}

type identityPayload struct {
	User struct {
		ID          string   `json:"id"`
		Username    string   `json:"username"`
		DisplayName string   `json:"display_name"`
		Active      *bool    `json:"active"`
		Roles       []string `json:"roles"`
	} `json:"user"`
}

func parseIdentity(body []byte) (*Identity, error) {
	var payload identityPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, detailed(ReasonInvalidResponse, err.Error())
	}
	user := payload.User
	if strings.TrimSpace(user.ID) == "" || strings.TrimSpace(user.Username) == "" || user.Active == nil {
		return nil, detailed(ReasonInvalidResponse, "identity fields are missing")
	}
	if !*user.Active {
		return nil, detailed(ReasonUnauthorized, "the SecCheck account is disabled")
	}
	return &Identity{ID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Roles: user.Roles}, nil
}
