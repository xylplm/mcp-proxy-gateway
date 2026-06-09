package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/myGithub/mcp-proxy-gateway/internal/domain"
)

// UpstreamRow 是上游 MCP 在持久层的完整行表示。
//
// 它在领域类型 domain.Upstream 的基础上额外携带 credential_enc（加密后的鉴权凭证字节）。
// 仓储层只负责存取密文，加解密由上层加密服务（Encryption_Service）完成；
// domain.Upstream.Config.Credential（明文）不会被持久化。
// State 与 LastError 为运行期状态，由连接管理器维护，读取时为零值。
type UpstreamRow struct {
	domain.Upstream
	// CredentialEnc 为加密后的鉴权凭证字节；无凭证时为 nil。
	CredentialEnc []byte
}

// UpstreamRepo 提供上游 MCP 服务（upstream_mcp 表）的类型安全增删查改。
type UpstreamRepo struct {
	pool *pgxpool.Pool
}

// NewUpstreamRepo 构造上游 MCP 仓储。
func NewUpstreamRepo(pool *pgxpool.Pool) *UpstreamRepo {
	return &UpstreamRepo{pool: pool}
}

// Create 持久化一条上游 MCP 配置并返回含生成标识与时间戳的记录。
//   - 名称与其他上游重复（违反 UNIQUE）返回 CodeConflict（Req 2.7）。
//   - 连接参数以 JSONB 存储；credentialEnc 为加密后的凭证字节（可为 nil）。
func (r *UpstreamRepo) Create(ctx context.Context, cfg domain.UpstreamConfig, credentialEnc []byte) (*UpstreamRow, error) {
	connParams, err := marshalConnParams(cfg.ConnParams)
	if err != nil {
		return nil, err
	}

	id := newUUID()
	const q = `
		INSERT INTO upstream_mcp
			(id, name, tags, transport, conn_params, credential_enc, enabled, sort_order, auto_sync)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		RETURNING created_at, updated_at`

	var createdAt, updatedAt time.Time
	err = r.pool.QueryRow(ctx, q,
		id, cfg.Name, storeTags(cfg.Tags), string(cfg.Transport), connParams, credentialEnc,
		cfg.Enabled, cfg.SortOrder, cfg.AutoSync,
	).Scan(&createdAt, &updatedAt)
	if err != nil {
		return nil, classifyWrite(err, "上游 MCP 名称已存在："+cfg.Name, "上游 MCP 不存在")
	}

	row := &UpstreamRow{CredentialEnc: credentialEnc}
	row.ID = uuidString(id)
	row.Config = cfg
	row.Config.Credential = "" // 明文不随记录返回
	row.CreatedAt = createdAt
	row.UpdatedAt = updatedAt
	return row, nil
}

// Get 按标识查询单条上游 MCP；不存在返回 CodeNotFound（Req 2.6）。
func (r *UpstreamRepo) Get(ctx context.Context, id string) (*UpstreamRow, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	const q = `
		SELECT id, name, tags, transport, conn_params, credential_enc,
		       enabled, sort_order, auto_sync, created_at, updated_at
		FROM upstream_mcp
		WHERE id = $1`
	row, err := scanUpstream(r.pool.QueryRow(ctx, q, uid))
	if err != nil {
		return nil, notFoundIfNoRows(err, "上游 MCP 不存在")
	}
	return row, nil
}

// List 返回全部上游 MCP，按 sort_order 升序、创建时间次序排列；无数据返回空切片（Req 2.8、3.4）。
func (r *UpstreamRepo) List(ctx context.Context) ([]UpstreamRow, error) {
	const q = `
		SELECT id, name, tags, transport, conn_params, credential_enc,
		       enabled, sort_order, auto_sync, created_at, updated_at
		FROM upstream_mcp
		ORDER BY sort_order ASC, created_at ASC`
	rows, err := r.pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := make([]UpstreamRow, 0)
	for rows.Next() {
		row, err := scanUpstream(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, *row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

// Update 更新指定上游 MCP 的配置（含 credential_enc）并刷新 updated_at。
//   - 标识不存在返回 CodeNotFound（Req 2.6）。
//   - 名称与其他上游重复返回 CodeConflict（Req 2.7）。
func (r *UpstreamRepo) Update(ctx context.Context, id string, cfg domain.UpstreamConfig, credentialEnc []byte) (*UpstreamRow, error) {
	uid, err := parseUUID(id)
	if err != nil {
		return nil, err
	}
	connParams, err := marshalConnParams(cfg.ConnParams)
	if err != nil {
		return nil, err
	}

	const q = `
		UPDATE upstream_mcp
		SET name = $2, tags = $3, transport = $4, conn_params = $5,
		    credential_enc = CASE WHEN $6::bytea IS NULL THEN credential_enc ELSE $6::bytea END,
		    enabled = $7, sort_order = $8, auto_sync = $9, updated_at = now()
		WHERE id = $1
		RETURNING credential_enc, created_at, updated_at`

	var savedCredentialEnc []byte
	var createdAt, updatedAt time.Time
	err = r.pool.QueryRow(ctx, q,
		uid, cfg.Name, storeTags(cfg.Tags), string(cfg.Transport), connParams, credentialEnc,
		cfg.Enabled, cfg.SortOrder, cfg.AutoSync,
	).Scan(&savedCredentialEnc, &createdAt, &updatedAt)
	if err != nil {
		if e := notFoundIfNoRows(err, "上游 MCP 不存在"); e != err {
			return nil, e
		}
		return nil, classifyWrite(err, "上游 MCP 名称已存在："+cfg.Name, "上游 MCP 不存在")
	}

	row := &UpstreamRow{CredentialEnc: savedCredentialEnc}
	row.ID = id
	row.Config = cfg
	row.Config.Credential = ""
	row.CreatedAt = createdAt
	row.UpdatedAt = updatedAt
	return row, nil
}

// SetEnabled 仅更新启停状态（Req 3.1、3.2）；标识不存在返回 CodeNotFound。
func (r *UpstreamRepo) SetEnabled(ctx context.Context, id string, enabled bool) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	const q = `UPDATE upstream_mcp SET enabled = $2, updated_at = now() WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, uid, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NewError(domain.CodeNotFound, "上游 MCP 不存在")
	}
	return nil
}

// SetSortOrder 仅更新单个上游的排序值，供排序持久化复用（Req 3.4）。
func (r *UpstreamRepo) SetSortOrder(ctx context.Context, id string, sortOrder int) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	const q = `UPDATE upstream_mcp SET sort_order = $2, updated_at = now() WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, uid, sortOrder)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NewError(domain.CodeNotFound, "上游 MCP 不存在")
	}
	return nil
}

// Delete 删除指定上游 MCP；其从属规则、ACL 与工具缓存通过外键 ON DELETE CASCADE 级联清理（Req 2.5、6.6）。
//   - 标识不存在返回 CodeNotFound（Req 2.6）。
func (r *UpstreamRepo) Delete(ctx context.Context, id string) error {
	uid, err := parseUUID(id)
	if err != nil {
		return err
	}
	const q = `DELETE FROM upstream_mcp WHERE id = $1`
	tag, err := r.pool.Exec(ctx, q, uid)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return domain.NewError(domain.CodeNotFound, "上游 MCP 不存在")
	}
	return nil
}

// marshalConnParams 将连接参数序列化为 JSONB 字节；nil map 序列化为 "{}"。
func marshalConnParams(params map[string]any) ([]byte, error) {
	if params == nil {
		params = map[string]any{}
	}
	b, err := json.Marshal(params)
	if err != nil {
		return nil, domain.NewError(domain.CodeValidation, "连接参数序列化失败："+err.Error())
	}
	return b, nil
}

func storeTags(tags []string) []string {
	if tags == nil {
		return []string{}
	}
	return tags
}

// scanUpstream 从单行结果扫描出 UpstreamRow。
func scanUpstream(row pgx.Row) (*UpstreamRow, error) {
	var (
		id            pgtype.UUID
		name          string
		tags          []string
		transport     string
		connParams    []byte
		credentialEnc []byte
		enabled       bool
		sortOrder     int
		autoSync      bool
		createdAt     time.Time
		updatedAt     time.Time
	)
	if err := row.Scan(&id, &name, &tags, &transport, &connParams, &credentialEnc,
		&enabled, &sortOrder, &autoSync, &createdAt, &updatedAt); err != nil {
		return nil, err
	}

	var params map[string]any
	if len(connParams) > 0 {
		if err := json.Unmarshal(connParams, &params); err != nil {
			return nil, domain.NewError(domain.CodeValidation, "连接参数反序列化失败："+err.Error())
		}
	}

	out := &UpstreamRow{CredentialEnc: credentialEnc}
	out.ID = uuidString(id)
	out.Config = domain.UpstreamConfig{
		Name:       name,
		Tags:       tags,
		Transport:  domain.TransportType(transport),
		ConnParams: params,
		Enabled:    enabled,
		SortOrder:  sortOrder,
		AutoSync:   autoSync,
	}
	out.CreatedAt = createdAt
	out.UpdatedAt = updatedAt
	return out, nil
}
