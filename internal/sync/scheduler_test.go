package syncsvc

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// asAPIError 将 error 断言为 *domain.APIError，失败则终止用例。
func asAPIError(t *testing.T, err error) *domain.APIError {
	t.Helper()
	if err == nil {
		t.Fatal("期望返回错误，但得到 nil")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望错误类型为 *domain.APIError，实际为 %T：%v", err, err)
	}
	return apiErr
}

// TestValidateCronAcceptsValidExpressions 验证标准 5 段、带秒的 6 段表达式以及
// 预定义描述符均能通过校验（Req 7.3）。
func TestValidateCronAcceptsValidExpressions(t *testing.T) {
	valid := []string{
		"*/30 * * * *",   // 标准 5 段：每 30 分钟
		"0 */30 * * * *", // 带秒的 6 段（与默认值一致）
		"0 0 * * *",      // 每天 0 点
		"0 0 1 1 *",      // 每年 1 月 1 日
		"15 14 1 * *",    // 每月 1 日 14:15
		"  0 0 * * *  ",  // 含首尾空白，应被裁剪后通过
		"@hourly",        // 描述符
		"@every 1h30m",   // @every 描述符
		"0 0 * * MON",    // 星期使用英文缩写
	}
	for _, expr := range valid {
		if err := ValidateCron(expr); err != nil {
			t.Errorf("表达式 %q 应通过校验，却返回错误：%v", expr, err)
		}
	}
}

// TestValidateCronRejectsInvalidExpressions 验证空表达式、字段数量不符、含非法字符、
// 以及字段取值超出合法范围的表达式均被拒绝并返回 VALIDATION 错误（Req 7.4）。
func TestValidateCronRejectsInvalidExpressions(t *testing.T) {
	invalid := []string{
		"",            // 空表达式
		"   ",         // 仅空白
		"* * *",       // 字段数量不足
		"60 * * * *",  // 分钟越界（合法 0-59）
		"* 24 * * *",  // 小时越界（合法 0-23）
		"* * 32 * *",  // 日越界（合法 1-31）
		"* * * 13 *",  // 月越界（合法 1-12）
		"* * * * 8",   // 周越界（合法 0-7）
		"not-a-cron",  // 非法字符串
		"*/0 * * * *", // 步长为 0
	}
	for _, expr := range invalid {
		err := ValidateCron(expr)
		apiErr := asAPIError(t, err)
		if apiErr.Code != domain.CodeValidation {
			t.Errorf("表达式 %q 期望 VALIDATION 错误，实际错误码 %q", expr, apiErr.Code)
		}
		if _, ok := apiErr.Fields["sync.cron"]; !ok {
			t.Errorf("表达式 %q 的校验错误应包含字段级说明 sync.cron，实际 fields=%v", expr, apiErr.Fields)
		}
	}
}

// TestUpdateScheduleRejectsNilJob 验证回调为空时拒绝且不注册任何 entry。
func TestUpdateScheduleRejectsNilJob(t *testing.T) {
	s := NewScheduler()
	err := s.UpdateSchedule("@every 1s", nil)
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("期望 VALIDATION 错误，实际错误码 %q", apiErr.Code)
	}
	if s.hasEntry {
		t.Error("回调为空时不应注册任何调度 entry")
	}
}

// TestUpdateScheduleRejectsInvalidCronWithoutEntry 验证非法 cron 被拒绝且不改动现有调度。
func TestUpdateScheduleRejectsInvalidCronWithoutEntry(t *testing.T) {
	s := NewScheduler()
	err := s.UpdateSchedule("60 * * * *", func() {})
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("期望 VALIDATION 错误，实际错误码 %q", apiErr.Code)
	}
	if s.hasEntry {
		t.Error("非法 cron 不应注册任何调度 entry")
	}
}

// TestUpdateScheduleRegistersSingleEntry 验证注册后仅存在一条同步任务 entry。
func TestUpdateScheduleRegistersSingleEntry(t *testing.T) {
	s := NewScheduler()
	if err := s.UpdateSchedule("0 */30 * * * *", func() {}); err != nil {
		t.Fatalf("UpdateSchedule 不应返回错误：%v", err)
	}
	if !s.hasEntry {
		t.Fatal("注册后应存在调度 entry")
	}
	if got := len(s.cron.Entries()); got != 1 {
		t.Errorf("期望仅 1 条 entry，实际 %d 条", got)
	}
}

// TestUpdateScheduleReloadCancelsOldEntry 验证重载时取消旧调度下尚未触发的任务：
// 重载后仍仅有一条 entry，且其 EntryID 已更换为新注册的 entry（Req 7.6、7.7）。
func TestUpdateScheduleReloadCancelsOldEntry(t *testing.T) {
	s := NewScheduler()
	if err := s.UpdateSchedule("0 0 * * *", func() {}); err != nil {
		t.Fatalf("首次 UpdateSchedule 失败：%v", err)
	}
	oldID := s.entryID

	if err := s.UpdateSchedule("0 */5 * * * *", func() {}); err != nil {
		t.Fatalf("重载 UpdateSchedule 失败：%v", err)
	}

	if got := len(s.cron.Entries()); got != 1 {
		t.Errorf("重载后期望仍仅 1 条 entry（旧任务已取消），实际 %d 条", got)
	}
	if s.entryID == oldID {
		t.Errorf("重载后 EntryID 应更换（旧 %v 应被移除），实际仍为 %v", oldID, s.entryID)
	}
	// 旧 entry 应已从调度器中移除。
	if entry := s.cron.Entry(oldID); entry.ID == oldID && entry.Job != nil {
		t.Errorf("旧 EntryID %v 应已被移除，却仍存在于调度器中", oldID)
	}
}

// TestUpdateScheduleNewPeriodTakesEffectWithoutRestart 验证在调度器已运行的情况下
// 更新调度后，新周期无需重启即生效（Req 7.7）。
func TestUpdateScheduleNewPeriodTakesEffectWithoutRestart(t *testing.T) {
	s := NewScheduler()
	// 先注册一个几乎不会触发的周期，并启动调度器。
	if err := s.UpdateSchedule("0 0 1 1 *", func() {}); err != nil {
		t.Fatalf("首次 UpdateSchedule 失败：%v", err)
	}
	s.Start()
	defer s.Stop()

	// 在运行期间将调度更新为高频周期，新周期应即时生效。
	var fired int32
	if err := s.UpdateSchedule("@every 500ms", func() {
		atomic.AddInt32(&fired, 1)
	}); err != nil {
		t.Fatalf("运行期重载 UpdateSchedule 失败：%v", err)
	}

	// 等待足以触发数次的时间。
	time.Sleep(1500 * time.Millisecond)
	if atomic.LoadInt32(&fired) < 1 {
		t.Error("更新调度后新周期未在不重启的情况下生效（任务未触发）")
	}
}

func TestScheduleRecoversPanicAndKeepsRunning(t *testing.T) {
	s := NewScheduler()
	defer s.Stop()

	var fired int32
	if err := s.UpdateSchedule("@every 100ms", func() {
		n := atomic.AddInt32(&fired, 1)
		if n == 1 {
			panic("sync job panic")
		}
	}); err != nil {
		t.Fatalf("UpdateSchedule 不应返回错误：%v", err)
	}

	s.Start()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if atomic.LoadInt32(&fired) >= 2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("调度任务 panic 后应继续运行，实际触发次数=%d", atomic.LoadInt32(&fired))
}

// TestApplyCronUpdatePersistsAndReloads 验证完整流程：校验通过后持久化到配置文件，
// 随后动态重载调度（Req 7.3、7.6、7.7）。
func TestApplyCronUpdatePersistsAndReloads(t *testing.T) {
	mgr := newTestConfigManager(t)
	s := NewScheduler()

	const newCron = "0 */15 * * * *"
	if err := s.ApplyCronUpdate(mgr, newCron, func() {}); err != nil {
		t.Fatalf("ApplyCronUpdate 不应返回错误：%v", err)
	}

	// 内存配置应已更新。
	if got := mgr.Config().Sync.Cron; got != newCron {
		t.Errorf("内存配置 sync.cron 期望 %q，实际 %q", newCron, got)
	}
	// 重新加载配置文件，确认已持久化到磁盘。
	reloaded, err := config.Load(nil, filepath.Dir(mgr.YAMLPath()))
	if err != nil {
		t.Fatalf("重新加载配置失败：%v", err)
	}
	if got := reloaded.Config().Sync.Cron; got != newCron {
		t.Errorf("持久化后磁盘配置 sync.cron 期望 %q，实际 %q", newCron, got)
	}
	// 调度应已注册。
	if !s.hasEntry || len(s.cron.Entries()) != 1 {
		t.Errorf("ApplyCronUpdate 后应注册 1 条调度 entry，hasEntry=%v entries=%d", s.hasEntry, len(s.cron.Entries()))
	}
}

// TestApplyCronUpdateRejectsInvalidWithoutPersist 验证非法 cron 不持久化、不重载，
// 已生效的旧调度保持不变（Req 7.3、7.4）。
func TestApplyCronUpdateRejectsInvalidWithoutPersist(t *testing.T) {
	mgr := newTestConfigManager(t)
	s := NewScheduler()
	original := mgr.Config().Sync.Cron

	err := s.ApplyCronUpdate(mgr, "60 * * * *", func() {})
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("期望 VALIDATION 错误，实际错误码 %q", apiErr.Code)
	}

	// 内存配置不应被修改。
	if got := mgr.Config().Sync.Cron; got != original {
		t.Errorf("非法 cron 不应修改内存配置：期望 %q，实际 %q", original, got)
	}
	// 不应注册任何调度。
	if s.hasEntry {
		t.Error("非法 cron 不应注册任何调度 entry")
	}
	// 磁盘配置应保持原值。
	reloaded, err := config.Load(nil, filepath.Dir(mgr.YAMLPath()))
	if err != nil {
		t.Fatalf("重新加载配置失败：%v", err)
	}
	if got := reloaded.Config().Sync.Cron; got != original {
		t.Errorf("非法 cron 不应改动磁盘配置：期望 %q，实际 %q", original, got)
	}
}

// TestApplyCronUpdateRejectsNilManager 验证配置管理器为空时返回 VALIDATION 错误。
func TestApplyCronUpdateRejectsNilManager(t *testing.T) {
	s := NewScheduler()
	err := s.ApplyCronUpdate(nil, "0 */15 * * * *", func() {})
	apiErr := asAPIError(t, err)
	if apiErr.Code != domain.CodeValidation {
		t.Errorf("期望 VALIDATION 错误，实际错误码 %q", apiErr.Code)
	}
}

// newTestConfigManager 在临时目录下创建一个带默认配置的 config.Manager，
// 供 ApplyCronUpdate 相关用例使用。
func newTestConfigManager(t *testing.T) *config.Manager {
	t.Helper()
	// 设置必需环境变量为合法值；t.Setenv 会在用例结束后自动恢复。
	t.Setenv("MPG_PG_DSN", "postgres://user:pass@localhost:5432/mpg?sslmode=disable")
	t.Setenv("MPG_REDIS_ADDR", "localhost:6379")

	dataDir := t.TempDir()
	mgr, err := config.Load(nil, dataDir)
	if err != nil {
		t.Fatalf("构造测试用 config.Manager 失败：%v", err)
	}
	// 确认默认配置文件已落盘，便于后续重新加载验证持久化。
	if _, statErr := os.Stat(mgr.YAMLPath()); statErr != nil {
		t.Fatalf("默认配置文件未创建：%v", statErr)
	}
	return mgr
}
