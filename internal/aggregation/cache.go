package aggregation

import (
	"context"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// invalidatingCache decorates the shared tool cache so all successful physical
// tool-list writes invalidate aggregation's derived views at one boundary.
// Reads are delegated unchanged and failed writes retain the previous derived
// view, matching the cache's "last successful list" semantics.
type invalidatingCache struct {
	inner      domain.Tool_Cache
	invalidate func()
}

var _ domain.Tool_Cache = (*invalidatingCache)(nil)

// NewInvalidatingCache returns a Tool_Cache whose successful Replace and Delete
// operations clear svc's short aggregate cache. The aggregation service itself
// must continue to receive the original cache to avoid a dependency cycle.
func NewInvalidatingCache(inner domain.Tool_Cache, svc *Service) domain.Tool_Cache {
	if inner == nil || svc == nil {
		return inner
	}
	return &invalidatingCache{inner: inner, invalidate: svc.InvalidateToolSetCache}
}

func (c *invalidatingCache) Get(ctx context.Context, upstreamID string) ([]domain.ToolDef, time.Time, bool) {
	return c.inner.Get(ctx, upstreamID)
}

func (c *invalidatingCache) Replace(ctx context.Context, upstreamID string, tools []domain.ToolDef) error {
	if err := c.inner.Replace(ctx, upstreamID, tools); err != nil {
		return err
	}
	c.invalidate()
	return nil
}

func (c *invalidatingCache) Delete(ctx context.Context, upstreamID string) error {
	if err := c.inner.Delete(ctx, upstreamID); err != nil {
		return err
	}
	c.invalidate()
	return nil
}
