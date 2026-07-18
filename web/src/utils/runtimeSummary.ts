import type { RuntimeSummary, RuntimeToolStatus } from '@/api/runtime'

export function toolStatusLabel(tool: RuntimeToolStatus): string {
  return tool.available ? '可用' : '未检测到'
}

export function toolStatusTone(tool: RuntimeToolStatus): 'success' | 'warning' {
  return tool.available ? 'success' : 'warning'
}

export function summarizeToolHealth(summary: Pick<RuntimeSummary, 'availableCount' | 'missingCount' | 'tools'>): string {
  const fromTools = summary.tools?.length ?? 0
  const fromCounts = (summary.availableCount ?? 0) + (summary.missingCount ?? 0)
  const total = Math.max(fromTools, fromCounts)
  if (total <= 0) return '暂无探测项'
  if ((summary.missingCount ?? 0) <= 0) {
    return `已检测到 ${summary.availableCount ?? total} 个常用工具`
  }
  return `可用 ${summary.availableCount ?? 0} · 缺失 ${summary.missingCount ?? 0}`
}

export function stdioPolicyLabel(enabled: boolean): string {
  return enabled ? '本地 stdio 已启用' : '本地 stdio 已禁用'
}

export function formatAllowlist(list: string[] | null | undefined): string {
  if (list == null || list.length === 0) return '未配置（使用服务端默认）'
  return list.join('、')
}
