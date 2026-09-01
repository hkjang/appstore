package httpapi

import (
	"errors"
	"net/http"
	"testing"

	"github.com/hkjang/appstore/internal/model"
)

func TestOverlayAIModelLimitOnlyNarrowsProvider(t *testing.T) {
	provider := model.AIProvider{
		ContextWindow: 262144, MaxInputTokens: 200000, MaxOutputTokens: 62144,
	}
	limited, err := overlayAIModelLimit(provider, model.AIModel{
		Enabled: true, ContextWindow: 131072, MaxInputTokens: 100000, MaxOutputTokens: 31072,
	})
	if err != nil {
		t.Fatal(err)
	}
	if limited.ContextWindow != 131072 || limited.MaxInputTokens != 100000 || limited.MaxOutputTokens != 31072 {
		t.Fatalf("narrowed provider = %#v", limited)
	}

	unchanged, err := overlayAIModelLimit(provider, model.AIModel{
		Enabled: true, ContextWindow: 262144, MaxInputTokens: 262144, MaxOutputTokens: 262144,
	})
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.ContextWindow != provider.ContextWindow || unchanged.MaxInputTokens != provider.MaxInputTokens || unchanged.MaxOutputTokens != provider.MaxOutputTokens {
		t.Fatalf("model limits expanded provider: %#v", unchanged)
	}
}

func TestOverlayAIModelLimitRejectsDisabledModel(t *testing.T) {
	_, err := overlayAIModelLimit(model.AIProvider{}, model.AIModel{Enabled: false})
	var apiError *APIError
	if !errors.As(err, &apiError) || apiError.Status != http.StatusUnprocessableEntity || apiError.Code != "AI_MODEL_DISABLED" {
		t.Fatalf("disabled model error = %#v", err)
	}
}
