package store

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	appstore "github.com/hkjang/appstore"
	"github.com/hkjang/appstore/internal/auth"
	"github.com/hkjang/appstore/internal/config"
	"github.com/hkjang/appstore/internal/database"
	"github.com/hkjang/appstore/internal/model"
)

func TestPostgreSQLRepositoryIntegration(t *testing.T) {
	dsn := os.Getenv("APPSTORE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("APPSTORE_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg := config.Config{
		PostgresDSN:            dsn,
		BootstrapAdmin:         "bootstrap-admin",
		BootstrapAdminPassword: "initial-bootstrap-password",
		EncryptionKey:          "01234567890123456789012345678901",
	}
	pool, err := database.Initialize(ctx, cfg)
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
	repository := New(pool)

	var migrationCount, appCount, categoryCount, seededServiceURLs int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM schema_migrations`).Scan(&migrationCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM apps WHERE external_seed_id IS NOT NULL`).Scan(&appCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM categories`).Scan(&categoryCount); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM apps WHERE external_seed_id IS NOT NULL AND service_url <> ''`).Scan(&seededServiceURLs); err != nil {
		t.Fatal(err)
	}
	if migrationCount != 1 || appCount != 73 || categoryCount < 7 || seededServiceURLs != 0 {
		t.Fatalf("seed state migrations=%d apps=%d categories=%d serviceURLs=%d", migrationCount, appCount, categoryCount, seededServiceURLs)
	}
	if err := database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := database.SeedApps(ctx, pool, appstore.DefaultAppsJSON); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM apps WHERE external_seed_id IS NOT NULL`).Scan(&appCount); err != nil {
		t.Fatal(err)
	}
	if appCount != 73 {
		t.Fatalf("idempotent seed app count = %d, want 73", appCount)
	}

	credential, err := repository.GetBootstrapCredential(ctx, "bootstrap-admin")
	if err != nil {
		t.Fatal(err)
	}
	if !auth.CheckPassword(credential.PasswordHash, cfg.BootstrapAdminPassword) {
		t.Fatal("initial bootstrap password does not match")
	}
	originalHash := credential.PasswordHash

	// A restart with changed bootstrap values must not reset the initialized
	// account. Run the individual idempotent steps against the same pool.
	if _, err := database.EnsureBootstrapAdmin(ctx, pool, "", ""); err != nil {
		t.Fatal(err)
	}
	credential, err = repository.GetBootstrapCredential(ctx, "bootstrap-admin")
	if err != nil {
		t.Fatal(err)
	}
	if credential.PasswordHash != originalHash {
		t.Fatal("existing bootstrap credential was reset")
	}

	if err := database.SeedApps(ctx, pool, nil); err == nil {
		t.Fatal("empty seed unexpectedly succeeded")
	}
	if err := database.VerifyEncryptionKey(ctx, pool, "abcdefghijklmnopqrstuvwxyzABCDEF"); err == nil {
		t.Fatal("mismatched encryption key unexpectedly succeeded")
	}

	suffix := uuid.NewString()
	user, err := repository.UpsertOIDCUser(ctx, OIDCUserInput{
		Subject: "integration:" + suffix, Username: "user-" + suffix,
		Email: "integration@example.test", DisplayName: "Integration User", Team: "QA",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.DeleteUser(context.Background(), user.ID) }()
	users, err := repository.ListUsers(ctx, UserListOptions{Query: suffix, Limit: 10})
	if err != nil || users.Total != 1 || users.Items[0].ID != user.ID {
		t.Fatalf("list users = %#v err=%v", users, err)
	}
	wildcard, err := repository.ListUsers(ctx, UserListOptions{Query: "%", Limit: 10})
	if err != nil || wildcard.Total != 0 {
		t.Fatalf("wildcard user search = %#v err=%v", wildcard, err)
	}
	user, err = repository.ReplaceUserRoles(ctx, user.ID, []string{"user", "contributor"})
	if err != nil {
		t.Fatal(err)
	}
	if len(user.Permissions) == 0 {
		t.Fatal("role assignment did not resolve permissions")
	}
	systemRole, err := repository.GetRoleByKey(ctx, "user")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpdateRole(ctx, systemRole.ID, RoleInput{
		Key: "renamed-user", Name: systemRole.Name, Description: systemRole.Description,
		Permissions: systemRole.Permissions,
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("system role key mutation error = %v", err)
	}
	permissionKey := "integration:" + suffix
	permission, err := repository.UpsertPermission(ctx, model.Permission{
		Key: permissionKey, Description: "Integration permission", Category: "integration", Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	roleKey := "integration-" + suffix
	role, err := repository.CreateRole(ctx, RoleInput{
		Key: roleKey, Name: "Integration Role", Permissions: []string{permission.Key},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.DeleteRole(context.Background(), role.ID) }()
	role.Name = "Updated Integration Role"
	role, err = repository.UpdateRole(ctx, role.ID, RoleInput{
		Key: role.Key, Name: role.Name, Description: "updated", Permissions: role.Permissions,
	})
	if err != nil || role.Name != "Updated Integration Role" {
		t.Fatalf("update role = %#v err=%v", role, err)
	}
	user, err = repository.ReplaceUserRoles(ctx, user.ID, []string{"user", "contributor", role.Key})
	if err != nil {
		t.Fatal(err)
	}
	foundPermission := false
	for _, value := range user.Permissions {
		foundPermission = foundPermission || value == permission.Key
	}
	if !foundPermission {
		t.Fatal("custom role permission was not resolved for user")
	}

	stateDigest := sha256.Sum256([]byte("state:" + suffix))
	if err := repository.CreateOIDCAuthRequest(ctx, OIDCAuthRequest{
		StateHash: stateDigest[:], Nonce: "nonce", Verifier: "verifier",
		ReturnTo: "/submit", ExpiresAt: time.Now().Add(time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	authRequest, err := repository.ConsumeOIDCAuthRequest(ctx, stateDigest[:])
	if err != nil || authRequest.ReturnTo != "/submit" {
		t.Fatalf("consume OIDC request = %#v err=%v", authRequest, err)
	}
	if _, err := repository.ConsumeOIDCAuthRequest(ctx, stateDigest[:]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("OIDC state was not single use: %v", err)
	}

	category, err := repository.CreateCategory(ctx, CategoryInput{
		Slug: "integration-" + suffix, Name: "Integration", Icon: "🧪", Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.DeleteCategory(context.Background(), category.ID) }()

	app, err := repository.CreateApp(ctx, &user.ID, model.AppInput{
		Name: "Integration App", Slug: "integration-app-" + suffix,
		Summary: "summary", Description: "description", ServiceURL: "https://service.example.test",
		CategoryID: category.ID.String(), SupportsAPI: true, Visibility: "public",
	}, model.AppStatusDraft)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.DeleteApp(context.Background(), app.ID) }()
	if loaded, err := repository.GetAppBySlug(ctx, app.Slug, true); err != nil || loaded.ID != app.ID {
		t.Fatalf("get app by slug = %#v err=%v", loaded, err)
	}

	workflowConfig, err := repository.GetWorkflowConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workflowConfig.Enabled = false
	workflowConfig.ReviewerRoles = []string{"reviewer"}
	if _, err := repository.UpdateWorkflowConfig(ctx, workflowConfig); err != nil {
		t.Fatal(err)
	}
	submitted, err := repository.SubmitApp(ctx, app.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if submitted.App.Status != model.AppStatusPublished || submitted.Review != nil {
		t.Fatalf("workflow-off submission = %#v", submitted)
	}
	listedApps, err := repository.ListApps(ctx, model.AppListOptions{
		Query: suffix, Category: category.Slug, IncludeAll: true, Limit: 10,
	})
	if err != nil || listedApps.Total != 1 || listedApps.Items[0].ID != app.ID {
		t.Fatalf("list apps = %#v err=%v", listedApps, err)
	}
	wildcardApps, err := repository.ListApps(ctx, model.AppListOptions{
		Query: "%", Category: category.Slug, IncludeAll: true, Limit: 10,
	})
	if err != nil || wildcardApps.Total != 0 {
		t.Fatalf("wildcard app search = %#v err=%v", wildcardApps, err)
	}
	if err := repository.AddFavorite(ctx, user.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	favorites, err := repository.ListFavorites(ctx, user.ID, 10, 0)
	if err != nil || favorites.Total != 1 || favorites.Items[0].ID != app.ID {
		t.Fatalf("favorites = %#v err=%v", favorites, err)
	}
	if err := repository.RemoveFavorite(ctx, user.ID, app.ID); err != nil {
		t.Fatal(err)
	}

	workflowConfig.Enabled = true
	workflowConfig.Levels = 2
	workflowConfig.AutoPublish = true
	if _, err := repository.UpdateWorkflowConfig(ctx, workflowConfig); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.SetAppStatus(ctx, app.ID, model.AppStatusDraft); err != nil {
		t.Fatal(err)
	}
	pending, err := repository.SubmitApp(ctx, app.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.App.Status != model.AppStatusPending || pending.Review == nil || pending.Review.Level != 1 {
		t.Fatalf("workflow-on submission = %#v", pending)
	}
	firstDecision, err := repository.DecideReview(ctx, pending.Review.ID, credential.User.ID, "approved", "")
	if err != nil {
		t.Fatal(err)
	}
	if firstDecision.NextReview == nil || firstDecision.NextReview.Level != 2 || firstDecision.AppStatus != model.AppStatusPending {
		t.Fatalf("first review decision = %#v", firstDecision)
	}
	finalDecision, err := repository.DecideReview(ctx, firstDecision.NextReview.ID, credential.User.ID, "approved", "")
	if err != nil || finalDecision.AppStatus != model.AppStatusPublished {
		t.Fatalf("final review decision = %#v err=%v", finalDecision, err)
	}
	if _, err := repository.SetAppStatus(ctx, app.ID, model.AppStatusDraft); err != nil {
		t.Fatal(err)
	}
	rejectedSubmission, err := repository.SubmitApp(ctx, app.ID, user.ID)
	if err != nil || rejectedSubmission.Review == nil {
		t.Fatalf("rejection submission = %#v err=%v", rejectedSubmission, err)
	}
	if _, err := repository.DecideReview(ctx, rejectedSubmission.Review.ID, user.ID, "approved", ""); !errors.Is(err, ErrForbidden) {
		t.Fatalf("self approval error = %v", err)
	}
	rejectedDecision, err := repository.DecideReview(ctx, rejectedSubmission.Review.ID, credential.User.ID, "rejected", "needs changes")
	if err != nil || rejectedDecision.AppStatus != model.AppStatusRejected || rejectedDecision.Review.Reason != "needs changes" {
		t.Fatalf("rejected review decision = %#v err=%v", rejectedDecision, err)
	}

	keyDigest := sha256.Sum256([]byte("key:" + suffix))
	hash := keyDigest[:]
	key, err := repository.CreateAPIKey(ctx, CreateAPIKeyParams{
		UserID: user.ID, Name: "integration", Prefix: "aps_test", Hash: hash,
		Permissions: []string{"apps:read", "mcp:read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	keyCredential, err := repository.GetAPIKeyCredentialByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	if keyCredential.Key.ID != key.ID || keyCredential.UserID != user.ID {
		t.Fatalf("API key credential mismatch: %#v", keyCredential)
	}
	rotatedDigest := sha256.Sum256([]byte("rotated-key:" + suffix))
	rotated, err := repository.RotateAPIKey(ctx, key.ID, CreateAPIKeyParams{
		UserID: user.ID, Name: "rotated", Prefix: "aps_rotated", Hash: rotatedDigest[:],
		Permissions: []string{"apps:read", "mcp:read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.NewKey.RotatedFrom == nil || *rotated.NewKey.RotatedFrom != key.ID || rotated.OldKey.ExpiresAt == nil {
		t.Fatalf("API key rotation = %#v", rotated)
	}
	if err := repository.RevokeAPIKey(ctx, user.ID, rotated.NewKey.ID); err != nil {
		t.Fatal(err)
	}

	definitions, err := repository.ListKeyPermissionDefinitions(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	var mcpReadDefinition model.KeyPermissionDefinition
	for _, definition := range definitions {
		if definition.Key == "mcp:read" {
			mcpReadDefinition = definition
			break
		}
	}
	if mcpReadDefinition.Key == "" {
		t.Fatal("mcp:read key permission definition is missing")
	}
	defer func() {
		mcpReadDefinition.Active = true
		_, _ = repository.UpsertKeyPermissionDefinition(context.Background(), mcpReadDefinition)
	}()
	mcpReadDefinition.Active = false
	if _, err := repository.UpsertKeyPermissionDefinition(ctx, mcpReadDefinition); err != nil {
		t.Fatal(err)
	}
	filteredCredential, err := repository.GetAPIKeyCredentialByHash(ctx, hash)
	if err != nil {
		t.Fatal(err)
	}
	hasAppsRead, hasMCPRead := false, false
	for _, value := range filteredCredential.Permissions {
		hasAppsRead = hasAppsRead || value == "apps:read"
		hasMCPRead = hasMCPRead || value == "mcp:read"
	}
	if !hasAppsRead || hasMCPRead {
		t.Fatalf("inactive key permission was not filtered: %#v", filteredCredential.Permissions)
	}
	mcpReadDefinition.Active = true
	if _, err := repository.UpsertKeyPermissionDefinition(ctx, mcpReadDefinition); err != nil {
		t.Fatal(err)
	}

	originalPolicy, err := repository.GetKeyPolicy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = repository.UpdateKeyPolicy(context.Background(), originalPolicy) }()
	policyDigest := sha256.Sum256([]byte("policy-key:" + suffix))
	policyKey, err := repository.CreateAPIKey(ctx, CreateAPIKeyParams{
		UserID: user.ID, Name: "policy", Prefix: "aps_policy", Hash: policyDigest[:],
		Permissions: []string{"apps:read"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = repository.RevokeAPIKey(context.Background(), user.ID, policyKey.ID) }()
	if _, err := repository.GetAPIKeyCredentialByHash(ctx, policyDigest[:]); err != nil {
		t.Fatalf("fresh policy test key was not accepted: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE api_keys SET created_at = now() - interval '2 days', last_used_at = NULL WHERE id = $1`, policyKey.ID); err != nil {
		t.Fatal(err)
	}
	unusedPolicy := originalPolicy
	unusedPolicy.ExpireUnused = true
	unusedPolicy.UnusedExpiryDays = 1
	unusedPolicy.ForceRotation = false
	if _, err := repository.UpdateKeyPolicy(ctx, unusedPolicy); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetAPIKeyCredentialByHash(ctx, policyDigest[:]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unused key policy did not reject stale key: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE api_keys SET created_at = now() - interval '2 days', last_used_at = now() WHERE id = $1`, policyKey.ID); err != nil {
		t.Fatal(err)
	}
	rotationPolicy := originalPolicy
	rotationPolicy.ExpireUnused = false
	rotationPolicy.ForceRotation = true
	rotationPolicy.ForceRotationDays = 1
	if _, err := repository.UpdateKeyPolicy(ctx, rotationPolicy); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetAPIKeyCredentialByHash(ctx, policyDigest[:]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("forced rotation policy did not reject stale key: %v", err)
	}
	if _, err := repository.UpdateKeyPolicy(ctx, originalPolicy); err != nil {
		t.Fatal(err)
	}

	keyPermission, err := repository.UpsertKeyPermissionDefinition(ctx, model.KeyPermissionDefinition{
		Key: "integration:key:" + suffix, Name: "Integration Key Permission", Active: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	template, err := repository.CreateKeyPermissionTemplate(ctx, model.KeyPermissionTemplate{
		Name: "Integration Template " + suffix, Permissions: []string{keyPermission.Key},
	})
	if err != nil {
		t.Fatal(err)
	}
	template.Description = "updated"
	template, err = repository.UpdateKeyPermissionTemplate(ctx, template)
	if err != nil || template.Description != "updated" {
		t.Fatalf("key permission template = %#v err=%v", template, err)
	}
	if err := repository.DeleteKeyPermissionTemplate(ctx, template.ID); err != nil {
		t.Fatal(err)
	}

	sessionDigest := sha256.Sum256([]byte("session:" + suffix))
	csrfDigest := sha256.Sum256([]byte("csrf:" + suffix))
	sessionHash := sessionDigest[:]
	session, err := repository.CreateSession(ctx, CreateSessionParams{
		UserID: user.ID, TokenHash: sessionHash, CSRFHash: csrfDigest[:],
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	loadedSession, loadedUser, err := repository.GetSessionByTokenHash(ctx, sessionHash)
	if err != nil {
		t.Fatal(err)
	}
	if loadedSession.ID != session.ID || loadedUser.ID != user.ID {
		t.Fatal("session round trip mismatch")
	}
	if err := repository.TouchSession(ctx, sessionHash, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteSessionByTokenHash(ctx, sessionHash); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repository.GetSessionByTokenHash(ctx, sessionHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted session lookup error = %v", err)
	}

	systemSettings, err := repository.GetSystemSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	systemSettings.SiteURL = "https://appstore.example.test"
	if _, err := repository.UpdateSystemSettings(ctx, systemSettings, &credential.User.ID); err != nil {
		t.Fatal(err)
	}
	apiSettings, err := repository.GetAPISettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	apiSettings.Anonymous = false
	if _, err := repository.UpdateAPISettings(ctx, apiSettings, &credential.User.ID); err != nil {
		t.Fatal(err)
	}
	mcpSettings, err := repository.GetMCPSettings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	mcpSettings.Anonymous = false
	if _, err := repository.UpdateMCPSettings(ctx, mcpSettings, &credential.User.ID); err != nil {
		t.Fatal(err)
	}
	oidcSecret := "enc:v1:integration-ciphertext"
	oidcSettings := model.OIDCSettings{
		Enabled: true, IssuerURL: "https://sso.example.test/realms/company", ClientID: "appstore",
		RoleClaimPath: "realm_access.roles", GroupClaimPath: "groups",
		RoleMappings:  map[string][]string{"appstore-admin": {"admin"}},
		GroupMappings: map[string][]string{}, Scopes: []string{"openid", "profile"},
	}
	storedOIDC, err := repository.UpdateOIDCSettings(ctx, oidcSettings, &oidcSecret, &credential.User.ID)
	if err != nil || storedOIDC.ClientSecret != oidcSecret || !storedOIDC.ClientSecretSet {
		t.Fatalf("OIDC settings = %#v err=%v", storedOIDC, err)
	}
	storedOIDC, err = repository.UpdateOIDCSettings(ctx, storedOIDC, nil, &credential.User.ID)
	if err != nil || storedOIDC.ClientSecret != oidcSecret {
		t.Fatalf("OIDC secret was not preserved = %#v err=%v", storedOIDC, err)
	}

	provider, err := repository.CreateAIProvider(ctx, model.AIProvider{
		Name: "integration-" + suffix, Kind: "openai_compatible",
		BaseURL: "http://ai.example.test/v1", DefaultModel: "model",
		ContextWindow: 262144, MaxInputTokens: 200000, MaxOutputTokens: 62144,
		Temperature: 0.7, TimeoutSeconds: 120, Retries: 1, Streaming: true, Enabled: true,
	}, "enc:v1:integration-api-key")
	if err != nil {
		t.Fatal(err)
	}
	provider.MaxInputTokens = 0
	provider.MaxOutputTokens = 262144
	updatedProvider, err := repository.UpdateAIProvider(ctx, provider, nil)
	if err != nil || updatedProvider.APIKey == "" || updatedProvider.MaxOutputTokens != 262144 {
		t.Fatalf("updated AI provider = %#v err=%v", updatedProvider, err)
	}
	providers, err := repository.ListAIProviders(ctx)
	if err != nil {
		t.Fatal(err)
	}
	foundMaskedProvider := false
	for _, item := range providers {
		if item.ID == provider.ID {
			foundMaskedProvider = item.APIKey == "" && item.APIKeySet
		}
	}
	if !foundMaskedProvider {
		t.Fatal("AI provider list did not suppress encrypted API key")
	}
	aiModel, err := repository.UpsertAIModel(ctx, model.AIModel{
		ProviderID: provider.ID, Name: "integration-model", ContextWindow: 262144,
		MaxInputTokens: 200000, MaxOutputTokens: 62144, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	models, err := repository.ListAIModels(ctx, provider.ID)
	if err != nil || len(models) != 1 || models[0].ID != aiModel.ID {
		t.Fatalf("AI models = %#v err=%v", models, err)
	}
	if err := repository.DeleteAIModel(ctx, aiModel.ID); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteAIProvider(ctx, provider.ID); err != nil {
		t.Fatal(err)
	}

	auditEntry, err := repository.AppendAudit(ctx, model.AuditLog{
		ActorID: &user.ID, Actor: user.Username, Action: "integration.test",
		Resource: "app", ResourceID: app.ID.String(), RequestID: suffix,
	})
	if err != nil || auditEntry.ID == 0 {
		t.Fatalf("append audit: %#v, %v", auditEntry, err)
	}
	logs, err := repository.ListAuditLogs(ctx, AuditListOptions{RequestID: suffix, Limit: 10})
	if err != nil || logs.Total != 1 {
		t.Fatalf("audit logs total=%d err=%v", logs.Total, err)
	}

	removedUser, err := repository.UpsertOIDCUser(ctx, OIDCUserInput{
		Subject: "removed:" + suffix, Username: "removed-" + suffix,
		Email: "removed@example.test", DisplayName: "Removed User", Team: "Former Team",
	})
	if err != nil {
		t.Fatal(err)
	}
	removedSessionDigest := sha256.Sum256([]byte("removed-session:" + suffix))
	removedCSRF := sha256.Sum256([]byte("removed-csrf:" + suffix))
	if _, err := repository.CreateSession(ctx, CreateSessionParams{
		UserID: removedUser.ID, TokenHash: removedSessionDigest[:], CSRFHash: removedCSRF[:],
		ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	removedKeyDigest := sha256.Sum256([]byte("removed-key:" + suffix))
	if _, err := repository.CreateAPIKey(ctx, CreateAPIKeyParams{
		UserID: removedUser.ID, Name: "removed", Prefix: "aps_removed",
		Hash: removedKeyDigest[:], Permissions: []string{"apps:read"},
	}); err != nil {
		t.Fatal(err)
	}
	removedAuditRequestID := "removed-audit:" + suffix
	if _, err := repository.AppendAudit(ctx, model.AuditLog{
		ActorID: &removedUser.ID, Actor: removedUser.Username, Action: "integration.removed",
		Resource: "user", ResourceID: removedUser.ID.String(), RequestID: removedAuditRequestID,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.DeleteUser(ctx, removedUser.ID); err != nil {
		t.Fatal(err)
	}
	retainedUser, err := repository.GetUserByID(ctx, removedUser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retainedUser.Active || retainedUser.Email != "" || retainedUser.Team != "" {
		t.Fatalf("soft-deleted user was not sanitized: %#v", retainedUser)
	}
	if _, _, err := repository.GetSessionByTokenHash(ctx, removedSessionDigest[:]); !errors.Is(err, ErrNotFound) {
		t.Fatalf("soft-deleted user's session remains usable: %v", err)
	}
	if _, err := repository.GetAPIKeyCredentialByHash(ctx, removedKeyDigest[:]); !errors.Is(err, ErrForbidden) {
		t.Fatalf("soft-deleted user's API key error = %v, want ErrForbidden", err)
	}
	newSessionDigest := sha256.Sum256([]byte("removed-new-session:" + suffix))
	if _, err := repository.CreateSession(ctx, CreateSessionParams{
		UserID: removedUser.ID, TokenHash: newSessionDigest[:], CSRFHash: removedCSRF[:],
		ExpiresAt: time.Now().Add(time.Hour),
	}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("inactive user session creation error = %v, want ErrForbidden", err)
	}
	retainedAudit, err := repository.ListAuditLogs(ctx, AuditListOptions{RequestID: removedAuditRequestID, Limit: 10})
	if err != nil || retainedAudit.Total != 1 || retainedAudit.Items[0].ActorID == nil || *retainedAudit.Items[0].ActorID != removedUser.ID {
		t.Fatalf("soft-delete did not retain audit identity: %#v err=%v", retainedAudit, err)
	}
	if err := repository.DeleteUser(ctx, credential.User.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("bootstrap user deletion error = %v, want ErrForbidden", err)
	}
	if err := repository.DeleteUser(ctx, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user deletion error = %v, want ErrNotFound", err)
	}
}
