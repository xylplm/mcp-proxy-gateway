<script setup lang="ts">
/**
 * API Key 管理页（任务 26.3）。
 *
 * 覆盖 Req 12.1（API Key 增删/启停、有效期）、13.1（API Key 级屏蔽规则）、
 * 13.9（来源白名单 ACL）、17.5（管理 REST API 接入）、21（限流配置）。
 *
 * 关键行为（Req 12.3）：创建响应返回一次性明文密钥，UI 仅以模态框展示一次并提供复制，
 * 明确告知关闭后无法再次查看；列表/详情永不回显明文。
 *
 * 风格：Tailwind 工具类 + TailAdmin 组件风格（卡片、表格、徽章、按钮、开关、分页、模态框）；
 * 响应式：分页条数来自 useBreakpoint，小屏下表格可横向滚动。
 */
import { computed, onMounted, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import APIKeyCreateModal from '@/components/apikeys/APIKeyCreateModal.vue'
import PlaintextKeyModal from '@/components/apikeys/PlaintextKeyModal.vue'
import APIKeyConfigModal from '@/components/apikeys/APIKeyConfigModal.vue'
import { useBreakpoint } from '@/composables/useBreakpoint'
import { useClipboard } from '@/composables/useClipboard'
import {
  listAPIKeys,
  setAPIKeyEnabled,
  deleteAPIKey,
  type APIKey,
  type CreatedAPIKey,
} from '@/api/apikeys'

const { pageSize } = useBreakpoint()
const { copy } = useClipboard()

/** 全量 API Key 列表（按创建时间倒序）。 */
const apiKeys = ref<APIKey[]>([])
const loading = ref(false)
const errorMessage = ref('')
/** 行级操作进行中标记（key = id:action）。 */
const busy = ref<Set<string>>(new Set())
const toast = ref('')

/** 已展开明文的 Key 标识集合（小眼睛切换；默认全部隐藏）。 */
const revealed = ref<Set<string>>(new Set())

/** 分页：当前页（1 起）。 */
const currentPage = ref(1)

/** 各类弹窗开关与目标。 */
const createOpen = ref(false)
/** 创建成功后的一次性明文密钥（Req 12.3）。 */
const created = ref<CreatedAPIKey | null>(null)
/** 当前配置目标。 */
const configuring = ref<APIKey | null>(null)
/** 删除确认目标。 */
const deleting = ref<APIKey | null>(null)

/** 总页数。 */
const totalPages = computed(() => Math.max(1, Math.ceil(apiKeys.value.length / pageSize.value)))

/** 当前页展示切片。 */
const pagedKeys = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return apiKeys.value.slice(start, start + pageSize.value)
})

/** 标记/解除行级繁忙态。 */
function setBusy(key: string, on: boolean): void {
  const next = new Set(busy.value)
  if (on) next.add(key)
  else next.delete(key)
  busy.value = next
}

function isBusy(id: string, action: string): boolean {
  return busy.value.has(`${id}:${action}`)
}

function showToast(msg: string): void {
  toast.value = msg
  setTimeout(() => {
    if (toast.value === msg) toast.value = ''
  }, 2500)
}

/** 格式化时间（RFC3339 → 本地可读）。 */
function formatTime(value?: string): string {
  if (value === undefined || value === '') return '—'
  const d = new Date(value)
  return Number.isNaN(d.getTime()) ? value : d.toLocaleString()
}

/** 加载 API Key 列表。 */
async function loadAPIKeys(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    apiKeys.value = await listAPIKeys()
    if (currentPage.value > totalPages.value) currentPage.value = totalPages.value
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '加载 API Key 列表失败'
  } finally {
    loading.value = false
  }
}

onMounted(loadAPIKeys)

/** 创建成功：展示一次性明文并刷新列表（Req 12.3）。 */
async function onCreated(key: CreatedAPIKey): Promise<void> {
  createOpen.value = false
  created.value = key
  await loadAPIKeys()
}

/** 启用/停用切换（Req 12.4）。 */
async function toggleEnabled(key: APIKey): Promise<void> {
  const busyKey = `${key.id}:toggle`
  if (busy.value.has(busyKey)) return
  setBusy(busyKey, true)
  try {
    await setAPIKeyEnabled(key.id, !key.enabled)
    await loadAPIKeys()
  } catch (err) {
    showToast(err instanceof Error ? err.message : '操作失败')
  } finally {
    setBusy(busyKey, false)
  }
}

/** 请求删除确认。 */
function askDelete(key: APIKey): void {
  deleting.value = key
}

/** 确认删除（Req 12.2）。 */
async function confirmDelete(): Promise<void> {
  if (deleting.value === null) return
  const key = deleting.value
  try {
    await deleteAPIKey(key.id)
    showToast(`已删除「${key.name}」`)
    deleting.value = null
    await loadAPIKeys()
  } catch (err) {
    showToast(err instanceof Error ? err.message : '删除失败')
  }
}

/** 切换分页。 */
function goPage(p: number): void {
  if (p < 1 || p > totalPages.value) return
  currentPage.value = p
}

/** 是否已过期（仅用于展示，鉴权由后端判定）。 */
function isExpired(key: APIKey): boolean {
  return key.expiresAt !== undefined && new Date(key.expiresAt).getTime() < Date.now()
}

/** 切换某个 Key 的明文显隐（小眼睛）。 */
function toggleReveal(id: string): void {
  const next = new Set(revealed.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  revealed.value = next
}

/** 该 Key 的明文当前是否处于展开状态。 */
function isRevealed(id: string): boolean {
  return revealed.value.has(id)
}

/** 掩码展示：保留前缀，其余以圆点遮挡，避免肩窥。 */
function maskKey(key: APIKey): string {
  const plain = key.plaintextKey ?? ''
  if (plain === '') return `${key.keyPrefix}…`
  const head = plain.slice(0, 8)
  return `${head}${'•'.repeat(18)}`
}

/** 复制某个 Key 的完整明文到剪贴板。 */
async function copyKey(key: APIKey): Promise<void> {
  const plain = key.plaintextKey ?? ''
  if (plain === '') {
    showToast('该 Key 无可复制的明文')
    return
  }
  const ok = await copy(plain)
  showToast(ok ? '已复制到剪贴板' : '复制失败，请手动选择复制')
}
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="API Key 管理" />

    <!-- 工具栏 -->
    <div class="mb-4 flex flex-wrap items-center justify-between gap-3">
      <p class="text-sm text-gray-500 dark:text-gray-400">共 {{ apiKeys.length }} 个 API Key</p>
      <div class="flex flex-wrap items-center gap-2">
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-lg border border-gray-300 px-3.5 py-2 text-sm font-medium text-gray-700 transition hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          :disabled="loading"
          @click="loadAPIKeys"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
            <path d="M4 4v6h6M20 20v-6h-6M20 9a8 8 0 0 0-15-2M4 15a8 8 0 0 0 15 2" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
          刷新列表
        </button>
        <button
          type="button"
          class="inline-flex items-center gap-1.5 rounded-lg bg-brand-500 px-3.5 py-2 text-sm font-medium text-white transition hover:bg-brand-600"
          @click="createOpen = true"
        >
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
            <path d="M12 5v14M5 12h14" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
          </svg>
          新建 API Key
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

    <!-- 列表：卡片网格（响应式，移动端友好，替代表格） -->
    <div>
      <p
        v-if="errorMessage !== ''"
        class="mb-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
      >
        {{ errorMessage }}
      </p>

      <!-- 加载 / 空态 -->
      <div
        v-if="loading"
        class="rounded-2xl border border-gray-200 bg-white px-5 py-12 text-center text-sm text-gray-400 dark:border-gray-800 dark:bg-white/[0.03]"
      >
        加载中…
      </div>
      <div
        v-else-if="apiKeys.length === 0"
        class="rounded-2xl border border-dashed border-gray-300 bg-white px-5 py-12 text-center text-sm text-gray-400 dark:border-gray-700 dark:bg-white/[0.03]"
      >
        暂无 API Key，点击「新建 API Key」开始创建
      </div>

      <!-- 卡片网格：手机 1 列、平板 2 列、桌面 3 列 -->
      <div v-else class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
        <div
          v-for="key in pagedKeys"
          :key="key.id"
          class="flex flex-col rounded-2xl border border-gray-200 bg-white p-5 shadow-sm transition hover:shadow-md dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <!-- 头部：名称 + 启停 -->
          <div class="mb-3 flex items-start justify-between gap-3">
            <div class="min-w-0">
              <Tooltip :content="key.name" placement="bottom-start">
                <div class="truncate font-medium text-gray-800 dark:text-white/90">
                  {{ key.name }}
                </div>
              </Tooltip>
              <div class="mt-1 flex flex-wrap items-center gap-1.5">
                <span
                  class="inline-flex items-center rounded-full px-2 py-0.5 text-xs"
                  :class="
                    key.enabled
                      ? 'bg-success-50 text-success-600 dark:bg-success-500/10 dark:text-success-400'
                      : 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400'
                  "
                >
                  {{ key.enabled ? '已启用' : '已停用' }}
                </span>
                <span
                  v-if="key.rateLimit && key.rateWindowS"
                  class="inline-flex items-center rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-gray-800 dark:text-gray-400"
                >
                  限流 {{ key.rateLimit }}/{{ key.rateWindowS }}s
                </span>
              </div>
            </div>
            <button
              type="button"
              role="switch"
              :aria-checked="key.enabled"
              :disabled="isBusy(key.id, 'toggle')"
              class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition disabled:opacity-60"
              :class="key.enabled ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
              @click="toggleEnabled(key)"
            >
              <span
                class="inline-block h-4 w-4 transform rounded-full bg-white transition"
                :class="key.enabled ? 'translate-x-6' : 'translate-x-1'"
              ></span>
            </button>
          </div>

          <!-- 密钥行：掩码/明文 + 小眼睛 + 复制 -->
          <div class="mb-3 flex items-center gap-2 rounded-lg bg-gray-50 px-3 py-2 dark:bg-gray-800/50">
            <code class="min-w-0 flex-1 truncate font-mono text-xs text-gray-700 dark:text-gray-300">
              {{ isRevealed(key.id) ? key.plaintextKey : maskKey(key) }}
            </code>
            <button
              type="button"
              class="shrink-0 rounded-md p-1.5 text-gray-500 transition hover:bg-gray-200 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-gray-200"
              v-tooltip:bottom="isRevealed(key.id) ? '隐藏' : '查看'"
              :aria-label="isRevealed(key.id) ? '隐藏 API Key' : '查看 API Key'"
              @click="toggleReveal(key.id)"
            >
              <!-- 眼睛（显示）/ 斜杠眼睛（隐藏） -->
              <svg v-if="!isRevealed(key.id)" width="16" height="16" viewBox="0 0 24 24" fill="none">
                <path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7-10-7-10-7Z" stroke="currentColor" stroke-width="1.6" />
                <circle cx="12" cy="12" r="3" stroke="currentColor" stroke-width="1.6" />
              </svg>
              <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none">
                <path d="M3 3l18 18M10.6 10.6a3 3 0 0 0 4.2 4.2M9.9 4.2A9.9 9.9 0 0 1 12 4c6.5 0 10 7 10 7a17.6 17.6 0 0 1-3.3 4M6.1 6.1A17.6 17.6 0 0 0 2 11s3.5 7 10 7a9.8 9.8 0 0 0 3-.5" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
              </svg>
            </button>
            <button
              type="button"
              class="shrink-0 rounded-md p-1.5 text-gray-500 transition hover:bg-gray-200 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-gray-700 dark:hover:text-gray-200"
              v-tooltip:bottom="'复制'"
              aria-label="复制 API Key"
              @click="copyKey(key)"
            >
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
                <rect x="9" y="9" width="11" height="11" rx="2" stroke="currentColor" stroke-width="1.6" />
                <path d="M5 15V5a2 2 0 0 1 2-2h8" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" />
              </svg>
            </button>
          </div>

          <!-- 元信息：有效期 + 创建时间 -->
          <dl class="mb-4 space-y-1.5 text-xs">
            <div class="flex items-center justify-between gap-2">
              <dt class="text-gray-400">有效期</dt>
              <dd>
                <span v-if="key.expiresAt === undefined" class="text-gray-500 dark:text-gray-400">永不过期</span>
                <span
                  v-else
                  :class="isExpired(key) ? 'text-error-600 dark:text-error-400' : 'text-gray-600 dark:text-gray-300'"
                >
                  {{ isExpired(key) ? '已过期 · ' : '' }}{{ formatTime(key.expiresAt) }}
                </span>
              </dd>
            </div>
            <div class="flex items-center justify-between gap-2">
              <dt class="text-gray-400">创建时间</dt>
              <dd class="text-gray-600 dark:text-gray-300">{{ formatTime(key.createdAt) }}</dd>
            </div>
          </dl>

          <!-- 操作 -->
          <div class="mt-auto flex items-center justify-end gap-1.5 border-t border-gray-100 pt-3 dark:border-gray-800">
            <button
              type="button"
              class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-brand-600 hover:bg-brand-50 dark:text-brand-400 dark:hover:bg-brand-500/10"
              @click="configuring = key"
            >
              配置
            </button>
            <button
              type="button"
              class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-error-600 hover:bg-error-50 dark:text-error-400 dark:hover:bg-error-500/10"
              @click="askDelete(key)"
            >
              删除
            </button>
          </div>
        </div>
      </div>

      <!-- 分页 -->
      <div
        v-if="totalPages > 1"
        class="mt-4 flex items-center justify-between"
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

    <!-- 创建弹窗 -->
    <APIKeyCreateModal :open="createOpen" @close="createOpen = false" @created="onCreated" />

    <!-- 一次性明文密钥弹窗（Req 12.3） -->
    <PlaintextKeyModal :created="created" @close="created = null" />

    <!-- 配置弹窗（屏蔽规则 / ACL / 限流） -->
    <APIKeyConfigModal :apiKey="configuring" @close="configuring = null" />

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
            确定删除 API Key「{{ deleting.name }}」吗？该操作将级联清理其屏蔽规则与来源白名单，且不可恢复。
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
