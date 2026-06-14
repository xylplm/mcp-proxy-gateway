import request from '@/api/request'

export type SystemLogLevel = '' | 'debug' | 'info' | 'warn' | 'error'

export interface SystemLogEntry {
  id: number
  time: string
  level: string
  message: string
  source?: string
  attrs?: Record<string, unknown>
}

export interface SystemLogQuery {
  afterId?: number
  level?: SystemLogLevel
  limit?: number
}

interface SystemLogsResponse {
  logs: SystemLogEntry[] | null
}

interface ClearSystemLogsResponse {
  deleted: number
}

export async function listSystemLogs(query: SystemLogQuery = {}): Promise<SystemLogEntry[]> {
  const params: Record<string, string> = {}
  if (query.afterId !== undefined && query.afterId > 0) {
    params.afterId = String(query.afterId)
  }
  if (query.level !== undefined && query.level !== '') {
    params.level = query.level
  }
  if (query.limit !== undefined && query.limit > 0) {
    params.limit = String(query.limit)
  }
  const res = await request.get<SystemLogsResponse>('/system-logs', { params })
  return res.data?.logs ?? []
}

export async function clearSystemLogs(): Promise<number> {
  const res = await request.delete<ClearSystemLogsResponse>('/system-logs')
  return res.data?.deleted ?? 0
}
