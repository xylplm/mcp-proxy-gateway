// Package aggregation 实现聚合服务（Aggregation_Service）：编排聚合管线
// （读缓存 → 排序合并 → 屏蔽 → 别名/描述重写 → 去重 → API Key 级过滤），
// 并负责工具调用路由与别名反向映射。具体实现见后续任务。
package aggregation
