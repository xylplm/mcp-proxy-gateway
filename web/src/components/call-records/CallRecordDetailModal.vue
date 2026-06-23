<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import type { CallRecord } from '@/api/stats'

const props = defineProps<{
  open: boolean
  record: CallRecord | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
}>()

// 详情完整记录：props.record 为列表项摘要，可能不含入参/出参；模态框打开时按需补拉完整数据。
const detail = ref<CallRecord | null>(null)

const failureSummaryItems = computed(() => {
  if (detail.value === null || statusOf(detail.value) === 'success') return []
  const item = detail.value
  const d = item.FailureDetail
  if (d === null || d === undefined) return []
  const items: Array<{ label: string; value: string }> = []
  if (d.code !== undefined && d.code !== '') {
    items.push({ label: '错误码', value: String(d.code) })
  }
  if (d.httpStatus !== undefined && d.httpStatus > 0) {
    items.push({ label: 'HTTP 状态', value: String(d.httpStatus) })
  }
  if (d.businessCode !== undefined && d.businessCode > 0) {
    items.push({ label: '业务码', value: String(d.businessCode) })
  }
  if (d.timeout === true) {
    items.push({ label: '超时', value: '是' })
  }
  return items
})

const diagnosticInsight = computed(() => {
  if (detail.value === null || statusOf(detail.value) === 'success') return null
  return buildDiagnosticInsight(detail.value)
})

function formatDateTime(value: string): string {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '-'
  return date.toLocaleString('zh-CN', { hour12: false })
}

function formatLatency(value: number): string {
  return `${Math.max(0, Math.round(value)).toLocaleString('zh-CN')} ms`
}

function statusOf(item: CallRecord): string {
  if (item.Status !== undefined && item.Status !== '') return item.Status
  return item.Success ? 'success' : 'failed'
}

function statusLabel(item: CallRecord): string {
  switch (statusOf(item)) {
    case 'success':
      return '成功'
    case 'upstream_error':
      return '上游错误'
    case 'failed':
      return '调用失败'
    default:
      return '未知状态'
  }
}

function statusClass(item: CallRecord): string {
  switch (statusOf(item)) {
    case 'success':
      return 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
    case 'upstream_error':
      return 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
    case 'failed':
      return 'bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400'
    default:
      return 'bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-300'
  }
}

function responseTitle(item: CallRecord): string {
  return statusOf(item) === 'success' ? '出参' : '失败响应'
}

function buildDiagnosticInsight(item: CallRecord): { title: string; description: string; actions: string[] } {
  const d = item.FailureDetail
  const status = statusOf(item)
  const code = (d?.code ?? '').toLowerCase()
  const message = d?.message || item.ErrorMessage || ''
  const httpStatus = d?.httpStatus ?? 0
  const businessCode = d?.businessCode ?? 0

  if (d?.timeout === true || code.includes('timeout') || message.toLowerCase().includes('timeout')) {
    return {
      title: '上游响应超时',
      description: '请求已经到达网关，但上游没有在超时时间内返回结果。',
      actions: ['检查上游服务是否可用', '查看上游网络与鉴权配置', '确认超时设置是否匹配该工具耗时'],
    }
  }
  if (httpStatus === 401 || httpStatus === 403 || code.includes('unauthorized') || code.includes('forbidden')) {
    return {
      title: '上游鉴权失败',
      description: '上游返回了鉴权或权限相关错误，通常与凭证、请求头或上游账号权限有关。',
      actions: ['检查上游 MCP 凭证是否过期', '确认模板参数或自定义请求头是否正确', '用上游管理页的连接测试重新验证配置'],
    }
  }
  if (httpStatus === 404 || code.includes('not_found')) {
    return {
      title: '工具或上游资源不存在',
      description: '上游返回不存在，可能是工具缓存已过期、工具名变化，或目标资源已删除。',
      actions: ['刷新该上游工具列表', '确认规则别名后的工具名仍然存在', '检查调用入参里的目标资源标识'],
    }
  }
  if (httpStatus === 429 || code.includes('rate')) {
    return {
      title: '请求被限流',
      description: '调用频率超过上游或网关配置的限制。',
      actions: ['检查 API Key 或上游限流配置', '降低客户端并发或重试频率', '必要时提高对应限额'],
    }
  }
  if (httpStatus >= 500 || status === 'upstream_error') {
    return {
      title: '上游服务异常',
      description: '网关已完成路由，但上游返回异常或连接失败。',
      actions: ['查看上游最近错误和服务日志', '尝试重连或刷新工具列表', '确认上游运行环境和依赖服务正常'],
    }
  }
  if (businessCode > 0 || code !== '') {
    return {
      title: '业务校验未通过',
      description: '调用到达目标工具，但工具返回了业务错误码或校验错误。',
      actions: ['检查入参字段和值是否符合工具 Schema', '查看失败响应里的字段级错误', '必要时在工具目录确认该工具的入参定义'],
    }
  }
  return {
    title: '调用未成功',
    description: '当前记录没有足够明确的错误分类，可结合入参、失败响应和系统日志继续排查。',
    actions: ['确认调用来源和目标工具是否正确', '检查网关系统日志中的同时间错误', '必要时重新发起一次调用复现问题'],
  }
}

// 耗时分级：<1s 正常、1-3s 较慢、>3s 很慢。
function latencyLevel(value: number): 'normal' | 'warn' | 'danger' {
  const v = Math.max(0, value)
  if (v >= 3000) return 'danger'
  if (v >= 1000) return 'warn'
  return 'normal'
}

function latencyClass(value: number): string {
  switch (latencyLevel(value)) {
    case 'danger':
      return 'bg-error-50 text-error-600 dark:bg-error-500/10 dark:text-error-400'
    case 'warn':
      return 'bg-warning-50 text-warning-700 dark:bg-warning-500/10 dark:text-warning-400'
    default:
      return 'bg-success-50 text-success-700 dark:bg-success-500/10 dark:text-success-400'
  }
}

function modeLabel(item: CallRecord): string {
  return item.Mode === 'smart' ? '智能模式' : '全量模式'
}

function mcpLabel(item: CallRecord): string {
  return item.UpstreamName || item.UpstreamID || '未知 MCP'
}

function toolLabel(item: CallRecord): string {
  return item.ExposedName || item.OriginalName || '未知工具'
}

// 来源标识：xiaozhi 显示「小智接入」；否则回退到 API Key 名（不再显示「未知 API Key」）。
function isXiaozhi(item: CallRecord): boolean {
  return item.Source === 'xiaozhi'
}

function sourceLabel(item: CallRecord): string {
  if (isXiaozhi(item)) return '小智接入'
  return item.APIKeyName || item.APIKeyID || 'API 调用'
}

function prettify(value: unknown): string {
  if (value === null || value === undefined) return 'null'
  if (typeof value === 'string') {
    try {
      return JSON.stringify(JSON.parse(value), null, 2)
    } catch {
      return value
    }
  }
  try {
    return JSON.stringify(value, null, 2)
  } catch {
    return String(value)
  }
}

function handleKeydown(e: KeyboardEvent): void {
  if (e.key === 'Escape' && props.open) {
    emit('close')
  }
}

watch(
  () => props.record,
  (next) => {
    // 列表项摘要已含完整字段（后端列表与详情查询返回相同结构），直接复用，无需补拉。
    detail.value = next
  },
  { immediate: true },
)

onMounted(() => {
  window.addEventListener('keydown', handleKeydown)
})

onUnmounted(() => {
  window.removeEventListener('keydown', handleKeydown)
})
</script>

<template>
  <transition name="fade">
    <div
      v-if="open && detail !== null"
      class="fixed inset-0 z-[100000] flex items-center justify-center bg-gray-900/40 p-4 backdrop-blur-[1px]"
      @click.self="emit('close')"
    >
      <div
        class="flex max-h-[88vh] w-full max-w-5xl flex-col overflow-hidden rounded-2xl border border-gray-200 bg-white shadow-xl dark:border-gray-800 dark:bg-gray-900"
      >
        <!-- 头部 -->
        <div
          class="flex items-start justify-between gap-4 border-b border-gray-200 px-6 py-4 dark:border-gray-800"
        >
          <div class="min-w-0">
            <div class="flex flex-wrap items-center gap-2">
              <h3
                v-tooltip="{
                  content: detail.Description || '',
                  placement: 'bottom-start',
                  disabled: !detail.Description,
                  wrap: true,
                }"
                class="cursor-help truncate text-lg font-semibold text-gray-800 dark:text-white/90"
              >
                {{ toolLabel(detail) }}
              </h3>
              <span class="shrink-0 rounded-full px-2.5 py-1 text-xs font-medium" :class="statusClass(detail)">
                {{ statusLabel(detail) }}
              </span>
              <span
                class="shrink-0 rounded-full bg-gray-100 px-2 py-1 text-xs font-medium text-gray-600 dark:bg-white/5 dark:text-gray-400"
              >
                {{ modeLabel(detail) }}
              </span>
              <span
                v-if="isXiaozhi(detail)"
                class="shrink-0 rounded-full bg-brand-50 px-2.5 py-1 text-xs font-medium text-brand-600 dark:bg-brand-500/10 dark:text-brand-400"
              >
                小智接入
              </span>
            </div>
            <p class="mt-2 text-xs text-gray-500 dark:text-gray-400">记录 ID：{{ detail.ID }}</p>
          </div>
          <button
            type="button"
            class="shrink-0 rounded-lg p-2 text-gray-400 hover:bg-gray-100 hover:text-gray-600 dark:hover:bg-gray-800"
            aria-label="关闭"
            @click="emit('close')"
          >
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" aria-hidden="true">
              <path d="M6 6l12 12M6 18L18 6" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" />
            </svg>
          </button>
        </div>

        <!-- 主体 -->
        <div class="custom-scrollbar min-h-0 flex-1 overflow-y-auto px-6 py-5">
          <!-- 工具描述 -->
          <p
            v-if="detail.Description"
            class="mb-5 rounded-xl bg-gray-50 px-4 py-3 text-sm leading-6 text-gray-600 dark:bg-gray-800/60 dark:text-gray-300"
          >
            {{ detail.Description }}
          </p>

          <!-- 信息网格 -->
          <div class="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
            <div class="rounded-xl bg-gray-50 p-4 dark:bg-gray-800/60">
              <p class="text-xs text-gray-400 dark:text-gray-500">上游 MCP</p>
              <p class="mt-1 truncate text-sm font-medium text-gray-800 dark:text-white/90">
                {{ mcpLabel(detail) }}
              </p>
            </div>
            <div class="rounded-xl bg-gray-50 p-4 dark:bg-gray-800/60">
              <p class="text-xs text-gray-400 dark:text-gray-500">调用来源</p>
              <p class="mt-1 truncate text-sm font-medium text-gray-800 dark:text-white/90">
                {{ sourceLabel(detail) }}
              </p>
            </div>
            <div class="rounded-xl bg-gray-50 p-4 dark:bg-gray-800/60">
              <p class="text-xs text-gray-400 dark:text-gray-500">调用时间</p>
              <p class="mt-1 text-sm font-medium text-gray-800 dark:text-white/90">
                {{ formatDateTime(detail.CalledAt) }}
              </p>
            </div>
            <div class="rounded-xl bg-gray-50 p-4 dark:bg-gray-800/60">
              <p class="text-xs text-gray-400 dark:text-gray-500">耗时</p>
              <p class="mt-1">
                <span
                  class="inline-flex rounded-full px-2 py-0.5 text-sm font-medium"
                  :class="latencyClass(detail.LatencyMS)"
                >
                  {{ formatLatency(detail.LatencyMS) }}
                </span>
              </p>
            </div>
          </div>

          <!-- 失败详情 -->
          <section
            v-if="statusOf(detail) !== 'success'"
            class="mt-5 rounded-xl border border-error-100 bg-error-50 p-4 dark:border-error-500/20 dark:bg-error-500/10"
          >
            <div class="flex flex-wrap items-start justify-between gap-3">
              <div>
                <h4 class="text-sm font-semibold text-error-700 dark:text-error-300">失败详情</h4>
                <p class="mt-1 text-sm text-error-600 dark:text-error-400">
                  {{ detail.FailureDetail?.message || detail.ErrorMessage || '上游调用未返回成功结果' }}
                </p>
              </div>
              <span
                v-if="detail.FailureDetail?.timeout === true"
                class="rounded-full bg-error-100 px-2.5 py-1 text-xs font-medium text-error-700 dark:bg-error-500/20 dark:text-error-300"
              >
                超时
              </span>
            </div>
            <div v-if="failureSummaryItems.length > 0" class="mt-4 grid grid-cols-1 gap-3 sm:grid-cols-2 xl:grid-cols-4">
              <div
                v-for="item in failureSummaryItems"
                :key="item.label"
                class="rounded-lg bg-white/70 px-3 py-2 dark:bg-gray-950/20"
              >
                <p class="text-xs text-error-400 dark:text-error-300/80">{{ item.label }}</p>
                <p class="mt-1 break-all text-sm font-medium text-error-700 dark:text-error-200">{{ item.value }}</p>
              </div>
            </div>
          </section>

          <section
            v-if="diagnosticInsight !== null"
            class="mt-5 rounded-xl border border-warning-200 bg-warning-50/80 p-4 dark:border-warning-500/30 dark:bg-warning-500/10"
          >
            <div class="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
              <div class="min-w-0">
                <h4 class="text-sm font-semibold text-warning-800 dark:text-warning-200">
                  {{ diagnosticInsight.title }}
                </h4>
                <p class="mt-1 text-sm leading-6 text-warning-700 dark:text-warning-300">
                  {{ diagnosticInsight.description }}
                </p>
              </div>
              <div class="grid min-w-0 gap-2 text-xs text-warning-700 dark:text-warning-300">
                <span
                  v-for="action in diagnosticInsight.actions"
                  :key="action"
                  class="rounded-lg bg-white/70 px-3 py-2 dark:bg-gray-950/20"
                >
                  {{ action }}
                </span>
              </div>
            </div>
          </section>

          <!-- 入参 / 出参 -->
          <section class="mt-5 grid grid-cols-1 gap-5 xl:grid-cols-2">
            <article class="min-w-0">
              <h4 class="mb-3 text-sm font-semibold text-gray-800 dark:text-white/90">入参</h4>
              <pre class="custom-scrollbar h-80 overflow-auto rounded-xl bg-gray-950 p-4 text-xs leading-5 text-gray-100 xl:h-[420px]"><code>{{ prettify(detail.RequestArgs) }}</code></pre>
            </article>
            <article class="min-w-0">
              <h4 class="mb-3 text-sm font-semibold text-gray-800 dark:text-white/90">{{ responseTitle(detail) }}</h4>
              <pre class="custom-scrollbar h-80 overflow-auto rounded-xl bg-gray-950 p-4 text-xs leading-5 text-gray-100 xl:h-[420px]"><code>{{ prettify(detail.ResponseResult) }}</code></pre>
            </article>
          </section>

          <!-- 诊断 JSON -->
          <section v-if="statusOf(detail) !== 'success'" class="mt-5 min-w-0">
            <h4 class="mb-3 text-sm font-semibold text-gray-800 dark:text-white/90">诊断 JSON</h4>
            <pre class="custom-scrollbar max-h-[320px] overflow-auto rounded-xl bg-gray-950 p-4 text-xs leading-5 text-gray-100"><code>{{ prettify(detail.FailureDetail) }}</code></pre>
          </section>
        </div>
      </div>
    </div>
  </transition>
</template>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.18s ease;
}

.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
