package httpapi

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	appauth "github.com/hkjang/appstore/internal/auth"
	"github.com/hkjang/appstore/internal/config"
	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/hkjang/appstore/internal/database"
	"github.com/hkjang/appstore/internal/mail"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

// testRelay accepts mail on a loopback port the way an internal relay on
// port 25 does — no TLS, no credentials — and keeps the RCPT and subject of
// each message.
type testRelay struct {
	listener net.Listener
	mu       sync.Mutex
	received []string
	subjects []string
	logins   []string
}

func newTestRelay(t *testing.T) *testRelay {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	relay := &testRelay{listener: listener}
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go relay.session(connection)
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	return relay
}

func (relay *testRelay) session(connection net.Conn) {
	defer func() { _ = connection.Close() }()
	reader := bufio.NewReader(connection)
	write := func(line string) { _, _ = connection.Write([]byte(line + "\r\n")) }
	write("220 relay ESMTP")
	inData, recipient, subject := false, "", ""
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case inData && line == ".":
			inData = false
			relay.mu.Lock()
			relay.received = append(relay.received, recipient)
			relay.subjects = append(relay.subjects, subject)
			relay.mu.Unlock()
			write("250 queued")
		case inData:
			if strings.HasPrefix(line, "Subject: ") {
				subject = strings.TrimPrefix(line, "Subject: ")
			}
		case strings.HasPrefix(strings.ToUpper(line), "EHLO"):
			write("250-relay")
			write("250 AUTH PLAIN")
		case strings.HasPrefix(strings.ToUpper(line), "AUTH PLAIN "):
			relay.mu.Lock()
			relay.logins = append(relay.logins, strings.TrimPrefix(line, "AUTH PLAIN "))
			relay.mu.Unlock()
			write("235 ok")
		case strings.HasPrefix(strings.ToUpper(line), "RCPT TO:"):
			recipient = strings.Trim(strings.TrimPrefix(line, "RCPT TO:"), "<> ")
			write("250 ok")
		case strings.HasPrefix(strings.ToUpper(line), "MAIL"):
			write("250 ok")
		case strings.ToUpper(line) == "DATA":
			inData = true
			write("354 go ahead")
		case strings.ToUpper(line) == "QUIT":
			write("221 bye")
			return
		default:
			write("500 unknown")
		}
	}
}

func (relay *testRelay) recipients() []string {
	relay.mu.Lock()
	defer relay.mu.Unlock()
	return append([]string{}, relay.received...)
}

// waitFor polls until the condition holds, because deliveries finish in the
// background after the request that caused them has already returned.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestPostgreSQLMailIntegration walks the mail standard end to end: the
// settings round trip never returns the password, the test button reaches a
// relay, a submission mails the reviewer and not the submitter, a decision
// mails the submitter and not the reviewer, an administrator's status change
// mails the owner, every attempt is in the log, and a dead relay costs the
// request nothing.
func TestPostgreSQLMailIntegration(t *testing.T) {
	dsn := os.Getenv("APPSTORE_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("APPSTORE_TEST_POSTGRES_DSN is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	// Leave the database as it was found: no mail rows, no log, and the
	// workflow switched back.
	originalWorkflow, err := repository.GetWorkflowConfig(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM system_settings WHERE key LIKE 'mail.%'`)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM mail_deliveries`)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM apps WHERE slug LIKE 'mail-it-%'`)
		_, _ = pool.Exec(cleanupCtx, `DELETE FROM users WHERE subject LIKE 'mail-it-%'`)
		_, _ = repository.UpdateWorkflowConfig(cleanupCtx, originalWorkflow)
	}
	cleanup()
	defer cleanup()

	service, err := New(repository, box, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := service.Handler()
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, target, body string, headers map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.RemoteAddr = "198.51.100.50:1050"
		for key, value := range headers {
			req.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	// sessionFor signs a person in the way the OIDC callback would, so
	// requests can be made as a reviewer or an owner.
	sessionFor := func(user model.User) map[string]string {
		material, err := appauth.NewSessionMaterial(box, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repository.CreateSession(ctx, store.CreateSessionParams{
			UserID: user.ID, TokenHash: material.TokenHash, CSRFHash: material.CSRFHash, ExpiresAt: material.ExpiresAt,
		}); err != nil {
			t.Fatal(err)
		}
		return map[string]string{
			"Content-Type": "application/json", "X-CSRF-Token": material.CSRF,
			"Cookie": appauth.SessionCookieName + "=" + material.Token + "; " + appauth.CSRFCookieName + "=" + material.CSRF,
		}
	}
	person := func(subject, email string, roles ...string) model.User {
		user, err := repository.UpsertOIDCUser(ctx, store.OIDCUserInput{Subject: subject, Username: subject, Email: email, DisplayName: strings.TrimPrefix(subject, "mail-it-"), Team: "platform"})
		if err != nil {
			t.Fatal(err)
		}
		if user, err = repository.ReplaceUserRoles(ctx, user.ID, roles); err != nil {
			t.Fatal(err)
		}
		return user
	}
	admin := sessionFor(person("mail-it-admin", "admin@corp.example", "super_admin"))
	reviewer := person("mail-it-reviewer", "reviewer@corp.example", "reviewer")
	reviewerSession := sessionFor(reviewer)
	owner := person("mail-it-owner", "owner@corp.example", "contributor")
	ownerSession := sessionFor(owner)
	person("mail-it-silent", "", "reviewer") // a reviewer without an address is skipped quietly

	// A fresh database: mail is off, nothing is configured, no password.
	response := request(http.MethodGet, "/api/v1/admin/mail", "", admin)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"enabled":false`) ||
		!strings.Contains(response.Body.String(), `"passwordSet":false`) || !strings.Contains(response.Body.String(), `"smtpPort":25`) {
		t.Fatalf("fresh mail settings = %d %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPost, "/api/v1/admin/mail/test", `{"recipient":"x@corp.example"}`, admin); response.Code != http.StatusConflict {
		t.Fatalf("test send while disabled = %d %s", response.Code, response.Body.String())
	}

	relay := newTestRelay(t)
	address := relay.listener.Addr().(*net.TCPAddr)
	settings := fmt.Sprintf(`{"enabled":true,"smtpHost":"%s","smtpPort":%d,"security":"none","fromAddress":"appstore@corp.example","fromName":"AppStore","baseUrl":"https://apps.corp.example","timeoutSeconds":5,"username":"notifier","password":"relay-secret","events":{}}`, address.IP, address.Port)
	response = request(http.MethodPut, "/api/v1/admin/mail", settings, admin)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), "relay-secret") || !strings.Contains(response.Body.String(), `"passwordSet":true`) {
		t.Fatalf("save mail settings = %d %s", response.Code, response.Body.String())
	}
	var stored string
	if err := pool.QueryRow(ctx, `SELECT value::text FROM system_settings WHERE key = 'mail.password'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, "relay-secret") {
		t.Fatalf("the password must be stored encrypted, got %s", stored)
	}
	var hostRow string
	if err := pool.QueryRow(ctx, `SELECT value::text FROM system_settings WHERE key = 'mail.smtp_host'`).Scan(&hostRow); err != nil || !strings.Contains(hostRow, address.IP.String()) {
		t.Fatalf("mail.smtp_host row = %q err=%v", hostRow, err)
	}
	// Saving again without a password keeps the one already set.
	response = request(http.MethodPut, "/api/v1/admin/mail", strings.Replace(settings, `"password":"relay-secret",`, "", 1), admin)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"passwordSet":true`) {
		t.Fatalf("resave without password = %d %s", response.Code, response.Body.String())
	}
	if response := request(http.MethodPut, "/api/v1/admin/mail", `{"enabled":true,"smtpPort":25}`, admin); response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "smtpHost") {
		t.Fatalf("enabling without a host = %d %s", response.Code, response.Body.String())
	}

	// The test button sends a real message and reports the outcome.
	if response := request(http.MethodPost, "/api/v1/admin/mail/test", `{"recipient":"ops@corp.example"}`, admin); response.Code != http.StatusOK {
		t.Fatalf("test send = %d %s", response.Code, response.Body.String())
	}
	if got := relay.recipients(); len(got) != 1 || got[0] != "ops@corp.example" {
		t.Fatalf("relay received %v", got)
	}
	// The relay saw the plaintext credentials, so the stored ciphertext was
	// opened on the way out.
	relay.mu.Lock()
	login, _ := base64.StdEncoding.DecodeString(relay.logins[0])
	relay.mu.Unlock()
	if string(login) != "\x00notifier\x00relay-secret" {
		t.Fatalf("relay saw credentials %q", login)
	}
	response = request(http.MethodGet, "/api/v1/admin/mail/deliveries", "", admin)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"event":"test"`) || !strings.Contains(response.Body.String(), `"status":"sent"`) {
		t.Fatalf("deliveries after test = %d %s", response.Code, response.Body.String())
	}

	// Workflow on, one level, auto publish: a submission is a reviewer's turn.
	workflow := originalWorkflow
	workflow.Enabled, workflow.Levels, workflow.AutoPublish, workflow.PreventSelfApproval = true, 1, true, false
	workflow.ReviewerRoles, workflow.TeamLeaderRoles = []string{"reviewer"}, []string{"team_leader"}
	if _, err := repository.UpdateWorkflowConfig(ctx, workflow); err != nil {
		t.Fatal(err)
	}
	var category uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT id FROM categories ORDER BY position, id LIMIT 1`).Scan(&category); err != nil {
		t.Fatal(err)
	}
	appBody := fmt.Sprintf(`{"name":"Mail IT App","slug":"mail-it-app","summary":"통합 테스트 앱","description":"메일 알림을 확인하는 앱입니다.","icon":"mail","serviceUrl":"https://mail-it.corp.example","categoryId":"%s","language":"Go","team":"platform","visibility":"public"}`, category)
	response = request(http.MethodPost, "/api/v1/apps", appBody, ownerSession)
	if response.Code != http.StatusCreated {
		t.Fatalf("create app = %d %s", response.Code, response.Body.String())
	}
	var created model.App
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "review request mail", func() bool { return len(relay.recipients()) == 2 })
	if got := relay.recipients(); got[1] != "reviewer@corp.example" {
		t.Fatalf("the reviewer, and only the reviewer, must be mailed; relay received %v", got)
	}

	// The reviewer rejects with a reason: the owner is told, the reviewer is not.
	var pending struct {
		Items []model.Review `json:"items"`
	}
	response = request(http.MethodGet, "/api/v1/reviews?status=pending", "", reviewerSession)
	if err := json.Unmarshal(response.Body.Bytes(), &pending); err != nil || len(pending.Items) == 0 {
		t.Fatalf("pending reviews = %d %s (%v)", response.Code, response.Body.String(), err)
	}
	var review model.Review
	for _, item := range pending.Items {
		if item.AppID == created.ID {
			review = item
		}
	}
	if review.ID == uuid.Nil {
		t.Fatalf("no pending review for the created app in %+v", pending.Items)
	}
	response = request(http.MethodPost, "/api/v1/reviews/"+review.ID.String()+"/reject", `{"reason":"스크린샷이 없습니다."}`, reviewerSession)
	if response.Code != http.StatusOK {
		t.Fatalf("reject = %d %s", response.Code, response.Body.String())
	}
	waitFor(t, "rejection mail", func() bool { return len(relay.recipients()) == 3 })
	if got := relay.recipients(); got[2] != "owner@corp.example" {
		t.Fatalf("the owner must be told about the rejection; relay received %v", got)
	}

	// An administrator archives the app: the owner is told again.
	response = request(http.MethodPut, "/api/v1/admin/apps/"+created.ID.String()+"/status", `{"status":"archived"}`, admin)
	if response.Code != http.StatusOK {
		t.Fatalf("admin status change = %d %s", response.Code, response.Body.String())
	}
	waitFor(t, "status change mail", func() bool { return len(relay.recipients()) == 4 })
	if got := relay.recipients(); got[3] != "owner@corp.example" {
		t.Fatalf("the owner must be told about the archive; relay received %v", got)
	}
	// The owner archiving their own app is their own action: no mail.
	response = request(http.MethodPut, "/api/v1/admin/apps/"+created.ID.String()+"/status", `{"status":"draft"}`, admin)
	if response.Code != http.StatusOK {
		t.Fatalf("admin status change back = %d %s", response.Code, response.Body.String())
	}
	waitFor(t, "draft mail", func() bool { return len(relay.recipients()) == 5 })
	response = request(http.MethodDelete, "/api/v1/apps/"+created.ID.String(), "", ownerSession)
	if response.Code != http.StatusNoContent {
		t.Fatalf("owner archive = %d %s", response.Code, response.Body.String())
	}

	// Every attempt is in the log, with the subject and no body.
	waitFor(t, "delivery log", func() bool {
		page, err := repository.ListMailDeliveries(ctx, "", 50)
		return err == nil && page.Status[mail.StatusSent] == 5 && page.Status[mail.StatusQueued] == 0
	})
	page, err := repository.ListMailDeliveries(ctx, mail.StatusSent, 50)
	if err != nil {
		t.Fatal(err)
	}
	events := map[string]int{}
	for _, item := range page.Items {
		events[item.Event]++
		if item.Event != mail.EventTest && item.Reference != created.ID.String() {
			t.Fatalf("delivery %+v does not reference the app", item)
		}
	}
	if events[mail.EventReviewRequested] != 1 || events[mail.EventReviewDecided] != 1 || events[mail.EventAppStatus] != 2 || events[mail.EventTest] != 1 {
		t.Fatalf("unexpected delivery events %v", events)
	}
	if len(relay.recipients()) != 5 {
		t.Fatalf("an owner's own action must not be mailed; relay received %v", relay.recipients())
	}

	// A dead relay: the request still succeeds and the log says why not.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	deadAddress := dead.Addr().(*net.TCPAddr)
	_ = dead.Close()
	deadSettings := fmt.Sprintf(`{"enabled":true,"smtpHost":"127.0.0.1","smtpPort":%d,"security":"none","fromAddress":"appstore@corp.example","timeoutSeconds":1,"events":{"review.decided":false}}`, deadAddress.Port)
	if response := request(http.MethodPut, "/api/v1/admin/mail", deadSettings, admin); response.Code != http.StatusOK {
		t.Fatalf("save dead relay = %d %s", response.Code, response.Body.String())
	}
	started := time.Now()
	response = request(http.MethodPost, "/api/v1/apps", strings.Replace(appBody, "mail-it-app", "mail-it-dead", 1), ownerSession)
	if response.Code != http.StatusCreated {
		t.Fatalf("create app with dead relay = %d %s", response.Code, response.Body.String())
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("the request waited on the relay: %s", elapsed)
	}
	waitFor(t, "failed delivery", func() bool {
		page, err := repository.ListMailDeliveries(ctx, mail.StatusFailed, 10)
		return err == nil && len(page.Items) == 1 && page.Items[0].Attempts == 2 && strings.Contains(page.Items[0].ErrorMessage, "SMTP 연결 실패")
	})
	if response := request(http.MethodPost, "/api/v1/admin/mail/test", `{"recipient":"ops@corp.example"}`, admin); response.Code != http.StatusBadGateway {
		t.Fatalf("test send against a dead relay = %d %s", response.Code, response.Body.String())
	}
	// The switched-off event kind stays silent even though the relay is set.
	response = request(http.MethodGet, "/api/v1/reviews?status=pending", "", reviewerSession)
	if err := json.Unmarshal(response.Body.Bytes(), &pending); err != nil {
		t.Fatal(err)
	}
	for _, item := range pending.Items {
		if item.AppSlug == "mail-it-dead" {
			review = item
		}
	}
	before, _ := repository.ListMailDeliveries(ctx, "", 50)
	if response := request(http.MethodPost, "/api/v1/reviews/"+review.ID.String()+"/approve", "", reviewerSession); response.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", response.Code, response.Body.String())
	}
	after, _ := repository.ListMailDeliveries(ctx, "", 50)
	if after.Total != before.Total {
		t.Fatalf("a switched-off event must not be recorded: %d -> %d", before.Total, after.Total)
	}
}
