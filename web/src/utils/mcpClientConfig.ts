export type MCPClientConfigKind = 'mcp-json' | 'server-entry'
export type MCPClientPresetKey = 'generic' | 'claude' | 'cursor' | 'vscode' | 'cherry-studio'

export interface MCPClientPreset {
  key: MCPClientPresetKey
  label: string
  badge: string
  desc: string
  mode: 'full' | 'smart'
  endpoint: 'sse' | 'http' | 'ws'
  auth: 'header' | 'bearer' | 'query'
  configKind: MCPClientConfigKind
  serverName: string
  hint: string
}

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

export const MCP_CLIENT_PRESETS: ReadonlyArray<MCPClientPreset> = [
  {
    key: 'generic',
    label: '通用 MCP JSON',
    badge: '通用',
    desc: '适合支持 mcpServers JSON 的客户端。',
    mode: 'full',
    endpoint: 'http',
    auth: 'bearer',
    configKind: 'mcp-json',
    serverName: defaultServerName,
    hint: '复制完整 mcpServers 配置，按客户端要求粘贴到对应配置文件或设置页。',
  },
  {
    key: 'claude',
    label: 'Claude Desktop',
    badge: '常用',
    desc: '生成 Claude 常见的 mcpServers 配置片段。',
    mode: 'full',
    endpoint: 'http',
    auth: 'bearer',
    configKind: 'mcp-json',
    serverName: 'mpg',
    hint: '适合放入 Claude Desktop 的 MCP 配置 JSON；如客户端版本只接受本地命令，可改用通用服务条目手动适配。',
  },
  {
    key: 'cursor',
    label: 'Cursor',
    badge: '编辑器',
    desc: '生成 Cursor 常用的 MCP JSON 配置。',
    mode: 'full',
    endpoint: 'http',
    auth: 'bearer',
    configKind: 'mcp-json',
    serverName: 'mpg',
    hint: '适合粘贴到 Cursor 的 MCP 配置入口；服务名可按项目或团队习惯调整。',
  },
  {
    key: 'vscode',
    label: 'VS Code',
    badge: '编辑器',
    desc: '生成 VS Code / GitHub Copilot 常用配置形态。',
    mode: 'full',
    endpoint: 'http',
    auth: 'bearer',
    configKind: 'mcp-json',
    serverName: 'mpg',
    hint: '适合复制到 VS Code 相关 MCP 配置中；如果已有 mcpServers 对象，可切换为服务条目。',
  },
  {
    key: 'cherry-studio',
    label: 'Cherry Studio',
    badge: '客户端',
    desc: '生成便于导入或手动填写的服务条目。',
    mode: 'full',
    endpoint: 'http',
    auth: 'bearer',
    configKind: 'server-entry',
    serverName: 'mpg',
    hint: '适合在客户端里新增单个 MCP 服务；需要完整文件时可切回 MCP JSON。',
  },
]

export function normalizeMCPServerName(value: string): string {
  const trimmed = value.trim()
  return trimmed === '' ? defaultServerName : trimmed
}

export function getMCPClientPreset(key: string): MCPClientPreset {
  return MCP_CLIENT_PRESETS.find((item) => item.key === key) ?? MCP_CLIENT_PRESETS[0]!
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
