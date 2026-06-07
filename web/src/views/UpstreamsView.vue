<script setup lang="ts">
/**
 * 上游 MCP 管理页（任务 26.1）。
 *
 * 覆盖 Req 14.3/14.6/14.7（模板市场接入向导）、17.5（管理 REST API 接入）以及
 * 上游 CRUD / 启停 / 排序 / 连接状态展示 / 手动刷新（Req 2.x、3.x、5.6、6.4）。
 *
 * 风格：Tailwind 工具类 + TailAdmin 组件风格（卡片、表格、徽章、按钮、分页）；
 * 响应式：分页条数来自 useBreakpoint，小屏下表格可横向滚动。
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

const currentPageTitle = ref('上游 MCP 管理')

/** 总页数。 */
const totalPages = computed(() => Math.max(1, Math.ceil(upstreams.value.length / pageSize.value)))

/** 当前页展示的上游切片。 */
const pagedUpstreams = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return upstreams.value.slice(start, start + pageSize.value)
})

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

/**
 * 上移/下移：交换相邻两项后提交全量顺序（Req 3.4、3.5）。
 * 基于全量列表索引（非分页索引）操作，保证跨页排序正确。
 */
async function move(up: Upstream, direction: -1 | 1): Promise<void> {
  const idx = upstreams.value.findIndex((u) => u.id === up.id)
  const target = idx + direction
  if (idx < 0 || target < 0 || target >= upstreams.value.length) return

  const reordered = [...upstreams.value]
  const tmp = reordered[idx]
  reordered[idx] = reordered[target]
  reordered[target] = tmp

  // 乐观更新。
  upstreams.value = reordered
  try {
    await reorderUpstreams(reordered.map((u) => u.id))
    await loadUpstreams()
  } catch (err) {
    showToast(err instanceof Error ? err.message : '排序失败')
    await loadUpstreams()
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

/** 全量列表中的索引（用于禁用首/尾项的上移/下移按钮）。 */
function globalIndex(up: Upstream): number {
  return upstreams.value.findIndex((u) => u.id === up.id)
}
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb :pageTitle="currentPageTitle" />

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
            <path d="M4 4v6h6M20 20v-6h-6M20 9a8 8 0 0 0-15-2M4 15a8 8 0 0 0 15 2" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
          刷新列表
        </button>
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-lg border border-brand-300 px-3.5 py-2 text-sm font-medium text-brand-600 transition hover:bg-brand-50 dark:border-brand-500/40 dark:text-brand-400 dark:hover:bg-brand-500/10"
          @click="marketOpen = true"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
            <path d="M3 9h18M9 21V9M4 4h16a1 1 0 0 1 1 1v14a1 1 0 0 1-1 1H4a1 1 0 0 1-1-1V5a1 1 0 0 1 1-1Z" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round" />
          </svg>
          模板市场
        </button>
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-lg bg-brand-500 px-3.5 py-2 text-sm font-medium text-white transition hover:bg-brand-600"
          @click="openCreate"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
            <path d="M12 5v14M5 12h14" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
          </svg>
          新建上游
        </button>
      </div>
    </div>

    <!-- 操作提示 -->
    <p
      v-if="toast !== ''"
      class="mb-4 rounded-lg bg-success-50 px-4 py-2.5 text-sm text-success-700 dark:bg-success-500/10 dark:text-success-400"
    >
      {{ toast }}
    </p>

    <!-- 列表卡片 -->
    <div
      class="rounded-2xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-white/[0.03]"
    >
      <p
        v-if="errorMessage !== ''"
        class="m-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
      >
        {{ errorMessage }}
      </p>

      <div class="max-w-full overflow-x-auto">
        <table class="min-w-full">
          <thead>
            <tr class="border-b border-gray-200 text-left text-xs font-medium text-gray-500 uppercase dark:border-gray-800 dark:text-gray-400">
              <th class="px-5 py-3.5">名称</th>
              <th class="px-5 py-3.5">传输类型</th>
              <th class="px-5 py-3.5">启用</th>
              <th class="px-5 py-3.5">连接状态</th>
              <th class="px-5 py-3.5">排序</th>
              <th class="px-5 py-3.5 text-right">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-gray-800">
            <tr v-if="loading">
              <td colspan="6" class="px-5 py-10 text-center text-sm text-gray-400">加载中…</td>
            </tr>
            <tr v-else-if="upstreams.length === 0">
              <td colspan="6" class="px-5 py-10 text-center text-sm text-gray-400">
                暂无上游 MCP 服务，点击「新建上游」或「模板市场」开始接入
              </td>
            </tr>
            <tr
              v-for="up in pagedUpstreams"
              v-else
              :key="up.id"
              class="text-sm text-gray-700 dark:text-gray-300"
            >
              <!-- 名称 -->
              <td class="px-5 py-4">
                <div class="font-medium text-gray-800 dark:text-white/90">{{ up.config.name }}</div>
                <div v-if="up.lastError" class="mt-0.5 max-w-xs truncate text-xs text-error-500" :title="up.lastError">
                  {{ up.lastError }}
                </div>
              </td>
              <!-- 传输类型徽章 -->
              <td class="px-5 py-4">
                <span class="inline-flex items-center rounded-full bg-gray-100 px-2.5 py-1 text-xs text-gray-600 dark:bg-gray-800 dark:text-gray-300">
                  {{ transportLabel(up.config.transport) }}
                </span>
              </td>
              <!-- 启用开关 -->
              <td class="px-5 py-4">
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
              </td>
              <!-- 连接状态徽章 -->
              <td class="px-5 py-4">
                <ConnStateBadge :state="up.state" />
              </td>
              <!-- 排序（上移/下移） -->
              <td class="px-5 py-4">
                <div class="flex items-center gap-1">
                  <button
                    type="button"
                    class="rounded-md border border-gray-200 p-1 text-gray-500 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:hover:bg-gray-800"
                    :disabled="globalIndex(up) === 0"
                    aria-label="上移"
                    @click="move(up, -1)"
                  >
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none"><path d="M6 15l6-6 6 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
                  </button>
                  <button
                    type="button"
                    class="rounded-md border border-gray-200 p-1 text-gray-500 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:hover:bg-gray-800"
                    :disabled="globalIndex(up) === upstreams.length - 1"
                    aria-label="下移"
                    @click="move(up, 1)"
                  >
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none"><path d="M6 9l6 6 6-6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
                  </button>
                </div>
              </td>
              <!-- 操作 -->
              <td class="px-5 py-4">
                <div class="flex items-center justify-end gap-1.5">
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
                    class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-brand-600 hover:bg-brand-50 dark:text-brand-400 dark:hover:bg-brand-500/10"
                    @click="openEdit(up)"
                  >
                    编辑
                  </button>
                  <button
                    type="button"
                    class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-error-600 hover:bg-error-50 dark:text-error-400 dark:hover:bg-error-500/10"
                    @click="askDelete(up)"
                  >
                    删除
                  </button>
                </div>
              </td>
            </tr>
          </tbody>
        </table>
      </div>

      <!-- 分页 -->
      <div
        v-if="totalPages > 1"
        class="flex items-center justify-between border-t border-gray-200 px-5 py-4 dark:border-gray-800"
      >
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
        <div class="w-full max-w-sm rounded-2xl border border-gray-200 bg-white p-6 shadow-xl dark:border-gray-800 dark:bg-gray-900">
          <h3 class="mb-2 text-base font-semibold text-gray-800 dark:text-white/90">确认删除</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            确定删除上游 MCP「{{ deleting.config.name }}」吗？该操作将级联清理其工具缓存与规则，且不可恢复。
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
              class="rounded-lg bg-error-500 px-4 py-2 text-sm font-medium text-white transition hover:bg-error-600"
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
