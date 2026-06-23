/**
 * 上游 MCP 管理 API 封装
 *
 * 设计要点（对应 design.md「路由分面」与 Req 2.1、3.1、3.4、5.6、6.4、14.x）：
 * - 全部端点挂载于管理前缀 `/api/admin/upstreams`（见 internal/httpapi/upstream.go）；
 * - 复用全局 Axios 实例（`@/api/request`），自动注入 JWT 并处理 401（Req 17.6）；
 * - 凭证（credential）以明文存储，创建/更新入参与列表/详情响应均携带明文，便于编辑回显。
 *
 * 后端真实路由（已实现）：
 *   GET    /upstreams              列出全部上游及连接状态
 *   POST   /upstreams              创建上游
 *   POST   /upstreams/test         临时测试连接并预览工具
 *   PUT    /upstreams/:id          更新上游
 *   DELETE /upstreams/:id          删除上游
 *   POST   /upstreams/:id/enable   启用上游
 *   POST   /upstreams/:id/disable  停用上游
 *   POST   /upstreams/reorder      重排序上游（body: { orderedIds: string[] }）
 *   POST   /upstreams/:id/reconnect 手动重连
 *   POST   /upstreams/:id/refresh  手动刷新工具列表
 */
import request from '@/api/request'
import type { ToolDef } from '@/api/tools'
import type { UpstreamRateLimits } from '@/api/rateLimits'

/** 上游 MCP 传输类型，与后端 domain.TransportType 对齐。 */
export type TransportType = 'stdio' | 'sse' | 'streamable-http' | 'websocket'

/** 上游连接状态，与后端 domain.ConnState 对齐。 */
export type ConnState = 'connecting' | 'available' | 'unavailable' | 'suspended'

/** 传输类型可选项（含中文显示名），供表单下拉与徽章展示复用。 */
export const TRANSPORT_OPTIONS: ReadonlyArray<{ value: TransportType; label: string }> = [
  { value: 'stdio', label: 'stdio（标准输入输出）' },
  { value: 'sse', label: 'SSE（Server-Sent Events）' },
  { value: 'streamable-http', label: 'Streamable-HTTP' },
  { value: 'websocket', label: 'WebSocket' },
]

/** 连接状态的中文显示名映射。 */
export const CONN_STATE_LABELS: Record<ConnState, string> = {
  connecting: '连接中',
  available: '可用',
  unavailable: '不可用',
  suspended: '已暂停',
}

/**
 * 上游连接参数。
 * - stdio：command（必填）、args（可选字符串数组）；
 * - sse/streamable-http/websocket：url（必填）。
 * 允许其它任意键以兼容模板预设参数。
 */
export interface ConnParams {
  command?: string
  args?: string[]
  env?: Record<string, string>
  cwd?: string
  url?: string
  headers?: Record<string, string>
  [key: string]: unknown
}

/**
 * 创建/更新上游的请求体，与后端 upstreamConfigRequest 对齐。
 * credential 为鉴权凭证明文，明文存储，入参与响应均携带。
 */
export interface UpstreamConfigRequest {
  /** 服务名称，长度 1-100。 */
  name: string
  /** 用户自定义标签，用于分组与识别。 */
  tags?: string[]
  /** 传输类型。 */
  transport: TransportType
  /** 传输相关连接参数。 */
  connParams: ConnParams
  /** 鉴权凭证明文（明文存储；编辑时直接回显并整体覆盖）。 */
  credential?: string
  /** 是否启用并参与聚合。 */
  enabled: boolean
  /** 排序顺序。 */
  sortOrder: number
  /** 是否开启工具列表自动同步。 */
  autoSync: boolean
  /** 本地按上游维度执行的调用频率与周期额度。 */
  rateLimits?: UpstreamRateLimits
}

/** 上游配置（响应内嵌；凭证明文随 credential 字段回显，便于编辑回填）。 */
export interface UpstreamConfig {
  name: string
  tags?: string[]
  transport: TransportType
  connParams: ConnParams
  /** 鉴权凭证明文，由后端原样回显，供编辑时回填。 */
  credential?: string
  enabled: boolean
  sortOrder: number
  autoSync: boolean
  rateLimits?: UpstreamRateLimits
}

/** 已持久化的上游 MCP 实例及运行期状态，与后端 domain.Upstream 对齐。 */
export interface Upstream {
  /** 上游唯一标识。 */
  id: string
  /** 上游配置。 */
  config: UpstreamConfig
  /** 当前连接状态。 */
  state: ConnState
  /** 最近一次连接失败原因（如有）。 */
  lastError?: string
  /** 创建时间（RFC3339）。 */
  createdAt: string
  /** 最近更新时间（RFC3339）。 */
  updatedAt: string
}

/** 列表响应体：{ upstreams: [...] }。 */
interface ListUpstreamsResponse {
  upstreams: Upstream[] | null
}

export interface UpstreamToolsResult {
  id: string
  tools: ToolDef[]
  count: number
  updatedAt?: string | null
}

export interface UpstreamTestResult {
  ok: boolean
  stage: string
  durationMs: number
  message?: string
  count: number
  tools: ToolDef[]
}

/**
 * 列出全部上游 MCP 及其连接状态（Req 2.3、2.8）。
 * 后端可能返回 null（空集合），此处归一化为空数组。
 */
export async function listUpstreams(): Promise<Upstream[]> {
  const res = await request.get<ListUpstreamsResponse>('/upstreams')
  return res.data?.upstreams ?? []
}

/** 创建上游 MCP（Req 2.1）。 */
export async function createUpstream(payload: UpstreamConfigRequest): Promise<Upstream> {
  const res = await request.post<Upstream>('/upstreams', payload)
  return res.data
}

/** 基于未持久化配置临时测试连接并预览工具。 */
export async function testUpstream(payload: UpstreamConfigRequest): Promise<UpstreamTestResult> {
  const res = await request.post<UpstreamTestResult>('/upstreams/test', payload, { timeout: 50000 })
  return {
    ok: res.data?.ok ?? false,
    stage: res.data?.stage ?? 'connect',
    durationMs: res.data?.durationMs ?? 0,
    message: res.data?.message ?? '',
    count: res.data?.count ?? 0,
    tools: res.data?.tools ?? [],
  }
}

/** 更新指定上游 MCP（Req 2.4）。 */
export async function updateUpstream(
  id: string,
  payload: UpstreamConfigRequest,
): Promise<Upstream> {
  const res = await request.put<Upstream>(`/upstreams/${encodeURIComponent(id)}`, payload)
  return res.data
}

/** 删除指定上游 MCP（Req 2.5）。 */
export async function deleteUpstream(id: string): Promise<void> {
  await request.delete(`/upstreams/${encodeURIComponent(id)}`)
}

/** 启用或停用指定上游 MCP（Req 3.1、3.2）。 */
export async function setUpstreamEnabled(id: string, enabled: boolean): Promise<void> {
  const action = enabled ? 'enable' : 'disable'
  await request.post(`/upstreams/${encodeURIComponent(id)}/${action}`)
}

/** 提交新的上游排序顺序（Req 3.4、3.5）。 */
export async function reorderUpstreams(orderedIds: string[]): Promise<void> {
  await request.post('/upstreams/reorder', { orderedIds })
}

/** 手动重连指定上游 MCP（Req 5.6）。 */
export async function reconnectUpstream(id: string): Promise<void> {
  await request.post(`/upstreams/${encodeURIComponent(id)}/reconnect`)
}

/** 手动刷新指定上游 MCP 的工具列表，返回刷新后的工具数量（Req 6.4、6.5）。 */
export async function refreshUpstream(id: string): Promise<number> {
  const res = await request.post<{ id: string; count: number }>(
    `/upstreams/${encodeURIComponent(id)}/refresh`,
  )
  return res.data?.count ?? 0
}

/** 读取某个上游 MCP 当前缓存的工具列表。 */
export async function listUpstreamTools(id: string): Promise<UpstreamToolsResult> {
  const res = await request.get<UpstreamToolsResult>(
    `/upstreams/${encodeURIComponent(id)}/tools`,
  )
  return {
    id: res.data?.id ?? id,
    tools: res.data?.tools ?? [],
    count: res.data?.count ?? 0,
    updatedAt: res.data?.updatedAt ?? null,
  }
}
