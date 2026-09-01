package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/config"
	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/hkjang/appstore/internal/database"
	"github.com/hkjang/appstore/internal/mcp"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

func TestPostgreSQLHTTPPolicyIntegration(t *testing.T) {
	dsn := os.Getenv("APPSTORE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("APPSTORE_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Initialize(ctx, config.Config{
		PostgresDSN: dsn, BootstrapAdmin: "bootstrap-admin",
		BootstrapAdminPassword: "initial-bootstrap-password",
		EncryptionKey:          "01234567890123456789012345678901",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	lockConnection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lockConnection.Exec(ctx, `SELECT pg_advisory_lock($1)`, int64(0x41505053544f5245)); err != nil {
		lockConnection.Release()
		t.Fatal(err)
	}
	defer func() {
		unlockCtx, unlockCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer unlockCancel()
		_, _ = lockConnection.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, int64(0x41505053544f5245))
		lockConnection.Release()
	}()
	repository := store.New(pool)
	box, err := appcrypto.NewSecretBox("01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}

	originalSystem, err := repository.GetSystemSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	originalAPI, err := repository.GetAPISettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	originalMCP, err := repository.GetMCPSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = repository.UpdateSystemSettings(context.Background(), originalSystem, nil)
		_, _ = repository.UpdateAPISettings(context.Background(), originalAPI, nil)
		_, _ = repository.UpdateMCPSettings(context.Background(), originalMCP, nil)
	}()

	systemSettings := originalSystem
	systemSettings.PublicMode = true
	if _, err := repository.UpdateSystemSettings(ctx, systemSettings, nil); err != nil {
		t.Fatal(err)
	}
	apiSettings := originalAPI
	apiSettings.Enabled = true
	apiSettings.Anonymous = true
	apiSettings.RateLimitPerMinute = 100
	if _, err := repository.UpdateAPISettings(ctx, apiSettings, nil); err != nil {
		t.Fatal(err)
	}
	mcpSettings := originalMCP
	mcpSettings.Enabled = true
	mcpSettings.Anonymous = true
	mcpSettings.RateLimitPerMinute = 100
	mcpSettings.ProtocolVersion = mcp.ProtocolVersion
	if _, err := repository.UpdateMCPSettings(ctx, mcpSettings, nil); err != nil {
		t.Fatal(err)
	}

	newHandler := func() http.Handler {
		service, err := New(repository, box, nil)
		if err != nil {
			t.Fatal(err)
		}
		handler, err := service.Handler()
		if err != nil {
			t.Fatal(err)
		}
		return handler
	}
	request := func(handler http.Handler, method, target, body, remote string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.RemoteAddr = remote
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}

	handler := newHandler()
	if response := request(handler, http.MethodGet, "/api/v1/apps", "", "198.51.100.1:1001", nil); response.Code != http.StatusOK {
		t.Fatalf("public catalog status=%d body=%s", response.Code, response.Body.String())
	}
	apiSettings.Anonymous = false
	if _, err := repository.UpdateAPISettings(ctx, apiSettings, nil); err != nil {
		t.Fatal(err)
	}
	if response := request(handler, http.MethodGet, "/api/v1/apps", "", "198.51.100.2:1002", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous-off catalog status=%d body=%s", response.Code, response.Body.String())
	}
	apiSettings.Anonymous = true
	if _, err := repository.UpdateAPISettings(ctx, apiSettings, nil); err != nil {
		t.Fatal(err)
	}
	systemSettings.PublicMode = false
	if _, err := repository.UpdateSystemSettings(ctx, systemSettings, nil); err != nil {
		t.Fatal(err)
	}
	if response := request(handler, http.MethodGet, "/api/v1/categories", "", "198.51.100.3:1003", nil); response.Code != http.StatusUnauthorized {
		t.Fatalf("public-mode-off catalog status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(handler, http.MethodGet, "/api/v1/public/config", "", "198.51.100.3:1003", nil); response.Code != http.StatusOK {
		t.Fatalf("public config status=%d body=%s", response.Code, response.Body.String())
	}
	systemSettings.PublicMode = true
	if _, err := repository.UpdateSystemSettings(ctx, systemSettings, nil); err != nil {
		t.Fatal(err)
	}

	apiSettings.RateLimitPerMinute = 2
	if _, err := repository.UpdateAPISettings(ctx, apiSettings, nil); err != nil {
		t.Fatal(err)
	}
	handler = newHandler()
	for attempt := 1; attempt <= 3; attempt++ {
		response := request(handler, http.MethodGet, "/api/v1/categories", "", "198.51.100.4:1004", nil)
		want := http.StatusOK
		if attempt == 3 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("API rate attempt=%d status=%d body=%s", attempt, response.Code, response.Body.String())
		}
	}

	mcpSettings.RateLimitPerMinute = 1
	if _, err := repository.UpdateMCPSettings(ctx, mcpSettings, nil); err != nil {
		t.Fatal(err)
	}
	handler = newHandler()
	mcpBody := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"integration","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`
	mcpHeaders := map[string]string{
		"Content-Type": "application/json", "MCP-Protocol-Version": mcp.ProtocolVersion,
		"Mcp-Method": "tools/list",
	}
	if response := request(handler, http.MethodPost, "/mcp", mcpBody, "198.51.100.5:1005", mcpHeaders); response.Code != http.StatusOK {
		t.Fatalf("first MCP request status=%d body=%s", response.Code, response.Body.String())
	}
	if response := request(handler, http.MethodPost, "/mcp", mcpBody, "198.51.100.5:1005", mcpHeaders); response.Code != http.StatusTooManyRequests {
		t.Fatalf("limited MCP request status=%d body=%s", response.Code, response.Body.String())
	}

	apiSettings.RateLimitPerMinute = 100
	if _, err := repository.UpdateAPISettings(ctx, apiSettings, nil); err != nil {
		t.Fatal(err)
	}
	handler = newHandler()
	loginBody := `{"username":"bootstrap-admin","password":"definitely-wrong"}`
	for attempt := 1; attempt <= 6; attempt++ {
		response := request(handler, http.MethodPost, "/api/v1/auth/bootstrap/login", loginBody, "198.51.100.6:1006", map[string]string{"Content-Type": "application/json"})
		want := http.StatusUnauthorized
		if attempt == 6 {
			want = http.StatusTooManyRequests
		}
		if response.Code != want {
			t.Fatalf("login rate attempt=%d status=%d body=%s", attempt, response.Code, response.Body.String())
		}
	}

	var upstreamCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamCalls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer upstream.Close()
	provider, err := repository.CreateAIProvider(ctx, model.AIProvider{
		Name: "http-integration-" + uuid.NewString(), Kind: "openai_compatible",
		BaseURL: upstream.URL + "/v1", DefaultModel: "default-model",
		ContextWindow: 262144, MaxInputTokens: 200000, MaxOutputTokens: 62144,
		Temperature: 0.7, TimeoutSeconds: 120, Retries: 1, Streaming: true, Enabled: true,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.DeleteAIProvider(context.Background(), provider.ID) }()
	if _, err := repository.UpsertAIModel(ctx, model.AIModel{
		ProviderID: provider.ID, Name: "limited-model", ContextWindow: 131072,
		MaxInputTokens: 100000, MaxOutputTokens: 31072, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	service, err := New(repository, box, nil)
	if err != nil {
		t.Fatal(err)
	}
	resolveRequest := httptest.NewRequest(http.MethodPost, "/api/v1/ai/chat/stream", nil)
	resolved, err := service.resolveAIProvider(resolveRequest, provider.ID.String(), "limited-model")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.ContextWindow != 131072 || resolved.MaxInputTokens != 100000 || resolved.MaxOutputTokens != 31072 {
		t.Fatalf("resolved model overlay = %#v", resolved)
	}

	login := request(handler, http.MethodPost, "/api/v1/auth/bootstrap/login",
		`{"username":"bootstrap-admin","password":"initial-bootstrap-password"}`,
		"198.51.100.7:1007", map[string]string{"Content-Type": "application/json"})
	if login.Code != http.StatusOK {
		t.Fatalf("bootstrap login status=%d body=%s", login.Code, login.Body.String())
	}
	var session sessionResponse
	if err := json.Unmarshal(login.Body.Bytes(), &session); err != nil || !session.Authenticated || session.CSRFToken == "" {
		t.Fatalf("bootstrap session = %#v err=%v", session, err)
	}
	loginResult := login.Result()
	defer loginResult.Body.Close()
	cookieParts := make([]string, 0, len(loginResult.Cookies()))
	for _, cookie := range loginResult.Cookies() {
		cookieParts = append(cookieParts, cookie.Name+"="+cookie.Value)
	}
	authHeaders := map[string]string{
		"Content-Type": "application/json", "Cookie": strings.Join(cookieParts, "; "),
		"X-CSRF-Token": session.CSRFToken,
	}
	invalidModel, err := json.Marshal(model.AIModel{
		ProviderID: provider.ID, Name: "invalid-envelope", ContextWindow: 131072,
		MaxInputTokens: 100000, MaxOutputTokens: 40000, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response := request(handler, http.MethodPut, "/api/v1/admin/ai/models", string(invalidModel), "198.51.100.7:1007", authHeaders); response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid model REST status=%d body=%s", response.Code, response.Body.String())
	}
	validModel, err := json.Marshal(model.AIModel{
		ProviderID: provider.ID, Name: "rest-model", ContextWindow: 131072,
		MaxInputTokens: 100000, MaxOutputTokens: 31072, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response := request(handler, http.MethodPut, "/api/v1/admin/ai/models", string(validModel), "198.51.100.7:1007", authHeaders); response.Code != http.StatusOK {
		t.Fatalf("valid model REST status=%d body=%s", response.Code, response.Body.String())
	}

	limitedStream := `{"providerId":"` + provider.ID.String() + `","model":"limited-model","messages":[{"role":"user","content":"hello"}],"maxTokens":40000}`
	response := request(handler, http.MethodPost, "/api/v1/ai/chat/stream", limitedStream, "198.51.100.7:1007", authHeaders)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "AI_STREAM_ERROR") || upstreamCalls.Load() != 0 {
		t.Fatalf("model-limited stream status=%d calls=%d body=%s", response.Code, upstreamCalls.Load(), response.Body.String())
	}
	validStream := `{"providerId":"` + provider.ID.String() + `","model":"limited-model","messages":[{"role":"user","content":"hello"}],"maxTokens":31072}`
	response = request(handler, http.MethodPost, "/api/v1/ai/chat/stream", validStream, "198.51.100.7:1007", authHeaders)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "event: message") || !strings.Contains(response.Body.String(), "event: finish") || upstreamCalls.Load() != 1 {
		t.Fatalf("valid model stream status=%d calls=%d body=%s", response.Code, upstreamCalls.Load(), response.Body.String())
	}
}
