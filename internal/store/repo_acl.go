package store

import (
	"context"
	"net/netip"

	"gorm.io/gorm"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// ACLEntry 是 API Key 来源白名单（api_key_acl 表）的一条记录（Req 13.9）。
type ACLEntry struct {
	// ID 为白名单记录唯一标识。
	ID string
	// APIKeyID 为该白名单记录绑定的 API Key 标识。
	APIKeyID string
	// CIDR 为允许来源的 IP 或网段（CIDR 表示，如 "10.0.0.0/8" 或 "1.2.3.4/32"）。
	CIDR string
}

// ACLRepo 提供 API Key 来源白名单的按 API Key 增删查。
type ACLRepo struct {
	db *gorm.DB
}

// NewACLRepo 构造来源白名单仓储。
func NewACLRepo(db *gorm.DB) *ACLRepo {
	return &ACLRepo{db: db}
}

// Create 为某 API Key 新增一条来源白名单记录（Req 13.9）。
//   - CIDR 文本非法返回 CodeValidation。
//   - 绑定的 api_key_id 不存在（违反外键）返回 CodeNotFound。
func (r *ACLRepo) Create(ctx context.Context, entry ACLEntry) (ACLEntry, error) {
	apiKeyID, err := parseUUID(entry.APIKeyID)
	if err != nil {
		return ACLEntry{}, err
	}
	cidr, err := normalizeCIDR(entry.CIDR)
	if err != nil {
		return ACLEntry{}, err
	}
	id := newUUID()
	if err := r.db.WithContext(ctx).Model(&apiKeyACLModel{}).Create(map[string]any{
		"id":         id,
		"api_key_id": apiKeyID,
		"cidr":       cidr,
	}).Error; err != nil {
		return ACLEntry{}, classifyWrite(err, "来源白名单冲突", "绑定的 API Key 不存在")
	}
	entry.ID = id
	entry.CIDR = cidr
	return entry, nil
}

// ListByAPIKey 返回某 API Key 的全部来源白名单记录；无数据返回空切片。
func (r *ACLRepo) ListByAPIKey(ctx context.Context, apiKeyID string) ([]ACLEntry, error) {
	uid, err := parseUUID(apiKeyID)
	if err != nil {
		return nil, err
	}
	type aclRow struct {
		ID       string
		APIKeyID string
		CIDR     string
	}
	var rows []aclRow
	err = r.db.WithContext(ctx).Table("api_key_acl").
		Select("id, api_key_id, host(cidr) || '/' || masklen(cidr) AS cidr").
		Where("api_key_id = ?", uid).
		Order("api_key_acl.cidr ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	result := make([]ACLEntry, 0, len(rows))
	for _, row := range rows {
		result = append(result, ACLEntry{ID: row.ID, APIKeyID: row.APIKeyID, CIDR: row.CIDR})
	}
	return result, nil
}

// Delete 删除一条来源白名单记录；不存在返回 CodeNotFound。
func (r *ACLRepo) Delete(ctx context.Context, id string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	res := r.db.WithContext(ctx).Where("id = ?", uid).Delete(&apiKeyACLModel{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return domain.NewError(domain.CodeNotFound, "来源白名单记录不存在")
	}
	return nil
}

// DeleteByAPIKey 删除某 API Key 的全部来源白名单记录，返回删除条数。
//
// 供「整体替换白名单」场景复用（先清空再批量插入）。
func (r *ACLRepo) DeleteByAPIKey(ctx context.Context, apiKeyID string) (int, error) {
	uid, err := parseUUID(apiKeyID)
	if err != nil {
		return 0, err
	}
	res := r.db.WithContext(ctx).Where("api_key_id = ?", uid).Delete(&apiKeyACLModel{})
	if res.Error != nil {
		return 0, res.Error
	}
	return int(res.RowsAffected), nil
}

// normalizeCIDR 校验并规范化 CIDR 文本。
//
// 既接受带掩码的网段（如 "10.0.0.0/8"），也接受单个 IP（自动补全为 /32 或 /128），
// 校验失败返回 CodeValidation 错误。
func normalizeCIDR(s string) (string, error) {
	if s == "" {
		return "", domain.NewError(domain.CodeValidation, "来源 CIDR 不能为空")
	}
	// 优先按带掩码的网段解析。
	if prefix, err := netip.ParsePrefix(s); err == nil {
		return prefix.String(), nil
	}
	// 退化为单个 IP，按其位宽补全为主机掩码。
	if addr, err := netip.ParseAddr(s); err == nil {
		bits := addr.BitLen()
		return netip.PrefixFrom(addr, bits).String(), nil
	}
	return "", domain.NewError(domain.CodeValidation, "来源 CIDR 格式非法："+s)
}
