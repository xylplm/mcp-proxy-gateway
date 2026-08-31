package store

import (
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/risk"
)

func TestProviderModelMappingPreservesPlaintextAPIKey(t *testing.T) {
	createdAt := time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC)
	provider := risk.Provider{
		ID: "33333333-3333-4333-8333-333333333333", Name: "demo-provider",
		BaseURL: "https://api.example.com/v1", APIStyle: risk.APIStyleResponses,
		Model: "demo-model", APIKey: "provider-plain-secret", Enabled: true, Active: true,
		TimeoutS: 60, BatchSize: 10, MaxConcurrency: 2, AutoAssess: true,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}

	model := providerToModel(provider)
	if model.APIKey != provider.APIKey {
		t.Fatalf("模型映射丢失 API Key：got=%q want=%q", model.APIKey, provider.APIKey)
	}
	got := modelToProvider(model)
	if got.APIKey != provider.APIKey {
		t.Fatalf("领域映射丢失 API Key：got=%q want=%q", got.APIKey, provider.APIKey)
	}
}
