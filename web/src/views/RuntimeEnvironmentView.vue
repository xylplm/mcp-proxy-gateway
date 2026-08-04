<script setup lang="ts">
/**
 * 运行环境页：策略摘要、卷路径、工具探测、受控预置安装与进程加固说明。
 */
import { computed, onMounted, onUnmounted, ref } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import { BoxCubeIcon, PlusIcon, RefreshIcon, TrashIcon } from '@/icons'
import {
  getRuntimeSummary,
  installRuntimePackage,
  installRuntimeDep,
  listRuntimeDeps,
  uninstallRuntimePackage,
  uninstallRuntimeDep,
  type RuntimeCatalogPackage,
  type RuntimeDepKind,
  type RuntimeDependency,
  type RuntimeDepLogEntry,
  type RuntimeInstallLogEntry,
  type RuntimeListDepsResult,
  type RuntimeSummary,
  type RuntimeToolStatus,
} from '@/api/runtime'
import {
  formatAllowlist,
  findRuntimePackageForTool,
  packageStatusLabel,
  packageStatusTone,
  runtimeBinDir,
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
let installProgressTimer: ReturnType<typeof setInterval> | null = null

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

const binDir = computed(() => (summary.value === null ? '' : runtimeBinDir(summary.value)))

const catalog = computed(() => summary.value?.catalog ?? [])
function packageForTool(tool: RuntimeToolStatus): RuntimeCatalogPackage | undefined {
  return findRuntimePackageForTool(tool.name, catalog.value)
}
const environmentConclusion = computed(() => {
  const current = summary.value
  if (!current) return null
  if (!current.stdioEnabled) {
    return { tone: 'muted', message: '本地 stdio 已禁用，仅可使用远程与 OpenAPI 上游。', action: '去启用' }
  }
  if ((current.missingCount ?? 0) > 0) {
    return {
      tone: 'warning',
      message: '缺少 ' + String(current.missingCount) + ' 个常用工具，部分 stdio 模板暂不可用。',
      action: '查看补齐方案',
    }
  }
  return { tone: 'success', message: '本地运行环境已就绪，可以创建 stdio 上游。', action: '管理上游' }
})

const conclusionClass = computed(() => {
  const tone = environmentConclusion.value?.tone
  if (tone === 'success') return 'border-success-200 bg-success-50 text-success-800 dark:border-success-500/30 dark:bg-success-500/10 dark:text-success-300'
  if (tone === 'warning') return 'border-warning-200 bg-warning-50 text-warning-800 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-300'
  return 'border-gray-200 bg-gray-50 text-gray-700 dark:border-gray-700 dark:bg-white/[0.04] dark:text-gray-300'
})

const hasKernelIsolation = computed(
  () => summary.value?.sandbox?.filesystemIsolationSupported || summary.value?.sandbox?.networkIsolationSupported,
)
const activeInstallPackageId = computed(
  () => busyPackageId.value || summary.value?.installProgress?.packageId || '',
)

// --- 依赖管理（npm/pip 包安装/升级/卸载）状态 ---
const depKind = ref<RuntimeDepKind>('npm')
const depList = ref<Record<RuntimeDepKind, RuntimeListDepsResult | null>>({
  npm: null,
  pip: null,
})
const depListLoading = ref(false)
const depListError = ref('')
const depInput = ref('')
const depInstalling = ref(false)
const depUninstallingName = ref('')
const depInputError = ref('')

const depProgress = computed(() => summary.value?.deps?.depProgress ?? null)
const depError = computed(() => summary.value?.deps?.depError ?? '')
const depLogs = computed<RuntimeDepLogEntry[]>(() => summary.value?.deps?.depLogs ?? [])
const showDepPanel = computed(
  () =>
    !!depProgress.value ||
    (depLogs.value != null && depLogs.value.length > 0),
)

/** 当前 kind 的依赖操作是否进行中（安装/卸载/列表加载）。 */
const depBusy = computed(
  () => depInstalling.value || depUninstallingName.value !== '' || depListLoading.value,
)

const currentDepList = computed(() => depList.value[depKind.value])

/** 校验依赖输入合法性（即时反馈，不阻塞提交）。 */
const validDepInput = computed(() => {
  const spec = depInput.value.trim()
  if (spec === '') return true
  // 允许逗号/空格分隔多个包。
  const parts = spec.split(/[,\s]+/).filter((p) => p !== '')
  return parts.every((p) => isDepSpecValid(p))
})

function isDepSpecValid(spec: string): boolean {
  if (spec === '' || spec.length > 256) return false
  // 取裸包名（去掉 @version / ==version）。
  const { name } = parseDepSpec(spec)
  if (name === '' || name.length > 214) return false
  if (name.includes('..')) return false
  // 反斜杠 / 控制字符 拒绝。
  if (name.includes('\\')) return false
  for (const ch of name) {
    const c = ch.codePointAt(0)
    if (c === undefined || c < 0x20 || c === 0x7f) return false
  }
  // @scope/name 允许一个斜杠；否则不含斜杠/空格。
  const slashCount = (name.match(/\//g) || []).length
  if (name.startsWith('@')) {
    if (slashCount !== 1) return false
    const segs = name.split('/')
    return segs[0].length >= 2 && segs[1] !== '' && !segs[1].includes('/')
  }
  return slashCount === 0 && !name.includes(' ')
}

/** 解析 spec 为裸包名与版本（与后端 parseDepSpecName 对齐）。 */
function parseDepSpec(spec: string): { name: string; version: string } {
  const s = spec.trim()
  const eqIdx = s.indexOf('==')
  if (eqIdx > 0) return { name: s.slice(0, eqIdx), version: s.slice(eqIdx + 2) }
  // name@version（注意 @scope/name 的 @ 在开头，用 LastIndex）。
  const at = s.lastIndexOf('@')
  if (at > 0) return { name: s.slice(0, at), version: s.slice(at + 1) }
  return { name: s, version: '' }
}

async function loadDepList(kind: RuntimeDepKind): Promise<void> {
  if (depListLoading.value) return
  depListLoading.value = true
  depListError.value = ''
  try {
    depList.value[kind] = await listRuntimeDeps(kind)
  } catch (err) {
    depListError.value = err instanceof Error ? err.message : '加载依赖列表失败'
  } finally {
    depListLoading.value = false
  }
}

async function switchDepKind(kind: RuntimeDepKind): Promise<void> {
  if (depKind.value === kind) return
  depKind.value = kind
  // 切换生态时清空输入：npm 用 name@version、pip 用 name==version，语法不同，避免误用。
  depInput.value = ''
  depInputError.value = ''
  if (depList.value[kind] === null) {
    await loadDepList(kind)
  }
}

/** 依赖操作轮询：安装期间 1.5s 拉取 summary 更新 depProgress/depLogs。 */
function startDepProgressPolling(): void {
  if (installProgressTimer !== null) return // 复用同一轮询（预置安装/依赖安装不会同时进行）
  installProgressTimer = setInterval(async () => {
    try {
      const next = await getRuntimeSummary()
      summary.value = next
      if (!next.installProgress && !next.deps?.depProgress) {
        stopInstallProgressPolling()
      }
    } catch {
      // 保留最近状态，下一轮继续尝试。
    }
  }, 1500)
}

async function onInstallDep(): Promise<void> {
  const raw = depInput.value.trim()
  if (raw === '' || depBusy.value) return
  if (!validDepInput.value) {
    depInputError.value = '包名格式不正确'
    return
  }
  depInputError.value = ''
  // 支持逗号/空格分隔多个包，逐个安装。
  const specs = raw.split(/[,\s]+/).filter((p) => p !== '')
  if (specs.length === 0) return
  const kind = depKind.value
  // 多个包时二次确认。
  const message =
    specs.length === 1
      ? `将向 ${kind === 'npm' ? 'Node 全局区' : 'Python venv'} 安装 ${specs[0]}（走已配置的镜像源）。是否继续？`
      : `将依次安装 ${specs.length} 个包到 ${kind === 'npm' ? 'Node 全局区' : 'Python venv'}：${specs.join('、')}。是否继续？`
  const ok = await confirm({
    title: `安装 ${kind} 依赖`,
    message,
    confirmText: '安装',
    tone: 'warning',
  })
  if (!ok) return
  depInstalling.value = true
  startDepProgressPolling()
  try {
    for (const spec of specs) {
      await installRuntimeDep(kind, spec)
    }
    toast.success(`已安装 ${specs.length} 个包`)
    depInput.value = ''
    await loadDepList(kind)
  } catch (err) {
    const msg = err instanceof Error ? err.message : '安装依赖失败'
    toast.error(msg)
  } finally {
    depInstalling.value = false
    if (!summary.value?.installProgress && !summary.value?.deps?.depProgress) {
      stopInstallProgressPolling()
    }
  }
}

async function onUpgradeDep(dep: RuntimeDependency): Promise<void> {
  if (depBusy.value) return
  const kind = depKind.value
  // npm 用 name@latest，pip 直接 install name（默认最新）。
  const spec = kind === 'npm' ? `${dep.name}@latest` : dep.name
  const ok = await confirm({
    title: `升级 ${dep.name}`,
    message: `将把 ${dep.name} 升级到最新版本（${kind === 'npm' ? 'Node 全局区' : 'Python venv'}）。是否继续？`,
    confirmText: '升级',
    tone: 'warning',
  })
  if (!ok) return
  depInstalling.value = true
  startDepProgressPolling()
  try {
    await installRuntimeDep(kind, spec)
    toast.success(`${dep.name} 已升级`)
    await loadDepList(kind)
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '升级失败')
  } finally {
    depInstalling.value = false
    if (!summary.value?.installProgress && !summary.value?.deps?.depProgress) {
      stopInstallProgressPolling()
    }
  }
}

async function onUninstallDep(dep: RuntimeDependency): Promise<void> {
  if (depBusy.value) return
  const kind = depKind.value
  const ok = await confirm({
    title: `卸载 ${dep.name}`,
    message: `将从 ${kind === 'npm' ? 'Node 全局区' : 'Python venv'} 移除 ${dep.name}。依赖它的 stdio 上游可能无法启动。是否继续？`,
    confirmText: '卸载',
    tone: 'danger',
  })
  if (!ok) return
  depUninstallingName.value = dep.name
  startDepProgressPolling()
  try {
    await uninstallRuntimeDep(kind, dep.name)
    toast.success(`${dep.name} 已卸载`)
    await loadDepList(kind)
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '卸载失败')
  } finally {
    depUninstallingName.value = ''
    if (!summary.value?.installProgress && !summary.value?.deps?.depProgress) {
      stopInstallProgressPolling()
    }
  }
}

/** 依赖日志时间线辅助：级别点颜色 / 时间。 */
function depLogDotClass(entry: RuntimeDepLogEntry): string {
  if (entry.level === 'success') return 'bg-success-500'
  if (entry.level === 'error') return 'bg-error-500'
  return 'bg-brand-500'
}

function depLogTimeShort(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number): string => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

const installProgressLabel = computed(() => {
  const phase = summary.value?.installProgress?.phase
  if (phase === 'downloading') return '正在下载'
  if (phase === 'verifying') return '正在校验'
  if (phase === 'extracting') return '正在解压'
  if (phase === 'placing') return '正在安装'
  if (phase === 'preparing') return '正在准备'
  return '安装中'
})

const installProgressPercent = computed(() => {
  const progress = summary.value?.installProgress
  if (!progress || progress.total <= 0) return 0
  return Math.min(100, Math.round((progress.bytes / progress.total) * 100))
})

/** 安装中或上次失败时，是否展示安装日志/错误面板。 */
const showInstallPanel = computed(
  () =>
    !!summary.value?.installProgress ||
    (summary.value?.installLogs != null && summary.value.installLogs.length > 0),
)

const installError = computed(() => summary.value?.installError ?? '')

const installLogs = computed<RuntimeInstallLogEntry[]>(
  () => summary.value?.installLogs ?? [],
)

function stopInstallProgressPolling(): void {
  if (installProgressTimer !== null) {
    clearInterval(installProgressTimer)
    installProgressTimer = null
  }
}

function startInstallProgressPolling(): void {
  if (installProgressTimer !== null) return
  installProgressTimer = setInterval(async () => {
    try {
      const next = await getRuntimeSummary()
      summary.value = next
      // 预置安装与依赖安装共享同一轮询；两者都结束时才停止（避免一方仍在进行时误停）。
      if (!next.installProgress && !next.deps?.depProgress) stopInstallProgressPolling()
    } catch {
      // 保留最近一次进度，下一轮继续尝试；安装请求本身负责报告最终错误。
    }
  }, 1500)
}

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

/** 安装日志时间线条目的左侧圆点颜色。 */
function logEntryDotClass(entry: RuntimeInstallLogEntry): string {
  if (entry.level === 'success') return 'bg-success-500'
  if (entry.level === 'error') return 'bg-error-500'
  return 'bg-brand-500'
}

/** 安装日志阶段标签。 */
function logPhaseLabel(phase: string): string {
  switch (phase) {
    case 'preparing':
      return '准备'
    case 'downloading':
      return '下载'
    case 'verifying':
      return '校验'
    case 'extracting':
      return '解压'
    case 'placing':
      return '安装'
    default:
      return phase
  }
}

/** 简化的日志时间（HH:MM:SS），避免时间线过宽。 */
function logTimeShort(iso: string): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number): string => String(n).padStart(2, '0')
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

async function onInstallForTool(tool: RuntimeToolStatus): Promise<void> {
  const pkg = packageForTool(tool)
  if (pkg) await onInstall(pkg)
}

async function onInstall(pkg: RuntimeCatalogPackage): Promise<void> {
  if (activeInstallPackageId.value !== '' || !pkg.supported) return
  const ok = await confirm({
    title: `安装 ${pkg.name} ${pkg.version}`,
    message: `将从官方源下载固定版本并安装到数据卷运行时目录（SHA256 校验）。仅允许内置目录中的包，不会执行任意脚本。是否继续？`,
    confirmText: '开始安装',
    tone: 'warning',
  })
  if (!ok) return
  busyPackageId.value = pkg.id
  startInstallProgressPolling()
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
    if (!summary.value?.installProgress && !summary.value?.deps?.depProgress) stopInstallProgressPolling()
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

onMounted(async () => {
  await load()
  if (summary.value?.installProgress) startInstallProgressPolling()
  // 默认加载 npm 依赖列表（pip 段切换时再懒加载）。
  void loadDepList('npm')
})
onUnmounted(stopInstallProgressPolling)
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
      <div class="mb-5 flex flex-wrap items-center justify-between gap-3 rounded-xl border px-4 py-3 text-sm" :class="conclusionClass">
        <span>{{ environmentConclusion?.message }}</span>
        <router-link v-if="environmentConclusion?.tone === 'muted'" to="/settings" class="shrink-0 font-medium underline underline-offset-2">{{ environmentConclusion?.action }}</router-link>
        <a v-else-if="environmentConclusion?.tone === 'warning'" href="#runtime-install" class="shrink-0 font-medium underline underline-offset-2">{{ environmentConclusion?.action }}</a>
        <router-link v-else to="/upstreams" class="shrink-0 font-medium underline underline-offset-2">{{ environmentConclusion?.action }}</router-link>
      </div>
      <div class="mb-5 grid grid-cols-1 gap-4 sm:grid-cols-2 lg:grid-cols-4">
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
          三档模式（标准 / 严格 / 完全放行）可在上游表单单独设置。Linux 安装 bubblewrap
          后，严格档将自动启用文件 bind 隔离；网络 deny 会 unshare 网络命名空间断网。
        </p>
        <div v-if="hasKernelIsolation" class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-3">
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
                  ? `严格档已启用（${summary.sandbox?.isolationBackend || 'backend'}）`
                  : '策略 allowlist（本宿主无 bwrap）'
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
                  ? summary.sandbox?.hostAllowlistEnforced
                    ? `严格档可强制（${summary.sandbox?.isolationBackend || 'backend'}）`
                    : `deny 真断网（${summary.sandbox?.isolationBackend || 'backend'}）；主机名单仍为策略`
                  : '策略声明（本宿主无 bwrap）'
              }}
            </p>
          </div>
        </div>
        <div v-else class="mt-4 rounded-xl border border-gray-100 bg-gray-50 px-3 py-3 text-sm text-gray-600 dark:border-white/5 dark:bg-white/[0.03] dark:text-gray-300">
          当前主机未提供 bubblewrap，文件和网络限制以策略校验为主。
          <details class="mt-2 text-xs">
            <summary class="cursor-pointer font-medium text-brand-600 dark:text-brand-300">如何启用内核隔离</summary>
            <p class="mt-2 leading-5">在 Linux 容器中安装 bubblewrap（例如 Debian/Ubuntu：<code class="font-mono">apt-get install bubblewrap</code>），然后刷新探测。</p>
          </details>
        </div>
      </section>

      <section
        v-if="showGuide"
        id="runtime-install"
        class="border-warning-200 bg-warning-50/60 dark:border-warning-500/20 dark:bg-warning-500/5 mb-5 rounded-2xl border p-5 shadow-sm"
      >
        <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">如何补齐缺失工具</h3>
        <p class="mt-1 text-sm leading-6 text-gray-600 dark:text-gray-300">
          默认镜像不含 Node / Python / uv。推荐使用下方预置安装，或将工具放入数据卷。
          <span v-if="binDir" class="font-mono text-xs">优先路径：{{ binDir }}</span>
        </p>
        <div class="mt-3 grid gap-3 text-sm leading-6 text-gray-700 dark:text-gray-200 sm:grid-cols-2">
          <div class="rounded-xl border border-warning-200 bg-white/60 p-3 dark:border-warning-500/20 dark:bg-white/[0.03]">
            <p class="font-medium">推荐：使用下方预置安装</p>
            <p class="mt-1 text-xs text-gray-600 dark:text-gray-300">固定版本、官方源和 SHA256 校验，安装后自动刷新探测。</p>
          </div>
          <div class="rounded-xl border border-gray-200 bg-white/60 p-3 dark:border-gray-700 dark:bg-white/[0.03]">
            <p class="font-medium">高级：手动放入 bin 目录</p>
            <p class="mt-1 text-xs text-gray-600 dark:text-gray-300">将可执行文件放入 <code class="font-mono">{{ binDir }}</code>，也可使用 node/bin、python/bin、uv/bin 布局。</p>
          </div>
        </div>
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
            <div
              v-if="summary.installProgress?.packageId === pkg.id"
              class="mt-3 rounded-lg bg-brand-50 px-3 py-2 text-xs text-brand-700 dark:bg-brand-500/10 dark:text-brand-300"
            >
              <div class="flex items-center justify-between gap-2">
                <span>{{ installProgressLabel }}…</span>
                <span v-if="summary.installProgress.total > 0">{{ installProgressPercent }}%</span>
              </div>
              <div class="mt-1.5 h-1.5 overflow-hidden rounded-full bg-brand-100 dark:bg-brand-500/20">
                <div
                  v-if="summary.installProgress.total > 0"
                  class="h-full rounded-full bg-brand-500 transition-[width] duration-300"
                  :style="{ width: String(installProgressPercent) + '%' }"
                />
                <div v-else class="h-full w-1/3 animate-pulse rounded-full bg-brand-500" />
              </div>
            </div>
            <div class="mt-4 flex flex-wrap gap-2">
              <button
                v-if="!pkg.installed"
                type="button"
                class="bg-brand-500 hover:bg-brand-600 inline-flex h-9 items-center rounded-lg px-3.5 text-sm font-medium text-white transition disabled:cursor-not-allowed disabled:opacity-50"
                :disabled="!pkg.supported || activeInstallPackageId !== ''"
                :aria-label="`安装 ${pkg.name}`"
                @click="onInstall(pkg)"
              >
                <span
                  v-if="busyPackageId === pkg.id"
                  class="mr-1.5 inline-block h-3.5 w-3.5 animate-spin rounded-full border-2 border-white/40 border-t-white"
                />
                {{ activeInstallPackageId === pkg.id ? '安装中…' : '安装' }}
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

        <!-- 安装日志与错误：不再被 toast 吞掉，便于排查被墙/超时/校验失败 -->
        <section
          v-if="showInstallPanel"
          class="mt-4 rounded-2xl border bg-white p-5 shadow-sm dark:bg-white/[0.03]"
          :class="installError ? 'border-error-200 dark:border-error-500/30' : 'border-gray-200 dark:border-gray-800'"
        >
          <div class="mb-3 flex flex-wrap items-center justify-between gap-2">
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">安装日志</h3>
            <span
              v-if="summary.installProgress"
              class="inline-flex rounded-full bg-brand-50 px-2.5 py-0.5 text-[11px] font-medium text-brand-700 dark:bg-brand-500/10 dark:text-brand-300"
            >
              {{ installProgressLabel }}…
            </span>
          </div>

          <!-- 失败错误条：持久展示，直到下一次安装开始 -->
          <div
            v-if="installError"
            class="mb-4 rounded-xl border border-error-200 bg-error-50 px-4 py-3 text-sm text-error-700 dark:border-error-500/30 dark:bg-error-500/10 dark:text-error-300"
          >
            <p class="font-semibold">安装失败</p>
            <p class="mt-1 break-all">{{ installError }}</p>
            <p class="mt-2 text-xs text-error-600/80 dark:text-error-400/80">
              国内网络下官方源(nodejs.org/github.com)常不可达。系统已自动尝试镜像源，若仍失败可稍后重试或检查网络。
            </p>
          </div>

          <!-- 日志时间线：小屏单列 -->
          <ol v-if="installLogs.length > 0" class="space-y-2.5">
            <li
              v-for="(entry, idx) in installLogs"
              :key="idx"
              class="flex gap-3 text-xs"
            >
              <div class="flex flex-col items-center">
                <span class="mt-1 h-2 w-2 shrink-0 rounded-full" :class="logEntryDotClass(entry)"></span>
                <span
                  v-if="idx < installLogs.length - 1"
                  class="mt-0.5 w-px flex-1 bg-gray-200 dark:bg-gray-700"
                ></span>
              </div>
              <div class="min-w-0 flex-1 pb-1">
                <div class="flex flex-wrap items-center gap-x-2 gap-y-0.5">
                  <span class="font-mono text-gray-400 dark:text-gray-500">{{ logTimeShort(entry.at) }}</span>
                  <span
                    class="inline-flex rounded bg-gray-100 px-1.5 py-0.5 text-[10px] font-medium text-gray-600 dark:bg-white/5 dark:text-gray-300"
                  >{{ logPhaseLabel(entry.phase) }}</span>
                  <span
                    v-if="entry.level === 'success'"
                    class="text-success-600 dark:text-success-400"
                  >成功</span>
                  <span
                    v-else-if="entry.level === 'error'"
                    class="text-error-600 dark:text-error-400"
                  >失败</span>
                </div>
                <p class="mt-0.5 break-all text-gray-700 dark:text-gray-200">{{ entry.message }}</p>
                <p v-if="entry.source" class="mt-0.5 break-all font-mono text-[10px] text-gray-400 dark:text-gray-500">
                  {{ entry.source }}
                </p>
              </div>
            </li>
          </ol>
          <p v-else-if="!summary.installProgress" class="text-sm text-gray-400 dark:text-gray-500">
            暂无安装日志。
          </p>
        </section>
      </section>

      <!-- 依赖管理：npm/pip 第三方包安装/升级/卸载（青龙式集中管理） -->
      <section
        class="mb-5 rounded-2xl border border-gray-200 bg-white p-5 shadow-sm dark:border-gray-800 dark:bg-white/[0.03]"
      >
        <div class="mb-4 flex flex-wrap items-start justify-between gap-2">
          <div class="min-w-0">
            <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">依赖管理</h3>
            <p class="mt-1 max-w-3xl text-sm text-gray-500 dark:text-gray-400">
              为已安装的 Node / Python 运行时集中安装第三方包，所有 stdio 上游共享。npm 包装到 Node
              全局区（node/npx 上游可直接 require）；pip 包装到共享 venv（python 上游需把该 venv
              加入 PATH）。走已配置的镜像源。
            </p>
          </div>
          <button
            type="button"
            class="inline-flex h-9 items-center gap-1.5 rounded-lg border border-gray-300 px-3 text-sm font-medium text-gray-700 transition hover:bg-gray-50 disabled:opacity-60 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
            :disabled="depBusy"
            :aria-label="`刷新${depKind} 依赖列表`"
            @click="loadDepList(depKind)"
          >
            <RefreshIcon class="h-4 w-4" :class="depListLoading ? 'animate-spin' : ''" aria-hidden="true" />
            刷新
          </button>
        </div>

        <!-- npm / pip 分段切换 -->
        <div class="mb-4 inline-flex rounded-lg border border-gray-200 p-1 dark:border-gray-700">
          <button
            type="button"
            class="rounded-md px-4 py-1.5 text-sm font-medium transition"
            :class="depKind === 'npm' ? 'bg-brand-500 text-white' : 'text-gray-600 hover:text-gray-800 dark:text-gray-300 dark:hover:text-white'"
            @click="switchDepKind('npm')"
          >
            npm
          </button>
          <button
            type="button"
            class="rounded-md px-4 py-1.5 text-sm font-medium transition"
            :class="depKind === 'pip' ? 'bg-brand-500 text-white' : 'text-gray-600 hover:text-gray-800 dark:text-gray-300 dark:hover:text-white'"
            @click="switchDepKind('pip')"
          >
            pip
          </button>
        </div>

        <!-- 运行时未就绪时的引导：npm 未装 / Python 解释器缺失 -->
        <div
          v-if="currentDepList && !currentDepList.ready && (currentDepList.warning || currentDepList.pythonHint)"
          class="mb-4 rounded-xl border border-warning-200 bg-warning-50 px-4 py-3 text-sm text-warning-800 dark:border-warning-500/30 dark:bg-warning-500/10 dark:text-warning-300"
        >
          <p class="font-semibold">
            {{ depKind === 'pip' ? 'Python 解释器未检测到' : `${depKind} 运行时未就绪` }}
          </p>
          <p class="mt-1">{{ currentDepList.pythonHint || currentDepList.warning }}</p>
          <a
            href="#runtime-install"
            class="mt-2 inline-block font-medium underline underline-offset-2"
          >查看预置安装</a>
        </div>

        <!-- 安装输入条：单行，支持逗号/空格分隔多个包 -->
        <div class="mb-4 flex flex-col gap-2 sm:flex-row sm:items-center">
          <div class="flex-1">
            <input
              v-model="depInput"
              type="text"
              class="h-10 w-full rounded-lg border bg-transparent px-3 text-sm text-gray-800 shadow-sm placeholder:text-gray-400 focus:outline-none focus:ring-3 focus:ring-brand-500/10 dark:text-white/90 dark:placeholder:text-white/30"
              :class="depInputError !== '' || !validDepInput ? 'border-error-300 focus:border-error-300' : 'border-gray-300 focus:border-brand-300 dark:border-gray-700'"
              :placeholder="depKind === 'npm' ? 'lodash 或 lodash@4.17.21（多个用逗号分隔）' : 'requests 或 requests==2.31.0（多个用逗号分隔）'"
              :disabled="depBusy"
              @keydown.enter.prevent="onInstallDep"
            />
            <p
              v-if="depInputError !== '' || !validDepInput"
              class="mt-1 text-xs text-error-500"
            >
              {{ depInputError || '包名格式不正确' }}
            </p>
          </div>
          <button
            type="button"
            class="inline-flex h-10 items-center justify-center gap-1.5 rounded-lg px-4 text-sm font-medium text-white transition disabled:cursor-not-allowed disabled:opacity-50"
            :class="depKind === 'npm' ? 'bg-brand-500 hover:bg-brand-600' : 'bg-success-500 hover:bg-success-600'"
            :disabled="depInput.trim() === '' || depBusy || (currentDepList !== null && !currentDepList.ready)"
            :aria-label="`安装 ${depKind} 依赖`"
            @click="onInstallDep"
          >
            <span
              v-if="depInstalling"
              class="mr-1 inline-block h-3.5 w-3.5 animate-spin rounded-full border-2 border-white/40 border-t-white"
            />
            <PlusIcon v-else class="h-4 w-4" aria-hidden="true" />
            {{ depInstalling ? '安装中…' : '安装' }}
          </button>
        </div>

        <!-- 安装进度条（依赖操作进行中） -->
        <div
          v-if="depProgress"
          class="mb-4 rounded-lg bg-brand-50 px-3 py-2 text-xs text-brand-700 dark:bg-brand-500/10 dark:text-brand-300"
        >
          <div class="flex items-center justify-between gap-2">
            <span>
              {{ depProgress.action === 'uninstall' ? '正在卸载' : '正在安装' }}
              <span class="font-mono">{{ depProgress.spec || '' }}</span>
            </span>
            <span class="inline-flex items-center gap-1">
              <span class="inline-block h-3 w-3 animate-spin rounded-full border-2 border-brand-300 border-t-brand-600"></span>
            </span>
          </div>
          <div class="mt-1.5 h-1.5 overflow-hidden rounded-full bg-brand-100 dark:bg-brand-500/20">
            <div class="h-full w-1/3 animate-pulse rounded-full bg-brand-500" />
          </div>
        </div>

        <!-- 依赖操作失败错误条 -->
        <div
          v-if="depError"
          class="mb-4 rounded-xl border border-error-200 bg-error-50 px-4 py-3 text-sm text-error-700 dark:border-error-500/30 dark:bg-error-500/10 dark:text-error-300"
        >
          <p class="font-semibold">操作失败</p>
          <p class="mt-1 break-all">{{ depError }}</p>
        </div>

        <!-- 列表加载错误 -->
        <p
          v-if="depListError !== ''"
          class="mb-4 rounded-xl border border-error-200 bg-error-50 px-4 py-3 text-sm text-error-600 dark:border-error-500/30 dark:bg-error-500/10 dark:text-error-400"
        >
          {{ depListError }}
        </p>

        <!-- 依赖列表：响应式卡片网格 -->
        <div
          v-if="currentDepList && currentDepList.ready"
        >
          <div
            v-if="currentDepList.count === 0"
            class="rounded-xl border border-dashed border-gray-200 px-4 py-8 text-center text-sm text-gray-400 dark:border-gray-700"
          >
            暂无已安装的第三方包。可在上方输入包名安装，例如
            <span class="font-mono">{{ depKind === 'npm' ? 'lodash' : 'requests' }}</span>。
          </div>
          <div v-else class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4 3xl:grid-cols-5">
            <article
              v-for="dep in currentDepList.items"
              :key="dep.name"
              class="group hover:border-brand-200 dark:hover:border-brand-500/30 rounded-xl border border-gray-100 bg-gray-50/80 p-3 transition duration-300 hover:bg-white hover:shadow-md dark:border-gray-800 dark:bg-white/[0.03] dark:hover:bg-white/[0.05]"
            >
              <div class="flex items-start justify-between gap-2">
                <h4 class="min-w-0 break-all font-mono text-sm font-semibold text-gray-800 dark:text-white/90">
                  {{ dep.name }}
                </h4>
                <span class="inline-flex shrink-0 rounded-full bg-success-50 px-2 py-0.5 text-[10px] font-medium text-success-700 dark:bg-success-500/10 dark:text-success-400">
                  {{ dep.version || '—' }}
                </span>
              </div>
              <div class="mt-3 flex flex-wrap gap-1.5">
                <button
                  type="button"
                  class="inline-flex h-7 items-center rounded-md border border-gray-300 px-2 text-xs font-medium text-gray-600 transition hover:bg-gray-100 disabled:opacity-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
                  :disabled="depBusy"
                  :aria-label="`升级 ${dep.name}`"
                  title="升级到最新版"
                  @click="onUpgradeDep(dep)"
                >
                  <RefreshIcon class="h-3.5 w-3.5" aria-hidden="true" />
                </button>
                <button
                  type="button"
                  class="inline-flex h-7 items-center rounded-md border border-gray-300 px-2 text-xs font-medium text-error-600 transition hover:bg-error-50 disabled:opacity-50 dark:border-gray-700 dark:text-error-400 dark:hover:bg-error-500/10"
                  :disabled="depBusy"
                  :aria-label="`卸载 ${dep.name}`"
                  title="卸载"
                  @click="onUninstallDep(dep)"
                >
                  <span
                    v-if="depUninstallingName === dep.name"
                    class="mr-1 inline-block h-3 w-3 animate-spin rounded-full border-2 border-error-300 border-t-error-600"
                  />
                  <TrashIcon v-else class="h-3.5 w-3.5" aria-hidden="true" />
                </button>
              </div>
            </article>
          </div>
        </div>

        <!-- 依赖操作日志时间线 -->
        <details
          v-if="showDepPanel"
          class="mt-4 rounded-xl border border-gray-100 bg-gray-50/50 p-3 dark:border-white/5 dark:bg-white/[0.02]"
        >
          <summary class="cursor-pointer text-xs font-semibold text-gray-600 dark:text-gray-300">
            依赖操作日志（{{ depLogs.length }} 条）
          </summary>
          <ol class="mt-3 space-y-2">
            <li
              v-for="(entry, idx) in depLogs"
              :key="idx"
              class="flex gap-2.5 text-xs"
            >
              <div class="flex flex-col items-center">
                <span class="mt-1 h-2 w-2 shrink-0 rounded-full" :class="depLogDotClass(entry)"></span>
                <span
                  v-if="idx < depLogs.length - 1"
                  class="mt-0.5 w-px flex-1 bg-gray-200 dark:bg-gray-700"
                ></span>
              </div>
              <div class="min-w-0 flex-1 pb-1">
                <div class="flex flex-wrap items-center gap-x-2 gap-y-0.5">
                  <span class="font-mono text-gray-400 dark:text-gray-500">{{ depLogTimeShort(entry.at) }}</span>
                  <span
                    class="inline-flex rounded bg-gray-100 px-1.5 py-0.5 text-[10px] font-medium text-gray-600 dark:bg-white/5 dark:text-gray-300"
                  >{{ entry.kind }}</span>
                  <span
                    v-if="entry.level === 'success'"
                    class="text-success-600 dark:text-success-400"
                  >成功</span>
                  <span
                    v-else-if="entry.level === 'error'"
                    class="text-error-600 dark:text-error-400"
                  >失败</span>
                </div>
                <p class="mt-0.5 break-all text-gray-700 dark:text-gray-200">{{ entry.message }}</p>
              </div>
            </li>
          </ol>
        </details>
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

        <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3 2xl:grid-cols-4 3xl:grid-cols-5">
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
            <div v-if="!tool.available && !tool.warning && packageForTool(tool)" class="mt-3 flex items-center justify-between gap-2">
              <span class="text-[11px] text-gray-400 dark:text-gray-500">
                可安装 {{ packageForTool(tool)?.name }} {{ packageForTool(tool)?.version }}
              </span>
              <button
                type="button"
                class="bg-brand-500 hover:bg-brand-600 inline-flex h-8 shrink-0 items-center rounded-lg px-3 text-xs font-medium text-white transition disabled:cursor-not-allowed disabled:opacity-50"
                :disabled="activeInstallPackageId !== ''"
                :aria-label="`${packageForTool(tool)?.installed ? '修复' : '安装'} ${tool.name}`"
                @click="onInstallForTool(tool)"
              >
                <span
                  v-if="packageForTool(tool)?.id === activeInstallPackageId"
                  class="mr-1.5 inline-block h-3 w-3 animate-spin rounded-full border-2 border-white/40 border-t-white"
                  aria-hidden="true"
                />
                {{ packageForTool(tool)?.installed ? '修复安装' : '一键安装' }}
              </button>
            </div>
          </article>
        </div>
      </section>

      <details
        class="mb-5 rounded-2xl border border-gray-200 bg-white shadow-sm dark:border-gray-800 dark:bg-white/[0.03]"
      >
        <summary class="cursor-pointer list-none px-5 py-4 text-base font-semibold text-gray-800 marker:hidden dark:text-white/90">
          安全说明（{{ summary.riskNotes.length }} 条）
        </summary>
        <div class="border-t border-gray-100 px-5 pb-5 pt-4 dark:border-white/5">
          <ul class="space-y-2 text-sm leading-6 text-gray-600 dark:text-gray-300">
            <li v-for="(note, idx) in summary.riskNotes" :key="idx" class="flex gap-2">
              <span class="bg-brand-500 mt-2 h-1.5 w-1.5 shrink-0 rounded-full"></span>
              <span>{{ note }}</span>
            </li>
          </ul>
        </div>
        <div class="flex flex-wrap gap-2 px-5 pb-5">
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
      </details>
    </template>
  </AdminLayout>
</template>
