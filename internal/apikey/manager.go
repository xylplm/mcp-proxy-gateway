package apikey

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// 名称长度约束：API Key 名称需在 1 至 100 个字符之间（Req 12.1、12.8）。
const (
	// minNameLen 为 API Key 名称的最小字符数。
	minNameLen = 1
	// maxNameLen 为 API Key 名称的最大字符数。
	maxNameLen = 100
)

// 明文密钥生成与哈希方案相关常量。
const (
	// keyPlaintextPrefix 为明文密钥的固定前缀，便于人眼与日志中辨识来源。
	keyPlaintextPrefix = "mpg_"
	// keyRandomBytes 为明文密钥所含随机熵的字节数（256 位），足以抵御暴力枚举。
	keyRandomBytes = 32
	// keyPrefixLen 为展示用前缀的字符数；与 api_key.key_prefix 的 VARCHAR(12) 对齐。
	keyPrefixLen = 12
)

// APIKeyRepository 是 API Key 管理器依赖的仓储窄接口（Req 12）。
//
// 仅声明本组件实际使用的方法，便于在单元测试（任务 14.2）中以 mock 替换，
// 同时使依赖关系一目了然。*store.APIKeyRepo 满足该接口。
//
// 说明：鉴权按哈希查找（GetByHash）属鉴权中间件职责（任务 14.4），故此处不纳入。
type APIKeyRepository interface {
	// Create 持久化一条 API Key 元数据并回填生成标识与创建时间；名称重复返回 CONFLICT。
	Create(ctx context.Context, key store.APIKey) (store.APIKey, error)
	// Get 按标识查询单条 API Key；不存在返回 NOT_FOUND。
	Get(ctx context.Context, id string) (store.APIKey, error)
	// List 返回全部 API Key 元数据，无数据返回空切片。
	List(ctx context.Context) ([]store.APIKey, error)
	// SetEnabled 仅更新启停状态；不存在返回 NOT_FOUND。
	SetEnabled(ctx context.Context, id string, enabled bool) error
	// Delete 删除一条 API Key；不存在返回 NOT_FOUND。
	Delete(ctx context.Context, id string) error
}

// Metadata 为 API Key 的对外元数据视图。
//
// 自部署场景下允许二次查看明文：PlaintextKey 携带完整明文密钥，供管理台查看/复制。
// List 与 Get 均返回此视图（含明文）。鉴权仍走哈希等值查询，明文不参与鉴权。
type Metadata struct {
	// ID 为 API Key 唯一标识。
	ID string `json:"id"`
	// Name 为名称。
	Name string `json:"name"`
	// PlaintextKey 为完整明文密钥，供管理台二次查看/复制（自部署场景）。
	PlaintextKey string `json:"plaintextKey"`
	// KeyPrefix 为展示用前缀（明文的前若干字符），用于在界面上区分不同 Key。
	KeyPrefix string `json:"keyPrefix"`
	// Enabled 表示该 Key 是否启用。
	Enabled bool `json:"enabled"`
	// ExpiresAt 为可选有效期；nil 表示永不过期（Req 12.6）。
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	// RateLimit 为可选速率上限；nil 表示不限流（Req 21，由后续任务配置）。
	RateLimit *int `json:"rateLimit,omitempty"`
	// RateWindowS 为限流计数窗口秒数；nil 表示未配置。
	RateWindowS *int `json:"rateWindowS,omitempty"`
	// QuotaPerDay 为每日调用上限；nil 表示不限额。
	QuotaPerDay *int `json:"quotaPerDay,omitempty"`
	// QuotaPerMonth 为每月调用上限；nil 表示不限额。
	QuotaPerMonth *int `json:"quotaPerMonth,omitempty"`
	// CreatedAt 为创建时间。
	CreatedAt time.Time `json:"createdAt"`
}

// IsExpired 判断该 API Key 在给定时刻 now 是否已超过有效期（Req 12.6）。
//
// 未配置有效期（ExpiresAt 为 nil）时永不过期，返回 false；否则当 now 严格晚于
// ExpiresAt 时视为已失效。鉴权中间件（任务 14.4）据此在校验时拒绝过期的 Key。
func (m Metadata) IsExpired(now time.Time) bool {
	return m.ExpiresAt != nil && now.After(*m.ExpiresAt)
}

// Usable 判断该 API Key 在给定时刻 now 是否可用于鉴权（Req 12.4、12.6）。
//
// 仅当 Key 处于启用状态且未超过有效期时返回 true。该判定供鉴权中间件
// （任务 14.4）复用，集中表达"停用或过期即拒绝"的语义。
func (m Metadata) Usable(now time.Time) bool {
	return m.Enabled && !m.IsExpired(now)
}

// Created 为创建 API Key 的结果。
//
// 由于 Metadata 现已携带明文密钥（PlaintextKey），Created 直接复用 Metadata 即可；
// 保留该类型名以兼容既有调用方语义（"刚创建的 Key，含明文"）。
type Created struct {
	Metadata
}

// CreateInput 为创建 API Key 的输入参数。
type CreateInput struct {
	// Name 为名称，长度需在 1 至 100 个字符之间（Req 12.1、12.8）。
	Name string
	// ExpiresAt 为可选有效期；nil 表示永不过期（Req 12.6）。
	ExpiresAt *time.Time
}

// Manager 是 API Key 管理器（ApiKey_Manager）的实现。
//
// 本任务（14.1）聚焦 API Key 自身的生命周期：创建（生成明文、仅存哈希+前缀）、
// 删除、启停、列表与单条查询。绑定屏蔽规则（任务 14.3）、鉴权中间件（任务 14.4）、
// ACL 白名单（任务 14.5）与限流（任务 14.7）不在本任务范围内。
type Manager struct {
	// repo 为 API Key 元数据仓储。
	repo APIKeyRepository
}

// New 构造 API Key 管理器。repo 为必需依赖。
func New(repo APIKeyRepository) *Manager {
	return &Manager{repo: repo}
}

// Create 创建一个全局唯一的 API Key（Req 12.1、12.8）。
//
// 流程：校验名称长度（1-100）→ 生成高熵明文密钥并计算哈希与展示前缀 → 以启用状态
// 持久化哈希、明文、前缀与元数据。返回结构携带明文密钥（PlaintextKey）；由于明文同时
// 入库，后续 List/Get 仍可经管理台二次查看。
//
// 错误语义：
//   - 名称长度不在 1 至 100 个字符范围内：返回 VALIDATION（Fields 含 name），
//     不持久化任何元数据（Req 12.8）。
//   - 名称与既有 API Key 重复：仓储层返回 CONFLICT（全局唯一，Req 12.1）。
func (m *Manager) Create(ctx context.Context, in CreateInput) (Created, error) {
	if err := validateName(in.Name); err != nil {
		return Created{}, err
	}

	plaintext, hash, prefix, err := generateKey()
	if err != nil {
		return Created{}, err
	}

	row, err := m.repo.Create(ctx, store.APIKey{
		Name:      in.Name,
		KeyHash:   hash,
		KeyPlain:  plaintext,
		KeyPrefix: prefix,
		Enabled:   true, // 初始启用（Req 12.1）。
		ExpiresAt: in.ExpiresAt,
	})
	if err != nil {
		return Created{}, err
	}

	return Created{Metadata: toMetadata(row)}, nil
}

// Get 按标识返回单个 API Key 的元数据（含明文，供管理台二次查看/复制）；不存在返回 NOT_FOUND（Req 12.7）。
func (m *Manager) Get(ctx context.Context, id string) (Metadata, error) {
	row, err := m.repo.Get(ctx, id)
	if err != nil {
		return Metadata{}, err
	}
	return toMetadata(row), nil
}

// List 返回所有 API Key 的元数据，按创建时间倒序（Req 12.9）。
//
// 返回视图携带完整明文密钥（PlaintextKey），供管理台二次查看/复制（自部署场景）；
// 鉴权仍走 KeyHash，明文不参与鉴权。系统中无任何 API Key 时返回空切片而非错误（Req 12.9）。
func (m *Manager) List(ctx context.Context) ([]Metadata, error) {
	rows, err := m.repo.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Metadata, 0, len(rows))
	for i := range rows {
		out = append(out, toMetadata(rows[i]))
	}
	return out, nil
}

// SetEnabled 启用或停用某个 API Key（Req 12.4）。
//
// 停用后，携带该 Key 的对外 MCP API 请求将在鉴权阶段（任务 14.4）被拒绝。
// 标识不存在返回 NOT_FOUND（Req 12.7）。
func (m *Manager) SetEnabled(ctx context.Context, id string, enabled bool) error {
	return m.repo.SetEnabled(ctx, id, enabled)
}

// Delete 删除某个 API Key（Req 12.2）。
//
// 删除后携带该 Key 的对外请求将在鉴权阶段被拒绝；其从属屏蔽规则与 ACL 由数据库
// 外键 ON DELETE CASCADE 级联清理。标识不存在返回 NOT_FOUND（Req 12.7）。
func (m *Manager) Delete(ctx context.Context, id string) error {
	return m.repo.Delete(ctx, id)
}

// validateName 校验 API Key 名称长度需在 1 至 100 个字符之间（按 Unicode 字符计数）。
//
// 不通过时返回携带字段级说明（Fields 含 name）的 VALIDATION 错误（Req 12.8）。
func validateName(name string) error {
	if n := utf8.RuneCountInString(name); n < minNameLen || n > maxNameLen {
		return domain.NewValidationError("API Key 名称校验失败", map[string]string{
			"name": "名称长度需在 1 至 100 个字符之间",
		})
	}
	return nil
}

// generateKey 生成一个新的明文密钥及其哈希与展示前缀。
//
// 返回值：
//   - plaintext：完整明文密钥，形如 "mpg_<base64url(32 字节随机数)>"，会作为 key_plain 持久化，
//     并经管理台 List/Get 二次查看。
//   - hash：明文的 SHA-256 摘要字节，作为 key_hash 持久化。选用 SHA-256 而非 bcrypt，
//     是为了让鉴权中间件（任务 14.4）能以等值查询（GetByHash）按密钥快速定位 API Key；
//     由于明文本身具备 256 位随机熵，等价于不可枚举，故无需 bcrypt 的加盐慢哈希。
//   - prefix：明文的前 keyPrefixLen 个字符，仅作展示用途。
func generateKey() (plaintext string, hash []byte, prefix string, err error) {
	buf := make([]byte, keyRandomBytes)
	if _, err = rand.Read(buf); err != nil {
		return "", nil, "", domain.NewError(
			domain.CodeValidation,
			fmt.Sprintf("生成 API Key 失败：读取随机熵出错：%v", err),
		)
	}

	plaintext = keyPlaintextPrefix + base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(plaintext))
	prefix = plaintext[:keyPrefixLen] // 明文长度恒大于 12，截取安全。

	return plaintext, sum[:], prefix, nil
}

// toMetadata 将仓储行映射为对外元数据视图。
//
// 携带明文密钥（PlaintextKey）以支持管理台二次查看/复制（自部署场景）；刻意丢弃 KeyHash，
// 避免哈希外泄。
func toMetadata(row store.APIKey) Metadata {
	return Metadata{
		ID:            row.ID,
		Name:          row.Name,
		PlaintextKey:  row.KeyPlain,
		KeyPrefix:     row.KeyPrefix,
		Enabled:       row.Enabled,
		ExpiresAt:     row.ExpiresAt,
		RateLimit:     row.RateLimit,
		RateWindowS:   row.RateWindowS,
		QuotaPerDay:   row.QuotaPerDay,
		QuotaPerMonth: row.QuotaPerMonth,
		CreatedAt:     row.CreatedAt,
	}
}
