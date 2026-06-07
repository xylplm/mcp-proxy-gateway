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
import {
  listAPIKeys,
  setAPIKeyEnabled,
  deleteAPIKey,
  type APIKey,
  type CreatedAPIKey,
} from '@/api/apikeys'

const { pageSize } = useBreakpoint()

/** 全量 API Key 列表（按创建时间倒序）。 */
const apiKeys = ref<APIKey[]>([])
const loading = ref(false)
const errorMessage = ref('')
/** 行级操作进行中标记（key = id:action）。 */
const busy = ref<Set<string>>(new Set())
const toast = ref('')

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

const currentPageTitle = ref('API Key 管理')

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
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb :pageTitle="currentPageTitle" />

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

    <!-- 列表卡片 -->
    <div class="rounded-2xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-white/[0.03]">
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
              <th class="px-5 py-3.5">前缀</th>
              <th class="px-5 py-3.5">启用</th>
              <th class="px-5 py-3.5">有效期</th>
              <th class="px-5 py-3.5">创建时间</th>
              <th class="px-5 py-3.5 text-right">操作</th>
            </tr>
          </thead>
          <tbody class="divide-y divide-gray-100 dark:divide-gray-800">
            <tr v-if="loading">
              <td colspan="6" class="px-5 py-10 text-center text-sm text-gray-400">加载中…</td>
            </tr>
            <tr v-else-if="apiKeys.length === 0">
              <td colspan="6" class="px-5 py-10 text-center text-sm text-gray-400">
                暂无 API Key，点击「新建 API Key」开始创建
              </td>
            </tr>
            <tr
              v-for="key in pagedKeys"
              v-else
              :key="key.id"
              class="text-sm text-gray-700 dark:text-gray-300"
            >
              <!-- 名称 -->
              <td class="px-5 py-4">
                <div class="font-medium text-gray-800 dark:text-white/90">{{ key.name }}</div>
                <div v-if="key.rateLimit && key.rateWindowS" class="mt-0.5 text-xs text-gray-400">
                  限流 {{ key.rateLimit }} 次 / {{ key.rateWindowS }} 秒
                </div>
              </td>
              <!-- 前缀 -->
              <td class="px-5 py-4">
                <span class="font-mono text-xs text-gray-500 dark:text-gray-400">
                  {{ key.keyPrefix }}…
                </span>
              </td>
              <!-- 启用开关 -->
              <td class="px-5 py-4">
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
              </td>
              <!-- 有效期 -->
              <td class="px-5 py-4">
                <span v-if="key.expiresAt === undefined" class="text-xs text-gray-400">永不过期</span>
                <span
                  v-else
                  class="inline-flex items-center rounded-full px-2.5 py-1 text-xs"
                  :class="
                    isExpired(key)
                      ? 'bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400'
                      : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'
                  "
                >
                  {{ isExpired(key) ? '已过期 · ' : '' }}{{ formatTime(key.expiresAt) }}
                </span>
              </td>
              <!-- 创建时间 -->
              <td class="px-5 py-4 text-xs text-gray-500 dark:text-gray-400">
                {{ formatTime(key.createdAt) }}
              </td>
              <!-- 操作 -->
              <td class="px-5 py-4">
                <div class="flex items-center justify-end gap-1.5">
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
