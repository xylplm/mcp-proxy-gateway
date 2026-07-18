<script setup lang="ts">
/**
 * 运行环境页：展示本地 stdio 策略与宿主工具探测结果（P0）。
 * 不负责在线装包；引导用户安装运行时或改用远程上游。
 */
import { computed, onMounted, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import { BoxCubeIcon, RefreshIcon } from '@/icons'
import { getRuntimeSummary, type RuntimeSummary, type RuntimeToolStatus } from '@/api/runtime'
import {
  formatAllowlist,
  stdioPolicyLabel,
  summarizeToolHealth,
  toolStatusLabel,
  toolStatusTone,
} from '@/utils/runtimeSummary'
import { useToast } from '@/composables/useToast'

const toast = useToast()
const loading = ref(false)
const loadError = ref('')
const summary = ref<RuntimeSummary | null>(null)

const healthLabel = computed(() =>
  summary.value === null ? '—' : summarizeToolHealth(summary.value),
)

const stdioLabel = computed(() =>
  summary.value === null ? '—' : stdioPolicyLabel(summary.value.stdioEnabled),
)

const stdioToneClass = computed(() => {
  if (summary.value === null) return 'bg-gray-100 text-gray-600 dark:bg-white/5 dark:text-gray-400'
  return summary.value.stdioEnabled
    ? 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
    : 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
})

async function load(): Promise<void> {
  if (loading.value) return
  loading.value = true
  loadError.value = ''
  try {
    summary.value = await getRuntimeSummary()
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : '加载运行环境失败'
  } finally {
    loading.value = false
  }
}

async function refresh(): Promise<void> {
  await load()
  if (loadError.value === '') toast.success('运行环境已刷新')
}

function toolChipClass(tool: RuntimeToolStatus): string {
  return toolStatusTone(tool) === 'success'
    ? 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
    : 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
}

onMounted(load)
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="运行环境" />

    <div class="mb-5 flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <h2 class="text-lg font-semibold text-gray-800 dark:text-white/90">本地运行时与 stdio 能力</h2>
        <p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400">
          探测网关进程可见的 Node / Python / uv / Docker 等工具，并展示当前 stdio 安全策略。远程上游不依赖本页结果。
        </p>
      </div>
      <button
        type="button"
        class="inline-flex h-10 items-center gap-1.5 rounded-lg border border-gray-300 px-3.5 text-sm font-medium text-gray-700 transition hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
        :disabled="loading"
        @click="refresh"
      >
        <RefreshIcon class="h-4 w-4" :class="loading ? 'animate-spin' : ''" aria-hidden="true" />
        刷新探测
      </button>
    </div>

    <p
      v-if="loadError !== ''"
      class="bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400 mb-4 rounded-xl px-4 py-3 text-sm"
    >
      {{ loadError }}
    </p>

    <div
      v-if="loading && summary === null"
      class="rounded-2xl border border-gray-200 bg-white px-5 py-12 text-center text-sm text-gray-400 dark:border-gray-800 dark:bg-white/[0.03]"
    >
      正在探测运行环境…
    </div>

    <template v-else-if="summary !== null">
      <div class="mb-5 grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <div class="mb-3 flex h-10 w-10 items-center justify-center rounded-xl bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-300">
            <BoxCubeIcon class="h-5 w-5" aria-hidden="true" />
          </div>
          <p class="text-xs text-gray-400 dark:text-gray-500">stdio 策略</p>
          <p class="mt-1 text-sm font-semibold text-gray-800 dark:text-white/90">{{ stdioLabel }}</p>
          <span class="mt-3 inline-flex rounded-full px-2.5 py-1 text-xs font-medium" :class="stdioToneClass">
            {{ summary.stdioEnabled ? '可创建本地上游' : '仅远程 / OpenAPI' }}
          </span>
        </section>

        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <p class="text-xs text-gray-400 dark:text-gray-500">工具探测</p>
          <p class="mt-2 text-sm font-semibold text-gray-800 dark:text-white/90">{{ healthLabel }}</p>
          <p class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">
            仅检查 PATH 中是否存在可执行文件，不启动版本探测子进程。
          </p>
        </section>

        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-800 dark:bg-white/[0.03] sm:col-span-2"
        >
          <p class="text-xs text-gray-400 dark:text-gray-500">命令白名单</p>
          <p class="mt-2 break-all text-sm font-medium text-gray-800 dark:text-white/90">
            {{ formatAllowlist(summary.commandAllowlist) }}
          </p>
          <p class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">
            可在系统设置中调整；shell 类命令始终禁止。
          </p>
        </section>
      </div>

      <section
        class="mb-5 rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-800 dark:bg-white/[0.03]"
      >
        <div class="mb-4 flex flex-wrap items-center justify-between gap-2">
          <div>
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">宿主工具</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              与模板市场常见 stdio 命令对齐。Docker 镜像默认极简，缺失属预期。
            </p>
          </div>
          <span v-if="summary.dataDir" class="text-xs text-gray-400 dark:text-gray-500">
            数据目录 {{ summary.dataDir }}
          </span>
        </div>

        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <article
            v-for="tool in summary.tools"
            :key="tool.name"
            class="rounded-xl border border-gray-100 bg-gray-50/80 p-4 transition hover:border-brand-200 hover:bg-white dark:border-gray-800 dark:bg-white/[0.03] dark:hover:border-brand-500/30"
          >
            <div class="flex items-start justify-between gap-2">
              <h4 class="font-mono text-sm font-semibold text-gray-800 dark:text-white/90">
                {{ tool.name }}
              </h4>
              <span class="inline-flex shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium" :class="toolChipClass(tool)">
                {{ toolStatusLabel(tool) }}
              </span>
            </div>
            <p class="mt-2 break-all text-xs leading-5 text-gray-500 dark:text-gray-400">
              {{ tool.available ? tool.path || '已在 PATH 中找到' : 'PATH 中未找到，stdio 使用该命令会失败' }}
            </p>
          </article>
        </div>
      </section>

      <section
        class="mb-5 rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-800 dark:bg-white/[0.03]"
      >
        <h3 class="mb-3 text-base font-semibold text-gray-800 dark:text-white/90">安全说明</h3>
        <ul class="space-y-2 text-sm leading-6 text-gray-600 dark:text-gray-300">
          <li v-for="(note, idx) in summary.riskNotes" :key="idx" class="flex gap-2">
            <span class="mt-2 h-1.5 w-1.5 shrink-0 rounded-full bg-brand-500"></span>
            <span>{{ note }}</span>
          </li>
        </ul>
        <div class="mt-5 flex flex-wrap gap-2">
          <router-link
            to="/settings"
            class="rounded-lg border border-gray-300 px-3.5 py-2 text-sm font-medium text-gray-700 transition hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
          >
            调整 stdio 策略
          </router-link>
          <router-link
            to="/upstreams"
            class="rounded-lg bg-brand-500 px-3.5 py-2 text-sm font-medium text-white transition hover:bg-brand-600"
          >
            管理上游 MCP
          </router-link>
        </div>
      </section>
    </template>
  </AdminLayout>
</template>
