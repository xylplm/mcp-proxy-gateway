import request from '@/api/request'

export interface ToolDef {
  originalName: string
  name: string
  description: string
  inputSchema: unknown
  upstreamId: string
  order: number
}

export interface GatewayTool {
  name: string
  description: string
}

interface AggregatedToolsResponse {
  tools: ToolDef[] | null
  count: number
  gatewayTools: GatewayTool[] | null
}

export interface AggregatedToolsResult {
  tools: ToolDef[]
  count: number
  gatewayTools: GatewayTool[]
}

export async function getAggregatedTools(): Promise<AggregatedToolsResult> {
  const res = await request.get<AggregatedToolsResponse>('/tools/aggregated')
  return {
    tools: res.data?.tools ?? [],
    count: res.data?.count ?? 0,
    gatewayTools: res.data?.gatewayTools ?? [],
  }
}
