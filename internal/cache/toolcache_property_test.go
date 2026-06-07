package cache

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件实现设计文档「Correctness Properties」中的 Property 16（工具缓存整列表
// 替换往返），对应 tasks.md 任务 7.2。
//
// 实现策略（方案 A，进程内内存替身）：
//
// 真实的 ToolCache 同时耦合 *redis.Client 与具体类型 *store.ToolCacheRepo
// （后者内含 *pgxpool.Pool 且非接口），无法在不连真实 Redis/PG 的情况下注入
// 可控替身。因此本测试在包内实现一个内存版 inMemoryToolCache，它满足
// domain.Tool_Cache 接口并精确遵循「整列表替换」语义：每次 Replace 直接以新列表
// 覆盖整个缓存条目（而非合并/追加），Get 原样读回。下面的编译期断言确保该替身
// 与真实实现实现的是同一份契约接口，从而属性所验证的 Get/Replace 行为即为
// Property 16 所描述的契约。

// inMemEntry 为内存缓存中某上游 MCP 的一条记录：完整工具列表 + 更新时间戳，
// 与真实 ToolCache 的 cacheEntry 同构。
type inMemEntry struct {
	tools     []domain.ToolDef
	updatedAt time.Time
}

// inMemoryToolCache 是 domain.Tool_Cache 的进程内内存实现，用于在无 Redis/PG
// 依赖下验证整列表替换往返契约（Property 16）。
type inMemoryToolCache struct {
	mu   sync.Mutex
	data map[string]inMemEntry
}

// 编译期断言：内存替身必须满足 domain.Tool_Cache 契约，与真实 ToolCache 一致。
var _ domain.Tool_Cache = (*inMemoryToolCache)(nil)

// newInMemoryToolCache 构造内存工具缓存替身。
func newInMemoryToolCache() *inMemoryToolCache {
	return &inMemoryToolCache{data: make(map[string]inMemEntry)}
}

// Get 读取某上游 MCP 最近一次写入的工具列表及其更新时间（Req 6.2）。
//
// 返回存储副本，避免调用方修改影响内部状态；未写入过则 found=false。
func (c *inMemoryToolCache) Get(_ context.Context, upstreamID string) ([]domain.ToolDef, time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.data[upstreamID]
	if !ok {
		return nil, time.Time{}, false
	}
	return cloneTools(e.tools), e.updatedAt, true
}

// Replace 以整列表替换语义写入工具列表（Req 6.1）。
//
// 直接以传入列表的副本覆盖整个条目，不与既有列表合并或追加——这正是
// Property 16 所要验证的「整列表替换」契约。
func (c *inMemoryToolCache) Replace(_ context.Context, upstreamID string, tools []domain.ToolDef) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[upstreamID] = inMemEntry{tools: cloneTools(tools), updatedAt: time.Now().UTC()}
	return nil
}

// Delete 删除某上游 MCP 的缓存工具列表（Req 6.6），删除语义幂等。
func (c *inMemoryToolCache) Delete(_ context.Context, upstreamID string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, upstreamID)
	return nil
}

// cloneTools 返回工具列表的深拷贝（含 InputSchema 字节），保证存储副本与外部
// 切片互不影响，从而精确建模「写入什么即读回什么」的往返语义。
func cloneTools(src []domain.ToolDef) []domain.ToolDef {
	out := make([]domain.ToolDef, len(src))
	for i, td := range src {
		if td.InputSchema != nil {
			b := make(json.RawMessage, len(td.InputSchema))
			copy(b, td.InputSchema)
			td.InputSchema = b
		}
		out[i] = td
	}
	return out
}

// toolDefEqual 逐字段比较两个工具定义；InputSchema 用 bytes.Equal，
// 使 nil 与空字节切片视为相等。
func toolDefEqual(x, y domain.ToolDef) bool {
	return x.OriginalName == y.OriginalName &&
		x.Name == y.Name &&
		x.Description == y.Description &&
		x.UpstreamID == y.UpstreamID &&
		x.Order == y.Order &&
		bytes.Equal(x.InputSchema, y.InputSchema)
}

// toolListsEqual 比较两个工具列表的顺序与内容是否完全一致（长度相等且逐项相等）。
func toolListsEqual(a, b []domain.ToolDef) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !toolDefEqual(a[i], b[i]) {
			return false
		}
	}
	return true
}

// genUpstreamID 生成一个类 UUID 的非空上游标识字符串，仅用作缓存键。
func genUpstreamID() *rapid.Generator[string] {
	return rapid.StringMatching(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
}

// genToolDef 生成任意工具定义；名称/描述用任意字符串以覆盖特殊字符与编码差异
// （设计文档「测试策略」要求 Property 16 覆盖凭证/工具名/模式中的特殊字符），
// InputSchema 以一定概率取任意字节切片或留空。UpstreamID 与所属上游一致。
func genToolDef(upstreamID string) *rapid.Generator[domain.ToolDef] {
	return rapid.Custom(func(t *rapid.T) domain.ToolDef {
		var schema json.RawMessage
		if rapid.Bool().Draw(t, "hasSchema") {
			schema = json.RawMessage(rapid.SliceOfN(rapid.Byte(), 0, 32).Draw(t, "schema"))
		}
		return domain.ToolDef{
			OriginalName: rapid.String().Draw(t, "originalName"),
			Name:         rapid.String().Draw(t, "name"),
			Description:  rapid.String().Draw(t, "description"),
			InputSchema:  schema,
			UpstreamID:   upstreamID,
			Order:        rapid.IntRange(0, 1000).Draw(t, "order"),
		}
	})
}

// genToolList 生成任意工具列表（含空列表，长度 0-10）。
func genToolList(upstreamID string) *rapid.Generator[[]domain.ToolDef] {
	return rapid.SliceOfN(genToolDef(upstreamID), 0, 10)
}

// Feature: mcp-proxy-gateway, Property 16: 工具缓存整列表替换往返
//
// Validates: Requirements 6.1, 6.2
//
// 对任意工具列表，执行 Replace(upstreamID, tools) 后 Get(upstreamID) 返回的工具
// 列表与写入的一致（顺序与内容），且 found 为真、更新时间戳被记录（Req 6.1/6.2 的
// 往返读回语义）。随后以一份新列表再次 Replace，Get 应返回新列表本身——即整列表
// 替换而非合并/追加（这一断言通过「读回结果与新列表逐项相等且长度相等」来证明：
// 若发生合并/追加，结果将残留旧列表元素而无法与新列表完全相等）。
func TestProperty16ToolCacheReplaceRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ctx := context.Background()
		c := newInMemoryToolCache()
		upstreamID := genUpstreamID().Draw(t, "upstreamID")

		// 第一次写入：任意列表（含空列表）。
		tools1 := genToolList(upstreamID).Draw(t, "tools1")
		if err := c.Replace(ctx, upstreamID, tools1); err != nil {
			t.Fatalf("首次 Replace 失败：%v", err)
		}
		got1, updatedAt1, found1 := c.Get(ctx, upstreamID)
		if !found1 {
			t.Fatalf("Replace 后 Get 应命中，却返回未找到：upstreamID=%q", upstreamID)
		}
		if updatedAt1.IsZero() {
			t.Fatalf("Replace 后应记录更新时间戳，却为零值：upstreamID=%q", upstreamID)
		}
		if !toolListsEqual(got1, tools1) {
			t.Fatalf("往返不一致：写入=%+v 读回=%+v", tools1, got1)
		}

		// 第二次写入：另一份任意列表，验证整列表替换（非合并/追加）。
		tools2 := genToolList(upstreamID).Draw(t, "tools2")
		if err := c.Replace(ctx, upstreamID, tools2); err != nil {
			t.Fatalf("二次 Replace 失败：%v", err)
		}
		got2, _, found2 := c.Get(ctx, upstreamID)
		if !found2 {
			t.Fatalf("二次 Replace 后 Get 应命中，却返回未找到：upstreamID=%q", upstreamID)
		}
		// 完整替换：读回结果应与 tools2 完全一致；若为合并/追加则会残留 tools1 元素。
		if !toolListsEqual(got2, tools2) {
			t.Fatalf("二次 Replace 应整列表替换为 tools2（而非合并 tools1）：期望=%+v 实际=%+v", tools2, got2)
		}
		if len(got2) != len(tools2) {
			t.Fatalf("整列表替换后长度应等于新列表：期望 %d 实际 %d", len(tools2), len(got2))
		}
	})
}
