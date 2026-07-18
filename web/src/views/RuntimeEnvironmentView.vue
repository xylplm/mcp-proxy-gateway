<script setup lang="ts">
/**
 * 运行环境页：策略摘要、卷路径、工具探测、受控预置安装与进程加固说明。
 */
import { computed, onMounted, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import { BoxCubeIcon, RefreshIcon } from '@/icons'
import {
  getRuntimeSummary,
  installRuntimePackage,
  uninstallRuntimePackage,
  type RuntimeCatalogPackage,
  type RuntimeSummary,
  type RuntimeToolStatus,
} from '@/api/runtime'
import {
  formatAllowlist,
  packageStatusLabel,
  packageStatusTone,
  runtimeBinDir,
  runtimeGuideSteps,
  sandboxHardeningLabel,
  shouldShowRuntimeGuide,
  stdioPolicyLabel,
  summarizeToolHealth,
  toolStatusLabel,
  toolStatusTone,
} from '@/utils/runtimeSummary'
import { useToast } from '@/composables/useToast'
import { useConfirm } from '@/composables/useConfirm'

const toast = useToast()
const { confirm } = useConfirm()
const loading = ref(false)
const loadError = ref('')
const summary = ref<RuntimeSummary | null>(null)
const busyPackageId = ref('')

const healthLabel = computed(() =>
  summary.value === null ? '—' : summarizeToolHealth(summary.value),
)

const stdioLabel = computed(() =>
  summary.value === null ? '—' : stdioPolicyLabel(summary.value.stdioEnabled),
)

const hardeningLabel = computed(() =>
  summary.value === null ? '—' : sandboxHardeningLabel(summary.value),
)

const defaultSecurityModeLabel = computed(() => {
  const m = String(summary.value?.defaultStdioSecurityMode ?? 'standard').toLowerCase()
  if (m === 'strict') return '严格安全'
  if (m === 'unrestricted') return '完全放行'
  return '标准'
})

const stdioToneClass = computed(() => {
  if (summary.value === null) return 'bg-gray-100 text-gray-600 dark:bg-white/5 dark:text-gray-400'
  return summary.value.stdioEnabled
    ? 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
    : 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
})

const showGuide = computed(() => summary.value !== null && shouldShowRuntimeGuide(summary.value))

const guideSteps = computed(() => (summary.value === null ? [] : runtimeGuideSteps(summary.value)))

const binDir = computed(() => (summary.value === null ? '' : runtimeBinDir(summary.value)))

const catalog = computed(() => summary.value?.catalog ?? [])

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

function packageChipClass(pkg: RuntimeCatalogPackage): string {
  const tone = packageStatusTone(pkg)
  if (tone === 'success') {
    return 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
  }
  if (tone === 'muted') {
    return 'bg-gray-100 text-gray-500 dark:bg-white/5 dark:text-gray-400'
  }
  return 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
}

async function onInstall(pkg: RuntimeCatalogPackage): Promise<void> {
  if (busyPackageId.value !== '' || !pkg.supported) return
  const ok = await confirm({
    title: `安装 ${pkg.name} ${pkg.version}`,
    message: `将从官方源下载固定版本并安装到数据卷运行时目录（SHA256 校验）。仅允许内置目录中的包，不会执行任意脚本。是否继续？`,
    confirmText: '开始安装',
    tone: 'warning',
  })
  if (!ok) return
  busyPackageId.value = pkg.id
  try {
    const result = await installRuntimePackage(pkg.id)
    toast.success(
      result.reused
        ? `${result.name} 已存在，已跳过下载`
        : `${result.name} ${result.version} 安装完成`,
    )
    await load()
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '安装失败')
  } finally {
    busyPackageId.value = ''
  }
}

async function onUninstall(pkg: RuntimeCatalogPackage): Promise<void> {
  if (busyPackageId.value !== '') return
  const ok = await confirm({
    title: `卸载 ${pkg.name}`,
    message: `将移除数据卷中由预置安装写入的 ${pkg.name} 文件。手动放入的其他工具不受影响。是否继续？`,
    confirmText: '卸载',
    tone: 'danger',
  })
  if (!ok) return
  busyPackageId.value = pkg.id
  try {
    await uninstallRuntimePackage(pkg.id)
    toast.success(`${pkg.name} 已卸载`)
    await load()
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '卸载失败')
  } finally {
    busyPackageId.value = ''
  }
}

onMounted(load)
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="运行环境" />

    <div class="mb-5 flex flex-wrap items-start justify-between gap-3">
      <div class="min-w-0">
        <h2 class="text-lg font-semibold text-gray-800 dark:text-white/90">
          本地运行时与 stdio 能力
        </h2>
        <p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400">
          探测网关可见的 Node / Python / uv / Docker，管理数据卷预置运行时，并展示 stdio
          安全策略。远程上游不依赖本页结果。
        </p>
      </div>
      <button
        type="button"
        class="inline-flex h-10 items-center gap-1.5 rounded-lg border border-gray-300 px-3.5 text-sm font-medium text-gray-700 transition hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
        :disabled="loading || busyPackageId !== ''"
        aria-label="刷新运行环境探测"
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
          class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm transition duration-300 hover:shadow-md dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <div
            class="bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-300 mb-3 flex h-10 w-10 items-center justify-center rounded-xl"
          >
            <BoxCubeIcon class="h-5 w-5" aria-hidden="true" />
          </div>
          <p class="text-xs text-gray-400 dark:text-gray-500">stdio 策略</p>
          <p class="mt-1 text-sm font-semibold text-gray-800 dark:text-white/90">
            {{ stdioLabel }}
          </p>
          <span
            class="mt-3 inline-flex rounded-full px-2.5 py-1 text-xs font-medium"
            :class="stdioToneClass"
          >
            {{ summary.stdioEnabled ? '可创建本地上游' : '仅远程 / OpenAPI' }}
          </span>
        </section>

        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm transition duration-300 hover:shadow-md dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <p class="text-xs text-gray-400 dark:text-gray-500">工具探测</p>
          <p class="mt-2 text-sm font-semibold text-gray-800 dark:text-white/90">
            {{ healthLabel }}
          </p>
          <p class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">
            优先查找数据卷运行时目录，再检查系统 PATH。
          </p>
        </section>

        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm transition duration-300 hover:shadow-md dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <p class="text-xs text-gray-400 dark:text-gray-500">运行时目录</p>
          <p
            class="mt-2 font-mono text-sm font-semibold break-all text-gray-800 dark:text-white/90"
          >
            {{ summary.runtimeDir || '—' }}
          </p>
          <p class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">
            <span v-if="summary.layoutReady">目录已就绪</span>
            <span v-else>尚未创建或不可读</span>
            <span v-if="summary.dataDir"> · 数据目录 {{ summary.dataDir }}</span>
          </p>
        </section>

        <section
          class="rounded-2xl border border-gray-200 bg-white p-5 shadow-sm transition duration-300 hover:shadow-md dark:border-gray-800 dark:bg-white/[0.03]"
        >
          <p class="text-xs text-gray-400 dark:text-gray-500">默认安全档位</p>
          <p class="mt-2 text-sm font-semibold text-gray-800 dark:text-white/90">
            {{ defaultSecurityModeLabel }}
          </p>
          <p class="mt-2 text-xs leading-5 text-gray-500 dark:text-gray-400">
            进程加固：{{ hardeningLabel }}。
            {{ summary.sandbox?.description || '策略层命令白名单与环境清理始终生效。' }}
          </p>
        </section>
      </div>

      <section
        class="mb-5 rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-800 dark:bg-white/[0.03]"
      >
        <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">本地安全能力</h3>
        <p class="mt-1 text-sm leading-6 text-gray-500 dark:text-gray-400">
          三档模式（标准 / 严格 /
          完全放行）可在上游表单单独设置。当前为策略约束；内核级文件/网络隔离取决于宿主能力。
        </p>
        <div class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-3">
          <div
            class="rounded-xl border border-gray-100 bg-gray-50 px-3 py-3 text-xs dark:border-white/5 dark:bg-white/[0.03]"
          >
            <p class="font-medium text-gray-700 dark:text-gray-200">进程加固</p>
            <p class="mt-1 text-gray-500 dark:text-gray-400">
              {{ summary.sandbox?.processHardeningSupported ? '平台支持' : '本平台策略层' }}
            </p>
          </div>
          <div
            class="rounded-xl border border-gray-100 bg-gray-50 px-3 py-3 text-xs dark:border-white/5 dark:bg-white/[0.03]"
          >
            <p class="font-medium text-gray-700 dark:text-gray-200">文件系统隔离</p>
            <p class="mt-1 text-gray-500 dark:text-gray-400">
              {{
                summary.sandbox?.filesystemIsolationSupported
                  ? `可升级（${summary.sandbox?.isolationBackend || 'backend'}）`
                  : '策略 allowlist（非内核）'
              }}
            </p>
          </div>
          <div
            class="rounded-xl border border-gray-100 bg-gray-50 px-3 py-3 text-xs dark:border-white/5 dark:bg-white/[0.03]"
          >
            <p class="font-medium text-gray-700 dark:text-gray-200">网络隔离</p>
            <p class="mt-1 text-gray-500 dark:text-gray-400">
              {{
                summary.sandbox?.networkIsolationSupported
                  ? `可升级（${summary.sandbox?.isolationBackend || 'backend'}）`
                  : '策略声明（非内核）'
              }}
            </p>
          </div>
        </div>
      </section>

      <section
        v-if="showGuide"
        class="border-warning-200 bg-warning-50/60 dark:border-warning-500/20 dark:bg-warning-500/5 mb-5 rounded-2xl border p-5 shadow-sm"
      >
        <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">如何补齐缺失工具</h3>
        <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-gray-300">
          默认镜像不含 Node / Python / uv。推荐使用下方预置安装，或将工具放入数据卷。
          <span v-if="binDir" class="font-mono text-xs">优先路径：{{ binDir }}</span>
        </p>
        <ol
          class="mt-3 list-decimal space-y-1.5 pl-5 text-sm leading-6 text-gray-700 dark:text-gray-200"
        >
          <li v-for="(step, idx) in guideSteps" :key="idx">{{ step }}</li>
        </ol>
      </section>

      <section
        class="mb-5 rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-800 dark:bg-white/[0.03]"
      >
        <div class="mb-4 flex flex-wrap items-start justify-between gap-2">
          <div>
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">预置运行时安装</h3>
            <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">
              仅可安装网关内置的固定版本（官方源 + SHA256）。不会开放任意 npm/pip 包名或自定义
              URL，安装结果落在数据卷，容器更新可保留。
            </p>
          </div>
        </div>

        <div
          v-if="catalog.length === 0"
          class="rounded-xl border border-dashed border-gray-200 px-4 py-8 text-center text-sm text-gray-400 dark:border-gray-700"
        >
          暂无可用预置包
        </div>

        <div v-else class="grid grid-cols-1 gap-3 lg:grid-cols-2">
          <article
            v-for="pkg in catalog"
            :key="pkg.id"
            class="group hover:border-brand-200 dark:hover:border-brand-500/30 rounded-2xl border border-gray-100 bg-gradient-to-br from-gray-50/90 to-white p-4 transition duration-300 hover:shadow-md dark:border-gray-800 dark:from-white/[0.04] dark:to-white/[0.02]"
          >
            <div class="flex flex-wrap items-start justify-between gap-2">
              <div class="min-w-0">
                <h4 class="text-sm font-semibold text-gray-800 dark:text-white/90">
                  {{ pkg.name }}
                  <span class="ml-1 font-mono text-xs font-normal text-gray-400">{{
                    pkg.version
                  }}</span>
                </h4>
                <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  {{ pkg.description }}
                </p>
              </div>
              <span
                class="inline-flex shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium"
                :class="packageChipClass(pkg)"
              >
                {{ packageStatusLabel(pkg) }}
              </span>
            </div>
            <p class="mt-2 font-mono text-[11px] text-gray-400">
              工具 {{ (pkg.tools || []).join(' · ') || '—' }}
              <span v-if="pkg.assetGoos"> · {{ pkg.assetGoos }}/{{ pkg.assetGoarch }}</span>
            </p>
            <div class="mt-4 flex flex-wrap gap-2">
              <button
                v-if="!pkg.installed"
                type="button"
                class="bg-brand-500 hover:bg-brand-600 inline-flex h-9 items-center rounded-lg px-3.5 text-sm font-medium text-white transition disabled:cursor-not-allowed disabled:opacity-50"
                :disabled="!pkg.supported || busyPackageId !== ''"
                :aria-label="`安装 ${pkg.name}`"
                @click="onInstall(pkg)"
              >
                <span
                  v-if="busyPackageId === pkg.id"
                  class="mr-1.5 inline-block h-3.5 w-3.5 animate-spin rounded-full border-2 border-white/40 border-t-white"
                />
                {{ busyPackageId === pkg.id ? '安装中…' : '安装' }}
              </button>
              <button
                v-else
                type="button"
                class="inline-flex h-9 items-center rounded-lg border border-gray-300 px-3.5 text-sm font-medium text-gray-700 transition hover:bg-gray-50 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                :disabled="busyPackageId !== ''"
                :aria-label="`卸载 ${pkg.name}`"
                @click="onUninstall(pkg)"
              >
                {{ busyPackageId === pkg.id ? '处理中…' : '卸载' }}
              </button>
            </div>
          </article>
        </div>
      </section>

      <section
        class="mb-5 rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-800 dark:bg-white/[0.03]"
      >
        <div class="mb-4 flex flex-wrap items-center justify-between gap-2">
          <div>
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">宿主工具</h3>
            <p class="mt-1 text-sm text-gray-500 dark:text-gray-400">
              与模板市场常见 stdio 命令对齐。白名单：{{ formatAllowlist(summary.commandAllowlist) }}
            </p>
          </div>
          <div class="text-right text-xs text-gray-400 dark:text-gray-500">
            <p v-if="summary.pathPrefixes?.length">
              PATH 前缀 {{ summary.pathPrefixes.length }} 项
            </p>
            <p v-else-if="summary.runtimeDir">尚未发现 bin 子目录</p>
          </div>
        </div>

        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <article
            v-for="tool in summary.tools"
            :key="tool.name"
            class="hover:border-brand-200 dark:hover:border-brand-500/30 rounded-xl border border-gray-100 bg-gray-50/80 p-4 transition duration-300 hover:bg-white dark:border-gray-800 dark:bg-white/[0.03]"
          >
            <div class="flex items-start justify-between gap-2">
              <h4 class="font-mono text-sm font-semibold text-gray-800 dark:text-white/90">
                {{ tool.name }}
              </h4>
              <span
                class="inline-flex shrink-0 rounded-full px-2 py-0.5 text-[11px] font-medium"
                :class="toolChipClass(tool)"
              >
                {{ toolStatusLabel(tool) }}
              </span>
            </div>
            <p class="mt-2 text-xs leading-5 break-all text-gray-500 dark:text-gray-400">
              {{
                tool.available
                  ? tool.path || '已在 PATH 中找到'
                  : '未在运行时目录或 PATH 中找到，stdio 使用该命令会失败'
              }}
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
            <span class="bg-brand-500 mt-2 h-1.5 w-1.5 shrink-0 rounded-full"></span>
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
            class="bg-brand-500 hover:bg-brand-600 rounded-lg px-3.5 py-2 text-sm font-medium text-white transition"
          >
            管理上游 MCP
          </router-link>
        </div>
      </section>
    </template>
  </AdminLayout>
</template>
