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
 * - statsByUpstream（@/api/stats，最近 7 天）→ 调用量卡片；
 * - getToolSummary（@/api/tools）→ 有效工具卡片。
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
  WarningIcon,
  SuccessIcon,
} from '@/icons'
import { listUpstreams, type Upstream } from '@/api/upstreams'
import { listAPIKeys, type APIKey } from '@/api/apikeys'
import { statsByUpstream } from '@/api/stats'
import { getToolSummary } from '@/api/tools'
import { getSecuritySummary, type SecuritySummary } from '@/api/security'

const loading = ref(true)
const loadError = ref('')
const upstreamsSnapshot = ref<Upstream[]>([])
const apiKeysSnapshot = ref<APIKey[]>([])
const securitySummary = ref<SecuritySummary | null>(null)

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

/** 指标卡描述。 */
interface MetricCard {
  key: string
  label: string
  value: number
  hint: string
  icon: Component
}

interface ActionItem {
  key: string
  title: string
  desc: string
  to: string
  action: string
  icon: Component
  tone: 'brand' | 'warning' | 'error' | 'success'
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

const actionItems = computed<ActionItem[]>(() => {
  const upstreams = upstreamsSnapshot.value
  const apiKeys = apiKeysSnapshot.value
  const enabledUpstreams = upstreams.filter((u) => u.config.enabled)
  const unavailableEnabled = enabledUpstreams.filter((u) => u.state !== 'available')
  const security = securitySummary.value
  const securityEvents24h =
    (security?.AuthFailures24h ?? 0) +
    (security?.ACLDenies24h ?? 0) +
    (security?.HighRiskSubjects24h ?? 0)
  const items: ActionItem[] = []

  if (upstreams.length === 0) {
    items.push({
      key: 'no-upstream',
      title: '还没有上游 MCP',
      desc: '先接入一个上游服务，网关才有工具可以对外提供。',
      to: '/upstreams',
      action: '去接入',
      icon: PlugInIcon,
      tone: 'brand',
    })
  }

  if (unavailableEnabled.length > 0) {
    items.push({
      key: 'unavailable-upstream',
      title: `${unavailableEnabled.length} 个启用的上游不可用`,
      desc: '查看最近错误，必要时调整连接参数或手动重连。',
      to: '/upstreams',
      action: '查看上游',
      icon: WarningIcon,
      tone: 'error',
    })
  }

  if (enabledUpstreams.length > 0 && effectiveToolCount.value === 0) {
    items.push({
      key: 'no-tools',
      title: '当前没有可用工具',
      desc: '刷新工具列表，确认上游已返回可聚合的工具。',
      to: '/upstreams',
      action: '刷新工具',
      icon: BoxCubeIcon,
      tone: 'warning',
    })
  }

  if (apiKeys.length === 0) {
    items.push({
      key: 'no-apikey',
      title: '还没有 API Key',
      desc: '创建访问密钥后，客户端才能调用聚合后的 MCP 服务。',
      to: '/apikeys',
      action: '创建 Key',
      icon: UserGroupIcon,
      tone: 'brand',
    })
  }

  if (upstreams.length > 0 && apiKeys.length > 0 && recentCalls.value === 0) {
    items.push({
      key: 'no-calls',
      title: '最近 7 天没有调用记录',
      desc: '打开 API 服务页复制接入示例，完成一次真实调用验证。',
      to: '/api-service',
      action: '查看接入',
      icon: BarChartIcon,
      tone: 'brand',
    })
  }

  if ((security?.ActiveBlocks ?? 0) > 0 || securityEvents24h > 0) {
    items.push({
      key: 'security',
      title:
        (security?.ActiveBlocks ?? 0) > 0
          ? `${security?.ActiveBlocks ?? 0} 条来源正在封禁`
          : '最近 24 小时有安全事件',
      desc: '检查异常来源、ACL 拒绝和高风险访问，确认是否需要解除或调整规则。',
      to: '/security',
      action: '打开安全中心',
      icon: FlagIcon,
      tone: (security?.ActiveBlocks ?? 0) > 0 ? 'error' : 'warning',
    })
  }

  if (items.length === 0) {
    items.push({
      key: 'all-good',
      title: '当前没有待处理事项',
      desc: '核心配置和近期运行状态看起来都正常。',
      to: '/statistics',
      action: '查看统计',
      icon: SuccessIcon,
      tone: 'success',
    })
  }

  return items
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
  try {
    const [upstreams, apiKeys, counts, tools, security] = await Promise.all([
      listUpstreams(),
      listAPIKeys(),
      statsByUpstream({ start: sevenDaysAgoRFC3339() }),
      getToolSummary(),
      getSecuritySummary().catch(() => null),
    ])

    upstreamsSnapshot.value = upstreams
    apiKeysSnapshot.value = apiKeys
    securitySummary.value = security

    upstreamTotal.value = upstreams.length
    upstreamEnabled.value = upstreams.filter((u) => u.config.enabled).length
    upstreamConnected.value = upstreams.filter((u) => u.state === 'available').length

    apiKeyTotal.value = apiKeys.length
    apiKeyEnabled.value = apiKeys.filter((k) => k.enabled).length

    recentCalls.value = counts.reduce((sum, c) => sum + c.Count, 0)
    effectiveToolCount.value = tools.count
  } catch (err) {
    loadError.value = errorMessage(err)
  } finally {
    loading.value = false
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

    <!-- 待处理事项 -->
    <section :class="cardClass" class="mt-6">
      <div class="mb-5 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">待处理事项</h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            根据当前配置和近期运行状态给出下一步建议。
          </p>
        </div>
        <button
          type="button"
          class="rounded-lg border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 transition hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          :disabled="loading"
          @click="loadOverview"
        >
          刷新
        </button>
      </div>

      <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
        <div
          v-if="loading"
          class="min-h-[116px] rounded-xl border border-gray-200 bg-gray-50/70 p-4 text-sm text-gray-500 dark:border-gray-800 dark:bg-white/[0.03] dark:text-gray-400"
        >
          正在分析当前运行状态...
        </div>
        <template v-else>
          <router-link
            v-for="item in actionItems"
            :key="item.key"
            :to="item.to"
            class="group flex min-h-[116px] items-start gap-4 rounded-xl border p-4 transition hover:-translate-y-0.5"
            :class="[
              item.tone === 'error'
                ? 'border-error-200 bg-error-50/70 hover:border-error-300 dark:border-error-500/20 dark:bg-error-500/10 dark:hover:border-error-500/40'
                : item.tone === 'warning'
                  ? 'border-warning-200 bg-warning-50/70 hover:border-warning-300 dark:border-warning-500/20 dark:bg-warning-500/10 dark:hover:border-warning-500/40'
                  : item.tone === 'success'
                    ? 'border-success-200 bg-success-50/70 hover:border-success-300 dark:border-success-500/20 dark:bg-success-500/10 dark:hover:border-success-500/40'
                    : 'border-gray-200 bg-white hover:border-brand-300 hover:bg-brand-50/40 dark:border-gray-800 dark:bg-white/[0.03] dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.06]',
            ]"
          >
            <span
              class="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg"
              :class="[
                item.tone === 'error'
                  ? 'bg-error-100 text-error-600 dark:bg-error-500/15 dark:text-error-400'
                  : item.tone === 'warning'
                    ? 'bg-warning-100 text-warning-600 dark:bg-warning-500/15 dark:text-warning-400'
                    : item.tone === 'success'
                      ? 'bg-success-100 text-success-600 dark:bg-success-500/15 dark:text-success-400'
                      : 'bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400',
              ]"
            >
              <component :is="item.icon" class="h-5 w-5" />
            </span>
            <span class="min-w-0 flex-1">
              <span class="block text-sm font-semibold text-gray-800 dark:text-white/90">
                {{ item.title }}
              </span>
              <span class="mt-1 block text-sm leading-6 text-gray-500 dark:text-gray-400">
                {{ item.desc }}
              </span>
              <span
                class="mt-3 inline-flex items-center text-sm font-medium text-brand-600 group-hover:text-brand-700 dark:text-brand-400"
              >
                {{ item.action }}
                <span aria-hidden="true" class="ml-1">→</span>
              </span>
            </span>
          </router-link>
        </template>
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
