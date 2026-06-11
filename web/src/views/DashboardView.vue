<script setup lang="ts">
/**
 * 仪表盘总览页。
 *
 * 以 TailAdmin 指标卡（rounded-2xl 边框卡片 + 图标 + 标签 + 数值）展示网关关键概览：
 * - 上游 MCP：总数 / 已启用 / 已连接（state=available）；
 * - API Key：总数 / 已启用；
 * - 最近 7 天调用量：对 statsByUpstream 按区间求和。
 * 另提供「快速入口」区，链接到各管理页面。
 *
 * 数据源：
 * - listUpstreams（@/api/upstreams）→ 上游卡片；
 * - listAPIKeys（@/api/apikeys）→ API Key 卡片；
 * - statsByUpstream（@/api/stats，最近 7 天）→ 调用量卡片。
 *
 * 容错：统一 loading / error 状态；任一数据加载失败给出整体错误提示并支持重试。
 * 响应式：指标卡网格在手机 1 列、平板 2 列、大屏 4 列（Tailwind 断点）。
 */
import { computed, onMounted, ref, type Component } from 'vue'
import { RouterLink } from 'vue-router'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import {
  PlugInIcon,
  UserGroupIcon,
  BarChartIcon,
  BoxCubeIcon,
  ListIcon,
  SettingsIcon,
  FlagIcon,
  Message2Line,
} from '@/icons'
import { listUpstreams } from '@/api/upstreams'
import { listAPIKeys } from '@/api/apikeys'
import { statsByUpstream } from '@/api/stats'
import { getSettings, updateSettings, type MCPMode, type YAMLConfig } from '@/api/settings'
import { getAggregatedTools } from '@/api/tools'
import { useToast } from '@/composables/useToast'

const toast = useToast()

const loading = ref(true)
const loadError = ref('')

// 上游 MCP 概览
const upstreamTotal = ref(0)
const upstreamEnabled = ref(0)
const upstreamConnected = ref(0)

// API Key 概览
const apiKeyTotal = ref(0)
const apiKeyEnabled = ref(0)

// 最近 7 天调用量
const recentCalls = ref(0)
const effectiveToolCount = ref(0)

const settings = ref<YAMLConfig | null>(null)
const modeSaving = ref(false)
const modeError = ref('')

/** 指标卡描述。 */
interface MetricCard {
  key: string
  label: string
  value: number
  hint: string
  icon: Component
}

const cards = computed<MetricCard[]>(() => [
  {
    key: 'upstreams',
    label: '上游 MCP',
    value: upstreamTotal.value,
    hint: `已启用 ${upstreamEnabled.value} · 已连接 ${upstreamConnected.value}`,
    icon: PlugInIcon,
  },
  {
    key: 'apikeys',
    label: 'API Key',
    value: apiKeyTotal.value,
    hint: `已启用 ${apiKeyEnabled.value}`,
    icon: UserGroupIcon,
  },
  {
    key: 'tools',
    label: '有效工具',
    value: effectiveToolCount.value,
    hint: '当前聚合后的真实工具数',
    icon: BoxCubeIcon,
  },
  {
    key: 'calls',
    label: '最近 7 天调用量',
    value: recentCalls.value,
    hint: '聚合各上游调用条数',
    icon: BarChartIcon,
  },
])

/** 快速入口项。 */
const quickLinks: ReadonlyArray<{ to: string; label: string; desc: string; icon: Component }> = [
  { to: '/upstreams', label: '上游 MCP 管理', desc: '配置与连接上游服务', icon: PlugInIcon },
  { to: '/api-service', label: 'API 服务', desc: '查看接入地址与服务状态', icon: BoxCubeIcon },
  { to: '/call-records', label: '调用记录', desc: '追踪工具调用明细', icon: BarChartIcon },
  { to: '/apikeys', label: 'API Key 管理', desc: '签发与管控访问密钥', icon: UserGroupIcon },
  { to: '/rules', label: '规则管理', desc: '工具屏蔽与别名规则', icon: ListIcon },
  { to: '/statistics', label: '调用统计', desc: '查看调用排行与趋势', icon: BarChartIcon },
  { to: '/audit', label: '审计日志', desc: '追溯关键操作记录', icon: FlagIcon },
  { to: '/system-logs', label: '系统日志', desc: '查看运行时日志输出', icon: Message2Line },
  { to: '/settings', label: '系统设置', desc: '调整网关运行参数', icon: SettingsIcon },
]

const modeOptions: ReadonlyArray<{
  value: MCPMode
  title: string
  desc: string
  note: string
}> = [
  {
    value: 'smart',
    title: '智能模式',
    desc: '智能发现并按需调度真实工具，让客户端少加载、更精准、更省上下文。',
    note: '适合工具多、上下文宝贵且需要更高命中效率的场景',
  },
  {
    value: 'full',
    title: '全量模式',
    desc: '客户端连接后直接拿到当前可见的全部工具。',
    note: '适合工具数量较少、客户端需要完整工具清单的场景',
  },
]

const currentMode = computed<MCPMode | null>(() => settings.value?.mcp_api.mode ?? null)
const currentModeLabel = computed(() => {
  if (currentMode.value === null) return '加载中'
  return currentMode.value === 'full' ? '全量模式' : '智能模式'
})
const currentModeClass = computed(() => {
  if (currentMode.value === null) return 'bg-gray-100 text-gray-600 dark:bg-white/5 dark:text-gray-400'
  return currentMode.value === 'full'
    ? 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
    : 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
})

/** 计算最近 7 天的 RFC3339 起点（当前时刻向前 7 天）。 */
function sevenDaysAgoRFC3339(): string {
  const d = new Date(Date.now() - 7 * 24 * 60 * 60 * 1000)
  return d.toISOString()
}

/** 解析错误信息，回退通用文案。 */
function errorMessage(err: unknown): string {
  // 请求层已将失败统一为 Error（ApiError），其 message 即后端中文提示。
  return err instanceof Error ? err.message : '数据加载失败，请稍后重试'
}

async function loadOverview(): Promise<void> {
  loading.value = true
  loadError.value = ''
  modeError.value = ''
  try {
    const [upstreams, apiKeys, counts, cfg, aggregated] = await Promise.all([
      listUpstreams(),
      listAPIKeys(),
      statsByUpstream({ start: sevenDaysAgoRFC3339() }),
      getSettings(),
      getAggregatedTools(),
    ])

    upstreamTotal.value = upstreams.length
    upstreamEnabled.value = upstreams.filter((u) => u.config.enabled).length
    upstreamConnected.value = upstreams.filter((u) => u.state === 'available').length

    apiKeyTotal.value = apiKeys.length
    apiKeyEnabled.value = apiKeys.filter((k) => k.enabled).length

    recentCalls.value = counts.reduce((sum, c) => sum + c.Count, 0)
    effectiveToolCount.value = aggregated.count
    settings.value = cfg
  } catch (err) {
    loadError.value = errorMessage(err)
  } finally {
    loading.value = false
  }
}

async function switchMode(mode: MCPMode): Promise<void> {
  if (settings.value === null || modeSaving.value || currentMode.value === mode) return
  modeError.value = ''
  modeSaving.value = true
  const next: YAMLConfig = {
    ...settings.value,
    mcp_api: {
      ...settings.value.mcp_api,
      mode,
    },
  }
  try {
    settings.value = await updateSettings(next)
    toast.success('对外服务模式已更新')
  } catch (err) {
    modeError.value = errorMessage(err)
  } finally {
    modeSaving.value = false
  }
}

onMounted(loadOverview)

const cardClass =
  'rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]'
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="仪表盘" />

    <!-- 错误提示 -->
    <div
      v-if="loadError !== ''"
      class="mb-6 flex items-center justify-between rounded-lg bg-error-50 px-4 py-3 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
    >
      <span>{{ loadError }}</span>
      <button
        type="button"
        class="rounded-md border border-error-200 px-3 py-1 text-xs font-medium hover:bg-error-100 dark:border-error-500/30 dark:hover:bg-error-500/10"
        @click="loadOverview"
      >
        重试
      </button>
    </div>

    <!-- 指标卡网格：手机 1 列、平板 2 列、大屏 4 列 -->
    <div class="grid grid-cols-1 gap-5 sm:grid-cols-2 xl:grid-cols-4">
      <div v-for="card in cards" :key="card.key" :class="cardClass">
        <div
          class="flex h-12 w-12 items-center justify-center rounded-xl bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400"
        >
          <component :is="card.icon" class="h-6 w-6" />
        </div>
        <div class="mt-5">
          <span class="text-sm text-gray-500 dark:text-gray-400">{{ card.label }}</span>
          <h4
            class="mt-1 text-2xl font-bold text-gray-800 tabular-nums dark:text-white/90"
          >
            <span v-if="loading" class="text-gray-300 dark:text-gray-600">—</span>
            <span v-else>{{ card.value }}</span>
          </h4>
          <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">{{ card.hint }}</p>
        </div>
      </div>
    </div>

    <!-- 对外服务模式 -->
    <section :class="cardClass" class="mt-6">
      <div class="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">对外 MCP 模式</h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            控制外部客户端连接网关后看到工具的方式。
          </p>
        </div>
        <span
          class="rounded-full px-2.5 py-1 text-xs font-medium"
          :class="currentModeClass"
        >
          {{ currentModeLabel }}
        </span>
      </div>

      <p
        v-if="modeError !== ''"
        class="mt-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
      >
        {{ modeError }}
      </p>

      <div class="mt-5 grid grid-cols-1 gap-4 lg:grid-cols-2">
        <button
          v-for="mode in modeOptions"
          :key="mode.value"
          type="button"
          class="group flex min-h-36 flex-col rounded-xl border p-4 text-left transition disabled:cursor-not-allowed disabled:opacity-70"
          :class="
            currentMode === mode.value
              ? 'border-brand-300 bg-brand-50/60 shadow-theme-sm dark:border-brand-500/50 dark:bg-brand-500/[0.08]'
              : 'border-gray-200 bg-white hover:border-brand-300 hover:bg-brand-50/40 dark:border-gray-800 dark:bg-white/[0.02] dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.06]'
          "
          :disabled="loading || settings === null || modeSaving"
          @click="switchMode(mode.value)"
        >
          <span class="flex items-center justify-between gap-3">
            <span class="text-sm font-semibold text-gray-800 dark:text-white/90">
              {{ mode.title }}
            </span>
            <span
              class="flex h-5 w-5 items-center justify-center rounded-full border transition"
              :class="
                currentMode === mode.value
                  ? 'border-brand-500 bg-brand-500 text-white'
                  : 'border-gray-300 text-transparent dark:border-gray-700'
              "
            >
              <span class="h-2 w-2 rounded-full bg-current"></span>
            </span>
          </span>
          <span class="mt-3 text-sm leading-6 text-gray-600 dark:text-gray-300">
            {{ mode.desc }}
          </span>
          <span class="mt-auto pt-4 text-xs text-gray-400 dark:text-gray-500">
            {{ mode.note }}
          </span>
        </button>
      </div>
    </section>

    <!-- 快速入口 -->
    <section :class="cardClass" class="mt-6">
      <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">快速入口</h3>
      <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">前往各管理模块进行配置与查看。</p>
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
        <router-link
          v-for="link in quickLinks"
          :key="link.to"
          :to="link.to"
          class="group flex items-center gap-4 rounded-xl border border-gray-200 p-4 transition hover:border-brand-300 hover:bg-brand-50/40 dark:border-gray-800 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.06]"
        >
          <span
            class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-gray-100 text-gray-600 group-hover:bg-brand-100 group-hover:text-brand-600 dark:bg-white/5 dark:text-gray-400 dark:group-hover:bg-brand-500/10 dark:group-hover:text-brand-400"
          >
            <component :is="link.icon" class="h-5 w-5" />
          </span>
          <span class="min-w-0">
            <span class="block text-sm font-medium text-gray-800 dark:text-white/90">
              {{ link.label }}
            </span>
            <span class="block truncate text-xs text-gray-500 dark:text-gray-400">
              {{ link.desc }}
            </span>
          </span>
        </router-link>
      </div>
    </section>
  </AdminLayout>
</template>
