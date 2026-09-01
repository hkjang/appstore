package ai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/hkjang/appstore/internal/model"
)

const MaximumTokens int64 = 262144

type Message struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type ChatRequest struct {
	ProviderID  string    `json:"providerId,omitempty"`
	Model       string    `json:"model,omitempty"`
	Messages    []Message `json:"messages"`
	MaxTokens   int64     `json:"maxTokens,omitempty"`
	Temperature *float64  `json:"temperature,omitempty"`
}

type Event struct {
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	FinishReason string `json:"finishReason,omitempty"`
	PromptTokens int64  `json:"promptTokens,omitempty"`
	OutputTokens int64  `json:"outputTokens,omitempty"`
	TotalTokens  int64  `json:"totalTokens,omitempty"`
	Raw          any    `json:"-"`
}

type Streamer struct {
	HTTPClient *http.Client
	Box        *appcrypto.SecretBox
}

func ValidateProvider(provider model.AIProvider) error {
	if strings.TrimSpace(provider.Name) == "" {
		return errors.New("provider name is required")
	}
	parsed, err := url.Parse(provider.BaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return errors.New("provider base URL must be an absolute HTTP(S) URL")
	}
	for name, value := range map[string]int64{
		"contextWindow": provider.ContextWindow, "maxInputTokens": provider.MaxInputTokens, "maxOutputTokens": provider.MaxOutputTokens,
	} {
		if value < 0 || value > MaximumTokens {
			return fmt.Errorf("%s must be between 0 and %d", name, MaximumTokens)
		}
	}
	if provider.MaxInputTokens+provider.MaxOutputTokens > provider.ContextWindow && provider.ContextWindow > 0 {
		return errors.New("max input tokens plus max output tokens cannot exceed the context window")
	}
	if provider.Temperature < 0 || provider.Temperature > 2 {
		return errors.New("temperature must be between 0 and 2")
	}
	if provider.TimeoutSeconds < 1 || provider.TimeoutSeconds > 3600 {
		return errors.New("timeout must be between 1 and 3600 seconds")
	}
	if provider.Retries < 0 || provider.Retries > 10 {
		return errors.New("retries must be between 0 and 10")
	}
	return nil
}

func (s *Streamer) Stream(ctx context.Context, provider model.AIProvider, input ChatRequest, emit func(Event) error) error {
	if err := ValidateProvider(provider); err != nil {
		return err
	}
	if len(input.Messages) == 0 {
		return errors.New("at least one message is required")
	}
	if input.MaxTokens < 0 || input.MaxTokens > MaximumTokens {
		return fmt.Errorf("maxTokens must be between 0 and %d", MaximumTokens)
	}
	if input.MaxTokens == 0 {
		input.MaxTokens = provider.MaxOutputTokens
	}
	if provider.MaxOutputTokens > 0 && input.MaxTokens > provider.MaxOutputTokens {
		return fmt.Errorf("maxTokens exceeds provider limit of %d", provider.MaxOutputTokens)
	}
	modelName := input.Model
	if modelName == "" {
		modelName = provider.DefaultModel
	}
	if modelName == "" {
		return errors.New("model is required")
	}
	temperature := provider.Temperature
	if input.Temperature != nil {
		temperature = *input.Temperature
	}
	if temperature < 0 || temperature > 2 {
		return errors.New("temperature must be between 0 and 2")
	}

	apiKey, err := s.Box.Decrypt(provider.APIKey)
	if err != nil {
		return fmt.Errorf("decrypt provider API key: %w", err)
	}
	payload := map[string]any{
		"model": modelName, "messages": input.Messages, "max_tokens": input.MaxTokens,
		"temperature": temperature, "stream": true,
		"stream_options": map[string]any{"include_usage": true},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	timeout := time.Duration(provider.TimeoutSeconds) * time.Second
	requestCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	endpoint := strings.TrimRight(provider.BaseURL, "/") + "/chat/completions"
	client := s.HTTPClient
	if client == nil {
		client = &http.Client{Transport: http.DefaultTransport}
	}
	var response *http.Response
	for attempt := 0; attempt <= provider.Retries; attempt++ {
		request, requestErr := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(body))
		if requestErr != nil {
			return requestErr
		}
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "text/event-stream")
		if apiKey != "" {
			request.Header.Set("Authorization", "Bearer "+apiKey)
		}
		response, err = client.Do(request)
		if err != nil {
			if attempt < provider.Retries && requestCtx.Err() == nil {
				if err := retryDelay(requestCtx, attempt); err != nil {
					return err
				}
				continue
			}
			return fmt.Errorf("connect to AI provider: %w", err)
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			break
		}
		limited, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		_ = response.Body.Close()
		if attempt < provider.Retries && (response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500) {
			if err := retryDelay(requestCtx, attempt); err != nil {
				return err
			}
			continue
		}
		return fmt.Errorf("AI provider returned HTTP %d: %s", response.StatusCode, sanitizeProviderError(limited))
	}
	if response == nil {
		return errors.New("AI provider did not return a response")
	}
	defer response.Body.Close()
	if !strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "text/event-stream") {
		return errors.New("AI provider did not return an SSE stream")
	}
	return consumeOpenAIStream(requestCtx, response.Body, emit)
}

func retryDelay(ctx context.Context, attempt int) error {
	delay := 100 * time.Millisecond
	for index := 0; index < attempt && delay < 2*time.Second; index++ {
		delay *= 2
	}
	if delay > 2*time.Second {
		delay = 2 * time.Second
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type openAIChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int64 `json:"prompt_tokens"`
		CompletionTokens int64 `json:"completion_tokens"`
		TotalTokens      int64 `json:"total_tokens"`
	} `json:"usage"`
}

func consumeOpenAIStream(ctx context.Context, reader io.Reader, emit func(Event) error) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)
	finishSent := false
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			if !finishSent {
				if err := emit(Event{Type: "finish", FinishReason: "stop"}); err != nil {
					return err
				}
			}
			return nil
		}
		var chunk openAIChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return fmt.Errorf("decode AI stream chunk: %w", err)
		}
		if chunk.Usage != nil {
			if err := emit(Event{Type: "usage", PromptTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens, TotalTokens: chunk.Usage.TotalTokens}); err != nil {
				return err
			}
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.Content != "" {
				if err := emit(Event{Type: "token", Text: choice.Delta.Content}); err != nil {
					return err
				}
			}
			if choice.FinishReason != "" {
				finishSent = true
				if err := emit(Event{Type: "finish", FinishReason: choice.FinishReason}); err != nil {
					return err
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read AI stream: %w", err)
	}
	if !finishSent {
		return errors.New("AI stream ended before a finish event")
	}
	return nil
}

func sanitizeProviderError(value []byte) string {
	text := strings.TrimSpace(string(value))
	text = strings.ReplaceAll(text, "\r", " ")
	text = strings.ReplaceAll(text, "\n", " ")
	if len(text) > 512 {
		text = text[:512]
	}
	return text
}
