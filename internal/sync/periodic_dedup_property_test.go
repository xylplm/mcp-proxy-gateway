package syncsvc

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件实现「同步并发去重」的属性测试（任务 10.4，Property 19）。
//
// 复用 periodic_test.go / refresh_test.go 中已有的测试替身：
//   - gateFetcher：可显式同步、可阻塞的 ToolFetcher 替身（newGateFetcher / started / release / callCount）；
//   - stubLister：UpstreamLister 替身；
//   - memCache：domain.Tool_Cache 进程内替身（replaceHits 记录整列表替换次数）；
//   - toolDef：构造 domain.ToolDef。
//
// 本任务新增的辅助标识符统一使用 p19 前缀，避免与上述替身命名冲突。
//
// 并发 + 阻塞的确定性同步策略：第一次 SyncOne 在后台执行并阻塞于 gateFetcher.release，
// 通过 <-fetcher.started 确保其已进入拉取（即 sync.Map 去重标志已登记）后，再并发发起
// N 次对同一上游的触发，从而稳定复现「上一次同步尚未完成」的场景，杜绝 flaky。

// p19SyncResult 汇总一次后台 SyncOne 调用的结果，供并发断言使用。
type p19SyncResult struct {
	ran atomic.Int32 // 1 表示实际执行（ran=true），0 表示被跳过（ran=false）
	err error
}

// Feature: mcp-proxy-gateway, Property 19: 同步并发去重
//
// Validates: Requirements 7.8
//
// 对任意上游 MCP：当其上一次同步尚未完成时，对该上游到达的任意数量并发同步触发
// 都被跳过（ran=false 且不返回错误），且底层拉取（fetcher）不被重复调用——即被
// 跳过的触发不重复拉取（Req 7.8）；待上一次同步完成后，其「进行中」标志被释放，
// 后续对同一上游的触发应能再次实际执行（ran=true）。
//
// N（并发触发次数）与上游标识由 rapid 生成；阻塞与并发通过 channel 确定性协调，
// 期望结果不依赖具体调度时序。
func TestProperty19SyncConcurrentDedup(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// 生成并发触发次数与上游标识（保证非空）。
		n := rapid.IntRange(1, 8).Draw(t, "concurrentTriggers")
		upstreamID := "up-" + strconv.Itoa(rapid.IntRange(0, 1000).Draw(t, "upstreamID"))

		fetcher := newGateFetcher([]domain.ToolDef{toolDef("a")})
		cache := &memCache{}
		s := NewPeriodicSyncer(fetcher, cache, &stubLister{}, 0, nil)

		// 第一次触发：在后台执行，将阻塞在 fetcher.release 上，制造「上一次同步尚未完成」。
		first := &p19SyncResult{}
		firstDone := make(chan struct{})
		go func() {
			defer close(firstDone)
			ran, err := s.SyncOne(context.Background(), upstreamID)
			if ran {
				first.ran.Store(1)
			}
			first.err = err
		}()

		// 等待第一次拉取确实进入 fetcher：此时 sync.Map 去重标志已在 SyncOne 中登记。
		select {
		case <-fetcher.started:
		case <-time.After(2 * time.Second):
			t.Fatal("第一次拉取未在预期时间内进入 fetcher")
		}

		// 并发发起 N 次对同一上游的触发：上一次尚未完成，全部应被跳过。
		results := make([]p19SyncResult, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Go(func() {
				ran, err := s.SyncOne(context.Background(), upstreamID)
				if ran {
					results[i].ran.Store(1)
				}
				results[i].err = err
			})
		}
		wg.Wait()

		// 断言一：N 次并发触发全部被跳过（ran=false 且无错误）。
		for i := range n {
			if results[i].ran.Load() != 0 {
				t.Fatalf("第 %d/%d 次并发触发应被跳过（ran 期望 false），实际 ran=true", i+1, n)
			}
			if results[i].err != nil {
				t.Fatalf("被跳过的触发不应返回错误，实际=%v", results[i].err)
			}
		}

		// 断言二：被跳过的触发不重复拉取，fetcher 仅被第一次触发调用 1 次。
		if got := fetcher.callCount(); got != 1 {
			t.Fatalf("被跳过的触发不应调用 fetcher，期望拉取 1 次，实际 %d 次", got)
		}

		// 断言三：第一次同步仍阻塞中，缓存尚未被写入（整列表替换尚未发生）。
		cache.mu.Lock()
		hits := cache.replaceHits
		cache.mu.Unlock()
		if hits != 0 {
			t.Fatalf("第一次同步尚未完成，缓存不应被写入，实际替换 %d 次", hits)
		}

		// 放行第一次触发并等待其完成（其 defer 已释放去重标志）。
		close(fetcher.release)
		<-firstDone
		if first.err != nil {
			t.Fatalf("第一次同步不应返回错误：%v", first.err)
		}
		if first.ran.Load() != 1 {
			t.Fatal("第一次触发应实际执行（ran 期望 true）")
		}

		// 断言四：同步完成后去重标志已释放，后续对同一上游的触发应能再次执行。
		ran, err := s.SyncOne(context.Background(), upstreamID)
		if err != nil {
			t.Fatalf("去重标志释放后再次同步不应返回错误：%v", err)
		}
		if !ran {
			t.Fatal("同步完成后去重标志应释放，后续触发应可再次执行（ran 期望 true）")
		}

		// 断言五：共发生两次实际拉取（首次 + 释放后再次），被跳过的触发未贡献调用。
		if got := fetcher.callCount(); got != 2 {
			t.Fatalf("期望共拉取 2 次（首次 + 释放后再次执行），实际 %d 次", got)
		}
	})
}
