/**
 * 上游 stdio 运行时依赖：推断、展示与 preflight 辅助（纯函数，便于单测）。
 */

export type RequirementsMode = 'auto' | 'manual'

export interface RuntimeRequirements {
  mode: RequirementsMode
  tools: string[]
  note?: string
}

export interface KnownToolOption {
  name: string
  label: string
  description?: string
  packageId?: string
}

export function inferToolsFromCommand(command: string): string[] {
  const base = command.trim().split(/[/\\]/).pop()?.toLowerCase() ?? ''
  const name = base.replace(/\.(exe|cmd|bat|com)$/i, '')
  switch (name) {
    case 'npx':
      return ['node', 'npx']
    case 'npm':
      return ['node', 'npm']
    case 'node':
      return ['node']
    case 'python':
      return ['python']
    case 'python3':
      return ['python3']
    case 'uv':
      return ['uv']
    case 'uvx':
      return ['uv', 'uvx']
    case 'docker':
      return ['docker']
    default:
      return []
  }
}

export function inferToolsFromTemplateRuntimes(tags: string[] | null | undefined): string[] {
  const out: string[] = []
  const seen = new Set<string>()
  const add = (...names: string[]) => {
    for (const n of names) {
      if (seen.has(n)) continue
      seen.add(n)
      out.push(n)
    }
  }
  for (const raw of tags ?? []) {
    switch (String(raw).toLowerCase().trim()) {
      case 'node':
        add('node', 'npx')
        break
      case 'python':
        add('python3', 'python')
        break
      case 'uv':
      case 'uvx':
        add('uv', 'uvx')
        break
      case 'docker':
        add('docker')
        break
      default:
        break
    }
  }
  return out
}

export function normalizeRequirements(raw: unknown): RuntimeRequirements {
  if (raw == null || typeof raw !== 'object') {
    return { mode: 'auto', tools: [] }
  }
  const obj = raw as Record<string, unknown>
  const mode = obj.mode === 'manual' ? 'manual' : 'auto'
  const tools = Array.isArray(obj.tools)
    ? obj.tools.filter((t): t is string => typeof t === 'string').map((t) => t.trim().toLowerCase()).filter(Boolean)
    : []
  const note = typeof obj.note === 'string' ? obj.note.trim() : undefined
  return { mode, tools, note: note || undefined }
}

export function resolveEffectiveTools(
  command: string,
  req: RuntimeRequirements,
  templateRuntimes?: string[],
): { effective: string[]; suggested: string[] } {
  const suggested = uniqueTools([
    ...inferToolsFromCommand(command),
    ...inferToolsFromTemplateRuntimes(templateRuntimes),
  ])
  if (req.mode === 'manual') {
    return { effective: uniqueTools(req.tools), suggested }
  }
  if (suggested.length > 0) return { effective: suggested, suggested }
  return { effective: uniqueTools(req.tools), suggested }
}

function uniqueTools(tools: string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const t of tools) {
    const n = t.trim().toLowerCase()
    if (!n || seen.has(n)) continue
    seen.add(n)
    out.push(n)
  }
  return out
}

export function preflightReadyLabel(ready: boolean, stdioEnabled: boolean, commandAllowed: boolean): string {
  if (!stdioEnabled) return '本地 stdio 已禁用'
  if (!commandAllowed) return '启动命令不被策略允许'
  return ready ? '依赖已就绪' : '依赖未就绪'
}

export function preflightTone(
  ready: boolean,
  stdioEnabled: boolean,
  commandAllowed: boolean,
): 'success' | 'warning' | 'error' {
  if (!stdioEnabled || !commandAllowed) return 'error'
  return ready ? 'success' : 'warning'
}
