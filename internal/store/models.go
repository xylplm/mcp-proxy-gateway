package store

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// JSONB 为 PostgreSQL JSONB 字段提供 database/sql 编解码。
//
// 非空 JSON 以字符串形式传给驱动，避免 []byte 被按 bytea 处理；nil/空切片表示 SQL NULL。
type JSONB []byte

func (j JSONB) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	if !json.Valid(j) {
		return nil, fmt.Errorf("非法 JSON: %s", string(j))
	}
	return string(j), nil
}

func (j *JSONB) Scan(value any) error {
	if j == nil {
		return errors.New("JSONB: Scan on nil receiver")
	}
	if value == nil {
		*j = nil
		return nil
	}
	switch v := value.(type) {
	case []byte:
		*j = append((*j)[:0], v...)
		return nil
	case string:
		*j = append((*j)[:0], v...)
		return nil
	default:
		return fmt.Errorf("JSONB: 不支持扫描类型 %T", value)
	}
}

type upstreamMCPModel struct {
	ID         string         `gorm:"column:id;type:uuid;primaryKey"`
	Name       string         `gorm:"column:name;type:varchar(100);not null;unique"`
	Tags       pq.StringArray `gorm:"column:tags;type:text[];not null;default:'{}'"`
	Transport  string         `gorm:"column:transport;type:varchar(32);not null"`
	ConnParams JSONB          `gorm:"column:conn_params;type:jsonb;not null"`
	Credential string         `gorm:"column:credential;type:text;not null;default:''"`
	Enabled    bool           `gorm:"column:enabled;type:boolean;not null;default:true"`
	SortOrder  int            `gorm:"column:sort_order;type:integer;not null"`
	AutoSync   bool           `gorm:"column:auto_sync;type:boolean;not null;default:false"`
	RateLimits JSONB          `gorm:"column:rate_limits;type:jsonb;not null;default:'{}'"`
	CreatedAt  time.Time      `gorm:"column:created_at;type:timestamptz;not null;default:now();autoCreateTime:false"`
	UpdatedAt  time.Time      `gorm:"column:updated_at;type:timestamptz;not null;default:now();autoUpdateTime:false"`
}

func (upstreamMCPModel) TableName() string { return "upstream_mcp" }

type aliasRuleModel struct {
	ID         string    `gorm:"column:id;type:uuid;primaryKey"`
	ScopeType  string    `gorm:"column:scope_type;type:varchar(16);not null;default:'all';index:idx_alias_rule_scope,priority:1"`
	Pattern    string    `gorm:"column:pattern;type:varchar(200);not null"`
	IsRegex    bool      `gorm:"column:is_regex;type:boolean;not null;default:false"`
	TargetName *string   `gorm:"column:target_name;type:varchar(100)"`
	TargetDesc *string   `gorm:"column:target_desc;type:varchar(1024)"`
	SortOrder  int       `gorm:"column:sort_order;type:integer;not null;index:idx_alias_rule_scope,priority:2"`
	CreatedAt  time.Time `gorm:"column:created_at;type:timestamptz;not null;default:now();autoCreateTime:false"`
}

func (aliasRuleModel) TableName() string { return "alias_rule" }

type aliasRuleUpstreamModel struct {
	RuleID     string `gorm:"column:rule_id;type:uuid;primaryKey"`
	UpstreamID string `gorm:"column:upstream_id;type:uuid;primaryKey;index:idx_alias_rule_upstream"`
}

func (aliasRuleUpstreamModel) TableName() string { return "alias_rule_upstream" }

type filterRuleMCPModel struct {
	ID        string    `gorm:"column:id;type:uuid;primaryKey"`
	ScopeType string    `gorm:"column:scope_type;type:varchar(16);not null;default:'all';index:idx_filter_rule_mcp_scope,priority:1"`
	Pattern   string    `gorm:"column:pattern;type:varchar(200);not null"`
	IsRegex   bool      `gorm:"column:is_regex;type:boolean;not null;default:false"`
	Enabled   bool      `gorm:"column:enabled;type:boolean;not null;default:true"`
	SortOrder int       `gorm:"column:sort_order;type:integer;not null;index:idx_filter_rule_mcp_scope,priority:2"`
	CreatedAt time.Time `gorm:"column:created_at;type:timestamptz;not null;default:now()"`
}

func (filterRuleMCPModel) TableName() string { return "filter_rule_mcp" }

type filterRuleMCPUpstreamModel struct {
	RuleID     string `gorm:"column:rule_id;type:uuid;primaryKey"`
	UpstreamID string `gorm:"column:upstream_id;type:uuid;primaryKey;index:idx_filter_rule_mcp_upstream"`
}

func (filterRuleMCPUpstreamModel) TableName() string { return "filter_rule_mcp_upstream" }

type toolPolicyRuleModel struct {
	ID              string    `gorm:"column:id;type:uuid;primaryKey"`
	Pattern         string    `gorm:"column:pattern;type:varchar(200);not null"`
	IsRegex         bool      `gorm:"column:is_regex;type:boolean;not null;default:false"`
	Enabled         bool      `gorm:"column:enabled;type:boolean;not null;default:true"`
	SortOrder       int       `gorm:"column:sort_order;type:integer;not null;index:idx_tool_policy_rule_order,priority:1"`
	RoutingStrategy string    `gorm:"column:routing_strategy;type:varchar(32);not null;default:''"`
	CacheEnabled    bool      `gorm:"column:cache_enabled;type:boolean;not null;default:false"`
	CacheTTLSeconds int       `gorm:"column:cache_ttl_seconds;type:integer;not null;default:0"`
	RiskTags        JSONB     `gorm:"column:risk_tags;type:jsonb;not null;default:'[]'"`
	IgnoredRiskTags JSONB     `gorm:"column:ignored_risk_tags;type:jsonb;not null;default:'[]'"`
	CreatedAt       time.Time `gorm:"column:created_at;type:timestamptz;not null;default:now();autoCreateTime:false"`
}

func (toolPolicyRuleModel) TableName() string { return "tool_policy_rule" }

type apiKeyModel struct {
	ID                 string     `gorm:"column:id;type:uuid;primaryKey"`
	Name               string     `gorm:"column:name;type:varchar(100);not null"`
	KeyHash            []byte     `gorm:"column:key_hash;type:bytea;not null"`
	KeyPlain           string     `gorm:"column:key_plain;type:text;not null;default:''"`
	KeyPrefix          string     `gorm:"column:key_prefix;type:varchar(12);not null"`
	Enabled            bool       `gorm:"column:enabled;type:boolean;not null;default:true"`
	ExpiresAt          *time.Time `gorm:"column:expires_at;type:timestamptz"`
	RateLimit          *int       `gorm:"column:rate_limit;type:integer"`
	RateWindowS        *int       `gorm:"column:rate_window_s;type:integer"`
	QuotaPerDay        *int       `gorm:"column:quota_per_day;type:integer"`
	QuotaPerMonth      *int       `gorm:"column:quota_per_month;type:integer"`
	RiskProfile        string     `gorm:"column:risk_profile;type:varchar(32);not null;default:'legacy_unrestricted'"`
	UpstreamAccessMode string     `gorm:"column:upstream_access_mode;type:varchar(16);not null;default:'all'"`
	CreatedAt          time.Time  `gorm:"column:created_at;type:timestamptz;not null;default:now()"`
}

func (apiKeyModel) TableName() string { return "api_key" }

type filterRuleAPIKeyModel struct {
	ID        string `gorm:"column:id;type:uuid;primaryKey"`
	APIKeyID  string `gorm:"column:api_key_id;type:uuid;not null;index:idx_filter_rule_apikey_apikey"`
	Pattern   string `gorm:"column:pattern;type:varchar(200);not null"`
	IsRegex   bool   `gorm:"column:is_regex;type:boolean;not null;default:false"`
	Enabled   bool   `gorm:"column:enabled;type:boolean;not null;default:true"`
	SortOrder int    `gorm:"column:sort_order;type:integer;not null"`
}

func (filterRuleAPIKeyModel) TableName() string { return "filter_rule_apikey" }

type apiKeyACLModel struct {
	ID       string `gorm:"column:id;type:uuid;primaryKey"`
	APIKeyID string `gorm:"column:api_key_id;type:uuid;not null;index:idx_api_key_acl_apikey"`
	CIDR     string `gorm:"column:cidr;type:cidr;not null"`
}

func (apiKeyACLModel) TableName() string { return "api_key_acl" }

type apiKeyUpstreamAccessModel struct {
	APIKeyID   string `gorm:"column:api_key_id;type:uuid;primaryKey;index:idx_api_key_upstream_access_upstream,priority:2"`
	UpstreamID string `gorm:"column:upstream_id;type:uuid;primaryKey;index:idx_api_key_upstream_access_upstream,priority:1"`
}

func (apiKeyUpstreamAccessModel) TableName() string { return "api_key_upstream_access" }

type toolCacheModel struct {
	UpstreamID     string    `gorm:"column:upstream_id;type:uuid;primaryKey"`
	Tools          JSONB     `gorm:"column:tools;type:jsonb;not null"`
	UpdatedAt      time.Time `gorm:"column:updated_at;type:timestamptz;not null"`
	AddedCount     int       `gorm:"column:added_count;type:integer;not null;default:0"`
	RemovedCount   int       `gorm:"column:removed_count;type:integer;not null;default:0"`
	SchemaChanged  int       `gorm:"column:schema_changed;type:integer;not null;default:0"`
	ChangeSyncedAt time.Time `gorm:"column:change_synced_at;type:timestamptz"`
}

func (toolCacheModel) TableName() string { return "tool_cache" }

type aiProviderModel struct {
	ID               string    `gorm:"column:id;type:uuid;primaryKey"`
	Name             string    `gorm:"column:name;type:varchar(100);not null;unique"`
	BaseURL          string    `gorm:"column:base_url;type:text;not null"`
	APIStyle         string    `gorm:"column:api_style;type:varchar(32);not null;default:'chat_completions'"`
	Model            string    `gorm:"column:model;type:varchar(200);not null"`
	APIKeyCiphertext []byte    `gorm:"column:api_key_ciphertext;type:bytea"`
	APIKeyNonce      []byte    `gorm:"column:api_key_nonce;type:bytea"`
	Enabled          bool      `gorm:"column:enabled;type:boolean;not null;default:true"`
	Active           bool      `gorm:"column:active;type:boolean;not null;default:false"`
	TimeoutS         int       `gorm:"column:timeout_s;type:integer;not null;default:60"`
	BatchSize        int       `gorm:"column:batch_size;type:integer;not null;default:10"`
	MaxConcurrency   int       `gorm:"column:max_concurrency;type:integer;not null;default:1"`
	AutoAssess       bool      `gorm:"column:auto_assess;type:boolean;not null;default:false"`
	CreatedAt        time.Time `gorm:"column:created_at;type:timestamptz;not null;default:now();autoCreateTime:false"`
	UpdatedAt        time.Time `gorm:"column:updated_at;type:timestamptz;not null;default:now();autoUpdateTime:false"`
}

func (aiProviderModel) TableName() string { return "ai_provider" }

type toolRiskAssessmentModel struct {
	ID                    string     `gorm:"column:id;type:uuid;primaryKey"`
	UpstreamID            string     `gorm:"column:upstream_id;type:uuid;not null;uniqueIndex:idx_tool_risk_source,priority:1"`
	OriginalName          string     `gorm:"column:original_name;type:varchar(200);not null;uniqueIndex:idx_tool_risk_source,priority:2"`
	ExposedNameSnapshot   string     `gorm:"column:exposed_name_snapshot;type:varchar(200);not null;default:''"`
	DescriptionSnapshot   string     `gorm:"column:description_snapshot;type:text;not null;default:''"`
	DescriptionZhSnapshot string     `gorm:"column:description_zh_snapshot;type:text;not null;default:''"`
	InputSchemaSnapshot   JSONB      `gorm:"column:input_schema_snapshot;type:jsonb;not null;default:'{}'"`
	SchemaFingerprint     string     `gorm:"column:schema_fingerprint;type:char(64);not null"`
	DeterministicFloor    string     `gorm:"column:deterministic_floor;type:varchar(16);not null"`
	RuleVersion           string     `gorm:"column:rule_version;type:varchar(32);not null"`
	AITags                JSONB      `gorm:"column:ai_tags;type:jsonb;not null;default:'[]'"`
	AILevel               *string    `gorm:"column:ai_level;type:varchar(16)"`
	AIConfidence          *float64   `gorm:"column:ai_confidence;type:numeric(5,4)"`
	AIReason              string     `gorm:"column:ai_reason;type:text;not null;default:''"`
	ReviewReasons         JSONB      `gorm:"column:review_reasons;type:jsonb;not null;default:'[]'"`
	ProviderID            *string    `gorm:"column:provider_id;type:uuid"`
	ProviderNameSnapshot  string     `gorm:"column:provider_name_snapshot;type:varchar(100);not null;default:''"`
	ModelSnapshot         string     `gorm:"column:model_snapshot;type:varchar(200);not null;default:''"`
	PromptVersion         string     `gorm:"column:prompt_version;type:varchar(32);not null;default:''"`
	Status                string     `gorm:"column:status;type:varchar(20);not null"`
	LastError             string     `gorm:"column:last_error;type:text;not null;default:''"`
	ManualLevel           *string    `gorm:"column:manual_level;type:varchar(16)"`
	ManualTags            JSONB      `gorm:"column:manual_tags;type:jsonb;not null;default:'[]'"`
	ManualReason          string     `gorm:"column:manual_reason;type:text;not null;default:''"`
	ManualForceDowngrade  bool       `gorm:"column:manual_force_downgrade;type:boolean;not null;default:false"`
	ReviewedAt            *time.Time `gorm:"column:reviewed_at;type:timestamptz"`
	AssessedAt            *time.Time `gorm:"column:assessed_at;type:timestamptz"`
	CreatedAt             time.Time  `gorm:"column:created_at;type:timestamptz;not null;default:now();autoCreateTime:false"`
	UpdatedAt             time.Time  `gorm:"column:updated_at;type:timestamptz;not null;default:now();autoUpdateTime:false"`
}

func (toolRiskAssessmentModel) TableName() string { return "tool_risk_assessment" }

type riskAssessmentJobModel struct {
	ID             string     `gorm:"column:id;type:uuid;primaryKey"`
	ProviderID     string     `gorm:"column:provider_id;type:uuid;not null"`
	Scope          string     `gorm:"column:scope;type:varchar(20);not null"`
	ScopePayload   JSONB      `gorm:"column:scope_payload;type:jsonb;not null;default:'{}'"`
	Status         string     `gorm:"column:status;type:varchar(20);not null"`
	RequestedCount int        `gorm:"column:requested_count;type:integer;not null;default:0"`
	ProcessedCount int        `gorm:"column:processed_count;type:integer;not null;default:0"`
	SuccessCount   int        `gorm:"column:success_count;type:integer;not null;default:0"`
	ReviewCount    int        `gorm:"column:review_count;type:integer;not null;default:0"`
	FailureCount   int        `gorm:"column:failure_count;type:integer;not null;default:0"`
	RetryCount     int        `gorm:"column:retry_count;type:integer;not null;default:0"`
	SplitCount     int        `gorm:"column:split_count;type:integer;not null;default:0"`
	ErrorCounts    JSONB      `gorm:"column:error_counts;type:jsonb;not null;default:'{}'"`
	LastError      string     `gorm:"column:last_error;type:text;not null;default:''"`
	CreatedAt      time.Time  `gorm:"column:created_at;type:timestamptz;not null;default:now();autoCreateTime:false"`
	StartedAt      *time.Time `gorm:"column:started_at;type:timestamptz"`
	FinishedAt     *time.Time `gorm:"column:finished_at;type:timestamptz"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;type:timestamptz;not null;default:now();autoUpdateTime:false"`
}

func (riskAssessmentJobModel) TableName() string { return "risk_assessment_job" }

type callStatDailyModel struct {
	StatDate             time.Time  `gorm:"column:stat_date;type:date;primaryKey"`
	Source               string     `gorm:"column:source;type:varchar(16);primaryKey;not null;default:'api'"`
	Mode                 string     `gorm:"column:mode;type:varchar(16);primaryKey;not null;default:'full'"`
	UpstreamID           string     `gorm:"column:upstream_id;type:varchar(36);primaryKey;not null;default:''"`
	UpstreamNameSnapshot string     `gorm:"column:upstream_name_snapshot;type:varchar(100);not null;default:''"`
	APIKeyID             string     `gorm:"column:api_key_id;type:varchar(36);primaryKey;not null;default:''"`
	APIKeyNameSnapshot   string     `gorm:"column:api_key_name_snapshot;type:varchar(100);not null;default:''"`
	OriginalName         string     `gorm:"column:original_name;type:varchar(100);primaryKey;not null;default:''"`
	ExposedNameSnapshot  string     `gorm:"column:exposed_name_snapshot;type:varchar(100);not null;default:''"`
	TotalCalls           int64      `gorm:"column:total_calls;type:bigint;not null;default:0"`
	SuccessCalls         int64      `gorm:"column:success_calls;type:bigint;not null;default:0"`
	FailureCalls         int64      `gorm:"column:failure_calls;type:bigint;not null;default:0"`
	UpstreamErrorCalls   int64      `gorm:"column:upstream_error_calls;type:bigint;not null;default:0"`
	FailedCalls          int64      `gorm:"column:failed_calls;type:bigint;not null;default:0"`
	LatencySumMS         int64      `gorm:"column:latency_sum_ms;type:bigint;not null;default:0"`
	LatencyMaxMS         int        `gorm:"column:latency_max_ms;type:integer;not null;default:0"`
	FailureLatencySumMS  int64      `gorm:"column:failure_latency_sum_ms;type:bigint;not null;default:0"`
	LatencyLT50          int64      `gorm:"column:latency_lt_50;type:bigint;not null;default:0"`
	LatencyLT100         int64      `gorm:"column:latency_lt_100;type:bigint;not null;default:0"`
	LatencyLT200         int64      `gorm:"column:latency_lt_200;type:bigint;not null;default:0"`
	LatencyLT500         int64      `gorm:"column:latency_lt_500;type:bigint;not null;default:0"`
	LatencyLT1000        int64      `gorm:"column:latency_lt_1000;type:bigint;not null;default:0"`
	LatencyLT3000        int64      `gorm:"column:latency_lt_3000;type:bigint;not null;default:0"`
	LatencyGTE3000       int64      `gorm:"column:latency_gte_3000;type:bigint;not null;default:0"`
	LastCalledAt         *time.Time `gorm:"column:last_called_at;type:timestamptz"`
	LastFailedAt         *time.Time `gorm:"column:last_failed_at;type:timestamptz"`
	LastErrorMessage     string     `gorm:"column:last_error_message;type:text;not null;default:''"`
	CreatedAt            time.Time  `gorm:"column:created_at;type:timestamptz;not null;default:now();autoCreateTime:false"`
	UpdatedAt            time.Time  `gorm:"column:updated_at;type:timestamptz;not null;default:now();autoUpdateTime:false"`
}

func (callStatDailyModel) TableName() string { return "call_stat_daily" }

type auditLogModel struct {
	ID         int64     `gorm:"column:id;type:bigserial;primaryKey;autoIncrement"`
	EventType  string    `gorm:"column:event_type;type:varchar(64);not null"`
	Target     *string   `gorm:"column:target;type:varchar(255)"`
	Detail     JSONB     `gorm:"column:detail;type:jsonb"`
	OccurredAt time.Time `gorm:"column:occurred_at;type:timestamptz;not null;default:now();autoCreateTime:false;index:idx_audit_log_occurred_at,sort:desc"`
}

func (auditLogModel) TableName() string { return "audit_log" }

type securityEventModel struct {
	ID             int64     `gorm:"column:id;type:bigserial;primaryKey;autoIncrement"`
	EventType      string    `gorm:"column:event_type;type:varchar(32);not null;index:idx_security_event_type_time,priority:1"`
	SubjectType    string    `gorm:"column:subject_type;type:varchar(32);not null;default:'';index:idx_security_event_subject_time,priority:1"`
	Subject        string    `gorm:"column:subject;type:varchar(255);not null;default:'';index:idx_security_event_subject_time,priority:2"`
	ClientIP       string    `gorm:"column:client_ip;type:varchar(64);not null;default:'';index:idx_security_event_ip_time,priority:1"`
	APIKeyID       string    `gorm:"column:api_key_id;type:varchar(36);not null;default:'';index:idx_security_event_apikey_time,priority:1"`
	APIKeyPrefix   string    `gorm:"column:api_key_prefix;type:varchar(12);not null;default:''"`
	KeyFingerprint string    `gorm:"column:key_fingerprint;type:varchar(64);not null;default:''"`
	Method         string    `gorm:"column:method;type:varchar(16);not null;default:''"`
	Path           string    `gorm:"column:path;type:varchar(255);not null;default:''"`
	UserAgent      string    `gorm:"column:user_agent;type:varchar(512);not null;default:''"`
	Reason         string    `gorm:"column:reason;type:varchar(64);not null;default:''"`
	Count          int       `gorm:"column:count;type:integer;not null;default:0"`
	CreatedAt      time.Time `gorm:"column:created_at;type:timestamptz;not null;default:now();autoCreateTime:false;index:idx_security_event_type_time,priority:2;index:idx_security_event_subject_time,priority:3;index:idx_security_event_ip_time,priority:2;index:idx_security_event_apikey_time,priority:2"`
}

func (securityEventModel) TableName() string { return "security_event" }

type securityBlockModel struct {
	ID             string     `gorm:"column:id;type:uuid;primaryKey"`
	SubjectType    string     `gorm:"column:subject_type;type:varchar(32);not null;index:idx_security_block_subject,priority:1"`
	Subject        string     `gorm:"column:subject;type:varchar(255);not null;index:idx_security_block_subject,priority:2"`
	ClientIP       string     `gorm:"column:client_ip;type:varchar(64);not null;default:'';index:idx_security_block_ip"`
	APIKeyID       string     `gorm:"column:api_key_id;type:varchar(36);not null;default:'';index:idx_security_block_apikey"`
	APIKeyPrefix   string     `gorm:"column:api_key_prefix;type:varchar(12);not null;default:''"`
	KeyFingerprint string     `gorm:"column:key_fingerprint;type:varchar(64);not null;default:''"`
	Reason         string     `gorm:"column:reason;type:varchar(64);not null;default:''"`
	FailureCount   int        `gorm:"column:failure_count;type:integer;not null;default:0"`
	Status         string     `gorm:"column:status;type:varchar(16);not null;default:'active';index:idx_security_block_status_until,priority:1"`
	BlockedUntil   *time.Time `gorm:"column:blocked_until;type:timestamptz;index:idx_security_block_status_until,priority:2"`
	ReleasedAt     *time.Time `gorm:"column:released_at;type:timestamptz"`
	CreatedAt      time.Time  `gorm:"column:created_at;type:timestamptz;not null;default:now();autoCreateTime:false"`
	UpdatedAt      time.Time  `gorm:"column:updated_at;type:timestamptz;not null;default:now();autoUpdateTime:false"`
}

func (securityBlockModel) TableName() string { return "security_block" }

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	v := s
	return &v
}

func stringValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
