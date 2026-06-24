import type { ToolChangeSummary } from '@/api/upstreams'

export interface ToolCountSnapshot {
  count: number
  synced: boolean
  changeSummary?: ToolChangeSummary
}

export function buildToolCountSnapshot(
  count: number,
  updatedAt?: string | null,
  changeSummary?: ToolChangeSummary | null,
): ToolCountSnapshot {
  return {
    count,
    synced: updatedAt !== undefined && updatedAt !== null && updatedAt !== '',
    changeSummary: normalizeToolChangeSummary(changeSummary),
  }
}

export function toolCountLabel(snapshot?: ToolCountSnapshot): string {
  if (snapshot === undefined) return '工具 —'
  if (!snapshot.synced) return '工具 未同步'
  return `工具 ${snapshot.count}`
}

export function toolChangeSummaryLabel(snapshot?: ToolCountSnapshot): string {
  const summary = snapshot?.changeSummary
  if (summary === undefined) return '暂无同步变化'
  const parts: string[] = []
  if (summary.added > 0) parts.push(`新增 ${summary.added}`)
  if (summary.removed > 0) parts.push(`移除 ${summary.removed}`)
  if (summary.schemaChanged > 0) parts.push(`Schema 变更 ${summary.schemaChanged}`)
  return parts.length > 0 ? parts.join('，') : '本次同步无变化'
}

function normalizeToolChangeSummary(summary?: ToolChangeSummary | null): ToolChangeSummary | undefined {
  if (summary === undefined || summary === null) return undefined
  return {
    added: Math.max(0, Number(summary.added ?? 0)),
    removed: Math.max(0, Number(summary.removed ?? 0)),
    schemaChanged: Math.max(0, Number(summary.schemaChanged ?? 0)),
    syncedAt: summary.syncedAt ?? '',
  }
}
