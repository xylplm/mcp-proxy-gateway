<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import AdminLayout from '@/components/layout/AdminLayout.vue'
import PageBreadcrumb from '@/components/common/PageBreadcrumb.vue'
import CodeEditor from '@/components/common/CodeEditor.vue'
import {
  activateScriptVersion,
  analyzeScriptContent,
  createScript,
  deleteScript,
  diffScriptVersions,
  getScript,
  getScriptVersion,
  listScriptVersions,
  listScripts,
  saveScriptContent,
  updateScriptMeta,
  type ScriptDetail,
  type ScriptDiffResult,
  type ScriptItem,
  type ScriptLanguage,
  type ScriptRiskReport,
  type ScriptVersion,
} from '@/api/scripts'
import {
  SCRIPT_TEMPLATES,
  exportScriptPackage,
  languageLabel,
  parseScriptImport,
  riskBadgeClass,
  riskLabel,
} from '@/utils/scripts'
import { useToast } from '@/composables/useToast'
import { useConfirm } from '@/composables/useConfirm'

const toast = useToast()
const { confirm } = useConfirm()

const scripts = ref<ScriptItem[]>([])
const loading = ref(false)
const loadError = ref('')
const search = ref('')
const languageFilter = ref('')
const riskFilter = ref('')

const editorOpen = ref(false)
const editorMode = ref<'create' | 'edit'>('create')
const detail = ref<ScriptDetail | null>(null)
const editingName = ref('')
const editingDescription = ref('')
const editingTags = ref('')
const editingLanguage = ref<ScriptLanguage>('python')
const editingRuntime = ref('python3')
const editingContent = ref('')
const versionNote = ref('')
const saving = ref(false)
const analyzing = ref(false)
const liveRisk = ref<ScriptRiskReport | null>(null)
const editorError = ref('')
const importInput = ref<HTMLInputElement | null>(null)
let detailSeq = 0
let analyzeSeq = 0
let versionsSeq = 0

const versions = ref<ScriptVersion[]>([])
const versionsLoading = ref(false)
const diffOpen = ref(false)
const diffResult = ref<ScriptDiffResult | null>(null)
const diffLoading = ref(false)
const diffLeft = ref('')
const diffRight = ref('')

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  return scripts.value.filter((item) => {
    if (languageFilter.value && item.language !== languageFilter.value) return false
    if (riskFilter.value && item.risk.level !== riskFilter.value) return false
    if (!q) return true
    const blob = [item.name, item.description ?? '', ...(item.tags ?? [])].join(' ').toLowerCase()
    return blob.includes(q)
  })
})

const activeRisk = computed(() => liveRisk.value ?? detail.value?.risk ?? null)
/** 编辑器语言（仅 python/javascript 两态，供 CodeEditor）。 */
const editorLanguage = computed<'python' | 'javascript'>(() =>
  editingLanguage.value === 'javascript' ? 'javascript' : 'python',
)
const isDirty = computed(() => {
  if (editorMode.value === 'create')
    return editingContent.value.trim() !== '' || editingName.value.trim() !== ''
  if (!detail.value) return false
  return (
    editingName.value !== detail.value.name ||
    editingDescription.value !== (detail.value.description ?? '') ||
    editingTags.value !== (detail.value.tags ?? []).join(', ') ||
    editingContent.value !== detail.value.content
  )
})

async function load(): Promise<void> {
  if (loading.value) return
  loading.value = true
  loadError.value = ''
  try {
    scripts.value = await listScripts()
  } catch (err) {
    loadError.value = err instanceof Error ? err.message : '加载脚本失败'
  } finally {
    loading.value = false
  }
}

function openCreate(language: 'python' | 'javascript' = 'python'): void {
  detailSeq += 1
  analyzeSeq += 1
  versionsSeq += 1
  const tpl = SCRIPT_TEMPLATES[language]
  editorMode.value = 'create'
  detail.value = null
  editingName.value = tpl.name
  editingDescription.value = ''
  editingTags.value = ''
  editingLanguage.value = language
  editingRuntime.value = tpl.runtime
  editingContent.value = tpl.content
  versionNote.value = '初始版本'
  liveRisk.value = null
  versions.value = []
  editorError.value = ''
  editorOpen.value = true
  void analyzeCurrent()
}

/**
 * 判断编辑器内容是否仍是当前语言的未改动模板（用于切换语言时决定是否替换内容）。
 * 用户已手动编辑则保留其内容，仅切换高亮/检测语言。
 */
function isPristineTemplate(): boolean {
  const lang = editingLanguage.value
  if (lang !== 'python' && lang !== 'javascript') return false
  const tpl = SCRIPT_TEMPLATES[lang]
  return (
    editingContent.value === tpl.content &&
    editingName.value === tpl.name &&
    editingDescription.value === ''
  )
}

/**
 * 切换脚本语言：仅在 create 模式且内容仍是当前语言模板（未手改）时，替换为新语言模板；
 * 否则保留用户内容，仅切换高亮/检测语言。同时同步 runtime。
 * 此 watcher 取代了语言 <select> 上原内联 @change（统一处理 runtime + 模板）。
 */
watch(editingLanguage, (next) => {
  if (editorMode.value === 'create' && isPristineTemplate()) {
    if (next === 'python' || next === 'javascript') {
      const tpl = SCRIPT_TEMPLATES[next]
      editingContent.value = tpl.content
      editingName.value = tpl.name
    }
  }
  editingRuntime.value = next === 'python' ? 'python3' : 'node'
})

async function openEdit(item: ScriptItem): Promise<void> {
  const seq = ++detailSeq
  analyzeSeq += 1
  versionsSeq += 1
  editorOpen.value = true
  editorMode.value = 'edit'
  editorError.value = ''
  liveRisk.value = null
  versions.value = []
  try {
    const d = await getScript(item.id)
    if (seq !== detailSeq || !editorOpen.value) return
    detail.value = d
    editingName.value = d.name
    editingDescription.value = d.description ?? ''
    editingTags.value = (d.tags ?? []).join(', ')
    editingLanguage.value = d.language
    editingRuntime.value = d.runtime
    editingContent.value = d.content
    versionNote.value = ''
    await loadVersions(d.id)
  } catch (err) {
    if (seq === detailSeq && editorOpen.value) {
      editorError.value = err instanceof Error ? err.message : '加载脚本详情失败'
    }
  }
}

async function closeEditor(): Promise<void> {
  if (isDirty.value) {
    const ok = await confirm({
      title: '放弃未保存修改？',
      message: '当前脚本有未保存修改，关闭后会丢失。',
      confirmText: '放弃修改',
      cancelText: '继续编辑',
      tone: 'warning',
    })
    if (!ok) return
  }
  editorOpen.value = false
  detailSeq += 1
  analyzeSeq += 1
  versionsSeq += 1
}

function parseTags(): string[] {
  return editingTags.value
    .split(/[，,、;；\n]/)
    .map((item) => item.trim())
    .filter(Boolean)
}

async function analyzeCurrent(): Promise<void> {
  const content = editingContent.value
  if (!content.trim()) return
  const seq = ++analyzeSeq
  analyzing.value = true
  try {
    const report = await analyzeScriptContent(content)
    if (seq === analyzeSeq && editorOpen.value && editingContent.value === content) {
      liveRisk.value = report
    }
  } catch (err) {
    if (seq === analyzeSeq && editorOpen.value) {
      toast.error(err instanceof Error ? err.message : '风险分析失败')
    }
  } finally {
    if (seq === analyzeSeq) analyzing.value = false
  }
}

async function save(): Promise<void> {
  if (saving.value) return
  editorError.value = ''
  if (!editingName.value.trim()) {
    editorError.value = '请输入脚本名称'
    return
  }
  if (!editingContent.value.trim()) {
    editorError.value = '脚本内容不能为空'
    return
  }
  saving.value = true
  try {
    if (editorMode.value === 'create') {
      const created = await createScript({
        name: editingName.value.trim(),
        description: editingDescription.value.trim(),
        language: editingLanguage.value,
        runtime: editingRuntime.value,
        content: editingContent.value,
        tags: parseTags(),
        note: versionNote.value.trim(),
      })
      detail.value = created
      editorMode.value = 'edit'
      toast.success('脚本已创建')
    } else if (detail.value) {
      const metaChanged =
        editingName.value !== detail.value.name ||
        editingDescription.value !== (detail.value.description ?? '') ||
        editingTags.value !== (detail.value.tags ?? []).join(', ')
      if (metaChanged) {
        await updateScriptMeta(detail.value.id, {
          name: editingName.value.trim(),
          description: editingDescription.value.trim(),
          tags: parseTags(),
        })
      }
      if (editingContent.value !== detail.value.content) {
        detail.value = await saveScriptContent(detail.value.id, {
          content: editingContent.value,
          note: versionNote.value.trim(),
        })
      } else {
        detail.value = await getScript(detail.value.id)
      }
      toast.success('脚本已保存')
    }
    liveRisk.value = null
    versionNote.value = ''
    if (detail.value) await loadVersions(detail.value.id)
    await load()
  } catch (err) {
    editorError.value = err instanceof Error ? err.message : '保存脚本失败'
  } finally {
    saving.value = false
  }
}

async function remove(item: ScriptItem): Promise<void> {
  const ok = await confirm({
    title: '移至回收站',
    message: `确认删除脚本「${item.name}」？脚本文件会保留在网关回收站中。请先确认没有上游仍在使用它。`,
    confirmText: '移至回收站',
    cancelText: '取消',
    tone: 'danger',
  })
  if (!ok) return
  try {
    const result = await deleteScript(item.id)
    if (result.warning) {
      toast.warning('脚本已停用，但磁盘清理未完成，请稍后重试或联系管理员')
    } else {
      toast.success('脚本已移至回收站')
    }
    if (detail.value?.id === item.id) editorOpen.value = false
    await load()
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '删除失败')
  }
}

async function loadVersions(id: string): Promise<void> {
  const seq = ++versionsSeq
  versionsLoading.value = true
  try {
    const loaded = await listScriptVersions(id)
    if (seq !== versionsSeq || detail.value?.id !== id || !editorOpen.value) return
    versions.value = loaded
    if (versions.value.length >= 2) {
      diffLeft.value = versions.value[1].version
      diffRight.value = versions.value[0].version
    } else {
      diffLeft.value = ''
      diffRight.value = ''
    }
  } finally {
    if (seq === versionsSeq) versionsLoading.value = false
  }
}

async function viewVersion(version: string): Promise<void> {
  if (!detail.value) return
  try {
    const res = await getScriptVersion(detail.value.id, version)
    editingContent.value = res.content
    liveRisk.value = res.meta.risk
    versionNote.value = `基于 ${version} 恢复后发布新版本`
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '读取版本失败')
  }
}

async function activate(version: string): Promise<void> {
  if (!detail.value || version === detail.value.currentVersion) return
  const ok = await confirm({
    title: '切换当前版本',
    message: `确认将 ${version} 设为脚本中心当前版本？已有上游仍保持其固定版本与哈希；需要升级时请在上游中重新选择。`,
    confirmText: '切换版本',
    cancelText: '取消',
    tone: 'warning',
  })
  if (!ok) return
  try {
    await activateScriptVersion(detail.value.id, version)
    await openEdit(detail.value)
    await load()
    toast.success('当前版本已切换')
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '切换版本失败')
  }
}

async function showDiff(): Promise<void> {
  if (!detail.value || versions.value.length < 2) return
  if (!diffLeft.value) diffLeft.value = versions.value[1].version
  if (!diffRight.value) diffRight.value = versions.value[0].version
  if (!diffLeft.value || !diffRight.value || diffLeft.value === diffRight.value) {
    toast.info('请选择两个不同版本')
    return
  }
  diffOpen.value = true
  diffLoading.value = true
  try {
    diffResult.value = await diffScriptVersions(detail.value.id, diffLeft.value, diffRight.value)
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '生成差异失败')
  } finally {
    diffLoading.value = false
  }
}

function chooseImport(): void {
  importInput.value?.click()
}

async function onImportFile(event: Event): Promise<void> {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return
  if (file.size > 1024 * 1024) {
    toast.error('单个脚本文件不能超过 1 MiB')
    return
  }
  try {
    const parsed = parseScriptImport(file.name, await file.text())
    editorMode.value = 'create'
    detail.value = null
    editingName.value = parsed.name
    editingDescription.value = parsed.description
    editingTags.value = parsed.tags.join(', ')
    editingLanguage.value = parsed.language
    editingRuntime.value = parsed.runtime
    editingContent.value = parsed.content
    versionNote.value = parsed.note
    liveRisk.value = null
    editorOpen.value = true
    await analyzeCurrent()
  } catch (err) {
    toast.error(err instanceof Error ? err.message : '导入失败')
  }
}

function exportCurrent(): void {
  if (!detail.value) return
  const blob = new Blob([exportScriptPackage({ ...detail.value, content: editingContent.value })], {
    type: 'application/json;charset=utf-8',
  })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${detail.value.name}-${detail.value.currentVersion}.mpg-script.json`
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
  toast.success('脚本文件已导出')
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`
  return `${(n / 1024).toFixed(1)} KiB`
}

function formatTime(v: string): string {
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? v : d.toLocaleString()
}

onMounted(load)
</script>

<template>
  <AdminLayout>
    <PageBreadcrumb pageTitle="脚本中心" />

    <section
      class="rounded-2xl border border-gray-200 bg-white p-5 dark:border-gray-800 dark:bg-white/[0.03]"
    >
      <div class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
        <div>
          <h2 class="text-lg font-semibold text-gray-900 dark:text-white/90">受管脚本库</h2>
          <p class="mt-1 max-w-3xl text-sm leading-6 text-gray-500 dark:text-gray-400">
            统一管理本地 stdio MCP
            脚本。脚本保存到网关数据目录，只能由受控解释器启动；静态风险分析不等同于内核沙箱。
          </p>
        </div>
        <div class="flex flex-wrap gap-2">
          <button
            type="button"
            class="rounded-xl border border-gray-300 px-4 py-2.5 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-white/5"
            @click="chooseImport"
          >
            导入脚本
          </button>
          <input
            ref="importInput"
            class="sr-only"
            type="file"
            accept=".py,.js,.mjs,.cjs,.json,text/plain,application/json"
            @change="onImportFile"
          />
          <button
            type="button"
            class="bg-brand-500 hover:bg-brand-600 rounded-xl px-4 py-2.5 text-sm font-medium text-white"
            @click="openCreate('python')"
          >
            新建脚本
          </button>
        </div>
      </div>

      <div class="mt-5 grid grid-cols-1 gap-3 md:grid-cols-[minmax(0,1fr)_180px_160px]">
        <input
          v-model="search"
          type="search"
          class="focus:border-brand-300 focus:ring-brand-500/10 h-11 rounded-xl border border-gray-300 px-4 text-sm outline-none focus:ring-3 dark:border-gray-700 dark:bg-transparent dark:text-white/90"
          placeholder="搜索名称、描述或标签"
        />
        <select
          v-model="languageFilter"
          class="h-11 rounded-xl border border-gray-300 px-3 text-sm dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
        >
          <option value="">全部语言</option>
          <option value="python">Python</option>
          <option value="javascript">JavaScript</option>
        </select>
        <select
          v-model="riskFilter"
          class="h-11 rounded-xl border border-gray-300 px-3 text-sm dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
        >
          <option value="">全部风险</option>
          <option value="low">低风险</option>
          <option value="medium">中风险</option>
          <option value="high">高风险</option>
          <option value="critical">极高风险</option>
        </select>
      </div>
    </section>

    <p
      v-if="loadError"
      class="bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-300 mt-4 rounded-xl px-4 py-3 text-sm"
    >
      {{ loadError }}
    </p>
    <div v-if="loading" class="mt-5 grid grid-cols-1 gap-4 md:grid-cols-2 2xl:grid-cols-3 4k:grid-cols-4">
      <div
        v-for="n in 6"
        :key="n"
        class="h-48 animate-pulse rounded-2xl bg-gray-100 dark:bg-white/5"
      ></div>
    </div>
    <div
      v-else-if="filtered.length === 0"
      class="mt-5 rounded-2xl border border-dashed border-gray-300 bg-white px-5 py-14 text-center dark:border-gray-700 dark:bg-white/[0.02]"
    >
      <p class="text-sm font-medium text-gray-700 dark:text-gray-200">暂无脚本</p>
      <p class="mt-1 text-xs text-gray-400">
        新建 Python / JavaScript 脚本，或导入网关要运行的单文件脚本。
      </p>
      <div class="mt-4 flex justify-center gap-2">
        <button
          class="bg-brand-500 rounded-lg px-3 py-2 text-sm text-white"
          @click="openCreate('python')"
        >
          新建 Python
        </button>
        <button
          class="rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-600 dark:border-gray-700 dark:text-gray-300"
          @click="openCreate('javascript')"
        >
          新建 JavaScript
        </button>
      </div>
    </div>
    <div v-else class="4k:grid-cols-4 mt-5 grid grid-cols-1 gap-4 md:grid-cols-2 2xl:grid-cols-3">
      <article
        v-for="item in filtered"
        :key="item.id"
        class="group hover:border-brand-200 dark:hover:border-brand-500/30 rounded-2xl border border-gray-200 bg-white p-5 transition hover:-translate-y-0.5 hover:shadow-lg dark:border-gray-800 dark:bg-white/[0.03]"
      >
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <h3 class="truncate font-semibold text-gray-900 dark:text-white/90">{{ item.name }}</h3>
            <p class="mt-1 text-xs text-gray-400">
              {{ languageLabel(item.language) }} · {{ item.runtime }} · {{ item.currentVersion }}
            </p>
          </div>
          <span
            class="shrink-0 rounded-full px-2.5 py-1 text-[11px] font-semibold"
            :class="riskBadgeClass(item.risk.level)"
            >{{ riskLabel(item.risk.level) }}风险 · {{ item.risk.score }}</span
          >
        </div>
        <p class="mt-3 line-clamp-2 min-h-10 text-sm leading-5 text-gray-500 dark:text-gray-400">
          {{ item.description || '暂无描述' }}
        </p>
        <div class="mt-3 flex flex-wrap gap-1.5">
          <span
            v-for="tag in item.tags ?? []"
            :key="tag"
            class="rounded-full bg-gray-100 px-2 py-0.5 text-[10px] text-gray-600 dark:bg-white/5 dark:text-gray-300"
            >{{ tag }}</span
          >
        </div>
        <div class="mt-4 flex items-center justify-between text-[11px] text-gray-400">
          <span>{{ formatBytes(item.sizeBytes) }}</span
          ><span>{{ formatTime(item.updatedAt) }}</span>
        </div>
        <div class="mt-4 flex gap-2">
          <button
            class="hover:bg-brand-600 flex-1 rounded-lg bg-gray-900 px-3 py-2 text-sm font-medium text-white dark:bg-white dark:text-gray-900"
            @click="openEdit(item)"
          >
            查看与编辑</button
          ><button
            class="text-error-600 hover:bg-error-50 dark:hover:bg-error-500/10 rounded-lg border border-gray-200 px-3 py-2 text-sm dark:border-gray-700"
            @click="remove(item)"
          >
            删除
          </button>
        </div>
      </article>
    </div>

    <Teleport to="body">
      <transition name="fade">
        <div
          v-if="editorOpen"
          class="fixed inset-0 z-[100010] flex justify-end bg-gray-950/45 backdrop-blur-[2px]"
          @click.self="closeEditor"
        >
          <div class="flex h-full w-full max-w-6xl flex-col bg-white shadow-2xl dark:bg-gray-900">
            <header
              class="flex items-start justify-between border-b border-gray-200 px-4 py-4 sm:px-6 dark:border-gray-800"
            >
              <div>
                <h2 class="text-lg font-semibold text-gray-900 dark:text-white/90">
                  {{ editorMode === 'create' ? '新建受管脚本' : editingName }}
                </h2>
                <p class="mt-1 text-xs text-gray-400">
                  内容保存在网关数据目录；发布会创建不可变新版本。
                </p>
              </div>
              <button
                class="rounded-lg p-2 text-gray-400 hover:bg-gray-100 dark:hover:bg-white/5"
                aria-label="关闭"
                @click="closeEditor"
              >
                ✕
              </button>
            </header>
            <div
              class="grid min-h-0 flex-1 grid-cols-1 overflow-y-auto xl:grid-cols-[minmax(0,1fr)_340px] xl:overflow-hidden"
            >
              <main class="flex min-h-[60vh] flex-col p-4 sm:p-6 xl:min-h-0">
                <div class="grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
                  <input
                    v-model="editingName"
                    class="h-10 rounded-lg border border-gray-300 px-3 text-sm dark:border-gray-700 dark:bg-transparent dark:text-white/90"
                    placeholder="脚本名称"
                  />
                  <select
                    v-model="editingLanguage"
                    :disabled="editorMode === 'edit'"
                    class="h-10 rounded-lg border border-gray-300 px-3 text-sm dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
                  >
                    <option value="python">Python</option>
                    <option value="javascript">JavaScript</option>
                  </select>
                  <select
                    v-model="editingRuntime"
                    :disabled="editorMode === 'edit'"
                    class="h-10 rounded-lg border border-gray-300 px-3 text-sm dark:border-gray-700 dark:bg-gray-900 dark:text-white/90"
                  >
                    <template v-if="editingLanguage === 'python'"
                      ><option value="python3">python3</option>
                      <option value="python">python</option></template
                    >
                    <option v-else value="node">node</option>
                  </select>
                  <input
                    v-model="editingTags"
                    class="h-10 rounded-lg border border-gray-300 px-3 text-sm dark:border-gray-700 dark:bg-transparent dark:text-white/90"
                    placeholder="标签，逗号分隔"
                  />
                </div>
                <textarea
                  v-model="editingDescription"
                  rows="2"
                  class="mt-3 rounded-lg border border-gray-300 px-3 py-2 text-sm dark:border-gray-700 dark:bg-transparent dark:text-white/90"
                  placeholder="脚本用途与权限说明"
                ></textarea>
                <div
                  class="mt-4 flex min-h-[420px] flex-1 flex-col overflow-hidden rounded-2xl border border-gray-800 bg-[#0b1020] shadow-inner"
                >
                  <div
                    class="flex items-center justify-between border-b border-white/10 px-4 py-2 text-xs text-gray-400"
                  >
                    <span>{{ languageLabel(editingLanguage) }} · UTF-8 · 最大 1 MiB</span
                    ><button
                      class="rounded-lg border border-white/10 px-2.5 py-1 text-gray-300 hover:bg-white/5"
                      :disabled="analyzing"
                      @click="analyzeCurrent"
                    >
                      {{ analyzing ? '分析中…' : '风险分析' }}
                    </button>
                  </div>
                  <CodeEditor
                    v-model="editingContent"
                    :language="editorLanguage"
                    class="min-h-[380px] flex-1"
                  />
                </div>
              </main>
              <aside
                class="border-t border-gray-200 p-4 sm:p-6 xl:overflow-y-auto xl:border-t-0 xl:border-l dark:border-gray-800"
              >
                <section
                  v-if="activeRisk"
                  class="rounded-2xl border border-gray-200 p-4 dark:border-gray-800"
                >
                  <div class="flex items-center justify-between">
                    <h3 class="font-semibold text-gray-800 dark:text-white/90">静态风险</h3>
                    <span
                      class="rounded-full px-2.5 py-1 text-xs font-semibold"
                      :class="riskBadgeClass(activeRisk.level)"
                      >{{ riskLabel(activeRisk.level) }} · {{ activeRisk.score }}</span
                    >
                  </div>
                  <p class="mt-2 text-xs leading-5 text-gray-400">
                    仅静态规则提示，不代表代码安全或已隔离。
                  </p>
                  <div class="mt-3 space-y-2">
                    <div
                      v-for="f in activeRisk.findings"
                      :key="`${f.rule}-${f.line}`"
                      class="rounded-lg bg-gray-50 px-3 py-2 text-xs dark:bg-white/5"
                    >
                      <p class="font-medium text-gray-700 dark:text-gray-200">{{ f.message }}</p>
                      <p class="mt-1 text-gray-400">
                        {{ f.rule }}<span v-if="f.line"> · 第 {{ f.line }} 行</span>
                      </p>
                    </div>
                    <p v-if="activeRisk.findings.length === 0" class="text-success-600 text-xs">
                      未命中内置高风险规则
                    </p>
                  </div>
                </section>
                <section
                  v-if="editorMode === 'edit'"
                  class="mt-4 rounded-2xl border border-gray-200 p-4 dark:border-gray-800"
                >
                  <div class="flex items-center justify-between">
                    <h3 class="font-semibold text-gray-800 dark:text-white/90">版本</h3>
                    <button
                      class="text-brand-600 text-xs disabled:opacity-40"
                      :disabled="versions.length < 2"
                      @click="showDiff"
                    >
                      对比版本
                    </button>
                  </div>
                  <div
                    v-if="versions.length >= 2"
                    class="mt-3 grid grid-cols-[1fr_auto_1fr] items-center gap-2"
                  >
                    <select
                      v-model="diffLeft"
                      class="h-8 rounded-lg border border-gray-200 px-2 text-xs dark:border-gray-700 dark:bg-gray-900"
                    >
                      <option v-for="v in versions" :key="`l-${v.version}`" :value="v.version">
                        {{ v.version }}
                      </option></select
                    ><span class="text-xs text-gray-400">对</span
                    ><select
                      v-model="diffRight"
                      class="h-8 rounded-lg border border-gray-200 px-2 text-xs dark:border-gray-700 dark:bg-gray-900"
                    >
                      <option v-for="v in versions" :key="`r-${v.version}`" :value="v.version">
                        {{ v.version }}
                      </option>
                    </select>
                  </div>
                  <div v-if="versionsLoading" class="mt-3 text-xs text-gray-400">加载中…</div>
                  <div v-else class="mt-3 space-y-2">
                    <div
                      v-for="v in versions"
                      :key="v.version"
                      class="rounded-xl border border-gray-100 p-3 dark:border-gray-800"
                    >
                      <div class="flex justify-between">
                        <span class="text-sm font-medium text-gray-700 dark:text-gray-200"
                          >{{ v.version
                          }}<b
                            v-if="detail?.currentVersion === v.version"
                            class="text-success-600 ml-2 text-[10px]"
                            >当前</b
                          ></span
                        ><span class="text-[10px] text-gray-400">{{
                          formatTime(v.createdAt)
                        }}</span>
                      </div>
                      <p class="mt-1 truncate text-[11px] text-gray-400">
                        {{ v.note || '无版本说明' }}
                      </p>
                      <div class="mt-2 flex gap-2">
                        <button class="text-brand-600 text-xs" @click="viewVersion(v.version)">
                          载入编辑</button
                        ><button
                          v-if="detail?.currentVersion !== v.version"
                          class="text-warning-600 text-xs"
                          @click="activate(v.version)"
                        >
                          设为当前
                        </button>
                      </div>
                    </div>
                  </div>
                </section>
                <section class="mt-4 space-y-3">
                  <input
                    v-model="versionNote"
                    class="h-10 w-full rounded-lg border border-gray-300 px-3 text-sm dark:border-gray-700 dark:bg-transparent dark:text-white/90"
                    placeholder="版本说明（推荐）"
                  />
                  <p
                    v-if="editorError"
                    class="bg-error-50 text-error-600 dark:bg-error-500/10 rounded-lg px-3 py-2 text-xs"
                  >
                    {{ editorError }}
                  </p>
                  <div class="flex flex-wrap gap-2">
                    <button
                      v-if="detail"
                      class="rounded-lg border border-gray-300 px-3 py-2 text-sm text-gray-600 dark:border-gray-700 dark:text-gray-300"
                      @click="exportCurrent"
                    >
                      导出</button
                    ><button
                      class="bg-brand-500 flex-1 rounded-lg px-4 py-2 text-sm font-medium text-white disabled:opacity-50"
                      :disabled="saving"
                      @click="save"
                    >
                      {{
                        saving ? '保存中…' : editorMode === 'create' ? '创建脚本' : '保存并发布版本'
                      }}
                    </button>
                  </div>
                </section>
              </aside>
            </div>
          </div>
        </div>
      </transition>

      <div
        v-if="diffOpen"
        class="fixed inset-0 z-[100030] flex items-center justify-center bg-gray-950/50 p-4"
        @click.self="diffOpen = false"
      >
        <div
          class="max-h-[85vh] w-full max-w-4xl overflow-hidden rounded-2xl bg-white shadow-2xl dark:bg-gray-900"
        >
          <header
            class="flex justify-between border-b border-gray-200 px-5 py-4 dark:border-gray-800"
          >
            <h3 class="font-semibold text-gray-800 dark:text-white/90">版本差异</h3>
            <button @click="diffOpen = false">✕</button>
          </header>
          <div class="max-h-[70vh] overflow-auto bg-[#0b1020] p-4 font-mono text-xs leading-6">
            <p v-if="diffLoading" class="text-gray-400">生成中…</p>
            <template v-else
              ><div
                v-for="(h, i) in diffResult?.hunks ?? []"
                :key="i"
                :class="h.startsWith('+') ? 'text-success-300' : 'text-error-300'"
              >
                {{ h }}
              </div>
              <p v-if="diffResult?.truncated" class="text-warning-300 mt-3">差异过大，已截断。</p>
              <p v-if="(diffResult?.hunks?.length ?? 0) === 0" class="text-gray-400">
                两个版本内容一致。
              </p></template
            >
          </div>
        </div>
      </div>
    </Teleport>
  </AdminLayout>
</template>
