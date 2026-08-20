<script setup lang="ts">
/**
 * MCP 级屏蔽规则区块（任务 26.2，Req 9.1、9.11）。
 *
 * 提供独立 MCP 级屏蔽规则的列表、新建/编辑（模态框）、删除（确认）、单条启停、上移/下移排序。
 * 屏蔽规则支持独立启停（enable/disable 端点）；排序通过交换相邻规则 sortOrder 后经 PUT 持久化实现。
 * 风格：Tailwind 工具类 + TailAdmin（卡片 rounded-2xl border、徽章、开关、模态框）。
 */
import { computed, onMounted, ref, watch } from 'vue'
import {
  listFilters,
  createFilter,
  updateFilter,
  deleteFilter,
  setFilterEnabled,
  type FilterRule,
  type FilterRuleRequest,
} from '@/api/rules'
import type { Upstream } from '@/api/upstreams'
import type { ToolDef } from '@/api/tools'
import { useConfirm } from '@/composables/useConfirm'
import {
  buildToolRulePreview,
  createOriginalNameMatcher,
  loadCachedToolsForEnabledUpstreams,
  scopedEnabledUpstreamIDs,
  type ToolRulePreviewSummary,
} from '@/utils/rulePreview'

const props = defineProps<{
  upstreams: Upstream[]
}>()

const emit = defineEmits<{
  (e: 'toast', message: string): void
}>()
const { confirm } = useConfirm()

/** 屏蔽规则列表（按 sortOrder 升序）。 */
const rules = ref<FilterRule[]>([])
const loading = ref(false)
const errorMessage = ref('')

/** 行级繁忙标记（key = ruleId:action）。 */
const busy = ref<Set<string>>(new Set())

/** 模态框开关与编辑目标。 */
const modalOpen = ref(false)
const editing = ref<FilterRule | null>(null)
/** 保存中标记与表单错误。 */
const saving = ref(false)
const formError = ref('')

/** 表单模型。 */
const form = ref<FilterRuleRequest>({
  scopeType: 'all',
  upstreamIds: [],
  pattern: '',
  isRegex: false,
  enabled: true,
  sortOrder: 0,
})

const isEdit = computed(() => editing.value !== null)

interface ToolPreviewItem {
  key: string
  upstreamName: string
  originalName: string
  exposedName: string
}

interface RulePreviewSummary {
  label: string
  items: ToolPreviewItem[]
  hiddenCount: number
}

const toolsByUpstream = ref<Record<string, ToolDef[]>>({})

const previewSummaries = computed<Record<string, RulePreviewSummary>>(() => {
  const summaries: Record<string, RulePreviewSummary> = {}
  for (const rule of rules.value) {
    summaries[rule.id] = buildRulePreviewSummary(rule)
  }
  return summaries
})

const draftPreviewSummary = computed<ToolRulePreviewSummary>(() =>
  buildToolRulePreview(form.value, props.upstreams, toolsByUpstream.value, {
    emptyLabel: '填写匹配模式后显示预计屏蔽影响',
    noHitLabel: '当前缓存暂未命中工具',
    hitLabel: (count) => `预计屏蔽 ${count} 个工具`,
  }),
)

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

/** 加载屏蔽规则（按 sortOrder 升序）。 */
async function load(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const list = await listFilters()
    list.sort((a, b) => a.sortOrder - b.sortOrder)
    rules.value = list
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '加载屏蔽规则失败'
  } finally {
    loading.value = false
  }
}

async function reload(): Promise<void> {
  await Promise.all([load(), loadToolPreview()])
}

async function loadToolPreview(): Promise<void> {
  toolsByUpstream.value = await loadCachedToolsForEnabledUpstreams(props.upstreams)
}

onMounted(reload)

watch(
  () => props.upstreams.map((up) => `${up.id}:${up.config.enabled}`).join('|'),
  () => {
    void loadToolPreview()
  },
)

/** 打开新建模态框（sortOrder 追加到末尾）。 */
function openCreate(): void {
  editing.value = null
  formError.value = ''
  form.value = {
    scopeType: 'all',
    upstreamIds: [],
    pattern: '',
    isRegex: false,
    enabled: true,
    sortOrder: nextSortOrder(),
  }
  modalOpen.value = true
}

/** 打开编辑模态框。 */
function openEdit(rule: FilterRule): void {
  editing.value = rule
  formError.value = ''
  form.value = {
    scopeType: rule.scopeType ?? 'all',
    upstreamIds: rule.upstreamIds ?? [],
    pattern: rule.pattern,
    isRegex: rule.isRegex,
    enabled: rule.enabled,
    sortOrder: rule.sortOrder,
  }
  modalOpen.value = true
}

/** 关闭模态框。 */
function closeModal(): void {
  modalOpen.value = false
  editing.value = null
}

/** 前端基础校验：模式必填、长度限制。 */
function validate(): boolean {
  const f = form.value
  if (f.pattern.trim() === '') {
    formError.value = '匹配模式不能为空'
    return false
  }
  if (f.pattern.length > 200) {
    formError.value = '匹配模式长度不能超过 200 个字符'
    return false
  }
  if (f.scopeType === 'upstreams' && f.upstreamIds.length === 0) {
    formError.value = '选择“指定上游”时至少选择一个上游 MCP'
    return false
  }
  formError.value = ''
  return true
}

/** 提交保存（创建或更新）。 */
async function submit(): Promise<void> {
  if (!validate()) return
  saving.value = true
  try {
    const payload = toPayload(form.value)
    if (isEdit.value && editing.value !== null) {
      await updateFilter(editing.value.id, payload)
      emit('toast', '屏蔽规则已更新')
    } else {
      await createFilter(payload)
      emit('toast', '屏蔽规则已创建')
    }
    closeModal()
    await load()
  } catch (err) {
    formError.value = err instanceof Error ? err.message : '保存失败'
  } finally {
    saving.value = false
  }
}

/** 切换启用/停用（Req 9.11）。 */
async function toggleEnabled(rule: FilterRule): Promise<void> {
  const key = `${rule.id}:toggle`
  if (busy.value.has(key)) return
  setBusy(key, true)
  try {
    await setFilterEnabled(rule.id, !rule.enabled)
    await load()
  } catch (err) {
    emit('toast', err instanceof Error ? err.message : '操作失败')
  } finally {
    setBusy(key, false)
  }
}

/** 请求删除确认。 */
async function askDelete(rule: FilterRule): Promise<void> {
  const ok = await confirm({
    title: '确认删除',
    message: `确定删除屏蔽规则「${rule.pattern}」吗？该操作不可恢复。`,
    confirmText: '删除',
    tone: 'danger',
  })
  if (!ok) return
  try {
    await deleteFilter(rule.id)
    emit('toast', '屏蔽规则已删除')
    await load()
  } catch (err) {
    emit('toast', err instanceof Error ? err.message : '删除失败')
  }
}

/**
 * 上移/下移：交换相邻两条规则的 sortOrder 后分别 PUT 持久化（Req 9.1）。
 * 乐观更新本地列表，失败时回滚重载。
 */
async function move(rule: FilterRule, direction: -1 | 1): Promise<void> {
  const idx = rules.value.findIndex((r) => r.id === rule.id)
  const target = idx + direction
  if (idx < 0 || target < 0 || target >= rules.value.length) return

  const a = rules.value[idx]
  const b = rules.value[target]
  const aOrder = a.sortOrder
  const bOrder = b.sortOrder

  // 乐观更新。
  const reordered = [...rules.value]
  reordered[idx] = { ...b, sortOrder: aOrder }
  reordered[target] = { ...a, sortOrder: bOrder }
  reordered.sort((x, y) => x.sortOrder - y.sortOrder)
  rules.value = reordered

  try {
    await Promise.all([
      updateFilter(a.id, toRequest(a, bOrder)),
      updateFilter(b.id, toRequest(b, aOrder)),
    ])
    await load()
  } catch (err) {
    emit('toast', err instanceof Error ? err.message : '排序失败')
    await load()
  }
}

/** 将规则连同新的 sortOrder 转为更新请求体。 */
function toRequest(rule: FilterRule, sortOrder: number): FilterRuleRequest {
  return toPayload({
    pattern: rule.pattern,
    isRegex: rule.isRegex,
    enabled: rule.enabled,
    sortOrder,
    scopeType: rule.scopeType ?? 'all',
    upstreamIds: rule.upstreamIds ?? [],
  })
}

function toPayload(value: FilterRuleRequest): FilterRuleRequest {
  return {
    ...value,
    upstreamIds: value.scopeType === 'upstreams' ? [...new Set(value.upstreamIds)] : [],
  }
}

function nextSortOrder(): number {
  if (rules.value.length === 0) return 0
  return Math.max(...rules.value.map((rule) => rule.sortOrder)) + 1
}

function upstreamName(id: string): string {
  return props.upstreams.find((u) => u.id === id)?.config.name ?? id
}

function scopeLabel(rule: FilterRule): string {
  if ((rule.scopeType ?? 'all') === 'all') return '全部上游'
  const ids = rule.upstreamIds ?? []
  if (ids.length === 0) return '未选择上游'
  const names = ids.map(upstreamName)
  if (names.length <= 3) return names.join('、')
  return `${names.slice(0, 3).join('、')} 等 ${names.length} 个`
}

function previewSummary(rule: FilterRule): RulePreviewSummary {
  return previewSummaries.value[rule.id] ?? buildRulePreviewSummary(rule)
}

function buildRulePreviewSummary(rule: FilterRule): RulePreviewSummary {
  if (!rule.enabled) {
    return { label: '规则未启用', items: [], hiddenCount: 0 }
  }

  const ids = scopedEnabledUpstreamIDs(rule, props.upstreams)
  if (ids.length === 0) {
    return { label: '作用范围内暂无启用上游', items: [], hiddenCount: 0 }
  }

  const matcher = createOriginalNameMatcher(rule.pattern, rule.isRegex)
  if (matcher === null) {
    return { label: '当前缓存未命中工具', items: [], hiddenCount: 0 }
  }

  const hits: ToolPreviewItem[] = []
  for (const upstreamID of ids) {
    const tools = toolsByUpstream.value[upstreamID] ?? []
    for (const tool of tools) {
      if (!matcher(tool.originalName)) continue
      hits.push({
        key: `${upstreamID}:${tool.originalName}:${hits.length}`,
        upstreamName: upstreamName(upstreamID),
        originalName: tool.originalName,
        exposedName: tool.name,
      })
    }
  }

  return {
    label: hits.length > 0 ? `当前缓存命中 ${hits.length} 个工具` : '当前缓存未命中工具',
    items: hits.slice(0, 3),
    hiddenCount: Math.max(0, hits.length - 3),
  }
}

defineExpose({ reload })
</script>

<template>
  <section
    class="flex flex-col rounded-2xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-white/[0.03]"
  >
    <!-- 区块头部 -->
    <div class="flex items-center justify-between border-b border-gray-200 px-5 py-4 dark:border-gray-800">
      <div>
        <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">MCP 级屏蔽过滤</h3>
        <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
          共 {{ rules.length }} 条规则，可作用于全部上游或指定多个上游
        </p>
      </div>
      <button
        type="button"
        class="inline-flex items-center gap-1.5 rounded-lg bg-brand-500 px-3 py-2 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-50"
        @click="openCreate"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none">
          <path d="M12 5v14M5 12h14" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
        </svg>
        新建屏蔽规则
      </button>
    </div>

    <p
      v-if="errorMessage !== ''"
      class="m-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
    >
      {{ errorMessage }}
    </p>

    <!-- 列表：卡片网格（响应式，移动端友好） -->
    <div v-if="loading" class="rounded-xl border border-gray-200 px-5 py-10 text-center text-sm text-gray-400 dark:border-gray-800">
      加载中…
    </div>
    <div v-else-if="rules.length === 0" class="rounded-xl border border-dashed border-gray-300 px-5 py-10 text-center text-sm text-gray-400 dark:border-gray-700">
      暂无屏蔽规则，点击「新建屏蔽规则」开始添加
    </div>
    <div v-else class="grid grid-cols-1 gap-3 md:grid-cols-2 2xl:grid-cols-3">
      <div
        v-for="(rule, index) in rules"
        :key="rule.id"
        class="flex flex-col rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-white/[0.03]"
      >
        <div class="mb-3 flex items-start justify-between gap-2">
          <AppTooltip :content="rule.pattern" placement="bottom-start" class="min-w-0 flex-1">
            <code class="block truncate rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-800 dark:bg-gray-800 dark:text-gray-200">{{ rule.pattern }}</code>
          </AppTooltip>
          <div class="flex shrink-0 items-center gap-2">
            <span
              class="inline-flex items-center rounded-full px-2 py-0.5 text-xs"
              :class="rule.isRegex
                ? 'bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400'
                : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'"
            >
              {{ rule.isRegex ? '正则' : '精确' }}
            </span>
            <button
              type="button"
              role="switch"
              :aria-checked="rule.enabled"
              :disabled="isBusy(rule.id, 'toggle')"
              class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition disabled:opacity-60"
              :class="rule.enabled ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
              @click="toggleEnabled(rule)"
            >
              <span
                class="inline-block h-4 w-4 transform rounded-full bg-white transition"
                :class="rule.enabled ? 'translate-x-6' : 'translate-x-1'"
              ></span>
            </button>
          </div>
        </div>
        <div class="mb-3 rounded-lg bg-gray-50 px-2.5 py-2 text-xs text-gray-500 dark:bg-gray-800/60 dark:text-gray-400">
          作用范围：{{ scopeLabel(rule) }}
        </div>
        <div
          class="mb-3 rounded-lg border border-warning-200 bg-warning-50 px-2.5 py-2 text-xs text-warning-700 dark:border-warning-500/20 dark:bg-warning-500/10 dark:text-warning-300"
        >
          <div class="flex items-center justify-between gap-2">
            <span>{{ previewSummary(rule).label }}</span>
            <span v-if="previewSummary(rule).hiddenCount > 0" class="shrink-0 text-warning-600 dark:text-warning-300">
              +{{ previewSummary(rule).hiddenCount }}
            </span>
          </div>
          <div v-if="previewSummary(rule).items.length > 0" class="mt-2 flex flex-wrap gap-1.5">
            <AppTooltip
              v-for="item in previewSummary(rule).items"
              :key="item.key"
              :content="item.exposedName !== item.originalName ? `对外名称：${item.exposedName}` : item.originalName"
              placement="bottom"
            >
              <span
                class="inline-flex max-w-full items-center rounded-md bg-white px-1.5 py-0.5 text-[11px] text-warning-700 ring-1 ring-warning-200 dark:bg-white/5 dark:text-warning-200 dark:ring-warning-500/20"
              >
                <span class="truncate">{{ item.upstreamName }} / {{ item.originalName }}</span>
              </span>
            </AppTooltip>
          </div>
        </div>
        <div class="mt-auto flex items-center justify-between border-t border-gray-100 pt-2.5 dark:border-gray-800">
          <div class="flex items-center gap-1">
            <button
              type="button"
              class="rounded-md border border-gray-200 p-1 text-gray-500 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:hover:bg-gray-800"
              :disabled="index === 0"
              aria-label="上移"
              @click="move(rule, -1)"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none"><path d="M6 15l6-6 6 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
            </button>
            <button
              type="button"
              class="rounded-md border border-gray-200 p-1 text-gray-500 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:hover:bg-gray-800"
              :disabled="index === rules.length - 1"
              aria-label="下移"
              @click="move(rule, 1)"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none"><path d="M6 9l6 6 6-6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
            </button>
          </div>
          <div class="flex items-center gap-1.5">
            <button
              type="button"
              class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-brand-600 hover:bg-brand-50 dark:text-brand-400 dark:hover:bg-brand-500/10"
              @click="openEdit(rule)"
            >
              编辑
            </button>
            <button
              type="button"
              class="rounded-lg px-2.5 py-1.5 text-xs font-medium text-error-600 hover:bg-error-50 dark:text-error-400 dark:hover:bg-error-500/10"
              @click="askDelete(rule)"
            >
              删除
            </button>
          </div>
        </div>
      </div>
    </div>

    <!-- 创建/编辑模态框 -->
    <transition name="fade">
      <div
        v-if="modalOpen"
        class="fixed inset-0 z-[100001] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
      >
        <div class="max-h-[calc(100vh-2rem)] w-full max-w-lg overflow-y-auto rounded-2xl border border-gray-200 bg-white p-6 shadow-xl dark:border-gray-800 dark:bg-gray-900">
          <h3 class="mb-4 text-base font-semibold text-gray-800 dark:text-white/90">
            {{ isEdit ? '编辑屏蔽规则' : '新建屏蔽规则' }}
          </h3>

          <p
            v-if="formError !== ''"
            class="mb-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
          >
            {{ formError }}
          </p>

          <div class="flex flex-col gap-4">
            <div>
              <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">匹配模式</label>
              <input
                v-model="form.pattern"
                type="text"
                placeholder="工具原始名称或正则表达式"
                class="w-full rounded-lg border border-gray-300 px-3.5 py-2.5 text-sm text-gray-800 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
              />
            </div>
            <div class="flex items-center justify-between gap-4 rounded-lg border border-gray-200 px-3 py-2.5 dark:border-gray-700">
              <span class="text-sm text-gray-700 dark:text-gray-300">使用正则匹配（完整匹配）</span>
              <button
                type="button"
                role="switch"
                :aria-checked="form.isRegex"
                class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition"
                :class="form.isRegex ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
                @click="form.isRegex = !form.isRegex"
              >
                <span
                  class="inline-block h-4 w-4 transform rounded-full bg-white transition"
                  :class="form.isRegex ? 'translate-x-6' : 'translate-x-1'"
                ></span>
              </button>
            </div>
            <div>
              <label class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">作用范围</label>
              <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <label class="flex rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-700 dark:border-gray-700 dark:text-gray-300">
                  <input v-model="form.scopeType" type="radio" value="all" class="mt-0.5 mr-2 h-4 w-4 border-gray-300 text-brand-500 focus:ring-brand-400" />
                  全部上游
                </label>
                <label class="flex rounded-lg border border-gray-200 px-3 py-2 text-sm text-gray-700 dark:border-gray-700 dark:text-gray-300">
                  <input v-model="form.scopeType" type="radio" value="upstreams" class="mt-0.5 mr-2 h-4 w-4 border-gray-300 text-brand-500 focus:ring-brand-400" />
                  指定上游
                </label>
              </div>
              <div v-if="form.scopeType === 'upstreams'" class="mt-3 grid max-h-44 grid-cols-1 gap-2 overflow-y-auto rounded-lg border border-gray-200 p-3 dark:border-gray-700 sm:grid-cols-2">
                <label
                  v-for="up in upstreams"
                  :key="up.id"
                  class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300"
                >
                  <input v-model="form.upstreamIds" type="checkbox" :value="up.id" class="h-4 w-4 rounded border-gray-300 text-brand-500 focus:ring-brand-400" />
                  <span class="min-w-0 truncate">{{ up.config.name }}</span>
                </label>
                <p v-if="upstreams.length === 0" class="text-sm text-gray-400">暂无上游 MCP</p>
              </div>
            </div>
            <div class="flex items-center justify-between gap-4 rounded-lg border border-gray-200 px-3 py-2.5 dark:border-gray-700">
              <span class="text-sm text-gray-700 dark:text-gray-300">启用该规则</span>
              <button
                type="button"
                role="switch"
                :aria-checked="form.enabled"
                class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition"
                :class="form.enabled ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
                @click="form.enabled = !form.enabled"
              >
                <span
                  class="inline-block h-4 w-4 transform rounded-full bg-white transition"
                  :class="form.enabled ? 'translate-x-6' : 'translate-x-1'"
                ></span>
              </button>
            </div>
            <div class="rounded-lg border border-warning-200 bg-warning-50 px-3 py-2 text-xs text-warning-700 dark:border-warning-500/20 dark:bg-warning-500/10 dark:text-warning-300">
              <div class="flex items-center justify-between gap-2">
                <span>{{ draftPreviewSummary.label }}</span>
                <span v-if="draftPreviewSummary.hiddenCount > 0" class="shrink-0 text-warning-600 dark:text-warning-300">
                  +{{ draftPreviewSummary.hiddenCount }}
                </span>
              </div>
              <div v-if="draftPreviewSummary.items.length > 0" class="mt-2 flex flex-wrap gap-1.5">
                <AppTooltip
                  v-for="item in draftPreviewSummary.items"
                  :key="item.key"
                  :content="`${item.upstreamName} / ${item.originalName}`"
                  placement="bottom"
                >
                  <span
                    class="inline-flex max-w-full items-center rounded-md bg-white px-1.5 py-0.5 text-[11px] text-warning-700 ring-1 ring-warning-200 dark:bg-white/5 dark:text-warning-200 dark:ring-warning-500/20"
                  >
                    <span class="truncate">{{ item.upstreamName }} / {{ item.originalName }}</span>
                  </span>
                </AppTooltip>
              </div>
            </div>
          </div>

          <div class="mt-6 flex justify-end gap-3">
            <button
              type="button"
              class="rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
              @click="closeModal"
            >
              取消
            </button>
            <button
              type="button"
              class="rounded-lg bg-brand-500 px-4 py-2 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-50"
              :disabled="saving"
              @click="submit"
            >
              {{ saving ? '保存中…' : '保存' }}
            </button>
          </div>
        </div>
      </div>
    </transition>

  </section>
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
