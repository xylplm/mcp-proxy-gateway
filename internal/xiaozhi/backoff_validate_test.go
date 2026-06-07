package xiaozhi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// 本文件（任务 21.2）为小智地址校验与指数退避重连编写单元测试，覆盖：
//   - 接入点地址校验仅接受 ws:// 或 wss:// 合法 URL，非法拒绝（Req 15.6）；
//   - Reconfigure 在地址非法时保持原配置不变并返回错误（Req 15.6）；
//   - 指数退避重连调度器产出 min(initial × 倍数^n, 上限) 序列、始终请求重连、可重置（Req 15.4）；
//   - Connector 在每个启用周期开始时重置退避（Req 15.4/15.5）。

// --- 地址校验（Req 15.6）---

func TestValidateEndpoint(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		wantErr  bool
	}{
		{name: "合法 ws 地址", endpoint: "ws://example.com/mcp", wantErr: false},
		{name: "合法 wss 地址", endpoint: "wss://example.com:8080/mcp", wantErr: false},
		{name: "合法 ws 带查询参数", endpoint: "ws://example.com/mcp?token=abc", wantErr: false},
		{name: "空地址", endpoint: "", wantErr: true},
		{name: "仅空白", endpoint: "   ", wantErr: true},
		{name: "协议为 http", endpoint: "http://example.com", wantErr: true},
		{name: "协议为 https", endpoint: "https://example.com", wantErr: true},
		{name: "无协议", endpoint: "example.com/mcp", wantErr: true},
		{name: "缺少主机名", endpoint: "ws://", wantErr: true},
		{name: "wss 缺少主机名", endpoint: "wss:///path", wantErr: true},
		{name: "非法 URL", endpoint: "ws://exa mple.com", wantErr: true},
		// URL 协议大小写不敏感（RFC 3986），url.Parse 会归一化为小写，故大写协议被接受，
		// 与 config 层校验保持一致。
		{name: "大写协议归一化后接受", endpoint: "WS://example.com", wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateEndpoint(tc.endpoint)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望地址 %q 校验失败，但通过了", tc.endpoint)
				}
				var apiErr *domain.APIError
				if !errors.As(err, &apiErr) {
					t.Fatalf("期望错误类型为 *domain.APIError，实际为 %T", err)
				}
				if apiErr.Code != domain.CodeValidation {
					t.Fatalf("期望错误码为 VALIDATION，实际为 %s", apiErr.Code)
				}
				if _, ok := apiErr.Fields[endpointField]; !ok {
					t.Fatalf("期望字段级错误包含 %s，实际 Fields=%v", endpointField, apiErr.Fields)
				}
			} else if err != nil {
				t.Fatalf("期望地址 %q 校验通过，实际返回错误：%v", tc.endpoint, err)
			}
		})
	}
}

// --- Reconfigure：非法地址保持原配置（Req 15.6）---

func TestReconfigureRejectsInvalidEndpointAndPreservesConfig(t *testing.T) {
	agg := &fakeAggregation{}
	c := NewConnector("ws://old.example/mcp", true, agg, WithConnector(newFakeConnector()))

	err := c.Reconfigure("http://bad.example", true)
	if err == nil {
		t.Fatal("启用时提交非法协议地址应返回错误")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeValidation {
		t.Fatalf("期望返回 VALIDATION 校验错误，实际：%v", err)
	}

	// 原配置应保持不变（Req 15.6）。
	if got := c.Endpoint(); got != "ws://old.example/mcp" {
		t.Fatalf("非法更新后接入点地址应保持不变，实际=%q", got)
	}
	if !c.Enabled() {
		t.Fatal("非法更新后启用状态应保持不变")
	}
}

func TestReconfigureAcceptsValidEndpoint(t *testing.T) {
	agg := &fakeAggregation{}
	c := NewConnector("ws://old.example/mcp", true, agg, WithConnector(newFakeConnector()))

	if err := c.Reconfigure("wss://new.example/mcp", true); err != nil {
		t.Fatalf("合法地址更新应成功，实际：%v", err)
	}
	if got := c.Endpoint(); got != "wss://new.example/mcp" {
		t.Fatalf("接入点地址未更新，实际=%q", got)
	}
	if !c.Enabled() {
		t.Fatal("启用状态应为 true")
	}
}

func TestReconfigureDisabledSkipsAddressValidation(t *testing.T) {
	agg := &fakeAggregation{}
	c := NewConnector("ws://old.example/mcp", true, agg, WithConnector(newFakeConnector()))

	// 停用时不校验地址（停用不依赖地址，Req 15.5）。
	if err := c.Reconfigure("", false); err != nil {
		t.Fatalf("停用且地址为空应被接受，实际：%v", err)
	}
	if c.Enabled() {
		t.Fatal("更新后应为停用状态")
	}
}

// --- 指数退避序列（Req 15.4）---

func TestBackoffReconnectorSequenceWithDefaults(t *testing.T) {
	r := newBackoffReconnector(DefaultBackoffPolicy())

	// 默认：初始 1s、倍数 2、上限 60s → 1,2,4,8,16,32,60,60,...（封顶 60s）。
	want := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		32 * time.Second,
		60 * time.Second, // min(64, 60) 封顶
		60 * time.Second,
		60 * time.Second,
	}

	var prev time.Duration
	for i, w := range want {
		got, ok := r.NextDelay()
		if !ok {
			t.Fatalf("第 %d 次应请求重连（小智在启用期间持续重连，Req 15.4）", i)
		}
		if got != w {
			t.Fatalf("第 %d 次退避间隔期望 %v，实际 %v", i, w, got)
		}
		// 任何单次退避不超过上限，且封顶前单调不减（Req 15.4）。
		if got > 60*time.Second {
			t.Fatalf("第 %d 次退避 %v 超过上限 60s", i, got)
		}
		if got < prev {
			t.Fatalf("退避序列递减：第 %d 次 %v < 前一次 %v", i, got, prev)
		}
		prev = got
	}
}

func TestBackoffReconnectorNormalizesOutOfRange(t *testing.T) {
	// 初始退避越上限（>60s）应被钳到 60s；倍数 <1 应回落默认 2。
	r := newBackoffReconnector(BackoffPolicy{
		Initial:    120 * time.Second,
		Max:        10 * time.Second,
		Multiplier: 0,
	})

	// Initial 钳到 60s，但 Max 钳到默认 60s（10s 在范围内？10s 合法，钳制仅对越界），
	// 此处 Max=10s 合法保留；Initial=60s >= Max=10s，故首次即封顶到 Max=10s。
	got, ok := r.NextDelay()
	if !ok {
		t.Fatal("应请求重连")
	}
	if got != 10*time.Second {
		t.Fatalf("初始值不小于上限时应钳到上限 10s，实际 %v", got)
	}
}

func TestBackoffReconnectorMaxOutOfRangeClampedToBoundary(t *testing.T) {
	// 上限越上界（>3600s）应钳到上界 3600s（默认 60s 仅在低于下界 1s 时回落）。
	r := newBackoffReconnector(BackoffPolicy{
		Initial:    1 * time.Second,
		Max:        7200 * time.Second,
		Multiplier: 2,
	})
	// 推进足够多次（2^12=4096s > 3600s）应封顶到上界 3600s。
	var last time.Duration
	for i := 0; i < 14; i++ {
		last, _ = r.NextDelay()
	}
	if last != 3600*time.Second {
		t.Fatalf("上限越上界应钳到 3600s 并封顶，实际 %v", last)
	}
}

func TestBackoffReconnectorMaxBelowLowerBoundFallsBackToDefault(t *testing.T) {
	// 上限低于下界（<1s）应回落默认 60s。
	r := newBackoffReconnector(BackoffPolicy{
		Initial:    1 * time.Second,
		Max:        0, // 越下界
		Multiplier: 2,
	})
	var last time.Duration
	for i := 0; i < 12; i++ {
		last, _ = r.NextDelay()
	}
	if last != 60*time.Second {
		t.Fatalf("上限越下界应回落默认 60s 并封顶，实际 %v", last)
	}
}

func TestBackoffReconnectorReset(t *testing.T) {
	r := newBackoffReconnector(DefaultBackoffPolicy())

	// 推进几次使退避增长。
	_, _ = r.NextDelay() // 1s
	_, _ = r.NextDelay() // 2s
	got, _ := r.NextDelay()
	if got != 4*time.Second {
		t.Fatalf("第 3 次退避期望 4s，实际 %v", got)
	}

	// 重置后应从初始退避重新开始（Req 15.4/15.5）。
	r.Reset()
	got, _ = r.NextDelay()
	if got != 1*time.Second {
		t.Fatalf("重置后首次退避应回到初始 1s，实际 %v", got)
	}
}

// --- Connector 在每个启用周期重置退避（Req 15.4/15.5）---

// recordingReconnector 记录 NextDelay 与 Reset 的调用，用于断言重置时机。
type recordingReconnector struct {
	delay      time.Duration
	resetCalls int
	nextCalls  int
}

func (r *recordingReconnector) NextDelay() (time.Duration, bool) {
	r.nextCalls++
	return r.delay, true
}

func (r *recordingReconnector) Reset() { r.resetCalls++ }

func TestStartResetsBackoff(t *testing.T) {
	agg := &fakeAggregation{}
	fc := newFakeConnector()
	fc.blockUntilCancel = true

	rr := &recordingReconnector{delay: time.Millisecond}
	c := NewConnector("ws://endpoint.example/mcp", true, agg,
		WithConnector(fc),
		WithReconnector(rr),
	)

	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start 失败：%v", err)
	}
	t.Cleanup(c.Stop)

	select {
	case <-fc.startedCh:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve 未被调用")
	}

	if rr.resetCalls != 1 {
		t.Fatalf("Start 应重置退避恰好 1 次，实际 %d", rr.resetCalls)
	}
}

// TestBackoffReconnectorIsDefault 验证默认构造的连接器使用指数退避重连（Req 15.4）。
func TestBackoffReconnectorIsDefault(t *testing.T) {
	agg := &fakeAggregation{}
	c := NewConnector("ws://endpoint.example/mcp", true, agg)
	if _, ok := c.reconnector.(*backoffReconnector); !ok {
		t.Fatalf("默认重连调度器应为指数退避 backoffReconnector，实际 %T", c.reconnector)
	}
}
