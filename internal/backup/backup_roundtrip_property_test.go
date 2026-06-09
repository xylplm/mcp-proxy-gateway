package backup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"github.com/myGithub/mcp-proxy-gateway/internal/store"
	"pgregory.net/rapid"
)

// 本文件实现配置导入导出往返的属性测试（任务 24.2，Property 27）。
//
// 三类断言对应 Req 23.4 / 23.5 / 23.6：
//  1. 往返等价：任意合法备份经 Marshal → ParseAndValidate（及 Service.Export →
//     Import → 再 Export）后得到与原始等价的配置；
//  2. 格式非法：任意非备份 / 畸形 JSON 字节被拒绝，返回 CodeBackupInvalid；
//  3. 校验失败：结构可解析但内容非法（错误版本、必填字段为空、标识重复、CIDR
//     非法、YAML 取值越界等）的备份被拒绝，返回 CodeBackupInvalid。
//
// 生成器约定（为保证 JSON 往返后语义等价）：
//   - 字节切片只生成 nil 或长度 ≥1，规避「nil 与空切片」往返不一致；
//   - map 只生成 nil 或非空，且值统一为字符串（避免 any 数字被解码为 float64）；
//   - time.Time 由 time.Unix(sec,0).UTC() 构造，无单调时钟、无亚秒尾零问题。

// --- 基础生成器 ---------------------------------------------------------------

// genNonEmptyToken 生成不含空白、长度 1~20 的标识/名称片段，确保通过 TrimSpace 非空校验。
func genNonEmptyToken(t *rapid.T, label string) string {
	return rapid.StringMatching(`[a-zA-Z0-9_\-]{1,20}`).Draw(t, label)
}

// genOptionalBytes 生成 nil 或长度 ≥1 的字节切片。
func genOptionalBytes(t *rapid.T, label string) []byte {
	if !rapid.Bool().Draw(t, label+"_present") {
		return nil
	}
	return rapid.SliceOfN(rapid.Byte(), 1, 8).Draw(t, label)
}

// genConnParams 生成 nil 或值为字符串的非空连接参数 map（字符串值可无损 JSON 往返）。
func genConnParams(t *rapid.T, label string) map[string]any {
	if !rapid.Bool().Draw(t, label+"_present") {
		return nil
	}
	n := rapid.IntRange(1, 3).Draw(t, label+"_n")
	m := make(map[string]any, n)
	for i := 0; i < n; i++ {
		m[fmt.Sprintf("p%d", i)] = rapid.String().Draw(t, fmt.Sprintf("%s_v%d", label, i))
	}
	return m
}

// genCleanTime 生成可无损 JSON 往返的 UTC 时间。
func genCleanTime(t *rapid.T, label string) time.Time {
	sec := rapid.Int64Range(0, 4102444800).Draw(t, label) // 0 ~ 约公元 2100 年
	return time.Unix(sec, 0).UTC()
}

// genOptionalTime 生成 nil 或干净 UTC 时间指针。
func genOptionalTime(t *rapid.T, label string) *time.Time {
	if !rapid.Bool().Draw(t, label+"_present") {
		return nil
	}
	v := genCleanTime(t, label)
	return &v
}

// genOptionalInt 生成 nil 或正整数指针。
func genOptionalInt(t *rapid.T, label string) *int {
	if !rapid.Bool().Draw(t, label+"_present") {
		return nil
	}
	v := rapid.IntRange(1, 1000).Draw(t, label)
	return &v
}

// validTransports 为合法的上游传输类型集合。
var validTransports = []domain.TransportType{
	domain.TransportStdio, domain.TransportSSE,
	domain.TransportStreamableHTTP, domain.TransportWebSocket,
}

// validCIDRs 为 validateBusiness 接受的合法 CIDR / 裸 IP 集合。
var validCIDRs = []string{
	"10.0.0.0/8", "192.168.1.1/32", "127.0.0.1",
	"::1", "2001:db8::/32", "172.16.0.0/12",
}

// --- 合法 YAML 配置生成器 -----------------------------------------------------

// genValidYAMLConfig 在 ValidateYAMLConfig 接受的取值范围内生成 YAML 常规配置。
func genValidYAMLConfig(t *rapid.T) config.YAMLConfig {
	cfg := config.DefaultYAMLConfig()

	// Admin 在备份往返中原样保留；用受限 token / 任意串覆盖，含空值（首次初始化场景）。
	if rapid.Bool().Draw(t, "adminInit") {
		cfg.Admin.Username = genNonEmptyToken(t, "adminUser")
		cfg.Admin.PasswordHash = genNonEmptyToken(t, "adminHash")
		cfg.Admin.Initialized = true
	} else {
		cfg.Admin = config.AdminConfig{}
	}

	cfg.Auth.SessionTimeoutS = rapid.IntRange(300, 86400).Draw(t, "sessionTimeout")
	cfg.Sync.Cron = rapid.SampledFrom([]string{"0 */30 * * * *", "0 0 * * * *", "*/5 * * * * *"}).Draw(t, "cron")
	cfg.Sync.TimeoutS = rapid.IntRange(5, 300).Draw(t, "syncTimeout")
	cfg.Connection.ConnectTimeoutS = rapid.IntRange(1, 3600).Draw(t, "connectTimeout")
	cfg.Connection.RetryInitialBackoffS = rapid.IntRange(1, 60).Draw(t, "retryInitial")
	cfg.Connection.RetryMultiplier = rapid.IntRange(1, 10).Draw(t, "retryMultiplier")
	cfg.Connection.RetryMaxBackoffS = rapid.IntRange(1, 3600).Draw(t, "retryMax")
	cfg.Connection.FailureThreshold = rapid.IntRange(1, 100).Draw(t, "failureThreshold")
	cfg.Aggregation.UpstreamCallTimeoutS = rapid.IntRange(1, 600).Draw(t, "aggTimeout")
	cfg.MCPAPI.Mode = rapid.SampledFrom([]string{config.ModeSmart, config.ModeFull}).Draw(t, "mcpMode")
	cfg.MCPAPI.SmartDiscoveryLimit = rapid.IntRange(1, 200).Draw(t, "smartLimit")
	cfg.Statistics.TopLimitDefault = rapid.IntRange(1, 100).Draw(t, "topLimit")
	cfg.Statistics.RetentionDays = rapid.IntRange(1, 3650).Draw(t, "statRetention")
	cfg.Audit.PageSizeDefault = rapid.IntRange(1, 200).Draw(t, "auditPage")
	cfg.Audit.RetentionDays = rapid.IntRange(1, 3650).Draw(t, "auditRetention")

	// XiaoZhi：启用时 endpoint 必须为合法 ws/wss URL。
	if rapid.Bool().Draw(t, "xiaozhiEnabled") {
		cfg.XiaoZhi.Enabled = true
		cfg.XiaoZhi.Endpoint = rapid.SampledFrom([]string{
			"ws://localhost:8080/mcp", "wss://example.com/xiaozhi",
		}).Draw(t, "xiaozhiEndpoint")
	} else {
		cfg.XiaoZhi = config.XiaoZhiConfig{}
	}

	return cfg
}

// --- 合法业务配置生成器 -------------------------------------------------------

// genValidAliasRule 生成通过 validateBusiness 字段校验的别名规则。
func genValidAliasRule(t *rapid.T, upstreamIDs []string, label string) domain.AliasRule {
	scopeType := "all"
	ruleUpstreamIDs := []string(nil)
	if len(upstreamIDs) > 0 && rapid.Bool().Draw(t, label+"_scoped") {
		scopeType = "upstreams"
		ruleUpstreamIDs = []string{rapid.SampledFrom(upstreamIDs).Draw(t, label+"_upstreamID")}
	}
	return domain.AliasRule{
		ID:          genNonEmptyToken(t, label+"_id"),
		ScopeType:   scopeType,
		UpstreamIDs: ruleUpstreamIDs,
		Pattern:     genNonEmptyToken(t, label+"_pattern"),
		IsRegex:     rapid.Bool().Draw(t, label+"_isRegex"),
		TargetName:  genNonEmptyToken(t, label+"_targetName"),
		TargetDesc:  rapid.String().Draw(t, label+"_targetDesc"),
		SortOrder:   rapid.IntRange(0, 100).Draw(t, label+"_sortOrder"),
	}
}

// genValidFilterRule 生成通过 validateBusiness 字段校验的屏蔽规则。
func genValidFilterRule(t *rapid.T, upstreamIDs []string, label string) domain.FilterRule {
	scopeType := "all"
	ruleUpstreamIDs := []string(nil)
	if len(upstreamIDs) > 0 && rapid.Bool().Draw(t, label+"_scoped") {
		scopeType = "upstreams"
		ruleUpstreamIDs = []string{rapid.SampledFrom(upstreamIDs).Draw(t, label+"_upstreamID")}
	}
	return domain.FilterRule{
		ID:          genNonEmptyToken(t, label+"_id"),
		ScopeType:   scopeType,
		UpstreamIDs: ruleUpstreamIDs,
		Pattern:     genNonEmptyToken(t, label+"_pattern"),
		IsRegex:     rapid.Bool().Draw(t, label+"_isRegex"),
		Enabled:     rapid.Bool().Draw(t, label+"_enabled"),
		SortOrder:   rapid.IntRange(0, 100).Draw(t, label+"_sortOrder"),
	}
}

// genValidBusinessConfig 生成引用完整、字段合法且标识唯一的业务配置。
func genValidBusinessConfig(t *rapid.T) BusinessConfig {
	var bc BusinessConfig

	nUp := rapid.IntRange(0, 3).Draw(t, "nUpstreams")
	bc.Upstreams = make([]UpstreamEntry, 0, nUp)
	upstreamIDs := make([]string, 0, nUp)
	for i := 0; i < nUp; i++ {
		upID := fmt.Sprintf("upstream-%d-%s", i, genNonEmptyToken(t, fmt.Sprintf("upID%d", i)))
		upstreamIDs = append(upstreamIDs, upID)
		entry := UpstreamEntry{
			ID: upID,
			Config: domain.UpstreamConfig{
				Name:       genNonEmptyToken(t, fmt.Sprintf("upName%d", i)),
				Transport:  rapid.SampledFrom(validTransports).Draw(t, fmt.Sprintf("upTransport%d", i)),
				ConnParams: genConnParams(t, fmt.Sprintf("upConn%d", i)),
				Enabled:    rapid.Bool().Draw(t, fmt.Sprintf("upEnabled%d", i)),
				SortOrder:  rapid.IntRange(0, 100).Draw(t, fmt.Sprintf("upSort%d", i)),
				AutoSync:   rapid.Bool().Draw(t, fmt.Sprintf("upAutoSync%d", i)),
			},
			CredentialEnc: genOptionalBytes(t, fmt.Sprintf("upCred%d", i)),
		}
		bc.Upstreams = append(bc.Upstreams, entry)
	}
	nAlias := rapid.IntRange(0, 4).Draw(t, "nAlias")
	bc.AliasRules = make([]domain.AliasRule, 0, nAlias)
	for i := 0; i < nAlias; i++ {
		bc.AliasRules = append(bc.AliasRules, genValidAliasRule(t, upstreamIDs, fmt.Sprintf("alias%d", i)))
	}
	nMCPFilter := rapid.IntRange(0, 4).Draw(t, "nMCPFilter")
	bc.MCPFilterRules = make([]domain.FilterRule, 0, nMCPFilter)
	for i := 0; i < nMCPFilter; i++ {
		bc.MCPFilterRules = append(bc.MCPFilterRules, genValidFilterRule(t, upstreamIDs, fmt.Sprintf("mcpFilter%d", i)))
	}

	nKey := rapid.IntRange(0, 3).Draw(t, "nKeys")
	bc.APIKeys = make([]APIKeyEntry, 0, nKey)
	for i := 0; i < nKey; i++ {
		entry := APIKeyEntry{
			Meta: store.APIKey{
				ID:        fmt.Sprintf("key-%d-%s", i, genNonEmptyToken(t, fmt.Sprintf("keyID%d", i))),
				Name:      genNonEmptyToken(t, fmt.Sprintf("keyName%d", i)),
				KeyHash:   genOptionalBytes(t, fmt.Sprintf("keyHash%d", i)),
				KeyPrefix: rapid.StringMatching(`[a-zA-Z0-9_]{0,10}`).Draw(t, fmt.Sprintf("keyPrefix%d", i)),
				Enabled:   rapid.Bool().Draw(t, fmt.Sprintf("keyEnabled%d", i)),
				ExpiresAt: genOptionalTime(t, fmt.Sprintf("keyExpires%d", i)),
				CreatedAt: genCleanTime(t, fmt.Sprintf("keyCreated%d", i)),
			},
		}
		nFilter := rapid.IntRange(0, 2).Draw(t, fmt.Sprintf("nKeyFilter%d", i))
		for j := 0; j < nFilter; j++ {
			entry.FilterRules = append(entry.FilterRules,
				genValidFilterRule(t, nil, fmt.Sprintf("keyFilter%d_%d", i, j)))
		}
		nACL := rapid.IntRange(0, 3).Draw(t, fmt.Sprintf("nACL%d", i))
		for j := 0; j < nACL; j++ {
			entry.ACLCIDRs = append(entry.ACLCIDRs,
				rapid.SampledFrom(validCIDRs).Draw(t, fmt.Sprintf("acl%d_%d", i, j)))
		}
		bc.APIKeys = append(bc.APIKeys, entry)
	}

	return bc
}

// genValidBackup 组装一份完整的合法备份。
func genValidBackup(t *rapid.T) Backup {
	return Backup{
		Version:  FormatVersion,
		YAML:     genValidYAMLConfig(t),
		Business: genValidBusinessConfig(t),
	}
}

// --- 非法备份生成器（结构可解析但内容非法）-----------------------------------

// corruptValidBackup 接收一份合法备份，注入一处确定的内容级非法，使其必被
// Validate 以 CodeBackupInvalid 拒绝；返回被破坏后的备份。
func corruptValidBackup(t *rapid.T, b Backup) Backup {
	kind := rapid.IntRange(0, 6).Draw(t, "corruptKind")
	switch kind {
	case 0:
		// 错误版本号。
		b.Version = "mpg-backup/v" + genNonEmptyToken(t, "badVersion")
	case 1:
		// YAML 取值越界（mode 非法）。
		b.YAML.MCPAPI.Mode = "invalid-mode-" + genNonEmptyToken(t, "badMode")
	case 2:
		// 上游标识为空。
		if len(b.Business.Upstreams) == 0 {
			b.Business.Upstreams = append(b.Business.Upstreams, UpstreamEntry{Config: domain.UpstreamConfig{Name: "x", Transport: domain.TransportSSE}})
		}
		b.Business.Upstreams[0].ID = ""
	case 3:
		// 上游标识重复。
		if len(b.Business.Upstreams) == 0 {
			e := UpstreamEntry{ID: "dup", Config: domain.UpstreamConfig{Name: "x", Transport: domain.TransportSSE}}
			b.Business.Upstreams = append(b.Business.Upstreams, e, e)
		} else {
			b.Business.Upstreams = append(b.Business.Upstreams, b.Business.Upstreams[0])
		}
	case 4:
		// 上游名称为空。
		if len(b.Business.Upstreams) == 0 {
			b.Business.Upstreams = append(b.Business.Upstreams, UpstreamEntry{ID: "u1", Config: domain.UpstreamConfig{Transport: domain.TransportSSE}})
		} else {
			b.Business.Upstreams[0].Config.Name = ""
		}
	case 5:
		// API Key 名称为空。
		if len(b.Business.APIKeys) == 0 {
			b.Business.APIKeys = append(b.Business.APIKeys, APIKeyEntry{Meta: store.APIKey{ID: "k1"}})
		} else {
			b.Business.APIKeys[0].Meta.Name = ""
		}
	case 6:
		// 非法 CIDR。
		if len(b.Business.APIKeys) == 0 {
			b.Business.APIKeys = append(b.Business.APIKeys, APIKeyEntry{Meta: store.APIKey{ID: "k1", Name: "n1"}, ACLCIDRs: []string{"not-a-cidr"}})
		} else {
			b.Business.APIKeys[0].ACLCIDRs = append(b.Business.APIKeys[0].ACLCIDRs, "999.999.0.0/8")
		}
	}
	return b
}

// genMalformedBytes 生成「格式非法 / 非备份」的字节序列：既包括根本无法解析的
// 畸形 JSON，也包括能解析为 JSON 但不是合法备份对象（含未知字段、尾随内容、
// 顶层非对象等）的字节。
func genMalformedBytes(t *rapid.T) []byte {
	kind := rapid.IntRange(0, 6).Draw(t, "malformedKind")
	switch kind {
	case 0:
		return []byte(rapid.String().Draw(t, "arbitraryStr"))
	case 1:
		return rapid.SliceOf(rapid.Byte()).Draw(t, "arbitraryBytes")
	case 2:
		return []byte("{ this is not valid json")
	case 3:
		// 顶层为 JSON 数组而非对象。
		return []byte("[1,2,3]")
	case 4:
		// 含未知字段（DisallowUnknownFields 拒绝）。
		return []byte(`{"version":"` + FormatVersion + `","unexpectedField":true}`)
	case 5:
		// 合法对象 + 尾随内容。
		return []byte(`{"version":"` + FormatVersion + `"} {"trailing":1}`)
	case 6:
		// 字段类型不符（version 应为字符串）。
		return []byte(`{"version":12345}`)
	}
	return []byte("{}")
}

// assertBackupInvalidRapid 在 rapid 上下文中断言 err 为 CodeBackupInvalid 错误。
func assertBackupInvalidRapid(t *rapid.T, err error, desc string) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望返回备份无效错误，却返回 nil：%s", desc)
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("期望错误类型 *domain.APIError，实际 %T：%s err=%v", err, desc, err)
	}
	if apiErr.Code != domain.CodeBackupInvalid {
		t.Fatalf("期望错误码 %q，实际 %q：%s（%s）", domain.CodeBackupInvalid, apiErr.Code, desc, apiErr.Message)
	}
}

// Feature: mcp-proxy-gateway, Property 27: 配置导入导出往返
//
// Validates: Requirements 23.4, 23.5, 23.6
//
// 对任意系统配置：
//   - （Req 23.4/23.5）合法备份经 Marshal → ParseAndValidate 得到与原始
//     JSON 语义等价的备份；且经 Service.Export → Import → 再 Export
//     得到字节级等价、配置等价的结果（导出再导入再导出闭环稳定）。
//   - （Req 23.6）任意非备份 / 畸形字节被 ParseAndValidate 与 Service.Import
//     拒绝，返回 CodeBackupInvalid，且不对目标存储产生写入。
//   - （Req 23.6）结构可解析但内容非法的备份被拒绝，返回 CodeBackupInvalid。
//
// 在编解码层（Marshal/ParseAndValidate）与服务层（Export/Import，借助内存 fake
// 存储）双重验证往返；内存 fake 复用 service_test.go 中的 fakeYAMLStore /
// fakeBusinessStore，使往返无需真实数据库。
func TestProperty27ConfigBackupRoundTrip(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		ctx := context.Background()

		// ---- 1. 合法备份：编解码层往返等价（Req 23.4、23.5）----
		orig := genValidBackup(t)

		// 前置自检：生成器必须产出合法备份，否则属性失去意义。
		if err := Validate(orig); err != nil {
			t.Fatalf("生成器应产出合法备份，却校验失败：%v", err)
		}

		data, err := Marshal(orig)
		if err != nil {
			t.Fatalf("Marshal 合法备份失败：%v", err)
		}
		got, err := ParseAndValidate(data)
		if err != nil {
			t.Fatalf("ParseAndValidate 合法备份失败：%v", err)
		}
		gotData, err := Marshal(got)
		if err != nil {
			t.Fatalf("Marshal 往返备份失败：%v", err)
		}
		if string(data) != string(gotData) {
			t.Fatalf("编解码往返后备份不等价\n原始=%+v\n往返=%+v", orig, got)
		}

		// ---- 2. 合法备份：服务层 Export → Import → 再 Export 闭环（Req 23.4、23.5）----
		srcYAML := &fakeYAMLStore{cfg: orig.YAML}
		srcBiz := &fakeBusinessStore{bc: orig.Business}
		srcSvc := NewService(srcYAML, srcBiz)

		exported, err := srcSvc.Export(ctx)
		if err != nil {
			t.Fatalf("Service.Export 失败：%v", err)
		}

		dstYAML := &fakeYAMLStore{cfg: config.DefaultYAMLConfig()}
		dstBiz := &fakeBusinessStore{}
		dstSvc := NewService(dstYAML, dstBiz)
		if err := dstSvc.Import(ctx, exported); err != nil {
			t.Fatalf("Service.Import 合法备份失败：%v", err)
		}

		// 导入后的配置应与原始等价。
		if !reflect.DeepEqual(dstYAML.cfg, orig.YAML) {
			t.Fatalf("导入后 YAML 配置不等价\n原始=%+v\n导入=%+v", orig.YAML, dstYAML.cfg)
		}
		if !jsonEqual(orig.Business, dstBiz.bc) {
			t.Fatalf("导入后业务配置不等价\n原始=%+v\n导入=%+v", orig.Business, dstBiz.bc)
		}

		// 闭环稳定：再次导出应得到与首次导出字节级一致的备份。
		reExported, err := dstSvc.Export(ctx)
		if err != nil {
			t.Fatalf("二次 Service.Export 失败：%v", err)
		}
		if !reflect.DeepEqual(exported, reExported) {
			t.Fatalf("导出→导入→再导出未得到字节级等价的备份")
		}

		// ---- 3. 格式非法 / 非备份字节被拒绝（Req 23.6）----
		malformed := genMalformedBytes(t)
		_, perr := ParseAndValidate(malformed)
		assertBackupInvalidRapid(t, perr, "ParseAndValidate(畸形字节)")

		// 经服务层导入同样被拒绝，且不污染已有数据。
		guardYAML := &fakeYAMLStore{cfg: orig.YAML}
		guardBiz := &fakeBusinessStore{bc: orig.Business}
		guardSvc := NewService(guardYAML, guardBiz)
		ierr := guardSvc.Import(ctx, malformed)
		assertBackupInvalidRapid(t, ierr, "Service.Import(畸形字节)")
		if !jsonEqual(orig.Business, guardBiz.bc) || !reflect.DeepEqual(guardYAML.cfg, orig.YAML) {
			t.Fatalf("导入非法备份不应改动已有配置")
		}

		// ---- 4. 内容非法（结构可解析但校验失败）的备份被拒绝（Req 23.6）----
		corrupted := corruptValidBackup(t, genValidBackup(t))
		cdata, cerr := Marshal(corrupted)
		if cerr != nil {
			t.Fatalf("Marshal 被破坏备份失败：%v", cerr)
		}
		_, cverr := ParseAndValidate(cdata)
		assertBackupInvalidRapid(t, cverr, "ParseAndValidate(内容非法备份)")
		assertBackupInvalidRapid(t, guardSvc.Import(ctx, cdata), "Service.Import(内容非法备份)")
	})
}

func jsonEqual[T any](a, b T) bool {
	ab, err := json.Marshal(a)
	if err != nil {
		return false
	}
	bb, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(ab) == string(bb)
}
