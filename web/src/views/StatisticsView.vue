<script setup lang="ts">
/**
 * 统计排行页（任务 26.5）。
 *
 * 以 TailAdmin 风格（分区卡片、表格、ApexCharts 图表）展示调用统计：
 * - 时间区间选择（start/end，本地 datetime-local → RFC3339）；
 * - 「按上游 MCP」「按 API Key」两个维度的区间调用条数（卡片网格 + 表格，大屏并排）；
 * - 工具调用排行（ApexCharts 横向柱状图，可配置 limit）。
 *
 * 覆盖 Req 16.2（按上游维度统计）、16.3（工具排行降序）、16.5（时间区间）、17.5（管理 REST API）。
 *
 * 容错：无记录时展示空态而非报错；开始晚于结束由后端返回 VALIDATION（400），
 * 前端捕获后给出整体错误提示并提供清晰说明（Req 16.7）。
 * 响应式：useBreakpoint.isLargeScreen 决定上游/API Key 两块统计为并排（大屏）或堆叠（小屏）。
 */
import { computed, onMounted, ref } from 'vue'
import type { ApexOptions } from 'apexcharts'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import { useBreakpoint } from '@/composables/useBreakpoint'
import {
  statsByUpstream,
  statsByAPIKey,
  topTools,
  type DimensionCount,
  type ToolRank,
} from '@/api/stats'
import { listUpstreams } from '@/api/upstreams'
import { listAPIKeys } from '@/api/apikeys'

const { isLargeScreen } = useBreakpoint()

/** 两块维度统计的栅格类：大屏并排两列、小屏堆叠单列。 */
const dimensionGridClass = computed(() =>
  isLargeScreen.value ? 'grid grid-cols-2 gap-6' : 'grid grid-cols-1 gap-6',
)

// ── 查询条件 ──────────────────────────────────────────────────────────────
/** datetime-local 形式的起止时间（本地时区，无时区后缀）。 */
const startLocal = ref('')
const endLocal = ref('')
/** 工具排行返回条数（缺省由后端取配置默认值）。 */
const toolLimit = ref(10)

// ── 数据与状态 ──────────────────────────────────────────────────────────────
const upstreamCounts = ref<DimensionCount[]>([])
const apiKeyCounts = ref<DimensionCount[]>([])
const toolRanks = ref<ToolRank[]>([])

/** ID → 名称映射，用于把维度标识渲染为可读名称。 */
const upstreamNames = ref<Record<string, string>>({})
const apiKeyNames = ref<Record<string, string>>({})

const loading = ref(false)
const queryError = ref('')

/** 将 datetime-local 值转换为 RFC3339；空串返回 undefined（表示不传该参数）。 */
function toRFC3339(local: string): string | undefined {
  if (local === '') return undefined
  const d = new Date(local)
  if (Number.isNaN(d.getTime())) return undefined
  return d.toISOString()
}

/** 解析后端统一错误体的 message，回退到通用文案。 */
function errorMessage(err: unknown): string {
  const body = (err as { response?: { data?: { message?: string } } })?.response?.data
  if (body?.message) return body.message
  return err instanceof Error ? err.message : '查询失败，请稍后重试'
}

/** 维度标识的可读展示：优先名称，空标识显示「(未知)」，无名称回退标识本身。 */
function upstreamLabel(id: string): string {
  if (id === '') return '(未知上游)'
  return upstreamNames.value[id] ?? id
}
function apiKeyLabel(id: string): string {
  if (id === '') return '(未知 Key)'
  return apiKeyNames.value[id] ?? id
}
/** 工具排行项的可读名称：上游名 / 原始工具名。 */
function toolLabel(t: ToolRank): string {
  const up = t.UpstreamID === '' ? '(未知上游)' : (upstreamNames.value[t.UpstreamID] ?? t.UpstreamID)
  return `${up} / ${t.OriginalName}`
}

/** 各维度合计调用条数（卡片汇总展示）。 */
const upstreamTotal = computed(() => upstreamCounts.value.reduce((s, c) => s + c.Count, 0))
const apiKeyTotal = computed(() => apiKeyCounts.value.reduce((s, c) => s + c.Count, 0))

// ── 工具排行图表（ApexCharts 横向柱状图）──────────────────────────────────
/** 图表分类标签（工具可读名，与 series 数据一一对应）。 */
const chartCategories = computed(() => toolRanks.value.map(toolLabel))
/** 图表数据系列（调用次数）。 */
const chartSeries = computed(() => [
  { name: '调用次数', data: toolRanks.value.map((t) => t.Count) },
])

/** ApexCharts 配置：横向柱状图，按次数降序（数据已由后端排序）。 */
const chartOptions = computed<ApexOptions>(() => ({
  chart: {
    type: 'bar',
    fontFamily: 'Outfit, sans-serif',
    toolbar: { show: false },
  },
  plotOptions: {
    bar: { horizontal: true, borderRadius: 4, barHeight: '60%' },
  },
  colors: ['#465fff'],
  dataLabels: { enabled: false },
  grid: { borderColor: '#e5e7eb', strokeDashArray: 4 },
  xaxis: {
    categories: chartCategories.value,
    title: { text: '调用次数' },
  },
  tooltip: { y: { formatter: (v: number) => `${v} 次` } },
}))

/** 触发一次查询（按当前条件刷新三类统计）。 */
async function runQuery(): Promise<void> {
  if (loading.value) return
  loading.value = true
  queryError.value = ''
  const range = { start: toRFC3339(startLocal.value), end: toRFC3339(endLocal.value) }
  try {
    const [ups, keys, tools] = await Promise.all([
      statsByUpstream(range),
      statsByAPIKey(range),
      topTools(range, toolLimit.value),
    ])
    upstreamCounts.value = ups
    apiKeyCounts.value = keys
    toolRanks.value = tools
  } catch (err) {
    queryError.value = errorMessage(err)
  } finally {
    loading.value = false
  }
}

/** 加载维度名称映射（上游、API Key），失败不阻塞统计展示。 */
async function loadNameMaps(): Promise<void> {
  try {
    const [ups, keys] = await Promise.all([listUpstreams(), listAPIKeys()])
    upstreamNames.value = Object.fromEntries(ups.map((u) => [u.id, u.config.name]))
    apiKeyNames.value = Object.fromEntries(keys.map((k) => [k.id, k.name]))
  } catch {
    // 名称映射失败时回退为标识展示，不影响统计查询。
  }
}

onMounted(async () => {
  await loadNameMaps()
  await runQuery()
})

/** 通用样式类（TailAdmin 风格）。 */
const inputClass =
  'h-11 w-full rounded-lg border border-gray-300 bg-transparent px-4 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90'
const labelClass = 'mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-400'
const cardClass =
  'rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]'
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="调用统计" />

    <!-- 查询条件 -->
    <section :class="cardClass">
      <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">查询条件</h3>
      <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
        选择时间区间与工具排行条数后查询；区间为空表示自最早记录起至当前时刻（Req 16.5）。
      </p>
      <div class="grid grid-cols-1 gap-x-6 gap-y-5 md:grid-cols-2 xl:grid-cols-4">
        <div>
          <label :class="labelClass">开始时间</label>
          <input v-model="startLocal" type="datetime-local" :class="inputClass" />
        </div>
        <div>
          <label :class="labelClass">结束时间</label>
          <input v-model="endLocal" type="datetime-local" :class="inputClass" />
        </div>
        <div>
          <label :class="labelClass">工具排行条数</label>
          <input
            v-model.number="toolLimit"
            type="number"
            min="1"
            max="100"
            :class="inputClass"
          />
          <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">范围 1 – 100，默认取系统配置。</p>
        </div>
        <div class="flex items-end">
          <button
            type="button"
            class="h-11 w-full rounded-lg bg-brand-500 px-5 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-60"
            :disabled="loading"
            @click="runQuery"
          >
            {{ loading ? '查询中…' : '查询' }}
          </button>
        </div>
      </div>

      <p
        v-if="queryError !== ''"
        class="mt-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
      >
        {{ queryError }}
      </p>
    </section>

    <!-- 维度统计：上游 MCP / API Key（大屏并排，小屏堆叠）-->
    <div :class="dimensionGridClass" class="mt-6">
      <!-- 按上游 MCP -->
      <section :class="cardClass">
        <div class="mb-4 flex items-center justify-between">
          <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">按上游 MCP</h3>
          <span class="rounded-full bg-brand-50 px-3 py-1 text-xs font-medium text-brand-600 dark:bg-brand-500/10 dark:text-brand-400">
            合计 {{ upstreamTotal }} 次
          </span>
        </div>
        <div class="overflow-x-auto">
          <table class="min-w-full text-sm">
            <thead>
              <tr class="border-b border-gray-100 text-left text-gray-500 dark:border-gray-800 dark:text-gray-400">
                <th class="px-3 py-2.5 font-medium">上游 MCP</th>
                <th class="px-3 py-2.5 text-right font-medium">调用条数</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="c in upstreamCounts"
                :key="c.ID"
                class="border-b border-gray-50 text-gray-700 dark:border-gray-800/60 dark:text-gray-300"
              >
                <td class="px-3 py-2.5">{{ upstreamLabel(c.ID) }}</td>
                <td class="px-3 py-2.5 text-right tabular-nums">{{ c.Count }}</td>
              </tr>
              <tr v-if="upstreamCounts.length === 0">
                <td colspan="2" class="px-3 py-8 text-center text-gray-400 dark:text-gray-500">
                  暂无统计数据
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>

      <!-- 按 API Key -->
      <section :class="cardClass">
        <div class="mb-4 flex items-center justify-between">
          <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">按 API Key</h3>
          <span class="rounded-full bg-brand-50 px-3 py-1 text-xs font-medium text-brand-600 dark:bg-brand-500/10 dark:text-brand-400">
            合计 {{ apiKeyTotal }} 次
          </span>
        </div>
        <div class="overflow-x-auto">
          <table class="min-w-full text-sm">
            <thead>
              <tr class="border-b border-gray-100 text-left text-gray-500 dark:border-gray-800 dark:text-gray-400">
                <th class="px-3 py-2.5 font-medium">API Key</th>
                <th class="px-3 py-2.5 text-right font-medium">调用条数</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="c in apiKeyCounts"
                :key="c.ID"
                class="border-b border-gray-50 text-gray-700 dark:border-gray-800/60 dark:text-gray-300"
              >
                <td class="px-3 py-2.5">{{ apiKeyLabel(c.ID) }}</td>
                <td class="px-3 py-2.5 text-right tabular-nums">{{ c.Count }}</td>
              </tr>
              <tr v-if="apiKeyCounts.length === 0">
                <td colspan="2" class="px-3 py-8 text-center text-gray-400 dark:text-gray-500">
                  暂无统计数据
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </section>
    </div>

    <!-- 工具调用排行（ApexCharts 横向柱状图）-->
    <section :class="cardClass" class="mt-6">
      <h3 class="mb-1 text-base font-semibold text-gray-800 dark:text-white/90">工具调用排行</h3>
      <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
        按调用次数降序排列，至多展示 {{ toolLimit }} 条（Req 16.3）。
      </p>
      <div v-if="toolRanks.length > 0">
        <apexchart
          type="bar"
          :height="Math.max(240, toolRanks.length * 44)"
          :options="chartOptions"
          :series="chartSeries"
        />
      </div>
      <div
        v-else
        class="py-12 text-center text-sm text-gray-400 dark:text-gray-500"
      >
        暂无工具调用记录
      </div>
    </section>
  </AdminLayout>
</template>
