package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

type publicConfigResponse struct {
	SiteName string `json:"siteName"`
	// The banner wording is empty when the administrator has not set it; the
	// store front then renders its shipped default.
	model.HomeCopy
	SiteURL         string `json:"siteUrl,omitempty"`
	LogoURL         string `json:"logoUrl,omitempty"`
	FaviconURL      string `json:"faviconUrl,omitempty"`
	PublicMode      bool   `json:"publicMode"`
	OIDCEnabled     bool   `json:"oidcEnabled"`
	OIDCConfigured  bool   `json:"oidcConfigured"`
	WorkflowEnabled bool   `json:"workflowEnabled"`
	AnonymousMCP    bool   `json:"anonymousMcp"`
	Theme           string `json:"theme"`
}

func (s *Server) publicConfig(w http.ResponseWriter, r *http.Request) {
	settings := model.SystemSettings{SiteName: "Dev App Store", Theme: "system", PublicMode: true, PageSize: 24, DefaultLanguage: "ko"}
	var raw []byte
	if err := s.repository.Pool().QueryRow(r.Context(), `SELECT value FROM system_settings WHERE key = 'system'`).Scan(&raw); err == nil {
		_ = json.Unmarshal(raw, &settings)
	}
	var oidcEnabled bool
	var issuer, clientID, clientSecret string
	_ = s.repository.Pool().QueryRow(r.Context(), `SELECT enabled, issuer_url, client_id, client_secret_encrypted FROM oidc_settings WHERE singleton`).Scan(&oidcEnabled, &issuer, &clientID, &clientSecret)
	workflow, _ := s.repository.GetWorkflowConfig(r.Context())
	anonymousMCP := true
	if err := s.repository.Pool().QueryRow(r.Context(), `SELECT value FROM system_settings WHERE key = 'mcp'`).Scan(&raw); err == nil {
		var config struct {
			Anonymous bool `json:"anonymous"`
		}
		if json.Unmarshal(raw, &config) == nil {
			anonymousMCP = config.Anonymous
		}
	}
	// An uploaded image wins over a typed URL: it is served from this origin,
	// so it works under the image content security policy and keeps working if
	// the original host disappears.
	checksums, _ := s.repository.ListBrandingChecksums(r.Context())
	logoURL := settings.LogoURL
	if checksum, ok := checksums[store.BrandingLogo]; ok {
		logoURL = brandingURL(store.BrandingLogo, checksum)
	}
	faviconURL := settings.FaviconURL
	if checksum, ok := checksums[store.BrandingFavicon]; ok {
		faviconURL = brandingURL(store.BrandingFavicon, checksum)
	}
	WriteJSON(w, http.StatusOK, publicConfigResponse{
		SiteName: settings.SiteName, HomeCopy: settings.HomeCopy,
		SiteURL: settings.SiteURL, LogoURL: logoURL,
		FaviconURL: faviconURL,
		PublicMode: settings.PublicMode, OIDCEnabled: oidcEnabled,
		OIDCConfigured: issuer != "" && clientID != "" && clientSecret != "", WorkflowEnabled: workflow.Enabled,
		AnonymousMCP: anonymousMCP, Theme: settings.Theme,
	})
}

func (s *Server) listApps(w http.ResponseWriter, r *http.Request) {
	limit, offset := pagination(r, 24, 100)
	options := model.AppListOptions{
		Query: r.URL.Query().Get("q"), Category: r.URL.Query().Get("category"),
		Language: r.URL.Query().Get("language"), MCPOnly: boolQuery(r.URL.Query().Get("mcp")),
		Featured: boolQuery(r.URL.Query().Get("featured")), Sort: normalizedSort(r.URL.Query().Get("sort")),
		Limit: limit, Offset: offset,
	}
	page, err := s.repository.ListApps(r.Context(), options)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

func (s *Server) getApp(w http.ResponseWriter, r *http.Request) {
	app, err := s.repository.GetAppBySlug(r.Context(), chi.URLParam(r, "app"), false)
	if err != nil {
		WriteError(w, r, storeError(err, "APP_NOT_FOUND", "앱을 찾을 수 없습니다."))
		return
	}
	WriteJSON(w, http.StatusOK, app)
}

func (s *Server) listCategories(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListCategories(r.Context(), false)
	if err != nil {
		WriteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func pagination(r *http.Request, defaultLimit, maximum int) (int, int) {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 {
		limit = defaultLimit
	}
	if limit > maximum {
		limit = maximum
	}
	offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
	if err != nil || offset < 0 {
		offset = 0
	}
	return limit, offset
}

// normalizedSort keeps only sort keys the store understands. Anything else
// becomes the empty string, which asks the store for its own default: most
// recently updated, or the editorial order when the list is featured-only.
func normalizedSort(value string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(value)); normalized {
	case "name", "created", "trending", "published", "featured", "updated":
		return normalized
	default:
		return ""
	}
}
