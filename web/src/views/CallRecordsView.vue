<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import { listCallRecords, type CallRecord } from '@/api/stats'
import { RefreshIcon } from '@/icons'

const records = ref<CallRecord[]>([])
const loading = ref(false)
const refreshing = ref(false)
const errorMessage = ref('')
const newCount = ref(0)
let pollTimer: number | undefined
const pageLimit = 30
const maxLocalRecords = 120

const latestId = computed(() => records.value.reduce((max, item) => Math.max(max, item.ID), 0))
const latestCalledAt = computed(() => records.value[0]?.CalledAt ?? '')
const successCount = computed(() => records.value.filter((item) => item.Success).length)
const failureCount = computed(() => records.value.length - successCount.value)
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

function mcpLabel(record: CallRecord): string {
  return record.UpstreamName || record.UpstreamID || '未知 MCP'
}

function toolLabel(record: CallRecord): string {
  return record.ExposedName || record.OriginalName || '未知工具'
}

function apiKeyLabel(record: CallRecord): string {
  return record.APIKeyName || record.APIKeyID || '未知 API Key'
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

function clearNewCount(): void {
  newCount.value = 0
}

onMounted(async () => {
  await loadInitial()
  pollTimer = window.setInterval(() => void refreshNow(), 5000)
})

onUnmounted(() => {
  if (pollTimer !== undefined) window.clearInterval(pollTimer)
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
          最新调用排在最上方，停留在本页时会自动追加新记录。
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
      </div>
    </div>

    <p
      v-if="errorMessage !== ''"
      class="mb-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
    >
      {{ errorMessage }}
    </p>

    <div class="mb-5 grid grid-cols-1 gap-4 sm:grid-cols-3">
      <section :class="cardClass">
        <p class="text-sm text-gray-500 dark:text-gray-400">当前列表</p>
        <p class="mt-2 text-2xl font-semibold text-gray-800 dark:text-white/90">
          {{ records.length.toLocaleString('zh-CN') }}
        </p>
      </section>
      <section :class="cardClass">
        <p class="text-sm text-gray-500 dark:text-gray-400">成功 / 失败</p>
        <p class="mt-2 text-2xl font-semibold text-gray-800 dark:text-white/90">
          {{ successCount }} / {{ failureCount }}
        </p>
      </section>
      <section :class="cardClass">
        <p class="text-sm text-gray-500 dark:text-gray-400">平均耗时</p>
        <p class="mt-2 text-2xl font-semibold text-gray-800 dark:text-white/90">
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
      <router-link
        v-for="record in records"
        :key="record.ID"
        :to="`/call-records/${record.ID}`"
        class="group rounded-2xl border border-gray-200 bg-white p-4 transition hover:border-brand-300 hover:shadow-theme-sm dark:border-gray-800 dark:bg-white/[0.03] dark:hover:border-brand-500/40"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <p class="truncate text-sm font-semibold text-gray-800 dark:text-white/90">
              {{ toolLabel(record) }}
            </p>
            <p class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400">
              {{ mcpLabel(record) }}
            </p>
          </div>
          <span
            class="shrink-0 rounded-full px-2.5 py-1 text-xs font-medium"
            :class="
              record.Success
                ? 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
                : 'bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400'
            "
          >
            {{ record.Success ? '成功' : '失败' }}
          </span>
        </div>

        <div class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-3">
          <div>
            <p class="text-xs text-gray-400 dark:text-gray-500">调用时间</p>
            <p class="mt-1 truncate text-sm text-gray-700 dark:text-gray-300">
              {{ formatDateTime(record.CalledAt) }}
            </p>
          </div>
          <div>
            <p class="text-xs text-gray-400 dark:text-gray-500">API Key</p>
            <p class="mt-1 truncate text-sm text-gray-700 dark:text-gray-300">
              {{ apiKeyLabel(record) }}
            </p>
          </div>
          <div>
            <p class="text-xs text-gray-400 dark:text-gray-500">耗时</p>
            <p class="mt-1 text-sm font-medium text-gray-800 dark:text-white/90">
              {{ formatLatency(record.LatencyMS) }}
            </p>
          </div>
        </div>
      </router-link>
    </div>
  </AdminLayout>
</template>
