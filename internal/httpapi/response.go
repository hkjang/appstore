package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

type ErrorPayload struct {
	Error APIErrorBody `json:"error"`
}

type APIErrorBody struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	RequestID string         `json:"requestId"`
	Details   map[string]any `json:"details,omitempty"`
}

type APIError struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *APIError) Error() string { return e.Message }

func WriteJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	apiError := &APIError{Status: http.StatusInternalServerError, Code: "INTERNAL_ERROR", Message: "요청을 처리하지 못했습니다."}
	var candidate *APIError
	if errors.As(err, &candidate) {
		apiError = candidate
	}
	WriteJSON(w, apiError.Status, ErrorPayload{Error: APIErrorBody{
		Code: apiError.Code, Message: apiError.Message, RequestID: RequestID(r.Context()), Details: apiError.Details,
	}})
}

func DecodeJSON(w http.ResponseWriter, r *http.Request, value any) error {
	if r.ContentLength > 2<<20 {
		return &APIError{Status: http.StatusRequestEntityTooLarge, Code: "REQUEST_TOO_LARGE", Message: "요청 본문이 너무 큽니다."}
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return &APIError{Status: http.StatusBadRequest, Code: "INVALID_JSON", Message: "요청 형식이 올바르지 않습니다."}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return &APIError{Status: http.StatusBadRequest, Code: "INVALID_JSON", Message: "요청에는 하나의 JSON 값만 포함할 수 있습니다."}
	}
	return nil
}

func NotFound(code, message string) *APIError {
	return &APIError{Status: http.StatusNotFound, Code: code, Message: message}
}

func Unauthorized(message string) *APIError {
	return &APIError{Status: http.StatusUnauthorized, Code: "UNAUTHORIZED", Message: message}
}

func Forbidden(message string) *APIError {
	return &APIError{Status: http.StatusForbidden, Code: "FORBIDDEN", Message: message}
}

func Validation(message string, details map[string]any) *APIError {
	return &APIError{Status: http.StatusUnprocessableEntity, Code: "VALIDATION_ERROR", Message: message, Details: details}
}
