package app

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestRequestIDMiddlewareReusesValidHeader(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	e := gin.New()
	e.Use(requestIDMiddleware(nil))
	e.GET("/ping", func(c *gin.Context) {
		if got := c.GetString(requestIDKey); got != "req-123" {
			t.Fatalf("上下文 requestID 未透传，实际 %q", got)
		}
		c.String(http.StatusOK, "ok")
	})

	req := httptest.NewRequest(http.MethodGet, "/ping", nil)
	req.Header.Set(requestIDHeader, "req-123")
	w := httptest.NewRecorder()
	e.ServeHTTP(w, req)

	if got := w.Header().Get(requestIDHeader); got != "req-123" {
		t.Fatalf("响应头 request id 期望复用 req-123，实际 %q", got)
	}
}

func TestRequestIDMiddlewareGeneratesWhenMissingOrInvalid(t *testing.T) {
	for _, header := range []string{"", "bad id with spaces"} {
		gin.SetMode(gin.ReleaseMode)
		e := gin.New()
		e.Use(requestIDMiddleware(nil))
		e.GET("/ping", func(c *gin.Context) {
			if got := c.GetString(requestIDKey); got == "" || got == header {
				t.Fatalf("应生成新的 request id，header=%q got=%q", header, got)
			}
			c.String(http.StatusOK, "ok")
		})

		req := httptest.NewRequest(http.MethodGet, "/ping", nil)
		if header != "" {
			req.Header.Set(requestIDHeader, header)
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)

		if got := w.Header().Get(requestIDHeader); got == "" || got == header {
			t.Fatalf("响应头应包含新 request id，header=%q got=%q", header, got)
		}
	}
}

func TestSanitizeRequestID(t *testing.T) {
	if got := sanitizeRequestID("abc-DEF_123.:"); got != "abc-DEF_123.:" {
		t.Fatalf("合法 request id 不应改变，实际 %q", got)
	}
	if got := sanitizeRequestID("bad/value"); got != "" {
		t.Fatalf("非法字符应被拒绝，实际 %q", got)
	}
	if got := sanitizeRequestID(string(make([]byte, 129))); got != "" {
		t.Fatalf("过长 request id 应被拒绝，实际 %q", got)
	}
}
