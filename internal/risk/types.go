package risk

import (
	"encoding/json"
	"time"
)

type Level string

const (
	LevelLow     Level = "low"
	LevelMedium  Level = "medium"
	LevelHigh    Level = "high"
	LevelBlocked Level = "blocked"
)

type Profile string

const (
	ProfileLegacy     Profile = "legacy_unrestricted"
	ProfileReadonly   Profile = "readonly"
	ProfileStandard   Profile = "standard"
	ProfilePrivileged Profile = "privileged"
)

type Status string

const (
	StatusPending     Status = "pending"
	StatusRated       Status = "rated"
	StatusNeedsReview Status = "needs_review"
	StatusStale       Status = "stale"
	StatusError       Status = "error"
	StatusRemoved     Status = "removed"
)

type ReviewReason string

const (
	ReviewReasonInsufficientEvidence ReviewReason = "insufficient_evidence"
	ReviewReasonAmbiguousScope       ReviewReason = "ambiguous_scope"
	ReviewReasonConflictingSignals   ReviewReason = "conflicting_signals"
	ReviewReasonLowConfidence        ReviewReason = "low_confidence"
	ReviewReasonBelowRuleFloor       ReviewReason = "below_rule_floor"
	ReviewReasonLegacyAIRequest      ReviewReason = "legacy_ai_request"
)

const (
	RuleVersion   = "rules-v1"
	PromptVersion = "risk-prompt-v2"
)

type Assessment struct {
	ID              string          `json:"id"`
	UpstreamID      string          `json:"upstreamId"`
	UpstreamName    string          `json:"upstreamName,omitempty"`
	OriginalName    string          `json:"originalName"`
	ExposedName     string          `json:"exposedName"`
	Description     string          `json:"description"`
	DescriptionZh   string          `json:"descriptionZh"`
	InputSchema     json.RawMessage `json:"inputSchema,omitempty"`
	Fingerprint     string          `json:"schemaFingerprint"`
	Floor           Level           `json:"deterministicFloor"`
	RuleVersion     string          `json:"ruleVersion"`
	AILevel         Level           `json:"aiLevel,omitempty"`
	AITags          []string        `json:"aiTags"`
	AIConfidence    *float64        `json:"aiConfidence,omitempty"`
	AIReason        string          `json:"aiReason"`
	ReviewReasons   []ReviewReason  `json:"reviewReasons"`
	ProviderID      string          `json:"providerId,omitempty"`
	ProviderName    string          `json:"providerName"`
	Model           string          `json:"model"`
	PromptVersion   string          `json:"promptVersion"`
	Status          Status          `json:"status"`
	LastError       string          `json:"lastError"`
	ManualLevel     Level           `json:"manualLevel,omitempty"`
	ManualTags      []string        `json:"manualTags"`
	ManualReason    string          `json:"manualReason"`
	ManualForce     bool            `json:"manualForceDowngrade"`
	ManualConfirmed bool            `json:"manualConfirmed"`
	ReviewedAt      *time.Time      `json:"reviewedAt,omitempty"`
	AssessedAt      *time.Time      `json:"assessedAt,omitempty"`
	CreatedAt       time.Time       `json:"createdAt"`
	UpdatedAt       time.Time       `json:"updatedAt"`
	Effective       Level           `json:"effectiveLevel"`
}

type DeterministicResult struct {
	Floor Level    `json:"floor"`
	Tags  []string `json:"tags"`
}

type APIStyle string

const (
	APIStyleChatCompletions APIStyle = "chat_completions"
	APIStyleResponses       APIStyle = "responses"
)

type Provider struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	BaseURL        string    `json:"baseUrl"`
	APIStyle       APIStyle  `json:"apiStyle"`
	Model          string    `json:"model"`
	APIKey         string    `json:"apiKey"`
	Enabled        bool      `json:"enabled"`
	Active         bool      `json:"active"`
	TimeoutS       int       `json:"timeoutS"`
	BatchSize      int       `json:"batchSize"`
	MaxConcurrency int       `json:"maxConcurrency"`
	AutoAssess     bool      `json:"autoAssess"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type JobStatus string

const (
	JobQueued    JobStatus = "queued"
	JobRunning   JobStatus = "running"
	JobCompleted JobStatus = "completed"
	JobPartial   JobStatus = "partial"
	JobFailed    JobStatus = "failed"
	JobCancelled JobStatus = "cancelled"
)

type AssessmentJob struct {
	ID             string         `json:"id"`
	ProviderID     string         `json:"providerId"`
	Scope          string         `json:"scope"`
	ScopePayload   map[string]any `json:"scopePayload"`
	Status         JobStatus      `json:"status"`
	RequestedCount int            `json:"requestedCount"`
	ProcessedCount int            `json:"processedCount"`
	SuccessCount   int            `json:"successCount"`
	ReviewCount    int            `json:"reviewCount"`
	FailureCount   int            `json:"failureCount"`
	RetryCount     int            `json:"retryCount"`
	SplitCount     int            `json:"splitCount"`
	ErrorCounts    map[string]int `json:"errorCounts"`
	LastError      string         `json:"lastError"`
	CreatedAt      time.Time      `json:"createdAt"`
	StartedAt      *time.Time     `json:"startedAt,omitempty"`
	FinishedAt     *time.Time     `json:"finishedAt,omitempty"`
	UpdatedAt      time.Time      `json:"updatedAt"`
}

func ValidLevel(level Level) bool {
	switch level {
	case LevelLow, LevelMedium, LevelHigh, LevelBlocked:
		return true
	default:
		return false
	}
}

func ReviewReasonsFor(result AIResult, floor Level) []ReviewReason {
	reasons := make([]ReviewReason, 0, 3)
	if result.RequiresReview {
		reasons = append(reasons, ReviewReason(result.ReviewReason))
	}
	if result.Confidence < 0.80 {
		reasons = append(reasons, ReviewReasonLowConfidence)
	}
	if MaxLevel(result.RiskLevel, floor) != result.RiskLevel {
		reasons = append(reasons, ReviewReasonBelowRuleFloor)
	}
	return reasons
}

func ValidProfile(profile Profile) bool {
	switch profile {
	case ProfileLegacy, ProfileReadonly, ProfileStandard, ProfilePrivileged:
		return true
	default:
		return false
	}
}
