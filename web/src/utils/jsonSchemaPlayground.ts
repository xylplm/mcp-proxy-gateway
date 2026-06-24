export type PlaygroundSchemaFieldKind = 'string' | 'number' | 'integer' | 'boolean' | 'json'

export interface PlaygroundSchemaEnumOption {
  key: string
  label: string
  value: unknown
}

export interface PlaygroundSchemaField {
  name: string
  label: string
  description: string
  kind: PlaygroundSchemaFieldKind
  required: boolean
  defaultValue?: unknown
  enumOptions: PlaygroundSchemaEnumOption[]
}

export interface PlaygroundSchemaArgsSuccess {
  ok: true
  value: Record<string, unknown>
}

export interface PlaygroundSchemaArgsFailure {
  ok: false
  error: string
}

export type PlaygroundSchemaArgsResult = PlaygroundSchemaArgsSuccess | PlaygroundSchemaArgsFailure

export function buildPlaygroundSchemaFields(schema: unknown): PlaygroundSchemaField[] {
  const root = asRecord(schema)
  if (root === null) return []
  const properties = asRecord(root.properties)
  if (properties === null) return []
  const required = new Set(Array.isArray(root.required) ? root.required.filter((item): item is string => typeof item === 'string') : [])

  return Object.entries(properties)
    .map(([name, raw]) => buildField(name, raw, required.has(name)))
    .filter((field): field is PlaygroundSchemaField => field !== null)
}

export function initialSchemaFormValues(fields: PlaygroundSchemaField[]): Record<string, string> {
  const values: Record<string, string> = {}
  for (const field of fields) {
    values[field.name] = schemaValueToFormValue(field, field.defaultValue)
  }
  return values
}

export function schemaFormDefaultArgs(fields: PlaygroundSchemaField[]): Record<string, unknown> {
  const args: Record<string, unknown> = {}
  for (const field of fields) {
    if (field.defaultValue === undefined) continue
    const value = normalizeFieldValue(field, schemaValueToFormValue(field, field.defaultValue), false)
    if (value.ok) args[field.name] = value.value
  }
  return args
}

export function schemaArgsToFormValues(
  fields: PlaygroundSchemaField[],
  args: Record<string, unknown>,
): Record<string, string> {
  const values: Record<string, string> = {}
  for (const field of fields) {
    values[field.name] = schemaValueToFormValue(field, args[field.name])
  }
  return values
}

export function schemaFormValuesToArgs(
  fields: PlaygroundSchemaField[],
  values: Record<string, string>,
): PlaygroundSchemaArgsResult {
  const args: Record<string, unknown> = {}
  for (const field of fields) {
    const normalized = normalizeFieldValue(field, values[field.name] ?? '', field.required)
    if (!normalized.ok) return normalized
    if (normalized.hasValue) args[field.name] = normalized.value
  }
  return { ok: true, value: args }
}

function buildField(name: string, raw: unknown, required: boolean): PlaygroundSchemaField | null {
  const schema = asRecord(raw)
  if (schema === null) return null
  const kind = schemaFieldKind(schema)
  const enumOptions = Array.isArray(schema.enum)
    ? schema.enum.map((value, index) => ({
        key: String(index),
        label: formatSchemaValue(value),
        value,
      }))
    : []
  return {
    name,
    label: typeof schema.title === 'string' && schema.title.trim() !== '' ? schema.title.trim() : name,
    description: typeof schema.description === 'string' ? schema.description.trim() : '',
    kind,
    required,
    defaultValue: schema.default,
    enumOptions,
  }
}

function schemaFieldKind(schema: Record<string, unknown>): PlaygroundSchemaFieldKind {
  const type = schemaType(schema.type)
  if (type === 'integer') return 'integer'
  if (type === 'number') return 'number'
  if (type === 'boolean') return 'boolean'
  if (type === 'array' || type === 'object') return 'json'
  return 'string'
}

function schemaType(value: unknown): string {
  if (typeof value === 'string') return value
  if (Array.isArray(value)) {
    const preferred = value.find((item) => typeof item === 'string' && item !== 'null')
    return typeof preferred === 'string' ? preferred : ''
  }
  return ''
}

function schemaValueToFormValue(field: PlaygroundSchemaField, value: unknown): string {
  if (value === undefined || value === null) return ''
  if (field.enumOptions.length > 0) {
    const key = field.enumOptions.find((item) => sameSchemaValue(item.value, value))?.key
    return key ?? ''
  }
  if (field.kind === 'boolean') {
    if (value === true) return 'true'
    if (value === false) return 'false'
    return ''
  }
  if (field.kind === 'json') {
    return formatJSONInput(value)
  }
  return String(value)
}

function normalizeFieldValue(
  field: PlaygroundSchemaField,
  rawValue: string,
  required: boolean,
): { ok: true; hasValue: boolean; value?: unknown } | PlaygroundSchemaArgsFailure {
  const text = rawValue.trim()
  if (text === '') {
    if (required) return { ok: false, error: `${field.label} 为必填项` }
    return { ok: true, hasValue: false }
  }

  if (field.enumOptions.length > 0) {
    const option = field.enumOptions.find((item) => item.key === rawValue)
    if (option === undefined) return { ok: false, error: `${field.label} 不是有效选项` }
    return { ok: true, hasValue: true, value: option.value }
  }

  if (field.kind === 'number' || field.kind === 'integer') {
    const value = Number(text)
    if (!Number.isFinite(value)) return { ok: false, error: `${field.label} 必须是数字` }
    if (field.kind === 'integer' && !Number.isInteger(value)) return { ok: false, error: `${field.label} 必须是整数` }
    return { ok: true, hasValue: true, value }
  }

  if (field.kind === 'boolean') {
    if (text === 'true') return { ok: true, hasValue: true, value: true }
    if (text === 'false') return { ok: true, hasValue: true, value: false }
    return { ok: false, error: `${field.label} 必须选择 true 或 false` }
  }

  if (field.kind === 'json') {
    try {
      return { ok: true, hasValue: true, value: JSON.parse(text) as unknown }
    } catch {
      return { ok: false, error: `${field.label} 不是合法 JSON` }
    }
  }

  return { ok: true, hasValue: true, value: rawValue }
}

function asRecord(value: unknown): Record<string, unknown> | null {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return null
  return value as Record<string, unknown>
}

function formatSchemaValue(value: unknown): string {
  if (typeof value === 'string') return value
  if (value === null) return 'null'
  if (value === undefined) return ''
  return formatJSONInput(value)
}

function formatJSONInput(value: unknown): string {
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

function sameSchemaValue(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b)
}
