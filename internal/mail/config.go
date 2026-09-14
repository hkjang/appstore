package mail

import (
	"strings"
	"time"
)

// Defaults aim at the common case: an internal relay on port 25 that accepts
// mail from the network without credentials.
const (
	defaultPort     = 25
	defaultSecurity = SecurityAuto
	defaultTimeout  = 10 * time.Second
	defaultFromName = "AppStore"
)

// Setting keys. They are the same in every service that follows the mail
// standard, so an operator who has configured one has configured them all.
// Each is stored as its own system_settings row.
const (
	KeyEnabled        = "mail.enabled"
	KeyHost           = "mail.smtp_host"
	KeyPort           = "mail.smtp_port"
	KeySecurity       = "mail.security"
	KeySkipTLSVerify  = "mail.skip_tls_verify"
	KeyUsername       = "mail.username"
	KeyPassword       = "mail.password"
	KeyFromAddress    = "mail.from_address"
	KeyFromName       = "mail.from_name"
	KeyBaseURL        = "mail.base_url"
	KeyTimeoutSeconds = "mail.timeout_seconds"
	// KeyPrefix selects every mail setting row at once.
	KeyPrefix = "mail."
	// eventKeyPrefix plus the event's switch name is the per-event setting.
	eventKeyPrefix = "mail.notify_"
)

// Default is the configuration of a fresh installation: mail off, and the
// relay shape that needs the least filling in once it is switched on.
func Default() Config {
	return Config{
		SMTPPort: defaultPort, Security: defaultSecurity, FromName: defaultFromName,
		TimeoutSeconds: int(defaultTimeout / time.Second), Events: defaultEvents(),
	}
}

func defaultEvents() map[string]bool {
	events := make(map[string]bool, len(Events))
	for _, event := range Events {
		events[event.Name] = true
	}
	return events
}

// ConfigFromValues builds the configuration from the stored setting rows,
// keyed by the standard names above. Missing rows take the defaults, so a
// database that has never seen the mail screen reads as "off".
func ConfigFromValues(values map[string]any) Config {
	config := Default()
	config.Enabled = boolValue(values, KeyEnabled, false)
	config.SMTPHost = stringValue(values, KeyHost, "")
	config.Username = stringValue(values, KeyUsername, "")
	config.Password = stringValue(values, KeyPassword, "")
	config.PasswordSet = config.Password != ""
	config.FromAddress = stringValue(values, KeyFromAddress, "")
	config.FromName = stringValue(values, KeyFromName, defaultFromName)
	config.Security = stringValue(values, KeySecurity, defaultSecurity)
	config.BaseURL = stringValue(values, KeyBaseURL, "")
	config.SkipTLSVerify = boolValue(values, KeySkipTLSVerify, false)
	if port, ok := numberValue(values, KeyPort); ok && port > 0 {
		config.SMTPPort = port
	}
	if seconds, ok := numberValue(values, KeyTimeoutSeconds); ok && seconds > 0 {
		config.TimeoutSeconds = seconds
	}
	for _, event := range Events {
		if enabled, ok := values[eventKeyPrefix+event.Switch].(bool); ok {
			config.Events[event.Name] = enabled
		}
	}
	return config.Normalize()
}

// Values is the inverse of ConfigFromValues: the rows to store. The password
// is deliberately absent — it has its own row and its own write path, so a
// settings save can never overwrite it with a blank.
func (c Config) Values() map[string]any {
	c = c.Normalize()
	values := map[string]any{
		KeyEnabled: c.Enabled, KeyHost: c.SMTPHost, KeyPort: c.SMTPPort, KeySecurity: c.Security,
		KeySkipTLSVerify: c.SkipTLSVerify, KeyUsername: c.Username, KeyFromAddress: c.FromAddress,
		KeyFromName: c.FromName, KeyBaseURL: c.BaseURL, KeyTimeoutSeconds: c.TimeoutSeconds,
	}
	for _, event := range Events {
		values[eventKeyPrefix+event.Switch] = c.Allows(event.Name)
	}
	return values
}

// Normalize trims every field, lowers the enumeration and fills the defaults
// so the rest of the package can compare values directly.
func (c Config) Normalize() Config {
	c.SMTPHost = strings.TrimSpace(c.SMTPHost)
	c.Username = strings.TrimSpace(c.Username)
	c.FromAddress = strings.TrimSpace(c.FromAddress)
	c.FromName = strings.TrimSpace(c.FromName)
	c.BaseURL = strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	c.Security = strings.ToLower(strings.TrimSpace(c.Security))
	if c.Security == "" {
		c.Security = defaultSecurity
	}
	// A relay on the implicit TLS port needs no extra configuration.
	if c.Security == SecurityAuto && c.SMTPPort == 465 {
		c.Security = SecurityTLS
	}
	if c.SMTPPort == 0 {
		c.SMTPPort = defaultPort
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = int(defaultTimeout / time.Second)
	}
	if c.FromName == "" {
		c.FromName = defaultFromName
	}
	events := defaultEvents()
	for name, enabled := range c.Events {
		if _, known := events[name]; known {
			events[name] = enabled
		}
	}
	c.Events = events
	return c
}

// Public strips what a response must never carry. PasswordSet stays, so the
// screen can say "configured" without ever seeing the value.
func (c Config) Public() Config {
	c.Password = ""
	return c
}

func stringValue(values map[string]any, key, fallback string) string {
	if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func boolValue(values map[string]any, key string, fallback bool) bool {
	if value, ok := values[key].(bool); ok {
		return value
	}
	return fallback
}

func numberValue(values map[string]any, key string) (int, bool) {
	switch typed := values[key].(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	case int64:
		return int(typed), true
	}
	return 0, false
}
