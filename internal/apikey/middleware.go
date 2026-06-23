package apikey

import (
	"context"
	"crypto/sha256"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 提取 API Key 的请求头与查询参数名称约定（Req 11.9、12.5）。
//
// 对外 MCP API 客户端可通过以下任一方式携带 API Key（按下列顺序取首个非空值）：
//   - X-API-Key 请求头；
//   - Authorization 请求头的 Bearer 方案（"Authorization: Bearer <key>"）；
//   - api_key 查询参数。
const (
	// apiKeyHeader 为承载 API Key 的专用请求头名称。
	apiKeyHeader = "X-API-Key"
	// authorizationHeaderName 为承载 Bearer 令牌的标准请求头名称。
	authorizationHeaderName = "Authorization"
	// bearerScheme 为 Authorization 头中 API Key 值的方案前缀（不区分大小写匹配）。
	bearerScheme = "Bearer "
	// apiKeyQueryParam 为承载 API Key 的查询参数名称。
	apiKeyQueryParam = "api_key"
)

// ctxAPIKeyMetadataKey 是鉴权通过的 API Key 元数据在 gin.Context 中的存储键。
//
// 鉴权中间件（本文件）校验通过后将 Metadata 写入该键；ACL（acl.go）与限流（ratelimit.go）
// 中间件位于鉴权之后，复用同一上下文键取出当前 API Key——MetadataFromContext 即为统一读取入口，
// 其函数签名与 ACLKeyResolver、RateLimitKeyResolver 一致，可直接作为二者的 resolver 传入。
const ctxAPIKeyMetadataKey = "apikey.metadata"

const (
	AuthFailureMissingKey  = "missing_key"
	AuthFailureInvalidKey  = "invalid_key"
	AuthFailureLookupError = "lookup_error"
)

// AuthKeyLookup 是 API Key 鉴权依赖的按哈希查找窄接口（Req 12.5）。
//
// 仅声明鉴权实际使用的方法：按密钥哈希定位一条 API Key。*store.APIKeyRepo 满足该接口
// （其 GetByHash 在无匹配时返回 domain.CodeNotFound 错误）。以接口而非具体类型依赖，
// 便于在单元测试中以内存 fake 替换，使核心鉴权判定可脱离真实数据库验证。
type AuthKeyLookup interface {
	// GetByHash 按密钥哈希查询单条 API Key；不存在返回 NOT_FOUND。
	GetByHash(ctx context.Context, keyHash []byte) (store.APIKey, error)
}

// Authenticator 实现对外 MCP API 的 API Key 鉴权（Req 11.9、12.5）。
//
// 它从请求中提取 API Key 明文，按与创建时一致的 SHA-256 方案（见 manager.go 的 generateKey）
// 计算哈希后查库定位 Key，再用 Metadata.Usable(now) 判定其是否启用且未过期。任一环节不通过
// 即返回鉴权失败（UNAUTHORIZED）并中止请求，不会将请求路由到任何聚合能力。
type Authenticator struct {
	// lookup 为按哈希查找 API Key 的仓储。
	lookup AuthKeyLookup
	// logger 用于记录查找后端异常；为空时回退到 slog.Default()。
	logger *slog.Logger
	// failureRecorder 在鉴权失败时记录安全事件；为空时跳过。
	failureRecorder AuthFailureRecorder
}

// AuthFailureRecorder 是鉴权失败旁路记录器，供安全中心接入。
type AuthFailureRecorder interface {
	RecordAuthFailure(c *gin.Context, plaintext, reason string)
}

// NewAuthenticator 构造 API Key 鉴权器。lookup 为必需依赖；logger 为空时回退到默认 logger。
func NewAuthenticator(lookup AuthKeyLookup, logger *slog.Logger) *Authenticator {
	if logger == nil {
		logger = slog.Default()
	}
	return &Authenticator{lookup: lookup, logger: logger}
}

// WithFailureRecorder 注入鉴权失败记录器。
func (a *Authenticator) WithFailureRecorder(recorder AuthFailureRecorder) *Authenticator {
	a.failureRecorder = recorder
	return a
}

// Authenticate 校验请求所携带的 API Key 并返回其元数据（Req 11.9、12.5）。
//
// 流程：提取明文 → SHA-256 哈希 → 按哈希查库 → Metadata.Usable(now) 判定启用且未过期。
// 任一不通过返回 ok=false 与一条 UNAUTHORIZED 错误，调用方据此拒绝请求且不路由到聚合能力。
//
// 安全考量：鉴权是访问控制边界，遵循"无法核验即拒绝"（fail-closed）。无论 API Key 缺失、
// 不存在、查库失败，还是已停用/已过期，对外都返回同一类 UNAUTHORIZED，不泄露失败的具体原因，
// 避免被用于探测有效密钥；查库的基础设施异常额外记录告警以便排障。
func (a *Authenticator) Authenticate(ctx context.Context, plaintext string, now time.Time) (Metadata, bool, error) {
	meta, ok, _, err := a.AuthenticateDetailed(ctx, plaintext, now)
	return meta, ok, err
}

// AuthenticateDetailed 校验 API Key 并返回内部失败原因，供安全中心区分无效尝试与后端异常。
func (a *Authenticator) AuthenticateDetailed(ctx context.Context, plaintext string, now time.Time) (Metadata, bool, string, error) {
	plaintext = strings.TrimSpace(plaintext)
	if plaintext == "" {
		return Metadata{}, false, AuthFailureMissingKey, domain.NewError(domain.CodeUnauthorized, "缺少 API Key")
	}

	sum := sha256.Sum256([]byte(plaintext))
	row, err := a.lookup.GetByHash(ctx, sum[:])
	if err != nil {
		// 不存在（NOT_FOUND）视为鉴权失败；其余为基础设施异常，记录告警后同样 fail-closed。
		var apiErr *domain.APIError
		if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeNotFound {
			a.logger.Warn("API Key 查找失败，按鉴权失败处理", "error", err)
			return Metadata{}, false, AuthFailureLookupError, domain.NewError(domain.CodeUnauthorized, "API Key 鉴权暂不可用")
		}
		return Metadata{}, false, AuthFailureInvalidKey, domain.NewError(domain.CodeUnauthorized, "API Key 无效")
	}

	meta := toMetadata(row)
	if !meta.Usable(now) {
		// 已停用或已超过有效期（Req 12.4、12.5、12.6）。
		return Metadata{}, false, AuthFailureInvalidKey, domain.NewError(domain.CodeUnauthorized, "API Key 已停用或已过期")
	}

	return meta, true, "", nil
}

// Middleware 返回一个校验对外 MCP API 请求所携带 API Key 的 gin 中间件（Req 11.9、12.5）。
//
// 中间件流程：
//   - 从请求头/查询参数提取 API Key（见 extractAPIKey 的取值顺序）。
//   - 调用 Authenticate 校验存在/启用/未过期；任一不通过以统一错误模型
//     （domain.APIError，code=UNAUTHORIZED）返回 HTTP 401 并 c.Abort()，不路由到任何聚合能力。
//   - 校验通过则将 API Key 元数据（含 ID）写入 gin.Context，供后续 ACL、限流与聚合处理读取，
//     并放行至下一处理器。
//
// 鉴权判定以服务器当前时刻为准。
func (a *Authenticator) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		plaintext, ok := extractAPIKey(c)
		if !ok {
			if a.failureRecorder != nil {
				a.failureRecorder.RecordAuthFailure(c, "", AuthFailureMissingKey)
			}
			abortUnauthorized(c, "缺少 API Key")
			return
		}

		meta, ok, reason, _ := a.AuthenticateDetailed(c.Request.Context(), plaintext, time.Now())
		if !ok {
			// 不存在/已停用/已过期或查库失败：统一返回鉴权失败，不暴露具体原因（Req 12.5）。
			if a.failureRecorder != nil && reason != AuthFailureLookupError {
				a.failureRecorder.RecordAuthFailure(c, plaintext, reason)
			}
			abortUnauthorized(c, "API Key 无效或已失效")
			return
		}

		// 校验通过：将元数据存入上下文，供 ACL/限流/聚合等后续处理器复用同一上下文键。
		c.Set(ctxAPIKeyMetadataKey, meta)
		c.Next()
	}
}

// MetadataFromContext 从 gin.Context 读取由鉴权中间件存入的 API Key 元数据。
//
// 仅当请求已通过 Authenticator.Middleware 校验时返回的 ok 为 true。其函数签名与
// ACLKeyResolver、RateLimitKeyResolver 一致，可直接作为二者的 resolver 传入，使
// ACL（acl.go）与限流（ratelimit.go）中间件与本中间件共享同一上下文键。
func MetadataFromContext(c *gin.Context) (Metadata, bool) {
	v, exists := c.Get(ctxAPIKeyMetadataKey)
	if !exists {
		return Metadata{}, false
	}
	meta, ok := v.(Metadata)
	return meta, ok
}

// extractAPIKey 从请求头或查询参数中提取 API Key 明文（Req 11.9、12.5）。
//
// 取值顺序：X-API-Key 头 → Authorization Bearer 头 → api_key 查询参数；
// 取首个去除两侧空白后非空的值。三者皆缺失或为空时返回 ok=false。
func extractAPIKey(c *gin.Context) (string, bool) {
	return ExtractAPIKey(c)
}

// ExtractAPIKey 从请求头或查询参数中提取 API Key 明文。
func ExtractAPIKey(c *gin.Context) (string, bool) {
	if v := strings.TrimSpace(c.GetHeader(apiKeyHeader)); v != "" {
		return v, true
	}
	if token, ok := extractBearerKey(c.GetHeader(authorizationHeaderName)); ok {
		return token, true
	}
	if v := strings.TrimSpace(c.Query(apiKeyQueryParam)); v != "" {
		return v, true
	}
	return "", false
}

// extractBearerKey 从 Authorization 头值中提取 Bearer 方案携带的 API Key。
//
// 接受形如 "Bearer <key>" 的头值（方案前缀不区分大小写），去除两侧空白后要求其非空。
// 不符合该形式或值为空时返回 ok=false。
func extractBearerKey(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if len(header) < len(bearerScheme) || !strings.EqualFold(header[:len(bearerScheme)], bearerScheme) {
		return "", false
	}
	key := strings.TrimSpace(header[len(bearerScheme):])
	if key == "" {
		return "", false
	}
	return key, true
}

// abortUnauthorized 以统一错误模型返回 HTTP 401 并中止后续处理器执行（Req 11.9、12.5）。
func abortUnauthorized(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized,
		domain.NewError(domain.CodeUnauthorized, message))
}
