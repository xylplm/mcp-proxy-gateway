<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import { getAggregatedTools, type ToolDef, type ToolDetail, type ToolSource } from '@/api/tools'
import type { UpstreamRateLimits } from '@/api/rateLimits'
import { RefreshIcon } from '@/icons'

type ConflictFilter = 'all' | 'conflict' | 'multi'

const loading = ref(false)
const refreshing = ref(false)
const loadError = ref('')
const toolDetails = ref<ToolDetail[]>([])
const searchKeyword = ref('')
const selectedUpstream = ref('')
const conflictFilter = ref<ConflictFilter>('all')
const selectedToolName = ref('')
const detailOpen = ref(false)

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

const visibleToolDetails = computed(() => {
  const keyword = normalizedSearchKeyword.value
  return toolDetails.value.filter((detail) => {
    if (selectedUpstream.value !== '' && !(detail.sources ?? []).some((source) => source.upstreamId === selectedUpstream.value)) {
      return false
    }
    if (conflictFilter.value === 'conflict' && !detail.tool.schemaConflict) return false
    if (conflictFilter.value === 'multi' && (detail.tool.sourceCount ?? detail.sources?.length ?? 1) <= 1) return false
    if (keyword !== '' && !toolSearchText(detail).includes(keyword)) return false
    return true
  })
})

const selectedToolDetail = computed<ToolDetail | null>(() => {
  if (selectedToolName.value === '') return null
  return toolDetailsByName.value.get(selectedToolName.value) ?? null
})

const totalSourceCount = computed(() =>
  toolDetails.value.reduce((sum, detail) => sum + (detail.sources?.length ?? 0), 0),
)
const multiSourceCount = computed(
  () => toolDetails.value.filter((detail) => (detail.tool.sourceCount ?? detail.sources?.length ?? 1) > 1).length,
)
const schemaConflictCount = computed(
  () => toolDetails.value.filter((detail) => detail.tool.schemaConflict === true).length,
)
const visibleCountLabel = computed(() => {
  if (hasSearchKeyword.value || selectedUpstream.value !== '' || conflictFilter.value !== 'all') {
    return `${visibleToolDetails.value.length.toLocaleString('zh-CN')} / ${toolDetails.value.length.toLocaleString('zh-CN')}`
  }
  return toolDetails.value.length.toLocaleString('zh-CN')
})

async function loadTools(showLoading = true): Promise<void> {
  if (showLoading) loading.value = true
  else refreshing.value = true
  loadError.value = ''
  try {
    const result = await getAggregatedTools()
    toolDetails.value = result.toolDetails.length > 0
      ? result.toolDetails
      : result.tools.map((tool) => ({ tool, sources: [] }))
    ensureSelectedTool()
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : '加载工具目录失败'
  } finally {
    loading.value = false
    refreshing.value = false
  }
}

function ensureSelectedTool(): void {
  if (selectedToolName.value === '') return
  if (!toolDetailsByName.value.has(selectedToolName.value)) {
    selectedToolName.value = ''
    detailOpen.value = false
  }
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
  detailOpen.value = true
}

function closeDetail(): void {
  detailOpen.value = false
}

function resetFilters(): void {
  searchKeyword.value = ''
  selectedUpstream.value = ''
  conflictFilter.value = 'all'
}

function sourceCountText(detail: ToolDetail): string {
  const count = detail.tool.sourceCount ?? detail.sources?.length ?? 1
  return `${count} 个来源`
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
  return 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
}

onMounted(() => {
  void loadTools()
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
        <p class="mt-2 text-2xl font-semibold" :class="schemaConflictCount > 0 ? 'text-warning-700 dark:text-warning-400' : 'text-success-700 dark:text-success-400'">
          {{ schemaConflictCount.toLocaleString('zh-CN') }}
        </p>
        <p class="mt-1 text-xs text-gray-500 dark:text-gray-400">
          多来源 {{ multiSourceCount.toLocaleString('zh-CN') }} 个
        </p>
      </section>
    </div>

    <section :class="[cardClass, 'mb-5']">
      <div class="grid grid-cols-1 gap-3 lg:grid-cols-[minmax(0,1fr)_260px_180px_auto] lg:items-end">
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
        <button
          type="button"
          class="h-10 rounded-lg border border-gray-300 px-4 text-sm font-medium text-gray-700 transition hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          @click="resetFilters"
        >
          重置
        </button>
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
            </div>

            <div
              v-if="selectedToolDetail.tool.schemaConflict"
              class="mt-4 rounded-lg border border-warning-200 bg-warning-50 px-3 py-2 text-xs leading-5 text-warning-700 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-300"
            >
              同名来源的入参 Schema 不完全一致，调用时只会选择与当前展示 Schema 一致的来源。
            </div>

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
                    {{ source.compatible ? '可调用' : 'Schema 不一致' }}
                  </span>
                </div>
                <p class="mt-2 line-clamp-3 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  {{ source.description || '上游未提供有效描述' }}
                </p>
                <p class="mt-2 text-[11px] leading-5 text-gray-400 dark:text-gray-500">
                  限流额度：{{ formatRateLimits(source.rateLimits) }}
                </p>
              </div>
            </div>

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
