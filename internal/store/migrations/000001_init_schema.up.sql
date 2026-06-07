-- 初始 schema 迁移（向上）
-- 严格依据 design.md「PostgreSQL 表结构」章节定义，覆盖业务数据持久化需求 23.3，
-- 以及统计记录按时间分区以支持保留期清理的需求 16.10。
-- 说明：
--   * 删除上游 MCP / API Key 时通过 ON DELETE CASCADE 级联清理其从属规则、ACL 与缓存副本。
--   * 规则数量上限（100 条）在应用层强制，不在数据库以触发器实现。
--   * call_stat 使用 PostgreSQL 声明式分区（PARTITION BY RANGE (called_at)），
--     保留期清理通过 DROP 超期分区高效完成。

-- 上游 MCP 服务（Req 2、3、4）
CREATE TABLE upstream_mcp (
    id             UUID PRIMARY KEY,
    name           VARCHAR(100) NOT NULL UNIQUE,          -- 名称唯一（Req 2.7）
    transport      VARCHAR(32)  NOT NULL,                 -- stdio|sse|streamable-http|websocket
    conn_params    JSONB        NOT NULL,                 -- 传输相关连接参数
    credential_enc BYTEA,                                 -- 加密后的鉴权凭证（Req 19）
    enabled        BOOLEAN      NOT NULL DEFAULT true,
    sort_order     INTEGER      NOT NULL,                 -- 排序（Req 3.4）
    auto_sync      BOOLEAN      NOT NULL DEFAULT false,   -- 自动同步开关（Req 7）
    created_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now()
);

-- 别名规则（绑定上游 MCP，Req 8）
CREATE TABLE alias_rule (
    id           UUID PRIMARY KEY,
    upstream_id  UUID NOT NULL REFERENCES upstream_mcp(id) ON DELETE CASCADE,
    pattern      VARCHAR(200) NOT NULL,
    is_regex     BOOLEAN NOT NULL DEFAULT false,
    target_name  VARCHAR(100),                            -- 目标名称（与描述至少一项）
    target_desc  VARCHAR(1024),
    sort_order   INTEGER NOT NULL,                        -- 多规则按序仅应用首条（Req 8.5）
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- 屏蔽规则（绑定上游 MCP，Req 9）
CREATE TABLE filter_rule_mcp (
    id           UUID PRIMARY KEY,
    upstream_id  UUID NOT NULL REFERENCES upstream_mcp(id) ON DELETE CASCADE,
    pattern      VARCHAR(200) NOT NULL,
    is_regex     BOOLEAN NOT NULL DEFAULT false,
    enabled      BOOLEAN NOT NULL DEFAULT true,           -- 单条启停（Req 9.11）
    sort_order   INTEGER NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- API Key 元数据（Req 12）
CREATE TABLE api_key (
    id            UUID PRIMARY KEY,
    name          VARCHAR(100) NOT NULL,
    key_hash      BYTEA NOT NULL,                          -- 仅存哈希，不存明文（Req 12.3）
    key_prefix    VARCHAR(12) NOT NULL,                    -- 展示用前缀
    enabled       BOOLEAN NOT NULL DEFAULT true,
    expires_at    TIMESTAMPTZ,                             -- 可选有效期（Req 12.6）
    rate_limit    INTEGER,                                 -- 可选速率上限（Req 21）
    rate_window_s INTEGER,                                 -- 计数窗口秒数
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- API Key 屏蔽规则（绑定 API Key，Req 13）
CREATE TABLE filter_rule_apikey (
    id          UUID PRIMARY KEY,
    api_key_id  UUID NOT NULL REFERENCES api_key(id) ON DELETE CASCADE,
    pattern     VARCHAR(200) NOT NULL,
    is_regex    BOOLEAN NOT NULL DEFAULT false,
    enabled     BOOLEAN NOT NULL DEFAULT true,
    sort_order  INTEGER NOT NULL
);

-- API Key 访问控制白名单（IP/CIDR，Req 13.9）
CREATE TABLE api_key_acl (
    id          UUID PRIMARY KEY,
    api_key_id  UUID NOT NULL REFERENCES api_key(id) ON DELETE CASCADE,
    cidr        CIDR NOT NULL
);

-- 工具缓存持久化副本（Redis 为热路径，PG 为持久层，Req 6.1）
CREATE TABLE tool_cache (
    upstream_id  UUID PRIMARY KEY REFERENCES upstream_mcp(id) ON DELETE CASCADE,
    tools        JSONB NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL
);

-- 调用统计记录（Req 16），按 called_at 时间范围分区（Req 16.10）。
-- 注意：PostgreSQL 声明式分区要求主键必须包含分区键，因此主键为 (id, called_at)；
-- id 仍由 BIGSERIAL 序列自增生成，全表共享同一序列。
CREATE TABLE call_stat (
    id            BIGSERIAL,
    upstream_id   UUID,                                    -- 稳定统计维度（不随别名/排序改名而断裂）
    original_name VARCHAR(100) NOT NULL,                   -- 上游原始工具名（稳定标识）
    exposed_name  VARCHAR(100),                            -- 调用时的对外名，仅作展示
    api_key_id    UUID,
    called_at     TIMESTAMPTZ NOT NULL,                    -- 毫秒精度
    latency_ms    INTEGER NOT NULL,
    success       BOOLEAN NOT NULL,
    PRIMARY KEY (id, called_at)
) PARTITION BY RANGE (called_at);

-- 默认分区：兜底接收未落入任何时间分区的记录，保证迁移完成后即可写入。
-- 保留期清理任务可按需创建/删除具体时间分区（Req 16.10）。
CREATE TABLE call_stat_default PARTITION OF call_stat DEFAULT;

-- call_stat 关键索引（design.md 标注）。
-- 在分区父表上创建索引会自动级联到所有分区（含默认分区与未来新增分区）。
CREATE INDEX idx_call_stat_called_at            ON call_stat (called_at);
CREATE INDEX idx_call_stat_upstream_called_at   ON call_stat (upstream_id, called_at);
CREATE INDEX idx_call_stat_apikey_called_at     ON call_stat (api_key_id, called_at);
CREATE INDEX idx_call_stat_upstream_orig_name   ON call_stat (upstream_id, original_name);

-- 审计日志（Req 22）
CREATE TABLE audit_log (
    id          BIGSERIAL PRIMARY KEY,
    event_type  VARCHAR(64) NOT NULL,                      -- login|create|update|delete|access_denied
    target      VARCHAR(255),
    detail      JSONB,
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT now()         -- 倒序分页查询（Req 22.4）
);

-- 支持外键级联与按从属关系查询的索引。
CREATE INDEX idx_alias_rule_upstream         ON alias_rule (upstream_id);
CREATE INDEX idx_filter_rule_mcp_upstream    ON filter_rule_mcp (upstream_id);
CREATE INDEX idx_filter_rule_apikey_apikey   ON filter_rule_apikey (api_key_id);
CREATE INDEX idx_api_key_acl_apikey          ON api_key_acl (api_key_id);

-- 审计日志按发生时间倒序分页查询（Req 22.4）。
CREATE INDEX idx_audit_log_occurred_at       ON audit_log (occurred_at DESC);
