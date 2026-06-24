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
  routingAvailable?: boolean
  temporarilyDegraded?: boolean
  degradationReason?: string
  degradationUntil?: string
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

export interface ToolPlaygroundRequest {
  apiKeyId?: string
  name: string
  args: unknown
}

export interface ToolPlaygroundResponse {
  toolName: string
  apiKeyId?: string
  latencyMs: number
  success: boolean
  isError: boolean
  content?: unknown
  errorCode?: string
  error?: string
  calledAt: string
  finishedAt: string
}

export async function getToolSummary(): Promise<ToolSummary> {
  const res = await request.get<ToolSummaryResponse>('/tools/summary')
  return {
    count: res.data?.count ?? 0,
  }
}

export async function getAggregatedTools(params?: {
  apiKeyId?: string
}): Promise<AggregatedToolsResult> {
  const res = await request.get<AggregatedToolsResponse>('/tools/aggregated', {
    params: params?.apiKeyId ? { apiKeyId: params.apiKeyId } : undefined,
  })
  return {
    tools: res.data?.tools ?? [],
    toolDetails: res.data?.toolDetails ?? [],
    count: res.data?.count ?? 0,
    gatewayTools: res.data?.gatewayTools ?? [],
  }
}

export async function invokeToolPlayground(
  payload: ToolPlaygroundRequest,
): Promise<ToolPlaygroundResponse> {
  const res = await request.post<ToolPlaygroundResponse>('/tools/playground', payload)
  return res.data
}
