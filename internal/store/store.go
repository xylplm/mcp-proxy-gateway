package store

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// NewDB 依据 DSN 建立 GORM PostgreSQL 连接并校验连通性。
//
// 设计为可测试与可装配：调用方传入连接串（DSN），返回数据库句柄与 error。
//   - DSN 解析失败（格式非法）立即返回校验类错误，不发起网络连接（Req 18.1）。
//   - 解析成功后打开连接并以 Ping 校验连通性；连通失败返回错误供启动终止。
//
// 返回的数据库句柄由调用方在程序退出前通过 CloseDB 释放底层连接池。
func NewDB(ctx context.Context, dsn string) (*gorm.DB, error) {
	if dsn == "" {
		return nil, domain.NewError(domain.CodeValidation, "PostgreSQL DSN 不能为空")
	}

	// 先复用 pgx 解析器尽早暴露 DSN 格式错误（无需网络）；GORM PostgreSQL 驱动底层同样使用 pgx。
	if _, err := pgx.ParseConfig(dsn); err != nil {
		return nil, domain.NewError(domain.CodeValidation, "解析 PostgreSQL DSN 失败："+err.Error())
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		DisableForeignKeyConstraintWhenMigrating: true,
		TranslateError:                           true,
	})
	if err != nil {
		return nil, fmt.Errorf("打开 PostgreSQL 连接失败：%w", err)
	}
	if err := PingDB(ctx, db); err != nil {
		_ = CloseDB(db)
		return nil, err
	}
	return db, nil
}

// PingDB 校验 PostgreSQL 连通性，供启动期连通性探测复用。
func PingDB(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return domain.NewError(domain.CodeValidation, "PostgreSQL 客户端为空")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("获取 PostgreSQL 底层连接失败：%w", err)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		return fmt.Errorf("连接 PostgreSQL 失败：%w", err)
	}
	return nil
}

// CloseDB 释放 GORM 底层 database/sql 连接池。
func CloseDB(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// NewRedisClient 依据地址与密码建立 go-redis/v9 客户端。
//
// go-redis 采用惰性连接（首次命令时才建立连接），因此此处仅做参数校验与客户端构造，
// 不在构造阶段发起网络请求，便于装配与单元测试。连通性探测在健康检查中进行。
//   - addr 为空时返回校验类错误。
func NewRedisClient(addr, password string) (*redis.Client, error) {
	if addr == "" {
		return nil, domain.NewError(domain.CodeValidation, "Redis 地址不能为空")
	}

	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
	})
	return client, nil
}

// PingRedis 校验 Redis 连通性，供启动期连通性探测复用。
func PingRedis(ctx context.Context, client *redis.Client) error {
	if client == nil {
		return domain.NewError(domain.CodeValidation, "Redis 客户端为空")
	}
	if err := client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("连接 Redis 失败：%w", err)
	}
	return nil
}

// AutoMigrate 在连接 PostgreSQL 成功后、对外服务前初始化当前开发期 schema。
//
// 普通业务表使用 GORM AutoMigrate 维护；调用统计按每日多维聚合事实表保存，
// 由额外幂等 DDL 维护主键与查询索引。
func AutoMigrate(ctx context.Context, db *gorm.DB, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	if db == nil {
		err := domain.NewError(domain.CodeValidation, "PostgreSQL 客户端为空")
		logger.Error("初始化数据库 schema 失败", "error", err)
		return err
	}

	tx := db.WithContext(ctx)
	models := []any{
		&upstreamMCPModel{},
		&aliasRuleModel{},
		&aliasRuleUpstreamModel{},
		&filterRuleMCPModel{},
		&filterRuleMCPUpstreamModel{},
		&toolPolicyRuleModel{},
		&apiKeyModel{},
		&aiProviderModel{},
		&toolRiskAssessmentModel{},
		&riskAssessmentJobModel{},
		&filterRuleAPIKeyModel{},
		&apiKeyACLModel{},
		&apiKeyUpstreamAccessModel{},
		&toolCacheModel{},
		&auditLogModel{},
		&securityEventModel{},
		&securityBlockModel{},
	}
	if err := tx.AutoMigrate(models...); err != nil {
		logger.Error("执行 GORM AutoMigrate 失败", "error", err)
		return fmt.Errorf("执行 GORM AutoMigrate 失败：%w", err)
	}
	if err := ensureSchemaExtras(ctx, db); err != nil {
		logger.Error("初始化 PostgreSQL 特性 schema 失败", "error", err)
		return err
	}

	logger.Info("数据库 schema 初始化完成")
	return nil
}

func ensureSchemaExtras(ctx context.Context, db *gorm.DB) error {
	steps := []string{
		createCallStatDailyTableSQL,
		// Provider 密钥改为明文后，不迁移旧密文；按当前产品策略直接丢弃历史加密字段。
		`ALTER TABLE ai_provider ADD COLUMN IF NOT EXISTS api_key text NOT NULL DEFAULT ''`,
		`ALTER TABLE ai_provider DROP COLUMN IF EXISTS api_key_ciphertext`,
		`ALTER TABLE ai_provider DROP COLUMN IF EXISTS api_key_nonce`,
		`UPDATE ai_provider SET active = false WHERE enabled = false AND active = true`,
		`CREATE INDEX IF NOT EXISTS idx_call_stat_daily_date ON call_stat_daily (stat_date)`,
		`CREATE INDEX IF NOT EXISTS idx_call_stat_daily_upstream_date ON call_stat_daily (upstream_id, stat_date)`,
		`CREATE INDEX IF NOT EXISTS idx_call_stat_daily_apikey_date ON call_stat_daily (api_key_id, stat_date)`,
		`CREATE INDEX IF NOT EXISTS idx_call_stat_daily_tool_date ON call_stat_daily (upstream_id, original_name, stat_date)`,
		`CREATE INDEX IF NOT EXISTS idx_alias_rule_scope ON alias_rule (scope_type, sort_order)`,
		`CREATE INDEX IF NOT EXISTS idx_alias_rule_upstream ON alias_rule_upstream (upstream_id)`,
		`CREATE INDEX IF NOT EXISTS idx_filter_rule_mcp_scope ON filter_rule_mcp (scope_type, sort_order)`,
		`CREATE INDEX IF NOT EXISTS idx_filter_rule_mcp_upstream ON filter_rule_mcp_upstream (upstream_id)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_policy_rule_order ON tool_policy_rule (sort_order, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_filter_rule_apikey_apikey ON filter_rule_apikey (api_key_id)`,
		`CREATE INDEX IF NOT EXISTS idx_api_key_acl_apikey ON api_key_acl (api_key_id)`,
		`CREATE INDEX IF NOT EXISTS idx_api_key_upstream_access_upstream ON api_key_upstream_access (upstream_id, api_key_id)`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_provider_single_active ON ai_provider (active) WHERE active = true`,
		`CREATE INDEX IF NOT EXISTS idx_tool_risk_status ON tool_risk_assessment (status, updated_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_risk_level ON tool_risk_assessment (deterministic_floor, ai_level)`,
		`CREATE INDEX IF NOT EXISTS idx_tool_risk_effective_level ON tool_risk_assessment (effective_level, status)`,
		// 存量行 effective_level 默认为 'high'，对 status 非 rated/needs_review 的行这已经正确；
		// 对已评级行需要按业务规则补全：manual_level 优先，否则取 ai_level 与 deterministic_floor 的较高值。
		`UPDATE tool_risk_assessment SET effective_level = CASE
			WHEN status NOT IN ('rated','needs_review') THEN 'high'
			WHEN manual_level IS NOT NULL THEN manual_level
			WHEN ai_level IS NULL THEN 'high'
			WHEN (CASE ai_level WHEN 'low' THEN 1 WHEN 'medium' THEN 2 WHEN 'high' THEN 3 WHEN 'blocked' THEN 4 ELSE 0 END)
			  >= (CASE deterministic_floor WHEN 'low' THEN 1 WHEN 'medium' THEN 2 WHEN 'high' THEN 3 WHEN 'blocked' THEN 4 ELSE 0 END)
			THEN ai_level
			ELSE deterministic_floor
		END
		WHERE effective_level = 'high'
		  AND status IN ('rated','needs_review')
		  AND (manual_level IS NOT NULL OR ai_level IS NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS idx_risk_job_status ON risk_assessment_job (status, created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_risk_job_provider_status ON risk_assessment_job (provider_id, status)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_log_occurred_at ON audit_log (occurred_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_security_event_created_at ON security_event (created_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_security_block_created_at ON security_block (created_at DESC)`,
	}
	for _, step := range steps {
		if err := db.WithContext(ctx).Exec(step).Error; err != nil {
			return fmt.Errorf("执行 schema 初始化 SQL 失败：%w", err)
		}
	}
	return ensureCascadeConstraints(ctx, db)
}

const createCallStatDailyTableSQL = `
	CREATE TABLE IF NOT EXISTS call_stat_daily (
		stat_date                  DATE NOT NULL,
		source                     VARCHAR(16) NOT NULL DEFAULT 'api',
		mode                       VARCHAR(16) NOT NULL DEFAULT 'full',
		upstream_id                VARCHAR(36) NOT NULL DEFAULT '',
		upstream_name_snapshot     VARCHAR(100) NOT NULL DEFAULT '',
		api_key_id                 VARCHAR(36) NOT NULL DEFAULT '',
		api_key_name_snapshot      VARCHAR(100) NOT NULL DEFAULT '',
		original_name              VARCHAR(100) NOT NULL DEFAULT '',
		exposed_name_snapshot      VARCHAR(100) NOT NULL DEFAULT '',
		total_calls                BIGINT NOT NULL DEFAULT 0,
		success_calls              BIGINT NOT NULL DEFAULT 0,
		failure_calls              BIGINT NOT NULL DEFAULT 0,
		upstream_error_calls       BIGINT NOT NULL DEFAULT 0,
		failed_calls               BIGINT NOT NULL DEFAULT 0,
		latency_sum_ms             BIGINT NOT NULL DEFAULT 0,
		latency_max_ms             INTEGER NOT NULL DEFAULT 0,
		failure_latency_sum_ms     BIGINT NOT NULL DEFAULT 0,
		latency_lt_50              BIGINT NOT NULL DEFAULT 0,
		latency_lt_100             BIGINT NOT NULL DEFAULT 0,
		latency_lt_200             BIGINT NOT NULL DEFAULT 0,
		latency_lt_500             BIGINT NOT NULL DEFAULT 0,
		latency_lt_1000            BIGINT NOT NULL DEFAULT 0,
		latency_lt_3000            BIGINT NOT NULL DEFAULT 0,
		latency_gte_3000           BIGINT NOT NULL DEFAULT 0,
		last_called_at             TIMESTAMPTZ,
		last_failed_at             TIMESTAMPTZ,
		last_error_message         TEXT NOT NULL DEFAULT '',
		created_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_at                 TIMESTAMPTZ NOT NULL DEFAULT now(),
		PRIMARY KEY (stat_date, source, mode, upstream_id, api_key_id, original_name)
	)`

type cascadeConstraint struct {
	Table string
	Name  string
	SQL   string
}

func cascadeConstraintDefinitions() []cascadeConstraint {
	return []cascadeConstraint{
		{"alias_rule_upstream", "alias_rule_upstream_rule_id_fkey", `ALTER TABLE alias_rule_upstream ADD CONSTRAINT alias_rule_upstream_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES alias_rule(id) ON DELETE CASCADE`},
		{"alias_rule_upstream", "alias_rule_upstream_upstream_id_fkey", `ALTER TABLE alias_rule_upstream ADD CONSTRAINT alias_rule_upstream_upstream_id_fkey FOREIGN KEY (upstream_id) REFERENCES upstream_mcp(id) ON DELETE CASCADE`},
		{"filter_rule_mcp_upstream", "filter_rule_mcp_upstream_rule_id_fkey", `ALTER TABLE filter_rule_mcp_upstream ADD CONSTRAINT filter_rule_mcp_upstream_rule_id_fkey FOREIGN KEY (rule_id) REFERENCES filter_rule_mcp(id) ON DELETE CASCADE`},
		{"filter_rule_mcp_upstream", "filter_rule_mcp_upstream_upstream_id_fkey", `ALTER TABLE filter_rule_mcp_upstream ADD CONSTRAINT filter_rule_mcp_upstream_upstream_id_fkey FOREIGN KEY (upstream_id) REFERENCES upstream_mcp(id) ON DELETE CASCADE`},
		{"filter_rule_apikey", "filter_rule_apikey_api_key_id_fkey", `ALTER TABLE filter_rule_apikey ADD CONSTRAINT filter_rule_apikey_api_key_id_fkey FOREIGN KEY (api_key_id) REFERENCES api_key(id) ON DELETE CASCADE`},
		{"api_key_acl", "api_key_acl_api_key_id_fkey", `ALTER TABLE api_key_acl ADD CONSTRAINT api_key_acl_api_key_id_fkey FOREIGN KEY (api_key_id) REFERENCES api_key(id) ON DELETE CASCADE`},
		{"api_key_upstream_access", "api_key_upstream_access_api_key_id_fkey", `ALTER TABLE api_key_upstream_access ADD CONSTRAINT api_key_upstream_access_api_key_id_fkey FOREIGN KEY (api_key_id) REFERENCES api_key(id) ON DELETE CASCADE`},
		{"api_key_upstream_access", "api_key_upstream_access_upstream_id_fkey", `ALTER TABLE api_key_upstream_access ADD CONSTRAINT api_key_upstream_access_upstream_id_fkey FOREIGN KEY (upstream_id) REFERENCES upstream_mcp(id) ON DELETE CASCADE`},
		{"tool_cache", "tool_cache_upstream_id_fkey", `ALTER TABLE tool_cache ADD CONSTRAINT tool_cache_upstream_id_fkey FOREIGN KEY (upstream_id) REFERENCES upstream_mcp(id) ON DELETE CASCADE`},
		{"tool_risk_assessment", "tool_risk_assessment_upstream_id_fkey", `ALTER TABLE tool_risk_assessment ADD CONSTRAINT tool_risk_assessment_upstream_id_fkey FOREIGN KEY (upstream_id) REFERENCES upstream_mcp(id) ON DELETE CASCADE`},
		{"tool_risk_assessment", "tool_risk_assessment_provider_id_fkey", `ALTER TABLE tool_risk_assessment ADD CONSTRAINT tool_risk_assessment_provider_id_fkey FOREIGN KEY (provider_id) REFERENCES ai_provider(id) ON DELETE SET NULL`},
		{"risk_assessment_job", "risk_assessment_job_provider_id_fkey", `ALTER TABLE risk_assessment_job ADD CONSTRAINT risk_assessment_job_provider_id_fkey FOREIGN KEY (provider_id) REFERENCES ai_provider(id) ON DELETE CASCADE`},
	}
}

func quoteSQLLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func ensureCascadeConstraints(ctx context.Context, db *gorm.DB) error {
	for _, c := range cascadeConstraintDefinitions() {
		stmt := fmt.Sprintf(`
DO $$
BEGIN
	IF NOT EXISTS (
		SELECT 1 FROM pg_constraint
		WHERE conname = %s AND conrelid = %s::regclass
	) THEN
		%s;
	END IF;
END $$`, quoteSQLLiteral(c.Name), quoteSQLLiteral(c.Table), c.SQL)
		if err := db.WithContext(ctx).Exec(stmt).Error; err != nil {
			return fmt.Errorf("创建外键约束 %s 失败：%w", c.Name, err)
		}
	}
	return nil
}
