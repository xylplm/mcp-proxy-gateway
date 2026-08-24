package apikey

import (
	"context"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

type UpstreamAccessMode string

const (
	UpstreamAccessAll      UpstreamAccessMode = "all"
	UpstreamAccessSelected UpstreamAccessMode = "selected"
)

type UpstreamAccessConfig struct {
	Mode        UpstreamAccessMode `json:"mode"`
	UpstreamIDs []string           `json:"upstreamIds"`
}

type UpstreamAccessRepository interface {
	Get(ctx context.Context, apiKeyID string) (store.APIKeyUpstreamAccess, error)
	Replace(ctx context.Context, apiKeyID, mode string, upstreamIDs []string) error
}

// UpstreamAccessManager 管理 API Key 可访问的上游范围，并供聚合链路执行实时授权。
type UpstreamAccessManager struct {
	repo UpstreamAccessRepository
}

func NewUpstreamAccessManager(repo UpstreamAccessRepository) *UpstreamAccessManager {
	return &UpstreamAccessManager{repo: repo}
}

func (m *UpstreamAccessManager) Get(ctx context.Context, apiKeyID string) (UpstreamAccessConfig, error) {
	row, err := m.repo.Get(ctx, apiKeyID)
	if err != nil {
		return UpstreamAccessConfig{}, err
	}
	return UpstreamAccessConfig{Mode: normalizeAccessMode(row.Mode), UpstreamIDs: row.UpstreamIDs}, nil
}

func (m *UpstreamAccessManager) Set(ctx context.Context, apiKeyID string, cfg UpstreamAccessConfig) (UpstreamAccessConfig, error) {
	if !validAccessMode(cfg.Mode) {
		return UpstreamAccessConfig{}, domain.NewValidationError("上游权限配置校验失败", map[string]string{
			"mode": "仅支持 all 或 selected",
		})
	}
	ids := cfg.UpstreamIDs
	if cfg.Mode == UpstreamAccessAll {
		ids = nil
	}
	if err := m.repo.Replace(ctx, apiKeyID, string(cfg.Mode), ids); err != nil {
		return UpstreamAccessConfig{}, err
	}
	return m.Get(ctx, apiKeyID)
}

func (m *UpstreamAccessManager) FilterUpstreams(ctx context.Context, apiKeyID string, upstreams []domain.Upstream) ([]domain.Upstream, error) {
	if apiKeyID == "" {
		return append([]domain.Upstream(nil), upstreams...), nil
	}
	cfg, err := m.Get(ctx, apiKeyID)
	if err != nil {
		return nil, err
	}
	if cfg.Mode == UpstreamAccessAll {
		return append([]domain.Upstream(nil), upstreams...), nil
	}
	allowed := make(map[string]struct{}, len(cfg.UpstreamIDs))
	for _, id := range cfg.UpstreamIDs {
		allowed[id] = struct{}{}
	}
	out := make([]domain.Upstream, 0, len(upstreams))
	for _, upstream := range upstreams {
		if _, ok := allowed[upstream.ID]; ok {
			out = append(out, upstream)
		}
	}
	return out, nil
}

func (m *UpstreamAccessManager) AuthorizeUpstream(ctx context.Context, apiKeyID, upstreamID string) error {
	if apiKeyID == "" {
		return nil
	}
	cfg, err := m.Get(ctx, apiKeyID)
	if err != nil {
		return err
	}
	if cfg.Mode == UpstreamAccessAll {
		return nil
	}
	for _, id := range cfg.UpstreamIDs {
		if id == upstreamID {
			return nil
		}
	}
	return domain.NewError(domain.CodeForbidden, "当前 API Key 不允许访问该上游 MCP")
}

func validAccessMode(mode UpstreamAccessMode) bool {
	return mode == UpstreamAccessAll || mode == UpstreamAccessSelected
}

func normalizeAccessMode(mode string) UpstreamAccessMode {
	if UpstreamAccessMode(mode) == UpstreamAccessSelected {
		return UpstreamAccessSelected
	}
	return UpstreamAccessAll
}
