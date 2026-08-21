package syncsvc

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/robfig/cron/v3"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// cronParser 为本服务统一使用的 cron 表达式解析器（Req 7.3、7.4）。
//
// 采用 SecondOptional 选项：秒字段可选，因此既接受标准 5 段表达式
// （分 时 日 月 周，如 "*/30 * * * *"），也接受带秒的 6 段表达式
// （秒 分 时 日 月 周，如 "0 */30 * * * *"，与 design.md 默认值一致）。
// 同时启用 Descriptor 以支持 @hourly、@every 1h 等预定义描述符。
//
// 解析器对各字段做语法与取值范围校验：字段数量不符、含非法字符、或某字段
// 取值超出其合法范围（如分钟 0-59、小时 0-23）时，Parse 返回错误。
var cronParser = cron.NewParser(
	cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor,
)

// ValidateCron 校验 cron 表达式是否为标准 cron 格式且每个字段取值落在合法范围内
// （Req 7.3、7.4）。
//
// 该函数为独立纯函数（不持有状态、无副作用），便于属性测试（任务 10.2）覆盖。
// 校验通过返回 nil；空表达式或非法表达式返回携带字段级说明的 VALIDATION 类
// APIError，调用方据此拒绝持久化并向管理员返回 cron 格式错误。
func ValidateCron(expr string) error {
	trimmed := strings.TrimSpace(expr)
	if trimmed == "" {
		return domain.NewValidationError("cron 表达式校验失败", map[string]string{
			"sync.cron": "cron 表达式不能为空",
		})
	}
	if _, err := cronParser.Parse(trimmed); err != nil {
		return domain.NewValidationError("cron 表达式校验失败", map[string]string{
			"sync.cron": fmt.Sprintf("非法的 cron 表达式：%v", err),
		})
	}
	return nil
}

// Scheduler 是同步调度器，封装 *cron.Cron，负责注册周期同步任务并支持动态重载
// （Req 7.6、7.7）。
//
// 设计要点：
//   - 仅维护「同步任务」这一条 cron entry；UpdateSchedule 以「先移除旧 entry、再
//     注册新 entry」的方式实现不重启即生效的动态重载，旧调度下尚未触发的任务被取消。
//   - 内部以互斥锁保护 entry 状态，导出方法对并发调用安全。
type Scheduler struct {
	mu       sync.Mutex
	cron     *cron.Cron
	entryID  cron.EntryID
	hasEntry bool
}

// NewScheduler 创建一个使用统一解析器的同步调度器。
//
// 通过 cron.WithParser 注入与 ValidateCron 相同的解析器，确保「能通过校验的表达式
// 一定能被调度器注册」，二者对合法性的判定保持一致。
func NewScheduler() *Scheduler {
	return &Scheduler{
		cron: cron.New(
			cron.WithParser(cronParser),
			cron.WithChain(cron.Recover(slogCronLogger{})),
		),
	}
}

type slogCronLogger struct{}

func (slogCronLogger) Info(msg string, keysAndValues ...any) {
	slog.Default().Info("cron "+msg, normalizeCronLogAttrs(keysAndValues...)...)
}

func (slogCronLogger) Error(err error, msg string, keysAndValues ...any) {
	attrs := append([]any{"error", err}, normalizeCronLogAttrs(keysAndValues...)...)
	slog.Default().Error("cron "+msg, attrs...)
}

func normalizeCronLogAttrs(keysAndValues ...any) []any {
	out := make([]any, 0, len(keysAndValues))
	for i := 0; i < len(keysAndValues); i += 2 {
		key, ok := keysAndValues[i].(string)
		if !ok || key == "" {
			key = fmt.Sprintf("arg%d", i)
		}
		out = append(out, key)
		if i+1 < len(keysAndValues) {
			out = append(out, keysAndValues[i+1])
		} else {
			out = append(out, "")
		}
	}
	return out
}

// UpdateSchedule 校验并注册（或重载）周期同步任务（Req 7.6、7.7）。
//
// 流程：先校验 expr 合法性；校验通过后取消旧调度下尚未触发的任务（移除旧 entry），
// 再以新周期注册 job。整个过程不重启 cron 实例，新周期即时生效。
//
// 校验失败或回调为空时返回 VALIDATION 错误，且不改动现有调度。
func (s *Scheduler) UpdateSchedule(expr string, job func()) error {
	if job == nil {
		return domain.NewError(domain.CodeValidation, "同步任务回调不能为空")
	}
	if err := ValidateCron(expr); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 取消旧调度下尚未触发的后续任务（Req 7.6）。
	if s.hasEntry {
		s.cron.Remove(s.entryID)
		s.hasEntry = false
	}

	// 注册新周期；ValidateCron 已通过，此处再次解析理论上不会失败，仅作兜底。
	id, err := s.cron.AddFunc(strings.TrimSpace(expr), job)
	if err != nil {
		return domain.NewValidationError("cron 表达式校验失败", map[string]string{
			"sync.cron": fmt.Sprintf("非法的 cron 表达式：%v", err),
		})
	}
	s.entryID = id
	s.hasEntry = true
	return nil
}

// ApplyCronUpdate 完成「校验通过才持久化、随后动态重载」的完整流程（Req 7.3、7.6、7.7）。
//
// 步骤：
//  1. 校验新 cron 表达式格式与字段范围（Req 7.3、7.4）；
//  2. 在当前配置快照基础上仅更新 sync.cron 字段，交由 config.Manager.Save 做整体
//     校验并持久化（Req 7.3：仅在校验通过时持久化）；
//  3. 持久化成功后动态重载调度，按新周期生效（Req 7.6、7.7）。
//
// 任一步骤失败均不改变现有调度：校验失败不写盘、不重载；持久化失败（含 Save 内部
// 的整体范围校验失败）同样不重载，已生效的旧调度保持不变。
func (s *Scheduler) ApplyCronUpdate(mgr *config.Manager, expr string, job func()) error {
	if mgr == nil {
		return domain.NewError(domain.CodeValidation, "配置管理器不能为空")
	}
	if err := ValidateCron(expr); err != nil {
		return err
	}

	// 在配置快照副本上仅改动 cron 字段，避免影响其他配置项；Save 会对整体再次校验。
	cfg := mgr.Config()
	cfg.Sync.Cron = strings.TrimSpace(expr)
	if err := mgr.Save(cfg); err != nil {
		return err
	}

	// 持久化成功后才重载调度，保证「校验通过 → 落盘 → 生效」的顺序。
	return s.UpdateSchedule(cfg.Sync.Cron, job)
}

// Start 启动调度器，开始按已注册的周期触发任务。重复调用是安全的。
func (s *Scheduler) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cron.Start()
}

// Stop 停止调度器，不再触发新任务，并返回一个在所有正在执行的任务结束后被取消的
// context，便于调用方实现优雅停机。已停止的调度器可再次 Start。
func (s *Scheduler) Stop() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cron.Stop()
}
