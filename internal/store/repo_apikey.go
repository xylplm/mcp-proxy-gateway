package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// APIKey 是 API Key 元数据（api_key 表）的持久层表示。
//
// 系统仅存储密钥哈希（KeyHash）与展示用前缀（KeyPrefix），永不存储明文（Req 12.3）。
// RateLimit 与 RateWindowS 为可选速率上限配置（Req 21）；ExpiresAt 为可选有效期（Req 12.6）。
type APIKey struct {
	// ID 为 API Key 唯一标识。
	ID string
	// Name 为名称，长度需在 1 至 100 个字符之间。
	Name string
	// KeyHash 为密钥的哈希字节，用于鉴权时比对。
	KeyHash []byte
	// KeyPrefix 为展示用前缀（明文的前若干字符）。
	KeyPrefix string
	// Enabled 表示该 Key 是否启用。
	Enabled bool
	// ExpiresAt 为可选有效期；nil 表示永不过期。
	ExpiresAt *time.Time
	// RateLimit 为可选速率上限；nil 表示不限流。
	RateLimit *int
	// RateWindowS 为限流计数窗口秒数；nil 表示未配置。
	RateWindowS *int
	// CreatedAt 为创建时间。
	CreatedAt time.Time
}

// APIKeyRepo 提供 API Key 元数据的类型安全增删查改。
type APIKeyRepo struct {
	pool *pgxpool.Pool
}

// NewAPIKeyRepo 构造 API Key 元数据仓储。
func NewAPIKeyRepo(pool *pgxpool.Pool) *APIKeyRepo {
	return &APIKeyRepo{pool: pool}
}

// Create 持久化一条 API Key 元数据并回填生成标识与创建时间（Req 12.1）。
func (r *APIKeyRepo) Create(ctx context.Context, key APIKey) (APIKey, error) {
	id := newUUID()
	const q = `
		INSERT INTO api_key
			(id, name, key_hash, key_prefix, enabled, expires_at, rate_limit, rate_window_s)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING created_at`
	var createdAt time.Time
	err := r.pool.QueryRow(ctx, q,
		id, key.Name, key.KeyHash, key.KeyPrefix, key.Enabled,
		nullableTime(key.ExpiresAt), nullableInt(key.RateLimit), nullableInt(key.RateWindowS),
	).Scan(&createdAt)
	if err != nil {
		return APIKey{}, classifyWrite(err, "API Key 名称已存在："+key.Name, "API Key 不存在")
	}
	key.ID = uuidString(id)
	key.CreatedAt = createdAt
	return key, nil
}

// Get 按标识查询单条 API Key；不存在返回 CodeNotFound（Req 12.7）。
func (r *APIKeyRepo) Get(ctx context.Context, id string) (APIKey, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return APIKey{}, err
	}
	const q = `
		SELECT id, name, key_hash, key_prefix, enabled, expires_at, rate_limit, rate_window_s, created_at
		FROM api_key
		WHERE id = $1`
	key, err := scanAPIKey(r.pool.QueryRow(ctx, q, uid))
	if err != nil {
		return APIKey{}, notFoundIfNoRows(err, "API Key 不存在")
	}
	return key, nil
}

// GetByHash 按密钥哈希查询单条 API Key，供鉴权中间件比对使用；不存在返回 CodeNotFound。
func (r *APIKeyRepo) GetByHash(ctx context.Context, keyHash []byte) (APIKey, error) {
	const q = `
		SELECT id, name, key_hash, key_prefix, enabled, expires_at, rate_limit, rate_window_s, created_at
		FROM api_key
		WHERE key_hash = $1`
	key, err := scanAPIKey(r.pool.QueryRow(ctx, q, keyHash))
	if err != nil {
		return APIKey{}, notFoundIfNoRows(err, "API Key 不存在")
	}
	return key, nil
}

// List 返回全部 API Key 元数据，按创建时间倒序排列；无数据返回空切片（Req 12.3、12.9）。
//
// 注意：仓储不返回明文密钥（明文从不持久化），仅返回哈希、前缀与元数据。
func (r *APIKeyRepo) List(ctx context.Context) ([]APIKey, error) {
	const q = `
		SELECT id, name, key_hash, key_prefix, enabled, expires_at, rate_limit, rate_window_s, created_at
		FROM api_key
		ORDER BY created_at DESC`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]APIKey, 0)
	for rows.Next() {
		key, err := scanAPIKey(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, key)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Update 更新 API Key 的可变元数据（名称、启停、有效期、限流配置）；不存在返回 CodeNotFound。
//
// 密钥哈希与前缀在创建后不可变更，故此处不更新。
func (r *APIKeyRepo) Update(ctx context.Context, key APIKey) (APIKey, error) {
	uid, err := parseUUID(key.ID)
	if err != nil {
		return APIKey{}, err
	}
	const q = `
		UPDATE api_key
		SET name = $2, enabled = $3, expires_at = $4, rate_limit = $5, rate_window_s = $6
		WHERE id = $1
		RETURNING key_hash, key_prefix, created_at`
	var (
		keyHash   []byte
		keyPrefix string
		createdAt time.Time
	)
	err = r.pool.QueryRow(ctx, q,
		uid, key.Name, key.Enabled,
		nullableTime(key.ExpiresAt), nullableInt(key.RateLimit), nullableInt(key.RateWindowS),
	).Scan(&keyHash, &keyPrefix, &createdAt)
	if err != nil {
		if e := notFoundIfNoRows(err, "API Key 不存在"); e != err {
			return APIKey{}, e
		}
		return APIKey{}, classifyWrite(err, "API Key 名称已存在："+key.Name, "API Key 不存在")
	}
	key.KeyHash = keyHash
	key.KeyPrefix = keyPrefix
	key.CreatedAt = createdAt
	return key, nil
}

// SetEnabled 仅更新 API Key 的启停状态（Req 12.4）；不存在返回 CodeNotFound。
func (r *APIKeyRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	const q = `UPDATE api_key SET enabled = $2 WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, uid, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NewError(domain.CodeNotFound, "API Key 不存在")
	}
	return nil
}

// Delete 删除一条 API Key；其从属屏蔽规则与 ACL 通过外键 ON DELETE CASCADE 级联清理（Req 12.2）。
//   - 标识不存在返回 CodeNotFound（Req 12.7）。
func (r *APIKeyRepo) Delete(ctx context.Context, id string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	const q = `DELETE FROM api_key WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, uid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NewError(domain.CodeNotFound, "API Key 不存在")
	}
	return nil
}

// scanAPIKey 从单行结果扫描出 APIKey。
func scanAPIKey(row pgx.Row) (APIKey, error) {
	var (
		id          pgtype.UUID
		name        string
		keyHash     []byte
		keyPrefix   string
		enabled     bool
		expiresAt   pgtype.Timestamptz
		rateLimit   pgtype.Int4
		rateWindowS pgtype.Int4
		createdAt   time.Time
	)
	if err := row.Scan(&id, &name, &keyHash, &keyPrefix, &enabled,
		&expiresAt, &rateLimit, &rateWindowS, &createdAt); err != nil {
		return APIKey{}, err
	}
	return APIKey{
		ID:          uuidString(id),
		Name:        name,
		KeyHash:     keyHash,
		KeyPrefix:   keyPrefix,
		Enabled:     enabled,
		ExpiresAt:   timePtr(expiresAt),
		RateLimit:   intPtr(rateLimit),
		RateWindowS: intPtr(rateWindowS),
		CreatedAt:   createdAt,
	}, nil
}
