package stats

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
)

// 本文件为任务 17.3「实现统计保留期清理」的单元测试，覆盖以下核心行为（Req 16.10）：
//   - Cleanup 依保留期计算截止时刻并依次预建分区、DROP 超期分区、逐行兜底删除；
//   - 保留期取自配置，越界或缺失时回退默认 90 天，清理边界始终合法；
//   - 后台循环启动即清理一次并按周期重复，Stop 可幂等停止；
//   - 任一步骤失败时 Cleanup 返回错误，循环仅记日志不中断。
//
// 测试以内存 fake（fakeMaintainer / fakeCfg）替换仓储与配置，脱离真实数据库验证逻辑。

// --- 测试替身 ---

// fakeMaintainer 是 PartitionMaintainer 的内存实现：记录各方法调用入参，并可注入错误。
type fakeMaintainer struct {
	mu sync.Mutex

	ensureCalls []ensureArgs
	dropCutoffs []time.Time
	delCutoffs  []time.Time

	ensureErr error
	dropErr   error
	delErr    error

	droppedReturn int
	deletedReturn int64
}

type ensureArgs struct {
	now   time.Time
	ahead int
}

func (m *fakeMaintainer) EnsurePartitions(_ context.Context, now time.Time, ahead int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureCalls = append(m.ensureCalls, ensureArgs{now: now, ahead: ahead})
	return m.ensureErr
}

func (m *fakeMaintainer) DropPartitionsOlderThan(_ context.Context, cutoff time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.dropCutoffs = append(m.dropCutoffs, cutoff)
	if m.dropErr != nil {
		return 0, m.dropErr
	}
	return m.droppedReturn, nil
}

func (m *fakeMaintainer) DeleteOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.delCutoffs = append(m.delCutoffs, cutoff)
	if m.delErr != nil {
		return 0, m.delErr
	}
	return m.deletedReturn, nil
}

func (m *fakeMaintainer) ensureCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.ensureCalls)
}

func (m *fakeMaintainer) dropCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.dropCutoffs)
}

// fakeCfg 是 CleanerConfigProvider 的内存实现：返回固定的统计保留天数。
type fakeCfg struct {
	retentionDays int
}

func (c fakeCfg) Config() config.YAMLConfig {
	var cfg config.YAMLConfig
	cfg.Statistics.RetentionDays = c.retentionDays
	return cfg
}

// fixedClock 返回固定时刻的时钟，便于断言截止时间计算。
func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// --- 测试用例 ---

// TestCleanupComputesCutoffFromRetention 验证：Cleanup 以「当前时刻 - 保留天数」为截止时刻，
// 依次预建分区、DROP 超期分区、逐行兜底删除，且三处截止时间一致（Req 16.10）。
func TestCleanupComputesCutoffFromRetention(t *testing.T) {
	now := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	repo := &fakeMaintainer{droppedReturn: 2, deletedReturn: 5}
	c := NewCleaner(repo, fakeCfg{retentionDays: 30},
		WithCleanerClock(fixedClock(now)), WithPartitionAhead(1))

	dropped, deleted, err := c.Cleanup(context.Background())
	if err != nil {
		t.Fatalf("Cleanup 不应返回错误：%v", err)
	}
	if dropped != 2 || deleted != 5 {
		t.Fatalf("返回值不符：dropped=%d deleted=%d，期望 2/5", dropped, deleted)
	}

	wantCutoff := now.AddDate(0, 0, -30)
	if len(repo.dropCutoffs) != 1 || !repo.dropCutoffs[0].Equal(wantCutoff) {
		t.Errorf("DROP 截止时间不符：got=%v want=%v", repo.dropCutoffs, wantCutoff)
	}
	if len(repo.delCutoffs) != 1 || !repo.delCutoffs[0].Equal(wantCutoff) {
		t.Errorf("逐行删除截止时间不符：got=%v want=%v", repo.delCutoffs, wantCutoff)
	}
	if len(repo.ensureCalls) != 1 || repo.ensureCalls[0].ahead != 1 {
		t.Errorf("预建分区调用不符：%+v", repo.ensureCalls)
	}
}

// TestRetentionDaysDefaultsWhenOutOfRange 验证：配置保留期越界或缺失时回退默认 90 天（Req 16.10）。
func TestRetentionDaysDefaultsWhenOutOfRange(t *testing.T) {
	now := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		days int
	}{
		{"零值缺失", 0},
		{"低于下界", -1},
		{"高于上界", 99999},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeMaintainer{}
			c := NewCleaner(repo, fakeCfg{retentionDays: tc.days}, WithCleanerClock(fixedClock(now)))
			if _, _, err := c.Cleanup(context.Background()); err != nil {
				t.Fatalf("Cleanup 不应返回错误：%v", err)
			}
			wantCutoff := now.AddDate(0, 0, -defaultStatRetentionDays)
			if !repo.dropCutoffs[0].Equal(wantCutoff) {
				t.Errorf("越界保留期应回退默认 90 天：got cutoff=%v want=%v", repo.dropCutoffs[0], wantCutoff)
			}
		})
	}
}

// TestRetentionDaysHonorsValidConfig 验证：配置在合法范围内时按配置值计算截止时间（Req 16.10）。
func TestRetentionDaysHonorsValidConfig(t *testing.T) {
	now := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	for _, days := range []int{minStatRetentionDays, defaultStatRetentionDays, maxStatRetentionDays} {
		repo := &fakeMaintainer{}
		c := NewCleaner(repo, fakeCfg{retentionDays: days}, WithCleanerClock(fixedClock(now)))
		if _, _, err := c.Cleanup(context.Background()); err != nil {
			t.Fatalf("Cleanup 不应返回错误：%v", err)
		}
		wantCutoff := now.AddDate(0, 0, -days)
		if !repo.dropCutoffs[0].Equal(wantCutoff) {
			t.Errorf("保留期 %d 天：截止时间 got=%v want=%v", days, repo.dropCutoffs[0], wantCutoff)
		}
	}
}

// TestCleanupStopsOnEnsureError 验证：预建分区失败时立即返回错误，不再 DROP/删除（Req 16.10）。
func TestCleanupStopsOnEnsureError(t *testing.T) {
	repo := &fakeMaintainer{ensureErr: errors.New("create partition failed")}
	c := NewCleaner(repo, fakeCfg{retentionDays: 30}, WithCleanerClock(fixedClock(time.Now())))

	if _, _, err := c.Cleanup(context.Background()); err == nil {
		t.Fatal("预建分区失败时 Cleanup 应返回错误")
	}
	if repo.dropCount() != 0 {
		t.Error("预建失败后不应继续 DROP 分区")
	}
	if len(repo.delCutoffs) != 0 {
		t.Error("预建失败后不应继续逐行删除")
	}
}

// TestCleanupStopsOnDropError 验证：DROP 分区失败时返回错误且不再逐行删除（Req 16.10）。
func TestCleanupStopsOnDropError(t *testing.T) {
	repo := &fakeMaintainer{dropErr: errors.New("drop failed")}
	c := NewCleaner(repo, fakeCfg{retentionDays: 30}, WithCleanerClock(fixedClock(time.Now())))

	if _, _, err := c.Cleanup(context.Background()); err == nil {
		t.Fatal("DROP 分区失败时 Cleanup 应返回错误")
	}
	if len(repo.delCutoffs) != 0 {
		t.Error("DROP 失败后不应继续逐行删除")
	}
}

// TestStartRunsCleanupAndStopHalts 验证：Start 启动即清理一次并可被 Stop 幂等停止（Req 16.10）。
func TestStartRunsCleanupAndStopHalts(t *testing.T) {
	repo := &fakeMaintainer{}
	// 周期设得很长，确保观察到的清理来自「启动即执行一次」而非周期触发。
	c := NewCleaner(repo, fakeCfg{retentionDays: 30},
		WithCleanerClock(fixedClock(time.Now())), WithCleanInterval(time.Hour))

	c.Start(context.Background())
	if !c.Running() {
		t.Fatal("Start 后应处于运行状态")
	}
	waitFor(t, 2*time.Second, func() bool { return repo.ensureCount() >= 1 })

	c.Stop()
	if c.Running() {
		t.Fatal("Stop 后不应处于运行状态")
	}
	// 再次 Stop 应为无操作，不 panic。
	c.Stop()
}

// TestStartIsIdempotent 验证：重复 Start 不重复启动循环（幂等）。
func TestStartIsIdempotent(t *testing.T) {
	repo := &fakeMaintainer{}
	c := NewCleaner(repo, fakeCfg{retentionDays: 30},
		WithCleanerClock(fixedClock(time.Now())), WithCleanInterval(time.Hour))
	c.Start(context.Background())
	c.Start(context.Background()) // 第二次应为无操作
	defer c.Stop()
	if !c.Running() {
		t.Fatal("Start 后应处于运行状态")
	}
}

// TestCleanupResilientWhenLoopErrors 验证：周期清理失败时循环不退出，仍可被 Stop 收束（Req 16.10）。
func TestCleanupResilientWhenLoopErrors(t *testing.T) {
	repo := &fakeMaintainer{ensureErr: errors.New("transient error")}
	c := NewCleaner(repo, fakeCfg{retentionDays: 30},
		WithCleanerClock(fixedClock(time.Now())), WithCleanInterval(10*time.Millisecond))

	c.Start(context.Background())
	// 即便每次清理都失败，循环也应持续尝试且保持运行。
	waitFor(t, 2*time.Second, func() bool { return repo.ensureCount() >= 2 })
	if !c.Running() {
		t.Error("清理失败不应导致循环退出")
	}
	c.Stop()
}
