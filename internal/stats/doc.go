// Package stats 实现统计服务（Statistics_Service）：异步采集调用记录，提供多维度
// 统计与排行，并按保留期清理。
//
// 异步写入与降级见 Recorder（recorder.go）；保留期清理见 Cleaner（cleaner.go），其按
// 配置的 statistics.retention_days（默认 90 天，范围 1-3650）定时回收 call_stat 超期数据：
// 优先 DROP 整段超期的时间分区，再逐行兜底清理边界分区与默认分区的残留（Req 16.10）。
package stats
