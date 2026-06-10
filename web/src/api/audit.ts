/**
 * 审计日志查询 API 封装（任务 26.5）
 *
 * 设计要点（对应 internal/httpapi/audit.go 与 Req 22.4、17.5）：
 * - 端点挂载于管理前缀 `/api/admin/audit`（见 internal/httpapi/audit.go）；
 * - 复用全局 Axios 实例（`@/api/request`，baseURL=`/api/admin`），自动注入 JWT 并处理 401（Req 17.6）；
 * - 按发生时间倒序分页：page 从 1 起、pageSize 默认 20、范围 1-200，越界由后端收敛（Req 22.4）。
 *
 * 后端真实路由（已实现，见 internal/httpapi/audit.go）：
 *   GET /audit?page=&pageSize=   按 occurred_at 倒序分页返回审计记录及总数
 *
 * 响应字段名与后端 Go 结构体一致：记录字段为首字母大写（store.AuditRecord 无 json tag），
 * 分页元信息为小写（gin.H 中显式指定 page/pageSize/total）。
 */
import request from '@/api/request'

/** 审计事件类型，与后端 AuditEvent* 常量对齐。 */
export type AuditEventType = 'login' | 'create' | 'update' | 'delete' | 'access_denied'

/** 审计事件类型的中文显示名映射。 */
export const AUDIT_EVENT_LABELS: Record<string, string> = {
  login: '登录',
  create: '创建',
  update: '更新',
  delete: '删除',
  access_denied: '访问被拒',
}

/**
 * 一条审计日志记录（与后端 store.AuditRecord 对齐，无 json tag 故首字母大写）。
 */
export interface AuditRecord {
  /** 审计记录自增标识。 */
  ID: number
  /** 事件类型（见 AuditEventType）。 */
  EventType: string
  /** 操作目标对象（如上游名称、规则标识）；可为空。 */
  Target: string
  /** 事件明细的结构化 JSON（如登录结果、变更字段）；可为空（null）。 */
  Detail: unknown
  /** 事件发生时间（RFC3339）。 */
  OccurredAt: string
}

/** 审计分页查询结果（与后端 queryAudit 响应体对齐）。 */
export interface AuditPage {
  /** 本页记录，按发生时间倒序；无数据时为空数组。 */
  records: AuditRecord[]
  /** 生效页码（从 1 起，已对非法入参收敛）。 */
  page: number
  /** 生效的每页条数（已收敛到 1-200）。 */
  pageSize: number
  /** 审计记录总数，供调用方计算总页数。 */
  total: number
}

export interface AuditQuery {
  page?: number
  pageSize?: number
  eventType?: string
  start?: string
  end?: string
}

/** GET /audit 的原始响应体；records 可能为 null，归一化为空数组。 */
interface AuditResponse {
  records: AuditRecord[] | null
  page: number
  pageSize: number
  total: number
}

/** 审计分页每页条数下界（与后端一致）。 */
export const AUDIT_MIN_PAGE_SIZE = 1
/** 审计分页每页条数上界（与后端一致）。 */
export const AUDIT_MAX_PAGE_SIZE = 200
/** 审计分页每页条数默认值（与后端一致）。 */
export const AUDIT_DEFAULT_PAGE_SIZE = 20

/**
 * 按发生时间倒序分页查询审计记录（Req 22.4）。
 *
 * page/pageSize 均为可选：缺省或非正由后端收敛（page→1，pageSize→默认 20），
 * pageSize 超过 200 收敛为 200。响应回显实际生效的 page/pageSize 与总数。
 */
export async function listAudit(query: AuditQuery = {}): Promise<AuditPage> {
  const params: Record<string, string> = {}
  if (query.page !== undefined && query.page > 0) {
    params.page = String(query.page)
  }
  if (query.pageSize !== undefined && query.pageSize > 0) {
    params.pageSize = String(query.pageSize)
  }
  if (query.eventType !== undefined && query.eventType !== '') {
    params.eventType = query.eventType
  }
  if (query.start !== undefined && query.start !== '') {
    params.start = query.start
  }
  if (query.end !== undefined && query.end !== '') {
    params.end = query.end
  }
  const res = await request.get<AuditResponse>('/audit', { params })
  return {
    records: res.data?.records ?? [],
    page: res.data?.page ?? 1,
    pageSize: res.data?.pageSize ?? AUDIT_DEFAULT_PAGE_SIZE,
    total: res.data?.total ?? 0,
  }
}
