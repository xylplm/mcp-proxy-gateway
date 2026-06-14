package store

import (
	"errors"
	"testing"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// TestNormalizeTimezoneName 验证统计分组时区名的归一化与校验。
//
//   - 空串回退 UTC，保证缺省 tz 的旧调用方行为不变；
//   - 合法 IANA 时区名原样返回（如 Asia/Shanghai、America/New_York）；
//   - 非法时区名返回字段级 CodeValidation 错误，避免任意字符串拼入 SQL。
func TestNormalizeTimezoneName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{"空串回退UTC", "", "UTC", false},
		{"UTC 显式", "UTC", "UTC", false},
		{"东八区", "Asia/Shanghai", "Asia/Shanghai", false},
		{"西五区", "America/New_York", "America/New_York", false},
		{"非 IANA 名称", "Beijing Time", "", true},
		{"不存在的区域", "Mars/Olympus", "", true},
		{"拼接注入企图", "UTC'); DROP TABLE x;--", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeTimezoneName(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望校验错误，实际 got=%q err=nil", got)
				}
				var apiErr *domain.APIError
				if !errors.As(err, &apiErr) || apiErr.Code != domain.CodeValidation {
					t.Fatalf("期望 CodeValidation，实际 %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("非预期错误：%v", err)
			}
			if got != tc.want {
				t.Errorf("归一化结果不符：got=%q want=%q", got, tc.want)
			}
		})
	}
}
