<script setup lang="ts">
import { computed, nextTick, onUnmounted, ref, watch } from 'vue'
import {
  getBrowseRoots,
  listBrowseDir,
  type BrowseEntry,
  type BrowseMode,
  type BrowseRoot,
} from '@/api/fsbrowse'
import { ApiError } from '@/api/request'
import FolderIcon from '@/icons/FolderIcon.vue'
import {
  browseRootTone,
  breadcrumbParts,
  displayPath,
  loadRecentPaths,
  rememberPath,
} from '@/utils/pathPicker'

const props = withDefaults(
  defineProps<{
    open: boolean
    mode?: BrowseMode
    title?: string
    initialPath?: string
    contextRoots?: string[]
    allowMissing?: boolean
  }>(),
  {
    mode: 'directory',
    title: '选择路径',
    initialPath: '',
    contextRoots: () => [],
    allowMissing: true,
  },
)

const emit = defineEmits<{
  close: []
  select: [path: string]
}>()

const roots = ref<BrowseRoot[]>([])
const recent = ref<string[]>([])
const platform = ref('')
const pathSeparator = ref('/')
const currentPath = ref('')
const parentPath = ref('')
const entries = ref<BrowseEntry[]>([])
const truncated = ref(false)
const loading = ref(false)
const errorMessage = ref('')
const filterText = ref('')
const selectedPath = ref('')
const selectedType = ref<'dir' | 'file' | 'missing' | ''>('')
const requestSeq = ref(0)
const dialogRef = ref<HTMLElement | null>(null)
let abortCtrl: AbortController | null = null
let triggerElement: HTMLElement | null = null
let previousBodyOverflow = ''

const filteredEntries = computed(() => {
  const q = filterText.value.trim().toLowerCase()
  if (q === '') return entries.value
  return entries.value.filter((item) => item.name.toLowerCase().includes(q))
})

const crumbs = computed(() => breadcrumbParts(currentPath.value, pathSeparator.value))
const hostLabel = computed(() => {
  if (platform.value === 'windows') return '网关主机（Windows）'
  if (platform.value === 'linux') return '网关主机（Linux）'
  if (platform.value === 'darwin') return '网关主机（macOS）'
  return '网关主机'
})

const canConfirm = computed(() => {
  const p = selectedPath.value.trim()
  if (p === '') return false
  if (props.mode === 'file') return selectedType.value === 'file'
  if (props.mode === 'directory') {
    if (selectedType.value === 'dir') return true
    return props.allowMissing && selectedType.value === 'missing'
  }
  if (selectedType.value === 'file' || selectedType.value === 'dir') return true
  return props.allowMissing && selectedType.value === 'missing'
})

function cancelInFlight(): void {
  if (abortCtrl) {
    abortCtrl.abort()
    abortCtrl = null
  }
}

async function loadRoots(): Promise<void> {
  const data = await getBrowseRoots(props.contextRoots)
  roots.value = data.roots ?? []
  platform.value = data.platform ?? ''
  pathSeparator.value = data.pathSeparator || '/'
  recent.value = loadRecentPaths(data.platform || 'default')
}

async function openPath(path: string): Promise<void> {
  const target = path.trim()
  if (target === '') return
  cancelInFlight()
  const ctrl = new AbortController()
  abortCtrl = ctrl
  const seq = ++requestSeq.value
  loading.value = true
  errorMessage.value = ''
  try {
    const data = await listBrowseDir({
      path: target,
      mode: props.mode,
      contextRoots: props.contextRoots,
      signal: ctrl.signal,
    })
    if (seq !== requestSeq.value) return
    currentPath.value = data.path
    parentPath.value = data.parent ?? ''
    entries.value = data.entries ?? []
    truncated.value = !!data.truncated
    pathSeparator.value = data.pathSeparator || pathSeparator.value
    platform.value = data.platform || platform.value
    selectedPath.value = data.path
    selectedType.value = 'dir'
  } catch (err) {
    if (ctrl.signal.aborted || seq !== requestSeq.value) return
    errorMessage.value =
      err instanceof ApiError ? err.message : err instanceof Error ? err.message : '加载目录失败'
    entries.value = []
    truncated.value = false
  } finally {
    if (seq === requestSeq.value) loading.value = false
  }
}

async function bootstrap(): Promise<void> {
  const seq = ++requestSeq.value
  loading.value = true
  errorMessage.value = ''
  try {
    await loadRoots()
    if (seq !== requestSeq.value || !props.open) return
    const start =
      props.initialPath.trim() ||
      roots.value[0]?.path ||
      props.contextRoots.find((item) => item.trim() !== '') ||
      ''
    if (start) {
      selectedPath.value = start.trim()
      selectedType.value = props.allowMissing ? 'missing' : ''
      await openPath(start)
    } else {
      currentPath.value = ''
      entries.value = []
      errorMessage.value = '暂无可浏览根目录。请先配置数据目录、运行时目录或全局文件允许路径。'
    }
  } catch (err) {
    if (seq !== requestSeq.value || !props.open) return
    errorMessage.value =
      err instanceof ApiError ? err.message : err instanceof Error ? err.message : '加载根目录失败'
  } finally {
    if (seq === requestSeq.value) loading.value = false
  }
}

function selectEntry(entry: BrowseEntry): void {
  if (entry.type === 'dir' && entry.enterable) {
    void openPath(entry.path)
    return
  }
  if (props.mode !== 'directory') {
    selectedPath.value = entry.path
    selectedType.value = entry.type === 'file' ? 'file' : 'dir'
  }
}

function confirmSelection(): void {
  const path = selectedPath.value.trim()
  if (!path || !canConfirm.value) return
  rememberPath(path, platform.value || 'default')
  emit('select', path)
  emit('close')
}

function useCurrentAsSelected(): void {
  if (currentPath.value) {
    selectedPath.value = currentPath.value
    selectedType.value = 'dir'
  }
}

function confirmCurrentDirectory(): void {
  useCurrentAsSelected()
  confirmSelection()
}

function goUp(): void {
  if (parentPath.value) void openPath(parentPath.value)
}

function closeModal(): void {
  emit('close')
}

function onDialogKeydown(event: KeyboardEvent): void {
  if (event.key === 'Escape') {
    event.preventDefault()
    closeModal()
    return
  }
  if (event.key !== 'Tab' || !dialogRef.value) return
  const focusable = Array.from(
    dialogRef.value.querySelectorAll<HTMLElement>(
      'button:not([disabled]), input:not([disabled]), [href], [tabindex]:not([tabindex="-1"])',
    ),
  ).filter((item) => item.offsetParent !== null)
  if (focusable.length === 0) return
  const first = focusable[0]
  const last = focusable[focusable.length - 1]
  if (event.shiftKey && document.activeElement === first) {
    event.preventDefault()
    last?.focus()
  } else if (!event.shiftKey && document.activeElement === last) {
    event.preventDefault()
    first?.focus()
  }
}

function onCrumbClick(index: number): void {
  const parts = crumbs.value
  if (parts.length === 0) return
  // 始终从当前路径前缀切片，避免点到「/」或盘符根时跳出允许浏览范围。
  const full = currentPath.value
  if (!full) return
  const slashy = displayPath(full, '/')
  if (slashy.startsWith('/')) {
    const segs = slashy.replace(/^\/+/, '').split('/').filter(Boolean)
    // crumbs: ['/', 'data', 'ws'] → index 0 对应第一个可浏览前缀（通常是允许根），不跳到 /
    if (index <= 0) {
      const rootGuess = roots.value.find(
        (r) =>
          slashy === displayPath(r.path, '/') ||
          slashy.startsWith(displayPath(r.path, '/').replace(/\/$/, '') + '/'),
      )
      if (rootGuess) void openPath(rootGuess.path)
      return
    }
    const next = '/' + segs.slice(0, index).join('/')
    void openPath(next)
    return
  }
  // Windows: crumbs like ['C:\', 'data', 'ws']
  if (/^[A-Za-z]:/.test(parts[0] ?? '')) {
    const segs = slashy
      .replace(/^[A-Za-z]:[\\/]?/, '')
      .split(/[\\/]+/)
      .filter(Boolean)
    const drive = (parts[0] ?? '').replace(/[\\/]+$/, '')
    if (index <= 0) {
      const rootGuess = roots.value.find((r) => {
        const rp = displayPath(r.path, '/').toLowerCase()
        return slashy.toLowerCase().startsWith(rp.replace(/\/$/, ''))
      })
      if (rootGuess) void openPath(rootGuess.path)
      else void openPath(drive + (pathSeparator.value || '\\'))
      return
    }
    void openPath([drive, ...segs.slice(0, index)].join(pathSeparator.value || '\\'))
    return
  }
  void openPath(parts.slice(0, index + 1).join(pathSeparator.value || '/'))
}

watch(
  () => props.open,
  (open) => {
    if (open) {
      triggerElement = document.activeElement instanceof HTMLElement ? document.activeElement : null
      previousBodyOverflow = document.body.style.overflow
      document.body.style.overflow = 'hidden'
      filterText.value = ''
      selectedPath.value = props.initialPath.trim()
      selectedType.value = props.initialPath.trim() && props.allowMissing ? 'missing' : ''
      void bootstrap()
      void nextTick(() => dialogRef.value?.querySelector<HTMLElement>('button, input')?.focus())
    } else {
      cancelInFlight()
      requestSeq.value += 1
      document.body.style.overflow = previousBodyOverflow
      triggerElement?.focus()
      triggerElement = null
    }
  },
)

onUnmounted(() => {
  cancelInFlight()
  document.body.style.overflow = previousBodyOverflow
})
</script>

<template>
  <teleport to="body">
    <transition name="path-fade">
      <div
        v-if="open"
        class="fixed inset-0 z-[100020] flex items-end justify-center bg-gray-950/45 p-0 backdrop-blur-[2px] sm:items-center sm:p-4"
      >
        <transition name="path-pop" appear>
          <div
            ref="dialogRef"
            class="flex max-h-[92vh] w-full max-w-4xl flex-col overflow-hidden rounded-t-3xl border border-gray-200 bg-white shadow-2xl sm:max-h-[min(820px,90vh)] sm:rounded-3xl dark:border-gray-800 dark:bg-gray-900"
            role="dialog"
            aria-modal="true"
            :aria-label="title"
            @keydown="onDialogKeydown"
          >
            <div
              class="flex items-start justify-between gap-3 border-b border-gray-100 px-4 py-4 sm:px-5 dark:border-gray-800"
            >
              <div class="min-w-0">
                <h3 class="text-base font-semibold text-gray-900 dark:text-white/90">
                  {{ title }}
                </h3>
                <p class="mt-1 text-xs leading-5 text-gray-500 dark:text-gray-400">
                  路径位于{{
                    hostLabel
                  }}，不是浏览器本机。可浏览范围受数据目录、运行时目录与全局文件允许路径约束。
                </p>
              </div>
              <button
                type="button"
                class="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-xl text-gray-400 transition hover:bg-gray-100 hover:text-gray-700 dark:hover:bg-white/5 dark:hover:text-gray-200"
                aria-label="关闭"
                @click="closeModal"
              >
                <svg class="h-4 w-4" viewBox="0 0 16 16" fill="none" aria-hidden="true">
                  <path
                    d="M4 4l8 8M12 4l-8 8"
                    stroke="currentColor"
                    stroke-width="1.8"
                    stroke-linecap="round"
                  />
                </svg>
              </button>
            </div>

            <div class="grid min-h-0 flex-1 grid-cols-1 lg:grid-cols-[220px_minmax(0,1fr)]">
              <aside
                class="border-b border-gray-100 p-3 lg:border-r lg:border-b-0 dark:border-gray-800"
              >
                <p class="px-2 text-[11px] font-medium tracking-wide text-gray-400 uppercase">
                  快捷入口
                </p>
                <div
                  class="mt-2 flex gap-2 overflow-x-auto pb-1 lg:block lg:space-y-1 lg:overflow-visible"
                >
                  <button
                    v-for="root in roots"
                    :key="root.id + root.path"
                    type="button"
                    class="min-w-[9.5rem] shrink-0 rounded-xl px-3 py-2 text-left transition lg:w-full lg:min-w-0"
                    :class="
                      currentPath === root.path || selectedPath === root.path
                        ? 'bg-brand-50 ring-brand-200 dark:bg-brand-500/10 dark:ring-brand-500/30 ring-1'
                        : 'hover:bg-gray-50 dark:hover:bg-white/5'
                    "
                    @click="openPath(root.path)"
                  >
                    <span
                      class="inline-flex rounded-full px-2 py-0.5 text-[10px] font-medium"
                      :class="browseRootTone(root.kind)"
                    >
                      {{ root.label }}
                    </span>
                    <span
                      class="mt-1 block truncate font-mono text-[11px] text-gray-600 dark:text-gray-300"
                    >
                      {{ displayPath(root.path, pathSeparator) }}
                    </span>
                  </button>
                </div>

                <div v-if="recent.length > 0" class="mt-4">
                  <p class="px-2 text-[11px] font-medium tracking-wide text-gray-400 uppercase">
                    最近使用
                  </p>
                  <div class="mt-2 space-y-1">
                    <button
                      v-for="item in recent"
                      :key="item"
                      type="button"
                      class="w-full truncate rounded-lg px-3 py-1.5 text-left font-mono text-[11px] text-gray-600 transition hover:bg-gray-50 dark:text-gray-300 dark:hover:bg-white/5"
                      @click="openPath(item)"
                    >
                      {{ displayPath(item, pathSeparator) }}
                    </button>
                  </div>
                </div>
              </aside>

              <section class="flex min-h-0 flex-col">
                <div class="space-y-2 border-b border-gray-100 px-4 py-3 dark:border-gray-800">
                  <div class="flex flex-wrap items-center gap-2">
                    <button
                      type="button"
                      class="rounded-lg border border-gray-200 px-2.5 py-1.5 text-xs font-medium text-gray-600 transition hover:bg-gray-50 disabled:opacity-40 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-white/5"
                      :disabled="!parentPath || loading"
                      @click="goUp"
                    >
                      上级
                    </button>
                    <div
                      class="flex min-w-0 flex-1 flex-wrap items-center gap-1 text-xs text-gray-500 dark:text-gray-400"
                    >
                      <button
                        v-for="(part, idx) in crumbs"
                        :key="`${part}-${idx}`"
                        type="button"
                        class="max-w-[9rem] truncate rounded-md px-1.5 py-0.5 transition hover:bg-gray-100 hover:text-gray-800 dark:hover:bg-white/5 dark:hover:text-gray-200"
                        @click="onCrumbClick(idx)"
                      >
                        {{ part }}
                        <span v-if="idx < crumbs.length - 1" class="text-gray-300">/</span>
                      </button>
                    </div>
                  </div>
                  <div class="flex flex-col gap-2 sm:flex-row">
                    <input
                      v-model="selectedPath"
                      type="text"
                      @input="selectedType = allowMissing ? 'missing' : ''"
                      class="focus:border-brand-300 focus:ring-brand-500/10 h-10 w-full rounded-xl border border-gray-200 bg-gray-50 px-3 font-mono text-xs text-gray-800 outline-none focus:bg-white focus:ring-3 dark:border-gray-700 dark:bg-white/[0.03] dark:text-white/90"
                      placeholder="可直接粘贴绝对路径"
                      @keydown.enter.prevent="openPath(selectedPath)"
                    />
                    <input
                      v-model="filterText"
                      type="search"
                      class="focus:border-brand-300 focus:ring-brand-500/10 h-10 w-full rounded-xl border border-gray-200 bg-white px-3 text-xs text-gray-700 outline-none focus:ring-3 sm:max-w-[180px] dark:border-gray-700 dark:bg-gray-900 dark:text-gray-200"
                      placeholder="过滤当前目录"
                    />
                  </div>
                </div>

                <div class="min-h-0 flex-1 overflow-y-auto px-2 py-2 sm:px-3">
                  <div v-if="loading" class="space-y-2 p-2">
                    <div
                      v-for="n in 6"
                      :key="n"
                      class="h-11 animate-pulse rounded-xl bg-gray-100 dark:bg-white/5"
                    ></div>
                  </div>
                  <div
                    v-else-if="errorMessage"
                    class="border-error-200 bg-error-50 text-error-600 dark:border-error-500/30 dark:bg-error-500/10 dark:text-error-300 m-2 rounded-xl border px-3 py-3 text-sm"
                  >
                    {{ errorMessage }}
                  </div>
                  <div
                    v-else-if="filteredEntries.length === 0"
                    class="flex h-40 flex-col items-center justify-center gap-2 text-sm text-gray-400"
                  >
                    <FolderIcon class="h-8 w-8 opacity-50" aria-hidden="true" />
                    当前目录为空
                  </div>
                  <ul v-else class="space-y-1">
                    <li v-for="entry in filteredEntries" :key="entry.path">
                      <button
                        type="button"
                        class="group flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left transition"
                        :class="
                          selectedPath === entry.path
                            ? 'bg-brand-50 ring-brand-200 dark:bg-brand-500/10 dark:ring-brand-500/30 ring-1'
                            : 'hover:bg-gray-50 dark:hover:bg-white/5'
                        "
                        @click="selectEntry(entry)"
                        @dblclick="
                          entry.type === 'dir' && entry.enterable ? openPath(entry.path) : undefined
                        "
                      >
                        <span
                          class="inline-flex h-9 w-9 shrink-0 items-center justify-center rounded-xl"
                          :class="
                            entry.type === 'dir'
                              ? 'bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-300'
                              : 'bg-gray-100 text-gray-500 dark:bg-white/5 dark:text-gray-400'
                          "
                        >
                          <FolderIcon
                            v-if="entry.type === 'dir'"
                            class="h-4 w-4"
                            aria-hidden="true"
                          />
                          <svg
                            v-else
                            class="h-4 w-4"
                            viewBox="0 0 16 16"
                            fill="none"
                            aria-hidden="true"
                          >
                            <path
                              d="M4 2.5h5l3 3V13.5H4v-11Z"
                              stroke="currentColor"
                              stroke-width="1.4"
                              stroke-linejoin="round"
                            />
                          </svg>
                        </span>
                        <span class="min-w-0 flex-1">
                          <span
                            class="block truncate text-sm font-medium text-gray-800 dark:text-white/90"
                          >
                            {{ entry.name }}
                          </span>
                          <span class="mt-0.5 block truncate font-mono text-[11px] text-gray-400">
                            {{ displayPath(entry.path, pathSeparator) }}
                          </span>
                        </span>
                        <span v-if="entry.type === 'dir'" class="text-[11px] text-gray-400"
                          >进入</span
                        >
                      </button>
                    </li>
                  </ul>
                  <p
                    v-if="truncated"
                    class="text-warning-600 dark:text-warning-400 px-3 py-2 text-[11px]"
                  >
                    仅显示前若干项，请用过滤缩小范围或进入子目录。
                  </p>
                </div>
              </section>
            </div>

            <div
              class="flex flex-col gap-3 border-t border-gray-100 px-4 py-3 sm:flex-row sm:items-center sm:justify-between sm:px-5 dark:border-gray-800"
            >
              <div class="min-w-0">
                <p class="text-[11px] text-gray-400">当前选择</p>
                <p class="truncate font-mono text-xs text-gray-700 dark:text-gray-200">
                  {{ selectedPath ? displayPath(selectedPath, pathSeparator) : '未选择' }}
                </p>
              </div>
              <div class="flex flex-wrap justify-end gap-2">
                <button
                  type="button"
                  class="rounded-xl border border-gray-200 px-3.5 py-2 text-sm font-medium text-gray-600 transition hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-white/5"
                  @click="closeModal"
                >
                  取消
                </button>
                <button
                  v-if="mode !== 'file'"
                  type="button"
                  class="rounded-xl border border-gray-200 px-3.5 py-2 text-sm font-medium text-gray-700 transition hover:bg-gray-50 dark:border-gray-700 dark:text-gray-200 dark:hover:bg-white/5"
                  :disabled="!currentPath"
                  @click="confirmCurrentDirectory"
                >
                  使用当前目录
                </button>
                <button
                  type="button"
                  class="bg-brand-500 hover:bg-brand-600 rounded-xl px-4 py-2 text-sm font-medium text-white transition disabled:opacity-50"
                  :disabled="!canConfirm"
                  @click="confirmSelection"
                >
                  选择此路径
                </button>
              </div>
            </div>
          </div>
        </transition>
      </div>
    </transition>
  </teleport>
</template>

<style scoped>
.path-fade-enter-active,
.path-fade-leave-active {
  transition: opacity 0.18s ease;
}
.path-fade-enter-from,
.path-fade-leave-to {
  opacity: 0;
}
.path-pop-enter-active {
  transition:
    opacity 0.2s ease,
    transform 0.2s ease;
}
.path-pop-leave-active {
  transition:
    opacity 0.15s ease,
    transform 0.15s ease;
}
.path-pop-enter-from,
.path-pop-leave-to {
  opacity: 0;
  transform: translateY(10px) scale(0.98);
}
@media (prefers-reduced-motion: reduce) {
  .path-fade-enter-active,
  .path-fade-leave-active,
  .path-pop-enter-active,
  .path-pop-leave-active {
    transition: none;
  }
}
</style>
