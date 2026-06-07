package backup

import (
	"fmt"
	"net/netip"
	"strings"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// validateBusiness 校验业务配置的引用完整性与字段级约束（Req 23.5、23.6）。
//
// 由于备份采用「父-子嵌套」结构（规则内嵌于其所属上游/API Key），归属关系天然成立；
// 此处重点校验：标识非空且唯一、必填字段齐备、CIDR 文本合法。任一不满足返回
// domain.CodeBackupInvalid 错误。
func validateBusiness(bc BusinessConfig) error {
	fields := make(map[string]string)

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
		for j, ar := range u.AliasRules {
			if strings.TrimSpace(ar.Pattern) == "" {
				fields[fmt.Sprintf("%s.aliasRules[%d].pattern", prefix, j)] = "别名规则匹配模式不能为空"
			}
		}
		for j, fr := range u.FilterRules {
			if strings.TrimSpace(fr.Pattern) == "" {
				fields[fmt.Sprintf("%s.filterRules[%d].pattern", prefix, j)] = "屏蔽规则匹配模式不能为空"
			}
		}
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
