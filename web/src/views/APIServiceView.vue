<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import {
  BoxCubeIcon,
  CheckIcon,
  DocsIcon,
  InfoCircleIcon,
  PlugInIcon,
  RefreshIcon,
  UserGroupIcon,
} from '@/icons'
import { listAPIKeys } from '@/api/apikeys'
import { getHealth, type DetailHealthReport } from '@/api/health'
import {
  extractAPIError,
  getSettingsSnapshot,
  updateSettings,
  type MCPMode,
  type ServerConfig,
  type YAMLConfig,
} from '@/api/settings'
import { getAggregatedTools, type GatewayTool, type ToolDef, type ToolDetail, type ToolSource } from '@/api/tools'
import type { UpstreamRateLimits } from '@/api/rateLimits'
import { useClipboard } from '@/composables/useClipboard'
import { useToast } from '@/composables/useToast'
import {
  buildMCPClientConfig,
  getMCPClientPreset,
  MCP_CLIENT_PRESETS,
  normalizeMCPServerName,
  type MCPClientConfigKind,
  type MCPClientPreset,
  type MCPClientPresetKey,
} from '@/utils/mcpClientConfig'
import {
  DEFAULT_MCP_HTTP_ORIGIN,
  DEFAULT_MCP_WS_ORIGIN,
  loadMCPAccessHistory,
  markMCPOriginRecentlyUsed,
  mcpHTTPOrigin,
  mcpWSOrigin,
  normalizeMCPOrigin,
  saveMCPAccessHistory,
  type MCPAccessHistory,
} from '@/utils/mcpAccessHistory'

type EndpointKey = 'sse' | 'http' | 'ws'
type AuthKey = 'header' | 'bearer' | 'query'

interface EndpointItem {
  key: EndpointKey
  name: string
  badge: string
  address: string
  desc: string
  clientType: string
  guideDesc: string
}

interface AuthOption {
  key: AuthKey
  label: string
  badge: string
  desc: string
}

interface GuideSnippet {
  key: string
  title: string
  desc: string
  code: string
}

const loading = ref(false)
const refreshing = ref(false)
const saving = ref(false)
const loadError = ref('')
const formError = ref('')
const copiedKey = ref('')
const guideOpen = ref(false)
const selectedGuidePreset = ref<MCPClientPresetKey>('generic')
const guideMode = ref<'full' | 'smart'>('full')
const selectedGuideEndpoint = ref<EndpointKey>('http')
const selectedGuideAuth = ref<AuthKey>('bearer')
const guideConfigKind = ref<MCPClientConfigKind>('mcp-json')
const guideServerName = ref('mcp-proxy-gateway')
const guideOrigin = ref('')
const guideOriginHistory = ref<MCPAccessHistory>({ origins: [] })
const guideCopiedKey = ref('')
const guideCopyError = ref('')
const { copy } = useClipboard()
const toast = useToast()

const settings = ref<YAMLConfig | null>(null)
const runtimeServer = ref<ServerConfig | null>(null)
const health = ref<DetailHealthReport | null>(null)
const apiKeyTotal = ref(0)
const apiKeyEnabled = ref(0)
const aggregatedTools = ref<ToolDef[]>([])
const aggregatedToolDetails = ref<ToolDetail[]>([])
const gatewayTools = ref<GatewayTool[]>([])
const selectedAggregatedToolName = ref('')
const toolDetailOpen = ref(false)

const xiaozhiEnabled = ref(false)
const xiaozhiEndpoint = ref('')
const xiaozhiMode = ref<MCPMode>('full')
const endpointTab = ref<'full' | 'smart'>('full')
const fieldErrors = reactive<Record<string, string>>({})

const effectiveServer = computed<ServerConfig | null>(() => runtimeServer.value ?? settings.value?.server ?? null)
const targetServer = computed<ServerConfig | null>(() => settings.value?.server ?? runtimeServer.value ?? null)
const listenerReused = computed(() => (effectiveServer.value?.public_mcp_addr.trim() ?? '') === '')
const targetListenerReused = computed(() => (targetServer.value?.public_mcp_addr.trim() ?? '') === '')
const listenerPendingRestart = computed(
  () => settings.value !== null && runtimeServer.value !== null && JSON.stringify(settings.value.server) !== JSON.stringify(runtimeServer.value),
)

// 对外地址采用占位符：网关部署在容器内，无法预知用户真实的对外域名、IP 与端口。
// http(s) / ws(s) 表示协议二选一（按是否启用 TLS 填写），<your-host> 与 <port> 为占位符，
// 由用户根据实际对外可达地址替换。
const HTTP_ORIGIN = DEFAULT_MCP_HTTP_ORIGIN
const WS_ORIGIN = DEFAULT_MCP_WS_ORIGIN

const mcpPortModeLabel = computed(() => {
  if (targetServer.value === null) return '加载中'
  if (!targetListenerReused.value) return '独立 MCP 端口'
  return '管理端口复用'
})

const mcpPortSecurityText = computed(() => {
  if (effectiveServer.value === null) return '加载监听配置中'
  if (listenerPendingRestart.value) {
    if (!targetListenerReused.value) {
      if (targetServer.value?.expose_mcp_on_admin_addr) return '监听配置已保存，网关重启完成后会启用独立 MCP 端口；管理端口仍保留 /mcp/* 兼容入口。'
      return '监听配置已保存，网关重启完成后会启用独立 MCP 端口，管理端口不暴露 /mcp/*。'
    }
    return '监听配置已保存，网关重启完成后会复用管理端口。公网暴露时建议启用独立 MCP 端口。'
  }
  if (listenerReused.value) return '当前对外 MCP 与管理台共用监听端口。公网暴露时建议启用独立 MCP 端口，并关闭管理端口 MCP 入口。'
  if (effectiveServer.value.expose_mcp_on_admin_addr) return '独立 MCP 端口已运行；管理端口仍保留 /mcp/* 兼容入口，可在确认客户端迁移后关闭。'
  return '独立 MCP 端口已运行，管理端口不暴露 /mcp/*。'
})

const mcpPortNoticeClass = computed(() => {
  if (targetListenerReused.value) {
    return 'mt-5 rounded-xl border border-warning-200 bg-warning-50/80 p-4 dark:border-warning-500/30 dark:bg-warning-500/10'
  }
  return 'mt-5 rounded-xl border border-success-200 bg-success-50/70 p-4 dark:border-success-500/30 dark:bg-success-500/10'
})

const mcpPortBadgeClass = computed(() => {
  if (targetListenerReused.value) {
    return 'shrink-0 rounded-full bg-white px-2.5 py-1 text-xs font-medium text-warning-700 dark:bg-white/10 dark:text-warning-300'
  }
  return 'shrink-0 rounded-full bg-white px-2.5 py-1 text-xs font-medium text-success-700 dark:bg-white/10 dark:text-success-300'
})

const mcpPortSettingsLinkClass = computed(() => {
  if (targetListenerReused.value) {
    return 'inline-flex h-8 items-center rounded-lg border border-warning-200 bg-white px-3 text-xs font-medium text-warning-700 transition hover:bg-warning-100 dark:border-warning-500/30 dark:bg-white/10 dark:text-warning-300 dark:hover:bg-warning-500/10'
  }
  return 'inline-flex h-8 items-center rounded-lg border border-success-200 bg-white px-3 text-xs font-medium text-success-700 transition hover:bg-success-100 dark:border-success-500/30 dark:bg-white/10 dark:text-success-300 dark:hover:bg-success-500/10'
})

const endpoints = computed<EndpointItem[]>(() => [
  {
    key: 'sse',
    name: 'SSE',
    badge: '事件流',
    address: `${HTTP_ORIGIN}/mcp/sse`,
    desc: '适合仍使用 SSE 传输的 MCP 客户端。',
    clientType: 'sse',
    guideDesc: '兼容旧版或仍以事件流建立会话的客户端，适合需要保持服务端推送通道的场景。',
  },
  {
    key: 'http',
    name: 'Streamable HTTP',
    badge: '推荐',
    address: `${HTTP_ORIGIN}/mcp/http`,
    desc: '适合支持新版 HTTP 传输的客户端。',
    clientType: 'streamable-http',
    guideDesc: '当前最推荐的远程 MCP 接入方式，配置简单，适合绝大多数支持新版 MCP 传输的客户端。',
  },
  {
    key: 'ws',
    name: 'WebSocket',
    badge: '长连接',
    address: `${WS_ORIGIN}/mcp/ws`,
    desc: '适合需要稳定双向通道的客户端。',
    clientType: 'websocket',
    guideDesc: '适合需要长连接和双向通信的客户端，浏览器直连时建议使用查询参数认证。',
  },
])

const smartEndpoints = computed<EndpointItem[]>(() => [
  {
    key: 'sse',
    name: 'SSE',
    badge: '事件流',
    address: `${HTTP_ORIGIN}/mcp/smart/sse`,
    desc: '智能模式 SSE 端点。',
    clientType: 'sse',
    guideDesc: '智能模式 SSE 传输端点。',
  },
  {
    key: 'http',
    name: 'Streamable HTTP',
    badge: '推荐',
    address: `${HTTP_ORIGIN}/mcp/smart/http`,
    desc: '智能模式 HTTP 端点。',
    clientType: 'streamable-http',
    guideDesc: '智能模式 Streamable HTTP 端点，适合大多数客户端。',
  },
  {
    key: 'ws',
    name: 'WebSocket',
    badge: '长连接',
    address: `${WS_ORIGIN}/mcp/smart/ws`,
    desc: '智能模式 WebSocket 端点。',
    clientType: 'websocket',
    guideDesc: '智能模式 WebSocket 端点。',
  },
])

const authOptions: ReadonlyArray<AuthOption> = [
  {
    key: 'bearer',
    label: 'Bearer Token',
    badge: '推荐',
    desc: '把 API Key 放在 Authorization 请求头中，兼容多数 MCP 客户端和 HTTP 工具。',
  },
  {
    key: 'header',
    label: 'X-API-Key 请求头',
    badge: '清晰',
    desc: '使用独立的 X-API-Key 请求头，便于网关、代理和日志中识别访问凭证。',
  },
  {
    key: 'query',
    label: 'URL 查询参数',
    badge: '浏览器友好',
    desc: '把 API Key 拼到地址上，适合浏览器 EventSource 或 WebSocket 这类不方便设置 Header 的场景。',
  },
]

const jsonRPCPayload = {
  jsonrpc: '2.0',
  id: 'tools-list-1',
  method: 'tools/list',
  params: {},
}

const enabledUpstreams = computed(
  () => health.value?.upstreams.filter((item) => item.enabled).length ?? 0,
)
const availableUpstreams = computed(
  () =>
    health.value?.upstreams.filter((item) => item.enabled && item.state === 'available').length ??
    0,
)
const dependencyOK = computed(
  () => health.value?.dependencies.filter((item) => item.status === 'ok').length ?? 0,
)
const aggregatedToolCount = computed(() => aggregatedTools.value.length)
const toolDetailsByName = computed(() => {
  const map = new Map<string, ToolDetail>()
  for (const item of aggregatedToolDetails.value) {
    if (item.tool?.name) map.set(item.tool.name, item)
  }
  return map
})
const selectedToolDetail = computed<ToolDetail | null>(() => {
  if (selectedAggregatedToolName.value !== '') {
    const detail = toolDetailsByName.value.get(selectedAggregatedToolName.value)
    if (detail) return detail
  }
  return null
})
const activeEndpoints = computed(() => endpointTab.value === 'smart' ? smartEndpoints.value : endpoints.value)
const guideHTTPOrigin = computed(() => mcpHTTPOrigin(guideOrigin.value))
const guideWSOrigin = computed(() => mcpWSOrigin(guideOrigin.value))
const guideOriginDisplay = computed(() => normalizeMCPOrigin(guideOrigin.value) || HTTP_ORIGIN)
const guidePresetOptions = MCP_CLIENT_PRESETS
const selectedPreset = computed<MCPClientPreset>(() => getMCPClientPreset(selectedGuidePreset.value))
const guideEndpoints = computed<EndpointItem[]>(() => {
  const httpOrigin = guideHTTPOrigin.value
  const wsOrigin = guideWSOrigin.value
  if (guideMode.value === 'smart') {
    return [
      {
        key: 'sse',
        name: 'SSE',
        badge: '事件流',
        address: `${httpOrigin}/mcp/smart/sse`,
        desc: '智能模式 SSE 端点。',
        clientType: 'sse',
        guideDesc: '智能模式 SSE 传输端点。',
      },
      {
        key: 'http',
        name: 'Streamable HTTP',
        badge: '推荐',
        address: `${httpOrigin}/mcp/smart/http`,
        desc: '智能模式 HTTP 端点。',
        clientType: 'streamable-http',
        guideDesc: '智能模式 Streamable HTTP 端点，适合大多数客户端。',
      },
      {
        key: 'ws',
        name: 'WebSocket',
        badge: '长连接',
        address: `${wsOrigin}/mcp/smart/ws`,
        desc: '智能模式 WebSocket 端点。',
        clientType: 'websocket',
        guideDesc: '智能模式 WebSocket 端点。',
      },
    ]
  }
  return [
    {
      key: 'sse',
      name: 'SSE',
      badge: '事件流',
      address: `${httpOrigin}/mcp/sse`,
      desc: '适合仍使用 SSE 传输的 MCP 客户端。',
      clientType: 'sse',
      guideDesc: '兼容旧版或仍以事件流建立会话的客户端，适合需要保持服务端推送通道的场景。',
    },
    {
      key: 'http',
      name: 'Streamable HTTP',
      badge: '推荐',
      address: `${httpOrigin}/mcp/http`,
      desc: '适合支持新版 HTTP 传输的客户端。',
      clientType: 'streamable-http',
      guideDesc: '当前最推荐的远程 MCP 接入方式，配置简单，适合绝大多数支持新版 MCP 传输的客户端。',
    },
    {
      key: 'ws',
      name: 'WebSocket',
      badge: '长连接',
      address: `${wsOrigin}/mcp/ws`,
      desc: '适合需要稳定双向通道的客户端。',
      clientType: 'websocket',
      guideDesc: '适合需要长连接和双向通信的客户端，浏览器直连时建议使用查询参数认证。',
    },
  ]
})
const selectedEndpoint = computed<EndpointItem>(
  () => guideEndpoints.value.find((item) => item.key === selectedGuideEndpoint.value) ?? guideEndpoints.value[0]!,
)
const selectedAuth = computed<AuthOption>(
  () => authOptions.find((item) => item.key === selectedGuideAuth.value) ?? authOptions[0]!,
)
const guideAddress = computed(() => {
  if (selectedGuideAuth.value !== 'query') return selectedEndpoint.value.address
  const separator = selectedEndpoint.value.address.includes('?') ? '&' : '?'
  return `${selectedEndpoint.value.address}${separator}api_key=<API Key>`
})
const guideAuthHeaders = computed<Record<string, string>>(() => {
  if (selectedGuideAuth.value === 'bearer') {
    return { Authorization: 'Bearer <API Key>' }
  }
  if (selectedGuideAuth.value === 'header') {
    return { 'X-API-Key': '<API Key>' }
  }
  return {} as Record<string, string>
})
const guideAuthText = computed(() => {
  const entries = Object.entries(guideAuthHeaders.value)
  if (entries.length === 0) return '已通过 URL 查询参数传递 API Key'
  return entries.map(([key, value]) => `${key}: ${value}`).join('\n')
})
const guideAddressHint = computed(() => {
  if (normalizeMCPOrigin(guideOrigin.value) === '') {
    return '当前使用占位符；可填入真实域名、IP 和端口，复制示例时会自动记住。'
  }
  return '已使用自定义访问地址生成下方示例，复制后会记住该地址。'
})
const guideSnippets = computed<GuideSnippet[]>(() => [
  {
    key: 'client-config',
    title: guideConfigKind.value === 'mcp-json' ? `${selectedPreset.value.label} 配置` : `${selectedPreset.value.label} 服务条目`,
    desc: guideConfigKind.value === 'mcp-json'
      ? selectedPreset.value.hint
      : '复制到已有服务列表或 mcpServers 对象中使用。',
    code: clientConfigSnippet(),
  },
  {
    key: selectedEndpoint.value.key === 'ws' ? 'wscat' : 'curl',
    title: selectedEndpoint.value.key === 'ws' ? '命令行调试' : 'cURL 调试',
    desc: selectedEndpoint.value.key === 'ws' ? '用 wscat 快速验证 WebSocket 通道。' : '用命令行快速验证连接和认证。',
    code: guideCommandSnippet(),
  },
  {
    key: 'javascript',
    title: selectedEndpoint.value.key === 'ws' ? 'JavaScript / Node.js' : 'JavaScript 示例',
    desc: javascriptSnippetDesc(),
    code: javascriptSnippet(),
  },
])
const xiaozhiStatusLabel = computed(() => {
  if (!settings.value?.xiaozhi.enabled) return '未启用'
  if (health.value?.xiaozhi?.connected) return '已连接'
  return '等待连接'
})
const xiaozhiStatusClass = computed(() => {
  if (!settings.value?.xiaozhi.enabled) {
    return 'bg-gray-100 text-gray-600 dark:bg-white/5 dark:text-gray-400'
  }
  if (health.value?.xiaozhi?.connected) {
    return 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
  }
  return 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
})
const systemStatusLabel = computed(() => {
  if (health.value === null) return '加载中'
  return health.value.status === 'ok' ? '运行正常' : '部分异常'
})
const systemStatusClass = computed(() =>
  health.value?.status === 'ok'
    ? 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
    : 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400',
)

function syncForm(cfg: YAMLConfig): void {
  xiaozhiEnabled.value = cfg.xiaozhi.enabled
  xiaozhiEndpoint.value = cfg.xiaozhi.endpoint
  xiaozhiMode.value = cfg.xiaozhi.mode || 'full'
}

function errorMessage(err: unknown): string {
  return err instanceof Error ? err.message : '数据加载失败，请稍后重试'
}

function clearFieldErrors(): void {
  formError.value = ''
  for (const key of Object.keys(fieldErrors)) {
    delete fieldErrors[key]
  }
}

function openGuide(): void {
  guideCopyError.value = ''
  guideCopiedKey.value = ''
  guideMode.value = endpointTab.value
  guideOriginHistory.value = loadMCPAccessHistory()
  if (guideOrigin.value.trim() === '') {
    guideOrigin.value = guideOriginHistory.value.origins[0] ?? inferCurrentOrigin()
  }
  guideOpen.value = true
}

function applyGuidePreset(key: MCPClientPresetKey): void {
  const preset = getMCPClientPreset(key)
  selectedGuidePreset.value = preset.key
  guideMode.value = preset.mode
  selectedGuideEndpoint.value = preset.endpoint
  selectedGuideAuth.value = preset.auth
  guideConfigKind.value = preset.configKind
  guideServerName.value = preset.serverName
}

function closeGuide(): void {
  guideOpen.value = false
  guideCopyError.value = ''
  guideCopiedKey.value = ''
}

function formatJSON(value: unknown): string {
  return JSON.stringify(value, null, 2)
}

function authHeadersJSON(): Record<string, string> | undefined {
  const headers = guideAuthHeaders.value
  return Object.keys(headers).length > 0 ? headers : undefined
}

function headerLines(): string[] {
  return Object.entries(guideAuthHeaders.value).map(([key, value]) => `-H "${key}: ${value}"`)
}

function shellLines(command: string, args: string[]): string {
  if (args.length === 0) return command
  return [command, ...args].join(' \\' + '\n  ')
}

function inferCurrentOrigin(): string {
  if (typeof window === 'undefined') return ''
  return window.location.origin
}

function rememberGuideOrigin(): void {
  const normalized = normalizeMCPOrigin(guideOrigin.value)
  if (normalized === '') return
  guideOriginHistory.value = markMCPOriginRecentlyUsed(guideOriginHistory.value, normalized)
  saveMCPAccessHistory(guideOriginHistory.value)
}

function useGuideOrigin(origin: string): void {
  guideOrigin.value = origin
}

function resetGuideOrigin(): void {
  guideOrigin.value = ''
}

function clientConfigSnippet(): string {
  return buildMCPClientConfig(
    {
      serverName: guideServerName.value,
      clientType: selectedEndpoint.value.clientType,
      url: guideAddress.value,
      headers: authHeadersJSON(),
    },
    guideConfigKind.value,
  )
}

function guideCommandSnippet(): string {
  if (selectedEndpoint.value.key === 'ws') {
    return shellLines(`wscat -c "${guideAddress.value}"`, headerLines())
  }

  if (selectedEndpoint.value.key === 'sse') {
    return shellLines(`curl -N "${guideAddress.value}"`, [
      '-H "Accept: text/event-stream"',
      ...headerLines(),
    ])
  }

  return shellLines(`curl -X POST "${guideAddress.value}"`, [
    '-H "Content-Type: application/json"',
    ...headerLines(),
    `-d '${JSON.stringify(jsonRPCPayload)}'`,
  ])
}

function javascriptSnippetDesc(): string {
  if (selectedEndpoint.value.key === 'sse' && selectedGuideAuth.value !== 'query') {
    return '浏览器 EventSource 不支持自定义 Header，此处使用 fetch 读取事件流。'
  }
  if (selectedEndpoint.value.key === 'ws' && selectedGuideAuth.value !== 'query') {
    return '浏览器 WebSocket 不支持自定义 Header，此处使用 Node.js ws 示例。'
  }
  return '适合在脚本或自定义客户端中快速验证。'
}

function javascriptSnippet(): string {
  if (selectedEndpoint.value.key === 'sse') {
    if (selectedGuideAuth.value === 'query') {
      return `const source = new EventSource('${guideAddress.value}')

source.onmessage = (event) => {
  console.log('message', event.data)
}

source.onerror = (error) => {
  console.error('sse error', error)
  source.close()
}`
    }

    return `const response = await fetch('${guideAddress.value}', {
  headers: ${formatJSON({ Accept: 'text/event-stream', ...guideAuthHeaders.value })},
})

if (!response.ok) throw new Error('SSE connection failed')

const reader = response.body?.getReader()
while (reader) {
  const { value, done } = await reader.read()
  if (done) break
  console.log(new TextDecoder().decode(value))
}`
  }

  if (selectedEndpoint.value.key === 'ws') {
    if (selectedGuideAuth.value === 'query') {
      return `const ws = new WebSocket('${guideAddress.value}')

ws.onopen = () => {
  ws.send(${JSON.stringify(JSON.stringify(jsonRPCPayload))})
}

ws.onmessage = (event) => {
  console.log('message', event.data)
}`
    }

    return `import WebSocket from 'ws'

const ws = new WebSocket('${guideAddress.value}', {
  headers: ${formatJSON(guideAuthHeaders.value)},
})

ws.on('open', () => {
  ws.send(${JSON.stringify(JSON.stringify(jsonRPCPayload))})
})

ws.on('message', (data) => {
  console.log(data.toString())
})`
  }

  return `const response = await fetch('${guideAddress.value}', {
  method: 'POST',
  headers: ${formatJSON({ 'Content-Type': 'application/json', ...guideAuthHeaders.value })},
  body: JSON.stringify(${formatJSON(jsonRPCPayload)}),
})

console.log(await response.json())`
}

async function copyGuideText(key: string, value: string): Promise<void> {
  guideCopyError.value = ''
  rememberGuideOrigin()
  const ok = await copy(value)
  if (!ok) {
    guideCopyError.value = '复制失败，请手动选择内容'
    toast.error('复制失败，请手动选择内容')
    return
  }
  guideCopiedKey.value = key
  window.setTimeout(() => {
    if (guideCopiedKey.value === key) guideCopiedKey.value = ''
  }, 1500)
}

async function loadAPIService(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const [snapshot, report, keys, tools] = await Promise.all([
      getSettingsSnapshot(),
      getHealth(),
      listAPIKeys(),
      getAggregatedTools(),
    ])
    const cfg = snapshot.settings
    settings.value = cfg
    runtimeServer.value = snapshot.runtime.server
    health.value = report
    apiKeyTotal.value = keys.length
    apiKeyEnabled.value = keys.filter((item) => item.enabled).length
    aggregatedTools.value = tools.tools
    aggregatedToolDetails.value = tools.toolDetails
    gatewayTools.value = tools.gatewayTools
    ensureSelectedAggregatedTool()
    syncForm(cfg)
  } catch (err) {
    loadError.value = errorMessage(err)
  } finally {
    loading.value = false
  }
}

async function refreshStatus(): Promise<void> {
  if (refreshing.value) return
  refreshing.value = true
  try {
    const [report, keys, tools] = await Promise.all([getHealth(), listAPIKeys(), getAggregatedTools()])
    health.value = report
    apiKeyTotal.value = keys.length
    apiKeyEnabled.value = keys.filter((item) => item.enabled).length
    aggregatedTools.value = tools.tools
    aggregatedToolDetails.value = tools.toolDetails
    gatewayTools.value = tools.gatewayTools
    ensureSelectedAggregatedTool()
  } catch (err) {
    toast.error(errorMessage(err))
  } finally {
    refreshing.value = false
  }
}

async function saveAPISettings(): Promise<void> {
  if (settings.value === null || saving.value) return
  clearFieldErrors()
  saving.value = true
  const next: YAMLConfig = {
    ...settings.value,
    xiaozhi: {
      enabled: xiaozhiEnabled.value,
      endpoint: xiaozhiEndpoint.value.trim(),
      mode: xiaozhiMode.value,
    },
  }
  try {
    settings.value = await updateSettings(next)
    syncForm(settings.value)
    await refreshStatus()
    toast.success('API 服务设置已保存')
  } catch (err) {
    const body = extractAPIError(err)
    if (body?.fields) {
      for (const [key, value] of Object.entries(body.fields)) {
        fieldErrors[key] = value
      }
    }
    formError.value = body?.message ?? errorMessage(err)
  } finally {
    saving.value = false
  }
}

async function copyEndpoint(key: string, value: string): Promise<void> {
  // 与 copyGuideText 一致走 useClipboard：非安全上下文下需降级到 execCommand。
  const ok = await copy(value)
  if (!ok) {
    toast.error('复制失败，请手动选择地址')
    return
  }
  copiedKey.value = key
  window.setTimeout(() => {
    if (copiedKey.value === key) copiedKey.value = ''
  }, 1800)
}

function toolDescription(tool: ToolDef): string {
  const desc = tool.description?.trim() ?? ''
  if (desc !== '' && desc !== tool.name && desc !== tool.originalName) return desc
  return '上游未提供有效描述'
}

function ensureSelectedAggregatedTool(): void {
  if (aggregatedTools.value.length === 0 || selectedAggregatedToolName.value === '') {
    selectedAggregatedToolName.value = ''
    toolDetailOpen.value = false
    return
  }
  if (!aggregatedTools.value.some((tool) => tool.name === selectedAggregatedToolName.value)) {
    selectedAggregatedToolName.value = ''
    toolDetailOpen.value = false
  }
}

function openToolDetail(tool: ToolDef): void {
  selectedAggregatedToolName.value = tool.name
  toolDetailOpen.value = true
}

function closeToolDetail(): void {
  toolDetailOpen.value = false
}

function sourceCountText(tool: ToolDef): string {
  const count = tool.sourceCount ?? 1
  return `${count} 个来源上游`
}

function formatRateLimits(limits?: UpstreamRateLimits): string {
  if (!limits?.enabled) return '未配置'
  const parts: string[] = []
  if (limits.perSecond && limits.perSecond > 0) parts.push(`每秒 ${limits.perSecond}`)
  if (limits.perMinute && limits.perMinute > 0) parts.push(`每分钟 ${limits.perMinute}`)
  if (limits.perHour && limits.perHour > 0) parts.push(`每小时 ${limits.perHour}`)
  if (limits.perDay && limits.perDay > 0) parts.push(`每日 ${limits.perDay}`)
  if (limits.perWeek && limits.perWeek > 0) parts.push(`每周 ${limits.perWeek}`)
  if (limits.perMonth && limits.perMonth > 0) parts.push(`每月 ${limits.perMonth}`)
  return parts.length > 0 ? parts.join(' / ') : '已启用，未设置上限'
}

function schemaPreview(value: unknown): string {
  if (value === null || value === undefined || value === '') {
    return JSON.stringify({ type: 'object' }, null, 2)
  }
  if (typeof value === 'string') return value
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

function sourceStatusClass(source: ToolSource): string {
  if (!source.compatible) {
    return 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
  }
  if (source.temporarilyDegraded) {
    return 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
  }
  if (source.routingAvailable === false) {
    return 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'
  }
  return 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
}

function sourceStatusLabel(source: ToolSource): string {
  if (!source.compatible) return 'Schema 不一致'
  if (source.temporarilyDegraded) return '临时降级'
  if (source.routingAvailable === false) return '暂不路由'
  return '可参与路由'
}

function degradedSources(detail: ToolDetail): ToolSource[] {
  return (detail.sources ?? []).filter((source) => source.temporarilyDegraded)
}

function policyRiskTags(detail: ToolDetail): string[] {
  return detail.policy?.riskTags ?? []
}

function degradationText(source: ToolSource): string {
  const reason = source.degradationReason?.trim() || '该来源近期连续失败，网关会优先尝试其他健康来源。'
  if (!source.degradationUntil) return reason
  const until = new Date(source.degradationUntil)
  if (Number.isNaN(until.getTime())) return reason
  return `${reason} 预计 ${until.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })} 后恢复尝试。`
}

onMounted(loadAPIService)

const cardClass =
  'rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]'
const inputClass =
  'h-11 w-full rounded-lg border border-gray-300 bg-transparent px-4 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30'
const labelClass = 'mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-400'
const hintClass = 'mt-1 text-xs text-gray-400 dark:text-gray-500'
const errClass = 'mt-1 text-xs text-error-500'
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="API 服务" />

    <div
      v-if="loadError !== ''"
      class="mb-6 flex items-center justify-between gap-3 rounded-lg bg-error-50 px-4 py-3 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
    >
      <span>{{ loadError }}</span>
      <button
        type="button"
        class="rounded-md border border-error-200 px-3 py-1 text-xs font-medium hover:bg-error-100 dark:border-error-500/30 dark:hover:bg-error-500/10"
        @click="loadAPIService"
      >
        重试
      </button>
    </div>

    <section :class="cardClass" class="mb-6">
      <div class="flex flex-wrap items-start justify-between gap-4">
        <div>
          <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">服务概览</h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            外部客户端接入、真实工具与第三方连接状态集中在这里查看和调整。全量模式与智能模式可同时使用。
          </p>
        </div>
        <button
          v-tooltip:bottom-end="'刷新状态'"
          type="button"
          class="inline-flex h-10 w-10 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 disabled:opacity-60 dark:border-gray-800 dark:text-gray-400 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400"
          :disabled="loading || refreshing"
          aria-label="刷新状态"
          @click="refreshStatus"
        >
          <RefreshIcon class="h-5 w-5" :class="refreshing ? 'animate-spin' : ''" />
        </button>
      </div>

      <div class="mt-4 grid grid-cols-1 gap-3 md:grid-cols-2 2xl:grid-cols-4">
        <div class="flex min-h-20 items-center gap-3 rounded-xl border border-gray-100 bg-gray-50 px-4 py-3 dark:border-gray-800 dark:bg-white/[0.02]">
          <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400">
            <BoxCubeIcon class="h-5 w-5" />
          </span>
          <div class="min-w-0 flex-1">
            <p class="text-xs text-gray-400 dark:text-gray-500">系统状态</p>
            <p class="mt-0.5 truncate text-sm font-semibold text-gray-800 dark:text-white/90">{{ systemStatusLabel }}</p>
            <span class="mt-1 inline-flex rounded-full px-2 py-0.5 text-xs font-medium" :class="systemStatusClass">
              {{ dependencyOK }}/{{ health?.dependencies.length ?? 0 }} 依赖正常
            </span>
          </div>
        </div>
        <div class="flex min-h-20 items-center gap-3 rounded-xl border border-gray-100 bg-gray-50 px-4 py-3 dark:border-gray-800 dark:bg-white/[0.02]">
          <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-success-50 text-success-600 dark:bg-success-500/10 dark:text-success-400">
            <PlugInIcon class="h-5 w-5" />
          </span>
          <div class="min-w-0 flex-1">
            <p class="text-xs text-gray-400 dark:text-gray-500">上游连接</p>
            <p class="mt-0.5 truncate text-sm font-semibold text-gray-800 dark:text-white/90">
              {{ availableUpstreams }}/{{ enabledUpstreams }} 可用
            </p>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">已启用上游</p>
          </div>
        </div>
        <div class="flex min-h-20 items-center gap-3 rounded-xl border border-gray-100 bg-gray-50 px-4 py-3 dark:border-gray-800 dark:bg-white/[0.02]">
          <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-warning-50 text-warning-600 dark:bg-warning-500/10 dark:text-warning-400">
            <UserGroupIcon class="h-5 w-5" />
          </span>
          <div class="min-w-0 flex-1">
            <p class="text-xs text-gray-400 dark:text-gray-500">API Key</p>
            <p class="mt-0.5 truncate text-sm font-semibold text-gray-800 dark:text-white/90">
              {{ apiKeyEnabled }}/{{ apiKeyTotal }} 可用
            </p>
            <router-link to="/apikeys" class="mt-1 inline-flex text-xs font-medium text-brand-600 hover:text-brand-700 dark:text-brand-400">
              管理密钥
            </router-link>
          </div>
        </div>
        <div class="flex min-h-20 items-center gap-3 rounded-xl border border-gray-100 bg-gray-50 px-4 py-3 dark:border-gray-800 dark:bg-white/[0.02]">
          <span class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-blue-light-50 text-blue-light-700 dark:bg-blue-light-500/10 dark:text-blue-light-400">
            <InfoCircleIcon class="h-5 w-5" />
          </span>
          <div class="min-w-0 flex-1">
            <p class="text-xs text-gray-400 dark:text-gray-500">真实工具</p>
            <p class="mt-0.5 truncate text-sm font-semibold text-gray-800 dark:text-white/90">{{ aggregatedToolCount }} 个</p>
            <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">智能模式另含 {{ gatewayTools.length }} 个网关工具</p>
          </div>
        </div>
      </div>
    </section>

    <div class="grid grid-cols-1 gap-6 3xl:grid-cols-[minmax(0,1.25fr)_minmax(360px,0.75fr)]">
      <section :class="cardClass">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">对外接口地址</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              全量模式与智能模式各自拥有独立端点，可同时使用。外部 MCP 客户端使用这些地址连接本系统。
            </p>
          </div>
          <div class="flex items-center gap-3">
            <button
              type="button"
              class="inline-flex items-center justify-center gap-2 rounded-lg bg-brand-500 px-3.5 py-2 text-sm font-medium text-white transition hover:bg-brand-600"
              @click="openGuide"
            >
              <DocsIcon class="h-4 w-4" />
              对接引导
            </button>
          </div>
        </div>

        <div class="mt-4 rounded-lg border border-gray-200 bg-gray-50 p-3 dark:border-gray-800 dark:bg-white/[0.02]">
          <p class="text-xs leading-5 text-gray-500 dark:text-gray-400">
            下方地址为占位符。<span class="font-mono text-gray-700 dark:text-gray-200">http(s)</span> 按是否启用 TLS 选择 http 或 https，<span class="font-mono text-gray-700 dark:text-gray-200">&lt;your-host&gt;</span> 填域名或 IP，<span class="font-mono text-gray-700 dark:text-gray-200">&lt;port&gt;</span> 填对外映射端口。网关部署在容器中，无法预知你的真实网络环境，请按实际可达地址替换。
          </p>
        </div>

        <div class="mt-4 flex gap-1 rounded-lg border border-gray-200 bg-gray-50 p-1 dark:border-gray-800 dark:bg-white/[0.03]">
          <button
            type="button"
            class="flex-1 rounded-md px-3 py-2 text-sm font-medium transition"
            :class="endpointTab === 'full' ? 'bg-white text-gray-800 shadow-sm dark:bg-gray-800 dark:text-white/90' : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300'"
            @click="endpointTab = 'full'"
          >
            全量模式
          </button>
          <button
            type="button"
            class="flex-1 rounded-md px-3 py-2 text-sm font-medium transition"
            :class="endpointTab === 'smart' ? 'bg-white text-gray-800 shadow-sm dark:bg-gray-800 dark:text-white/90' : 'text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-300'"
            @click="endpointTab = 'smart'"
          >
            智能模式
            <span class="ml-1 text-xs text-gray-400">（分步发现）</span>
          </button>
        </div>

        <div v-if="endpointTab === 'smart'" class="mt-3 rounded-lg border border-warning-200 bg-warning-50/80 p-3 dark:border-warning-500/30 dark:bg-warning-500/10">
          <p class="text-xs leading-5 text-warning-700 dark:text-warning-300">
            智能模式通过 4 个网关工具（list_tools / search_tools / get_tool / call_tool）按需发现和调用真实工具，减少客户端工具噪音和上下文占用。搜索支持多个关键词、常见缩写和上游来源筛选；无结果时会给出下一步建议。但部分模型可能不支持分步骤获取工具信息的交互模式，导致无法正确使用网关工具。
          </p>
        </div>

        <div class="mt-4 grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
          <article
            v-for="item in activeEndpoints"
            :key="item.key"
            class="rounded-xl border border-gray-200 p-4 dark:border-gray-800 dark:bg-white/[0.02]"
          >
            <div class="flex items-start justify-between gap-3">
              <div>
                <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">{{ item.name }}</h4>
                <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ item.desc }}</p>
              </div>
              <span class="shrink-0 rounded-full bg-gray-100 px-2.5 py-1 text-xs font-medium text-gray-600 dark:bg-white/5 dark:text-gray-400">
                {{ item.badge }}
              </span>
            </div>

            <div class="mt-4 flex items-center gap-2 rounded-lg bg-gray-50 p-2.5 dark:bg-gray-900/60">
              <p class="min-w-0 flex-1 break-all font-mono text-xs leading-5 text-gray-700 dark:text-gray-200">
                {{ item.address }}
              </p>
              <button
                v-tooltip:bottom="copiedKey === item.key ? '已复制' : '复制地址'"
                type="button"
                class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-gray-200 bg-white text-gray-600 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 dark:border-gray-800 dark:bg-white/[0.03] dark:text-gray-300 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400"
                :aria-label="`复制 ${item.name} 地址`"
                @click="copyEndpoint(item.key, item.address)"
              >
                <CheckIcon v-if="copiedKey === item.key" class="h-4 w-4" />
                <DocsIcon v-else class="h-4 w-4" />
              </button>
            </div>
          </article>
        </div>

        <div :class="mcpPortNoticeClass">
          <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
            <div class="min-w-0">
              <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">监听模式</h4>
              <p class="mt-1 text-xs leading-5 text-gray-600 dark:text-gray-300">{{ mcpPortSecurityText }}</p>
            </div>
            <div class="flex shrink-0 items-center gap-2">
              <span :class="mcpPortBadgeClass">{{ mcpPortModeLabel }}</span>
              <router-link
                to="/settings"
                :class="mcpPortSettingsLinkClass"
              >
                去设置
              </router-link>
            </div>
          </div>
        </div>

        <div class="mt-5 rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
          <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">鉴权方式</h4>
          <div class="mt-3 grid grid-cols-1 gap-3 lg:grid-cols-3">
            <div class="rounded-lg bg-white p-3 dark:bg-gray-900/60">
              <p class="text-xs text-gray-400 dark:text-gray-500">请求头</p>
              <p class="mt-1 break-all font-mono text-xs text-gray-700 dark:text-gray-200">X-API-Key: &lt;API Key&gt;</p>
            </div>
            <div class="rounded-lg bg-white p-3 dark:bg-gray-900/60">
              <p class="text-xs text-gray-400 dark:text-gray-500">Bearer</p>
              <p class="mt-1 break-all font-mono text-xs text-gray-700 dark:text-gray-200">Authorization: Bearer &lt;API Key&gt;</p>
            </div>
            <div class="rounded-lg bg-white p-3 dark:bg-gray-900/60">
              <p class="text-xs text-gray-400 dark:text-gray-500">查询参数</p>
              <p class="mt-1 break-all font-mono text-xs text-gray-700 dark:text-gray-200">?api_key=&lt;API Key&gt;</p>
            </div>
          </div>
        </div>

        <div class="mt-5 space-y-4">
          <div
            v-if="endpointTab === 'smart'"
            class="rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]"
          >
            <div class="flex items-start justify-between gap-3">
              <div>
                <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">客户端可见工具</h4>
                <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  智能模式只向客户端暴露网关工具，真实工具按需发现和调用。
                </p>
              </div>
              <span class="shrink-0 rounded-full bg-brand-50 px-2.5 py-1 text-xs font-medium text-brand-600 dark:bg-brand-500/10 dark:text-brand-400">
                智能模式
              </span>
            </div>
            <div class="custom-scrollbar mt-4 grid max-h-80 grid-cols-1 gap-2 overflow-y-auto pr-1 md:grid-cols-2">
              <div
                v-for="tool in gatewayTools"
                :key="tool.name"
                class="rounded-lg bg-white p-3 dark:bg-gray-900/60"
              >
                <p class="truncate font-mono text-xs font-medium text-gray-800 dark:text-white/90">
                  {{ tool.name }}
                </p>
                <p class="mt-1 line-clamp-2 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  {{ tool.description || '暂无描述' }}
                </p>
              </div>
              <p
                v-if="gatewayTools.length === 0"
                class="py-4 text-center text-sm text-gray-400"
              >
                暂无工具
              </p>
            </div>
          </div>

          <div class="rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
            <div class="flex items-start justify-between gap-3">
              <div>
                <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">真实聚合工具</h4>
                <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  已经过上游启停、别名和过滤规则处理后的工具集合。
                </p>
              </div>
              <span class="shrink-0 rounded-full bg-success-50 px-2.5 py-1 text-xs font-medium text-success-700 dark:bg-success-500/10 dark:text-success-400">
                {{ aggregatedToolCount }} 个
              </span>
            </div>
            <div class="custom-scrollbar mt-4 grid max-h-[30rem] grid-cols-1 gap-2 overflow-y-auto pr-1 md:grid-cols-2 2xl:grid-cols-3">
              <button
                v-for="tool in aggregatedTools"
                :key="tool.name"
                type="button"
                class="rounded-lg border border-transparent bg-white p-3 text-left transition hover:border-brand-200 hover:bg-brand-50/50 focus:border-brand-300 focus:ring-2 focus:ring-brand-500/10 focus:outline-none dark:bg-gray-900/60 dark:hover:border-brand-500/30 dark:hover:bg-brand-500/[0.06]"
                :aria-label="`查看 ${tool.name} 工具详情`"
                @click="openToolDetail(tool)"
              >
                <span class="flex items-start justify-between gap-3">
                  <span class="min-w-0 truncate font-mono text-xs font-medium text-gray-800 dark:text-white/90">
                    {{ tool.name }}
                  </span>
                  <span
                    v-if="(tool.sourceCount ?? 1) > 1"
                    class="shrink-0 rounded-full bg-brand-50 px-2 py-0.5 text-[11px] font-medium text-brand-600 dark:bg-brand-500/10 dark:text-brand-400"
                  >
                    {{ sourceCountText(tool) }}
                  </span>
                </span>
                <span class="mt-1 line-clamp-2 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  {{ toolDescription(tool) }}
                </span>
                <span
                  v-if="tool.schemaConflict"
                  class="mt-2 inline-flex rounded-full bg-warning-50 px-2 py-0.5 text-[11px] font-medium text-warning-700 dark:bg-warning-500/10 dark:text-warning-400"
                >
                  Schema 不一致
                </span>
              </button>
              <p v-if="aggregatedTools.length === 0" class="py-8 text-center text-sm text-gray-400 md:col-span-2 2xl:col-span-3">
                暂无可用工具
              </p>
            </div>
          </div>
        </div>
      </section>

      <section :class="cardClass">
        <form class="flex h-full flex-col" @submit.prevent="saveAPISettings">
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">第三方对接</h3>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                管理面向特定客户端或平台的扩展接入能力。
              </p>
            </div>
          </div>

          <p
            v-if="formError !== ''"
            class="mt-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
          >
            {{ formError }}
          </p>

          <div class="mt-5 rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div class="min-w-0">
                <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">小智接入</h4>
                <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  为小智客户端提供独立接入点，连接状态会随网关健康检查刷新。
                </p>
              </div>
              <span class="rounded-full px-2.5 py-1 text-xs font-medium" :class="xiaozhiStatusClass">
                {{ xiaozhiStatusLabel }}
              </span>
            </div>

            <div class="mt-5 flex flex-col gap-5">
              <div class="flex items-center justify-between gap-4 rounded-lg bg-white p-3 dark:bg-gray-900/60">
                <div>
                  <label :class="labelClass">启用小智接入</label>
                  <p class="text-xs text-gray-400 dark:text-gray-500">保存后立即应用到当前网关进程。</p>
                </div>
                <button
                  type="button"
                  role="switch"
                  :aria-checked="xiaozhiEnabled"
                  class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition"
                  :class="xiaozhiEnabled ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
                  @click="xiaozhiEnabled = !xiaozhiEnabled"
                >
                  <span
                    class="inline-block h-4 w-4 transform rounded-full bg-white transition"
                    :class="xiaozhiEnabled ? 'translate-x-6' : 'translate-x-1'"
                  ></span>
                </button>
              </div>

              <div>
                <label :class="labelClass">接入点地址</label>
                <input
                  v-model="xiaozhiEndpoint"
                  type="text"
                  placeholder="wss://example.com/mcp"
                  :disabled="!xiaozhiEnabled"
                  :class="[inputClass, !xiaozhiEnabled ? 'opacity-60' : '']"
                />
                <p :class="hintClass">启用时需填写 ws:// 或 wss:// 地址。</p>
                <p v-if="fieldErrors['xiaozhi.endpoint']" :class="errClass">
                  {{ fieldErrors['xiaozhi.endpoint'] }}
                </p>
              </div>

              <div>
                <label :class="labelClass">对接模式</label>
                <div class="mt-1 flex gap-1 rounded-lg border border-gray-200 bg-gray-50 p-1 dark:border-gray-700 dark:bg-gray-800/50">
                  <button
                    type="button"
                    class="flex-1 rounded-md px-3 py-2 text-sm font-medium transition"
                    :class="xiaozhiMode === 'full' ? 'bg-white text-gray-800 shadow-sm dark:bg-gray-700 dark:text-white/90' : 'text-gray-500 hover:text-gray-700 dark:text-gray-400'"
                    :disabled="!xiaozhiEnabled"
                    @click="xiaozhiMode = 'full'"
                  >
                    全量模式
                  </button>
                  <button
                    type="button"
                    class="flex-1 rounded-md px-3 py-2 text-sm font-medium transition"
                    :class="xiaozhiMode === 'smart' ? 'bg-white text-gray-800 shadow-sm dark:bg-gray-700 dark:text-white/90' : 'text-gray-500 hover:text-gray-700 dark:text-gray-400'"
                    :disabled="!xiaozhiEnabled"
                    @click="xiaozhiMode = 'smart'"
                  >
                    智能模式
                  </button>
                </div>
                <p :class="hintClass">
                  {{ xiaozhiMode === 'smart' ? '通过网关工具按需发现和调用，减少工具噪音。' : '直接暴露全部聚合工具，兼容性好。' }}
                </p>
                <p v-if="fieldErrors['xiaozhi.mode']" :class="errClass">
                  {{ fieldErrors['xiaozhi.mode'] }}
                </p>
              </div>

              <div class="rounded-lg bg-white p-3 dark:bg-gray-900/60">
                <p class="text-xs text-gray-400 dark:text-gray-500">当前连接</p>
                <p class="mt-1 break-all text-sm font-medium text-gray-800 dark:text-white/90">
                  {{ health?.xiaozhi?.endpoint || settings?.xiaozhi.endpoint || '未配置' }}
                </p>
              </div>

              <button
                type="submit"
                class="self-end rounded-lg bg-brand-500 px-5 py-2.5 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-60"
                :disabled="saving || settings === null"
              >
                {{ saving ? '保存中…' : '保存小智接入' }}
              </button>
            </div>
          </div>
        </form>
      </section>
    </div>

    <transition name="fade">
      <div
        v-if="toolDetailOpen && selectedToolDetail"
        class="fixed inset-0 z-[100000] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
      >
        <div
          class="flex max-h-[88vh] w-full max-w-4xl flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-gray-800 dark:bg-gray-900"
          role="dialog"
          aria-modal="true"
        >
          <div class="flex items-start justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-gray-800">
            <div class="min-w-0">
              <p class="truncate font-mono text-base font-semibold text-gray-800 dark:text-white/90">
                {{ selectedToolDetail.tool.name }}
              </p>
              <p class="mt-1 text-sm leading-6 text-gray-500 dark:text-gray-400">
                {{ toolDescription(selectedToolDetail.tool) }}
              </p>
            </div>
            <button
              type="button"
              class="shrink-0 rounded-lg p-2 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-800"
              aria-label="关闭工具详情"
              @click="closeToolDetail"
            >
              <span class="block text-xl leading-none">×</span>
            </button>
          </div>

          <div class="custom-scrollbar min-h-0 flex-1 overflow-y-auto p-5">
            <div class="flex flex-wrap items-center gap-2">
              <span class="rounded-full bg-success-50 px-2.5 py-1 text-xs font-medium text-success-700 dark:bg-success-500/10 dark:text-success-400">
                {{ selectedToolDetail.sources?.length ?? 0 }} 个来源上游
              </span>
              <span
                v-if="selectedToolDetail.tool.schemaConflict"
                class="rounded-full bg-warning-50 px-2.5 py-1 text-xs font-medium text-warning-700 dark:bg-warning-500/10 dark:text-warning-400"
              >
                Schema 不一致
              </span>
              <span
                v-if="degradedSources(selectedToolDetail).length > 0"
                class="rounded-full bg-warning-50 px-2.5 py-1 text-xs font-medium text-warning-700 dark:bg-warning-500/10 dark:text-warning-400"
              >
                {{ degradedSources(selectedToolDetail).length }} 个来源降级
              </span>
              <span
                v-for="tag in policyRiskTags(selectedToolDetail)"
                :key="tag"
                class="rounded-full bg-warning-50 px-2.5 py-1 text-xs font-medium text-warning-700 dark:bg-warning-500/10 dark:text-warning-300"
              >
                {{ tag }}
              </span>
            </div>

            <div
              v-if="selectedToolDetail.tool.schemaConflict"
              class="mt-4 rounded-lg border border-warning-200 bg-warning-50 px-3 py-2 text-xs leading-5 text-warning-700 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-300"
            >
              同名来源的入参 Schema 不完全一致，调用时只会选择与当前展示 Schema 一致的来源。
            </div>

            <div
              v-if="degradedSources(selectedToolDetail).length > 0"
              class="mt-4 rounded-lg border border-warning-200 bg-warning-50 px-3 py-2 text-xs leading-5 text-warning-700 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-300"
            >
              部分来源近期连续失败，网关正在优先选择其他健康来源；冷却结束后会自动恢复尝试。
            </div>

            <div class="mt-4 grid grid-cols-1 gap-3 md:grid-cols-2">
              <div
                v-for="source in selectedToolDetail.sources ?? []"
                :key="`${source.upstreamId}:${source.originalName}`"
                class="rounded-lg border border-gray-100 bg-gray-50 p-3 dark:border-gray-800 dark:bg-white/[0.02]"
              >
                <div class="flex flex-wrap items-start justify-between gap-2">
                  <div class="min-w-0">
                    <p class="truncate text-sm font-medium text-gray-800 dark:text-white/90">
                      {{ source.upstreamName || source.upstreamId }}
                    </p>
                    <p class="mt-1 truncate font-mono text-[11px] text-gray-400 dark:text-gray-500">
                      {{ source.originalName }}
                    </p>
                  </div>
                  <span class="rounded-full px-2 py-0.5 text-[11px] font-medium" :class="sourceStatusClass(source)">
                    {{ sourceStatusLabel(source) }}
                  </span>
                </div>
                <p class="mt-2 line-clamp-3 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  {{ source.description || '上游未提供有效描述' }}
                </p>
                <p class="mt-2 text-[11px] leading-5 text-gray-400 dark:text-gray-500">
                  限流额度：{{ formatRateLimits(source.rateLimits) }}
                </p>
                <p
                  v-if="source.temporarilyDegraded"
                  class="mt-2 rounded-md bg-warning-50 px-2 py-1 text-[11px] leading-5 text-warning-700 dark:bg-warning-500/10 dark:text-warning-300"
                >
                  {{ degradationText(source) }}
                </p>
              </div>
            </div>

            <details class="mt-4 rounded-lg bg-gray-50 p-3 dark:bg-white/[0.03]">
              <summary class="cursor-pointer text-xs font-medium text-gray-700 dark:text-gray-300">
                查看入参 Schema
              </summary>
              <pre class="custom-scrollbar mt-3 max-h-72 overflow-auto text-xs leading-5 text-gray-600 dark:text-gray-300">{{ schemaPreview(selectedToolDetail.tool.inputSchema) }}</pre>
            </details>
          </div>
        </div>
      </div>
    </transition>

    <transition name="fade">
      <div
        v-if="guideOpen"
        class="fixed inset-0 z-[100001] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
      >
        <div
          class="flex max-h-[88vh] w-full max-w-5xl flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-gray-800 dark:bg-gray-900"
        >
          <div class="flex items-start justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-gray-800">
            <div>
              <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">对接引导</h3>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">选择接口和认证方式，复制即可接入。</p>
            </div>
            <button
              type="button"
              class="rounded-lg p-2 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-800"
              aria-label="关闭对接引导"
              @click="closeGuide"
            >
              <span class="block text-xl leading-none">×</span>
            </button>
          </div>

          <div class="custom-scrollbar min-h-0 flex-1 overflow-y-auto p-5">
            <div class="grid grid-cols-1 gap-5 lg:grid-cols-[minmax(0,0.9fr)_minmax(0,1.1fr)]">
              <div class="space-y-5">
                <div>
                  <div class="mb-3 flex items-center gap-2">
                    <span class="flex h-6 w-6 items-center justify-center rounded-full bg-brand-50 text-xs font-semibold text-brand-600 dark:bg-brand-500/10 dark:text-brand-400">1</span>
                    <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">客户端预设</h4>
                  </div>
                  <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
                    <button
                      v-for="preset in guidePresetOptions"
                      :key="preset.key"
                      type="button"
                      class="rounded-xl border p-3 text-left transition"
                      :class="selectedGuidePreset === preset.key
                        ? 'border-brand-300 bg-brand-50/60 dark:border-brand-500/50 dark:bg-brand-500/[0.08]'
                        : 'border-gray-200 hover:border-brand-300 hover:bg-brand-50/40 dark:border-gray-800 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.06]'"
                      @click="applyGuidePreset(preset.key)"
                    >
                      <span class="flex items-center justify-between gap-2">
                        <span class="truncate text-sm font-semibold text-gray-800 dark:text-white/90">{{ preset.label }}</span>
                        <span class="shrink-0 rounded-full bg-gray-100 px-2 py-0.5 text-[11px] text-gray-600 dark:bg-white/5 dark:text-gray-400">{{ preset.badge }}</span>
                      </span>
                      <span class="mt-1.5 block line-clamp-2 text-xs leading-5 text-gray-500 dark:text-gray-400">{{ preset.desc }}</span>
                    </button>
                  </div>
                </div>

                <div>
                  <div class="mb-3 flex items-center gap-2">
                    <span class="flex h-6 w-6 items-center justify-center rounded-full bg-brand-50 text-xs font-semibold text-brand-600 dark:bg-brand-500/10 dark:text-brand-400">2</span>
                    <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">访问地址</h4>
                  </div>
                  <div class="rounded-xl border border-gray-200 p-4 dark:border-gray-800">
                    <label class="block">
                      <span class="mb-1.5 block text-xs font-medium text-gray-500 dark:text-gray-400">公网或局域网 Base URL</span>
                      <input
                        v-model="guideOrigin"
                        type="text"
                        class="h-10 w-full rounded-lg border border-gray-300 bg-white px-3 font-mono text-sm text-gray-800 shadow-sm transition placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
                        placeholder="https://mcp.example.com 或 http://127.0.0.1:8080"
                      />
                    </label>
                    <p class="mt-2 text-[11px] leading-5 text-gray-400 dark:text-gray-500">
                      {{ guideAddressHint }}
                    </p>
                    <div v-if="guideOriginHistory.origins.length > 0" class="mt-3 flex flex-wrap gap-2">
                      <button
                        v-for="origin in guideOriginHistory.origins"
                        :key="origin"
                        type="button"
                        class="max-w-full truncate rounded-full border border-gray-200 px-2.5 py-1 font-mono text-[11px] text-gray-600 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 dark:border-gray-800 dark:text-gray-300 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400"
                        @click="useGuideOrigin(origin)"
                      >
                        {{ origin }}
                      </button>
                    </div>
                    <div class="mt-3 flex flex-wrap items-center justify-between gap-2">
                      <p class="min-w-0 break-all font-mono text-[11px] text-gray-500 dark:text-gray-400">
                        当前：{{ guideOriginDisplay }}
                      </p>
                      <button
                        type="button"
                        class="text-xs font-medium text-brand-600 hover:text-brand-700 dark:text-brand-400"
                        @click="resetGuideOrigin"
                      >
                        使用占位符
                      </button>
                    </div>
                  </div>
                </div>

                <div>
                  <div class="mb-3 flex items-center gap-2">
                    <span class="flex h-6 w-6 items-center justify-center rounded-full bg-brand-50 text-xs font-semibold text-brand-600 dark:bg-brand-500/10 dark:text-brand-400">3</span>
                    <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">服务模式</h4>
                  </div>
                  <div class="grid grid-cols-1 gap-3">
                    <button
                      type="button"
                      class="rounded-xl border p-4 text-left transition"
                      :class="guideMode === 'full' ? 'border-brand-300 bg-brand-50/60 dark:border-brand-500/50 dark:bg-brand-500/[0.08]' : 'border-gray-200 hover:border-brand-300 hover:bg-brand-50/40 dark:border-gray-800 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.06]'"
                      @click="guideMode = 'full'"
                    >
                      <span class="flex items-center justify-between gap-3">
                        <span class="text-sm font-semibold text-gray-800 dark:text-white/90">全量模式</span>
                        <span class="rounded-full bg-success-50 px-2 py-0.5 text-xs text-success-700 dark:bg-success-500/10 dark:text-success-400">推荐</span>
                      </span>
                      <span class="mt-2 block text-xs leading-5 text-gray-500 dark:text-gray-400">直接暴露全部聚合工具，兼容性好，所有模型均可使用。</span>
                    </button>
                    <button
                      type="button"
                      class="rounded-xl border p-4 text-left transition"
                      :class="guideMode === 'smart' ? 'border-brand-300 bg-brand-50/60 dark:border-brand-500/50 dark:bg-brand-500/[0.08]' : 'border-gray-200 hover:border-brand-300 hover:bg-brand-50/40 dark:border-gray-800 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.06]'"
                      @click="guideMode = 'smart'"
                    >
                      <span class="flex items-center justify-between gap-3">
                        <span class="text-sm font-semibold text-gray-800 dark:text-white/90">智能模式</span>
                        <span class="rounded-full bg-warning-50 px-2 py-0.5 text-xs text-warning-700 dark:bg-warning-500/10 dark:text-warning-400">分步发现</span>
                      </span>
                      <span class="mt-2 block text-xs leading-5 text-gray-500 dark:text-gray-400">通过 4 个网关工具按需发现和调用。减少工具噪音，但部分模型可能不支持分步骤交互。</span>
                    </button>
                  </div>
                </div>

                <div>
                  <div class="mb-3 flex items-center gap-2">
                    <span class="flex h-6 w-6 items-center justify-center rounded-full bg-brand-50 text-xs font-semibold text-brand-600 dark:bg-brand-500/10 dark:text-brand-400">4</span>
                    <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">接口方式</h4>
                  </div>
                  <div class="grid grid-cols-1 gap-3">
                    <button
                      v-for="item in guideEndpoints"
                      :key="item.key"
                      type="button"
                      class="rounded-xl border p-4 text-left transition"
                      :class="
                        selectedGuideEndpoint === item.key
                          ? 'border-brand-300 bg-brand-50/60 dark:border-brand-500/50 dark:bg-brand-500/[0.08]'
                          : 'border-gray-200 hover:border-brand-300 hover:bg-brand-50/40 dark:border-gray-800 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.06]'
                      "
                      @click="selectedGuideEndpoint = item.key"
                    >
                      <span class="flex items-center justify-between gap-3">
                        <span class="font-mono text-sm font-semibold text-gray-800 dark:text-white/90">{{ item.name }}</span>
                        <span class="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-600 dark:bg-white/5 dark:text-gray-400">{{ item.badge }}</span>
                      </span>
                      <span class="mt-2 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{ item.guideDesc }}</span>
                    </button>
                  </div>
                </div>

                <div>
                  <div class="mb-3 flex items-center gap-2">
                    <span class="flex h-6 w-6 items-center justify-center rounded-full bg-brand-50 text-xs font-semibold text-brand-600 dark:bg-brand-500/10 dark:text-brand-400">5</span>
                    <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">认证方式</h4>
                  </div>
                  <div class="grid grid-cols-1 gap-3">
                    <button
                      v-for="item in authOptions"
                      :key="item.key"
                      type="button"
                      class="rounded-xl border p-4 text-left transition"
                      :class="
                        selectedGuideAuth === item.key
                          ? 'border-brand-300 bg-brand-50/60 dark:border-brand-500/50 dark:bg-brand-500/[0.08]'
                          : 'border-gray-200 hover:border-brand-300 hover:bg-brand-50/40 dark:border-gray-800 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.06]'
                      "
                      @click="selectedGuideAuth = item.key"
                    >
                      <span class="flex items-center justify-between gap-3">
                        <span class="text-sm font-semibold text-gray-800 dark:text-white/90">{{ item.label }}</span>
                        <span class="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-600 dark:bg-white/5 dark:text-gray-400">{{ item.badge }}</span>
                      </span>
                      <span class="mt-2 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{ item.desc }}</span>
                    </button>
                  </div>
                </div>

                <div>
                  <div class="mb-3 flex items-center gap-2">
                    <span class="flex h-6 w-6 items-center justify-center rounded-full bg-brand-50 text-xs font-semibold text-brand-600 dark:bg-brand-500/10 dark:text-brand-400">6</span>
                    <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">配置生成</h4>
                  </div>
                  <div class="rounded-xl border border-gray-200 p-4 dark:border-gray-800">
                    <label class="block">
                      <span class="mb-1.5 block text-xs font-medium text-gray-500 dark:text-gray-400">服务名称</span>
                      <input
                        v-model="guideServerName"
                        type="text"
                        class="h-10 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-800 shadow-sm transition placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
                        placeholder="mcp-proxy-gateway"
                      />
                    </label>
                    <div class="mt-3 grid grid-cols-1 gap-2 sm:grid-cols-2">
                      <button
                        type="button"
                        class="rounded-lg border px-3 py-2 text-left transition"
                        :class="guideConfigKind === 'mcp-json'
                          ? 'border-brand-300 bg-brand-50/60 dark:border-brand-500/50 dark:bg-brand-500/[0.08]'
                          : 'border-gray-200 hover:border-brand-300 hover:bg-brand-50/40 dark:border-gray-800 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.06]'"
                        @click="guideConfigKind = 'mcp-json'"
                      >
                        <span class="block text-xs font-semibold text-gray-800 dark:text-white/90">完整 MCP JSON</span>
                        <span class="mt-1 block text-[11px] leading-4 text-gray-500 dark:text-gray-400">适合新建配置文件</span>
                      </button>
                      <button
                        type="button"
                        class="rounded-lg border px-3 py-2 text-left transition"
                        :class="guideConfigKind === 'server-entry'
                          ? 'border-brand-300 bg-brand-50/60 dark:border-brand-500/50 dark:bg-brand-500/[0.08]'
                          : 'border-gray-200 hover:border-brand-300 hover:bg-brand-50/40 dark:border-gray-800 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.06]'"
                        @click="guideConfigKind = 'server-entry'"
                      >
                        <span class="block text-xs font-semibold text-gray-800 dark:text-white/90">服务条目</span>
                        <span class="mt-1 block text-[11px] leading-4 text-gray-500 dark:text-gray-400">适合合并到已有 mcpServers</span>
                      </button>
                    </div>
                    <p class="mt-2 text-[11px] leading-5 text-gray-400 dark:text-gray-500">
                      当前服务名：{{ normalizeMCPServerName(guideServerName) }}
                    </p>
                  </div>
                </div>
              </div>

              <div class="space-y-4">
                <div class="rounded-xl border border-gray-200 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
                  <div class="flex flex-wrap items-center justify-between gap-3">
                    <div>
                      <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">当前对接信息</h4>
                      <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                        {{ selectedPreset.label }} · {{ selectedEndpoint.name }} · {{ selectedAuth.label }}
                      </p>
                    </div>
                    <button
                      v-tooltip:bottom="guideCopiedKey === 'address' ? '已复制' : '复制地址'"
                      type="button"
                      class="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 dark:border-gray-800 dark:text-gray-300 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400"
                      aria-label="复制对接地址"
                      @click="copyGuideText('address', guideAddress)"
                    >
                      <CheckIcon v-if="guideCopiedKey === 'address'" class="h-4 w-4" />
                      <DocsIcon v-else class="h-4 w-4" />
                    </button>
                  </div>
                  <div class="mt-4 rounded-lg bg-gray-50 p-3 dark:bg-gray-900/60">
                    <p class="mb-2 text-[11px] leading-5 text-gray-500 dark:text-gray-400">
                      {{ selectedPreset.hint }}
                    </p>
                    <p class="break-all font-mono text-xs leading-5 text-gray-700 dark:text-gray-200">{{ guideAddress }}</p>
                    <p class="mt-2 text-[11px] leading-5 text-gray-400 dark:text-gray-500">
                      {{ guideAddressHint }}
                    </p>
                  </div>
                  <div class="mt-3 rounded-lg bg-gray-50 p-3 dark:bg-gray-900/60">
                    <p class="text-xs text-gray-400 dark:text-gray-500">认证</p>
                    <pre class="mt-1 whitespace-pre-wrap break-all font-mono text-xs leading-5 text-gray-700 dark:text-gray-200">{{ guideAuthText }}</pre>
                  </div>
                  <p v-if="guideCopyError !== ''" class="mt-3 rounded-lg bg-error-50 px-3 py-2 text-xs text-error-600 dark:bg-error-500/10 dark:text-error-400">
                    {{ guideCopyError }}
                  </p>
                </div>

                <div v-for="snippet in guideSnippets" :key="snippet.key" class="rounded-xl border border-gray-200 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
                  <div class="flex items-start justify-between gap-3">
                    <div>
                      <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">{{ snippet.title }}</h4>
                      <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">{{ snippet.desc }}</p>
                    </div>
                    <button
                      v-tooltip:bottom="guideCopiedKey === snippet.key ? '已复制' : '复制示例'"
                      type="button"
                      class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 dark:border-gray-800 dark:text-gray-300 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400"
                      :aria-label="`复制${snippet.title}`"
                      @click="copyGuideText(snippet.key, snippet.code)"
                    >
                      <CheckIcon v-if="guideCopiedKey === snippet.key" class="h-4 w-4" />
                      <DocsIcon v-else class="h-4 w-4" />
                    </button>
                  </div>
                  <pre class="custom-scrollbar mt-3 max-h-60 overflow-auto rounded-lg bg-gray-950 p-4 text-xs leading-5 text-gray-100"><code>{{ snippet.code }}</code></pre>
                </div>
              </div>
            </div>
          </div>
        </div>
      </div>
    </transition>
  </AdminLayout>
</template>
