package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件提供管理 REST API 的统一响应模型与辅助函数。
//
// 统一响应信封（envelope）：所有管理端点（/api/admin/*、/api/auth/*）的响应体均为
// 固定三段式 { code, message, data }：
//   - code：数字业务状态码。成功恒为 codeSuccess（20000）；失败为按错误类别映射的数字码。
//   - message：人类可读的提示文案，供前端默认提示直接展示。
//   - data：业务数据载荷。成功时为具体数据；失败时为 null 或 { fields } 字段级校验明细。
//
// HTTP 状态码仍表达"系统/传输语义"（200/201 成功，4xx/5xx 失败），与业务 code 互补：
// 前端拦截器据 HTTP 状态判定成败、据 code/message 做默认提示，业务层仍可拿到 code 与 data。
//
// 注意：对外 MCP API（/mcp/*，JSON-RPC 协议）与健康探针（/healthz）不使用本信封，
// 它们遵循各自的协议/约定格式，不在此处包装。

// codeSuccess 为成功响应的统一数字业务码。
const codeSuccess = 20000

// envelope 为统一响应信封。data 不省略，确保前端始终可读取 data 字段（成功为数据、失败为 null）。
type envelope struct {
	// Code 为数字业务状态码（成功 20000，失败见 codeToBusinessCode）。
	Code int `json:"code"`
	// Message 为人类可读提示，供前端默认提示展示。
	Message string `json:"message"`
	// Data 为业务数据载荷；失败时为 null 或含 fields 的字段级明细。
	Data any `json:"data"`
}

// respondOK 以 HTTP 200 返回成功信封。
func respondOK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, envelope{Code: codeSuccess, Message: "ok", Data: data})
}

// respondCreated 以 HTTP 201 返回创建成功信封。
func respondCreated(c *gin.Context, data any) {
	c.JSON(http.StatusCreated, envelope{Code: codeSuccess, Message: "created", Data: data})
}

// respondNoContent 表达"操作成功、无返回数据"的语义。
//
// 为便于前端拦截器对所有管理端点做一致解包，统一返回带信封的 HTTP 200（data 为 null），
// 而非裸 204；语义上等价于"成功且无数据"。
func respondNoContent(c *gin.Context) {
	c.JSON(http.StatusOK, envelope{Code: codeSuccess, Message: "ok", Data: nil})
}

// codeToStatus 将领域错误码映射为 HTTP 状态码。
//
// 未在表中列出的错误码（包括 nil 或非 APIError）由 respondError 兜底为 500。
func codeToStatus(code domain.ErrorCode) int {
	switch code {
	case domain.CodeValidation:
		return http.StatusBadRequest
	case domain.CodeNotFound:
		return http.StatusNotFound
	case domain.CodeConflict:
		return http.StatusConflict
	case domain.CodeUnauthorized:
		return http.StatusUnauthorized
	case domain.CodeForbidden:
		return http.StatusForbidden
	case domain.CodeRateLimited:
		return http.StatusTooManyRequests
	case domain.CodeUpstreamUnavailable:
		return http.StatusBadGateway
	case domain.CodeUpstreamTimeout:
		return http.StatusGatewayTimeout
	case domain.CodeToolNotFound:
		return http.StatusNotFound
	case domain.CodeBackupInvalid:
		return http.StatusBadRequest
	default:
		return http.StatusInternalServerError
	}
}

// codeToBusinessCode 将领域错误码映射为数字业务状态码。
//
// 约定：数字码大致对齐 HTTP 语义并细分类别，便于前端按 code 精确区分（而非解析字符串）：
//   - 4xxxx 客户端类错误；5xxxx 服务端类错误。
//
// 未在表中列出的错误码兜底为 50000（内部错误）。
func codeToBusinessCode(code domain.ErrorCode) int {
	switch code {
	case domain.CodeValidation:
		return 40000
	case domain.CodeUnauthorized:
		return 40100
	case domain.CodeForbidden:
		return 40300
	case domain.CodeNotFound:
		return 40400
	case domain.CodeToolNotFound:
		return 40401
	case domain.CodeConflict:
		return 40900
	case domain.CodeBackupInvalid:
		return 42200
	case domain.CodeRateLimited:
		return 42900
	case domain.CodeInternal:
		return 50000
	case domain.CodeUpstreamUnavailable:
		return 50200
	case domain.CodeUpstreamTimeout:
		return 50400
	default:
		return 50000
	}
}

// respondError 以统一信封返回错误响应并中止后续处理器。
//
//   - 若 err 为 *domain.APIError：按其 Code 映射 HTTP 状态码与数字业务码；message 取其
//     描述；字段级校验明细（若有）置于 data.fields。
//   - 否则：归类为 HTTP 500 / code 50000，以通用提示返回，不泄露底层错误细节。
func respondError(c *gin.Context, err error) {
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) {
		var data any
		if len(apiErr.Fields) > 0 {
			data = gin.H{"fields": apiErr.Fields}
		}
		c.AbortWithStatusJSON(codeToStatus(apiErr.Code), envelope{
			Code:    codeToBusinessCode(apiErr.Code),
			Message: apiErr.Message,
			Data:    data,
		})
		return
	}
	c.AbortWithStatusJSON(http.StatusInternalServerError, envelope{
		Code:    50000,
		Message: "服务器内部错误",
		Data:    nil,
	})
}

// bindJSON 将请求体解析为 dst；解析失败时以 VALIDATION 错误响应并返回 false。
//
// 统一在此处理 JSON 绑定错误，使各处理器无需各自构造校验错误响应。
func bindJSON(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		respondError(c, domain.NewValidationError("请求体格式非法", map[string]string{
			"body": err.Error(),
		}))
		return false
	}
	return true
}
