package audit

import (
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/model"
)

func NewEntry(r *http.Request, actorID *uuid.UUID, actor, action, resource, resourceID, requestID string, before, after any) model.AuditLog {
	return model.AuditLog{
		ActorID: actorID, Actor: fallback(actor, "system"), Action: action,
		Resource: resource, ResourceID: resourceID, Before: marshal(before), After: marshal(after),
		IP: clientIP(r), UserAgent: truncate(r.UserAgent(), 512), RequestID: requestID, CreatedAt: time.Now().UTC(),
	}
}

func marshal(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return json.RawMessage(`{"redacted":true}`)
	}
	return encoded
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return truncate(host, 128)
	}
	return truncate(r.RemoteAddr, 128)
}

func fallback(value, defaultValue string) string {
	if strings.TrimSpace(value) == "" {
		return defaultValue
	}
	return value
}

func truncate(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) > limit {
		return value[:limit]
	}
	return value
}
