<script setup lang="ts">
/**
 * API Key 配置弹窗（任务 26.3）。
 *
 * 集中管理单个 API Key 的：
 * - 屏蔽规则（Req 13.1）：列出/创建/启停/删除，字段含 pattern / isRegex / enabled；
 * - 来源白名单 ACL（Req 13.9）：列出/新增/删除 CIDR；
 * - 限流配置（Req 21）：rateLimit + rateWindowS，清空二者即禁用限流。
 *
 * 有效期/启停等元数据在列表页直接展示与操作，本弹窗聚焦上述三类从属配置。
 */
import { ref, watch } from 'vue'
import {
  listAPIKeyFilters,
  createAPIKeyFilter,
  setAPIKeyFilterEnabled,
  deleteAPIKeyFilter,
  listACL,
  createACL,
  deleteACL,
  getRateLimit,
  updateRateLimit,
  type APIKey,
  type APIKeyFilter,
  type ACLEntry,
} from '@/api/apikeys'

const props = defineProps<{
  /** 目标 API Key；为 null 时不渲染。 */
  apiKey: APIKey | null
}>()

const emit = defineEmits<{ (e: 'close'): void }>()

/** 当前激活的分页签。 */
type Tab = 'filters' | 'acl' | 'ratelimit'
const activeTab = ref<Tab>('filters')

/** 通用提示与加载状态。 */
const toast = ref('')
const errorMessage = ref('')

function showToast(msg: string): void {
  toast.value = msg
  setTimeout(() => {
    if (toast.value === msg) toast.value = ''
  }, 2500)
}

function showError(err: unknown, fallback: string): void {
  errorMessage.value = err instanceof Error ? err.message : fallback
  setTimeout(() => {
    errorMessage.value = ''
  }, 4000)
}

// ── 屏蔽规则 ──────────────────────────────────────────────────────────────
const filters = ref<APIKeyFilter[]>([])
const filtersLoading = ref(false)
const newFilterPattern = ref('')
const newFilterIsRegex = ref(false)
const filterBusy = ref(false)

async function loadFilters(id: string): Promise<void> {
  filtersLoading.value = true
  try {
    filters.value = await listAPIKeyFilters(id)
  } catch (err) {
    showError(err, '加载屏蔽规则失败')
  } finally {
    filtersLoading.value = false
  }
}

async function addFilter(): Promise<void> {
  if (props.apiKey === null) return
  const pattern = newFilterPattern.value.trim()
  if (pattern.length < 1 || pattern.length > 200) {
    showError(null, '匹配模式长度需在 1 至 200 个字符之间')
    return
  }
  filterBusy.value = true
  try {
    await createAPIKeyFilter(props.apiKey.id, {
      pattern,
      isRegex: newFilterIsRegex.value,
      enabled: true,
    })
    newFilterPattern.value = ''
    newFilterIsRegex.value = false
    await loadFilters(props.apiKey.id)
    showToast('已添加屏蔽规则')
  } catch (err) {
    showError(err, '添加屏蔽规则失败')
  } finally {
    filterBusy.value = false
  }
}

async function toggleFilter(rule: APIKeyFilter): Promise<void> {
  if (props.apiKey === null) return
  try {
    await setAPIKeyFilterEnabled(rule.id, !rule.enabled)
    await loadFilters(props.apiKey.id)
  } catch (err) {
    showError(err, '操作失败')
  }
}

async function removeFilter(rule: APIKeyFilter): Promise<void> {
  if (props.apiKey === null) return
  try {
    await deleteAPIKeyFilter(rule.id)
    await loadFilters(props.apiKey.id)
    showToast('已删除屏蔽规则')
  } catch (err) {
    showError(err, '删除屏蔽规则失败')
  }
}

// ── 来源白名单 ACL ────────────────────────────────────────────────────────
const aclEntries = ref<ACLEntry[]>([])
const aclLoading = ref(false)
const newCidr = ref('')
const aclBusy = ref(false)

async function loadACL(id: string): Promise<void> {
  aclLoading.value = true
  try {
    aclEntries.value = await listACL(id)
  } catch (err) {
    showError(err, '加载来源白名单失败')
  } finally {
    aclLoading.value = false
  }
}

async function addACL(): Promise<void> {
  if (props.apiKey === null) return
  const cidr = newCidr.value.trim()
  if (cidr === '') {
    showError(null, '请输入 IP 或 CIDR 网段')
    return
  }
  aclBusy.value = true
  try {
    await createACL(props.apiKey.id, { cidr })
    newCidr.value = ''
    await loadACL(props.apiKey.id)
    showToast('已添加来源白名单')
  } catch (err) {
    showError(err, '添加来源白名单失败')
  } finally {
    aclBusy.value = false
  }
}

async function removeACL(entry: ACLEntry): Promise<void> {
  if (props.apiKey === null) return
  try {
    await deleteACL(entry.ID)
    await loadACL(props.apiKey.id)
    showToast('已删除来源白名单')
  } catch (err) {
    showError(err, '删除来源白名单失败')
  }
}

// ── 限流配置 ──────────────────────────────────────────────────────────────
const rateLimit = ref<number | null>(null)
const rateWindowS = ref<number | null>(null)
const rateLoading = ref(false)
const rateSaving = ref(false)

async function loadRateLimit(id: string): Promise<void> {
  rateLoading.value = true
  try {
    const cfg = await getRateLimit(id)
    rateLimit.value = cfg.rateLimit ?? null
    rateWindowS.value = cfg.rateWindowS ?? null
  } catch (err) {
    showError(err, '加载限流配置失败')
  } finally {
    rateLoading.value = false
  }
}

async function saveRateLimit(): Promise<void> {
  if (props.apiKey === null) return
  // 两字段须同时为正才生效（Req 21.4）；任一缺失即视为禁用，提交 null 清除。
  const limit = rateLimit.value
  const windowS = rateWindowS.value
  const bothSet = limit !== null && limit > 0 && windowS !== null && windowS > 0
  rateSaving.value = true
  try {
    const cfg = await updateRateLimit(props.apiKey.id, {
      rateLimit: bothSet ? limit : null,
      rateWindowS: bothSet ? windowS : null,
    })
    rateLimit.value = cfg.rateLimit ?? null
    rateWindowS.value = cfg.rateWindowS ?? null
    showToast(bothSet ? '已保存限流配置' : '已禁用限流')
  } catch (err) {
    showError(err, '保存限流配置失败')
  } finally {
    rateSaving.value = false
  }
}

/** 清空限流字段（保存后即禁用限流）。 */
function clearRateLimit(): void {
  rateLimit.value = null
  rateWindowS.value = null
}

// ── 打开时加载全部从属配置 ────────────────────────────────────────────────
watch(
  () => props.apiKey,
  (key) => {
    if (key === null) return
    activeTab.value = 'filters'
    toast.value = ''
    errorMessage.value = ''
    newFilterPattern.value = ''
    newFilterIsRegex.value = false
    newCidr.value = ''
    void loadFilters(key.id)
    void loadACL(key.id)
    void loadRateLimit(key.id)
  },
  { immediate: true },
)
</script>

<template>
  <transition name="fade">
    <div
      v-if="apiKey !== null"
      class="fixed inset-0 z-[100001] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
      @click.self="emit('close')"
    >
      <div
        class="flex max-h-[90vh] w-full max-w-2xl flex-col rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-gray-800 dark:bg-gray-900"
      >
        <!-- 头部 -->
        <div
          class="flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-gray-800"
        >
          <div>
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">
              配置 API Key
            </h3>
            <p class="mt-0.5 text-sm text-gray-500 dark:text-gray-400">
              {{ apiKey.name }}
              <span class="ml-1 font-mono text-xs text-gray-400">{{ apiKey.keyPrefix }}…</span>
            </p>
          </div>
          <button
            type="button"
            class="rounded-lg p-1.5 text-gray-400 hover:bg-gray-100 dark:hover:bg-gray-800"
            aria-label="关闭"
            @click="emit('close')"
          >
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none">
              <path d="M18 6 6 18M6 6l12 12" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
            </svg>
          </button>
        </div>

        <!-- 分页签 -->
        <div class="flex gap-1 border-b border-gray-200 px-6 dark:border-gray-800">
          <button
            v-for="tab in [
              { key: 'filters', label: '屏蔽规则' },
              { key: 'acl', label: '来源白名单' },
              { key: 'ratelimit', label: '限流配置' },
            ]"
            :key="tab.key"
            type="button"
            class="border-b-2 px-3 py-3 text-sm font-medium transition"
            :class="
              activeTab === tab.key
                ? 'border-brand-500 text-brand-600 dark:text-brand-400'
                : 'border-transparent text-gray-500 hover:text-gray-700 dark:text-gray-400 dark:hover:text-gray-200'
            "
            @click="activeTab = tab.key as Tab"
          >
            {{ tab.label }}
          </button>
        </div>

        <!-- 提示 -->
        <div class="px-6 pt-4">
          <p
            v-if="errorMessage !== ''"
            class="mb-3 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
          >
            {{ errorMessage }}
          </p>
          <p
            v-if="toast !== ''"
            class="mb-3 rounded-lg bg-success-50 px-4 py-2.5 text-sm text-success-700 dark:bg-success-500/10 dark:text-success-400"
          >
            {{ toast }}
          </p>
        </div>

        <!-- 内容区 -->
        <div class="flex-1 overflow-y-auto px-6 pb-6">
          <!-- 屏蔽规则 -->
          <section v-if="activeTab === 'filters'">
            <div class="mb-4 flex flex-wrap items-end gap-2">
              <div class="flex-1 min-w-[200px]">
                <label class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-400">
                  匹配模式
                </label>
                <input
                  v-model="newFilterPattern"
                  type="text"
                  maxlength="200"
                  placeholder="如：secret_* 或 ^admin\..+$"
                  class="w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-800 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
                  @keyup.enter="addFilter"
                />
              </div>
              <label class="flex items-center gap-2 pb-2 text-sm text-gray-600 dark:text-gray-300">
                <input
                  v-model="newFilterIsRegex"
                  type="checkbox"
                  class="h-4 w-4 rounded border-gray-300 text-brand-500 focus:ring-brand-400 dark:border-gray-600 dark:bg-gray-800"
                />
                正则
              </label>
              <button
                type="button"
                class="rounded-lg bg-brand-500 px-3.5 py-2 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-60"
                :disabled="filterBusy"
                @click="addFilter"
              >
                添加
              </button>
            </div>

            <div class="overflow-hidden rounded-xl border border-gray-200 dark:border-gray-800">
              <table class="min-w-full">
                <thead>
                  <tr class="border-b border-gray-200 text-left text-xs font-medium text-gray-500 uppercase dark:border-gray-800 dark:text-gray-400">
                    <th class="px-4 py-3">匹配模式</th>
                    <th class="px-4 py-3">类型</th>
                    <th class="px-4 py-3">启用</th>
                    <th class="px-4 py-3 text-right">操作</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-gray-100 dark:divide-gray-800">
                  <tr v-if="filtersLoading">
                    <td colspan="4" class="px-4 py-8 text-center text-sm text-gray-400">加载中…</td>
                  </tr>
                  <tr v-else-if="filters.length === 0">
                    <td colspan="4" class="px-4 py-8 text-center text-sm text-gray-400">
                      暂无屏蔽规则
                    </td>
                  </tr>
                  <tr
                    v-for="rule in filters"
                    v-else
                    :key="rule.id"
                    class="text-sm text-gray-700 dark:text-gray-300"
                  >
                    <td class="px-4 py-3 font-mono text-xs break-all">{{ rule.pattern }}</td>
                    <td class="px-4 py-3">
                      <span
                        class="inline-flex items-center rounded-full px-2 py-0.5 text-xs"
                        :class="
                          rule.isRegex
                            ? 'bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400'
                            : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'
                        "
                      >
                        {{ rule.isRegex ? '正则' : '通配' }}
                      </span>
                    </td>
                    <td class="px-4 py-3">
                      <button
                        type="button"
                        role="switch"
                        :aria-checked="rule.enabled"
                        class="relative inline-flex h-5 w-9 shrink-0 items-center rounded-full transition"
                        :class="rule.enabled ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
                        @click="toggleFilter(rule)"
                      >
                        <span
                          class="inline-block h-3.5 w-3.5 transform rounded-full bg-white transition"
                          :class="rule.enabled ? 'translate-x-5' : 'translate-x-1'"
                        ></span>
                      </button>
                    </td>
                    <td class="px-4 py-3 text-right">
                      <button
                        type="button"
                        class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-error-600 hover:bg-error-50 dark:text-error-400 dark:hover:bg-error-500/10"
                        @click="removeFilter(rule)"
                      >
                        删除
                      </button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>

          <!-- 来源白名单 -->
          <section v-else-if="activeTab === 'acl'">
            <div class="mb-4 flex flex-wrap items-end gap-2">
              <div class="flex-1 min-w-[200px]">
                <label class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-400">
                  IP / CIDR 网段
                </label>
                <input
                  v-model="newCidr"
                  type="text"
                  placeholder="如：10.0.0.0/8 或 1.2.3.4"
                  class="w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-800 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
                  @keyup.enter="addACL"
                />
              </div>
              <button
                type="button"
                class="rounded-lg bg-brand-500 px-3.5 py-2 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-60"
                :disabled="aclBusy"
                @click="addACL"
              >
                添加
              </button>
            </div>

            <p class="mb-3 text-xs text-gray-400">
              留空表示不限制来源；配置后仅白名单内的来源 IP 可使用该 API Key。
            </p>

            <div class="overflow-hidden rounded-xl border border-gray-200 dark:border-gray-800">
              <table class="min-w-full">
                <thead>
                  <tr class="border-b border-gray-200 text-left text-xs font-medium text-gray-500 uppercase dark:border-gray-800 dark:text-gray-400">
                    <th class="px-4 py-3">CIDR</th>
                    <th class="px-4 py-3 text-right">操作</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-gray-100 dark:divide-gray-800">
                  <tr v-if="aclLoading">
                    <td colspan="2" class="px-4 py-8 text-center text-sm text-gray-400">加载中…</td>
                  </tr>
                  <tr v-else-if="aclEntries.length === 0">
                    <td colspan="2" class="px-4 py-8 text-center text-sm text-gray-400">
                      暂无来源白名单（不限制来源）
                    </td>
                  </tr>
                  <tr
                    v-for="entry in aclEntries"
                    v-else
                    :key="entry.ID"
                    class="text-sm text-gray-700 dark:text-gray-300"
                  >
                    <td class="px-4 py-3 font-mono text-xs">{{ entry.CIDR }}</td>
                    <td class="px-4 py-3 text-right">
                      <button
                        type="button"
                        class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-error-600 hover:bg-error-50 dark:text-error-400 dark:hover:bg-error-500/10"
                        @click="removeACL(entry)"
                      >
                        删除
                      </button>
                    </td>
                  </tr>
                </tbody>
              </table>
            </div>
          </section>

          <!-- 限流配置 -->
          <section v-else>
            <div v-if="rateLoading" class="px-1 py-8 text-center text-sm text-gray-400">
              加载中…
            </div>
            <div v-else class="max-w-md">
              <div class="mb-4">
                <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
                  请求上限（次）
                </label>
                <input
                  v-model.number="rateLimit"
                  type="number"
                  min="0"
                  placeholder="留空表示不限流"
                  class="w-full rounded-lg border border-gray-300 px-3.5 py-2.5 text-sm text-gray-800 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
                />
              </div>
              <div class="mb-3">
                <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
                  计数窗口（秒）
                </label>
                <input
                  v-model.number="rateWindowS"
                  type="number"
                  min="0"
                  placeholder="留空表示不限流"
                  class="w-full rounded-lg border border-gray-300 px-3.5 py-2.5 text-sm text-gray-800 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
                />
              </div>
              <p class="mb-5 text-xs text-gray-400">
                请求上限与计数窗口须同时为正整数才生效；清空任一字段并保存即禁用限流。
              </p>
              <div class="flex items-center gap-3">
                <button
                  type="button"
                  class="rounded-lg bg-brand-500 px-4 py-2 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-60"
                  :disabled="rateSaving"
                  @click="saveRateLimit"
                >
                  {{ rateSaving ? '保存中…' : '保存' }}
                </button>
                <button
                  type="button"
                  class="rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                  @click="clearRateLimit"
                >
                  清空（禁用限流）
                </button>
              </div>
            </div>
          </section>
        </div>
      </div>
    </div>
  </transition>
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
