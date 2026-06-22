// Package stats 实现统计服务（Statistics_Service）：异步采集调用记录，提供多维度
// 统计与排行，并按保留期清理。
//
// 异步写入与降级见 Recorder（recorder.go）；保留期清理见 Cleaner（cleaner.go），其按
// 配置的 statistics.retention_days（默认 90 天，范围 1-3650）定时回收 call_stat_daily
// 超期聚合行（Req 16.10）。调用记录详情仅保留在 Redis 最近记录中。
package stats
