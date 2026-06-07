package health

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// fakePinger 是 Pinger 窄接口的内存实现，可分别注入 PG/Redis 的探测错误。
type fakePinger struct {
	pgErr    error
	redisErr error
}

func (f fakePinger) PingPG(_ context.Context) error    { return f.pgErr }
func (f fakePinger) PingRedis(_ context.Context) error { return f.redisErr }

// fakeConfig 是 ConfigProvider 窄接口的内存实现，返回固定的小智接入配置。
type fakeConfig struct {
	xz config.XiaoZhiConfig
}

func (c fakeConfig) Config() config.YAMLConfig {
	return config.YAMLConfig{XiaoZhi: c.xz}
}

// newUpstream 构造一个用于测试的上游 MCP 领域对象。
func newUpstream(name string, enabled bool) domain.Upstream {
	return domain.Upstream{
		ID: name + "-id",
		Config: domain.UpstreamConfig{
			Name:      name,
			Transport: domain.TransportSSE,
			Enabled:   enabled,
		},
	}
}

// captureLogger 返回一个写入缓冲区的 JSON slog.Logger 以及该缓冲区，便于断言日志内容。
func captureLogger() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelInfo}))
	return logger, &buf
}

// findResult 在报告中查找首个匹配 component 且 name 匹配的结果。
func findResult(report StartupReport, component, name string) (ProbeResult, bool) {
	for _, r := range report.Results {
		if r.Component == component && r.Name == name {
			return r, true
		}
	}
	return ProbeResult{}, false
}

func TestProbeStartup_AllSuccess(t *testing.T) {
	logger, _ := captureLogger()
	prober := NewStartupProber(Options{
		Pinger: fakePinger{},
		ListUpstreams: func(_ context.Context) ([]domain.Upstream, error) {
			return []domain.Upstream{newUpstream("up-a", true), newUpstream("up-b", true)}, nil
		},
		ProbeUpstream: func(_ context.Context, _ domain.Upstream) error { return nil },
		Config:        fakeConfig{xz: config.XiaoZhiConfig{Enabled: true, Endpoint: "wss://xz.example/mcp"}},
		ProbeXiaoZhi:  func(_ context.Context, _ string) error { return nil },
		Logger:        logger,
	})

	report := prober.ProbeStartup(context.Background())

	if !report.AllOK() {
		t.Fatalf("全部探测应成功，失败项：%+v", report.Failures())
	}
	// 应包含 PG、Redis、两个上游、小智，共 5 项，且按序排列。
	if len(report.Results) != 5 {
		t.Fatalf("应有 5 项探测结果，实际 %d 项：%+v", len(report.Results), report.Results)
	}
	wantOrder := []string{ComponentPostgres, ComponentRedis, ComponentUpstream, ComponentUpstream, ComponentXiaoZhi}
	for i, want := range wantOrder {
		if report.Results[i].Component != want {
			t.Errorf("第 %d 项组件应为 %q，实际 %q", i, want, report.Results[i].Component)
		}
	}
}

func TestProbeStartup_PartialFailureRecordsReason(t *testing.T) {
	logger, buf := captureLogger()
	pgErr := errors.New("连接 PostgreSQL 失败：dial timeout")
	upErr := errors.New("上游初始化失败：handshake error")
	prober := NewStartupProber(Options{
		Pinger: fakePinger{pgErr: pgErr},
		ListUpstreams: func(_ context.Context) ([]domain.Upstream, error) {
			return []domain.Upstream{newUpstream("up-bad", true)}, nil
		},
		ProbeUpstream: func(_ context.Context, _ domain.Upstream) error { return upErr },
		Config:        fakeConfig{xz: config.XiaoZhiConfig{Enabled: false}},
		Logger:        logger,
	})

	report := prober.ProbeStartup(context.Background())

	if report.AllOK() {
		t.Fatal("存在失败项时 AllOK 应为 false")
	}

	// PG 失败应记录原因。
	pgRes, ok := findResult(report, ComponentPostgres, "PostgreSQL")
	if !ok {
		t.Fatal("应包含 PostgreSQL 探测结果")
	}
	if pgRes.Status != ProbeFailed || pgRes.Reason != pgErr.Error() {
		t.Errorf("PG 探测应失败且记录原因，实际 status=%q reason=%q", pgRes.Status, pgRes.Reason)
	}

	// Redis 未注入错误，应成功。
	redisRes, ok := findResult(report, ComponentRedis, "Redis")
	if !ok || !redisRes.OK() {
		t.Errorf("Redis 探测应成功，实际 %+v", redisRes)
	}

	// 上游失败应记录原因。
	upRes, ok := findResult(report, ComponentUpstream, "up-bad")
	if !ok || upRes.Status != ProbeFailed || upRes.Reason != upErr.Error() {
		t.Errorf("上游探测应失败且记录原因，实际 %+v", upRes)
	}

	// Failures 应恰好包含 PG 与上游两项。
	failures := report.Failures()
	if len(failures) != 2 {
		t.Fatalf("应有 2 项失败，实际 %d 项：%+v", len(failures), failures)
	}

	// 校验失败原因被写入结构化日志。
	logOut := buf.String()
	if !strings.Contains(logOut, "dial timeout") {
		t.Errorf("日志应包含 PG 失败原因，实际：%s", logOut)
	}
	if !strings.Contains(logOut, "handshake error") {
		t.Errorf("日志应包含上游失败原因，实际：%s", logOut)
	}
	assertFailureLogged(t, logOut, ComponentPostgres, "PostgreSQL")
	assertFailureLogged(t, logOut, ComponentUpstream, "up-bad")
}

// assertFailureLogged 校验日志中存在一条对应组件/名称且 status=failed 的失败记录。
func assertFailureLogged(t *testing.T, logOut, component, name string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(logOut), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("日志行应为合法 JSON：%v（行：%s）", err, line)
		}
		if entry["component"] == component && entry["name"] == name && entry["status"] == string(ProbeFailed) {
			return
		}
	}
	t.Errorf("日志应包含 component=%q name=%q status=failed 的记录，实际：%s", component, name, logOut)
}

func TestProbeStartup_OnlyProbesEnabledUpstreams(t *testing.T) {
	logger, _ := captureLogger()
	var probed []string
	prober := NewStartupProber(Options{
		Pinger: fakePinger{},
		ListUpstreams: func(_ context.Context) ([]domain.Upstream, error) {
			return []domain.Upstream{
				newUpstream("enabled-1", true),
				newUpstream("disabled-1", false),
				newUpstream("enabled-2", true),
			}, nil
		},
		ProbeUpstream: func(_ context.Context, up domain.Upstream) error {
			probed = append(probed, up.Config.Name)
			return nil
		},
		Config: fakeConfig{},
		Logger: logger,
	})

	report := prober.ProbeStartup(context.Background())

	if len(probed) != 2 {
		t.Fatalf("应仅探测 2 个启用上游，实际探测 %d 个：%v", len(probed), probed)
	}
	for _, name := range probed {
		if name == "disabled-1" {
			t.Errorf("停用上游不应被探测，实际探测了 %q", name)
		}
	}
	// 停用上游不应出现在结果中。
	if _, ok := findResult(report, ComponentUpstream, "disabled-1"); ok {
		t.Error("停用上游不应出现在探测结果中")
	}
}

func TestProbeStartup_XiaoZhiEnabled(t *testing.T) {
	logger, _ := captureLogger()
	var probedEndpoint string
	prober := NewStartupProber(Options{
		Pinger:        fakePinger{},
		ListUpstreams: func(_ context.Context) ([]domain.Upstream, error) { return nil, nil },
		ProbeUpstream: func(_ context.Context, _ domain.Upstream) error { return nil },
		Config:        fakeConfig{xz: config.XiaoZhiConfig{Enabled: true, Endpoint: "wss://xz.example/mcp"}},
		ProbeXiaoZhi: func(_ context.Context, endpoint string) error {
			probedEndpoint = endpoint
			return nil
		},
		Logger: logger,
	})

	report := prober.ProbeStartup(context.Background())

	if probedEndpoint != "wss://xz.example/mcp" {
		t.Errorf("应以配置的接入点地址探测小智，实际 %q", probedEndpoint)
	}
	xzRes, ok := findResult(report, ComponentXiaoZhi, "wss://xz.example/mcp")
	if !ok || !xzRes.OK() {
		t.Errorf("小智探测应成功，实际 %+v", xzRes)
	}
}

func TestProbeStartup_XiaoZhiEnabledFailureRecordsReason(t *testing.T) {
	logger, buf := captureLogger()
	xzErr := errors.New("小智接入点连接失败：connection refused")
	prober := NewStartupProber(Options{
		Pinger:        fakePinger{},
		ListUpstreams: func(_ context.Context) ([]domain.Upstream, error) { return nil, nil },
		ProbeUpstream: func(_ context.Context, _ domain.Upstream) error { return nil },
		Config:        fakeConfig{xz: config.XiaoZhiConfig{Enabled: true, Endpoint: "wss://xz.example/mcp"}},
		ProbeXiaoZhi:  func(_ context.Context, _ string) error { return xzErr },
		Logger:        logger,
	})

	report := prober.ProbeStartup(context.Background())

	xzRes, ok := findResult(report, ComponentXiaoZhi, "wss://xz.example/mcp")
	if !ok || xzRes.Status != ProbeFailed || xzRes.Reason != xzErr.Error() {
		t.Errorf("小智探测应失败且记录原因，实际 %+v", xzRes)
	}
	if !strings.Contains(buf.String(), "connection refused") {
		t.Errorf("日志应包含小智失败原因，实际：%s", buf.String())
	}
}

func TestProbeStartup_XiaoZhiDisabledSkipped(t *testing.T) {
	logger, _ := captureLogger()
	xiaoZhiCalled := false
	prober := NewStartupProber(Options{
		Pinger:        fakePinger{},
		ListUpstreams: func(_ context.Context) ([]domain.Upstream, error) { return nil, nil },
		ProbeUpstream: func(_ context.Context, _ domain.Upstream) error { return nil },
		Config:        fakeConfig{xz: config.XiaoZhiConfig{Enabled: false, Endpoint: "wss://xz.example/mcp"}},
		ProbeXiaoZhi: func(_ context.Context, _ string) error {
			xiaoZhiCalled = true
			return nil
		},
		Logger: logger,
	})

	report := prober.ProbeStartup(context.Background())

	if xiaoZhiCalled {
		t.Error("小智接入未启用时不应执行小智探测")
	}
	for _, r := range report.Results {
		if r.Component == ComponentXiaoZhi {
			t.Errorf("小智未启用时不应出现小智探测结果，实际 %+v", r)
		}
	}
}

func TestProbeStartup_MissingPingerSkipsDependencies(t *testing.T) {
	logger, _ := captureLogger()
	prober := NewStartupProber(Options{
		Config: fakeConfig{},
		Logger: logger,
	})

	report := prober.ProbeStartup(context.Background())

	if len(report.Results) != 0 {
		t.Fatalf("无任何探测能力时不应有探测结果，实际 %d 项", len(report.Results))
	}
	// 无任何探测项时 AllOK 约定为 true。
	if !report.AllOK() {
		t.Error("无探测项时 AllOK 应为 true")
	}
}
