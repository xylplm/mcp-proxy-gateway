package apikey

import (
	"context"
	"errors"
	"testing"
	"time"
)

// fakeCounter 是 RateCounter 的内存实现，用于在不依赖真实 Redis 的情况下测试限流逻辑。
//
// 它以 map 模拟 Redis 的 INCR 语义（键不存在时从 0 起算），并记录 Expire 调用次数，
// 便于断言「仅窗口内首次计数才设置 TTL」。incrErr 可注入以模拟计数后端异常。
type fakeCounter struct {
	counts      map[string]int64
	expireCalls map[string]int
	incrErr     error
}

func newFakeCounter() *fakeCounter {
	return &fakeCounter{
		counts:      make(map[string]int64),
		expireCalls: make(map[string]int),
	}
}

func (f *fakeCounter) Incr(_ context.Context, key string) (int64, error) {
	if f.incrErr != nil {
		return 0, f.incrErr
	}
	f.counts[key]++
	return f.counts[key], nil
}

func (f *fakeCounter) Expire(_ context.Context, key string, _ time.Duration) error {
	f.expireCalls[key]++
	return nil
}

// ptrInt 返回指向所给整数的指针，便于构造可选的限流配置字段。
func ptrInt(v int) *int { return &v }

// metaWithLimit 构造一个配置了速率上限与窗口的 API Key 元数据。
func metaWithLimit(id string, limit, windowS int) Metadata {
	return Metadata{ID: id, RateLimit: ptrInt(limit), RateWindowS: ptrInt(windowS)}
}

// TestAllow_NoLimitConfigured 验证未配置上限的 API Key 不被限流（Req 21.4）。
func TestAllow_NoLimitConfigured(t *testing.T) {
	counter := newFakeCounter()
	limiter := NewRateLimiter(counter, nil)
	now := time.Unix(1000, 0)

	// RateLimit 为 nil：不限流。
	key := Metadata{ID: "k1"}
	for i := 0; i < 100; i++ {
		allowed, err := limiter.Allow(context.Background(), key, now)
		if err != nil {
			t.Fatalf("第 %d 次调用返回错误：%v", i, err)
		}
		if !allowed {
			t.Fatalf("未配置上限的 API Key 第 %d 次请求被拒，应全部放行", i)
		}
	}

	// 不限流时不应触及计数后端。
	if len(counter.counts) != 0 {
		t.Fatalf("未配置上限不应进行计数，却产生了计数键：%v", counter.counts)
	}
}

// TestAllow_NonPositiveConfigTreatedAsNoLimit 验证上限或窗口非正时视为不限流。
func TestAllow_NonPositiveConfigTreatedAsNoLimit(t *testing.T) {
	counter := newFakeCounter()
	limiter := NewRateLimiter(counter, nil)
	now := time.Unix(1000, 0)

	cases := []Metadata{
		{ID: "zero-limit", RateLimit: ptrInt(0), RateWindowS: ptrInt(60)},
		{ID: "neg-limit", RateLimit: ptrInt(-1), RateWindowS: ptrInt(60)},
		{ID: "zero-window", RateLimit: ptrInt(5), RateWindowS: ptrInt(0)},
		{ID: "nil-window", RateLimit: ptrInt(5)},
	}
	for _, key := range cases {
		allowed, err := limiter.Allow(context.Background(), key, now)
		if err != nil {
			t.Fatalf("%s 返回错误：%v", key.ID, err)
		}
		if !allowed {
			t.Fatalf("%s 配置非法应视为不限流并放行，却被拒绝", key.ID)
		}
	}
}

// TestAllow_WithinLimit 验证窗口内未达上限的请求全部放行（Req 21.1）。
func TestAllow_WithinLimit(t *testing.T) {
	counter := newFakeCounter()
	limiter := NewRateLimiter(counter, nil)
	now := time.Unix(1000, 0)
	key := metaWithLimit("k1", 5, 60)

	for i := 0; i < 5; i++ {
		allowed, err := limiter.Allow(context.Background(), key, now)
		if err != nil {
			t.Fatalf("第 %d 次调用返回错误：%v", i, err)
		}
		if !allowed {
			t.Fatalf("窗口内第 %d 次请求（未超上限 5）被拒，应放行", i+1)
		}
	}

	// 窗口内首次计数应恰好设置一次 TTL。
	start := windowStartUnix(now, 60)
	wantKey := rateLimitKey("k1", start)
	if got := counter.expireCalls[wantKey]; got != 1 {
		t.Fatalf("窗口内应仅首次计数设置 TTL，期望 1 次，实际 %d 次", got)
	}
}

// TestAllow_ExceedLimitRejected 验证超额请求被拒绝（Req 21.2）。
func TestAllow_ExceedLimitRejected(t *testing.T) {
	counter := newFakeCounter()
	limiter := NewRateLimiter(counter, nil)
	now := time.Unix(1000, 0)
	key := metaWithLimit("k1", 3, 60)

	// 前 3 次放行。
	for i := 0; i < 3; i++ {
		allowed, _ := limiter.Allow(context.Background(), key, now)
		if !allowed {
			t.Fatalf("窗口内第 %d 次请求（未超上限 3）被拒，应放行", i+1)
		}
	}

	// 第 4、5 次超额，应被拒绝。
	for i := 3; i < 5; i++ {
		allowed, err := limiter.Allow(context.Background(), key, now)
		if err != nil {
			t.Fatalf("第 %d 次调用返回错误：%v", i+1, err)
		}
		if allowed {
			t.Fatalf("窗口内第 %d 次请求（超上限 3）被放行，应拒绝", i+1)
		}
	}
}

// TestAllow_NextWindowRecovers 验证进入下一窗口后配额恢复（Req 21.3）。
func TestAllow_NextWindowRecovers(t *testing.T) {
	counter := newFakeCounter()
	limiter := NewRateLimiter(counter, nil)
	key := metaWithLimit("k1", 2, 60)

	// 第一窗口：用尽配额并触发一次拒绝。
	w1 := time.Unix(1000, 0) // 窗口起点 floor(1000/60)*60 = 960
	for i := 0; i < 2; i++ {
		if allowed, _ := limiter.Allow(context.Background(), key, w1); !allowed {
			t.Fatalf("第一窗口第 %d 次请求应放行", i+1)
		}
	}
	if allowed, _ := limiter.Allow(context.Background(), key, w1); allowed {
		t.Fatal("第一窗口超额请求应被拒绝")
	}

	// 进入下一窗口（时间推进超过一个窗口长度）：配额恢复。
	w2 := time.Unix(1000+60, 0) // 落入下一个窗口
	if startW1, startW2 := windowStartUnix(w1, 60), windowStartUnix(w2, 60); startW1 == startW2 {
		t.Fatalf("测试前置条件不成立：w1 与 w2 应属于不同窗口（%d == %d）", startW1, startW2)
	}
	for i := 0; i < 2; i++ {
		allowed, err := limiter.Allow(context.Background(), key, w2)
		if err != nil {
			t.Fatalf("下一窗口第 %d 次调用返回错误：%v", i+1, err)
		}
		if !allowed {
			t.Fatalf("进入下一窗口后第 %d 次请求应恢复放行", i+1)
		}
	}
}

// TestAllow_CounterErrorFailOpen 验证计数后端异常时降级放行（可用性优先）。
func TestAllow_CounterErrorFailOpen(t *testing.T) {
	counter := newFakeCounter()
	counter.incrErr = errors.New("redis 不可用")
	limiter := NewRateLimiter(counter, nil)
	now := time.Unix(1000, 0)
	key := metaWithLimit("k1", 1, 60)

	allowed, err := limiter.Allow(context.Background(), key, now)
	if err != nil {
		t.Fatalf("计数异常应降级放行而非返回错误，却返回：%v", err)
	}
	if !allowed {
		t.Fatal("计数后端异常时应降级放行（fail-open），却被拒绝")
	}
}

// TestWindowStartUnix 验证窗口起点按 floor 对齐到窗口边界。
func TestWindowStartUnix(t *testing.T) {
	cases := []struct {
		now     int64
		windowS int
		want    int64
	}{
		{now: 960, windowS: 60, want: 960},  // 恰好边界
		{now: 1000, windowS: 60, want: 960}, // 边界内
		{now: 1019, windowS: 60, want: 960}, // 窗口末尾
		{now: 1020, windowS: 60, want: 1020},
		{now: 5, windowS: 10, want: 0},
	}
	for _, c := range cases {
		if got := windowStartUnix(time.Unix(c.now, 0), c.windowS); got != c.want {
			t.Fatalf("windowStartUnix(%d, %d)=%d，期望 %d", c.now, c.windowS, got, c.want)
		}
	}
}
