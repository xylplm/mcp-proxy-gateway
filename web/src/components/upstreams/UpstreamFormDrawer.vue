<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import {
  createUpstream,
  testUpstream,
  updateUpstream,
  TRANSPORT_OPTIONS,
  type ConnParams,
  type TransportType,
  type Upstream,
  type UpstreamConfigRequest,
  type UpstreamTestResult,
} from '@/api/upstreams'
import {
  getRuntimeKnownTools,
  installRuntimePackage,
  preflightRuntime,
  type RuntimeKnownTool,
  type RuntimePreflightResult,
} from '@/api/runtime'
import { emptyRateLimits, type UpstreamRateLimits } from '@/api/rateLimits'
import type { PrefillForm, Placeholder } from '@/api/templates'
import { ApiError } from '@/api/request'
import {
  CheckIcon,
  ChevronDownIcon,
  ErrorIcon,
  InfoCircleIcon,
  RefreshIcon,
  SuccessIcon,
} from '@/icons'
import { testStageLabel, upstreamTestDiagnostic } from '@/utils/upstreamTestDiagnostics'
import { buildUpstreamCloneFormSource } from '@/utils/upstreamCopy'
import { normalizeTags } from '@/utils/upstreamTags'
import {
  inferToolsFromCommand,
  normalizeRequirements,
  preflightReadyLabel,
  preflightTone,
  type RequirementsMode,
} from '@/utils/runtimeRequirements'
import { useToast } from '@/composables/useToast'
import { useConfirm } from '@/composables/useConfirm'

const props = defineProps<{
  open: boolean
  upstream: Upstream | null
  prefill: PrefillForm | null
  /** 复制来源；与编辑互斥，优先级低于编辑。 */
  cloneSource?: Upstream | null
  /** 复制时建议名称（父组件保证尽量唯一）。 */
  cloneName?: string
  tagOptions?: string[]
  nextSortOrder?: number
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'saved', payload: { upstream: Upstream; mode: 'create' | 'edit' }): void
}>()

type RemoteAuthMode = 'none' | 'bearer' | 'api-key' | 'custom'
type StdioAuthMode = 'none' | 'env' | 'custom'
type OpenAPIAuthMode =
  | 'none'
  | 'bearer'
  | 'basic'
  | 'api-key-header'
  | 'api-key-query'
  | 'custom-header'
type OpenAPIDocMode = 'url' | 'content'

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
  openapi: {
    title: 'OpenAPI 转工具',
    description: '把已有 REST / OpenAPI 服务转换成 MCP 工具。',
    placeholder: 'https://api.example.com/v1',
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

const openAPIAuthOptions: ReadonlyArray<{ value: OpenAPIAuthMode; label: string; desc: string }> = [
  { value: 'none', label: '无需认证', desc: '接口可直接访问。' },
  { value: 'bearer', label: 'Bearer Token', desc: '发送 Authorization: Bearer <Token>。' },
  { value: 'api-key-header', label: 'API Key Header', desc: '把凭证放入指定请求头。' },
  { value: 'api-key-query', label: 'API Key Query', desc: '把凭证放入指定查询参数。' },
  { value: 'basic', label: 'Basic Auth', desc: '凭证填写 username:password。' },
  { value: 'custom-header', label: '自定义 Header', desc: '把凭证作为指定 Header 值发送。' },
]

const isEdit = computed(() => props.upstream !== null)
const fromClone = computed(() => !isEdit.value && props.cloneSource != null)
const fromTemplate = computed(
  () => props.prefill !== null && !isEdit.value && !fromClone.value,
)
const currentTransport = computed(() => transportHelp[form.transport])
const isRemoteTransport = computed(() => form.transport !== 'stdio')
const isOpenAPITransport = computed(() => form.transport === 'openapi')

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
  if (fromClone.value && props.cloneSource != null) {
    return `复制上游：${props.cloneSource.config.name}`
  }
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
  openAPIBaseUrl: string
  openAPIDocMode: OpenAPIDocMode
  openAPIDocUrl: string
  openAPIDocContent: string
  openAPIAuthMode: OpenAPIAuthMode
  openAPIAuthName: string
  credential: string
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
  rateLimitEnabled: boolean
  rateLimitTimezone: string
  perSecond: number | null
  perMinute: number | null
  perHour: number | null
  perDay: number | null
  perWeek: number | null
  perMonth: number | null
  reqMode: RequirementsMode
  reqTools: string[]
  reqNote: string
}>({
  name: '',
  tagDraft: '',
  tags: [],
  transport: 'stdio',
  command: '',
  args: '',
  url: '',
  openAPIBaseUrl: '',
  openAPIDocMode: 'url',
  openAPIDocUrl: '',
  openAPIDocContent: '',
  openAPIAuthMode: 'none',
  openAPIAuthName: 'X-API-Key',
  credential: '',
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
  rateLimitEnabled: false,
  rateLimitTimezone: 'UTC',
  perSecond: null,
  perMinute: null,
  perHour: null,
  perDay: null,
  perWeek: null,
  perMonth: null,
  reqMode: 'auto',
  reqTools: [],
  reqNote: '',
})

const toast = useToast()
const { confirm } = useConfirm()
const placeholderValues = reactive<Record<string, string>>({})
const placeholders = ref<Placeholder[]>([])
const presetParams = ref<ConnParams>({})
const submitting = ref(false)
const testingConnection = ref(false)
const testResult = ref<UpstreamTestResult | null>(null)
const testRequestToken = ref(0)
const fieldErrors = reactive<Record<string, string>>({})
const formError = ref('')
const testDiagnostic = computed(() => upstreamTestDiagnostic(testResult.value, form.transport))
const knownTools = ref<RuntimeKnownTool[]>([])
const preflight = ref<RuntimePreflightResult | null>(null)
const preflightLoading = ref(false)
const installingPackageId = ref('')
let preflightTimer: ReturnType<typeof setTimeout> | null = null
let preflightSeq = 0

const showRuntimeDeps = computed(() => form.transport === 'stdio')
const preflightBanner = computed(() => {
  if (form.transport !== 'stdio') return null
  if (preflight.value === null) return null
  const tone = preflightTone(
    preflight.value.ready,
    preflight.value.stdioEnabled,
    preflight.value.commandAllowed,
  )
  return {
    tone,
    label: preflightReadyLabel(
      preflight.value.ready,
      preflight.value.stdioEnabled,
      preflight.value.commandAllowed,
    ),
  }
})
const suggestedFromCommand = computed(() => inferToolsFromCommand(form.command))

const normalizedTagOptions = computed(() => normalizeTags(props.tagOptions ?? []))
const formTags = computed(() => normalizeTags([...form.tags, ...parseTags(form.tagDraft)]))
const availableTagOptions = computed(() => normalizedTagOptions.value.filter((tag) => !hasTag(tag)))

const selectedAuthMayUseCredential = computed(() => {
  if (fromTemplate.value) return false
  if (form.transport === 'openapi') return form.openAPIAuthMode !== 'none'
  if (form.transport === 'stdio') return form.stdioAuthMode !== 'none'
  return form.remoteAuthMode !== 'none'
})

const shouldShowCredentialInput = computed(() => {
  if (fromTemplate.value) return false
  return selectedAuthMayUseCredential.value
})

function clearErrors(): void {
  for (const k of Object.keys(fieldErrors)) delete fieldErrors[k]
  formError.value = ''
}

function clearTestResult(): void {
  testResult.value = null
}

function invalidateTestResult(): void {
  testRequestToken.value += 1
  testingConnection.value = false
  clearTestResult()
}

function parseTags(raw: string): string[] {
  return raw
    .split(/[\n,，、;；]/)
    .map((s) => s.trim())
    .filter(Boolean)
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
  form.reqMode = 'auto'
  form.reqTools = []
  form.reqNote = ''
  preflight.value = null
  form.command = ''
  form.args = ''
  form.url = ''
  form.openAPIBaseUrl = ''
  form.openAPIDocMode = 'url'
  form.openAPIDocUrl = ''
  form.openAPIDocContent = ''
  form.openAPIAuthMode = 'none'
  form.openAPIAuthName = 'X-API-Key'
  form.credential = ''
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

function applyRateLimits(limits?: UpstreamRateLimits): void {
  const value = limits ?? emptyRateLimits()
  form.rateLimitEnabled = value.enabled
  form.rateLimitTimezone = value.timezone || 'UTC'
  form.perSecond = value.perSecond && value.perSecond > 0 ? value.perSecond : null
  form.perMinute = value.perMinute && value.perMinute > 0 ? value.perMinute : null
  form.perHour = value.perHour && value.perHour > 0 ? value.perHour : null
  form.perDay = value.perDay && value.perDay > 0 ? value.perDay : null
  form.perWeek = value.perWeek && value.perWeek > 0 ? value.perWeek : null
  form.perMonth = value.perMonth && value.perMonth > 0 ? value.perMonth : null
}

function limitValue(value: number | null): number {
  return typeof value === 'number' && Number.isFinite(value) && value > 0 ? Math.floor(value) : 0
}

function buildRateLimits(): UpstreamRateLimits {
  return {
    enabled: form.rateLimitEnabled,
    perSecond: form.rateLimitEnabled ? limitValue(form.perSecond) : 0,
    perMinute: form.rateLimitEnabled ? limitValue(form.perMinute) : 0,
    perHour: form.rateLimitEnabled ? limitValue(form.perHour) : 0,
    perDay: form.rateLimitEnabled ? limitValue(form.perDay) : 0,
    perWeek: form.rateLimitEnabled ? limitValue(form.perWeek) : 0,
    perMonth: form.rateLimitEnabled ? limitValue(form.perMonth) : 0,
    timezone: form.rateLimitTimezone.trim() || 'UTC',
  }
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
    if (
      ![
        'command',
        'args',
        'url',
        'env',
        'cwd',
        'headers',
        'baseUrl',
        'docUrl',
        'docContent',
        'authType',
        'authName',
        'authValue',
      ].includes(key)
    )
      custom[key] = value
  }
  return Object.keys(custom).length > 0 ? JSON.stringify(custom, null, 2) : ''
}

function normalizeOpenAPIAuthMode(value: unknown): OpenAPIAuthMode {
  switch (value) {
    case 'bearer':
    case 'basic':
    case 'api-key-header':
    case 'api-key-query':
    case 'custom-header':
      return value
    default:
      return 'none'
  }
}

function requiresOpenAPIAuthName(value: OpenAPIAuthMode): boolean {
  return value === 'api-key-header' || value === 'api-key-query' || value === 'custom-header'
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

function fillFromConfig(
  cfg: {
    name: string
    tags?: string[]
    transport: TransportType
    connParams?: ConnParams | null
    credential?: string
    enabled: boolean
    autoSync: boolean
    rateLimits?: UpstreamRateLimits
  },
  options?: { forceName?: string },
): void {
  // 崩溃防御：异常/残缺配置不得让抽屉初始化抛错。
  const connParams =
    cfg.connParams !== null &&
    cfg.connParams !== undefined &&
    typeof cfg.connParams === 'object' &&
    !Array.isArray(cfg.connParams)
      ? cfg.connParams
      : {}
  const headers = normalizeRecord(connParams.headers)
  const env = normalizeRecord(connParams.env)

  form.name = options?.forceName ?? cfg.name
  form.tags = normalizeTags(cfg.tags ?? [])
  form.tagDraft = ''
  form.transport = cfg.transport
  resetManualFields()
  form.command = typeof connParams.command === 'string' ? connParams.command : ''
  form.args = Array.isArray(connParams.args) ? connParams.args.join('\n') : ''
  form.url = typeof connParams.url === 'string' ? connParams.url : ''
  form.openAPIBaseUrl = typeof connParams.baseUrl === 'string' ? connParams.baseUrl : ''
  form.openAPIDocUrl = typeof connParams.docUrl === 'string' ? connParams.docUrl : ''
  form.openAPIDocContent =
    typeof connParams.docContent === 'string' ? connParams.docContent : ''
  form.openAPIDocMode = form.openAPIDocContent.trim() !== '' ? 'content' : 'url'
  form.openAPIAuthMode = normalizeOpenAPIAuthMode(connParams.authType)
  form.openAPIAuthName =
    typeof connParams.authName === 'string' ? connParams.authName : 'X-API-Key'
  form.credential = cfg.credential ?? ''
  applyDetectedAuth(headers, env)
  form.headersText = formatKeyValues(headers, ':')
  form.envText = formatKeyValues(env, '=')
  form.cwd = typeof connParams.cwd === 'string' ? connParams.cwd : ''
  const rr = normalizeRequirements(connParams.runtimeRequirements)
  form.reqMode = rr.mode
  form.reqTools = [...rr.tools]
  form.reqNote = rr.note ?? ''
  if (form.reqMode === 'auto' && form.reqTools.length === 0 && form.command.trim() !== '') {
    form.reqTools = inferToolsFromCommand(form.command)
  }
  form.customParamsJson = customParamsFrom(connParams)
  form.advancedOpen =
    form.headersText !== '' ||
    form.envText !== '' ||
    form.cwd !== '' ||
    form.customParamsJson !== ''
  form.enabled = cfg.enabled
  form.autoSync = cfg.autoSync
  applyRateLimits(cfg.rateLimits)
  schedulePreflight()
  placeholders.value = []
  presetParams.value = {}
}

function resetForm(): void {
  clearErrors()
  invalidateTestResult()
  for (const k of Object.keys(placeholderValues)) delete placeholderValues[k]

  // 优先级：编辑 > 复制 > 模板 > 空白创建
  if (props.upstream !== null) {
    fillFromConfig(props.upstream.config)
    return
  }

  if (props.cloneSource != null) {
    const suggested = (props.cloneName ?? '').trim() || `${props.cloneSource.config.name} 副本`
    const clone = buildUpstreamCloneFormSource(props.cloneSource, suggested)
    fillFromConfig(
      {
        name: clone.name,
        tags: clone.tags,
        transport: clone.transport,
        connParams: clone.connParams,
        credential: clone.credential,
        enabled: clone.enabled,
        autoSync: clone.autoSync,
        rateLimits: clone.rateLimits,
      },
      { forceName: clone.name },
    )
    return
  }

  form.name = props.prefill?.name ?? ''
  form.tags = []
  form.tagDraft = ''
  form.transport = props.prefill?.transport ?? 'stdio'
  resetManualFields()
  form.enabled = true
  form.autoSync = true
  applyRateLimits()
  placeholders.value = props.prefill?.placeholders ?? []
  presetParams.value = props.prefill?.presetParams ?? {}
  for (const ph of placeholders.value) placeholderValues[ph.name] = ''
}

watch(
  () => [props.open, props.upstream, props.prefill, props.cloneSource, props.cloneName],
  () => {
    if (props.open) resetForm()
  },
  { immediate: true },
)

watch(
  () => [
    form.transport,
    form.command,
    form.args,
    form.url,
    form.openAPIBaseUrl,
    form.openAPIDocMode,
    form.openAPIDocUrl,
    form.openAPIDocContent,
    form.openAPIAuthMode,
    form.openAPIAuthName,
    form.credential,
    form.remoteAuthMode,
    form.stdioAuthMode,
    form.apiKeyHeader,
    form.envCredentialName,
    form.headersText,
    form.envText,
    form.cwd,
    form.customParamsJson,
    form.reqMode,
    form.reqTools.join(','),
    JSON.stringify(placeholderValues),
  ],
  () => {
    invalidateTestResult()
    schedulePreflight()
  },
)

watch(
  () => form.command,
  (cmd) => {
    if (form.transport !== 'stdio' || form.reqMode !== 'auto') return
    form.reqTools = inferToolsFromCommand(cmd)
  },
)

async function ensureKnownTools(): Promise<void> {
  if (knownTools.value.length > 0) return
  try {
    knownTools.value = await getRuntimeKnownTools()
  } catch {
    knownTools.value = [
      { name: 'node', label: 'Node.js', packageId: 'node-22.14.0' },
      { name: 'npx', label: 'npx', packageId: 'node-22.14.0' },
      { name: 'npm', label: 'npm', packageId: 'node-22.14.0' },
      { name: 'python', label: 'Python' },
      { name: 'python3', label: 'Python 3' },
      { name: 'uv', label: 'uv', packageId: 'uv-0.6.14' },
      { name: 'uvx', label: 'uvx', packageId: 'uv-0.6.14' },
      { name: 'docker', label: 'Docker' },
    ]
  }
}

function schedulePreflight(): void {
  if (preflightTimer !== null) clearTimeout(preflightTimer)
  if (form.transport !== 'stdio') {
    preflight.value = null
    return
  }
  preflightTimer = setTimeout(() => {
    void runPreflight()
  }, 280)
}

async function runPreflight(): Promise<void> {
  if (form.transport !== 'stdio') {
    preflight.value = null
    return
  }
  await ensureKnownTools()
  const seq = ++preflightSeq
  preflightLoading.value = true
  try {
    const result = await preflightRuntime({
      transport: 'stdio',
      command: form.command.trim(),
      requirements: {
        mode: form.reqMode,
        tools: form.reqTools,
        ...(form.reqNote.trim() !== '' ? { note: form.reqNote.trim() } : {}),
      },
    })
    if (seq === preflightSeq) preflight.value = result
  } catch {
    if (seq === preflightSeq) preflight.value = null
  } finally {
    if (seq === preflightSeq) preflightLoading.value = false
  }
}

function toggleReqTool(name: string): void {
  if (form.reqMode !== 'manual') return
  const i = form.reqTools.indexOf(name)
  if (i >= 0) form.reqTools.splice(i, 1)
  else form.reqTools.push(name)
  schedulePreflight()
}

function setReqMode(mode: RequirementsMode): void {
  form.reqMode = mode
  if (mode === 'auto') form.reqTools = inferToolsFromCommand(form.command)
  schedulePreflight()
}

function applySuggestedTools(): void {
  form.reqMode = 'manual'
  form.reqTools = [...suggestedFromCommand.value]
  schedulePreflight()
}

async function handleInstallPackage(packageId: string): Promise<void> {
  if (!packageId || installingPackageId.value !== '') return
  const ok = await confirm({
    title: '安装运行时',
    message: '将从官方源下载固定版本到数据卷（SHA256 校验）。是否继续？',
    confirmText: '安装',
    tone: 'warning',
  })
  if (!ok) return
  installingPackageId.value = packageId
  try {
    const result = await installRuntimePackage(packageId)
    toast.success(result.reused ? `${result.name} 已存在` : `${result.name} 安装完成`)
    await runPreflight()
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '安装失败')
  } finally {
    installingPackageId.value = ''
  }
}

function itemStatusClass(available: boolean): string {
  return available
    ? 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
    : 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
}

function bannerClass(tone: 'success' | 'warning' | 'error'): string {
  if (tone === 'success') return 'border-success-200 bg-success-50/70 dark:border-success-500/20 dark:bg-success-500/10'
  if (tone === 'error') return 'border-error-200 bg-error-50/70 dark:border-error-500/20 dark:bg-error-500/10'
  return 'border-warning-200 bg-warning-50/70 dark:border-warning-500/20 dark:bg-warning-500/10'
}

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

  if (form.transport === 'openapi') {
    const headers = parseKeyValues(form.headersText, '请求头', 'header')
    if (headers === null) return null
    const params: ConnParams = {
      ...custom,
      baseUrl: form.openAPIBaseUrl.trim(),
      authType: form.openAPIAuthMode,
    }
    if (form.openAPIDocMode === 'content') params.docContent = form.openAPIDocContent.trim()
    else params.docUrl = form.openAPIDocUrl.trim()
    if (requiresOpenAPIAuthName(form.openAPIAuthMode)) params.authName = form.openAPIAuthName.trim()
    if (form.openAPIAuthMode !== 'none') params.authValue = credentialPlaceholder
    if (Object.keys(headers).length > 0) params.headers = headers
    return params
  }

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
    const tools =
      form.reqMode === 'manual'
        ? form.reqTools
        : form.reqTools.length > 0
          ? form.reqTools
          : inferToolsFromCommand(form.command)
    params.runtimeRequirements = {
      mode: form.reqMode,
      tools: [...tools],
      ...(form.reqNote.trim() !== '' ? { note: form.reqNote.trim() } : {}),
    }
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
  if (form.transport === 'openapi') {
    if (
      !validateURLField(
        'openAPIBaseUrl',
        form.openAPIBaseUrl.trim(),
        ['http', 'https'],
        '请输入 API 基础地址',
      )
    ) {
      ok = false
    }
    if (form.openAPIDocMode === 'url') {
      if (
        !validateURLField(
          'openAPIDocUrl',
          form.openAPIDocUrl.trim(),
          ['http', 'https'],
          '请输入 OpenAPI 文档地址',
        )
      ) {
        ok = false
      }
    } else if (form.openAPIDocContent.trim() === '') {
      fieldErrors.openAPIDocContent = '请粘贴 OpenAPI JSON 或 YAML 文档'
      ok = false
    }
    if (requiresOpenAPIAuthName(form.openAPIAuthMode) && form.openAPIAuthName.trim() === '') {
      fieldErrors.openAPIAuthName = '请输入 Header 或 Query 名称'
      ok = false
    }
    return ok
  }
  if (form.transport === 'stdio') {
    if (form.command.trim() === '') {
      fieldErrors.command = '请输入启动命令'
      ok = false
    }
    return ok
  }

  const wantWs = form.transport === 'websocket'
  ok =
    validateURLField(
      'url',
      form.url.trim(),
      wantWs ? ['ws', 'wss'] : ['http', 'https'],
      '请输入服务地址',
    ) && ok
  return ok
}

function validateURLField(
  field: string,
  value: string,
  allowed: string[],
  emptyMessage: string,
): boolean {
  if (value === '') {
    fieldErrors[field] = emptyMessage
    return false
  }
  if (!isValidURL(value)) {
    fieldErrors[field] = '地址不是合法 URL'
    return false
  }
  const proto = value.split(':')[0].toLowerCase()
  if (!allowed.includes(proto)) {
    fieldErrors[field] = `地址协议必须为 ${allowed.join(' / ')}`
    return false
  }
  return true
}

function validateCredential(connParams: ConnParams, credential: string): boolean {
  if (fromTemplate.value) return true
  const usesCredential = hasCredentialReference(connParams)
  const credentialValue = credential.trim()

  if (usesCredential && credentialValue === '') {
    fieldErrors.credential = '请输入 Token 或 API Key'
    return false
  }
  return true
}

function buildConnectionDraft(): { connParams: ConnParams; credential: string } | null {
  let ok = true
  let credential = form.credential.trim()
  let connParams: ConnParams

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

  return { connParams, credential }
}

function makePayload(
  name: string,
  connParams: ConnParams,
  credential: string,
): UpstreamConfigRequest {
  const payload: UpstreamConfigRequest = {
    name,
    tags: formTags.value,
    transport: form.transport,
    connParams,
    enabled: form.enabled,
    sortOrder:
      isEdit.value && props.upstream !== null
        ? props.upstream.config.sortOrder
        : (props.nextSortOrder ?? 0),
    autoSync: form.autoSync,
    rateLimits: buildRateLimits(),
  }

  if (hasCredentialReference(connParams)) {
    payload.credential = credential
  }
  return payload
}

function buildPayload(): UpstreamConfigRequest | null {
  clearErrors()
  let ok = validateName()
  ok = validateTags() && ok
  const draft = buildConnectionDraft()
  if (!ok || draft === null) return null
  return makePayload(form.name.trim(), draft.connParams, draft.credential)
}

function buildConnectionTestPayload(): UpstreamConfigRequest | null {
  clearErrors()
  const draft = buildConnectionDraft()
  if (draft === null) return null
  return makePayload(form.name.trim() || '连接测试', draft.connParams, draft.credential)
}

function mapServerField(field: string): string {
  const local = field.startsWith('connParams.') ? field.slice('connParams.'.length) : field
  const map: Record<string, string> = {
    command: 'command',
    args: 'args',
    url: 'url',
    baseUrl: 'openAPIBaseUrl',
    docUrl: 'openAPIDocUrl',
    docContent: 'openAPIDocContent',
    authType: 'openAPIAuthMode',
    authName: 'openAPIAuthName',
    authValue: 'credential',
    env: 'envText',
    cwd: 'cwd',
    headers: 'headersText',
    rateLimits: 'rateLimits',
    'rateLimits.timezone': 'rateLimitTimezone',
    'rateLimits.perSecond': 'perSecond',
    'rateLimits.perMinute': 'perMinute',
    'rateLimits.perHour': 'perHour',
    'rateLimits.perDay': 'perDay',
    'rateLimits.perWeek': 'perWeek',
    'rateLimits.perMonth': 'perMonth',
  }
  return map[local] ?? local
}

function applyServerError(err: unknown, fallback = '保存失败，请稍后重试'): void {
  if (err instanceof ApiError) {
    for (const [k, v] of Object.entries(err.fields)) fieldErrors[mapServerField(k)] = v
    formError.value = err.message || fallback
    return
  }
  formError.value = err instanceof Error ? err.message : fallback
}

async function handleTestConnection(): Promise<void> {
  if (testingConnection.value || submitting.value) return
  clearTestResult()
  if (form.transport === 'stdio') {
    await runPreflight()
    if (preflight.value && !preflight.value.ready) {
      const ok = await confirm({
        title: '依赖尚未就绪',
        message: '当前宿主缺少部分运行时依赖，测试连接可能失败。建议先补齐依赖，仍要继续测试吗？',
        confirmText: '仍要测试',
        cancelText: '先去补齐',
        tone: 'warning',
      })
      if (!ok) return
    }
  }
  const payload = buildConnectionTestPayload()
  if (payload === null) return

  const token = testRequestToken.value + 1
  testRequestToken.value = token
  testingConnection.value = true
  try {
    const result = await testUpstream(payload)
    if (token === testRequestToken.value) testResult.value = result
  } catch (err) {
    if (token === testRequestToken.value) applyServerError(err, '测试失败，请检查配置')
  } finally {
    if (token === testRequestToken.value) testingConnection.value = false
  }
}

async function handleSubmit(): Promise<void> {
  if (submitting.value) return
  const payload = buildPayload()
  if (payload === null) return

  submitting.value = true
  try {
    const saved =
      isEdit.value && props.upstream !== null
        ? await updateUpstream(props.upstream.id, payload)
        : await createUpstream(payload)
    emit('saved', { upstream: saved, mode: isEdit.value ? 'edit' : 'create' })
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
            <div
              v-if="fromClone"
              class="border-brand-200 bg-brand-50/80 dark:border-brand-500/20 dark:bg-brand-500/10 flex gap-3 rounded-xl border border-l-4 border-l-brand-500 px-4 py-3"
            >
              <InfoCircleIcon
                class="text-brand-500 dark:text-brand-300 mt-0.5 h-4 w-4 shrink-0"
                aria-hidden="true"
              />
              <div class="min-w-0 text-sm leading-6 text-gray-700 dark:text-gray-200">
                <p class="font-medium text-gray-800 dark:text-white/90">已预填连接配置与凭证</p>
                <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
                  请确认认证信息后创建。复制会生成独立上游，不会带走工具缓存与规则绑定。
                </p>
              </div>
            </div>

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
                <p :class="helpClass">用于管理台分组与识别，输入后回车加入，也可点选已有标签。</p>
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
                  <AppTooltip
                    content="不同 MCP Server 会提供不同接入方式；本地命令通常来自 npm、uvx 或 docker，远程服务通常提供 URL。"
                    placement="right"
                  >
                    <InfoCircleIcon class="h-4 w-4 text-gray-400" aria-hidden="true" />
                  </AppTooltip>
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

                    <div
                      v-if="showRuntimeDeps"
                      class="rounded-xl border border-gray-200 p-4 transition dark:border-gray-800"
                    >
                      <div class="flex flex-wrap items-start justify-between gap-2">
                        <div>
                          <h5 class="text-sm font-semibold text-gray-800 dark:text-white/90">
                            运行环境依赖
                          </h5>
                          <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                            声明该本地 MCP 需要的宿主工具。可自动按命令建议，也可手动选择。
                          </p>
                        </div>
                        <span
                          v-if="preflightBanner"
                          class="inline-flex rounded-full px-2.5 py-1 text-[11px] font-medium"
                          :class="itemStatusClass(preflightBanner.tone === 'success')"
                        >
                          {{ preflightLoading ? '检测中…' : preflightBanner.label }}
                        </span>
                      </div>

                      <div
                        v-if="preflightBanner && preflightBanner.tone !== 'success'"
                        class="mt-3 rounded-lg border px-3 py-2 text-xs leading-5 text-gray-700 dark:text-gray-200"
                        :class="bannerClass(preflightBanner.tone)"
                      >
                        <p v-if="preflight?.commandError">{{ preflight.commandError }}</p>
                        <p v-else>缺少依赖时仍可先保存配置；测试连接或实际启动前建议补齐。</p>
                      </div>

                      <div class="mt-3 inline-flex rounded-lg bg-gray-100 p-1 dark:bg-white/[0.04]">
                        <button
                          type="button"
                          class="rounded-md px-3 py-1.5 text-xs font-medium transition"
                          :class="
                            form.reqMode === 'auto'
                              ? 'bg-white text-gray-900 shadow-sm dark:bg-gray-800 dark:text-white'
                              : 'text-gray-500 dark:text-gray-400'
                          "
                          @click="setReqMode('auto')"
                        >
                          按命令自动
                        </button>
                        <button
                          type="button"
                          class="rounded-md px-3 py-1.5 text-xs font-medium transition"
                          :class="
                            form.reqMode === 'manual'
                              ? 'bg-white text-gray-900 shadow-sm dark:bg-gray-800 dark:text-white'
                              : 'text-gray-500 dark:text-gray-400'
                          "
                          @click="setReqMode('manual')"
                        >
                          我自己选择
                        </button>
                      </div>

                      <div class="mt-3 flex flex-wrap gap-2">
                        <button
                          v-for="tool in knownTools"
                          :key="tool.name"
                          type="button"
                          class="inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition"
                          :class="
                            form.reqTools.includes(tool.name)
                              ? 'border-brand-300 bg-brand-50 text-brand-700 dark:border-brand-500/40 dark:bg-brand-500/10 dark:text-brand-300'
                              : 'border-gray-200 text-gray-500 dark:border-gray-700 dark:text-gray-400'
                          "
                          :disabled="form.reqMode === 'auto'"
                          @click="toggleReqTool(tool.name)"
                        >
                          {{ tool.label }}
                          <span
                            v-if="preflight?.items?.find((i) => i.name === tool.name)"
                            class="h-1.5 w-1.5 rounded-full"
                            :class="
                              preflight.items.find((i) => i.name === tool.name)?.available
                                ? 'bg-success-500'
                                : 'bg-warning-500'
                            "
                          />
                        </button>
                      </div>

                      <div
                        v-if="form.reqMode === 'auto' && suggestedFromCommand.length === 0"
                        class="mt-2 text-xs text-gray-500 dark:text-gray-400"
                      >
                        无法从命令判断运行时（例如自定义二进制）。请切换到「我自己选择」勾选实际依赖。
                        <button
                          type="button"
                          class="ml-1 font-medium text-brand-600 hover:underline dark:text-brand-400"
                          @click="setReqMode('manual')"
                        >
                          手动选择
                        </button>
                      </div>

                      <div
                        v-if="form.reqMode === 'manual' && suggestedFromCommand.length > 0"
                        class="mt-2"
                      >
                        <button
                          type="button"
                          class="text-xs font-medium text-brand-600 hover:underline dark:text-brand-400"
                          @click="applySuggestedTools"
                        >
                          使用命令建议（{{ suggestedFromCommand.join('、') }}）
                        </button>
                      </div>

                      <div
                        v-if="preflight?.items?.length"
                        class="mt-3 space-y-1.5 rounded-lg bg-gray-50 p-3 dark:bg-white/[0.03]"
                      >
                        <div
                          v-for="item in preflight.items"
                          :key="item.name"
                          class="flex flex-wrap items-center justify-between gap-2 text-xs"
                        >
                          <span class="font-mono text-gray-700 dark:text-gray-200">
                            {{ item.label }}
                            <span class="text-gray-400">({{ item.name }})</span>
                          </span>
                          <span class="inline-flex items-center gap-2">
                            <span
                              class="rounded-full px-2 py-0.5 font-medium"
                              :class="itemStatusClass(item.available)"
                            >
                              {{ item.available ? '可用' : '缺失' }}
                            </span>
                            <button
                              v-if="!item.available && item.fixable && item.packageId"
                              type="button"
                              class="font-medium text-brand-600 hover:underline disabled:opacity-50 dark:text-brand-400"
                              :disabled="installingPackageId !== ''"
                              @click="handleInstallPackage(item.packageId!)"
                            >
                              {{
                                installingPackageId === item.packageId ? '安装中…' : '一键安装'
                              }}
                            </button>
                          </span>
                        </div>
                      </div>

                      <div class="mt-3 flex flex-wrap gap-2">
                        <router-link
                          to="/runtime"
                          class="text-xs font-medium text-brand-600 hover:underline dark:text-brand-400"
                        >
                          打开运行环境
                        </router-link>
                        <button
                          type="button"
                          class="text-xs font-medium text-gray-500 hover:text-gray-700 dark:text-gray-400"
                          :disabled="preflightLoading"
                          @click="runPreflight"
                        >
                          重新检测
                        </button>
                      </div>
                      <p v-if="fieldErrors.runtimeRequirements" :class="errorClass">
                        {{ fieldErrors.runtimeRequirements }}
                      </p>
                    </div>
                  </div>
                </template>

                <template v-else-if="isOpenAPITransport">
                  <div class="space-y-4">
                    <div>
                      <label for="up-openapi-base-url" :class="labelClass"
                        >API 基础地址<span class="text-error-500">*</span></label
                      >
                      <input
                        id="up-openapi-base-url"
                        v-model="form.openAPIBaseUrl"
                        type="text"
                        :class="inputClass"
                        :placeholder="currentTransport.placeholder"
                      />
                      <p :class="helpClass">
                        业务接口的基础地址，例如 https://api.example.com/v1。
                      </p>
                      <p v-if="fieldErrors.openAPIBaseUrl" :class="errorClass">
                        {{ fieldErrors.openAPIBaseUrl }}
                      </p>
                    </div>

                    <div>
                      <span :class="labelClass">OpenAPI 文档</span>
                      <div class="mb-3 inline-flex rounded-lg bg-gray-100 p-1 dark:bg-white/[0.04]">
                        <button
                          type="button"
                          class="rounded-md px-3 py-1.5 text-xs font-medium transition"
                          :class="
                            form.openAPIDocMode === 'url'
                              ? 'text-brand-700 dark:text-brand-300 bg-white shadow-sm dark:bg-gray-900'
                              : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'
                          "
                          @click="form.openAPIDocMode = 'url'"
                        >
                          文档地址
                        </button>
                        <button
                          type="button"
                          class="rounded-md px-3 py-1.5 text-xs font-medium transition"
                          :class="
                            form.openAPIDocMode === 'content'
                              ? 'text-brand-700 dark:text-brand-300 bg-white shadow-sm dark:bg-gray-900'
                              : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'
                          "
                          @click="form.openAPIDocMode = 'content'"
                        >
                          粘贴文档
                        </button>
                      </div>
                      <input
                        v-if="form.openAPIDocMode === 'url'"
                        id="up-openapi-doc-url"
                        v-model="form.openAPIDocUrl"
                        type="text"
                        :class="inputClass"
                        placeholder="https://api.example.com/openapi.json"
                      />
                      <textarea
                        v-else
                        id="up-openapi-doc-content"
                        v-model="form.openAPIDocContent"
                        rows="8"
                        :class="textareaClass"
                        placeholder="粘贴 OpenAPI JSON 或 YAML"
                      ></textarea>
                      <p :class="helpClass">
                        支持 OpenAPI 3.x 的 JSON/YAML，生成工具后可在工具目录预览。
                      </p>
                      <p v-if="fieldErrors.openAPIDocUrl" :class="errorClass">
                        {{ fieldErrors.openAPIDocUrl }}
                      </p>
                      <p v-if="fieldErrors.openAPIDocContent" :class="errorClass">
                        {{ fieldErrors.openAPIDocContent }}
                      </p>
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

                <div v-if="isOpenAPITransport" class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                  <label
                    v-for="item in openAPIAuthOptions"
                    :key="item.value"
                    :class="authOptionClass(form.openAPIAuthMode === item.value)"
                  >
                    <input
                      v-model="form.openAPIAuthMode"
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

                <div v-else-if="isRemoteTransport" class="grid grid-cols-1 gap-3 sm:grid-cols-2">
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

                <div
                  v-if="requiresOpenAPIAuthName(form.openAPIAuthMode) && isOpenAPITransport"
                  class="max-w-md"
                >
                  <label for="up-openapi-auth-name" :class="labelClass">
                    {{ form.openAPIAuthMode === 'api-key-query' ? 'Query 参数名' : 'Header 名称' }}
                  </label>
                  <input
                    id="up-openapi-auth-name"
                    v-model="form.openAPIAuthName"
                    type="text"
                    :class="inputClass"
                    :placeholder="
                      form.openAPIAuthMode === 'api-key-query' ? 'api_key' : 'X-API-Key'
                    "
                  />
                  <p v-if="fieldErrors.openAPIAuthName" :class="errorClass">
                    {{ fieldErrors.openAPIAuthName }}
                  </p>
                </div>

                <div v-if="!isRemoteTransport" class="grid grid-cols-1 gap-3 sm:grid-cols-3">
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

                <div
                  v-if="form.remoteAuthMode === 'api-key' && isRemoteTransport && !isOpenAPITransport"
                  class="max-w-md"
                >
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

                <div v-if="shouldShowCredentialInput" class="max-w-xl">
                  <label for="up-credential" :class="labelClass">
                    Token / API Key（${credential} 的值）
                  </label>
                  <input
                    id="up-credential"
                    v-model="form.credential"
                    type="text"
                    autocomplete="off"
                    :class="inputClass"
                    placeholder="粘贴第三方服务提供的凭证"
                  />
                  <p :class="helpClass">
                    连接参数里的 ${credential} 会在实际连接时替换成这里填写的凭证。
                  </p>
                  <p v-if="fieldErrors.credential" :class="errorClass">
                    {{ fieldErrors.credential }}
                  </p>
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
              <div class="rounded-lg border border-gray-200 p-3 dark:border-gray-800">
                <label class="flex cursor-pointer items-start justify-between gap-4">
                  <span>
                    <span class="block text-sm font-medium text-gray-800 dark:text-white/90"
                      >限流与额度</span
                    >
                    <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">
                      按该上游维度统计调用次数，用于避开第三方频率限制和周期额度。
                    </span>
                  </span>
                  <input
                    v-model="form.rateLimitEnabled"
                    type="checkbox"
                    class="text-brand-500 focus:ring-brand-500/20 mt-1 h-4 w-4 rounded border-gray-300"
                  />
                </label>

                <div
                  v-if="form.rateLimitEnabled"
                  class="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2"
                >
                  <div>
                    <label for="up-rate-second" :class="labelClass">每秒上限</label>
                    <input
                      id="up-rate-second"
                      v-model.number="form.perSecond"
                      type="number"
                      min="0"
                      :class="inputClass"
                      placeholder="不填表示不限"
                    />
                    <p v-if="fieldErrors.perSecond" :class="errorClass">
                      {{ fieldErrors.perSecond }}
                    </p>
                  </div>
                  <div>
                    <label for="up-rate-minute" :class="labelClass">每分钟上限</label>
                    <input
                      id="up-rate-minute"
                      v-model.number="form.perMinute"
                      type="number"
                      min="0"
                      :class="inputClass"
                      placeholder="不填表示不限"
                    />
                    <p v-if="fieldErrors.perMinute" :class="errorClass">
                      {{ fieldErrors.perMinute }}
                    </p>
                  </div>
                  <div>
                    <label for="up-rate-hour" :class="labelClass">每小时上限</label>
                    <input
                      id="up-rate-hour"
                      v-model.number="form.perHour"
                      type="number"
                      min="0"
                      :class="inputClass"
                      placeholder="不填表示不限"
                    />
                    <p v-if="fieldErrors.perHour" :class="errorClass">{{ fieldErrors.perHour }}</p>
                  </div>
                  <div>
                    <label for="up-rate-day" :class="labelClass">每日额度</label>
                    <input
                      id="up-rate-day"
                      v-model.number="form.perDay"
                      type="number"
                      min="0"
                      :class="inputClass"
                      placeholder="不填表示不限"
                    />
                    <p v-if="fieldErrors.perDay" :class="errorClass">{{ fieldErrors.perDay }}</p>
                  </div>
                  <div>
                    <label for="up-rate-week" :class="labelClass">每周额度</label>
                    <input
                      id="up-rate-week"
                      v-model.number="form.perWeek"
                      type="number"
                      min="0"
                      :class="inputClass"
                      placeholder="不填表示不限"
                    />
                    <p v-if="fieldErrors.perWeek" :class="errorClass">{{ fieldErrors.perWeek }}</p>
                  </div>
                  <div>
                    <label for="up-rate-month" :class="labelClass">每月额度</label>
                    <input
                      id="up-rate-month"
                      v-model.number="form.perMonth"
                      type="number"
                      min="0"
                      :class="inputClass"
                      placeholder="不填表示不限"
                    />
                    <p v-if="fieldErrors.perMonth" :class="errorClass">
                      {{ fieldErrors.perMonth }}
                    </p>
                  </div>
                  <div class="sm:col-span-2">
                    <label for="up-rate-timezone" :class="labelClass">额度重置时区</label>
                    <input
                      id="up-rate-timezone"
                      v-model="form.rateLimitTimezone"
                      type="text"
                      :class="inputClass"
                      placeholder="UTC"
                    />
                    <p :class="helpClass">
                      用于每日、每周、每月额度窗口，填写 IANA 时区，例如 UTC 或 Asia/Shanghai。
                    </p>
                    <p v-if="fieldErrors.rateLimitTimezone" :class="errorClass">
                      {{ fieldErrors.rateLimitTimezone }}
                    </p>
                  </div>
                </div>
                <p v-if="fieldErrors.rateLimits" :class="errorClass">
                  {{ fieldErrors.rateLimits }}
                </p>
              </div>
            </section>

            <section
              v-if="testResult !== null"
              :class="[
                'rounded-lg border p-4',
                testResult.ok
                  ? 'border-success-200 bg-success-50/70 dark:border-success-500/20 dark:bg-success-500/10'
                  : 'border-error-200 bg-error-50/70 dark:border-error-500/20 dark:bg-error-500/10',
              ]"
              role="status"
            >
              <div class="flex items-start gap-3">
                <SuccessIcon
                  v-if="testResult.ok"
                  class="text-success-500 mt-0.5 h-5 w-5 shrink-0"
                  aria-hidden="true"
                />
                <ErrorIcon
                  v-else
                  class="text-error-500 mt-0.5 h-5 w-5 shrink-0"
                  aria-hidden="true"
                />
                <div class="min-w-0 flex-1">
                  <div class="flex flex-wrap items-center gap-x-3 gap-y-1">
                    <h4
                      :class="[
                        'text-sm font-semibold',
                        testResult.ok
                          ? 'text-success-700 dark:text-success-300'
                          : 'text-error-700 dark:text-error-300',
                      ]"
                    >
                      {{ testResult.ok ? '连接正常' : '测试未通过' }}
                    </h4>
                    <span class="text-xs text-gray-500 dark:text-gray-400">
                      {{ testStageLabel(testResult.stage) }} · {{ testResult.durationMs }} ms
                    </span>
                  </div>

                  <p
                    :class="[
                      'mt-1 text-sm',
                      testResult.ok
                        ? 'text-success-700 dark:text-success-300'
                        : 'text-error-700 dark:text-error-300',
                    ]"
                  >
                    <template v-if="testResult.ok">
                      拉取到 {{ testResult.count }} 个工具。
                    </template>
                    <template v-else>
                      {{ testResult.message || '上游未返回具体错误。' }}
                    </template>
                  </p>

                  <div
                    v-if="testDiagnostic !== null"
                    class="border-error-200/80 dark:border-error-500/20 mt-3 rounded-lg border bg-white/70 p-3 dark:bg-white/[0.03]"
                  >
                    <p class="text-error-700 dark:text-error-300 text-sm font-medium">
                      {{ testDiagnostic.title }}
                    </p>
                    <p class="text-error-700/80 dark:text-error-300/80 mt-1 text-xs leading-5">
                      {{ testDiagnostic.description }}
                    </p>
                    <ul class="mt-2 space-y-1 text-xs leading-5 text-gray-600 dark:text-gray-300">
                      <li v-for="action in testDiagnostic.actions" :key="action" class="flex gap-2">
                        <span
                          class="bg-error-400 mt-2 h-1 w-1 shrink-0 rounded-full"
                          aria-hidden="true"
                        ></span>
                        <span>{{ action }}</span>
                      </li>
                    </ul>
                  </div>

                  <div
                    v-if="testResult.ok && testResult.tools.length > 0"
                    class="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-2"
                  >
                    <div
                      v-for="tool in testResult.tools"
                      :key="tool.originalName || tool.name"
                      class="border-success-200/70 dark:border-success-500/20 rounded-lg border bg-white/70 p-3 dark:bg-white/[0.03]"
                    >
                      <p class="truncate text-sm font-medium text-gray-800 dark:text-white/90">
                        {{ tool.name || tool.originalName }}
                      </p>
                      <p
                        v-if="tool.description"
                        class="mt-1 line-clamp-2 text-xs leading-5 text-gray-500 dark:text-gray-400"
                      >
                        {{ tool.description }}
                      </p>
                    </div>
                  </div>
                  <p
                    v-else-if="testResult.ok"
                    class="mt-2 text-xs text-gray-500 dark:text-gray-400"
                  >
                    上游连接正常，但当前未返回工具。
                  </p>
                  <p
                    v-if="testResult.ok && testResult.count > testResult.tools.length"
                    class="mt-2 text-xs text-gray-500 dark:text-gray-400"
                  >
                    已预览前 {{ testResult.tools.length }} 个工具。
                  </p>
                </div>
              </div>
            </section>

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
          class="flex flex-wrap items-center justify-end gap-3 border-t border-gray-200 px-4 py-4 sm:px-6 dark:border-gray-800"
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
            :disabled="testingConnection || submitting"
            class="inline-flex items-center gap-2 rounded-lg border border-gray-300 px-4 py-2.5 text-sm font-medium text-gray-700 transition hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            @click="handleTestConnection"
          >
            <RefreshIcon
              class="h-4 w-4"
              :class="testingConnection ? 'animate-spin' : ''"
              aria-hidden="true"
            />
            {{ testingConnection ? '测试中...' : '测试连接' }}
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
