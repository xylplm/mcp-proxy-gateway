package backup

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// validateBusiness 校验业务配置的引用完整性与字段级约束（Req 23.5、23.6）。
//
// 此处重点校验：标识非空且唯一、规则作用范围引用存在、必填字段齐备、CIDR 文本合法。任一不满足返回
// domain.CodeBackupInvalid 错误。
func validateBusiness(bc BusinessConfig) error {
	fields := make(map[string]string)
	ruleEngine := domain.NewRuleEngine()

	seenUpstreamID := make(map[string]struct{})
	for i, u := range bc.Upstreams {
		prefix := fmt.Sprintf("upstreams[%d]", i)
		if strings.TrimSpace(u.ID) == "" {
			fields[prefix+".id"] = "上游标识不能为空"
		} else if _, dup := seenUpstreamID[u.ID]; dup {
			fields[prefix+".id"] = "上游标识重复：" + u.ID
		} else {
			seenUpstreamID[u.ID] = struct{}{}
		}
		if strings.TrimSpace(u.Config.Name) == "" {
			fields[prefix+".config.name"] = "上游名称不能为空"
		}
		if strings.TrimSpace(string(u.Config.Transport)) == "" {
			fields[prefix+".config.transport"] = "上游传输类型不能为空"
		}
	}

	for i, ar := range bc.AliasRules {
		prefix := fmt.Sprintf("aliasRules[%d]", i)
		if strings.TrimSpace(ar.Pattern) == "" {
			fields[prefix+".pattern"] = "别名规则匹配模式不能为空"
		}
		validateRuleScope(fields, prefix, ar.ScopeType, ar.UpstreamIDs, seenUpstreamID)
		mergeRuleValidation(fields, prefix, ruleEngine.ValidateAlias(ar))
	}
	for i, fr := range bc.MCPFilterRules {
		prefix := fmt.Sprintf("mcpFilterRules[%d]", i)
		if strings.TrimSpace(fr.Pattern) == "" {
			fields[prefix+".pattern"] = "屏蔽规则匹配模式不能为空"
		}
		validateRuleScope(fields, prefix, fr.ScopeType, fr.UpstreamIDs, seenUpstreamID)
		mergeRuleValidation(fields, prefix, ruleEngine.ValidateFilter(fr))
	}
	for i, policy := range bc.ToolPolicyRules {
		prefix := fmt.Sprintf("toolPolicyRules[%d]", i)
		if strings.TrimSpace(policy.Pattern) == "" {
			fields[prefix+".pattern"] = "工具策略匹配模式不能为空"
		}
		mergeRuleValidation(fields, prefix, ruleEngine.ValidateToolPolicy(policy))
	}

	seenKeyID := make(map[string]struct{})
	for i, k := range bc.APIKeys {
		prefix := fmt.Sprintf("apiKeys[%d]", i)
		if strings.TrimSpace(k.Meta.ID) == "" {
			fields[prefix+".meta.id"] = "API Key 标识不能为空"
		} else if _, dup := seenKeyID[k.Meta.ID]; dup {
			fields[prefix+".meta.id"] = "API Key 标识重复：" + k.Meta.ID
		} else {
			seenKeyID[k.Meta.ID] = struct{}{}
		}
		if strings.TrimSpace(k.Meta.Name) == "" {
			fields[prefix+".meta.name"] = "API Key 名称不能为空"
		}
		for j, fr := range k.FilterRules {
			if strings.TrimSpace(fr.Pattern) == "" {
				fields[fmt.Sprintf("%s.filterRules[%d].pattern", prefix, j)] = "屏蔽规则匹配模式不能为空"
			}
			mergeRuleValidation(fields, fmt.Sprintf("%s.filterRules[%d]", prefix, j), ruleEngine.ValidateFilter(fr))
		}
		for j, cidr := range k.ACLCIDRs {
			if _, err := netip.ParsePrefix(cidr); err != nil {
				if _, aerr := netip.ParseAddr(cidr); aerr != nil {
					fields[fmt.Sprintf("%s.aclCidrs[%d]", prefix, j)] = "来源 CIDR 格式非法：" + cidr
				}
			}
		}
	}

	if len(fields) > 0 {
		return &domain.APIError{
			Code:    domain.CodeBackupInvalid,
			Message: "业务配置校验失败",
			Fields:  fields,
		}
	}
	return nil
}

func mergeRuleValidation(fields map[string]string, prefix string, err error) {
	if err == nil {
		return
	}
	var apiErr *domain.APIError
	if errors.As(err, &apiErr) {
		for key, msg := range apiErr.Fields {
			fields[prefix+"."+key] = msg
		}
		return
	}
	fields[prefix] = err.Error()
}

func validateRuleScope(fields map[string]string, prefix, scopeType string, upstreamIDs []string, upstreams map[string]struct{}) {
	switch scopeType {
	case "", "all":
		return
	case "upstreams":
		if len(upstreamIDs) == 0 {
			fields[prefix+".upstreamIds"] = "选择指定上游时至少选择一个上游 MCP"
			return
		}
		for j, id := range upstreamIDs {
			if _, ok := upstreams[id]; !ok {
				fields[fmt.Sprintf("%s.upstreamIds[%d]", prefix, j)] = "选择的上游 MCP 不存在：" + id
			}
		}
	default:
		fields[prefix+".scopeType"] = "作用范围只能是 all 或 upstreams"
	}
}
