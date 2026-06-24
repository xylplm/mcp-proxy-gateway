<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  listAliases,
  listFilters,
  listToolPolicies,
  type AliasRule,
  type FilterRule,
  type ToolPolicyRule,
} from '@/api/rules'
import type { Upstream } from '@/api/upstreams'
import type { ToolDef } from '@/api/tools'
import { RefreshIcon } from '@/icons'
import { loadCachedToolsForEnabledUpstreams } from '@/utils/rulePreview'
import {
  aliasEffectLabel,
  buildRuleMatchPreview,
  rulePatternLabel,
  toolPolicyEffectLabel,
  type RuleMatchPreviewSource,
} from '@/utils/ruleMatchPreview'

const props = defineProps<{
  upstreams: Upstream[]
}>()

const query = ref('')
const filters = ref<FilterRule[]>([])
const aliases = ref<AliasRule[]>([])
const toolPolicies = ref<ToolPolicyRule[]>([])
const toolsByUpstream = ref<Record<string, ToolDef[]>>({})
const loading = ref(false)
const errorMessage = ref('')

const preview = computed(() =>
  buildRuleMatchPreview({
    query: query.value,
    upstreams: props.upstreams,
    toolsByUpstream: toolsByUpstream.value,
    filters: filters.value,
    aliases: aliases.value,
    toolPolicies: toolPolicies.value,
  }),
)

const visibleSources = computed(() => preview.value.sources.slice(0, 6))
const hiddenCount = computed(() => Math.max(0, preview.value.sources.length - visibleSources.value.length))

async function reload(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const [nextFilters, nextAliases, nextPolicies, nextTools] = await Promise.all([
      listFilters(),
      listAliases(),
      listToolPolicies(),
      loadCachedToolsForEnabledUpstreams(props.upstreams),
    ])
    filters.value = nextFilters
    aliases.value = nextAliases
    toolPolicies.value = nextPolicies
    toolsByUpstream.value = nextTools
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '加载规则预览数据失败'
  } finally {
    loading.value = false
  }
}

function sourceToneClass(source: RuleMatchPreviewSource): string {
  if (source.status === 'filtered') {
    return 'border-error-200 bg-error-50 dark:border-error-500/30 dark:bg-error-500/10'
  }
  if (source.aliasRule || source.policyRule) {
    return 'border-brand-200 bg-brand-50 dark:border-brand-500/30 dark:bg-brand-500/10'
  }
  return 'border-gray-200 bg-gray-50 dark:border-gray-800 dark:bg-white/[0.03]'
}

function statusLabel(source: RuleMatchPreviewSource): string {
  if (source.status === 'filtered') return '已屏蔽'
  if (source.aliasRule || source.policyRule) return '已命中'
  return '可见'
}

onMounted(reload)

watch(
  () => props.upstreams.map((up) => `${up.id}:${up.config.enabled}`).join('|'),
  () => {
    void reload()
  },
)
</script>

<template>
  <section class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]">
    <div class="flex flex-col gap-3 lg:flex-row lg:items-start lg:justify-between">
      <div class="min-w-0">
        <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">策略命中预览</h3>
        <p class="mt-1 text-sm leading-6 text-gray-500 dark:text-gray-400">
          输入工具名，查看屏蔽、别名和工具策略的最终命中链路。
        </p>
      </div>
      <button
        v-tooltip:bottom-end="'刷新预览数据'"
        type="button"
        class="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-lg border border-gray-300 text-gray-700 transition hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
        :disabled="loading"
        aria-label="刷新预览数据"
        @click="reload"
      >
        <RefreshIcon class="h-4 w-4" :class="{ 'animate-spin': loading }" />
      </button>
    </div>

    <div class="mt-4 grid grid-cols-1 gap-3 lg:grid-cols-[minmax(0,22rem)_minmax(0,1fr)]">
      <label class="block">
        <span class="mb-1 block text-xs font-medium text-gray-500 dark:text-gray-400">工具名</span>
        <input
          v-model="query"
          type="text"
          placeholder="例如 read_file 或 search"
          class="h-10 w-full rounded-lg border border-gray-300 bg-white px-3 text-sm text-gray-800 shadow-sm transition focus:border-brand-300 focus:ring-3 focus:ring-brand-500/10 focus:outline-none dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
        />
        <span class="mt-1.5 block text-xs leading-5 text-gray-500 dark:text-gray-400">
          按当前缓存和已启用上游预览，不会发起真实调用。
        </span>
      </label>

      <div class="rounded-xl border border-gray-200 p-3 dark:border-gray-800">
        <div class="flex flex-wrap items-center justify-between gap-2">
          <p class="text-sm font-medium text-gray-800 dark:text-white/90">{{ preview.summary }}</p>
          <div v-if="query.trim() !== ''" class="flex flex-wrap gap-1.5">
            <span class="rounded-full bg-success-50 px-2 py-0.5 text-xs text-success-700 dark:bg-success-500/10 dark:text-success-300">
              可见 {{ preview.visibleCount }}
            </span>
            <span class="rounded-full bg-error-50 px-2 py-0.5 text-xs text-error-600 dark:bg-error-500/10 dark:text-error-300">
              屏蔽 {{ preview.filteredCount }}
            </span>
          </div>
        </div>

        <p v-if="errorMessage !== ''" class="mt-3 rounded-lg bg-error-50 px-3 py-2 text-xs text-error-600 dark:bg-error-500/10 dark:text-error-400">
          {{ errorMessage }}
        </p>

        <div v-if="visibleSources.length > 0" class="mt-3 grid grid-cols-1 gap-2 xl:grid-cols-2">
          <div
            v-for="source in visibleSources"
            :key="`${source.upstreamId}:${source.originalName}:${source.status}`"
            class="rounded-lg border p-3"
            :class="sourceToneClass(source)"
          >
            <div class="flex items-start justify-between gap-3">
              <div class="min-w-0">
                <p class="truncate font-mono text-xs font-semibold text-gray-800 dark:text-white/90">
                  {{ source.exposedName }}
                </p>
                <p class="mt-1 truncate text-[11px] text-gray-500 dark:text-gray-400">
                  {{ source.upstreamName }} · {{ source.originalName }}
                </p>
              </div>
              <span class="shrink-0 rounded-full bg-white/70 px-2 py-0.5 text-[11px] text-gray-600 dark:bg-gray-900/60 dark:text-gray-300">
                {{ statusLabel(source) }}
              </span>
            </div>
            <div class="mt-2 space-y-1 text-[11px] leading-5 text-gray-600 dark:text-gray-300">
              <p v-if="source.filterRule">
                屏蔽规则：{{ rulePatternLabel(source.filterRule) }}
              </p>
              <p v-if="source.aliasRule">
                别名规则：{{ rulePatternLabel(source.aliasRule) }}，{{ aliasEffectLabel(source.aliasRule) }}
              </p>
              <p v-if="source.policyRule">
                工具策略：{{ rulePatternLabel(source.policyRule) }}，{{ toolPolicyEffectLabel(source.policyRule) }}
              </p>
              <p v-if="!source.filterRule && !source.aliasRule && !source.policyRule">
                未命中规则，按原始工具信息对外展示。
              </p>
            </div>
          </div>
        </div>
        <p v-if="hiddenCount > 0" class="mt-2 text-xs text-gray-500 dark:text-gray-400">
          另有 {{ hiddenCount }} 个来源未展开展示。
        </p>
      </div>
    </div>
  </section>
</template>
