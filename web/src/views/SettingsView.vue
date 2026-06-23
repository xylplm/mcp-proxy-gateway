<script setup lang="ts">
/**
 * 系统设置页（任务 26.4）。
 *
 * 以 TailAdmin 表单组件风格编辑网关运行参数：同步 cron、连接/重试超时、统计/审计保留期、
 * 会话超时。个人密码、对外服务模式与 API 服务接入已拆到更贴近使用场景的页面。
 *
 * 系统设置页：配置同步、连接、安全与管理后台参数。
 *
 * 校验策略：字段范围由后端统一强制（见 internal/config/validate.go），前端仅以 min/max
 * 提示与 number 输入辅助，并在保存失败时按后端返回的 fields 将错误定位到对应字段。
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
import {
  exportBackup,
  importBackup,
  previewBackup,
  type BackupPreview,
} from '@/api/backup'

const { isLargeScreen } = useBreakpoint()
const toast = useToast()
const { confirm } = useConfirm()

/** 分区内表单栅格类：大屏两列、小屏单列。 */
const gridClass = computed(() =>
  isLargeScreen.value ? 'grid grid-cols-2 gap-x-6 gap-y-5' : 'grid grid-cols-1 gap-y-5',
)

/** 当前配置表单模型（加载后填充）。 */
const config = ref<YAMLConfig | null>(null)

const trustedProxyCIDRs = ref('')
const exemptCIDRs = ref('')

/** Port helpers: strip ':prefix on load, restore on save. */
const adminPort = ref<number|string>('')
const publicMCPPort = ref<number|string>('')

function addrToPort(addr: string): number|string {
  const s = addr.trim()
  if (s === '') return ''
  const m = s.match(/^(?:[\d.]+|\[?[\da-f:]+\]?)?:(\d+)$/i)
  if (m) return Number(m[1])
  return s
}

function portToAddr(port: number|string): string {
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

const routingStrategies: ReadonlyArray<{ value: ToolRoutingStrategy; label: string; desc: string }> = [
  {
    value: 'round_robin',
    label: '均衡分配',
    desc: '在同名工具的多个来源之间轮询调用，适合多个账号或渠道共同分摊额度。',
  },
  {
    value: 'priority_fill',
    label: '优先可用上游',
    desc: '按上游排序优先调用第一个可用且未超额的来源，适合主备或优先级明确的场景。',
  },
]

const securityModes = [
  { value: 'monitor', label: '仅记录', desc: '记录异常事件，不自动拦截来源。' },
  { value: 'enforce', label: '自动封禁', desc: '达到阈值后临时封禁异常来源。' },
  { value: 'off', label: '关闭', desc: '不记录鉴权失败事件，也不触发自动封禁。' },
] as const

const trustedProxyCIDRError = computed(() => firstIndexedError('security.trusted_proxy_cidrs'))
const exemptCIDRError = computed(() => firstIndexedError('security.exempt_cidrs'))

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

function syncSecurityTextFields(): void {
  if (config.value === null) return
  trustedProxyCIDRs.value = cidrListToText(config.value.security.trusted_proxy_cidrs ?? [])
  exemptCIDRs.value = cidrListToText(config.value.security.exempt_cidrs ?? [])
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
  formError.value =
    body?.message ?? (err instanceof Error ? err.message : '保存失败，请稍后重试')
}

/** 保存常规配置。 */
async function saveSettings(): Promise<void> {
  if (config.value === null || saving.value) return
  const ok = await confirm({
    title: '确认保存系统设置',
    message: '保存后网关会自动重启以应用最新配置。重启期间管理台和对外 MCP 服务会短暂不可用，请确认当前没有关键调用正在进行。',
    confirmText: '保存并重启',
    cancelText: '取消',
    tone: 'warning',
  })
  if (!ok) return

  clearErrors()
  // Restore port strings from numeric port inputs before saving
  config.value.server.admin_addr = portToAddr(adminPort.value)
  config.value.server.public_mcp_addr = portToAddr(publicMCPPort.value)
  config.value.security.trusted_proxy_cidrs = textToCIDRList(trustedProxyCIDRs.value)
  config.value.security.exempt_cidrs = textToCIDRList(exemptCIDRs.value)
  saving.value = true
  try {
    config.value = await updateSettings(config.value, { restart: true })
    adminPort.value = addrToPort(config.value.server.admin_addr)
    publicMCPPort.value = addrToPort(config.value.server.public_mcp_addr)
    syncSecurityTextFields()
    toast.success('系统设置已保存，网关正在重启')
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
    message: '导入后会覆盖当前系统设置、上游、API Key、规则和白名单，并自动重启网关。建议先导出当前备份。',
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
      class="rounded-2xl border border-error-200 bg-error-50 px-5 py-4 text-sm text-error-600 dark:border-error-500/30 dark:bg-error-500/10 dark:text-error-400"
    >
      {{ loadError }}
      <button type="button" class="ml-2 underline" @click="loadSettings">重试</button>
    </p>

    <template v-else-if="config !== null">
      <p
        v-if="formError !== ''"
        class="mb-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
      >
        {{ formError }}
      </p>

      <form class="flex flex-col gap-6 pb-28" @submit.prevent="saveSettings">
        <!-- 配置备份 -->
        <section
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
              <p class="mt-2 text-xs text-warning-600 dark:text-warning-400">
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
            class="mt-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
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
                class="rounded-lg bg-warning-500 px-4 py-2.5 text-sm font-medium text-white transition hover:bg-warning-600 disabled:opacity-60"
                :disabled="backupImporting"
                @click="confirmImportBackup"
              >
                {{ backupImporting ? '导入中…' : '导入并覆盖' }}
              </button>
            </div>
            <div class="mt-4 grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
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
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">同步</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            工具列表自动同步的调度与超时设置。
          </p>
          <div :class="gridClass">
            <div>
              <FieldLabel label="同步 cron 表达式" required tooltip="用于定期刷新上游 MCP 工具列表，服务端会校验 cron 格式。" />
              <input
                v-model="config.sync.cron"
                type="text"
                placeholder="如：0 */30 * * * *"
                :class="inputClass"
              />
              <p :class="hintClass">标准 6 段 cron 表达式，由服务端校验合法性。</p>
              <p v-if="fieldErrors['sync.cron']" :class="errClass">{{ fieldErrors['sync.cron'] }}</p>
            </div>
            <div>
              <FieldLabel label="同步超时（秒）" required tooltip="单个上游工具同步允许等待的最长时间。" />
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
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">超时与重试</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            上游连接、重试退避与聚合调用的超时设置。
          </p>
          <div :class="gridClass">
            <div>
              <FieldLabel label="连接建立超时（秒）" required tooltip="建立上游 MCP 连接时允许等待的最长时间。" />
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
              <FieldLabel label="初始退避（秒）" required tooltip="连接失败后首次重试前等待的时间。" />
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
              <FieldLabel label="退避倍数" required tooltip="连续失败时每次重试等待时间的放大倍数。" />
              <input
                v-model.number="config.connection.retry_multiplier"
                type="number"
                min="1"
                :class="inputClass"
              />
              <p :class="hintClass">需大于等于 1，默认 5。</p>
              <p v-if="fieldErrors['connection.retry_multiplier']" :class="errClass">
                {{ fieldErrors['connection.retry_multiplier'] }}
              </p>
            </div>
            <div>
              <FieldLabel label="退避上限（秒）" required tooltip="自动重试等待时间不会超过这个上限。" />
              <input
                v-model.number="config.connection.retry_max_backoff_s"
                type="number"
                min="1"
                max="86400"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 86400，默认 3600。</p>
              <p v-if="fieldErrors['connection.retry_max_backoff_s']" :class="errClass">
                {{ fieldErrors['connection.retry_max_backoff_s'] }}
              </p>
            </div>
            <div>
              <FieldLabel label="连续失败阈值" required tooltip="达到该失败次数后暂停该上游自动重试。" />
              <input
                v-model.number="config.connection.failure_threshold"
                type="number"
                min="1"
                max="100"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1 – 100，默认 10；达到阈值后熔断暂停。</p>
              <p v-if="fieldErrors['connection.failure_threshold']" :class="errClass">
                {{ fieldErrors['connection.failure_threshold'] }}
              </p>
            </div>
            <div>
              <FieldLabel label="上游调用超时（秒）" required tooltip="转发工具调用到上游 MCP 时允许等待的最长时间。" />
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
              <FieldLabel label="工具调用策略" required tooltip="当同名工具来自多个上游时，网关实际调用某个来源上游的选择方式。" />
              <div class="mt-1 grid grid-cols-1 gap-3 lg:grid-cols-2">
                <label
                  v-for="item in routingStrategies"
                  :key="item.value"
                  class="cursor-pointer rounded-lg border p-3 transition"
                  :class="
                    config.aggregation.tool_routing_strategy === item.value
                      ? 'border-brand-300 bg-brand-50/70 dark:border-brand-500/50 dark:bg-brand-500/[0.08]'
                      : 'border-gray-200 hover:border-brand-200 hover:bg-brand-50/40 dark:border-gray-800 dark:hover:border-brand-500/30 dark:hover:bg-brand-500/[0.06]'
                  "
                >
                  <input
                    v-model="config.aggregation.tool_routing_strategy"
                    class="sr-only"
                    type="radio"
                    :value="item.value"
                  />
                  <span class="block text-sm font-medium text-gray-800 dark:text-white/90">{{ item.label }}</span>
                  <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{ item.desc }}</span>
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
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">服务监听</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            管理端口用于后台操作；对外 MCP 可启用独立端口，便于公网暴露时叠加更严格的反向代理和安全策略。
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
              <p :class="hintClass">范围 1–65535，默认 8080。公网部署建议只在内网或反向代理内侧暴露。</p>
              <p v-if="fieldErrors['server.admin_addr']" :class="errClass">
                {{ fieldErrors['server.admin_addr'] }}
              </p>
            </div>
            <div>
              <FieldLabel label="独立 MCP 监听端口" tooltip="对外 MCP 服务的独立监听端口号。留空表示不启用独立端口。" />
              <input
                v-model.number="publicMCPPort"
                type="number"
                min="1"
                max="65535"
                placeholder="8081"
                :class="inputClass"
              />
              <p :class="hintClass">范围 1–65535。留空表示不启用独立端口，启用后可只把该端口暴露到公网。</p>
              <p v-if="fieldErrors['server.public_mcp_addr']" :class="errClass">
                {{ fieldErrors['server.public_mcp_addr'] }}
              </p>
            </div>
          </div>
          <div class="mt-5 rounded-xl border border-gray-100 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.02]">
            <div class="flex flex-wrap items-start justify-between gap-4">
              <div class="min-w-0">
                <FieldLabel label="管理端口同时暴露 MCP" tooltip="关闭后，管理监听地址不再注册 /mcp/*；对外客户端只能访问独立 MCP 监听地址。" />
                <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                  启用独立 MCP 监听地址后，建议关闭此项，让管理端口只服务后台；未配置独立端口时不可关闭。
                </p>
              </div>
              <button
                type="button"
                role="switch"
                :aria-checked="config.server.expose_mcp_on_admin_addr"
                class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition"
                :class="config.server.expose_mcp_on_admin_addr ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
                @click="config.server.expose_mcp_on_admin_addr = !config.server.expose_mcp_on_admin_addr"
              >
                <span
                  class="inline-block h-4 w-4 transform rounded-full bg-white transition"
                  :class="config.server.expose_mcp_on_admin_addr ? 'translate-x-6' : 'translate-x-1'"
                ></span>
              </button>
            </div>
          </div>
          <div class="mt-5">
            <FieldLabel label="日志级别" required tooltip="控制进程日志输出的详细程度。保存后立即生效，无需重启服务。debug 会记录调用链每次工具调用的入口与结果，便于排查问题但日志量较大。" />
            <select
              v-model="config.server.log_level"
              :class="inputClass"
            >
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
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">对外 API 默认值</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            对外 MCP 服务在智能模式下使用的默认参数。
          </p>
          <div :class="gridClass">
            <div>
              <FieldLabel label="智能模式默认返回工具数" required tooltip="智能模式下，网关工具列表发现接口默认返回的真实工具摘要数量。" />
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
          </div>
        </section>

        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <div class="mb-5 flex flex-wrap items-start justify-between gap-3">
            <div>
              <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">安全防护</h3>
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
                  : 'border-gray-200 hover:border-brand-200 hover:bg-brand-50/40 dark:border-gray-800 dark:hover:border-brand-500/30 dark:hover:bg-brand-500/[0.06]'
              "
            >
              <input v-model="config.security.mode" class="sr-only" type="radio" :value="item.value" />
              <span class="block text-sm font-medium text-gray-800 dark:text-white/90">{{ item.label }}</span>
              <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">{{ item.desc }}</span>
            </label>
          </div>
          <p v-if="fieldErrors['security.mode']" :class="errClass">{{ fieldErrors['security.mode'] }}</p>

          <div :class="gridClass">
            <div>
              <FieldLabel label="失败统计窗口（秒）" required tooltip="在该时间窗口内累计鉴权失败和来源拒绝次数。" />
              <input v-model.number="config.security.failure_window_s" type="number" min="60" max="3600" :class="inputClass" />
              <p :class="hintClass">范围 60 - 3600，默认 300。</p>
              <p v-if="fieldErrors['security.failure_window_s']" :class="errClass">{{ fieldErrors['security.failure_window_s'] }}</p>
            </div>
            <div>
              <FieldLabel label="单 IP 失败阈值" required tooltip="同一来源 IP 在统计窗口内允许的无效 API Key 尝试次数。" />
              <input v-model.number="config.security.max_failures_per_ip" type="number" min="1" max="10000" :class="inputClass" />
              <p :class="hintClass">默认 30。公网暴露时可适当收紧。</p>
              <p v-if="fieldErrors['security.max_failures_per_ip']" :class="errClass">{{ fieldErrors['security.max_failures_per_ip'] }}</p>
            </div>
            <div>
              <FieldLabel label="疑似 Key 失败阈值" required tooltip="同一疑似 API Key 指纹在统计窗口内允许失败的次数。" />
              <input v-model.number="config.security.max_failures_per_key_fingerprint" type="number" min="1" max="10000" :class="inputClass" />
              <p :class="hintClass">默认 8。用于识别持续尝试同一无效凭证的来源。</p>
              <p v-if="fieldErrors['security.max_failures_per_key_fingerprint']" :class="errClass">{{ fieldErrors['security.max_failures_per_key_fingerprint'] }}</p>
            </div>
            <div>
              <FieldLabel label="ACL 拒绝阈值" required tooltip="同一 API Key 与来源 IP 在统计窗口内被来源白名单拒绝的次数。" />
              <input v-model.number="config.security.max_acl_denies_per_key_ip" type="number" min="1" max="10000" :class="inputClass" />
              <p :class="hintClass">默认 5。命中后封禁该 API Key 与来源 IP 的组合。</p>
              <p v-if="fieldErrors['security.max_acl_denies_per_key_ip']" :class="errClass">{{ fieldErrors['security.max_acl_denies_per_key_ip'] }}</p>
            </div>
            <div>
              <FieldLabel label="首次封禁时长（秒）" required tooltip="第一次达到自动封禁阈值后的临时封禁时长。" />
              <input v-model.number="config.security.first_block_duration_s" type="number" min="60" max="86400" :class="inputClass" />
              <p :class="hintClass">默认 900，即 15 分钟。</p>
              <p v-if="fieldErrors['security.first_block_duration_s']" :class="errClass">{{ fieldErrors['security.first_block_duration_s'] }}</p>
            </div>
            <div>
              <FieldLabel label="最长自动封禁（秒）" required tooltip="重复触发封禁升级后允许达到的最长封禁时长。" />
              <input v-model.number="config.security.max_block_duration_s" type="number" min="60" max="604800" :class="inputClass" />
              <p :class="hintClass">默认 86400，即 24 小时。</p>
              <p v-if="fieldErrors['security.max_block_duration_s']" :class="errClass">{{ fieldErrors['security.max_block_duration_s'] }}</p>
            </div>
            <div>
              <FieldLabel label="封禁升级窗口（秒）" required tooltip="在该时间内重复触发同一对象封禁时会逐步延长封禁时长。" />
              <input v-model.number="config.security.escalation_window_s" type="number" min="300" max="604800" :class="inputClass" />
              <p :class="hintClass">默认 86400，即 24 小时。</p>
              <p v-if="fieldErrors['security.escalation_window_s']" :class="errClass">{{ fieldErrors['security.escalation_window_s'] }}</p>
            </div>
            <div>
              <FieldLabel label="可信代理 CIDR" tooltip="每行一个 IP 或 CIDR。只有这些代理来源的转发头会被用于识别真实客户端 IP。" />
              <textarea v-model="trustedProxyCIDRs" rows="4" :class="[inputClass, 'h-auto py-3 font-mono']" placeholder="10.0.0.0/8"></textarea>
              <p :class="hintClass">部署在反向代理后时填写代理出口地址。</p>
              <p v-if="trustedProxyCIDRError" :class="errClass">{{ trustedProxyCIDRError }}</p>
            </div>
            <div>
              <FieldLabel label="自动封禁豁免 CIDR" tooltip="每行一个 IP 或 CIDR。匹配来源仍会记录事件，但不会被自动封禁。" />
              <textarea v-model="exemptCIDRs" rows="4" :class="[inputClass, 'h-auto py-3 font-mono']" placeholder="192.168.0.0/16"></textarea>
              <p :class="hintClass">适合内网监控、探活或固定可信出口。</p>
              <p v-if="exemptCIDRError" :class="errClass">{{ exemptCIDRError }}</p>
            </div>
          </div>
        </section>

        <!-- 保留期 -->
        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">保留期</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            控制调用记录、统计数据与操作日志的保留时间，避免长期运行后数据无限增长。
          </p>
          <div :class="gridClass">
            <div>
              <FieldLabel label="调用记录保留天数" required tooltip="调用记录、调用统计和趋势数据保留时间；后台清理任务会删除超期数据。" />
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
              <FieldLabel label="工具排行默认条数" required tooltip="调用统计页和相关接口未指定条数时使用的默认排行数量。" />
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
              <FieldLabel label="操作日志保留天数" required tooltip="系统操作日志的保留时间，用于审计日志页面和后台清理。" />
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
              <FieldLabel label="审计分页默认每页条数" required tooltip="审计日志接口未指定页大小时使用的默认数量。" />
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
          class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">会话</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            管理后台会话令牌的有效期。
          </p>
          <div :class="gridClass">
            <div>
              <FieldLabel label="会话超时（秒）" required tooltip="管理后台登录会话的有效时间，超时后需要重新登录。" />
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
            <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">修改后保存才会写入；服务监听配置需重启后生效。</p>
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
            class="rounded-lg bg-brand-500 px-5 py-2.5 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-60"
            :disabled="saving"
          >
            {{ saving ? '保存中…' : '保存设置' }}
          </button>
        </FloatingActionBar>
      </form>

    </template>
  </AdminLayout>
</template>
