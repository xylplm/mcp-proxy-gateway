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
import { computed, onMounted, ref } from 'vue'
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
  reorderUpstreams,
  refreshUpstream,
  reconnectUpstream,
  TRANSPORT_OPTIONS,
  type Upstream,
} from '@/api/upstreams'
import type { PrefillForm } from '@/api/templates'

const { pageSize } = useBreakpoint()

/** 全量上游列表（按 sortOrder 升序）。 */
const upstreams = ref<Upstream[]>([])
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
const draggingID = ref<string | null>(null)
const dragOverID = ref<string | null>(null)

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
async function loadUpstreams(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const list = await listUpstreams()
    list.sort((a, b) => a.config.sortOrder - b.config.sortOrder)
    upstreams.value = list
    if (currentPage.value > totalPages.value) currentPage.value = totalPages.value
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '加载上游列表失败'
  } finally {
    loading.value = false
  }
}

onMounted(loadUpstreams)

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
}

/** 启用/停用切换（Req 3.1、3.2）。 */
async function toggleEnabled(up: Upstream): Promise<void> {
  const key = `${up.id}:toggle`
  if (busy.value.has(key)) return
  setBusy(key, true)
  try {
    await setUpstreamEnabled(up.id, !up.config.enabled)
    await loadUpstreams()
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
  } catch (err) {
    showToast(err instanceof Error ? err.message : '重连失败')
  } finally {
    setBusy(key, false)
  }
}

function onDragStart(event: DragEvent, up: Upstream): void {
  if (busy.value.has('reorder')) {
    event.preventDefault()
    return
  }
  draggingID.value = up.id
  dragOverID.value = null
  event.dataTransfer?.setData('text/plain', up.id)
  if (event.dataTransfer) event.dataTransfer.effectAllowed = 'move'
}

function onDragOver(event: DragEvent, up: Upstream): void {
  if (draggingID.value === null || draggingID.value === up.id) return
  event.preventDefault()
  dragOverID.value = up.id
  if (event.dataTransfer) event.dataTransfer.dropEffect = 'move'
}

async function onDrop(event: DragEvent, target: Upstream): Promise<void> {
  event.preventDefault()
  const sourceID = draggingID.value ?? event.dataTransfer?.getData('text/plain') ?? ''
  draggingID.value = null
  dragOverID.value = null
  if (sourceID === '' || sourceID === target.id || busy.value.has('reorder')) return

  const from = upstreams.value.findIndex((u) => u.id === sourceID)
  const to = upstreams.value.findIndex((u) => u.id === target.id)
  if (from < 0 || to < 0) return

  const reordered = [...upstreams.value]
  const [moved] = reordered.splice(from, 1)
  reordered.splice(to, 0, moved)

  upstreams.value = reordered
  setBusy('reorder', true)
  try {
    await reorderUpstreams(reordered.map((u) => u.id))
    await loadUpstreams()
  } catch (err) {
    showToast(err instanceof Error ? err.message : '排序失败')
    await loadUpstreams()
  } finally {
    setBusy('reorder', false)
  }
}

function onDragEnd(): void {
  draggingID.value = null
  dragOverID.value = null
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
          @click="loadUpstreams"
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
          :class="[
            draggingID === up.id ? 'opacity-60' : '',
            dragOverID === up.id
              ? 'border-brand-300 ring-brand-500/20 dark:border-brand-500/60 ring-2'
              : '',
          ]"
          @dragover="onDragOver($event, up)"
          @drop="onDrop($event, up)"
        >
          <!-- 头部：名称 + 启停 -->
          <div class="mb-3 flex items-start justify-between gap-3">
            <div class="flex min-w-0 flex-1 items-start gap-2">
              <button
                v-tooltip:bottom="busy.has('reorder') ? '正在保存排序' : '拖拽排序'"
                type="button"
                draggable="true"
                class="mt-0.5 inline-flex h-8 w-8 shrink-0 cursor-grab items-center justify-center rounded-lg text-gray-400 transition hover:bg-gray-100 hover:text-gray-600 active:cursor-grabbing disabled:cursor-wait disabled:opacity-50 dark:hover:bg-gray-800 dark:hover:text-gray-300"
                :disabled="busy.has('reorder')"
                aria-label="拖拽排序"
                @dragstart="onDragStart($event, up)"
                @dragend="onDragEnd"
              >
                <svg aria-hidden="true" width="18" height="18" viewBox="0 0 24 24" fill="none">
                  <path
                    d="M9 5.5h.01M15 5.5h.01M9 12h.01M15 12h.01M9 18.5h.01M15 18.5h.01"
                    stroke="currentColor"
                    stroke-width="3"
                    stroke-linecap="round"
                  />
                </svg>
              </button>
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
          <p
            v-if="up.lastError"
            class="bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400 mb-3 truncate rounded-lg px-3 py-1.5 text-xs"
            :title="up.lastError"
          >
            {{ up.lastError }}
          </p>

          <!-- 操作 -->
          <div
            class="mt-auto flex flex-wrap items-center justify-end gap-1.5 border-t border-gray-100 pt-3 dark:border-gray-800"
          >
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
