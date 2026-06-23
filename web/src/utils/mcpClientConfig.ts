export type MCPClientConfigKind = 'mcp-json' | 'server-entry'

export interface MCPClientConfigInput {
  serverName: string
  clientType: string
  url: string
  headers?: Record<string, string>
}

export interface MCPServerConfig {
  type: string
  url: string
  headers?: Record<string, string>
}

const defaultServerName = 'mcp-proxy-gateway'

export function normalizeMCPServerName(value: string): string {
  const trimmed = value.trim()
  return trimmed === '' ? defaultServerName : trimmed
}

export function buildMCPServerConfig(input: MCPClientConfigInput): MCPServerConfig {
  const headers = compactHeaders(input.headers)
  return {
    type: input.clientType,
    url: input.url,
    ...(headers === undefined ? {} : { headers }),
  }
}

export function buildMCPClientConfig(input: MCPClientConfigInput, kind: MCPClientConfigKind): string {
  const server = buildMCPServerConfig(input)
  if (kind === 'server-entry') return formatJSON(server)
  return formatJSON({
    mcpServers: {
      [normalizeMCPServerName(input.serverName)]: server,
    },
  })
}

function compactHeaders(headers?: Record<string, string>): Record<string, string> | undefined {
  if (headers === undefined) return undefined
  const out: Record<string, string> = {}
  for (const [key, value] of Object.entries(headers)) {
    const k = key.trim()
    const v = value.trim()
    if (k === '' || v === '') continue
    out[k] = v
  }
  return Object.keys(out).length > 0 ? out : undefined
}

function formatJSON(value: unknown): string {
  return JSON.stringify(value, null, 2)
}
