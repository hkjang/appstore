package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	appcrypto "github.com/hkjang/appstore/internal/crypto"
	"github.com/hkjang/appstore/internal/model"
)

func TestValidateProviderSupports256K(t *testing.T) {
	p := model.AIProvider{Name: "local", BaseURL: "http://vllm.test/v1", ContextWindow: MaximumTokens, MaxInputTokens: 131072, MaxOutputTokens: 131072, TimeoutSeconds: 30}
	if err := ValidateProvider(p); err != nil {
		t.Fatal(err)
	}
	p.MaxOutputTokens++
	if err := ValidateProvider(p); err == nil {
		t.Fatal("expected combined context validation error")
	}
}

func TestStreamNormalizesEvents(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"안녕\"},\"finish_reason\":\"\"}]}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":1,\"total_tokens\":3}}\n\n")
		_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	box, _ := appcrypto.NewSecretBox("01234567890123456789012345678901")
	secret, _ := box.Encrypt("secret")
	provider := model.AIProvider{
		Name: "test", BaseURL: upstream.URL, APIKey: secret, DefaultModel: "model",
		ContextWindow: 8192, MaxInputTokens: 4096, MaxOutputTokens: 4096, TimeoutSeconds: 5, Streaming: true,
	}
	var events []Event
	err := (&Streamer{Box: box}).Stream(context.Background(), provider, ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}}, func(event Event) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Text != "안녕" || events[1].TotalTokens != 3 || events[2].FinishReason != "stop" {
		t.Fatalf("events = %#v", events)
	}
}

func TestStreamOmitsUnsetMaxTokens(t *testing.T) {
	for _, testCase := range []struct {
		name            string
		maxOutputTokens int64
		requestMax      int64
		want            any
	}{
		{name: "unset provider limit", want: nil},
		{name: "provider limit", maxOutputTokens: 4096, want: float64(4096)},
		{name: "request limit", maxOutputTokens: 4096, requestMax: 128, want: float64(128)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var payload map[string]any
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Errorf("decode payload: %v", err)
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
				_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer upstream.Close()
			box, _ := appcrypto.NewSecretBox("01234567890123456789012345678901")
			provider := model.AIProvider{
				Name: "test", BaseURL: upstream.URL, DefaultModel: "model",
				MaxOutputTokens: testCase.maxOutputTokens, TimeoutSeconds: 5, Streaming: true,
			}
			request := ChatRequest{Messages: []Message{{Role: "user", Content: "hello"}}, MaxTokens: testCase.requestMax}
			err := (&Streamer{Box: box}).Stream(context.Background(), provider, request, func(Event) error { return nil })
			if err != nil {
				t.Fatal(err)
			}
			got, present := payload["max_tokens"]
			if testCase.want == nil {
				if present {
					t.Fatalf("max_tokens = %#v, want it omitted", got)
				}
				return
			}
			if got != testCase.want {
				t.Fatalf("max_tokens = %#v, want %#v", got, testCase.want)
			}
		})
	}
}

func TestSanitizeProviderErrorKeepsRuneBoundary(t *testing.T) {
	if got := sanitizeProviderError([]byte(" upstream\r\nrefused ")); got != "upstream  refused" {
		t.Fatalf("sanitizeProviderError = %q", got)
	}
	long := sanitizeProviderError([]byte(strings.Repeat("한", 200)))
	if !utf8.ValidString(long) {
		t.Fatalf("sanitizeProviderError produced invalid UTF-8: %q", long)
	}
	if len(long) > 512 || len([]rune(long)) != 170 {
		t.Fatalf("sanitizeProviderError = %d bytes, %d runes", len(long), len([]rune(long)))
	}
}
