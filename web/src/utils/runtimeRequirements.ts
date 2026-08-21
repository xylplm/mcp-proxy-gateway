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
  inferFrom?: string[]
  templateRuntimes?: string[]
}

// 在运行时目录元数据尚未加载时，表单与预检仍应给出稳定、可解释的基础建议。
// 服务端返回目录后会显式传入完整列表，不会丢失可安装包等扩展信息。
const DEFAULT_KNOWN_TOOLS: KnownToolOption[] = [
  { name: 'node', label: 'Node.js', inferFrom: ['node', 'npx'], templateRuntimes: ['node'] },
  { name: 'npx', label: 'npx', inferFrom: ['npx'], templateRuntimes: ['node'] },
  { name: 'python3', label: 'Python 3', inferFrom: ['python', 'python3'], templateRuntimes: ['python'] },
  { name: 'uv', label: 'uv', inferFrom: ['uv', 'uvx'], templateRuntimes: ['python', 'uv'] },
  { name: 'uvx', label: 'uvx', inferFrom: ['uvx'], templateRuntimes: ['python', 'uv'] },
  { name: 'docker', label: 'Docker', inferFrom: ['docker'], templateRuntimes: ['docker'] },
]

function effectiveKnownTools(knownTools: KnownToolOption[]): KnownToolOption[] {
  return knownTools.length > 0 ? knownTools : DEFAULT_KNOWN_TOOLS
}

export function inferToolsFromCommand(command: string, knownTools: KnownToolOption[] = []): string[] {
  const tools = effectiveKnownTools(knownTools)
  const base = command.trim().split(/[/\\]/).pop()?.toLowerCase() ?? ''
  const name = base.replace(/\.(exe|cmd|bat|com)$/i, '')
	return tools
		.filter((tool) => tool.inferFrom?.some((candidate) => candidate.toLowerCase() === name))
		.map((tool) => tool.name)
}

export function inferToolsFromTemplateRuntimes(
	tags: string[] | null | undefined,
	knownTools: KnownToolOption[] = [],
): string[] {
  const tools = effectiveKnownTools(knownTools)
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
		const runtime = String(raw).toLowerCase().trim()
		for (const tool of tools) {
			if (tool.templateRuntimes?.some((candidate) => candidate.toLowerCase() === runtime)) add(tool.name)
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
	knownTools: KnownToolOption[] = [],
): { effective: string[]; suggested: string[] } {
	const suggested = uniqueTools([
		...inferToolsFromCommand(command, knownTools),
		...inferToolsFromTemplateRuntimes(templateRuntimes, knownTools),
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
