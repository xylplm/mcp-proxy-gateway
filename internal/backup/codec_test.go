package backup

import (
	"reflect"
	"strings"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/config"
)

// TestMarshalUnmarshalRoundTrip 验证 Marshal 后 Unmarshal 得到等价的备份对象。
func TestMarshalUnmarshalRoundTrip(t *testing.T) {
	orig := Backup{
		Version:  FormatVersion,
		YAML:     config.DefaultYAMLConfig(),
		Business: sampleBusiness(),
	}
	data, err := Marshal(orig)
	if err != nil {
		t.Fatalf("Marshal 失败：%v", err)
	}
	got, err := Unmarshal(data)
	if err != nil {
		t.Fatalf("Unmarshal 失败：%v", err)
	}
	if !reflect.DeepEqual(orig, got) {
		t.Errorf("往返后备份对象不等价\n原始=%+v\n往返=%+v", orig, got)
	}
}

// TestUnmarshalRejectsUnknownFields 验证含未知字段的备份被拒绝（避免静默丢弃）。
func TestUnmarshalRejectsUnknownFields(t *testing.T) {
	data := []byte(`{"version":"` + FormatVersion + `","unexpected":true}`)
	if _, err := Unmarshal(data); err == nil {
		t.Fatal("含未知字段的备份应被拒绝，却解析成功")
	}
}

// TestUnmarshalRejectsTrailingContent 验证含多余尾随内容的备份被拒绝。
func TestUnmarshalRejectsTrailingContent(t *testing.T) {
	data := []byte(`{"version":"` + FormatVersion + `"} {"extra":1}`)
	if _, err := Unmarshal(data); err == nil {
		t.Fatal("含尾随内容的备份应被拒绝，却解析成功")
	}
}

// TestValidateAcceptsValidBackup 验证一份合法备份通过校验。
func TestValidateAcceptsValidBackup(t *testing.T) {
	b := Backup{Version: FormatVersion, YAML: config.DefaultYAMLConfig(), Business: sampleBusiness()}
	if err := Validate(b); err != nil {
		t.Fatalf("合法备份应通过校验，却返回错误：%v", err)
	}
}

// TestValidateRejectsDuplicateUpstreamID 验证上游标识重复的业务配置被拒绝。
func TestValidateRejectsDuplicateUpstreamID(t *testing.T) {
	biz := sampleBusiness()
	dup := biz.Upstreams[0]
	biz.Upstreams = append(biz.Upstreams, dup)
	b := Backup{Version: FormatVersion, YAML: config.DefaultYAMLConfig(), Business: biz}
	assertBackupInvalid(t, Validate(b))
}

// TestValidateRejectsEmptyUpstreamName 验证上游名称为空的业务配置被拒绝。
func TestValidateRejectsEmptyUpstreamName(t *testing.T) {
	biz := sampleBusiness()
	biz.Upstreams[0].Config.Name = ""
	b := Backup{Version: FormatVersion, YAML: config.DefaultYAMLConfig(), Business: biz}
	assertBackupInvalid(t, Validate(b))
}

// TestValidateRejectsAliasWithoutTarget 验证备份导入复用领域别名规则校验，
// 不允许绕过管理 API 导入缺少目标名称/描述的别名规则。
func TestValidateRejectsAliasWithoutTarget(t *testing.T) {
	biz := sampleBusiness()
	biz.AliasRules[0].TargetName = ""
	biz.AliasRules[0].TargetDesc = ""
	b := Backup{Version: FormatVersion, YAML: config.DefaultYAMLConfig(), Business: biz}

	err := Validate(b)
	assertBackupInvalid(t, err)
	assertBackupField(t, err, "aliasRules[0].targetName")
}

// TestValidateRejectsInvalidRegexFilter 验证备份中的 MCP 级屏蔽规则仍需满足领域正则校验。
func TestValidateRejectsInvalidRegexFilter(t *testing.T) {
	biz := sampleBusiness()
	biz.MCPFilterRules[0].Pattern = "["
	biz.MCPFilterRules[0].IsRegex = true
	b := Backup{Version: FormatVersion, YAML: config.DefaultYAMLConfig(), Business: biz}

	err := Validate(b)
	assertBackupInvalid(t, err)
	assertBackupField(t, err, "mcpFilterRules[0].pattern")
}

// TestValidateRejectsInvalidAPIKeyFilter 验证 API Key 从属屏蔽规则同样不能绕过领域规则校验。
func TestValidateRejectsInvalidAPIKeyFilter(t *testing.T) {
	biz := sampleBusiness()
	biz.APIKeys[0].FilterRules[0].Pattern = strings.Repeat("x", 201)
	b := Backup{Version: FormatVersion, YAML: config.DefaultYAMLConfig(), Business: biz}

	err := Validate(b)
	assertBackupInvalid(t, err)
	assertBackupField(t, err, "apiKeys[0].filterRules[0].pattern")
}
