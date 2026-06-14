package auth

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// ctxClaimsKey 是校验通过的会话信息在 gin.Context 中的存储键。
//
// 使用包内非导出的字符串常量作为键，避免与其他中间件写入的键冲突，
// 同时通过导出的 ClaimsFromContext 提供受控的读取入口。
const ctxClaimsKey = "auth.claims"

// authorizationHeader 为承载访问令牌的请求头名称。
const authorizationHeader = "Authorization"

// bearerPrefix 为 Authorization 头中令牌值的方案前缀（区分大小写按 RFC 6750，
// 此处不区分大小写匹配以提升兼容性）。
const bearerPrefix = "Bearer "

// TokenParser 是中间件依赖的令牌校验窄接口。
//
// 仅声明中间件实际使用的方法：解析并校验访问令牌、返回其会话信息。
// *Service 满足该接口（其 ParseToken 校验签名方法、签名有效性与过期时间，
// 失败时返回 domain.CodeUnauthorized 错误，过期判定即覆盖会话超时，Req 1.7）。
// 以接口而非具体类型依赖，便于单元测试替换。
type TokenParser interface {
	// ParseToken 校验并解析访问令牌，返回其会话信息；缺失/无效/过期返回错误。
	ParseToken(tokenString string) (Claims, error)
}

// AccessDeniedHook 是鉴权失败时的回调，供装配层接入审计记录（Req 22.3）。
//
// 在 RequireAdmin 每个鉴权失败点（parser 未初始化/缺令牌/令牌无效或过期）abort 之前触发，
// 传入当前请求上下文与拒绝原因。回调应快速返回（如异步提交审计），不得阻塞鉴权链路或返回错误。
// 为 nil 时跳过（默认），保持向后兼容。
type AccessDeniedHook func(c *gin.Context, reason string)

// Option 为 RequireAdmin 的可选配置项（函数式选项）。
type Option func(*requireAdminConfig)

// requireAdminConfig 聚合 RequireAdmin 的可选配置。
type requireAdminConfig struct {
	onAccessDenied AccessDeniedHook
}

// WithAccessDeniedHook 注入鉴权失败回调，供装配层在管理员后台鉴权失败时记录 access_denied 审计（Req 22.3）。
//
// 回调在 abort 之前触发，可通过 c.Request.URL.Path 取得被拒目标、reason 作为明细。
func WithAccessDeniedHook(hook AccessDeniedHook) Option {
	return func(cfg *requireAdminConfig) {
		cfg.onAccessDenied = hook
	}
}

// RequireAdmin 返回一个校验管理员 JWT 的 gin 中间件（Req 1.6、1.7）。
//
// 中间件流程：
//   - 从 Authorization 头提取 Bearer 令牌；缺失或格式不符返回 UNAUTHORIZED 并中止。
//   - 调用 parser.ParseToken 校验签名与过期；无效或已过期（含超过会话超时）返回
//     UNAUTHORIZED 并中止（Req 1.6、1.7）。
//   - 校验通过则将 Claims 存入 gin.Context 供后续处理使用，并放行至下一处理器。
//
// 鉴权失败时以统一错误模型（domain.APIError，code=UNAUTHORIZED）返回 HTTP 401，
// 并调用 c.Abort() 阻断后续处理器执行；若注入了 AccessDeniedHook，会在 abort 前触发以供记录 access_denied 审计（Req 22.3）。
// parser 为 nil 时该中间件会拒绝所有请求，避免误将未配置鉴权的端点暴露为无保护。
func RequireAdmin(parser TokenParser, opts ...Option) gin.HandlerFunc {
	cfg := &requireAdminConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(cfg)
		}
	}
	return func(c *gin.Context) {
		if parser == nil {
			deny(c, cfg.onAccessDenied, "鉴权中间件未正确初始化")
			return
		}

		token, ok := extractBearerToken(c.GetHeader(authorizationHeader))
		if !ok {
			// 令牌缺失或 Authorization 头格式不符（Req 1.6）。
			deny(c, cfg.onAccessDenied, "缺少有效的 Authorization Bearer 令牌")
			return
		}

		claims, err := parser.ParseToken(token)
		if err != nil {
			// 令牌无效、签名不符或已过期（含超过会话超时，Req 1.6、1.7）。
			deny(c, cfg.onAccessDenied, "令牌无效或已过期")
			return
		}

		// 校验通过：将会话信息存入上下文供后续处理器使用。
		c.Set(ctxClaimsKey, claims)
		c.Next()
	}
}

// deny 统一处理鉴权失败：先触发审计回调（如果注入），再返回 401 并 abort。
func deny(c *gin.Context, hook AccessDeniedHook, message string) {
	if hook != nil {
		hook(c, message)
	}
	abortUnauthorized(c, message)
}

// ClaimsFromContext 从 gin.Context 读取由 RequireAdmin 存入的会话信息。
//
// 仅当请求已通过 RequireAdmin 校验时返回的 ok 为 true。后续处理器据此可获取
// 当前管理员身份而无需再次解析令牌。
func ClaimsFromContext(c *gin.Context) (Claims, bool) {
	v, exists := c.Get(ctxClaimsKey)
	if !exists {
		return Claims{}, false
	}
	claims, ok := v.(Claims)
	return claims, ok
}

// extractBearerToken 从 Authorization 头值中提取 Bearer 令牌。
//
// 接受形如 "Bearer <token>" 的头值（方案前缀不区分大小写），去除两侧空白后
// 要求令牌非空。不符合该形式或令牌为空时返回 ok=false。
func extractBearerToken(header string) (string, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", false
	}
	if len(header) < len(bearerPrefix) || !strings.EqualFold(header[:len(bearerPrefix)], bearerPrefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(bearerPrefix):])
	if token == "" {
		return "", false
	}
	return token, true
}

// abortUnauthorized 以统一错误模型返回 HTTP 401 并中止后续处理器执行（Req 1.6）。
func abortUnauthorized(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusUnauthorized, domain.NewError(domain.CodeUnauthorized, message))
}
