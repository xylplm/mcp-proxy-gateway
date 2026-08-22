package risk

import (
	"fmt"
	"strings"
)

func rank(level Level) int {
	switch level {
	case LevelLow:
		return 1
	case LevelMedium:
		return 2
	case LevelHigh:
		return 3
	case LevelBlocked:
		return 4
	default:
		return 0
	}
}

func MaxLevel(a, b Level) Level {
	if rank(a) >= rank(b) {
		return a
	}
	return b
}

func EffectiveLevel(a Assessment) Level {
	if a.Status != StatusRated && a.Status != StatusNeedsReview {
		return LevelHigh
	}
	if a.ManualConfirmed && ValidLevel(a.ManualLevel) {
		return a.ManualLevel
	}
	if !ValidLevel(a.AILevel) {
		return LevelHigh
	}
	floor := a.Floor
	if !ValidLevel(floor) {
		floor = LevelLow
	}
	return MaxLevel(a.AILevel, floor)
}

func StatusAfterClearingManualOverride(a Assessment) Status {
	if a.Status != StatusRated && a.Status != StatusNeedsReview {
		return a.Status
	}
	if !ValidLevel(a.AILevel) {
		return StatusPending
	}
	if len(a.ReviewReasons) > 0 {
		return StatusNeedsReview
	}
	return StatusRated
}

func ProfileAllows(profile Profile, level Level) bool {
	if profile == ProfileLegacy {
		return true
	}
	if level == LevelBlocked || !ValidLevel(level) {
		return false
	}
	switch profile {
	case ProfileReadonly:
		return level == LevelLow
	case ProfileStandard:
		return rank(level) <= rank(LevelMedium)
	case ProfilePrivileged:
		return rank(level) <= rank(LevelHigh)
	default:
		return false
	}
}

func ValidateManualOverride(level, floor Level, force bool, reason string) error {
	if !ValidLevel(level) {
		return fmt.Errorf("无效风险等级 %q", level)
	}
	if !ValidLevel(floor) {
		floor = LevelLow
	}
	if rank(level) >= rank(floor) {
		return nil
	}
	if !force {
		return fmt.Errorf("人工等级低于确定性风险下限，必须显式确认强制降级")
	}
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("强制降级必须填写复核理由")
	}
	return nil
}
