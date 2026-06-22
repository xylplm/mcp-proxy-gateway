package store

import (
	"errors"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

func TestAggregateDailyModelsRollsUpByUTCDayAndDimensions(t *testing.T) {
	calledAt := time.Date(2024, 5, 2, 23, 59, 0, 0, time.UTC)
	nextDay := calledAt.Add(2 * time.Minute)
	recs := []CallStatRecord{
		{
			UpstreamID:   "up-1",
			UpstreamName: "主上游",
			OriginalName: "search",
			ExposedName:  "web_search",
			APIKeyID:     "key-1",
			APIKeyName:   "服务 Key",
			CalledAt:     calledAt,
			LatencyMS:    80,
			Success:      true,
			Status:       CallStatusSuccess,
			Mode:         "smart",
			Source:       "api",
		},
		{
			UpstreamID:    "up-1",
			OriginalName:  "search",
			ExposedName:   "web_search_alias",
			APIKeyID:      "key-1",
			CalledAt:      calledAt.Add(time.Second),
			LatencyMS:     1200,
			Success:       false,
			Status:        CallStatusUpstreamError,
			ErrorMessage:  "upstream returned error",
			Mode:          "smart",
			Source:        "api",
			FailureDetail: []byte(`{"kind":"upstream_result_error"}`),
		},
		{
			UpstreamID:   "up-1",
			OriginalName: "search",
			APIKeyID:     "key-1",
			CalledAt:     nextDay,
			LatencyMS:    20,
			Success:      true,
			Mode:         "smart",
			Source:       "api",
		},
	}

	models := aggregateDailyModels(recs)
	if len(models) != 2 {
		t.Fatalf("应按 UTC 日期拆为 2 个聚合行，实际 %d", len(models))
	}
	var first callStatDailyModel
	for _, m := range models {
		if m.StatDate.Equal(utcDayStart(calledAt)) {
			first = m
		}
	}
	if first.TotalCalls != 2 || first.SuccessCalls != 1 || first.FailureCalls != 1 || first.UpstreamErrorCalls != 1 || first.FailedCalls != 0 {
		t.Fatalf("聚合计数不符：%+v", first)
	}
	if first.LatencySumMS != 1280 || first.LatencyMaxMS != 1200 || first.FailureLatencySumMS != 1200 {
		t.Fatalf("延迟聚合不符：%+v", first)
	}
	if first.LatencyLT100 != 1 || first.LatencyLT3000 != 1 {
		t.Fatalf("延迟桶聚合不符：%+v", first)
	}
	if first.ExposedNameSnapshot != "web_search_alias" {
		t.Fatalf("展示名应使用批次内最后非空快照，实际 %q", first.ExposedNameSnapshot)
	}
	if first.LastFailedAt == nil || !first.LastFailedAt.Equal(calledAt.Add(time.Second)) || first.LastErrorMessage != "upstream returned error" {
		t.Fatalf("最近失败快照不符：lastFailedAt=%v lastError=%q", first.LastFailedAt, first.LastErrorMessage)
	}
}

func TestEstimateP95LatencyMSFromBuckets(t *testing.T) {
	buckets := []latencyBucketCount{
		{UpperBoundMS: 50, Count: 90},
		{UpperBoundMS: 100, Count: 4},
		{UpperBoundMS: 200, Count: 1},
		{UpperBoundMS: 500, Count: 5},
	}
	if got := estimateP95LatencyMS(buckets, 500); got != 200 {
		t.Fatalf("P95 估算不符：got %.0f want 200", got)
	}
	if got := estimateP95LatencyMS(nil, 0); got != 0 {
		t.Fatalf("空桶 P95 应为 0，实际 %.0f", got)
	}
}

func TestNormalizeStatDateRangeUsesUTCDays(t *testing.T) {
	start := time.Date(2024, 5, 2, 23, 30, 0, 0, time.FixedZone("UTC+8", 8*3600))
	end := time.Date(2024, 5, 3, 1, 15, 0, 0, time.UTC)
	startDate, endDate, err := normalizeStatDateRange(start, end)
	if err != nil {
		t.Fatalf("合法范围不应返回错误：%v", err)
	}
	if !startDate.Equal(time.Date(2024, 5, 2, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("开始日期应归一到 UTC 日，实际 %v", startDate)
	}
	if !endDate.Equal(time.Date(2024, 5, 3, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("结束日期应归一到 UTC 日，实际 %v", endDate)
	}

	_, _, err = normalizeStatDateRange(end, start)
	if err == nil {
		t.Fatal("开始晚于结束应返回校验错误")
	}
	var apiErr *domain.APIError
	if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeValidation {
		t.Fatalf("应返回 CodeValidation，实际 %v", err)
	}
}
