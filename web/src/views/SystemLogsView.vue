<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import {
  clearSystemLogs,
  exportSystemLogs,
  listSystemLogs,
  type SystemLogEntry,
  type SystemLogLevel,
} from '@/api/systemLogs'
import { ArchiveIcon, RefreshIcon, TrashIcon } from '@/icons'
import { useConfirm } from '@/composables/useConfirm'
import { useToast } from '@/composables/useToast'

const toast = useToast()
const { confirm } = useConfirm()

const logs = ref<SystemLogEntry[]>([])
const loading = ref(false)
const refreshing = ref(false)
const clearing = ref(false)
const exporting = ref(false)
const errorMessage = ref('')
const autoRefresh = ref(true)
const level = ref<SystemLogLevel>('')
const consoleRef = ref<HTMLElement | null>(null)

let pollTimer: number | undefined
const pageLimit = 200
const maxLocalLogs = 500

const latestId = computed(() => logs.value.reduce((max, item) => Math.max(max, item.id), 0))
const debugCount = computed(() => logs.value.filter((item) => item.level === 'debug').length)
const infoCount = computed(() => logs.value.filter((item) => item.level === 'info').length)
const warnCount = computed(() => logs.value.filter((item) => item.level === 'warn').length)
const errorCount = computed(() => logs.value.filter((item) => item.level === 'error').length)

const levelOptions: ReadonlyArray<{ value: SystemLogLevel; label: string }> = [
  { value: '', label: '全部级别' },
  { value: 'debug', label: 'Debug' },
  { value: 'info', label: 'Info' },
  { value: 'warn', label: 'Warn' },
  { value: 'error', label: 'Error' },
]

function formatTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString('zh-CN', { hour12: false })
}

function levelLabel(value: string): string {
  switch (value) {
    case 'debug':
      return 'DEBUG'
    case 'info':
      return 'INFO'
    case 'warn':
      return 'WARN'
    case 'error':
      return 'ERROR'
    default:
      return value.toUpperCase() || 'LOG'
  }
}

function levelClass(value: string): string {
  switch (value) {
    case 'debug':
      return 'text-emerald-400'
    case 'info':
      return 'text-sky-400'
    case 'warn':
      return 'text-amber-400'
    case 'error':
      return 'text-red-400'
    default:
      return 'text-gray-400'
  }
}

function attrsText(entry: SystemLogEntry): string {
  if (entry.attrs === undefined || Object.keys(entry.attrs).length === 0) return ''
  try {
    return JSON.stringify(entry.attrs, null, 2)
  } catch {
    return String(entry.attrs)
  }
}

function logFileNameNow(): string {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  const suffix = level.value === '' ? '' : `-${level.value}`
  return `mpg-system-logs${suffix}-${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}-${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}.json`
}

async function downloadLogs(): Promise<void> {
  if (exporting.value) return
  exporting.value = true
  errorMessage.value = ''
  try {
    const blob = await exportSystemLogs({ level: level.value })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = logFileNameNow()
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
    toast.success('系统日志已导出')
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '导出系统日志失败')
  } finally {
    exporting.value = false
  }
}

// 可展开详情的日志条目 id 集合。
const expandedIds = ref<Set<number>>(new Set())

function toggleExpand(id: number): void {
  const next = new Set(expandedIds.value)
  if (next.has(id)) {
    next.delete(id)
  } else {
    next.add(id)
  }
  expandedIds.value = next
}

async function scrollToBottom(): Promise<void> {
  await nextTick()
  const el = consoleRef.value
  if (el !== null) {
    el.scrollTop = el.scrollHeight
  }
}

function mergeLatest(nextLogs: SystemLogEntry[]): void {
  if (nextLogs.length === 0) return
  const exists = new Set(logs.value.map((item) => item.id))
  const fresh = nextLogs.filter((item) => !exists.has(item.id))
  if (fresh.length === 0) return
  logs.value = [...logs.value, ...fresh]
    .sort((a, b) => a.id - b.id)
    .slice(-maxLocalLogs)
  void scrollToBottom()
}

async function loadInitial(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    logs.value = await listSystemLogs({ level: level.value, limit: pageLimit })
    void scrollToBottom()
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '加载系统日志失败'
  } finally {
    loading.value = false
  }
}

async function refreshNow(): Promise<void> {
  if (refreshing.value) return
  refreshing.value = true
  errorMessage.value = ''
  try {
    const next = await listSystemLogs({
      level: level.value,
      afterId: latestId.value,
      limit: pageLimit,
    })
    if (next.length === 0 && logs.value.length === 0) {
      logs.value = await listSystemLogs({ level: level.value, limit: pageLimit })
      void scrollToBottom()
    } else {
      mergeLatest(next)
    }
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '刷新系统日志失败'
  } finally {
    refreshing.value = false
  }
}

async function clearLogs(): Promise<void> {
  if (clearing.value) return
  const ok = await confirm({
    title: '清空系统日志',
    message: '确定清空当前进程内的系统日志？该操作只影响当前进程内缓存。',
    confirmText: '清空',
    tone: 'danger',
  })
  if (!ok) return
  clearing.value = true
  errorMessage.value = ''
  try {
    const deleted = await clearSystemLogs()
    logs.value = []
    toast.success(`已清空 ${deleted.toLocaleString('zh-CN')} 条系统日志`)
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '清空系统日志失败')
  } finally {
    clearing.value = false
  }
}

function startPolling(): void {
  stopPolling()
  if (!autoRefresh.value) return
  pollTimer = window.setInterval(() => void refreshNow(), 3000)
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

watch(level, () => {
  stopPolling()
  void loadInitial().then(startPolling)
})

onMounted(async () => {
  await loadInitial()
  startPolling()
})

onUnmounted(() => {
  stopPolling()
})

const cardClass =
  'rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-white/[0.03]'
const controlClass =
  'h-10 rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-700 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:bg-gray-900 dark:text-gray-300'
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="系统日志" />

    <div class="mb-5 flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold text-gray-800 dark:text-white/90">系统日志</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          查看当前进程的运行日志，适合排查启动、连接、同步和运行时异常。
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <select v-model="level" :class="controlClass" aria-label="系统日志级别">
          <option v-for="item in levelOptions" :key="item.value" :value="item.value">
            {{ item.label }}
          </option>
        </select>
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
          aria-label="立即刷新系统日志"
          @click="refreshNow"
        >
          <RefreshIcon class="h-5 w-5" :class="refreshing ? 'animate-spin' : ''" />
        </button>
        <button
          v-tooltip:bottom-end="'导出当前级别日志'"
          type="button"
          class="inline-flex h-10 w-10 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 disabled:opacity-60 dark:border-gray-800 dark:text-gray-400 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400"
          :disabled="loading || exporting"
          aria-label="导出当前级别系统日志"
          @click="downloadLogs"
        >
          <ArchiveIcon class="h-5 w-5" />
        </button>
        <button
          v-tooltip:bottom-end="'清空系统日志'"
          type="button"
          class="inline-flex h-10 w-10 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition hover:border-error-300 hover:bg-error-50 hover:text-error-600 disabled:opacity-60 dark:border-gray-800 dark:text-gray-400 dark:hover:border-error-500/40 dark:hover:bg-error-500/[0.08] dark:hover:text-error-400"
          :disabled="loading || clearing || logs.length === 0"
          aria-label="清空系统日志"
          @click="clearLogs"
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
    <div class="mb-5 grid grid-cols-2 gap-3 md:grid-cols-5">
      <section :class="cardClass">
        <p class="text-xs text-gray-500 dark:text-gray-400">当前缓存</p>
        <p class="mt-2 text-xl font-semibold text-gray-800 dark:text-white/90">
          {{ logs.length.toLocaleString('zh-CN') }}
        </p>
      </section>
      <section :class="cardClass">
        <p class="text-xs text-gray-500 dark:text-gray-400">Debug</p>
        <p class="mt-2 text-xl font-semibold text-gray-700 dark:text-gray-200">{{ debugCount }}</p>
      </section>
      <section :class="cardClass">
        <p class="text-xs text-gray-500 dark:text-gray-400">Info</p>
        <p class="mt-2 text-xl font-semibold text-brand-600 dark:text-brand-400">{{ infoCount }}</p>
      </section>
      <section :class="cardClass">
        <p class="text-xs text-gray-500 dark:text-gray-400">Warn</p>
        <p class="mt-2 text-xl font-semibold text-warning-700 dark:text-warning-400">{{ warnCount }}</p>
      </section>
      <section :class="cardClass">
        <p class="text-xs text-gray-500 dark:text-gray-400">Error</p>
        <p class="mt-2 text-xl font-semibold text-error-600 dark:text-error-400">{{ errorCount }}</p>
      </section>
    </div>

    <section class="overflow-hidden rounded-xl border border-gray-800 bg-gray-950 shadow-theme-sm">
      <div
        ref="consoleRef"
        class="custom-scrollbar max-h-[68vh] min-h-[420px] overflow-auto font-mono text-xs leading-5"
      >
        <div v-if="loading" class="py-10 text-center text-gray-500">加载中...</div>
        <div v-else-if="logs.length === 0" class="py-10 text-center text-gray-500">暂无系统日志</div>
        <div v-else>
          <div
            v-for="entry in logs"
            :key="entry.id"
            class="group cursor-pointer whitespace-nowrap px-4 transition hover:bg-white/[0.04]"
            role="button"
            tabindex="0"
            @click="attrsText(entry) !== '' ? toggleExpand(entry.id) : undefined"
            @keydown.enter="attrsText(entry) !== '' ? toggleExpand(entry.id) : undefined"
          >
            <div class="flex items-baseline">
              <span class="shrink-0 text-gray-400">{{ formatTime(entry.time) }}</span>
              <span class="ml-3 w-12 shrink-0 font-semibold" :class="levelClass(entry.level)">
                {{ levelLabel(entry.level) }}
              </span>
              <span class="ml-3 truncate text-gray-200">{{ entry.message }}</span>
              <AppTooltip
                v-if="entry.source"
                :content="entry.source"
                placement="bottom-end"
                :wrap="true"
                tag="span"
                class="ml-auto min-w-0 shrink-0 pl-3 text-gray-400"
              >
                <span class="block max-w-[12rem] truncate">{{ entry.source }}</span>
              </AppTooltip>
              <span
                v-if="attrsText(entry) !== ''"
                class="ml-2 shrink-0 text-gray-400 transition group-hover:text-gray-200"
                :class="expandedIds.has(entry.id) ? 'rotate-90' : ''"
                aria-hidden="true"
              >▶</span>
            </div>
            <pre
              v-if="attrsText(entry) !== '' && expandedIds.has(entry.id)"
              class="mt-0.5 whitespace-pre-wrap break-words border-l border-gray-800 pl-3 text-gray-300"
            >{{ attrsText(entry) }}</pre>
          </div>
        </div>
      </div>
    </section>
  </AdminLayout>
</template>
