<script setup lang="ts">
/**
 * 系统设置页（任务 26.4）。
 *
 * 以模块选项卡分组编辑网关运行参数：备份恢复、网关运行、安全防护、本地运行时（stdio）、
 * 数据与会话。个人密码、对外服务模式与 API 服务接入已拆到更贴近使用场景的页面。
 *
 * 校验策略：字段范围由后端统一强制（见 internal/config/validate.go），前端仅以 min/max
 * 提示与 number 输入辅助，并在保存失败时按后端返回的 fields 将错误定位到对应模块。
 * 响应式：useBreakpoint.isLargeScreen 决定分区内表单为单列（小屏）或双列（大屏）。
 */
import { computed, onMounted, reactive, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import FieldLabel from '@/components/common/FieldLabel.vue'
import FloatingActionBar from '@/components/common/FloatingActionBar.vue'
import { useBreakpoint } from '@/composables/useBreakpoint'
import { useToast } from '@/composables/useToast'
import { useConfirm } from '@/composables/useConfirm'
import {
  getSettings,
  updateSettings,
  extractAPIError,
  type ToolRoutingStrategy,
  type YAMLConfig,
} from '@/api/settings'
import { exportBackup, importBackup, previewBackup, type BackupPreview } from '@/api/backup'
import {
  buildSettingsDraft,
  cloneSettingsConfig,
  collectSettingsChanges,
  settingsChangesRequireRestart,
  settingsConfirmMessage,
} from '@/utils/settingsChangeSummary'
import PathField from '@/components/common/PathField.vue'

const { isLargeScreen } = useBreakpoint()
const toast = useToast()
const { confirm } = useConfirm()

type SettingsTab = 'backup' | 'gateway' | 'security' | 'runtime' | 'data'

const settingsTabs: ReadonlyArray<{
  key: SettingsTab
  label: string
  desc: string
}> = [
  { key: 'backup', label: '备份恢复', desc: '导出与导入完整配置' },
  { key: 'gateway', label: '网关运行', desc: '同步、超时、监听与 API 默认值' },
  { key: 'security', label: '安全防护', desc: '失败监控、自动封禁与来源识别' },
  { key: 'runtime', label: '本地运行时', desc: 'stdio 上游启动与安全策略' },
  { key: 'data', label: '数据与会话', desc: '保留期与后台会话超时' },
]

const activeTab = ref<SettingsTab>('gateway')

/** 分区内表单栅格类：大屏两列、小屏单列。 */
const gridClass = computed(() =>
  isLargeScreen.value ? 'grid grid-cols-2 gap-x-6 gap-y-5' : 'grid grid-cols-1 gap-y-5',
)

function tabHasErrors(tab: SettingsTab): boolean {
  return Object.keys(fieldErrors).some((key) => fieldErrorBelongsToTab(key, tab))
}

function fieldErrorBelongsToTab(field: string, tab: SettingsTab): boolean {
  switch (tab) {
    case 'backup':
      return false
    case 'gateway':
      return (
        field.startsWith('sync.') ||
        field.startsWith('connection.') ||
        field.startsWith('aggregation.') ||
        field.startsWith('server.') ||
        field.startsWith('mcp_api.')
      )
    case 'security':
      return field.startsWith('security.')
    case 'runtime':
      return field.startsWith('runtime.')
    case 'data':
      return (
        field.startsWith('statistics.') || field.startsWith('audit.') || field.startsWith('auth.')
      )
    default:
      return false
  }
}

function firstTabWithErrors(): SettingsTab | null {
  for (const tab of settingsTabs) {
    if (tabHasErrors(tab.key)) return tab.key
  }
  return null
}

function selectSettingsTab(tab: SettingsTab): void {
  activeTab.value = tab
}

/** 当前配置表单模型（加载后填充）。 */
const config = ref<YAMLConfig | null>(null)
const savedConfig = ref<YAMLConfig | null>(null)

const trustedProxyCIDRs = ref('')
const exemptCIDRs = ref('')
const commandAllowlistText = ref('')
const strictCommandAllowlistText = ref('')
const strictPackageAllowlistText = ref('')
const globalFileRootsText = ref('')
const browseExtraRootsText = ref('')
const extraSensitiveEnvPrefixesText = ref('')
/** 包仓库镜像（加速国内 stdio 子进程拉依赖），均为单行 URL。 */
const npmRegistry = ref('')
const pipIndexURL = ref('')
const uvIndexURL = ref('')

/** Port helpers: strip ':prefix on load, restore on save. */
const adminPort = ref<number | string>('')
const publicMCPPort = ref<number | string>('')

function addrToPort(addr: string): number | string {
  const s = addr.trim()
  if (s === '') return ''
  const m = s.match(/^(?:[\d.]+|\[?[\da-f:]+\]?)?:(\d+)$/i)
  if (m) return Number(m[1])
  return s
}

function portToAddr(port: number | string): string {
  const n = typeof port === 'number' ? port : Number(port)
  if (!Number.isFinite(n) || n < 1) return ''
  return ':' + String(n)
}

/** 页面级加载/保存状态。 */
const loading = ref(false)
const saving = ref(false)
const loadError = ref('')
const backupExporting = ref(false)
const backupImporting = ref(false)
const backupContent = ref('')
const backupFileName = ref('')
const backupPreview = ref<BackupPreview | null>(null)
const backupError = ref('')
const backupFileInput = ref<HTMLInputElement | null>(null)

/** 保存表单（设置）的整体错误与字段级错误（键为后端字段路径，如 sync.cron）。 */
const formError = ref('')
const fieldErrors = reactive<Record<string, string>>({})

const routingStrategies: ReadonlyArray<{
  value: ToolRoutingStrategy
  label: string
  desc: string
}> = [
  {
    value: 'smart_balance',
    label: '智能均衡',
    desc: '推荐默认：在健康且未超额的来源间自动分配，遇到失败会短暂绕开问题来源。',
  },
  {
    value: 'priority_fill',
    label: '稳定优先',
    desc: '始终优先使用排序靠前的来源，仅在不可用、超额或短暂降级时切到后续来源。',
  },
]

const securityModes = [
  { value: 'monitor', label: '仅记录', desc: '记录异常事件，不自动拦截来源。' },
  { value: 'enforce', label: '自动封禁', desc: '达到阈值后临时封禁异常来源。' },
  { value: 'off', label: '关闭', desc: '不记录鉴权失败事件，也不触发自动封禁。' },
] as const

const trustedProxyCIDRError = computed(() => firstIndexedError('security.trusted_proxy_cidrs'))
const exemptCIDRError = computed(() => firstIndexedError('security.exempt_cidrs'))
const commandAllowlistError = computed(() => firstIndexedError('runtime.command_allowlist'))
const strictCommandAllowlistError = computed(() =>
  firstIndexedError('runtime.strict_command_allowlist'),
)
const strictPackageAllowlistError = computed(() =>
  firstIndexedError('runtime.strict_package_allowlist'),
)
const globalFileRootsError = computed(() => firstIndexedError('runtime.global_file_roots'))
const browseExtraRootsError = computed(() => firstIndexedError('runtime.browse_extra_roots'))
const extraSensitivePrefixError = computed(() =>
  firstIndexedError('runtime.extra_sensitive_env_prefixes'),
)
const npmRegistryError = computed(() => firstIndexedError('runtime.npm_registry'))
const pipIndexURLError = computed(() => firstIndexedError('runtime.pip_index_url'))
const uvIndexURLError = computed(() => firstIndexedError('runtime.uv_index_url'))

function firstIndexedError(prefix: string): string {
  for (const [key, value] of Object.entries(fieldErrors)) {
    if (key === prefix || key.startsWith(`${prefix}.`)) return value
  }
  return ''
}

function cidrListToText(values: string[]): string {
  return values.join('\n')
}

function textToCIDRList(value: string): string[] {
  return value
    .split(/\r?\n/)
    .map((item) => item.trim())
    .filter((item) => item !== '')
}

function currentDraftConfig(): YAMLConfig | null {
  if (config.value === null) return null
  return buildSettingsDraft(config.value, {
    adminAddr: portToAddr(adminPort.value),
    publicMCPAddr: portToAddr(publicMCPPort.value),
    trustedProxyCIDRs: textToCIDRList(trustedProxyCIDRs.value),
    exemptCIDRs: textToCIDRList(exemptCIDRs.value),
    commandAllowlist: textToCIDRList(commandAllowlistText.value),
    strictCommandAllowlist: textToCIDRList(strictCommandAllowlistText.value),
    strictPackageAllowlist: textToCIDRList(strictPackageAllowlistText.value),
    globalFileRoots: textToCIDRList(globalFileRootsText.value),
    browseExtraRoots: textToCIDRList(browseExtraRootsText.value),
    extraSensitiveEnvPrefixes: textToCIDRList(extraSensitiveEnvPrefixesText.value),
    npmRegistry: npmRegistry.value,
    pipIndexURL: pipIndexURL.value,
    uvIndexURL: uvIndexURL.value,
  })
}

function syncSecurityTextFields(): void {
  if (config.value === null) return
  trustedProxyCIDRs.value = cidrListToText(config.value.security.trusted_proxy_cidrs ?? [])
  exemptCIDRs.value = cidrListToText(config.value.security.exempt_cidrs ?? [])
  ensureRuntimeConfig()
  commandAllowlistText.value = cidrListToText(config.value.runtime?.command_allowlist ?? [])
  strictCommandAllowlistText.value = cidrListToText(
    config.value.runtime?.strict_command_allowlist ?? [],
  )
  strictPackageAllowlistText.value = cidrListToText(
    config.value.runtime?.strict_package_allowlist ?? [],
  )
  globalFileRootsText.value = cidrListToText(config.value.runtime?.global_file_roots ?? [])
  browseExtraRootsText.value = cidrListToText(config.value.runtime?.browse_extra_roots ?? [])
  extraSensitiveEnvPrefixesText.value = cidrListToText(
    config.value.runtime?.extra_sensitive_env_prefixes ?? [],
  )
  npmRegistry.value = config.value.runtime?.npm_registry ?? ''
  pipIndexURL.value = config.value.runtime?.pip_index_url ?? ''
  uvIndexURL.value = config.value.runtime?.uv_index_url ?? ''
}

function ensureRuntimeConfig(): void {
  if (config.value === null) return
  if (config.value.runtime == null) {
    config.value.runtime = {
      stdio_enabled: true,
      command_allowlist: ['node', 'npx', 'npm', 'python', 'python3', 'uv', 'uvx', 'docker'],
      extra_sensitive_env_prefixes: [],
      process_hardening: true,
      default_stdio_security_mode: 'standard',
      strict_command_allowlist: ['node', 'npx', 'python', 'python3', 'uv', 'uvx'],
      strict_package_allowlist: [
        '@modelcontextprotocol/*',
        '@playwright/mcp',
        '@notionhq/notion-mcp-server',
        'firecrawl-mcp',
        'exa-mcp-server',
      ],
      global_file_roots: [],
      browse_extra_roots: [],
      strict_path_only_runtime: true,
      strict_network_default: 'allowlist',
      strict_allow_policy_only: true,
    }
  }
  if (config.value.runtime.process_hardening == null) {
    config.value.runtime.process_hardening = true
  }
  if (!config.value.runtime.default_stdio_security_mode) {
    config.value.runtime.default_stdio_security_mode = 'standard'
  }
  if (!Array.isArray(config.value.runtime.strict_command_allowlist)) {
    config.value.runtime.strict_command_allowlist = [
      'node',
      'npx',
      'python',
      'python3',
      'uv',
      'uvx',
    ]
  }
  if (!Array.isArray(config.value.runtime.strict_package_allowlist)) {
    config.value.runtime.strict_package_allowlist = [
      '@modelcontextprotocol/*',
      '@playwright/mcp',
      '@notionhq/notion-mcp-server',
      'firecrawl-mcp',
      'exa-mcp-server',
    ]
  }
  if (!Array.isArray(config.value.runtime.global_file_roots)) {
    config.value.runtime.global_file_roots = []
  }
  if (!Array.isArray(config.value.runtime.browse_extra_roots)) {
    config.value.runtime.browse_extra_roots = []
  }
  if (config.value.runtime.strict_path_only_runtime == null) {
    config.value.runtime.strict_path_only_runtime = true
  }
  if (!config.value.runtime.strict_network_default) {
    config.value.runtime.strict_network_default = 'allowlist'
  }
  if (config.value.runtime.strict_allow_policy_only == null) {
    config.value.runtime.strict_allow_policy_only = true
  }
  // 包仓库镜像字段缺省视为空（不覆盖子进程默认源）。
  if (config.value.runtime.npm_registry == null) {
    config.value.runtime.npm_registry = ''
  }
  if (config.value.runtime.pip_index_url == null) {
    config.value.runtime.pip_index_url = ''
  }
  if (config.value.runtime.uv_index_url == null) {
    config.value.runtime.uv_index_url = ''
  }
}

/** 清空所有字段级错误与整体错误。 */
function clearErrors(): void {
  formError.value = ''
  for (const k of Object.keys(fieldErrors)) {
    delete fieldErrors[k]
  }
}

/** 加载当前配置快照。 */
async function loadSettings(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    config.value = await getSettings()
    savedConfig.value = cloneSettingsConfig(config.value)
    adminPort.value = addrToPort(config.value.server.admin_addr)
    publicMCPPort.value = addrToPort(config.value.server.public_mcp_addr)
    syncSecurityTextFields()
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : '加载系统设置失败'
  } finally {
    loading.value = false
  }
}

onMounted(loadSettings)

/** 将后端返回的字段级错误映射到本地，并设置整体错误信息。 */
function applyServerError(err: unknown): void {
  const body = extractAPIError(err)
  if (body?.fields) {
    for (const [k, v] of Object.entries(body.fields)) {
      fieldErrors[k] = v
    }
  }
  formError.value = body?.message ?? (err instanceof Error ? err.message : '保存失败，请稍后重试')
  const tab = firstTabWithErrors()
  if (tab !== null) {
    activeTab.value = tab
  }
}

/** 保存常规配置。 */
async function saveSettings(): Promise<void> {
  if (config.value === null || saving.value) return
  const draft = currentDraftConfig()
  const before = savedConfig.value
  if (draft === null || before === null) return
  const changes = collectSettingsChanges(before, draft)
  if (changes.length === 0) {
    toast.info('系统设置没有变更')
    return
  }
  const restart = settingsChangesRequireRestart(changes)
  const enteringUnrestrictedDefault =
    (before.runtime?.default_stdio_security_mode ?? 'standard') !== 'unrestricted' &&
    draft.runtime?.default_stdio_security_mode === 'unrestricted'
  const ok = await confirm({
    title: '确认保存系统设置',
    message:
      settingsConfirmMessage(changes) +
      (enteringUnrestrictedDefault
        ? '\n\n高风险确认：未单独声明安全档位的所有本地 stdio 上游将默认与网关同权限执行。请仅在明确受信环境中启用。'
        : ''),
    confirmText: restart ? '保存并重启' : '保存',
    cancelText: '取消',
    tone: 'warning',
  })
  if (!ok) return

  clearErrors()
  saving.value = true
  try {
    config.value = await updateSettings(draft, {
      restart,
      acknowledgeUnrestrictedDefault: enteringUnrestrictedDefault,
    })
    savedConfig.value = cloneSettingsConfig(config.value)
    adminPort.value = addrToPort(config.value.server.admin_addr)
    publicMCPPort.value = addrToPort(config.value.server.public_mcp_addr)
    syncSecurityTextFields()
    toast.success(restart ? '系统设置已保存，网关正在重启' : '系统设置已保存')
  } catch (err) {
    applyServerError(err)
  } finally {
    saving.value = false
  }
}

function backupFileNameNow(): string {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  return `mpg-backup-${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}-${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}.json`
}

async function downloadBackup(): Promise<void> {
  if (backupExporting.value) return
  backupExporting.value = true
  backupError.value = ''
  try {
    const blob = await exportBackup()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = backupFileNameNow()
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
    toast.success('备份文件已导出')
  } catch (err) {
    backupError.value = err instanceof Error ? err.message : '导出备份失败'
  } finally {
    backupExporting.value = false
  }
}

function chooseBackupFile(): void {
  backupFileInput.value?.click()
}

async function onBackupFileSelected(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  backupPreview.value = null
  backupContent.value = ''
  backupFileName.value = ''
  backupError.value = ''
  if (!file) return

  try {
    const content = await file.text()
    const preview = await previewBackup(content)
    backupContent.value = content
    backupFileName.value = file.name
    backupPreview.value = preview
    toast.success('备份文件已解析')
  } catch (err) {
    backupError.value = err instanceof Error ? err.message : '备份文件解析失败'
  }
}

async function confirmImportBackup(): Promise<void> {
  if (backupImporting.value || backupContent.value === '' || backupPreview.value === null) return
  const ok = await confirm({
    title: '确认导入备份',
    message:
      '导入后会覆盖当前系统设置、上游、API Key、规则和白名单，并自动重启网关。建议先导出当前备份。',
    confirmText: '导入并覆盖',
    cancelText: '取消',
    tone: 'warning',
  })
  if (!ok) return

  backupImporting.value = true
  backupError.value = ''
  try {
    await importBackup(backupContent.value)
    toast.success('备份已导入，网关正在重启')
    backupContent.value = ''
    backupFileName.value = ''
    backupPreview.value = null
  } catch (err) {
    backupError.value = err instanceof Error ? err.message : '导入备份失败'
  } finally {
    backupImporting.value = false
  }
}

/** 通用样式类（TailAdmin 风格）。 */
const inputClass =
  'h-11 w-full rounded-lg border border-gray-300 bg-transparent px-4 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30'
const inputWithSuffixClass =
  'h-11 w-full rounded-l-lg border border-gray-300 bg-transparent px-4 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90 dark:placeholder:text-white/30'
const hintClass = 'mt-1 text-xs text-gray-400 dark:text-gray-500'
const errClass = 'mt-1 text-xs text-error-500'
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="系统设置" />

    <!-- 加载/错误态 -->
    <div
      v-if="loading"
      class="rounded-2xl border border-gray-200 bg-white px-5 py-12 text-center text-sm text-gray-400 dark:border-gray-800 dark:bg-white/[0.03]"
    >
      加载中…
    </div>
    <p
      v-else-if="loadError !== ''"
      class="border-error-200 bg-error-50 text-error-600 dark:border-error-500/30 dark:bg-error-500/10 dark:text-error-400 rounded-2xl border px-5 py-4 text-sm"
    >
      {{ loadError }}
      <button type="button" class="ml-2 underline" @click="loadSettings">重试</button>
    </p>

    <template v-else-if="config !== null">
      <p
        v-if="formError !== ''"
        class="bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400 mb-4 rounded-lg px-4 py-2.5 text-sm"
      >
        {{ formError }}
      </p>

      <form class="flex flex-col gap-6 pb-28" @submit.prevent="saveSettings">
        <div
          class="sticky top-16 z-20 -mx-1 rounded-2xl border border-gray-200 bg-white/95 p-2 shadow-sm backdrop-blur sm:top-20 dark:border-gray-800 dark:bg-gray-900/95"
        >
          <div class="flex gap-1 overflow-x-auto pb-0.5" role="tablist" aria-label="系统设置模块">
            <button
              v-for="tab in settingsTabs"
              :key="tab.key"
              type="button"
              role="tab"
              class="relative min-w-[8.5rem] shrink-0 rounded-xl px-3 py-2.5 text-left transition"
              :class="
                activeTab === tab.key
                  ? 'bg-brand-50 text-brand-700 dark:bg-brand-500/10 dark:text-brand-300'
                  : 'text-gray-600 hover:bg-gray-50 hover:text-gray-800 dark:text-gray-400 dark:hover:bg-white/5 dark:hover:text-gray-200'
              "
              :aria-selected="activeTab === tab.key"
              @click="selectSettingsTab(tab.key)"
            >
              <span class="flex items-center gap-1.5 text-sm font-medium">
                {{ tab.label }}
                <span
                  v-if="tabHasErrors(tab.key)"
                  class="bg-error-500 inline-flex h-1.5 w-1.5 rounded-full"
                  aria-label="该模块存在校验错误"
                ></span>
              </span>
              <span class="mt-0.5 block text-[11px] leading-4 opacity-75">{{ tab.desc }}</span>
            </button>
          </div>
        </div>

        <!-- 配置备份 -->
        <section
          v-show="activeTab === 'backup'"
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <div class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
            <div class="min-w-0">
              <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">
                配置备份
              </h3>
              <p class="text-sm text-gray-500 dark:text-gray-400">
                导出或恢复系统设置、上游、API Key、规则和白名单。
              </p>
              <p class="text-warning-600 dark:text-warning-400 mt-2 text-xs">
                备份文件包含上游凭证和 API Key 明文，请按密钥文件保管。
              </p>
            </div>
            <div class="flex flex-wrap gap-2">
              <button
                type="button"
                class="rounded-lg border border-gray-300 px-4 py-2.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                :disabled="backupExporting || backupImporting"
                @click="downloadBackup"
              >
                {{ backupExporting ? '导出中…' : '导出备份' }}
              </button>
              <button
                type="button"
                class="rounded-lg border border-gray-300 px-4 py-2.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                :disabled="backupImporting"
                @click="chooseBackupFile"
              >
                选择备份文件
              </button>
              <input
                ref="backupFileInput"
                class="sr-only"
                type="file"
                accept=".json,application/json"
                @change="onBackupFileSelected"
              />
            </div>
          </div>

          <p
            v-if="backupError !== ''"
            class="bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400 mt-4 rounded-lg px-4 py-2.5 text-sm"
          >
            {{ backupError }}
          </p>

          <div
            v-if="backupPreview !== null"
            class="mt-5 rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]"
          >
            <div class="flex flex-col gap-4 lg:flex-row lg:items-center lg:justify-between">
              <div class="min-w-0">
                <p class="truncate text-sm font-medium text-gray-800 dark:text-white/90">
                  {{ backupFileName }}
                </p>
                <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  格式 {{ backupPreview.version }}
                </p>
              </div>
              <button
                type="button"
                class="bg-warning-500 hover:bg-warning-600 rounded-lg px-4 py-2.5 text-sm font-medium text-white transition disabled:opacity-60"
                :disabled="backupImporting"
                @click="confirmImportBackup"
              >
                {{ backupImporting ? '导入中…' : '导入并覆盖' }}
              </button>
            </div>
            <div class="mt-4 grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-7">
              <div class="rounded-lg bg-white px-3 py-2 dark:bg-gray-900/40">
                <p class="text-xs text-gray-500 dark:text-gray-400">上游</p>
                <p class="mt-1 text-base font-semibold text-gray-800 dark:text-white/90">
                  {{ backupPreview.upstreamCount }}
                </p>
              </div>
              <div class="rounded-lg bg-white px-3 py-2 dark:bg-gray-900/40">
                <p class="text-xs text-gray-500 dark:text-gray-400">API Key</p>
                <p class="mt-1 text-base font-semibold text-gray-800 dark:text-white/90">
                  {{ backupPreview.apiKeyCount }}
                </p>
              </div>
              <div class="rounded-lg bg-white px-3 py-2 dark:bg-gray-900/40">
                <p class="text-xs text-gray-500 dark:text-gray-400">别名规则</p>
                <p class="mt-1 text-base font-semibold text-gray-800 dark:text-white/90">
                  {{ backupPreview.aliasRuleCount }}
                </p>
              </div>
              <div class="rounded-lg bg-white px-3 py-2 dark:bg-gray-900/40">
                <p class="text-xs text-gray-500 dark:text-gray-400">上游屏蔽</p>
                <p class="mt-1 text-base font-semibold text-gray-800 dark:text-white/90">
                  {{ backupPreview.mcpFilterRuleCount }}
                </p>
              </div>
              <div class="rounded-lg bg-white px-3 py-2 dark:bg-gray-900/40">
                <p class="text-xs text-gray-500 dark:text-gray-400">工具策略</p>
                <p class="mt-1 text-base font-semibold text-gray-800 dark:text-white/90">
                  {{ backupPreview.toolPolicyRuleCount }}
                </p>
              </div>
              <div class="rounded-lg bg-white px-3 py-2 dark:bg-gray-900/40">
                <p class="text-xs text-gray-500 dark:text-gray-400">Key 屏蔽</p>
                <p class="mt-1 text-base font-semibold text-gray-800 dark:text-white/90">
                  {{ backupPreview.apiKeyFilterRuleCount }}
                </p>
              </div>
              <div class="rounded-lg bg-white px-3 py-2 dark:bg-gray-900/40">
                <p class="text-xs text-gray-500 dark:text-gray-400">白名单</p>
                <p class="mt-1 text-base font-semibold text-gray-800 dark:text-white/90">
                  {{ backupPreview.aclCount }}
                </p>
              </div>
            </div>
          </div>
        </section>

        <!-- 同步 -->
        <section
          v-show="activeTab === 'gateway'"
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">同步</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            工具列表自动同步的调度与超时设置。
          </p>
          <div :class="gridClass">
            <div>
              <FieldLabel
                label="同步 cron 表达式"
                required
                tooltip="用于定期刷新上游 MCP 工具列表，服务端会校验 cron 格式。"
              />
              <input
                v-model="config.sync.cron"
                type="text"
                placeholder="如：0 */30 * * * *"
                :class="inputClass"
              />
              <p :class="hintClass">标准 6 段 cron 表达式，由服务端校验合法性。</p>
              <p v-if="fieldErrors['sync.cron']" :class="errClass">
                {{ fieldErrors['sync.cron'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="同步超时（秒）"
                required
                tooltip="单个上游工具同步允许等待的最长时间。"
              />
              <input
                v-model.number="config.sync.timeout_s"
                type="number"
                min="5"
                max="300"
                :class="inputClass"
              />
              <p :class="hintClass">范围 5 – 300，默认 30。</p>
              <p v-if="fieldErrors['sync.timeout_s']" :class="errClass">
                {{ fieldErrors['sync.timeout_s'] }}
              </p>
            </div>
          </div>
        </section>

        <!-- 超时与重试 -->
        <section
          v-show="activeTab === 'gateway'"
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">超时与重试</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            上游连接、重试退避与聚合调用的超时设置。
          </p>
          <div :class="gridClass">
            <div>
              <FieldLabel
                label="连接建立超时（秒）"
                required
                tooltip="建立上游 MCP 连接时允许等待的最长时间。"
              />
              <input
                v-model.number="config.connection.connect_timeout_s"
                type="number"
                min="1"
                :class="inputClass"
              />
              <p :class="hintClass">需为正整数，默认 30。</p>
              <p v-if="fieldErrors['connection.connect_timeout_s']" :class="errClass">
                {{ fieldErrors['connection.connect_timeout_s'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="初始退避（秒）"
                required
                tooltip="连接失败后首次重试前等待的时间。"
              />
              <input
                v-model.number="config.connection.retry_initial_backoff_s"
                type="number"
                min="1"
                max="60"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 60，默认 5。</p>
              <p v-if="fieldErrors['connection.retry_initial_backoff_s']" :class="errClass">
                {{ fieldErrors['connection.retry_initial_backoff_s'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="退避倍数"
                required
                tooltip="连续失败时每次重试等待时间的放大倍数。"
              />
              <input
                v-model.number="config.connection.retry_multiplier"
                type="number"
                min="1"
                :class="inputClass"
              />
              <p :class="hintClass">需大于等于 1，默认 2。</p>
              <p v-if="fieldErrors['connection.retry_multiplier']" :class="errClass">
                {{ fieldErrors['connection.retry_multiplier'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="退避上限（秒）"
                required
                tooltip="自动重试等待时间不会超过这个上限。"
              />
              <input
                v-model.number="config.connection.retry_max_backoff_s"
                type="number"
                min="1"
                max="86400"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 86400，默认 300（5 分钟）。</p>
              <p v-if="fieldErrors['connection.retry_max_backoff_s']" :class="errClass">
                {{ fieldErrors['connection.retry_max_backoff_s'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="连续失败阈值"
                required
                tooltip="达到该次数后转为低频持续探测；不会永久停止自动恢复。"
              />
              <input
                v-model.number="config.connection.failure_threshold"
                type="number"
                min="1"
                max="100"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 100，默认 10；达到阈值后降频探测，恢复后自动上线。</p>
              <p v-if="fieldErrors['connection.failure_threshold']" :class="errClass">
                {{ fieldErrors['connection.failure_threshold'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="按需探测冷却（秒）"
                required
                tooltip="工具调用发现上游不可用时会提前唤醒一次共享重连；冷却期间的并发调用只等待，不重复拨号。"
              />
              <input
                v-model.number="config.connection.demand_reconnect_cooldown_s"
                type="number"
                min="1"
                max="300"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 300，默认 5；较高流量建议保持 3 – 10 秒。</p>
              <p v-if="fieldErrors['connection.demand_reconnect_cooldown_s']" :class="errClass">
                {{ fieldErrors['connection.demand_reconnect_cooldown_s'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="按需等待上限（秒）"
                required
                tooltip="调用会在自身总超时预算内等待共享重连结果；未发送的请求恢复后可继续执行，已发送的请求不会自动重放。"
              />
              <input
                v-model.number="config.connection.demand_reconnect_wait_s"
                type="number"
                min="1"
                max="30"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 30，默认 8；建议小于上游调用超时。</p>
              <p v-if="fieldErrors['connection.demand_reconnect_wait_s']" :class="errClass">
                {{ fieldErrors['connection.demand_reconnect_wait_s'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="上游调用超时（秒）"
                required
                tooltip="转发工具调用到上游 MCP 时允许等待的最长时间。"
              />
              <input
                v-model.number="config.aggregation.upstream_call_timeout_s"
                type="number"
                min="1"
                max="600"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 600，默认 30。</p>
              <p v-if="fieldErrors['aggregation.upstream_call_timeout_s']" :class="errClass">
                {{ fieldErrors['aggregation.upstream_call_timeout_s'] }}
              </p>
            </div>
            <div class="sm:col-span-2">
              <FieldLabel
                label="工具调用策略"
                required
                tooltip="当同名工具来自多个上游时，网关实际调用某个来源上游的选择方式。"
              />
              <div class="mt-1 grid grid-cols-1 gap-3 lg:grid-cols-2">
                <label
                  v-for="item in routingStrategies"
                  :key="item.value"
                  class="cursor-pointer rounded-lg border p-3 transition"
                  :class="
                    config.aggregation.tool_routing_strategy === item.value
                      ? 'border-brand-300 bg-brand-50/70 dark:border-brand-500/50 dark:bg-brand-500/[0.08]'
                      : 'hover:border-brand-200 hover:bg-brand-50/40 dark:hover:border-brand-500/30 dark:hover:bg-brand-500/[0.06] border-gray-200 dark:border-gray-800'
                  "
                >
                  <input
                    v-model="config.aggregation.tool_routing_strategy"
                    class="sr-only"
                    type="radio"
                    :value="item.value"
                  />
                  <span class="block text-sm font-medium text-gray-800 dark:text-white/90">{{
                    item.label
                  }}</span>
                  <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{
                    item.desc
                  }}</span>
                </label>
              </div>
              <p v-if="fieldErrors['aggregation.tool_routing_strategy']" :class="errClass">
                {{ fieldErrors['aggregation.tool_routing_strategy'] }}
              </p>
            </div>
          </div>
        </section>

        <!-- 服务监听 -->
        <section
          v-show="activeTab === 'gateway'"
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">服务监听</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            管理端口用于后台操作；对外 MCP
            可启用独立端口，便于公网暴露时叠加更严格的反向代理和安全策略。
          </p>
          <div :class="gridClass">
            <div>
              <FieldLabel label="管理监听端口" required tooltip="管理台与管理 API 的监听端口号。" />
              <input
                v-model.number="adminPort"
                type="number"
                min="1"
                max="65535"
                placeholder="8080"
                :class="inputClass"
              />
              <p :class="hintClass">
                范围 1–65535，默认 8080。公网部署建议只在内网或反向代理内侧暴露。
              </p>
              <p v-if="fieldErrors['server.admin_addr']" :class="errClass">
                {{ fieldErrors['server.admin_addr'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="独立 MCP 监听端口"
                tooltip="对外 MCP 服务的独立监听端口号。留空表示不启用独立端口。"
              />
              <input
                v-model.number="publicMCPPort"
                type="number"
                min="1"
                max="65535"
                placeholder="8081"
                :class="inputClass"
              />
              <p :class="hintClass">
                范围 1–65535。留空表示不启用独立端口，启用后可只把该端口暴露到公网。
              </p>
              <p v-if="fieldErrors['server.public_mcp_addr']" :class="errClass">
                {{ fieldErrors['server.public_mcp_addr'] }}
              </p>
            </div>
          </div>
          <div
            class="mt-5 rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]"
          >
            <div class="flex flex-wrap items-start justify-between gap-4">
              <div class="min-w-0">
                <FieldLabel
                  label="管理端口同时暴露 MCP"
                  tooltip="关闭后，管理监听地址不再注册 /mcp/*；对外客户端只能访问独立 MCP 监听地址。"
                />
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                  启用独立 MCP
                  监听地址后，建议关闭此项，让管理端口只服务后台；未配置独立端口时不可关闭。
                </p>
              </div>
              <button
                type="button"
                role="switch"
                :aria-checked="config.server.expose_mcp_on_admin_addr"
                class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition"
                :class="
                  config.server.expose_mcp_on_admin_addr
                    ? 'bg-brand-500'
                    : 'bg-gray-300 dark:bg-gray-700'
                "
                @click="
                  config.server.expose_mcp_on_admin_addr = !config.server.expose_mcp_on_admin_addr
                "
              >
                <span
                  class="inline-block h-4 w-4 transform rounded-full bg-white transition"
                  :class="
                    config.server.expose_mcp_on_admin_addr ? 'translate-x-6' : 'translate-x-1'
                  "
                ></span>
              </button>
            </div>
          </div>
          <div class="mt-5">
            <FieldLabel
              label="日志级别"
              required
              tooltip="控制进程日志输出的详细程度。保存后立即生效，无需重启服务。debug 会记录调用链每次工具调用的入口与结果，便于排查问题但日志量较大。"
            />
            <select v-model="config.server.log_level" :class="inputClass">
              <option value="debug">debug（详细，含调用链追踪）</option>
              <option value="info">info（默认，关键事件）</option>
              <option value="warn">warn（仅警告与错误）</option>
              <option value="error">error（仅错误）</option>
            </select>
            <p :class="hintClass">调整后立即生效。排查工具调用问题时建议临时切换到 debug。</p>
            <p v-if="fieldErrors['server.log_level']" :class="errClass">
              {{ fieldErrors['server.log_level'] }}
            </p>
          </div>
        </section>

        <!-- 对外 API 默认值 -->
        <section
          v-show="activeTab === 'gateway'"
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">
            对外 API 默认值
          </h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            对外 MCP 服务的默认参数，保存后立即应用到新请求。
          </p>
          <div :class="gridClass">
            <div>
              <FieldLabel
                label="智能模式默认返回工具数"
                required
                tooltip="智能模式下，网关工具列表发现接口默认返回的真实工具摘要数量。"
              />
              <input
                v-model.number="config.mcp_api.smart_discovery_limit"
                type="number"
                min="1"
                max="200"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 200，默认 50。</p>
              <p v-if="fieldErrors['mcp_api.smart_discovery_limit']" :class="errClass">
                {{ fieldErrors['mcp_api.smart_discovery_limit'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="请求体上限"
                required
                tooltip="单次对外 MCP POST 请求体的最大大小。图片或资源类工具可按需调大，普通 JSON 调用建议保持默认。"
              />
              <div class="flex">
                <input
                  v-model.number="config.mcp_api.request_body_limit_mib"
                  type="number"
                  min="1"
                  max="256"
                  :class="inputWithSuffixClass"
                />
                <span
                  class="inline-flex items-center rounded-r-lg border border-l-0 border-gray-300 bg-gray-50 px-3 text-sm text-gray-500 dark:border-gray-700 dark:bg-gray-900 dark:text-gray-400"
                >
                  MiB
                </span>
              </div>
              <p :class="hintClass">范围 1 – 256 MiB，默认 8 MiB；调大后会占用更多请求处理内存。</p>
              <p v-if="fieldErrors['mcp_api.request_body_limit_mib']" :class="errClass">
                {{ fieldErrors['mcp_api.request_body_limit_mib'] }}
              </p>
            </div>
          </div>
        </section>

        <section
          v-show="activeTab === 'security'"
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <div class="mb-5 flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">
                安全防护
              </h3>
              <p class="text-sm text-gray-500 dark:text-gray-400">
                控制对外 MCP 入口的鉴权失败记录和自动封禁策略，事件与封禁处置在安全中心查看。
              </p>
            </div>
            <router-link
              to="/security"
              class="rounded-lg border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            >
              打开安全中心
            </router-link>
          </div>

          <div class="mb-5 grid grid-cols-1 gap-3 lg:grid-cols-3">
            <label
              v-for="item in securityModes"
              :key="item.value"
              class="cursor-pointer rounded-lg border p-3 transition"
              :class="
                config.security.mode === item.value
                  ? 'border-brand-300 bg-brand-50/70 dark:border-brand-500/50 dark:bg-brand-500/[0.08]'
                  : 'hover:border-brand-200 hover:bg-brand-50/40 dark:hover:border-brand-500/30 dark:hover:bg-brand-500/[0.06] border-gray-200 dark:border-gray-800'
              "
            >
              <input
                v-model="config.security.mode"
                class="sr-only"
                type="radio"
                :value="item.value"
              />
              <span class="block text-sm font-medium text-gray-800 dark:text-white/90">{{
                item.label
              }}</span>
              <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{
                item.desc
              }}</span>
            </label>
          </div>
          <p v-if="fieldErrors['security.mode']" :class="errClass">
            {{ fieldErrors['security.mode'] }}
          </p>

          <div :class="gridClass">
            <div>
              <FieldLabel
                label="失败统计窗口（秒）"
                required
                tooltip="在该时间窗口内累计鉴权失败和来源拒绝次数。"
              />
              <input
                v-model.number="config.security.failure_window_s"
                type="number"
                min="60"
                max="3600"
                :class="inputClass"
              />
              <p :class="hintClass">范围 60 - 3600，默认 300。</p>
              <p v-if="fieldErrors['security.failure_window_s']" :class="errClass">
                {{ fieldErrors['security.failure_window_s'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="单 IP 失败阈值"
                required
                tooltip="同一来源 IP 在统计窗口内允许的无效 API Key 尝试次数。"
              />
              <input
                v-model.number="config.security.max_failures_per_ip"
                type="number"
                min="1"
                max="10000"
                :class="inputClass"
              />
              <p :class="hintClass">默认 30。公网暴露时可适当收紧。</p>
              <p v-if="fieldErrors['security.max_failures_per_ip']" :class="errClass">
                {{ fieldErrors['security.max_failures_per_ip'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="疑似 Key 失败阈值"
                required
                tooltip="同一疑似 API Key 指纹在统计窗口内允许失败的次数。"
              />
              <input
                v-model.number="config.security.max_failures_per_key_fingerprint"
                type="number"
                min="1"
                max="10000"
                :class="inputClass"
              />
              <p :class="hintClass">默认 8。用于识别持续尝试同一无效凭证的来源。</p>
              <p v-if="fieldErrors['security.max_failures_per_key_fingerprint']" :class="errClass">
                {{ fieldErrors['security.max_failures_per_key_fingerprint'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="ACL 拒绝阈值"
                required
                tooltip="同一 API Key 与来源 IP 在统计窗口内被来源白名单拒绝的次数。"
              />
              <input
                v-model.number="config.security.max_acl_denies_per_key_ip"
                type="number"
                min="1"
                max="10000"
                :class="inputClass"
              />
              <p :class="hintClass">默认 5。命中后封禁该 API Key 与来源 IP 的组合。</p>
              <p v-if="fieldErrors['security.max_acl_denies_per_key_ip']" :class="errClass">
                {{ fieldErrors['security.max_acl_denies_per_key_ip'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="首次封禁时长（秒）"
                required
                tooltip="第一次达到自动封禁阈值后的临时封禁时长。"
              />
              <input
                v-model.number="config.security.first_block_duration_s"
                type="number"
                min="60"
                max="86400"
                :class="inputClass"
              />
              <p :class="hintClass">默认 900，即 15 分钟。</p>
              <p v-if="fieldErrors['security.first_block_duration_s']" :class="errClass">
                {{ fieldErrors['security.first_block_duration_s'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="最长自动封禁（秒）"
                required
                tooltip="重复触发封禁升级后允许达到的最长封禁时长。"
              />
              <input
                v-model.number="config.security.max_block_duration_s"
                type="number"
                min="60"
                max="604800"
                :class="inputClass"
              />
              <p :class="hintClass">默认 86400，即 24 小时。</p>
              <p v-if="fieldErrors['security.max_block_duration_s']" :class="errClass">
                {{ fieldErrors['security.max_block_duration_s'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="封禁升级窗口（秒）"
                required
                tooltip="在该时间内重复触发同一对象封禁时会逐步延长封禁时长。"
              />
              <input
                v-model.number="config.security.escalation_window_s"
                type="number"
                min="300"
                max="604800"
                :class="inputClass"
              />
              <p :class="hintClass">默认 86400，即 24 小时。</p>
              <p v-if="fieldErrors['security.escalation_window_s']" :class="errClass">
                {{ fieldErrors['security.escalation_window_s'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="可信代理 CIDR"
                tooltip="CIDR 是 IP 或网段写法，例如 10.0.0.1 或 10.0.0.0/8。填写反向代理/负载均衡等可信中间层的出口地址；仅当直连来源落在这些范围内时，才会采信 X-Forwarded-For、X-Real-IP 等转发头中的真实客户端 IP，避免伪造来源绕过安全策略。"
              />
              <textarea
                v-model="trustedProxyCIDRs"
                rows="4"
                :class="[inputClass, 'h-auto py-3 font-mono']"
                placeholder="10.0.0.0/8"
              ></textarea>
              <p :class="hintClass">
                每行一个 IP 或网段。网关部署在 Nginx、Caddy、云 LB
                等反向代理后时，填写这些代理的出口地址；直连或未知代理不会采用转发头。
              </p>
              <p v-if="trustedProxyCIDRError" :class="errClass">{{ trustedProxyCIDRError }}</p>
            </div>
            <div>
              <FieldLabel
                label="自动封禁豁免 CIDR"
                tooltip="CIDR 是 IP 或网段写法，例如 192.168.1.10 或 192.168.0.0/16。命中这些地址的客户端 IP 仍会记录鉴权失败、ACL 拒绝等安全事件，但不会触发自动封禁，避免探活、内网巡检或固定办公出口被误封。"
              />
              <textarea
                v-model="exemptCIDRs"
                rows="4"
                :class="[inputClass, 'h-auto py-3 font-mono']"
                placeholder="192.168.0.0/16"
              ></textarea>
              <p :class="hintClass">
                每行一个 IP
                或网段。适合监控探活、内网运维出口或已知可信客户端；豁免只跳过自动封禁，不会放宽鉴权与访问控制。
              </p>
              <p v-if="exemptCIDRError" :class="errClass">{{ exemptCIDRError }}</p>
            </div>
          </div>
        </section>

        <!-- 本地运行时 / stdio 安全 -->
        <section
          v-show="activeTab === 'runtime'"
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <div class="mb-5 flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">
                本地运行时（stdio）
              </h3>
              <p class="text-sm text-gray-500 dark:text-gray-400">
                stdio 指网关以本地命令方式启动 MCP
                进程，并通过标准输入/输出通信。这里配置这类上游的总开关与安全策略；远程
                SSE/HTTP/WS、OpenAPI 不受影响。保存后立即生效，无需重启。
              </p>
            </div>
            <router-link
              to="/runtime"
              class="rounded-lg border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            >
              打开运行环境
            </router-link>
          </div>

          <template v-if="config.runtime">
            <div class="mb-5 space-y-3">
              <label
                class="flex cursor-pointer items-start gap-3 rounded-xl border border-gray-200 p-4 dark:border-gray-800"
              >
                <input
                  v-model="config.runtime.stdio_enabled"
                  type="checkbox"
                  class="text-brand-500 focus:ring-brand-500/30 mt-1 h-4 w-4 rounded border-gray-300"
                />
                <span>
                  <span class="block text-sm font-medium text-gray-800 dark:text-white/90">
                    允许本地 stdio 上游
                  </span>
                  <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">
                    开启后可创建 command 型本地上游（如 node / npx / python 启动的
                    MCP）。关闭后无法新建或连接此类上游，仅保留远程接入方式。
                  </span>
                </span>
              </label>
              <p v-if="fieldErrors['runtime.stdio_enabled']" :class="errClass">
                {{ fieldErrors['runtime.stdio_enabled'] }}
              </p>
              <label
                class="flex cursor-pointer items-start gap-3 rounded-xl border border-gray-200 p-4 dark:border-gray-800"
              >
                <input
                  v-model="config.runtime.process_hardening"
                  type="checkbox"
                  class="text-brand-500 focus:ring-brand-500/30 mt-1 h-4 w-4 rounded border-gray-300"
                />
                <span>
                  <span class="block text-sm font-medium text-gray-800 dark:text-white/90">
                    启用 stdio 进程加固
                  </span>
                  <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">
                    降低本地 MCP 子进程失控风险：Linux
                    上会放入独立进程组，并在网关退出时一并清理；其他平台保留策略层约束。建议保持开启。
                  </span>
                </span>
              </label>
              <label
                class="flex cursor-pointer items-start gap-3 rounded-xl border border-gray-200 p-4 dark:border-gray-800"
              >
                <input
                  v-model="config.runtime.strict_path_only_runtime"
                  type="checkbox"
                  class="text-brand-500 focus:ring-brand-500/30 mt-1 h-4 w-4 rounded border-gray-300"
                />
                <span>
                  <span class="block text-sm font-medium text-gray-800 dark:text-white/90">
                    严格档仅使用运行时卷内工具
                  </span>
                  <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">
                    仅对「严格安全」档位生效：不从系统 PATH 随便找命令，优先使用预置安装或放入
                    runtime/bin 的工具，缩小可被拉起的可执行文件范围。
                  </span>
                </span>
              </label>
            </div>

            <div :class="gridClass">
              <div>
                <FieldLabel
                  label="默认本地安全档位"
                  tooltip="stdio 安全档位决定新建/未单独声明安全配置的本地上游默认约束。标准：兼容常见模板；严格：更紧的命令、包与路径限制；完全放行：策略最松，仅建议受信环境使用。可在每个上游单独覆盖。"
                />
                <select v-model="config.runtime.default_stdio_security_mode" :class="inputClass">
                  <option value="standard">标准（兼容模板）</option>
                  <option value="strict">严格安全</option>
                  <option value="unrestricted">完全放行（高风险）</option>
                </select>
                <p :class="hintClass">
                  三档都是网关策略约束，不是操作系统内核沙箱。即使完全放行，也不允许
                  bash、cmd、powershell 等 shell 直接作为启动命令。
                </p>
                <p v-if="fieldErrors['runtime.default_stdio_security_mode']" :class="errClass">
                  {{ fieldErrors['runtime.default_stdio_security_mode'] }}
                </p>
              </div>
              <div>
                <FieldLabel
                  label="严格档默认网络策略"
                  tooltip="仅在安全档位为「严格」且上游未声明自己的网络策略时生效。允许名单：要求上游声明可访问主机；拒绝出站：策略上默认不允许外连。Linux 严格档 + bubblewrap 时 deny 会网络命名空间断网；主机 allowlist 仍为策略声明。"
                />
                <select v-model="config.runtime.strict_network_default" :class="inputClass">
                  <option value="allowlist">允许名单（启用自装包时需声明主机）</option>
                  <option value="deny">拒绝出站（Linux 严格档真断网）</option>
                </select>
                <p :class="hintClass">用于引导严格档上游声明网络需求；不是防火墙规则本身。</p>
              </div>
              <div class="sm:col-span-2">
                <FieldLabel
                  label="stdio 命令白名单"
                  tooltip="全局允许作为本地 MCP 启动命令的可执行文件基名，例如 node、npx、python3。每行一个。bash、sh、cmd、powershell 等 shell 始终禁止，避免通过 shell 绕过策略。"
                />
                <textarea
                  v-model="commandAllowlistText"
                  rows="4"
                  :class="[inputClass, 'h-auto py-3 font-mono']"
                  placeholder="node&#10;npx&#10;python3&#10;uvx"
                ></textarea>
                <p :class="hintClass">
                  默认包含
                  node/npx/npm/python/python3/uv/uvx/docker。未列入白名单的命令无法启动本地上游。
                </p>
                <p v-if="commandAllowlistError" :class="errClass">{{ commandAllowlistError }}</p>
              </div>
              <div class="sm:col-span-2">
                <FieldLabel
                  label="严格档命令子集"
                  tooltip="严格安全档实际允许的启动命令 = 本列表与上方全局命令白名单的交集。默认不含 docker、npm 等风险更高的启动器，可进一步收紧严格档能力。"
                />
                <textarea
                  v-model="strictCommandAllowlistText"
                  rows="3"
                  :class="[inputClass, 'h-auto py-3 font-mono']"
                  placeholder="node&#10;npx&#10;python3&#10;uvx"
                ></textarea>
                <p :class="hintClass">只影响严格档；标准档与完全放行仍以全局命令白名单为准。</p>
                <p v-if="strictCommandAllowlistError" :class="errClass">
                  {{ strictCommandAllowlistError }}
                </p>
              </div>
              <div class="sm:col-span-2">
                <FieldLabel
                  label="严格档 npx/uvx 包白名单"
                  tooltip="严格档下允许通过 npx 或 uvx 拉取/执行的包名列表。支持 @scope/* 通配。这不是禁用 npx/uvx，而是限制它们能运行哪些包；每个上游还可追加自己的 packageAllowlist。"
                />
                <textarea
                  v-model="strictPackageAllowlistText"
                  rows="4"
                  :class="[inputClass, 'h-auto py-3 font-mono']"
                  placeholder="@modelcontextprotocol/*&#10;@playwright/mcp&#10;firecrawl-mcp"
                ></textarea>
                <p :class="hintClass">
                  默认覆盖内置模板常用包。未命中白名单的包在严格档会被拒绝启动。
                </p>
                <p v-if="strictPackageAllowlistError" :class="errClass">
                  {{ strictPackageAllowlistError }}
                </p>
              </div>
              <div class="sm:col-span-2">
                <FieldLabel
                  label="全局文件允许路径"
                  tooltip="严格档或声明了文件策略时，本地 MCP 默认可访问的根目录。每行一个绝对路径。上游仍可单独声明额外路径；不配置则按上游自身声明与安全档位处理。路径位于网关主机，不是浏览器本机。"
                />
                <PathField
                  v-model="globalFileRootsText"
                  mode="directory"
                  multiple
                  :rows="3"
                  title="添加全局文件允许路径（网关主机）"
                  placeholder="/data/workspaces&#10;D:\\mcp-workspace"
                  :input-class="[inputClass, 'h-auto py-3 font-mono'].join(' ')"
                  :context-roots="textToCIDRList(globalFileRootsText)"
                />
                <p :class="hintClass">
                  可手输或点右侧文件夹浏览网关主机目录。适合统一工作区，例如共享数据卷或项目根路径。
                </p>
                <p v-if="globalFileRootsError" :class="errClass">{{ globalFileRootsError }}</p>
              </div>
              <div class="sm:col-span-2">
                <FieldLabel
                  label="路径浏览额外根"
                  tooltip="仅扩大管理台路径选择器可浏览范围，方便选择工作目录或文件允许路径。不会放宽 stdio 安全策略，也不会自动授权上游访问这些目录。每行一个绝对路径，路径位于网关主机。"
                />
                <PathField
                  v-model="browseExtraRootsText"
                  mode="directory"
                  multiple
                  :rows="3"
                  title="添加路径浏览额外根（网关主机）"
                  placeholder="D:\\mcp-workspace&#10;/opt/mcp-data"
                  :input-class="[inputClass, 'h-auto py-3 font-mono'].join(' ')"
                  :context-roots="[
                    ...textToCIDRList(globalFileRootsText),
                    ...textToCIDRList(browseExtraRootsText),
                  ]"
                />
                <p :class="hintClass">
                  当数据目录/运行时目录不够用时，在此声明额外可浏览根。保存后立即影响路径选择器。
                </p>
                <p v-if="browseExtraRootsError" :class="errClass">{{ browseExtraRootsError }}</p>
              </div>
              <div class="sm:col-span-2">
                <FieldLabel
                  label="额外敏感环境变量前缀"
                  tooltip="启动本地 MCP 时，默认会剥离一批敏感环境变量（如 MPG_、常见云厂商密钥前缀），避免把网关侧密钥继承给子进程。这里可追加更多前缀（大小写不敏感）。上游表单中显式配置的 env 仍会注入。"
                />
                <textarea
                  v-model="extraSensitiveEnvPrefixesText"
                  rows="3"
                  :class="[inputClass, 'h-auto py-3 font-mono']"
                  placeholder="CORP_&#10;PRIVATE_"
                ></textarea>
                <p :class="hintClass">内置剥离规则始终生效；此处仅追加企业或私有前缀。</p>
                <p v-if="extraSensitivePrefixError" :class="errClass">
                  {{ extraSensitivePrefixError }}
                </p>
              </div>
            </div>

            <!-- 加速镜像：国内网络拉 npm/pip/uv 依赖常被墙，为 stdio 子进程注入镜像源 -->
            <details class="mt-5 rounded-xl border border-gray-200 bg-gray-50/50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
              <summary class="cursor-pointer text-sm font-semibold text-gray-700 dark:text-gray-200">
                加速镜像（国内网络推荐）
              </summary>
              <div :class="gridClass" class="mt-4">
                <div>
                  <FieldLabel
                    label="npm 包仓库镜像"
                    tooltip="启动本地 MCP（如 npx/@modelcontextprotocol）时，把 npm 拉依赖的仓库指到这里。注入 NPM_CONFIG_REGISTRY。留空表示不覆盖（用子进程默认官方源）。仅影响本地 stdio 子进程，不影响远程上游。"
                  />
                  <input
                    v-model="npmRegistry"
                    type="url"
                    :class="inputClass"
                    placeholder="留空 = 不覆盖（用子进程默认）"
                  />
                  <p :class="hintClass">例如 https://registry.npmmirror.com</p>
                  <p v-if="npmRegistryError" :class="errClass">{{ npmRegistryError }}</p>
                </div>
                <div>
                  <FieldLabel
                    label="pip 包仓库镜像"
                    tooltip="启动本地 Python MCP（如 pip 安装的 server）时，把 pip 拉 PyPI 包的源指到这里。注入 PIP_INDEX_URL。留空表示不覆盖。仅影响本地 stdio 子进程。"
                  />
                  <input
                    v-model="pipIndexURL"
                    type="url"
                    :class="inputClass"
                    placeholder="留空 = 不覆盖（用子进程默认）"
                  />
                  <p :class="hintClass">例如 https://pypi.tuna.tsinghua.edu.cn/simple</p>
                  <p v-if="pipIndexURLError" :class="errClass">{{ pipIndexURLError }}</p>
                </div>
                <div>
                  <FieldLabel
                    label="uv 包仓库镜像"
                    tooltip="启动本地 MCP 用 uv/uvx 拉包时，把 PyPI 源指到这里。注入 UV_DEFAULT_INDEX。留空表示不覆盖。仅影响本地 stdio 子进程。"
                  />
                  <input
                    v-model="uvIndexURL"
                    type="url"
                    :class="inputClass"
                    placeholder="留空 = 不覆盖（用子进程默认）"
                  />
                  <p :class="hintClass">例如 https://pypi.tuna.tsinghua.edu.cn/simple</p>
                  <p v-if="uvIndexURLError" :class="errClass">{{ uvIndexURLError }}</p>
                </div>
              </div>
              <div class="mt-3 flex flex-wrap gap-2">
                <button
                  type="button"
                  class="rounded-lg border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 transition hover:bg-gray-100 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                  @click="npmRegistry = 'https://registry.npmmirror.com'"
                >
                  npm 用淘宝源
                </button>
                <button
                  type="button"
                  class="rounded-lg border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 transition hover:bg-gray-100 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                  @click="pipIndexURL = 'https://pypi.tuna.tsinghua.edu.cn/simple'; uvIndexURL = 'https://pypi.tuna.tsinghua.edu.cn/simple'"
                >
                  pip / uv 用清华源
                </button>
                <button
                  type="button"
                  class="rounded-lg border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 transition hover:bg-gray-100 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                  @click="npmRegistry = ''; pipIndexURL = ''; uvIndexURL = ''"
                >
                  清空（用官方源）
                </button>
              </div>
              <p :class="hintClass" class="mt-3">
                仅影响新增的 stdio 子进程；保存后即时生效无需重启。上游 env
                显式配置的同名键仍会覆盖这里的默认。
              </p>
            </details>
          </template>
          <p v-else class="text-sm text-gray-500 dark:text-gray-400">
            当前配置尚未包含本地运行时段，请重新加载系统设置。
          </p>
        </section>

        <!-- 保留期 -->
        <section
          v-show="activeTab === 'data'"
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">保留期</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            控制调用记录、统计数据与操作日志的保留时间，避免长期运行后数据无限增长。
          </p>
          <div :class="gridClass">
            <div>
              <FieldLabel
                label="调用记录保留天数"
                required
                tooltip="调用记录、调用统计和趋势数据保留时间；后台清理任务会删除超期数据。"
              />
              <input
                v-model.number="config.statistics.retention_days"
                type="number"
                min="1"
                max="3650"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 3650，默认 90。</p>
              <p v-if="fieldErrors['statistics.retention_days']" :class="errClass">
                {{ fieldErrors['statistics.retention_days'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="工具排行默认条数"
                required
                tooltip="调用统计页和相关接口未指定条数时使用的默认排行数量。"
              />
              <input
                v-model.number="config.statistics.top_limit_default"
                type="number"
                min="1"
                max="100"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 100，默认 10。</p>
              <p v-if="fieldErrors['statistics.top_limit_default']" :class="errClass">
                {{ fieldErrors['statistics.top_limit_default'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="操作日志保留天数"
                required
                tooltip="系统操作日志的保留时间，用于审计日志页面和后台清理。"
              />
              <input
                v-model.number="config.audit.retention_days"
                type="number"
                min="1"
                max="3650"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 3650，默认 180。</p>
              <p v-if="fieldErrors['audit.retention_days']" :class="errClass">
                {{ fieldErrors['audit.retention_days'] }}
              </p>
            </div>
            <div>
              <FieldLabel
                label="审计分页默认每页条数"
                required
                tooltip="审计日志接口未指定页大小时使用的默认数量。"
              />
              <input
                v-model.number="config.audit.page_size_default"
                type="number"
                min="1"
                max="200"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 200，默认 20。</p>
              <p v-if="fieldErrors['audit.page_size_default']" :class="errClass">
                {{ fieldErrors['audit.page_size_default'] }}
              </p>
            </div>
          </div>
        </section>

        <!-- 会话 -->
        <section
          v-show="activeTab === 'data'"
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">会话</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">管理后台会话令牌的有效期。</p>
          <div :class="gridClass">
            <div>
              <FieldLabel
                label="会话超时（秒）"
                required
                tooltip="管理后台登录会话的有效时间，超时后需要重新登录。"
              />
              <input
                v-model.number="config.auth.session_timeout_s"
                type="number"
                min="300"
                max="86400"
                :class="inputClass"
              />
              <p :class="hintClass">范围 300 – 86400，默认 3600。</p>
              <p v-if="fieldErrors['auth.session_timeout_s']" :class="errClass">
                {{ fieldErrors['auth.session_timeout_s'] }}
              </p>
            </div>
          </div>
        </section>

        <FloatingActionBar>
          <template #info>
            <p class="text-sm font-medium text-gray-800 dark:text-white/90">系统设置</p>
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
              可按模块切换编辑；修改后保存才会写入，服务监听配置需重启后生效。
            </p>
          </template>
          <button
            type="button"
            class="rounded-lg border border-gray-300 px-4 py-2.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            :disabled="saving"
            @click="loadSettings"
          >
            重置
          </button>
          <button
            type="submit"
            class="bg-brand-500 hover:bg-brand-600 rounded-lg px-5 py-2.5 text-sm font-medium text-white transition disabled:opacity-60"
            :disabled="saving"
          >
            {{ saving ? '保存中…' : '保存设置' }}
          </button>
        </FloatingActionBar>
      </form>
    </template>
  </AdminLayout>
</template>
