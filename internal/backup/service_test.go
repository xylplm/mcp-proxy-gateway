package backup

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
)

// fakeYAMLStore 是 YAMLStore 的内存实现，便于在不触及文件系统的前提下测试 Service。
type fakeYAMLStore struct {
	cfg config.YAMLConfig
}

func (f *fakeYAMLStore) Config() config.YAMLConfig { return f.cfg }

func (f *fakeYAMLStore) Save(cfg config.YAMLConfig) error {
	// 模仿 config.Manager.Save 的语义：先校验再应用。
	if err := config.ValidateYAMLConfig(cfg); err != nil {
		return err
	}
	f.cfg = cfg
	return nil
}

// fakeBusinessStore 是 BusinessStore 的内存实现。
type fakeBusinessStore struct {
	bc BusinessConfig
}

func (f *fakeBusinessStore) ExportBusiness(_ context.Context) (BusinessConfig, error) {
	return f.bc, nil
}

func (f *fakeBusinessStore) ImportBusiness(_ context.Context, bc BusinessConfig) error {
	f.bc = bc
	return nil
}

// sampleBusiness 构造一份典型业务配置，覆盖上游、别名/屏蔽规则与 API Key 各类从属配置。
func sampleBusiness() BusinessConfig {
	return BusinessConfig{
		Upstreams: []UpstreamEntry{
			{
				ID: "11111111-1111-4111-8111-111111111111",
				Config: domain.UpstreamConfig{
					Name:       "demo-upstream",
					Transport:  domain.TransportSSE,
					ConnParams: map[string]any{"url": "https://example.com/sse"},
					Credential: "plain-credential",
					Enabled:    true,
					SortOrder:  0,
					AutoSync:   true,
				},
			},
		},
		AliasRules: []domain.AliasRule{
			{ID: "a1", ScopeType: "upstreams", UpstreamIDs: []string{"11111111-1111-4111-8111-111111111111"}, Pattern: "foo", TargetName: "bar", SortOrder: 0},
		},
		MCPFilterRules: []domain.FilterRule{
			{ID: "f1", ScopeType: "upstreams", UpstreamIDs: []string{"11111111-1111-4111-8111-111111111111"}, Pattern: "secret*", IsRegex: false, Enabled: true, SortOrder: 0},
		},
		APIKeys: []APIKeyEntry{
			{
				Meta: store.APIKey{
					ID:        "22222222-2222-4222-8222-222222222222",
					Name:      "demo-key",
					KeyHash:   []byte{0xaa, 0xbb},
					KeyPrefix: "mpg_abc",
					Enabled:   true,
				},
				FilterRules: []domain.FilterRule{
					{ID: "f2", Pattern: "danger*", Enabled: true, SortOrder: 0},
				},
				ACLCIDRs: []string{"10.0.0.0/8", "192.168.1.1/32"},
			},
		},
	}
}

// TestExportImportRoundTrip 验证导出的备份再导入后得到等价配置（Req 23.4、23.5）。
func TestExportImportRoundTrip(t *testing.T) {
	srcYAML := &fakeYAMLStore{cfg: config.DefaultYAMLConfig()}
	srcYAML.cfg.MCPAPI.SmartDiscoveryLimit = 77 // 制造一处非默认值以确保确实被还原
	srcBiz := &fakeBusinessStore{bc: sampleBusiness()}
	srcSvc := NewService(srcYAML, srcBiz)

	data, err := srcSvc.Export(context.Background())
	if err != nil {
		t.Fatalf("导出失败：%v", err)
	}

	// 导入到一套全新的空存储中。
	dstYAML := &fakeYAMLStore{cfg: config.DefaultYAMLConfig()}
	dstBiz := &fakeBusinessStore{}
	dstSvc := NewService(dstYAML, dstBiz)

	if err := dstSvc.Import(context.Background(), data); err != nil {
		t.Fatalf("导入合法备份应成功，却返回错误：%v", err)
	}

	if !reflect.DeepEqual(dstYAML.cfg, srcYAML.cfg) {
		t.Errorf("导入后 YAML 配置应与导出前等价\n导出前=%+v\n导入后=%+v", srcYAML.cfg, dstYAML.cfg)
	}
	if len(dstBiz.bc.Upstreams) != 1 || dstBiz.bc.Upstreams[0].Config.Name != "demo-upstream" {
		t.Errorf("导入后上游配置不符：%+v", dstBiz.bc.Upstreams)
	}
	if len(dstBiz.bc.APIKeys) != 1 || dstBiz.bc.APIKeys[0].Meta.Name != "demo-key" {
		t.Errorf("导入后 API Key 配置不符：%+v", dstBiz.bc.APIKeys)
	}
	if len(dstBiz.bc.APIKeys[0].ACLCIDRs) != 2 {
		t.Errorf("导入后白名单条数不符：%+v", dstBiz.bc.APIKeys[0].ACLCIDRs)
	}
}

// TestImportRejectsMalformedJSON 验证格式非法（非 JSON）的备份被拒绝并返回备份无效错误（Req 23.6）。
func TestImportRejectsMalformedJSON(t *testing.T) {
	svc := NewService(&fakeYAMLStore{cfg: config.DefaultYAMLConfig()}, &fakeBusinessStore{})
	err := svc.Import(context.Background(), []byte("{ this is not valid json"))
	assertBackupInvalid(t, err)
}

// TestImportRejectsWrongVersion 验证版本号不匹配的备份被拒绝（Req 23.6）。
func TestImportRejectsWrongVersion(t *testing.T) {
	b := Backup{Version: "mpg-backup/v999", YAML: config.DefaultYAMLConfig(), Business: sampleBusiness()}
	data, err := Marshal(b)
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	svc := NewService(&fakeYAMLStore{cfg: config.DefaultYAMLConfig()}, &fakeBusinessStore{})
	assertBackupInvalid(t, svc.Import(context.Background(), data))
}

// TestImportRejectsInvalidYAMLConfig 验证 YAML 配置越界的备份被拒绝（Req 23.6）。
func TestImportRejectsInvalidYAMLConfig(t *testing.T) {
	cfg := config.DefaultYAMLConfig()
	cfg.XiaoZhi.Mode = "invalid-mode" // 非 smart/full
	b := Backup{Version: FormatVersion, YAML: cfg, Business: sampleBusiness()}
	data, _ := Marshal(b)

	svc := NewService(&fakeYAMLStore{cfg: config.DefaultYAMLConfig()}, &fakeBusinessStore{})
	assertBackupInvalid(t, svc.Import(context.Background(), data))
}

// TestImportRejectsInvalidBusinessConfig 验证业务配置非法（如 CIDR 非法）的备份被拒绝（Req 23.6）。
func TestImportRejectsInvalidBusinessConfig(t *testing.T) {
	biz := sampleBusiness()
	biz.APIKeys[0].ACLCIDRs = []string{"not-a-cidr"}
	b := Backup{Version: FormatVersion, YAML: config.DefaultYAMLConfig(), Business: biz}
	data, _ := Marshal(b)

	svc := NewService(&fakeYAMLStore{cfg: config.DefaultYAMLConfig()}, &fakeBusinessStore{})
	assertBackupInvalid(t, svc.Import(context.Background(), data))
}

// TestImportDoesNotApplyInvalidBackup 验证非法备份不会对目标存储产生任何写入（Req 23.6）。
func TestImportDoesNotApplyInvalidBackup(t *testing.T) {
	dstYAML := &fakeYAMLStore{cfg: config.DefaultYAMLConfig()}
	dstBiz := &fakeBusinessStore{bc: sampleBusiness()} // 预置原有数据
	svc := NewService(dstYAML, dstBiz)

	before := dstBiz.bc
	assertBackupInvalid(t, svc.Import(context.Background(), []byte("garbage")))

	if len(dstBiz.bc.Upstreams) != len(before.Upstreams) {
		t.Errorf("非法备份不应改动业务配置，却发生变更")
	}
}

// assertBackupInvalid 断言 err 为 domain.CodeBackupInvalid 错误。
func assertBackupInvalid(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("期望返回备份无效错误，却返回 nil")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望错误类型为 *domain.APIError，实际为 %T：%v", err, err)
	}
	if apiErr.Code != domain.CodeBackupInvalid {
		t.Fatalf("期望错误码 %q，实际 %q（%s）", domain.CodeBackupInvalid, apiErr.Code, apiErr.Message)
	}
}

func assertBackupField(t *testing.T, err error, field string) {
	t.Helper()
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望错误类型为 *domain.APIError，实际为 %T：%v", err, err)
	}
	if apiErr.Fields[field] == "" {
		t.Fatalf("期望字段 %q 有校验错误，实际 fields=%+v", field, apiErr.Fields)
	}
}
