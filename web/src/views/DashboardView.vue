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
} from '@/icons'
import { listUpstreams } from '@/api/upstreams'
import { listAPIKeys } from '@/api/apikeys'
import { statsByUpstream } from '@/api/stats'

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
  { to: '/apikeys', label: 'API Key 管理', desc: '签发与管控访问密钥', icon: UserGroupIcon },
  { to: '/rules', label: '规则管理', desc: '工具屏蔽与别名规则', icon: ListIcon },
  { to: '/statistics', label: '调用统计', desc: '查看调用排行与趋势', icon: BarChartIcon },
  { to: '/audit', label: '审计日志', desc: '追溯关键操作记录', icon: BoxCubeIcon },
  { to: '/settings', label: '系统设置', desc: '调整网关运行参数', icon: SettingsIcon },
]

/** 计算最近 7 天的 RFC3339 起点（当前时刻向前 7 天）。 */
function sevenDaysAgoRFC3339(): string {
  const d = new Date(Date.now() - 7 * 24 * 60 * 60 * 1000)
  return d.toISOString()
}

/** 解析错误信息，回退通用文案。 */
function errorMessage(err: unknown): string {
  const body = (err as { response?: { data?: { message?: string } } })?.response?.data
  if (body?.message) return body.message
  return err instanceof Error ? err.message : '数据加载失败，请稍后重试'
}

async function loadOverview(): Promise<void> {
  loading.value = true
  loadError.value = ''
  try {
    const [upstreams, apiKeys, counts] = await Promise.all([
      listUpstreams(),
      listAPIKeys(),
      statsByUpstream({ start: sevenDaysAgoRFC3339() }),
    ])

    upstreamTotal.value = upstreams.length
    upstreamEnabled.value = upstreams.filter((u) => u.config.enabled).length
    upstreamConnected.value = upstreams.filter((u) => u.state === 'available').length

    apiKeyTotal.value = apiKeys.length
    apiKeyEnabled.value = apiKeys.filter((k) => k.enabled).length

    recentCalls.value = counts.reduce((sum, c) => sum + c.Count, 0)
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
    <PageBreadcrumb pageTitle="Dashboard" />

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
