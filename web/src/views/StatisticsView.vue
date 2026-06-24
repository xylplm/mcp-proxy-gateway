<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, ref, watch } from 'vue'
import { nextTick, onUnmounted } from 'vue'
import type { ApexOptions } from 'apexcharts'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import {
  callHealth,
  dailyStats,
  statsByAPIKey,
  statsByUpstream,
  statsSummary,
  topToolErrors,
  topTools,
  type CallHealth,
  type CallHealthToolRank,
  type CallHealthUpstreamRank,
  type DailyCount,
  type DimensionCount,
  type StatsSummary,
  type TimeRangeQuery,
  type ToolErrorRank,
  type ToolRank,
} from '@/api/stats'
import { listAPIKeys } from '@/api/apikeys'
import { listUpstreams } from '@/api/upstreams'
import { getAggregatedTools } from '@/api/tools'

const ApexChart = defineAsyncComponent(() => import('vue3-apexcharts'))

const startLocal = ref('')
const endLocal = ref('')
const loading = ref(false)
const queryError = ref('')
const healthWindow = ref<'1h' | '24h'>('1h')
const health = ref<CallHealth>(emptyHealth('1h'))
const summary = ref<StatsSummary>(emptySummary())
const daily = ref<DailyCount[]>([])
const upstreamCounts = ref<DimensionCount[]>([])
const apiKeyCounts = ref<DimensionCount[]>([])
const toolRanks = ref<ToolRank[]>([])
const toolErrors = ref<ToolErrorRank[]>([])
const upstreamNames = ref<Record<string, string>>({})
const apiKeyNames = ref<Record<string, string>>({})
const toolDescriptions = ref<Record<string, string>>({})
let queryTimer: number | undefined
let statsRequestSeq = 0
const heatmapLegendLevels = [0, 1, 2, 3, 4] as const
const heatmapContainerRef = ref<HTMLElement | null>(null)
const heatmapContainerWidth = ref(0)
const HEATMAP_MIN_CELL = 14
const HEATMAP_MAX_CELL = 36
const HEATMAP_MAX_WIDTH = 2400

// 方块大小随容器线性缩放：手机 ~14px，4K ~36px。gap 也同步缩放。
function computeHeatmapCellPx(width: number): number {
  if (width <= 0) return HEATMAP_MIN_CELL
  const t = Math.min(1, Math.max(0, (width - 400) / (HEATMAP_MAX_WIDTH - 400)))
  return Math.round(HEATMAP_MIN_CELL + t * (HEATMAP_MAX_CELL - HEATMAP_MIN_CELL))
}

function computeHeatmapGap(width: number): number {
  if (width <= 0) return 2
  return Math.round(2 + Math.min(1, Math.max(0, (width - 400) / (HEATMAP_MAX_WIDTH - 400))) * 3)
}

// 先定方块大小，再算能放多少列，列数×7=天数。
function computeHeatmapDayCount(width: number): number {
  if (width <= 0) return 364
  const cell = computeHeatmapCellPx(width)
  const gap = computeHeatmapGap(width)
  const cols = Math.max(4, Math.floor((width + gap) / (cell + gap)))
  return cols * 7
}

const heatmapDayCount = computed(() => computeHeatmapDayCount(heatmapContainerWidth.value))
const heatmapCellPx = computed(() => computeHeatmapCellPx(heatmapContainerWidth.value))
const heatmapGap = computed(() => computeHeatmapGap(heatmapContainerWidth.value))
const cardClass =
  'rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]'
const inputClass =
  'h-10 rounded-lg border border-gray-300 bg-transparent px-3 text-sm text-gray-800 shadow-sm focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90'
const labelClass = 'text-xs font-medium text-gray-500 dark:text-gray-400'

function emptySummary(): StatsSummary {
  return {
    TotalCalls: 0,
    SuccessCalls: 0,
    FailureCalls: 0,
    ActiveUpstreams: 0,
    ActiveAPIKeys: 0,
    UniqueTools: 0,
    AvgLatencyMS: 0,
    P95LatencyMS: 0,
  }
}

function emptyHealth(window: '1h' | '24h'): CallHealth {
  return {
    Window: window,
    Since: '',
    Until: '',
    TotalCalls: 0,
    SuccessCalls: 0,
    FailureCalls: 0,
    SuccessRate: 0,
    P50LatencyMS: 0,
    P95LatencyMS: 0,
    TopErrorTools: [],
    TopSlowTools: [],
    TopUpstreams: [],
  }
}

function toRFC3339(local: string): string | undefined {
  if (local === '') return undefined
  const d = new Date(local)
  if (Number.isNaN(d.getTime())) return undefined
  return d.toISOString()
}

// 统计后端按 UTC 日聚合；热力图网格也用 UTC 日期 key，避免浏览器时区造成日期错位。
const statsTZ = 'UTC'

// 按 tz 时区把任意 Date 格式化为 YYYY-MM-DD（en-CA locale 默认输出 ISO 日期）。
// 网格日期与后端 Day 都经此函数生成 key，保证两端「一天」的边界完全一致。
function localDayKey(date: Date, tz: string): string {
  return new Intl.DateTimeFormat('en-CA', {
    timeZone: tz,
    year: 'numeric',
    month: '2-digit',
    day: '2-digit',
  }).format(date)
}

function range(): TimeRangeQuery {
  return { start: toRFC3339(startLocal.value), end: toRFC3339(endLocal.value), tz: statsTZ }
}

function formatInt(value: number): string {
  return Math.round(value).toLocaleString('zh-CN')
}

function formatMs(value: number): string {
  if (value <= 0) return '0 ms'
  return `${Math.round(value).toLocaleString('zh-CN')} ms`
}

function formatPercent(value: number): string {
  return `${value.toFixed(1)}%`
}

function formatDate(value: string | Date): string {
  const d = typeof value === 'string' ? new Date(value) : value
  if (Number.isNaN(d.getTime())) return '-'
  return d.toLocaleDateString('zh-CN', { timeZone: statsTZ, month: '2-digit', day: '2-digit' })
}

function formatFullDate(value: string | Date): string {
  const d = typeof value === 'string' ? new Date(value) : value
  if (Number.isNaN(d.getTime())) return '-'
  return d.toLocaleDateString('zh-CN', { timeZone: statsTZ })
}

function upstreamLabel(id: string): string {
  if (id === '') return '(未知上游)'
  return upstreamNames.value[id] ?? id
}

// API Key 维度标签：空 ID 时按 Source 区分——小智接入显示「小智接入」，其余才是真正的未知。
// 非 NULL 维度显示 Key 名称，缺失时回退标识。
function apiKeyLabel(item: DimensionCount): string {
  if (item.ID === '') {
    if (item.Source === 'xiaozhi') return '小智接入'
    return '(未知 Key)'
  }
  return apiKeyNames.value[item.ID] ?? item.ID
}

function toolLabel(t: Pick<ToolRank, 'UpstreamID' | 'OriginalName'>): string {
  return `${upstreamLabel(t.UpstreamID)} / ${t.OriginalName}`
}

function healthToolLabel(t: Pick<CallHealthToolRank, 'UpstreamID' | 'UpstreamName' | 'OriginalName' | 'ExposedName'>): string {
  return t.ExposedName || t.OriginalName || `${t.UpstreamName || upstreamLabel(t.UpstreamID)}`
}

function healthToolSubLabel(t: Pick<CallHealthToolRank, 'UpstreamID' | 'UpstreamName' | 'OriginalName'>): string {
  return `${t.UpstreamName || upstreamLabel(t.UpstreamID)} / ${t.OriginalName || '-'}`
}

function healthUpstreamLabel(item: Pick<CallHealthUpstreamRank, 'UpstreamID' | 'UpstreamName'>): string {
  return item.UpstreamName || upstreamLabel(item.UpstreamID)
}

// 工具描述用 "upstreamId:originalName" 做键，缺失时返回空串（指令会自动不显示 tooltip）。
function toolDescription(t: Pick<ToolRank, 'UpstreamID' | 'OriginalName'>): string {
  return toolDescriptions.value[`${t.UpstreamID}:${t.OriginalName}`] ?? ''
}

function heatmapLevelClass(level: number): string {
  if (level === 1) return 'bg-success-100 dark:bg-success-500/20'
  if (level === 2) return 'bg-success-300 dark:bg-success-500/40'
  if (level === 3) return 'bg-success-500/80 dark:bg-success-500/70'
  if (level >= 4) return 'bg-success-600 dark:bg-success-400'
  return 'bg-gray-100 dark:bg-gray-800'
}

function successRate(item = summary.value): number {
  return item.TotalCalls === 0 ? 0 : (item.SuccessCalls / item.TotalCalls) * 100
}

function failureRate(item = summary.value): number {
  return item.TotalCalls === 0 ? 0 : (item.FailureCalls / item.TotalCalls) * 100
}

const busiestDay = computed(() =>
  daily.value.reduce<DailyCount | null>((best, item) => {
    if (best === null || item.TotalCalls > best.TotalCalls) return item
    return best
  }, null),
)

const recentSeven = computed(() => daily.value.slice(-7))
const previousSeven = computed(() => daily.value.slice(-14, -7))

const recentSevenTotal = computed(() =>
  recentSeven.value.reduce((sum, item) => sum + item.TotalCalls, 0),
)
const previousSevenTotal = computed(() =>
  previousSeven.value.reduce((sum, item) => sum + item.TotalCalls, 0),
)
const weeklyDelta = computed(() => {
  if (previousSevenTotal.value === 0) return recentSevenTotal.value > 0 ? 100 : 0
  return ((recentSevenTotal.value - previousSevenTotal.value) / previousSevenTotal.value) * 100
})

const busiestTool = computed(() => toolRanks.value[0] ?? null)
const topErrorTool = computed(() => toolErrors.value[0] ?? null)
const healthErrorTools = computed(() => health.value.TopErrorTools ?? [])
const healthSlowTools = computed(() => health.value.TopSlowTools ?? [])
const healthUpstreams = computed(() => health.value.TopUpstreams ?? [])

function healthToneClass(rate: number): string {
  if (rate >= 99) return 'text-success-700 dark:text-success-400'
  if (rate >= 95) return 'text-warning-700 dark:text-warning-400'
  return 'text-error-600 dark:text-error-400'
}

const heatmapDays = computed(() => {
  const byDay = new Map<string, DailyCount>()
  for (const item of daily.value) {
    byDay.set(localDayKey(new Date(item.Day), statsTZ), item)
  }
  const end = endLocal.value === '' ? new Date() : new Date(endLocal.value)
  if (Number.isNaN(end.getTime())) end.setTime(Date.now())
  end.setUTCHours(0, 0, 0, 0)
  const start = new Date(end)
  const dayCount = heatmapDayCount.value
  start.setUTCDate(start.getUTCDate() - (dayCount - 1))
  const days: Array<{ key: string; date: Date; item: DailyCount | null; level: number }> = []
  const max = Math.max(1, ...daily.value.map((item) => item.TotalCalls))
  for (let i = 0; i < dayCount; i += 1) {
    const date = new Date(start)
    date.setUTCDate(start.getUTCDate() + i)
    const key = localDayKey(date, statsTZ)
    const item = byDay.get(key) ?? null
    const count = item?.TotalCalls ?? 0
    const level = count === 0 ? 0 : Math.min(4, Math.ceil((count / max) * 4))
    days.push({ key, date, item, level })
  }
  return days
})

const heatmapTotal = computed(() =>
  heatmapDays.value.reduce((sum, day) => sum + (day.item?.TotalCalls ?? 0), 0),
)

const trendCategories = computed(() => daily.value.map((item) => formatDate(item.Day)))
const trendSeries = computed(() => [
  { name: '成功', data: daily.value.map((item) => item.SuccessCalls) },
  { name: '失败', data: daily.value.map((item) => item.FailureCalls) },
])
const trendOptions = computed<ApexOptions>(() => ({
  chart: { type: 'area', toolbar: { show: false }, fontFamily: 'Outfit, sans-serif' },
  colors: ['#12b76a', '#f04438'],
  dataLabels: { enabled: false },
  stroke: { curve: 'smooth', width: 2 },
  fill: { type: 'gradient', gradient: { opacityFrom: 0.28, opacityTo: 0.02 } },
  grid: { borderColor: '#e5e7eb', strokeDashArray: 4 },
  xaxis: { categories: trendCategories.value, labels: { rotate: 0 } },
  yaxis: { labels: { formatter: (value: number) => Math.round(value).toString() } },
  tooltip: { y: { formatter: (value: number) => `${formatInt(value)} 次` } },
  legend: { position: 'top', horizontalAlign: 'right' },
}))

// 工具调用排行改用手写条形列表：第一行工具名、第二行所属 MCP（小字），
// 避免长拼接名在图表轴标签里被截断，并与相邻卡片风格保持一致。
const toolRankTotal = computed(() => toolRanks.value.reduce((sum, t) => sum + t.Count, 0))
const toolRankMax = computed(() => toolRanks.value.reduce((m, t) => Math.max(m, t.Count), 0))

const upstreamDistribution = computed(() => topDimensionItems(upstreamCounts.value, (item) => upstreamLabel(item.ID)))
const apiKeyDistribution = computed(() => topDimensionItems(apiKeyCounts.value, apiKeyLabel))

function topDimensionItems(items: DimensionCount[], labeler: (item: DimensionCount) => string) {
  const total = items.reduce((sum, item) => sum + item.Count, 0)
  return items.slice(0, 6).map((item) => ({
    key: `${item.ID}:${item.Source ?? ''}`,
    id: item.ID,
    label: labeler(item),
    count: item.Count,
    percent: total === 0 ? 0 : (item.Count / total) * 100,
  }))
}

async function loadNameMaps(): Promise<void> {
  try {
    const [ups, keys, tools] = await Promise.all([
      listUpstreams(),
      listAPIKeys(),
      getAggregatedTools(),
    ])
    upstreamNames.value = Object.fromEntries(ups.map((u) => [u.id, u.config.name]))
    apiKeyNames.value = Object.fromEntries(keys.map((k) => [k.id, k.name]))
    toolDescriptions.value = Object.fromEntries(
      tools.tools
        .filter((t) => t.description.trim() !== '')
        .map((t) => [`${t.upstreamId}:${t.originalName}`, t.description]),
    )
  } catch {
    // 名称映射失败时回退为标识展示。
  }
}

async function loadStats(): Promise<void> {
  const requestSeq = ++statsRequestSeq
  loading.value = true
  queryError.value = ''
  const currentRange = range()
  try {
    const [sum, days, ups, keys, tools, errors] = await Promise.all([
      statsSummary(currentRange),
      dailyStats(currentRange),
      statsByUpstream(currentRange),
      statsByAPIKey(currentRange),
      topTools(currentRange),
      topToolErrors(currentRange),
    ])
    if (requestSeq !== statsRequestSeq) return
    summary.value = sum
    daily.value = days
    upstreamCounts.value = ups
    apiKeyCounts.value = keys
    toolRanks.value = tools
    toolErrors.value = errors
  } catch (err) {
    if (requestSeq !== statsRequestSeq) return
    queryError.value = err instanceof Error ? err.message : '加载统计失败'
  } finally {
    if (requestSeq === statsRequestSeq) loading.value = false
  }
}

async function loadHealth(): Promise<void> {
  try {
    health.value = await callHealth(healthWindow.value)
  } catch (err) {
    queryError.value = err instanceof Error ? err.message : '加载健康数据失败'
  }
}

function scheduleLoadStats(): void {
  if (queryTimer !== undefined) window.clearTimeout(queryTimer)
  queryTimer = window.setTimeout(() => void loadStats(), 350)
}

watch([startLocal, endLocal], scheduleLoadStats)
watch(healthWindow, () => void loadHealth())

onMounted(async () => {
  // 热力图：nextTick 后 ref 已绑定，再挂 ResizeObserver
  await nextTick()
  const el = heatmapContainerRef.value
  if (el) {
    heatmapContainerWidth.value = el.clientWidth
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        heatmapContainerWidth.value = entry.contentRect.width
      }
    })
    ro.observe(el)
    onUnmounted(() => ro.disconnect())
  }
  await loadNameMaps()
  await Promise.all([loadStats(), loadHealth()])
})
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="调用统计" />

    <div class="mb-5 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold text-gray-800 dark:text-white/90">调用概览</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          调整时间后自动刷新；统计按 UTC 日期聚合。
        </p>
      </div>
      <div class="flex flex-wrap items-end gap-3">
        <label class="block">
          <span :class="labelClass">开始时间</span>
          <input v-model="startLocal" type="datetime-local" :class="inputClass" />
        </label>
        <label class="block">
          <span :class="labelClass">结束时间</span>
          <input v-model="endLocal" type="datetime-local" :class="inputClass" />
        </label>
      </div>
    </div>

    <p
      v-if="queryError !== ''"
      class="bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400 mb-4 rounded-lg px-4 py-2.5 text-sm"
    >
      {{ queryError }}
    </p>

    <div class="grid grid-cols-1 gap-5 xl:grid-cols-[minmax(0,1.45fr)_minmax(360px,0.85fr)]">
      <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <section :class="cardClass">
          <div class="text-sm text-gray-500 dark:text-gray-400">总调用</div>
          <div class="mt-2 text-2xl font-semibold text-gray-800 dark:text-white/90">
            {{ formatInt(summary.TotalCalls) }}
          </div>
          <div class="mt-2 text-xs text-gray-500 dark:text-gray-400">
            近 7 日 {{ weeklyDelta >= 0 ? '+' : '' }}{{ formatPercent(weeklyDelta) }}
          </div>
        </section>
        <section :class="cardClass">
          <div class="text-sm text-gray-500 dark:text-gray-400">成功率</div>
          <div class="mt-2 text-2xl font-semibold" :class="healthToneClass(successRate())">
            {{ formatPercent(successRate()) }}
          </div>
          <div class="mt-2 text-xs text-gray-500 dark:text-gray-400">
            失败 {{ formatInt(summary.FailureCalls) }} 次，{{ formatPercent(failureRate()) }}
          </div>
        </section>
        <section :class="cardClass">
          <div class="text-sm text-gray-500 dark:text-gray-400">响应耗时</div>
          <div class="mt-2 text-2xl font-semibold text-gray-800 dark:text-white/90">
            {{ formatMs(summary.P95LatencyMS) }}
          </div>
          <div class="mt-2 text-xs text-gray-500 dark:text-gray-400">
            平均 {{ formatMs(summary.AvgLatencyMS) }}
          </div>
        </section>
        <section :class="cardClass">
          <div class="text-sm text-gray-500 dark:text-gray-400">活跃资源</div>
          <div class="mt-2 text-2xl font-semibold text-gray-800 dark:text-white/90">
            {{ formatInt(summary.UniqueTools) }} 工具
          </div>
          <div class="mt-2 text-xs text-gray-500 dark:text-gray-400">
            {{ formatInt(summary.ActiveUpstreams) }} 上游 / {{ formatInt(summary.ActiveAPIKeys) }} Key
          </div>
        </section>
      </div>

      <section :class="[cardClass, 'flex flex-col']">
        <div class="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">近期健康</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">快速判断当前窗口是否异常。</p>
          </div>
          <div class="inline-flex rounded-lg border border-gray-200 p-1 dark:border-gray-800">
            <button
              v-for="item in ['1h', '24h']"
              :key="item"
              type="button"
              class="rounded-md px-3 py-1.5 text-xs font-medium transition"
              :class="healthWindow === item ? 'bg-brand-50 text-brand-700 dark:bg-brand-500/10 dark:text-brand-300' : 'text-gray-500 hover:bg-gray-50 dark:text-gray-400 dark:hover:bg-gray-800'"
              @click="healthWindow = item as '1h' | '24h'"
            >
              {{ item === '1h' ? '1h' : '24h' }}
            </button>
          </div>
        </div>

        <div class="mt-4 grid grid-cols-2 gap-3">
          <div class="rounded-lg bg-gray-50 px-3 py-2.5 dark:bg-gray-800/60">
            <div class="text-xs text-gray-500 dark:text-gray-400">成功率</div>
            <div class="mt-1 text-xl font-semibold" :class="healthToneClass(health.SuccessRate)">
              {{ formatPercent(health.SuccessRate) }}
            </div>
            <div class="mt-1 text-[11px] text-gray-500 dark:text-gray-400">
              {{ formatInt(health.TotalCalls) }} 次调用
            </div>
          </div>
          <div class="rounded-lg bg-gray-50 px-3 py-2.5 dark:bg-gray-800/60">
            <div class="text-xs text-gray-500 dark:text-gray-400">延迟</div>
            <div class="mt-1 text-xl font-semibold text-gray-800 dark:text-white/90">
              {{ formatMs(health.P95LatencyMS) }}
            </div>
            <div class="mt-1 text-[11px] text-gray-500 dark:text-gray-400">
              p50 {{ formatMs(health.P50LatencyMS) }}
            </div>
          </div>
        </div>

        <div class="mt-4 space-y-3">
          <div class="rounded-lg border border-gray-200 p-3 dark:border-gray-800">
            <div class="mb-2 flex items-center justify-between gap-2">
              <span class="text-xs font-medium text-gray-500 dark:text-gray-400">主要异常</span>
              <span class="text-xs text-gray-400 dark:text-gray-500">{{ formatInt(health.FailureCalls) }} 次失败</span>
            </div>
            <div v-if="healthErrorTools[0]" class="min-w-0">
              <div class="truncate text-sm font-medium text-gray-800 dark:text-white/90">
                {{ healthToolLabel(healthErrorTools[0]) }}
              </div>
              <div class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400">
                {{ healthToolSubLabel(healthErrorTools[0]) }} · {{ formatInt(healthErrorTools[0].FailureCalls) }} 次失败
              </div>
            </div>
            <div v-else class="py-1 text-sm text-gray-400">暂无错误</div>
          </div>

          <div class="rounded-lg border border-gray-200 p-3 dark:border-gray-800">
            <div class="mb-2 text-xs font-medium text-gray-500 dark:text-gray-400">失败上游</div>
            <div class="space-y-2">
              <div
                v-for="item in healthUpstreams.slice(0, 3)"
                :key="item.UpstreamID || item.UpstreamName"
                class="flex items-start justify-between gap-3"
              >
                <div class="min-w-0">
                  <div class="truncate text-sm text-gray-800 dark:text-white/90">{{ healthUpstreamLabel(item) }}</div>
                  <div class="truncate text-xs text-gray-500 dark:text-gray-400">成功率 {{ formatPercent(item.SuccessRate) }}</div>
                </div>
                <span class="shrink-0 text-sm font-semibold text-error-600 dark:text-error-400">{{ formatInt(item.FailureCalls) }}</span>
              </div>
              <div v-if="healthUpstreams.length === 0" class="py-1 text-sm text-gray-400">暂无失败</div>
            </div>
          </div>
        </div>
      </section>
    </div>

    <div class="mt-6 grid grid-cols-1 gap-5 xl:grid-cols-[minmax(0,1.6fr)_minmax(360px,1fr)]">
      <section :class="cardClass">
        <div class="mb-4 flex items-center justify-between gap-3">
          <div>
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">调用趋势</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">按 UTC 日查看成功与失败调用。</p>
          </div>
          <span
            v-if="busiestDay"
            class="rounded-full bg-gray-100 px-3 py-1 text-xs text-gray-600 dark:bg-gray-800 dark:text-gray-300"
          >
            峰值 {{ formatFullDate(busiestDay.Day) }} · {{ formatInt(busiestDay.TotalCalls) }} 次
          </span>
        </div>
        <ApexChart
          v-if="daily.length > 0"
          type="area"
          height="320"
          :options="trendOptions"
          :series="trendSeries"
        />
        <div v-else class="py-16 text-center text-sm text-gray-400 dark:text-gray-500">
          暂无趋势数据
        </div>
      </section>

      <section :class="cardClass">
        <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">趋势摘要</h3>
        <div class="mt-4 grid grid-cols-1 gap-3">
          <div class="rounded-lg bg-gray-50 px-4 py-3 dark:bg-gray-800/60">
            <div class="text-xs text-gray-500 dark:text-gray-400">近 7 日调用</div>
            <div class="mt-1 text-xl font-semibold text-gray-800 dark:text-white/90">
              {{ formatInt(recentSevenTotal) }}
            </div>
            <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              较前 7 日 {{ weeklyDelta >= 0 ? '+' : '' }}{{ formatPercent(weeklyDelta) }}
            </div>
          </div>
          <div class="rounded-lg bg-gray-50 px-4 py-3 dark:bg-gray-800/60">
            <div class="text-xs text-gray-500 dark:text-gray-400">最热工具</div>
            <div class="mt-1 truncate text-xl font-semibold text-gray-800 dark:text-white/90">
              {{ busiestTool ? busiestTool.OriginalName : '-' }}
            </div>
            <div class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400">
              {{ busiestTool ? upstreamLabel(busiestTool.UpstreamID) : '暂无调用记录' }}
            </div>
          </div>
          <div class="rounded-lg bg-gray-50 px-4 py-3 dark:bg-gray-800/60">
            <div class="text-xs text-gray-500 dark:text-gray-400">近期慢工具</div>
            <div class="mt-1 truncate text-xl font-semibold text-gray-800 dark:text-white/90">
              {{ healthSlowTools[0] ? healthToolLabel(healthSlowTools[0]) : '-' }}
            </div>
            <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">
              {{ healthSlowTools[0] ? `p95 ${formatMs(healthSlowTools[0].P95LatencyMS)}` : '暂无慢调用' }}
            </div>
          </div>
        </div>
      </section>
    </div>

    <section :class="[cardClass, 'mt-6']">
      <div class="mb-4 flex flex-wrap items-end justify-between gap-3">
        <div>
          <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">调用热力图</h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">按 UTC 日查看最近调用密度。</p>
          <p class="mt-0.5 text-xs text-gray-400 dark:text-gray-500">围绕当前结束日期展示每日调用密度。</p>
        </div>
        <div class="flex flex-wrap items-center gap-4 text-xs text-gray-500 dark:text-gray-400">
          <span>合计 {{ formatInt(heatmapTotal) }} 次</span>
          <span>近 7 日 {{ formatInt(recentSevenTotal) }} 次</span>
          <div class="flex items-center gap-1.5">
            <span>少</span>
            <span
              v-for="level in heatmapLegendLevels"
              :key="level"
              class="rounded-[3px] border border-gray-200 dark:border-gray-800"
              :style="{ height: `${Math.max(12, heatmapCellPx - 2)}px`, width: `${Math.max(12, heatmapCellPx - 2)}px` }"
              :class="heatmapLevelClass(level)"
            />
            <span>多</span>
          </div>
        </div>
      </div>
      <div class="overflow-x-auto pb-1">
        <div ref="heatmapContainerRef" class="w-full">
          <div class="grid grid-flow-col grid-rows-7" :style="{ gridTemplateRows: `repeat(7, ${heatmapCellPx}px)`, gridAutoColumns: `${heatmapCellPx}px`, gap: `${heatmapGap}px` }">
          <AppTooltip
            v-for="day in heatmapDays"
            :key="day.key"
            :content="`${formatFullDate(day.date)}：${formatInt(day.item?.TotalCalls ?? 0)} 次，失败 ${formatInt(day.item?.FailureCalls ?? 0)} 次`"
            placement="top"
          >
            <span
              class="block shrink-0 rounded-[4px] border border-gray-200 dark:border-gray-800"
              :style="{ height: `${heatmapCellPx}px`, width: `${heatmapCellPx}px` }"
              :class="heatmapLevelClass(day.level)"
            />
          </AppTooltip>
          </div>
        </div>
      </div>
    </section>

    <div class="mt-6 grid grid-cols-1 gap-5 xl:grid-cols-2">
      <section :class="cardClass">
        <h3 class="mb-4 text-base font-semibold text-gray-800 dark:text-white/90">上游调用分布</h3>
        <div class="space-y-3">
          <div v-for="item in upstreamDistribution" :key="item.key">
            <div class="mb-1 flex justify-between gap-3 text-sm">
              <span class="truncate text-gray-700 dark:text-gray-300">{{ item.label }}</span>
              <span class="shrink-0 text-gray-500 tabular-nums">{{ formatInt(item.count) }}</span>
            </div>
            <div class="h-2 rounded-full bg-gray-100 dark:bg-gray-800">
              <div
                class="bg-brand-500 h-2 rounded-full"
                :style="{ width: `${item.percent}%` }"
              ></div>
            </div>
          </div>
          <div
            v-if="upstreamDistribution.length === 0"
            class="py-8 text-center text-sm text-gray-400"
          >
            暂无上游统计
          </div>
        </div>
      </section>

      <section :class="cardClass">
        <h3 class="mb-4 text-base font-semibold text-gray-800 dark:text-white/90">
          API Key 调用分布
        </h3>
        <div class="space-y-3">
          <div v-for="item in apiKeyDistribution" :key="item.key">
            <div class="mb-1 flex justify-between gap-3 text-sm">
              <span class="truncate text-gray-700 dark:text-gray-300">{{ item.label }}</span>
              <span class="shrink-0 text-gray-500 tabular-nums">{{ formatInt(item.count) }}</span>
            </div>
            <div class="h-2 rounded-full bg-gray-100 dark:bg-gray-800">
              <div
                class="bg-success-500 h-2 rounded-full"
                :style="{ width: `${item.percent}%` }"
              ></div>
            </div>
          </div>
          <div
            v-if="apiKeyDistribution.length === 0"
            class="py-8 text-center text-sm text-gray-400"
          >
            暂无 API Key 统计
          </div>
        </div>
      </section>
    </div>

    <div class="mt-6 grid grid-cols-1 gap-5 xl:grid-cols-2">
      <section :class="cardClass">
        <div class="mb-4 flex items-end justify-between gap-3">
          <div>
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">工具调用排行</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">优先排查高频工具的流量集中度。</p>
          </div>
          <span
            v-if="toolRanks.length > 0"
            class="shrink-0 rounded-full bg-gray-100 px-3 py-1 text-xs text-gray-600 dark:bg-gray-800 dark:text-gray-300"
          >
            合计 {{ formatInt(toolRankTotal) }} 次
          </span>
        </div>
        <div v-if="toolRanks.length > 0" class="space-y-3">
          <div
            v-for="(item, index) in toolRanks"
            :key="`${item.UpstreamID}:${item.OriginalName}`"
            class="rounded-lg border border-gray-200 p-3 dark:border-gray-800"
          >
            <div class="flex items-start justify-between gap-3">
              <div class="flex min-w-0 items-start gap-2">
                <span
                  class="shrink-0 pt-0.5 text-xs font-semibold tabular-nums text-gray-400 dark:text-gray-500"
                >{{ index + 1 }}</span>
                <div class="min-w-0 flex-1">
                  <span
                    v-tooltip="{
                      content: toolDescription(item),
                      placement: 'bottom-start',
                      wrap: true,
                      disabled: toolDescription(item) === '',
                    }"
                    class="block truncate text-sm font-medium text-gray-800 dark:text-white/90"
                  >
                    {{ item.OriginalName }}
                  </span>
                  <div class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400">
                    {{ upstreamLabel(item.UpstreamID) }}
                  </div>
                </div>
              </div>
              <div class="shrink-0 text-right">
                <div class="text-sm font-semibold tabular-nums text-gray-800 dark:text-white/90">
                  {{ formatInt(item.Count) }}
                </div>
                <div class="text-xs text-gray-500 dark:text-gray-400">
                  {{ formatPercent(toolRankTotal === 0 ? 0 : (item.Count / toolRankTotal) * 100) }}
                </div>
              </div>
            </div>
            <div class="mt-3 h-1.5 rounded-full bg-gray-100 dark:bg-gray-800">
              <div
                class="h-1.5 rounded-full bg-brand-500"
                :style="{ width: `${toolRankMax === 0 ? 0 : (item.Count / toolRankMax) * 100}%` }"
              ></div>
            </div>
          </div>
        </div>
        <div v-else class="py-12 text-center text-sm text-gray-400 dark:text-gray-500">
          暂无工具调用记录
        </div>
      </section>

      <section :class="cardClass">
        <div class="mb-4 flex items-center justify-between gap-3">
          <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">工具错误排行</h3>
          <span
            v-if="topErrorTool"
            class="bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400 rounded-full px-3 py-1 text-xs font-medium"
          >
            最高 {{ formatInt(topErrorTool.FailureCalls) }} 次
          </span>
        </div>
        <div class="space-y-3">
          <div
            v-for="item in toolErrors"
            :key="`${item.UpstreamID}:${item.OriginalName}`"
            class="rounded-lg border border-gray-200 p-3 dark:border-gray-800"
          >
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <div class="truncate text-sm font-medium text-gray-800 dark:text-white/90">
                  {{ toolLabel(item) }}
                </div>
                <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                  最近失败 {{ formatFullDate(item.LastFailedAt) }}
                </div>
              </div>
              <div class="shrink-0 text-right">
                <div class="text-error-600 dark:text-error-400 text-sm font-semibold">
                  {{ formatInt(item.FailureCalls) }}
                </div>
                <div class="text-xs text-gray-500">失败</div>
              </div>
            </div>
            <div class="mt-3 flex flex-wrap gap-2 text-xs text-gray-500 dark:text-gray-400">
              <span>总调用 {{ formatInt(item.TotalCalls) }}</span>
              <span>平均耗时 {{ formatMs(item.AvgLatencyMS) }}</span>
            </div>
          </div>
          <div
            v-if="toolErrors.length === 0"
            class="py-10 text-center text-sm text-gray-400 dark:text-gray-500"
          >
            暂无错误记录
          </div>
        </div>
      </section>
    </div>
  </AdminLayout>
</template>
