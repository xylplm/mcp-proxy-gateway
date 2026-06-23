import type { ConnState, TransportType, Upstream } from '@/api/upstreams'
import type { ToolCountSnapshot } from '@/utils/toolCountSnapshot'

export interface UpstreamDetailItem {
  label: string
  value: string
}

export interface UpstreamDetailSummary {
  endpointLabel: string
  endpointValue: string
  healthDescription: string
  syncDescription: string
  toolLabel: string
  rateLimitLabel: string
  runtimeItems: UpstreamDetailItem[]
  connectionItems: UpstreamDetailItem[]
}

export function buildUpstreamDetailSummary(
  upstream: Upstream,
  toolSnapshot?: ToolCountSnapshot,
): UpstreamDetailSummary {
  const endpoint = endpointInfo(upstream)
  const toolLabel = toolSnapshot === undefined
    ? '工具缓存未知'
    : toolSnapshot.synced
      ? `${toolSnapshot.count.toLocaleString('zh-CN')} 个工具`
      : '尚未同步'

  return {
    endpointLabel: endpoint.label,
    endpointValue: endpoint.value,
    healthDescription: healthDescription(upstream),
    syncDescription: syncDescription(upstream, toolSnapshot),
    toolLabel,
    rateLimitLabel: formatRateLimits(upstream.config.rateLimits),
    runtimeItems: compactItems([
      { label: '启用状态', value: upstream.config.enabled ? '已启用' : '已停用' },
      { label: '连接状态', value: stateLabel(upstream.state) },
      { label: '自动同步', value: upstream.config.autoSync ? '已开启' : '未开启' },
      { label: '排序位置', value: String(upstream.config.sortOrder + 1) },
      { label: '创建时间', value: formatDateTime(upstream.createdAt) },
      { label: '更新时间', value: formatDateTime(upstream.updatedAt) },
    ]),
    connectionItems: compactItems([
      { label: '传输类型', value: transportLabel(upstream.config.transport) },
      { label: endpoint.label, value: endpoint.value },
      { label: '工作目录', value: stringValue(upstream.config.connParams.cwd) },
      { label: '命令参数', value: argsValue(upstream.config.connParams.args) },
      { label: '请求头', value: recordCount(upstream.config.connParams.headers, '个请求头') },
      { label: '环境变量', value: recordCount(upstream.config.connParams.env, '个变量') },
      { label: '访问凭证', value: upstream.config.credential ? '已配置' : '未配置' },
      { label: '限流额度', value: formatRateLimits(upstream.config.rateLimits) },
    ]),
  }
}

function endpointInfo(upstream: Upstream): { label: string; value: string } {
  if (upstream.config.transport === 'stdio') {
    return {
      label: '启动命令',
      value: stringValue(upstream.config.connParams.command) || '未配置启动命令',
    }
  }
  return {
    label: '连接地址',
    value: stringValue(upstream.config.connParams.url) || '未配置连接地址',
  }
}

function healthDescription(upstream: Upstream): string {
  if (!upstream.config.enabled) return '该上游已停用，不会参与工具聚合和调用路由。'
  switch (upstream.state) {
    case 'available':
      return '连接可用，启用后会参与工具聚合和调用路由。'
    case 'connecting':
      return '正在建立连接或刷新状态，稍后会自动更新。'
    case 'suspended':
      return withLastError(upstream, '连续失败后已暂停自动重试，可手动重连恢复探测。')
    case 'unavailable':
      return withLastError(upstream, '当前连接不可用，可检查配置后重连。')
    default:
      return '当前状态未知，可刷新列表重新获取运行状态。'
  }
}

function syncDescription(upstream: Upstream, snapshot?: ToolCountSnapshot): string {
  if (snapshot === undefined) return '工具缓存摘要暂未加载，刷新列表后会自动补齐。'
  if (!snapshot.synced) {
    return upstream.config.autoSync
      ? '尚未同步到工具缓存，自动同步开启后会在后台尝试刷新。'
      : '尚未同步到工具缓存，可手动刷新工具列表。'
  }
  return `当前缓存中有 ${snapshot.count.toLocaleString('zh-CN')} 个工具。`
}

function withLastError(upstream: Upstream, fallback: string): string {
  const message = stringValue(upstream.lastError)
  if (message === '') return fallback
  return `${fallback} 最近错误：${message}`
}

function formatRateLimits(limits: Upstream['config']['rateLimits']): string {
  if (!limits?.enabled) return '未配置'
  const parts: string[] = []
  if (limits.perSecond && limits.perSecond > 0) parts.push(`每秒 ${limits.perSecond}`)
  if (limits.perMinute && limits.perMinute > 0) parts.push(`每分钟 ${limits.perMinute}`)
  if (limits.perHour && limits.perHour > 0) parts.push(`每小时 ${limits.perHour}`)
  if (limits.perDay && limits.perDay > 0) parts.push(`每日 ${limits.perDay}`)
  if (limits.perWeek && limits.perWeek > 0) parts.push(`每周 ${limits.perWeek}`)
  if (limits.perMonth && limits.perMonth > 0) parts.push(`每月 ${limits.perMonth}`)
  return parts.length > 0 ? parts.join(' / ') : '已启用，未设置上限'
}

function transportLabel(value: TransportType): string {
  switch (value) {
    case 'stdio':
      return 'stdio（标准输入输出）'
    case 'sse':
      return 'SSE（Server-Sent Events）'
    case 'streamable-http':
      return 'Streamable-HTTP'
    case 'websocket':
      return 'WebSocket'
    default:
      return value
  }
}

function stateLabel(value: ConnState): string {
  switch (value) {
    case 'connecting':
      return '连接中'
    case 'available':
      return '可用'
    case 'unavailable':
      return '不可用'
    case 'suspended':
      return '已暂停'
    default:
      return value
  }
}

function formatDateTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '未知'
  return date.toLocaleString('zh-CN', { hour12: false })
}

function recordCount(value: unknown, suffix: string): string {
  if (value === null || value === undefined || typeof value !== 'object' || Array.isArray(value)) return ''
  const count = Object.keys(value).length
  return count > 0 ? `${count} ${suffix}` : ''
}

function argsValue(value: unknown): string {
  if (!Array.isArray(value) || value.length === 0) return ''
  return value.map((item) => String(item)).join(' ')
}

function stringValue(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function compactItems(items: UpstreamDetailItem[]): UpstreamDetailItem[] {
  return items.filter((item) => item.value !== '')
}
