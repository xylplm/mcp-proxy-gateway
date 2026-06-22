<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import AppTooltip from '@/components/common/AppTooltip.vue'
import CallRecordDetailModal from '@/components/call-records/CallRecordDetailModal.vue'
import { clearCallRecords, listCallRecords, type CallRecord } from '@/api/stats'
import { RefreshIcon, TrashIcon } from '@/icons'
import { useConfirm } from '@/composables/useConfirm'
import { useToast } from '@/composables/useToast'

const toast = useToast()
const { confirm } = useConfirm()

const records = ref<CallRecord[]>([])
const loading = ref(false)
const refreshing = ref(false)
const errorMessage = ref('')
const newCount = ref(0)
const autoRefresh = ref(true)
const clearing = ref(false)
let pollTimer: number | undefined
const pageLimit = 30
const maxLocalRecords = 120
// 选中的记录 + 模态框开关。点击卡片打开详情大模态框，取代子路由跳转。
const selectedRecord = ref<CallRecord | null>(null)
const detailOpen = ref(false)

const latestId = computed(() => records.value.reduce((max, item) => Math.max(max, item.ID), 0))
const latestCalledAt = computed(() => records.value[0]?.CalledAt ?? '')
const successCount = computed(() => records.value.filter((item) => statusOf(item) === 'success').length)
const upstreamErrorCount = computed(
  () => records.value.filter((item) => statusOf(item) === 'upstream_error').length,
)
const failedCount = computed(() => records.value.filter((item) => statusOf(item) === 'failed').length)
const avgLatency = computed(() => {
  if (records.value.length === 0) return 0
  return Math.round(records.value.reduce((sum, item) => sum + item.LatencyMS, 0) / records.value.length)
})

function formatDateTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString('zh-CN', { hour12: false })
}

function formatLatency(value: number): string {
  return `${Math.max(0, Math.round(value)).toLocaleString('zh-CN')} ms`
}

function statusOf(record: CallRecord): string {
  if (record.Status !== undefined && record.Status !== '') return record.Status
  return record.Success ? 'success' : 'failed'
}

function statusLabel(record: CallRecord): string {
  switch (statusOf(record)) {
    case 'success':
      return '成功'
    case 'upstream_error':
      return '上游错误'
    case 'failed':
      return '调用失败'
    default:
      return '未知状态'
  }
}

function statusClass(record: CallRecord): string {
  switch (statusOf(record)) {
    case 'success':
      return 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
    case 'upstream_error':
      return 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
    case 'failed':
      return 'bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400'
    default:
      return 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'
  }
}

function failureCode(record: CallRecord): string {
  return record.FailureDetail?.code ?? ''
}

function mcpLabel(record: CallRecord): string {
  return record.UpstreamName || record.UpstreamID || '未知 MCP'
}

function toolLabel(record: CallRecord): string {
  return record.ExposedName || record.OriginalName || '未知工具'
}

function modeLabel(record: CallRecord): string {
  return record.Mode === 'smart' ? '智能' : '全量'
}

function modeClass(record: CallRecord): string {
  return record.Mode === 'smart'
    ? 'bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400'
    : 'bg-gray-100 text-gray-600 dark:bg-white/5 dark:text-gray-400'
}

function apiKeyLabel(record: CallRecord): string {
  return record.APIKeyName || record.APIKeyID || 'API 调用'
}

// 来源标识：xiaozhi 显示「小智接入」，替代此前对无 API Key 记录的「未知 API Key」展示。
function isXiaozhi(record: CallRecord): boolean {
  return record.Source === 'xiaozhi'
}

function sourceLabel(record: CallRecord): string {
  if (isXiaozhi(record)) return '小智接入'
  return apiKeyLabel(record)
}

// 耗时分级：<1s 正常、1-3s 较慢、>3s 很慢。卡片耗时格与统计区平均耗时共用。
function latencyLevel(value: number): 'normal' | 'warn' | 'danger' {
  const v = Math.max(0, value)
  if (v >= 3000) return 'danger'
  if (v >= 1000) return 'warn'
  return 'normal'
}

function latencyClass(value: number): string {
  switch (latencyLevel(value)) {
    case 'danger':
      return 'text-error-600 dark:text-error-400'
    case 'warn':
      return 'text-warning-700 dark:text-warning-400'
    default:
      return 'text-gray-800 dark:text-white/90'
  }
}

function openDetail(record: CallRecord): void {
  selectedRecord.value = record
  detailOpen.value = true
}

function closeDetail(): void {
  detailOpen.value = false
  selectedRecord.value = null
}

function mergeLatest(nextRecords: CallRecord[]): void {
  if (nextRecords.length === 0) return
  const exists = new Set(records.value.map((item) => item.ID))
  const fresh = nextRecords.filter((item) => !exists.has(item.ID))
  if (fresh.length === 0) return
  records.value = [...fresh, ...records.value]
    .sort((a, b) => Date.parse(b.CalledAt) - Date.parse(a.CalledAt) || b.ID - a.ID)
    .slice(0, maxLocalRecords)
  newCount.value += fresh.length
}

async function loadInitial(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  newCount.value = 0
  try {
    records.value = await listCallRecords({ limit: pageLimit })
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '加载调用记录失败'
  } finally {
    loading.value = false
  }
}

async function refreshNow(): Promise<void> {
  if (refreshing.value) return
  refreshing.value = true
  errorMessage.value = ''
  try {
    const latest = await listCallRecords({
      limit: pageLimit,
      afterId: latestId.value,
      afterAt: latestCalledAt.value,
    })
    mergeLatest(latest)
    if (latest.length === 0 && records.value.length === 0) {
      records.value = await listCallRecords({ limit: pageLimit })
    }
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '刷新调用记录失败'
  } finally {
    refreshing.value = false
  }
}

async function clearRecords(): Promise<void> {
  if (clearing.value) return
  const ok = await confirm({
    title: '清空调用记录',
    message: '确定清空最近调用记录？此操作不会清空历史统计。',
    confirmText: '清空',
    tone: 'danger',
  })
  if (!ok) return
  clearing.value = true
  errorMessage.value = ''
  try {
    const deleted = await clearCallRecords()
    records.value = []
    newCount.value = 0
    toast.success(`已清空 ${deleted.toLocaleString('zh-CN')} 条调用记录`)
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '清空调用记录失败')
  } finally {
    clearing.value = false
  }
}

function startPolling(): void {
  stopPolling()
  if (!autoRefresh.value) return
  pollTimer = window.setInterval(() => void refreshNow(), 5000)
}

function stopPolling(): void {
  if (pollTimer !== undefined) {
    window.clearInterval(pollTimer)
    pollTimer = undefined
  }
}

function toggleAutoRefresh(): void {
  if (autoRefresh.value) {
    void refreshNow()
    startPolling()
    return
  }
  stopPolling()
}

function clearNewCount(): void {
  newCount.value = 0
}

onMounted(async () => {
  await loadInitial()
  startPolling()
})

onUnmounted(() => {
  stopPolling()
})

const cardClass =
  'rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]'
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="调用记录" />

    <div class="mb-5 flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold text-gray-800 dark:text-white/90">调用记录</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          最新调用排在最上方，仅保留最近 24 小时记录。
        </p>
      </div>
      <div class="flex items-center gap-2">
        <button
          v-if="newCount > 0"
          type="button"
          class="rounded-full bg-success-50 px-3 py-1.5 text-xs font-medium text-success-700 transition hover:bg-success-100 dark:bg-success-500/10 dark:text-success-400"
          @click="clearNewCount"
        >
          新增 {{ newCount }} 条
        </button>
        <label
          class="inline-flex h-10 items-center gap-2 rounded-lg border border-gray-200 px-3 text-sm text-gray-600 dark:border-gray-800 dark:text-gray-400"
        >
          <input
            v-model="autoRefresh"
            type="checkbox"
            class="h-4 w-4 rounded border-gray-300 text-brand-500 focus:ring-brand-500 dark:border-gray-700"
            @change="toggleAutoRefresh"
          />
          自动刷新
        </label>
        <button
          v-tooltip:bottom-end="'立即刷新'"
          type="button"
          class="inline-flex h-10 w-10 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 disabled:opacity-60 dark:border-gray-800 dark:text-gray-400 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400"
          :disabled="loading || refreshing"
          aria-label="立即刷新调用记录"
          @click="refreshNow"
        >
          <RefreshIcon class="h-5 w-5" :class="refreshing ? 'animate-spin' : ''" />
        </button>
        <button
          v-tooltip:bottom-end="'清空调用记录'"
          type="button"
          class="inline-flex h-10 w-10 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition hover:border-error-300 hover:bg-error-50 hover:text-error-600 disabled:opacity-60 dark:border-gray-800 dark:text-gray-400 dark:hover:border-error-500/40 dark:hover:bg-error-500/[0.08] dark:hover:text-error-400"
          :disabled="loading || clearing || records.length === 0"
          aria-label="清空调用记录"
          @click="clearRecords"
        >
          <TrashIcon class="h-5 w-5" />
        </button>
      </div>
    </div>

    <p
      v-if="errorMessage !== ''"
      class="mb-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
    >
      {{ errorMessage }}
    </p>
    <div class="mb-5 grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-5">
      <section :class="cardClass">
        <p class="text-sm text-gray-500 dark:text-gray-400">当前列表</p>
        <p class="mt-2 text-2xl font-semibold text-gray-800 dark:text-white/90">
          {{ records.length.toLocaleString('zh-CN') }}
        </p>
      </section>
      <section :class="cardClass">
        <p class="text-sm text-gray-500 dark:text-gray-400">成功</p>
        <p class="mt-2 text-2xl font-semibold text-success-700 dark:text-success-400">
          {{ successCount.toLocaleString('zh-CN') }}
        </p>
      </section>
      <section :class="cardClass">
        <p class="text-sm text-gray-500 dark:text-gray-400">上游错误</p>
        <p class="mt-2 text-2xl font-semibold text-warning-700 dark:text-warning-400">
          {{ upstreamErrorCount.toLocaleString('zh-CN') }}
        </p>
      </section>
      <section :class="cardClass">
        <p class="text-sm text-gray-500 dark:text-gray-400">调用失败</p>
        <p class="mt-2 text-2xl font-semibold text-error-600 dark:text-error-400">
          {{ failedCount.toLocaleString('zh-CN') }}
        </p>
      </section>
      <section :class="cardClass">
        <p class="text-sm text-gray-500 dark:text-gray-400">平均耗时</p>
        <p class="mt-2 text-2xl font-semibold" :class="latencyClass(avgLatency)">
          {{ formatLatency(avgLatency) }}
        </p>
      </section>
    </div>

    <div
      v-if="loading"
      class="rounded-2xl border border-gray-200 bg-white px-5 py-12 text-center text-sm text-gray-400 dark:border-gray-800 dark:bg-white/[0.03]"
    >
      加载中…
    </div>
    <div
      v-else-if="records.length === 0"
      class="rounded-2xl border border-dashed border-gray-300 bg-white px-5 py-12 text-center text-sm text-gray-400 dark:border-gray-700 dark:bg-white/[0.03]"
    >
      暂无调用记录
    </div>

    <div v-else class="grid grid-cols-1 gap-3 xl:grid-cols-2 3xl:grid-cols-3">
      <div
        v-for="record in records"
        :key="record.ID"
        role="button"
        tabindex="0"
        class="group cursor-pointer rounded-2xl border border-gray-200 bg-white p-4 transition hover:border-brand-300 hover:shadow-theme-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-400 dark:border-gray-800 dark:bg-white/[0.03] dark:hover:border-brand-500/40"
        @click="openDetail(record)"
        @keydown.enter="openDetail(record)"
        @keydown.space.prevent="openDetail(record)"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <div class="flex items-center gap-1">
              <AppTooltip
                :content="record.Description || ''"
                placement="top-start"
                :disabled="!record.Description"
                :wrap="true"
                tag="span"
                class="min-w-0"
              >
                <span class="cursor-help truncate text-sm font-semibold text-gray-800 dark:text-white/90">
                  {{ toolLabel(record) }}
                </span>
              </AppTooltip>
              <span class="shrink-0 rounded-full px-2 py-1 mr-1 text-xs font-medium" :class="modeClass(record)">
                {{ modeLabel(record) }}
              </span>
            </div>
            <p class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400">
              {{ mcpLabel(record) }}
            </p>
          </div>
          <span class="shrink-0 rounded-full px-2.5 py-1 text-xs font-medium" :class="statusClass(record)">
            {{ statusLabel(record) }}
          </span>
        </div>

        <p
          v-if="statusOf(record) !== 'success' && failureCode(record) !== ''"
          class="mt-3 truncate rounded-lg bg-gray-50 px-3 py-2 text-xs text-gray-500 dark:bg-gray-800/60 dark:text-gray-400"
        >
          错误码：{{ failureCode(record) }}
        </p>

        <div class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-3">
          <div>
            <p class="text-xs text-gray-400 dark:text-gray-500">调用时间</p>
            <p class="mt-1 truncate text-sm text-gray-700 dark:text-gray-300">
              {{ formatDateTime(record.CalledAt) }}
            </p>
          </div>
          <div>
            <p class="text-xs text-gray-400 dark:text-gray-500">调用来源</p>
            <p class="mt-1 flex items-center gap-1 truncate text-sm text-gray-700 dark:text-gray-300">
              <span
                v-if="isXiaozhi(record)"
                class="inline-flex shrink-0 rounded-full bg-brand-50 px-1.5 py-0.5 text-xs font-medium text-brand-600 dark:bg-brand-500/10 dark:text-brand-400"
              >
                小智
              </span>
              <span class="truncate">{{ sourceLabel(record) }}</span>
            </p>
          </div>
          <div>
            <p class="text-xs text-gray-400 dark:text-gray-500">耗时</p>
            <p class="mt-1 text-sm font-medium" :class="latencyClass(record.LatencyMS)">
              {{ formatLatency(record.LatencyMS) }}
            </p>
          </div>
        </div>
      </div>
    </div>

    <CallRecordDetailModal :open="detailOpen" :record="selectedRecord" @close="closeDetail" />
  </AdminLayout>
</template>
