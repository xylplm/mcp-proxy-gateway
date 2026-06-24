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
import { computed, ref, watch } from 'vue'
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
import { getAggregatedTools, type ToolDetail } from '@/api/tools'
import { getAPIKeyUsageProfile, type APIKeyToolUsage, type APIKeyUsageProfile } from '@/api/stats'
import { useConfirm } from '@/composables/useConfirm'
import { useToast } from '@/composables/useToast'
import {
  buildAPIKeyFilterPreview,
  type APIKeyFilterPreviewSummary,
} from '@/utils/apiKeyFilterPreview'
import { apiKeyLimitSummary } from '@/utils/apiKeyLimitSummary'

const props = defineProps<{
  /** 目标 API Key；为 null 时不渲染。 */
  apiKey: APIKey | null
}>()

const emit = defineEmits<{ (e: 'close'): void }>()
const { confirm } = useConfirm()
const toast = useToast()

/** 当前激活的分页签。 */
type Tab = 'profile' | 'filters' | 'acl' | 'ratelimit'
const activeTab = ref<Tab>('filters')

/** 通用提示与加载状态。 */
const errorMessage = ref('')

function showToast(msg: string): void {
  toast.success(msg)
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
const toolDetails = ref<ToolDetail[]>([])
const toolDetailsReady = ref(false)

const filterPreviewSummaries = computed<Record<string, APIKeyFilterPreviewSummary>>(() => {
  const summaries: Record<string, APIKeyFilterPreviewSummary> = {}
  for (const rule of filters.value) {
    summaries[rule.id] = buildFilterPreviewSummary(rule)
  }
  return summaries
})

const draftFilterPreviewSummary = computed<APIKeyFilterPreviewSummary>(() =>
  buildAPIKeyFilterPreview(
    {
      pattern: newFilterPattern.value,
      isRegex: newFilterIsRegex.value,
      enabled: true,
    },
    toolDetails.value,
    toolDetailsReady.value,
    {
      emptyLabel: '填写匹配模式后显示预计屏蔽影响',
      invalidPatternLabel: '正则暂不可用',
      hitLabel: (count) => `预计屏蔽 ${count} 个来源`,
    },
  ),
)

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

async function loadToolDetails(): Promise<void> {
  toolDetailsReady.value = false
  try {
    const result = await getAggregatedTools()
    toolDetails.value = result.toolDetails
    toolDetailsReady.value = true
  } catch {
    toolDetails.value = []
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
  const ok = await confirm({
    title: '确认删除',
    message: `确定删除屏蔽规则「${rule.pattern}」吗？该操作不可恢复。`,
    confirmText: '删除',
    tone: 'danger',
  })
  if (!ok) return
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
  const ok = await confirm({
    title: '确认删除',
    message: `确定删除来源白名单「${entry.CIDR}」吗？该操作不可恢复。`,
    confirmText: '删除',
    tone: 'danger',
  })
  if (!ok) return
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
const quotaPerDay = ref<number | null>(null)
const quotaPerMonth = ref<number | null>(null)
const rateLoading = ref(false)
const rateSaving = ref(false)

async function loadRateLimit(id: string): Promise<void> {
  rateLoading.value = true
  try {
    const cfg = await getRateLimit(id)
    rateLimit.value = cfg.rateLimit ?? null
    rateWindowS.value = cfg.rateWindowS ?? null
    quotaPerDay.value = cfg.quotaPerDay ?? null
    quotaPerMonth.value = cfg.quotaPerMonth ?? null
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
  const dayLimit = positiveNumberOrNull(quotaPerDay.value)
  const monthLimit = positiveNumberOrNull(quotaPerMonth.value)
  rateSaving.value = true
  try {
    const cfg = await updateRateLimit(props.apiKey.id, {
      rateLimit: bothSet ? limit : null,
      rateWindowS: bothSet ? windowS : null,
      quotaPerDay: dayLimit,
      quotaPerMonth: monthLimit,
    })
    rateLimit.value = cfg.rateLimit ?? null
    rateWindowS.value = cfg.rateWindowS ?? null
    quotaPerDay.value = cfg.quotaPerDay ?? null
    quotaPerMonth.value = cfg.quotaPerMonth ?? null
    showToast(apiKeyLimitSummary(cfg) === '无限制' ? '已清空调用限制' : '已保存调用限制')
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
  quotaPerDay.value = null
  quotaPerMonth.value = null
}

function positiveNumberOrNull(value: number | null): number | null {
  return value !== null && value > 0 ? value : null
}

// ── 使用画像 ──────────────────────────────────────────────────────────────
const usageProfile = ref<APIKeyUsageProfile | null>(null)
const profileLoading = ref(false)
let profileRequestSeq = 0

function emptyUsageProfile(apiKeyId = ''): APIKeyUsageProfile {
  return {
    APIKeyID: apiKeyId,
    TotalCalls: 0,
    SuccessCalls: 0,
    FailureCalls: 0,
    UniqueTools: 0,
    AvgLatencyMS: 0,
    P95LatencyMS: 0,
    TopTools: [],
  }
}

const profileStats = computed(() => usageProfile.value ?? emptyUsageProfile(props.apiKey?.id ?? ''))
const profileTopTools = computed(() => usageProfile.value?.TopTools ?? [])
const profileSuccessRate = computed(() => {
  const total = profileStats.value.TotalCalls
  return total === 0 ? 0 : (profileStats.value.SuccessCalls / total) * 100
})

function toolUsageLabel(item: APIKeyToolUsage): string {
  const detail = toolDetails.value.find(
    (entry) =>
      entry.tool.upstreamId === item.UpstreamID && entry.tool.originalName === item.OriginalName,
  )
  if (detail?.tool.name !== undefined && detail.tool.name !== '') {
    return detail.tool.name
  }
  return item.OriginalName || '(未知工具)'
}

function toolUsageSource(item: APIKeyToolUsage): string {
  const detail = toolDetails.value.find(
    (entry) =>
      entry.tool.upstreamId === item.UpstreamID && entry.tool.originalName === item.OriginalName,
  )
  const source = detail?.sources?.find(
    (entry) => entry.upstreamId === item.UpstreamID && entry.originalName === item.OriginalName,
  )
  return source?.upstreamName ?? item.UpstreamID
}

function formatInt(value: number): string {
  return Math.max(0, Math.round(value)).toLocaleString('zh-CN')
}

function formatPercent(value: number): string {
  return `${value.toFixed(1)}%`
}

function formatLatency(value: number): string {
  return `${Math.max(0, Math.round(value)).toLocaleString('zh-CN')} ms`
}

function formatDateTime(value?: string): string {
  if (value === undefined || value === '') return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime()) || date.getUTCFullYear() <= 1) return '-'
  return date.toLocaleString('zh-CN', { hour12: false })
}

async function loadUsageProfile(id: string): Promise<void> {
  const seq = ++profileRequestSeq
  profileLoading.value = true
  try {
    const profile = await getAPIKeyUsageProfile(id, {}, 10)
    if (seq === profileRequestSeq) {
      usageProfile.value = profile
    }
  } catch (err) {
    if (seq === profileRequestSeq) {
      usageProfile.value = emptyUsageProfile(id)
      showError(err, '加载使用画像失败')
    }
  } finally {
    if (seq === profileRequestSeq) {
      profileLoading.value = false
    }
  }
}

// ── 打开时加载全部从属配置 ────────────────────────────────────────────────
watch(
  () => props.apiKey,
  (key) => {
    if (key === null) {
      profileRequestSeq += 1
      usageProfile.value = null
      profileLoading.value = false
      return
    }
    activeTab.value = 'filters'
    errorMessage.value = ''
    newFilterPattern.value = ''
    newFilterIsRegex.value = false
    newCidr.value = ''
    usageProfile.value = emptyUsageProfile(key.id)
    void loadFilters(key.id)
    void loadToolDetails()
    void loadACL(key.id)
    void loadRateLimit(key.id)
    void loadUsageProfile(key.id)
  },
  { immediate: true },
)

function filterPreviewSummary(rule: APIKeyFilter): APIKeyFilterPreviewSummary {
  return filterPreviewSummaries.value[rule.id] ?? buildFilterPreviewSummary(rule)
}

function buildFilterPreviewSummary(rule: APIKeyFilter): APIKeyFilterPreviewSummary {
  return buildAPIKeyFilterPreview(rule, toolDetails.value, toolDetailsReady.value)
}
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
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">配置 API Key</h3>
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
              <path
                d="M18 6 6 18M6 6l12 12"
                stroke="currentColor"
                stroke-width="1.8"
                stroke-linecap="round"
              />
            </svg>
          </button>
        </div>

        <!-- 分页签 -->
        <div class="flex gap-1 overflow-x-auto border-b border-gray-200 px-6 dark:border-gray-800">
          <button
            v-for="tab in [
              { key: 'profile', label: '使用画像' },
              { key: 'filters', label: '屏蔽规则' },
              { key: 'acl', label: '来源白名单' },
              { key: 'ratelimit', label: '限流配置' },
            ]"
            :key="tab.key"
            type="button"
            class="shrink-0 border-b-2 px-3 py-3 text-sm font-medium transition"
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
            class="bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400 mb-3 rounded-lg px-4 py-2.5 text-sm"
          >
            {{ errorMessage }}
          </p>
        </div>

        <!-- 内容区 -->
        <div class="flex-1 overflow-y-auto px-6 pb-6">
          <!-- 使用画像 -->
          <section v-if="activeTab === 'profile'">
            <div
              v-if="profileLoading"
              class="rounded-xl border border-gray-200 px-4 py-8 text-center text-sm text-gray-400 dark:border-gray-800"
            >
              加载中…
            </div>
            <div v-else class="space-y-4">
              <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
                <div
                  class="rounded-xl border border-gray-200 bg-white p-3 dark:border-gray-800 dark:bg-white/[0.03]"
                >
                  <p class="text-xs text-gray-500 dark:text-gray-400">7 天调用</p>
                  <p class="mt-1 text-lg font-semibold text-gray-800 dark:text-white/90">
                    {{ formatInt(profileStats.TotalCalls) }}
                  </p>
                </div>
                <div
                  class="rounded-xl border border-gray-200 bg-white p-3 dark:border-gray-800 dark:bg-white/[0.03]"
                >
                  <p class="text-xs text-gray-500 dark:text-gray-400">成功率</p>
                  <p class="text-success-600 dark:text-success-400 mt-1 text-lg font-semibold">
                    {{ formatPercent(profileSuccessRate) }}
                  </p>
                </div>
                <div
                  class="rounded-xl border border-gray-200 bg-white p-3 dark:border-gray-800 dark:bg-white/[0.03]"
                >
                  <p class="text-xs text-gray-500 dark:text-gray-400">失败</p>
                  <p class="text-error-600 dark:text-error-400 mt-1 text-lg font-semibold">
                    {{ formatInt(profileStats.FailureCalls) }}
                  </p>
                </div>
                <div
                  class="rounded-xl border border-gray-200 bg-white p-3 dark:border-gray-800 dark:bg-white/[0.03]"
                >
                  <p class="text-xs text-gray-500 dark:text-gray-400">覆盖工具</p>
                  <p class="mt-1 text-lg font-semibold text-gray-800 dark:text-white/90">
                    {{ formatInt(profileStats.UniqueTools) }}
                  </p>
                </div>
              </div>

              <div class="grid grid-cols-1 gap-3 sm:grid-cols-3">
                <div
                  class="rounded-xl border border-gray-200 bg-white p-3 dark:border-gray-800 dark:bg-white/[0.03]"
                >
                  <p class="text-xs text-gray-500 dark:text-gray-400">最近调用</p>
                  <p class="mt-1 truncate text-sm font-medium text-gray-700 dark:text-gray-200">
                    {{ formatDateTime(profileStats.LastCalledAt) }}
                  </p>
                </div>
                <div
                  class="rounded-xl border border-gray-200 bg-white p-3 dark:border-gray-800 dark:bg-white/[0.03]"
                >
                  <p class="text-xs text-gray-500 dark:text-gray-400">平均耗时</p>
                  <p class="mt-1 text-sm font-medium text-gray-700 dark:text-gray-200">
                    {{ formatLatency(profileStats.AvgLatencyMS) }}
                  </p>
                </div>
                <div
                  class="rounded-xl border border-gray-200 bg-white p-3 dark:border-gray-800 dark:bg-white/[0.03]"
                >
                  <p class="text-xs text-gray-500 dark:text-gray-400">P95 耗时</p>
                  <p class="mt-1 text-sm font-medium text-gray-700 dark:text-gray-200">
                    {{ formatLatency(profileStats.P95LatencyMS) }}
                  </p>
                </div>
              </div>

              <div
                class="rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-white/[0.03]"
              >
                <div class="mb-3 flex items-center justify-between gap-3">
                  <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">常用工具</h4>
                  <span class="text-xs text-gray-400">近 7 天</span>
                </div>
                <div
                  v-if="profileTopTools.length === 0"
                  class="rounded-lg border border-dashed border-gray-300 px-4 py-8 text-center text-sm text-gray-400 dark:border-gray-700"
                >
                  暂无调用记录
                </div>
                <div v-else class="space-y-2">
                  <div
                    v-for="item in profileTopTools"
                    :key="`${item.UpstreamID}:${item.OriginalName}`"
                    class="flex items-center gap-3 rounded-lg border border-gray-100 px-3 py-2 dark:border-gray-800"
                  >
                    <div class="min-w-0 flex-1">
                      <p class="truncate text-sm font-medium text-gray-800 dark:text-white/90">
                        {{ toolUsageLabel(item) }}
                      </p>
                      <p class="mt-0.5 truncate text-xs text-gray-400">
                        {{ toolUsageSource(item) }} / {{ item.OriginalName }}
                      </p>
                    </div>
                    <span class="shrink-0 text-sm font-semibold text-gray-700 dark:text-gray-200">
                      {{ formatInt(item.Count) }}
                    </span>
                  </div>
                </div>
              </div>
            </div>
          </section>

          <!-- 屏蔽规则 -->
          <section v-else-if="activeTab === 'filters'">
            <div class="mb-4 flex flex-wrap items-end gap-2">
              <div class="min-w-[200px] flex-1">
                <label class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-400">
                  匹配模式
                </label>
                <input
                  v-model="newFilterPattern"
                  type="text"
                  maxlength="200"
                  placeholder="工具原始名称；正则模式如 ^admin\..+$"
                  class="focus:border-brand-400 focus:ring-brand-100 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-800 focus:ring-2 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
                  @keyup.enter="addFilter"
                />
              </div>
              <div class="flex items-center gap-2 pb-2 text-sm text-gray-600 dark:text-gray-300">
                <span>正则</span>
                <button
                  type="button"
                  role="switch"
                  :aria-checked="newFilterIsRegex"
                  class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition"
                  :class="newFilterIsRegex ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
                  @click="newFilterIsRegex = !newFilterIsRegex"
                >
                  <span
                    class="inline-block h-4 w-4 transform rounded-full bg-white transition"
                    :class="newFilterIsRegex ? 'translate-x-6' : 'translate-x-1'"
                  ></span>
                </button>
              </div>
              <button
                type="button"
                class="bg-brand-500 hover:bg-brand-600 rounded-lg px-3.5 py-2 text-sm font-medium text-white transition disabled:opacity-60"
                :disabled="filterBusy"
                @click="addFilter"
              >
                添加
              </button>
            </div>
            <div
              v-if="newFilterPattern.trim() !== ''"
              class="border-warning-200 bg-warning-50 text-warning-700 dark:border-warning-500/20 dark:bg-warning-500/10 dark:text-warning-300 mb-4 rounded-lg border px-2.5 py-2 text-xs"
            >
              <div class="flex items-center justify-between gap-2">
                <span>{{ draftFilterPreviewSummary.label }}</span>
                <span
                  v-if="draftFilterPreviewSummary.hiddenCount > 0"
                  class="text-warning-600 dark:text-warning-300 shrink-0"
                >
                  +{{ draftFilterPreviewSummary.hiddenCount }}
                </span>
              </div>
              <div
                v-if="draftFilterPreviewSummary.items.length > 0"
                class="mt-2 flex flex-wrap gap-1.5"
              >
                <AppTooltip
                  v-for="item in draftFilterPreviewSummary.items"
                  :key="item.key"
                  :content="`${item.upstreamName} / ${item.originalName}`"
                  placement="bottom"
                >
                  <span
                    class="text-warning-700 ring-warning-200 dark:text-warning-200 dark:ring-warning-500/20 inline-flex max-w-full items-center rounded-md bg-white px-1.5 py-0.5 text-[11px] ring-1 dark:bg-white/5"
                  >
                    <span class="truncate">{{ item.exposedName }} / {{ item.originalName }}</span>
                  </span>
                </AppTooltip>
              </div>
            </div>

            <div
              v-if="filtersLoading"
              class="rounded-xl border border-gray-200 px-4 py-8 text-center text-sm text-gray-400 dark:border-gray-800"
            >
              加载中…
            </div>
            <div
              v-else-if="filters.length === 0"
              class="rounded-xl border border-dashed border-gray-300 px-4 py-8 text-center text-sm text-gray-400 dark:border-gray-700"
            >
              暂无屏蔽规则
            </div>
            <div v-else class="space-y-2">
              <div
                v-for="rule in filters"
                :key="rule.id"
                class="rounded-xl border border-gray-200 bg-white p-3 dark:border-gray-800 dark:bg-white/[0.03]"
              >
                <div class="flex items-center gap-3">
                  <AppTooltip
                    :content="rule.pattern"
                    placement="bottom-start"
                    class="min-w-0 flex-1"
                  >
                    <code
                      class="block truncate font-mono text-xs text-gray-700 dark:text-gray-300"
                      >{{ rule.pattern }}</code
                    >
                  </AppTooltip>
                  <span
                    class="inline-flex shrink-0 items-center rounded-full px-2 py-0.5 text-xs"
                    :class="
                      rule.isRegex
                        ? 'bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400'
                        : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'
                    "
                  >
                    {{ rule.isRegex ? '正则' : '精确' }}
                  </span>
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
                  <button
                    type="button"
                    class="text-error-600 hover:bg-error-50 dark:text-error-400 dark:hover:bg-error-500/10 shrink-0 rounded-lg px-2.5 py-1.5 text-xs font-medium"
                    @click="removeFilter(rule)"
                  >
                    删除
                  </button>
                </div>
                <div
                  class="border-warning-200 bg-warning-50 text-warning-700 dark:border-warning-500/20 dark:bg-warning-500/10 dark:text-warning-300 mt-2 rounded-lg border px-2.5 py-2 text-xs"
                >
                  <div class="flex items-center justify-between gap-2">
                    <span>{{ filterPreviewSummary(rule).label }}</span>
                    <span
                      v-if="filterPreviewSummary(rule).hiddenCount > 0"
                      class="text-warning-600 dark:text-warning-300 shrink-0"
                    >
                      +{{ filterPreviewSummary(rule).hiddenCount }}
                    </span>
                  </div>
                  <div
                    v-if="filterPreviewSummary(rule).items.length > 0"
                    class="mt-2 flex flex-wrap gap-1.5"
                  >
                    <AppTooltip
                      v-for="item in filterPreviewSummary(rule).items"
                      :key="item.key"
                      :content="`${item.upstreamName} / ${item.originalName}`"
                      placement="bottom"
                    >
                      <span
                        class="text-warning-700 ring-warning-200 dark:text-warning-200 dark:ring-warning-500/20 inline-flex max-w-full items-center rounded-md bg-white px-1.5 py-0.5 text-[11px] ring-1 dark:bg-white/5"
                      >
                        <span class="truncate"
                          >{{ item.exposedName }} / {{ item.originalName }}</span
                        >
                      </span>
                    </AppTooltip>
                  </div>
                </div>
              </div>
            </div>
          </section>

          <!-- 来源白名单 -->
          <section v-else-if="activeTab === 'acl'">
            <div class="mb-4 flex flex-wrap items-end gap-2">
              <div class="min-w-[200px] flex-1">
                <label class="mb-1.5 block text-xs font-medium text-gray-600 dark:text-gray-400">
                  IP / CIDR 网段
                </label>
                <input
                  v-model="newCidr"
                  type="text"
                  placeholder="如：10.0.0.0/8 或 1.2.3.4"
                  class="focus:border-brand-400 focus:ring-brand-100 w-full rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-800 focus:ring-2 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
                  @keyup.enter="addACL"
                />
              </div>
              <button
                type="button"
                class="bg-brand-500 hover:bg-brand-600 rounded-lg px-3.5 py-2 text-sm font-medium text-white transition disabled:opacity-60"
                :disabled="aclBusy"
                @click="addACL"
              >
                添加
              </button>
            </div>

            <p class="mb-3 text-xs text-gray-400">
              留空表示不限制来源；配置后仅白名单内的来源 IP 可使用该 API Key。
            </p>

            <div
              v-if="aclLoading"
              class="rounded-xl border border-gray-200 px-4 py-8 text-center text-sm text-gray-400 dark:border-gray-800"
            >
              加载中…
            </div>
            <div
              v-else-if="aclEntries.length === 0"
              class="rounded-xl border border-dashed border-gray-300 px-4 py-8 text-center text-sm text-gray-400 dark:border-gray-700"
            >
              暂无来源白名单（不限制来源）
            </div>
            <div v-else class="space-y-2">
              <div
                v-for="entry in aclEntries"
                :key="entry.ID"
                class="flex items-center gap-3 rounded-xl border border-gray-200 bg-white p-3 dark:border-gray-800 dark:bg-white/[0.03]"
              >
                <code
                  class="min-w-0 flex-1 truncate font-mono text-xs text-gray-700 dark:text-gray-300"
                  >{{ entry.CIDR }}</code
                >
                <button
                  type="button"
                  class="text-error-600 hover:bg-error-50 dark:text-error-400 dark:hover:bg-error-500/10 shrink-0 rounded-lg px-2.5 py-1.5 text-xs font-medium"
                  @click="removeACL(entry)"
                >
                  删除
                </button>
              </div>
            </div>
          </section>

          <!-- 限流配置 -->
          <section v-else-if="activeTab === 'ratelimit'">
            <div v-if="rateLoading" class="px-1 py-8 text-center text-sm text-gray-400">
              加载中…
            </div>
            <div v-else class="max-w-2xl">
              <div class="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <div>
                  <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
                    请求上限（次）
                  </label>
                  <input
                    v-model.number="rateLimit"
                    type="number"
                    min="0"
                    placeholder="留空表示不限流"
                    class="focus:border-brand-400 focus:ring-brand-100 w-full rounded-lg border border-gray-300 px-3.5 py-2.5 text-sm text-gray-800 focus:ring-2 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
                  />
                </div>
                <div>
                  <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
                    计数窗口（秒）
                  </label>
                  <input
                    v-model.number="rateWindowS"
                    type="number"
                    min="0"
                    placeholder="留空表示不限流"
                    class="focus:border-brand-400 focus:ring-brand-100 w-full rounded-lg border border-gray-300 px-3.5 py-2.5 text-sm text-gray-800 focus:ring-2 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
                  />
                </div>
                <div>
                  <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
                    每日调用上限
                  </label>
                  <input
                    v-model.number="quotaPerDay"
                    type="number"
                    min="0"
                    placeholder="留空表示不限额"
                    class="focus:border-brand-400 focus:ring-brand-100 w-full rounded-lg border border-gray-300 px-3.5 py-2.5 text-sm text-gray-800 focus:ring-2 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
                  />
                </div>
                <div>
                  <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">
                    每月调用上限
                  </label>
                  <input
                    v-model.number="quotaPerMonth"
                    type="number"
                    min="0"
                    placeholder="留空表示不限额"
                    class="focus:border-brand-400 focus:ring-brand-100 w-full rounded-lg border border-gray-300 px-3.5 py-2.5 text-sm text-gray-800 focus:ring-2 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
                  />
                </div>
              </div>
              <p class="my-5 text-xs text-gray-400">
                请求上限与计数窗口须同时为正整数才生效；每日和每月上限独立生效。
              </p>
              <div class="flex items-center gap-3">
                <button
                  type="button"
                  class="bg-brand-500 hover:bg-brand-600 rounded-lg px-4 py-2 text-sm font-medium text-white transition disabled:opacity-60"
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
                  清空全部限制
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
