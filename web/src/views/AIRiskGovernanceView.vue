<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import FieldLabel from '@/components/common/FieldLabel.vue'
import RiskJobCard from '@/components/ai-risk/RiskJobCard.vue'
import { RefreshIcon, PlusIcon, CheckIcon, TrashIcon } from '@/icons'
import { useToast } from '@/composables/useToast'
import {
  activateProvider,
  bulkManualOverride,
  cancelAssessmentJob,
  clearManualOverride,
  createProvider,
  deleteProvider,
  listAssessmentJobs,
  listProviders,
  listRiskTools,
  queueAssessment,
  reassessRiskTool,
  reconcileRiskCatalog,
  setManualOverride,
  testProvider,
  updateProvider,
  type AIProvider,
  type AssessmentJob,
  type ProviderInput,
  type ReviewReason,
  type RiskLevel,
  type RiskSummary,
  type RiskStatus,
  type ToolRiskAssessment,
} from '@/api/aiRisk'
import { riskBadgeClass, riskLevelLabel, riskStatusLabel } from '@/utils/riskLevel'

const toast = useToast()
const providers = ref<AIProvider[]>([])
const tools = ref<ToolRiskAssessment[]>([])
const jobs = ref<AssessmentJob[]>([])
const summary = ref<RiskSummary>({
  total: 0,
  low: 0,
  medium: 0,
  high: 0,
  blocked: 0,
  needsReview: 0,
})
const loading = ref(false)
const busy = ref('')
const providerOpen = ref(false)
const editingProvider = ref<AIProvider | null>(null)
const overrideTarget = ref<ToolRiskAssessment | null>(null)
const bulkOverrideOpen = ref(false)
const jobHistoryOpen = ref(false)
const selectedToolIds = ref<string[]>([])
const bulkOverrideItems = ref<ToolRiskAssessment[]>([])
const currentPage = ref(1)
const pageSize = ref<20 | 50 | 100>(20)
const filteredTotal = ref(0)
const filters = ref<{ keyword: string; level: '' | RiskLevel; status: '' | RiskStatus }>({
  keyword: '',
  level: '',
  status: '',
})
const providerForm = ref<ProviderInput>(emptyProvider())
const overrideForm = ref<{ level: RiskLevel; reason: string; tags: string; force: boolean }>({
  level: 'medium',
  reason: '',
  tags: '',
  force: false,
})
let pollTimer: number | undefined
let loadRequestSeq = 0

const activeProvider = computed(() => providers.value.find((item) => item.active && item.enabled))
const totalPages = computed(() => Math.max(1, Math.ceil(filteredTotal.value / pageSize.value)))
const pageStart = computed(() =>
  filteredTotal.value === 0 ? 0 : (currentPage.value - 1) * pageSize.value + 1,
)
const pageEnd = computed(() => Math.min(currentPage.value * pageSize.value, filteredTotal.value))
const riskLevelRank: Record<RiskLevel, number> = { low: 1, medium: 2, high: 3, blocked: 4 }
const selectedToolItems = computed(() =>
  tools.value.filter((item) => selectedToolIds.value.includes(item.id)),
)
const overrideFloor = computed<RiskLevel | null>(() => {
  if (overrideTarget.value) return overrideTarget.value.deterministicFloor
  if (!bulkOverrideOpen.value || bulkOverrideItems.value.length === 0) return null
  return bulkOverrideItems.value.reduce<RiskLevel>(
    (highest, item) =>
      riskLevelRank[item.deterministicFloor] > riskLevelRank[highest]
        ? item.deterministicFloor
        : highest,
    'low',
  )
})
const overrideBelowFloor = computed(() => {
  if (!overrideFloor.value) return false
  return riskLevelRank[overrideForm.value.level] < riskLevelRank[overrideFloor.value]
})
const overrideDowngradeIncomplete = computed(
  () =>
    overrideBelowFloor.value &&
    (!overrideForm.value.force || overrideForm.value.reason.trim() === ''),
)
const overrideSaveDisabled = computed(() => busy.value !== '' || overrideDowngradeIncomplete.value)
function emptyProvider(): ProviderInput {
  return {
    name: '',
    baseUrl: '',
    apiStyle: 'chat_completions',
    model: '',
    apiKey: '',
    enabled: true,
    timeoutS: 60,
    batchSize: 10,
    maxConcurrency: 1,
    autoAssess: false,
  }
}
function errorText(error: unknown): string {
  return error instanceof Error ? error.message : '操作失败'
}

function normalizeRiskTool(item: ToolRiskAssessment): ToolRiskAssessment {
  return { ...item, reviewReasons: item.reviewReasons ?? [] }
}

const reviewReasonLabel: Record<ReviewReason, string> = {
  insufficient_evidence: '工具描述或参数证据不足',
  ambiguous_scope: '动态参数可能跨越多个风险等级',
  conflicting_signals: '工具名称、描述与参数之间存在冲突',
  low_confidence: 'AI 置信度低于 80%',
  below_rule_floor: 'AI 建议低于确定性规则下限',
  legacy_ai_request: '历史评级由 AI 主动标记复核，尚无结构化原因',
}

async function loadAll(): Promise<void> {
  const requestSeq = ++loadRequestSeq
  const requestedPage = currentPage.value
  const requestedPageSize = pageSize.value
  loading.value = true
  try {
    const [providerRows, page, jobRows] = await Promise.all([
      listProviders(),
      listRiskTools({
        page: requestedPage,
        pageSize: requestedPageSize,
        keyword: filters.value.keyword || undefined,
        level: filters.value.level || undefined,
        status: filters.value.status || undefined,
      }),
      listAssessmentJobs(),
    ])
    if (requestSeq !== loadRequestSeq) return
    const maxPage = Math.max(1, Math.ceil(page.total / requestedPageSize))
    if (requestedPage > maxPage) {
      currentPage.value = maxPage
      selectedToolIds.value = []
      void loadAll()
      return
    }
    providers.value = providerRows
    tools.value = (page.items ?? []).map(normalizeRiskTool)
    filteredTotal.value = page.total
    summary.value = page.summary
    jobs.value = jobRows
  } catch (error) {
    if (requestSeq !== loadRequestSeq) return
    toast.error(errorText(error))
  } finally {
    if (requestSeq === loadRequestSeq) loading.value = false
  }
}

function applyFilters(): void {
  currentPage.value = 1
  selectedToolIds.value = []
  void loadAll()
}

function goToPage(page: number): void {
  const next = Math.min(Math.max(page, 1), totalPages.value)
  if (next === currentPage.value) return
  currentPage.value = next
  selectedToolIds.value = []
  void loadAll()
}

function changePageSize(): void {
  currentPage.value = 1
  selectedToolIds.value = []
  void loadAll()
}

function openProvider(provider?: AIProvider): void {
  editingProvider.value = provider ?? null
  providerForm.value = provider
    ? {
        name: provider.name,
        baseUrl: provider.baseUrl,
        apiStyle: provider.apiStyle,
        model: provider.model,
        apiKey: '',
        enabled: provider.enabled,
        timeoutS: provider.timeoutS,
        batchSize: provider.batchSize,
        maxConcurrency: provider.maxConcurrency,
        autoAssess: provider.autoAssess,
      }
    : emptyProvider()
  providerOpen.value = true
}

async function saveProvider(): Promise<void> {
  busy.value = 'provider-save'
  try {
    if (editingProvider.value) await updateProvider(editingProvider.value.id, providerForm.value)
    else await createProvider(providerForm.value)
    providerOpen.value = false
    toast.success('Provider 配置已保存')
    await loadAll()
  } catch (error) {
    toast.error(errorText(error))
  } finally {
    busy.value = ''
  }
}

async function providerAction(
  action: 'activate' | 'test' | 'delete',
  provider: AIProvider,
): Promise<void> {
  busy.value = `${action}:${provider.id}`
  try {
    if (action === 'activate') {
      await activateProvider(provider.id)
      toast.success('已切换活动 Provider')
    }
    if (action === 'test') {
      const result = await testProvider(provider.id)
      toast.success(`连接成功，耗时 ${result.latencyMs} ms`)
    }
    if (action === 'delete') {
      await deleteProvider(provider.id)
      toast.success('Provider 已删除')
    }
    await loadAll()
  } catch (error) {
    toast.error(errorText(error))
  } finally {
    busy.value = ''
  }
}

async function reconcile(): Promise<void> {
  busy.value = 'reconcile'
  try {
    const result = await reconcileRiskCatalog()
    toast.success(
      `风险目录同步完成：新增 ${result.added}，变化 ${result.changed}，移除 ${result.removed}`,
    )
    await loadAll()
  } catch (error) {
    toast.error(errorText(error))
  } finally {
    busy.value = ''
  }
}
async function assess(): Promise<void> {
  busy.value = 'assess'
  try {
    await queueAssessment()
    toast.success('评级任务已加入队列')
    await loadAll()
  } catch (error) {
    toast.error(errorText(error))
  } finally {
    busy.value = ''
  }
}
async function cancelJob(job: AssessmentJob): Promise<void> {
  try {
    await cancelAssessmentJob(job.id)
    await loadAll()
  } catch (error) {
    toast.error(errorText(error))
  }
}

function openOverride(item: ToolRiskAssessment): void {
  bulkOverrideOpen.value = false
  overrideTarget.value = item
  overrideForm.value = {
    level: item.manualLevel ?? item.aiLevel ?? item.effectiveLevel,
    reason: item.manualConfirmed ? item.manualReason : item.aiReason,
    tags: (item.manualConfirmed ? item.manualTags : item.aiTags).join(', '),
    force: item.manualForceDowngrade,
  }
}
async function refreshAISuggestion(): Promise<void> {
  if (!overrideTarget.value) return
  busy.value = 'reassess-tool'
  try {
    const updated = normalizeRiskTool(await reassessRiskTool(overrideTarget.value))
    overrideTarget.value = updated
    const index = tools.value.findIndex((item) => item.id === updated.id)
    if (index >= 0) tools.value[index] = updated
    if (!updated.manualConfirmed) {
      overrideForm.value.level = updated.aiLevel ?? updated.effectiveLevel
      overrideForm.value.tags = updated.aiTags.join(', ')
      overrideForm.value.reason = updated.aiReason
    }
    toast.success('AI 建议和中文功能说明已刷新')
  } catch (error) {
    toast.error(errorText(error))
  } finally {
    busy.value = ''
  }
}
function openBulkOverride(): void {
  bulkOverrideItems.value = selectedToolItems.value.map((item) => ({ ...item }))
  if (bulkOverrideItems.value.length === 0) return
  bulkOverrideOpen.value = true
  overrideTarget.value = null
  overrideForm.value = { level: 'medium', reason: '', tags: '', force: false }
}
function closeOverride(): void {
  overrideTarget.value = null
  bulkOverrideOpen.value = false
  bulkOverrideItems.value = []
}
async function saveOverride(): Promise<void> {
  const didBulk = bulkOverrideOpen.value
  const selected = didBulk ? bulkOverrideItems.value : selectedToolItems.value
  if (!overrideTarget.value && (!bulkOverrideOpen.value || selected.length === 0)) return
  busy.value = 'override'
  try {
    const payload = {
      level: overrideForm.value.level,
      reason: overrideForm.value.reason,
      tags: overrideForm.value.tags
        .split(',')
        .map((tag) => tag.trim())
        .filter(Boolean),
      force: overrideBelowFloor.value && overrideForm.value.force,
    }
    if (overrideTarget.value) await setManualOverride(overrideTarget.value, payload)
    else await bulkManualOverride(selected, payload)
    overrideTarget.value = null
    bulkOverrideOpen.value = false
    bulkOverrideItems.value = []
    selectedToolIds.value = []
    toast.success(didBulk ? `已批量更新 ${selected.length} 个工具` : '人工评级已保存')
    await loadAll()
  } catch (error) {
    toast.error(errorText(error))
  } finally {
    busy.value = ''
  }
}
async function clearOverride(item: ToolRiskAssessment): Promise<void> {
  try {
    await clearManualOverride(item)
    toast.success('已恢复自动评级')
    await loadAll()
  } catch (error) {
    toast.error(errorText(error))
  }
}

onMounted(() => {
  void loadAll()
  pollTimer = window.setInterval(() => {
    if (jobs.value.some((job) => job.status === 'queued' || job.status === 'running'))
      void loadAll()
  }, 3000)
})
onBeforeUnmount(() => {
  if (pollTimer) window.clearInterval(pollTimer)
})
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb page-title="AI 风险治理" />

    <div class="space-y-6">
      <section class="border-y border-gray-200 bg-white py-5 dark:border-gray-800 dark:bg-gray-900">
        <div class="flex flex-wrap items-center justify-between gap-3 px-4 sm:px-6">
          <div>
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">风险目录</h3>
            <p class="mt-1 text-sm text-gray-500">
              共 {{ summary.total }} 个来源工具，活动 Provider：{{
                activeProvider?.name ?? '未配置'
              }}
            </p>
          </div>
          <div class="flex flex-wrap gap-2">
            <button
              v-tooltip:bottom="
                '根据当前已缓存的工具列表同步风险记录；不会主动连接上游刷新工具列表。'
              "
              class="inline-flex h-10 items-center gap-2 rounded-md border border-gray-300 px-3 text-sm font-medium dark:border-gray-700"
              :disabled="busy !== ''"
              @click="reconcile"
            >
              <RefreshIcon class="h-4 w-4" />同步风险目录
            </button>
            <button
              v-tooltip:bottom="
                '只处理待评级、已变化和失败项；待复核项保留现有 AI 结果，等待人工确认。'
              "
              class="bg-brand-500 inline-flex h-10 items-center gap-2 rounded-md px-3 text-sm font-medium text-white disabled:opacity-50"
              :disabled="!activeProvider || busy !== ''"
              @click="assess"
            >
              <CheckIcon class="h-4 w-4" />开始评级
            </button>
          </div>
        </div>
        <div
          class="mt-5 grid grid-cols-2 gap-px border-y border-gray-200 bg-gray-200 sm:grid-cols-5 dark:border-gray-800 dark:bg-gray-800"
        >
          <div
            v-for="item in [
              {
                label: '低风险',
                value: summary.low,
                tooltip:
                  '只读查询，不产生持久状态变化，也不向第三方发送内容。只读、标准和特权 API Key 均可使用。',
              },
              {
                label: '中风险',
                value: summary.medium,
                tooltip:
                  '可控且通常可验证或可恢复的业务写入，例如创建或修改普通业务数据。标准和特权 API Key 可使用。',
              },
              {
                label: '高风险',
                value: summary.high,
                tooltip:
                  '包含删除、权限或凭据变更、对外发送、任意执行、基础设施调整及高影响批量操作。仅特权 API Key 可使用。',
              },
              {
                label: '禁止',
                value: summary.blocked,
                tooltip:
                  '不应长期对外暴露的能力。所有启用风险治理的 API Key 都不能发现或调用此类工具。',
              },
              {
                label: '待复核',
                value: summary.needsReview,
                tooltip:
                  '只表示判断仍有不确定性：证据不足、行为边界含糊、信号冲突、置信度低于 80%，或 AI 建议低于规则下限。高风险本身不会触发复核；该状态与风险等级重叠。',
              },
            ]"
            :key="item.label"
            v-tooltip:top="item.tooltip"
            class="cursor-help bg-white px-4 py-3 dark:bg-gray-900"
          >
            <div class="text-xs text-gray-500">{{ item.label }}</div>
            <div class="mt-1 text-xl font-semibold text-gray-900 dark:text-white">
              {{ item.value }}
            </div>
          </div>
        </div>
      </section>

      <section
        class="border-y border-gray-200 bg-white px-4 py-5 sm:px-6 dark:border-gray-800 dark:bg-gray-900"
      >
        <div class="flex items-center justify-between gap-3">
          <div>
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">AI Provider</h3>
            <p class="mt-1 text-sm text-gray-500">API Key 加密保存，编辑时留空表示保留。</p>
          </div>
          <button
            class="bg-brand-500 inline-flex h-9 items-center gap-2 rounded-md px-3 text-sm font-medium text-white"
            @click="openProvider()"
          >
            <PlusIcon class="h-4 w-4" />新增
          </button>
        </div>
        <div
          v-if="providers.length"
          class="mt-4 divide-y divide-gray-100 border-y border-gray-100 dark:divide-gray-800 dark:border-gray-800"
        >
          <div
            v-for="provider in providers"
            :key="provider.id"
            class="flex flex-col gap-3 py-4 lg:flex-row lg:items-center lg:justify-between"
          >
            <div class="min-w-0">
              <div class="flex items-center gap-2">
                <span class="font-medium text-gray-900 dark:text-white">{{ provider.name }}</span
                ><span
                  v-if="provider.active && provider.enabled"
                  class="bg-success-50 text-success-700 rounded px-2 py-0.5 text-xs"
                  >活动</span
                ><span v-if="!provider.enabled" class="text-xs text-gray-400">已停用</span>
              </div>
              <p class="mt-1 truncate text-sm text-gray-500">
                {{ provider.model }} · {{ provider.baseUrl }} ·
                {{ provider.apiKeyMasked || '无密钥' }}
              </p>
            </div>
            <div class="flex flex-wrap gap-2 text-sm">
              <button
                class="rounded-md border px-3 py-1.5 dark:border-gray-700"
                @click="openProvider(provider)"
              >
                编辑</button
              ><button
                class="rounded-md border px-3 py-1.5 dark:border-gray-700"
                @click="providerAction('test', provider)"
              >
                测试</button
              ><button
                v-if="provider.enabled && !provider.active"
                class="rounded-md border px-3 py-1.5 dark:border-gray-700"
                @click="providerAction('activate', provider)"
              >
                设为活动</button
              ><button
                class="border-error-200 text-error-600 inline-flex items-center rounded-md border p-2"
                aria-label="删除 Provider"
                @click="providerAction('delete', provider)"
              >
                <TrashIcon class="h-4 w-4" />
              </button>
            </div>
          </div>
        </div>
        <p
          v-else
          class="mt-4 border-y border-dashed border-gray-200 py-8 text-center text-sm text-gray-500 dark:border-gray-800"
        >
          尚未配置 Provider
        </p>
      </section>

      <section
        v-if="jobs.length"
        class="border-y border-gray-200 bg-white px-4 py-5 sm:px-6 dark:border-gray-800 dark:bg-gray-900"
      >
        <div class="flex items-center justify-between gap-3">
          <h3 class="text-base font-semibold text-gray-900 dark:text-white">最近评级任务</h3>
          <button
            v-if="jobs.length > 1"
            class="text-brand-600 dark:text-brand-400 text-sm font-medium"
            @click="jobHistoryOpen = true"
          >
            查看历史（{{ jobs.length - 1 }}）
          </button>
        </div>
        <RiskJobCard class="mt-4" :job="jobs[0]!" @cancel="cancelJob" />
      </section>

      <section
        class="border-y border-gray-200 bg-white px-4 py-5 sm:px-6 dark:border-gray-800 dark:bg-gray-900"
      >
        <div class="grid gap-3 md:grid-cols-[minmax(0,1fr)_180px_180px_auto]">
          <input
            v-model="filters.keyword"
            class="h-10 rounded-md border-gray-300 text-sm dark:border-gray-700 dark:bg-gray-900"
            placeholder="搜索原始名、对外名或描述"
            @keyup.enter="applyFilters"
          />
          <select
            v-model="filters.level"
            class="h-10 rounded-md border-gray-300 text-sm dark:border-gray-700 dark:bg-gray-900"
          >
            <option value="">全部等级</option>
            <option
              v-for="level in ['low', 'medium', 'high', 'blocked'] as RiskLevel[]"
              :key="level"
              :value="level"
            >
              {{ riskLevelLabel[level] }}
            </option>
          </select>
          <select
            v-model="filters.status"
            class="h-10 rounded-md border-gray-300 text-sm dark:border-gray-700 dark:bg-gray-900"
          >
            <option value="">全部状态</option>
            <option
              v-for="status in [
                'pending',
                'rated',
                'needs_review',
                'stale',
                'error',
                'removed',
              ] as RiskStatus[]"
              :key="status"
              :value="status"
            >
              {{ riskStatusLabel[status] }}
            </option>
          </select>
          <button
            class="h-10 rounded-md border px-4 text-sm dark:border-gray-700"
            @click="applyFilters"
          >
            筛选
          </button>
        </div>

        <div
          v-if="selectedToolIds.length"
          class="mt-3 flex items-center justify-between border-y border-gray-100 py-2 text-sm dark:border-gray-800"
        >
          <span>已选择 {{ selectedToolIds.length }} 个工具</span>
          <button
            class="bg-brand-500 rounded-md px-3 py-1.5 font-medium text-white"
            @click="openBulkOverride"
          >
            批量覆盖
          </button>
        </div>

        <div class="mt-5 divide-y divide-gray-100 dark:divide-gray-800">
          <article
            v-for="item in tools"
            :key="item.id"
            class="grid gap-3 py-4 lg:grid-cols-[minmax(0,1fr)_160px_180px_auto] lg:items-center"
          >
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <input
                  v-model="selectedToolIds"
                  type="checkbox"
                  :value="item.id"
                  :aria-label="`选择 ${item.originalName}`"
                />
                <span class="font-medium text-gray-900 dark:text-white">{{
                  item.originalName
                }}</span
                ><span
                  :class="[
                    'rounded px-2 py-0.5 text-xs font-medium',
                    riskBadgeClass(item.effectiveLevel),
                  ]"
                  >{{ riskLevelLabel[item.effectiveLevel] }}</span
                ><span class="text-xs text-gray-500">{{ riskStatusLabel[item.status] }}</span>
              </div>
              <p
                v-if="item.status === 'needs_review' && item.reviewReasons.length"
                class="text-warning-600 dark:text-warning-400 mt-1 text-xs"
              >
                待复核：{{
                  item.reviewReasons.map((reason) => reviewReasonLabel[reason]).join('；')
                }}
              </p>
              <p class="mt-1 line-clamp-2 text-sm text-gray-500">
                {{ item.description || '无描述' }}
              </p>
              <p class="mt-1 truncate text-xs text-gray-400">
                {{ item.upstreamId }} · {{ item.exposedName }}
              </p>
            </div>
            <div class="text-sm">
              <div class="text-gray-500">AI / 下限</div>
              <div class="mt-1 text-gray-800 dark:text-gray-200">
                {{ item.aiLevel ? riskLevelLabel[item.aiLevel] : '未评级' }} /
                {{ riskLevelLabel[item.deterministicFloor] }}
              </div>
            </div>
            <div class="text-sm">
              <div class="text-gray-500">置信度 / 模型</div>
              <div class="mt-1 truncate text-gray-800 dark:text-gray-200">
                {{ item.aiConfidence == null ? '—' : `${Math.round(item.aiConfidence * 100)}%` }} ·
                {{ item.model || '—' }}
              </div>
            </div>
            <div class="flex gap-2">
              <button
                class="rounded-md border px-3 py-1.5 text-sm dark:border-gray-700"
                @click="openOverride(item)"
              >
                人工覆盖</button
              ><button
                v-if="item.manualConfirmed"
                class="text-error-600 rounded-md border px-3 py-1.5 text-sm dark:border-gray-700"
                @click="clearOverride(item)"
              >
                清除
              </button>
            </div>
          </article>
          <p v-if="!loading && tools.length === 0" class="py-10 text-center text-sm text-gray-500">
            没有匹配的风险记录
          </p>
        </div>

        <div
          v-if="filteredTotal > 0"
          class="mt-5 flex flex-col gap-3 border-t border-gray-100 pt-4 sm:flex-row sm:items-center sm:justify-between dark:border-gray-800"
        >
          <div class="flex flex-wrap items-center gap-3 text-sm text-gray-500">
            <span>显示 {{ pageStart }}–{{ pageEnd }}，共 {{ filteredTotal }} 条</span>
            <label class="inline-flex items-center gap-2">
              每页
              <select
                v-model.number="pageSize"
                class="h-9 rounded-md border-gray-300 py-1 pr-8 pl-3 text-sm dark:border-gray-700 dark:bg-gray-900"
                @change="changePageSize"
              >
                <option :value="20">20</option>
                <option :value="50">50</option>
                <option :value="100">100</option>
              </select>
              条
            </label>
          </div>
          <div class="flex items-center justify-between gap-2 sm:justify-end">
            <button
              type="button"
              class="h-9 rounded-md border px-3 text-sm transition disabled:cursor-not-allowed disabled:opacity-40 dark:border-gray-700"
              :disabled="currentPage <= 1 || loading"
              @click="goToPage(currentPage - 1)"
            >
              上一页
            </button>
            <span class="min-w-20 text-center text-sm text-gray-600 dark:text-gray-300">
              {{ currentPage }} / {{ totalPages }}
            </span>
            <button
              type="button"
              class="h-9 rounded-md border px-3 text-sm transition disabled:cursor-not-allowed disabled:opacity-40 dark:border-gray-700"
              :disabled="currentPage >= totalPages || loading"
              @click="goToPage(currentPage + 1)"
            >
              下一页
            </button>
          </div>
        </div>
      </section>
    </div>

    <div
      v-if="jobHistoryOpen"
      class="fixed inset-0 z-[100001] flex items-center justify-center bg-black/40 p-4"
    >
      <div
        class="flex max-h-[85dvh] w-full max-w-3xl flex-col rounded-md bg-white shadow-xl dark:bg-gray-900"
      >
        <div
          class="flex items-center justify-between border-b border-gray-200 px-5 py-4 dark:border-gray-800"
        >
          <div>
            <h3 class="text-base font-semibold text-gray-900 dark:text-white">评级任务历史</h3>
            <p class="mt-1 text-xs text-gray-500">共 {{ jobs.length - 1 }} 条较早记录</p>
          </div>
          <button
            type="button"
            class="rounded-md border px-3 py-1.5 text-sm dark:border-gray-700"
            @click="jobHistoryOpen = false"
          >
            关闭
          </button>
        </div>
        <div class="overflow-y-auto px-5">
          <RiskJobCard
            v-for="job in jobs.slice(1)"
            :key="job.id"
            :job="job"
            class="border-b border-gray-100 py-5 last:border-b-0 dark:border-gray-800"
            @cancel="cancelJob"
          />
        </div>
      </div>
    </div>

    <div
      v-if="providerOpen"
      class="fixed inset-0 z-[100001] flex items-center justify-center bg-black/40 p-4"
    >
      <form
        class="max-h-[90dvh] w-full max-w-2xl overflow-y-auto rounded-md bg-white p-5 shadow-xl dark:bg-gray-900"
        @submit.prevent="saveProvider"
      >
        <h3 class="text-base font-semibold dark:text-white">
          {{ editingProvider ? '编辑 Provider' : '新增 Provider' }}
        </h3>
        <div class="mt-4 grid gap-4 sm:grid-cols-2">
          <label class="text-sm"
            >名称<input
              v-model="providerForm.name"
              required
              class="mt-1 w-full rounded-md border-gray-300 dark:bg-gray-900" /></label
          ><label class="text-sm"
            >模型<input
              v-model="providerForm.model"
              required
              class="mt-1 w-full rounded-md border-gray-300 dark:bg-gray-900" /></label
          ><label class="text-sm sm:col-span-2"
            >Base URL<input
              v-model="providerForm.baseUrl"
              required
              placeholder="http://127.0.0.1:11434/v1"
              class="mt-1 w-full rounded-md border-gray-300 dark:bg-gray-900"
          /></label>
          <div>
            <FieldLabel
              label="API 接口协议"
              tooltip="决定请求端点和数据格式：Chat Completions 使用 /chat/completions，Responses 使用 /responses。多数 OpenAI 兼容服务选择 Chat Completions。"
            />
            <select
              v-model="providerForm.apiStyle"
              class="w-full rounded-md border-gray-300 dark:bg-gray-900"
            >
              <option value="chat_completions">Chat Completions</option>
              <option value="responses">Responses</option>
            </select>
          </div>
          <label class="text-sm"
            >API Key<input
              v-model="providerForm.apiKey"
              type="password"
              autocomplete="new-password"
              class="mt-1 w-full rounded-md border-gray-300 dark:bg-gray-900"
              placeholder="留空保留现有密钥" /></label
          ><label class="text-sm"
            >超时（秒）<input
              v-model.number="providerForm.timeoutS"
              type="number"
              min="1"
              max="300"
              class="mt-1 w-full rounded-md border-gray-300 dark:bg-gray-900"
          /></label>
          <div>
            <FieldLabel
              label="单批工具数"
              tooltip="一次 AI 请求包含的工具数量。批次越大，请求越少，但提示词更长，单批失败影响的工具也更多。"
            />
            <input
              v-model.number="providerForm.batchSize"
              type="number"
              min="1"
              max="50"
              class="w-full rounded-md border-gray-300 dark:bg-gray-900"
            />
          </div>
          <div>
            <FieldLabel
              label="最大并发数"
              tooltip="允许同时执行的 AI 请求数，范围为 1–3。并发越高通常越快，但更容易触发 Provider 限流或服务繁忙。"
            />
            <input
              v-model.number="providerForm.maxConcurrency"
              type="number"
              min="1"
              max="3"
              class="w-full rounded-md border-gray-300 dark:bg-gray-900"
            />
          </div>
          <div class="grid gap-2 sm:col-span-2">
            <div
              class="flex items-center justify-between gap-4 rounded-lg border border-gray-200 px-3 py-2.5 dark:border-gray-700"
            >
              <FieldLabel
                class="!mb-0"
                label="启用 Provider"
                tooltip="关闭后该 Provider 不可被设为活动配置，也不会用于测试、评级或生成 AI 建议。"
              />
              <button
                type="button"
                role="switch"
                :aria-checked="providerForm.enabled"
                class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition"
                :class="providerForm.enabled ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
                @click="providerForm.enabled = !providerForm.enabled"
              >
                <span
                  class="inline-block h-4 w-4 transform rounded-full bg-white transition"
                  :class="providerForm.enabled ? 'translate-x-6' : 'translate-x-1'"
                ></span>
              </button>
            </div>

            <div
              class="flex items-center justify-between gap-4 rounded-lg border border-gray-200 px-3 py-2.5 dark:border-gray-700"
            >
              <FieldLabel
                class="!mb-0"
                label="同步后自动评级"
                tooltip="工具目录同步完成后，自动为待评级、已变化和失败的工具创建评级任务；待复核项不会重复评级。"
              />
              <button
                type="button"
                role="switch"
                :aria-checked="providerForm.autoAssess"
                class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition"
                :class="providerForm.autoAssess ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
                @click="providerForm.autoAssess = !providerForm.autoAssess"
              >
                <span
                  class="inline-block h-4 w-4 transform rounded-full bg-white transition"
                  :class="providerForm.autoAssess ? 'translate-x-6' : 'translate-x-1'"
                ></span>
              </button>
            </div>
          </div>
        </div>
        <div class="mt-6 flex justify-end gap-2">
          <button
            type="button"
            class="rounded-md border px-4 py-2 text-sm dark:border-gray-700"
            @click="providerOpen = false"
          >
            取消</button
          ><button
            class="bg-brand-500 rounded-md px-4 py-2 text-sm font-medium text-white"
            :disabled="busy !== ''"
          >
            保存
          </button>
        </div>
      </form>
    </div>

    <div
      v-if="overrideTarget || bulkOverrideOpen"
      class="fixed inset-0 z-[100001] flex items-center justify-center bg-black/40 p-4"
    >
      <form
        class="max-h-[90dvh] w-full max-w-3xl overflow-y-auto rounded-md bg-white p-5 shadow-xl dark:bg-gray-900"
        @submit.prevent="saveOverride"
      >
        <h3 class="text-base font-semibold dark:text-white">
          {{
            bulkOverrideOpen
              ? `批量覆盖 ${bulkOverrideItems.length} 个工具`
              : `人工评级 · ${overrideTarget?.originalName}`
          }}
        </h3>
        <div
          v-if="overrideTarget"
          class="mt-4 space-y-4 rounded-md border border-gray-200 bg-gray-50 p-4 dark:border-gray-700 dark:bg-gray-950"
        >
          <div class="flex flex-wrap items-start justify-between gap-3">
            <div>
              <div class="text-xs font-medium tracking-wide text-gray-500">工具功能（中文）</div>
              <p
                v-if="overrideTarget.descriptionZh"
                class="mt-1 text-sm leading-6 text-gray-900 dark:text-gray-100"
              >
                {{ overrideTarget.descriptionZh }}
              </p>
              <p v-else class="text-warning-600 dark:text-warning-400 mt-1 text-sm">
                这条历史记录还没有中文功能说明，可点击右侧按钮生成。
              </p>
            </div>
            <button
              type="button"
              class="border-brand-200 text-brand-600 dark:border-brand-800 dark:text-brand-400 shrink-0 rounded-md border px-3 py-1.5 text-sm disabled:opacity-50"
              :disabled="busy !== ''"
              @click="refreshAISuggestion"
            >
              {{ busy === 'reassess-tool' ? '正在生成…' : '生成/刷新 AI 建议' }}
            </button>
          </div>
          <p class="text-xs leading-5 text-gray-500">
            此操作会调用当前活动 Provider，重新生成该工具的中文功能说明、风险等级、标签和判断理由。
          </p>

          <div>
            <div class="text-xs font-medium tracking-wide text-gray-500">工具原始描述</div>
            <p class="mt-1 text-sm leading-6 whitespace-pre-wrap text-gray-700 dark:text-gray-300">
              {{ overrideTarget.description || '无描述' }}
            </p>
          </div>

          <div class="grid gap-3 sm:grid-cols-4">
            <div>
              <div class="text-xs text-gray-500">AI 建议等级</div>
              <div class="mt-1 font-medium text-gray-900 dark:text-white">
                {{ overrideTarget.aiLevel ? riskLevelLabel[overrideTarget.aiLevel] : '未评级' }}
              </div>
            </div>
            <div>
              <div class="text-xs text-gray-500">规则下限</div>
              <div class="mt-1 font-medium text-gray-900 dark:text-white">
                {{ riskLevelLabel[overrideTarget.deterministicFloor] }}
              </div>
            </div>
            <div>
              <div class="text-xs text-gray-500">AI 置信度</div>
              <div class="mt-1 font-medium text-gray-900 dark:text-white">
                {{
                  overrideTarget.aiConfidence == null
                    ? '—'
                    : Math.round(overrideTarget.aiConfidence * 100) + '%'
                }}
              </div>
            </div>
            <div>
              <div class="text-xs text-gray-500">当前有效等级</div>
              <div class="mt-1 font-medium text-gray-900 dark:text-white">
                {{ riskLevelLabel[overrideTarget.effectiveLevel] }}
              </div>
            </div>
          </div>

          <div>
            <div class="text-xs font-medium tracking-wide text-gray-500">AI 判断理由</div>
            <p class="mt-1 text-sm leading-6 whitespace-pre-wrap text-gray-900 dark:text-gray-100">
              {{ overrideTarget.aiReason || 'AI 尚未提供判断理由' }}
            </p>
          </div>

          <div
            v-if="overrideTarget.reviewReasons.length"
            class="border-warning-200 bg-warning-50 dark:border-warning-800 dark:bg-warning-950/30 rounded-md border p-3"
          >
            <div class="text-warning-700 dark:text-warning-300 text-xs font-medium">待复核原因</div>
            <ul
              class="text-warning-700 dark:text-warning-300 mt-1 list-disc space-y-1 pl-5 text-sm"
            >
              <li v-for="reason in overrideTarget.reviewReasons" :key="reason">
                {{ reviewReasonLabel[reason] }}
              </li>
            </ul>
          </div>

          <div v-if="overrideTarget.aiTags.length" class="flex flex-wrap gap-2">
            <span
              v-for="tag in overrideTarget.aiTags"
              :key="tag"
              class="rounded bg-gray-200 px-2 py-1 text-xs text-gray-700 dark:bg-gray-800 dark:text-gray-300"
            >
              {{ tag }}
            </span>
          </div>
          <p class="text-xs text-gray-500">
            Provider：{{ overrideTarget.providerName || '—' }} · 模型：{{
              overrideTarget.model || '—'
            }}
          </p>
          <p v-if="overrideTarget.lastError" class="text-error-600 text-xs leading-5">
            最近错误：{{ overrideTarget.lastError }}
          </p>
        </div>
        <h4 class="mt-5 text-sm font-semibold text-gray-900 dark:text-white">人工复核结论</h4>
        <div class="mt-4 space-y-4">
          <label class="block text-sm"
            >风险等级<select
              v-model="overrideForm.level"
              class="mt-1 w-full rounded-md border-gray-300 dark:bg-gray-900"
            >
              <option
                v-for="level in ['low', 'medium', 'high', 'blocked'] as RiskLevel[]"
                :key="level"
                :value="level"
              >
                {{ riskLevelLabel[level] }}
              </option>
            </select></label
          ><label class="block text-sm"
            >标签<input
              v-model="overrideForm.tags"
              class="mt-1 w-full rounded-md border-gray-300 dark:bg-gray-900"
              placeholder="write, external_network" /></label
          ><label class="block text-sm"
            >复核理由<textarea
              v-model="overrideForm.reason"
              rows="3"
              class="mt-1 w-full rounded-md border-gray-300 dark:bg-gray-900"
            ></textarea></label
          ><label
            v-if="overrideBelowFloor"
            class="flex items-start gap-2 text-sm"
            :class="'border-warning-300 bg-warning-50 dark:border-warning-800 dark:bg-warning-950/30 rounded-md border p-3'"
            ><input v-model="overrideForm.force" type="checkbox" class="mt-0.5" /><span
              ><span>确认将等级降到确定性下限以下；此操作必须填写理由并进入审计。</span>
              <span
                v-if="overrideBelowFloor"
                class="text-warning-700 dark:text-warning-300 mt-1 block font-medium"
              >
                当前选择的“{{ riskLevelLabel[overrideForm.level] }}”低于{{
                  bulkOverrideOpen ? '所选工具中的最高规则下限' : '规则下限'
                }}“{{ riskLevelLabel[overrideFloor!] }}”。{{
                  !overrideForm.force
                    ? '请勾选本项确认强制降级。'
                    : overrideForm.reason.trim() === ''
                      ? '已确认强制降级，还需填写复核理由。'
                      : '已满足强制降级条件。'
                }}
              </span></span
            ></label
          >
        </div>
        <div class="mt-6 flex justify-end gap-2">
          <button
            type="button"
            class="rounded-md border px-4 py-2 text-sm dark:border-gray-700"
            @click="closeOverride"
          >
            取消</button
          ><button
            class="rounded-md px-4 py-2 text-sm font-medium transition-colors"
            :class="
              overrideSaveDisabled
                ? 'cursor-not-allowed bg-gray-300 text-gray-500 dark:bg-gray-700 dark:text-gray-400'
                : 'bg-brand-500 hover:bg-brand-600 text-white'
            "
            :disabled="overrideSaveDisabled"
          >
            保存覆盖
          </button>
        </div>
      </form>
    </div>
  </AdminLayout>
</template>
