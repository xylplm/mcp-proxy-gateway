<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import {
  createUpstream,
  updateUpstream,
  TRANSPORT_OPTIONS,
  type ConnParams,
  type CredentialAction,
  type TransportType,
  type Upstream,
  type UpstreamConfigRequest,
} from '@/api/upstreams'
import type { PrefillForm, Placeholder } from '@/api/templates'
import { ApiError } from '@/api/request'
import { CheckIcon, ChevronDownIcon, InfoCircleIcon } from '@/icons'

const props = defineProps<{
  open: boolean
  upstream: Upstream | null
  prefill: PrefillForm | null
  tagOptions?: string[]
  nextSortOrder?: number
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'saved'): void
}>()

type RemoteAuthMode = 'none' | 'bearer' | 'api-key' | 'custom'
type StdioAuthMode = 'none' | 'env' | 'custom'

const credentialPlaceholder = '${credential}'
const maxTagCount = 8
const maxTagLength = 32

const transportHelp: Record<
  TransportType,
  { title: string; description: string; placeholder: string }
> = {
  stdio: {
    title: '本地命令',
    description: '启动本机或容器内的 MCP Server，常见命令为 npx、uvx、python、docker。',
    placeholder: 'npx',
  },
  sse: {
    title: '远程 SSE',
    description: '连接 Server-Sent Events MCP 服务，服务地址通常以 /sse 结尾。',
    placeholder: 'https://example.com/sse',
  },
  'streamable-http': {
    title: '远程 HTTP',
    description: '当前常见的远程 MCP 接入方式，服务地址通常以 /mcp 结尾。',
    placeholder: 'https://example.com/mcp',
  },
  websocket: {
    title: 'WebSocket',
    description: '通过 ws 或 wss 长连接接入 MCP 服务。',
    placeholder: 'wss://example.com/mcp',
  },
}

const remoteAuthOptions: ReadonlyArray<{ value: RemoteAuthMode; label: string; desc: string }> = [
  { value: 'none', label: '无需认证', desc: '服务不要求 Token 或 API Key。' },
  { value: 'bearer', label: 'Bearer Token', desc: '发送 Authorization: Bearer <Token>。' },
  { value: 'api-key', label: 'API Key Header', desc: '把凭证放入指定请求头。' },
  { value: 'custom', label: '自定义请求头', desc: '在高级请求头里自行写入认证方式。' },
]

const stdioAuthOptions: ReadonlyArray<{ value: StdioAuthMode; label: string; desc: string }> = [
  { value: 'none', label: '无需认证', desc: '命令启动时不注入 Token。' },
  { value: 'env', label: '环境变量', desc: '把凭证注入为子进程环境变量。' },
  { value: 'custom', label: '自定义注入', desc: '在参数或高级环境变量中使用凭证占位。' },
]

const credentialActions: ReadonlyArray<{ value: CredentialAction; label: string; desc: string }> = [
  { value: 'keep', label: '保留当前凭证', desc: '保存其他配置，不改动已保存的 Token。' },
  { value: 'replace', label: '替换凭证', desc: '输入新的 Token 或 API Key。' },
  { value: 'clear', label: '清除凭证', desc: '删除已保存的凭证。' },
]

const isEdit = computed(() => props.upstream !== null)
const fromTemplate = computed(() => props.prefill !== null && !isEdit.value)
const currentTransport = computed(() => transportHelp[form.transport])
const isRemoteTransport = computed(() => form.transport !== 'stdio')

const transportCards = computed(() =>
  TRANSPORT_OPTIONS.map((opt) => ({
    value: opt.value,
    label: transportHelp[opt.value].title,
    subtitle: opt.label,
    description: transportHelp[opt.value].description,
  })),
)

const title = computed(() => {
  if (isEdit.value) return '编辑上游 MCP'
  if (props.prefill !== null) return `基于模板创建：${props.prefill.name}`
  return '新建上游 MCP'
})

const form = reactive<{
  name: string
  tagDraft: string
  tags: string[]
  transport: TransportType
  command: string
  args: string
  url: string
  credential: string
  credentialAction: CredentialAction
  remoteAuthMode: RemoteAuthMode
  stdioAuthMode: StdioAuthMode
  apiKeyHeader: string
  envCredentialName: string
  headersText: string
  envText: string
  cwd: string
  customParamsJson: string
  advancedOpen: boolean
  enabled: boolean
  autoSync: boolean
}>({
  name: '',
  tagDraft: '',
  tags: [],
  transport: 'stdio',
  command: '',
  args: '',
  url: '',
  credential: '',
  credentialAction: 'replace',
  remoteAuthMode: 'none',
  stdioAuthMode: 'none',
  apiKeyHeader: 'X-API-Key',
  envCredentialName: 'API_KEY',
  headersText: '',
  envText: '',
  cwd: '',
  customParamsJson: '',
  advancedOpen: false,
  enabled: true,
  autoSync: true,
})

const placeholderValues = reactive<Record<string, string>>({})
const placeholders = ref<Placeholder[]>([])
const presetParams = ref<ConnParams>({})
const submitting = ref(false)
const fieldErrors = reactive<Record<string, string>>({})
const formError = ref('')

const normalizedTagOptions = computed(() => normalizeTags(props.tagOptions ?? []))
const formTags = computed(() => normalizeTags([...form.tags, ...parseTags(form.tagDraft)]))
const availableTagOptions = computed(() => normalizedTagOptions.value.filter((tag) => !hasTag(tag)))

const selectedAuthMayUseCredential = computed(() => {
  if (fromTemplate.value) return false
  if (form.transport === 'stdio') return form.stdioAuthMode !== 'none'
  return form.remoteAuthMode !== 'none'
})

const shouldShowCredentialInput = computed(() => {
  if (fromTemplate.value) return false
  if (!isEdit.value) return selectedAuthMayUseCredential.value
  return form.credentialAction === 'replace' && selectedAuthMayUseCredential.value
})

function clearErrors(): void {
  for (const k of Object.keys(fieldErrors)) delete fieldErrors[k]
  formError.value = ''
}

function parseTags(raw: string): string[] {
  return raw
    .split(/[\n,，、;；]/)
    .map((s) => s.trim())
    .filter(Boolean)
}

function normalizeTags(tags: string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const tag of tags) {
    const value = tag.trim()
    if (value === '') continue
    const key = value.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    out.push(value)
  }
  return out
}

function addTag(tag: string): void {
  form.tags = normalizeTags([...form.tags, ...parseTags(tag)])
  form.tagDraft = ''
}

function commitTagDraft(): void {
  addTag(form.tagDraft)
}

function onTagInput(event: Event): void {
  const value = (event.target as HTMLInputElement).value
  if (/[，,、;；]/.test(value)) {
    form.tags = normalizeTags([...form.tags, ...parseTags(value)])
    form.tagDraft = ''
  }
}

function hasTag(tag: string): boolean {
  const key = tag.toLowerCase()
  return formTags.value.some((item) => item.toLowerCase() === key)
}

function removeTag(tag: string): void {
  const key = tag.toLowerCase()
  form.tags = form.tags.filter((item) => item.toLowerCase() !== key)
}

function resetManualFields(): void {
  form.command = ''
  form.args = ''
  form.url = ''
  form.credential = ''
  form.credentialAction = isEdit.value ? 'keep' : 'replace'
  form.remoteAuthMode = 'none'
  form.stdioAuthMode = 'none'
  form.apiKeyHeader = 'X-API-Key'
  form.envCredentialName = 'API_KEY'
  form.headersText = ''
  form.envText = ''
  form.cwd = ''
  form.customParamsJson = ''
  form.advancedOpen = false
}

function parseArgs(raw: string): string[] {
  return raw
    .split(/\n/)
    .map((s) => s.trim())
    .filter(Boolean)
}

function normalizeRecord(value: unknown): Record<string, string> {
  if (value === null || typeof value !== 'object' || Array.isArray(value)) return {}
  const out: Record<string, string> = {}
  for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
    if (typeof v === 'string') out[k] = v
  }
  return out
}

function formatKeyValues(record: Record<string, string>, separator: ':' | '='): string {
  return Object.entries(record)
    .map(([k, v]) => (separator === ':' ? `${k}: ${v}` : `${k}=${v}`))
    .join('\n')
}

function firstHeaderSeparator(line: string): number {
  const colon = line.indexOf(':')
  const equal = line.indexOf('=')
  if (colon < 0) return equal
  if (equal < 0) return colon
  return Math.min(colon, equal)
}

function parseKeyValues(
  raw: string,
  label: string,
  kind: 'header' | 'env',
): Record<string, string> | null {
  const out: Record<string, string> = {}
  const lines = raw.split(/\n/)
  for (let i = 0; i < lines.length; i += 1) {
    const line = lines[i].trim()
    if (line === '') continue
    const idx = kind === 'header' ? firstHeaderSeparator(line) : line.indexOf('=')
    if (idx <= 0) {
      fieldErrors[kind === 'header' ? 'headersText' : 'envText'] =
        `${label}第 ${i + 1} 行格式应为 ${kind === 'header' ? 'Header: value' : 'KEY=value'}`
      return null
    }
    const key = line.slice(0, idx).trim()
    const value = line.slice(idx + 1).trim()
    if (key === '') {
      fieldErrors[kind === 'header' ? 'headersText' : 'envText'] =
        `${label}第 ${i + 1} 行名称不能为空`
      return null
    }
    out[key] = value
  }
  return out
}

function hasCredentialReference(value: unknown): boolean {
  if (typeof value === 'string') return value.includes(credentialPlaceholder)
  if (Array.isArray(value)) return value.some((item) => hasCredentialReference(item))
  if (value !== null && typeof value === 'object') {
    return Object.values(value as Record<string, unknown>).some((item) =>
      hasCredentialReference(item),
    )
  }
  return false
}

function applyDetectedAuth(headers: Record<string, string>, env: Record<string, string>): void {
  form.remoteAuthMode = 'none'
  form.stdioAuthMode = 'none'
  form.apiKeyHeader = 'X-API-Key'
  form.envCredentialName = 'API_KEY'

  const authorizationKey = Object.keys(headers).find((key) => key.toLowerCase() === 'authorization')
  const authorization = authorizationKey ? headers[authorizationKey] : ''
  if (authorization === `Bearer ${credentialPlaceholder}`) {
    form.remoteAuthMode = 'bearer'
    if (authorizationKey) delete headers[authorizationKey]
  } else {
    const apiKey = Object.entries(headers).find(([, value]) => value === credentialPlaceholder)
    if (apiKey) {
      form.remoteAuthMode = 'api-key'
      form.apiKeyHeader = apiKey[0]
      delete headers[apiKey[0]]
    } else if (Object.keys(headers).length > 0) {
      form.remoteAuthMode = 'custom'
    }
  }

  const envKey = Object.entries(env).find(([, value]) => value === credentialPlaceholder)
  if (envKey) {
    form.stdioAuthMode = 'env'
    form.envCredentialName = envKey[0]
    delete env[envKey[0]]
  } else if (Object.keys(env).length > 0) {
    form.stdioAuthMode = 'custom'
  }
}

function customParamsFrom(params: ConnParams): string {
  const custom: Record<string, unknown> = {}
  for (const [key, value] of Object.entries(params)) {
    if (!['command', 'args', 'url', 'env', 'cwd', 'headers'].includes(key)) custom[key] = value
  }
  return Object.keys(custom).length > 0 ? JSON.stringify(custom, null, 2) : ''
}

function parseCustomParams(): ConnParams | null {
  const raw = form.customParamsJson.trim()
  if (raw === '') return {}
  try {
    const value = JSON.parse(raw) as unknown
    if (value === null || typeof value !== 'object' || Array.isArray(value)) {
      fieldErrors.customParamsJson = '额外连接参数必须是 JSON 对象'
      return null
    }
    return value as ConnParams
  } catch (err) {
    fieldErrors.customParamsJson =
      err instanceof Error ? `JSON 格式错误：${err.message}` : 'JSON 格式错误'
    return null
  }
}

function resetForm(): void {
  clearErrors()
  for (const k of Object.keys(placeholderValues)) delete placeholderValues[k]

  if (props.upstream !== null) {
    const cfg = props.upstream.config
    const headers = normalizeRecord(cfg.connParams.headers)
    const env = normalizeRecord(cfg.connParams.env)

    form.name = cfg.name
    form.tags = normalizeTags(cfg.tags ?? [])
    form.tagDraft = ''
    form.transport = cfg.transport
    resetManualFields()
    form.command = typeof cfg.connParams.command === 'string' ? cfg.connParams.command : ''
    form.args = Array.isArray(cfg.connParams.args) ? cfg.connParams.args.join('\n') : ''
    form.url = typeof cfg.connParams.url === 'string' ? cfg.connParams.url : ''
    form.credentialAction = 'keep'
    applyDetectedAuth(headers, env)
    form.headersText = formatKeyValues(headers, ':')
    form.envText = formatKeyValues(env, '=')
    form.cwd = typeof cfg.connParams.cwd === 'string' ? cfg.connParams.cwd : ''
    form.customParamsJson = customParamsFrom(cfg.connParams)
    form.advancedOpen =
      form.headersText !== '' ||
      form.envText !== '' ||
      form.cwd !== '' ||
      form.customParamsJson !== ''
    form.enabled = cfg.enabled
    form.autoSync = cfg.autoSync
    placeholders.value = []
    presetParams.value = {}
    return
  }

  form.name = props.prefill?.name ?? ''
  form.tags = []
  form.tagDraft = ''
  form.transport = props.prefill?.transport ?? 'stdio'
  resetManualFields()
  form.enabled = true
  form.autoSync = true
  placeholders.value = props.prefill?.placeholders ?? []
  presetParams.value = props.prefill?.presetParams ?? {}
  for (const ph of placeholders.value) placeholderValues[ph.name] = ''
}

watch(
  () => [props.open, props.upstream, props.prefill],
  () => {
    if (props.open) resetForm()
  },
  { immediate: true },
)

function placeholderLabel(ph: Placeholder): string {
  return ph.label?.trim() ? ph.label : ph.name
}

function isSecret(ph: Placeholder): boolean {
  return ph.rule?.kind === 'secret'
}

function validatePlaceholder(ph: Placeholder, raw: string): string {
  const val = raw.trim()
  if (val === '') return ph.required ? `缺少必填参数「${placeholderLabel(ph)}」` : ''
  const rule = ph.rule ?? { kind: 'string' as const }
  const len = [...val].length
  if (rule.minLen && len < rule.minLen)
    return `${placeholderLabel(ph)}长度不能小于 ${rule.minLen} 个字符`
  if (rule.maxLen && len > rule.maxLen)
    return `${placeholderLabel(ph)}长度不能超过 ${rule.maxLen} 个字符`
  if (rule.kind === 'url' && !isValidURL(val)) return `${placeholderLabel(ph)}不是合法 URL`
  if (rule.kind === 'int' && !/^-?\d+$/.test(val)) return `${placeholderLabel(ph)}必须为整数`
  if (rule.pattern) {
    try {
      const re = new RegExp(`^(?:${rule.pattern})$`)
      if (!re.test(val)) return `${placeholderLabel(ph)}格式不符合要求`
    } catch {
      return `${placeholderLabel(ph)}的校验规则非法`
    }
  }
  return ''
}

function isValidURL(s: string): boolean {
  try {
    const u = new URL(s)
    return u.protocol !== '' && u.host !== ''
  } catch {
    return false
  }
}

function collectRefs(value: unknown, names: Set<string>): void {
  if (typeof value === 'string') {
    const re = /\$\{([a-zA-Z0-9_]+)\}/g
    let m: RegExpExecArray | null
    while ((m = re.exec(value)) !== null) names.add(m[1])
  } else if (Array.isArray(value)) {
    value.forEach((item) => collectRefs(item, names))
  } else if (value !== null && typeof value === 'object') {
    Object.values(value as Record<string, unknown>).forEach((item) => collectRefs(item, names))
  }
}

function substitute(s: string, values: Record<string, string>, secretNames: Set<string>): string {
  return s.replace(/\$\{([a-zA-Z0-9_]+)\}/g, (match, name: string) => {
    if (!(name in values)) return match
    return secretNames.has(name) ? credentialPlaceholder : values[name]
  })
}

function injectValue(
  value: unknown,
  values: Record<string, string>,
  secretNames: Set<string>,
): unknown {
  if (typeof value === 'string') return substitute(value, values, secretNames)
  if (Array.isArray(value)) return value.map((item) => injectValue(item, values, secretNames))
  if (value !== null && typeof value === 'object') {
    const out: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      out[k] = injectValue(v, values, secretNames)
    }
    return out
  }
  return value
}

function buildTemplateConnParams(resolved: Record<string, string>): {
  connParams: ConnParams
  credential: string
} {
  const secretNames = new Set(placeholders.value.filter(isSecret).map((ph) => ph.name))
  const injected = injectValue(presetParams.value, resolved, secretNames) as ConnParams
  const referenced = new Set<string>()
  collectRefs(presetParams.value, referenced)

  for (const ph of placeholders.value) {
    if (!(ph.name in resolved) || referenced.has(ph.name)) continue
    injected[ph.name] = secretNames.has(ph.name) ? credentialPlaceholder : resolved[ph.name]
  }

  let credential = ''
  for (const ph of placeholders.value) {
    if (isSecret(ph) && ph.name in resolved) {
      credential = resolved[ph.name]
      break
    }
  }
  return { connParams: injected, credential }
}

function buildManualConnParams(): ConnParams | null {
  const custom = parseCustomParams()
  if (custom === null) return null

  if (form.transport === 'stdio') {
    const env = parseKeyValues(form.envText, '环境变量', 'env')
    if (env === null) return null

    if (form.stdioAuthMode === 'env') {
      const key = form.envCredentialName.trim()
      if (key === '') {
        fieldErrors.envCredentialName = '请输入环境变量名'
        return null
      }
      env[key] = credentialPlaceholder
    }

    const params: ConnParams = { ...custom, command: form.command.trim() }
    const args = parseArgs(form.args)
    if (args.length > 0) params.args = args
    if (Object.keys(env).length > 0) params.env = env
    if (form.cwd.trim() !== '') params.cwd = form.cwd.trim()
    return params
  }

  const headers = parseKeyValues(form.headersText, '请求头', 'header')
  if (headers === null) return null

  if (form.remoteAuthMode === 'bearer') {
    headers.Authorization = `Bearer ${credentialPlaceholder}`
  } else if (form.remoteAuthMode === 'api-key') {
    const key = form.apiKeyHeader.trim()
    if (key === '') {
      fieldErrors.apiKeyHeader = '请输入请求头名称'
      return null
    }
    headers[key] = credentialPlaceholder
  }

  const params: ConnParams = { ...custom, url: form.url.trim() }
  if (Object.keys(headers).length > 0) params.headers = headers
  return params
}

function validateName(): boolean {
  const n = [...form.name.trim()].length
  if (n < 1 || n > 100) {
    fieldErrors.name = '名称长度必须在 1 至 100 个字符之间'
    return false
  }
  return true
}

function validateTags(): boolean {
  const tags = formTags.value
  if (tags.length > maxTagCount) {
    fieldErrors.tags = `标签数量不能超过 ${maxTagCount} 个`
    return false
  }
  const tooLong = tags.find((tag) => [...tag].length > maxTagLength)
  if (tooLong !== undefined) {
    fieldErrors.tags = `标签「${tooLong}」不能超过 ${maxTagLength} 个字符`
    return false
  }
  return true
}

function validateManualBasics(): boolean {
  let ok = true
  if (form.transport === 'stdio') {
    if (form.command.trim() === '') {
      fieldErrors.command = '请输入启动命令'
      ok = false
    }
    return ok
  }

  const url = form.url.trim()
  const wantWs = form.transport === 'websocket'
  if (url === '') {
    fieldErrors.url = '请输入服务地址'
    ok = false
  } else if (!isValidURL(url)) {
    fieldErrors.url = '服务地址不是合法 URL'
    ok = false
  } else {
    const proto = url.split(':')[0].toLowerCase()
    const allowed = wantWs ? ['ws', 'wss'] : ['http', 'https']
    if (!allowed.includes(proto)) {
      fieldErrors.url = `服务地址协议必须为 ${allowed.join(' / ')}`
      ok = false
    }
  }
  return ok
}

function validateCredential(connParams: ConnParams, credential: string): boolean {
  if (fromTemplate.value) return true
  const usesCredential = hasCredentialReference(connParams)
  const credentialValue = credential.trim()

  if (!isEdit.value) {
    if (usesCredential && credentialValue === '') {
      fieldErrors.credential = '请输入 Token 或 API Key'
      return false
    }
    if (!usesCredential && credentialValue !== '') {
      fieldErrors.credential = `当前参数未使用 ${credentialPlaceholder}，请选择认证方式或移除凭证`
      return false
    }
    return true
  }

  if (form.credentialAction === 'replace') {
    if (credentialValue === '') {
      fieldErrors.credential = '请输入新凭证，或选择保留当前凭证'
      return false
    }
    if (!usesCredential) {
      fieldErrors.credentialAction = `当前参数未使用 ${credentialPlaceholder}，无法替换凭证`
      return false
    }
  }
  if (form.credentialAction === 'clear' && usesCredential) {
    fieldErrors.credentialAction =
      '当前配置仍会使用凭证，请先改为无需认证或移除高级参数里的凭证占位'
    return false
  }
  return true
}

function buildPayload(): UpstreamConfigRequest | null {
  clearErrors()
  let ok = validateName()
  ok = validateTags() && ok

  let connParams: ConnParams
  let credential = form.credential.trim()

  if (fromTemplate.value) {
    const resolved: Record<string, string> = {}
    for (const ph of placeholders.value) {
      const raw = placeholderValues[ph.name] ?? ''
      const msg = validatePlaceholder(ph, raw)
      if (msg !== '') {
        fieldErrors[ph.name] = msg
        ok = false
      } else if (raw.trim() !== '') {
        resolved[ph.name] = raw.trim()
      }
    }
    if (!ok) return null
    const built = buildTemplateConnParams(resolved)
    connParams = built.connParams
    credential = built.credential
  } else {
    ok = validateManualBasics() && ok
    const built = buildManualConnParams()
    if (built === null) ok = false
    if (!ok || built === null) return null
    connParams = built
    ok = validateCredential(connParams, credential) && ok
    if (!ok) return null
  }

  const payload: UpstreamConfigRequest = {
    name: form.name.trim(),
    tags: formTags.value,
    transport: form.transport,
    connParams,
    enabled: form.enabled,
    sortOrder:
      isEdit.value && props.upstream !== null
        ? props.upstream.config.sortOrder
        : (props.nextSortOrder ?? 0),
    autoSync: form.autoSync,
  }

  if (isEdit.value) {
    payload.credentialAction = form.credentialAction
  }
  if (credential !== '' && hasCredentialReference(connParams)) {
    payload.credential = credential
  }
  return payload
}

function mapServerField(field: string): string {
  const local = field.startsWith('connParams.') ? field.slice('connParams.'.length) : field
  const map: Record<string, string> = {
    command: 'command',
    args: 'args',
    url: 'url',
    env: 'envText',
    cwd: 'cwd',
    headers: 'headersText',
  }
  return map[local] ?? local
}

function applyServerError(err: unknown): void {
  if (err instanceof ApiError) {
    for (const [k, v] of Object.entries(err.fields)) fieldErrors[mapServerField(k)] = v
    formError.value = err.message || '保存失败，请稍后重试'
    return
  }
  formError.value = err instanceof Error ? err.message : '保存失败，请稍后重试'
}

async function handleSubmit(): Promise<void> {
  if (submitting.value) return
  const payload = buildPayload()
  if (payload === null) return

  submitting.value = true
  try {
    if (isEdit.value && props.upstream !== null) await updateUpstream(props.upstream.id, payload)
    else await createUpstream(payload)
    emit('saved')
  } catch (err) {
    applyServerError(err)
  } finally {
    submitting.value = false
  }
}

function optionCardClass(active: boolean, disabled = false): string[] {
  return [
    'relative flex min-h-[88px] rounded-lg border p-3 text-left transition',
    active
      ? 'border-brand-500 bg-brand-50 text-brand-700 dark:border-brand-400 dark:bg-brand-500/10 dark:text-brand-300'
      : 'border-gray-200 bg-white text-gray-700 hover:border-brand-200 hover:bg-gray-50 dark:border-gray-800 dark:bg-white/[0.03] dark:text-gray-300 dark:hover:border-brand-500/40 dark:hover:bg-white/[0.05]',
    disabled ? 'cursor-not-allowed opacity-70' : 'cursor-pointer',
  ]
}

function authOptionClass(active: boolean): string[] {
  return optionCardClass(active)
}

const inputClass =
  'h-11 w-full rounded-lg border border-gray-300 bg-transparent px-4 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30'
const textareaClass =
  'min-h-[108px] w-full rounded-lg border border-gray-300 bg-transparent px-4 py-2.5 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30'
const labelClass = 'mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300'
const helpClass = 'mt-1.5 text-xs leading-5 text-gray-500 dark:text-gray-400'
const errorClass = 'mt-1.5 text-xs text-error-500'
</script>

<template>
  <transition name="fade">
    <div
      v-if="open"
      class="fixed inset-0 z-[100000] flex justify-end bg-gray-900/40 backdrop-blur-[1px]"
      @click.self="emit('close')"
    >
      <div
        class="flex h-full w-full flex-col overflow-hidden border-l border-gray-200 bg-white shadow-xl sm:max-w-[720px] dark:border-gray-800 dark:bg-gray-900"
      >
        <div
          class="flex items-center justify-between gap-4 border-b border-gray-200 px-4 py-4 sm:px-6 dark:border-gray-800"
        >
          <div class="min-w-0">
            <h3 class="truncate text-lg font-semibold text-gray-800 dark:text-white/90">
              {{ title }}
            </h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              {{ currentTransport.description }}
            </p>
          </div>
          <button
            v-tooltip:bottom-end="'关闭'"
            type="button"
            class="inline-flex h-10 w-10 shrink-0 items-center justify-center rounded-lg text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-800"
            aria-label="关闭"
            @click="emit('close')"
          >
            <svg aria-hidden="true" width="20" height="20" viewBox="0 0 24 24" fill="none">
              <path
                d="M6 6l12 12M6 18L18 6"
                stroke="currentColor"
                stroke-width="1.8"
                stroke-linecap="round"
              />
            </svg>
          </button>
        </div>

        <form
          class="custom-scrollbar flex-1 overflow-y-auto px-4 py-5 sm:px-6"
          novalidate
          @submit.prevent="handleSubmit"
        >
          <div class="space-y-6">
            <section class="space-y-4">
              <div>
                <label for="up-name" :class="labelClass"
                  >名称<span class="text-error-500">*</span></label
                >
                <input
                  id="up-name"
                  v-model="form.name"
                  type="text"
                  :class="inputClass"
                  placeholder="例如 GitHub MCP、搜索服务"
                />
                <p v-if="fieldErrors.name" :class="errorClass">{{ fieldErrors.name }}</p>
              </div>

              <div>
                <label for="up-tags" :class="labelClass">标签</label>
                <div
                  class="focus-within:border-brand-300 focus-within:ring-brand-500/10 flex min-h-11 flex-wrap items-center gap-2 rounded-lg border border-gray-300 bg-transparent px-3 py-2 shadow-sm focus-within:ring-3 dark:border-gray-700"
                >
                  <button
                    v-for="tag in form.tags"
                    :key="tag"
                    type="button"
                    class="bg-brand-50 text-brand-600 hover:bg-brand-100 dark:bg-brand-500/10 dark:text-brand-300 inline-flex items-center gap-1 rounded-full px-2.5 py-1 text-xs font-medium"
                    @click="removeTag(tag)"
                  >
                    {{ tag }}
                    <span aria-hidden="true" class="text-brand-400">x</span>
                  </button>
                  <input
                    id="up-tags"
                    v-model="form.tagDraft"
                    type="text"
                    class="min-w-[120px] flex-1 border-0 bg-transparent p-0 text-sm text-gray-800 placeholder:text-gray-400 focus:ring-0 focus:outline-none dark:text-white/90 dark:placeholder:text-white/30"
                    placeholder="输入标签后回车"
                    @input="onTagInput"
                    @keydown.enter.prevent="commitTagDraft"
                    @keydown.tab="commitTagDraft"
                    @blur="commitTagDraft"
                  />
                </div>
                <p :class="helpClass">用于列表识别和归类，输入后回车加入，也可点选已有标签。</p>
                <p v-if="fieldErrors.tags" :class="errorClass">{{ fieldErrors.tags }}</p>
                <div v-if="availableTagOptions.length > 0" class="mt-2 flex flex-wrap gap-2">
                  <button
                    v-for="tag in availableTagOptions"
                    :key="tag"
                    type="button"
                    class="hover:border-brand-200 hover:bg-brand-50 hover:text-brand-600 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/10 dark:hover:text-brand-300 rounded-full border border-gray-200 px-2.5 py-1 text-xs font-medium text-gray-600 dark:border-gray-800 dark:text-gray-300"
                    @click="addTag(tag)"
                  >
                    {{ tag }}
                  </button>
                </div>
              </div>

              <div>
                <div class="mb-2 flex items-center gap-2">
                  <span :class="labelClass" class="mb-0">接入方式</span>
                  <Tooltip
                    content="不同 MCP Server 会提供不同接入方式；本地命令通常来自 npm、uvx 或 docker，远程服务通常提供 URL。"
                    placement="right"
                  >
                    <InfoCircleIcon class="h-4 w-4 text-gray-400" aria-hidden="true" />
                  </Tooltip>
                </div>
                <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <label
                    v-for="item in transportCards"
                    :key="item.value"
                    :class="optionCardClass(form.transport === item.value, fromTemplate)"
                  >
                    <input
                      v-model="form.transport"
                      class="sr-only"
                      type="radio"
                      :value="item.value"
                      :disabled="fromTemplate"
                    />
                    <span class="min-w-0 flex-1">
                      <span class="block text-sm font-semibold">{{ item.label }}</span>
                      <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400">{{
                        item.subtitle
                      }}</span>
                      <span class="mt-2 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{
                        item.description
                      }}</span>
                    </span>
                    <span
                      v-if="form.transport === item.value"
                      class="bg-brand-500 ml-3 inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full"
                    >
                      <CheckIcon aria-hidden="true" />
                    </span>
                  </label>
                </div>
                <p v-if="fromTemplate" :class="helpClass">模板已指定接入方式。</p>
              </div>
            </section>

            <section
              v-if="fromTemplate"
              class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-gray-800"
            >
              <div>
                <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">模板参数</h4>
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                  填写第三方服务要求的地址、账号或 Token。
                </p>
              </div>
              <div class="grid grid-cols-1 gap-4 md:grid-cols-2">
                <div v-for="ph in placeholders" :key="ph.name">
                  <label :for="`ph-${ph.name}`" :class="labelClass">
                    {{ placeholderLabel(ph)
                    }}<span v-if="ph.required" class="text-error-500">*</span>
                  </label>
                  <input
                    :id="`ph-${ph.name}`"
                    v-model="placeholderValues[ph.name]"
                    :type="isSecret(ph) ? 'password' : 'text'"
                    :autocomplete="isSecret(ph) ? 'new-password' : 'off'"
                    :class="inputClass"
                    :placeholder="ph.description || `请输入${placeholderLabel(ph)}`"
                  />
                  <p v-if="ph.description" :class="helpClass">{{ ph.description }}</p>
                  <p v-if="fieldErrors[ph.name]" :class="errorClass">{{ fieldErrors[ph.name] }}</p>
                </div>
              </div>
              <details
                v-if="Object.keys(presetParams).length > 0"
                class="rounded-lg bg-gray-50 p-3 dark:bg-white/[0.03]"
              >
                <summary
                  class="cursor-pointer text-sm font-medium text-gray-700 dark:text-gray-300"
                >
                  查看模板预设
                </summary>
                <pre
                  class="mt-3 overflow-x-auto text-xs leading-5 text-gray-600 dark:text-gray-300"
                  >{{ JSON.stringify(presetParams, null, 2) }}</pre
                >
              </details>
            </section>

            <template v-else>
              <section class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-gray-800">
                <div>
                  <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">连接信息</h4>
                  <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                    先填写服务能连上的最小配置，其他参数可在高级区补充。
                  </p>
                </div>

                <template v-if="form.transport === 'stdio'">
                  <div class="space-y-4">
                    <div>
                      <label for="up-command" :class="labelClass"
                        >启动命令<span class="text-error-500">*</span></label
                      >
                      <input
                        id="up-command"
                        v-model="form.command"
                        type="text"
                        :class="inputClass"
                        :placeholder="currentTransport.placeholder"
                      />
                      <p v-if="fieldErrors.command" :class="errorClass">
                        {{ fieldErrors.command }}
                      </p>
                    </div>
                    <div>
                      <label for="up-args" :class="labelClass">命令参数</label>
                      <textarea
                        id="up-args"
                        v-model="form.args"
                        rows="4"
                        :class="textareaClass"
                        placeholder="-y&#10;@modelcontextprotocol/server-filesystem&#10;D:\\data"
                      ></textarea>
                      <p :class="helpClass">
                        每行一个参数。需要在参数中放凭证时，选择自定义注入并写入
                        ${credential}；它会取认证区的 Token / API Key。
                      </p>
                      <p v-if="fieldErrors.args" :class="errorClass">{{ fieldErrors.args }}</p>
                    </div>
                  </div>
                </template>

                <template v-else>
                  <div>
                    <label for="up-url" :class="labelClass"
                      >服务地址<span class="text-error-500">*</span></label
                    >
                    <input
                      id="up-url"
                      v-model="form.url"
                      type="text"
                      :class="inputClass"
                      :placeholder="currentTransport.placeholder"
                    />
                    <p :class="helpClass">
                      {{
                        form.transport === 'websocket'
                          ? 'WebSocket 服务使用 ws:// 或 wss://。'
                          : 'HTTP/SSE 服务使用 http:// 或 https://。'
                      }}
                    </p>
                    <p v-if="fieldErrors.url" :class="errorClass">{{ fieldErrors.url }}</p>
                  </div>
                </template>
              </section>

              <section class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-gray-800">
                <div>
                  <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">认证</h4>
                  <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                    选择 Token / API Key
                    放到请求头、环境变量或自定义参数中；保存后列表不会回显明文。
                  </p>
                  <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                    ${credential} 不需要单独配置：新建或替换时，它等于这里填写的 Token / API
                    Key；编辑并选择保留时，它等于已保存的凭证。
                  </p>
                </div>

                <div v-if="isRemoteTransport" class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <label
                    v-for="item in remoteAuthOptions"
                    :key="item.value"
                    :class="authOptionClass(form.remoteAuthMode === item.value)"
                  >
                    <input
                      v-model="form.remoteAuthMode"
                      class="sr-only"
                      type="radio"
                      :value="item.value"
                    />
                    <span class="min-w-0 flex-1">
                      <span class="block text-sm font-medium">{{ item.label }}</span>
                      <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{
                        item.desc
                      }}</span>
                    </span>
                  </label>
                </div>

                <div v-else class="grid grid-cols-1 gap-3 sm:grid-cols-3">
                  <label
                    v-for="item in stdioAuthOptions"
                    :key="item.value"
                    :class="authOptionClass(form.stdioAuthMode === item.value)"
                  >
                    <input
                      v-model="form.stdioAuthMode"
                      class="sr-only"
                      type="radio"
                      :value="item.value"
                    />
                    <span class="min-w-0 flex-1">
                      <span class="block text-sm font-medium">{{ item.label }}</span>
                      <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{
                        item.desc
                      }}</span>
                    </span>
                  </label>
                </div>

                <div v-if="form.remoteAuthMode === 'api-key' && isRemoteTransport" class="max-w-md">
                  <label for="up-api-header" :class="labelClass">请求头名称</label>
                  <input
                    id="up-api-header"
                    v-model="form.apiKeyHeader"
                    type="text"
                    :class="inputClass"
                    placeholder="X-API-Key"
                  />
                  <p v-if="fieldErrors.apiKeyHeader" :class="errorClass">
                    {{ fieldErrors.apiKeyHeader }}
                  </p>
                </div>

                <div v-if="form.stdioAuthMode === 'env' && !isRemoteTransport" class="max-w-md">
                  <label for="up-env-credential" :class="labelClass">环境变量名</label>
                  <input
                    id="up-env-credential"
                    v-model="form.envCredentialName"
                    type="text"
                    :class="inputClass"
                    placeholder="API_KEY"
                  />
                  <p v-if="fieldErrors.envCredentialName" :class="errorClass">
                    {{ fieldErrors.envCredentialName }}
                  </p>
                </div>

                <div v-if="isEdit" class="space-y-2">
                  <span :class="labelClass" class="mb-0">保存时如何处理已保存的凭证</span>
                  <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
                    <label
                      v-for="item in credentialActions"
                      :key="item.value"
                      :class="authOptionClass(form.credentialAction === item.value)"
                    >
                      <input
                        v-model="form.credentialAction"
                        class="sr-only"
                        type="radio"
                        :value="item.value"
                      />
                      <span class="min-w-0 flex-1">
                        <span class="block text-sm font-medium">{{ item.label }}</span>
                        <span
                          class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400"
                          >{{ item.desc }}</span
                        >
                      </span>
                    </label>
                  </div>
                  <p v-if="fieldErrors.credentialAction" :class="errorClass">
                    {{ fieldErrors.credentialAction }}
                  </p>
                </div>

                <div v-if="shouldShowCredentialInput" class="max-w-xl">
                  <label for="up-credential" :class="labelClass">
                    Token / API Key（${credential} 的值）
                  </label>
                  <input
                    id="up-credential"
                    v-model="form.credential"
                    type="password"
                    autocomplete="new-password"
                    :class="inputClass"
                    placeholder="粘贴第三方服务提供的凭证"
                  />
                  <p :class="helpClass">
                    连接参数里的 ${credential} 会在实际连接时替换成这里填写或已保存的凭证。
                  </p>
                  <p v-if="fieldErrors.credential" :class="errorClass">
                    {{ fieldErrors.credential }}
                  </p>
                </div>
              </section>

              <section class="space-y-4 rounded-lg border border-gray-200 p-4 dark:border-gray-800">
                <div>
                  <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">运行设置</h4>
                </div>
                <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <label
                    class="flex cursor-pointer items-start gap-3 rounded-lg border border-gray-200 p-3 text-sm text-gray-700 dark:border-gray-800 dark:text-gray-300"
                  >
                    <input
                      v-model="form.enabled"
                      type="checkbox"
                      class="text-brand-500 focus:ring-brand-500/20 mt-0.5 h-4 w-4 rounded border-gray-300"
                    />
                    <span>
                      <span class="block font-medium">启用并参与聚合</span>
                      <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400"
                        >关闭后保留配置，但不会进入对外工具集合。</span
                      >
                    </span>
                  </label>
                  <label
                    class="flex cursor-pointer items-start gap-3 rounded-lg border border-gray-200 p-3 text-sm text-gray-700 dark:border-gray-800 dark:text-gray-300"
                  >
                    <input
                      v-model="form.autoSync"
                      type="checkbox"
                      class="text-brand-500 focus:ring-brand-500/20 mt-0.5 h-4 w-4 rounded border-gray-300"
                    />
                    <span>
                      <span class="block font-medium">自动同步工具列表</span>
                      <span class="mt-1 block text-xs text-gray-500 dark:text-gray-400"
                        >按系统同步计划刷新该上游工具。</span
                      >
                    </span>
                  </label>
                </div>
              </section>

              <section class="rounded-lg border border-gray-200 dark:border-gray-800">
                <button
                  type="button"
                  class="flex w-full items-center justify-between gap-3 px-4 py-3 text-left text-sm font-semibold text-gray-800 hover:bg-gray-50 dark:text-white/90 dark:hover:bg-white/[0.03]"
                  @click="form.advancedOpen = !form.advancedOpen"
                >
                  <span>高级参数</span>
                  <ChevronDownIcon
                    class="h-5 w-5 text-gray-500 transition"
                    :class="form.advancedOpen ? 'rotate-180' : ''"
                    aria-hidden="true"
                  />
                </button>
                <div
                  v-if="form.advancedOpen"
                  class="space-y-4 border-t border-gray-200 p-4 dark:border-gray-800"
                >
                  <template v-if="form.transport === 'stdio'">
                    <div>
                      <label for="up-env" :class="labelClass">环境变量</label>
                      <textarea
                        id="up-env"
                        v-model="form.envText"
                        rows="4"
                        :class="textareaClass"
                        placeholder="NODE_ENV=production&#10;HTTP_PROXY=http://127.0.0.1:7890"
                      ></textarea>
                      <p :class="helpClass">
                        每行一个 KEY=value。需要使用认证区的 Token / API Key 时，值写为
                        ${credential}。
                      </p>
                      <p v-if="fieldErrors.envText" :class="errorClass">
                        {{ fieldErrors.envText }}
                      </p>
                    </div>
                    <div>
                      <label for="up-cwd" :class="labelClass">工作目录</label>
                      <input
                        id="up-cwd"
                        v-model="form.cwd"
                        type="text"
                        :class="inputClass"
                        placeholder="D:\\mcp-server"
                      />
                      <p v-if="fieldErrors.cwd" :class="errorClass">{{ fieldErrors.cwd }}</p>
                    </div>
                  </template>

                  <template v-else>
                    <div>
                      <label for="up-headers" :class="labelClass">额外请求头</label>
                      <textarea
                        id="up-headers"
                        v-model="form.headersText"
                        rows="4"
                        :class="textareaClass"
                        placeholder="X-Team: dev&#10;X-API-Key: ${credential}"
                      ></textarea>
                      <p :class="helpClass">
                        每行一个 Header: value。需要使用认证区的 Token / API Key 时，值写为
                        ${credential}；常见认证方式会自动写入。
                      </p>
                      <p v-if="fieldErrors.headersText" :class="errorClass">
                        {{ fieldErrors.headersText }}
                      </p>
                    </div>
                  </template>

                  <div>
                    <label for="up-custom-json" :class="labelClass">额外连接参数</label>
                    <textarea
                      id="up-custom-json"
                      v-model="form.customParamsJson"
                      rows="5"
                      :class="textareaClass"
                      placeholder='{ "timeoutMs": 30000 }'
                    ></textarea>
                    <p :class="helpClass">
                      只填写当前表单未覆盖的 JSON 字段；常用字段会以表单内容为准。
                    </p>
                    <p v-if="fieldErrors.customParamsJson" :class="errorClass">
                      {{ fieldErrors.customParamsJson }}
                    </p>
                  </div>
                </div>
              </section>
            </template>

            <p
              v-if="formError !== ''"
              role="alert"
              class="bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400 rounded-lg px-4 py-2.5 text-sm"
            >
              {{ formError }}
            </p>
          </div>
        </form>

        <div
          class="flex items-center justify-end gap-3 border-t border-gray-200 px-4 py-4 sm:px-6 dark:border-gray-800"
        >
          <button
            type="button"
            class="rounded-lg border border-gray-300 px-4 py-2.5 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            @click="emit('close')"
          >
            取消
          </button>
          <button
            type="button"
            :disabled="submitting"
            class="bg-brand-500 hover:bg-brand-600 rounded-lg px-4 py-2.5 text-sm font-medium text-white transition disabled:cursor-not-allowed disabled:opacity-60"
            @click="handleSubmit"
          >
            {{ submitting ? '保存中...' : '保存' }}
          </button>
        </div>
      </div>
    </div>
  </transition>
</template>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
