/**
 * 调用统计排行 API 封装（任务 26.5）
 *
 * 设计要点（对应 internal/httpapi/stats.go 与 Req 16.2、16.3、16.4、16.5、17.5）：
 * - 全部端点挂载于管理前缀 `/api/admin/stats`（见 internal/httpapi/stats.go）；
 * - 复用全局 Axios 实例（`@/api/request`，baseURL=`/api/admin`），自动注入 JWT 并处理 401（Req 17.6）；
 * - 时间区间通过查询参数 start/end 传入（RFC3339）：缺省 start 表示自最早记录起，
 *   缺省 end 取当前时刻。「开始晚于结束」的非法区间由后端返回 VALIDATION（400），调用方据此提示。
 *
 * 后端真实路由（已实现，见 internal/httpapi/stats.go）：
 *   GET /stats/upstreams?start=&end=        各上游 MCP 区间调用条数（Req 16.2）
 *   GET /stats/apikeys?start=&end=          各 API Key 区间调用条数（Req 16.4）
 *   GET /stats/tools?start=&end=&limit=     工具调用排行（降序，至多 limit 条，Req 16.3）
 *
 * 响应字段名与后端 Go 结构体一致（无 json tag，故为首字母大写：ID/Count/UpstreamID/OriginalName）。
 */
import request from '@/api/request'

/**
 * 维度调用计数项（与后端 store.DimensionCount 对齐）。
 * 用于「按上游」「按 API Key」两个维度的区间调用条数统计（Req 16.2、16.4）。
 */
export interface DimensionCount {
  /** 维度标识（上游 MCP 标识或 API Key 标识）；空串表示该维度为 NULL（未知）。 */
  ID: string
  /** 该维度在时间区间内的调用条数（含成功与失败）。 */
  Count: number
}

/**
 * 工具调用排行项（与后端 store.ToolRank 对齐）。
 * 稳定标识为 (UpstreamID, OriginalName)，按 Count 降序排列（Req 16.3）。
 */
export interface ToolRank {
  /** 工具所属上游 MCP 标识。 */
  UpstreamID: string
  /** 上游原始工具名（稳定标识的一部分）。 */
  OriginalName: string
  /** 该工具在时间区间内的调用次数。 */
  Count: number
}

/** 时间区间查询参数；start/end 均为可选 RFC3339 字符串。 */
export interface TimeRangeQuery {
  /** 区间起点（RFC3339）；缺省表示自最早记录起。 */
  start?: string
  /** 区间终点（RFC3339）；缺省取当前时刻。 */
  end?: string
}

/** 维度统计响应体：{ counts: [...] }；后端可能返回 null，归一化为空数组。 */
interface CountsResponse {
  counts: DimensionCount[] | null
}

/** 工具排行响应体：{ tools: [...] }；后端可能返回 null，归一化为空数组。 */
interface ToolsResponse {
  tools: ToolRank[] | null
}

/** 构造仅含非空 start/end 的查询参数对象，避免传空串触发后端格式校验。 */
function buildRangeParams(range: TimeRangeQuery): Record<string, string> {
  const params: Record<string, string> = {}
  if (range.start !== undefined && range.start !== '') {
    params.start = range.start
  }
  if (range.end !== undefined && range.end !== '') {
    params.end = range.end
  }
  return params
}

/**
 * 查询各上游 MCP 在区间内的调用条数（Req 16.2、16.5）。
 * 无记录返回空数组；开始晚于结束时后端返回 VALIDATION（400）。
 */
export async function statsByUpstream(range: TimeRangeQuery = {}): Promise<DimensionCount[]> {
  const res = await request.get<CountsResponse>('/stats/upstreams', {
    params: buildRangeParams(range),
  })
  return res.data?.counts ?? []
}

/**
 * 查询各 API Key 在区间内的调用条数（Req 16.4、16.5）。
 * 无记录返回空数组；开始晚于结束时后端返回 VALIDATION（400）。
 */
export async function statsByAPIKey(range: TimeRangeQuery = {}): Promise<DimensionCount[]> {
  const res = await request.get<CountsResponse>('/stats/apikeys', {
    params: buildRangeParams(range),
  })
  return res.data?.counts ?? []
}

/**
 * 查询区间内按调用次数降序的工具排行（Req 16.3、16.5）。
 *
 * limit 缺省或为 0 时由后端取配置默认值，越界值由后端收敛到 [1,100]。
 * 无记录返回空数组；开始晚于结束时后端返回 VALIDATION（400）。
 */
export async function topTools(
  range: TimeRangeQuery = {},
  limit?: number,
): Promise<ToolRank[]> {
  const params = buildRangeParams(range)
  if (limit !== undefined && limit > 0) {
    params.limit = String(limit)
  }
  const res = await request.get<ToolsResponse>('/stats/tools', { params })
  return res.data?.tools ?? []
}
