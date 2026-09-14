package mail

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Store is what the service needs from the repository: the settings rows,
// the delivery log, and one lookup that turns account ids into addresses.
// The lookup is borrowed from the user table on purpose — mail keeps no
// directory of its own, so there is nothing to drift.
type Store interface {
	GetMailSettings(context.Context) (Config, error)
	LookupUserEmails(context.Context, []uuid.UUID) (map[uuid.UUID]string, error)
	RecordMailDelivery(context.Context, Delivery) (uuid.UUID, error)
	FinishMailDelivery(ctx context.Context, id uuid.UUID, status string, attempts int, errorMessage string) error
}

// Delivery is one attempt to send one mail. It carries the subject and the
// recipient, never the body: the log must answer "did it go out?" without
// becoming a second copy of everything that was said.
type Delivery struct {
	ID           uuid.UUID  `json:"id"`
	Event        string     `json:"event"`
	Recipient    string     `json:"recipient"`
	Subject      string     `json:"subject"`
	Reference    string     `json:"reference,omitempty"`
	ActorID      *uuid.UUID `json:"actorId,omitempty"`
	Status       string     `json:"status"`
	Attempts     int        `json:"attempts"`
	ErrorMessage string     `json:"errorMessage,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

const (
	StatusQueued = "queued"
	StatusSent   = "sent"
	StatusFailed = "failed"

	// maxSubjectRunes bounds what the log keeps of a subject.
	maxSubjectRunes = 300
	// maxErrorRunes bounds what the log keeps of a relay's complaint.
	maxErrorRunes = 1000
)

// retryDelay is the pause before the single retry a background delivery
// makes. A relay that briefly refuses a connection is common, and losing the
// notification is worse than a short wait.
var retryDelay = 2 * time.Second

// Service sends notifications and records what it sent.
type Service struct {
	store   Store
	decrypt func(string) (string, error)
	logger  *slog.Logger
	send    func(context.Context, Config, Message) error
	// wait lets tests observe background deliveries finishing.
	wait func()
}

// NewService wires the service to the repository. decrypt opens the stored
// password; it may be nil when the store already hands back plaintext.
func NewService(store Store, decrypt func(string) (string, error), logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{store: store, decrypt: decrypt, logger: logger, send: Deliver}
}

// SetSender replaces the transport so tests can drive the service without a
// relay.
func (s *Service) SetSender(sender func(context.Context, Config, Message) error) { s.send = sender }

// Config reads the settings with the password opened, ready to dial.
func (s *Service) Config(ctx context.Context) (Config, error) {
	config, err := s.store.GetMailSettings(ctx)
	if err != nil {
		return Config{}, err
	}
	if config.Password != "" && s.decrypt != nil {
		plaintext, err := s.decrypt(config.Password)
		if err != nil {
			return Config{}, err
		}
		config.Password = plaintext
	}
	return config, nil
}

// Notify resolves the recipients and sends in the background, so no request
// ever waits on a relay. The actor is dropped — nobody is told about their
// own action — and so are recipients without an address. When mail is on but
// unusable, each recipient gets a failed record that says why, because a
// silent "nothing left" is exactly what the log exists to rule out.
func (s *Service) Notify(ctx context.Context, notification Notification, actorID *uuid.UUID, recipients []uuid.UUID) {
	if s == nil || len(recipients) == 0 {
		return
	}
	ctx = context.WithoutCancel(ctx)
	config, err := s.Config(ctx)
	if err != nil {
		s.logger.Warn("mail settings were not read", "event", notification.Event, "error", err)
		return
	}
	if !config.Enabled || !config.Allows(notification.Event) {
		return
	}
	addresses := s.resolve(ctx, recipients, actorID)
	if len(addresses) == 0 {
		return
	}
	readiness := config.ready()
	body := notification.Render(config)
	for _, address := range addresses {
		delivery := Delivery{
			Event: notification.Event, Recipient: address, Subject: trimRunes(notification.Subject, maxSubjectRunes),
			Reference: notification.Reference, ActorID: actorID, Status: StatusQueued,
		}
		id, err := s.store.RecordMailDelivery(ctx, delivery)
		if err != nil {
			s.logger.Warn("mail delivery was not recorded", "event", delivery.Event, "error", err)
			continue
		}
		delivery.ID = id
		if readiness != nil {
			s.complete(ctx, delivery, 0, readiness)
			continue
		}
		go s.deliver(delivery, config, Message{To: address, Subject: notification.Subject, Body: body})
	}
}

// SendNow delivers immediately and reports the outcome, which is what the
// administrator's test button needs.
func (s *Service) SendNow(ctx context.Context, notification Notification, actorID *uuid.UUID, recipient string) error {
	config, err := s.Config(ctx)
	if err != nil {
		return err
	}
	if !config.Enabled {
		return ErrDisabled
	}
	recipient = strings.TrimSpace(recipient)
	delivery := Delivery{
		Event: notification.Event, Recipient: recipient, Subject: trimRunes(notification.Subject, maxSubjectRunes),
		ActorID: actorID, Status: StatusQueued,
	}
	ctx = context.WithoutCancel(ctx)
	if delivery.ID, err = s.store.RecordMailDelivery(ctx, delivery); err != nil {
		s.logger.Warn("mail delivery was not recorded", "event", delivery.Event, "error", err)
	}
	sendCtx, cancel := context.WithTimeout(ctx, config.Timeout()+5*time.Second)
	defer cancel()
	err = s.send(sendCtx, config, Message{To: recipient, Subject: notification.Subject, Body: notification.Render(config)})
	s.complete(ctx, delivery, 1, err)
	return err
}

// deliver retries once, then records the outcome.
func (s *Service) deliver(delivery Delivery, config Config, message Message) {
	if s.wait != nil {
		defer s.wait()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*config.Timeout()+retryDelay+5*time.Second)
	defer cancel()
	var err error
	attempts := 0
	for attempts < 2 {
		attempts++
		if err = s.send(ctx, config, message); err == nil {
			break
		}
		if attempts == 1 {
			select {
			case <-ctx.Done():
			case <-time.After(retryDelay):
			}
		}
	}
	s.complete(ctx, delivery, attempts, err)
}

func (s *Service) complete(ctx context.Context, delivery Delivery, attempts int, cause error) {
	status, message := StatusSent, ""
	if cause != nil {
		status, message = StatusFailed, cause.Error()
		s.logger.Warn("notification mail failed", "event", delivery.Event, "recipient", delivery.Recipient, "error", cause)
	}
	if delivery.ID == uuid.Nil {
		return
	}
	if err := s.store.FinishMailDelivery(ctx, delivery.ID, status, attempts, trimRunes(message, maxErrorRunes)); err != nil {
		s.logger.Warn("mail delivery status was not recorded", "id", delivery.ID, "error", err)
	}
}

// resolve turns account ids into unique addresses, dropping the actor so
// nobody is told about their own action.
func (s *Service) resolve(ctx context.Context, recipients []uuid.UUID, actorID *uuid.UUID) []string {
	wanted := make([]uuid.UUID, 0, len(recipients))
	seenID := map[uuid.UUID]struct{}{}
	for _, recipient := range recipients {
		if recipient == uuid.Nil || (actorID != nil && recipient == *actorID) {
			continue
		}
		if _, duplicate := seenID[recipient]; duplicate {
			continue
		}
		seenID[recipient] = struct{}{}
		wanted = append(wanted, recipient)
	}
	if len(wanted) == 0 {
		return nil
	}
	emails, err := s.store.LookupUserEmails(ctx, wanted)
	if err != nil {
		s.logger.Warn("mail recipients were not resolved", "error", err)
		return nil
	}
	seen := map[string]struct{}{}
	addresses := make([]string, 0, len(wanted))
	for _, id := range wanted {
		address := strings.TrimSpace(emails[id])
		if !validAddress(address) {
			continue
		}
		key := strings.ToLower(address)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		addresses = append(addresses, address)
	}
	return addresses
}

func trimRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}
