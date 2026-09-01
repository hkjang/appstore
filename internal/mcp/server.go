package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const ProtocolVersion = "2026-07-28"

type Caller struct {
	Authenticated bool
	UserID        string
	Permissions   map[string]bool
}

func (c Caller) Can(permission string) bool {
	return c.Permissions[permission] || c.Permissions["*"]
}

type Tool struct {
	Name         string         `json:"name"`
	Title        string         `json:"title,omitempty"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"inputSchema"`
	OutputSchema map[string]any `json:"outputSchema,omitempty"`
	Annotations  map[string]any `json:"annotations,omitempty"`
}

type ToolProvider interface {
	Tools(context.Context, Caller) []Tool
	Call(context.Context, Caller, string, map[string]any) (any, error)
}

type Authenticator func(*http.Request) (Caller, error)
type OriginValidator func(*http.Request, string) bool

type Server struct {
	Version        string
	Provider       ToolProvider
	Authenticate   Authenticator
	ValidateOrigin OriginValidator
	Enabled        func(context.Context) (enabled, anonymous bool, err error)
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  params          `json:"params"`
}

type params struct {
	Name      string         `json:"name,omitempty"`
	Arguments map[string]any `json:"arguments,omitempty"`
	Meta      requestMeta    `json:"_meta"`
}

type requestMeta struct {
	ProtocolVersion string         `json:"io.modelcontextprotocol/protocolVersion"`
	ClientInfo      map[string]any `json:"io.modelcontextprotocol/clientInfo"`
	Capabilities    map[string]any `json:"io.modelcontextprotocol/clientCapabilities"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		s.writeError(w, http.StatusMethodNotAllowed, nil, -32600, "only POST is supported")
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && !s.validOrigin(r, origin) {
		s.writeError(w, http.StatusForbidden, nil, -32000, "origin is not allowed")
		return
	}
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		s.writeError(w, http.StatusUnsupportedMediaType, nil, -32600, "Content-Type must be application/json")
		return
	}
	if r.ContentLength > 2<<20 {
		s.writeError(w, http.StatusRequestEntityTooLarge, nil, -32600, "request body is too large")
		return
	}

	anonymousAllowed := true
	if s.Enabled != nil {
		enabled, anonymous, err := s.Enabled(r.Context())
		if err != nil {
			s.writeError(w, http.StatusServiceUnavailable, nil, -32603, "MCP configuration is unavailable")
			return
		}
		if !enabled {
			s.writeError(w, http.StatusNotFound, nil, -32601, "MCP is disabled")
			return
		}
		anonymousAllowed = anonymous
		if !anonymous && s.Authenticate == nil {
			s.writeError(w, http.StatusUnauthorized, nil, -32001, "authentication is required")
			return
		}
	}

	body := http.MaxBytesReader(w, r.Body, 2<<20)
	defer body.Close()
	var message request
	decoder := json.NewDecoder(body)
	decoder.UseNumber()
	if err := decoder.Decode(&message); err != nil {
		s.writeError(w, http.StatusBadRequest, nil, -32700, "invalid JSON")
		return
	}
	if err := ensureEOF(decoder); err != nil {
		s.writeError(w, http.StatusBadRequest, message.ID, -32600, "body must contain one JSON-RPC message")
		return
	}
	if message.JSONRPC != "2.0" || message.Method == "" {
		s.writeError(w, http.StatusBadRequest, message.ID, -32600, "invalid JSON-RPC request")
		return
	}
	if err := validateMetadata(r, message); err != nil {
		s.writeError(w, http.StatusBadRequest, message.ID, -32020, err.Error())
		return
	}

	caller := Caller{Permissions: map[string]bool{}}
	if s.Authenticate != nil {
		var err error
		caller, err = s.Authenticate(r)
		if err != nil && !errors.Is(err, ErrAnonymous) {
			s.writeError(w, http.StatusUnauthorized, message.ID, -32001, "invalid authentication")
			return
		}
		if caller.Permissions == nil {
			caller.Permissions = map[string]bool{}
		}
	}
	if !anonymousAllowed && !caller.Authenticated {
		s.writeError(w, http.StatusUnauthorized, message.ID, -32001, "authentication is required")
		return
	}

	if len(message.ID) == 0 || string(message.ID) == "null" {
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, status, rpcErr := s.dispatch(r.Context(), caller, message)
	if rpcErr != nil {
		s.writeError(w, status, message.ID, rpcErr.Code, rpcErr.Message)
		return
	}
	s.writeJSON(w, http.StatusOK, response{JSONRPC: "2.0", ID: message.ID, Result: result})
}

var ErrAnonymous = errors.New("anonymous caller")

func (s *Server) dispatch(ctx context.Context, caller Caller, req request) (any, int, *rpcError) {
	switch req.Method {
	case "server/discover":
		version := s.Version
		if version == "" {
			version = "dev"
		}
		return map[string]any{
			"resultType":        "complete",
			"supportedVersions": []string{ProtocolVersion},
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"_meta": map[string]any{"io.modelcontextprotocol/serverInfo": map[string]any{
				"name": "appstore", "version": version,
			}},
			"instructions": "Search and manage the AppStore catalog. Mutating tools require an authenticated key with matching permissions.",
			"ttlMs":        3600000,
			"cacheScope":   cacheScope(caller),
		}, http.StatusOK, nil
	case "tools/list":
		if s.Provider == nil {
			return nil, http.StatusInternalServerError, &rpcError{Code: -32603, Message: "tool provider is unavailable"}
		}
		return map[string]any{
			"resultType": "complete",
			"tools":      s.Provider.Tools(ctx, caller),
			"ttlMs":      300000,
			"cacheScope": cacheScope(caller),
		}, http.StatusOK, nil
	case "tools/call":
		if req.Params.Name == "" || s.Provider == nil {
			return nil, http.StatusBadRequest, &rpcError{Code: -32602, Message: "tool name is required"}
		}
		value, err := s.Provider.Call(ctx, caller, req.Params.Name, req.Params.Arguments)
		if err != nil {
			if errors.Is(err, ErrUnknownTool) {
				return nil, http.StatusBadRequest, &rpcError{Code: -32602, Message: "unknown tool name"}
			}
			return map[string]any{
				"resultType": "complete",
				"content":    []map[string]any{{"type": "text", "text": err.Error()}},
				"isError":    true,
			}, http.StatusOK, nil
		}
		serialized, _ := json.Marshal(value)
		return map[string]any{
			"resultType":        "complete",
			"content":           []map[string]any{{"type": "text", "text": string(serialized)}},
			"structuredContent": value,
			"isError":           false,
		}, http.StatusOK, nil
	default:
		return nil, http.StatusNotFound, &rpcError{Code: -32601, Message: "method not found"}
	}
}

func cacheScope(c Caller) string {
	if c.Authenticated {
		return "private"
	}
	return "public"
}

func validateMetadata(r *http.Request, req request) error {
	version := r.Header.Get("MCP-Protocol-Version")
	if version == "" {
		return errors.New("header mismatch: MCP-Protocol-Version is required")
	}
	if version != ProtocolVersion {
		return fmt.Errorf("unsupported protocol version %q; supported: %s", version, ProtocolVersion)
	}
	if req.Params.Meta.ProtocolVersion != version {
		return errors.New("header mismatch: MCP-Protocol-Version does not match params._meta")
	}
	if req.Params.Meta.ClientInfo == nil || req.Params.Meta.Capabilities == nil {
		return errors.New("request metadata must include clientInfo and clientCapabilities")
	}
	if method := r.Header.Get("Mcp-Method"); method == "" || method != req.Method {
		return errors.New("header mismatch: Mcp-Method is missing or does not match the body")
	}
	if req.Method == "tools/call" {
		name, err := decodeHeaderValue(r.Header.Get("Mcp-Name"))
		if err != nil || name == "" || name != req.Params.Name {
			return errors.New("header mismatch: Mcp-Name is missing, malformed, or does not match the body")
		}
	}
	return nil
}

func decodeHeaderValue(value string) (string, error) {
	if strings.HasPrefix(value, "=?base64?") && strings.HasSuffix(value, "?=") {
		encoded := strings.TrimSuffix(strings.TrimPrefix(value, "=?base64?"), "?=")
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", err
		}
		return string(decoded), nil
	}
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return "", errors.New("header contains unsafe characters")
		}
	}
	return value, nil
}

func (s *Server) validOrigin(r *http.Request, origin string) bool {
	if s.ValidateOrigin != nil {
		return s.ValidateOrigin(r, origin)
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	return strings.EqualFold(parsed.Host, r.Host) && (parsed.Scheme == "http" || parsed.Scheme == "https")
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	err := decoder.Decode(&extra)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err == nil {
		return errors.New("additional JSON value")
	}
	return err
}

func (s *Server) writeError(w http.ResponseWriter, status int, id json.RawMessage, code int, message string) {
	s.writeJSON(w, status, response{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: message}})
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
