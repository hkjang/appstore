// Package mail sends event notifications through a company SMTP relay.
//
// An internal relay usually listens on port 25 and accepts mail from the
// network with no credentials and no TLS, so authentication and encryption are
// optional here and the session upgrades only as far as the relay advertises.
// Nothing in this package holds up a request: notifications are sent in the
// background and every attempt is recorded so an administrator can answer
// "did it go out?" without reading the relay's logs.
package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

var (
	ErrDisabled = errors.New("mail is disabled")
	ErrInvalid  = errors.New("invalid mail configuration")
)

const (
	SecurityAuto     = "auto"
	SecurityNone     = "none"
	SecuritySTARTTLS = "starttls"
	SecurityTLS      = "tls"
)

// Securities lists the accepted values of mail.security.
var Securities = []string{SecurityAuto, SecurityNone, SecuritySTARTTLS, SecurityTLS}

// Config is the relay configuration an administrator edits. Password holds
// whatever the store gave us — the ciphertext at rest, the plaintext once the
// service has opened it — and is never serialized; PasswordSet is the only
// thing a response says about it.
type Config struct {
	Enabled        bool            `json:"enabled"`
	SMTPHost       string          `json:"smtpHost"`
	SMTPPort       int             `json:"smtpPort"`
	Security       string          `json:"security"`
	SkipTLSVerify  bool            `json:"skipTlsVerify"`
	Username       string          `json:"username"`
	Password       string          `json:"-"`
	PasswordSet    bool            `json:"passwordSet"`
	FromAddress    string          `json:"fromAddress"`
	FromName       string          `json:"fromName"`
	BaseURL        string          `json:"baseUrl"`
	TimeoutSeconds int             `json:"timeoutSeconds"`
	Events         map[string]bool `json:"events"`
}

// Address is the RFC 5322 From header value.
func (c Config) Address() string {
	from := strings.TrimSpace(c.FromAddress)
	if name := strings.TrimSpace(c.FromName); name != "" {
		return fmt.Sprintf("%s <%s>", name, from)
	}
	return from
}

// Timeout is the dial and session budget of one delivery.
func (c Config) Timeout() time.Duration {
	if c.TimeoutSeconds <= 0 {
		return defaultTimeout
	}
	return time.Duration(c.TimeoutSeconds) * time.Second
}

// Allows reports whether an event kind should be sent. Unknown kinds are
// sent, so adding a notification never needs a settings change first.
func (c Config) Allows(event string) bool {
	if enabled, known := c.Events[event]; known {
		return enabled
	}
	return true
}

func (c Config) endpoint() string { return net.JoinHostPort(c.SMTPHost, fmt.Sprint(c.SMTPPort)) }

// FieldError names the field an administrator has to fix.
type FieldError struct {
	Field   string
	Message string
}

func (e *FieldError) Error() string { return e.Message }

// Validate reports what is wrong with the relay settings. Ranges are checked
// even while mail is off so nothing unusable is stored; the relay address and
// sender are only demanded once mail is switched on, so a half-filled form
// can be saved and finished later.
func (c Config) Validate() error {
	c = c.Normalize()
	known := false
	for _, security := range Securities {
		if c.Security == security {
			known = true
		}
	}
	if !known {
		return &FieldError{"security", "보안 방식은 auto, none, starttls, tls 중 하나여야 합니다."}
	}
	if c.SMTPPort < 1 || c.SMTPPort > 65535 {
		return &FieldError{"smtpPort", "SMTP 포트는 1에서 65535 사이여야 합니다."}
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 120 {
		return &FieldError{"timeoutSeconds", "제한 시간은 1초에서 120초 사이여야 합니다."}
	}
	if len([]rune(c.SMTPHost)) > 253 || strings.ContainsAny(c.SMTPHost, " /\\@") {
		return &FieldError{"smtpHost", "SMTP 호스트는 호스트 이름이나 IP 주소여야 합니다."}
	}
	if c.FromAddress != "" && !validAddress(c.FromAddress) {
		return &FieldError{"fromAddress", "보내는 주소는 이메일 주소여야 합니다."}
	}
	if len([]rune(c.FromName)) > 100 || len([]rune(c.Username)) > 200 {
		return &FieldError{"fromName", "보내는 이름은 100자, 사용자 이름은 200자를 넘을 수 없습니다."}
	}
	if c.BaseURL != "" && !strings.HasPrefix(c.BaseURL, "http://") && !strings.HasPrefix(c.BaseURL, "https://") {
		return &FieldError{"baseUrl", "서비스 주소는 http(s):// 로 시작하는 URL이어야 합니다."}
	}
	if !c.Enabled {
		return nil
	}
	if c.SMTPHost == "" {
		return &FieldError{"smtpHost", "메일을 켜려면 SMTP 릴레이 주소를 입력하세요."}
	}
	if c.FromAddress == "" {
		return &FieldError{"fromAddress", "메일을 켜려면 보내는 주소를 입력하세요."}
	}
	return nil
}

// ready is the check a delivery makes: the same rules as Validate, but as one
// plain error the delivery record can carry.
func (c Config) ready() error {
	if err := c.Validate(); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, err.Error())
	}
	if strings.TrimSpace(c.SMTPHost) == "" {
		return fmt.Errorf("%w: mail.smtp_host is required", ErrInvalid)
	}
	if strings.TrimSpace(c.FromAddress) == "" {
		return fmt.Errorf("%w: mail.from_address is required", ErrInvalid)
	}
	return nil
}

// validAddress accepts what a relay will: something@somewhere with no
// whitespace or header-breaking characters.
func validAddress(value string) bool {
	at := strings.LastIndex(value, "@")
	if at < 1 || at == len(value)-1 || len(value) > 254 {
		return false
	}
	return !strings.ContainsAny(value, " \t\r\n<>,\"")
}

// Message is one mail ready for the relay.
type Message struct {
	To      string
	Subject string
	Body    string
}

// Deliver opens a connection and sends one message. The settings screen uses
// it directly for the test button, so a broken relay is found before anything
// depends on it.
func Deliver(ctx context.Context, config Config, message Message) error {
	config = config.Normalize()
	if err := config.ready(); err != nil {
		return err
	}
	to := strings.TrimSpace(message.To)
	if !validAddress(to) {
		return fmt.Errorf("%w: recipient must be an email address", ErrInvalid)
	}
	client, err := dial(ctx, config)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if err := startSession(client, config); err != nil {
		return err
	}
	if err := client.Mail(config.FromAddress); err != nil {
		return fmt.Errorf("MAIL FROM 실패: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("RCPT TO 실패: %w", err)
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("DATA 실패: %w", err)
	}
	if _, err := writer.Write([]byte(compose(config, message))); err != nil {
		return fmt.Errorf("본문 전송 실패: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("본문 종료 실패: %w", err)
	}
	return client.Quit()
}

func dial(ctx context.Context, config Config) (*smtp.Client, error) {
	dialer := &net.Dialer{Timeout: config.Timeout()}
	var connection net.Conn
	var err error
	if config.Security == SecurityTLS {
		connection, err = (&tls.Dialer{NetDialer: dialer, Config: config.tlsConfig()}).DialContext(ctx, "tcp", config.endpoint())
	} else {
		connection, err = dialer.DialContext(ctx, "tcp", config.endpoint())
	}
	if err != nil {
		return nil, fmt.Errorf("SMTP 연결 실패: %w", err)
	}
	// The session budget covers the whole exchange, not only the dial, so a
	// relay that accepts the connection and then hangs cannot pin a goroutine.
	_ = connection.SetDeadline(time.Now().Add(config.Timeout()))
	client, err := smtp.NewClient(connection, config.SMTPHost)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("SMTP 세션 시작 실패: %w", err)
	}
	return client, nil
}

// startSession upgrades and authenticates only as far as the relay allows, so
// an unauthenticated internal relay works with the same settings as a hosted
// provider that demands both.
func startSession(client *smtp.Client, config Config) error {
	if err := client.Hello(helloName(config)); err != nil {
		return fmt.Errorf("EHLO 실패: %w", err)
	}
	if config.Security == SecuritySTARTTLS || config.Security == SecurityAuto {
		if supported, _ := client.Extension("STARTTLS"); supported {
			if err := client.StartTLS(config.tlsConfig()); err != nil {
				return fmt.Errorf("STARTTLS 실패: %w", err)
			}
		} else if config.Security == SecuritySTARTTLS {
			return fmt.Errorf("%w: 서버가 STARTTLS를 지원하지 않습니다", ErrInvalid)
		}
	}
	if config.Username == "" {
		return nil
	}
	supported, mechanisms := client.Extension("AUTH")
	if !supported {
		return fmt.Errorf("%w: 서버가 인증을 지원하지 않습니다. 사용자 이름을 비우고 사용하세요", ErrInvalid)
	}
	upper := strings.ToUpper(mechanisms)
	switch {
	case strings.Contains(upper, "PLAIN"):
		return client.Auth(smtp.PlainAuth("", config.Username, config.Password, config.SMTPHost))
	case strings.Contains(upper, "LOGIN"):
		return client.Auth(loginAuth{username: config.Username, password: config.Password, host: config.SMTPHost})
	default:
		return client.Auth(smtp.CRAMMD5Auth(config.Username, config.Password))
	}
}

func (c Config) tlsConfig() *tls.Config {
	return &tls.Config{ServerName: c.SMTPHost, MinVersion: tls.VersionTLS12, InsecureSkipVerify: c.SkipTLSVerify} //nolint:gosec // opt-in for internal relays with private certificates
}

// helloName keeps the EHLO name to the sender domain, which relays that check
// the greeting accept more readily than a container hostname.
func helloName(config Config) string {
	if index := strings.LastIndex(config.FromAddress, "@"); index >= 0 && index+1 < len(config.FromAddress) {
		return config.FromAddress[index+1:]
	}
	return "localhost"
}

// loginAuth implements the LOGIN mechanism several corporate relays offer
// instead of PLAIN. The standard library ships only PLAIN and CRAM-MD5.
type loginAuth struct{ username, password, host string }

func (a loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS && server.Name != a.host {
		return "", nil, errors.New("LOGIN 인증은 신뢰할 수 있는 서버에서만 사용합니다")
	}
	return "LOGIN", nil, nil
}

func (a loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimRight(string(fromServer), ": ")) {
	case "username":
		return []byte(a.username), nil
	case "password":
		return []byte(a.password), nil
	}
	return nil, fmt.Errorf("알 수 없는 LOGIN 요청: %s", fromServer)
}
