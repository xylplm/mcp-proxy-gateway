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
  setAPIKeyRiskProfile,
  getAPIKeyRiskImpact,
  getAPIKeyUpstreamAccess,
  updateAPIKeyUpstreamAccess,
  type APIKey,
  type APIKeyFilter,
  type ACLEntry,
  type UpstreamAccessMode,
} from '@/api/apikeys'
import type { RiskProfile } from '@/api/aiRisk'
import { riskProfileDescription, riskProfileLabel } from '@/utils/riskLevel'
import AppSelect from '@/components/common/AppSelect.vue'
import { getAggregatedTools, type ToolDetail } from '@/api/tools'
import { CONN_STATE_LABELS, listUpstreams, type Upstream } from '@/api/upstreams'
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
type Tab = 'profile' | 'risk' | 'upstreams' | 'filters' | 'acl' | 'ratelimit'
const activeTab = ref<Tab>('filters')

/** 通用提示与加载状态。 */
const errorMessage = ref('')

// ── 竞态保护：每次 apiKey 变化时递增，各 load 函数在写入响应式状态前比对序号。 ─────
let loadSeq = 0

function showToast(msg: string): void {
  toast.success(msg)
}

function showError(err: unknown, fallback: string, seq?: number): void {
  // 若传入了 seq，只在序号仍有效时显示错误，避免旧请求污染当前 tab。
  if (seq !== undefined && seq !== loadSeq) return
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

async function loadFilters(id: string, seq: number): Promise<void> {
  filtersLoading.value = true
  try {
    const data = await listAPIKeyFilters(id)
    if (seq !== loadSeq) return
    filters.value = data
  } catch (err) {
    showError(err, '加载屏蔽规则失败', seq)
  } finally {
    if (seq === loadSeq) filtersLoading.value = false
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
    await loadFilters(props.apiKey.id, loadSeq)
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
    await loadFilters(props.apiKey.id, loadSeq)
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
    await loadFilters(props.apiKey.id, loadSeq)
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

async function loadACL(id: string, seq: number): Promise<void> {
  aclLoading.value = true
  try {
    const data = await listACL(id)
    if (seq !== loadSeq) return
    aclEntries.value = data
  } catch (err) {
    showError(err, '加载 IP 白名单失败', seq)
  } finally {
    if (seq === loadSeq) aclLoading.value = false
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
    await loadACL(props.apiKey.id, loadSeq)
    showToast('已添加 IP 白名单')
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
    message: `确定删除 IP 白名单「${entry.CIDR}」吗？该操作不可恢复。`,
    confirmText: '删除',
    tone: 'danger',
  })
  if (!ok) return
  try {
    await deleteACL(entry.ID)
    await loadACL(props.apiKey.id, loadSeq)
    showToast('已删除 IP 白名单')
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

async function loadRateLimit(id: string, seq: number): Promise<void> {
  rateLoading.value = true
  try {
    const cfg = await getRateLimit(id)
    if (seq !== loadSeq) return
    rateLimit.value = cfg.rateLimit ?? null
    rateWindowS.value = cfg.rateWindowS ?? null
    quotaPerDay.value = cfg.quotaPerDay ?? null
    quotaPerMonth.value = cfg.quotaPerMonth ?? null
  } catch (err) {
    showError(err, '加载限流配置失败', seq)
  } finally {
    if (seq === loadSeq) rateLoading.value = false
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
const riskProfile = ref<RiskProfile>('standard')
const riskProfileSaving = ref(false)
const riskProfiles: RiskProfile[] = ['readonly', 'standard', 'privileged', 'legacy_unrestricted']
let profileRequestSeq = 0

// ── 上游权限 ─────────────────────────────────────────────
const upstreamAccessMode = ref<UpstreamAccessMode>('all')
const selectedUpstreamIDs = ref<string[]>([])
const upstreamOptions = ref<Upstream[]>([])
const upstreamAccessLoading = ref(false)
const upstreamAccessSaving = ref(false)
const upstreamSearch = ref('')

const filteredUpstreamOptions = computed(() => {
  const keyword = upstreamSearch.value.trim().toLowerCase()
  if (keyword === '') return upstreamOptions.value
  return upstreamOptions.value.filter((item) =>
    [item.config.name, ...(item.config.tags ?? [])].some((value) =>
      value.toLowerCase().includes(keyword),
    ),
  )
})

const selectedUpstreamCount = computed(() =>
  upstreamAccessMode.value === 'all'
    ? upstreamOptions.value.length
    : selectedUpstreamIDs.value.length,
)

function upstreamSelected(id: string): boolean {
  return selectedUpstreamIDs.value.includes(id)
}

function toggleUpstream(id: string): void {
  const current = new Set(selectedUpstreamIDs.value)
  if (current.has(id)) current.delete(id)
  else current.add(id)
  selectedUpstreamIDs.value = Array.from(current)
}

function selectAllUpstreams(): void {
  selectedUpstreamIDs.value = upstreamOptions.value.map((item) => item.id)
}

function clearSelectedUpstreams(): void {
  selectedUpstreamIDs.value = []
}

// 预聚合每个上游的工具数量，避免模板渲染时对每个上游都全量遍历 toolDetails。
const toolCountByUpstream = computed<Map<string, number>>(() => {
  const countMap = new Map<string, Set<string>>()
  for (const detail of toolDetails.value) {
    for (const source of detail.sources ?? []) {
      if (!countMap.has(source.upstreamId)) countMap.set(source.upstreamId, new Set())
      countMap.get(source.upstreamId)!.add(source.originalName)
    }
    // tool.upstreamId 是来源上游，originalName 是原始名
    if (detail.tool.upstreamId) {
      if (!countMap.has(detail.tool.upstreamId)) countMap.set(detail.tool.upstreamId, new Set())
      countMap.get(detail.tool.upstreamId)!.add(detail.tool.originalName)
    }
  }
  const result = new Map<string, number>()
  for (const [id, names] of countMap) result.set(id, names.size)
  return result
})

function upstreamToolCount(upstreamID: string): number {
  return toolCountByUpstream.value.get(upstreamID) ?? 0
}

async function loadUpstreamAccess(id: string, seq: number): Promise<void> {
  upstreamAccessLoading.value = true
  try {
    const [access, upstreams] = await Promise.all([getAPIKeyUpstreamAccess(id), listUpstreams()])
    if (seq !== loadSeq) return
    upstreamAccessMode.value = access.mode
    selectedUpstreamIDs.value = access.upstreamIds
    upstreamOptions.value = upstreams
  } catch (err) {
    showError(err, '加载上游权限失败', seq)
  } finally {
    if (seq === loadSeq) upstreamAccessLoading.value = false
  }
}

async function saveUpstreamAccess(): Promise<void> {
  if (props.apiKey === null) return
  const selectedCount = selectedUpstreamIDs.value.length
  const denyAll = upstreamAccessMode.value === 'selected' && selectedCount === 0
  const accepted = await confirm({
    title: denyAll ? '确认禁止全部上游' : '确认更新上游权限',
    message:
      upstreamAccessMode.value === 'all'
        ? '保存后该 API Key 可访问全部已启用上游，仍受风险档案和屏蔽规则限制。'
        : denyAll
          ? '当前没有选择任何上游，保存后该 API Key 将无法发现或调用任何 MCP 工具。'
          : `保存后仅允许访问 ${selectedCount} 个上游；新增上游默认不会自动放行。`,
    confirmText: '确认保存',
    tone: denyAll || upstreamAccessMode.value === 'all' ? 'warning' : 'info',
  })
  if (!accepted) return
  upstreamAccessSaving.value = true
  try {
    const saved = await updateAPIKeyUpstreamAccess(props.apiKey.id, {
      mode: upstreamAccessMode.value,
      upstreamIds: upstreamAccessMode.value === 'selected' ? selectedUpstreamIDs.value : [],
    })
    upstreamAccessMode.value = saved.mode
    selectedUpstreamIDs.value = saved.upstreamIds
    showToast('上游权限已更新')
  } catch (err) {
    showError(err, '更新上游权限失败')
  } finally {
    upstreamAccessSaving.value = false
  }
}

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

async function saveRiskProfile(): Promise<void> {
  if (props.apiKey === null) return
  riskProfileSaving.value = true
  try {
    const impact = await getAPIKeyRiskImpact(props.apiKey.id, riskProfile.value)
    const expands = impact.newlyAllowed > 0
    const accepted = await confirm({
      title: expands ? '确认扩大工具权限' : '确认切换风险档案',
      message: `切换后可见来源 ${impact.targetVisible} 个；新增放行 ${impact.newlyAllowed} 个，新增隐藏 ${impact.newlyHidden} 个。${expands ? '请确认该 Key 仅交给可信客户端。' : ''}`,
      confirmText: '确认切换',
      tone: expands ? 'warning' : 'info',
    })
    if (!accepted) return
    await setAPIKeyRiskProfile(props.apiKey.id, riskProfile.value)
    showToast('风险档案已更新')
  } catch (err) {
    showError(err, '更新风险档案失败')
  } finally {
    riskProfileSaving.value = false
  }
}

// ── 打开时加载全部从属配置 ────────────────────────────────────────────────
watch(
  () => props.apiKey,
  (key) => {
    if (key === null) {
      profileRequestSeq += 1
      loadSeq += 1
      usageProfile.value = null
      profileLoading.value = false
      return
    }
    // 每次切换 key 都产生新序号，使所有飞行中的旧请求在写入状态前被丢弃。
    const seq = ++loadSeq
    profileRequestSeq += 1
    activeTab.value = 'filters'
    errorMessage.value = ''
    newFilterPattern.value = ''
    newFilterIsRegex.value = false
    newCidr.value = ''
    usageProfile.value = emptyUsageProfile(key.id)
    riskProfile.value = key.riskProfile || 'legacy_unrestricted'
    upstreamSearch.value = ''
    void loadFilters(key.id, seq)
    void loadToolDetails()
    void loadACL(key.id, seq)
    void loadRateLimit(key.id, seq)
    void loadUsageProfile(key.id)
    void loadUpstreamAccess(key.id, seq)
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
        <div class="no-scrollbar flex gap-1 overflow-x-auto border-b border-gray-200 px-6 dark:border-gray-800">
          <button
            v-for="tab in [
              { key: 'profile', label: '使用画像' },
              { key: 'risk', label: '风险档案' },
              { key: 'upstreams', label: '上游权限' },
              { key: 'filters', label: '屏蔽规则' },
              { key: 'acl', label: 'IP 白名单' },
              { key: 'ratelimit', label: '调用限制' },
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

          <!-- 风险档案 -->
          <section v-else-if="activeTab === 'risk'" class="space-y-4">
            <div
              class="rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-white/[0.03]"
            >
              <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">工具风险档案</h4>
              <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                根据工具的有效风险等级控制可见性和调用权限，与上游权限、屏蔽规则共同生效。
              </p>
              <div class="mt-4 flex flex-col gap-3 sm:flex-row sm:items-end">
                <label class="min-w-0 flex-1 text-sm font-medium text-gray-700 dark:text-gray-300">
                  选择档案
                  <AppSelect
                    v-model="riskProfile"
                    class="mt-1.5"
                    :options="
                      riskProfiles.map((item) => ({
                        value: item,
                        label: riskProfileLabel[item],
                        description: riskProfileDescription[item],
                      }))
                    "
                    size="lg"
                    aria-label="选择风险档案"
                  />
                </label>
                <button
                  type="button"
                  class="bg-brand-500 hover:bg-brand-600 h-10 rounded-lg px-4 text-sm font-medium text-white transition disabled:bg-gray-300 disabled:text-gray-500 dark:disabled:bg-gray-700 dark:disabled:text-gray-400"
                  :disabled="riskProfileSaving"
                  @click="saveRiskProfile"
                >
                  {{ riskProfileSaving ? '保存中…' : '保存档案' }}
                </button>
              </div>
              <p v-if="riskProfile === 'legacy_unrestricted'" class="text-warning-600 mt-3 text-xs">
                兼容无限制会跳过风险目录，仅建议用于迁移旧客户端。
              </p>
            </div>
          </section>

          <!-- 上游权限 -->
          <section v-else-if="activeTab === 'upstreams'" class="space-y-4">
            <div
              v-if="upstreamAccessLoading"
              class="rounded-xl border border-gray-200 px-4 py-8 text-center text-sm text-gray-400 dark:border-gray-800"
            >
              加载中…
            </div>
            <template v-else>
              <div
                class="rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-white/[0.03]"
              >
                <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">上游访问范围</h4>
                <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  限制该 API Key 可访问的 MCP 上游。此配置只缩小范围，不会绕过风险档案或屏蔽规则。
                </p>
                <div class="mt-4 grid gap-2 sm:grid-cols-2">
                  <button
                    type="button"
                    class="rounded-xl border p-3 text-left transition"
                    :class="
                      upstreamAccessMode === 'all'
                        ? 'border-brand-400 bg-brand-50 dark:border-brand-500/50 dark:bg-brand-500/10'
                        : 'border-gray-200 hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-800'
                    "
                    @click="upstreamAccessMode = 'all'"
                  >
                    <span class="block text-sm font-medium text-gray-800 dark:text-white/90"
                      >允许全部上游</span
                    >
                    <span class="mt-1 block text-xs text-gray-500"
                      >新增上游会自动纳入可访问范围</span
                    >
                  </button>
                  <button
                    type="button"
                    class="rounded-xl border p-3 text-left transition"
                    :class="
                      upstreamAccessMode === 'selected'
                        ? 'border-brand-400 bg-brand-50 dark:border-brand-500/50 dark:bg-brand-500/10'
                        : 'border-gray-200 hover:bg-gray-50 dark:border-gray-700 dark:hover:bg-gray-800'
                    "
                    @click="upstreamAccessMode = 'selected'"
                  >
                    <span class="block text-sm font-medium text-gray-800 dark:text-white/90"
                      >仅允许已选上游</span
                    >
                    <span class="mt-1 block text-xs text-gray-500"
                      >新增上游默认不放行，适合严格授权</span
                    >
                  </button>
                </div>
              </div>

              <div v-if="upstreamAccessMode === 'selected'" class="space-y-3">
                <div class="flex flex-col gap-2 sm:flex-row sm:items-center">
                  <input
                    v-model="upstreamSearch"
                    type="search"
                    placeholder="搜索上游名称或标签"
                    class="min-w-0 flex-1 rounded-lg border border-gray-300 px-3 py-2 text-sm dark:border-gray-700 dark:bg-gray-800"
                  />
                  <div class="flex items-center gap-2 text-xs">
                    <button
                      type="button"
                      class="text-brand-600 dark:text-brand-400"
                      @click="selectAllUpstreams"
                    >
                      全选
                    </button>
                    <span class="text-gray-300 dark:text-gray-700">|</span>
                    <button type="button" class="text-gray-500" @click="clearSelectedUpstreams">
                      清空
                    </button>
                  </div>
                </div>
                <div
                  v-if="filteredUpstreamOptions.length === 0"
                  class="rounded-xl border border-dashed border-gray-300 px-4 py-8 text-center text-sm text-gray-400 dark:border-gray-700"
                >
                  没有匹配的上游 MCP
                </div>
                <div v-else class="space-y-2">
                  <button
                    v-for="upstream in filteredUpstreamOptions"
                    :key="upstream.id"
                    type="button"
                    class="flex w-full items-center gap-3 rounded-xl border p-3 text-left transition"
                    :class="
                      upstreamSelected(upstream.id)
                        ? 'border-brand-300 bg-brand-50/60 dark:border-brand-500/40 dark:bg-brand-500/10'
                        : 'border-gray-200 hover:bg-gray-50 dark:border-gray-800 dark:hover:bg-gray-800'
                    "
                    @click="toggleUpstream(upstream.id)"
                  >
                    <span
                      class="flex h-5 w-5 shrink-0 items-center justify-center rounded border text-xs"
                      :class="
                        upstreamSelected(upstream.id)
                          ? 'border-brand-500 bg-brand-500 text-white'
                          : 'border-gray-300 dark:border-gray-600'
                      "
                      >{{ upstreamSelected(upstream.id) ? '✓' : '' }}</span
                    >
                    <span class="min-w-0 flex-1">
                      <span
                        class="block truncate text-sm font-medium text-gray-800 dark:text-white/90"
                        >{{ upstream.config.name }}</span
                      >
                      <span class="mt-0.5 block text-xs text-gray-400">
                        {{ CONN_STATE_LABELS[upstream.state] }} ·
                        {{ upstreamToolCount(upstream.id) }} 个工具来源
                        <template v-if="!upstream.config.enabled"> · 已停用</template>
                      </span>
                    </span>
                  </button>
                </div>
              </div>

              <div
                class="flex flex-col gap-3 border-t border-gray-200 pt-4 sm:flex-row sm:items-center sm:justify-between dark:border-gray-800"
              >
                <p class="text-xs text-gray-500">
                  {{
                    upstreamAccessMode === 'all'
                      ? `将允许全部 ${upstreamOptions.length} 个上游`
                      : `已选择 ${selectedUpstreamCount} / ${upstreamOptions.length} 个上游`
                  }}
                </p>
                <button
                  type="button"
                  class="bg-brand-500 hover:bg-brand-600 rounded-lg px-4 py-2.5 text-sm font-medium text-white transition disabled:bg-gray-300 disabled:text-gray-500 dark:disabled:bg-gray-700 dark:disabled:text-gray-400"
                  :disabled="upstreamAccessSaving"
                  @click="saveUpstreamAccess"
                >
                  {{ upstreamAccessSaving ? '保存中…' : '保存上游权限' }}
                </button>
              </div>
            </template>
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

          <!-- IP 白名单 -->
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
              暂无 IP 白名单（不限制客户端 IP）
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
