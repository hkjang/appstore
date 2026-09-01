package openapi

import (
	"encoding/json"
	"testing"
)

func TestEmbeddedDocumentIsOpenAPI31(t *testing.T) {
	var document struct {
		OpenAPI    string                     `json:"openapi"`
		Paths      map[string]json.RawMessage `json:"paths"`
		Components map[string]json.RawMessage `json:"components"`
	}
	if err := json.Unmarshal(Document, &document); err != nil {
		t.Fatalf("decode embedded OpenAPI document: %v", err)
	}
	if document.OpenAPI != "3.1.0" {
		t.Fatalf("openapi = %q", document.OpenAPI)
	}
	if len(document.Paths) < 50 {
		t.Fatalf("documented paths = %d, want at least 50", len(document.Paths))
	}
	if len(document.Components) == 0 {
		t.Fatal("components are missing")
	}
}
