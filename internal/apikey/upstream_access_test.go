package apikey

import (
	"context"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

type fakeUpstreamAccessRepo struct {
	row store.APIKeyUpstreamAccess
}

func (r *fakeUpstreamAccessRepo) Get(context.Context, string) (store.APIKeyUpstreamAccess, error) {
	return r.row, nil
}

func (r *fakeUpstreamAccessRepo) Replace(_ context.Context, _ string, mode string, ids []string) error {
	r.row = store.APIKeyUpstreamAccess{Mode: mode, UpstreamIDs: append([]string(nil), ids...)}
	return nil
}

func TestUpstreamAccessSelectedFiltersAndAuthorizes(t *testing.T) {
	repo := &fakeUpstreamAccessRepo{row: store.APIKeyUpstreamAccess{Mode: "selected", UpstreamIDs: []string{"up-b"}}}
	manager := NewUpstreamAccessManager(repo)
	upstreams := []domain.Upstream{{ID: "up-a"}, {ID: "up-b"}}

	got, err := manager.FilterUpstreams(context.Background(), "key-1", upstreams)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "up-b" {
		t.Fatalf("过滤结果 = %+v，期望仅保留 up-b", got)
	}
	if err := manager.AuthorizeUpstream(context.Background(), "key-1", "up-b"); err != nil {
		t.Fatalf("已选上游应允许：%v", err)
	}
	if err := manager.AuthorizeUpstream(context.Background(), "key-1", "up-a"); err == nil {
		t.Fatal("未选上游应被拒绝")
	}
}

func TestUpstreamAccessAllClearsSelection(t *testing.T) {
	repo := &fakeUpstreamAccessRepo{}
	manager := NewUpstreamAccessManager(repo)
	got, err := manager.Set(context.Background(), "key-1", UpstreamAccessConfig{
		Mode: UpstreamAccessAll, UpstreamIDs: []string{"up-a"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != UpstreamAccessAll || len(got.UpstreamIDs) != 0 {
		t.Fatalf("all 模式应清空关联：%+v", got)
	}
}

func TestUpstreamAccessRejectsInvalidMode(t *testing.T) {
	manager := NewUpstreamAccessManager(&fakeUpstreamAccessRepo{})
	if _, err := manager.Set(context.Background(), "key-1", UpstreamAccessConfig{Mode: "invalid"}); err == nil {
		t.Fatal("非法上游访问模式应被拒绝")
	}
}
