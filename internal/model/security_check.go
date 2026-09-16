package model

import (
	"time"

	"github.com/google/uuid"
)

// SecurityCheckSettings never serializes the stored credential: only the
// server may decrypt it, and API responses say whether one is set.
type SecurityCheckSettings struct {
	Enabled         bool      `json:"enabled"`
	BaseURL         string    `json:"baseUrl"`
	APIKeyEncrypted string    `json:"-"`
	APIKeySet       bool      `json:"apiKeySet"`
	TimeoutSeconds  int       `json:"timeoutSeconds"`
	Revision        int64     `json:"revision"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// SecurityCheckState is what an owner or an administrator sees about one app.
// It is not part of public catalog data.
type SecurityCheckState struct {
	AppID          uuid.UUID  `json:"appId"`
	ChallengeNonce uuid.UUID  `json:"challengeNonce"`
	ReviewID       string     `json:"reviewId,omitempty"`
	ReviewNumber   string     `json:"reviewNumber,omitempty"`
	RemoteStatus   string     `json:"remoteStatus,omitempty"`
	FinalResult    string     `json:"finalResult,omitempty"`
	ApprovedAt     *time.Time `json:"approvedAt,omitempty"`
	CheckedAt      *time.Time `json:"checkedAt,omitempty"`
	Verified       bool       `json:"verified"`
}

// SecurityCheckResult carries what the SecCheck client read. It must never be
// assembled from a request body: the store re-checks the binding it implies.
type SecurityCheckResult struct {
	ReviewID     string
	ReviewNumber string
	RemoteStatus string
	FinalResult  string
	ApprovedAt   *time.Time
}
