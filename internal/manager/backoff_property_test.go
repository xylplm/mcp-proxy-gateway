package manager

import (
	"math"
	"math/big"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// 本文件实现设计文档「Correctness Properties」中的 Property 14（指数退避不超过
// 上限且非递减），对应 tasks.md 任务 9.6，验证 Req 5.1、5.2、5.3。
//
// 被测对象为 manager.ComputeBackoff(attempt, initial, max, multiplier) 纯函数，
// 其文档化不变量为：
//   - 结果始终不超过 max（当 max > 0 时）；
//   - 关于 attempt 单调非递减（更晚的重试退避不小于更早的重试）；
//   - 结果非负且不会因连乘溢出 int64。
//
// 测试中所有辅助标识均以 p14 前缀命名，避免与同包内 manager_test.go 及并行新增的
// reorder_property_test.go（p3）、lifecycle_property_test.go（p15）等文件冲突。

// p14GenDuration 生成覆盖各类边界的 time.Duration（以纳秒计）：
//   - 负值（被规范化为 0，覆盖 initial/max 取负的边界）；
//   - 0（覆盖零配置）；
//   - 典型可配置范围（1s 至数小时，含 1..86400s 配置区间）；
//   - 接近 int64 上限的极端值（覆盖防溢出路径）。
func p14GenDuration() *rapid.Generator[time.Duration] {
	return rapid.Custom(func(t *rapid.T) time.Duration {
		ns := rapid.OneOf(
			rapid.Int64Range(-int64(time.Second), int64(2*time.Hour)),
			rapid.Int64Range(0, int64(time.Hour)),
			rapid.Just(int64(0)),
			rapid.Just(int64(time.Second)),
			rapid.Just(int64(time.Hour)),
			rapid.Just(int64(math.MaxInt64)),
			rapid.Just(int64(math.MaxInt64/2)),
		).Draw(t, "durationNs")
		return time.Duration(ns)
	})
}

// p14GenMultiplier 生成退避倍数，覆盖 multiplier < 1（含负值与 0，被规范化为 1）、
// multiplier == 1（不增长边界）、设计默认值 2，以及较大的倍数（加速触发封顶/溢出守卫）。
func p14GenMultiplier() *rapid.Generator[int] {
	return rapid.OneOf(
		rapid.IntRange(-3, 5),
		rapid.Just(1),
		rapid.Just(2),
		rapid.IntRange(2, 1000),
	)
}

// p14OracleBackoff 是独立于被测实现的参考实现，逐步计算
// min(effInitial × effMult^attempt, effMax)，其中 eff* 为规范化后的有效值
// （initial/max 负值按 0、multiplier < 1 按 1）。
//
// 采用 math/big 逐次乘法并在达到/超过上限时提前返回，既保证精确（big.Int 不会
// 溢出，可作为被测 int64 实现的可信判据），又避免在 attempt 极大时计算出位数巨大的
// 幂（一旦乘积达到上限即停止），从而控制测试开销。multiplier == 1 时乘积恒为
// effInitial，直接取 min 即可，与 attempt 无关。
func p14OracleBackoff(attempt int, initial, max time.Duration, multiplier int) time.Duration {
	effInitial := initial
	if effInitial < 0 {
		effInitial = 0
	}
	effMax := max
	if effMax < 0 {
		effMax = 0
	}
	effMult := multiplier
	if effMult < 1 {
		effMult = 1
	}

	maxBig := big.NewInt(int64(effMax))
	prod := big.NewInt(int64(effInitial))
	// 与实现一致：乘积达到或超过上限即钳到上限（min 语义，含相等情形）。
	if prod.Cmp(maxBig) >= 0 {
		return effMax
	}
	// multiplier == 1 或 initial == 0 时乘积与 attempt 无关，恒为 effInitial，
	// 直接返回，避免在 attempt 极大时空转循环。
	if effMult == 1 || effInitial == 0 {
		return effInitial
	}
	multBig := big.NewInt(int64(effMult))
	for range attempt {
		prod.Mul(prod, multBig)
		if prod.Cmp(maxBig) >= 0 {
			return effMax
		}
	}
	// 此处 prod < effMax <= MaxInt64，Int64() 不会越界。
	return time.Duration(prod.Int64())
}

// Feature: mcp-proxy-gateway, Property 14: 指数退避不超过上限且非递减
//
// Validates: Requirements 5.1, 5.2, 5.3
//
// 对任意初始退避、上限、倍数与重试次数，ComputeBackoff 满足：
//  1. 结果与精确公式 min(initial × multiplier^attempt, max) 一致（按规范化语义）；
//  2. 当 max > 0 时结果不超过 max（Req 5.3 退避上限）；
//  3. 结果非负且不溢出（防连乘溢出 int64）；
//  4. 关于 attempt 单调非递减——f(n) <= f(n+1)，由相邻步成立可传递推得整段序列
//     在封顶前单调不减（Req 5.1/5.2 指数退避）。
func TestProperty14BackoffWithinCapAndNonDecreasing(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		initial := p14GenDuration().Draw(t, "initial")
		max := p14GenDuration().Draw(t, "max")
		multiplier := p14GenMultiplier().Draw(t, "multiplier")

		// 受限 attempt：用于与 big.Int 精确判据逐项比对（指数运算规模可控）。
		attempt := rapid.IntRange(0, 256).Draw(t, "attempt")
		got := ComputeBackoff(attempt, initial, max, multiplier)

		// 不变量 1：与精确公式一致。
		if want := p14OracleBackoff(attempt, initial, max, multiplier); got != want {
			t.Fatalf("退避值与精确公式不符：attempt=%d initial=%v max=%v multiplier=%d 期望=%v 实际=%v",
				attempt, initial, max, multiplier, want, got)
		}

		// 不变量 2：max > 0 时结果不超过上限。
		if max > 0 && got > max {
			t.Fatalf("退避值超过上限：got=%v > max=%v（attempt=%d initial=%v multiplier=%d）",
				got, max, attempt, initial, multiplier)
		}

		// 不变量 3：结果非负。
		if got < 0 {
			t.Fatalf("退避值为负：got=%v（attempt=%d initial=%v max=%v multiplier=%d）",
				got, attempt, initial, max, multiplier)
		}

		// 不变量 4：关于 attempt 单调非递减（相邻步比较）。
		next := ComputeBackoff(attempt+1, initial, max, multiplier)
		if next < got {
			t.Fatalf("退避值随 attempt 递减：f(%d)=%v > f(%d)=%v（initial=%v max=%v multiplier=%d）",
				attempt, got, attempt+1, next, initial, max, multiplier)
		}

		// 大 attempt 边界：仅校验「非负」「不超过上限」「不溢出」这些与规模无关的不变量，
		// 用于覆盖连乘防溢出路径（attempt 极大且 multiplier >= 2 时应稳定钳到上限）。
		largeAttempt := rapid.IntRange(0, 1_000_000).Draw(t, "largeAttempt")
		gotLarge := ComputeBackoff(largeAttempt, initial, max, multiplier)
		if gotLarge < 0 {
			t.Fatalf("大 attempt 退避值为负：got=%v（attempt=%d initial=%v max=%v multiplier=%d）",
				gotLarge, largeAttempt, initial, max, multiplier)
		}
		if max > 0 && gotLarge > max {
			t.Fatalf("大 attempt 退避值超过上限：got=%v > max=%v（attempt=%d initial=%v multiplier=%d）",
				gotLarge, max, largeAttempt, initial, multiplier)
		}
		if want := p14OracleBackoff(largeAttempt, initial, max, multiplier); gotLarge != want {
			t.Fatalf("大 attempt 退避值与精确公式不符：attempt=%d initial=%v max=%v multiplier=%d 期望=%v 实际=%v",
				largeAttempt, initial, max, multiplier, want, gotLarge)
		}
	})
}
