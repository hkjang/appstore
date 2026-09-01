package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/hkjang/appstore/internal/ai"
	appauth "github.com/hkjang/appstore/internal/auth"
	"github.com/hkjang/appstore/internal/buildinfo"
	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/hkjang/appstore/internal/mcp"
	"github.com/hkjang/appstore/internal/store"
	"github.com/hkjang/appstore/openapi"
)

type Server struct {
	repository        *store.Repository
	box               *appcrypto.SecretBox
	logger            *slog.Logger
	auth              *AuthMiddleware
	oidc              *appauth.OIDCClient
	streamer          *ai.Streamer
	startedAt         time.Time
	dummyPasswordHash string
	apiLimiter        *fixedWindowLimiter
	mcpLimiter        *fixedWindowLimiter
	loginLimiter      *fixedWindowLimiter
}

func New(repository *store.Repository, box *appcrypto.SecretBox, logger *slog.Logger) (*Server, error) {
	if repository == nil || box == nil {
		return nil, errors.New("repository and encryption box are required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	dummyHash, err := appauth.HashPassword("appstore-dummy-password-value")
	if err != nil {
		return nil, err
	}
	return &Server{
		repository: repository, box: box, logger: logger,
		auth:     &AuthMiddleware{Repository: repository, Box: box},
		oidc:     &appauth.OIDCClient{Box: box, HTTPClient: &http.Client{Timeout: 15 * time.Second}},
		streamer: &ai.Streamer{Box: box}, startedAt: time.Now().UTC(), dummyPasswordHash: dummyHash,
		apiLimiter: newFixedWindowLimiter(), mcpLimiter: newFixedWindowLimiter(), loginLimiter: newFixedWindowLimiter(),
	}, nil
}

func (s *Server) Handler() (http.Handler, error) {
	router := chi.NewRouter()
	router.Use(RequestIDMiddleware)
	router.Use(SecurityHeaders)
	router.Use(func(next http.Handler) http.Handler { return Recoverer(s.logger, next) })
	router.Use(func(next http.Handler) http.Handler { return AccessLog(s.logger, next) })

	router.Get("/health/live", s.live)
	router.Get("/healthz", s.live)
	router.Get("/health/ready", s.ready)
	router.Get("/readyz", s.ready)
	router.Get("/api/version", s.version)
	router.Get("/openapi.json", s.openAPIDocument)
	router.Get("/docs", s.apiDocsHTML)
	router.Get("/docs/", s.apiDocsHTML)
	router.Get("/docs/app.js", s.apiDocsJS)
	router.Get("/docs/style.css", s.apiDocsCSS)

	mcpServer := &mcp.Server{
		Version:      buildinfo.Current().Version,
		Provider:     mcp.AppTools{Execute: s.executeMCPTool},
		Authenticate: s.authenticateMCP,
		Enabled:      s.mcpPolicy,
	}
	router.Mount("/mcp", s.mcpRateLimit(mcpServer))

	router.Route("/api/v1", func(api chi.Router) {
		api.Use(NoStore)
		api.Use(s.auth.Optional)
		api.Use(s.apiPolicy)

		api.Get("/public/config", s.publicConfig)
		api.Get("/apps", s.listApps)
		api.Get("/apps/{app}", s.getApp)
		api.Get("/categories", s.listCategories)

		api.Get("/auth/session", s.sessionState)
		api.Post("/auth/bootstrap/login", s.bootstrapLogin)
		api.Get("/auth/oidc/login", s.oidcLogin)
		api.Get("/auth/oidc/callback", s.oidcCallback)

		api.Group(func(protected chi.Router) {
			protected.Use(RequireAuthenticated)
			protected.Use(s.auth.VerifyCSRF)
			protected.Post("/auth/logout", s.logout)
			protected.Get("/me", s.me)
			protected.Get("/me/apps", s.myApps)
			protected.Get("/me/activity", s.myActivity)
			protected.Get("/me/settings", s.mySettings)
			protected.Put("/me/settings", s.updateMySettings)
			protected.With(permission("favorites:read")).Get("/me/favorites", s.myFavorites)
			protected.With(permission("favorites:write")).Put("/me/favorites/{id}", s.addFavorite)
			protected.With(permission("favorites:write")).Delete("/me/favorites/{id}", s.removeFavorite)
			protected.With(permission("keys:manage")).Get("/me/keys", s.myKeys)
			protected.With(permission("keys:manage")).Post("/me/keys", s.createKey)
			protected.With(permission("keys:manage")).Post("/me/keys/{id}/rotate", s.rotateKey)
			protected.With(permission("keys:manage")).Post("/me/keys/{id}/revoke", s.revokeKey)
			protected.With(permission("keys:manage")).Get("/me/key-permissions", s.keyPermissions)

			protected.With(permission("apps:submit")).Post("/apps", s.createApp)
			protected.With(permission("apps:update")).Put("/apps/{app}", s.updateApp)
			protected.With(permission("apps:delete")).Delete("/apps/{app}", s.deleteApp)

			protected.With(permission("reviews:read")).Get("/reviews", s.listReviews)
			protected.With(permission("reviews:read")).Get("/reviews/{id}", s.getReview)
			protected.With(permission("reviews:decide")).Post("/reviews/{id}/approve", s.approveReview)
			protected.With(permission("reviews:decide")).Post("/reviews/{id}/reject", s.rejectReview)

			protected.With(permission("ai:use")).Post("/ai/chat/stream", s.aiStream)
			s.adminRoutes(protected)
		})

		api.NotFound(func(w http.ResponseWriter, r *http.Request) {
			WriteError(w, r, NotFound("API_NOT_FOUND", "API 경로를 찾을 수 없습니다."))
		})
	})

	spa, err := NewSPAHandler()
	if err != nil {
		return nil, err
	}
	router.Handle("/*", spa)
	return router, nil
}

func (s *Server) adminRoutes(r chi.Router) {
	r.With(permission("settings:read")).Get("/admin/dashboard", s.adminDashboard)
	r.With(permission("apps:manage")).Get("/admin/apps", s.adminApps)
	r.With(permission("apps:manage")).Put("/admin/apps/{id}/status", s.adminSetAppStatus)
	r.With(permission("apps:manage")).Get("/admin/categories", s.adminCategories)
	r.With(permission("apps:manage")).Post("/admin/categories", s.adminCreateCategory)
	r.With(permission("apps:manage")).Put("/admin/categories/{id}", s.adminUpdateCategory)
	r.With(permission("apps:manage")).Delete("/admin/categories/{id}", s.adminDeleteCategory)

	r.With(permission("users:manage")).Get("/admin/users", s.adminUsers)
	r.With(permission("users:manage")).Put("/admin/users/{id}", s.adminUpdateUser)
	r.With(permission("users:manage")).Put("/admin/users/{id}/roles", s.adminUpdateUserRoles)
	r.With(permission("users:manage")).Delete("/admin/users/{id}", s.adminDeleteUser)

	r.With(permission("roles:manage")).Get("/admin/roles", s.adminRoles)
	r.With(permission("roles:manage")).Put("/admin/roles", s.adminReplaceRoles)
	r.With(permission("roles:manage")).Post("/admin/roles", s.adminCreateRole)
	r.With(permission("roles:manage")).Put("/admin/roles/{id}", s.adminUpdateRole)
	r.With(permission("roles:manage")).Delete("/admin/roles/{id}", s.adminDeleteRole)

	r.With(permission("settings:read")).Get("/admin/workflow", s.adminWorkflow)
	r.With(permission("settings:write")).Put("/admin/workflow", s.adminUpdateWorkflow)
	r.With(permission("settings:read")).Get("/admin/authentication", s.adminAuthentication)
	r.With(permission("settings:write")).Put("/admin/authentication", s.adminUpdateAuthentication)
	r.With(permission("settings:write")).Post("/admin/authentication/test", s.adminTestAuthentication)
	r.With(permission("settings:read")).Get("/admin/ai", s.adminAI)
	r.With(permission("settings:write")).Put("/admin/ai", s.adminUpdateAI)
	r.With(permission("settings:read")).Get("/admin/ai/models", s.adminAIModels)
	r.With(permission("settings:write")).Put("/admin/ai/models", s.adminUpsertAIModel)
	r.With(permission("settings:write")).Delete("/admin/ai/models/{id}", s.adminDeleteAIModel)
	r.With(permission("settings:read")).Get("/admin/api", s.adminAPI)
	r.With(permission("settings:write")).Put("/admin/api", s.adminUpdateAPI)
	r.With(permission("settings:read")).Get("/admin/mcp", s.adminMCP)
	r.With(permission("settings:write")).Put("/admin/mcp", s.adminUpdateMCP)
	r.With(permission("settings:read")).Get("/admin/security", s.adminSecurity)
	r.With(permission("settings:write")).Put("/admin/security", s.adminUpdateSecurity)
	r.With(permission("settings:write")).Put("/admin/security/permissions/{key}", s.adminUpsertKeyPermission)
	r.With(permission("settings:write")).Post("/admin/security/templates", s.adminCreateKeyTemplate)
	r.With(permission("settings:write")).Put("/admin/security/templates/{id}", s.adminUpdateKeyTemplate)
	r.With(permission("settings:write")).Delete("/admin/security/templates/{id}", s.adminDeleteKeyTemplate)
	r.With(permission("settings:read")).Get("/admin/settings", s.adminSystemSettings)
	r.With(permission("settings:write")).Put("/admin/settings", s.adminUpdateSystemSettings)
	r.With(permission("settings:read")).Get("/admin/api-keys", s.adminAPIKeys)
	r.With(permission("audit:read")).Get("/admin/audit", s.adminAudit)
}

func permission(value string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return RequirePermission(value, next) }
}

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, map[string]any{"status": "ok", "uptimeSeconds": int64(time.Since(s.startedAt).Seconds())})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.repository.Pool().Ping(ctx); err != nil {
		WriteJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) version(w http.ResponseWriter, _ *http.Request) {
	WriteJSON(w, http.StatusOK, buildinfo.Current())
}

func (s *Server) openAPIDocument(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/vnd.oai.openapi+json;version=3.1")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(openapi.Document)
}

func serveDocAsset(w http.ResponseWriter, contentType string, value []byte) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(value)
}

func (s *Server) apiDocsHTML(w http.ResponseWriter, _ *http.Request) {
	serveDocAsset(w, "text/html; charset=utf-8", openapi.DocsHTML)
}
func (s *Server) apiDocsJS(w http.ResponseWriter, _ *http.Request) {
	serveDocAsset(w, "text/javascript; charset=utf-8", openapi.DocsJS)
}
func (s *Server) apiDocsCSS(w http.ResponseWriter, _ *http.Request) {
	serveDocAsset(w, "text/css; charset=utf-8", openapi.DocsCSS)
}

func (s *Server) authenticateMCP(r *http.Request) (mcp.Caller, error) {
	principal, err := s.auth.Authenticate(r)
	if err != nil {
		return mcp.Caller{}, err
	}
	if principal == nil {
		return mcp.Caller{Permissions: map[string]bool{}}, mcp.ErrAnonymous
	}
	return mcp.Caller{Authenticated: true, UserID: principal.User.ID.String(), Permissions: principal.Permissions}, nil
}

func decodeSetting[T any](value json.RawMessage, fallback T) T {
	if len(value) == 0 || json.Unmarshal(value, &fallback) != nil {
		return fallback
	}
	return fallback
}

func boolQuery(value string) bool {
	return value == "1" || strings.EqualFold(value, "true") || strings.EqualFold(value, "yes")
}
