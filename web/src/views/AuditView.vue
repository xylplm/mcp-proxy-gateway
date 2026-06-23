<script setup lang="ts">
/**
 * 审计日志页（任务 26.5）。
 *
 * 以 TailAdmin 表格风格展示审计日志：按发生时间倒序分页（Req 22.4），
 * 列含事件类型、操作目标、明细、发生时间；底部提供页码与每页条数控件
 * （默认 20，范围 1-200，越界由后端收敛）。
 *
 * 覆盖 Req 22.4（倒序分页查询）、17.5（管理 REST API）。
 * 容错：无记录时展示空态；加载失败给出错误提示与重试。
 */
import { computed, onMounted, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import { ArchiveIcon } from '@/icons'
import {
  exportAudit,
  listAudit,
  AUDIT_EVENT_LABELS,
  AUDIT_DEFAULT_PAGE_SIZE,
  AUDIT_MIN_PAGE_SIZE,
  AUDIT_MAX_PAGE_SIZE,
  type AuditRecord,
} from '@/api/audit'

const records = ref<AuditRecord[]>([])
const page = ref(1)
const pageSize = ref(AUDIT_DEFAULT_PAGE_SIZE)
const total = ref(0)
const eventType = ref('')
const startTime = ref('')
const endTime = ref('')

const loading = ref(false)
const exporting = ref(false)
const loadError = ref('')

/** 总页数（至少 1 页，便于禁用「下一页」判断）。 */
const totalPages = computed(() => {
  if (total.value <= 0 || pageSize.value <= 0) return 1
  return Math.max(1, Math.ceil(total.value / pageSize.value))
})

const eventTypeOptions = computed(() => Object.entries(AUDIT_EVENT_LABELS))

/** 事件类型徽章样式（按类别着色）。 */
function eventBadgeClass(type: string): string {
  const base = 'inline-flex rounded-full px-2.5 py-0.5 text-xs font-medium'
  switch (type) {
    case 'login':
      return `${base} bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400`
    case 'create':
      return `${base} bg-success-50 text-success-600 dark:bg-success-500/10 dark:text-success-400`
    case 'update':
      return `${base} bg-warning-50 text-warning-600 dark:bg-warning-500/10 dark:text-warning-400`
    case 'delete':
      return `${base} bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400`
    case 'access_denied':
      return `${base} bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400`
    default:
      return `${base} bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400`
  }
}

/** 事件类型可读名（未知类型回退原值）。 */
function eventLabel(type: string): string {
  return AUDIT_EVENT_LABELS[type] ?? type
}

/** 将发生时间格式化为本地可读字符串。 */
function formatTime(rfc3339: string): string {
  const d = new Date(rfc3339)
  if (Number.isNaN(d.getTime())) return rfc3339
  return d.toLocaleString()
}

/** 将明细（结构化 JSON 或字符串）压缩为单行展示文本。 */
function formatDetail(detail: unknown): string {
  if (detail === null || detail === undefined) return '—'
  if (typeof detail === 'string') return detail === '' ? '—' : detail
  try {
    return JSON.stringify(detail)
  } catch {
    return String(detail)
  }
}

/** 加载指定页的审计记录。 */
async function load(): Promise<void> {
  if (loading.value) return
  loading.value = true
  loadError.value = ''
  try {
    const res = await listAudit(auditQuery())
    records.value = res.records
    // 回显后端收敛后的实际分页参数与总数。
    page.value = res.page
    pageSize.value = res.pageSize
    total.value = res.total
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : '加载审计日志失败'
  } finally {
    loading.value = false
  }
}

function auditQuery() {
  return {
    page: page.value,
    pageSize: pageSize.value,
    eventType: eventType.value,
    start: toRFC3339(startTime.value),
    end: toRFC3339(endTime.value),
  }
}

function auditFileNameNow(): string {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  return `mpg-audit-${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}-${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}.json`
}

async function downloadAudit(): Promise<void> {
  if (exporting.value) return
  exporting.value = true
  loadError.value = ''
  try {
    const blob = await exportAudit(auditQuery())
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = auditFileNameNow()
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : '导出审计日志失败'
  } finally {
    exporting.value = false
  }
}

function toRFC3339(value: string): string {
  if (value === '') return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return date.toISOString()
}

function applyFilters(): void {
  page.value = 1
  void load()
}

function resetFilters(): void {
  eventType.value = ''
  startTime.value = ''
  endTime.value = ''
  page.value = 1
  void load()
}

/** 切换到上一页。 */
function prevPage(): void {
  if (page.value > 1) {
    page.value -= 1
    void load()
  }
}

/** 切换到下一页。 */
function nextPage(): void {
  if (page.value < totalPages.value) {
    page.value += 1
    void load()
  }
}

/** 应用新的每页条数（收敛到 [1,200] 后回到第 1 页重查）。 */
function applyPageSize(): void {
  let size = Math.trunc(pageSize.value)
  if (Number.isNaN(size) || size < AUDIT_MIN_PAGE_SIZE) size = AUDIT_MIN_PAGE_SIZE
  if (size > AUDIT_MAX_PAGE_SIZE) size = AUDIT_MAX_PAGE_SIZE
  pageSize.value = size
  page.value = 1
  void load()
}

onMounted(load)

const cardClass =
  'rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]'
const inputClass =
  'h-9 w-20 rounded-lg border border-gray-300 bg-transparent px-3 text-sm text-gray-800 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90'
const filterInputClass =
  'h-10 w-full rounded-lg border border-gray-300 bg-transparent px-3 text-sm text-gray-800 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90'
const pagerBtnClass =
  'rounded-lg border border-gray-300 px-3 py-1.5 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:cursor-not-allowed disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800'
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="审计日志" />

    <section :class="cardClass">
      <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
        <div>
          <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">审计日志</h3>
          <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
            按发生时间倒序排列，共 {{ total }} 条记录。
          </p>
        </div>
        <div class="flex items-center gap-2">
          <button
            v-tooltip:bottom="'导出当前页'"
            type="button"
            class="inline-flex h-9 items-center gap-1.5 rounded-lg border border-gray-300 px-3 text-sm font-medium text-gray-700 transition hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            :disabled="loading || exporting"
            @click="downloadAudit"
          >
            <ArchiveIcon class="h-4 w-4" />
            导出
          </button>
          <label class="text-sm text-gray-500 dark:text-gray-400">每页</label>
          <input
            v-model.number="pageSize"
            type="number"
            :min="AUDIT_MIN_PAGE_SIZE"
            :max="AUDIT_MAX_PAGE_SIZE"
            :class="inputClass"
            @keyup.enter="applyPageSize"
          />
          <button type="button" :class="pagerBtnClass" :disabled="loading" @click="applyPageSize">
            应用
          </button>
        </div>
      </div>

      <div class="mb-5 grid grid-cols-1 gap-3 rounded-xl bg-gray-50 p-3 sm:grid-cols-2 xl:grid-cols-[1fr_1fr_1fr_auto] dark:bg-white/[0.02]">
        <label class="block">
          <span class="mb-1.5 block text-xs font-medium text-gray-500 dark:text-gray-400">事件类型</span>
          <select v-model="eventType" :class="filterInputClass">
            <option value="">全部类型</option>
            <option v-for="[value, label] in eventTypeOptions" :key="value" :value="value">
              {{ label }}
            </option>
          </select>
        </label>
        <label class="block">
          <span class="mb-1.5 block text-xs font-medium text-gray-500 dark:text-gray-400">开始时间</span>
          <input v-model="startTime" type="datetime-local" :class="filterInputClass" />
        </label>
        <label class="block">
          <span class="mb-1.5 block text-xs font-medium text-gray-500 dark:text-gray-400">结束时间</span>
          <input v-model="endTime" type="datetime-local" :class="filterInputClass" />
        </label>
        <div class="flex items-end gap-2">
          <button type="button" :class="pagerBtnClass" :disabled="loading" @click="applyFilters">
            筛选
          </button>
          <button type="button" :class="pagerBtnClass" :disabled="loading" @click="resetFilters">
            重置
          </button>
        </div>
      </div>

      <!-- 加载/错误态 -->
      <div
        v-if="loading"
        class="py-12 text-center text-sm text-gray-400 dark:text-gray-500"
      >
        加载中…
      </div>
      <p
        v-else-if="loadError !== ''"
        class="rounded-lg bg-error-50 px-4 py-3 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
      >
        {{ loadError }}
        <button type="button" class="ml-2 underline" @click="load">重试</button>
      </p>

      <template v-else>
        <div class="overflow-x-auto">
          <table class="min-w-full text-sm">
            <thead>
              <tr class="border-b border-gray-100 text-left text-gray-500 dark:border-gray-800 dark:text-gray-400">
                <th class="px-3 py-2.5 font-medium">事件类型</th>
                <th class="px-3 py-2.5 font-medium">操作目标</th>
                <th class="px-3 py-2.5 font-medium">明细</th>
                <th class="px-3 py-2.5 font-medium whitespace-nowrap">发生时间</th>
              </tr>
            </thead>
            <tbody>
              <tr
                v-for="r in records"
                :key="r.ID"
                class="border-b border-gray-50 align-top text-gray-700 dark:border-gray-800/60 dark:text-gray-300"
              >
                <td class="px-3 py-3">
                  <span :class="eventBadgeClass(r.EventType)">{{ eventLabel(r.EventType) }}</span>
                </td>
                <td class="px-3 py-3">{{ r.Target === '' ? '—' : r.Target }}</td>
                <td class="max-w-md px-3 py-3 text-gray-500 dark:text-gray-400">
                  <AppTooltip :content="formatDetail(r.Detail)" placement="bottom-start">
                    <span class="block truncate">{{ formatDetail(r.Detail) }}</span>
                  </AppTooltip>
                </td>
                <td class="px-3 py-3 whitespace-nowrap text-gray-500 dark:text-gray-400">
                  {{ formatTime(r.OccurredAt) }}
                </td>
              </tr>
              <tr v-if="records.length === 0">
                <td colspan="4" class="px-3 py-12 text-center text-gray-400 dark:text-gray-500">
                  暂无审计记录
                </td>
              </tr>
            </tbody>
          </table>
        </div>

        <!-- 分页控件 -->
        <div class="mt-5 flex items-center justify-between">
          <span class="text-sm text-gray-500 dark:text-gray-400">
            第 {{ page }} / {{ totalPages }} 页
          </span>
          <div class="flex items-center gap-2">
            <button type="button" :class="pagerBtnClass" :disabled="loading || page <= 1" @click="prevPage">
              上一页
            </button>
            <button
              type="button"
              :class="pagerBtnClass"
              :disabled="loading || page >= totalPages"
              @click="nextPage"
            >
              下一页
            </button>
          </div>
        </div>
      </template>
    </section>
  </AdminLayout>
</template>
