<script setup lang="ts">
/**
 * 模板市场接入向导弹窗（TailAdmin 风格 modal）。
 *
 * 覆盖 Req 14.2-14.7：
 * - 分类导航浏览（左侧分类列表）+ 关键字检索（顶部搜索框）；
 * - 模板卡片列表，点击查看详情（简介、文档链接、传输类型、占位参数）；
 * - 选择模板后请求预填充数据并通过 select 事件交给父组件打开创建表单（Req 14.7）。
 *
 * 数据来源：@/api/templates（后端 Template_Market 服务，路由待暴露，见该模块说明）。
 */
import { computed, onUnmounted, ref, watch } from 'vue'
import {
  listTemplates,
  listTemplateCategories,
  getTemplatePrefill,
  CATEGORY_LABELS,
  type CategoryView,
  type Template,
  type TemplateCategory,
  type PrefillForm,
} from '@/api/templates'
import { TRANSPORT_OPTIONS } from '@/api/upstreams'
import { StaredIcon } from '@/icons'
import {
  filterTemplatesByPreference,
  loadTemplateMarketPrefs,
  markTemplateRecentlyUsed,
  saveTemplateMarketPrefs,
  toggleTemplateFavorite,
  type TemplateMarketPrefs,
  type TemplateMarketViewFilter,
} from '@/utils/templateMarketPrefs'
import { templateCardChips, templateMetaChips, templateMetadataSearchText, type TemplateMetaChip } from '@/utils/templateMetadata'

const props = defineProps<{ open: boolean }>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'select', prefill: PrefillForm): void
}>()

/** 分类视图（导航）。 */
const categories = ref<CategoryView[]>([])
/** 当前模板列表。 */
const templates = ref<Template[]>([])
/** 选中的分类；null 表示全部。 */
const activeCategory = ref<TemplateCategory | null>(null)
/** 关键字检索输入。 */
const keyword = ref('')
const viewFilter = ref<TemplateMarketViewFilter>('all')
/** 当前查看详情的模板。 */
const detail = ref<Template | null>(null)
/** 加载与错误状态。 */
const loading = ref(false)
const errorMessage = ref('')
/** 选择模板（请求预填充）的进行中标志。 */
const selecting = ref(false)
const prefs = ref<TemplateMarketPrefs>(loadTemplateMarketPrefs())

const locallyFilteredTemplates = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (kw === '') return templates.value
  return templates.value.filter((template) => {
    const text = `${template.name} ${template.summary} ${templateMetadataSearchText(template)}`.toLowerCase()
    return text.includes(kw)
  })
})

const visibleTemplates = computed(() =>
  filterTemplatesByPreference(locallyFilteredTemplates.value, prefs.value, viewFilter.value),
)

const favoriteCount = computed(() =>
  locallyFilteredTemplates.value.filter((template) => prefs.value.favoriteIds.includes(template.id)).length,
)

const recentCount = computed(() =>
  locallyFilteredTemplates.value.filter((template) => prefs.value.recentIds.includes(template.id)).length,
)

const filterTabs = computed<Array<{ key: TemplateMarketViewFilter; label: string; count: number }>>(() => [
  { key: 'all', label: '全部', count: locallyFilteredTemplates.value.length },
  { key: 'favorites', label: '收藏', count: favoriteCount.value },
  { key: 'recent', label: '最近使用', count: recentCount.value },
])

function isFavoriteTemplate(id: string): boolean {
  return prefs.value.favoriteIds.includes(id)
}

function updatePrefs(next: TemplateMarketPrefs): void {
  prefs.value = next
  saveTemplateMarketPrefs(next)
}

function toggleFavorite(tpl: Template, event?: MouseEvent): void {
  event?.stopPropagation()
  updatePrefs(toggleTemplateFavorite(prefs.value, tpl.id))
}

/** 传输类型显示名。 */
function transportLabel(value: string): string {
  return TRANSPORT_OPTIONS.find((o) => o.value === value)?.label ?? value
}

/** 分类显示名（兜底用本地映射）。 */
function categoryLabel(cat: TemplateCategory): string {
  return CATEGORY_LABELS[cat] ?? cat
}

function chipClass(chip: TemplateMetaChip): string {
  if (chip.tone === 'brand') return 'bg-brand-50 text-brand-700 dark:bg-brand-500/10 dark:text-brand-300'
  if (chip.tone === 'success') return 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-300'
  if (chip.tone === 'warning') return 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-300'
  return 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'
}

/** 拉取模板列表（按当前分类与关键字）。 */
async function fetchTemplates(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    templates.value = await listTemplates({
      category: activeCategory.value ?? undefined,
      keyword: keyword.value.trim() || undefined,
    })
  } catch (err) {
    errorMessage.value =
      err instanceof Error ? err.message : '加载模板失败，请确认后端模板市场接口已就绪'
    templates.value = []
  } finally {
    loading.value = false
  }
}

/** 拉取分类导航。 */
async function fetchCategories(): Promise<void> {
  try {
    categories.value = await listTemplateCategories()
  } catch {
    // 分类导航失败不阻塞主流程，保留全部入口。
    categories.value = []
  }
}

/** 选择分类并刷新列表。 */
function selectCategory(cat: TemplateCategory | null): void {
  activeCategory.value = cat
  detail.value = null
  void fetchTemplates()
}

/** 防抖检索定时器。 */
let searchTimer: ReturnType<typeof setTimeout> | null = null

function clearSearchTimer(): void {
  if (searchTimer === null) return
  clearTimeout(searchTimer)
  searchTimer = null
}
/** 关键字变更时防抖检索。 */
function onKeywordInput(): void {
  clearSearchTimer()
  searchTimer = setTimeout(() => {
    searchTimer = null
    detail.value = null
    void fetchTemplates()
  }, 300)
}

/** 打开模板详情。 */
function openDetail(tpl: Template): void {
  detail.value = tpl
}

/** 必填占位参数（详情中高亮标记）。 */
const requiredPlaceholders = computed(() =>
  (detail.value?.placeholders ?? []).filter((p) => p.required),
)

/** 选择当前详情模板，请求预填充并交给父组件（Req 14.7）。 */
async function useTemplate(): Promise<void> {
  if (detail.value === null || selecting.value) return
  selecting.value = true
  errorMessage.value = ''
  try {
    let prefill: PrefillForm
    try {
      prefill = await getTemplatePrefill(detail.value.id)
    } catch {
      // 预填充接口未就绪时，用详情数据本地组装预填充表单（降级，仍满足 Req 14.7）。
      prefill = {
        templateId: detail.value.id,
        name: detail.value.name,
        transport: detail.value.transport,
        presetParams: detail.value.presetParams ?? {},
        placeholders: detail.value.placeholders ?? [],
      }
    }
    updatePrefs(markTemplateRecentlyUsed(prefs.value, detail.value.id))
    emit('select', prefill)
  } finally {
    selecting.value = false
  }
}

// 弹窗打开时初始化加载。
watch(
  () => props.open,
  (open) => {
    if (open) {
      activeCategory.value = null
      keyword.value = ''
      viewFilter.value = 'all'
      detail.value = null
      prefs.value = loadTemplateMarketPrefs()
      void fetchCategories()
      void fetchTemplates()
    } else {
      clearSearchTimer()
    }
  },
  { immediate: true },
)

onUnmounted(clearSearchTimer)
</script>

<template>
  <transition name="fade">
    <div
      v-if="open"
      class="fixed inset-0 z-[100000] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
    >
      <div
        class="flex h-[80vh] w-full max-w-5xl flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-gray-800 dark:bg-gray-900"
      >
        <!-- 头部 -->
        <div
          class="flex items-center justify-between border-b border-gray-200 px-6 py-4 dark:border-gray-800"
        >
          <div>
            <h3 class="text-lg font-semibold text-gray-800 dark:text-white/90">模板市场</h3>
            <p class="text-xs text-gray-500 dark:text-gray-400">浏览并快速接入常用第三方 MCP 服务</p>
          </div>
          <button
            type="button"
            class="rounded-lg p-2 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-800"
            aria-label="关闭"
            @click="emit('close')"
          >
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none">
              <path d="M6 6l12 12M6 18L18 6" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
            </svg>
          </button>
        </div>

        <!-- 主体：左分类导航 + 右内容 -->
        <div class="flex min-h-0 flex-1">
          <!-- 分类导航 -->
          <aside
            class="hidden w-48 shrink-0 overflow-y-auto border-r border-gray-200 p-3 md:block dark:border-gray-800"
          >
            <button
              type="button"
              class="mb-1 w-full rounded-lg px-3 py-2 text-left text-sm transition"
              :class="
                activeCategory === null
                  ? 'bg-brand-50 font-medium text-brand-600 dark:bg-brand-500/10 dark:text-brand-400'
                  : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              "
              @click="selectCategory(null)"
            >
              全部
            </button>
            <button
              v-for="cv in categories"
              :key="cv.category"
              type="button"
              class="mb-1 w-full rounded-lg px-3 py-2 text-left text-sm transition"
              :class="
                activeCategory === cv.category
                  ? 'bg-brand-50 font-medium text-brand-600 dark:bg-brand-500/10 dark:text-brand-400'
                  : 'text-gray-600 hover:bg-gray-100 dark:text-gray-300 dark:hover:bg-gray-800'
              "
              @click="selectCategory(cv.category)"
            >
              {{ cv.displayName }}
            </button>
          </aside>

          <!-- 内容区 -->
          <div class="flex min-w-0 flex-1 flex-col">
            <!-- 搜索 + 移动端分类下拉 -->
            <div class="flex flex-col gap-3 border-b border-gray-200 p-4 sm:flex-row dark:border-gray-800">
              <input
                v-model="keyword"
                type="search"
                placeholder="按名称或简介检索模板…"
                class="h-10 w-full rounded-lg border border-gray-300 bg-transparent px-4 text-sm text-gray-800 placeholder:text-gray-400 focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:text-white/90"
                @input="onKeywordInput"
              />
              <select
                :value="activeCategory ?? ''"
                class="h-10 rounded-lg border border-gray-300 bg-transparent px-3 text-sm text-gray-800 focus:border-brand-300 focus:outline-none md:hidden dark:border-gray-700 dark:text-white/90"
                @change="selectCategory(($event.target as HTMLSelectElement).value as TemplateCategory || null)"
              >
                <option value="">全部分类</option>
                <option v-for="cv in categories" :key="cv.category" :value="cv.category">{{ cv.displayName }}</option>
              </select>
            </div>
            <div
              class="flex gap-2 overflow-x-auto border-b border-gray-200 px-4 py-3 dark:border-gray-800"
            >
              <button
                v-for="tab in filterTabs"
                :key="tab.key"
                type="button"
                class="inline-flex shrink-0 items-center gap-1.5 rounded-lg border px-3 py-1.5 text-sm transition"
                :class="
                  viewFilter === tab.key
                    ? 'border-brand-300 bg-brand-50 text-brand-600 dark:border-brand-500/50 dark:bg-brand-500/10 dark:text-brand-400'
                    : 'border-gray-200 text-gray-600 hover:border-brand-300 hover:bg-brand-50/40 dark:border-gray-800 dark:text-gray-300 dark:hover:border-brand-500/40 dark:hover:bg-brand-500/[0.06]'
                "
                @click="viewFilter = tab.key"
              >
                <span>{{ tab.label }}</span>
                <span class="text-xs opacity-70">{{ tab.count }}</span>
              </button>
            </div>

            <!-- 列表 / 详情 -->
            <div class="min-h-0 flex-1 overflow-y-auto p-4">
              <p v-if="errorMessage !== ''" class="rounded-lg bg-error-50 px-4 py-2.5 text-sm text-error-600 dark:bg-error-500/10 dark:text-error-400">
                {{ errorMessage }}
              </p>
              <p v-else-if="loading" class="py-10 text-center text-sm text-gray-400">加载中…</p>

              <!-- 详情视图 -->
              <div v-else-if="detail !== null" class="space-y-4">
                <button type="button" class="text-sm text-brand-600 hover:underline dark:text-brand-400" @click="detail = null">
                  ← 返回列表
                </button>
                <div>
                  <div class="flex flex-wrap items-center gap-2">
                    <h4 class="text-base font-semibold text-gray-800 dark:text-white/90">{{ detail.name }}</h4>
                    <span class="inline-flex items-center rounded-full bg-gray-100 px-2.5 py-0.5 text-xs text-gray-600 dark:bg-gray-800 dark:text-gray-300">
                      {{ categoryLabel(detail.category) }}
                    </span>
                    <span class="inline-flex items-center rounded-full bg-brand-50 px-2.5 py-0.5 text-xs text-brand-600 dark:bg-brand-500/10 dark:text-brand-400">
                      {{ transportLabel(detail.transport) }}
                    </span>
                    <button
                      v-tooltip:bottom="isFavoriteTemplate(detail.id) ? '取消收藏' : '收藏模板'"
                      type="button"
                      class="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-gray-200 text-gray-400 transition hover:border-warning-200 hover:bg-warning-50 hover:text-warning-600 dark:border-gray-800 dark:hover:border-warning-500/30 dark:hover:bg-warning-500/10 dark:hover:text-warning-300"
                      :class="isFavoriteTemplate(detail.id) ? 'border-warning-200 bg-warning-50 text-warning-600 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-300' : ''"
                      :aria-label="isFavoriteTemplate(detail.id) ? '取消收藏模板' : '收藏模板'"
                      @click="toggleFavorite(detail)"
                    >
                      <StaredIcon class="h-4 w-4" />
                    </button>
                  </div>
                  <p class="mt-2 text-sm text-gray-600 dark:text-gray-300">{{ detail.summary }}</p>
                  <a v-if="detail.docUrl" :href="detail.docUrl" target="_blank" rel="noopener noreferrer" class="mt-1 inline-block text-sm text-brand-600 hover:underline dark:text-brand-400">
                    查看文档 ↗
                  </a>
                </div>

                <div class="flex flex-wrap gap-1.5">
                  <span
                    v-for="chip in templateMetaChips(detail)"
                    :key="chip.key"
                    class="rounded-full px-2 py-0.5 text-xs font-medium"
                    :class="chipClass(chip)"
                  >
                    {{ chip.label }}
                  </span>
                </div>

                <div v-if="(detail.placeholders ?? []).length > 0">
                  <p class="mb-2 text-sm font-medium text-gray-700 dark:text-gray-300">需填写参数</p>
                  <ul class="space-y-2">
                    <li v-for="ph in detail.placeholders" :key="ph.name" class="rounded-lg border border-gray-200 px-3 py-2 dark:border-gray-800">
                      <div class="flex items-center gap-2">
                        <span class="text-sm text-gray-800 dark:text-white/90">{{ ph.label || ph.name }}</span>
                        <span v-if="ph.required" class="rounded-full bg-error-50 px-2 py-0.5 text-xs text-error-600 dark:bg-error-500/10 dark:text-error-400">必填</span>
                        <span class="rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-gray-800 dark:text-gray-400">{{ ph.rule?.kind }}</span>
                      </div>
                      <p v-if="ph.description" class="mt-1 text-xs text-gray-400">{{ ph.description }}</p>
                    </li>
                  </ul>
                </div>
                <p v-if="requiredPlaceholders.length > 0" class="text-xs text-gray-400">
                  选择后将预填充创建表单，{{ requiredPlaceholders.length }} 个必填参数需补充后方可创建。
                </p>
              </div>

              <!-- 空列表 -->
              <p v-else-if="visibleTemplates.length === 0" class="py-10 text-center text-sm text-gray-400">
                没有匹配的模板
              </p>

              <!-- 模板卡片网格 -->
              <div v-else class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
                <article
                  v-for="tpl in visibleTemplates"
                  :key="tpl.id"
                  class="group relative rounded-2xl border border-gray-200 bg-white transition hover:border-brand-300 hover:shadow-sm dark:border-gray-800 dark:bg-white/[0.03] dark:hover:border-brand-500/40"
                >
                  <button
                    type="button"
                    class="flex h-full w-full flex-col p-4 text-left focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-400"
                    @click="openDetail(tpl)"
                  >
                    <div class="mb-2 flex items-start gap-2">
                      <h4 class="min-w-0 flex-1 truncate pr-7 text-sm font-semibold text-gray-800 dark:text-white/90">{{ tpl.name }}</h4>
                      <span class="shrink-0 rounded-full bg-gray-100 px-2 py-0.5 text-xs text-gray-500 dark:bg-gray-800 dark:text-gray-400">{{ categoryLabel(tpl.category) }}</span>
                    </div>
                    <span
                      v-if="prefs.recentIds.includes(tpl.id)"
                      class="mb-2 inline-flex w-fit rounded-full bg-brand-50 px-2 py-0.5 text-[11px] text-brand-600 dark:bg-brand-500/10 dark:text-brand-400"
                    >
                      最近使用
                    </span>
                    <p class="line-clamp-2 flex-1 text-xs text-gray-500 dark:text-gray-400">{{ tpl.summary }}</p>
                    <div class="mt-3 flex flex-wrap gap-1.5">
                      <span
                        v-for="chip in templateCardChips(tpl)"
                        :key="chip.key"
                        class="rounded-full px-2 py-0.5 text-[11px] font-medium"
                        :class="chipClass(chip)"
                      >
                        {{ chip.label }}
                      </span>
                    </div>
                    <span class="mt-3 inline-block text-xs text-brand-600 dark:text-brand-400">查看详情 →</span>
                  </button>
                  <button
                    type="button"
                    v-tooltip:bottom-end="isFavoriteTemplate(tpl.id) ? '取消收藏' : '收藏模板'"
                    class="absolute top-3 right-3 inline-flex h-8 w-8 items-center justify-center rounded-lg text-gray-300 transition hover:bg-warning-50 hover:text-warning-600 dark:hover:bg-warning-500/10 dark:hover:text-warning-300"
                    :class="isFavoriteTemplate(tpl.id) ? 'text-warning-500 dark:text-warning-300' : 'group-hover:text-gray-500 dark:group-hover:text-gray-300'"
                    :aria-label="isFavoriteTemplate(tpl.id) ? '取消收藏模板' : '收藏模板'"
                    @click="toggleFavorite(tpl, $event)"
                  >
                    <StaredIcon class="h-4 w-4" />
                  </button>
                </article>
              </div>
            </div>
          </div>
        </div>

        <!-- 底部操作（详情态下显示「使用此模板」） -->
        <div
          v-if="detail !== null"
          class="flex items-center justify-end gap-3 border-t border-gray-200 px-6 py-4 dark:border-gray-800"
        >
          <button
            type="button"
            class="rounded-lg border border-gray-300 px-4 py-2.5 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            @click="emit('close')"
          >
            取消
          </button>
          <button
            type="button"
            :disabled="selecting"
            class="rounded-lg bg-brand-500 px-4 py-2.5 text-sm font-medium text-white transition hover:bg-brand-600 disabled:cursor-not-allowed disabled:opacity-60"
            @click="useTemplate"
          >
            {{ selecting ? '处理中…' : '使用此模板' }}
          </button>
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
