package risk

import "testing"

func TestReviewReasonsSeparateSeverityFromUncertainty(t *testing.T) {
	clearHigh := ReviewReasonsFor(AIResult{RiskLevel: LevelHigh, Confidence: 0.99, ReviewReason: "none"}, LevelHigh)
	if len(clearHigh) != 0 {
		t.Fatalf("明确的高风险不应自动待复核: %v", clearHigh)
	}

	ambiguous := ReviewReasonsFor(AIResult{RiskLevel: LevelHigh, Confidence: 0.92, RequiresReview: true, ReviewReason: string(ReviewReasonAmbiguousScope)}, LevelMedium)
	if len(ambiguous) != 1 || ambiguous[0] != ReviewReasonAmbiguousScope {
		t.Fatalf("行为边界含糊应记录结构化原因: %v", ambiguous)
	}

	lowConfidence := ReviewReasonsFor(AIResult{RiskLevel: LevelLow, Confidence: 0.79, ReviewReason: "none"}, LevelMedium)
	if len(lowConfidence) != 2 || lowConfidence[0] != ReviewReasonLowConfidence || lowConfidence[1] != ReviewReasonBelowRuleFloor {
		t.Fatalf("低置信度和低于规则下限应分别记录: %v", lowConfidence)
	}
}
