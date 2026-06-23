export type CallTraceTone = 'success' | 'warning' | 'error' | 'info' | 'neutral'

export interface CallTraceRecord {
  ID: number
  UpstreamID: string
  UpstreamName: string
  OriginalName: string
  ExposedName: string
  APIKeyID: string
  APIKeyName: string
  CalledAt: string
  LatencyMS: number
  Success: boolean
  Status?: string
  ErrorMessage: string
  Mode?: string
  Source?: string
}

export interface CallTraceMeta {
  label: string
  value: string
}

export interface CallTraceTimelineItem {
  key: string
  label: string
  timeLabel: string
  description: string
  meta: CallTraceMeta[]
  tone: CallTraceTone
}

export function buildCallTraceTimeline(record: CallTraceRecord): CallTraceTimelineItem[] {
  const status = statusOf(record)
  const source = sourceLabel(record)
  const tool = toolLabel(record)
  const upstream = upstreamLabel(record)
  const mode = modeLabel(record)
  const latency = formatLatency(record.LatencyMS)
  const calledAt = formatDateTime(record.CalledAt)
  const completedAt = formatDateTime(addMilliseconds(record.CalledAt, record.LatencyMS))

  return [
    {
      key: 'receive',
      label: '接收请求',
      timeLabel: calledAt,
      description: `${source} 发起工具调用。`,
      meta: compactMeta([
        { label: '工具', value: tool },
        { label: '模式', value: mode },
        { label: '记录', value: `#${record.ID}` },
      ]),
      tone: 'info',
    },
    {
      key: 'route',
      label: '解析路由',
      timeLabel: calledAt,
      description: routeDescription(record),
      meta: compactMeta([
        { label: '对外名', value: record.ExposedName },
        { label: '原始名', value: record.OriginalName },
        { label: '上游', value: upstream },
      ]),
      tone: 'neutral',
    },
    {
      key: 'upstream',
      label: '调用上游',
      timeLabel: latency,
      description: `请求转发到 ${upstream} 并等待结果。`,
      meta: compactMeta([
        { label: '耗时', value: latency },
        { label: '上游 ID', value: record.UpstreamID },
      ]),
      tone: latencyTone(record.LatencyMS),
    },
    {
      key: 'result',
      label: '返回结果',
      timeLabel: completedAt,
      description: resultDescription(record, status),
      meta: compactMeta([
        { label: '状态', value: statusLabel(status) },
        { label: '错误', value: record.ErrorMessage },
      ]),
      tone: statusTone(status),
    },
  ]
}

function statusOf(record: CallTraceRecord): string {
  if (record.Status !== undefined && record.Status !== '') return record.Status
  return record.Success ? 'success' : 'failed'
}

function statusLabel(status: string): string {
  switch (status) {
    case 'success':
      return '成功'
    case 'upstream_error':
      return '上游错误'
    case 'failed':
      return '调用失败'
    default:
      return '未知状态'
  }
}

function statusTone(status: string): CallTraceTone {
  switch (status) {
    case 'success':
      return 'success'
    case 'upstream_error':
      return 'warning'
    case 'failed':
      return 'error'
    default:
      return 'neutral'
  }
}

function latencyTone(latencyMS: number): CallTraceTone {
  const latency = Math.max(0, latencyMS)
  if (latency >= 3000) return 'warning'
  return 'success'
}

function sourceLabel(record: CallTraceRecord): string {
  if (record.Source === 'xiaozhi') return '小智接入'
  return record.APIKeyName || record.APIKeyID || 'API 调用'
}

function toolLabel(record: CallTraceRecord): string {
  return record.ExposedName || record.OriginalName || '未知工具'
}

function upstreamLabel(record: CallTraceRecord): string {
  return record.UpstreamName || record.UpstreamID || '未知 MCP'
}

function modeLabel(record: CallTraceRecord): string {
  return record.Mode === 'smart' ? '智能模式' : '全量模式'
}

function routeDescription(record: CallTraceRecord): string {
  const exposed = record.ExposedName.trim()
  const original = record.OriginalName.trim()
  if (exposed !== '' && original !== '' && exposed !== original) {
    return `对外工具名 ${exposed} 已映射到上游原始工具 ${original}。`
  }
  if (original !== '') return `工具名保持为 ${original}，直接按原始名路由。`
  return '网关已完成可见性检查并选择目标上游。'
}

function resultDescription(record: CallTraceRecord, status: string): string {
  switch (status) {
    case 'success':
      return '上游返回成功结果，网关已原样回传给客户端。'
    case 'upstream_error':
      return '上游返回了错误结果，网关保留原始响应并标记为上游错误。'
    case 'failed':
      return record.ErrorMessage || '网关调用链路未完成成功返回。'
    default:
      return '调用已结束，可结合入参、出参与诊断信息继续排查。'
  }
}

function compactMeta(items: CallTraceMeta[]): CallTraceMeta[] {
  return items.filter((item) => item.value.trim() !== '')
}

function formatDateTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString('zh-CN', { hour12: false })
}

function addMilliseconds(value: string, ms: number): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return new Date(date.getTime() + Math.max(0, Math.round(ms))).toISOString()
}

function formatLatency(value: number): string {
  return `${Math.max(0, Math.round(value)).toLocaleString('zh-CN')} ms`
}
