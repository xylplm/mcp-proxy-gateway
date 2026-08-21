package manager

import (
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

// 本文件实现设计文档「Correctness Properties」中的 Property 15（重连成功重置失败
// 计数），对应 tasks.md 任务 9.7，验证 Req 5.5。
//
// 被测对象为 lifecycle.go 中 *Connection 的未导出状态机方法（同包测试可直接访问）：
//   - newConnection(id)            构造初始为 unavailable、失败计数为 0 的连接记录；
//   - fail(reason, threshold)      记录一次失败（失败计数 +1、记录原因），达阈值转
//                                  suspended、否则转 unavailable；threshold < 1 归一化为 1；
//   - succeed()                    连接建立成功：恢复 available、失败计数清零、清空原因（Req 5.5）；
//   - snapshot()                   返回 (state, lastErr, failures) 三元组。
//
// 为避免与同包内已有文件的测试标识冲突（manager_test.go、reorder_property_test.go
// 的 p3 前缀、backoff_property_test.go 的 p14 前缀），本文件内的辅助生成器与类型
// 统一采用 p15 前缀命名。

// p15FailOp 描述一次 fail 调用的入参：失败原因与当次生效的失败阈值。
type p15FailOp struct {
	// reason 为本次失败原因（写入 lastErr）。
	reason string
	// threshold 为本次失败判定使用的阈值（< 1 时被 fail 归一化为 1）。
	threshold int
}

// p15GenReason 生成失败原因字符串，覆盖空串、典型错误文案与任意字符串，
// 用于确认 succeed 后无论先前原因为何都会被清空。
func p15GenReason() *rapid.Generator[string] {
	return rapid.OneOf(
		rapid.Just(""),
		rapid.Just("连接超时"),
		rapid.Just("dial tcp: connection refused"),
		rapid.String(),
	)
}

// p15GenThreshold 生成失败阈值，覆盖：
//   - 非正值（被 fail 归一化为 1，边界守卫）；
//   - 1（最小合法阈值，单次失败即 suspended）；
//   - 设计配置范围 1..100（Req 5.6）；
//   - 大于范围的较大阈值（确保多次失败仍不转 suspended）。
func p15GenThreshold() *rapid.Generator[int] {
	return rapid.OneOf(
		rapid.IntRange(-3, 0),
		rapid.Just(1),
		rapid.IntRange(1, 100),
		rapid.IntRange(100, 1000),
	)
}

// p15GenFailOps 生成长度 1..40 的失败操作序列，每个元素的原因与阈值独立采样。
// 至少一次失败，确保 succeed 之前确有非零失败计数需要被重置。
func p15GenFailOps() *rapid.Generator[[]p15FailOp] {
	return rapid.SliceOfN(rapid.Custom(func(t *rapid.T) p15FailOp {
		return p15FailOp{
			reason:    p15GenReason().Draw(t, "reason"),
			threshold: p15GenThreshold().Draw(t, "threshold"),
		}
	}), 1, 40)
}

// Feature: mcp-proxy-gateway, Property 15: 重连成功重置失败计数
//
// Validates: Requirements 5.5
//
// 对任意失败次数序列（每次失败的原因与阈值任意），随后一次 succeed() 后，
// *Connection 的连续失败计数被重置为 0、状态恢复 ConnAvailable、lastErr 为空。
// 补充：succeed 之后再 fail 一次，失败计数应从 1 重新计数（而非延续 succeed 前的累计）。
func TestProperty15SucceedResetsFailureCount(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ops := p15GenFailOps().Draw(t, "failOps")

		c := newConnection("up-p15")

		// 初始不变量：新建连接连续失败计数为 0。
		if _, _, f0 := c.snapshot(); f0 != 0 {
			t.Fatalf("新建连接连续失败计数应为 0，实际 %d", f0)
		}

		// 施加任意次失败（任意 reason 与 threshold）。
		// fail 无论是否转 suspended 都会累计失败计数，因此第 i 次后应恰为 i+1。
		for i, op := range ops {
			c.fail(op.reason, op.threshold)
			if _, _, f := c.snapshot(); f != i+1 {
				t.Fatalf("第 %d 次失败后连续失败次数应为 %d，实际 %d", i+1, i+1, f)
			}
		}

		// 一次 succeed 后应恢复可用、失败计数清零、最近失败原因清空（Req 5.5）。
		c.succeed()
		state, lastErr, failures := c.snapshot()
		if state != domain.ConnAvailable {
			t.Fatalf("succeed 后状态应为 ConnAvailable，实际 %q（失败序列长度=%d）", state, len(ops))
		}
		if failures != 0 {
			t.Fatalf("succeed 后连续失败计数应重置为 0，实际 %d（失败序列长度=%d）", failures, len(ops))
		}
		if lastErr != "" {
			t.Fatalf("succeed 后最近失败原因应清空，实际 %q", lastErr)
		}

		// 补充：succeed 后再 fail 一次，失败计数应从 1 重新计数。
		nextReason := p15GenReason().Draw(t, "nextReason")
		nextThreshold := p15GenThreshold().Draw(t, "nextThreshold")
		c.fail(nextReason, nextThreshold)
		state2, lastErr2, failures2 := c.snapshot()
		if failures2 != 1 {
			t.Fatalf("succeed 后再失败一次，连续失败计数应从 1 重新计数，实际 %d", failures2)
		}
		if lastErr2 != nextReason {
			t.Fatalf("再失败一次后最近失败原因应为 %q，实际 %q", nextReason, lastErr2)
		}

		// 状态依据归一化阈值判定：归一化后阈值 == 1 时单次失败即 suspended，否则 unavailable。
		effThreshold := max(nextThreshold, 1)
		wantState := domain.ConnUnavailable
		if effThreshold == 1 {
			wantState = domain.ConnSuspended
		}
		if state2 != wantState {
			t.Fatalf("succeed 后再失败一次状态应为 %q（阈值=%d），实际 %q", wantState, nextThreshold, state2)
		}
	})
}
