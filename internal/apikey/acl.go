package apikey

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"

	"github.com/gin-gonic/gin"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// MatchCIDR 判定来源地址 remoteIP 是否被 IP/CIDR 白名单 cidrs 放行（Req 13.9、13.10）。
//
// 语义（这是 ACL 访问控制的唯一判定真相，供中间件与属性测试 Property 21 复用）：
//   - 白名单为空（未配置 ACL）表示不限制来源，直接放行：返回 (true, nil)。
//   - 白名单非空时，当且仅当 remoteIP 落在其中任一条目所表示的网段内时放行：
//     命中返回 (true, nil)，全部未命中返回 (false, nil)。
//
// 白名单条目既接受带掩码的网段（如 "10.0.0.0/8"），也接受单个 IP（如 "1.2.3.4"，
// 按其位宽视作 /32 或 /128 的主机网段）。来源地址或任一白名单条目格式非法时返回
// CodeValidation 错误（allowed 为 false），由调用方按"无法核验即拒绝"处理。
//
// 该函数为纯函数：不依赖任何外部状态，匹配结果仅由入参决定，便于属性测试覆盖。
// IPv4-mapped IPv6 形式的来源地址（如 "::ffff:10.0.0.1"）会被归一为其 IPv4 形式后比较，
// 使其可命中等价的 IPv4 网段。
func MatchCIDR(remoteIP string, cidrs []string) (bool, error) {
	if len(cidrs) == 0 {
		// 未配置白名单：不限制来源（Req 13.9 的反面——无限制即放行）。
		return true, nil
	}

	addr, err := netip.ParseAddr(remoteIP)
	if err != nil {
		return false, domain.NewError(domain.CodeValidation, "来源地址格式非法："+remoteIP)
	}
	addr = addr.Unmap() // 归一 IPv4-mapped IPv6，使其可命中等价的 IPv4 网段。

	for _, entry := range cidrs {
		prefix, perr := parseCIDREntry(entry)
		if perr != nil {
			return false, perr
		}
		if prefix.Contains(addr) {
			return true, nil
		}
	}
	return false, nil
}

// parseCIDREntry 将单条白名单文本解析为规范化网段。
//
// 既接受带掩码的网段（"10.0.0.0/8"），也接受单个 IP（"1.2.3.4" / "::1"，补全为主机掩码）。
// 文本为空或格式非法时返回 CodeValidation 错误。
func parseCIDREntry(s string) (netip.Prefix, error) {
	if s == "" {
		return netip.Prefix{}, domain.NewError(domain.CodeValidation, "白名单 CIDR 条目不能为空")
	}
	// 优先按带掩码的网段解析，并对齐到网络边界。
	if prefix, err := netip.ParsePrefix(s); err == nil {
		return prefix.Masked(), nil
	}
	// 退化为单个 IP，按其位宽补全为主机网段（/32 或 /128）。
	if addr, err := netip.ParseAddr(s); err == nil {
		addr = addr.Unmap()
		return netip.PrefixFrom(addr, addr.BitLen()), nil
	}
	return netip.Prefix{}, domain.NewError(domain.CodeValidation, "白名单 CIDR 条目格式非法："+s)
}

// ACLLister 是 ACL 校验依赖的白名单加载窄接口（Req 13.9）。
//
// 仅声明本组件实际使用的方法：按 API Key 标识返回其全部来源白名单记录。
// *store.ACLRepo 满足该接口。以接口而非具体类型依赖，便于在单元测试中以内存 fake 替换，
// 使核心放行判定可脱离真实数据库验证。
type ACLLister interface {
	// ListByAPIKey 返回某 API Key 的全部来源白名单记录；无数据返回空切片。
	ListByAPIKey(ctx context.Context, apiKeyID string) ([]store.ACLEntry, error)
}

// ACLKeyResolver 从 gin 上下文中解析当前请求所属的 API Key 元数据。
//
// ACL 中间件位于 API Key 鉴权之后（见设计文档「鉴权中间件」的对外链路），鉴权通过后
// 将 API Key 元数据写入上下文，本解析器据此取出待校验来源的 Key。以函数注入而非固定
// 上下文键依赖，便于解耦与单元测试。返回 ok=false 表示上下文中无 API Key。
type ACLKeyResolver func(c *gin.Context) (Metadata, bool)

// ACLGuard 实现按 API Key 的来源白名单（IP/CIDR）访问控制（Req 13.9、13.10）。
//
// 它从仓储加载某 API Key 的白名单，结合请求来源地址调用 MatchCIDR 完成放行判定：
// 配置了白名单时仅放行命中条目的来源，未命中返回 FORBIDDEN；未配置白名单时不限制来源。
type ACLGuard struct {
	// lister 为来源白名单仓储。
	lister ACLLister
	// logger 用于记录白名单加载等异常；为空时回退到 slog.Default()。
	logger *slog.Logger
}

// NewACLGuard 构造来源白名单访问控制组件。lister 为必需依赖；logger 为空时回退到默认 logger。
func NewACLGuard(lister ACLLister, logger *slog.Logger) *ACLGuard {
	if logger == nil {
		logger = slog.Default()
	}
	return &ACLGuard{lister: lister, logger: logger}
}

// Allow 判定来源地址 remoteIP 是否被允许使用 apiKeyID 对应的 API Key（Req 13.9、13.10）。
//
// 流程：加载该 API Key 的来源白名单 → 调用 MatchCIDR 判定放行。
// 未配置白名单（空）时不限制来源，返回 (true, nil)；配置后仅命中条目的来源被放行。
// 加载失败或地址/条目格式非法时返回错误（allowed 为 false），由调用方按"无法核验即拒绝"处理。
//
// 该判定函数可被鉴权链直接复用，无需经由 gin 中间件。
func (g *ACLGuard) Allow(ctx context.Context, apiKeyID, remoteIP string) (bool, error) {
	entries, err := g.lister.ListByAPIKey(ctx, apiKeyID)
	if err != nil {
		return false, err
	}
	return MatchCIDR(remoteIP, cidrsFromEntries(entries))
}

// cidrsFromEntries 抽取白名单记录中的 CIDR 文本列表。
func cidrsFromEntries(entries []store.ACLEntry) []string {
	cidrs := make([]string, 0, len(entries))
	for i := range entries {
		cidrs = append(cidrs, entries[i].CIDR)
	}
	return cidrs
}

// Middleware 返回一个按 API Key 来源白名单校验的 gin 中间件（Req 13.9、13.10）。
//
// 中间件流程：
//   - 通过 resolve 从上下文取出当前 API Key；取不到则放行（无校验对象，交由前序鉴权负责拒绝）。
//   - 以请求客户端 IP（c.ClientIP()）为来源地址调用 Allow 判定是否放行。
//   - 未通过（来源不在白名单内）则以统一错误模型（domain.APIError，code=FORBIDDEN）
//     返回 HTTP 403 并中止后续处理器（Req 13.10）。
//   - 通过或未配置白名单则放行至下一处理器（Req 13.9）。
//
// 安全考量：ACL 是访问控制边界，遵循"无法核验即拒绝"（fail-closed）。当白名单加载失败或
// 来源地址无法解析时，记录告警并返回 FORBIDDEN，绝不在无法确认来源合法时放行，以信守
// "仅允许白名单内来源"的安全保证。这与限流中间件可用性优先的降级放行策略刻意不同。
//
// resolve 为 nil 时中间件直接放行，避免误将其接入到无法解析 Key 的链路上造成全量拒绝。
func (g *ACLGuard) Middleware(resolve ACLKeyResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		if resolve == nil {
			c.Next()
			return
		}

		key, ok := resolve(c)
		if !ok {
			// 上下文中无 API Key：无校验对象，放行（前序鉴权中间件负责拒绝无效请求）。
			c.Next()
			return
		}

		allowed, err := g.Allow(c.Request.Context(), key.ID, c.ClientIP())
		if err != nil {
			// 无法核验来源（白名单加载失败或地址非法）：fail-closed，拒绝以信守安全保证。
			g.logger.Warn("来源白名单校验失败，拒绝请求", "apiKeyID", key.ID, "clientIP", c.ClientIP(), "error", err)
			abortForbidden(c)
			return
		}

		if !allowed {
			abortForbidden(c)
			return
		}

		c.Next()
	}
}

// abortForbidden 以统一错误模型返回 HTTP 403 并中止后续处理器执行（Req 13.10）。
func abortForbidden(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden,
		domain.NewError(domain.CodeForbidden, "请求来源不在该 API Key 的访问白名单内"))
}
