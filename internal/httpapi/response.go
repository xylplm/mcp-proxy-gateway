package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件提供管理 REST API 的统一响应辅助：把领域层的 *domain.APIError 错误码映射为
// 恰当的 HTTP 状态码并以统一结构返回（与任务 19.3「统一错误模型」一致的基础实现）。
//
// 设计要点：处理器只需把应用服务/仓储返回的 error 透传给 respondError，由本文件集中
// 完成「错误码 → HTTP 状态」的映射，避免在每个处理器内重复判断。非 *domain.APIError
// 的未知错误一律归类为 500，并以 VALIDATION 之外的通用错误体返回，避免泄露内部细节。

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

// respondError 以统一错误模型返回错误响应并中止后续处理器。
//
//   - 若 err 为 *domain.APIError：按其 Code 映射 HTTP 状态码，原样返回该结构（含字段级
//     校验错误 Fields）。
//   - 否则：归类为 500 INTERNAL，以通用错误体返回，不泄露底层错误细节。
func respondError(c *gin.Context, err error) {
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) {
		c.AbortWithStatusJSON(codeToStatus(apiErr.Code), apiErr)
		return
	}
	c.AbortWithStatusJSON(http.StatusInternalServerError,
		domain.NewError(domain.CodeInternal, "服务器内部错误"))
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
