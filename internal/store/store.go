package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"github.com/golang-migrate/migrate/v4"
	migratepgx "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgxpool"
	// 注册 pgx/v5 的 database/sql 驱动（驱动名 "pgx"），供迁移执行时打开连接使用。
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/redis/go-redis/v9"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// migrationsDir 为内嵌迁移脚本在 MigrationsFS() 中的子目录名。
const migrationsDir = "migrations"

// NewPGPool 依据 DSN 建立 PostgreSQL 连接池（pgxpool）并校验连通性。
//
// 设计为可测试与可装配：调用方传入连接串（DSN），返回连接池与 error。
//   - DSN 解析失败（格式非法）立即返回校验类错误，不发起网络连接（Req 18.1）。
//   - 解析成功后建立连接池并以 Ping 校验连通性；连通失败返回错误供启动终止。
//
// 返回的连接池由调用方在程序退出前调用 Close 释放。
func NewPGPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	if dsn == "" {
		return nil, domain.NewError(domain.CodeValidation, "PostgreSQL DSN 不能为空")
	}

	// 先解析配置以尽早暴露 DSN 格式错误（无需网络）。
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, domain.NewError(domain.CodeValidation, "解析 PostgreSQL DSN 失败："+err.Error())
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("创建 PostgreSQL 连接池失败：%w", err)
	}

	// Ping 校验连通性，确保「连接 PG 成功」后再继续后续启动流程（Req 18.1）。
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("连接 PostgreSQL 失败：%w", err)
	}
	return pool, nil
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

// RunMigrations 在连接 PostgreSQL 成功后、对外服务前执行向上迁移。
//
// 迁移脚本来源为内嵌文件系统（MigrationsFS()/migrations），通过 golang-migrate 的
// iofs source 读取；数据库驱动使用 pgx/v5。无待应用迁移视为成功。
//   - 迁移失败时通过 slog 记录错误并返回 error，供调用方据此终止启动（Req 23.3、18.1）。
//
// dsn 复用 PostgreSQL 连接串（pgx 驱动同时支持 URL 与 keyword 两种格式），
// 因此对各类 DSN 形态均可工作，无需对 scheme 做特殊改写。
func RunMigrations(dsn string, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	if dsn == "" {
		err := domain.NewError(domain.CodeValidation, "PostgreSQL DSN 不能为空")
		logger.Error("执行数据库迁移失败", "error", err)
		return err
	}

	// 以内嵌迁移脚本构造 iofs source。
	src, err := iofs.New(MigrationsFS(), migrationsDir)
	if err != nil {
		logger.Error("加载内嵌迁移脚本失败", "error", err)
		return fmt.Errorf("加载内嵌迁移脚本失败：%w", err)
	}
	defer func() { _ = src.Close() }()

	// 通过 pgx/v5 的 database/sql 驱动打开连接，供迁移驱动使用。
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		logger.Error("打开迁移数据库连接失败", "error", err)
		return fmt.Errorf("打开迁移数据库连接失败：%w", err)
	}
	defer func() { _ = db.Close() }()

	// 构造 pgx/v5 迁移数据库驱动（WithInstance 内部会校验连通性）。
	driver, err := migratepgx.WithInstance(db, &migratepgx.Config{})
	if err != nil {
		logger.Error("初始化迁移数据库驱动失败", "error", err)
		return fmt.Errorf("初始化迁移数据库驱动失败：%w", err)
	}

	m, err := migrate.NewWithInstance("iofs", src, "pgx5", driver)
	if err != nil {
		logger.Error("初始化迁移器失败", "error", err)
		return fmt.Errorf("初始化迁移器失败：%w", err)
	}

	// 执行向上迁移；无变更（已是最新版本）视为成功。
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		logger.Error("执行向上迁移失败", "error", err)
		return fmt.Errorf("执行向上迁移失败：%w", err)
	}

	logger.Info("数据库迁移执行完成")
	return nil
}
