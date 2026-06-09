<script setup lang="ts">
/**
 * 上游 MCP 管理页（任务 26.1）。
 *
 * 覆盖 Req 14.3/14.6/14.7（模板市场接入向导）、17.5（管理 REST API 接入）以及
 * 上游 CRUD / 启停 / 排序 / 连接状态展示 / 手动刷新（Req 2.x、3.x、5.6、6.4）。
 *
 * 风格：Tailwind 工具类 + TailAdmin 组件风格（卡片、徽章、按钮、分页）；
 * 响应式：分页条数来自 useBreakpoint，小屏单列，大屏提升卡片密度。
 */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import ConnStateBadge from '@/components/upstreams/ConnStateBadge.vue'
import UpstreamFormDrawer from '@/components/upstreams/UpstreamFormDrawer.vue'
import TemplateMarketModal from '@/components/upstreams/TemplateMarketModal.vue'
import { useBreakpoint } from '@/composables/useBreakpoint'
import {
  listUpstreams,
  setUpstreamEnabled,
  deleteUpstream,
  listUpstreamTools,
  reorderUpstreams,
  refreshUpstream,
  reconnectUpstream,
  TRANSPORT_OPTIONS,
  type Upstream,
} from '@/api/upstreams'
import type { ToolDef } from '@/api/tools'
import type { PrefillForm } from '@/api/templates'

const { pageSize } = useBreakpoint()

/** 全量上游列表（按 sortOrder 升序）。 */
const upstreams = ref<Upstream[]>([])
const toolCounts = ref<Record<string, number>>({})
/** 列表加载/错误状态。 */
const loading = ref(false)
const errorMessage = ref('')
/** 行级操作进行中标记（key = upstream id + action）。 */
const busy = ref<Set<string>>(new Set())
/** 操作结果提示。 */
const toast = ref('')

/** 分页：当前页（1 起）。 */
const currentPage = ref(1)

/** 抽屉与弹窗开关。 */
const drawerOpen = ref(false)
const marketOpen = ref(false)
/** 当前编辑目标与模板预填充。 */
const editing = ref<Upstream | null>(null)
const prefill = ref<PrefillForm | null>(null)

/** 删除确认目标。 */
const deleting = ref<Upstream | null>(null)
const sortingOpen = ref(false)
const sortDraft = ref<Upstream[]>([])
const sortDraggingID = ref<string | null>(null)
const sortDragOverID = ref<string | null>(null)
const sortSelectedID = ref('')
const sortTargetPosition = ref('')
const sortMoveMessage = ref('')

const toolModalOpen = ref(false)
const toolModalUpstream = ref<Upstream | null>(null)
const toolModalTools = ref<ToolDef[]>([])
const toolModalUpdatedAt = ref<string | null>(null)
const toolModalLoading = ref(false)
const toolModalError = ref('')

let statusPollTimer: number | undefined
let statusPollingUntil = 0

/** 总页数。 */
const totalPages = computed(() => Math.max(1, Math.ceil(upstreams.value.length / pageSize.value)))

/** 当前页展示的上游切片。 */
const pagedUpstreams = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return upstreams.value.slice(start, start + pageSize.value)
})

const allTags = computed(() => normalizeTags(upstreams.value.flatMap((up) => up.config.tags ?? [])))
const nextSortOrder = computed(
  () => upstreams.value.reduce((max, up) => Math.max(max, up.config.sortOrder), -1) + 1,
)
const hasConnectingUpstream = computed(() => upstreams.value.some((up) => up.state === 'connecting'))

function normalizeTags(tags: string[]): string[] {
  const seen = new Set<string>()
  const out: string[] = []
  for (const tag of tags) {
    const value = tag.trim()
    if (value === '') continue
    const key = value.toLowerCase()
    if (seen.has(key)) continue
    seen.add(key)
    out.push(value)
  }
  return out
}

/** 传输类型显示名。 */
function transportLabel(value: string): string {
  return TRANSPORT_OPTIONS.find((o) => o.value === value)?.label ?? value
}

/** 标记/解除行级繁忙态。 */
function setBusy(key: string, on: boolean): void {
  const next = new Set(busy.value)
  if (on) next.add(key)
  else next.delete(key)
  busy.value = next
}

/** 判断某行某操作是否繁忙。 */
function isBusy(id: string, action: string): boolean {
  return busy.value.has(`${id}:${action}`)
}

/** 展示短暂提示。 */
function showToast(msg: string): void {
  toast.value = msg
  setTimeout(() => {
    if (toast.value === msg) toast.value = ''
  }, 2500)
}

/** 加载上游列表（按 sortOrder 排序）。 */
async function loadUpstreams(showLoading = true): Promise<void> {
  if (showLoading) loading.value = true
  errorMessage.value = ''
  try {
    const list = await listUpstreams()
    list.sort((a, b) => a.config.sortOrder - b.config.sortOrder)
    upstreams.value = list
    await loadToolCounts(list)
    if (currentPage.value > totalPages.value) currentPage.value = totalPages.value
    if (hasConnectingUpstream.value) ensureStatusPolling(60_000)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '加载上游列表失败'
  } finally {
    if (showLoading) loading.value = false
  }
}

onMounted(loadUpstreams)
onUnmounted(stopStatusPolling)

async function loadToolCounts(list: Upstream[]): Promise<void> {
  const next = { ...toolCounts.value }
  const results = await Promise.allSettled(
    list.map(async (up) => {
      const result = await listUpstreamTools(up.id)
      return [up.id, result.count] as const
    }),
  )
  for (const result of results) {
    if (result.status === 'fulfilled') {
      const [id, count] = result.value
      next[id] = count
    }
  }
  toolCounts.value = next
}

function ensureStatusPolling(durationMs: number): void {
  statusPollingUntil = Math.max(statusPollingUntil, Date.now() + durationMs)
  if (statusPollTimer !== undefined) return
  statusPollTimer = window.setInterval(() => {
    if (!hasConnectingUpstream.value && Date.now() > statusPollingUntil) {
      stopStatusPolling()
      return
    }
    void loadUpstreams(false)
  }, 3000)
}

function stopStatusPolling(): void {
  if (statusPollTimer !== undefined) {
    window.clearInterval(statusPollTimer)
    statusPollTimer = undefined
  }
}

/** 打开新建抽屉（手动）。 */
function openCreate(): void {
  editing.value = null
  prefill.value = null
  drawerOpen.value = true
}

/** 打开编辑抽屉。 */
function openEdit(up: Upstream): void {
  editing.value = up
  prefill.value = null
  drawerOpen.value = true
}

/** 模板市场选中模板：关闭弹窗并以预填充打开创建抽屉（Req 14.7）。 */
function onTemplateSelected(pf: PrefillForm): void {
  marketOpen.value = false
  editing.value = null
  prefill.value = pf
  drawerOpen.value = true
}

/** 抽屉保存成功：关闭并刷新列表。 */
async function onSaved(): Promise<void> {
  drawerOpen.value = false
  prefill.value = null
  editing.value = null
  showToast('保存成功')
  await loadUpstreams()
  ensureStatusPolling(60_000)
}

/** 启用/停用切换（Req 3.1、3.2）。 */
async function toggleEnabled(up: Upstream): Promise<void> {
  const key = `${up.id}:toggle`
  if (busy.value.has(key)) return
  setBusy(key, true)
  try {
    await setUpstreamEnabled(up.id, !up.config.enabled)
    await loadUpstreams()
    if (!up.config.enabled) ensureStatusPolling(45_000)
  } catch (err) {
    showToast(err instanceof Error ? err.message : '操作失败')
  } finally {
    setBusy(key, false)
  }
}

/** 手动刷新工具列表（Req 6.4）。 */
async function refresh(up: Upstream): Promise<void> {
  const key = `${up.id}:refresh`
  if (busy.value.has(key)) return
  setBusy(key, true)
  try {
    const count = await refreshUpstream(up.id)
    toolCounts.value = { ...toolCounts.value, [up.id]: count }
    showToast(`已刷新「${up.config.name}」，共 ${count} 个工具`)
    await loadUpstreams()
  } catch (err) {
    showToast(err instanceof Error ? err.message : '刷新失败')
  } finally {
    setBusy(key, false)
  }
}

/** 手动重连（Req 5.6）。 */
async function reconnect(up: Upstream): Promise<void> {
  const key = `${up.id}:reconnect`
  if (busy.value.has(key)) return
  setBusy(key, true)
  try {
    await reconnectUpstream(up.id)
    showToast(`已触发「${up.config.name}」重连`)
    await loadUpstreams()
    ensureStatusPolling(60_000)
  } catch (err) {
    showToast(err instanceof Error ? err.message : '重连失败')
  } finally {
    setBusy(key, false)
  }
}

function formatToolUpdatedAt(value: string | null): string {
  if (value === null || value === '') return '暂无同步时间'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '暂无同步时间'
  return date.toLocaleString('zh-CN', { hour12: false })
}

function schemaPreview(schema: unknown): string {
  if (schema === null || schema === undefined) return '{}'
  try {
    return JSON.stringify(schema)
  } catch {
    return String(schema)
  }
}

async function openToolModal(up: Upstream): Promise<void> {
  toolModalOpen.value = true
  toolModalUpstream.value = up
  toolModalTools.value = []
  toolModalUpdatedAt.value = null
  toolModalError.value = ''
  toolModalLoading.value = true
  try {
    const result = await listUpstreamTools(up.id)
    toolModalTools.value = result.tools
    toolModalUpdatedAt.value = result.updatedAt ?? null
    toolCounts.value = { ...toolCounts.value, [up.id]: result.count }
  } catch (err) {
    toolModalError.value = err instanceof Error ? err.message : '加载工具列表失败'
  } finally {
    toolModalLoading.value = false
  }
}

function openSorting(): void {
  sortDraft.value = [...upstreams.value]
  sortDraggingID.value = null
  sortDragOverID.value = null
  sortSelectedID.value = sortDraft.value[0]?.id ?? ''
  sortTargetPosition.value = sortDraft.value.length > 0 ? '1' : ''
  sortMoveMessage.value = ''
  sortingOpen.value = true
}

function sortDraftIndex(up: Upstream): number {
  return sortDraft.value.findIndex((item) => item.id === up.id)
}

function selectSortItem(up: Upstream): void {
  sortSelectedID.value = up.id
  const index = sortDraftIndex(up)
  sortTargetPosition.value = index >= 0 ? String(index + 1) : ''
  sortMoveMessage.value = ''
}

function syncSelectedSortPosition(): void {
  const index = sortDraft.value.findIndex((item) => item.id === sortSelectedID.value)
  sortTargetPosition.value = index >= 0 ? String(index + 1) : ''
  sortMoveMessage.value = ''
}

function moveSelectedSortDraft(): void {
  const from = sortDraft.value.findIndex((item) => item.id === sortSelectedID.value)
  const target = Number.parseInt(sortTargetPosition.value, 10)
  if (from < 0) {
    sortMoveMessage.value = '请选择要移动的上游'
    return
  }
  if (!Number.isInteger(target) || target < 1 || target > sortDraft.value.length) {
    sortMoveMessage.value = `目标位置需在 1 至 ${sortDraft.value.length} 之间`
    return
  }

  const to = target - 1
  if (from === to) {
    sortMoveMessage.value = '已在目标位置'
    return
  }

  const next = [...sortDraft.value]
  const [moved] = next.splice(from, 1)
  next.splice(to, 0, moved)
  sortDraft.value = next
  sortSelectedID.value = moved.id
  sortTargetPosition.value = String(to + 1)
  sortMoveMessage.value = `已移动到第 ${to + 1} 位`
}

function moveSortDraft(up: Upstream, direction: -1 | 1): void {
  const from = sortDraftIndex(up)
  const to = from + direction
  if (from < 0 || to < 0 || to >= sortDraft.value.length) return
  const next = [...sortDraft.value]
  const [moved] = next.splice(from, 1)
  next.splice(to, 0, moved)
  sortDraft.value = next
  sortSelectedID.value = moved.id
  sortTargetPosition.value = String(to + 1)
  sortMoveMessage.value = ''
}

function moveSortDraftTo(up: Upstream, target: 'first' | 'last'): void {
  const from = sortDraftIndex(up)
  if (from < 0) return
  const next = [...sortDraft.value]
  const [moved] = next.splice(from, 1)
  next.splice(target === 'first' ? 0 : next.length, 0, moved)
  sortDraft.value = next
  sortSelectedID.value = moved.id
  sortTargetPosition.value = target === 'first' ? '1' : String(next.length)
  sortMoveMessage.value = ''
}

function onSortDragStart(event: DragEvent, up: Upstream): void {
  if (busy.value.has('reorder')) {
    event.preventDefault()
    return
  }
  sortDraggingID.value = up.id
  sortSelectedID.value = up.id
  sortDragOverID.value = null
  event.dataTransfer?.setData('text/plain', up.id)
  if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move'
}

function onSortDragOver(event: DragEvent, up: Upstream): void {
  if (sortDraggingID.value === null || sortDraggingID.value === up.id) return
  event.preventDefault()
  sortDragOverID.value = up.id
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
}

function onSortDrop(event: DragEvent, target: Upstream): void {
  event.preventDefault()
  const sourceID = sortDraggingID.value ?? event.dataTransfer?.getData('text/plain') ?? ''
  sortDraggingID.value = null
  sortDragOverID.value = null
  if (sourceID === '' || sourceID === target.id || busy.value.has('reorder')) return

  const from = sortDraft.value.findIndex((u) => u.id === sourceID)
  const to = sortDraft.value.findIndex((u) => u.id === target.id)
  if (from < 0 || to < 0) return

  const reordered = [...sortDraft.value]
  const [moved] = reordered.splice(from, 1)
  reordered.splice(to, 0, moved)
  sortDraft.value = reordered
  sortSelectedID.value = moved.id
  sortTargetPosition.value = String(to + 1)
  sortMoveMessage.value = ''
}

function onSortDragEnd(): void {
  sortDraggingID.value = null
  sortDragOverID.value = null
}

async function saveSorting(): Promise<void> {
  if (busy.value.has('reorder')) return
  const reordered = [...sortDraft.value]
  setBusy('reorder', true)
  try {
    await reorderUpstreams(reordered.map((u) => u.id))
    upstreams.value = reordered
    sortingOpen.value = false
    showToast('排序已保存')
    await loadUpstreams()
  } catch (err) {
    showToast(err instanceof Error ? err.message : '排序失败')
    await loadUpstreams()
  } finally {
    setBusy('reorder', false)
  }
}

/** 请求删除确认。 */
function askDelete(up: Upstream): void {
  deleting.value = up
}

/** 确认删除（Req 2.5）。 */
async function confirmDelete(): Promise<void> {
  if (deleting.value === null) return
  const up = deleting.value
  try {
    await deleteUpstream(up.id)
    showToast(`已删除「${up.config.name}」`)
    deleting.value = null
    await loadUpstreams()
  } catch (err) {
    showToast(err instanceof Error ? err.message : '删除失败')
  }
}

/** 切换分页。 */
function goPage(p: number): void {
  if (p < 1 || p > totalPages.value) return
  currentPage.value = p
}
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="上游 MCP 管理" />

    <!-- 工具栏 -->
    <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <p class="text-sm text-gray-500 dark:text-gray-400">
        共 {{ upstreams.length }} 个上游 MCP 服务
      </p>
      <div class="flex flex-wrap items-center gap-2">
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-lg border border-gray-300 px-3.5 py-2 text-sm font-medium text-gray-700 transition hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          :disabled="loading"
          @click="() => loadUpstreams()"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
            <path
              d="M4 4v6h6M20 20v-6h-6M20 9a8 8 0 0 0-15-2M4 15a8 8 0 0 0 15 2"
              stroke="currentColor"
              stroke-width="1.8"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
          </svg>
          刷新列表
        </button>
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-lg border border-gray-300 px-3.5 py-2 text-sm font-medium text-gray-700 transition hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          :disabled="upstreams.length <= 1 || loading"
          @click="openSorting"
        >
          <svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none">
            <path
              d="M8 7h12M4 7h.01M8 12h12M4 12h.01M8 17h12M4 17h.01"
              stroke="currentColor"
              stroke-width="2"
              stroke-linecap="round"
            />
          </svg>
          排序
        </button>
        <button
          type="button"
          class="border-brand-300 text-brand-600 hover:bg-brand-50 dark:border-brand-500/40 dark:text-brand-400 dark:hover:bg-brand-500/10 inline-flex items-center gap-1.5 rounded-lg border px-3.5 py-2 text-sm font-medium transition"
          @click="marketOpen = true"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
            <path
              d="M3 9h18M9 21V9M4 4h16a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1Z"
              stroke="currentColor"
              stroke-width="1.6"
              stroke-linejoin="round"
            />
          </svg>
          模板市场
        </button>
        <button
          type="button"
          class="bg-brand-500 hover:bg-brand-600 inline-flex items-center gap-1.5 rounded-lg px-3.5 py-2 text-sm font-medium text-white transition"
          @click="openCreate"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
            <path
              d="M12 5v14M5 12h14"
              stroke="currentColor"
              stroke-width="1.8"
              stroke-linecap="round"
            />
          </svg>
          新建上游
        </button>
      </div>
    </div>

    <!-- 操作提示 -->
    <p
      v-if="toast !== ''"
      class="bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400 mb-4 rounded-lg px-4 py-2.5 text-sm"
    >
      {{ toast }}
    </p>

    <!-- 列表：卡片网格（响应式，移动端友好，替代表格） -->
    <div>
      <p
        v-if="errorMessage !== ''"
        class="bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400 mb-4 rounded-lg px-4 py-2.5 text-sm"
      >
        {{ errorMessage }}
      </p>

      <div
        v-if="loading"
        class="rounded-2xl border border-gray-200 bg-white px-5 py-12 text-center text-sm text-gray-400 dark:border-gray-800 dark:bg-white/[0.03]"
      >
        加载中…
      </div>
      <div
        v-else-if="upstreams.length === 0"
        class="rounded-2xl border border-dashed border-gray-300 bg-white px-5 py-12 text-center text-sm text-gray-400 dark:border-gray-700 dark:bg-white/[0.03]"
      >
        暂无上游 MCP 服务，点击「新建上游」或「模板市场」开始接入
      </div>

      <div v-else class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
        <div
          v-for="up in pagedUpstreams"
          :key="up.id"
          class="flex flex-col rounded-2xl border border-gray-200 bg-white p-5 shadow-sm transition hover:shadow-md dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <!-- 头部：名称 + 启停 -->
          <div class="mb-3 flex items-start justify-between gap-3">
            <div class="min-w-0 flex-1">
              <div class="truncate font-medium text-gray-800 dark:text-white/90">
                {{ up.config.name }}
              </div>
              <div class="mt-1 flex flex-wrap items-center gap-1.5">
                <span
                  class="inline-flex items-center rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-600 dark:bg-gray-800 dark:text-gray-300"
                >
                  {{ transportLabel(up.config.transport) }}
                </span>
                <ConnStateBadge :state="up.state" />
              </div>
              <div v-if="up.config.tags?.length" class="mt-2 flex flex-wrap gap-1.5">
                <span
                  v-for="tag in up.config.tags"
                  :key="tag"
                  class="bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-300 inline-flex max-w-full items-center truncate rounded-full px-2 py-0.5 text-xs font-medium"
                >
                  {{ tag }}
                </span>
              </div>
            </div>
            <button
              type="button"
              role="switch"
              :aria-checked="up.config.enabled"
              :disabled="isBusy(up.id, 'toggle')"
              class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition disabled:opacity-60"
              :class="up.config.enabled ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
              @click="toggleEnabled(up)"
            >
              <span
                class="inline-block h-4 w-4 transform rounded-full bg-white transition"
                :class="up.config.enabled ? 'translate-x-6' : 'translate-x-1'"
              ></span>
            </button>
          </div>

          <!-- 最近错误（如有） -->
          <Tooltip v-if="up.lastError" :content="up.lastError" placement="bottom-start">
            <p
              class="bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400 mb-3 truncate rounded-lg px-3 py-1.5 text-xs"
            >
              {{ up.lastError }}
            </p>
          </Tooltip>

          <!-- 操作 -->
          <div
            class="mt-auto flex flex-wrap items-center justify-between gap-2 border-t border-gray-100 pt-3 dark:border-gray-800"
          >
            <button
              type="button"
              class="rounded-lg bg-gray-50 px-2.5 py-1.5 text-xs font-medium text-gray-600 transition hover:bg-brand-50 hover:text-brand-600 dark:bg-white/5 dark:text-gray-300 dark:hover:bg-brand-500/10 dark:hover:text-brand-400"
              @click="openToolModal(up)"
            >
              工具 {{ toolCounts[up.id] ?? 0 }}
            </button>
            <div class="flex flex-wrap items-center justify-end gap-1.5">
              <button
                type="button"
                class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-100 disabled:opacity-50 dark:text-gray-300 dark:hover:bg-gray-800"
                :disabled="isBusy(up.id, 'refresh')"
                @click="refresh(up)"
              >
                {{ isBusy(up.id, 'refresh') ? '刷新中…' : '刷新' }}
              </button>
              <button
                type="button"
                class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-100 disabled:opacity-50 dark:text-gray-300 dark:hover:bg-gray-800"
                :disabled="isBusy(up.id, 'reconnect')"
                @click="reconnect(up)"
              >
                {{ isBusy(up.id, 'reconnect') ? '重连中…' : '重连' }}
              </button>
              <button
                type="button"
                class="text-brand-600 hover:bg-brand-50 dark:text-brand-400 dark:hover:bg-brand-500/10 rounded-lg px-2.5 py-1.5 text-xs font-medium"
                @click="openEdit(up)"
              >
                编辑
              </button>
              <button
                type="button"
                class="text-error-600 hover:bg-error-50 dark:text-error-400 dark:hover:bg-error-500/10 rounded-lg px-2.5 py-1.5 text-xs font-medium"
                @click="askDelete(up)"
              >
                删除
              </button>
            </div>
          </div>
        </div>
      </div>

      <!-- 分页 -->
      <div v-if="totalPages > 1" class="mt-4 flex items-center justify-between">
        <span class="text-xs text-gray-500 dark:text-gray-400">
          第 {{ currentPage }} / {{ totalPages }} 页
        </span>
        <div class="flex items-center gap-1.5">
          <button
            type="button"
            class="rounded-lg border border-gray-300 px-3 py-1.5 text-sm text-gray-600 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            :disabled="currentPage === 1"
            @click="goPage(currentPage - 1)"
          >
            上一页
          </button>
          <button
            type="button"
            class="rounded-lg border border-gray-300 px-3 py-1.5 text-sm text-gray-600 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            :disabled="currentPage === totalPages"
            @click="goPage(currentPage + 1)"
          >
            下一页
          </button>
        </div>
      </div>
    </div>

    <!-- 创建/编辑抽屉 -->
    <UpstreamFormDrawer
      :open="drawerOpen"
      :upstream="editing"
      :prefill="prefill"
      :tag-options="allTags"
      :next-sort-order="nextSortOrder"
      @close="drawerOpen = false"
      @saved="onSaved"
    />

    <!-- 模板市场弹窗 -->
    <TemplateMarketModal
      :open="marketOpen"
      @close="marketOpen = false"
      @select="onTemplateSelected"
    />

    <!-- 工具列表 -->
    <transition name="fade">
      <div
        v-if="toolModalOpen && toolModalUpstream !== null"
        class="fixed inset-0 z-[100001] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
        @click.self="toolModalOpen = false"
      >
        <div
          class="flex max-h-[86vh] w-full max-w-3xl flex-col rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-gray-800 dark:bg-gray-900"
        >
          <div
            class="flex items-center justify-between gap-3 border-b border-gray-200 px-5 py-4 dark:border-gray-800"
          >
            <div class="min-w-0">
              <h3 class="truncate text-base font-semibold text-gray-800 dark:text-white/90">
                {{ toolModalUpstream.config.name }} 的工具
              </h3>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                {{ formatToolUpdatedAt(toolModalUpdatedAt) }} · 共 {{ toolModalTools.length }} 个
              </p>
            </div>
            <button
              v-tooltip:bottom-end="'关闭'"
              type="button"
              class="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-800"
              aria-label="关闭"
              @click="toolModalOpen = false"
            >
              <svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none">
                <path
                  d="M6 6l12 12M6 18L18 6"
                  stroke="currentColor"
                  stroke-width="1.8"
                  stroke-linecap="round"
                />
              </svg>
            </button>
          </div>

          <div class="custom-scrollbar flex-1 overflow-y-auto p-5">
            <p
              v-if="toolModalError !== ''"
              class="mb-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
            >
              {{ toolModalError }}
            </p>
            <div v-if="toolModalLoading" class="py-12 text-center text-sm text-gray-400">
              加载中…
            </div>
            <div
              v-else-if="toolModalTools.length === 0"
              class="rounded-xl border border-dashed border-gray-300 px-4 py-10 text-center text-sm text-gray-400 dark:border-gray-700"
            >
              暂无工具缓存，可先刷新工具列表
            </div>
            <div v-else class="grid grid-cols-1 gap-3 md:grid-cols-2">
              <article
                v-for="tool in toolModalTools"
                :key="`${tool.upstreamId}:${tool.originalName}:${tool.name}`"
                class="rounded-xl border border-gray-200 p-4 dark:border-gray-800 dark:bg-white/[0.03]"
              >
                <div class="min-w-0">
                  <p class="truncate text-sm font-semibold text-gray-800 dark:text-white/90">
                    {{ tool.name }}
                  </p>
                  <p class="mt-1 truncate font-mono text-xs text-gray-400">
                    {{ tool.originalName }}
                  </p>
                </div>
                <p class="mt-3 line-clamp-3 text-sm leading-6 text-gray-500 dark:text-gray-400">
                  {{ tool.description || '暂无描述' }}
                </p>
                <p class="mt-3 truncate rounded-lg bg-gray-50 px-3 py-2 font-mono text-xs text-gray-500 dark:bg-gray-800/60 dark:text-gray-400">
                  {{ schemaPreview(tool.inputSchema) }}
                </p>
              </article>
            </div>
          </div>
        </div>
      </div>
    </transition>

    <!-- 全量排序 -->
    <transition name="fade">
      <div
        v-if="sortingOpen"
        class="fixed inset-0 z-[100001] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
        @click.self="sortingOpen = false"
      >
        <div
          class="flex max-h-[86vh] w-full max-w-2xl flex-col rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-gray-800 dark:bg-gray-900"
        >
          <div
            class="flex items-center justify-between gap-3 border-b border-gray-200 px-5 py-4 dark:border-gray-800"
          >
            <div>
              <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">上游排序</h3>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                拖拽适合近距离调整；数量多时可选择上游并输入目标位置。
              </p>
            </div>
            <button
              v-tooltip:bottom-end="'关闭'"
              type="button"
              class="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-800"
              aria-label="关闭"
              @click="sortingOpen = false"
            >
              <svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none">
                <path
                  d="M6 6l12 12M6 18L18 6"
                  stroke="currentColor"
                  stroke-width="1.8"
                  stroke-linecap="round"
                />
              </svg>
            </button>
          </div>

          <div class="border-b border-gray-200 px-4 py-3 dark:border-gray-800">
            <div
              class="grid grid-cols-1 gap-3 sm:grid-cols-[minmax(0,1fr)_160px_auto] sm:items-end"
            >
              <label class="block">
                <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400"
                  >上游</span
                >
                <select
                  v-model="sortSelectedID"
                  class="focus:border-brand-300 focus:ring-brand-500/10 h-10 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-800 shadow-sm focus:ring-3 focus:outline-none dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
                  @change="syncSelectedSortPosition"
                >
                  <option v-for="(up, index) in sortDraft" :key="up.id" :value="up.id">
                    {{ index + 1 }}. {{ up.config.name }}
                  </option>
                </select>
              </label>
              <label class="block">
                <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400"
                  >目标位置</span
                >
                <input
                  v-model="sortTargetPosition"
                  type="number"
                  min="1"
                  :max="sortDraft.length"
                  class="focus:border-brand-300 focus:ring-brand-500/10 h-10 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-800 shadow-sm focus:ring-3 focus:outline-none dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
                />
              </label>
              <button
                type="button"
                class="bg-brand-500 hover:bg-brand-600 h-10 rounded-lg px-4 text-sm font-medium text-white transition disabled:opacity-60"
                :disabled="sortDraft.length <= 1 || busy.has('reorder')"
                @click="moveSelectedSortDraft"
              >
                移动
              </button>
            </div>
            <p v-if="sortMoveMessage !== ''" class="mt-2 text-xs text-gray-500 dark:text-gray-400">
              {{ sortMoveMessage }}
            </p>
          </div>

          <div class="custom-scrollbar flex-1 space-y-2 overflow-y-auto p-4">
            <div
              v-for="(up, index) in sortDraft"
              :key="up.id"
              draggable="true"
              class="flex items-center gap-3 rounded-lg border border-gray-200 bg-white p-3 transition dark:border-gray-800 dark:bg-white/[0.03]"
              :class="[
                sortDraggingID === up.id ? 'opacity-60' : '',
                sortSelectedID === up.id
                  ? 'border-brand-300 bg-brand-50/50 dark:border-brand-500/60 dark:bg-brand-500/10'
                  : '',
                sortDragOverID === up.id
                  ? 'border-brand-300 ring-brand-500/20 dark:border-brand-500/60 ring-2'
                  : '',
              ]"
              @click="selectSortItem(up)"
              @dragstart="onSortDragStart($event, up)"
              @dragover="onSortDragOver($event, up)"
              @drop="onSortDrop($event, up)"
              @dragend="onSortDragEnd"
            >
              <div
                class="flex h-8 w-8 shrink-0 cursor-grab items-center justify-center rounded-lg text-gray-400 active:cursor-grabbing"
              >
                <svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none">
                  <path
                    d="M9 5.5h.01M15 5.5h.01M9 12h.01M15 12h.01M9 18.5h.01M15 18.5h.01"
                    stroke="currentColor"
                    stroke-width="3"
                    stroke-linecap="round"
                  />
                </svg>
              </div>
              <div class="w-8 shrink-0 text-sm text-gray-400 tabular-nums">{{ index + 1 }}</div>
              <div class="min-w-0 flex-1">
                <div class="truncate text-sm font-medium text-gray-800 dark:text-white/90">
                  {{ up.config.name }}
                </div>
                <div class="mt-1 flex flex-wrap items-center gap-1.5">
                  <span
                    class="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-gray-800 dark:text-gray-400"
                  >
                    {{ transportLabel(up.config.transport) }}
                  </span>
                  <span
                    v-for="tag in up.config.tags ?? []"
                    :key="tag"
                    class="bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-300 rounded-full px-2 py-0.5 text-xs"
                  >
                    {{ tag }}
                  </span>
                </div>
              </div>
              <div class="flex shrink-0 items-center gap-1">
                <button
                  v-tooltip:bottom="'置顶'"
                  type="button"
                  class="rounded-md p-1.5 text-gray-500 hover:bg-gray-100 disabled:opacity-30 dark:hover:bg-gray-800"
                  :disabled="index === 0"
                  aria-label="置顶"
                  @click="moveSortDraftTo(up, 'first')"
                >
                  <svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none">
                    <path
                      d="M6 5h12M7 15l5-5 5 5"
                      stroke="currentColor"
                      stroke-width="2"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                    />
                  </svg>
                </button>
                <button
                  v-tooltip:bottom="'上移'"
                  type="button"
                  class="rounded-md p-1.5 text-gray-500 hover:bg-gray-100 disabled:opacity-30 dark:hover:bg-gray-800"
                  :disabled="index === 0"
                  aria-label="上移"
                  @click="moveSortDraft(up, -1)"
                >
                  <svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none">
                    <path
                      d="M7 14l5-5 5 5"
                      stroke="currentColor"
                      stroke-width="2"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                    />
                  </svg>
                </button>
                <button
                  v-tooltip:bottom="'下移'"
                  type="button"
                  class="rounded-md p-1.5 text-gray-500 hover:bg-gray-100 disabled:opacity-30 dark:hover:bg-gray-800"
                  :disabled="index === sortDraft.length - 1"
                  aria-label="下移"
                  @click="moveSortDraft(up, 1)"
                >
                  <svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none">
                    <path
                      d="M7 10l5 5 5-5"
                      stroke="currentColor"
                      stroke-width="2"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                    />
                  </svg>
                </button>
                <button
                  v-tooltip:bottom-end="'置底'"
                  type="button"
                  class="rounded-md p-1.5 text-gray-500 hover:bg-gray-100 disabled:opacity-30 dark:hover:bg-gray-800"
                  :disabled="index === sortDraft.length - 1"
                  aria-label="置底"
                  @click="moveSortDraftTo(up, 'last')"
                >
                  <svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none">
                    <path
                      d="M6 19h12M7 9l5 5 5-5"
                      stroke="currentColor"
                      stroke-width="2"
                      stroke-linecap="round"
                      stroke-linejoin="round"
                    />
                  </svg>
                </button>
              </div>
            </div>
          </div>

          <div
            class="flex items-center justify-end gap-3 border-t border-gray-200 px-5 py-4 dark:border-gray-800"
          >
            <button
              type="button"
              class="rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
              @click="sortingOpen = false"
            >
              取消
            </button>
            <button
              type="button"
              class="bg-brand-500 hover:bg-brand-600 rounded-lg px-4 py-2 text-sm font-medium text-white transition disabled:opacity-60"
              :disabled="busy.has('reorder')"
              @click="saveSorting"
            >
              {{ busy.has('reorder') ? '保存中...' : '保存排序' }}
            </button>
          </div>
        </div>
      </div>
    </transition>

    <!-- 删除确认 -->
    <transition name="fade">
      <div
        v-if="deleting !== null"
        class="fixed inset-0 z-[100001] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
        @click.self="deleting = null"
      >
        <div
          class="w-full max-w-sm rounded-2xl border border-gray-200 bg-white p-6 shadow-xl dark:border-gray-800 dark:bg-gray-900"
        >
          <h3 class="mb-2 text-base font-semibold text-gray-800 dark:text-white/90">确认删除</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            确定删除上游 MCP「{{
              deleting.config.name
            }}」吗？该操作将级联清理其工具缓存与规则，且不可恢复。
          </p>
          <div class="flex justify-end gap-3">
            <button
              type="button"
              class="rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
              @click="deleting = null"
            >
              取消
            </button>
            <button
              type="button"
              class="bg-error-500 hover:bg-error-600 rounded-lg px-4 py-2 text-sm font-medium text-white transition"
              @click="confirmDelete"
            >
              删除
            </button>
          </div>
        </div>
      </div>
    </transition>
  </AdminLayout>
</template>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.2s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
