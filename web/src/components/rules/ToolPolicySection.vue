<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  createToolPolicy,
  deleteToolPolicy,
  listToolPolicies,
  setToolPolicyEnabled,
  updateToolPolicy,
  type ToolPolicyRule,
  type ToolPolicyRuleRequest,
  type ToolRoutingStrategy,
} from '@/api/rules'
import type { Upstream } from '@/api/upstreams'
import type { ToolDef } from '@/api/tools'
import { useConfirm } from '@/composables/useConfirm'
import {
  buildToolRulePreview,
  loadCachedToolsForEnabledUpstreams,
  type ToolRulePreviewSummary,
} from '@/utils/rulePreview'
import {
  cachePolicyLabel,
  normalizeIgnoredRiskTags,
  normalizePolicyRiskTags,
  routingStrategyLabel,
  toolPolicyToRequest,
  TOOL_POLICY_AUTO_RISK_TAGS,
  TOOL_POLICY_RISK_TAG_PRESETS,
} from '@/utils/toolPolicy'

const props = defineProps<{
  upstreams: Upstream[]
}>()

const emit = defineEmits<{
  (e: 'toast', message: string): void
}>()

const { confirm } = useConfirm()

const rules = ref<ToolPolicyRule[]>([])
const loading = ref(false)
const errorMessage = ref('')
const busy = ref<Set<string>>(new Set())
const modalOpen = ref(false)
const editing = ref<ToolPolicyRule | null>(null)
const saving = ref(false)
const formError = ref('')
const tagInput = ref('')
const toolsByUpstream = ref<Record<string, ToolDef[]>>({})

const form = ref<ToolPolicyRuleRequest>({
  pattern: '',
  isRegex: false,
  enabled: true,
  sortOrder: 0,
  routingStrategy: '',
  cacheEnabled: false,
  cacheTtlSeconds: 60,
  riskTags: [],
  ignoredRiskTags: [],
})

const isEdit = computed(() => editing.value !== null)

const draftPreviewSummary = computed<ToolRulePreviewSummary>(() =>
  buildToolRulePreview(
    {
      scopeType: 'all',
      pattern: form.value.pattern,
      isRegex: form.value.isRegex,
      enabled: form.value.enabled,
    },
    props.upstreams,
    toolsByUpstream.value,
    {
      matchField: 'exposedName',
      emptyLabel: '填写匹配模式后显示预计命中的工具',
      disabledLabel: '策略未启用',
      noHitLabel: '当前缓存暂未命中工具',
      hitLabel: (count) => `预计命中 ${count} 个工具`,
    },
  ),
)

const enabledCount = computed(() => rules.value.filter((rule) => rule.enabled).length)

function setBusy(key: string, on: boolean): void {
  const next = new Set(busy.value)
  if (on) next.add(key)
  else next.delete(key)
  busy.value = next
}

function isBusy(id: string, action: string): boolean {
  return busy.value.has(`${id}:${action}`)
}

async function load(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const list = await listToolPolicies()
    list.sort((a, b) => a.sortOrder - b.sortOrder)
    rules.value = list
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '加载工具策略失败'
  } finally {
    loading.value = false
  }
}

async function loadToolPreview(): Promise<void> {
  toolsByUpstream.value = await loadCachedToolsForEnabledUpstreams(props.upstreams)
}

async function reload(): Promise<void> {
  await Promise.all([load(), loadToolPreview()])
}

onMounted(reload)

watch(
  () => props.upstreams.map((up) => `${up.id}:${up.config.enabled}`).join('|'),
  () => {
    void loadToolPreview()
  },
)

function openCreate(): void {
  editing.value = null
  formError.value = ''
  tagInput.value = ''
  form.value = {
    pattern: '',
    isRegex: false,
    enabled: true,
    sortOrder: nextSortOrder(),
    routingStrategy: '',
    cacheEnabled: false,
    cacheTtlSeconds: 60,
    riskTags: [],
    ignoredRiskTags: [],
  }
  modalOpen.value = true
}

function openEdit(rule: ToolPolicyRule): void {
  editing.value = rule
  formError.value = ''
  tagInput.value = ''
  form.value = toolPolicyToRequest({
    ...rule,
    routingStrategy: rule.routingStrategy ?? '',
    cacheTtlSeconds: rule.cacheTtlSeconds ?? 0,
    riskTags: rule.riskTags ?? [],
    ignoredRiskTags: rule.ignoredRiskTags ?? [],
  })
  if (form.value.cacheEnabled && form.value.cacheTtlSeconds <= 0) {
    form.value.cacheTtlSeconds = 60
  }
  modalOpen.value = true
}

function closeModal(): void {
  modalOpen.value = false
  editing.value = null
}

function validate(): boolean {
  const value = form.value
  if (value.pattern.trim() === '') {
    formError.value = '匹配模式不能为空'
    return false
  }
  if (value.pattern.length > 200) {
    formError.value = '匹配模式长度不能超过 200 个字符'
    return false
  }
  if (value.cacheEnabled && (value.cacheTtlSeconds <= 0 || value.cacheTtlSeconds > 3600)) {
    formError.value = '缓存 TTL 需在 1 到 3600 秒之间'
    return false
  }
  formError.value = ''
  return true
}

async function submit(): Promise<void> {
  if (!validate()) return
  saving.value = true
  try {
    const payload = toolPolicyToRequest(form.value)
    if (isEdit.value && editing.value !== null) {
      await updateToolPolicy(editing.value.id, payload)
      emit('toast', '工具策略已更新')
    } else {
      await createToolPolicy(payload)
      emit('toast', '工具策略已创建')
    }
    closeModal()
    await load()
  } catch (err) {
    formError.value = err instanceof Error ? err.message : '保存失败'
  } finally {
    saving.value = false
  }
}

async function toggleEnabled(rule: ToolPolicyRule): Promise<void> {
  const key = `${rule.id}:toggle`
  if (busy.value.has(key)) return
  setBusy(key, true)
  try {
    await setToolPolicyEnabled(rule.id, !rule.enabled)
    await load()
  } catch (err) {
    emit('toast', err instanceof Error ? err.message : '操作失败')
  } finally {
    setBusy(key, false)
  }
}

async function askDelete(rule: ToolPolicyRule): Promise<void> {
  const ok = await confirm({
    title: '确认删除',
    message: `确定删除工具策略「${rule.pattern}」吗？该操作不可恢复。`,
    confirmText: '删除',
    tone: 'danger',
  })
  if (!ok) return
  try {
    await deleteToolPolicy(rule.id)
    emit('toast', '工具策略已删除')
    await load()
  } catch (err) {
    emit('toast', err instanceof Error ? err.message : '删除失败')
  }
}

async function move(rule: ToolPolicyRule, direction: -1 | 1): Promise<void> {
  const idx = rules.value.findIndex((item) => item.id === rule.id)
  const target = idx + direction
  if (idx < 0 || target < 0 || target >= rules.value.length) return

  const a = rules.value[idx]
  const b = rules.value[target]
  const aOrder = a.sortOrder
  const bOrder = b.sortOrder

  const reordered = [...rules.value]
  reordered[idx] = { ...b, sortOrder: aOrder }
  reordered[target] = { ...a, sortOrder: bOrder }
  reordered.sort((x, y) => x.sortOrder - y.sortOrder)
  rules.value = reordered

  try {
    await Promise.all([
      updateToolPolicy(a.id, toolPolicyToRequest({ ...a, sortOrder: bOrder })),
      updateToolPolicy(b.id, toolPolicyToRequest({ ...b, sortOrder: aOrder })),
    ])
    await load()
  } catch (err) {
    emit('toast', err instanceof Error ? err.message : '排序失败')
    await load()
  }
}

function nextSortOrder(): number {
  if (rules.value.length === 0) return 0
  return Math.max(...rules.value.map((rule) => rule.sortOrder)) + 1
}

function addTag(tag: string): void {
  form.value.riskTags = normalizePolicyRiskTags([...form.value.riskTags, tag])
  tagInput.value = ''
}

function removeTag(tag: string): void {
  form.value.riskTags = form.value.riskTags.filter((item) => item !== tag)
}

function togglePresetTag(tag: string): void {
  if (form.value.riskTags.includes(tag)) {
    removeTag(tag)
  } else {
    addTag(tag)
  }
}

function toggleIgnoredRiskTag(key: string): void {
  const current = new Set(form.value.ignoredRiskTags)
  if (current.has(key)) current.delete(key)
  else current.add(key)
  form.value.ignoredRiskTags = normalizeIgnoredRiskTags(Array.from(current))
}

function onTagInputKeydown(event: KeyboardEvent): void {
  if (event.key !== 'Enter') return
  event.preventDefault()
  if (tagInput.value.trim() !== '') addTag(tagInput.value)
}

function routingClass(strategy?: ToolRoutingStrategy): string {
  if (strategy === 'priority_fill') return 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-300'
  if (strategy === 'round_robin') return 'bg-brand-50 text-brand-700 dark:bg-brand-500/10 dark:text-brand-300'
  return 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'
}

function routingStrategyDescription(strategy?: ToolRoutingStrategy): string {
  if (strategy === 'priority_fill') {
    return '优先调用排序靠前的健康来源；失败或不可用时自动尝试后续来源。'
  }
  if (strategy === 'round_robin') {
    return '在多个健康来源间轮流分配调用，适合同名工具能力一致的场景。'
  }
  return '跟随系统设置里的全局路由策略，通常保持默认即可。'
}

defineExpose({ reload })
</script>

<template>
  <section
    class="flex flex-col rounded-2xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-white/[0.03]"
  >
    <div class="flex items-center justify-between border-b border-gray-200 px-5 py-4 dark:border-gray-800">
      <div>
        <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">工具策略</h3>
        <p class="mt-0.5 text-xs text-gray-500 dark:text-gray-400">
          共 {{ rules.length }} 条策略，{{ enabledCount }} 条启用
        </p>
      </div>
      <button
        type="button"
        class="inline-flex items-center gap-1.5 rounded-lg bg-brand-500 px-3 py-2 text-sm font-medium text-white transition hover:bg-brand-600 disabled:opacity-50"
        @click="openCreate"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="M12 5v14M5 12h14" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
        </svg>
        新建策略
      </button>
    </div>

    <p
      v-if="errorMessage !== ''"
      class="m-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
    >
      {{ errorMessage }}
    </p>

    <div v-if="loading" class="px-5 py-10 text-center text-sm text-gray-400">
      加载中…
    </div>
    <div v-else-if="rules.length === 0" class="px-5 py-10 text-center text-sm text-gray-400">
      暂无工具策略，可按对外工具名匹配后覆盖路由、缓存或提示标签
    </div>
    <div v-else class="grid grid-cols-1 gap-3 p-4 md:grid-cols-2 2xl:grid-cols-3">
      <div
        v-for="(rule, index) in rules"
        :key="rule.id"
        class="flex flex-col rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-white/[0.03]"
      >
        <div class="mb-3 flex items-start justify-between gap-2">
          <AppTooltip :content="rule.pattern" placement="bottom-start" class="min-w-0 flex-1">
            <code class="block truncate rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-800 dark:bg-gray-800 dark:text-gray-200">{{ rule.pattern }}</code>
          </AppTooltip>
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

        <div class="mb-3 flex flex-wrap gap-1.5">
          <span class="rounded-full px-2 py-0.5 text-[11px] font-medium" :class="routingClass(rule.routingStrategy)">
            {{ routingStrategyLabel(rule.routingStrategy ?? '') }}
          </span>
          <span class="rounded-full bg-gray-100 px-2 py-0.5 text-[11px] font-medium text-gray-600 dark:bg-gray-800 dark:text-gray-300">
            {{ rule.isRegex ? '正则' : '精确' }}
          </span>
          <span
            class="rounded-full px-2 py-0.5 text-[11px] font-medium"
            :class="rule.cacheEnabled ? 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-300' : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'"
          >
            {{ cachePolicyLabel(rule) }}
          </span>
        </div>

        <div v-if="(rule.riskTags ?? []).length > 0" class="mb-3 flex flex-wrap gap-1.5">
          <span
            v-for="tag in rule.riskTags"
            :key="tag"
            class="rounded-full bg-warning-50 px-2 py-0.5 text-[11px] text-warning-700 dark:bg-warning-500/10 dark:text-warning-300"
          >
            {{ tag }}
          </span>
        </div>

        <div v-if="(rule.ignoredRiskTags ?? []).length > 0" class="mb-3 flex flex-wrap gap-1.5">
          <span
            v-for="tag in TOOL_POLICY_AUTO_RISK_TAGS.filter((item) => rule.ignoredRiskTags?.includes(item.key))"
            :key="tag.key"
            class="rounded-full bg-gray-100 px-2 py-0.5 text-[11px] text-gray-600 dark:bg-gray-800 dark:text-gray-300"
          >
            忽略{{ tag.label }}
          </span>
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
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M6 15l6-6 6 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
            </button>
            <button
              type="button"
              class="rounded-md border border-gray-200 p-1 text-gray-500 hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:hover:bg-gray-800"
              :disabled="index === rules.length - 1"
              aria-label="下移"
              @click="move(rule, 1)"
            >
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="M6 9l6 6 6-6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" /></svg>
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

    <transition name="fade">
      <div
        v-if="modalOpen"
        class="fixed inset-0 z-[100001] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
        @click.self="closeModal"
      >
        <div class="max-h-[calc(100vh-2rem)] w-full max-w-2xl overflow-y-auto rounded-2xl border border-gray-200 bg-white p-6 shadow-xl dark:border-gray-800 dark:bg-gray-900">
          <h3 class="mb-4 text-base font-semibold text-gray-800 dark:text-white/90">
            {{ isEdit ? '编辑工具策略' : '新建工具策略' }}
          </h3>

          <p
            v-if="formError !== ''"
            class="mb-4 rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400"
          >
            {{ formError }}
          </p>

          <div class="grid grid-cols-1 gap-4 lg:grid-cols-2">
            <div class="lg:col-span-2">
              <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">匹配对外工具名</label>
              <input
                v-model="form.pattern"
                type="text"
                placeholder="例如 read_file 或 read_.+"
                class="w-full rounded-lg border border-gray-300 px-3.5 py-2.5 text-sm text-gray-800 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
              />
            </div>

            <div class="flex items-center justify-between gap-4 rounded-lg border border-gray-200 px-3 py-2.5 dark:border-gray-700">
              <span class="text-sm text-gray-700 dark:text-gray-300">使用正则匹配</span>
              <button
                type="button"
                role="switch"
                :aria-checked="form.isRegex"
                class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition"
                :class="form.isRegex ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
                @click="form.isRegex = !form.isRegex"
              >
                <span class="inline-block h-4 w-4 transform rounded-full bg-white transition" :class="form.isRegex ? 'translate-x-6' : 'translate-x-1'"></span>
              </button>
            </div>

            <div class="flex items-center justify-between gap-4 rounded-lg border border-gray-200 px-3 py-2.5 dark:border-gray-700">
              <span class="text-sm text-gray-700 dark:text-gray-300">启用策略</span>
              <button
                type="button"
                role="switch"
                :aria-checked="form.enabled"
                class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition"
                :class="form.enabled ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
                @click="form.enabled = !form.enabled"
              >
                <span class="inline-block h-4 w-4 transform rounded-full bg-white transition" :class="form.enabled ? 'translate-x-6' : 'translate-x-1'"></span>
              </button>
            </div>

            <section class="rounded-xl border border-gray-200 px-4 py-3 dark:border-gray-700 lg:col-span-2">
              <div>
                <h4 class="text-sm font-medium text-gray-700 dark:text-gray-300">调用策略</h4>
                <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  命中该工具策略后，可单独覆盖路由方式，并按需缓存稳定查询结果。
                </p>
              </div>

              <div class="mt-3 divide-y divide-gray-100 rounded-lg bg-gray-50 px-3 dark:divide-gray-800 dark:bg-white/[0.03]">
                <label class="grid gap-2 py-3 sm:grid-cols-[minmax(0,1fr)_14rem] sm:items-start sm:gap-4">
                  <span>
                    <span class="block text-sm font-medium text-gray-700 dark:text-gray-300">路由策略</span>
                    <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">
                      {{ routingStrategyDescription(form.routingStrategy) }}
                    </span>
                  </span>
                  <select
                    v-model="form.routingStrategy"
                    class="w-full rounded-lg border border-gray-300 bg-white px-3.5 py-2.5 text-sm text-gray-800 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
                  >
                    <option value="">不覆盖全局策略</option>
                    <option value="priority_fill">优先顺序</option>
                    <option value="round_robin">轮询</option>
                  </select>
                </label>

                <div class="grid gap-2 py-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-center sm:gap-4">
                  <div>
                    <p class="text-sm font-medium text-gray-700 dark:text-gray-300">成功结果缓存</p>
                    <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                      仅缓存成功调用结果，命中时直接复用。
                    </p>
                  </div>
                  <button
                    type="button"
                    role="switch"
                    aria-label="开启或关闭成功结果缓存"
                    :aria-checked="form.cacheEnabled"
                    class="relative inline-flex h-6 w-11 shrink-0 items-center rounded-full transition sm:justify-self-end"
                    :class="form.cacheEnabled ? 'bg-brand-500' : 'bg-gray-300 dark:bg-gray-700'"
                    @click="form.cacheEnabled = !form.cacheEnabled"
                  >
                    <span class="inline-block h-4 w-4 transform rounded-full bg-white transition" :class="form.cacheEnabled ? 'translate-x-6' : 'translate-x-1'"></span>
                  </button>
                </div>

                <label
                  class="grid gap-2 py-3 sm:grid-cols-[minmax(0,1fr)_14rem] sm:items-start sm:gap-4"
                  :class="!form.cacheEnabled ? 'opacity-75' : ''"
                >
                  <span>
                    <span class="block text-sm font-medium text-gray-700 dark:text-gray-300">缓存时间</span>
                    <span class="mt-1 block text-xs leading-5 text-gray-500 dark:text-gray-400">
                      {{ form.cacheEnabled ? '范围 1-3600 秒，适合稳定的查询类工具。' : '开启成功结果缓存后生效。' }}
                    </span>
                  </span>
                  <span class="relative block">
                    <input
                      v-model.number="form.cacheTtlSeconds"
                      type="number"
                      min="1"
                      max="3600"
                      aria-label="成功结果缓存时间，单位秒"
                      class="w-full rounded-lg border border-gray-300 bg-white px-3 py-2.5 pr-12 text-sm text-gray-800 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none disabled:cursor-not-allowed disabled:bg-gray-100 disabled:text-gray-400 dark:border-gray-700 dark:bg-gray-800 dark:text-white/90 dark:disabled:bg-gray-800/60 dark:disabled:text-gray-500"
                      :disabled="!form.cacheEnabled"
                    />
                    <span class="pointer-events-none absolute top-1/2 right-3 -translate-y-1/2 text-xs text-gray-400">
                      秒
                    </span>
                  </span>
                </label>
              </div>
            </section>

            <div class="lg:col-span-2">
              <span class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">风险提示标签</span>
              <div class="mb-2 flex flex-wrap gap-2">
                <button
                  v-for="tag in TOOL_POLICY_RISK_TAG_PRESETS"
                  :key="tag"
                  type="button"
                  class="rounded-full border px-2.5 py-1 text-xs font-medium transition"
                  :class="form.riskTags.includes(tag) ? 'border-warning-300 bg-warning-50 text-warning-700 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-300' : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800'"
                  @click="togglePresetTag(tag)"
                >
                  {{ tag }}
                </button>
              </div>
              <div class="flex gap-2">
                <input
                  v-model="tagInput"
                  type="text"
                  placeholder="自定义标签"
                  class="min-w-0 flex-1 rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-800 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
                  @keydown="onTagInputKeydown"
                />
                <button
                  type="button"
                  class="rounded-lg border border-gray-300 px-3 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                  @click="addTag(tagInput)"
                >
                  添加
                </button>
              </div>
              <div v-if="form.riskTags.length > 0" class="mt-2 flex flex-wrap gap-1.5">
                <button
                  v-for="tag in form.riskTags"
                  :key="tag"
                  type="button"
                  class="rounded-full bg-warning-50 px-2 py-0.5 text-xs text-warning-700 dark:bg-warning-500/10 dark:text-warning-300"
                  @click="removeTag(tag)"
                >
                  {{ tag }} ×
                </button>
              </div>
            </div>

            <div class="lg:col-span-2">
              <span class="mb-2 block text-sm font-medium text-gray-700 dark:text-gray-300">忽略自动风险提示</span>
              <div class="grid grid-cols-1 gap-2 sm:grid-cols-2">
                <button
                  v-for="tag in TOOL_POLICY_AUTO_RISK_TAGS"
                  :key="tag.key"
                  type="button"
                  class="rounded-lg border px-3 py-2 text-left text-xs transition"
                  :class="form.ignoredRiskTags.includes(tag.key) ? 'border-gray-300 bg-gray-100 text-gray-700 dark:border-gray-600 dark:bg-gray-800 dark:text-gray-200' : 'border-gray-200 text-gray-600 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800'"
                  @click="toggleIgnoredRiskTag(tag.key)"
                >
                  <span class="block font-medium">{{ form.ignoredRiskTags.includes(tag.key) ? '已忽略' : '自动识别' }}：{{ tag.label }}</span>
                  <span class="mt-1 block text-[11px] opacity-80">{{ tag.description }}</span>
                </button>
              </div>
            </div>

            <div class="rounded-lg border border-brand-100 bg-brand-50 px-3 py-2 text-xs leading-5 text-brand-700 dark:border-brand-500/20 dark:bg-brand-500/10 dark:text-brand-300 lg:col-span-2">
              <div class="flex items-center justify-between gap-2">
                <span>{{ draftPreviewSummary.label }}</span>
                <span v-if="draftPreviewSummary.hiddenCount > 0" class="shrink-0">
                  +{{ draftPreviewSummary.hiddenCount }}
                </span>
              </div>
              <div v-if="draftPreviewSummary.items.length > 0" class="mt-2 flex flex-wrap gap-1.5">
                <span
                  v-for="item in draftPreviewSummary.items"
                  :key="item.key"
                  class="inline-flex max-w-full items-center rounded-md bg-white px-1.5 py-0.5 text-[11px] text-brand-700 ring-1 ring-brand-200 dark:bg-white/5 dark:text-brand-200 dark:ring-brand-500/20"
                >
                  <span class="truncate">{{ item.exposedName }}</span>
                </span>
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
