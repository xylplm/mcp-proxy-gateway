package manager

import "github.com/myGithub/mcp-proxy-gateway/internal/domain"

// ValidateReorder 校验 orderedIDs 是否为 registered（已注册上游标识集合）的
// 「恰好一次排列」（Req 3.5）。
//
// 该函数为纯函数，是排序完整性校验属性测试（任务 9.4 / Property 3）的主要目标。
// 当且仅当 orderedIDs 不含重复、不含未注册标识、且覆盖全部已注册标识时返回 nil；
// 否则返回携带具体原因的 VALIDATION 错误，原因分三类并放入 Fields：
//   - duplicate：orderedIDs 中存在重复标识；
//   - unregistered：orderedIDs 含未注册标识；
//   - missing：orderedIDs 缺失某个已注册标识。
//
// 校验逻辑保证「恰好一次排列」：无重复 + 全部属于已注册集合（子集）+ 覆盖全部
// 已注册标识 三者同时成立，等价于 orderedIDs 与 registered 构成双射。
func ValidateReorder(registered, orderedIDs []string) error {
	regSet := make(map[string]struct{}, len(registered))
	for _, id := range registered {
		regSet[id] = struct{}{}
	}

	seen := make(map[string]struct{}, len(orderedIDs))
	for _, id := range orderedIDs {
		if _, dup := seen[id]; dup {
			return domain.NewValidationError(
				"排序顺序无效：包含重复的服务标识",
				map[string]string{"orderedIDs": "包含重复的服务标识：" + id},
			)
		}
		seen[id] = struct{}{}
		if _, ok := regSet[id]; !ok {
			return domain.NewValidationError(
				"排序顺序无效：包含未注册的服务标识",
				map[string]string{"orderedIDs": "包含未注册的服务标识：" + id},
			)
		}
	}

	// 子集且无重复后，仅需再校验是否覆盖全部已注册标识（缺失检查）。
	for _, id := range registered {
		if _, ok := seen[id]; !ok {
			return domain.NewValidationError(
				"排序顺序无效：缺失已注册的服务标识",
				map[string]string{"orderedIDs": "缺失已注册的服务标识：" + id},
			)
		}
	}
	return nil
}
