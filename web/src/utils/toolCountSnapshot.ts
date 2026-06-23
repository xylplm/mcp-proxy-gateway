export interface ToolCountSnapshot {
  count: number
  synced: boolean
}

export function buildToolCountSnapshot(count: number, updatedAt?: string | null): ToolCountSnapshot {
  return {
    count,
    synced: updatedAt !== undefined && updatedAt !== null && updatedAt !== '',
  }
}

export function toolCountLabel(snapshot?: ToolCountSnapshot): string {
  if (snapshot === undefined) return '工具 —'
  if (!snapshot.synced) return '工具 未同步'
  return `工具 ${snapshot.count}`
}
