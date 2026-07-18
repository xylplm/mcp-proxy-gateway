/**
 * 运行环境（本地 stdio 能力探测）API。
 * 对应后端 GET /api/admin/runtime/summary。
 */
import request from '@/api/request'

export interface RuntimeToolStatus {
  name: string
  available: boolean
  path?: string
}

export interface RuntimeSummary {
  stdioEnabled: boolean
  commandAllowlist: string[]
  tools: RuntimeToolStatus[]
  availableCount: number
  missingCount: number
  dataDir?: string
  runtimeDir?: string
  pathPrefixes?: string[]
  layoutReady?: boolean
  riskNotes: string[]
}

export async function getRuntimeSummary(): Promise<RuntimeSummary> {
  const res = await request.get<RuntimeSummary>('/runtime/summary')
  return res.data
}
