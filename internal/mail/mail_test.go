package mail

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeRelay speaks just enough SMTP to accept one message per session, the
// way an internal relay on port 25 does: no TLS, and credentials only when
// the test says so.
type fakeRelay struct {
	listener net.Listener
	auth     bool
	mu       sync.Mutex
	messages []string
	logins   []string
}

func newFakeRelay(t *testing.T, auth bool) *fakeRelay {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	relay := &fakeRelay{listener: listener, auth: auth}
	go relay.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return relay
}

func (f *fakeRelay) serve() {
	for {
		connection, err := f.listener.Accept()
		if err != nil {
			return
		}
		go f.session(connection)
	}
}

func (f *fakeRelay) session(connection net.Conn) {
	defer func() { _ = connection.Close() }()
	reader := bufio.NewReader(connection)
	write := func(line string) { _, _ = connection.Write([]byte(line + "\r\n")) }
	write("220 relay.corp.example ESMTP")
	var data strings.Builder
	inData := false
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				f.mu.Lock()
				f.messages = append(f.messages, data.String())
				f.mu.Unlock()
				data.Reset()
				write("250 queued")
				continue
			}
			// Undo the dot-stuffing a DATA writer applies.
			data.WriteString(strings.TrimPrefix(line, ".") + "\n")
			continue
		}
		command := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(command, "EHLO"):
			if f.auth {
				write("250-relay.corp.example")
				write("250 AUTH PLAIN LOGIN")
			} else {
				write("250 relay.corp.example")
			}
		case strings.HasPrefix(command, "AUTH PLAIN"):
			f.mu.Lock()
			f.logins = append(f.logins, strings.TrimPrefix(line, "AUTH PLAIN "))
			f.mu.Unlock()
			write("235 ok")
		case strings.HasPrefix(command, "MAIL"), strings.HasPrefix(command, "RCPT"):
			write("250 ok")
		case command == "DATA":
			inData = true
			write("354 go ahead")
		case command == "QUIT":
			write("221 bye")
			return
		default:
			write("500 unknown")
		}
	}
}

func (f *fakeRelay) config() Config {
	address := f.listener.Addr().(*net.TCPAddr)
	return Config{Enabled: true, SMTPHost: address.IP.String(), SMTPPort: address.Port, Security: SecurityNone,
		FromAddress: "appstore@corp.example", FromName: "앱스토어", TimeoutSeconds: 5}
}

func (f *fakeRelay) received() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.messages...)
}

func TestDeliverThroughPlainRelay(t *testing.T) {
	relay := newFakeRelay(t, false)
	config := relay.config()
	err := Deliver(context.Background(), config, Message{To: "owner@corp.example", Subject: "검토 요청", Body: "첫 줄\n.둘째 줄은 점으로 시작"})
	if err != nil {
		t.Fatal(err)
	}
	messages := relay.received()
	if len(messages) != 1 {
		t.Fatalf("expected one message, got %d", len(messages))
	}
	message := messages[0]
	for _, want := range []string{
		"To: owner@corp.example\n",
		"From: =?utf-8?q?=EC=95=B1=EC=8A=A4=ED=86=A0=EC=96=B4?= <appstore@corp.example>\n",
		"Subject: =?utf-8?q?=EA=B2=80=ED=86=A0_=EC=9A=94=EC=B2=AD?=\n",
		"Auto-Submitted: auto-generated\n",
		"X-AppStore-Notification: 1\n",
		"\n첫 줄\n.둘째 줄은 점으로 시작\n",
	} {
		if !strings.Contains(message, want) {
			t.Errorf("message lacks %q:\n%s", want, message)
		}
	}
}

func TestDeliverAuthenticatesOnlyWhenAsked(t *testing.T) {
	relay := newFakeRelay(t, true)
	config := relay.config()
	if err := Deliver(context.Background(), config, Message{To: "a@corp.example", Subject: "x", Body: "y"}); err != nil {
		t.Fatal(err)
	}
	relay.mu.Lock()
	logins := len(relay.logins)
	relay.mu.Unlock()
	if logins != 0 {
		t.Fatalf("an empty username must not authenticate, got %d AUTH commands", logins)
	}
	config.Username, config.Password = "notifier", "secret"
	if err := Deliver(context.Background(), config, Message{To: "a@corp.example", Subject: "x", Body: "y"}); err != nil {
		t.Fatal(err)
	}
	relay.mu.Lock()
	logins = len(relay.logins)
	relay.mu.Unlock()
	if logins != 1 {
		t.Fatalf("expected one AUTH PLAIN, got %d", logins)
	}
}

func TestDeliverRefusesUnusableConfiguration(t *testing.T) {
	err := Deliver(context.Background(), Config{Enabled: true, FromAddress: "a@b"}, Message{To: "x@y", Subject: "s"})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "SMTP") {
		t.Fatalf("expected a configuration error naming the host, got %v", err)
	}
}

func TestConfigFromValuesDefaultsAndRoundTrip(t *testing.T) {
	config := ConfigFromValues(map[string]any{})
	if config.Enabled || config.SMTPPort != 25 || config.Security != SecurityAuto || config.TimeoutSeconds != 10 ||
		config.FromName != "AppStore" || config.PasswordSet {
		t.Fatalf("unexpected defaults: %+v", config)
	}
	for _, event := range Events {
		if !config.Allows(event.Name) {
			t.Fatalf("event %s must default to on", event.Name)
		}
	}
	values := map[string]any{
		KeyEnabled: true, KeyHost: " relay.corp.example ", KeyPort: float64(465), KeyUsername: "u", KeyPassword: "ciphertext",
		KeyFromAddress: "noreply@corp.example", KeyBaseURL: "https://apps.corp.example/",
		"mail.notify_review_decided": false,
	}
	config = ConfigFromValues(values)
	if !config.Enabled || config.SMTPHost != "relay.corp.example" || config.SMTPPort != 465 || config.Security != SecurityTLS ||
		!config.PasswordSet || config.Password != "ciphertext" || config.BaseURL != "https://apps.corp.example" {
		t.Fatalf("unexpected parse: %+v", config)
	}
	if config.Allows(EventReviewDecided) || !config.Allows(EventReviewRequested) {
		t.Fatalf("event switch was not read: %+v", config.Events)
	}
	stored := config.Values()
	if _, leaked := stored[KeyPassword]; leaked {
		t.Fatal("Values must never carry the password")
	}
	if stored[KeyHost] != "relay.corp.example" || stored[KeyPort] != 465 || stored["mail.notify_review_decided"] != false {
		t.Fatalf("unexpected stored values: %+v", stored)
	}
	again := ConfigFromValues(stored)
	if again.SMTPHost != config.SMTPHost || again.Security != config.Security || again.Allows(EventReviewDecided) {
		t.Fatalf("round trip changed the configuration: %+v", again)
	}
}

func TestConfigSerializationOmitsPassword(t *testing.T) {
	config := Config{Password: "plain", PasswordSet: true}
	encoded, err := json.Marshal(config.Public())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "plain") || !strings.Contains(string(encoded), `"passwordSet":true`) {
		t.Fatalf("response must say only that a password is set: %s", encoded)
	}
	encoded, _ = json.Marshal(config)
	if strings.Contains(string(encoded), "plain") {
		t.Fatalf("even the raw config must not serialize the password: %s", encoded)
	}
}

func TestValidate(t *testing.T) {
	base := Config{Enabled: true, SMTPHost: "relay", SMTPPort: 25, Security: "auto", FromAddress: "a@b.c", TimeoutSeconds: 10}
	cases := []struct {
		name   string
		mutate func(*Config)
		field  string
	}{
		{"valid", func(*Config) {}, ""},
		{"disabled without host", func(c *Config) { c.Enabled = false; c.SMTPHost = "" }, ""},
		{"enabled without host", func(c *Config) { c.SMTPHost = "" }, "smtpHost"},
		{"enabled without sender", func(c *Config) { c.FromAddress = "" }, "fromAddress"},
		{"bad sender", func(c *Config) { c.FromAddress = "not-an-address" }, "fromAddress"},
		{"bad port even when off", func(c *Config) { c.Enabled = false; c.SMTPPort = 70000 }, "smtpPort"},
		{"bad security", func(c *Config) { c.Security = "ssl" }, "security"},
		{"bad timeout", func(c *Config) { c.TimeoutSeconds = 600 }, "timeoutSeconds"},
		{"bad base url", func(c *Config) { c.BaseURL = "apps.corp.example" }, "baseUrl"},
		{"host with path", func(c *Config) { c.SMTPHost = "relay/evil" }, "smtpHost"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			config := base
			testCase.mutate(&config)
			err := config.Validate()
			var field *FieldError
			switch {
			case testCase.field == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case testCase.field != "" && (!errors.As(err, &field) || field.Field != testCase.field):
				t.Fatalf("expected field %q, got %v", testCase.field, err)
			}
		})
	}
}

// fakeStore stands in for the repository: settings in memory, a delivery
// log, and a directory of two people.
type fakeStore struct {
	mu         sync.Mutex
	config     Config
	err        error
	emails     map[uuid.UUID]string
	deliveries []Delivery
}

func (f *fakeStore) GetMailSettings(context.Context) (Config, error) { return f.config, f.err }
func (f *fakeStore) LookupUserEmails(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]string, error) {
	result := map[uuid.UUID]string{}
	for _, id := range ids {
		if email, ok := f.emails[id]; ok {
			result[id] = email
		}
	}
	return result, nil
}
func (f *fakeStore) RecordMailDelivery(_ context.Context, delivery Delivery) (uuid.UUID, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delivery.ID = uuid.New()
	f.deliveries = append(f.deliveries, delivery)
	return delivery.ID, nil
}
func (f *fakeStore) FinishMailDelivery(_ context.Context, id uuid.UUID, status string, attempts int, message string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for index := range f.deliveries {
		if f.deliveries[index].ID == id {
			f.deliveries[index].Status, f.deliveries[index].Attempts, f.deliveries[index].ErrorMessage = status, attempts, message
			return nil
		}
	}
	return errors.New("no such delivery")
}
func (f *fakeStore) log() []Delivery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Delivery{}, f.deliveries...)
}

var (
	owner    = uuid.MustParse("00000000-0000-0000-0000-000000000001")
	reviewer = uuid.MustParse("00000000-0000-0000-0000-000000000002")
	noEmail  = uuid.MustParse("00000000-0000-0000-0000-000000000003")
)

func newFakeStore(config Config) *fakeStore {
	return &fakeStore{config: config, emails: map[uuid.UUID]string{owner: "owner@corp.example", reviewer: "reviewer@corp.example"}}
}

func TestNotifySendsNothingWhileDisabled(t *testing.T) {
	store := newFakeStore(Config{SMTPHost: "relay", FromAddress: "a@b.c"})
	sent := 0
	service := NewService(store, nil, nil)
	service.SetSender(func(context.Context, Config, Message) error { sent++; return nil })
	service.Notify(context.Background(), TestMessage(), nil, []uuid.UUID{owner})
	if sent != 0 || len(store.log()) != 0 {
		t.Fatalf("disabled mail must neither send nor record, got %d sent and %d records", sent, len(store.log()))
	}
}

func TestNotifyRecordsWhyNothingLeftWhenUnconfigured(t *testing.T) {
	store := newFakeStore(Config{Enabled: true})
	service := NewService(store, nil, nil)
	service.SetSender(func(context.Context, Config, Message) error {
		t.Error("an unconfigured relay must not be dialled")
		return nil
	})
	service.Notify(context.Background(), TestMessage(), nil, []uuid.UUID{owner})
	log := store.log()
	if len(log) != 1 || log[0].Status != StatusFailed || !strings.Contains(log[0].ErrorMessage, "SMTP") {
		t.Fatalf("expected one failed record naming the missing host, got %+v", log)
	}
}

func TestNotifyRunsInBackgroundAndRecordsFailure(t *testing.T) {
	retryDelay = 10 * time.Millisecond
	defer func() { retryDelay = 2 * time.Second }()
	store := newFakeStore(Config{Enabled: true, SMTPHost: "relay", FromAddress: "a@b.c"})
	release := make(chan struct{})
	var attempts int
	var mu sync.Mutex
	service := NewService(store, nil, nil)
	var group sync.WaitGroup
	group.Add(1)
	service.wait = group.Done
	service.SetSender(func(context.Context, Config, Message) error {
		<-release
		mu.Lock()
		defer mu.Unlock()
		attempts++
		return errors.New("connection refused")
	})
	done := make(chan struct{})
	go func() {
		service.Notify(context.Background(), TestMessage(), nil, []uuid.UUID{owner})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Notify must return before the relay answers")
	}
	close(release)
	group.Wait()
	log := store.log()
	if len(log) != 1 || log[0].Status != StatusFailed || log[0].Attempts != 2 || log[0].ErrorMessage != "connection refused" {
		t.Fatalf("expected a failed record after two attempts, got %+v", log)
	}
}

func TestNotifyDropsActorMissingAddressesAndDuplicates(t *testing.T) {
	store := newFakeStore(Config{Enabled: true, SMTPHost: "relay", FromAddress: "a@b.c"})
	var mu sync.Mutex
	var recipients []string
	var group sync.WaitGroup
	service := NewService(store, nil, nil)
	service.wait = group.Done
	service.SetSender(func(_ context.Context, _ Config, message Message) error {
		mu.Lock()
		defer mu.Unlock()
		recipients = append(recipients, message.To)
		return nil
	})
	group.Add(1)
	actor := owner
	service.Notify(context.Background(), TestMessage(), &actor, []uuid.UUID{owner, reviewer, reviewer, noEmail})
	group.Wait()
	if len(recipients) != 1 || recipients[0] != "reviewer@corp.example" {
		t.Fatalf("expected only the reviewer, got %v", recipients)
	}
	log := store.log()
	if len(log) != 1 || log[0].Status != StatusSent || log[0].Attempts != 1 || log[0].ActorID == nil || *log[0].ActorID != owner {
		t.Fatalf("expected one sent record naming the actor, got %+v", log)
	}
}

func TestNotifyHonoursEventSwitch(t *testing.T) {
	config := Config{Enabled: true, SMTPHost: "relay", FromAddress: "a@b.c", Events: map[string]bool{EventReviewDecided: false}}
	store := newFakeStore(config.Normalize())
	var group sync.WaitGroup
	sent := map[string]int{}
	var mu sync.Mutex
	service := NewService(store, nil, nil)
	service.wait = group.Done
	service.SetSender(func(_ context.Context, _ Config, message Message) error {
		mu.Lock()
		defer mu.Unlock()
		sent[message.Subject]++
		return nil
	})
	service.Notify(context.Background(), ReviewDecided("검토자", "앱", "id", "slug", false, false, "사유"), nil, []uuid.UUID{owner})
	group.Add(1)
	service.Notify(context.Background(), ReviewRequested("등록자", "앱", "id", 1, 1), nil, []uuid.UUID{reviewer})
	group.Wait()
	if len(sent) != 1 || sent["[AppStore] '앱' 앱이 검토를 기다립니다"] != 1 {
		t.Fatalf("only the switched-on event must go out, got %v", sent)
	}
	if len(store.log()) != 1 {
		t.Fatalf("a switched-off event must not be recorded either, got %+v", store.log())
	}
}

func TestSendNowReportsOutcomeAndRecords(t *testing.T) {
	store := newFakeStore(Config{SMTPHost: "relay", FromAddress: "a@b.c"})
	service := NewService(store, nil, nil)
	service.SetSender(func(context.Context, Config, Message) error { return nil })
	if err := service.SendNow(context.Background(), TestMessage(), nil, "x@y.z"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
	store.config.Enabled = true
	if err := service.SendNow(context.Background(), TestMessage(), nil, "x@y.z"); err != nil {
		t.Fatal(err)
	}
	service.SetSender(func(context.Context, Config, Message) error { return errors.New("550 relay denied") })
	if err := service.SendNow(context.Background(), TestMessage(), nil, "x@y.z"); err == nil {
		t.Fatal("expected the relay's error")
	}
	log := store.log()
	if len(log) != 2 || log[0].Status != StatusSent || log[1].Status != StatusFailed || log[1].ErrorMessage != "550 relay denied" {
		t.Fatalf("expected both outcomes recorded, got %+v", log)
	}
}

func TestServiceDecryptsStoredPassword(t *testing.T) {
	store := newFakeStore(Config{Enabled: true, SMTPHost: "relay", FromAddress: "a@b.c", Username: "u", Password: "enc:secret"})
	service := NewService(store, func(value string) (string, error) { return strings.TrimPrefix(value, "enc:"), nil }, nil)
	var seen string
	service.SetSender(func(_ context.Context, config Config, _ Message) error { seen = config.Password; return nil })
	if err := service.SendNow(context.Background(), TestMessage(), nil, "x@y.z"); err != nil {
		t.Fatal(err)
	}
	if seen != "secret" {
		t.Fatalf("the transport must see the plaintext, got %q", seen)
	}
}

func TestRenderAppendsLinkAndFooter(t *testing.T) {
	config := Config{BaseURL: "https://apps.corp.example/"}
	body := ReviewRequested("홍길동", "Release Radar", "app-1", 2, 3).Render(config)
	for _, want := range []string{"홍길동 님이 등록한 'Release Radar' 앱이 검토 대기에 들어갔습니다 (2/3단계).", "바로 열기: https://apps.corp.example/review", "자동으로 발송되었습니다"} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %q:\n%s", want, body)
		}
	}
	if strings.Contains(ReviewDecided("검토자", "앱", "id", "slug", false, false, "").Render(Config{}), "바로 열기") {
		t.Error("without a base URL no link may be rendered")
	}
	rejected := ReviewDecided("검토자", "앱", "id", "slug", false, false, "설명이 짧습니다\n스크린샷도 없습니다").Render(Config{})
	if !strings.Contains(rejected, "> 설명이 짧습니다\n> 스크린샷도 없습니다") {
		t.Errorf("rejection reason must be quoted:\n%s", rejected)
	}
	published := ReviewDecided("검토자", "앱", "id", "my-app", true, true, "")
	if published.Link != "/apps/my-app" || !strings.Contains(published.Subject, "게시되었습니다") {
		t.Errorf("unexpected approval notification: %+v", published)
	}
	changed := AppStatusChanged("관리자", "앱", "id", "slug", "published", "archived")
	if !strings.Contains(changed.Subject, "보관됨") || !strings.Contains(changed.Lines[0], "게시됨에서 보관됨") {
		t.Errorf("unexpected status notification: %+v", changed)
	}
}
