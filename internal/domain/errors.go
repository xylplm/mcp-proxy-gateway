package domain

import "fmt"

// ErrorCode 为统一错误模型的错误类别码。
type ErrorCode string

const (
	// CodeValidation 表示输入校验失败。
	CodeValidation ErrorCode = "VALIDATION"
	// CodeNotFound 表示目标资源不存在。
	CodeNotFound ErrorCode = "NOT_FOUND"
	// CodeConflict 表示资源冲突（如名称重复）。
	CodeConflict ErrorCode = "CONFLICT"
	// CodeUnauthorized 表示鉴权失败。
	CodeUnauthorized ErrorCode = "UNAUTHORIZED"
	// CodeForbidden 表示访问被拒绝（如来源不在白名单内）。
	CodeForbidden ErrorCode = "FORBIDDEN"
	// CodeRateLimited 表示触发限流。
	CodeRateLimited ErrorCode = "RATE_LIMITED"
	// CodeUpstreamUnavailable 表示上游 MCP 连接不可用。
	CodeUpstreamUnavailable ErrorCode = "UPSTREAM_UNAVAILABLE"
	// CodeUpstreamTimeout 表示上游 MCP 调用超时。
	CodeUpstreamTimeout ErrorCode = "UPSTREAM_TIMEOUT"
	// CodeToolNotFound 表示目标工具不存在于可见聚合工具集合中。
	CodeToolNotFound ErrorCode = "TOOL_NOT_FOUND"
	// CodeBackupInvalid 表示导入的配置备份文件格式无效或内容校验失败（Req 23.6）。
	CodeBackupInvalid ErrorCode = "BACKUP_INVALID"
	// CodeInternal 表示未归类的服务器内部错误，用于兜底未知 error，避免泄露内部细节。
	CodeInternal ErrorCode = "INTERNAL"
)

// APIError 为统一错误响应结构，区分错误类别以便前端与调用方处理。
type APIError struct {
	// Code 为错误类别码。
	Code ErrorCode `json:"code"`
	// Message 为人类可读的错误描述。
	Message string `json:"message"`
	// Fields 为字段级校验错误，键为字段名、值为错误说明。
	Fields map[string]string `json:"fields,omitempty"`
	// cause 仅供进程内错误分类使用，不参与 JSON 响应，避免泄露底层网络与 SDK 细节。
	cause error
}

// Error 实现 error 接口。
func (e *APIError) Error() string {
	if len(e.Fields) > 0 {
		return fmt.Sprintf("%s: %s %v", e.Code, e.Message, e.Fields)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

// Unwrap 保留进程内的根因链，供连接生命周期精确识别网络/会话终态错误；对外响应
// 始终只使用 Code 和 Message，不会暴露 cause。
func (e *APIError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

// NewError 构造一个不含字段级错误的 APIError。
func NewError(code ErrorCode, message string) *APIError {
	return &APIError{Code: code, Message: message}
}

// WrapError 构造统一错误并保留根因，供内部以 errors.Is/errors.As 做安全分类。
func WrapError(code ErrorCode, message string, cause error) *APIError {
	return &APIError{Code: code, Message: message, cause: cause}
}

// NewValidationError 构造一个携带字段级错误的校验类 APIError。
func NewValidationError(message string, fields map[string]string) *APIError {
	return &APIError{Code: CodeValidation, Message: message, Fields: fields}
}

// ErrNotImplemented 为骨架阶段的占位错误，表示对应能力尚未实现。
var ErrNotImplemented = NewError(CodeValidation, "尚未实现")
