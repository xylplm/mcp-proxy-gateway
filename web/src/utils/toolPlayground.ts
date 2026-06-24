export interface PlaygroundArgsParseSuccess {
  ok: true
  value: Record<string, unknown>
}

export interface PlaygroundArgsParseFailure {
  ok: false
  error: string
}

export type PlaygroundArgsParseResult = PlaygroundArgsParseSuccess | PlaygroundArgsParseFailure

export function parsePlaygroundArgs(input: string): PlaygroundArgsParseResult {
  const text = input.trim()
  if (text === '') {
    return { ok: true, value: {} }
  }
  try {
    const value = JSON.parse(text) as unknown
    if (!isPlainObject(value)) {
      return { ok: false, error: '入参必须是 JSON 对象' }
    }
    return { ok: true, value }
  } catch {
    return { ok: false, error: '入参不是合法 JSON' }
  }
}

export function prettifyPlaygroundValue(value: unknown): string {
  if (value === undefined) return ''
  if (typeof value === 'string') {
    try {
      return JSON.stringify(JSON.parse(value), null, 2)
    } catch {
      return value
    }
  }
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

export function buildPlaygroundCallRecordQuery(toolName: string): Record<string, string> {
  const q = toolName.trim()
  return q === '' ? {} : { q }
}

function isPlainObject(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value)
}
