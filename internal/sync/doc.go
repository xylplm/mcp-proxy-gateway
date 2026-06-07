// Package syncsvc 实现工具同步服务（Sync_Service）：基于 cron 调度从上游 MCP 拉取
// 工具列表写入缓存，含并发去重、超时与失败降级。
//
// 包名取为 syncsvc 而非 sync，是为了避免与 Go 标准库的 sync 包混淆——本包后续
// 任务（如 10.3）需要直接使用标准库的 sync.Map 等类型做并发去重。目录名仍保留为
// sync 以对应需求 Glossary 中的 Sync_Service。
package syncsvc
