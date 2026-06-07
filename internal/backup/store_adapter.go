package backup

import (
	"context"

	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// StoreAdapter 基于 PostgreSQL 仓储实现 BusinessStore：导出读取全部业务配置，
// 导入以「整体替换」语义重建业务配置（Req 23.4、23.5）。
//
// 导入采用「先删除现有上游/API Key（外键级联清理其从属规则与白名单），再按备份
// 内容重建」的策略。重建时由仓储生成新的标识，并在内存中维护「备份中旧标识 → 新
// 标识」的映射，以保证父子归属关系正确落库；备份的父子嵌套结构使该映射无需依赖
// 旧标识本身的稳定性，导入后得到与备份结构等价的业务配置。
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
		aliases, err := a.repos.Alias.ListByUpstream(ctx, u.ID)
		if err != nil {
			return BusinessConfig{}, err
		}
		filterRows, err := a.repos.FilterMCP.ListByUpstream(ctx, u.ID)
		if err != nil {
			return BusinessConfig{}, err
		}
		entry := UpstreamEntry{
			ID:            u.ID,
			Config:        u.Config,
			CredentialEnc: u.CredentialEnc,
			AliasRules:    aliases,
		}
		for _, fr := range filterRows {
			entry.FilterRules = append(entry.FilterRules, fr.FilterRule)
		}
		bc.Upstreams = append(bc.Upstreams, entry)
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
// 注意：当前实现非单一事务，逐条写入；若中途失败可能产生部分应用。调用方应在
// 调用前完成全部校验（Service.Import 已保证仅在校验通过后才进入本方法）。
func (a *StoreAdapter) ImportBusiness(ctx context.Context, bc BusinessConfig) error {
	// 1. 清空现有业务配置：删除全部上游（级联其别名/屏蔽规则）与全部 API Key
	//    （级联其屏蔽规则与白名单）。
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

	// 2. 重建上游及其从属规则。
	for _, ue := range bc.Upstreams {
		created, err := a.repos.Upstream.Create(ctx, ue.Config, ue.CredentialEnc)
		if err != nil {
			return err
		}
		for _, ar := range ue.AliasRules {
			ar.UpstreamID = created.ID
			ar.ID = ""
			if _, err := a.repos.Alias.Create(ctx, ar); err != nil {
				return err
			}
		}
		for _, fr := range ue.FilterRules {
			row := store.FilterMCPRow{FilterRule: fr, UpstreamID: created.ID}
			row.ID = ""
			if _, err := a.repos.FilterMCP.Create(ctx, row); err != nil {
				return err
			}
		}
	}

	// 3. 重建 API Key 及其从属规则与白名单。
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
