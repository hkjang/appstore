package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/hkjang/appstore/internal/ai"
	"github.com/hkjang/appstore/internal/model"
	"github.com/hkjang/appstore/internal/store"
)

func (s *Server) aiStream(w http.ResponseWriter, r *http.Request) {
	var input ai.ChatRequest
	if err := DecodeJSON(w, r, &input); err != nil {
		WriteError(w, r, err)
		return
	}
	provider, err := s.resolveAIProvider(r, input.ProviderID, input.Model)
	if err != nil {
		WriteError(w, r, storeError(err, "AI_PROVIDER_NOT_FOUND", "사용 가능한 AI Provider가 없습니다."))
		return
	}
	if err := ai.ValidateProvider(provider); err != nil {
		WriteError(w, r, Validation(err.Error(), nil))
		return
	}
	if len(input.Messages) == 0 || input.MaxTokens < 0 || input.MaxTokens > ai.MaximumTokens {
		WriteError(w, r, Validation("AI 요청의 messages 또는 maxTokens를 확인해 주세요.", nil))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	controller := http.NewResponseController(w)
	_ = controller.Flush()

	events := make(chan ai.Event, 32)
	errorsChannel := make(chan error, 1)
	go func() {
		err := s.streamer.Stream(r.Context(), provider, input, func(event ai.Event) error {
			select {
			case events <- event:
				return nil
			case <-r.Context().Done():
				return r.Context().Err()
			}
		})
		errorsChannel <- err
		close(events)
	}()

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			if err := writeSSE(w, "heartbeat", map[string]any{"time": time.Now().UTC()}); err != nil || controller.Flush() != nil {
				return
			}
		case event, ok := <-events:
			if !ok {
				err := <-errorsChannel
				if err != nil && r.Context().Err() == nil {
					_ = writeSSE(w, "error", map[string]any{"code": "AI_STREAM_ERROR", "message": err.Error(), "requestId": RequestID(r.Context())})
					_ = controller.Flush()
				}
				return
			}
			eventName := event.Type
			if eventName == "token" {
				eventName = "message"
			}
			if err := writeSSE(w, eventName, event); err != nil || controller.Flush() != nil {
				return
			}
		}
	}
}

func (s *Server) resolveAIProvider(r *http.Request, value, modelName string) (model.AIProvider, error) {
	if value != "" {
		id, err := uuid.Parse(value)
		if err != nil {
			return model.AIProvider{}, &APIError{Status: http.StatusUnprocessableEntity, Code: "AI_PROVIDER_INVALID", Message: "AI Provider ID가 올바르지 않습니다."}
		}
		provider, err := s.repository.GetAIProvider(r.Context(), id)
		if err != nil {
			return model.AIProvider{}, err
		}
		if !provider.Enabled {
			return model.AIProvider{}, &APIError{Status: http.StatusUnprocessableEntity, Code: "AI_PROVIDER_DISABLED", Message: "선택한 AI Provider가 비활성화되어 있습니다."}
		}
		return s.applyAIModelLimit(r, provider, modelName)
	}
	providers, err := s.repository.ListAIProviders(r.Context())
	if err != nil {
		return model.AIProvider{}, err
	}
	for _, provider := range providers {
		if provider.Enabled {
			full, getErr := s.repository.GetAIProvider(r.Context(), provider.ID)
			if getErr != nil {
				return model.AIProvider{}, getErr
			}
			return s.applyAIModelLimit(r, full, modelName)
		}
	}
	return model.AIProvider{}, store.ErrNotFound
}

// applyAIModelLimit overlays a configured model's stricter limits. Model
// selection itself remains in the request; this method only narrows the
// provider envelope and never expands it.
func (s *Server) applyAIModelLimit(r *http.Request, provider model.AIProvider, modelName string) (model.AIProvider, error) {
	if modelName == "" {
		modelName = provider.DefaultModel
	}
	items, err := s.repository.ListAIModels(r.Context(), provider.ID)
	if err != nil {
		return model.AIProvider{}, err
	}
	for _, item := range items {
		if item.Name != modelName {
			continue
		}
		return overlayAIModelLimit(provider, item)
	}
	return provider, nil
}

func overlayAIModelLimit(provider model.AIProvider, item model.AIModel) (model.AIProvider, error) {
	if !item.Enabled {
		return model.AIProvider{}, &APIError{Status: http.StatusUnprocessableEntity, Code: "AI_MODEL_DISABLED", Message: "선택한 AI Model이 비활성화되어 있습니다."}
	}
	provider.ContextWindow = minimumPositive(provider.ContextWindow, item.ContextWindow)
	provider.MaxInputTokens = minimumPositive(provider.MaxInputTokens, item.MaxInputTokens)
	provider.MaxOutputTokens = minimumPositive(provider.MaxOutputTokens, item.MaxOutputTokens)
	return provider, nil
}

func minimumPositive(first, second int64) int64 {
	if first == 0 {
		return second
	}
	if second == 0 {
		return first
	}
	if second < first {
		return second
	}
	return first
}

func writeSSE(w http.ResponseWriter, event string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = w.Write([]byte("event: " + event + "\ndata: " + string(encoded) + "\n\n"))
	return err
}
