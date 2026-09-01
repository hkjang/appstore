package ai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

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
