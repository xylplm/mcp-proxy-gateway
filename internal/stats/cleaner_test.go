package stats

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
)

// 本文件为统计保留期清理单元测试，覆盖每日聚合统计清理：
//   - Cleanup 依保留期计算截止时刻并删除超期 daily aggregate；
//   - 保留期取自配置，越界或缺失时回退默认 90 天；
//   - 后台循环启动即清理一次并按周期重复，Stop 可幂等停止；
//   - 清理失败时循环仅记日志不中断。

// --- 测试替身 ---

type fakeMaintainer struct {
	mu sync.Mutex

	delCutoffs []time.Time
	delErr     error
	deletedRet int64
	panicOnce  bool
	panicCnt   int
}

func (m *fakeMaintainer) DeleteOlderThan(_ context.Context, cutoff time.Time) (int64, error) {
	m.mu.Lock()
	m.delCutoffs = append(m.delCutoffs, cutoff)
	if m.panicOnce {
		m.panicOnce = false
		m.panicCnt++
		m.mu.Unlock()
		panic("cleanup panic")
	}
	defer m.mu.Unlock()
	if m.delErr != nil {
		return 0, m.delErr
	}
	return m.deletedRet, nil
}

func (m *fakeMaintainer) deleteCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.delCutoffs)
}

func (m *fakeMaintainer) panicCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.panicCnt
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

func TestCleanupComputesCutoffFromRetention(t *testing.T) {
	now := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	repo := &fakeMaintainer{deletedRet: 5}
	c := NewCleaner(repo, fakeCfg{retentionDays: 30}, WithCleanerClock(fixedClock(now)))

	deleted, err := c.Cleanup(context.Background())
	if err != nil {
		t.Fatalf("Cleanup 不应返回错误：%v", err)
	}
	if deleted != 5 {
		t.Fatalf("返回值不符：deleted=%d，期望 5", deleted)
	}

	wantCutoff := now.AddDate(0, 0, -30)
	if len(repo.delCutoffs) != 1 || !repo.delCutoffs[0].Equal(wantCutoff) {
		t.Errorf("删除截止时间不符：got=%v want=%v", repo.delCutoffs, wantCutoff)
	}
}

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
			if _, err := c.Cleanup(context.Background()); err != nil {
				t.Fatalf("Cleanup 不应返回错误：%v", err)
			}
			wantCutoff := now.AddDate(0, 0, -defaultStatRetentionDays)
			if !repo.delCutoffs[0].Equal(wantCutoff) {
				t.Errorf("越界保留期应回退默认 90 天：got cutoff=%v want=%v", repo.delCutoffs[0], wantCutoff)
			}
		})
	}
}

func TestRetentionDaysHonorsValidConfig(t *testing.T) {
	now := time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC)
	for _, days := range []int{minStatRetentionDays, defaultStatRetentionDays, maxStatRetentionDays} {
		repo := &fakeMaintainer{}
		c := NewCleaner(repo, fakeCfg{retentionDays: days}, WithCleanerClock(fixedClock(now)))
		if _, err := c.Cleanup(context.Background()); err != nil {
			t.Fatalf("Cleanup 不应返回错误：%v", err)
		}
		wantCutoff := now.AddDate(0, 0, -days)
		if !repo.delCutoffs[0].Equal(wantCutoff) {
			t.Errorf("保留期 %d 天：截止时间 got=%v want=%v", days, repo.delCutoffs[0], wantCutoff)
		}
	}
}

func TestCleanupPropagatesDeleteError(t *testing.T) {
	repo := &fakeMaintainer{delErr: errors.New("delete failed")}
	c := NewCleaner(repo, fakeCfg{retentionDays: 30}, WithCleanerClock(fixedClock(time.Now())))

	if _, err := c.Cleanup(context.Background()); err == nil {
		t.Fatal("删除失败时 Cleanup 应返回错误")
	}
}

func TestStartRunsCleanupAndStopHalts(t *testing.T) {
	repo := &fakeMaintainer{}
	// 周期设得很长，确保观察到的清理来自「启动即执行一次」而非周期触发。
	c := NewCleaner(repo, fakeCfg{retentionDays: 30},
		WithCleanerClock(fixedClock(time.Now())), WithCleanInterval(time.Hour))

	c.Start(context.Background())
	if !c.Running() {
		t.Fatal("Start 后应处于运行状态")
	}
	waitFor(t, 2*time.Second, func() bool { return repo.deleteCount() >= 1 })

	c.Stop()
	if c.Running() {
		t.Fatal("Stop 后不应处于运行状态")
	}
	// 再次 Stop 应为无操作，不 panic。
	c.Stop()
}

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

func TestCleanupResilientWhenLoopErrors(t *testing.T) {
	repo := &fakeMaintainer{delErr: errors.New("transient error")}
	c := NewCleaner(repo, fakeCfg{retentionDays: 30},
		WithCleanerClock(fixedClock(time.Now())), WithCleanInterval(10*time.Millisecond))

	c.Start(context.Background())
	// 即便每次清理都失败，循环也应持续尝试且保持运行。
	waitFor(t, 2*time.Second, func() bool { return repo.deleteCount() >= 2 })
	if !c.Running() {
		t.Error("清理失败不应导致循环退出")
	}
	c.Stop()
}

func TestCleanupResilientWhenCleanupPanics(t *testing.T) {
	repo := &fakeMaintainer{panicOnce: true}
	c := NewCleaner(repo, fakeCfg{retentionDays: 30},
		WithCleanerClock(fixedClock(time.Now())), WithCleanInterval(10*time.Millisecond))

	c.Start(context.Background())
	defer c.Stop()

	waitFor(t, 2*time.Second, func() bool { return repo.panicCount() == 1 && repo.deleteCount() >= 2 })
	if !c.Running() {
		t.Error("清理 panic 不应导致循环退出")
	}
}
