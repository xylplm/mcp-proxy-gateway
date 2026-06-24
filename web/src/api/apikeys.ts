/**
 * API Key 管理 API 封装
 *
 * 设计要点（对应 design.md「路由分面」与 Req 12.1、13.1、13.9、17.5、21）：
 * - 全部端点挂载于管理前缀 `/api/admin`（见 internal/httpapi/apikey.go）；
 * - 复用全局 Axios 实例（`@/api/request`），自动注入 JWT 并处理 401（Req 17.6）；
 * - 自部署场景下 API Key 明文密钥会持久化到数据库，List/Get/Create 响应均返回明文
 *   （plaintextKey），供管理台随时查看/复制；鉴权仍走哈希等值查询，明文不参与鉴权。
 *   DB 被拖库即等价明文泄露，部署时需妥善保护数据库访问权限。
 *
 * 后端真实路由（已实现，见 internal/httpapi/apikey.go）：
 *   API Key 生命周期（Req 12.1）：
 *     GET    /apikeys              列出全部 API Key 元数据（含明文）
 *     POST   /apikeys              创建 API Key（生成明文并持久化）
 *     GET    /apikeys/:id          查询单个 API Key 元数据（含明文）
 *     POST   /apikeys/:id/enable   启用 API Key
 *     POST   /apikeys/:id/disable  停用 API Key
 *     DELETE /apikeys/:id          删除 API Key（级联清理规则与 ACL）
 *   API Key 级屏蔽规则（Req 13.1）：
 *     GET    /apikeys/:id/filters            列出某 API Key 的屏蔽规则
 *     POST   /apikeys/:id/filters            创建屏蔽规则
 *     POST   /apikey-filters/:ruleId/enable  启用屏蔽规则
 *     POST   /apikey-filters/:ruleId/disable 停用屏蔽规则
 *     DELETE /apikey-filters/:ruleId         删除屏蔽规则
 *   来源白名单 ACL（Req 13.9）：
 *     GET    /apikeys/:id/acl   列出某 API Key 的来源白名单
 *     POST   /apikeys/:id/acl   新增一条来源白名单
 *     DELETE /acl/:entryId      删除一条来源白名单
 *   限流配置（Req 21）：
 *     GET    /apikeys/:id/ratelimit  读取限流配置
 *     PUT    /apikeys/:id/ratelimit  更新限流配置
 */
import request from '@/api/request'

/**
 * API Key 元数据视图（与后端 apikey.Metadata 对齐）。
 *
 * 自部署场景下允许二次查看：plaintextKey 携带完整明文密钥，供管理台查看/复制；
 * keyPrefix 仍保留用于列表紧凑展示。
 */
export interface APIKey {
  /** API Key 唯一标识。 */
  id: string
  /** 名称（长度 1-100）。 */
  name: string
  /** 完整明文密钥，供管理台二次查看/复制（自部署场景）。 */
  plaintextKey: string
  /** 展示用前缀（明文的前若干字符），用于区分不同 Key。 */
  keyPrefix: string
  /** 是否启用。 */
  enabled: boolean
  /** 可选有效期（RFC3339）；缺省表示永不过期（Req 12.6）。 */
  expiresAt?: string
  /** 可选速率上限；缺省表示不限流（Req 21）。 */
  rateLimit?: number
  /** 限流计数窗口秒数；缺省表示未配置。 */
  rateWindowS?: number
  /** 每日调用上限；缺省表示不限额。 */
  quotaPerDay?: number
  /** 每月调用上限；缺省表示不限额。 */
  quotaPerMonth?: number
  /** 创建时间（RFC3339）。 */
  createdAt: string
}

/**
 * 创建 API Key 的结果（与后端 apikey.Created 对齐）。
 *
 * 由于 APIKey 已含 plaintextKey，创建结果结构与列表项一致；保留该类型名以表达
 * "刚创建的 Key"语义。
 */
export type CreatedAPIKey = APIKey

/** 创建 API Key 的请求体（与后端 apiKeyCreateRequest 对齐）。 */
export interface CreateAPIKeyRequest {
  /** 名称，长度需在 1 至 100 个字符之间。 */
  name: string
  /** 可选有效期（RFC3339）；为空表示永不过期（Req 12.6）。 */
  expiresAt?: string | null
}

/** API Key 级屏蔽规则视图（与后端 apikey.Filter 对齐）。 */
export interface APIKeyFilter {
  /** 规则唯一标识。 */
  id: string
  /** 绑定的 API Key 标识。 */
  apiKeyId: string
  /** 匹配模式（长度 1-200）。 */
  pattern: string
  /** 是否启用正则匹配（完整匹配）。 */
  isRegex: boolean
  /** 是否启用（支持单条启停）。 */
  enabled: boolean
  /** 排序顺序，列表按其升序返回。 */
  sortOrder: number
}

/** 创建 API Key 级屏蔽规则的请求体（与后端 apiKeyFilterRequest 对齐）。 */
export interface CreateAPIKeyFilterRequest {
  /** 匹配模式，长度需在 1 至 200 个字符之间。 */
  pattern: string
  /** 是否启用正则匹配。 */
  isRegex: boolean
  /** 创建后该规则是否处于启用状态。 */
  enabled: boolean
}

/**
 * 来源白名单 ACL 记录（与后端 store.ACLEntry 对齐）。
 * 后端使用 Go 默认 JSON 序列化（无 tag），故字段为首字母大写。
 */
export interface ACLEntry {
  /** 白名单记录唯一标识。 */
  ID: string
  /** 绑定的 API Key 标识。 */
  APIKeyID: string
  /** 允许来源的 IP 或网段（CIDR，如 "10.0.0.0/8" 或 "1.2.3.4/32"）。 */
  CIDR: string
}

/** 新增来源白名单的请求体（与后端 aclEntryRequest 对齐）。 */
export interface CreateACLRequest {
  /** 允许来源的 IP 或网段。 */
  cidr: string
}

/**
 * 限流配置视图（与后端 rateLimitConfigResponse 对齐）。
 * 两字段均为可选：缺省表示该项未配置；二者须同时为正才生效（Req 21.4）。
 */
export interface RateLimitConfig {
  /** API Key 标识。 */
  id: string
  /** 窗口内的请求上限；缺省表示不限流。 */
  rateLimit?: number
  /** 限流计数窗口秒数；缺省表示未配置。 */
  rateWindowS?: number
  /** 每日调用上限；缺省表示不限额。 */
  quotaPerDay?: number
  /** 每月调用上限；缺省表示不限额。 */
  quotaPerMonth?: number
}

/**
 * 更新限流配置的请求体（与后端 rateLimitConfigRequest 对齐）。
 * 两字段均为指针语义：传 null 表示清除该项（即不限流）；二者须同时为正才生效（Req 21.4）。
 */
export interface UpdateRateLimitRequest {
  /** 窗口内的请求上限；null 表示不限流。 */
  rateLimit: number | null
  /** 限流计数窗口秒数；null 表示未配置。 */
  rateWindowS: number | null
  /** 每日调用上限；null 表示不限额。 */
  quotaPerDay: number | null
  /** 每月调用上限；null 表示不限额。 */
  quotaPerMonth: number | null
}

/** 列表响应体：{ apiKeys: [...] }；后端可能返回 null，归一化为空数组。 */
interface ListAPIKeysResponse {
  apiKeys: APIKey[] | null
}

/** 屏蔽规则列表响应体：{ filters: [...] }。 */
interface ListFiltersResponse {
  filters: APIKeyFilter[] | null
}

/** 来源白名单列表响应体：{ acl: [...] }。 */
interface ListACLResponse {
  acl: ACLEntry[] | null
}

// ── API Key 生命周期（Req 12.1） ──────────────────────────────────────────

/** 列出全部 API Key 元数据（含明文，供管理台二次查看/复制）。 */
export async function listAPIKeys(): Promise<APIKey[]> {
  const res = await request.get<ListAPIKeysResponse>('/apikeys')
  return res.data?.apiKeys ?? []
}

/**
 * 创建一个 API Key（Req 12.1）。
 * 生成新的明文密钥（plaintextKey）并持久化，响应返回该明文。由于明文同时入库，
 * 后续 List/Get 仍可经管理台二次查看；创建时仍应提示用户妥善保存。
 */
export async function createAPIKey(payload: CreateAPIKeyRequest): Promise<CreatedAPIKey> {
  const res = await request.post<CreatedAPIKey>('/apikeys', payload)
  return res.data
}

/** 查询单个 API Key 的元数据（含明文，Req 12.7）。 */
export async function getAPIKey(id: string): Promise<APIKey> {
  const res = await request.get<APIKey>(`/apikeys/${encodeURIComponent(id)}`)
  return res.data
}

/** 启用或停用某个 API Key（Req 12.4）。 */
export async function setAPIKeyEnabled(id: string, enabled: boolean): Promise<void> {
  const action = enabled ? 'enable' : 'disable'
  await request.post(`/apikeys/${encodeURIComponent(id)}/${action}`)
}

/** 删除某个 API Key 并级联清理其屏蔽规则与 ACL（Req 12.2）。 */
export async function deleteAPIKey(id: string): Promise<void> {
  await request.delete(`/apikeys/${encodeURIComponent(id)}`)
}

// ── API Key 级屏蔽规则（Req 13.1） ────────────────────────────────────────

/** 列出某 API Key 的全部屏蔽规则（Req 13.1）。 */
export async function listAPIKeyFilters(apiKeyId: string): Promise<APIKeyFilter[]> {
  const res = await request.get<ListFiltersResponse>(
    `/apikeys/${encodeURIComponent(apiKeyId)}/filters`,
  )
  return res.data?.filters ?? []
}

/** 在某 API Key 上创建一条屏蔽规则（Req 13.1、13.4）。 */
export async function createAPIKeyFilter(
  apiKeyId: string,
  payload: CreateAPIKeyFilterRequest,
): Promise<APIKeyFilter> {
  const res = await request.post<APIKeyFilter>(
    `/apikeys/${encodeURIComponent(apiKeyId)}/filters`,
    payload,
  )
  return res.data
}

/** 启用或停用一条 API Key 级屏蔽规则（Req 13.8）。 */
export async function setAPIKeyFilterEnabled(ruleId: string, enabled: boolean): Promise<void> {
  const action = enabled ? 'enable' : 'disable'
  await request.post(`/apikey-filters/${encodeURIComponent(ruleId)}/${action}`)
}

/** 删除一条 API Key 级屏蔽规则（Req 13.1）。 */
export async function deleteAPIKeyFilter(ruleId: string): Promise<void> {
  await request.delete(`/apikey-filters/${encodeURIComponent(ruleId)}`)
}

// ── 来源白名单 ACL（Req 13.9） ────────────────────────────────────────────

/** 列出某 API Key 的全部来源白名单（Req 13.9）。 */
export async function listACL(apiKeyId: string): Promise<ACLEntry[]> {
  const res = await request.get<ListACLResponse>(`/apikeys/${encodeURIComponent(apiKeyId)}/acl`)
  return res.data?.acl ?? []
}

/** 为某 API Key 新增一条来源白名单（Req 13.9）。 */
export async function createACL(apiKeyId: string, payload: CreateACLRequest): Promise<ACLEntry> {
  const res = await request.post<ACLEntry>(
    `/apikeys/${encodeURIComponent(apiKeyId)}/acl`,
    payload,
  )
  return res.data
}

/** 删除一条来源白名单记录（Req 13.9）。 */
export async function deleteACL(entryId: string): Promise<void> {
  await request.delete(`/acl/${encodeURIComponent(entryId)}`)
}

// ── 限流配置（Req 21） ────────────────────────────────────────────────────

/** 读取某 API Key 的限流配置（Req 21）。 */
export async function getRateLimit(apiKeyId: string): Promise<RateLimitConfig> {
  const res = await request.get<RateLimitConfig>(
    `/apikeys/${encodeURIComponent(apiKeyId)}/ratelimit`,
  )
  return res.data
}

/** 更新某 API Key 的限流配置（Req 21）；传 null 清除以禁用限流。 */
export async function updateRateLimit(
  apiKeyId: string,
  payload: UpdateRateLimitRequest,
): Promise<RateLimitConfig> {
  const res = await request.put<RateLimitConfig>(
    `/apikeys/${encodeURIComponent(apiKeyId)}/ratelimit`,
    payload,
  )
  return res.data
}
