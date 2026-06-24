/**
 * 规则管理 API 封装：规则独立管理，作用范围支持全部上游或指定多个上游。
 */
import request from '@/api/request'

export type RuleScopeType = 'all' | 'upstreams'
export type ToolRoutingStrategy = '' | 'smart_balance' | 'priority_fill' | 'round_robin'

/**
 * 别名/描述重写规则，与后端 domain.AliasRule 对齐。
 * targetName 与 targetDesc 至少提供其一（保存前由后端规则引擎校验）。
 */
export interface AliasRule {
  /** 规则唯一标识。 */
  id: string
  scopeType: RuleScopeType
  upstreamIds?: string[]
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
  scopeType: RuleScopeType
  upstreamIds: string[]
  pattern: string
  isRegex: boolean
  targetName: string
  targetDesc: string
  sortOrder: number
}

/**
 * MCP 级屏蔽规则，与后端 store.FilterMCPRow（内嵌 domain.FilterRule）对齐。
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
  scopeType: RuleScopeType
  upstreamIds?: string[]
}

/** 创建/更新屏蔽规则的请求体，与后端 filterRuleRequest 对齐。 */
export interface FilterRuleRequest {
  scopeType: RuleScopeType
  upstreamIds: string[]
  pattern: string
  isRegex: boolean
  enabled: boolean
  sortOrder: number
}

export interface ToolPolicyRule {
  id: string
  pattern: string
  isRegex: boolean
  enabled: boolean
  sortOrder: number
  routingStrategy?: ToolRoutingStrategy
  cacheEnabled: boolean
  cacheTtlSeconds?: number
  riskTags?: string[]
  ignoredRiskTags?: string[]
}

export interface ToolPolicyRuleRequest {
  pattern: string
  isRegex: boolean
  enabled: boolean
  sortOrder: number
  routingStrategy: ToolRoutingStrategy
  cacheEnabled: boolean
  cacheTtlSeconds: number
  riskTags: string[]
  ignoredRiskTags: string[]
}

/** 别名规则列表响应体：{ aliases: [...] }。 */
interface ListAliasesResponse {
  aliases: AliasRule[] | null
}

/** 屏蔽规则列表响应体：{ filters: [...] }。 */
interface ListFiltersResponse {
  filters: FilterRule[] | null
}

interface ListToolPoliciesResponse {
  toolPolicies: ToolPolicyRule[] | null
}

/* ===================== 别名/描述重写规则（Req 8.1） ===================== */

/** 列出全部别名规则。后端可能返回 null，归一化为空数组。 */
export async function listAliases(): Promise<AliasRule[]> {
  const res = await request.get<ListAliasesResponse>('/aliases')
  return res.data?.aliases ?? []
}

/** 创建一条别名规则（Req 8.1、8.9）。 */
export async function createAlias(payload: AliasRuleRequest): Promise<AliasRule> {
  const res = await request.post<AliasRule>('/aliases', payload)
  return res.data
}

/** 更新一条别名规则（Req 8.1、8.9）。 */
export async function updateAlias(ruleId: string, payload: AliasRuleRequest): Promise<AliasRule> {
  const res = await request.put<AliasRule>(`/aliases/${encodeURIComponent(ruleId)}`, payload)
  return res.data
}

/** 删除一条别名规则（Req 8.1）。 */
export async function deleteAlias(ruleId: string): Promise<void> {
  await request.delete(`/aliases/${encodeURIComponent(ruleId)}`)
}

/* ===================== MCP 级屏蔽规则（Req 9.1） ===================== */

/** 列出全部屏蔽规则。后端可能返回 null，归一化为空数组。 */
export async function listFilters(): Promise<FilterRule[]> {
  const res = await request.get<ListFiltersResponse>('/filters')
  return res.data?.filters ?? []
}

/** 创建一条屏蔽规则（Req 9.1、9.2、9.9）。 */
export async function createFilter(payload: FilterRuleRequest): Promise<FilterRule> {
  const res = await request.post<FilterRule>('/filters', payload)
  return res.data
}

/** 更新一条屏蔽规则（Req 9.1、9.7、9.8）。 */
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

/* ===================== 工具策略规则 ===================== */

export async function listToolPolicies(): Promise<ToolPolicyRule[]> {
  const res = await request.get<ListToolPoliciesResponse>('/tool-policies')
  return res.data?.toolPolicies ?? []
}

export async function createToolPolicy(payload: ToolPolicyRuleRequest): Promise<ToolPolicyRule> {
  const res = await request.post<ToolPolicyRule>('/tool-policies', payload)
  return res.data
}

export async function updateToolPolicy(
  ruleId: string,
  payload: ToolPolicyRuleRequest,
): Promise<ToolPolicyRule> {
  const res = await request.put<ToolPolicyRule>(`/tool-policies/${encodeURIComponent(ruleId)}`, payload)
  return res.data
}

export async function setToolPolicyEnabled(ruleId: string, enabled: boolean): Promise<void> {
  const action = enabled ? 'enable' : 'disable'
  await request.post(`/tool-policies/${encodeURIComponent(ruleId)}/${action}`)
}

export async function deleteToolPolicy(ruleId: string): Promise<void> {
  await request.delete(`/tool-policies/${encodeURIComponent(ruleId)}`)
}
