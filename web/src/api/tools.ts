import request from '@/api/request'
import type { UpstreamRateLimits } from '@/api/rateLimits'

export interface ToolDef {
  originalName: string
  name: string
  description: string
  inputSchema: unknown
  upstreamId: string
  order: number
  sourceCount?: number
  schemaConflict?: boolean
}

export interface ToolSource {
  upstreamId: string
  upstreamName: string
  originalName: string
  description: string
  inputSchema: unknown
  compatible: boolean
  schemaConflict: boolean
  rateLimits?: UpstreamRateLimits
}

export interface ToolDetail {
  tool: ToolDef
  sources: ToolSource[] | null
}

export interface GatewayTool {
  name: string
  description: string
}

interface AggregatedToolsResponse {
  tools: ToolDef[] | null
  toolDetails: ToolDetail[] | null
  count: number
  gatewayTools: GatewayTool[] | null
}

interface ToolSummaryResponse {
  count: number
}

export interface AggregatedToolsResult {
  tools: ToolDef[]
  toolDetails: ToolDetail[]
  count: number
  gatewayTools: GatewayTool[]
}

export interface ToolSummary {
  count: number
}

export async function getToolSummary(): Promise<ToolSummary> {
  const res = await request.get<ToolSummaryResponse>('/tools/summary')
  return {
    count: res.data?.count ?? 0,
  }
}

export async function getAggregatedTools(): Promise<AggregatedToolsResult> {
  const res = await request.get<AggregatedToolsResponse>('/tools/aggregated')
  return {
    tools: res.data?.tools ?? [],
    toolDetails: res.data?.toolDetails ?? [],
    count: res.data?.count ?? 0,
    gatewayTools: res.data?.gatewayTools ?? [],
  }
}
