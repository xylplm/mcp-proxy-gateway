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
// 普通业务表使用 GORM AutoMigrate 维护；call_stat 是 PostgreSQL RANGE 分区表，
// 需通过 GORM Exec 执行幂等 Raw DDL，避免被 AutoMigrate 创建成普通表。
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
		&apiKeyModel{},
		&filterRuleAPIKeyModel{},
		&apiKeyACLModel{},
		&toolCacheModel{},
		&auditLogModel{},
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
		createCallStatTableSQL,
		`CREATE TABLE IF NOT EXISTS call_stat_default PARTITION OF call_stat DEFAULT`,
		`CREATE INDEX IF NOT EXISTS idx_call_stat_called_at ON call_stat (called_at)`,
		`CREATE INDEX IF NOT EXISTS idx_call_stat_upstream_called_at ON call_stat (upstream_id, called_at)`,
		`CREATE INDEX IF NOT EXISTS idx_call_stat_apikey_called_at ON call_stat (api_key_id, called_at)`,
		`CREATE INDEX IF NOT EXISTS idx_call_stat_upstream_orig_name ON call_stat (upstream_id, original_name)`,
		`CREATE INDEX IF NOT EXISTS idx_call_stat_called_at_id_desc ON call_stat (called_at DESC, id DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_call_stat_status_called_at ON call_stat (status, called_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_call_stat_mode ON call_stat (mode)`,
		`CREATE INDEX IF NOT EXISTS idx_call_stat_source ON call_stat (source)`,
		`CREATE INDEX IF NOT EXISTS idx_alias_rule_scope ON alias_rule (scope_type, sort_order)`,
		`CREATE INDEX IF NOT EXISTS idx_alias_rule_upstream ON alias_rule_upstream (upstream_id)`,
		`CREATE INDEX IF NOT EXISTS idx_filter_rule_mcp_scope ON filter_rule_mcp (scope_type, sort_order)`,
		`CREATE INDEX IF NOT EXISTS idx_filter_rule_mcp_upstream ON filter_rule_mcp_upstream (upstream_id)`,
		`CREATE INDEX IF NOT EXISTS idx_filter_rule_apikey_apikey ON filter_rule_apikey (api_key_id)`,
		`CREATE INDEX IF NOT EXISTS idx_api_key_acl_apikey ON api_key_acl (api_key_id)`,
		`CREATE INDEX IF NOT EXISTS idx_audit_log_occurred_at ON audit_log (occurred_at DESC)`,
	}
	for _, step := range steps {
		if err := db.WithContext(ctx).Exec(step).Error; err != nil {
			return fmt.Errorf("执行 schema 初始化 SQL 失败：%w", err)
		}
	}
	return ensureCascadeConstraints(ctx, db)
}

const createCallStatTableSQL = `
CREATE TABLE IF NOT EXISTS call_stat (
	id              BIGSERIAL,
	upstream_id     UUID,
	original_name   VARCHAR(100) NOT NULL,
	exposed_name    VARCHAR(100),
	api_key_id      UUID,
	called_at       TIMESTAMPTZ NOT NULL,
	latency_ms      INTEGER NOT NULL,
	success         BOOLEAN NOT NULL,
	status          VARCHAR(32) NOT NULL DEFAULT 'success',
	request_args    JSONB,
	response_result JSONB,
	error_message   TEXT,
	failure_detail  JSONB,
	mode            VARCHAR(16) NOT NULL DEFAULT 'full',
	source          VARCHAR(16) NOT NULL DEFAULT 'api',
	PRIMARY KEY (id, called_at)
) PARTITION BY RANGE (called_at)`

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
		{"tool_cache", "tool_cache_upstream_id_fkey", `ALTER TABLE tool_cache ADD CONSTRAINT tool_cache_upstream_id_fkey FOREIGN KEY (upstream_id) REFERENCES upstream_mcp(id) ON DELETE CASCADE`},
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
