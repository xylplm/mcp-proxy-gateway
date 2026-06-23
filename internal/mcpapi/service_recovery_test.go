package mcpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

type panicAggregation struct{}

func (panicAggregation) BuildToolSet(context.Context, string) ([]domain.ToolDef, error) {
	panic("hidden build detail")
}

func (panicAggregation) BuildToolDetails(context.Context, string) ([]domain.ToolDetail, error) {
	panic("hidden detail")
}

func (panicAggregation) InvokeTool(context.Context, string, string, json.RawMessage) (domain.ToolResult, error) {
	panic("hidden invoke detail")
}

func TestFullToolHandlerRecoversPanic(t *testing.T) {
	svc := NewService(panicAggregation{}, 50, slog.New(slog.NewTextHandler(io.Discard, nil)))
	handler := svc.fullCallHandler("key-1", "api", "read_file", NewFullModeHandler(panicAggregation{}))

	res, err := handler(context.Background(), &mcp.CallToolRequest{})
	if res != nil {
		t.Fatalf("panic recovery should not return a result, got %+v", res)
	}
	assertInternalPanicError(t, err)
}

func TestGatewayToolHandlerRecoversPanic(t *testing.T) {
	svc := NewService(panicAggregation{}, 50, slog.New(slog.NewTextHandler(io.Discard, nil)))
	handler := svc.gatewayHandler("key-1", "api", GatewayToolListTools, NewSmartModeHandler(panicAggregation{}, 50))

	res, err := handler(context.Background(), &mcp.CallToolRequest{})
	if res != nil {
		t.Fatalf("panic recovery should not return a result, got %+v", res)
	}
	assertInternalPanicError(t, err)
}

func assertInternalPanicError(t *testing.T, err error) {
	t.Helper()
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *domain.APIError, got %T: %v", err, err)
	}
	if apiErr.Code != domain.CodeInternal {
		t.Fatalf("expected INTERNAL, got %s", apiErr.Code)
	}
	if apiErr.Message == "" || apiErr.Message == "hidden invoke detail" || apiErr.Message == "hidden build detail" {
		t.Fatalf("panic detail should not leak, got message %q", apiErr.Message)
	}
}
