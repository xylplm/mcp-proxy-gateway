<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import ToolPlaygroundPanel from '@/components/tools/ToolPlaygroundPanel.vue'
import { listAPIKeys, type APIKey } from '@/api/apikeys'
import {
  createToolPolicy,
  listToolPolicies,
  updateToolPolicy,
  type ToolPolicyRule,
  type ToolPolicyRuleRequest,
} from '@/api/rules'
import { getAggregatedTools, type ToolDef, type ToolDetail, type ToolSource } from '@/api/tools'
import type { UpstreamRateLimits } from '@/api/rateLimits'
import { RefreshIcon } from '@/icons'
import { explainToolGovernance, type ToolGovernanceTone } from '@/utils/toolGovernanceExplain'
import {
  automaticToolRiskTags,
  highestRiskLevel,
  toolRiskTags,
  type ToolRiskLevel,
  type ToolRiskTag,
} from '@/utils/toolRiskTags'
import {
  normalizeIgnoredRiskTags,
  normalizePolicyRiskTags,
  toolPolicyToRequest,
  TOOL_POLICY_AUTO_RISK_TAGS,
  TOOL_POLICY_RISK_TAG_PRESETS,
  type AutoRiskTagKey,
} from '@/utils/toolPolicy'
import {
  buildToolCatalogQuery,
  parseToolCatalogQuery,
  sameToolCatalogQuery,
  type ToolCatalogConflictFilter,
  type ToolCatalogRiskFilter,
  type ToolCatalogSmartView,
} from '@/utils/toolCatalogQuery'

type ConflictFilter = ToolCatalogConflictFilter
type RiskFilter = ToolCatalogRiskFilter
type SmartView = ToolCatalogSmartView

const route = useRoute()
const router = useRouter()
const loading = ref(false)
const refreshing = ref(false)
const loadError = ref('')
const apiKeys = ref<APIKey[]>([])
const selectedAPIKeyID = ref('')
const toolDetails = ref<ToolDetail[]>([])
const smartView = ref<SmartView>('all')
const searchKeyword = ref('')
const selectedUpstream = ref('')
const conflictFilter = ref<ConflictFilter>('all')
const riskFilter = ref<RiskFilter>('all')
const selectedToolName = ref('')
const detailOpen = ref(false)
const riskSaving = ref(false)
const riskError = ref('')
const riskTagInput = ref('')
let syncingRoute = false
let applyingRoute = false
let routeSyncTimer: ReturnType<typeof setTimeout> | null = null

const cardClass =
  'rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]'
const controlClass =
  'h-10 rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-800 shadow-sm transition focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:bg-gray-900 dark:text-white/90'

const toolDetailsByName = computed(() => {
  const map = new Map<string, ToolDetail>()
  for (const item of toolDetails.value) {
    if (item.tool?.name) map.set(item.tool.name, item)
  }
  return map
})

const normalizedSearchKeyword = computed(() => searchKeyword.value.trim().toLowerCase())
const hasSearchKeyword = computed(() => normalizedSearchKeyword.value !== '')

const upstreamOptions = computed(() => {
  const map = new Map<string, string>()
  for (const detail of toolDetails.value) {
    for (const source of detail.sources ?? []) {
      if (source.upstreamId === '') continue
      map.set(source.upstreamId, source.upstreamName || source.upstreamId)
    }
  }
  return Array.from(map.entries())
    .map(([id, name]) => ({ id, name }))
    .sort((a, b) => a.name.localeCompare(b.name, 'zh-CN'))
})

const selectedAPIKey = computed(() =>
  apiKeys.value.find((item) => item.id === selectedAPIKeyID.value) ?? null,
)
const perspectiveLabel = computed(() => selectedAPIKey.value?.name ?? '全局视角')
const perspectiveDescription = computed(() => {
  if (selectedAPIKey.value === null) {
    return '展示全部启用上游经过 MCP 级规则处理后的可见工具。'
  }
  return '展示该 API Key 叠加专属屏蔽规则后的实际可见工具。'
})

const visibleToolDetails = computed(() => {
  const keyword = normalizedSearchKeyword.value
  return toolDetails.value.filter((detail) => {
    if (!matchesSmartView(detail, smartView.value)) return false
    if (selectedUpstream.value !== '' && !(detail.sources ?? []).some((source) => source.upstreamId === selectedUpstream.value)) {
      return false
    }
    if (conflictFilter.value === 'conflict' && !detail.tool.schemaConflict) return false
    if (conflictFilter.value === 'multi' && (detail.tool.sourceCount ?? detail.sources?.length ?? 1) <= 1) return false
    if (riskFilter.value === 'risk' && toolRiskTags(detail).length === 0) return false
    if (keyword !== '' && !toolSearchText(detail).includes(keyword)) return false
    return true
  })
})

const selectedToolDetail = computed<ToolDetail | null>(() => {
  if (selectedToolName.value === '') return null
  return toolDetailsByName.value.get(selectedToolName.value) ?? null
})
const selectedGovernance = computed(() =>
  selectedToolDetail.value === null ? null : explainToolGovernance(selectedToolDetail.value),
)

const totalSourceCount = computed(() =>
  toolDetails.value.reduce((sum, detail) => sum + (detail.sources?.length ?? 0), 0),
)
const multiSourceCount = computed(
  () => toolDetails.value.filter((detail) => (detail.tool.sourceCount ?? detail.sources?.length ?? 1) > 1).length,
)
const schemaConflictCount = computed(
  () => toolDetails.value.filter((detail) => detail.tool.schemaConflict === true).length,
)
const riskToolCount = computed(() => toolDetails.value.filter((detail) => toolRiskTags(detail).length > 0).length)
const degradedSourceCount = computed(() =>
  toolDetails.value.reduce((sum, detail) => sum + degradedSources(detail).length, 0),
)
const smartViewOptions = computed<Array<{ value: SmartView; label: string; count: number }>>(() => [
  { value: 'all', label: '全部', count: toolDetails.value.length },
  { value: 'attention', label: '需关注', count: attentionToolCount.value },
  { value: 'multi', label: '多来源', count: multiSourceCount.value },
  { value: 'risk', label: '风险提示', count: riskToolCount.value },
  { value: 'degraded', label: '降级来源', count: degradedToolCount.value },
])
const attentionToolCount = computed(() =>
  toolDetails.value.filter((detail) => matchesSmartView(detail, 'attention')).length,
)
const degradedToolCount = computed(() =>
  toolDetails.value.filter((detail) => matchesSmartView(detail, 'degraded')).length,
)
const visibleCountLabel = computed(() => {
  if (smartView.value !== 'all' || hasSearchKeyword.value || selectedUpstream.value !== '' || conflictFilter.value !== 'all' || riskFilter.value !== 'all') {
    return `${visibleToolDetails.value.length.toLocaleString('zh-CN')} / ${toolDetails.value.length.toLocaleString('zh-CN')}`
  }
  return toolDetails.value.length.toLocaleString('zh-CN')
})

async function loadTools(showLoading = true): Promise<void> {
  if (showLoading) loading.value = true
  else refreshing.value = true
  loadError.value = ''
  try {
    const result = await getAggregatedTools({ apiKeyId: selectedAPIKeyID.value || undefined })
    toolDetails.value = result.toolDetails.length > 0
      ? result.toolDetails
      : result.tools.map((tool) => ({ tool, sources: [] }))
    openSelectedToolFromQuery()
    ensureSelectedTool()
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : '加载工具目录失败'
  } finally {
    loading.value = false
    refreshing.value = false
  }
}

async function loadAPIKeyOptions(): Promise<void> {
  try {
    apiKeys.value = await listAPIKeys()
    if (selectedAPIKeyID.value !== '' && !apiKeys.value.some((key) => key.id === selectedAPIKeyID.value)) {
      selectedAPIKeyID.value = ''
      scheduleRouteSync()
    }
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : '加载 API Key 失败'
  }
}

function ensureSelectedTool(): void {
  if (selectedToolName.value === '') return
  if (!toolDetailsByName.value.has(selectedToolName.value)) {
    selectedToolName.value = ''
    detailOpen.value = false
    scheduleRouteSync()
  }
}

function openSelectedToolFromQuery(): void {
  if (selectedToolName.value === '') return
  if (!toolDetailsByName.value.has(selectedToolName.value)) return
  detailOpen.value = true
  riskError.value = ''
  riskTagInput.value = ''
}

function toolDescription(tool: ToolDef): string {
  const desc = tool.description?.trim() ?? ''
  if (desc !== '' && desc !== tool.name && desc !== tool.originalName) return desc
  return '上游未提供有效描述'
}

function toolSearchText(detail: ToolDetail): string {
  return [
    detail.tool.name,
    detail.tool.originalName,
    toolDescription(detail.tool),
    ...toolRiskTags(detail).map((tag) => tag.label),
    ...degradedSources(detail).map(() => '临时降级 来源降级'),
    ...(detail.sources ?? []).flatMap((source) => [
      source.upstreamId,
      source.upstreamName,
      source.originalName,
      source.description,
    ]),
  ]
    .join(' ')
    .toLowerCase()
}

function openDetail(detail: ToolDetail): void {
  selectedToolName.value = detail.tool.name
  riskError.value = ''
  riskTagInput.value = ''
  detailOpen.value = true
  scheduleRouteSync()
}

function closeDetail(): void {
  detailOpen.value = false
  selectedToolName.value = ''
  riskError.value = ''
  riskTagInput.value = ''
  scheduleRouteSync()
}

function resetFilters(): void {
  smartView.value = 'all'
  searchKeyword.value = ''
  selectedUpstream.value = ''
  conflictFilter.value = 'all'
  riskFilter.value = 'all'
  selectedToolName.value = ''
  detailOpen.value = false
  scheduleRouteSync()
}

async function changePerspective(): Promise<void> {
  resetFilters()
  await loadTools()
  scheduleRouteSync()
}

function applyRouteQuery(): boolean {
  applyingRoute = true
  const state = parseToolCatalogQuery(route.query)
  const apiKeyChanged = selectedAPIKeyID.value !== state.apiKeyId
  selectedAPIKeyID.value = state.apiKeyId
  smartView.value = state.view
  searchKeyword.value = state.search
  selectedUpstream.value = state.upstreamId
  conflictFilter.value = state.status
  riskFilter.value = state.risk
  selectedToolName.value = state.tool
  detailOpen.value = state.tool !== ''
  riskError.value = ''
  riskTagInput.value = ''
  applyingRoute = false
  if (toolDetails.value.length > 0) {
    openSelectedToolFromQuery()
    ensureSelectedTool()
  }
  return apiKeyChanged
}

function scheduleRouteSync(): void {
  if (applyingRoute) return
  if (routeSyncTimer !== null) {
    clearTimeout(routeSyncTimer)
  }
  routeSyncTimer = setTimeout(() => {
    routeSyncTimer = null
    void syncRouteQuery()
  }, 120)
}

async function syncRouteQuery(): Promise<void> {
  if (syncingRoute || applyingRoute) return
  const query = buildToolCatalogQuery({
    apiKeyId: selectedAPIKeyID.value,
    view: smartView.value,
    search: searchKeyword.value,
    upstreamId: selectedUpstream.value,
    status: conflictFilter.value,
    risk: riskFilter.value,
    tool: detailOpen.value ? selectedToolName.value : '',
  })
  const currentQuery = buildToolCatalogQuery(parseToolCatalogQuery(route.query))
  if (sameToolCatalogQuery(query, currentQuery)) return
  syncingRoute = true
  try {
    await router.replace({ query })
  } finally {
    syncingRoute = false
  }
}

function sourceCountText(detail: ToolDetail): string {
  const count = detail.tool.sourceCount ?? detail.sources?.length ?? 1
  return `${count} 个来源`
}

function setSmartView(view: SmartView): void {
  smartView.value = view
}

function matchesSmartView(detail: ToolDetail, view: SmartView): boolean {
  if (view === 'all') return true
  if (view === 'multi') return (detail.tool.sourceCount ?? detail.sources?.length ?? 1) > 1
  if (view === 'risk') return toolRiskTags(detail).length > 0
  if (view === 'degraded') return degradedSources(detail).length > 0
  return detail.tool.schemaConflict === true
    || toolRiskTags(detail).length > 0
    || degradedSources(detail).length > 0
}

function schemaPreview(value: unknown): string {
  if (value === null || value === undefined || value === '') {
    return JSON.stringify({ type: 'object' }, null, 2)
  }
  if (typeof value === 'string') return value
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

function formatRateLimits(limits?: UpstreamRateLimits): string {
  if (!limits?.enabled) return '未配置'
  const parts: string[] = []
  if (limits.perSecond && limits.perSecond > 0) parts.push(`每秒 ${limits.perSecond}`)
  if (limits.perMinute && limits.perMinute > 0) parts.push(`每分钟 ${limits.perMinute}`)
  if (limits.perHour && limits.perHour > 0) parts.push(`每小时 ${limits.perHour}`)
  if (limits.perDay && limits.perDay > 0) parts.push(`每日 ${limits.perDay}`)
  if (limits.perWeek && limits.perWeek > 0) parts.push(`每周 ${limits.perWeek}`)
  if (limits.perMonth && limits.perMonth > 0) parts.push(`每月 ${limits.perMonth}`)
  return parts.length > 0 ? parts.join(' / ') : '已启用，未设置上限'
}

function sourceStatusClass(source: ToolSource): string {
  if (!source.compatible) {
    return 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
  }
  if (source.temporarilyDegraded) {
    return 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
  }
  if (source.routingAvailable === false) {
    return 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'
  }
  return 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
}

function sourceStatusLabel(source: ToolSource): string {
  if (!source.compatible) return 'Schema 不一致'
  if (source.temporarilyDegraded) return '临时降级'
  if (source.routingAvailable === false) return '暂不路由'
  return '可参与路由'
}

function degradedSources(detail: ToolDetail): ToolSource[] {
  return (detail.sources ?? []).filter((source) => source.temporarilyDegraded)
}

function degradationText(source: ToolSource): string {
  const reason = source.degradationReason?.trim() || '该来源近期连续失败，网关会优先尝试其他健康来源。'
  if (!source.degradationUntil) return reason
  const until = new Date(source.degradationUntil)
  if (Number.isNaN(until.getTime())) return reason
  return `${reason} 预计 ${until.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })} 后恢复尝试。`
}

function governanceToneClass(tone: ToolGovernanceTone): string {
  switch (tone) {
    case 'success':
      return 'border-success-100 bg-success-50 text-success-700 dark:border-success-500/20 dark:bg-success-500/10 dark:text-success-300'
    case 'warning':
      return 'border-warning-200 bg-warning-50 text-warning-700 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-300'
    case 'info':
      return 'border-brand-100 bg-brand-50 text-brand-700 dark:border-brand-500/20 dark:bg-brand-500/10 dark:text-brand-300'
    default:
      return 'border-gray-200 bg-gray-50 text-gray-600 dark:border-gray-800 dark:bg-white/[0.03] dark:text-gray-300'
  }
}

function riskTags(detail: ToolDetail): ToolRiskTag[] {
  return toolRiskTags(detail)
}

function automaticRiskTags(detail: ToolDetail): ToolRiskTag[] {
  return automaticToolRiskTags(detail)
}

function ignoredRiskTags(detail: ToolDetail): AutoRiskTagKey[] {
  return normalizeIgnoredRiskTags(detail.policy?.ignoredRiskTags ?? [])
}

function customRiskTags(detail: ToolDetail): string[] {
  return normalizePolicyRiskTags(detail.policy?.riskTags ?? [])
}

function autoRiskTagDetected(detail: ToolDetail, key: string): boolean {
  return automaticRiskTags(detail).some((tag) => tag.key === key)
}

function autoRiskTagIgnored(detail: ToolDetail, key: string): boolean {
  return ignoredRiskTags(detail).includes(key as AutoRiskTagKey)
}

function nextExactPolicySortOrder(policies: ToolPolicyRule[]): number {
  if (policies.length === 0) return 0
  return Math.min(...policies.map((policy) => policy.sortOrder)) - 1
}

function baseRiskPolicy(detail: ToolDetail, policies: ToolPolicyRule[]): {
  exact: ToolPolicyRule | null
  payload: ToolPolicyRuleRequest
} {
  const exact = policies.find((policy) => !policy.isRegex && policy.pattern === detail.tool.name) ?? null
  const matched = detail.policy?.ruleId ? policies.find((policy) => policy.id === detail.policy?.ruleId) : null
  const exactIsActive = exact !== null && detail.policy?.ruleId === exact.id
  const source = exactIsActive ? exact : (matched ?? exact)
  const payload = toolPolicyToRequest(
    source ?? {
      pattern: detail.tool.name,
      isRegex: false,
      enabled: true,
      sortOrder: nextExactPolicySortOrder(policies),
      routingStrategy: detail.policy?.routingStrategy ?? '',
      cacheEnabled: detail.policy?.cacheEnabled ?? false,
      cacheTtlSeconds: detail.policy?.cacheTtlSeconds ?? 0,
      riskTags: detail.policy?.riskTags ?? [],
      ignoredRiskTags: detail.policy?.ignoredRiskTags ?? [],
    },
  )
  payload.pattern = detail.tool.name
  payload.isRegex = false
  payload.enabled = true
  if (exact === null || !exactIsActive) {
    payload.sortOrder = nextExactPolicySortOrder(policies)
  }
  return { exact, payload }
}

async function saveRiskPolicy(mutator: (payload: ToolPolicyRuleRequest) => void): Promise<void> {
  const detail = selectedToolDetail.value
  if (detail === null || riskSaving.value) return
  riskSaving.value = true
  riskError.value = ''
  try {
    const policies = await listToolPolicies()
    const { exact, payload } = baseRiskPolicy(detail, policies)
    mutator(payload)
    const normalized = toolPolicyToRequest(payload)
    if (exact !== null) {
      await updateToolPolicy(exact.id, normalized)
    } else {
      await createToolPolicy(normalized)
    }
    riskTagInput.value = ''
    await loadTools(false)
  } catch (err) {
    riskError.value = err instanceof Error ? err.message : '保存风险提示失败'
  } finally {
    riskSaving.value = false
  }
}

async function toggleAutoRiskTagIgnore(key: AutoRiskTagKey): Promise<void> {
  await saveRiskPolicy((payload) => {
    const current = new Set(normalizeIgnoredRiskTags(payload.ignoredRiskTags))
    if (current.has(key)) current.delete(key)
    else current.add(key)
    payload.ignoredRiskTags = Array.from(current)
  })
}

async function toggleCustomRiskTag(tag: string): Promise<void> {
  await saveRiskPolicy((payload) => {
    const current = new Set(normalizePolicyRiskTags(payload.riskTags))
    if (current.has(tag)) current.delete(tag)
    else current.add(tag)
    payload.riskTags = Array.from(current)
  })
}

async function addCustomRiskTag(tag: string): Promise<void> {
  const value = tag.trim()
  if (value === '') return
  await saveRiskPolicy((payload) => {
    payload.riskTags = normalizePolicyRiskTags([...payload.riskTags, value])
  })
}

async function removeCustomRiskTag(tag: string): Promise<void> {
  await saveRiskPolicy((payload) => {
    payload.riskTags = normalizePolicyRiskTags(payload.riskTags.filter((item) => item !== tag))
  })
}

async function restoreAutomaticRiskTags(): Promise<void> {
  await saveRiskPolicy((payload) => {
    payload.riskTags = []
    payload.ignoredRiskTags = []
  })
}

function onRiskTagInputKeydown(event: KeyboardEvent): void {
  if (event.key !== 'Enter') return
  event.preventDefault()
  void addCustomRiskTag(riskTagInput.value)
}

function riskTagClass(level: ToolRiskLevel): string {
  if (level === 'high') {
    return 'bg-error-50 text-error-700 dark:bg-error-500/10 dark:text-error-300'
  }
  if (level === 'medium') {
    return 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-300'
  }
  return 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'
}

function riskNoticeClass(detail: ToolDetail): string {
  const level = highestRiskLevel(riskTags(detail))
  if (level === 'high') {
    return 'border-error-100 bg-error-50 text-error-700 dark:border-error-500/20 dark:bg-error-500/10 dark:text-error-300'
  }
  return 'border-warning-200 bg-warning-50 text-warning-700 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-300'
}

watch(
  () => route.query,
  () => {
    if (syncingRoute) return
    const apiKeyChanged = applyRouteQuery()
    if (apiKeyChanged) {
      void loadTools()
    }
  },
)

watch(
  [smartView, searchKeyword, selectedUpstream, conflictFilter, riskFilter],
  () => {
    scheduleRouteSync()
  },
)

onMounted(async () => {
  applyRouteQuery()
  await loadAPIKeyOptions()
  await loadTools()
})

onUnmounted(() => {
  if (routeSyncTimer !== null) {
    clearTimeout(routeSyncTimer)
    routeSyncTimer = null
  }
})
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="工具目录" />

    <div class="mb-5 flex flex-wrap items-start justify-between gap-3">
      <div>
        <h2 class="text-lg font-semibold text-gray-800 dark:text-white/90">工具目录</h2>
        <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
          查看经过启停、别名和屏蔽规则处理后的真实聚合工具。
        </p>
      </div>
      <button
        v-tooltip:bottom-end="'刷新工具目录'"
        type="button"
        class="inline-flex h-10 w-10 items-center justify-center rounded-lg border border-gray-200 text-gray-600 transition hover:border-brand-300 hover:bg-brand-50 hover:text-brand-600 disabled:opacity-60 dark:border-gray-800 dark:text-gray-400 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.08] dark:hover:text-brand-400"
        :disabled="loading || refreshing"
        aria-label="刷新工具目录"
        @click="() => loadTools(false)"
      >
        <RefreshIcon class="h-5 w-5" :class="refreshing ? 'animate-spin' : ''" />
      </button>
    </div>

    <p
      v-if="loadError !== ''"
      class="mb-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
    >
      {{ loadError }}
    </p>

    <div class="mb-5 grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <section :class="cardClass">
        <p class="text-sm text-gray-500 dark:text-gray-400">可见工具</p>
        <p class="mt-2 text-2xl font-semibold text-gray-800 dark:text-white/90">{{ visibleCountLabel }}</p>
      </section>
      <section :class="cardClass">
        <p class="text-sm text-gray-500 dark:text-gray-400">来源上游</p>
        <p class="mt-2 text-2xl font-semibold text-gray-800 dark:text-white/90">
          {{ upstreamOptions.length.toLocaleString('zh-CN') }}
        </p>
      </section>
      <section :class="cardClass">
        <p class="text-sm text-gray-500 dark:text-gray-400">来源映射</p>
        <p class="mt-2 text-2xl font-semibold text-gray-800 dark:text-white/90">
          {{ totalSourceCount.toLocaleString('zh-CN') }}
        </p>
      </section>
      <section :class="cardClass">
        <p class="text-sm text-gray-500 dark:text-gray-400">需关注</p>
        <p class="mt-2 text-2xl font-semibold" :class="attentionToolCount > 0 ? 'text-warning-700 dark:text-warning-400' : 'text-success-700 dark:text-success-400'">
          {{ attentionToolCount.toLocaleString('zh-CN') }}
        </p>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          风险提示 {{ riskToolCount.toLocaleString('zh-CN') }} 个，降级来源 {{ degradedSourceCount.toLocaleString('zh-CN') }} 个
        </p>
      </section>
    </div>

    <section :class="[cardClass, 'mb-5']">
      <div class="mb-4">
        <div class="mb-2 flex items-center justify-between gap-3">
          <span class="text-xs font-medium text-gray-500 dark:text-gray-400">智能视图</span>
          <span v-if="smartView !== 'all'" class="text-xs text-gray-400 dark:text-gray-500">
            已按视图筛选
          </span>
        </div>
        <div class="flex flex-wrap gap-2">
          <button
            v-for="item in smartViewOptions"
            :key="item.value"
            type="button"
            class="inline-flex items-center gap-1.5 rounded-lg border px-3 py-1.5 text-xs font-medium transition"
            :class="smartView === item.value
              ? 'border-brand-300 bg-brand-50 text-brand-700 dark:border-brand-500/30 dark:bg-brand-500/10 dark:text-brand-300'
              : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-gray-800 dark:text-gray-300 dark:hover:bg-gray-800'"
            @click="setSmartView(item.value)"
          >
            <span>{{ item.label }}</span>
            <span class="rounded-full bg-white/80 px-1.5 py-0.5 text-[11px] text-gray-500 dark:bg-gray-900/60 dark:text-gray-400">
              {{ item.count.toLocaleString('zh-CN') }}
            </span>
          </button>
        </div>
      </div>

      <div class="grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-[240px_minmax(0,1fr)_220px_170px_160px_auto] xl:items-end">
        <label class="block">
          <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">调用视角</span>
          <select v-model="selectedAPIKeyID" :class="[controlClass, 'w-full']" @change="changePerspective">
            <option value="">全局视角</option>
            <option v-for="key in apiKeys" :key="key.id" :value="key.id">
              {{ key.name }}{{ key.enabled ? '' : '（已停用）' }}
            </option>
          </select>
        </label>
        <label class="block">
          <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">搜索工具</span>
          <input
            v-model="searchKeyword"
            type="search"
            placeholder="搜索工具名、描述、来源上游"
            :class="[controlClass, 'w-full']"
          />
        </label>
        <label class="block">
          <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">来源上游</span>
          <select v-model="selectedUpstream" :class="[controlClass, 'w-full']">
            <option value="">全部上游</option>
            <option v-for="upstream in upstreamOptions" :key="upstream.id" :value="upstream.id">
              {{ upstream.name }}
            </option>
          </select>
        </label>
        <label class="block">
          <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">工具状态</span>
          <select v-model="conflictFilter" :class="[controlClass, 'w-full']">
            <option value="all">全部工具</option>
            <option value="multi">多来源工具</option>
            <option value="conflict">Schema 不一致</option>
          </select>
        </label>
        <label class="block">
          <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">风险提示</span>
          <select v-model="riskFilter" :class="[controlClass, 'w-full']">
            <option value="all">全部工具</option>
            <option value="risk">仅看风险提示</option>
          </select>
        </label>
        <button
          type="button"
          class="h-10 rounded-lg border border-gray-300 px-4 text-sm font-medium text-gray-700 transition hover:bg-gray-50 md:col-span-2 xl:col-span-1 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          @click="resetFilters"
        >
          重置
        </button>
      </div>
      <div class="mt-4 rounded-lg border border-brand-100 bg-brand-50 px-3 py-2 text-xs leading-5 text-brand-700 dark:border-brand-500/20 dark:bg-brand-500/10 dark:text-brand-300">
        当前视角：{{ perspectiveLabel }}。{{ perspectiveDescription }}
      </div>
    </section>

    <div
      v-if="loading"
      class="rounded-2xl border border-gray-200 bg-white px-5 py-12 text-center text-sm text-gray-400 dark:border-gray-800 dark:bg-white/[0.03]"
    >
      加载中…
    </div>
    <div
      v-else-if="toolDetails.length === 0"
      class="rounded-2xl border border-dashed border-gray-300 bg-white px-5 py-12 text-center text-sm text-gray-400 dark:border-gray-700 dark:bg-white/[0.03]"
    >
      暂无可用工具，可先检查上游连接和工具刷新状态
    </div>
    <div
      v-else-if="visibleToolDetails.length === 0"
      class="rounded-2xl border border-dashed border-gray-300 bg-white px-5 py-12 text-center text-sm text-gray-400 dark:border-gray-700 dark:bg-white/[0.03]"
    >
      没有匹配的工具
    </div>

    <div v-else class="grid grid-cols-1 gap-4 xl:grid-cols-2 3xl:grid-cols-3">
      <button
        v-for="detail in visibleToolDetails"
        :key="detail.tool.name"
        type="button"
        class="rounded-2xl border border-gray-200 bg-white p-4 text-left shadow-sm transition hover:border-brand-300 hover:shadow-theme-sm focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-400 dark:border-gray-800 dark:bg-white/[0.03] dark:hover:border-brand-500/40"
        :aria-label="`查看 ${detail.tool.name} 工具详情`"
        @click="openDetail(detail)"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <p class="truncate font-mono text-sm font-semibold text-gray-800 dark:text-white/90">
              {{ detail.tool.name }}
            </p>
            <p class="mt-1 truncate text-xs text-gray-500 dark:text-gray-400">
              {{ detail.tool.originalName }}
            </p>
          </div>
          <div class="flex shrink-0 flex-col items-end gap-1.5">
            <span
              v-if="(detail.tool.sourceCount ?? detail.sources?.length ?? 1) > 1"
              class="rounded-full bg-brand-50 px-2 py-0.5 text-[11px] font-medium text-brand-600 dark:bg-brand-500/10 dark:text-brand-400"
            >
              {{ sourceCountText(detail) }}
            </span>
            <span
              v-if="detail.tool.schemaConflict"
              class="rounded-full bg-warning-50 px-2 py-0.5 text-[11px] font-medium text-warning-700 dark:bg-warning-500/10 dark:text-warning-400"
            >
              Schema 不一致
            </span>
            <span
              v-if="degradedSources(detail).length > 0"
              class="rounded-full bg-warning-50 px-2 py-0.5 text-[11px] font-medium text-warning-700 dark:bg-warning-500/10 dark:text-warning-400"
            >
              来源降级
            </span>
            <span
              v-for="tag in riskTags(detail).slice(0, 2)"
              :key="tag.key"
              class="rounded-full px-2 py-0.5 text-[11px] font-medium"
              :class="riskTagClass(tag.level)"
            >
              {{ tag.label }}
            </span>
          </div>
        </div>
        <p class="mt-3 line-clamp-3 text-sm leading-6 text-gray-500 dark:text-gray-400">
          {{ toolDescription(detail.tool) }}
        </p>
        <div class="mt-4 flex flex-wrap gap-1.5">
          <span
            v-for="source in (detail.sources ?? []).slice(0, 3)"
            :key="`${detail.tool.name}:${source.upstreamId}:${source.originalName}`"
            class="max-w-full truncate rounded-full bg-gray-100 px-2 py-0.5 text-[11px] text-gray-600 dark:bg-gray-800 dark:text-gray-300"
          >
            {{ source.upstreamName || source.upstreamId }}
          </span>
        </div>
      </button>
    </div>

    <transition name="fade">
      <div
        v-if="detailOpen && selectedToolDetail !== null"
        class="fixed inset-0 z-[100000] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
        @click.self="closeDetail"
      >
        <div
          class="flex max-h-[88vh] w-full max-w-4xl flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-gray-800 dark:bg-gray-900"
          role="dialog"
          aria-modal="true"
        >
          <div class="flex items-start justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-gray-800">
            <div class="min-w-0">
              <p class="truncate font-mono text-base font-semibold text-gray-800 dark:text-white/90">
                {{ selectedToolDetail.tool.name }}
              </p>
              <p class="mt-1 text-sm leading-6 text-gray-500 dark:text-gray-400">
                {{ toolDescription(selectedToolDetail.tool) }}
              </p>
            </div>
            <button
              type="button"
              class="shrink-0 rounded-lg p-2 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-800"
              aria-label="关闭工具详情"
              @click="closeDetail"
            >
              <span class="block text-xl leading-none">x</span>
            </button>
          </div>

          <div class="custom-scrollbar min-h-0 flex-1 overflow-y-auto p-5">
            <div class="flex flex-wrap items-center gap-2">
              <span class="rounded-full bg-success-50 px-2.5 py-1 text-xs font-medium text-success-700 dark:bg-success-500/10 dark:text-success-400">
                {{ selectedToolDetail.sources?.length ?? 0 }} 个来源上游
              </span>
              <span
                v-if="selectedToolDetail.tool.schemaConflict"
                class="rounded-full bg-warning-50 px-2.5 py-1 text-xs font-medium text-warning-700 dark:bg-warning-500/10 dark:text-warning-400"
              >
                Schema 不一致
              </span>
              <span
                v-if="degradedSources(selectedToolDetail).length > 0"
                class="rounded-full bg-warning-50 px-2.5 py-1 text-xs font-medium text-warning-700 dark:bg-warning-500/10 dark:text-warning-400"
              >
                {{ degradedSources(selectedToolDetail).length }} 个来源降级
              </span>
              <span
                v-for="tag in riskTags(selectedToolDetail)"
                :key="tag.key"
                class="rounded-full px-2.5 py-1 text-xs font-medium"
                :class="riskTagClass(tag.level)"
              >
                {{ tag.label }}
              </span>
            </div>

            <div
              v-if="riskTags(selectedToolDetail).length > 0"
              class="mt-4 rounded-lg border px-3 py-2 text-xs leading-5"
              :class="riskNoticeClass(selectedToolDetail)"
            >
              该工具名称或描述包含可能改变数据、发送消息或触发财务动作的关键词；网关仅做提示与筛选，不会默认拦截。
            </div>

            <section class="mt-4 rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-white/[0.02]">
              <div class="flex flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                <div class="min-w-0">
                  <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">风险提示修正</h4>
                  <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                    调整当前工具的提示标签，仅影响展示和筛选，不改变调用行为。
                  </p>
                </div>
                <button
                  type="button"
                  class="shrink-0 rounded-lg border border-gray-300 px-3 py-1.5 text-xs font-medium text-gray-700 transition hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                  :disabled="riskSaving"
                  @click="restoreAutomaticRiskTags"
                >
                  恢复自动识别
                </button>
              </div>

              <p
                v-if="riskError !== ''"
                class="mt-3 rounded-lg bg-error-50 px-3 py-2 text-xs text-error-600 dark:bg-error-500/10 dark:text-error-400"
              >
                {{ riskError }}
              </p>

              <div class="mt-4">
                <p class="mb-2 text-xs font-medium text-gray-600 dark:text-gray-300">自动标签</p>
                <div class="grid grid-cols-1 gap-2 sm:grid-cols-2 xl:grid-cols-4">
                  <button
                    v-for="tag in TOOL_POLICY_AUTO_RISK_TAGS"
                    :key="tag.key"
                    type="button"
                    class="rounded-lg border px-3 py-2 text-left text-xs transition disabled:cursor-not-allowed disabled:opacity-50"
                    :class="autoRiskTagIgnored(selectedToolDetail, tag.key)
                      ? 'border-gray-300 bg-gray-100 text-gray-700 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200'
                      : autoRiskTagDetected(selectedToolDetail, tag.key)
                        ? 'border-warning-200 bg-warning-50 text-warning-700 hover:bg-warning-100 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-300'
                        : 'border-gray-200 text-gray-500 dark:border-gray-800 dark:text-gray-500'"
                    :disabled="riskSaving || !autoRiskTagDetected(selectedToolDetail, tag.key)"
                    @click="toggleAutoRiskTagIgnore(tag.key)"
                  >
                    <span class="block font-medium">
                      {{ autoRiskTagIgnored(selectedToolDetail, tag.key) ? '已忽略' : autoRiskTagDetected(selectedToolDetail, tag.key) ? '已识别' : '未识别' }}：{{ tag.label }}
                    </span>
                    <span class="mt-1 block text-[11px] opacity-80">{{ tag.description }}</span>
                  </button>
                </div>
              </div>

              <div class="mt-4">
                <p class="mb-2 text-xs font-medium text-gray-600 dark:text-gray-300">手动标签</p>
                <div class="mb-2 flex flex-wrap gap-2">
                  <button
                    v-for="tag in TOOL_POLICY_RISK_TAG_PRESETS"
                    :key="tag"
                    type="button"
                    class="rounded-full border px-2.5 py-1 text-xs font-medium transition disabled:opacity-50"
                    :class="customRiskTags(selectedToolDetail).includes(tag) ? 'border-warning-300 bg-warning-50 text-warning-700 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-300' : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800'"
                    :disabled="riskSaving"
                    @click="toggleCustomRiskTag(tag)"
                  >
                    {{ tag }}
                  </button>
                </div>
                <div class="flex gap-2">
                  <input
                    v-model="riskTagInput"
                    type="text"
                    placeholder="自定义标签"
                    class="min-w-0 flex-1 rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-800 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none disabled:opacity-50 dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
                    :disabled="riskSaving"
                    @keydown="onRiskTagInputKeydown"
                  />
                  <button
                    type="button"
                    class="rounded-lg border border-gray-300 px-3 text-sm font-medium text-gray-700 transition hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                    :disabled="riskSaving"
                    @click="addCustomRiskTag(riskTagInput)"
                  >
                    添加
                  </button>
                </div>
                <div v-if="customRiskTags(selectedToolDetail).length > 0" class="mt-2 flex flex-wrap gap-1.5">
                  <button
                    v-for="tag in customRiskTags(selectedToolDetail)"
                    :key="tag"
                    type="button"
                    class="rounded-full bg-warning-50 px-2 py-0.5 text-xs text-warning-700 transition hover:bg-warning-100 disabled:opacity-50 dark:bg-warning-500/10 dark:text-warning-300"
                    :disabled="riskSaving"
                    @click="removeCustomRiskTag(tag)"
                  >
                    {{ tag }} ×
                  </button>
                </div>
              </div>
            </section>

            <div
              v-if="selectedToolDetail.tool.schemaConflict"
              class="mt-4 rounded-lg border border-warning-200 bg-warning-50 px-3 py-2 text-xs leading-5 text-warning-700 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-300"
            >
              同名来源的入参 Schema 不完全一致，调用时只会选择与当前展示 Schema 一致的来源。
            </div>

            <div
              v-if="degradedSources(selectedToolDetail).length > 0"
              class="mt-4 rounded-lg border border-warning-200 bg-warning-50 px-3 py-2 text-xs leading-5 text-warning-700 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-300"
            >
              部分来源近期连续失败，网关正在优先选择其他健康来源；冷却结束后会自动恢复尝试。
            </div>

            <section
              v-if="selectedGovernance !== null"
              class="mt-4 rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-white/[0.02]"
            >
              <div class="flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
                <div class="min-w-0">
                  <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">
                    {{ selectedGovernance.title }}
                  </h4>
                  <p class="mt-1 text-sm leading-6 text-gray-500 dark:text-gray-400">
                    {{ selectedGovernance.description }}
                  </p>
                </div>
                <span class="shrink-0 rounded-full bg-gray-100 px-2.5 py-1 text-xs text-gray-600 dark:bg-white/5 dark:text-gray-400">
                  治理解释
                </span>
              </div>
              <div class="mt-4 grid grid-cols-1 gap-2 md:grid-cols-2">
                <div
                  v-for="item in selectedGovernance.items"
                  :key="item.key"
                  class="rounded-lg border px-3 py-2"
                  :class="governanceToneClass(item.tone)"
                >
                  <p class="text-xs font-semibold">{{ item.title }}</p>
                  <p class="mt-1 text-xs leading-5 opacity-90">{{ item.description }}</p>
                </div>
              </div>
            </section>

            <div class="mt-4 grid grid-cols-1 gap-3 md:grid-cols-2">
              <div
                v-for="source in selectedToolDetail.sources ?? []"
                :key="`${source.upstreamId}:${source.originalName}`"
                class="rounded-lg border border-gray-100 bg-gray-50 p-3 dark:border-gray-800 dark:bg-white/[0.02]"
              >
                <div class="flex flex-wrap items-start justify-between gap-2">
                  <div class="min-w-0">
                    <p class="truncate text-sm font-medium text-gray-800 dark:text-white/90">
                      {{ source.upstreamName || source.upstreamId }}
                    </p>
                    <p class="mt-1 truncate font-mono text-[11px] text-gray-400 dark:text-gray-500">
                      {{ source.originalName }}
                    </p>
                  </div>
                  <span class="rounded-full px-2 py-0.5 text-[11px] font-medium" :class="sourceStatusClass(source)">
                    {{ sourceStatusLabel(source) }}
                  </span>
                </div>
                <p class="mt-2 line-clamp-3 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  {{ source.description || '上游未提供有效描述' }}
                </p>
                <p class="mt-2 text-[11px] leading-5 text-gray-400 dark:text-gray-500">
                  限流额度：{{ formatRateLimits(source.rateLimits) }}
                </p>
                <p
                  v-if="source.temporarilyDegraded"
                  class="mt-2 rounded-md bg-warning-50 px-2 py-1 text-[11px] leading-5 text-warning-700 dark:bg-warning-500/10 dark:text-warning-300"
                >
                  {{ degradationText(source) }}
                </p>
              </div>
            </div>

            <ToolPlaygroundPanel
              :tool-name="selectedToolDetail.tool.name"
              :input-schema="selectedToolDetail.tool.inputSchema"
              :initial-api-key-id="selectedAPIKeyID"
            />

            <details class="mt-4 rounded-lg bg-gray-50 p-3 dark:bg-white/[0.03]">
              <summary class="cursor-pointer text-xs font-medium text-gray-700 dark:text-gray-300">
                查看入参 Schema
              </summary>
              <pre class="custom-scrollbar mt-3 max-h-72 overflow-auto text-xs leading-5 text-gray-600 dark:text-gray-300">{{ schemaPreview(selectedToolDetail.tool.inputSchema) }}</pre>
            </details>
          </div>
        </div>
      </div>
    </transition>
  </AdminLayout>
</template>
