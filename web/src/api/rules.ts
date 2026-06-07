/**
 * 规则管理 API 封装：别名/描述重写规则 + MCP 级屏蔽规则
 *
 * 设计要点（对应 design.md「路由分面」与 Req 8.1、9.1、9.2、9.9、9.11、17.5）：
 * - 全部端点挂载于管理前缀 `/api/admin`（见 internal/httpapi/rules.go）；
 * - 复用全局 Axios 实例（`@/api/request`），自动注入 JWT 并处理 401（Req 17.6）；
 * - 规则均绑定到某个上游 MCP：列表/创建按上游分组，更新/删除/启停按规则标识。
 *
 * 后端真实路由（已实现，见 internal/httpapi/rules.go）：
 *   别名规则：
 *     GET    /upstreams/:id/aliases   列出某上游的别名规则（响应 { aliases: [...] }）
 *     POST   /upstreams/:id/aliases   创建别名规则
 *     PUT    /aliases/:ruleId         更新别名规则
 *     DELETE /aliases/:ruleId         删除别名规则
 *   MCP 级屏蔽规则：
 *     GET    /upstreams/:id/filters         列出某上游的屏蔽规则（响应 { filters: [...] }）
 *     POST   /upstreams/:id/filters         创建屏蔽规则
 *     PUT    /filters/:ruleId               更新屏蔽规则
 *     POST   /filters/:ruleId/enable        启用屏蔽规则
 *     POST   /filters/:ruleId/disable       停用屏蔽规则
 *     DELETE /filters/:ruleId               删除屏蔽规则
 *
 * 说明：
 * - 别名规则不支持独立启停（无 enabled 字段）；排序通过 sortOrder 字段经 PUT 更新实现。
 * - 屏蔽规则支持单条启停（enable/disable 端点）；排序通过 sortOrder 字段经 PUT 更新实现。
 */
import request from '@/api/request'

/**
 * 别名/描述重写规则，与后端 domain.AliasRule 对齐。
 * targetName 与 targetDesc 至少提供其一（保存前由后端规则引擎校验）。
 */
export interface AliasRule {
  /** 规则唯一标识。 */
  id: string
  /** 绑定的上游 MCP 标识。 */
  upstreamId: string
  /** 匹配模式，长度 1-200。 */
  pattern: string
  /** 是否启用正则匹配（完整匹配）。 */
  isRegex: boolean
  /** 目标名称，长度 1-100，与目标描述至少提供其一。 */
  targetName?: string
  /** 目标描述，长度不超过 1024。 */
  targetDesc?: string
  /** 规则排序顺序，多规则匹配时仅应用首条。 */
  sortOrder: number
}

/** 创建/更新别名规则的请求体，与后端 aliasRuleRequest 对齐。 */
export interface AliasRuleRequest {
  pattern: string
  isRegex: boolean
  targetName: string
  targetDesc: string
  sortOrder: number
}

/**
 * MCP 级屏蔽规则，与后端 store.FilterMCPRow（内嵌 domain.FilterRule）对齐。
 * 注意：后端 UpstreamID 字段无 json tag，序列化为大写 `UpstreamID`。
 */
export interface FilterRule {
  /** 规则唯一标识。 */
  id: string
  /** 匹配模式，长度 1-200。 */
  pattern: string
  /** 是否启用正则匹配（完整匹配）。 */
  isRegex: boolean
  /** 是否启用，支持单条启停。 */
  enabled: boolean
  /** 规则排序顺序。 */
  sortOrder: number
  /** 绑定的上游 MCP 标识（后端字段名为大写 UpstreamID）。 */
  UpstreamID?: string
}

/** 创建/更新屏蔽规则的请求体，与后端 filterRuleRequest 对齐。 */
export interface FilterRuleRequest {
  pattern: string
  isRegex: boolean
  enabled: boolean
  sortOrder: number
}

/** 别名规则列表响应体：{ aliases: [...] }。 */
interface ListAliasesResponse {
  aliases: AliasRule[] | null
}

/** 屏蔽规则列表响应体：{ filters: [...] }。 */
interface ListFiltersResponse {
  filters: FilterRule[] | null
}

/* ===================== 别名/描述重写规则（Req 8.1） ===================== */

/** 列出某上游 MCP 的全部别名规则。后端可能返回 null，归一化为空数组。 */
export async function listAliases(upstreamId: string): Promise<AliasRule[]> {
  const res = await request.get<ListAliasesResponse>(
    `/upstreams/${encodeURIComponent(upstreamId)}/aliases`,
  )
  return res.data?.aliases ?? []
}

/** 在某上游 MCP 上创建一条别名规则（Req 8.1、8.9）。 */
export async function createAlias(
  upstreamId: string,
  payload: AliasRuleRequest,
): Promise<AliasRule> {
  const res = await request.post<AliasRule>(
    `/upstreams/${encodeURIComponent(upstreamId)}/aliases`,
    payload,
  )
  return res.data
}

/** 更新一条别名规则（Req 8.1、8.9）。绑定上游不可变更。 */
export async function updateAlias(ruleId: string, payload: AliasRuleRequest): Promise<AliasRule> {
  const res = await request.put<AliasRule>(`/aliases/${encodeURIComponent(ruleId)}`, payload)
  return res.data
}

/** 删除一条别名规则（Req 8.1）。 */
export async function deleteAlias(ruleId: string): Promise<void> {
  await request.delete(`/aliases/${encodeURIComponent(ruleId)}`)
}

/* ===================== MCP 级屏蔽规则（Req 9.1） ===================== */

/** 列出某上游 MCP 的全部屏蔽规则。后端可能返回 null，归一化为空数组。 */
export async function listFilters(upstreamId: string): Promise<FilterRule[]> {
  const res = await request.get<ListFiltersResponse>(
    `/upstreams/${encodeURIComponent(upstreamId)}/filters`,
  )
  return res.data?.filters ?? []
}

/** 在某上游 MCP 上创建一条屏蔽规则（Req 9.1、9.2、9.9）。 */
export async function createFilter(
  upstreamId: string,
  payload: FilterRuleRequest,
): Promise<FilterRule> {
  const res = await request.post<FilterRule>(
    `/upstreams/${encodeURIComponent(upstreamId)}/filters`,
    payload,
  )
  return res.data
}

/** 更新一条屏蔽规则（Req 9.1、9.7、9.8）。绑定上游不可变更。 */
export async function updateFilter(
  ruleId: string,
  payload: FilterRuleRequest,
): Promise<FilterRule> {
  const res = await request.put<FilterRule>(`/filters/${encodeURIComponent(ruleId)}`, payload)
  return res.data
}

/** 启用或停用一条屏蔽规则（Req 9.11）。 */
export async function setFilterEnabled(ruleId: string, enabled: boolean): Promise<void> {
  const action = enabled ? 'enable' : 'disable'
  await request.post(`/filters/${encodeURIComponent(ruleId)}/${action}`)
}

/** 删除一条屏蔽规则（Req 9.1）。 */
export async function deleteFilter(ruleId: string): Promise<void> {
  await request.delete(`/filters/${encodeURIComponent(ruleId)}`)
}
