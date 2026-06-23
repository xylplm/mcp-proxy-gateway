package backup

import (
	"context"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// StoreAdapter 基于 PostgreSQL 仓储实现 BusinessStore：导出读取全部业务配置，
// 导入以「整体替换」语义重建业务配置（Req 23.4、23.5）。
//
// 导入采用「先删除现有上游/API Key/独立规则，再按备份内容重建」的策略。重建时由
// 仓储生成新的上游/API Key 标识，并在内存中维护「备份中旧标识 → 新标识」的映射，
// 以保证规则作用范围和 API Key 从属关系正确落库。
type StoreAdapter struct {
	repos *store.Repositories
}

// NewStoreAdapter 构造基于仓储的业务配置导出/导入适配器。
func NewStoreAdapter(repos *store.Repositories) *StoreAdapter {
	return &StoreAdapter{repos: repos}
}

// ExportBusiness 读取并返回全部业务配置快照（上游/规则/API Key 元数据/白名单）。
func (a *StoreAdapter) ExportBusiness(ctx context.Context) (BusinessConfig, error) {
	var bc BusinessConfig

	upstreams, err := a.repos.Upstream.List(ctx)
	if err != nil {
		return BusinessConfig{}, err
	}
	bc.Upstreams = make([]UpstreamEntry, 0, len(upstreams))
	for _, u := range upstreams {
		entry := UpstreamEntry{
			ID:     u.ID,
			Config: u.Config,
		}
		bc.Upstreams = append(bc.Upstreams, entry)
	}

	aliases, err := a.repos.Alias.List(ctx)
	if err != nil {
		return BusinessConfig{}, err
	}
	bc.AliasRules = aliases

	filterRows, err := a.repos.FilterMCP.List(ctx)
	if err != nil {
		return BusinessConfig{}, err
	}
	bc.MCPFilterRules = make([]domain.FilterRule, 0, len(filterRows))
	for _, fr := range filterRows {
		bc.MCPFilterRules = append(bc.MCPFilterRules, fr.FilterRule)
	}

	keys, err := a.repos.APIKey.List(ctx)
	if err != nil {
		return BusinessConfig{}, err
	}
	bc.APIKeys = make([]APIKeyEntry, 0, len(keys))
	for _, k := range keys {
		filterRows, err := a.repos.FilterAPIKey.ListByAPIKey(ctx, k.ID)
		if err != nil {
			return BusinessConfig{}, err
		}
		aclRows, err := a.repos.ACL.ListByAPIKey(ctx, k.ID)
		if err != nil {
			return BusinessConfig{}, err
		}
		entry := APIKeyEntry{Meta: k}
		for _, fr := range filterRows {
			entry.FilterRules = append(entry.FilterRules, fr.FilterRule)
		}
		for _, ace := range aclRows {
			entry.ACLCIDRs = append(entry.ACLCIDRs, ace.CIDR)
		}
		bc.APIKeys = append(bc.APIKeys, entry)
	}

	return bc, nil
}

// ImportBusiness 以备份内容整体替换库中现有业务配置（Req 23.5）。
//
// 整个替换过程在数据库事务内完成；若任一步失败，删除和重建都会回滚，避免半导入状态。
// 调用方应在调用前完成全部校验（Service.Import 已保证仅在校验通过后才进入本方法）。
func (a *StoreAdapter) ImportBusiness(ctx context.Context, bc BusinessConfig) error {
	return a.repos.WithTransaction(ctx, func(repos *store.Repositories) error {
		txAdapter := &StoreAdapter{repos: repos}
		return txAdapter.importBusiness(ctx, bc)
	})
}

func (a *StoreAdapter) importBusiness(ctx context.Context, bc BusinessConfig) error {
	// 1. 清空现有业务配置：删除全部独立规则、上游与 API Key。
	existingAliases, err := a.repos.Alias.List(ctx)
	if err != nil {
		return err
	}
	for _, ar := range existingAliases {
		if err := a.repos.Alias.Delete(ctx, ar.ID); err != nil {
			return err
		}
	}
	existingFilters, err := a.repos.FilterMCP.List(ctx)
	if err != nil {
		return err
	}
	for _, fr := range existingFilters {
		if err := a.repos.FilterMCP.Delete(ctx, fr.ID); err != nil {
			return err
		}
	}
	existingUpstreams, err := a.repos.Upstream.List(ctx)
	if err != nil {
		return err
	}
	for _, u := range existingUpstreams {
		if err := a.repos.Upstream.Delete(ctx, u.ID); err != nil {
			return err
		}
	}
	existingKeys, err := a.repos.APIKey.List(ctx)
	if err != nil {
		return err
	}
	for _, k := range existingKeys {
		if err := a.repos.APIKey.Delete(ctx, k.ID); err != nil {
			return err
		}
	}

	// 2. 重建上游并记录旧标识到新标识的映射。
	upstreamIDMap := make(map[string]string, len(bc.Upstreams))
	for _, ue := range bc.Upstreams {
		created, err := a.repos.Upstream.Create(ctx, ue.Config)
		if err != nil {
			return err
		}
		upstreamIDMap[ue.ID] = created.ID
	}

	// 3. 重建独立别名与 MCP 级屏蔽规则。
	for _, ar := range bc.AliasRules {
		ar.ID = ""
		ar.UpstreamIDs = remapIDs(ar.UpstreamIDs, upstreamIDMap)
		if _, err := a.repos.Alias.Create(ctx, ar); err != nil {
			return err
		}
	}
	for _, fr := range bc.MCPFilterRules {
		fr.ID = ""
		fr.UpstreamIDs = remapIDs(fr.UpstreamIDs, upstreamIDMap)
		if _, err := a.repos.FilterMCP.Create(ctx, store.FilterMCPRow{FilterRule: fr}); err != nil {
			return err
		}
	}

	// 4. 重建 API Key 及其从属规则与白名单。
	for _, ke := range bc.APIKeys {
		meta := ke.Meta
		meta.ID = "" // 由仓储生成新标识
		created, err := a.repos.APIKey.Create(ctx, meta)
		if err != nil {
			return err
		}
		for _, fr := range ke.FilterRules {
			row := store.FilterAPIKeyRow{FilterRule: fr, APIKeyID: created.ID}
			row.ID = ""
			if _, err := a.repos.FilterAPIKey.Create(ctx, row); err != nil {
				return err
			}
		}
		for _, cidr := range ke.ACLCIDRs {
			if _, err := a.repos.ACL.Create(ctx, store.ACLEntry{APIKeyID: created.ID, CIDR: cidr}); err != nil {
				return err
			}
		}
	}

	return nil
}

func remapIDs(ids []string, idMap map[string]string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if mapped, ok := idMap[id]; ok {
			out = append(out, mapped)
		}
	}
	return out
}
