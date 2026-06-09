<script setup lang="ts">
/**
 * 别名/描述重写规则区块（任务 26.2，Req 8.1）。
 *
 * 提供独立别名规则的列表、新建/编辑（模态框）、删除（确认）、上移/下移排序。
 * 别名规则不支持独立启停；排序通过交换相邻规则 sortOrder 后经 PUT 持久化实现。
 * 风格：Tailwind 工具类 + TailAdmin（卡片 rounded-2xl border、徽章、按钮、模态框）。
 */
import { computed, onMounted, ref } from 'vue'
import {
  listAliases,
  createAlias,
  updateAlias,
  deleteAlias,
  type AliasRule,
  type AliasRuleRequest,
} from '@/api/rules'
import type { Upstream } from '@/api/upstreams'

const props = defineProps<{
  upstreams: Upstream[]
}>()

const emit = defineEmits<{
  (e: 'toast', message: string): void
}>()

/** 别名规则列表（按 sortOrder 升序）。 */
const rules = ref<AliasRule[]>([])
const loading = ref(false)
const errorMessage = ref('')

/** 模态框开关与编辑目标。 */
const modalOpen = ref(false)
const editing = ref<AliasRule | null>(null)
/** 删除确认目标。 */
const deleting = ref<AliasRule | null>(null)
/** 保存中标记。 */
const saving = ref(false)
/** 表单字段级校验错误。 */
const formError = ref('')

/** 表单模型。 */
const form = ref<AliasRuleRequest>({
  scopeType: 'all',
  upstreamIds: [],
  pattern: '',
  isRegex: false,
  targetName: '',
  targetDesc: '',
  sortOrder: 0,
})

const isEdit = computed(() => editing.value !== null)

/** 加载别名规则（按 sortOrder 升序）。 */
async function load(): Promise<void> {
  loading.value = true
  errorMessage.value = ''
  try {
    const list = await listAliases()
    list.sort((a, b) => a.sortOrder - b.sortOrder)
    rules.value = list
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : '加载别名规则失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)

/** 打开新建模态框（sortOrder 追加到末尾）。 */
function openCreate(): void {
  editing.value = null
  formError.value = ''
  form.value = {
    scopeType: 'all',
    upstreamIds: [],
    pattern: '',
    isRegex: false,
    targetName: '',
    targetDesc: '',
    sortOrder: nextSortOrder(),
  }
  modalOpen.value = true
}

/** 打开编辑模态框。 */
function openEdit(rule: AliasRule): void {
  editing.value = rule
  formError.value = ''
  form.value = {
    scopeType: rule.scopeType ?? 'all',
    upstreamIds: rule.upstreamIds ?? [],
    pattern: rule.pattern,
    isRegex: rule.isRegex,
    targetName: rule.targetName ?? '',
    targetDesc: rule.targetDesc ?? '',
    sortOrder: rule.sortOrder,
  }
  modalOpen.value = true
}

/** 关闭模态框。 */
function closeModal(): void {
  modalOpen.value = false
  editing.value = null
}

/** 前端基础校验：模式必填、目标名称/描述至少其一。 */
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
  if (f.targetName.trim() === '' && f.targetDesc.trim() === '') {
    formError.value = '目标名称与目标描述至少填写其一'
    return false
  }
  if (f.targetName.length > 100) {
    formError.value = '目标名称长度不能超过 100 个字符'
    return false
  }
  if (f.targetDesc.length > 1024) {
    formError.value = '目标描述长度不能超过 1024 个字符'
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
      await updateAlias(editing.value.id, payload)
      emit('toast', '别名规则已更新')
    } else {
      await createAlias(payload)
      emit('toast', '别名规则已创建')
    }
    closeModal()
    await load()
  } catch (err) {
    formError.value = err instanceof Error ? err.message : '保存失败'
  } finally {
    saving.value = false
  }
}

/** 请求删除确认。 */
function askDelete(rule: AliasRule): void {
  deleting.value = rule
}

/** 确认删除。 */
async function confirmDelete(): Promise<void> {
  if (deleting.value === null) return
  const rule = deleting.value
  try {
    await deleteAlias(rule.id)
    emit('toast', '别名规则已删除')
    deleting.value = null
    await load()
  } catch (err) {
    emit('toast', err instanceof Error ? err.message : '删除失败')
  }
}

/**
 * 上移/下移：交换相邻两条规则的 sortOrder 后分别 PUT 持久化（Req 8.1）。
 * 乐观更新本地列表，失败时回滚重载。
 */
async function move(rule: AliasRule, direction: -1 | 1): Promise<void> {
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
      updateAlias(a.id, toRequest(a, bOrder)),
      updateAlias(b.id, toRequest(b, aOrder)),
    ])
    await load()
  } catch (err) {
    emit('toast', err instanceof Error ? err.message : '排序失败')
    await load()
  }
}

/** 将规则连同新的 sortOrder 转为更新请求体。 */
function toRequest(rule: AliasRule, sortOrder: number): AliasRuleRequest {
  return toPayload({
    pattern: rule.pattern,
    isRegex: rule.isRegex,
    targetName: rule.targetName ?? '',
    targetDesc: rule.targetDesc ?? '',
    sortOrder,
    scopeType: rule.scopeType ?? 'all',
    upstreamIds: rule.upstreamIds ?? [],
  })
}

function toPayload(value: AliasRuleRequest): AliasRuleRequest {
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

function scopeLabel(rule: AliasRule): string {
  if ((rule.scopeType ?? 'all') === 'all') return '全部上游'
  const ids = rule.upstreamIds ?? []
  if (ids.length === 0) return '未选择上游'
  const names = ids.map(upstreamName)
  if (names.length <= 3) return names.join('、')
  return `${names.slice(0, 3).join('、')} 等 ${names.length} 个`
}

defineExpose({ reload: load })
</script>

<template>
  <section
    class="flex flex-col rounded-2xl border border-gray-200 bg-white dark:border-gray-800 dark:bg-white/[0.03]"
  >
    <!-- 区块头部 -->
    <div class="flex items-center justify-between border-b border-gray-200 px-5 py-4 dark:border-gray-800">
      <div>
        <h3 class="text-base font-semibold text-gray-800 dark:text-white/90">别名 / 描述重写</h3>
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
        新建别名规则
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
      暂无别名规则，点击「新建别名规则」开始添加
    </div>
    <div v-else class="grid grid-cols-1 gap-3 md:grid-cols-2 2xl:grid-cols-3">
      <div
        v-for="(rule, index) in rules"
        :key="rule.id"
        class="flex flex-col rounded-xl border border-gray-200 bg-white p-4 dark:border-gray-800 dark:bg-white/[0.03]"
      >
        <div class="mb-2 flex items-start justify-between gap-2">
          <code class="min-w-0 flex-1 truncate rounded bg-gray-100 px-1.5 py-0.5 text-xs text-gray-800 dark:bg-gray-800 dark:text-gray-200" :title="rule.pattern">{{ rule.pattern }}</code>
          <span
            class="shrink-0 inline-flex items-center rounded-full px-2 py-0.5 text-xs"
            :class="rule.isRegex
              ? 'bg-brand-50 text-brand-600 dark:bg-brand-500/10 dark:text-brand-400'
              : 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'"
          >
            {{ rule.isRegex ? '正则' : '精确' }}
          </span>
        </div>
        <div class="mb-3">
          <div v-if="rule.targetName" class="text-sm font-medium text-gray-800 dark:text-white/90">→ {{ rule.targetName }}</div>
          <div v-if="rule.targetDesc" class="mt-0.5 truncate text-xs text-gray-500 dark:text-gray-400" :title="rule.targetDesc">
            {{ rule.targetDesc }}
          </div>
          <div class="mt-2 rounded-lg bg-gray-50 px-2.5 py-2 text-xs text-gray-500 dark:bg-gray-800/60 dark:text-gray-400">
            作用范围：{{ scopeLabel(rule) }}
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
        @click.self="closeModal"
      >
        <div class="max-h-[calc(100vh-2rem)] w-full max-w-lg overflow-y-auto rounded-2xl border border-gray-200 bg-white p-6 shadow-xl dark:border-gray-800 dark:bg-gray-900">
          <h3 class="mb-4 text-base font-semibold text-gray-800 dark:text-white/90">
            {{ isEdit ? '编辑别名规则' : '新建别名规则' }}
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
            <label class="flex items-center gap-2 text-sm text-gray-700 dark:text-gray-300">
              <input v-model="form.isRegex" type="checkbox" class="h-4 w-4 rounded border-gray-300 text-brand-500 focus:ring-brand-400" />
              使用正则匹配（完整匹配）
            </label>
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
            <div>
              <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">目标名称</label>
              <input
                v-model="form.targetName"
                type="text"
                placeholder="重写后的工具名称（与目标描述至少填一项）"
                class="w-full rounded-lg border border-gray-300 px-3.5 py-2.5 text-sm text-gray-800 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
              />
            </div>
            <div>
              <label class="mb-1.5 block text-sm font-medium text-gray-700 dark:text-gray-300">目标描述</label>
              <textarea
                v-model="form.targetDesc"
                rows="3"
                placeholder="重写后的工具描述（与目标名称至少填一项）"
                class="w-full rounded-lg border border-gray-300 px-3.5 py-2.5 text-sm text-gray-800 focus:border-brand-400 focus:ring-2 focus:ring-brand-100 focus:outline-none dark:border-gray-700 dark:bg-gray-800 dark:text-white/90"
              ></textarea>
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

    <!-- 删除确认 -->
    <transition name="fade">
      <div
        v-if="deleting !== null"
        class="fixed inset-0 z-[100001] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
        @click.self="deleting = null"
      >
        <div class="w-full max-w-sm rounded-2xl border border-gray-200 bg-white p-6 shadow-xl dark:border-gray-800 dark:bg-gray-900">
          <h3 class="mb-2 text-base font-semibold text-gray-800 dark:text-white/90">确认删除</h3>
          <p class="mb-5 text-sm text-gray-500 dark:text-gray-400">
            确定删除别名规则「{{ deleting.pattern }}」吗？该操作不可恢复。
          </p>
          <div class="flex justify-end gap-3">
            <button
              type="button"
              class="rounded-lg border border-gray-300 px-4 py-2 text-sm font-medium text-gray-700 hover:bg-gray-50 dark:border-gray-700 dark:text-gray-300 dark:hover:bg-gray-800"
              @click="deleting = null"
            >
              取消
            </button>
            <button
              type="button"
              class="rounded-lg bg-error-500 px-4 py-2 text-sm font-medium text-white transition hover:bg-error-600"
              @click="confirmDelete"
            >
              删除
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
