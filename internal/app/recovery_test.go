package app

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSafeRecoveryReturnsAPIEnvelope(t *testing.T) {
	engine := newRecoveryTestEngine(t)
	engine.GET("/api/admin/panic", func(*gin.Context) {
		panic("sensitive database detail")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/admin/panic", nil)
	req.Header.Set(requestIDHeader, "req-recovery-api")
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic should return HTTP 500, got %d body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(requestIDHeader); got != "req-recovery-api" {
		t.Fatalf("request id header should be preserved, got %q", got)
	}

	var body struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response should be JSON envelope: %v body=%s", err, rec.Body.String())
	}
	if body.Code != 50000 || body.Message == "" || string(body.Data) != "null" {
		t.Fatalf("unexpected API panic envelope: %+v data=%s", body, body.Data)
	}
	if strings.Contains(rec.Body.String(), "sensitive database detail") {
		t.Fatalf("panic detail leaked to client: %s", rec.Body.String())
	}
}

func TestSafeRecoveryReturnsServiceErrorForMCP(t *testing.T) {
	engine := newRecoveryTestEngine(t)
	engine.GET("/mcp/http", func(*gin.Context) {
		panic("secret upstream state")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/mcp/http", nil)
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("panic should return HTTP 500, got %d body=%s", rec.Code, rec.Body.String())
	}

	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("response should be JSON error: %v body=%s", err, rec.Body.String())
	}
	if body.Code != "INTERNAL" || body.Message == "" {
		t.Fatalf("unexpected MCP panic error: %+v", body)
	}
	if strings.Contains(rec.Body.String(), "secret upstream state") {
		t.Fatalf("panic detail leaked to client: %s", rec.Body.String())
	}
}

func newRecoveryTestEngine(t *testing.T) *gin.Engine {
	t.Helper()
	a := &App{logger: slog.New(slog.NewTextHandler(io_Discard{}, nil))}
	return a.newBaseEngine()
}
