export const MCP_ACCESS_HISTORY_STORAGE_KEY = 'mpg.mcpAccessHistory.v1'
export const MCP_ACCESS_HISTORY_LIMIT = 5
export const DEFAULT_MCP_HTTP_ORIGIN = 'http(s)://<your-host>:<port>'
export const DEFAULT_MCP_WS_ORIGIN = 'ws(s)://<your-host>:<port>'

export interface MCPAccessHistory {
  origins: string[]
}

export const EMPTY_MCP_ACCESS_HISTORY: MCPAccessHistory = {
  origins: [],
}

export function normalizeMCPAccessHistory(value: unknown): MCPAccessHistory {
  if (!isRecord(value)) return { ...EMPTY_MCP_ACCESS_HISTORY }
  return {
    origins: uniqueOrigins(value.origins).slice(0, MCP_ACCESS_HISTORY_LIMIT),
  }
}

export function markMCPOriginRecentlyUsed(
  history: MCPAccessHistory,
  origin: string,
  limit = MCP_ACCESS_HISTORY_LIMIT,
): MCPAccessHistory {
  const normalized = normalizeMCPOrigin(origin)
  if (normalized === '') return normalizeMCPAccessHistory(history)
  return normalizeMCPAccessHistory({
    origins: [normalized, ...history.origins.filter((item) => item !== normalized)].slice(
      0,
      Math.max(1, limit),
    ),
  })
}

export function normalizeMCPOrigin(value: string): string {
  let origin = value.trim()
  while (origin.endsWith('/')) {
    origin = origin.slice(0, -1)
  }
  return origin
}

export function mcpHTTPOrigin(value: string): string {
  const origin = normalizeMCPOrigin(value)
  if (origin === '') return DEFAULT_MCP_HTTP_ORIGIN
  if (origin.startsWith('wss://')) return `https://${origin.slice('wss://'.length)}`
  if (origin.startsWith('ws://')) return `http://${origin.slice('ws://'.length)}`
  if (origin.startsWith('ws(s)://')) return `http(s)://${origin.slice('ws(s)://'.length)}`
  return origin
}

export function mcpWSOrigin(value: string): string {
  const origin = normalizeMCPOrigin(value)
  if (origin === '') return DEFAULT_MCP_WS_ORIGIN
  if (origin.startsWith('https://')) return `wss://${origin.slice('https://'.length)}`
  if (origin.startsWith('http://')) return `ws://${origin.slice('http://'.length)}`
  if (origin.startsWith('http(s)://')) return `ws(s)://${origin.slice('http(s)://'.length)}`
  return origin
}

export function loadMCPAccessHistory(): MCPAccessHistory {
  if (typeof localStorage === 'undefined') return { ...EMPTY_MCP_ACCESS_HISTORY }
  try {
    const raw = localStorage.getItem(MCP_ACCESS_HISTORY_STORAGE_KEY)
    if (raw === null || raw === '') return { ...EMPTY_MCP_ACCESS_HISTORY }
    return normalizeMCPAccessHistory(JSON.parse(raw))
  } catch {
    return { ...EMPTY_MCP_ACCESS_HISTORY }
  }
}

export function saveMCPAccessHistory(history: MCPAccessHistory): void {
  if (typeof localStorage === 'undefined') return
  try {
    localStorage.setItem(
      MCP_ACCESS_HISTORY_STORAGE_KEY,
      JSON.stringify(normalizeMCPAccessHistory(history)),
    )
  } catch {
    // 历史地址只用于减少重复输入，存储失败时不影响对接引导。
  }
}

function uniqueOrigins(value: unknown): string[] {
  if (!Array.isArray(value)) return []
  const seen = new Set<string>()
  const out: string[] = []
  for (const item of value) {
    if (typeof item !== 'string') continue
    const origin = normalizeMCPOrigin(item)
    if (origin === '' || seen.has(origin)) continue
    seen.add(origin)
    out.push(origin)
  }
  return out
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null
}
