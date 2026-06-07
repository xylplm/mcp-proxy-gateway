package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件验证统一错误模型与校验错误响应（任务 19.3，Req 2.2、4.8、14.10）：
//   1. 每个领域错误码都集中映射到正确的 HTTP 状态码；
//   2. 校验错误响应携带逐字段的 Fields 映射，标识每个无效字段；
//   3. 非 *domain.APIError 的未知错误兜底为 500 且不泄露原始错误信息；
//   4. bindJSON 对非法请求体返回带字段信息的 VALIDATION 错误。

// errBody 为错误响应信封的解析视图（批 1 统一响应模型）。
type errBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		Fields map[string]string `json:"fields"`
	} `json:"data"`
}

// runRespondError 在一个最小 gin 上下文中调用 respondError，并返回状态码与解析后的错误信封。
func runRespondError(t *testing.T, err error) (int, errBody) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	respondError(c, err)

	var body errBody
	if uerr := json.Unmarshal(w.Body.Bytes(), &body); uerr != nil {
		t.Fatalf("解析错误响应体失败：%v，原始响应 %s", uerr, w.Body.String())
	}
	return w.Code, body
}

// TestCodeToStatusMappingComplete 验证每个领域错误码都映射到约定的 HTTP 状态码与数字业务码。
func TestCodeToStatusMappingComplete(t *testing.T) {
	cases := []struct {
		code    domain.ErrorCode
		status  int
		bizCode int
	}{
		{domain.CodeValidation, http.StatusBadRequest, 40000},
		{domain.CodeNotFound, http.StatusNotFound, 40400},
		{domain.CodeConflict, http.StatusConflict, 40900},
		{domain.CodeUnauthorized, http.StatusUnauthorized, 40100},
		{domain.CodeForbidden, http.StatusForbidden, 40300},
		{domain.CodeRateLimited, http.StatusTooManyRequests, 42900},
		{domain.CodeUpstreamUnavailable, http.StatusBadGateway, 50200},
		{domain.CodeUpstreamTimeout, http.StatusGatewayTimeout, 50400},
		{domain.CodeToolNotFound, http.StatusNotFound, 40401},
		{domain.CodeBackupInvalid, http.StatusBadRequest, 42200},
	}
	for _, tc := range cases {
		t.Run(string(tc.code), func(t *testing.T) {
			status, body := runRespondError(t, domain.NewError(tc.code, "boom"))
			if status != tc.status {
				t.Fatalf("错误码 %s 期望状态 %d，实际 %d", tc.code, tc.status, status)
			}
			if body.Code != tc.bizCode {
				t.Errorf("响应体业务码期望 %d，实际 %d", tc.bizCode, body.Code)
			}
			if body.Message != "boom" {
				t.Errorf("响应体 message 期望原样透传，实际 %q", body.Message)
			}
		})
	}
}

// TestValidationErrorCarriesPerFieldMap 验证校验错误响应在 data.fields 携带逐字段映射（Req 2.2、4.8、14.10）。
func TestValidationErrorCarriesPerFieldMap(t *testing.T) {
	fields := map[string]string{
		"name":      "长度需在 1 至 100 个字符之间",
		"transport": "不支持的传输类型",
	}
	status, body := runRespondError(t, domain.NewValidationError("校验失败", fields))

	if status != http.StatusBadRequest {
		t.Fatalf("校验错误期望 HTTP 400，实际 %d", status)
	}
	if body.Code != 40000 {
		t.Errorf("期望业务码 40000(VALIDATION)，实际 %d", body.Code)
	}
	if len(body.Data.Fields) != len(fields) {
		t.Fatalf("期望 %d 个字段级错误，实际 %d：%+v", len(fields), len(body.Data.Fields), body.Data.Fields)
	}
	for k, v := range fields {
		if body.Data.Fields[k] != v {
			t.Errorf("字段 %q 期望错误说明 %q，实际 %q", k, v, body.Data.Fields[k])
		}
	}
}

// TestUnknownErrorMapsTo500WithoutLeak 验证非 APIError 的未知错误兜底为 500 且不泄露原始信息。
func TestUnknownErrorMapsTo500WithoutLeak(t *testing.T) {
	const secret = "数据库连接字符串 password=topsecret 暴露了"
	status, body := runRespondError(t, errors.New(secret))

	if status != http.StatusInternalServerError {
		t.Fatalf("未知错误期望 HTTP 500，实际 %d", status)
	}
	if body.Code != 50000 {
		t.Errorf("期望兜底业务码 50000(INTERNAL)，实际 %d", body.Code)
	}
	if body.Message == secret {
		t.Error("响应不应回显原始错误信息（泄露内部细节）")
	}
	if body.Data.Fields != nil {
		t.Errorf("通用 500 错误不应携带字段级信息，实际 %+v", body.Data.Fields)
	}
}

// TestWrappedAPIErrorIsUnwrapped 验证被包裹的 *domain.APIError 仍能被识别并正确映射。
func TestWrappedAPIErrorIsUnwrapped(t *testing.T) {
	wrapped := errors.Join(errors.New("上下文"), domain.NewError(domain.CodeConflict, "名称重复"))
	status, body := runRespondError(t, wrapped)

	if status != http.StatusConflict {
		t.Fatalf("被包裹的 CONFLICT 期望 HTTP 409，实际 %d", status)
	}
	if body.Code != 40900 {
		t.Errorf("期望业务码 40900(CONFLICT)，实际 %d", body.Code)
	}
}

// TestBindJSONInvalidBodyReturnsValidationError 验证 bindJSON 对非法请求体返回带字段信息的 VALIDATION 错误。
func TestBindJSONInvalidBodyReturnsValidationError(t *testing.T) {
	keys := &fakeAPIKeys{}
	e := newTestEngine(Deps{APIKeys: keys})

	// 发送非法 JSON 触发绑定失败。
	w := doJSON(e, http.MethodPost, "/api/admin/apikeys", `{"name":`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("非法请求体期望 HTTP 400，实际 %d，响应体 %s", w.Code, w.Body.String())
	}
	code, _, fields := parseErrorEnvelope(t, w)
	if code != 40000 {
		t.Errorf("期望业务码 40000(VALIDATION)，实际 %d", code)
	}
	if _, ok := fields["body"]; !ok {
		t.Errorf("校验错误应通过 data.fields 标识无效字段，实际 %+v", fields)
	}
}
