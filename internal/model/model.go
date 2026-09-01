package model

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

const (
	AppStatusDraft     = "draft"
	AppStatusPending   = "pending_review"
	AppStatusPublished = "published"
	AppStatusRejected  = "rejected"
)

type User struct {
	ID          uuid.UUID `json:"id"`
	Subject     string    `json:"-"`
	Username    string    `json:"username"`
	Email       string    `json:"email,omitempty"`
	DisplayName string    `json:"displayName"`
	Team        string    `json:"team,omitempty"`
	AuthSource  string    `json:"authSource"`
	Active      bool      `json:"active"`
	Roles       []string  `json:"roles"`
	Permissions []string  `json:"permissions"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type Category struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	Name        string    `json:"name"`
	Icon        string    `json:"icon"`
	Description string    `json:"description,omitempty"`
	Position    int       `json:"position"`
	AppCount    int       `json:"appCount"`
	Active      bool      `json:"active"`
}

type App struct {
	ID            uuid.UUID  `json:"id"`
	Name          string     `json:"name"`
	Slug          string     `json:"slug"`
	Summary       string     `json:"summary"`
	Description   string     `json:"description"`
	Icon          string     `json:"icon"`
	Gradient      string     `json:"gradient,omitempty"`
	ServiceURL    string     `json:"serviceUrl,omitempty"`
	Category      *Category  `json:"category,omitempty"`
	CategoryID    uuid.UUID  `json:"categoryId"`
	Tags          []string   `json:"tags"`
	Screenshots   []string   `json:"screenshots"`
	Language      string     `json:"language,omitempty"`
	Framework     string     `json:"framework,omitempty"`
	SupportsMCP   bool       `json:"supportsMcp"`
	SupportsAPI   bool       `json:"supportsApi"`
	OwnerID       *uuid.UUID `json:"ownerId,omitempty"`
	OwnerName     string     `json:"ownerName,omitempty"`
	Team          string     `json:"team,omitempty"`
	Version       string     `json:"version,omitempty"`
	Visibility    string     `json:"visibility"`
	Status        string     `json:"status"`
	Featured      bool       `json:"featured"`
	TrendingScore int        `json:"trendingScore"`
	CreatedAt     time.Time  `json:"createdAt"`
	UpdatedAt     time.Time  `json:"updatedAt"`
	PublishedAt   *time.Time `json:"publishedAt,omitempty"`
}

type AppInput struct {
	Name        string   `json:"name"`
	Slug        string   `json:"slug"`
	Summary     string   `json:"summary"`
	Description string   `json:"description"`
	Icon        string   `json:"icon"`
	Gradient    string   `json:"gradient,omitempty"`
	ServiceURL  string   `json:"serviceUrl"`
	CategoryID  string   `json:"categoryId"`
	Tags        []string `json:"tags"`
	Screenshots []string `json:"screenshots"`
	Language    string   `json:"language"`
	Framework   string   `json:"framework"`
	SupportsMCP bool     `json:"supportsMcp"`
	SupportsAPI bool     `json:"supportsApi"`
	Team        string   `json:"team"`
	Version     string   `json:"version"`
	Visibility  string   `json:"visibility"`
}

type AppListOptions struct {
	Query      string
	Category   string
	Language   string
	Status     string
	OwnerID    *uuid.UUID
	MCPOnly    bool
	Featured   bool
	Sort       string
	Limit      int
	Offset     int
	IncludeAll bool
}

type Page[T any] struct {
	Items  []T `json:"items"`
	Total  int `json:"total"`
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

type WorkflowConfig struct {
	Enabled              bool      `json:"enabled"`
	Levels               int       `json:"levels"`
	ReviewerRoles        []string  `json:"reviewerRoles"`
	TeamLeaderRoles      []string  `json:"teamLeaderRoles"`
	AutoPublish          bool      `json:"autoPublish"`
	RejectReasonRequired bool      `json:"rejectReasonRequired"`
	ReapprovalAfterEdit  bool      `json:"reapprovalAfterEdit"`
	PreventSelfApproval  bool      `json:"preventSelfApproval"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

type Review struct {
	ID            uuid.UUID  `json:"id"`
	AppID         uuid.UUID  `json:"appId"`
	AppName       string     `json:"appName,omitempty"`
	AppSlug       string     `json:"appSlug,omitempty"`
	SubmitterID   uuid.UUID  `json:"submitterId"`
	SubmitterName string     `json:"submitterName,omitempty"`
	ReviewerID    *uuid.UUID `json:"reviewerId,omitempty"`
	ReviewerName  string     `json:"reviewerName,omitempty"`
	Team          string     `json:"team,omitempty"`
	Level         int        `json:"level"`
	Status        string     `json:"status"`
	Reason        string     `json:"reason,omitempty"`
	CreatedAt     time.Time  `json:"createdAt"`
	DecidedAt     *time.Time `json:"decidedAt,omitempty"`
}

type Permission struct {
	Key         string    `json:"key"`
	Description string    `json:"description"`
	Category    string    `json:"category"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"createdAt"`
}

type Role struct {
	ID          uuid.UUID `json:"id"`
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	System      bool      `json:"system"`
	Permissions []string  `json:"permissions"`
	UserCount   int       `json:"userCount"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type APIKey struct {
	ID          uuid.UUID  `json:"id"`
	Name        string     `json:"name"`
	Prefix      string     `json:"prefix"`
	Permissions []string   `json:"permissions"`
	CreatedAt   time.Time  `json:"createdAt"`
	ExpiresAt   *time.Time `json:"expiresAt,omitempty"`
	LastUsedAt  *time.Time `json:"lastUsedAt,omitempty"`
	RevokedAt   *time.Time `json:"revokedAt,omitempty"`
	RotatedFrom *uuid.UUID `json:"rotatedFrom,omitempty"`
}

type KeyPolicy struct {
	MaxKeys           int  `json:"maxKeys"`
	DefaultExpiryDays int  `json:"defaultExpiryDays"`
	RotationGraceDays int  `json:"rotationGraceDays"`
	ExpireUnused      bool `json:"expireUnused"`
	UnusedExpiryDays  int  `json:"unusedExpiryDays"`
	ForceRotation     bool `json:"forceRotation"`
	ForceRotationDays int  `json:"forceRotationDays"`
}

type KeyPermissionDefinition struct {
	Key         string    `json:"key"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Active      bool      `json:"active"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type KeyPermissionTemplate struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Permissions []string  `json:"permissions"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

type OIDCSettings struct {
	Enabled         bool                `json:"enabled"`
	IssuerURL       string              `json:"issuerUrl"`
	ClientID        string              `json:"clientId"`
	ClientSecret    string              `json:"-"`
	ClientSecretSet bool                `json:"clientSecretSet"`
	RoleClaimPath   string              `json:"roleClaimPath"`
	GroupClaimPath  string              `json:"groupClaimPath"`
	RoleMappings    map[string][]string `json:"roleMappings"`
	GroupMappings   map[string][]string `json:"groupMappings"`
	Scopes          []string            `json:"scopes"`
	UpdatedAt       time.Time           `json:"updatedAt"`
}

type SystemSettings struct {
	SiteName        string `json:"siteName"`
	SiteURL         string `json:"siteUrl"`
	LogoURL         string `json:"logoUrl,omitempty"`
	FaviconURL      string `json:"faviconUrl,omitempty"`
	Theme           string `json:"theme"`
	DefaultLanguage string `json:"defaultLanguage"`
	PageSize        int    `json:"pageSize"`
	PublicMode      bool   `json:"publicMode"`
}

type APISettings struct {
	Enabled            bool `json:"enabled"`
	Anonymous          bool `json:"anonymous"`
	RateLimitPerMinute int  `json:"rateLimitPerMinute"`
}

type MCPSettings struct {
	Enabled            bool   `json:"enabled"`
	Anonymous          bool   `json:"anonymous"`
	RateLimitPerMinute int    `json:"rateLimitPerMinute"`
	ProtocolVersion    string `json:"protocolVersion"`
}

type AIProvider struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	Kind            string    `json:"kind"`
	BaseURL         string    `json:"baseUrl"`
	APIKey          string    `json:"-"`
	APIKeySet       bool      `json:"apiKeySet"`
	DefaultModel    string    `json:"defaultModel"`
	ContextWindow   int64     `json:"contextWindow"`
	MaxInputTokens  int64     `json:"maxInputTokens"`
	MaxOutputTokens int64     `json:"maxOutputTokens"`
	Temperature     float64   `json:"temperature"`
	TimeoutSeconds  int       `json:"timeoutSeconds"`
	Retries         int       `json:"retries"`
	Streaming       bool      `json:"streaming"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type AIModel struct {
	ID              uuid.UUID `json:"id"`
	ProviderID      uuid.UUID `json:"providerId"`
	Name            string    `json:"name"`
	ContextWindow   int64     `json:"contextWindow"`
	MaxInputTokens  int64     `json:"maxInputTokens"`
	MaxOutputTokens int64     `json:"maxOutputTokens"`
	Enabled         bool      `json:"enabled"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

type AuditLog struct {
	ID         int64           `json:"id"`
	ActorID    *uuid.UUID      `json:"actorId,omitempty"`
	Actor      string          `json:"actor"`
	Action     string          `json:"action"`
	Resource   string          `json:"resource"`
	ResourceID string          `json:"resourceId,omitempty"`
	Before     json.RawMessage `json:"before,omitempty"`
	After      json.RawMessage `json:"after,omitempty"`
	IP         string          `json:"ip,omitempty"`
	UserAgent  string          `json:"userAgent,omitempty"`
	RequestID  string          `json:"requestId"`
	CreatedAt  time.Time       `json:"createdAt"`
}
