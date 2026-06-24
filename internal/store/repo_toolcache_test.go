package store

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

func TestComputeToolChangeSummaryForFirstSync(t *testing.T) {
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)

	got := computeToolChangeSummary(nil, []domain.ToolDef{
		toolChangeSummaryTestTool("search", `{"type":"object"}`),
		toolChangeSummaryTestTool("read", `{"type":"object"}`),
	}, false, now)

	if got.Added != 2 || got.Removed != 0 || got.SchemaChanged != 0 || !got.SyncedAt.Equal(now) {
		t.Fatalf("首次同步摘要不符合预期：%+v", got)
	}
}

func TestComputeToolChangeSummaryForIncrementalSync(t *testing.T) {
	now := time.Date(2026, 6, 23, 10, 0, 0, 0, time.UTC)
	previous := []domain.ToolDef{
		toolChangeSummaryTestTool("search", `{"type":"object","properties":{"q":{"type":"string"}}}`),
		toolChangeSummaryTestTool("remove_me", `{"type":"object"}`),
		toolChangeSummaryTestTool("same", `{"properties":{"id":{"type":"integer"}},"type":"object"}`),
	}
	current := []domain.ToolDef{
		toolChangeSummaryTestTool("search", `{"type":"object","properties":{"q":{"type":"string"},"limit":{"type":"integer"}}}`),
		toolChangeSummaryTestTool("same", `{"type":"object","properties":{"id":{"type":"integer"}}}`),
		toolChangeSummaryTestTool("new_tool", `{"type":"object"}`),
	}

	got := computeToolChangeSummary(previous, current, true, now)

	if got.Added != 1 || got.Removed != 1 || got.SchemaChanged != 1 || !got.SyncedAt.Equal(now) {
		t.Fatalf("增量同步摘要不符合预期：%+v", got)
	}
}

func toolChangeSummaryTestTool(name string, schema string) domain.ToolDef {
	return domain.ToolDef{
		OriginalName: name,
		Name:         name,
		InputSchema:  json.RawMessage(schema),
	}
}
