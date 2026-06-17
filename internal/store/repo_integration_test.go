//go:build integration

// 仓储层集成测试（任务 2.4）。
//
// 这些用例需要一个真实可用的 PostgreSQL 实例，通过环境变量 MPG_TEST_PG_DSN 提供连接串。
// 采用双重门控，确保在无数据库环境下默认 `go test ./...` 始终为绿：
//  1. 文件以 `//go:build integration` 构建标签门控，默认构建不编译本文件；
//  2. 即便携带 -tags integration 运行，若未设置 MPG_TEST_PG_DSN 也会 t.Skip 而非失败。
//
// 运行方式（示例）：
//
//	MPG_TEST_PG_DSN="postgres://user:pass@localhost:5432/mpg_test?sslmode=disable" \
//	    go test -tags integration ./internal/store/...
//
// 覆盖需求：2.1（创建并持久化）、2.5（删除上游级联清理规则绑定与工具缓存）、23.3（业务数据持久化到 PG）。
package store

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// nonexistentUUID 是一个格式合法但库中不存在的标识，用于触发 NOT_FOUND 分支。
const nonexistentUUID = "11111111-1111-4111-8111-111111111111"

// requirePGDSN 读取集成测试所需的 PG 连接串；未设置时跳过整组用例（而非失败）。
func requirePGDSN(t *testing.T) string {
	t.Helper()
	dsn := os.Getenv("MPG_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设置 MPG_TEST_PG_DSN，跳过仓储层集成测试")
	}
	return dsn
}

// setupRepos 连接临时 PG、执行向上迁移、清空业务表以保证用例间隔离，并返回连接池与聚合仓储。
//
// 迁移幂等执行（已是最新版本视为成功）；连接池在用例结束时自动关闭。
func setupRepos(t *testing.T) (context.Context, *pgxpool.Pool, *Repositories) {
	t.Helper()
	dsn := requirePGDSN(t)
	ctx := context.Background()

	// 连接 PG 成功后、对外（此处指执行用例）前执行向上迁移（Req 23.3）。
	if err := RunMigrations(dsn, nil); err != nil {
		t.Fatalf("执行数据库迁移失败: %v", err)
	}

	pool, err := NewPGPool(ctx, dsn)
	if err != nil {
		t.Fatalf("建立 PostgreSQL 连接池失败: %v", err)
	}
	t.Cleanup(pool.Close)

	// 清空相关表，保证每个用例从空库开始；CASCADE 会一并清理从属表。
	if _, err := pool.Exec(ctx,
		`TRUNCATE upstream_mcp, api_key, audit_log, call_stat RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("清理测试数据失败: %v", err)
	}
	return ctx, pool, NewRepositories(pool)
}

// sampleUpstreamConfig 构造一份合法的上游 MCP 配置，供创建用例复用。
func sampleUpstreamConfig(name string) domain.UpstreamConfig {
	return domain.UpstreamConfig{
		Name:       name,
		Transport:  domain.TransportStreamableHTTP,
		ConnParams: map[string]any{"url": "https://example.com/mcp"},
		Enabled:    true,
		SortOrder:  0,
		AutoSync:   false,
	}
}

// TestUpstreamRepoEmptyList 验证无数据时列表返回空切片而非错误（Req 2.8）。
func TestUpstreamRepoEmptyList(t *testing.T) {
	ctx, _, repos := setupRepos(t)

	list, err := repos.Upstream.List(ctx)
	if err != nil {
		t.Fatalf("查询空列表不应报错: %v", err)
	}
	if list == nil {
		t.Fatal("空列表应返回非 nil 的空切片")
	}
	if len(list) != 0 {
		t.Fatalf("空库应返回 0 条记录，实际 %d 条", len(list))
	}
}

// TestUpstreamRepoCRUD 验证上游 MCP 的 Create/Get/List/Update/Delete 完整生命周期（Req 2.1、23.3）。
func TestUpstreamRepoCRUD(t *testing.T) {
	ctx, _, repos := setupRepos(t)

	// Create：持久化配置并回填标识与时间戳。
	cfg := sampleUpstreamConfig("crud-upstream")
	cfg.Tags = []string{"生产", "搜索"}
	cfg.Credential = "plain-credential"
	created, err := repos.Upstream.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("创建上游失败: %v", err)
	}
	if created.ID == "" {
		t.Fatal("创建后应回填非空标识")
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatal("创建后应回填创建/更新时间戳")
	}
	if created.Config.Credential != "plain-credential" {
		t.Fatalf("响应应回显明文凭证，实际 %q", created.Config.Credential)
	}

	// Get：按标识读回，核对关键字段与明文凭证。
	got, err := repos.Upstream.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("查询上游失败: %v", err)
	}
	if got.Config.Name != "crud-upstream" {
		t.Errorf("名称不一致，期望 crud-upstream，实际 %s", got.Config.Name)
	}
	if got.Config.Transport != domain.TransportStreamableHTTP {
		t.Errorf("传输类型不一致，实际 %s", got.Config.Transport)
	}
	if !reflect.DeepEqual(got.Config.Tags, []string{"生产", "搜索"}) {
		t.Errorf("标签未正确持久化，实际 %v", got.Config.Tags)
	}
	if got.Config.Credential != "plain-credential" {
		t.Errorf("明文凭证应原样持久化，实际 %q", got.Config.Credential)
	}
	if url, _ := got.Config.ConnParams["url"].(string); url != "https://example.com/mcp" {
		t.Errorf("连接参数未正确持久化，实际 %v", got.Config.ConnParams)
	}

	// List：应包含刚创建的 1 条记录。
	list, err := repos.Upstream.List(ctx)
	if err != nil {
		t.Fatalf("查询列表失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("期望 1 条记录，实际 %d 条", len(list))
	}

	// Update：更新可变字段并刷新 updated_at；凭证随 cfg.Credential 整体覆盖。
	updatedCfg := got.Config
	updatedCfg.Name = "crud-upstream-renamed"
	updatedCfg.Tags = []string{"生产", "向量"}
	updatedCfg.Enabled = false
	updatedCfg.SortOrder = 5
	updatedCfg.Credential = "plain-credential-2"
	updated, err := repos.Upstream.Update(ctx, created.ID, updatedCfg)
	if err != nil {
		t.Fatalf("更新上游失败: %v", err)
	}
	if updated.Config.Name != "crud-upstream-renamed" || updated.Config.Enabled {
		t.Errorf("更新后字段未生效: %+v", updated.Config)
	}

	reGot, err := repos.Upstream.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("更新后查询失败: %v", err)
	}
	if reGot.Config.SortOrder != 5 {
		t.Errorf("排序值未持久化，实际 %d", reGot.Config.SortOrder)
	}
	if !reflect.DeepEqual(reGot.Config.Tags, []string{"生产", "向量"}) {
		t.Errorf("更新后标签未生效，实际 %v", reGot.Config.Tags)
	}
	if reGot.Config.Credential != "plain-credential-2" {
		t.Errorf("更新后明文凭证未生效，实际 %q", reGot.Config.Credential)
	}

	// Update：清空凭证应整体覆盖为空字符串。
	emptyCredCfg := reGot.Config
	emptyCredCfg.Name = "crud-upstream-empty-cred"
	emptyCredCfg.Credential = ""
	if _, err := repos.Upstream.Update(ctx, created.ID, emptyCredCfg); err != nil {
		t.Fatalf("清空凭证更新失败: %v", err)
	}
	emptyGot, err := repos.Upstream.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("清空凭证后查询失败: %v", err)
	}
	if emptyGot.Config.Credential != "" {
		t.Errorf("清空凭证后库中应无凭证，实际 %q", emptyGot.Config.Credential)
	}

	// Delete：删除后再查应得 NOT_FOUND。
	if err := repos.Upstream.Delete(ctx, created.ID); err != nil {
		t.Fatalf("删除上游失败: %v", err)
	}
	if _, err := repos.Upstream.Get(ctx, created.ID); err == nil {
		t.Fatal("删除后查询应返回错误")
	} else if got := asAPIError(t, err); got.Code != domain.CodeNotFound {
		t.Errorf("期望 NOT_FOUND，实际 %s", got.Code)
	}
}

// TestUpstreamRepoNameConflict 验证名称重复时创建返回 CONFLICT 且不持久化（Req 2.7）。
func TestUpstreamRepoNameConflict(t *testing.T) {
	ctx, _, repos := setupRepos(t)

	if _, err := repos.Upstream.Create(ctx, sampleUpstreamConfig("dup-name")); err != nil {
		t.Fatalf("首次创建失败: %v", err)
	}

	_, err := repos.Upstream.Create(ctx, sampleUpstreamConfig("dup-name"))
	if err == nil {
		t.Fatal("重名创建应返回错误")
	}
	if got := asAPIError(t, err); got.Code != domain.CodeConflict {
		t.Errorf("期望 CONFLICT，实际 %s", got.Code)
	}

	// 确认未产生第二条记录。
	list, err := repos.Upstream.List(ctx)
	if err != nil {
		t.Fatalf("查询列表失败: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("冲突创建不应持久化，期望 1 条，实际 %d 条", len(list))
	}
}

// TestUpstreamRepoNotFound 验证对不存在标识的 Get/Update/Delete 均返回 NOT_FOUND（Req 2.6）。
func TestUpstreamRepoNotFound(t *testing.T) {
	ctx, _, repos := setupRepos(t)

	if _, err := repos.Upstream.Get(ctx, nonexistentUUID); err == nil {
		t.Error("查询不存在记录应返回错误")
	} else if got := asAPIError(t, err); got.Code != domain.CodeNotFound {
		t.Errorf("Get 期望 NOT_FOUND，实际 %s", got.Code)
	}

	if _, err := repos.Upstream.Update(ctx, nonexistentUUID, sampleUpstreamConfig("ghost")); err == nil {
		t.Error("更新不存在记录应返回错误")
	} else if got := asAPIError(t, err); got.Code != domain.CodeNotFound {
		t.Errorf("Update 期望 NOT_FOUND，实际 %s", got.Code)
	}

	if err := repos.Upstream.Delete(ctx, nonexistentUUID); err == nil {
		t.Error("删除不存在记录应返回错误")
	} else if got := asAPIError(t, err); got.Code != domain.CodeNotFound {
		t.Errorf("Delete 期望 NOT_FOUND，实际 %s", got.Code)
	}
}

// TestUpstreamCascadeDelete 验证删除上游后，其规则绑定与工具缓存被 ON DELETE CASCADE 级联清理（Req 2.5）。
func TestUpstreamCascadeDelete(t *testing.T) {
	ctx, _, repos := setupRepos(t)

	up, err := repos.Upstream.Create(ctx, sampleUpstreamConfig("cascade-upstream"))
	if err != nil {
		t.Fatalf("创建上游失败: %v", err)
	}

	// 创建一条仅作用于该上游的别名规则。
	alias, err := repos.Alias.Create(ctx, domain.AliasRule{
		ScopeType:   "upstreams",
		UpstreamIDs: []string{up.ID},
		Pattern:     "old_tool",
		IsRegex:     false,
		TargetName:  "new_tool",
		SortOrder:   0,
	})
	if err != nil {
		t.Fatalf("创建别名规则失败: %v", err)
	}

	// 创建一条仅作用于该上游的 MCP 级屏蔽规则。
	filterRow := FilterMCPRow{}
	filterRow.ScopeType = "upstreams"
	filterRow.UpstreamIDs = []string{up.ID}
	filterRow.Pattern = "secret_tool"
	filterRow.IsRegex = false
	filterRow.Enabled = true
	filterRow.SortOrder = 0
	filter, err := repos.FilterMCP.Create(ctx, filterRow)
	if err != nil {
		t.Fatalf("创建屏蔽规则失败: %v", err)
	}

	// 写入工具缓存持久副本。
	tools := []domain.ToolDef{{OriginalName: "old_tool", Name: "old_tool", UpstreamID: up.ID}}
	if err := repos.ToolCache.Replace(ctx, up.ID, tools, time.Now()); err != nil {
		t.Fatalf("写入工具缓存失败: %v", err)
	}

	// 删除前确认绑定规则与工具缓存均存在。
	if aliases, err := repos.Alias.ListByUpstream(ctx, up.ID); err != nil || len(aliases) != 1 {
		t.Fatalf("删除前别名规则应为 1 条，err=%v len=%d", err, len(aliases))
	}
	if filters, err := repos.FilterMCP.ListByUpstream(ctx, up.ID); err != nil || len(filters) != 1 {
		t.Fatalf("删除前屏蔽规则应为 1 条，err=%v len=%d", err, len(filters))
	}
	if _, _, found, err := repos.ToolCache.Get(ctx, up.ID); err != nil || !found {
		t.Fatalf("删除前工具缓存应存在，err=%v found=%v", err, found)
	}

	// 删除上游，触发级联清理。
	if err := repos.Upstream.Delete(ctx, up.ID); err != nil {
		t.Fatalf("删除上游失败: %v", err)
	}

	// 删除后绑定关系应被级联清理，独立规则定义本身保留。
	if aliases, err := repos.Alias.ListByUpstream(ctx, up.ID); err != nil {
		t.Fatalf("查询别名规则失败: %v", err)
	} else if len(aliases) != 0 {
		t.Errorf("级联删除绑定后该上游适用别名规则应为 0 条，实际 %d 条", len(aliases))
	}
	if alias, err := repos.Alias.Get(ctx, alias.ID); err != nil {
		t.Fatalf("独立别名规则定义应保留: %v", err)
	} else if len(alias.UpstreamIDs) != 0 {
		t.Errorf("级联删除绑定后别名规则绑定应为空，实际 %d 条", len(alias.UpstreamIDs))
	}
	if filters, err := repos.FilterMCP.ListByUpstream(ctx, up.ID); err != nil {
		t.Fatalf("查询屏蔽规则失败: %v", err)
	} else if len(filters) != 0 {
		t.Errorf("级联删除绑定后该上游适用屏蔽规则应为 0 条，实际 %d 条", len(filters))
	}
	if filter, err := repos.FilterMCP.Get(ctx, filter.ID); err != nil {
		t.Fatalf("独立屏蔽规则定义应保留: %v", err)
	} else if len(filter.UpstreamIDs) != 0 {
		t.Errorf("级联删除绑定后屏蔽规则绑定应为空，实际 %d 条", len(filter.UpstreamIDs))
	}
	if _, _, found, err := repos.ToolCache.Get(ctx, up.ID); err != nil {
		t.Fatalf("查询工具缓存失败: %v", err)
	} else if found {
		t.Error("级联删除后工具缓存应不存在")
	}
}

// TestAPIKeyCascadeDelete 验证删除 API Key 后，其屏蔽规则与来源白名单（ACL）被级联清理（Req 23.3 关联的级联约束）。
func TestAPIKeyCascadeDelete(t *testing.T) {
	ctx, _, repos := setupRepos(t)

	key, err := repos.APIKey.Create(ctx, APIKey{
		Name:      "cascade-key",
		KeyHash:   []byte("hash-bytes"),
		KeyPrefix: "mpg_abc",
		Enabled:   true,
	})
	if err != nil {
		t.Fatalf("创建 API Key 失败: %v", err)
	}

	// 绑定一条 API Key 级屏蔽规则。
	apkFilter := FilterAPIKeyRow{APIKeyID: key.ID}
	apkFilter.Pattern = "blocked_tool"
	apkFilter.Enabled = true
	apkFilter.SortOrder = 0
	if _, err := repos.FilterAPIKey.Create(ctx, apkFilter); err != nil {
		t.Fatalf("创建 API Key 屏蔽规则失败: %v", err)
	}

	// 绑定一条来源白名单。
	if _, err := repos.ACL.Create(ctx, ACLEntry{APIKeyID: key.ID, CIDR: "10.0.0.0/8"}); err != nil {
		t.Fatalf("创建来源白名单失败: %v", err)
	}

	// 删除前确认从属数据存在。
	if rules, err := repos.FilterAPIKey.ListByAPIKey(ctx, key.ID); err != nil || len(rules) != 1 {
		t.Fatalf("删除前 API Key 屏蔽规则应为 1 条，err=%v len=%d", err, len(rules))
	}
	if acls, err := repos.ACL.ListByAPIKey(ctx, key.ID); err != nil || len(acls) != 1 {
		t.Fatalf("删除前来源白名单应为 1 条，err=%v len=%d", err, len(acls))
	}

	// 删除 API Key，触发级联清理。
	if err := repos.APIKey.Delete(ctx, key.ID); err != nil {
		t.Fatalf("删除 API Key 失败: %v", err)
	}

	if rules, err := repos.FilterAPIKey.ListByAPIKey(ctx, key.ID); err != nil {
		t.Fatalf("查询 API Key 屏蔽规则失败: %v", err)
	} else if len(rules) != 0 {
		t.Errorf("级联删除后 API Key 屏蔽规则应为 0 条，实际 %d 条", len(rules))
	}
	if acls, err := repos.ACL.ListByAPIKey(ctx, key.ID); err != nil {
		t.Fatalf("查询来源白名单失败: %v", err)
	} else if len(acls) != 0 {
		t.Errorf("级联删除后来源白名单应为 0 条，实际 %d 条", len(acls))
	}
}
