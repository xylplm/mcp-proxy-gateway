package risk

import "testing"

func TestEffectiveLevel(t *testing.T) {
	tests := []struct {
		name string
		in   Assessment
		want Level
	}{
		{"人工覆盖优先", Assessment{Status: StatusRated, AILevel: LevelHigh, Floor: LevelMedium, ManualLevel: LevelLow, ManualConfirmed: true}, LevelLow},
		{"工具变化后旧人工覆盖失效", Assessment{Status: StatusStale, AILevel: LevelLow, Floor: LevelLow, ManualLevel: LevelLow, ManualConfirmed: true}, LevelHigh},
		{"AI 不得低于规则下限", Assessment{Status: StatusRated, AILevel: LevelLow, Floor: LevelHigh}, LevelHigh},
		{"待评级默认高风险", Assessment{Status: StatusPending, Floor: LevelLow}, LevelHigh},
		{"评级失败默认高风险", Assessment{Status: StatusError, AILevel: LevelLow, Floor: LevelLow}, LevelHigh},
		{"无 AI 结果默认高风险", Assessment{Status: StatusRated, Floor: LevelLow}, LevelHigh},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EffectiveLevel(tt.in); got != tt.want {
				t.Fatalf("EffectiveLevel() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestProfileAllows(t *testing.T) {
	for _, tt := range []struct {
		profile Profile
		level   Level
		want    bool
	}{
		{ProfileReadonly, LevelLow, true},
		{ProfileReadonly, LevelMedium, false},
		{ProfileStandard, LevelMedium, true},
		{ProfileStandard, LevelHigh, false},
		{ProfilePrivileged, LevelHigh, true},
		{ProfilePrivileged, LevelBlocked, false},
		{ProfileLegacy, LevelBlocked, true},
	} {
		if got := ProfileAllows(tt.profile, tt.level); got != tt.want {
			t.Errorf("ProfileAllows(%q, %q) = %v, want %v", tt.profile, tt.level, got, tt.want)
		}
	}
}

func TestStatusAfterClearingManualOverride(t *testing.T) {
	tests := []struct {
		name string
		in   Assessment
		want Status
	}{
		{"已变化状态保持失效", Assessment{Status: StatusStale, AILevel: LevelLow}, StatusStale},
		{"失败状态保持失效", Assessment{Status: StatusError, AILevel: LevelLow}, StatusError},
		{"没有 AI 结果回到待评级", Assessment{Status: StatusRated}, StatusPending},
		{"存在复核原因回到待复核", Assessment{Status: StatusRated, AILevel: LevelMedium, ReviewReasons: []ReviewReason{ReviewReasonLowConfidence}}, StatusNeedsReview},
		{"有效 AI 结果回到已评级", Assessment{Status: StatusRated, AILevel: LevelMedium}, StatusRated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StatusAfterClearingManualOverride(tt.in); got != tt.want {
				t.Fatalf("StatusAfterClearingManualOverride() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateManualOverrideRequiresForceBelowFloor(t *testing.T) {
	if err := ValidateManualOverride(LevelLow, LevelHigh, false, ""); err == nil {
		t.Fatal("低于规则下限且未强制时应被拒绝")
	}
	if err := ValidateManualOverride(LevelLow, LevelHigh, true, ""); err == nil {
		t.Fatal("强制降级但缺少理由时应被拒绝")
	}
	if err := ValidateManualOverride(LevelLow, LevelHigh, true, "已完成线下安全复核"); err != nil {
		t.Fatalf("有效强制降级不应失败: %v", err)
	}
}
