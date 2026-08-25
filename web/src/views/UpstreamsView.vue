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
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import AppSelect from '@/components/common/AppSelect.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import ConnStateBadge from '@/components/upstreams/ConnStateBadge.vue'
import UpstreamFormDrawer from '@/components/upstreams/UpstreamFormDrawer.vue'
import UpstreamOnboardingPanel from '@/components/upstreams/UpstreamOnboardingPanel.vue'
import TemplateMarketModal from '@/components/upstreams/TemplateMarketModal.vue'
import { useBreakpoint } from '@/composables/useBreakpoint'
import { useConfirm } from '@/composables/useConfirm'
import { useToast } from '@/composables/useToast'
import { preflightRuntime, type RuntimePreflightResult } from '@/api/runtime'
import { normalizeRequirements } from '@/utils/runtimeRequirements'
import {
  normalizeSecurityProfile,
  preflightReadyLabelEx,
  preflightToneEx,
  securityModeBadgeClass,
  securityModeLabel,
  securityRiskLabel,
} from '@/utils/stdioSecurity'
import {
  listUpstreams,
  setUpstreamEnabled,
  deleteUpstream,
  listUpstreamToolSummaries,
  listUpstreamTools,
  reorderUpstreams,
  refreshUpstream,
  reconnectUpstream,
  previewUpstreamImport,
  importUpstreamsFromJSON,
  exportUpstreamsMCPJSON,
  CONN_STATE_LABELS,
  TRANSPORT_OPTIONS,
  type Upstream,
  type UpstreamImportItem,
  type UpstreamImportResultItem,
} from '@/api/upstreams'
import type { ToolDef } from '@/api/tools'
import type { PrefillForm } from '@/api/templates'
import {
  buildToolCountSnapshot,
  toolChangeSummaryLabel,
  toolCountLabel,
  type ToolCountSnapshot,
} from '@/utils/toolCountSnapshot'
import { buildUpstreamDetailSummary } from '@/utils/upstreamDetailSummary'
import {
  formatFileSize,
  MAX_UPSTREAM_IMPORT_FILE_BYTES,
  validateUpstreamImportFile,
} from '@/utils/upstreamImportFile'
import { suggestUniqueUpstreamName } from '@/utils/upstreamCopy'
import {
  collectAllTags,
  countUntagged,
  filterUpstreamsByTag,
  groupUpstreamsByTag,
  type UpstreamTagFilter,
} from '@/utils/upstreamTags'
import { ChevronDownIcon, ListIcon, FolderIcon } from '@/icons'

type UpstreamViewMode = 'list' | 'group'

const { pageSize } = useBreakpoint()
const toast = useToast()
const { confirm } = useConfirm()

/** 全量上游列表（按 sortOrder 升序）。 */
const upstreams = ref<Upstream[]>([])
const toolCounts = ref<Record<string, ToolCountSnapshot>>({})
/** 列表加载/错误状态。 */
const loading = ref(false)
const errorMessage = ref('')
/** 行级操作进行中标记（key = upstream id + action）。 */
const busy = ref<Set<string>>(new Set())

/** 分页：当前页（1 起）。 */
const currentPage = ref(1)
const searchKeyword = ref('')
/** 标签筛选：all | untagged | 具体标签。 */
const selectedTagFilter = ref<UpstreamTagFilter>('all')
/** 展示模式：列表 / 分组。 */
const viewMode = ref<UpstreamViewMode>('list')
/** 分组视图中折叠的分区 key。 */
const collapsedGroupKeys = ref<Set<string>>(new Set())

/** 抽屉与弹窗开关。 */
const drawerOpen = ref(false)
const marketOpen = ref(false)
const importOpen = ref(false)
/** 当前编辑目标与模板预填充。 */
const editing = ref<Upstream | null>(null)
const prefill = ref<PrefillForm | null>(null)
/** 复制来源与建议名称。 */
const cloneSource = ref<Upstream | null>(null)
const cloneName = ref('')

const importContent = ref('')
const importPreviewItems = ref<UpstreamImportItem[]>([])
const importCreated = ref<UpstreamImportResultItem[]>([])
const importFailed = ref<UpstreamImportResultItem[]>([])
const importLoading = ref(false)
const importExecuting = ref(false)
const importFileReading = ref(false)
const importError = ref('')
const importFileName = ref('')
const exportingMCPJSON = ref(false)

const sortingOpen = ref(false)
const sortDraft = ref<Upstream[]>([])
const sortDraggingID = ref<string | null>(null)
const sortDragOverID = ref<string | null>(null)
const sortSelectedID = ref('')
const sortTargetPosition = ref('')
const sortMoveMessage = ref('')
const sortSelectOptions = computed(() =>
  sortDraft.value.map((upstream, index) => ({
    value: upstream.id,
    label: String(index + 1) + '. ' + upstream.config.name,
  })),
)

const toolModalOpen = ref(false)
const toolModalUpstream = ref<Upstream | null>(null)
const toolModalTools = ref<ToolDef[]>([])
const toolModalUpdatedAt = ref<string | null>(null)
const toolModalLoading = ref(false)
const toolModalError = ref('')
const toolModalSearchKeyword = ref('')
const detailOpen = ref(false)
const detailUpstream = ref<Upstream | null>(null)
const onboardingUpstream = ref<Upstream | null>(null)

let statusPollTimer: number | undefined
let statusPollingUntil = 0
let recoveryClockTimer: number | undefined
const recoveryClock = ref(Date.now())
const recoverySchedules = new Map<string, { retryAt: number; startedAt: number }>()

const normalizedSearchKeyword = computed(() => searchKeyword.value.trim().toLowerCase())
const hasSearchKeyword = computed(() => normalizedSearchKeyword.value !== '')
const hasTagFilter = computed(() => selectedTagFilter.value !== 'all')
const searchedUpstreams = computed(() => {
  const keyword = normalizedSearchKeyword.value
  if (keyword === '') return upstreams.value
  return upstreams.value.filter((up) => upstreamSearchText(up).includes(keyword))
})
const filteredUpstreams = computed(() =>
  filterUpstreamsByTag(searchedUpstreams.value, selectedTagFilter.value),
)
const tagCounts = computed(() => collectAllTags(upstreams.value))
const untaggedCount = computed(() => countUntagged(upstreams.value))
const allTags = computed(() => tagCounts.value.map((item) => item.tag))
const tagFilterOptions = computed(() => {
  const options: Array<{ key: string; value: UpstreamTagFilter; label: string; count: number }> = [
    { key: 'all', value: 'all', label: '全部', count: upstreams.value.length },
  ]
  if (untaggedCount.value > 0 || selectedTagFilter.value === 'untagged') {
    options.push({
      key: 'untagged',
      value: 'untagged',
      label: '未分组',
      count: untaggedCount.value,
    })
  }
  for (const item of tagCounts.value) {
    options.push({
      key: `tag:${item.tag.toLowerCase()}`,
      value: item.tag,
      label: item.tag,
      count: item.count,
    })
  }
  return options
})
const showTagFilterBar = computed(
  () => tagCounts.value.length > 0 || untaggedCount.value > 0 || hasTagFilter.value,
)
/**
 * 分组视图数据。
 * 已按「具体标签 / 未分组」筛选时，只展示对应单一分区，
 * 避免多标签上游把其它标签分区也带出来造成认知偏差。
 */
const groupedUpstreams = computed(() => {
  const list = filteredUpstreams.value
  const selected = selectedTagFilter.value
  if (selected === 'untagged') {
    return [
      {
        tag: null,
        key: 'untagged',
        label: '未分组',
        items: [...list],
      },
    ]
  }
  if (selected !== 'all' && selected !== '') {
    return [
      {
        tag: selected,
        key: `tag:${selected.toLowerCase()}`,
        label: selected,
        items: [...list],
      },
    ]
  }
  return groupUpstreamsByTag(list)
})
const upstreamCountLabel = computed(() => {
  const total = upstreams.value.length
  const matched = filteredUpstreams.value.length
  if (!hasSearchKeyword.value && !hasTagFilter.value) {
    return `共 ${total} 个上游 MCP 服务`
  }
  const parts: string[] = [`匹配 ${matched}`]
  if (hasTagFilter.value) {
    if (selectedTagFilter.value === 'untagged') parts.push('未分组')
    else parts.push(`标签「${selectedTagFilter.value}」`)
  }
  if (hasSearchKeyword.value) parts.push('搜索')
  return `${parts.join(' · ')} / 共 ${total} 个`
})

/** 总页数（仅列表模式分页）。 */
const totalPages = computed(() =>
  Math.max(1, Math.ceil(filteredUpstreams.value.length / pageSize.value)),
)

/** 当前页展示的上游切片（列表模式）。 */
const pagedUpstreams = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return filteredUpstreams.value.slice(start, start + pageSize.value)
})

const nextSortOrder = computed(
  () => upstreams.value.reduce((max, up) => Math.max(max, up.config.sortOrder), -1) + 1,
)
const hasRecoveringUpstream = computed(() =>
  upstreams.value.some((up) => up.state === 'connecting' || up.nextRetryAt != null),
)
const normalizedToolModalSearchKeyword = computed(() =>
  toolModalSearchKeyword.value.trim().toLowerCase(),
)
const hasToolModalSearchKeyword = computed(() => normalizedToolModalSearchKeyword.value !== '')
const filteredToolModalTools = computed(() => {
  const keyword = normalizedToolModalSearchKeyword.value
  if (keyword === '') return toolModalTools.value
  return toolModalTools.value.filter((tool) => toolSearchText(tool).includes(keyword))
})
const toolModalCountLabel = computed(() => {
  if (!hasToolModalSearchKeyword.value) return `共 ${toolModalTools.value.length} 个`
  return `匹配 ${filteredToolModalTools.value.length} / 共 ${toolModalTools.value.length} 个`
})
const importCanPreview = computed(
  () => importContent.value.trim() !== '' && !importLoading.value && !importExecuting.value,
)
const importCanSubmit = computed(
  () => importPreviewItems.value.length > 0 && !importLoading.value && !importExecuting.value,
)
const importResultLabel = computed(() => {
  const created = importCreated.value.length
  const failed = importFailed.value.length
  if (created === 0 && failed === 0) return ''
  if (failed === 0) return `已导入 ${created} 个上游`
  return `已导入 ${created} 个，${failed} 个需要处理`
})
const detailSummary = computed(() =>
  detailUpstream.value === null
    ? null
    : buildUpstreamDetailSummary(detailUpstream.value, toolCounts.value[detailUpstream.value.id]),
)

const detailPreflight = ref<RuntimePreflightResult | null>(null)
const detailPreflightLoading = ref(false)
let detailPreflightSeq = 0

const detailIsStdio = computed(() => detailUpstream.value?.config.transport === 'stdio')

const detailPreflightBanner = computed(() => {
  if (!detailIsStdio.value || detailPreflight.value === null) return null
  const p = detailPreflight.value
  return {
    tone: preflightToneEx(p.ready, p.stdioEnabled, p.commandAllowed, p.securityOk, p.riskLevel),
    label: preflightReadyLabelEx(
      p.ready,
      p.stdioEnabled,
      p.commandAllowed,
      p.securityOk,
      p.securityError,
    ),
    mode: p.securityMode || 'standard',
    risk: securityRiskLabel(p.riskLevel),
  }
})

async function loadDetailPreflight(): Promise<void> {
  const u = detailUpstream.value
  if (u === null || u.config.transport !== 'stdio') {
    detailPreflight.value = null
    return
  }
  const seq = ++detailPreflightSeq
  detailPreflightLoading.value = true
  try {
    const rr = normalizeRequirements(u.config.connParams.runtimeRequirements)
    const sp = normalizeSecurityProfile(u.config.connParams.securityProfile)
    const args = Array.isArray(u.config.connParams.args)
      ? u.config.connParams.args.filter((a): a is string => typeof a === 'string')
      : []
    const result = await preflightRuntime({
      transport: 'stdio',
      command: typeof u.config.connParams.command === 'string' ? u.config.connParams.command : '',
      args,
      cwd: typeof u.config.connParams.cwd === 'string' ? u.config.connParams.cwd : undefined,
      requirements: {
        mode: rr.mode,
        tools: rr.tools,
        ...(rr.note ? { note: rr.note } : {}),
      },
      securityProfile: sp,
    })
    if (seq === detailPreflightSeq) detailPreflight.value = result
  } catch {
    if (seq === detailPreflightSeq) detailPreflight.value = null
  } finally {
    if (seq === detailPreflightSeq) detailPreflightLoading.value = false
  }
}

function depChipClass(available: boolean): string {
  return available
    ? 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
    : 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
}

watch(
  () => {
    const u = detailUpstream.value
    if (u === null) return ''
    const rr = u.config.connParams?.runtimeRequirements
    const sp = u.config.connParams?.securityProfile
    return [
      u.id,
      u.config.transport,
      typeof u.config.connParams?.command === 'string' ? u.config.connParams.command : '',
      typeof u.config.connParams?.cwd === 'string' ? u.config.connParams.cwd : '',
      JSON.stringify(u.config.connParams?.args ?? null),
      JSON.stringify(rr ?? null),
      JSON.stringify(sp ?? null),
    ].join('|')
  },
  () => {
    detailPreflightSeq += 1
    void loadDetailPreflight()
  },
)

/** 传输类型显示名。 */
function transportLabel(value: string): string {
  return TRANSPORT_OPTIONS.find((o) => o.value === value)?.label ?? value
}

function isTagFilterActive(value: UpstreamTagFilter): boolean {
  if (value === 'all' || value === 'untagged') return selectedTagFilter.value === value
  return (
    selectedTagFilter.value !== 'all' &&
    selectedTagFilter.value !== 'untagged' &&
    selectedTagFilter.value.toLowerCase() === value.toLowerCase()
  )
}

function selectTagFilter(value: UpstreamTagFilter): void {
  selectedTagFilter.value = value
  currentPage.value = 1
}

function clearFilters(): void {
  searchKeyword.value = ''
  selectedTagFilter.value = 'all'
  currentPage.value = 1
}

function setViewMode(mode: UpstreamViewMode): void {
  viewMode.value = mode
  currentPage.value = 1
}

function isGroupCollapsed(key: string): boolean {
  return collapsedGroupKeys.value.has(key)
}

function toggleGroupCollapsed(key: string): void {
  const next = new Set(collapsedGroupKeys.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  collapsedGroupKeys.value = next
}

function filterByTag(tag: string): void {
  selectTagFilter(tag)
  // 详情内点选标签时关闭详情，避免筛选变化被遮罩挡住。
  if (detailOpen.value) closeDetail()
}

/** 当前选中的具体标签是否仍存在于列表中。 */
function isSelectedTagPresent(): boolean {
  const selected = selectedTagFilter.value
  if (selected === 'all' || selected === 'untagged') return true
  const key = selected.trim().toLowerCase()
  if (key === '') return false
  return tagCounts.value.some((item) => item.tag.toLowerCase() === key)
}

function upstreamSearchText(up: Upstream): string {
  const connParams =
    up.config.connParams !== null &&
    up.config.connParams !== undefined &&
    typeof up.config.connParams === 'object' &&
    !Array.isArray(up.config.connParams)
      ? up.config.connParams
      : {}
  return [
    up.id,
    up.config.name,
    up.config.transport,
    transportLabel(up.config.transport),
    connParams.url,
    connParams.command,
    connParams.cwd,
    ...(Array.isArray(connParams.args) ? connParams.args : []),
    ...(up.config.tags ?? []),
    up.config.enabled ? '已启用 enabled' : '已停用 disabled',
    up.state,
    CONN_STATE_LABELS[up.state],
    up.lastError ?? '',
  ]
    .join(' ')
    .toLowerCase()
}

watch(searchKeyword, () => {
  currentPage.value = 1
})

watch(totalPages, (next) => {
  if (currentPage.value > next) currentPage.value = next
})

// 标签被删光或重命名后，避免残留无效筛选导致「空列表但无选中 chip」。
// 同时清理已不存在标签的折叠状态，避免 Set 无限增长。
watch(tagCounts, () => {
  if (selectedTagFilter.value === 'all') {
    // no-op for filter
  } else if (selectedTagFilter.value === 'untagged') {
    if (untaggedCount.value === 0) selectedTagFilter.value = 'all'
  } else if (!isSelectedTagPresent()) {
    selectedTagFilter.value = 'all'
  }

  if (collapsedGroupKeys.value.size === 0) return
  const valid = new Set<string>(['untagged'])
  for (const item of tagCounts.value) valid.add(`tag:${item.tag.toLowerCase()}`)
  let changed = false
  const next = new Set<string>()
  for (const key of collapsedGroupKeys.value) {
    if (valid.has(key)) next.add(key)
    else changed = true
  }
  if (changed) collapsedGroupKeys.value = next
})

watch(upstreams, (next) => {
  if (detailUpstream.value === null) return
  const latest = next.find((up) => up.id === detailUpstream.value?.id) ?? null
  if (latest === null) {
    closeDetail()
    return
  }
  detailUpstream.value = latest
})

watch(upstreams, (next) => {
  if (onboardingUpstream.value === null) return
  const latest = next.find((up) => up.id === onboardingUpstream.value?.id) ?? null
  onboardingUpstream.value = latest
})

// 复制源被删除时关闭复制抽屉。
// 注意：不要在列表轮询时重绑 cloneSource 引用，否则会触发表单 reset 冲掉用户已改内容。
watch(upstreams, (next) => {
  if (cloneSource.value === null) return
  const stillExists = next.some((up) => up.id === cloneSource.value?.id)
  if (stillExists) return
  if (drawerOpen.value && editing.value === null && prefill.value === null) {
    closeDrawer()
    toast.info('复制来源已删除，已关闭表单')
    return
  }
  cloneSource.value = null
  cloneName.value = ''
})

watch(importContent, () => {
  importPreviewItems.value = []
  importCreated.value = []
  importFailed.value = []
  importError.value = ''
})

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

function showError(err: unknown, fallback: string): void {
  toast.error(err instanceof Error ? err.message : fallback)
}

function mcpJSONFileNameNow(): string {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  return `mpg-mcp-servers-${d.getFullYear()}${pad(d.getMonth() + 1)}${pad(d.getDate())}-${pad(d.getHours())}${pad(d.getMinutes())}${pad(d.getSeconds())}.json`
}

/** 加载上游列表（按 sortOrder 排序）。 */
async function loadUpstreams(showLoading = true): Promise<void> {
  if (showLoading) loading.value = true
  errorMessage.value = ''
  try {
    const list = await listUpstreams()
    list.sort((a, b) => a.config.sortOrder - b.config.sortOrder)
    upstreams.value = list
    syncRecoverySchedules(list)
    await loadToolCounts(list)
    if (currentPage.value > totalPages.value) currentPage.value = totalPages.value
    if (hasRecoveringUpstream.value) ensureStatusPolling(60_000)
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '加载上游列表失败'
  } finally {
    if (showLoading) loading.value = false
  }
}

onMounted(loadUpstreams)
onUnmounted(() => {
  stopStatusPolling()
  stopRecoveryClock()
})

async function loadToolCounts(list: Upstream[]): Promise<void> {
  const next = { ...toolCounts.value }
  try {
    const summaries = await listUpstreamToolSummaries()
    const visibleIDs = new Set(list.map((up) => up.id))
    for (const item of summaries) {
      if (!visibleIDs.has(item.id)) continue
      next[item.id] = buildToolCountSnapshot(item.count, item.updatedAt, item.changeSummary)
    }
  } catch {
    // 工具摘要是列表辅助信息，失败时保留未知状态，不影响上游管理主流程。
  }
  toolCounts.value = next
}

function ensureStatusPolling(durationMs: number): void {
  statusPollingUntil = Math.max(statusPollingUntil, Date.now() + durationMs)
  if (statusPollTimer !== undefined) return
  statusPollTimer = window.setInterval(() => {
    // 处于低频恢复中的上游会长期存在；页面可见时保持轻量轮询，离开或恢复完成后
    // 自动停止，避免为每张卡片创建独立定时器。
    if (!hasRecoveringUpstream.value && Date.now() > statusPollingUntil) {
      stopStatusPolling()
      return
    }
    if (document.visibilityState === 'visible') void loadUpstreams(false)
  }, 3000)
}

function stopStatusPolling(): void {
  if (statusPollTimer !== undefined) {
    window.clearInterval(statusPollTimer)
    statusPollTimer = undefined
  }
}

function syncRecoverySchedules(list: Upstream[]): void {
  const activeIDs = new Set<string>()
  const now = Date.now()
  for (const up of list) {
    const retryAt = up.nextRetryAt ? new Date(up.nextRetryAt).getTime() : Number.NaN
    if (Number.isNaN(retryAt)) continue
    activeIDs.add(up.id)
    const previous = recoverySchedules.get(up.id)
    if (previous?.retryAt !== retryAt) {
      recoverySchedules.set(up.id, { retryAt, startedAt: now })
    }
  }
  for (const id of recoverySchedules.keys()) {
    if (!activeIDs.has(id)) recoverySchedules.delete(id)
  }
  if (activeIDs.size > 0) startRecoveryClock()
  else stopRecoveryClock()
}

function startRecoveryClock(): void {
  if (recoveryClockTimer !== undefined) return
  recoveryClock.value = Date.now()
  recoveryClockTimer = window.setInterval(() => {
    recoveryClock.value = Date.now()
  }, 1000)
}

function stopRecoveryClock(): void {
  if (recoveryClockTimer === undefined) return
  window.clearInterval(recoveryClockTimer)
  recoveryClockTimer = undefined
}

function clearDrawerSources(): void {
  editing.value = null
  prefill.value = null
  cloneSource.value = null
  cloneName.value = ''
}

function closeDrawer(): void {
  drawerOpen.value = false
  clearDrawerSources()
}

/** 打开新建抽屉（手动）。 */
function openCreate(): void {
  clearDrawerSources()
  drawerOpen.value = true
}

function openImport(): void {
  importOpen.value = true
  importError.value = ''
}

function closeImport(): void {
  if (importLoading.value || importExecuting.value || importFileReading.value) return
  importOpen.value = false
}

async function importFromFile(file: File | null | undefined): Promise<void> {
  if (!file || importLoading.value || importExecuting.value || importFileReading.value) return
  const validation = validateUpstreamImportFile(file)
  if (!validation.ok) {
    importFileName.value = ''
    importError.value = validation.error ?? '文件不可用'
    return
  }

  importFileReading.value = true
  importError.value = ''
  try {
    importContent.value = await file.text()
    importFileName.value = `${file.name || '本地文件'} · ${formatFileSize(file.size)}`
  } catch (err) {
    importError.value = err instanceof Error ? err.message : '读取文件失败'
  } finally {
    importFileReading.value = false
  }
}

function onImportFileChange(event: Event): void {
  const input = event.target as HTMLInputElement
  void importFromFile(input.files?.[0])
  input.value = ''
}

function onImportDrop(event: DragEvent): void {
  event.preventDefault()
  void importFromFile(event.dataTransfer?.files?.[0])
}

/** 打开编辑抽屉。 */
function openEdit(up: Upstream): void {
  clearDrawerSources()
  editing.value = up
  drawerOpen.value = true
}

/** 打开复制抽屉：预填连接配置，用户确认后再创建。 */
function openCopy(up: Upstream): void {
  clearDrawerSources()
  cloneSource.value = up
  cloneName.value = suggestUniqueUpstreamName(
    up.config.name,
    upstreams.value.map((item) => item.config.name),
  )
  drawerOpen.value = true
}

function openDetail(up: Upstream): void {
  detailUpstream.value = up
  detailOpen.value = true
}

function closeDetail(): void {
  detailOpen.value = false
  detailUpstream.value = null
}

function editFromDetail(up: Upstream): void {
  closeDetail()
  openEdit(up)
}

function copyFromDetail(up: Upstream): void {
  closeDetail()
  openCopy(up)
}

/** 模板市场选中模板：关闭弹窗并以预填充打开创建抽屉（Req 14.7）。 */
function onTemplateSelected(pf: PrefillForm): void {
  marketOpen.value = false
  clearDrawerSources()
  prefill.value = pf
  drawerOpen.value = true
}

/** 抽屉保存成功：关闭并刷新列表。 */
async function onSaved(payload: { upstream: Upstream; mode: 'create' | 'edit' }): Promise<void> {
  closeDrawer()
  if (payload.mode === 'create') {
    onboardingUpstream.value = payload.upstream
    toast.success('上游已创建')
  } else {
    toast.success('保存成功')
  }
  await loadUpstreams()
  ensureStatusPolling(60_000)
}

function dismissOnboarding(): void {
  onboardingUpstream.value = null
}

async function previewImport(): Promise<void> {
  if (!importCanPreview.value) {
    importError.value = '请先粘贴 MCP JSON 配置'
    return
  }
  importLoading.value = true
  importError.value = ''
  importCreated.value = []
  importFailed.value = []
  try {
    const preview = await previewUpstreamImport(importContent.value)
    importPreviewItems.value = preview.items
    if (preview.count === 0) {
      importError.value = '未识别到可导入的上游 MCP'
    }
  } catch (err) {
    importError.value = err instanceof Error ? err.message : '解析导入内容失败'
  } finally {
    importLoading.value = false
  }
}

async function submitImport(): Promise<void> {
  if (!importCanSubmit.value) return
  importExecuting.value = true
  importError.value = ''
  try {
    const result = await importUpstreamsFromJSON(importContent.value)
    importCreated.value = result.created
    importFailed.value = result.failed
    if (result.created.length > 0) {
      toast.success(`已导入 ${result.created.length} 个上游 MCP`)
      await loadUpstreams()
      ensureStatusPolling(60_000)
    }
    if (result.failed.length > 0) {
      toast.warning(`${result.failed.length} 个上游导入失败，可按提示调整后重试`, {
        duration: 3600,
      })
    }
  } catch (err) {
    importError.value = err instanceof Error ? err.message : '导入失败'
  } finally {
    importExecuting.value = false
  }
}

async function downloadMCPJSON(): Promise<void> {
  if (exportingMCPJSON.value) return
  exportingMCPJSON.value = true
  try {
    const blob = await exportUpstreamsMCPJSON()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = mcpJSONFileNameNow()
    document.body.appendChild(a)
    a.click()
    a.remove()
    URL.revokeObjectURL(url)
    toast.success('MCP JSON 已导出')
  } catch (err) {
    showError(err, '导出 MCP JSON 失败')
  } finally {
    exportingMCPJSON.value = false
  }
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
    showError(err, '操作失败')
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
    toolCounts.value = {
      ...toolCounts.value,
      [up.id]: buildToolCountSnapshot(count, new Date().toISOString()),
    }
    toast.success(`已刷新「${up.config.name}」，共 ${count} 个工具`)
    await loadUpstreams()
  } catch (err) {
    showError(err, '刷新失败')
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
    const result = await reconnectUpstream(up.id)
    toast.success(result.message ?? `已请求「${up.config.name}」立即探测`)
    await loadUpstreams()
    ensureStatusPolling(60_000)
  } catch (err) {
    showError(err, '重连失败')
  } finally {
    setBusy(key, false)
  }
}

function recoveryHint(up: Upstream): string {
  if (up.state === 'available') return ''
  if (up.state === 'connecting') return '正在建立连接…'
  if (up.nextRetryAt) {
    const next = new Date(up.nextRetryAt)
    if (!Number.isNaN(next.getTime())) {
      return `后台将在 ${next.toLocaleTimeString('zh-CN', { hour12: false })} 再次探测`
    }
  }
  return up.state === 'suspended' ? '正在低频自动恢复' : '等待自动恢复'
}

function recoveryProgress(up: Upstream): number {
  const schedule = recoverySchedules.get(up.id)
  if (schedule === undefined || schedule.retryAt <= schedule.startedAt) return 0
  return Math.min(100, Math.max(0, ((recoveryClock.value - schedule.startedAt) / (schedule.retryAt - schedule.startedAt)) * 100))
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

function toolSearchText(tool: ToolDef): string {
  return [tool.name, tool.originalName, tool.description, tool.upstreamId].join(' ').toLowerCase()
}

async function openToolModal(up: Upstream): Promise<void> {
  toolModalOpen.value = true
  toolModalUpstream.value = up
  toolModalTools.value = []
  toolModalUpdatedAt.value = null
  toolModalError.value = ''
  toolModalSearchKeyword.value = ''
  toolModalLoading.value = true
  try {
    const result = await listUpstreamTools(up.id)
    toolModalTools.value = result.tools
    toolModalUpdatedAt.value = result.updatedAt ?? null
    toolCounts.value = {
      ...toolCounts.value,
      [up.id]: buildToolCountSnapshot(result.count, result.updatedAt),
    }
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
    toast.success('排序已保存')
    await loadUpstreams()
  } catch (err) {
    showError(err, '排序失败')
    await loadUpstreams()
  } finally {
    setBusy('reorder', false)
  }
}

/** 请求删除确认。 */
async function askDelete(up: Upstream): Promise<void> {
  const ok = await confirm({
    title: '确认删除',
    message: `确定删除上游 MCP「${up.config.name}」吗？该操作将级联清理其工具缓存与规则，且不可恢复。`,
    confirmText: '删除',
    tone: 'danger',
  })
  if (!ok) return
  try {
    await deleteUpstream(up.id)
    toast.success(`已删除「${up.config.name}」`)
    await loadUpstreams()
  } catch (err) {
    showError(err, '删除失败')
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
    <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
      <div class="min-w-0">
        <p class="text-sm text-gray-500 dark:text-gray-400">{{ upstreamCountLabel }}</p>
        <p class="mt-0.5 text-xs text-gray-400 dark:text-gray-500">
          标签仅用于管理分组与识别，不影响工具路由
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-2">
        <label class="relative block w-full sm:w-72">
          <span class="sr-only">搜索上游 MCP</span>
          <input
            v-model="searchKeyword"
            type="search"
            placeholder="搜索名称、标签或状态"
            class="focus:border-brand-300 focus:ring-brand-500/10 h-10 w-full rounded-lg border border-gray-300 bg-white px-3 pr-12 text-sm text-gray-800 shadow-sm transition placeholder:text-gray-400 focus:ring-3 focus:outline-none dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
          />
          <button
            v-if="hasSearchKeyword"
            type="button"
            class="absolute top-1/2 right-2 -translate-y-1/2 rounded-md px-2 py-1 text-xs text-gray-500 transition hover:bg-gray-100 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-200"
            @click="searchKeyword = ''"
          >
            清空
          </button>
        </label>
        <div
          class="inline-flex h-10 items-center rounded-lg border border-gray-300 bg-white p-0.5 dark:border-gray-700 dark:bg-gray-900"
          role="group"
          aria-label="视图切换"
        >
          <button
            type="button"
            class="inline-flex h-9 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium transition"
            :class="
              viewMode === 'list'
                ? 'bg-brand-500 text-white shadow-sm'
                : 'text-gray-600 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-gray-800'
            "
            :aria-pressed="viewMode === 'list'"
            @click="setViewMode('list')"
          >
            <ListIcon class="h-4 w-4" aria-hidden="true" />
            列表
          </button>
          <button
            type="button"
            class="inline-flex h-9 items-center gap-1.5 rounded-md px-2.5 text-xs font-medium transition"
            :class="
              viewMode === 'group'
                ? 'bg-brand-500 text-white shadow-sm'
                : 'text-gray-600 hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-gray-800'
            "
            :aria-pressed="viewMode === 'group'"
            @click="setViewMode('group')"
          >
            <FolderIcon class="h-4 w-4" aria-hidden="true" />
            分组
          </button>
        </div>
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
          v-tooltip:bottom="'上游排序'"
          type="button"
          class="inline-flex h-10 w-10 items-center justify-center rounded-lg border border-gray-300 text-gray-700 transition hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          :disabled="upstreams.length <= 1 || loading"
          aria-label="上游排序"
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
        </button>
        <button
          v-tooltip:bottom="'导入 JSON'"
          type="button"
          class="inline-flex h-10 w-10 items-center justify-center rounded-lg border border-gray-300 text-gray-700 transition hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          aria-label="导入 JSON"
          @click="openImport"
        >
          <svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none">
            <path
              d="M12 3v11M8 10l4 4 4-4M5 17v2a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-2"
              stroke="currentColor"
              stroke-width="1.8"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
          </svg>
        </button>
        <button
          v-tooltip:bottom="exportingMCPJSON ? '导出中' : '导出 JSON'"
          type="button"
          class="inline-flex h-10 w-10 items-center justify-center rounded-lg border border-gray-300 text-gray-700 transition hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          :disabled="exportingMCPJSON || upstreams.length === 0"
          :aria-label="exportingMCPJSON ? '导出中' : '导出 JSON'"
          @click="downloadMCPJSON"
        >
          <svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none">
            <path
              d="M12 21V10M8 14l4-4 4 4M5 7V5a2 2 0 0 1 2-2h10a2 2 0 0 1 2 2v2"
              stroke="currentColor"
              stroke-width="1.8"
              stroke-linecap="round"
              stroke-linejoin="round"
            />
          </svg>
        </button>
        <button
          type="button"
          class="border-brand-300 text-brand-600 hover:bg-brand-50 dark:border-brand-500/40 dark:text-brand-400 dark:hover:bg-brand-500/10 inline-flex items-center gap-1.5 rounded-lg border px-3.5 py-2 text-sm font-medium transition"
          @click="marketOpen = true"
        >
          <svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none">
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

    <UpstreamOnboardingPanel
      v-if="onboardingUpstream !== null"
      :upstream="onboardingUpstream"
      :tool-count="toolCounts[onboardingUpstream.id]"
      :refreshing="isBusy(onboardingUpstream.id, 'refresh')"
      :reconnecting="isBusy(onboardingUpstream.id, 'reconnect')"
      @refresh="refresh(onboardingUpstream)"
      @reconnect="reconnect(onboardingUpstream)"
      @open-tools="openToolModal(onboardingUpstream)"
      @dismiss="dismissOnboarding"
    />

    <!-- 标签筛选条（管理分组） -->
    <div v-if="showTagFilterBar && !loading && upstreams.length > 0" class="mb-4">
      <div class="relative">
        <div
          class="flex [scrollbar-width:none] gap-2 overflow-x-auto pb-1 [-ms-overflow-style:none] [&::-webkit-scrollbar]:hidden"
          role="listbox"
          aria-label="按标签筛选上游"
        >
          <button
            v-for="option in tagFilterOptions"
            :key="option.key"
            type="button"
            role="option"
            class="inline-flex shrink-0 items-center gap-1.5 rounded-full border px-3 py-1.5 text-xs font-medium transition"
            :class="
              isTagFilterActive(option.value)
                ? 'border-brand-500 bg-brand-500 text-white shadow-sm'
                : 'hover:border-brand-200 hover:bg-brand-50/60 dark:hover:border-brand-500/40 border-gray-200 bg-white text-gray-600 dark:border-gray-700 dark:bg-white/[0.03] dark:text-gray-300'
            "
            :aria-selected="isTagFilterActive(option.value)"
            @click="selectTagFilter(option.value)"
          >
            <span class="max-w-[10rem] truncate">{{ option.label }}</span>
            <span
              class="rounded-full px-1.5 py-0.5 text-[10px] tabular-nums"
              :class="
                isTagFilterActive(option.value)
                  ? 'bg-white/20 text-white'
                  : 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400'
              "
            >
              {{ option.count }}
            </span>
          </button>
        </div>
        <div
          class="pointer-events-none absolute inset-y-0 right-0 w-8 bg-gradient-to-l from-gray-50 to-transparent dark:from-gray-900"
        ></div>
      </div>
      <div v-if="hasSearchKeyword || hasTagFilter" class="mt-2 flex items-center gap-2">
        <button
          type="button"
          class="text-brand-600 hover:text-brand-700 dark:text-brand-400 text-xs font-medium transition"
          @click="clearFilters"
        >
          清除筛选
        </button>
      </div>
    </div>

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
      <div
        v-else-if="filteredUpstreams.length === 0"
        class="rounded-2xl border border-dashed border-gray-300 bg-white px-5 py-12 text-center text-sm text-gray-400 dark:border-gray-700 dark:bg-white/[0.03]"
      >
        <p>没有匹配的上游 MCP 服务</p>
        <button
          v-if="hasSearchKeyword || hasTagFilter"
          type="button"
          class="text-brand-600 dark:text-brand-400 mt-3 text-sm font-medium"
          @click="clearFilters"
        >
          清除筛选条件
        </button>
      </div>

      <!-- 列表视图 -->
      <template v-else-if="viewMode === 'list'">
        <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          <div
            v-for="up in pagedUpstreams"
            :key="up.id"
            class="flex flex-col rounded-2xl border border-gray-200 bg-white p-5 shadow-sm transition hover:shadow-md dark:border-gray-800 dark:bg-white/[0.03]"
          >
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
                  <span
                    v-if="
                      up.config.transport === 'stdio' &&
                      normalizeSecurityProfile(up.config.connParams?.securityProfile).mode ===
                        'unrestricted'
                    "
                    class="bg-error-600 ring-error-300/70 dark:ring-error-400/40 inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-semibold tracking-wide text-white shadow-sm ring-1"
                  >
                    完全放行
                  </span>
                  <span
                    v-else-if="
                      up.config.transport === 'stdio' &&
                      normalizeSecurityProfile(up.config.connParams?.securityProfile).mode ===
                        'strict'
                    "
                    class="bg-success-50 text-success-700 ring-success-200 dark:bg-success-500/10 dark:text-success-400 dark:ring-success-500/30 inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-semibold ring-1"
                  >
                    严格安全
                  </span>
                  <ConnStateBadge :state="up.state" />
                </div>
                <div v-if="up.config.tags?.length" class="mt-2 flex flex-wrap gap-1.5">
                  <button
                    v-for="tag in up.config.tags"
                    :key="tag"
                    type="button"
                    class="inline-flex max-w-full items-center truncate rounded-full px-2 py-0.5 text-xs font-medium transition"
                    :class="
                      isTagFilterActive(tag)
                        ? 'bg-brand-500 text-white'
                        : 'bg-brand-50 text-brand-600 hover:bg-brand-100 dark:bg-brand-500/10 dark:text-brand-300 dark:hover:bg-brand-500/20'
                    "
                    :aria-label="`按标签 ${tag} 筛选`"
                    :aria-pressed="isTagFilterActive(tag)"
                    @click="filterByTag(tag)"
                  >
                    {{ tag }}
                  </button>
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

            <div v-if="up.state !== 'available'" class="mb-3 rounded-xl border border-gray-100 bg-gray-50/80 px-3 py-2 dark:border-gray-800 dark:bg-white/[0.03]">
              <div class="flex items-center justify-between gap-2">
                <p class="min-w-0 truncate text-xs font-medium text-gray-600 dark:text-gray-300">
                  {{ recoveryHint(up) }}
                </p>
                <span
                  v-if="(up.failureCount ?? 0) > 0"
                  class="shrink-0 rounded-full bg-warning-50 px-2 py-0.5 text-[10px] font-medium text-warning-700 dark:bg-warning-500/10 dark:text-warning-400"
                >
                  连续失败 {{ up.failureCount }} 次
                </span>
              </div>
              <div v-if="up.state === 'connecting' || up.nextRetryAt" class="mt-2 h-1 overflow-hidden rounded-full bg-brand-100 dark:bg-brand-500/15">
                <span
                  class="block h-full rounded-full bg-brand-500 transition-[width] duration-1000 ease-linear"
                  :style="{ width: `${up.nextRetryAt ? recoveryProgress(up) : 100}%` }"
                ></span>
              </div>
              <AppTooltip v-if="up.lastError" :content="up.lastError" placement="bottom-start">
                <p class="mt-2 line-clamp-2 break-all text-xs leading-5 text-error-600 dark:text-error-400">{{ up.lastError }}</p>
              </AppTooltip>
            </div>

            <div
              class="mt-auto flex flex-wrap items-center justify-between gap-2 border-t border-gray-100 pt-3 dark:border-gray-800"
            >
              <button
                type="button"
                class="hover:bg-brand-50 hover:text-brand-600 dark:hover:bg-brand-500/10 dark:hover:text-brand-400 rounded-lg bg-gray-50 px-2.5 py-1.5 text-xs font-medium text-gray-600 transition dark:bg-white/5 dark:text-gray-300"
                @click="openToolModal(up)"
              >
                {{ toolCountLabel(toolCounts[up.id]) }}
              </button>
              <span
                v-if="toolCounts[up.id]?.synced"
                class="min-w-0 flex-1 truncate text-xs text-gray-400 dark:text-gray-500"
              >
                {{ toolChangeSummaryLabel(toolCounts[up.id]) }}
              </span>
              <div class="flex flex-wrap items-center justify-end gap-1.5">
                <button
                  type="button"
                  class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800"
                  @click="openDetail(up)"
                >
                  详情
                </button>
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
                  class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800"
                  aria-label="复制上游"
                  @click="openCopy(up)"
                >
                  复制
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
      </template>

      <!-- 分组视图 -->
      <div v-else class="space-y-4">
        <section
          v-for="group in groupedUpstreams"
          :key="group.key"
          class="overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-sm dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <button
            type="button"
            class="flex w-full items-center justify-between gap-3 border-b border-gray-100 px-4 py-3 text-left transition hover:bg-gray-50/80 sm:px-5 dark:border-gray-800 dark:hover:bg-white/[0.04]"
            :aria-expanded="!isGroupCollapsed(group.key)"
            @click="toggleGroupCollapsed(group.key)"
          >
            <div class="flex min-w-0 items-center gap-2.5">
              <span
                class="inline-flex h-8 w-8 shrink-0 items-center justify-center rounded-lg"
                :class="
                  group.tag === null
                    ? 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400'
                    : 'bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-300'
                "
              >
                <FolderIcon class="h-4 w-4" aria-hidden="true" />
              </span>
              <div class="min-w-0">
                <div class="truncate text-sm font-semibold text-gray-800 dark:text-white/90">
                  {{ group.label }}
                </div>
                <div class="text-xs text-gray-400 dark:text-gray-500">
                  {{ group.items.length }} 个上游
                </div>
              </div>
            </div>
            <ChevronDownIcon
              class="h-4 w-4 shrink-0 text-gray-400 transition-transform duration-200"
              :class="isGroupCollapsed(group.key) ? '-rotate-90' : 'rotate-0'"
              aria-hidden="true"
            />
          </button>

          <div
            v-show="!isGroupCollapsed(group.key)"
            class="grid grid-cols-1 gap-4 p-4 sm:grid-cols-2 sm:p-5 xl:grid-cols-3"
          >
            <div
              v-for="up in group.items"
              :key="`${group.key}:${up.id}`"
              class="flex flex-col rounded-xl border border-gray-200 bg-white p-4 shadow-sm transition hover:shadow-md dark:border-gray-800 dark:bg-gray-900/40"
            >
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
                    <span
                      v-if="
                        up.config.transport === 'stdio' &&
                        normalizeSecurityProfile(up.config.connParams?.securityProfile).mode ===
                          'unrestricted'
                      "
                      class="bg-error-600 ring-error-300/70 dark:ring-error-400/40 inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-semibold tracking-wide text-white shadow-sm ring-1"
                    >
                      完全放行
                    </span>
                    <span
                      v-else-if="
                        up.config.transport === 'stdio' &&
                        normalizeSecurityProfile(up.config.connParams?.securityProfile).mode ===
                          'strict'
                      "
                      class="bg-success-50 text-success-700 ring-success-200 dark:bg-success-500/10 dark:text-success-400 dark:ring-success-500/30 inline-flex items-center rounded-full px-2 py-0.5 text-[10px] font-semibold ring-1"
                    >
                      严格安全
                    </span>
                    <ConnStateBadge :state="up.state" />
                  </div>
                  <div v-if="up.config.tags?.length" class="mt-2 flex flex-wrap gap-1.5">
                    <button
                      v-for="tag in up.config.tags"
                      :key="tag"
                      type="button"
                      class="inline-flex max-w-full items-center truncate rounded-full px-2 py-0.5 text-xs font-medium transition"
                      :class="
                        isTagFilterActive(tag)
                          ? 'bg-brand-500 text-white'
                          : 'bg-brand-50 text-brand-600 hover:bg-brand-100 dark:bg-brand-500/10 dark:text-brand-300 dark:hover:bg-brand-500/20'
                      "
                      :aria-label="`按标签 ${tag} 筛选`"
                      :aria-pressed="isTagFilterActive(tag)"
                      @click="filterByTag(tag)"
                    >
                      {{ tag }}
                    </button>
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

              <div v-if="up.state !== 'available'" class="mb-3 rounded-xl border border-gray-100 bg-gray-50/80 px-3 py-2 dark:border-gray-800 dark:bg-white/[0.03]">
                <div class="flex items-center justify-between gap-2">
                  <p class="min-w-0 truncate text-xs font-medium text-gray-600 dark:text-gray-300">
                    {{ recoveryHint(up) }}
                  </p>
                  <span
                    v-if="(up.failureCount ?? 0) > 0"
                    class="shrink-0 rounded-full bg-warning-50 px-2 py-0.5 text-[10px] font-medium text-warning-700 dark:bg-warning-500/10 dark:text-warning-400"
                  >
                    连续失败 {{ up.failureCount }} 次
                  </span>
                </div>
                <div v-if="up.state === 'connecting' || up.nextRetryAt" class="mt-2 h-1 overflow-hidden rounded-full bg-brand-100 dark:bg-brand-500/15">
                  <span
                    class="block h-full rounded-full bg-brand-500 transition-[width] duration-1000 ease-linear"
                    :style="{ width: `${up.nextRetryAt ? recoveryProgress(up) : 100}%` }"
                  ></span>
                </div>
                <AppTooltip v-if="up.lastError" :content="up.lastError" placement="bottom-start">
                  <p class="mt-2 line-clamp-2 break-all text-xs leading-5 text-error-600 dark:text-error-400">{{ up.lastError }}</p>
                </AppTooltip>
              </div>

              <div
                class="mt-auto flex flex-wrap items-center justify-between gap-2 border-t border-gray-100 pt-3 dark:border-gray-800"
              >
                <button
                  type="button"
                  class="hover:bg-brand-50 hover:text-brand-600 dark:hover:bg-brand-500/10 dark:hover:text-brand-400 rounded-lg bg-gray-50 px-2.5 py-1.5 text-xs font-medium text-gray-600 transition dark:bg-white/5 dark:text-gray-300"
                  @click="openToolModal(up)"
                >
                  {{ toolCountLabel(toolCounts[up.id]) }}
                </button>
                <div class="flex flex-wrap items-center justify-end gap-1.5">
                  <button
                    type="button"
                    class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800"
                    @click="openDetail(up)"
                  >
                    详情
                  </button>
                  <button
                    type="button"
                    class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800"
                    aria-label="复制上游"
                    @click="openCopy(up)"
                  >
                    复制
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
        </section>
      </div>
    </div>

    <!-- 创建/编辑/复制抽屉 -->
    <UpstreamFormDrawer
      :open="drawerOpen"
      :upstream="editing"
      :prefill="prefill"
      :clone-source="cloneSource"
      :clone-name="cloneName"
      :tag-options="allTags"
      :next-sort-order="nextSortOrder"
      @close="closeDrawer"
      @saved="onSaved"
    />

    <transition name="fade">
      <div
        v-if="detailOpen && detailUpstream !== null && detailSummary !== null"
        class="fixed inset-0 z-[100001] flex items-stretch justify-end bg-gray-900/40 backdrop-blur-[1px]"
      >
        <aside
          class="flex h-full w-full max-w-xl flex-col border-l border-gray-200 bg-white shadow-xl dark:border-gray-800 dark:bg-gray-900"
          role="dialog"
          aria-modal="true"
        >
          <div
            class="flex items-start justify-between gap-4 border-b border-gray-200 px-5 py-4 dark:border-gray-800"
          >
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <h3 class="truncate text-base font-semibold text-gray-800 dark:text-white/90">
                  {{ detailUpstream.config.name }}
                </h3>
                <ConnStateBadge :state="detailUpstream.state" />
              </div>
              <p class="mt-1 text-xs break-all text-gray-500 dark:text-gray-400">
                {{ detailSummary.endpointValue }}
              </p>
            </div>
            <button
              v-tooltip:bottom-end="'关闭'"
              type="button"
              class="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-800"
              aria-label="关闭上游详情"
              @click="closeDetail"
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

          <div class="custom-scrollbar min-h-0 flex-1 overflow-y-auto p-5">
            <div class="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <section
                class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.03]"
              >
                <p class="text-xs text-gray-500 dark:text-gray-400">运行状态</p>
                <p class="mt-2 text-sm leading-6 text-gray-700 dark:text-gray-300">
                  {{ detailSummary.healthDescription }}
                </p>
              </section>
              <section
                class="rounded-xl border border-gray-200 bg-gray-50 p-4 dark:border-gray-800 dark:bg-white/[0.03]"
              >
                <p class="text-xs text-gray-500 dark:text-gray-400">工具缓存</p>
                <p class="mt-2 text-lg font-semibold text-gray-800 dark:text-white/90">
                  {{ detailSummary.toolLabel }}
                </p>
                <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  {{ detailSummary.syncDescription }}
                </p>
              </section>
            </div>

            <section
              v-if="detailIsStdio"
              class="mt-4 rounded-xl border border-gray-200 p-4 dark:border-gray-800"
            >
              <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
                <div>
                  <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">
                    本地运行环境
                  </h4>
                  <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
                    安全档位、宿主依赖与当前探测结果。Linux 有 bubblewrap 时，严格档启用文件/网络隔离。
                  </p>
                </div>
                <div class="flex flex-wrap items-center gap-2">
                  <span
                    class="rounded-full px-2.5 py-1 text-xs font-semibold"
                    :class="
                      securityModeBadgeClass(
                        detailPreflight?.securityMode ||
                          normalizeSecurityProfile(
                            detailUpstream?.config.connParams.securityProfile,
                          ).mode ||
                          'standard',
                      )
                    "
                  >
                    {{
                      securityModeLabel(
                        detailPreflight?.securityMode ||
                          normalizeSecurityProfile(
                            detailUpstream?.config.connParams.securityProfile,
                          ).mode ||
                          'standard',
                      )
                    }}
                  </span>
                  <span
                    v-if="detailPreflightBanner"
                    class="rounded-full px-2.5 py-1 text-xs font-medium"
                    :class="depChipClass(detailPreflightBanner.tone === 'success')"
                  >
                    {{ detailPreflightLoading ? '检测中…' : detailPreflightBanner.label }}
                  </span>
                </div>
              </div>

              <div
                v-if="detailPreflight?.securityError || detailPreflight?.riskLevel === 'critical'"
                class="mb-3 rounded-lg border px-3 py-2 text-xs leading-5"
                :class="
                  detailPreflight?.riskLevel === 'critical' ||
                  detailPreflightBanner?.tone === 'error'
                    ? 'border-error-300 bg-error-50 text-error-800 dark:border-error-500/30 dark:bg-error-500/10 dark:text-error-200'
                    : 'border-warning-200 bg-warning-50 text-warning-800 dark:border-warning-500/20 dark:bg-warning-500/10 dark:text-warning-200'
                "
              >
                <p v-if="detailPreflight?.securityError">{{ detailPreflight.securityError }}</p>
                <p v-else-if="detailPreflightBanner?.risk">
                  风险等级：{{ detailPreflightBanner.risk }}
                </p>
              </div>

              <div v-if="detailPreflight?.items?.length" class="space-y-2">
                <div
                  v-for="item in detailPreflight.items"
                  :key="item.name"
                  class="flex flex-wrap items-center justify-between gap-2 rounded-lg bg-gray-50 px-3 py-2 text-xs dark:bg-white/[0.03]"
                >
                  <div class="min-w-0">
                    <p class="font-medium text-gray-800 dark:text-white/90">
                      {{ item.label }}
                      <span class="font-mono text-gray-400">({{ item.name }})</span>
                    </p>
                    <p v-if="item.path" class="mt-0.5 text-[11px] break-all text-gray-400">
                      {{ item.path }}
                    </p>
                    <p v-else-if="item.message" class="mt-0.5 text-[11px] text-gray-400">
                      {{ item.message }}
                    </p>
                  </div>
                  <div class="flex items-center gap-2">
                    <span
                      class="rounded-full px-2 py-0.5 font-medium"
                      :class="depChipClass(item.available)"
                    >
                      {{ item.available ? '可用' : '缺失' }}
                    </span>
                  </div>
                </div>
              </div>
              <p
                v-else-if="!detailPreflightLoading"
                class="text-xs text-gray-500 dark:text-gray-400"
              >
                未声明依赖时将按启动命令自动推断。可在编辑中手动选择。
              </p>

              <div class="mt-3 flex flex-wrap gap-3">
                <router-link
                  to="/runtime"
                  class="text-brand-600 dark:text-brand-400 text-xs font-medium hover:underline"
                >
                  打开运行环境
                </router-link>
                <button
                  type="button"
                  class="text-xs font-medium text-gray-500 hover:text-gray-700 dark:text-gray-400"
                  :disabled="detailPreflightLoading"
                  @click="loadDetailPreflight"
                >
                  重新检测
                </button>
              </div>
            </section>

            <section class="mt-4 rounded-xl border border-gray-200 p-4 dark:border-gray-800">
              <div class="mb-3 flex items-center justify-between gap-3">
                <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">连接摘要</h4>
                <span
                  class="rounded-full px-2.5 py-1 text-xs"
                  :class="
                    detailUpstream.config.enabled
                      ? 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
                      : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'
                  "
                >
                  {{ detailUpstream.config.enabled ? '启用' : '停用' }}
                </span>
              </div>
              <dl class="grid grid-cols-1 gap-3 sm:grid-cols-2">
                <div
                  v-for="item in detailSummary.runtimeItems"
                  :key="item.label"
                  class="min-w-0 rounded-lg bg-gray-50 px-3 py-2 dark:bg-white/[0.03]"
                >
                  <dt class="text-xs text-gray-400 dark:text-gray-500">{{ item.label }}</dt>
                  <dd class="mt-1 text-sm font-medium break-all text-gray-700 dark:text-gray-200">
                    {{ item.value }}
                  </dd>
                </div>
              </dl>
            </section>

            <section class="mt-4 rounded-xl border border-gray-200 p-4 dark:border-gray-800">
              <h4 class="mb-3 text-sm font-semibold text-gray-800 dark:text-white/90">连接配置</h4>
              <dl class="grid grid-cols-1 gap-3">
                <div
                  v-for="item in detailSummary.connectionItems"
                  :key="item.label"
                  class="min-w-0 rounded-lg bg-gray-50 px-3 py-2 dark:bg-white/[0.03]"
                >
                  <dt class="text-xs text-gray-400 dark:text-gray-500">{{ item.label }}</dt>
                  <dd class="mt-1 text-sm font-medium break-all text-gray-700 dark:text-gray-200">
                    {{ item.value }}
                  </dd>
                </div>
              </dl>
            </section>

            <section v-if="detailUpstream.config.tags?.length" class="mt-4">
              <h4 class="mb-2 text-sm font-semibold text-gray-800 dark:text-white/90">管理分组</h4>
              <div class="flex flex-wrap gap-1.5">
                <button
                  v-for="tag in detailUpstream.config.tags"
                  :key="tag"
                  type="button"
                  class="rounded-full px-2.5 py-1 text-xs font-medium transition"
                  :class="
                    isTagFilterActive(tag)
                      ? 'bg-brand-500 text-white'
                      : 'bg-brand-50 text-brand-600 hover:bg-brand-100 dark:bg-brand-500/10 dark:text-brand-300 dark:hover:bg-brand-500/20'
                  "
                  :aria-label="`按标签 ${tag} 筛选`"
                  :aria-pressed="isTagFilterActive(tag)"
                  @click="filterByTag(tag)"
                >
                  {{ tag }}
                </button>
              </div>
            </section>
          </div>

          <div class="border-t border-gray-200 p-4 dark:border-gray-800">
            <div class="grid grid-cols-2 gap-2 sm:grid-cols-3">
              <button
                type="button"
                class="rounded-lg border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                :disabled="isBusy(detailUpstream.id, 'refresh')"
                @click="refresh(detailUpstream)"
              >
                {{ isBusy(detailUpstream.id, 'refresh') ? '刷新中...' : '刷新工具' }}
              </button>
              <button
                type="button"
                class="rounded-lg border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                :disabled="isBusy(detailUpstream.id, 'reconnect')"
                @click="reconnect(detailUpstream)"
              >
                {{ isBusy(detailUpstream.id, 'reconnect') ? '重连中...' : '重连' }}
              </button>
              <button
                type="button"
                class="rounded-lg border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                @click="openToolModal(detailUpstream)"
              >
                查看工具
              </button>
              <button
                type="button"
                class="rounded-lg border border-gray-300 px-3 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                @click="copyFromDetail(detailUpstream)"
              >
                复制
              </button>
              <button
                type="button"
                class="bg-brand-500 hover:bg-brand-600 rounded-lg px-3 py-2 text-sm font-medium text-white transition sm:col-span-2"
                @click="editFromDetail(detailUpstream)"
              >
                编辑
              </button>
            </div>
          </div>
        </aside>
      </div>
    </transition>

    <!-- 模板市场弹窗 -->
    <TemplateMarketModal
      :open="marketOpen"
      @close="marketOpen = false"
      @select="onTemplateSelected"
    />

    <!-- MCP JSON 批量导入 -->
    <transition name="fade">
      <div
        v-if="importOpen"
        class="fixed inset-0 z-[100001] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
      >
        <div
          class="flex max-h-[88vh] w-full max-w-4xl flex-col rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-gray-800 dark:bg-gray-900"
        >
          <div
            class="flex items-center justify-between gap-3 border-b border-gray-200 px-5 py-4 dark:border-gray-800"
          >
            <div class="min-w-0">
              <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">
                导入 MCP JSON
              </h3>
              <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
                支持 mcpServers、upstreams 或上游数组，确认后会批量创建上游。
              </p>
            </div>
            <button
              v-tooltip:bottom-end="'关闭'"
              type="button"
              class="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-lg text-gray-400 hover:bg-gray-100 hover:text-gray-600 disabled:opacity-50 dark:hover:bg-gray-800"
              :disabled="importLoading || importExecuting"
              aria-label="关闭"
              @click="closeImport"
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
            <div
              class="hover:border-brand-300 hover:bg-brand-50/40 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.06] mb-4 rounded-xl border border-dashed border-gray-300 bg-gray-50 px-4 py-5 text-center transition dark:border-gray-700 dark:bg-white/[0.03]"
              @dragover.prevent
              @drop="onImportDrop"
            >
              <input
                id="upstream-import-file"
                type="file"
                accept=".json,.txt,application/json,text/plain"
                class="sr-only"
                :disabled="importLoading || importExecuting || importFileReading"
                @change="onImportFileChange"
              />
              <p class="text-sm font-medium text-gray-700 dark:text-gray-300">
                拖放 MCP JSON 文件到这里，或
                <label
                  for="upstream-import-file"
                  class="text-brand-600 dark:text-brand-400 cursor-pointer hover:underline"
                >
                  选择文件
                </label>
              </p>
              <p class="mt-1 text-xs text-gray-400 dark:text-gray-500">
                支持 .json / .txt，最大
                {{ formatFileSize(MAX_UPSTREAM_IMPORT_FILE_BYTES) }}；也可以直接在下方粘贴配置。
              </p>
              <p v-if="importFileReading" class="text-brand-600 dark:text-brand-400 mt-2 text-xs">
                正在读取文件...
              </p>
              <p
                v-else-if="importFileName !== ''"
                class="mt-2 text-xs text-gray-500 dark:text-gray-400"
              >
                已读取 {{ importFileName }}
              </p>
            </div>
            <label class="block">
              <span class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">
                JSON 配置
              </span>
              <textarea
                v-model="importContent"
                class="focus:border-brand-300 focus:ring-brand-500/10 min-h-64 w-full rounded-lg border border-gray-300 bg-white px-3 py-2 font-mono text-sm text-gray-800 shadow-sm transition placeholder:text-gray-400 focus:ring-3 focus:outline-none dark:border-gray-700 dark:bg-gray-950 dark:text-white/90"
                spellcheck="false"
                placeholder='{"mcpServers":{"filesystem":{"command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","D:/work"]}}}'
                @input="importFileName = ''"
              />
            </label>

            <p
              v-if="importError !== ''"
              class="bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400 mt-4 rounded-lg px-4 py-2.5 text-sm"
            >
              {{ importError }}
            </p>

            <div
              v-if="importPreviewItems.length > 0"
              class="mt-5 rounded-xl border border-gray-200 dark:border-gray-800"
            >
              <div
                class="flex items-center justify-between gap-3 border-b border-gray-200 px-4 py-3 dark:border-gray-800"
              >
                <p class="text-sm font-medium text-gray-800 dark:text-white/90">
                  将导入 {{ importPreviewItems.length }} 个上游
                </p>
                <span class="text-xs text-gray-400">确认前可先核对名称和连接方式</span>
              </div>
              <div class="grid grid-cols-1 gap-3 p-4 md:grid-cols-2">
                <article
                  v-for="item in importPreviewItems"
                  :key="item.index"
                  class="rounded-lg border border-gray-200 p-3 dark:border-gray-800 dark:bg-white/[0.03]"
                >
                  <div class="flex items-start justify-between gap-3">
                    <div class="min-w-0">
                      <p class="truncate text-sm font-semibold text-gray-800 dark:text-white/90">
                        {{ item.config.name || '未命名上游' }}
                      </p>
                      <p class="mt-1 truncate font-mono text-xs text-gray-400">
                        {{
                          item.config.connParams.url ||
                          item.config.connParams.command ||
                          '待补全连接参数'
                        }}
                      </p>
                    </div>
                    <span
                      class="shrink-0 rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-gray-800 dark:text-gray-400"
                    >
                      {{ transportLabel(item.config.transport) }}
                    </span>
                  </div>
                  <div class="mt-3 flex flex-wrap gap-1.5">
                    <span
                      v-for="tag in item.config.tags ?? []"
                      :key="tag"
                      class="bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-300 rounded-full px-2 py-0.5 text-xs"
                    >
                      {{ tag }}
                    </span>
                    <span
                      class="rounded-full px-2 py-0.5 text-xs"
                      :class="
                        item.config.enabled
                          ? 'bg-success-50 text-success-600 dark:bg-success-500/10 dark:text-success-400'
                          : 'bg-gray-100 text-gray-500 dark:bg-gray-800 dark:text-gray-400'
                      "
                    >
                      {{ item.config.enabled ? '启用' : '停用' }}
                    </span>
                  </div>
                </article>
              </div>
            </div>

            <div
              v-if="importResultLabel !== ''"
              class="mt-5 rounded-xl border border-gray-200 dark:border-gray-800"
            >
              <div class="border-b border-gray-200 px-4 py-3 dark:border-gray-800">
                <p class="text-sm font-medium text-gray-800 dark:text-white/90">
                  {{ importResultLabel }}
                </p>
              </div>
              <div class="space-y-3 p-4">
                <div v-if="importCreated.length > 0">
                  <p class="text-success-600 dark:text-success-400 mb-2 text-xs font-medium">
                    导入成功
                  </p>
                  <div class="flex flex-wrap gap-2">
                    <span
                      v-for="item in importCreated"
                      :key="`created-${item.index}`"
                      class="bg-success-50 text-success-600 dark:bg-success-500/10 dark:text-success-400 rounded-full px-2.5 py-1 text-xs"
                    >
                      {{ item.name }}
                    </span>
                  </div>
                </div>
                <div v-if="importFailed.length > 0">
                  <p class="text-error-600 dark:text-error-400 mb-2 text-xs font-medium">
                    需要处理
                  </p>
                  <div class="space-y-2">
                    <div
                      v-for="item in importFailed"
                      :key="`failed-${item.index}`"
                      class="bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400 rounded-lg px-3 py-2 text-sm"
                    >
                      <p class="font-medium">{{ item.name || `第 ${item.index + 1} 项` }}</p>
                      <p class="mt-1 text-xs leading-5">
                        {{ item.fields ? Object.values(item.fields).join('；') : item.error }}
                      </p>
                    </div>
                  </div>
                </div>
              </div>
            </div>
          </div>

          <div
            class="flex flex-col-reverse gap-3 border-t border-gray-200 px-5 py-4 sm:flex-row sm:items-center sm:justify-end dark:border-gray-800"
          >
            <button
              type="button"
              class="rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
              :disabled="importLoading || importExecuting"
              @click="closeImport"
            >
              关闭
            </button>
            <button
              type="button"
              class="rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
              :disabled="!importCanPreview"
              @click="previewImport"
            >
              {{ importLoading ? '解析中...' : '预览' }}
            </button>
            <button
              type="button"
              class="bg-brand-500 hover:bg-brand-600 rounded-lg px-4 py-2 text-sm font-medium text-white transition disabled:opacity-60"
              :disabled="!importCanSubmit"
              @click="submitImport"
            >
              {{ importExecuting ? '导入中...' : '确认导入' }}
            </button>
          </div>
        </div>
      </div>
    </transition>

    <!-- 工具列表 -->
    <transition name="fade">
      <div
        v-if="toolModalOpen && toolModalUpstream !== null"
        class="fixed inset-0 z-[100001] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
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
                {{ formatToolUpdatedAt(toolModalUpdatedAt) }} · {{ toolModalCountLabel }}
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
              class="bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400 mb-4 rounded-lg px-4 py-2.5 text-sm"
            >
              {{ toolModalError }}
            </p>
            <label v-if="toolModalTools.length > 0" class="relative mb-4 block">
              <span class="sr-only">搜索工具</span>
              <input
                v-model="toolModalSearchKeyword"
                type="search"
                placeholder="搜索工具名称、原始名或描述"
                class="focus:border-brand-300 focus:ring-brand-500/10 h-10 w-full rounded-lg border border-gray-300 bg-white px-3 pr-12 text-sm text-gray-800 shadow-sm transition placeholder:text-gray-400 focus:ring-3 focus:outline-none dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
              />
              <button
                v-if="hasToolModalSearchKeyword"
                type="button"
                class="absolute top-1/2 right-2 -translate-y-1/2 rounded-md px-2 py-1 text-xs text-gray-500 transition hover:bg-gray-100 hover:text-gray-700 dark:text-gray-400 dark:hover:bg-gray-800 dark:hover:text-gray-200"
                @click="toolModalSearchKeyword = ''"
              >
                清空
              </button>
            </label>
            <div v-if="toolModalLoading" class="py-12 text-center text-sm text-gray-400">
              加载中…
            </div>
            <div
              v-else-if="toolModalTools.length === 0"
              class="rounded-xl border border-dashed border-gray-300 px-4 py-10 text-center text-sm text-gray-400 dark:border-gray-700"
            >
              暂无工具缓存，可先刷新工具列表
            </div>
            <div
              v-else-if="filteredToolModalTools.length === 0"
              class="rounded-xl border border-dashed border-gray-300 px-4 py-10 text-center text-sm text-gray-400 dark:border-gray-700"
            >
              没有匹配的工具
            </div>
            <div v-else class="grid grid-cols-1 gap-3 md:grid-cols-2">
              <article
                v-for="tool in filteredToolModalTools"
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
                <p
                  class="mt-3 truncate rounded-lg bg-gray-50 px-3 py-2 font-mono text-xs text-gray-500 dark:bg-gray-800/60 dark:text-gray-400"
                >
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
                <AppSelect
                  v-model="sortSelectedID"
                  :options="sortSelectOptions"
                  aria-label="选择排序上游"
                  @change="syncSelectedSortPosition"
                />
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
                    v-if="
                      up.config.transport === 'stdio' &&
                      normalizeSecurityProfile(up.config.connParams?.securityProfile).mode ===
                        'unrestricted'
                    "
                    class="bg-error-600 rounded-full px-2 py-0.5 text-[10px] font-semibold text-white"
                  >
                    完全放行
                  </span>
                  <span
                    v-else-if="
                      up.config.transport === 'stdio' &&
                      normalizeSecurityProfile(up.config.connParams?.securityProfile).mode ===
                        'strict'
                    "
                    class="bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400 rounded-full px-2 py-0.5 text-[10px] font-semibold"
                  >
                    严格安全
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
