package apikey

import (
	"context"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// Feature: mcp-proxy-gateway, Property 20: 限流不超额且窗口恢复
//
// Validates: Requirements 21.1, 21.2, 21.3, 21.4
//
// 针对 RateLimiter.Allow 的固定窗口限流语义，验证四条不变量：
//   - 配置了上限的 Key，在同一计数窗口内前 limit 次请求受理、其后超额请求被拒绝
//     （受理数恒不超过上限，Req 21.1、21.2）；
//   - 时间推进到下一个计数窗口后（now 增加 >= windowS），该 Key 的配额恢复，
//     新窗口内前 limit 次请求重新受理（Req 21.3）；
//   - 未配置上限（RateLimit 为 nil）或配置非正（上限/窗口 <= 0）时，任意次数请求
//     全部放行且不触及计数后端（Req 21.4）。
//
// 复用 ratelimit_test.go 中的内存计数器 fakeCounter，使核心限流判定脱离真实 Redis 验证。
// 同一窗口内的多次请求使用同一时刻 now，保证落入同一窗口键；下一窗口通过将 now 增加
// 至少一个窗口长度构造，使窗口起点必然改变（floor((sec+k)/w)*w 在 k>=w 时严格增大）。
func TestProperty20RateLimitNotExceededAndWindowRecovers(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		// ---- 不变量一、二、三：配置上限时不超额受理且窗口恢复（Req 21.1、21.2、21.3）----
		limit := rapid.IntRange(1, 20).Draw(t, "limit")
		windowS := rapid.IntRange(1, 3600).Draw(t, "windowS")
		// 基准时刻限定在合理范围，避免窗口对齐计算溢出。
		baseSec := rapid.Int64Range(0, 4_102_444_800).Draw(t, "baseSec")

		counter := newFakeCounter()
		limiter := NewRateLimiter(counter, nil)
		key := metaWithLimit("p20-k", limit, windowS)

		// 第一窗口：发出 n1 次请求（可能未达、恰达或超过上限）。
		w1 := time.Unix(baseSec, 0)
		n1 := rapid.IntRange(0, limit*2+5).Draw(t, "n1")
		p20AssertWindow(t, limiter, key, w1, limit, n1)

		// 推进到下一个窗口：now 增加至少一个窗口长度，窗口起点必然改变，配额应恢复。
		delta := rapid.Int64Range(int64(windowS), int64(windowS)*3).Draw(t, "delta")
		w2 := time.Unix(baseSec+delta, 0)
		if s1, s2 := windowStartUnix(w1, windowS), windowStartUnix(w2, windowS); s1 == s2 {
			// 防御性自检：构造的两个时刻必须属于不同窗口，否则生成器有误。
			t.Fatalf("前置条件不成立：w1 与 w2 应属于不同窗口（start %d == %d，windowS=%d）", s1, s2, windowS)
		}
		n2 := rapid.IntRange(0, limit*2+5).Draw(t, "n2")
		p20AssertWindow(t, limiter, key, w2, limit, n2)

		// ---- 不变量四：未配置上限或配置非正时不限流（Req 21.4）----
		noLimitKey := p20DrawNoLimitMeta(t)
		noLimitCounter := newFakeCounter()
		noLimitLimiter := NewRateLimiter(noLimitCounter, nil)
		now := time.Unix(baseSec, 0)
		m := rapid.IntRange(0, 50).Draw(t, "noLimitN")
		for i := 0; i < m; i++ {
			allowed, err := noLimitLimiter.Allow(context.Background(), noLimitKey, now)
			if err != nil {
				t.Fatalf("不限流场景第 %d 次调用返回错误：%v", i+1, err)
			}
			if !allowed {
				t.Fatalf("未配置上限的 Key 第 %d 次请求被拒，应全部放行：meta=%+v", i+1, noLimitKey)
			}
		}
		if len(noLimitCounter.counts) != 0 {
			t.Fatalf("未配置上限不应触及计数后端，却产生计数键：%v", noLimitCounter.counts)
		}
	})
}

// p20AssertWindow 在同一窗口内（同一时刻 now）连续发出 n 次请求，断言：
// 前 limit 次受理、其后超额请求被拒绝（Req 21.1、21.2）。
//
// 同时统计实际受理数，确保窗口内受理总数恒不超过 limit（限流不超额）。
func p20AssertWindow(t *rapid.T, limiter *RateLimiter, key Metadata, now time.Time, limit, n int) {
	accepted := 0
	for i := 0; i < n; i++ {
		allowed, err := limiter.Allow(context.Background(), key, now)
		if err != nil {
			t.Fatalf("窗口内第 %d 次调用返回错误：%v", i+1, err)
		}
		want := i < limit
		if allowed != want {
			t.Fatalf("窗口内第 %d 次请求受理判定错误：got=%v want=%v（limit=%d）",
				i+1, allowed, want, limit)
		}
		if allowed {
			accepted++
		}
	}
	if accepted > limit {
		t.Fatalf("窗口内受理数 %d 超过上限 %d", accepted, limit)
	}
	if wantAccepted := min(n, limit); accepted != wantAccepted {
		t.Fatalf("窗口内受理数不符：got=%d want=%d（n=%d limit=%d）", accepted, wantAccepted, n, limit)
	}
}

// p20DrawNoLimitMeta 随机生成一个「不应被限流」的 API Key 元数据，覆盖四类配置形态：
// RateLimit 为 nil、RateWindowS 为 nil、上限非正、窗口非正——均应被视为不限流（Req 21.4）。
func p20DrawNoLimitMeta(t *rapid.T) Metadata {
	switch rapid.IntRange(0, 3).Draw(t, "noLimitKind") {
	case 0:
		// RateLimit 为 nil。
		return Metadata{ID: "p20-nolimit", RateWindowS: ptrInt(rapid.IntRange(1, 3600).Draw(t, "w0"))}
	case 1:
		// RateWindowS 为 nil。
		return Metadata{ID: "p20-nolimit", RateLimit: ptrInt(rapid.IntRange(1, 20).Draw(t, "l1"))}
	case 2:
		// 上限非正。
		return Metadata{
			ID:          "p20-nolimit",
			RateLimit:   ptrInt(rapid.IntRange(-20, 0).Draw(t, "l2")),
			RateWindowS: ptrInt(rapid.IntRange(1, 3600).Draw(t, "w2")),
		}
	default:
		// 窗口非正。
		return Metadata{
			ID:          "p20-nolimit",
			RateLimit:   ptrInt(rapid.IntRange(1, 20).Draw(t, "l3")),
			RateWindowS: ptrInt(rapid.IntRange(-3600, 0).Draw(t, "w3")),
		}
	}
}
