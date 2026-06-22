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

type apiKeyModel struct {
	ID          string     `gorm:"column:id;type:uuid;primaryKey"`
	Name        string     `gorm:"column:name;type:varchar(100);not null"`
	KeyHash     []byte     `gorm:"column:key_hash;type:bytea;not null"`
	KeyPlain    string     `gorm:"column:key_plain;type:text;not null;default:''"`
	KeyPrefix   string     `gorm:"column:key_prefix;type:varchar(12);not null"`
	Enabled     bool       `gorm:"column:enabled;type:boolean;not null;default:true"`
	ExpiresAt   *time.Time `gorm:"column:expires_at;type:timestamptz"`
	RateLimit   *int       `gorm:"column:rate_limit;type:integer"`
	RateWindowS *int       `gorm:"column:rate_window_s;type:integer"`
	CreatedAt   time.Time  `gorm:"column:created_at;type:timestamptz;not null;default:now()"`
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

type toolCacheModel struct {
	UpstreamID string    `gorm:"column:upstream_id;type:uuid;primaryKey"`
	Tools      JSONB     `gorm:"column:tools;type:jsonb;not null"`
	UpdatedAt  time.Time `gorm:"column:updated_at;type:timestamptz;not null"`
}

func (toolCacheModel) TableName() string { return "tool_cache" }

type callStatModel struct {
	ID             int64     `gorm:"column:id;type:bigserial;primaryKey;autoIncrement"`
	UpstreamID     *string   `gorm:"column:upstream_id;type:uuid"`
	OriginalName   string    `gorm:"column:original_name;type:varchar(100);not null"`
	ExposedName    *string   `gorm:"column:exposed_name;type:varchar(100)"`
	APIKeyID       *string   `gorm:"column:api_key_id;type:uuid"`
	CalledAt       time.Time `gorm:"column:called_at;type:timestamptz;primaryKey;not null"`
	LatencyMS      int       `gorm:"column:latency_ms;type:integer;not null"`
	Success        bool      `gorm:"column:success;type:boolean;not null"`
	Status         string    `gorm:"column:status;type:varchar(32);not null;default:'success'"`
	RequestArgs    JSONB     `gorm:"column:request_args;type:jsonb"`
	ResponseResult JSONB     `gorm:"column:response_result;type:jsonb"`
	ErrorMessage   *string   `gorm:"column:error_message;type:text"`
	FailureDetail  JSONB     `gorm:"column:failure_detail;type:jsonb"`
	Mode           string    `gorm:"column:mode;type:varchar(16);not null;default:'full'"`
	Source         string    `gorm:"column:source;type:varchar(16);not null;default:'api'"`
}

func (callStatModel) TableName() string { return "call_stat" }

type auditLogModel struct {
	ID         int64     `gorm:"column:id;type:bigserial;primaryKey;autoIncrement"`
	EventType  string    `gorm:"column:event_type;type:varchar(64);not null"`
	Target     *string   `gorm:"column:target;type:varchar(255)"`
	Detail     JSONB     `gorm:"column:detail;type:jsonb"`
	OccurredAt time.Time `gorm:"column:occurred_at;type:timestamptz;not null;default:now();autoCreateTime:false;index:idx_audit_log_occurred_at,sort:desc"`
}

func (auditLogModel) TableName() string { return "audit_log" }

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
