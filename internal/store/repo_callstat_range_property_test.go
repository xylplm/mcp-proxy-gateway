package store

import (
	"errors"
	"testing"
	"time"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
	"pgregory.net/rapid"
)

// p22BaseMillis 为生成调用时间的基准时刻（2024-01-01T00:00:00Z 的毫秒时间戳）。
const p22BaseMillis int64 = 1_704_067_200_000

// Feature: mcp-proxy-gateway, Property 22: 统计时间范围按 UTC 日期归一
//
// Validates: Requirements 16.5, 16.7
//
// 数据库不再保存调用明细，统计查询以 call_stat_daily.stat_date 为过滤维度。因此任意
// RFC3339 start/end 会先归一到各自所在的 UTC 日期，再查询闭合日期区间
// [start_date, end_date]。开始时间严格晚于结束时间时仍返回 CodeValidation。
func TestProperty22StatRangeNormalizesToUTCDays(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		a := rapid.Int64Range(-86_400_000, 86_400_000).Draw(t, "端点A偏移")
		b := rapid.Int64Range(-86_400_000, 86_400_000).Draw(t, "端点B偏移")
		lo, hi := a, b
		if lo > hi {
			lo, hi = hi, lo
		}
		start := time.UnixMilli(p22BaseMillis + lo).UTC()
		end := time.UnixMilli(p22BaseMillis + hi).UTC()

		startDate, endDate, err := normalizeStatDateRange(start, end)
		if err != nil {
			t.Fatalf("合法范围（start<=end）不应被拒绝：start=%v end=%v err=%v", start, end, err)
		}
		if !startDate.Equal(utcDayStart(start)) {
			t.Fatalf("start 应归一到其 UTC 日期：got=%v want=%v", startDate, utcDayStart(start))
		}
		if !endDate.Equal(utcDayStart(end)) {
			t.Fatalf("end 应归一到其 UTC 日期：got=%v want=%v", endDate, utcDayStart(end))
		}
		if startDate.After(endDate) {
			t.Fatalf("合法时间范围归一后日期不应倒置：startDate=%v endDate=%v", startDate, endDate)
		}

		if lo < hi {
			_, _, err := normalizeStatDateRange(end, start)
			if err == nil {
				t.Fatalf("开始时间晚于结束时间应被拒绝：start=%v end=%v", end, start)
			}
			var apiErr *domain.APIError
			if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeValidation {
				t.Fatalf("范围无效应返回 CodeValidation 校验错误，实际：%v", err)
			}
		}
	})
}
